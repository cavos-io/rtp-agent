package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/library/logger"
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
	SampleRate() int
	Channels() int
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
	encoder    *opus.Encoder
	sampleRate int
	channels   int
	buf        []byte
}

func newOpusEncoder(sampleRate int, channels int) (*opusEncoder, error) {
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		return nil, err
	}
	return &opusEncoder{
		encoder:    enc,
		sampleRate: sampleRate,
		channels:   channels,
		buf:        make([]byte, 4000), // Max packet size
	}, nil
}

func (e *opusEncoder) SampleRate() int { return e.sampleRate }
func (e *opusEncoder) Channels() int   { return e.channels }

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
	ParticipantKinds    []lksdk.ParticipantKind
	ParticipantIdentity string
	CloseOnDisconnect   bool
	DeleteRoomOnClose   bool
	JobContext          *JobContext // Used for room deletion if DeleteRoomOnClose is true
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

	onPlaybackStarted  func(ev agent.PlaybackStartedEvent)
	onPlaybackFinished func(ev agent.PlaybackFinishedEvent)

	playoutCh     chan interface{}
	clearBufferCh chan struct{}

	playbackStarted bool
	pushedDuration  time.Duration
	paused          bool
	flushWaiters    []chan struct{}

	preConnectAudio *PreConnectAudioHandler
	audioInCh       chan *model.AudioFrame

	participantIdentity string

	sync *agent.TranscriptSynchronizer
}

// --- agent.TextOutput Implementation ---

type RoomTextOutput struct {
	room   *lksdk.Room
	writer *lksdk.TextStreamWriter
	mu     sync.Mutex
}

func NewRoomTextOutput(room *lksdk.Room) *RoomTextOutput {
	return &RoomTextOutput{room: room}
}

func (t *RoomTextOutput) Label() string {
	return "RoomTextOutput"
}

func (t *RoomTextOutput) CaptureText(text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.room == nil || t.room.LocalParticipant == nil {
		return fmt.Errorf("room or local participant not ready")
	}

	if t.writer == nil {
		opts := lksdk.StreamTextOptions{
			Topic: "lk-agent-transcription",
		}
		t.writer = t.room.LocalParticipant.StreamText(opts)
	}

	if t.writer != nil {
		t.writer.Write(text, nil)
		return nil
	}

	return nil
}

func (t *RoomTextOutput) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.writer != nil {
		t.writer.Close()
		t.writer = nil
	}
}

func (t *RoomTextOutput) OnAttached() {}
func (t *RoomTextOutput) OnDetached() {}

func NewRoomIO(room *lksdk.Room, session *agent.AgentSession, opts RoomOptions) *RoomIO {
	dec, _ := newOpusDecoder(48000, 1)
	enc, _ := newOpusEncoder(48000, 1)

	preConnectAudio := NewPreConnectAudioHandler(room, 5*time.Second)
	preConnectAudio.Register()

	rio := &RoomIO{
		Room:                room,
		AgentSession:        session,
		Options:             opts,
		decoder:             dec,
		encoder:             enc,
		Recorder:            NewRecorderIO(session),
		preConnectAudio:     preConnectAudio,
		audioInCh:           make(chan *model.AudioFrame, 100),
		playoutCh:           make(chan interface{}, 100),
		clearBufferCh:       make(chan struct{}),
		flushWaiters:        make([]chan struct{}, 0),
		participantIdentity: opts.ParticipantIdentity,
	}

	go rio.playoutLoop(context.Background())

	if session.Assistant == nil {
		session.Assistant = agent.NewPipelineAgent(session.VAD, session.STT, session.LLM, session.TTS, session.ChatCtx)
	}

	session.Input.Audio = rio

	if session.Options.UseTTSAlignedTranscript {
		textOut := NewRoomTextOutput(room)
		sync := agent.NewTranscriptSynchronizer(session.Options.SpeakingRate)
		rio.sync = sync

		syncedAudio := agent.NewSyncedAudioOutput(sync, rio)
		syncedText := agent.NewSyncedTextOutput(sync, textOut)

		session.SetAudioOutput(syncedAudio)
		session.Output.Transcription = syncedText
	} else {
		session.SetAudioOutput(rio)
	}
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

func (rio *RoomIO) OnPlaybackStarted(f func(ev agent.PlaybackStartedEvent)) {
	rio.onPlaybackStarted = f
}

func (rio *RoomIO) OnPlaybackFinished(f func(ev agent.PlaybackFinishedEvent)) {
	rio.onPlaybackFinished = f
}

// --- agent.AudioOutput Implementation ---

type flushMarker chan struct{}

func (rio *RoomIO) CaptureFrame(frame *model.AudioFrame) error {
	if rio.Recorder != nil {
		rio.Recorder.RecordOutput(frame)
	}

	rio.playoutCh <- frame
	return nil
}

func (rio *RoomIO) playoutLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-rio.clearBufferCh:
			// Drain playout channel
		drain:
			for {
				select {
				case item := <-rio.playoutCh:
					if fm, ok := item.(flushMarker); ok {
						close(fm)
					}
				default:
					break drain
				}
			}

			rio.mu.Lock()
			rio.closeFlushWaitersLocked()
			if rio.playbackStarted {
				if rio.onPlaybackFinished != nil {
					go rio.onPlaybackFinished(agent.PlaybackFinishedEvent{
						PlaybackPosition: rio.pushedDuration,
						Interrupted:      true,
					})
				}
				rio.playbackStarted = false
				rio.pushedDuration = 0
			}
			rio.mu.Unlock()

		case item := <-rio.playoutCh:
			switch v := item.(type) {
			case flushMarker:
				rio.mu.Lock()
				if rio.playbackStarted {
					if rio.onPlaybackFinished != nil {
						go rio.onPlaybackFinished(agent.PlaybackFinishedEvent{
							PlaybackPosition: rio.pushedDuration,
							Interrupted:      false,
						})
					}
					rio.playbackStarted = false
					rio.pushedDuration = 0
				}

				// Remove from flushWaiters array and close it
				for i, ch := range rio.flushWaiters {
					if ch == (chan struct{})(v) {
						rio.flushWaiters = append(rio.flushWaiters[:i], rio.flushWaiters[i+1:]...)
						break
					}
				}
				rio.mu.Unlock()
				close(v)

			case *model.AudioFrame:
				frame := v
				rio.mu.Lock()
				paused := rio.paused
				track := rio.audioTrack
				encoder := rio.encoder

				if !paused && !rio.playbackStarted {
					rio.playbackStarted = true
					if rio.onPlaybackStarted != nil {
						go rio.onPlaybackStarted(agent.PlaybackStartedEvent{CreatedAt: time.Now()})
					}
				}
				rio.mu.Unlock()

				if track == nil {
					continue
				}

				duration := time.Duration(frame.SamplesPerChannel) * time.Second / time.Duration(frame.SampleRate)

				if paused {
					// If paused, just sleep to simulate playout pacing and drop frame
					time.Sleep(duration)
					continue
				}

				if encoder == nil || encoder.SampleRate() != int(frame.SampleRate) || encoder.Channels() != int(frame.NumChannels) {
					enc, err := newOpusEncoder(int(frame.SampleRate), int(frame.NumChannels))
					if err == nil {
						rio.mu.Lock()
						if rio.encoder != nil {
							rio.encoder.Close()
						}
						rio.encoder = enc
						encoder = enc
						rio.mu.Unlock()
					}
				}

				data := frame.Data
				if encoder != nil {
					if encoded, err := encoder.Encode(frame.Data); err == nil {
						data = encoded
					}
				}

				rio.mu.Lock()
				rio.pushedDuration += duration
				rio.mu.Unlock()

				_ = track.WriteSample(media.Sample{
					Data:     data,
					Duration: duration,
				}, nil)
			}
		}
	}
}

func (rio *RoomIO) closeFlushWaitersLocked() {
	for _, ch := range rio.flushWaiters {
		close(ch)
	}
	rio.flushWaiters = rio.flushWaiters[:0]
}

func (rio *RoomIO) Flush() {
	rio.mu.Lock()
	done := make(chan struct{})
	rio.flushWaiters = append(rio.flushWaiters, done)
	rio.mu.Unlock()

	rio.playoutCh <- flushMarker(done)
}

func (rio *RoomIO) ClearBuffer() {
	rio.clearBufferCh <- struct{}{}
}

func (rio *RoomIO) Pause() {
	rio.mu.Lock()
	rio.paused = true
	rio.mu.Unlock()
}

func (rio *RoomIO) Resume() {
	rio.mu.Lock()
	rio.paused = false
	rio.mu.Unlock()
}

func (rio *RoomIO) WaitForPlayout(ctx context.Context) error {
	rio.mu.Lock()
	var done chan struct{}
	if len(rio.flushWaiters) > 0 {
		done = rio.flushWaiters[len(rio.flushWaiters)-1]
	} else {
		// If no waiters, flush is already done
		rio.mu.Unlock()
		return nil
	}
	rio.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (rio *RoomIO) GetCallback() *lksdk.RoomCallback {
	cb := lksdk.NewRoomCallback()
	cb.OnTrackSubscribed = rio.onTrackSubscribed
	cb.OnParticipantDisconnected = rio.onParticipantDisconnected
	return cb
}

func (rio *RoomIO) SetParticipant(identity string) {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	rio.participantIdentity = identity
}

func (rio *RoomIO) UnsetParticipant() {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	rio.participantIdentity = ""
}

func (rio *RoomIO) onParticipantDisconnected(participant *lksdk.RemoteParticipant) {
	rio.mu.Lock()
	linkedIdentity := rio.participantIdentity
	rio.mu.Unlock()

	if linkedIdentity == participant.Identity() {
		if rio.Options.CloseOnDisconnect && rio.AgentSession != nil {
			_ = rio.AgentSession.Close()
		}
	}
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
	rio.mu.Lock()
	identity := rio.participantIdentity
	rio.mu.Unlock()

	if identity != "" && identity != rp.Identity() {
		// Ignore tracks from non-linked participants if a specific identity is targeted
		return
	}

	if identity == "" && len(rio.Options.ParticipantKinds) > 0 {
		// If no specific identity is targeted, check if the participant kind is accepted
		kindAccepted := false
		for _, kind := range rio.Options.ParticipantKinds {
			if rp.Kind() == kind {
				kindAccepted = true
				break
			}
		}
		if !kindAccepted {
			return
		}
	}

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

	if rio.sync != nil {
		rio.sync.Close()
	}

	if rio.Options.DeleteRoomOnClose {
		if rio.Options.JobContext != nil && rio.Room != nil {
			logger.Logger.Infow("deleting room on agent session close", "room", rio.Room.Name())
			_, err := rio.Options.JobContext.DeleteRoom(context.Background(), rio.Room.Name())
			if err != nil {
				logger.Logger.Errorw("failed to delete room", err)
			}
		} else if rio.Room != nil && rio.Room.LocalParticipant != nil {
			logger.Logger.Warnw("DeleteRoomOnClose is true but no JobContext provided, disconnecting instead", nil)
			rio.Room.Disconnect()
		}
	}

	return nil
}
