package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/hraban/opus"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/server-sdk-go/v2/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type AudioDecoder interface {
	Decode(data []byte) ([]byte, error)
	Close() error
}

type AudioEncoder interface {
	Encode(pcm []byte) ([]byte, error)
	Close() error
}

type opusDecoder struct {
	decoder *opus.Decoder
	buf     []int16
}

func newOpusDecoder(sampleRate int, channels int) (*opusDecoder, error) {
	dec, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, err
	}
	// Max frame size is typically 120ms at 48kHz = 5760 samples per channel
	return &opusDecoder{
		decoder: dec,
		buf:     make([]int16, 5760*channels),
	}, nil
}

func (d *opusDecoder) Decode(data []byte) ([]byte, error) {
	n, err := d.decoder.Decode(data, d.buf)
	if err != nil {
		return nil, err
	}

	// Convert int16 slice to byte slice
	out := make([]byte, n*2) // Assuming 1 channel for now, multiply by channels if needed
	for i := 0; i < n; i++ {
		out[i*2] = byte(d.buf[i])
		out[i*2+1] = byte(d.buf[i] >> 8)
	}
	return out, nil
}

func (d *opusDecoder) Close() error {
	return nil
}

type opusEncoder struct {
	encoder *opus.Encoder
	buf     []byte
}

func newOpusEncoder(sampleRate int, channels int) (*opusEncoder, error) {
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		return nil, err
	}
	return &opusEncoder{
		encoder: enc,
		buf:     make([]byte, 4000), // Max packet size
	}, nil
}

func (e *opusEncoder) Encode(pcm []byte) ([]byte, error) {
	// Convert byte slice back to int16 slice for Opus encoder
	in := make([]int16, len(pcm)/2)
	for i := 0; i < len(in); i++ {
		in[i] = int16(pcm[i*2]) | (int16(pcm[i*2+1]) << 8)
	}

	n, err := e.encoder.Encode(in, e.buf)
	if err != nil {
		return nil, err
	}

	out := make([]byte, n)
	copy(out, e.buf[:n])
	return out, nil
}

func (e *opusEncoder) Close() error {
	return nil
}

type RoomOptions struct {
}

type RoomIO struct {
	Room         *lksdk.Room
	AgentSession *agent.AgentSession
	Options      RoomOptions
	Recorder     *RecorderIO

	mu     sync.Mutex
	closed bool

	audioTrack *lksdk.LocalTrack
	decoder    AudioDecoder
	encoder    AudioEncoder

	preConnectAudio *PreConnectAudioHandler
	audioInCh       chan *model.AudioFrame
}

func NewRoomIO(room *lksdk.Room, session *agent.AgentSession, opts RoomOptions) *RoomIO {
	dec, _ := newOpusDecoder(48000, 1)
	enc, _ := newOpusEncoder(48000, 1)

	preConnectAudio := NewPreConnectAudioHandler(room, 5*time.Second)
	preConnectAudio.Register()

	rio := &RoomIO{
		Room:            room,
		AgentSession:    session,
		Options:         opts,
		decoder:         dec,
		encoder:         enc,
		Recorder:        NewRecorderIO(session),
		preConnectAudio: preConnectAudio,
		audioInCh:       make(chan *model.AudioFrame, 100),
	}

	if session.Assistant == nil {
		session.Assistant = agent.NewPipelineAgent(session.VAD, session.STT, session.LLM, session.TTS, session.ChatCtx)
	}

	session.Input.Audio = rio
	session.Output.Audio = rio

	return rio
}

// --- agent.AudioInput Implementation ---
func (rio *RoomIO) Label() string {
	return "RoomAudioIO"
}

func (rio *RoomIO) Stream() <-chan *model.AudioFrame {
	return rio.audioInCh
}

func (rio *RoomIO) OnAttached() {}
func (rio *RoomIO) OnDetached() {}

// --- agent.AudioOutput Implementation ---
func (rio *RoomIO) CaptureFrame(frame *model.AudioFrame) error {
	if rio.Recorder != nil {
		rio.Recorder.RecordOutput(frame)
	}

	rio.mu.Lock()
	track := rio.audioTrack
	encoder := rio.encoder
	rio.mu.Unlock()

	if track == nil {
		return fmt.Errorf("no audio track")
	}

	data := frame.Data
	if encoder != nil {
		if encoded, err := encoder.Encode(frame.Data); err == nil {
			data = encoded
		}
	}

	// Calculate duration based on sample rate and samples
	duration := time.Duration(frame.SamplesPerChannel) * time.Second / time.Duration(frame.SampleRate)

	return track.WriteSample(media.Sample{
		Data:     data,
		Duration: duration,
	}, nil)
}

func (rio *RoomIO) Flush()       {}
func (rio *RoomIO) ClearBuffer() {}
func (rio *RoomIO) Pause()       {}
func (rio *RoomIO) Resume()      {}

func (rio *RoomIO) GetCallback() *lksdk.RoomCallback {
	cb := lksdk.NewRoomCallback()
	cb.OnTrackSubscribed = rio.onTrackSubscribed
	return cb
}

func (rio *RoomIO) Start(ctx context.Context) error {
	track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: 48000,
		Channels:  1, // Match encoder
	})
	if err != nil {
		return err
	}

	_, err = rio.Room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name: "agent-audio",
	})
	if err != nil {
		return err
	}

	rio.audioTrack = track
	return nil
}

func (rio *RoomIO) onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if track.Kind() == webrtc.RTPCodecTypeAudio {
		go rio.handleAudioTrack(track)
	}
}

func (rio *RoomIO) handleAudioTrack(track *webrtc.TrackRemote) {
	// First, check for and flush any pre-connect audio buffered
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if frames := rio.preConnectAudio.WaitForData(ctx, track.ID()); len(frames) > 0 {
		for _, frame := range frames {
			if rio.Recorder != nil {
				rio.Recorder.RecordInput(frame)
			}
			rio.audioInCh <- frame
		}
	}

	sb := samplebuilder.New(20, &codecs.OpusPacket{}, track.Codec().ClockRate)

	for {
		rio.mu.Lock()
		if rio.closed {
			rio.mu.Unlock()
			return
		}
		rio.mu.Unlock()

		pkt, _, err := track.ReadRTP()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				// log error
			}
			return
		}

		sb.Push(pkt)
		for {
			sample := sb.Pop()
			if sample == nil {
				break
			}

			pcm := sample.Data
			if rio.decoder != nil {
				if decoded, err := rio.decoder.Decode(sample.Data); err == nil {
					pcm = decoded
				}
			}

			frame := &model.AudioFrame{
				Data:              pcm,
				SampleRate:        track.Codec().ClockRate,
				NumChannels:       1, // We decode to mono for simplicity
				SamplesPerChannel: uint32(len(pcm) / 2),
			}

			if rio.Recorder != nil {
				rio.Recorder.RecordInput(frame)
			}
			rio.audioInCh <- frame
		}
	}
}

func (rio *RoomIO) Close() error {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	rio.closed = true
	if rio.decoder != nil {
		rio.decoder.Close()
	}
	if rio.encoder != nil {
		rio.encoder.Close()
	}
	return nil
}
