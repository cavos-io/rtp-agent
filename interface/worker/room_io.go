package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/google/uuid"
	"github.com/hraban/opus"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/server-sdk-go/v2/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"google.golang.org/protobuf/proto"
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
	decoder   *opus.Decoder
	buf       []int16
	callCount int
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
	d.decoder = nil
	d.buf = nil
	return nil
}

type opusEncoder struct {
	encoder    *opus.Encoder
	sampleRate int
	channels   int
	buf        []byte
}

func newOpusEncoder(sampleRate int, channels int) (*opusEncoder, error) {
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppAudio)
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
	e.encoder = nil
	e.buf = nil
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
	Enabled             bool
	SampleRate          int
	NumChannels         int
	TrackName           string
	TrackPublishOptions *lksdk.TrackPublicationOptions
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
	ctx    context.Context // session lifecycle context — cancelled on disconnect
	cancel context.CancelFunc

	audioTrack *lksdk.LocalSampleTrack
	audioPub   *lksdk.LocalTrackPublication
	videoTrack *lksdk.LocalSampleTrack
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

	sync              *agent.TranscriptSynchronizer
	targetPlayoutTime time.Time

	roomCallback *lksdk.RoomCallback
}

// --- agent.TextOutput Implementation ---

type RoomTextOutput struct {
	room                *lksdk.Room
	participantIdentity string
	trackID             string
	writer              *lksdk.TextStreamWriter
	segmentID           string
	mu                  sync.Mutex
	textCh              chan string
	ctx                 context.Context
	cancel              context.CancelFunc

	apiKey    string
	apiSecret string
	url       string
	client    *lksdk.RoomServiceClient
}

func NewRoomTextOutput(room *lksdk.Room, participantIdentity string, url, apiKey, apiSecret string) *RoomTextOutput {
	ctx, cancel := context.WithCancel(context.Background())
	t := &RoomTextOutput{
		room:                room,
		participantIdentity: participantIdentity,
		textCh:              make(chan string, 100),
		ctx:                 ctx,
		cancel:              cancel,
		url:                 url,
		apiKey:              apiKey,
		apiSecret:           apiSecret,
		client:              lksdk.NewRoomServiceClient(url, apiKey, apiSecret),
	}
	go t.worker()
	return t
}

func (t *RoomTextOutput) Label() string {
	return "RoomTextOutput"
}

func (t *RoomTextOutput) SetSegmentID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.segmentID = id
}

func (t *RoomTextOutput) SetTrackID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.trackID = id
}

func (t *RoomTextOutput) CaptureText(text string) error {
	select {
	case t.textCh <- text:
		return nil
	case <-t.ctx.Done():
		return t.ctx.Err()
	default:
		return fmt.Errorf("text output buffer full")
	}
}

func (t *RoomTextOutput) worker() {
	ticker := time.NewTicker(50 * time.Millisecond) // Throttling bit
	defer ticker.Stop()

	for {
		select {
		case <-t.ctx.Done():
			return
		case text := <-t.textCh:
			t.mu.Lock()
			if t.room == nil || t.room.LocalParticipant == nil {
				t.mu.Unlock()
				continue
			}

			if t.writer == nil {
				if t.segmentID == "" {
					t.segmentID = "SG_" + uuid.NewString()[:8]
				}
				opts := lksdk.StreamTextOptions{
					Topic: "lk.transcription",
					Attributes: map[string]string{
						"lk.segment_id":          t.segmentID,
						"lk.transcription_final": "false",
					},
				}

				if t.trackID != "" {
					opts.Attributes["lk.transcribed_track_id"] = t.trackID
				}

				t.writer = t.room.LocalParticipant.StreamText(opts)
			}

			if t.writer != nil {
				t.writer.Write(text, nil)
			}

			// GAP-001 Fallback: Send dual mechanism via RoomServiceClient.SendData
			// Official SDK recommendation for high-reliability transcript delivery
			if t.client != nil && t.room != nil && t.room.LocalParticipant != nil {
				tp := &livekit.Transcription{
					TranscribedParticipantIdentity: t.participantIdentity,
					TrackId:                        t.trackID,
					Segments: []*livekit.TranscriptionSegment{
						{
							Id:    t.segmentID,
							Text:  text,
							Final: false,
						},
					},
				}
				packet := &livekit.DataPacket{
					Kind: livekit.DataPacket_RELIABLE,
					Value: &livekit.DataPacket_Transcription{
						Transcription: tp,
					},
				}

				if buf, err := proto.Marshal(packet); err == nil {
					go t.client.SendData(t.ctx, &livekit.SendDataRequest{
						Room: t.room.Name(),
						Data: buf,
					})
				}
			}

			t.mu.Unlock()
			<-ticker.C
		}
	}
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

func (t *RoomTextOutput) Close() {
	t.cancel()
}

func (t *RoomTextOutput) OnAttached() {}
func (t *RoomTextOutput) OnDetached() {}

func NewRoomIO(room *lksdk.Room, session *agent.AgentSession, opts RoomOptions) *RoomIO {
	inRate, inChannels := 48000, 1
	if opts.AudioInput != nil {
		if opts.AudioInput.SampleRate > 0 {
			inRate = opts.AudioInput.SampleRate
		}
		if opts.AudioInput.NumChannels > 0 {
			inChannels = opts.AudioInput.NumChannels
		}
	}
	dec, _ := newOpusDecoder(inRate, inChannels)

	outRate, outChannels := 48000, 1
	if opts.AudioOutput != nil {
		if opts.AudioOutput.SampleRate > 0 {
			outRate = opts.AudioOutput.SampleRate
		}
		if opts.AudioOutput.NumChannels > 0 {
			outChannels = opts.AudioOutput.NumChannels
		}
	}
	enc, _ := newOpusEncoder(outRate, outChannels)

	if opts.ParticipantIdentity != "" {
		session.Options.LinkedParticipant = room.GetParticipantByIdentity(opts.ParticipantIdentity)
	}

	preConnectAudio := NewPreConnectAudioHandler(room, 5*time.Second)
	preConnectAudio.Register()

	rioCtx, rioCancel := context.WithCancel(context.Background())

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
		cancel:              rioCancel,
	}

	// Wire the room to the session so that state attributes (lk.agent.state)
	// and the ClientEventsDispatcher are available for the Playground.
	session.SetRoom(room)

	rio.registerRoomCallbacks()

	go rio.playoutLoop(rioCtx)

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
		jc := rio.Options.JobContext
		if syncTranscription {
			textOut := NewRoomTextOutput(room, rio.participantIdentity, jc.URL, jc.APIKey, jc.APISecret)
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
				textOut := NewRoomTextOutput(room, rio.participantIdentity, jc.URL, jc.APIKey, jc.APISecret)
				session.Output.Transcription = textOut
			}
		}
	} else if opts.TextOutput != nil && opts.TextOutput.Enabled {
		jc := rio.Options.JobContext
		session.Output.Transcription = NewRoomTextOutput(room, rio.participantIdentity, jc.URL, jc.APIKey, jc.APISecret)
	}

	videoOutputEnabled := false
	if opts.VideoOutput != nil && opts.VideoOutput.Enabled {
		videoOutputEnabled = true
	}
	if videoOutputEnabled {
		session.Output.Video = rio
	}

	// 	// GAP-008: Register the RoomIO as a MediaPublisher to decouple AgentSession from Room
	session.Output.Publisher = rio

	return rio
}

// --- agent.MediaPublisher Implementation ---
func (rio *RoomIO) Identity() string {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if rio.Room == nil || rio.Room.LocalParticipant == nil {
		return ""
	}
	return rio.Room.LocalParticipant.Identity()
}

func (rio *RoomIO) PublishData(data []byte, topic string, destinationSIDs []string) error {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if rio.Room == nil || rio.Room.LocalParticipant == nil {
		return fmt.Errorf("room not connected")
	}
	pkt := &lksdk.UserDataPacket{
		Payload: data,
		Topic:   topic,
	}
	opts := []lksdk.DataPublishOption{
		lksdk.WithDataPublishReliable(true),
	}
	if len(destinationSIDs) > 0 {
		opts = append(opts, lksdk.WithDataPublishDestination(destinationSIDs))
	}
	return rio.Room.LocalParticipant.PublishDataPacket(pkt, opts...)
}

func (rio *RoomIO) SetAttributes(attrs map[string]string) error {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if rio.Room == nil || rio.Room.LocalParticipant == nil {
		return fmt.Errorf("room not connected")
	}
	rio.Room.LocalParticipant.SetAttributes(attrs)
	return nil
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

// --- agent.VideoOutput Implementation ---
func (rio *RoomIO) CaptureVideoFrame(frame *model.VideoFrame) error {
	rio.mu.Lock()
	track := rio.videoTrack
	rio.mu.Unlock()

	if track == nil {
		return nil
	}

	// Use 1/30s as default duration if not specified
	dur := time.Second / 30
	return track.WriteSample(media.Sample{
		Data:     frame.Data,
		Duration: dur,
	}, nil)
}

func (rio *RoomIO) OnPlaybackStarted(f func(ev agent.PlaybackStartedEvent)) {
	rio.onPlaybackStarted = f
}

func (rio *RoomIO) OnPlaybackFinished(f func(ev agent.PlaybackFinishedEvent)) {
	rio.onPlaybackFinished = f
}

// --- agent.AudioOutput Implementation ---

type flushMarker chan struct{}

func (rio *RoomIO) CaptureFrame(frame *model.AudioFrame) error {
	rio.mu.Lock()
	if rio.closed {
		rio.mu.Unlock()
		return nil
	}
	rio.mu.Unlock()
	rio.playoutCh <- frame
	return nil
}

func (rio *RoomIO) playoutLoop(ctx context.Context) {
	// Keep-alive: send comfort noise when idle so the RTP stream stays
	// active and the LiveKit server doesn't auto-mute the track.
	const silenceFrameMs = 20
	silenceDuration := time.Duration(silenceFrameMs) * time.Millisecond
	silenceSamples := 48000 * silenceFrameMs / 1000 // 960 samples at 48kHz
	// Comfort noise: very low amplitude random-ish values instead of pure
	// zeros.  Pure silence can trigger server-side mute detection.
	comfortPCM := make([]byte, silenceSamples*1*2) // mono 16-bit
	for i := 0; i < len(comfortPCM); i += 2 {
		// ~-90 dBFS square wiggle: alternating +1/-1 samples
		if (i/2)%2 == 0 {
			comfortPCM[i] = 1
		} else {
			comfortPCM[i] = 0xFF // -1 in int16 LE = 0xFFFF
			comfortPCM[i+1] = 0xFF
		}
	}
	var cachedComfortOpus []byte

	// Send keep-alive every 200ms (not every 20ms) — enough to prevent
	// server-side mute detection without wasting CPU/bandwidth.
	silenceTicker := time.NewTicker(200 * time.Millisecond)
	defer silenceTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-silenceTicker.C:
			rio.mu.Lock()
			track := rio.audioTrack
			pub := rio.audioPub
			playing := rio.playbackStarted
			paused := rio.paused
			encoder := rio.encoder
			rio.mu.Unlock()

			if track == nil || playing || paused {
				continue
			}

			// Lazily encode and cache the comfort-noise Opus frame.
			if cachedComfortOpus == nil && encoder != nil {
				if encoded, err := encoder.Encode(comfortPCM); err == nil {
					cachedComfortOpus = make([]byte, len(encoded))
					copy(cachedComfortOpus, encoded)
				}
			}
			if cachedComfortOpus != nil {
				_ = track.WriteSample(media.Sample{
					Data:     cachedComfortOpus,
					Duration: silenceDuration,
				}, nil)
			}

			// Explicitly tell the server the track is NOT muted so the
			// Playground keeps showing the agent audio panel.
			if pub != nil {
				pub.SetMuted(false)
			}
		case <-rio.clearBufferCh:
			// Drain playout channel
		drain:
			for {
				select {
				case <-rio.playoutCh:
					// flushMarkers found here are still in flushWaiters;
					// closeFlushWaitersLocked() below will close them.
					// Do NOT close them here to avoid "close of closed channel".
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
			pub := rio.audioPub
			rio.mu.Unlock()

			if pub != nil {
				pub.SetMuted(false)
			}

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
				pub := rio.audioPub
				rio.mu.Unlock()
				close(v)

				// Immediately tell server track is still active after
				// playback ends so Playground doesn't show "Waiting...".
				if pub != nil {
					pub.SetMuted(false)
				}

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

				totalDuration := time.Duration(frame.SamplesPerChannel) * time.Second / time.Duration(frame.SampleRate)

				if paused {
					// If paused, just sleep to simulate playout pacing and drop frame
					time.Sleep(totalDuration)
					continue
				}

				// Ensure encoder matches frame properties.
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

				// Opus requires specific frame sizes (up to 60 ms). TTS providers such as
				// ElevenLabs return large PCM chunks (~880 ms). Feeding an oversized buffer
				// to opus_encode returns OPUS_BAD_ARG and the wrapper silently falls back to
				// raw PCM, which the remote decoder cannot parse → silence. Split the frame
				// into 20 ms sub-frames so every Encode call uses a valid frame size.
				const opusFrameMs = 20
				opusSamplesPerFrame := int(frame.SampleRate) * opusFrameMs / 1000
				opusBytesPerFrame := opusSamplesPerFrame * int(frame.NumChannels) * 2

				pcmData := frame.Data
				for len(pcmData) > 0 {
					end := opusBytesPerFrame
					if end > len(pcmData) {
						end = len(pcmData)
					}
					chunkPCM := pcmData[:end]
					pcmData = pcmData[end:]

					chunkSamples := len(chunkPCM) / (int(frame.NumChannels) * 2)
					subDuration := time.Duration(chunkSamples) * time.Second / time.Duration(frame.SampleRate)

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

					// Pad an incomplete trailing chunk to a valid Opus frame size so the
					// encoder never receives fewer than opusSamplesPerFrame samples.
					encodePCM := chunkPCM
					if chunkSamples < opusSamplesPerFrame {
						padding := make([]byte, (opusSamplesPerFrame-chunkSamples)*int(frame.NumChannels)*2)
						encodePCM = append(append([]byte(nil), chunkPCM...), padding...)
					}

					data := chunkPCM
					if encoder != nil {
						if encoded, err := encoder.Encode(encodePCM); err == nil {
							data = encoded
						}
					}

					rio.mu.Lock()
					rio.pushedDuration += subDuration
					rio.targetPlayoutTime = rio.targetPlayoutTime.Add(subDuration)
					rio.mu.Unlock()

					_ = track.WriteSample(media.Sample{
						Data:     data,
						Duration: subDuration,
					}, nil)
				}
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
	rio.roomCallback = cb
	return cb
}

func (rio *RoomIO) SetParticipant(identity string) {
	rio.mu.Lock()
	prevIdentity := rio.participantIdentity
	rio.participantIdentity = identity
	rio.mu.Unlock()

	if prevIdentity != "" && prevIdentity != identity {
		rio.trackContextsMu.Lock()
		for id, cancel := range rio.trackContexts {
			cancel()
			delete(rio.trackContexts, id)
		}
		rio.trackContextsMu.Unlock()
	}
}

func (rio *RoomIO) UnsetParticipant() {
	rio.mu.Lock()
	prevIdentity := rio.participantIdentity
	rio.participantIdentity = ""
	rio.mu.Unlock()

	if prevIdentity != "" {
		rio.trackContextsMu.Lock()
		for id, cancel := range rio.trackContexts {
			cancel()
			delete(rio.trackContexts, id)
		}
		rio.trackContextsMu.Unlock()
	}
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
	rio.ctx = ctx

	audioOutputEnabled := true
	if rio.Options.AudioOutput != nil && !rio.Options.AudioOutput.Enabled {
		audioOutputEnabled = false
	}

	if !audioOutputEnabled {
		return nil
	}

	track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   48000, // Opus RTP clock rate is always 48000 per WebRTC spec
		Channels:    2,     // Opus SDP always advertises 2 channels, even for mono audio
		SDPFmtpLine: "minptime=10;useinbandfec=1",
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
			Name:   trackName,
			Source: livekit.TrackSource_MICROPHONE,
		}
	} else if pubOpts.Name == "" {
		pubOpts.Name = trackName
	}

	pub, err := rio.Room.LocalParticipant.PublishTrack(track, pubOpts)
	if err != nil {
		return err
	}

	// Store agent track SID for transcript attribution.
	if pub != nil {
		rio.AgentSession.SetAgentTrackSID(pub.SID())
		logger.Logger.Infow("🎙️ [RoomIO] Agent audio track SID", "sid", pub.SID())
	}

	rio.mu.Lock()
	rio.audioTrack = track
	rio.audioPub = pub
	rio.mu.Unlock()

	// Update transcription output with track ID for protocol alignment
	if rio.AgentSession != nil && rio.AgentSession.Output.Transcription != nil {
		if textOut, ok := rio.AgentSession.Output.Transcription.(interface{ SetTrackID(string) }); ok {
			textOut.SetTrackID(pub.SID())
		}
	}

	videoOutputEnabled := false
	if rio.Options.VideoOutput != nil && rio.Options.VideoOutput.Enabled {
		videoOutputEnabled = true
	}

	if videoOutputEnabled {
		vtrack, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeVP8,
			ClockRate: 90000,
		})
		if err != nil {
			return err
		}

		_, err = rio.Room.LocalParticipant.PublishTrack(vtrack, &lksdk.TrackPublicationOptions{
			Name: "agent-video",
		})
		if err != nil {
			return err
		}
		rio.videoTrack = vtrack
	}

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

	// Store human participant info for transcript attribution.
	rio.AgentSession.SetRemoteUserIdentity(rp.Identity())
	rio.AgentSession.SetRemoteTrackSID(publication.SID())

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
	logger.Logger.Infow("[STT-PIPE] handleAudioTrack started", "trackID", track.ID(), "codec", track.Codec().MimeType, "clockRate", track.Codec().ClockRate)
	// First, check for and flush any pre-connect audio buffered
	preCtx, preCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer preCancel()

	if frames := rio.preConnectAudio.WaitForData(preCtx, track.ID()); len(frames) > 0 {
		logger.Logger.Infow("[STT-PIPE] flushing pre-connect audio", "frames", len(frames))
		for _, frame := range frames {
			rio.audioInCh <- frame
		}
	}

	logger.Logger.Infow("handleAudioTrack started", "trackID", track.ID(), "codec", track.Codec().MimeType, "sampleRate", track.Codec().ClockRate)

	// Create Opus decoder for this track
	decoder, err := newOpusDecoder(int(track.Codec().ClockRate), 1)
	if err != nil {
		logger.Logger.Errorw("Failed to create Opus decoder", err)
		return
	}
	defer decoder.Close()
	logger.Logger.Debugw("Opus decoder created")

	logger.Logger.Debugw("Starting RTP read loop")

	sb := samplebuilder.New(20, &codecs.OpusPacket{}, track.Codec().ClockRate)
	var frameCount int

	var rtpCount int
	var sampleCount int
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		rio.mu.Lock()
		closed := rio.closed
		rio.mu.Unlock()
		if closed {
			return
		}

		pkt, _, err := track.ReadRTP()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.Logger.Errorw("ReadRTP error", err)
			}
			return
		}

		rtpCount++
		if rtpCount == 1 || rtpCount%1000 == 0 {
			logger.Logger.Debugw("RTP packets read", "count", rtpCount, "payload_len", len(pkt.Payload))
		}

		sb.Push(pkt)
		for {
			sample := sb.Pop()
			if sample == nil {
				break
			}

			sampleCount++
			rawSize := len(sample.Data)
			pcm := sample.Data
			if decoded, err := decoder.Decode(sample.Data); err == nil {
				pcm = decoded
			} else if sampleCount <= 3 {
				logger.Logger.Warnw("Opus decode error", err)
			}

			if sampleCount <= 5 || sampleCount%1000 == 0 {
				logger.Logger.Debugw("Audio sample processed", "count", sampleCount, "raw_len", rawSize, "pcm_len", len(pcm))
			}

			frame := &model.AudioFrame{
				Data:              pcm,
				SampleRate:        track.Codec().ClockRate,
				NumChannels:       1,
				SamplesPerChannel: uint32(len(pcm) / 2),
			}

			frameCount++
			if frameCount%100 == 1 {
				logger.Logger.Infow("[STT-PIPE] handleAudioTrack sending frames", "frameCount", frameCount, "dataLen", len(pcm))
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

	// Cancel the RoomIO-level context so playoutLoop and other goroutines exit.
	if rio.cancel != nil {
		rio.cancel()
	}

	rio.trackContextsMu.Lock()
	for _, cancel := range rio.trackContexts {
		cancel()
	}
	rio.trackContexts = make(map[string]context.CancelFunc)
	rio.trackContextsMu.Unlock()

	// Close the audio input channel so RecorderAudioInput.loop() can exit.
	// Track contexts are already cancelled above, so senders (handleAudioTrack)
	// have stopped before we close the channel.
	close(rio.audioInCh)

	if rio.decoder != nil {
		rio.decoder.Close()
	}
	if rio.encoder != nil {
		rio.encoder.Close()
	}
	if rio.sync != nil {
		rio.sync.Close()
	}

	if rio.Recorder != nil {
		rio.Recorder.Stop()
	}

	if rio.preConnectAudio != nil {
		rio.preConnectAudio.Close()
	}

	if rio.AgentSession != nil && rio.AgentSession.Output.Transcription != nil {
		if closer, ok := rio.AgentSession.Output.Transcription.(io.Closer); ok {
			closer.Close()
		}
	}

	// Release references to help GC.
	rio.AgentSession = nil
	rio.onPlaybackStarted = nil
	rio.onPlaybackFinished = nil

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

func (rio *RoomIO) registerRoomCallbacks() {
	if rio.roomCallback == nil {
		return
	}

	rio.roomCallback.OnMetadataChanged = func(_ string, participant lksdk.Participant) {
		// Check for "lk.active" or "active" in metadata JSON
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(participant.Metadata()), &m); err == nil {
			active, _ := m["lk.active"].(bool)
			if !active {
				active, _ = m["active"].(bool)
			}

			if rio.AgentSession != nil && rio.AgentSession.Timeline != nil {
				rio.AgentSession.Timeline.AddEvent(&agent.ParticipantActiveEvent{
					ParticipantID: participant.SID(),
					Identity:      participant.Identity(),
					Active:        active,
					CreatedAt:     time.Now(),
				})
			}
		}
	}

	rio.roomCallback.OnParticipantConnected = func(participant *lksdk.RemoteParticipant) {
		if rio.AgentSession != nil && rio.AgentSession.Timeline != nil {
			rio.AgentSession.Timeline.AddEvent(&agent.ParticipantActiveEvent{
				ParticipantID: participant.SID(),
				Identity:      participant.Identity(),
				Active:        true,
				CreatedAt:     time.Now(),
			})
		}
	}
}
