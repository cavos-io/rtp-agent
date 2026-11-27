package livekit

import (
	"math"
	"testing"
)

func roomIOTestOpusPacket(t testing.TB) []byte {
	t.Helper()
	enc, err := newOpusEncoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]byte, 960*2) // 20 ms at 48 kHz
	for i := 0; i < 960; i++ {
		sample := int16(8000 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcm[i*2] = byte(sample)
		pcm[i*2+1] = byte(sample >> 8)
	}
	packet, err := enc.Encode(pcm)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), packet...)
}

func TestRoomIOOpusDecodeRate(t *testing.T) {
	for rate, want := range map[uint32]uint32{0: 48000, 8000: 8000, 16000: 16000, 24000: 24000, 44100: 48000, 48000: 48000} {
		if got := roomIOOpusDecodeRate(rate); got != want {
			t.Errorf("roomIOOpusDecodeRate(%d) = %d, want %d", rate, got, want)
		}
	}
}

// A 20 ms packet decoded at the session input rate must already be a full
// 20 ms frame at that rate, so the input converter passes it through.
func TestRoomIODecodesOpusAtInputRate(t *testing.T) {
	packet := roomIOTestOpusPacket(t)
	for _, rate := range []uint32{16000, 24000} {
		dec, err := newOpusDecoder(int(roomIOOpusDecodeRate(rate)), 1)
		if err != nil {
			t.Fatal(err)
		}
		pcm, err := dec.Decode(packet)
		if err != nil {
			t.Fatal(err)
		}
		frame := newRoomIOInputConverter(rate).Convert(roomIOInputFrameFromPCM(pcm, rate, 1))
		if frame == nil || frame.SampleRate != rate || frame.SamplesPerChannel != rate/50 {
			t.Fatalf("rate %d: got frame %+v", rate, frame)
		}
	}
}

// Compares the inbound per-packet cost of decoding at 48 kHz and resampling
// (the previous behaviour) with decoding directly at the 24 kHz input rate.
func BenchmarkRoomIOInputDecode(b *testing.B) {
	packet := roomIOTestOpusPacket(b)
	for _, tc := range []struct {
		name string
		rate uint32
	}{{"decode48k_resample24k", 48000}, {"decode24k", 24000}} {
		b.Run(tc.name, func(b *testing.B) {
			dec, err := newOpusDecoder(int(tc.rate), 1)
			if err != nil {
				b.Fatal(err)
			}
			converter := newRoomIOInputConverter(24000)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				pcm, err := dec.Decode(packet)
				if err != nil {
					b.Fatal(err)
				}
				converter.Convert(roomIOInputFrameFromPCM(pcm, tc.rate, 1))
			}
		})
	}
}
