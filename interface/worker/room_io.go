package worker

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/hraban/opus"
	"github.com/livekit/protocol/livekit"
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
	AudioTrackName           string
	PreConnectAudioTimeout   time.Duration
	DisablePreConnectAudio   bool
	DisableTextInput         bool
	DisableCloseOnDisconnect bool
	TextInputCallback        TextInputCallback
	ParticipantIdentity      string
	ParticipantKinds         []lksdk.ParticipantKind
}

const RoomIOChatTopic = "lk.chat"
const RoomIOPublishOnBehalfAttribute = "lk.publish_on_behalf"

type TextInputEvent struct {
	Text                string
	Info                lksdk.TextStreamInfo
	ParticipantIdentity string
}

type TextInputCallback func(context.Context, *agent.AgentSession, TextInputEvent) error

type roomIOTextResponder interface {
	Interrupt(force bool) error
	GenerateReply(ctx context.Context, userInput string) (*agent.SpeechHandle, error)
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
	textInput       TextInputCallback

	participantAvailable bool
}

func NewRoomIO(room *lksdk.Room, session *agent.AgentSession, opts RoomOptions) *RoomIO {
	dec, _ := newOpusDecoder(48000, 1)
	enc, _ := newOpusEncoder(48000, 1)

	var preConnectAudio *PreConnectAudioHandler
	if !opts.DisablePreConnectAudio {
		preConnectAudio = NewPreConnectAudioHandler(room, roomIOPreConnectAudioTimeout(opts))
		preConnectAudio.Register()
	}

	rio := &RoomIO{
		Room:            room,
		AgentSession:    session,
		Options:         opts,
		decoder:         dec,
		encoder:         enc,
		Recorder:        NewRecorderIO(session),
		preConnectAudio: preConnectAudio,
		textInput:       roomIOTextInputCallback(opts),
	}

	if !opts.DisableTextInput {
		rio.registerTextInput()
	}

	if session.Assistant == nil {
		session.Assistant = agent.NewPipelineAgent(session.VAD, session.STT, session.LLM, session.TTS, session.ChatCtx)
	}
	session.Assistant.PublishAudio = rio.PublishAudio

	return rio
}

func roomIOPreConnectAudioTimeout(opts RoomOptions) time.Duration {
	if opts.PreConnectAudioTimeout > 0 {
		return opts.PreConnectAudioTimeout
	}
	return 3 * time.Second
}

func roomIOTextInputCallback(opts RoomOptions) TextInputCallback {
	if opts.TextInputCallback != nil {
		return opts.TextInputCallback
	}
	return func(ctx context.Context, session *agent.AgentSession, ev TextInputEvent) error {
		return roomIODefaultTextInput(ctx, session, ev.Text)
	}
}

func roomIODefaultTextInput(ctx context.Context, responder roomIOTextResponder, text string) error {
	if err := responder.Interrupt(false); err != nil {
		return err
	}
	_, err := responder.GenerateReply(ctx, text)
	return err
}

func (rio *RoomIO) SetParticipant(participantIdentity string) {
	rio.setParticipant(participantIdentity, false)
}

func (rio *RoomIO) setParticipant(participantIdentity string, available bool) {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	rio.Options.ParticipantIdentity = participantIdentity
	rio.participantAvailable = available
}

func (rio *RoomIO) UnsetParticipant() {
	rio.SetParticipant("")
}

func (rio *RoomIO) registerTextInput() {
	if rio.Room == nil {
		return
	}
	defer func() {
		if recover() != nil {
			logger.Logger.Warnw("failed to register room text input handler", nil)
		}
	}()
	_ = rio.Room.RegisterTextStreamHandler(RoomIOChatTopic, rio.onChatTextStream)
}

func (rio *RoomIO) GetCallback() *lksdk.RoomCallback {
	cb := lksdk.NewRoomCallback()
	cb.OnParticipantConnected = rio.onParticipantConnected
	cb.OnTrackSubscribed = rio.onTrackSubscribed
	cb.OnParticipantDisconnected = rio.onParticipantDisconnected
	cb.OnDataPacket = rio.onDataPacket
	return cb
}

func (rio *RoomIO) onDataPacket(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
	if rio == nil || rio.AgentSession == nil {
		return
	}
	dtmf, ok := data.(*livekit.SipDTMF)
	if !ok {
		return
	}
	rio.AgentSession.EmitSipDTMF(agent.SipDTMFEvent{
		Digit:          dtmf.Digit,
		Code:           dtmf.Code,
		SenderIdentity: params.SenderIdentity,
	})
}

func (rio *RoomIO) onChatTextStream(reader *lksdk.TextStreamReader, participantIdentity string) {
	if rio == nil || rio.AgentSession == nil || rio.textInput == nil {
		return
	}
	go func() {
		rio.handleChatTextInput(context.Background(), reader.ReadAll(), reader.Info, participantIdentity)
	}()
}

func (rio *RoomIO) handleChatTextInput(ctx context.Context, text string, info lksdk.TextStreamInfo, participantIdentity string) {
	if rio == nil || rio.AgentSession == nil || rio.textInput == nil {
		return
	}
	if !rio.shouldHandleParticipant(participantIdentity) {
		return
	}
	if rio.Room != nil && participantIdentity != "" && rio.Room.GetParticipantByIdentity(participantIdentity) == nil {
		return
	}
	_ = rio.textInput(ctx, rio.AgentSession, TextInputEvent{
		Text:                text,
		Info:                info,
		ParticipantIdentity: participantIdentity,
	})
}

func (rio *RoomIO) participantIdentity() string {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	return rio.Options.ParticipantIdentity
}

func (rio *RoomIO) participantState() (string, bool) {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	return rio.Options.ParticipantIdentity, rio.participantAvailable
}

func (rio *RoomIO) shouldHandleParticipant(participantIdentity string) bool {
	linkedParticipant := rio.participantIdentity()
	return linkedParticipant == "" || participantIdentity == linkedParticipant
}

func (rio *RoomIO) shouldAcceptParticipant(identity string, kind lksdk.ParticipantKind, attributes map[string]string, localIdentity string) bool {
	if !rio.shouldHandleParticipant(identity) {
		return false
	}
	if rio.participantIdentity() == "" && localIdentity != "" && attributes[RoomIOPublishOnBehalfAttribute] == localIdentity {
		return false
	}
	return participantKindAllowed(kind, rio.participantKinds())
}

func (rio *RoomIO) handleParticipantConnected(identity string, kind lksdk.ParticipantKind, attributes map[string]string, localIdentity string) bool {
	if rio == nil {
		return false
	}
	linkedParticipant, available := rio.participantState()
	if linkedParticipant != "" && (identity != linkedParticipant || available) {
		return false
	}
	if !rio.shouldAcceptParticipant(identity, kind, attributes, localIdentity) {
		return false
	}
	rio.setParticipant(identity, true)
	return true
}

func (rio *RoomIO) participantKinds() []lksdk.ParticipantKind {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	return append([]lksdk.ParticipantKind(nil), rio.Options.ParticipantKinds...)
}

func participantKindAllowed(kind lksdk.ParticipantKind, allowed []lksdk.ParticipantKind) bool {
	if len(allowed) == 0 {
		allowed = []lksdk.ParticipantKind{
			lksdk.ParticipantConnector,
			lksdk.ParticipantSIP,
			lksdk.ParticipantStandard,
		}
	}
	for _, accepted := range allowed {
		if kind == accepted {
			return true
		}
	}
	return false
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

	_, err = rio.Room.LocalParticipant.PublishTrack(track, rio.audioTrackPublicationOptions())
	if err != nil {
		return err
	}

	rio.audioTrack = track
	return nil
}

func (rio *RoomIO) audioTrackPublicationOptions() *lksdk.TrackPublicationOptions {
	name := rio.Options.AudioTrackName
	if name == "" {
		name = "roomio_audio"
	}
	return &lksdk.TrackPublicationOptions{
		Name:   name,
		Source: livekit.TrackSource_MICROPHONE,
	}
}

func (rio *RoomIO) onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if rp != nil && !rio.shouldAcceptParticipant(rp.Identity(), rp.Kind(), rp.Attributes(), rio.localParticipantIdentity()) {
		return
	}
	if track.Kind() == webrtc.RTPCodecTypeAudio {
		go rio.handleAudioTrack(track)
	}
}

func (rio *RoomIO) localParticipantIdentity() string {
	if rio.Room == nil || rio.Room.LocalParticipant == nil {
		return ""
	}
	return rio.Room.LocalParticipant.Identity()
}

func (rio *RoomIO) onParticipantConnected(participant *lksdk.RemoteParticipant) {
	if participant == nil {
		return
	}
	rio.handleParticipantConnected(
		participant.Identity(),
		participant.Kind(),
		participant.Attributes(),
		rio.localParticipantIdentity(),
	)
}

func (rio *RoomIO) onParticipantDisconnected(participant *lksdk.RemoteParticipant) {
	if participant == nil {
		return
	}
	rio.handleParticipantDisconnected(participant.Identity(), participant.DisconnectReason())
}

func (rio *RoomIO) handleParticipantDisconnected(participantIdentity string, reason livekit.DisconnectReason) {
	if rio == nil {
		return
	}
	linkedParticipant, available := rio.participantState()
	if linkedParticipant == "" || participantIdentity != linkedParticipant || !available {
		return
	}
	rio.setParticipant(linkedParticipant, false)
	if rio.AgentSession == nil || rio.Options.DisableCloseOnDisconnect {
		return
	}
	if !roomIOCloseOnDisconnectReason(reason) {
		return
	}
	rio.AgentSession.CloseSoon(agent.CloseReasonParticipantDisconnected)
}

func roomIOCloseOnDisconnectReason(reason livekit.DisconnectReason) bool {
	switch reason {
	case livekit.DisconnectReason_CLIENT_INITIATED,
		livekit.DisconnectReason_ROOM_DELETED,
		livekit.DisconnectReason_USER_REJECTED:
		return true
	default:
		return false
	}
}

func (rio *RoomIO) handleAudioTrack(track *webrtc.TrackRemote) {
	// First, check for and flush any pre-connect audio buffered
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if rio.preConnectAudio != nil {
		if frames := rio.preConnectAudio.WaitForData(ctx, track.ID()); len(frames) > 0 {
			for _, frame := range frames {
				if rio.Recorder != nil {
					rio.Recorder.RecordInput(frame)
				}
				rio.AgentSession.OnAudioFrame(context.Background(), frame)
			}
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
			rio.AgentSession.OnAudioFrame(context.Background(), frame)
		}
	}
}

func (rio *RoomIO) PublishAudio(frame *model.AudioFrame) error {
	if rio.Recorder != nil {
		rio.Recorder.RecordOutput(frame)
	}

	rio.mu.Lock()
	track := rio.audioTrack
	encoder := rio.encoder
	rio.mu.Unlock()

	if track == nil {
		return nil
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
	if rio.preConnectAudio != nil {
		rio.preConnectAudio.Close()
	}
	if rio.Recorder != nil {
		if err := rio.Recorder.Stop(); err != nil {
			return err
		}
	}
	if rio.Room != nil && !rio.Options.DisableTextInput {
		rio.Room.UnregisterTextStreamHandler(RoomIOChatTopic)
	}
	return nil
}
