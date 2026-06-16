package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/google/uuid"
	"github.com/hraban/opus"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/server-sdk-go/v2/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/twitchtv/twirp"
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
	AudioTrackName             string
	AudioSubscriptionTimeout   time.Duration
	PreConnectAudioTimeout     time.Duration
	DisablePreConnectAudio     bool
	DisableAudioInput          bool
	DisableTextInput           bool
	DisableAudioOutput         bool
	DisableTranscriptionOutput bool
	DisableCloseOnDisconnect   bool
	DeleteRoomOnClose          bool
	DeleteRoom                 func(context.Context, string) error
	TextInputCallback          TextInputCallback
	ParticipantIdentity        string
	ParticipantKinds           []lksdk.ParticipantKind
}

const RoomIOChatTopic = "lk.chat"
const RoomIOTranscriptionTopic = "lk.transcription"
const RoomIOPublishOnBehalfAttribute = "lk.publish_on_behalf"
const RoomIOAgentStateAttribute = "lk.agent.state"
const RoomIOSimulatorAttribute = "lk.simulator"
const RoomIOTranscriptionFinalAttribute = "lk.transcription_final"
const RoomIOTranscriptionTrackIDAttribute = "lk.transcribed_track_id"
const RoomIOTranscriptionSegmentIDAttribute = "lk.segment_id"
const roomIODeleteRoomCloseTimeout = 10 * time.Second
const roomIOOpusClockRate uint32 = 48000
const roomIOOpusFrameSamples uint32 = 960
const roomIOInputSampleRate uint32 = 24000
const roomIOAudioSubscriptionTimeout = 3 * time.Second
const roomIOInputSilenceFlushDuration = 500 * time.Millisecond

func roomIOAudioOutputCodec() webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: roomIOOpusClockRate,
		Channels:  2,
	}
}

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

type roomIOTextTurnClaimer interface {
	ClaimUserTurn(ctx context.Context, fn func(context.Context) error) error
}

type PlaybackStartedEvent struct {
	CreatedAt time.Time
}

type PlaybackFinishedEvent struct {
	PlaybackPosition       time.Duration
	Interrupted            bool
	SynchronizedTranscript string
	AudioFrames            int
	AudioBytes             int
	AudioEncodedFrames     int
	AudioSampleRate        uint32
	AudioChannels          uint32
	AudioLastError         string
}

type RoomIOAudioOutputDiagnostics struct {
	TrackID                     string
	TrackPublished              bool
	TrackSubscribed             bool
	FramesReceived              int
	FramesPublished             int
	BytesReceived               int
	BytesPublished              int
	EncodedFramesPublished      int
	LastInputSampleRate         uint32
	LastInputSamplesPerChannel  uint32
	LastInputChannels           uint32
	LastPublishedSampleRate     uint32
	LastPublishedSamplesPerChan uint32
	LastPublishedChannels       uint32
	LastFrameAt                 time.Time
	LastPublishedAt             time.Time
	LastError                   string
	LastErrorAt                 time.Time
}

type roomIOClientEvents interface {
	DispatchAgentState(agent.AgentState)
	DispatchUserState(agent.UserState)
}

type RoomIO struct {
	Room         *lksdk.Room
	AgentSession *agent.AgentSession
	Options      RoomOptions
	Recorder     *RecorderIO

	mu     sync.Mutex
	closed bool

	audioTrack    *lksdk.LocalTrack
	audioTrackID  string
	decoder       AudioDecoder
	encoder       AudioEncoder
	audioDisabled bool

	audioPublication *lksdk.LocalTrackPublication
	audioSubscribed  chan struct{}
	audioSubOnce     sync.Once

	playbackCapturing         bool
	playbackSegmentsCount     int
	playbackFinishedCount     int
	playbackPosition          time.Duration
	playbackAudioFrames       int
	playbackAudioBytes        int
	playbackAudioEncoded      int
	playbackAudioSampleRate   uint32
	playbackAudioChannels     uint32
	playbackAudioLastError    string
	playbackTranscript        string
	pendingPlaybackTranscript string
	lastPlaybackEvent         PlaybackFinishedEvent
	playbackWaiters           []chan struct{}
	playbackStartedHandlers   []func(PlaybackStartedEvent)
	playbackFinishedHandlers  []func(PlaybackFinishedEvent)

	audioOutputPaused  bool
	audioOutputWaiters []chan audioOutputWaitResult

	audioOutputDiagnostics RoomIOAudioOutputDiagnostics

	preConnectAudio *PreConnectAudioHandler
	textInput       TextInputCallback

	participantAvailable  bool
	connectedParticipants map[string]struct{}

	userTranscriptionCancel        context.CancelFunc
	userTranscriptionTrackID       string
	userTranscriptionParticipantID string
	userTranscriptionSegmentID     string

	agentStateCancel         context.CancelFunc
	agentStatePublisher      func(map[string]string)
	agentStatePublishEnabled func() bool
	userStateCancel          context.CancelFunc
	clientEvents             roomIOClientEvents

	agentTranscriptionCancel         context.CancelFunc
	agentTranscriptionSegmentID      string
	agentTranscriptionText           string
	transcriptionTextPublisher       func(string, lksdk.StreamTextOptions)
	transcriptionPacketPublisher     func(*livekit.Transcription) error
	transcriptionParticipantIdentity func() string

	sessionCloseCancel context.CancelFunc
	deletingRoom       bool
	deleteRoomDone     chan struct{}
	roomName           func() string
}

type audioOutputWaitResult struct {
	drop bool
}

func NewRoomIO(room *lksdk.Room, session *agent.AgentSession, opts RoomOptions) *RoomIO {
	dec, _ := newOpusDecoder(48000, 1)
	enc, _ := newOpusEncoder(48000, 1)

	rio := &RoomIO{
		AgentSession: session,
		Options:      opts,
		decoder:      dec,
		encoder:      enc,
		Recorder:     NewRecorderIO(session),
		textInput:    roomIOTextInputCallback(opts),
	}
	rio.transcriptionTextPublisher = rio.publishTranscriptionText
	rio.transcriptionPacketPublisher = rio.publishTranscriptionPacket
	rio.transcriptionParticipantIdentity = rio.localParticipantIdentity
	rio.roomName = rio.liveKitRoomName
	rio.agentStatePublisher = rio.publishLocalParticipantAttributes
	rio.agentStatePublishEnabled = rio.roomConnected
	rio.startAgentStateListener()
	rio.startUserStateListener()
	rio.startUserTranscriptionListener()
	rio.startAgentTranscriptionListener()
	rio.startSessionCloseListener()

	if !opts.DisableAudioOutput {
		session.SetAudioOutputController(rio)
		session.SetAudioPlaybackController(roomIOPlaybackController{rio: rio})
		session.EnsureAssistant().SetPublishAudio(rio.PublishAudio)
	}

	rio.AttachRoom(room)
	return rio
}

type roomIOPlaybackController struct {
	rio *RoomIO
}

func (c roomIOPlaybackController) ClearBuffer() {
	if c.rio != nil {
		c.rio.ClearBuffer()
	}
}

func (c roomIOPlaybackController) WaitForPlayout(ctx context.Context) (agent.AudioPlaybackResult, error) {
	if c.rio == nil {
		return agent.AudioPlaybackResult{}, nil
	}
	ev, err := c.rio.WaitForPlayout(ctx)
	return agent.AudioPlaybackResult{
		PlaybackPosition:       ev.PlaybackPosition,
		Interrupted:            ev.Interrupted,
		SynchronizedTranscript: ev.SynchronizedTranscript,
	}, err
}

func (rio *RoomIO) AttachRoom(room *lksdk.Room) {
	if rio == nil || room == nil {
		return
	}
	rio.Room = room
	rio.clientEvents = agent.NewClientEventsDispatcher(room)
	if !rio.Options.DisableAudioInput && !rio.Options.DisablePreConnectAudio && rio.preConnectAudio == nil {
		rio.preConnectAudio = NewPreConnectAudioHandler(room, roomIOPreConnectAudioTimeout(rio.Options))
		rio.preConnectAudio.Register()
	}
	if !rio.Options.DisableTextInput {
		rio.registerTextInput()
	}
	for _, participant := range room.GetRemoteParticipants() {
		rio.onParticipantConnected(participant)
	}
}

func (rio *RoomIO) startAgentStateListener() {
	if rio == nil || rio.AgentSession == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	rio.agentStateCancel = cancel
	events := rio.AgentSession.AgentStateChangedEvents()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				rio.handleAgentStateChanged(ev)
			}
		}
	}()
}

func (rio *RoomIO) startUserStateListener() {
	if rio == nil || rio.AgentSession == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	rio.userStateCancel = cancel
	events := rio.AgentSession.UserStateChangedEvents()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				rio.handleUserStateChanged(ev)
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

func (rio *RoomIO) startAgentTranscriptionListener() {
	if rio == nil || rio.AgentSession == nil || rio.Options.DisableTranscriptionOutput {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	rio.agentTranscriptionCancel = cancel
	events := rio.AgentSession.AgentOutputTranscribedEvents()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				rio.handleAgentOutputTranscribed(ev)
			}
		}
	}()
}

func (rio *RoomIO) startUserTranscriptionListener() {
	if rio == nil || rio.AgentSession == nil || rio.Options.DisableTranscriptionOutput {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	rio.userTranscriptionCancel = cancel
	events := rio.AgentSession.UserInputTranscribedEvents()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				rio.handleUserInputTranscribed(ev)
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
	run := func(ctx context.Context) error {
		if err := responder.Interrupt(false); err != nil {
			return err
		}
		_, err := responder.GenerateReply(ctx, text)
		return err
	}
	if claimer, ok := responder.(roomIOTextTurnClaimer); ok {
		return claimer.ClaimUserTurn(ctx, run)
	}
	return run(ctx)
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
	if rio.clientEvents != nil {
		rio.clientEvents.DispatchAgentState(ev.NewState)
	}
}

func (rio *RoomIO) handleUserStateChanged(ev agent.UserStateChangedEvent) {
	if rio == nil || rio.clientEvents == nil {
		return
	}
	if rio.agentStatePublishEnabled != nil && !rio.agentStatePublishEnabled() {
		return
	}
	rio.clientEvents.DispatchUserState(ev.NewState)
}

func (rio *RoomIO) handleAgentOutputTranscribed(ev agent.AgentOutputTranscribedEvent) {
	if rio == nil || ev.Transcript == "" {
		return
	}
	segmentID, transcript := rio.agentOutputTranscriptionState(ev.Transcript, ev.IsFinal)
	rio.setPlaybackTranscript(transcript, ev.IsFinal)
	attributes := map[string]string{
		RoomIOTranscriptionFinalAttribute:     strconv.FormatBool(ev.IsFinal),
		RoomIOTranscriptionSegmentIDAttribute: segmentID,
	}
	if trackID := rio.transcriptionTrackID(); trackID != "" {
		attributes[RoomIOTranscriptionTrackIDAttribute] = trackID
	}
	legacyEv := ev
	legacyEv.Transcript = transcript
	rio.publishLegacyAgentTranscription(legacyEv, segmentID)
	if rio.transcriptionTextPublisher == nil {
		return
	}
	rio.transcriptionTextPublisher(transcript, lksdk.StreamTextOptions{
		Topic:      RoomIOTranscriptionTopic,
		Attributes: attributes,
	})
}

func (rio *RoomIO) agentOutputTranscriptionState(transcript string, final bool) (string, string) {
	if rio == nil {
		return roomIOTranscriptionSegmentID(), transcript
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if rio.agentTranscriptionSegmentID == "" {
		rio.agentTranscriptionSegmentID = roomIOTranscriptionSegmentID()
	}
	segmentID := rio.agentTranscriptionSegmentID
	publishText := transcript
	if !final {
		rio.agentTranscriptionText += transcript
		publishText = rio.agentTranscriptionText
	}
	if final {
		rio.agentTranscriptionSegmentID = ""
		rio.agentTranscriptionText = ""
	}
	return segmentID, publishText
}

func (rio *RoomIO) userInputTranscriptionState(final bool) string {
	if rio == nil {
		return roomIOTranscriptionSegmentID()
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if rio.userTranscriptionSegmentID == "" {
		rio.userTranscriptionSegmentID = roomIOTranscriptionSegmentID()
	}
	segmentID := rio.userTranscriptionSegmentID
	if final {
		rio.userTranscriptionSegmentID = ""
	}
	return segmentID
}

func (rio *RoomIO) handleUserInputTranscribed(ev agent.UserInputTranscribedEvent) {
	if rio == nil || ev.Transcript == "" {
		return
	}
	trackID, participantID := rio.userTranscriptionTarget()
	if trackID == "" || participantID == "" {
		return
	}
	segmentID := rio.userInputTranscriptionState(ev.IsFinal)
	rio.publishTranscriptionPacketWithSegment(participantID, trackID, &livekit.TranscriptionSegment{
		Id:       segmentID,
		Text:     ev.Transcript,
		Final:    ev.IsFinal,
		Language: ev.Language,
	})
	rio.publishTranscriptionTextStream(ev.Transcript, trackID, ev.IsFinal, segmentID)
}

func (rio *RoomIO) publishLegacyAgentTranscription(ev agent.AgentOutputTranscribedEvent, segmentID string) {
	if rio == nil || rio.transcriptionPacketPublisher == nil {
		return
	}
	trackID := rio.transcriptionTrackID()
	participantIdentity := ""
	if rio.transcriptionParticipantIdentity != nil {
		participantIdentity = rio.transcriptionParticipantIdentity()
	}
	if trackID == "" || participantIdentity == "" {
		return
	}
	rio.publishTranscriptionPacketWithSegment(participantIdentity, trackID, &livekit.TranscriptionSegment{
		Id:       segmentID,
		Text:     ev.Transcript,
		Final:    ev.IsFinal,
		Language: ev.Language,
	})
}

func (rio *RoomIO) publishTranscriptionPacketWithSegment(participantIdentity string, trackID string, segment *livekit.TranscriptionSegment) {
	if rio == nil || rio.transcriptionPacketPublisher == nil || segment == nil {
		return
	}
	if err := rio.transcriptionPacketPublisher(&livekit.Transcription{
		TranscribedParticipantIdentity: participantIdentity,
		TrackId:                        trackID,
		Segments:                       []*livekit.TranscriptionSegment{segment},
	}); err != nil {
		logger.Logger.Warnw("failed to publish transcription packet", err)
	}
}

func (rio *RoomIO) transcriptionTrackID() string {
	if rio == nil {
		return ""
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	return rio.audioTrackID
}

func (rio *RoomIO) userTranscriptionTarget() (string, string) {
	if rio == nil {
		return "", ""
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	return rio.userTranscriptionTrackID, rio.userTranscriptionParticipantID
}

func (rio *RoomIO) setUserTranscriptionTarget(trackID string, participantID string) {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	rio.userTranscriptionTrackID = trackID
	rio.userTranscriptionParticipantID = participantID
}

func roomIOTranscriptionSegmentID() string {
	return "SG_" + uuid.NewString()[:12]
}

func (rio *RoomIO) publishTranscriptionTextStream(text string, trackID string, final bool, segmentID string) {
	if rio == nil || rio.transcriptionTextPublisher == nil || text == "" {
		return
	}
	attributes := map[string]string{
		RoomIOTranscriptionFinalAttribute:     strconv.FormatBool(final),
		RoomIOTranscriptionSegmentIDAttribute: segmentID,
	}
	if trackID != "" {
		attributes[RoomIOTranscriptionTrackIDAttribute] = trackID
	}
	rio.transcriptionTextPublisher(text, lksdk.StreamTextOptions{
		Topic:      RoomIOTranscriptionTopic,
		Attributes: attributes,
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

func (rio *RoomIO) publishTranscriptionText(text string, opts lksdk.StreamTextOptions) {
	if rio == nil || rio.Room == nil || rio.Room.LocalParticipant == nil {
		return
	}
	rio.Room.LocalParticipant.SendText(text, opts)
}

func (rio *RoomIO) publishTranscriptionPacket(transcription *livekit.Transcription) error {
	if rio == nil || rio.Room == nil || rio.Room.LocalParticipant == nil || transcription == nil {
		return nil
	}
	return rio.Room.LocalParticipant.PublishDataPacket(roomIOTranscriptionPacket{transcription: transcription})
}

type roomIOTranscriptionPacket struct {
	transcription *livekit.Transcription
}

func (p roomIOTranscriptionPacket) ToProto() *livekit.DataPacket {
	return &livekit.DataPacket{
		Value: &livekit.DataPacket_Transcription{Transcription: p.transcription},
	}
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
	done := make(chan struct{})
	rio.deletingRoom = true
	rio.deleteRoomDone = done
	rio.mu.Unlock()

	roomName := ""
	if rio.roomName != nil {
		roomName = rio.roomName()
	}
	deleteRoom := rio.Options.DeleteRoom
	reason := ev.Reason
	go func() {
		defer func() {
			rio.mu.Lock()
			rio.deletingRoom = false
			if rio.deleteRoomDone == done {
				rio.deleteRoomDone = nil
			}
			rio.mu.Unlock()
			close(done)
		}()
		if err := deleteRoom(context.Background(), roomName); err != nil && !roomDeleteNotFound(err) {
			logger.Logger.Warnw("failed to delete room on agent session close", err, "room", roomName, "reason", reason)
		}
	}()
}

func roomDeleteNotFound(err error) bool {
	if err == nil {
		return false
	}
	var twerr twirp.Error
	if errors.As(err, &twerr) && twerr.Code() == twirp.NotFound {
		return true
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "not_found") && strings.Contains(errText, "room")
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
	rio.audioTrackID = ""
	rio.userTranscriptionTrackID = ""
	rio.userTranscriptionParticipantID = ""
	rio.userTranscriptionSegmentID = ""
	rio.mu.Unlock()

	if preConnectAudio != nil {
		preConnectAudio.Close()
	}
	if rio.AgentSession != nil {
		rio.AgentSession.OnAudioEnabledChanged(false)
	}
}

func (rio *RoomIO) SetParticipant(participantIdentity string) {
	currentParticipant, available := rio.participantState()
	rio.setParticipant(participantIdentity, (currentParticipant == participantIdentity && available) || rio.isParticipantConnected(participantIdentity))
}

func (rio *RoomIO) setParticipant(participantIdentity string, available bool) {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if participantIdentity == "" || rio.userTranscriptionParticipantID != participantIdentity {
		rio.userTranscriptionTrackID = ""
		rio.userTranscriptionParticipantID = ""
		rio.userTranscriptionSegmentID = ""
	}
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
	cb.OnLocalTrackSubscribed = rio.onLocalTrackSubscribed
	cb.OnTrackSubscribed = rio.onTrackSubscribed
	cb.OnParticipantDisconnected = rio.onParticipantDisconnected
	cb.OnDataPacket = rio.onDataPacket
	return cb
}

func (rio *RoomIO) onLocalTrackSubscribed(publication *lksdk.LocalTrackPublication, _ *lksdk.LocalParticipant) {
	if rio == nil || publication == nil {
		return
	}
	rio.mu.Lock()
	expected := rio.audioPublication
	matches := expected == publication || (expected != nil && expected.SID() != "" && expected.SID() == publication.SID())
	rio.mu.Unlock()
	if matches {
		rio.markAudioSubscribed()
	}
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

func (rio *RoomIO) PublishDTMF(code int32, digit string) error {
	if rio == nil || rio.Room == nil || rio.Room.LocalParticipant == nil {
		return errors.New("room local participant not available")
	}
	return rio.Room.LocalParticipant.PublishDataPacket(&livekit.SipDTMF{
		Code:  uint32(code),
		Digit: digit,
	}, lksdk.WithDataPublishReliable(true))
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
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to handle chat text stream", nil, "panic", recovered)
		}
	}()
	if !rio.shouldHandleParticipant(participantIdentity) {
		return
	}
	if rio.Room != nil && participantIdentity != "" && rio.Room.GetParticipantByIdentity(participantIdentity) == nil {
		return
	}
	if err := rio.textInput(ctx, rio.AgentSession, TextInputEvent{
		Text:                text,
		Info:                info,
		ParticipantIdentity: participantIdentity,
	}); err != nil {
		logger.Logger.Warnw("failed to handle chat text stream", err)
	}
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

func (rio *RoomIO) LinkedParticipant() (string, bool) {
	if rio == nil {
		return "", false
	}
	return rio.participantState()
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
	if rio == nil || rio.Options.DisableAudioOutput {
		return nil
	}
	track, err := lksdk.NewLocalSampleTrack(roomIOAudioOutputCodec())
	if err != nil {
		return err
	}

	publication, err := rio.Room.LocalParticipant.PublishTrack(track, rio.audioTrackPublicationOptions())
	if err != nil {
		return err
	}

	trackID := ""
	if publication != nil {
		trackID = publication.SID()
	}
	subscribed := make(chan struct{})
	rio.audioSubOnce = sync.Once{}
	rio.mu.Lock()
	rio.audioTrack = track
	rio.audioTrackID = trackID
	rio.audioPublication = publication
	rio.audioSubscribed = subscribed
	rio.audioOutputDiagnostics.TrackID = trackID
	rio.audioOutputDiagnostics.TrackPublished = publication != nil
	rio.audioOutputDiagnostics.TrackSubscribed = false
	rio.mu.Unlock()
	return rio.waitForAudioSubscription(ctx)
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
	if rio.Options.DisableAudioInput || rio.isAudioDisabled() {
		return
	}
	if track.Kind() == webrtc.RTPCodecTypeAudio {
		trackID := ""
		if publication != nil {
			trackID = publication.SID()
		}
		if rp != nil {
			rio.setUserTranscriptionTarget(trackID, rp.Identity())
		}
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
	rio.clearUserTranscriptionTargetForParticipant(participantIdentity)
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

func (rio *RoomIO) clearUserTranscriptionTargetForParticipant(participantIdentity string) {
	if rio == nil || participantIdentity == "" {
		return
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if rio.userTranscriptionParticipantID != participantIdentity {
		return
	}
	rio.userTranscriptionTrackID = ""
	rio.userTranscriptionParticipantID = ""
	rio.userTranscriptionSegmentID = ""
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
	if rio.Options.DisableAudioInput || rio.isAudioDisabled() {
		return
	}
	// First, check for and flush any pre-connect audio buffered
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if rio.preConnectAudio != nil {
		if frames := rio.preConnectAudio.WaitForData(ctx, track.ID()); len(frames) > 0 {
			for _, frame := range frames {
				rio.forwardRoomInputFrame(context.Background(), roomIOInputFrameFromFrame(frame))
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
			if errors.Is(err, io.EOF) {
				rio.forwardRoomInputFrame(context.Background(), roomIOInputSilenceFlushFrame())
			} else {
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

			frame := roomIOInputFrameFromPCM(pcm, track.Codec().ClockRate, 1)

			rio.forwardRoomInputFrame(context.Background(), frame)
		}
	}
}

func roomIOInputFrameFromPCM(pcm []byte, sampleRate uint32, channels uint32) *model.AudioFrame {
	if channels == 0 {
		channels = 1
	}
	frame := &model.AudioFrame{
		Data:              pcm,
		SampleRate:        sampleRate,
		NumChannels:       channels,
		SamplesPerChannel: uint32(len(pcm)) / channels / 2,
	}
	if sampleRate == 0 || sampleRate == roomIOInputSampleRate {
		return frame
	}
	resampled, err := audio.ResampleAudioFrame(frame, roomIOInputSampleRate)
	if err != nil {
		logger.Logger.Warnw("room audio input resample failed", err, "from", sampleRate, "to", roomIOInputSampleRate)
		return frame
	}
	return resampled
}

func roomIOInputFrameFromFrame(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil {
		return nil
	}
	return roomIOInputFrameFromPCM(frame.Data, frame.SampleRate, frame.NumChannels)
}

func roomIOInputSilenceFlushFrame() *model.AudioFrame {
	samples := uint32(roomIOInputSilenceFlushDuration.Seconds() * float64(roomIOInputSampleRate))
	return &model.AudioFrame{
		Data:              make([]byte, samples*2),
		SampleRate:        roomIOInputSampleRate,
		NumChannels:       1,
		SamplesPerChannel: samples,
	}
}

func (rio *RoomIO) forwardRoomInputFrame(ctx context.Context, frame *model.AudioFrame) {
	if rio == nil || frame == nil {
		return
	}
	if rio.Recorder != nil {
		rio.Recorder.RecordInput(frame)
	}
	if rio.AgentSession != nil {
		rio.AgentSession.OnAudioFrame(ctx, frame)
	}
}

func (rio *RoomIO) OnPlaybackStarted(callback func(PlaybackStartedEvent)) {
	if rio == nil || callback == nil {
		return
	}
	rio.mu.Lock()
	rio.playbackStartedHandlers = append(rio.playbackStartedHandlers, callback)
	rio.mu.Unlock()
}

func (rio *RoomIO) OffPlaybackStarted(callback func(PlaybackStartedEvent)) {
	if rio == nil || callback == nil {
		return
	}
	callbackPointer := reflect.ValueOf(callback).Pointer()
	rio.mu.Lock()
	defer rio.mu.Unlock()
	for i, handler := range rio.playbackStartedHandlers {
		if reflect.ValueOf(handler).Pointer() != callbackPointer {
			continue
		}
		rio.playbackStartedHandlers = append(rio.playbackStartedHandlers[:i], rio.playbackStartedHandlers[i+1:]...)
		return
	}
}

func (rio *RoomIO) OnPlaybackFinished(callback func(PlaybackFinishedEvent)) {
	if rio == nil || callback == nil {
		return
	}
	rio.mu.Lock()
	rio.playbackFinishedHandlers = append(rio.playbackFinishedHandlers, callback)
	rio.mu.Unlock()
}

func (rio *RoomIO) OffPlaybackFinished(callback func(PlaybackFinishedEvent)) {
	if rio == nil || callback == nil {
		return
	}
	callbackPointer := reflect.ValueOf(callback).Pointer()
	rio.mu.Lock()
	defer rio.mu.Unlock()
	for i, handler := range rio.playbackFinishedHandlers {
		if reflect.ValueOf(handler).Pointer() != callbackPointer {
			continue
		}
		rio.playbackFinishedHandlers = append(rio.playbackFinishedHandlers[:i], rio.playbackFinishedHandlers[i+1:]...)
		return
	}
}

func (rio *RoomIO) WaitForPlayout(ctx context.Context) (PlaybackFinishedEvent, error) {
	if rio == nil {
		return PlaybackFinishedEvent{}, nil
	}
	rio.mu.Lock()
	target := rio.playbackSegmentsCount
	for rio.playbackFinishedCount < target {
		waiter := make(chan struct{})
		rio.playbackWaiters = append(rio.playbackWaiters, waiter)
		rio.mu.Unlock()
		select {
		case <-waiter:
		case <-ctx.Done():
			rio.removePlaybackWaiter(waiter)
			return PlaybackFinishedEvent{}, ctx.Err()
		}
		rio.mu.Lock()
	}
	ev := rio.lastPlaybackEvent
	rio.mu.Unlock()
	return ev, nil
}

func (rio *RoomIO) removePlaybackWaiter(waiter chan struct{}) {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	for i, candidate := range rio.playbackWaiters {
		if candidate != waiter {
			continue
		}
		rio.playbackWaiters = append(rio.playbackWaiters[:i], rio.playbackWaiters[i+1:]...)
		return
	}
}

func (rio *RoomIO) Flush() {
	rio.finishPlayback(false, "")
}

func (rio *RoomIO) ClearBuffer() {
	rio.dropPausedAudioOutput()
	rio.finishPlayback(true, "")
}

func (rio *RoomIO) PauseAudioOutput() {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	rio.audioOutputPaused = true
	rio.mu.Unlock()
}

func (rio *RoomIO) CanPauseAudioOutput() bool {
	return rio != nil && !rio.Options.DisableAudioOutput
}

func (rio *RoomIO) ResumeAudioOutput() {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	rio.audioOutputPaused = false
	waiters := append([]chan audioOutputWaitResult{}, rio.audioOutputWaiters...)
	rio.audioOutputWaiters = nil
	rio.mu.Unlock()
	for _, waiter := range waiters {
		waiter <- audioOutputWaitResult{}
	}
}

func (rio *RoomIO) dropPausedAudioOutput() {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	waiters := append([]chan audioOutputWaitResult{}, rio.audioOutputWaiters...)
	rio.audioOutputWaiters = nil
	rio.mu.Unlock()
	for _, waiter := range waiters {
		waiter <- audioOutputWaitResult{drop: true}
	}
}

func (rio *RoomIO) waitForAudioOutputResume(ctx context.Context) (bool, error) {
	if rio == nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rio.mu.Lock()
	if !rio.audioOutputPaused {
		rio.mu.Unlock()
		return false, nil
	}
	waiter := make(chan audioOutputWaitResult, 1)
	rio.audioOutputWaiters = append(rio.audioOutputWaiters, waiter)
	rio.mu.Unlock()
	select {
	case result := <-waiter:
		return result.drop, nil
	case <-ctx.Done():
		rio.removeAudioOutputWaiter(waiter)
		return false, ctx.Err()
	}
}

func (rio *RoomIO) removeAudioOutputWaiter(waiter chan audioOutputWaitResult) {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	for i, candidate := range rio.audioOutputWaiters {
		if candidate != waiter {
			continue
		}
		rio.audioOutputWaiters = append(rio.audioOutputWaiters[:i], rio.audioOutputWaiters[i+1:]...)
		return
	}
}

func (rio *RoomIO) startPlayback() (PlaybackStartedEvent, []func(PlaybackStartedEvent), bool) {
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if rio.playbackCapturing {
		return PlaybackStartedEvent{}, nil, false
	}
	rio.playbackCapturing = true
	rio.playbackSegmentsCount++
	rio.playbackAudioFrames = 0
	rio.playbackAudioBytes = 0
	rio.playbackAudioEncoded = 0
	rio.playbackAudioSampleRate = 0
	rio.playbackAudioChannels = 0
	rio.playbackAudioLastError = ""
	rio.playbackTranscript = rio.pendingPlaybackTranscript
	ev := PlaybackStartedEvent{CreatedAt: time.Now()}
	handlers := append([]func(PlaybackStartedEvent){}, rio.playbackStartedHandlers...)
	return ev, handlers, true
}

func (rio *RoomIO) setPlaybackTranscript(transcript string, final bool) {
	if rio == nil || transcript == "" {
		return
	}
	rio.mu.Lock()
	if rio.playbackCapturing {
		rio.playbackTranscript = transcript
	} else if !final {
		rio.pendingPlaybackTranscript = transcript
	}
	rio.mu.Unlock()
}

func (rio *RoomIO) addPlaybackPosition(duration time.Duration) {
	rio.mu.Lock()
	rio.playbackPosition += duration
	rio.mu.Unlock()
}

func (rio *RoomIO) AudioOutputDiagnostics() RoomIOAudioOutputDiagnostics {
	if rio == nil {
		return RoomIOAudioOutputDiagnostics{}
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	return rio.audioOutputDiagnostics
}

func (rio *RoomIO) recordAudioOutputFrameReceived(frame *model.AudioFrame) {
	if rio == nil || frame == nil {
		return
	}
	now := time.Now()
	rio.mu.Lock()
	rio.audioOutputDiagnostics.FramesReceived++
	rio.audioOutputDiagnostics.BytesReceived += len(frame.Data)
	rio.audioOutputDiagnostics.LastInputSampleRate = frame.SampleRate
	rio.audioOutputDiagnostics.LastInputSamplesPerChannel = frame.SamplesPerChannel
	rio.audioOutputDiagnostics.LastInputChannels = frame.NumChannels
	rio.audioOutputDiagnostics.LastFrameAt = now
	rio.mu.Unlock()
}

func (rio *RoomIO) recordPlaybackInputFrame(frame *model.AudioFrame) {
	if rio == nil || frame == nil {
		return
	}
	rio.mu.Lock()
	rio.playbackAudioFrames++
	rio.playbackAudioBytes += len(frame.Data)
	rio.playbackAudioSampleRate = frame.SampleRate
	rio.playbackAudioChannels = frame.NumChannels
	rio.mu.Unlock()
}

func (rio *RoomIO) recordAudioOutputFramePublished(input *model.AudioFrame, published *model.AudioFrame, publishedBytes int, encodedFrames int) {
	if rio == nil || input == nil || published == nil {
		return
	}
	now := time.Now()
	rio.mu.Lock()
	rio.audioOutputDiagnostics.FramesPublished++
	rio.audioOutputDiagnostics.BytesPublished += publishedBytes
	rio.audioOutputDiagnostics.EncodedFramesPublished += encodedFrames
	rio.audioOutputDiagnostics.LastPublishedSampleRate = published.SampleRate
	rio.audioOutputDiagnostics.LastPublishedSamplesPerChan = published.SamplesPerChannel
	rio.audioOutputDiagnostics.LastPublishedChannels = published.NumChannels
	rio.audioOutputDiagnostics.LastPublishedAt = now

	rio.playbackAudioEncoded += encodedFrames
	if publishedBytes == 0 {
		rio.playbackAudioLastError = "room audio output produced empty encoded frame"
	}
	rio.mu.Unlock()
}

func (rio *RoomIO) recordAudioOutputError(err error) {
	if rio == nil || err == nil {
		return
	}
	now := time.Now()
	rio.mu.Lock()
	errText := err.Error()
	rio.audioOutputDiagnostics.LastError = errText
	rio.audioOutputDiagnostics.LastErrorAt = now
	rio.playbackAudioLastError = errText
	rio.mu.Unlock()
	logger.Logger.Warnw("room audio output publish failed", err)
}

func (rio *RoomIO) finishPlayback(interrupted bool, synchronizedTranscript string) {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	if rio.playbackFinishedCount >= rio.playbackSegmentsCount {
		rio.mu.Unlock()
		return
	}
	rio.playbackCapturing = false
	rio.playbackFinishedCount++
	if synchronizedTranscript == "" {
		synchronizedTranscript = rio.playbackTranscript
	}
	ev := PlaybackFinishedEvent{
		PlaybackPosition:       rio.playbackPosition,
		Interrupted:            interrupted,
		SynchronizedTranscript: synchronizedTranscript,
		AudioFrames:            rio.playbackAudioFrames,
		AudioBytes:             rio.playbackAudioBytes,
		AudioEncodedFrames:     rio.playbackAudioEncoded,
		AudioSampleRate:        rio.playbackAudioSampleRate,
		AudioChannels:          rio.playbackAudioChannels,
		AudioLastError:         rio.playbackAudioLastError,
	}
	rio.playbackPosition = 0
	rio.playbackAudioFrames = 0
	rio.playbackAudioBytes = 0
	rio.playbackAudioEncoded = 0
	rio.playbackAudioSampleRate = 0
	rio.playbackAudioChannels = 0
	rio.playbackAudioLastError = ""
	rio.playbackTranscript = ""
	rio.pendingPlaybackTranscript = ""
	rio.lastPlaybackEvent = ev
	handlers := append([]func(PlaybackFinishedEvent){}, rio.playbackFinishedHandlers...)
	waiters := append([]chan struct{}{}, rio.playbackWaiters...)
	rio.playbackWaiters = nil
	rio.mu.Unlock()

	for _, waiter := range waiters {
		close(waiter)
	}
	for _, handler := range handlers {
		callPlaybackFinishedHandler(handler, ev)
	}
}

func (rio *RoomIO) PublishAudio(ctx context.Context, frame *model.AudioFrame) error {
	if rio == nil || rio.Options.DisableAudioOutput || rio.isAudioDisabled() {
		return nil
	}
	rio.recordAudioOutputFrameReceived(frame)
	if rio.Recorder != nil {
		rio.Recorder.RecordOutput(frame)
	}

	rio.mu.Lock()
	track := rio.audioTrack
	encoder := rio.encoder
	rio.mu.Unlock()

	if track == nil {
		rio.recordAudioOutputError(errors.New("room audio output track not started"))
		return nil
	}

	drop, err := rio.waitForAudioOutputResume(ctx)
	if err != nil {
		return err
	}
	if drop {
		return nil
	}

	started, handlers, ok := rio.startPlayback()
	if ok {
		for _, handler := range handlers {
			callPlaybackStartedHandler(handler, started)
		}
	}
	rio.recordPlaybackInputFrame(frame)

	if encoder != nil {
		encodeFrames, err := roomIOOpusEncodeFrames(frame)
		if err != nil {
			return err
		}
		for i, encodeFrame := range encodeFrames {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			encoded, err := encoder.Encode(encodeFrame.Data)
			if err != nil {
				rio.recordAudioOutputError(err)
				return err
			}
			duration := time.Duration(audio.CalculateFrameDuration(encodeFrame) * float64(time.Second))
			if err := track.WriteSample(media.Sample{
				Data:     encoded,
				Duration: duration,
			}, nil); err != nil {
				rio.recordAudioOutputError(err)
				return err
			}
			rio.recordAudioOutputFramePublished(frame, encodeFrame, len(encoded), 1)
			rio.addPlaybackPosition(duration)
			if i < len(encodeFrames)-1 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(duration):
				}
			}
		}
		return nil
	}

	duration := time.Duration(audio.CalculateFrameDuration(frame) * float64(time.Second))

	if err := track.WriteSample(media.Sample{
		Data:     frame.Data,
		Duration: duration,
	}, nil); err != nil {
		rio.recordAudioOutputError(err)
		return err
	}
	rio.recordAudioOutputFramePublished(frame, frame, len(frame.Data), 1)
	rio.addPlaybackPosition(duration)
	return nil
}

func (rio *RoomIO) markAudioSubscribed() {
	rio.mu.Lock()
	ch := rio.audioSubscribed
	rio.mu.Unlock()
	if ch == nil {
		return
	}
	rio.audioSubOnce.Do(func() {
		rio.mu.Lock()
		rio.audioOutputDiagnostics.TrackSubscribed = true
		rio.mu.Unlock()
		close(ch)
	})
}

func (rio *RoomIO) waitForAudioSubscription(ctx context.Context) error {
	if rio == nil {
		return nil
	}
	rio.mu.Lock()
	ch := rio.audioSubscribed
	rio.mu.Unlock()
	if ch == nil {
		return nil
	}
	timeout := rio.audioSubscriptionTimeout()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return nil
	case <-timer.C:
		logger.Logger.Warnw("room audio output subscription wait timed out", nil, "timeout", timeout)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (rio *RoomIO) audioSubscriptionTimeout() time.Duration {
	if rio != nil && rio.Options.AudioSubscriptionTimeout > 0 {
		return rio.Options.AudioSubscriptionTimeout
	}
	return roomIOAudioSubscriptionTimeout
}

func roomIOOpusEncodeFrames(frame *model.AudioFrame) ([]*model.AudioFrame, error) {
	if frame == nil {
		return nil, nil
	}
	encodeFrame := frame
	if frame.SampleRate != 0 && frame.SampleRate != roomIOOpusClockRate {
		resampled, err := audio.ResampleAudioFrame(frame, roomIOOpusClockRate)
		if err != nil {
			return nil, err
		}
		encodeFrame = resampled
	}
	if encodeFrame.NumChannels == 0 {
		return nil, fmt.Errorf("cannot encode audio with zero channels")
	}

	bytesPerSample := int(encodeFrame.NumChannels * 2)
	if len(encodeFrame.Data)%bytesPerSample != 0 {
		return nil, fmt.Errorf("cannot encode incomplete PCM sample")
	}
	samplesPerChannel := encodeFrame.SamplesPerChannel
	if samplesPerChannel == 0 {
		samplesPerChannel = uint32(len(encodeFrame.Data) / bytesPerSample)
	}
	expectedBytes := int(samplesPerChannel) * bytesPerSample
	if len(encodeFrame.Data) < expectedBytes {
		return nil, fmt.Errorf("audio frame data is shorter than declared sample count")
	}
	data := encodeFrame.Data[:expectedBytes]
	if samplesPerChannel == 0 {
		return nil, nil
	}

	frames := make([]*model.AudioFrame, 0, int(samplesPerChannel/roomIOOpusFrameSamples)+1)
	for sampleOffset := uint32(0); sampleOffset < samplesPerChannel; {
		chunkSamples := minUint32(roomIOOpusFrameSamples, samplesPerChannel-sampleOffset)
		paddedSamples := roomIOValidOpusSamples(chunkSamples)
		start := int(sampleOffset) * bytesPerSample
		end := int(sampleOffset+chunkSamples) * bytesPerSample
		chunkData := make([]byte, int(paddedSamples)*bytesPerSample)
		copy(chunkData, data[start:end])
		frames = append(frames, &model.AudioFrame{
			Data:              chunkData,
			SampleRate:        roomIOOpusClockRate,
			NumChannels:       encodeFrame.NumChannels,
			SamplesPerChannel: paddedSamples,
		})
		sampleOffset += chunkSamples
	}
	return frames, nil
}

func roomIOValidOpusSamples(samples uint32) uint32 {
	for _, valid := range []uint32{120, 240, 480, roomIOOpusFrameSamples} {
		if samples <= valid {
			return valid
		}
	}
	return roomIOOpusFrameSamples
}

func minUint32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

func callPlaybackStartedHandler(handler func(PlaybackStartedEvent), ev PlaybackStartedEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit playback_started", fmt.Errorf("panic: %v", recovered))
		}
	}()
	handler(ev)
}

func callPlaybackFinishedHandler(handler func(PlaybackFinishedEvent), ev PlaybackFinishedEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit playback_finished", fmt.Errorf("panic: %v", recovered))
		}
	}()
	handler(ev)
}

func (rio *RoomIO) Close() error {
	rio.dropPausedAudioOutput()
	rio.mu.Lock()
	rio.closed = true
	if rio.agentStateCancel != nil {
		rio.agentStateCancel()
		rio.agentStateCancel = nil
	}
	if rio.userStateCancel != nil {
		rio.userStateCancel()
		rio.userStateCancel = nil
	}
	if rio.userTranscriptionCancel != nil {
		rio.userTranscriptionCancel()
		rio.userTranscriptionCancel = nil
	}
	if rio.sessionCloseCancel != nil {
		rio.sessionCloseCancel()
		rio.sessionCloseCancel = nil
	}
	if rio.agentTranscriptionCancel != nil {
		rio.agentTranscriptionCancel()
		rio.agentTranscriptionCancel = nil
	}
	deleteRoomDone := rio.deleteRoomDone
	rio.mu.Unlock()

	if deleteRoomDone != nil {
		select {
		case <-deleteRoomDone:
		case <-time.After(roomIODeleteRoomCloseTimeout):
			logger.Logger.Warnw("automatic room deletion timed out", nil)
		}
	}

	rio.mu.Lock()
	defer rio.mu.Unlock()
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
