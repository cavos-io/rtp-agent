package worker

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/logger"
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
	DeleteRoomOnClose        bool
	DeleteRoom               func(context.Context, string) error
	TextInputCallback        TextInputCallback
	ParticipantIdentity      string
	ParticipantKinds         []lksdk.ParticipantKind
}

const RoomIOChatTopic = "lk.chat"
const RoomIOPublishOnBehalfAttribute = "lk.publish_on_behalf"
const RoomIOAgentStateAttribute = "lk.agent.state"
const RoomIOSimulatorAttribute = "lk.simulator"

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

	audioTrack    *lksdk.LocalTrack
	decoder       AudioDecoder
	encoder       AudioEncoder
	audioDisabled bool

	preConnectAudio *PreConnectAudioHandler
	textInput       TextInputCallback

	participantAvailable  bool
	connectedParticipants map[string]struct{}

	agentStateCancel         context.CancelFunc
	agentStatePublisher      func(map[string]string)
	agentStatePublishEnabled func() bool

	sessionCloseCancel context.CancelFunc
	deletingRoom       bool
	roomName           func() string
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
	rio.agentStatePublisher = rio.publishLocalParticipantAttributes
	rio.agentStatePublishEnabled = rio.roomConnected
	rio.roomName = rio.liveKitRoomName
	rio.startAgentStateListener()
	rio.startSessionCloseListener()

	if !opts.DisableTextInput {
		rio.registerTextInput()
	}

	if session.Assistant == nil {
		session.Assistant = agent.NewPipelineAgent(session.VAD, session.STT, session.LLM, session.TTS, session.ChatCtx)
	}
	session.Assistant.PublishAudio = rio.PublishAudio

	return rio
}

func (rio *RoomIO) startAgentStateListener() {
	if rio == nil || rio.AgentSession == nil || rio.AgentSession.AgentStateChangedCh == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	rio.agentStateCancel = cancel
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-rio.AgentSession.AgentStateChangedCh:
				if !ok {
					return
				}
				rio.handleAgentStateChanged(ev)
			}
		}
	}()
}

func (rio *RoomIO) startSessionCloseListener() {
	if rio == nil || rio.AgentSession == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	rio.sessionCloseCancel = cancel
	closeEvents := rio.AgentSession.CloseEvents()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-closeEvents:
				if !ok {
					return
				}
				rio.handleAgentSessionClose(ev)
			}
		}
	}()
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

func (rio *RoomIO) handleAgentStateChanged(ev agent.AgentStateChangedEvent) {
	if rio == nil || rio.agentStatePublisher == nil {
		return
	}
	if rio.agentStatePublishEnabled != nil && !rio.agentStatePublishEnabled() {
		return
	}
	rio.agentStatePublisher(map[string]string{
		RoomIOAgentStateAttribute: string(ev.NewState),
	})
}

func (rio *RoomIO) roomConnected() bool {
	return rio != nil && rio.Room != nil && rio.Room.ConnectionState() == lksdk.ConnectionStateConnected
}

func (rio *RoomIO) publishLocalParticipantAttributes(attrs map[string]string) {
	if rio == nil || rio.Room == nil || rio.Room.LocalParticipant == nil {
		return
	}
	rio.Room.LocalParticipant.SetAttributes(attrs)
}

func (rio *RoomIO) handleAgentSessionClose(ev agent.CloseEvent) {
	if rio == nil || !rio.Options.DeleteRoomOnClose || rio.Options.DeleteRoom == nil {
		return
	}
	rio.mu.Lock()
	if rio.deletingRoom {
		rio.mu.Unlock()
		return
	}
	rio.deletingRoom = true
	rio.mu.Unlock()
	defer func() {
		rio.mu.Lock()
		rio.deletingRoom = false
		rio.mu.Unlock()
	}()

	roomName := ""
	if rio.roomName != nil {
		roomName = rio.roomName()
	}
	if err := rio.Options.DeleteRoom(context.Background(), roomName); err != nil {
		logger.Logger.Warnw("failed to delete room on agent session close", err, "room", roomName, "reason", ev.Reason)
	}
}

func (rio *RoomIO) liveKitRoomName() string {
	if rio == nil || rio.Room == nil {
		return ""
	}
	return rio.Room.Name()
}

func (rio *RoomIO) isDeletingRoom() bool {
	if rio == nil {
		return false
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	return rio.deletingRoom
}

func (rio *RoomIO) isAudioDisabled() bool {
	if rio == nil {
		return false
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	return rio.audioDisabled
}

func (rio *RoomIO) disableAudioIOForSimulator() {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	if rio.audioDisabled {
		rio.mu.Unlock()
		return
	}
	rio.audioDisabled = true
	preConnectAudio := rio.preConnectAudio
	rio.preConnectAudio = nil
	rio.audioTrack = nil
	rio.mu.Unlock()

	if preConnectAudio != nil {
		preConnectAudio.Close()
	}
}

func (rio *RoomIO) SetParticipant(participantIdentity string) {
	currentParticipant, available := rio.participantState()
	rio.setParticipant(participantIdentity, (currentParticipant == participantIdentity && available) || rio.isParticipantConnected(participantIdentity))
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
	rio.recordConnectedParticipant(identity)
	linkedParticipant, available := rio.participantState()
	if linkedParticipant != "" && (identity != linkedParticipant || available) {
		return false
	}
	if !rio.shouldAcceptParticipant(identity, kind, attributes, localIdentity) {
		return false
	}
	rio.setParticipant(identity, true)
	if attributes[RoomIOSimulatorAttribute] == "true" {
		rio.disableAudioIOForSimulator()
	}
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
	if rio.isAudioDisabled() {
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
	rio.forgetConnectedParticipant(participantIdentity)
	linkedParticipant, available := rio.participantState()
	if linkedParticipant == "" || participantIdentity != linkedParticipant || !available {
		return
	}
	rio.setParticipant(linkedParticipant, false)
	if rio.AgentSession == nil || rio.Options.DisableCloseOnDisconnect {
		return
	}
	if rio.isDeletingRoom() {
		return
	}
	if !roomIOCloseOnDisconnectReason(reason) {
		return
	}
	rio.AgentSession.CloseSoon(agent.CloseReasonParticipantDisconnected)
}

func (rio *RoomIO) recordConnectedParticipant(identity string) {
	if rio == nil || identity == "" {
		return
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if rio.connectedParticipants == nil {
		rio.connectedParticipants = make(map[string]struct{})
	}
	rio.connectedParticipants[identity] = struct{}{}
}

func (rio *RoomIO) forgetConnectedParticipant(identity string) {
	if rio == nil || identity == "" {
		return
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	delete(rio.connectedParticipants, identity)
}

func (rio *RoomIO) isParticipantConnected(identity string) bool {
	if rio == nil || identity == "" {
		return false
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	_, ok := rio.connectedParticipants[identity]
	return ok
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
	if rio.isAudioDisabled() {
		return
	}
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
		if rio.audioDisabled {
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
	if rio.isAudioDisabled() {
		return nil
	}
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
	if rio.agentStateCancel != nil {
		rio.agentStateCancel()
	}
	if rio.sessionCloseCancel != nil {
		rio.sessionCloseCancel()
	}
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
