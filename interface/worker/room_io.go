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
	"github.com/google/uuid"
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

type AudioInputOptions struct {
	Enabled                bool
	SampleRate             int
	NumChannels            int
	FrameSizeMs            int
	PreConnectAudio        bool
	PreConnectAudioTimeout time.Duration
}

type AudioOutputOptions struct {
	Enabled              bool
	SampleRate           int
	NumChannels          int
	TrackName            string
	TrackPublishOptions  *lksdk.TrackPublicationOptions
}

type VideoInputOptions struct {
	Enabled bool
}

type VideoOutputOptions struct {
	Enabled bool
}

type TextInputOptions struct {
	Enabled      bool
	InputHandler func(s *agent.AgentSession, text string) error
}

type TextOutputOptions struct {
	Enabled                  bool
	SyncTranscription        bool
	TranscriptionSpeedFactor float64
}

type RoomOptions struct {
	AudioInput          *AudioInputOptions
	AudioOutput         *AudioOutputOptions
	VideoInput          *VideoInputOptions
	VideoOutput         *VideoOutputOptions
	TextInput           *TextInputOptions
	TextOutput          *TextOutputOptions
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
	videoInCh       chan *model.VideoFrame

	participantIdentity string

	trackContexts   map[string]context.CancelFunc
	trackContextsMu sync.Mutex

	sync *agent.TranscriptSynchronizer

	targetPlayoutTime time.Time
}

// --- agent.TextOutput Implementation ---

type RoomTextOutput struct {
	room      *lksdk.Room
	writer    *lksdk.TextStreamWriter
	segmentID string
	mu        sync.Mutex
}

func NewRoomTextOutput(room *lksdk.Room) *RoomTextOutput {
	return &RoomTextOutput{room: room}
}

func (t *RoomTextOutput) Label() string {
	return "RoomTextOutput"
}

func (t *RoomTextOutput) SetSegmentID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.segmentID = id
}

func (t *RoomTextOutput) CaptureText(text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.room == nil || t.room.LocalParticipant == nil {
		return fmt.Errorf("room or local participant not ready")
	}

	if t.writer == nil {
		if t.segmentID == "" {
			t.segmentID = "SG_" + uuid.NewString()[:8]
		}
		opts := lksdk.StreamTextOptions{
			Topic: "lk-agent-transcription",
			Attributes: map[string]string{
				"segment_id": t.segmentID,
				"final":      "false",
			},
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
		// Close the current segment
		t.writer.Close()
		t.writer = nil
	}
}

func (t *RoomTextOutput) OnAttached() {}
func (t *RoomTextOutput) OnDetached() {}

func NewRoomIO(room *lksdk.Room, session *agent.AgentSession, opts RoomOptions) *RoomIO {
	dec, _ := newOpusDecoder(48000, 1)
	enc, _ := newOpusEncoder(48000, 1)

	if opts.ParticipantIdentity != "" {
		session.Options.LinkedParticipant = room.GetParticipantByIdentity(opts.ParticipantIdentity)
	}

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
		videoInCh:           make(chan *model.VideoFrame, 100),
		playoutCh:           make(chan interface{}, 100),
		clearBufferCh:       make(chan struct{}),
		flushWaiters:        make([]chan struct{}, 0),
		participantIdentity: opts.ParticipantIdentity,
		trackContexts:       make(map[string]context.CancelFunc),
	}

	go rio.playoutLoop(context.Background())

	if session.Assistant == nil {
		session.Assistant = agent.NewPipelineAgent(session.VAD, session.STT, session.LLM, session.TTS, session.ChatCtx)
	}

	// Setup Input
	audioInputEnabled := true
	if opts.AudioInput != nil && !opts.AudioInput.Enabled {
		audioInputEnabled = false
	}
	if audioInputEnabled {
		session.Input.Audio = rio.Recorder.RecordInput(rio)
	}

	videoInputEnabled := false
	if opts.VideoInput != nil && opts.VideoInput.Enabled {
		videoInputEnabled = true
	}
	if videoInputEnabled {
		session.Input.Video = NewRoomVideoInput(rio)
	}

	// Setup Output
	audioOutputEnabled := true
	if opts.AudioOutput != nil && !opts.AudioOutput.Enabled {
		audioOutputEnabled = false
	}

	syncTranscription := session.Options.UseTTSAlignedTranscript
	if opts.TextOutput != nil {
		syncTranscription = opts.TextOutput.SyncTranscription
	}

	if audioOutputEnabled {
		if syncTranscription {
			textOut := NewRoomTextOutput(room)
			speedFactor := 1.0
			if opts.TextOutput != nil && opts.TextOutput.TranscriptionSpeedFactor > 0 {
				speedFactor = opts.TextOutput.TranscriptionSpeedFactor
			}
			sync := agent.NewTranscriptSynchronizer(session.Options.SpeakingRate*speedFactor, session.Options.TranscriptRefreshRate)
			rio.sync = sync

			syncedAudio := agent.NewSyncedAudioOutput(sync, rio)
			syncedText := agent.NewSyncedTextOutput(sync, textOut)

			session.SetAudioOutput(rio.Recorder.RecordOutput(syncedAudio))
			session.Output.Transcription = syncedText
		} else {
			session.SetAudioOutput(rio.Recorder.RecordOutput(rio))
			if opts.TextOutput != nil && opts.TextOutput.Enabled {
				session.Output.Transcription = NewRoomTextOutput(room)
			}
		}
	} else if opts.TextOutput != nil && opts.TextOutput.Enabled {
		session.Output.Transcription = NewRoomTextOutput(room)
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

type RoomVideoInput struct {
	rio *RoomIO
}

func NewRoomVideoInput(rio *RoomIO) *RoomVideoInput {
	return &RoomVideoInput{rio: rio}
}

func (rvi *RoomVideoInput) Label() string {
	return "RoomVideoIO"
}

func (rvi *RoomVideoInput) Stream() <-chan *model.VideoFrame {
	return rvi.rio.videoInCh
}

func (rvi *RoomVideoInput) OnAttached() {}
func (rvi *RoomVideoInput) OnDetached() {}

func (rio *RoomIO) OnPlaybackStarted(f func(ev agent.PlaybackStartedEvent)) {
	rio.onPlaybackStarted = f
}

func (rio *RoomIO) OnPlaybackFinished(f func(ev agent.PlaybackFinishedEvent)) {
	rio.onPlaybackFinished = f
}

// --- agent.AudioOutput Implementation ---

type flushMarker chan struct{}

func (rio *RoomIO) CaptureFrame(frame *model.AudioFrame) error {
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
			rio.targetPlayoutTime = time.Time{}
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
				// Wait for real playout completion
				if !rio.targetPlayoutTime.IsZero() {
					delay := time.Until(rio.targetPlayoutTime)
					if delay > 0 {
						rio.mu.Unlock()
						time.Sleep(delay)
						rio.mu.Lock()
					}
				}

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
				rio.targetPlayoutTime = time.Time{}

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

				now := time.Now()
				rio.mu.Lock()
				if rio.targetPlayoutTime.IsZero() {
					rio.targetPlayoutTime = now
				}
				delay := time.Until(rio.targetPlayoutTime)
				rio.mu.Unlock()

				if delay > 0 {
					time.Sleep(delay)
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
				rio.targetPlayoutTime = rio.targetPlayoutTime.Add(duration)
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
	cb.OnTrackUnsubscribed = rio.onTrackUnsubscribed
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
	audioOutputEnabled := true
	if rio.Options.AudioOutput != nil && !rio.Options.AudioOutput.Enabled {
		audioOutputEnabled = false
	}

	if !audioOutputEnabled {
		return nil
	}

	track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: 48000,
		Channels:  1, // Match encoder
	})
	if err != nil {
		return err
	}

	trackName := "agent-audio"
	var pubOpts *lksdk.TrackPublicationOptions
	if rio.Options.AudioOutput != nil {
		if rio.Options.AudioOutput.TrackName != "" {
			trackName = rio.Options.AudioOutput.TrackName
		}
		pubOpts = rio.Options.AudioOutput.TrackPublishOptions
	}

	if pubOpts == nil {
		pubOpts = &lksdk.TrackPublicationOptions{
			Name: trackName,
		}
	} else if pubOpts.Name == "" {
		pubOpts.Name = trackName
	}

	_, err = rio.Room.LocalParticipant.PublishTrack(track, pubOpts)
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

	rio.trackContextsMu.Lock()
	if _, ok := rio.trackContexts[track.ID()]; ok {
		rio.trackContextsMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	rio.trackContexts[track.ID()] = cancel
	rio.trackContextsMu.Unlock()

	if track.Kind() == webrtc.RTPCodecTypeAudio {
		audioInputEnabled := true
		if rio.Options.AudioInput != nil && !rio.Options.AudioInput.Enabled {
			audioInputEnabled = false
		}
		if audioInputEnabled {
			go rio.handleAudioTrack(ctx, track)
		}
	} else if track.Kind() == webrtc.RTPCodecTypeVideo {
		videoInputEnabled := false
		if rio.Options.VideoInput != nil && rio.Options.VideoInput.Enabled {
			videoInputEnabled = true
		}
		if videoInputEnabled {
			go rio.handleVideoTrack(ctx, track)
		}
	}
}

func (rio *RoomIO) onTrackUnsubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	rio.trackContextsMu.Lock()
	if cancel, ok := rio.trackContexts[track.ID()]; ok {
		cancel()
		delete(rio.trackContexts, track.ID())
	}
	rio.trackContextsMu.Unlock()
}

func (rio *RoomIO) handleVideoTrack(ctx context.Context, track *webrtc.TrackRemote) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		rio.mu.Lock()
		if rio.closed {
			rio.mu.Unlock()
			return
		}
		rio.mu.Unlock()

		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}

		// This is a minimal video ingestion, just pushing RTP packets or raw frames.
		// A full implementation would decode the video, but for now we just wrap it
		// to fulfill the interface and pass it through the system.
		frame := &model.VideoFrame{
			Data:      pkt.Payload,
			Timestamp: time.Duration(pkt.Timestamp),
		}

		select {
		case rio.videoInCh <- frame:
		default:
			// drop frame if queue is full
		}
	}
}

func (rio *RoomIO) handleAudioTrack(ctx context.Context, track *webrtc.TrackRemote) {
	// First, check for and flush any pre-connect audio buffered
	preCtx, preCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer preCancel()

	if frames := rio.preConnectAudio.WaitForData(preCtx, track.ID()); len(frames) > 0 {
		for _, frame := range frames {
			rio.audioInCh <- frame
		}
	}

	sb := samplebuilder.New(20, &codecs.OpusPacket{}, track.Codec().ClockRate)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

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

			rio.audioInCh <- frame
		}
	}
}

func (rio *RoomIO) Close() error {
	rio.mu.Lock()
	if rio.closed {
		rio.mu.Unlock()
		return nil
	}
	rio.closed = true
	rio.mu.Unlock()

	rio.trackContextsMu.Lock()
	for _, cancel := range rio.trackContexts {
		cancel()
	}
	rio.trackContexts = make(map[string]context.CancelFunc)
	rio.trackContextsMu.Unlock()

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
