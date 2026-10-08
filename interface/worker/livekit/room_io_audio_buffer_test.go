package livekit

import (
	"encoding/binary"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func roomIOTestAudioPacket(seq uint16) *rtp.Packet {
	payload := make([]byte, 2)
	binary.BigEndian.PutUint16(payload, seq)
	return &rtp.Packet{
		Header:  rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq) * 960},
		Payload: payload,
	}
}

func TestRoomIOAudioBufferPackets(t *testing.T) {
	for _, tc := range []struct {
		name        string
		input, want []uint16
	}{
		{"packet gap regression", []uint16{100, 102, 141, 142}, []uint16{100, 102, 141, 142}},
		{"contiguous", []uint16{100, 101, 102}, []uint16{100, 101, 102}},
		{"reordered", []uint16{100, 102, 101, 103}, []uint16{100, 101, 102, 103}},
		{"wraparound", []uint16{65534, 0, 65535, 1}, []uint16{65534, 65535, 0, 1}},
		{"duplicates", []uint16{100, 102, 102, 101, 103}, []uint16{100, 101, 102, 103}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []uint16
			var atFinish []uint16
			b := newRoomIOAudioBuffer(func(payload []byte) {
				got = append(got, binary.BigEndian.Uint16(payload))
			}, func() { atFinish = append([]uint16(nil), got...) })
			t.Cleanup(func() { b.stop(false) })
			for _, seq := range tc.input {
				b.Push(roomIOTestAudioPacket(seq))
			}
			b.stop(true)
			if !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(atFinish, tc.want) {
				t.Fatalf("packets = %v, at finalization = %v, want %v", got, atFinish, tc.want)
			}
			b.Push(roomIOTestAudioPacket(143))
			b.stop(true)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("packets after stop = %v", got)
			}
		})
	}
}

func TestRoomIOAudioBufferTimeout(t *testing.T) {
	packets := make(chan uint16, 3)
	b := newRoomIOAudioBuffer(func(payload []byte) { packets <- binary.BigEndian.Uint16(payload) }, nil)
	defer b.stop(false)
	b.Push(roomIOTestAudioPacket(100))
	select {
	case <-packets:
	default:
		t.Fatal("contiguous audio was not emitted immediately")
	}
	b.Push(roomIOTestAudioPacket(102))
	select {
	case seq := <-packets:
		t.Fatalf("gap emitted immediately: %d", seq)
	default:
	}
	select {
	case seq := <-packets:
		if seq != 102 {
			t.Fatalf("packet = %d, want 102", seq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gap did not expire without another packet")
	}
}

func TestRoomIOAudioBufferStopDiscardsPending(t *testing.T) {
	var got []uint16
	b := newRoomIOAudioBuffer(func(payload []byte) { got = append(got, binary.BigEndian.Uint16(payload)) }, func() {
		t.Error("discard finalized the stream")
	})
	b.Push(roomIOTestAudioPacket(100))
	b.Push(roomIOTestAudioPacket(102))
	b.stop(false)
	b.Push(roomIOTestAudioPacket(103))
	b.stop(true)
	if !reflect.DeepEqual(got, []uint16{100}) {
		t.Fatalf("packets = %v, want [100]", got)
	}
}

func TestRoomIOAudioBufferShutdownWaitsForTimerCallback(t *testing.T) {
	rio := &RoomIO{}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	b := newRoomIOAudioBuffer(func(payload []byte) {
		if binary.BigEndian.Uint16(payload) == 102 {
			close(entered)
			<-release
			// Shutdown must not hold rio.mu while awaiting this callback.
			if !rio.isClosed() {
				t.Error("callback did not observe shutdown")
			}
		}
	}, nil)
	if !rio.registerAudioBuffer(b) {
		t.Fatal("registration rejected")
	}
	b.Push(roomIOTestAudioPacket(100))
	b.Push(roomIOTestAudioPacket(102))
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timer callback did not start")
	}
	done := make(chan struct{})
	rio.BeginShutdown()
	go func() { rio.stopAudioBuffers(); close(done) }()
	select {
	case <-done:
		t.Fatal("shutdown returned during callback")
	case <-time.After(20 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not complete")
	}
	b.Push(roomIOTestAudioPacket(103))
	late := newRoomIOAudioBuffer(func([]byte) { t.Error("late buffer delivered audio") }, nil)
	if rio.registerAudioBuffer(late) {
		t.Fatal("registration accepted after shutdown")
	}
	late.Push(roomIOTestAudioPacket(100))
}

func TestRoomIOAudioBuffersDeliverDecodedAudioBeforeEOFFlush(t *testing.T) {
	for _, aux := range []bool{false, true} {
		name := "primary"
		if aux {
			name = "auxiliary"
		}
		t.Run(name, func(t *testing.T) {
			dec, err := newOpusDecoder(48000, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			rio := &RoomIO{
				decoder: dec, audioInputGeneration: 1,
				Recorder: &RecorderIO{started: true, now: time.Now},
			}
			var b *roomIOAudioBuffer
			if aux {
				b = rio.newAuxAudioInputBuffer("track", 48000, dec)
			} else {
				b = rio.newAudioInputBuffer(1, 48000, rio.newInputAudioStream(), newRoomIOInputConverter(rio.audioInputSampleRate()))
			}
			defer b.stop(false)
			for _, seq := range []uint16{100, 102, 141} {
				p := roomIOTestAudioPacket(seq)
				p.Payload = []byte{0xf8, 0xff, 0xfe} // 20 ms Opus silence
				b.Push(p)
			}
			b.stop(true)
			frames := rio.Recorder.inFrames
			if aux {
				frames = rio.Recorder.auxFrames
			}
			var samples uint32
			for _, recorded := range frames {
				frame := recorded.frame
				if frame.SampleRate != 24000 || frame.NumChannels != 1 || len(frame.Data) != int(frame.SamplesPerChannel)*2 {
					t.Fatalf("invalid decoded frame: rate=%d channels=%d samples=%d bytes=%d", frame.SampleRate, frame.NumChannels, frame.SamplesPerChannel, len(frame.Data))
				}
				samples += frame.SamplesPerChannel
			}
			want := uint32(2880)
			if !aux {
				want = 1440 + 12000
			} // Existing 500 ms input silence flush.
			if samples != want {
				t.Fatalf("decoded samples = %d, want %d", samples, want)
			}
		})
	}
}

func TestRoomIOAudioInputBufferRejectsStaleAndDisabledAudio(t *testing.T) {
	for _, disable := range []bool{false, true} {
		rio := &RoomIO{audioInputGeneration: 1, Recorder: &RecorderIO{started: true, now: time.Now}}
		b := rio.newAudioInputBuffer(1, 48000, rio.newInputAudioStream(), newRoomIOInputConverter(48000))
		b.Push(roomIOTestAudioPacket(100))
		b.Push(roomIOTestAudioPacket(102))
		rio.mu.Lock()
		if disable {
			rio.audioDisabled = true
		} else {
			rio.audioInputGeneration++
		}
		rio.mu.Unlock()
		b.stop(true)
		if len(rio.Recorder.inFrames) != 0 {
			t.Fatal("inactive track flushed buffered audio")
		}
	}
}

type roomIOBlockingInputDecoder struct {
	entered chan struct{}
	release chan struct{}
	closed  atomic.Bool
	late    atomic.Bool
}

func (d *roomIOBlockingInputDecoder) Decode(payload []byte) ([]byte, error) {
	if d.closed.Load() {
		d.late.Store(true)
	}
	if binary.BigEndian.Uint16(payload) == 102 {
		close(d.entered)
		<-d.release
	}
	return make([]byte, 1920), nil
}

func (d *roomIOBlockingInputDecoder) Close() error {
	d.closed.Store(true)
	return nil
}

func TestRoomIOAudioInputBufferShutdownBeforeDecoderClose(t *testing.T) {
	dec := &roomIOBlockingInputDecoder{entered: make(chan struct{}), release: make(chan struct{})}
	rio := &RoomIO{decoder: dec, audioInputGeneration: 1}
	b := rio.newAudioInputBuffer(1, 48000, rio.newInputAudioStream(), newRoomIOInputConverter(24000))
	var once sync.Once
	t.Cleanup(func() {
		once.Do(func() { close(dec.release) })
		rio.releaseAudioBuffer(b)
	})
	if !rio.registerAudioBuffer(b) {
		t.Fatal("registration rejected")
	}
	b.Push(roomIOTestAudioPacket(100))
	b.Push(roomIOTestAudioPacket(102))
	select {
	case <-dec.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timer did not enter decoder")
	}
	done := make(chan error, 1)
	go func() { done <- rio.Close() }()
	select {
	case err := <-done:
		t.Fatalf("shutdown returned during decode: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if dec.closed.Load() {
		t.Fatal("decoder closed while in use")
	}
	once.Do(func() { close(dec.release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not complete")
	}
	b.Push(roomIOTestAudioPacket(103))
	if !dec.closed.Load() || dec.late.Load() {
		t.Fatal("decoder lifetime crossed shutdown")
	}
}
