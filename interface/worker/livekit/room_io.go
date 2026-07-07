package livekit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"strconv"
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
const roomIOInputFrameSizeMS uint32 = 50
const roomIOAudioSubscriptionTimeout = 10 * time.Second
const roomIOInputSilenceFlushDuration = 500 * time.Millisecond
const roomIOOutputMaxLead = 200 * time.Millisecond

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
	PlaybackPosition          time.Duration
	Interrupted               bool
	SynchronizedTranscript    string
	HasSynchronizedTranscript bool
	AudioFrames               int
	AudioBytes                int
	AudioEncodedFrames        int
	AudioSampleRate           uint32
	AudioChannels             uint32
	AudioLastError            string
}

const (
	RoomEventDisconnected                 = "disconnected"
	RoomEventConnectionStateChanged       = "connection_state_changed"
	RoomEventParticipantConnected         = "participant_connected"
	RoomEventParticipantDisconnected      = "participant_disconnected"
	RoomEventParticipantAttributesChanged = "participant_attributes_changed"
	RoomEventParticipantActive            = "participant_active"
	RoomEventTrackSubscribed              = "track_subscribed"
	RoomEventTrackUnpublished             = "track_unpublished"
	RoomEventTrackPublished               = "track_published"
	RoomEventLocalTrackPublished          = "local_track_published"
	RoomEventSipDTMFReceived              = "sip_dtmf_received"
)

type RoomEvent interface {
	Type() string
}

type RoomDisconnectedEvent struct{}

func (*RoomDisconnectedEvent) Type() string { return RoomEventDisconnected }

type RoomConnectionStateChangedEvent struct {
	State string
}

func (*RoomConnectionStateChangedEvent) Type() string { return RoomEventConnectionStateChanged }

type RoomParticipantConnectedEvent struct {
	Participant *lksdk.RemoteParticipant
}

func (*RoomParticipantConnectedEvent) Type() string { return RoomEventParticipantConnected }

type RoomParticipantDisconnectedEvent struct {
	Participant *lksdk.RemoteParticipant
}

func (*RoomParticipantDisconnectedEvent) Type() string {
	return RoomEventParticipantDisconnected
}

type RoomParticipantAttributesChangedEvent struct {
	Changed     map[string]string
	Participant lksdk.Participant
}

func (*RoomParticipantAttributesChangedEvent) Type() string {
	return RoomEventParticipantAttributesChanged
}

type RoomParticipantActiveEvent struct {
	Participant lksdk.Participant
}

func (*RoomParticipantActiveEvent) Type() string { return RoomEventParticipantActive }

type RoomTrackSubscribedEvent struct {
	Track       *webrtc.TrackRemote
	Publication *lksdk.RemoteTrackPublication
	Participant *lksdk.RemoteParticipant
}

func (*RoomTrackSubscribedEvent) Type() string { return RoomEventTrackSubscribed }

type RoomTrackUnpublishedEvent struct {
	Publication *lksdk.RemoteTrackPublication
	Participant *lksdk.RemoteParticipant
}

func (*RoomTrackUnpublishedEvent) Type() string { return RoomEventTrackUnpublished }

type RoomTrackPublishedEvent struct {
	Publication *lksdk.RemoteTrackPublication
	Participant *lksdk.RemoteParticipant
}

func (*RoomTrackPublishedEvent) Type() string { return RoomEventTrackPublished }

type RoomLocalTrackPublishedEvent struct {
	Publication *lksdk.LocalTrackPublication
	Participant *lksdk.LocalParticipant
}

func (*RoomLocalTrackPublishedEvent) Type() string { return RoomEventLocalTrackPublished }

type RoomSipDTMFReceivedEvent struct {
	Event  *livekit.SipDTMF
	Params lksdk.DataReceiveParams
}

func (*RoomSipDTMFReceivedEvent) Type() string { return RoomEventSipDTMFReceived }

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

	playbackCapturing            bool
	playbackSegmentsCount        int
	playbackFinishedCount        int
	playbackPosition             time.Duration
	playbackStartedAt            time.Time
	playbackAudioFrames          int
	playbackAudioBytes           int
	playbackAudioEncoded         int
	playbackAudioSampleRate      uint32
	playbackAudioChannels        uint32
	playbackAudioLastError       string
	playbackTranscript           string
	playbackTranscriptSet        bool
	pendingPlaybackTranscript    string
	pendingPlaybackTranscriptSet bool
	lastPlaybackEvent            PlaybackFinishedEvent
	playbackWaiters              []chan struct{}
	playbackStartedHandlers      []func(PlaybackStartedEvent)
	playbackFinishedHandlers     []func(PlaybackFinishedEvent)
	roomEventListeners           map[string]map[uint64]func(RoomEvent)
	nextRoomEventListenerID      uint64

	audioOutputPaused   bool
	audioOutputWaiters  []chan audioOutputWaitResult
	audioOutputCarry    []byte
	audioOutputDeadline time.Time

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
	agentStatePublishSeq     uint64
	userStateCancel          context.CancelFunc
	clientEvents             roomIOClientEvents

	agentTranscriptionCancel         context.CancelFunc
	agentTranscriptionSegmentID      string
	agentTranscriptionText           string
	transcriptionTextPublisher       func(string, lksdk.StreamTextOptions)
	transcriptionPacketPublisher     func(*livekit.Transcription) error
	transcriptionParticipantIdentity func() string

	agentTextStreamOpener     func(lksdk.StreamTextOptions) roomIOTextStreamWriter
	agentTextStreamMu         sync.Mutex
	agentTextStreamWriter     roomIOTextStreamWriter
	agentTextStreamSegmentID  string
	agentTextStreamHasContent bool

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
	rio.agentTextStreamOpener = rio.openRoomTextStream
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
		rio.audioSubscribed = make(chan struct{})
		session.SetAudioOutputController(rio)
		session.SetAudioPlaybackController(roomIOPlaybackController{rio: rio})
		session.SetUserAwayTimerGate(rio.userAwayTimerBlocked)
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

func (c roomIOPlaybackController) Flush() {
	if c.rio != nil {
		c.rio.Flush()
	}
}

func (c roomIOPlaybackController) WaitForPlayout(ctx context.Context) (agent.AudioPlaybackResult, error) {
	if c.rio == nil {
		return agent.AudioPlaybackResult{}, nil
	}
	ev, err := c.rio.WaitForPlayout(ctx)
	return agent.AudioPlaybackResult{
		PlaybackPosition:          ev.PlaybackPosition,
		Interrupted:               ev.Interrupted,
		SynchronizedTranscript:    ev.SynchronizedTranscript,
		HasSynchronizedTranscript: ev.HasSynchronizedTranscript,
	}, err
}

func (rio *RoomIO) AttachRoom(room *lksdk.Room) {
	if rio == nil || room == nil {
		return
	}
	rio.Room = room
	rio.clientEvents = newClientEventsDispatcher(room)
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

	speechEvents := rio.AgentSession.SpeechCreatedEvents()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-speechEvents:
				if !ok {
					return
				}
				rio.mu.Lock()
				rio.agentTranscriptionSegmentID = ""
				rio.agentTranscriptionText = ""
				rio.mu.Unlock()
				rio.closeAgentTextStream()
			}
		}
	}()

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
	if rio == nil || (rio.agentStatePublisher == nil && rio.clientEvents == nil) {
		return
	}
	if rio.agentStatePublisher != nil {
		publisher := rio.agentStatePublisher
		enabled := rio.agentStatePublishEnabled
		attrs := map[string]string{
			RoomIOAgentStateAttribute: string(ev.NewState),
		}
		rio.mu.Lock()
		rio.agentStatePublishSeq++
		seq := rio.agentStatePublishSeq
		rio.mu.Unlock()
		go func() {
			if !rio.isCurrentAgentStatePublish(seq) {
				return
			}
			if enabled != nil && !enabled() {
				return
			}
			if !rio.isCurrentAgentStatePublish(seq) {
				return
			}
			publisher(attrs)
		}()
	}
	if rio.clientEvents != nil {
		rio.clientEvents.DispatchAgentState(ev.NewState)
	}
}

func (rio *RoomIO) isCurrentAgentStatePublish(seq uint64) bool {
	if rio == nil {
		return false
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	return seq == rio.agentStatePublishSeq
}

func (rio *RoomIO) handleUserStateChanged(ev agent.UserStateChangedEvent) {
	if rio == nil || rio.clientEvents == nil {
		return
	}
	rio.clientEvents.DispatchUserState(ev.NewState)
}

func (rio *RoomIO) handleAgentOutputTranscribed(ev agent.AgentOutputTranscribedEvent) {
	if rio == nil || (ev.Transcript == "" && !ev.IsFinal) {
		return
	}
	segmentID, streamText, legacyText, ok := rio.agentOutputTranscriptionState(ev.Transcript, ev.IsFinal)
	if !ok {
		return
	}
	if legacyText != "" {
		rio.setPlaybackTranscript(legacyText, ev.IsFinal)
	}
	attributes := map[string]string{
		RoomIOTranscriptionFinalAttribute:     strconv.FormatBool(ev.IsFinal),
		RoomIOTranscriptionSegmentIDAttribute: segmentID,
	}
	if trackID := rio.transcriptionTrackID(); trackID != "" {
		attributes[RoomIOTranscriptionTrackIDAttribute] = trackID
	}
	legacyEv := ev
	legacyEv.Transcript = legacyText
	rio.publishLegacyAgentTranscription(legacyEv, segmentID)
	rio.publishAgentTranscriptionStream(streamText, lksdk.StreamTextOptions{
		Topic:      RoomIOTranscriptionTopic,
		Attributes: attributes,
	})
}

func (rio *RoomIO) agentOutputTranscriptionState(transcript string, final bool) (string, string, string, bool) {
	if rio == nil {
		return roomIOTranscriptionSegmentID(), transcript, transcript, true
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if final && transcript == "" && rio.agentTranscriptionSegmentID == "" {
		return "", "", "", false
	}
	if rio.agentTranscriptionSegmentID == "" {
		rio.agentTranscriptionSegmentID = roomIOTranscriptionSegmentID()
	}
	segmentID := rio.agentTranscriptionSegmentID
	legacyText := transcript
	if !final {
		rio.agentTranscriptionText += transcript
		legacyText = rio.agentTranscriptionText
	}
	if final {
		rio.agentTranscriptionSegmentID = ""
		rio.agentTranscriptionText = ""
	}
	return segmentID, transcript, legacyText, true
}

func (rio *RoomIO) userInputTranscriptionState(transcript string, final bool) (string, bool) {
	if rio == nil {
		return roomIOTranscriptionSegmentID(), true
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if final && transcript == "" && rio.userTranscriptionSegmentID == "" {
		return "", false
	}
	if rio.userTranscriptionSegmentID == "" {
		rio.userTranscriptionSegmentID = roomIOTranscriptionSegmentID()
	}
	segmentID := rio.userTranscriptionSegmentID
	if final {
		rio.userTranscriptionSegmentID = ""
	}
	return segmentID, true
}

func (rio *RoomIO) handleUserInputTranscribed(ev agent.UserInputTranscribedEvent) {
	if rio == nil {
		return
	}
	trackID, participantID := rio.userTranscriptionTarget()
	if trackID == "" || participantID == "" {
		return
	}
	segmentID, ok := rio.userInputTranscriptionState(ev.Transcript, ev.IsFinal)
	if !ok {
		return
	}
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
	if rio == nil || rio.transcriptionTextPublisher == nil {
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

type roomIOTextStreamWriter interface {
	Write(text string)
	Close()
}

const roomIOTextStreamWriteTimeout = 2 * time.Second

type lkTextStreamWriter interface {
	Write(data string, onDone *func())
	Close()
}

type roomIOSDKTextStreamWriter struct {
	writer       lkTextStreamWriter
	writeTimeout time.Duration
}

func (w roomIOSDKTextStreamWriter) Write(text string) {
	if w.writer == nil {
		return
	}
	done := make(chan struct{})
	cb := func() { close(done) }
	w.writer.Write(text, &cb)
	timeout := w.writeTimeout
	if timeout <= 0 {
		timeout = roomIOTextStreamWriteTimeout
	}
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (w roomIOSDKTextStreamWriter) Close() {
	if w.writer != nil {
		w.writer.Close()
	}
}

func (rio *RoomIO) openRoomTextStream(opts lksdk.StreamTextOptions) roomIOTextStreamWriter {
	if rio == nil || rio.Room == nil || rio.Room.LocalParticipant == nil {
		return nil
	}
	if rio.Room.ConnectionState() != lksdk.ConnectionStateConnected {
		return nil
	}
	return roomIOSDKTextStreamWriter{writer: rio.Room.LocalParticipant.StreamText(opts)}
}

func (rio *RoomIO) publishAgentTranscriptionStream(text string, opts lksdk.StreamTextOptions) {
	if rio == nil {
		return
	}
	if rio.agentTextStreamOpener == nil {
		if rio.transcriptionTextPublisher != nil {
			rio.transcriptionTextPublisher(text, opts)
		}
		return
	}

	segmentID := opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	final := opts.Attributes[RoomIOTranscriptionFinalAttribute] == "true"

	rio.agentTextStreamMu.Lock()
	defer rio.agentTextStreamMu.Unlock()

	if rio.agentTextStreamWriter != nil && rio.agentTextStreamSegmentID != segmentID {
		rio.agentTextStreamWriter.Close()
		rio.agentTextStreamWriter = nil
		rio.agentTextStreamSegmentID = ""
		rio.agentTextStreamHasContent = false
	}

	if rio.agentTextStreamWriter == nil {
		writer := rio.agentTextStreamOpener(opts)
		if writer == nil {
			return
		}
		rio.agentTextStreamWriter = writer
		rio.agentTextStreamSegmentID = segmentID
		rio.agentTextStreamHasContent = false
	}

	if !final {
		if text != "" {
			rio.agentTextStreamWriter.Write(text)
			rio.agentTextStreamHasContent = true
		}
		return
	}
	if !rio.agentTextStreamHasContent && text != "" {
		rio.agentTextStreamWriter.Write(text)
	}
	rio.agentTextStreamWriter.Close()
	rio.agentTextStreamWriter = nil
	rio.agentTextStreamSegmentID = ""
	rio.agentTextStreamHasContent = false
}

func (rio *RoomIO) closeAgentTextStream() {
	if rio == nil {
		return
	}
	rio.agentTextStreamMu.Lock()
	defer rio.agentTextStreamMu.Unlock()
	if rio.agentTextStreamWriter != nil {
		rio.agentTextStreamWriter.Close()
		rio.agentTextStreamWriter = nil
	}
	rio.agentTextStreamSegmentID = ""
	rio.agentTextStreamHasContent = false
}

func (rio *RoomIO) publishTranscriptionPacket(transcription *livekit.Transcription) error {
	if rio == nil || rio.Room == nil || rio.Room.LocalParticipant == nil || transcription == nil {
		return nil
	}
	if rio.Room.ConnectionState() != lksdk.ConnectionStateConnected {
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
		if err := deleteRoom(context.Background(), roomName); err != nil && !RoomDeleteNotFound(err) {
			logger.Logger.Warnw("failed to delete room on agent session close", err, "room", roomName, "reason", reason)
		}
	}()
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
	return rio.WithCallback(nil)
}

func (rio *RoomIO) WithCallback(cb *lksdk.RoomCallback) *lksdk.RoomCallback {
	return RoomCallbackWithHandlers(cb, RoomCallbackHandlers{
		OnDisconnected: rio.onRoomDisconnected,
		OnReconnecting: func() {
			rio.emitRoomEvent(&RoomConnectionStateChangedEvent{State: "reconnecting"})
		},
		OnReconnected: func() {
			rio.emitRoomEvent(&RoomConnectionStateChangedEvent{State: "connected"})
		},
		OnParticipantConnected: func(participant RemoteParticipantView) {
			rio.onParticipantConnected(participant.(*lksdk.RemoteParticipant))
		},
		OnParticipantDisconnected: func(participant RemoteParticipantView) {
			rio.onParticipantDisconnected(participant.(*lksdk.RemoteParticipant))
		},
		OnLocalTrackPublished: func(publication *lksdk.LocalTrackPublication, participant *lksdk.LocalParticipant) {
			rio.emitRoomEvent(&RoomLocalTrackPublishedEvent{Publication: publication, Participant: participant})
		},
		OnLocalTrackSubscribed: rio.onLocalTrackSubscribed,
		OnTrackSubscribed:      rio.onTrackSubscribed,
		OnTrackUnpublished: func(publication *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
			rio.emitRoomEvent(&RoomTrackUnpublishedEvent{Publication: publication, Participant: participant})
			trackID := ""
			if publication != nil {
				trackID = publication.SID()
			}
			participantID := ""
			if participant != nil {
				participantID = participant.Identity()
			}
			rio.handleTrackUnpublished(trackID, participantID)
		},
		OnTrackPublishedEvent: func(publication *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
			rio.emitRoomEvent(&RoomTrackPublishedEvent{Publication: publication, Participant: participant})
		},
		OnAttributesChanged: func(changed map[string]string, participant lksdk.Participant) {
			rio.emitRoomEvent(&RoomParticipantAttributesChangedEvent{Changed: maps.Clone(changed), Participant: participant})
		},
		OnIsSpeakingChanged: func(participant lksdk.Participant) {
			if participant != nil && participant.IsSpeaking() {
				rio.emitRoomEvent(&RoomParticipantActiveEvent{Participant: participant})
			}
		},
		OnDataPacket: rio.onDataPacket,
	})
}

func (rio *RoomIO) onRoomDisconnected() {
	rio.emitRoomEvent(&RoomDisconnectedEvent{})
	rio.emitRoomEvent(&RoomConnectionStateChangedEvent{State: "disconnected"})
	if rio == nil || rio.AgentSession == nil || rio.Options.DisableCloseOnDisconnect {
		return
	}
	if rio.isDeletingRoom() {
		return
	}
	rio.AgentSession.CloseSoon(agent.CloseReasonParticipantDisconnected)
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
	rio.emitRoomEvent(&RoomSipDTMFReceivedEvent{Event: dtmf, Params: params})
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
	if rio.Room.ConnectionState() != lksdk.ConnectionStateConnected {
		return errors.New("room is not connected")
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
	rio.mu.Lock()
	closed := rio.closed
	rio.mu.Unlock()
	if closed {
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
	rio.setAudioOutputTrack(track, trackID, publication)
	return rio.waitForAudioSubscription(ctx)
}

func (rio *RoomIO) setAudioOutputTrack(track *lksdk.LocalTrack, trackID string, publication *lksdk.LocalTrackPublication) {
	rio.audioSubOnce = sync.Once{}
	rio.mu.Lock()
	if rio.audioSubscribed == nil {
		rio.audioSubscribed = make(chan struct{})
	}
	rio.audioTrack = track
	rio.audioTrackID = trackID
	rio.audioPublication = publication
	rio.audioOutputDiagnostics.TrackID = trackID
	rio.audioOutputDiagnostics.TrackPublished = publication != nil
	rio.audioOutputDiagnostics.TrackSubscribed = false
	rio.mu.Unlock()
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
	if track != nil {
		rio.emitRoomEvent(&RoomTrackSubscribedEvent{
			Track:       track,
			Publication: publication,
			Participant: rp,
		})
	}
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
	rio.emitRoomEvent(&RoomParticipantConnectedEvent{Participant: participant})
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
	rio.emitRoomEvent(&RoomParticipantDisconnectedEvent{Participant: participant})
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

func (rio *RoomIO) handleTrackUnpublished(trackID string, participantIdentity string) {
	if rio == nil || trackID == "" || participantIdentity == "" {
		return
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	if rio.userTranscriptionTrackID != trackID || rio.userTranscriptionParticipantID != participantIdentity {
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
	inputStream := newRoomIOInputAudioStream()

	if rio.preConnectAudio != nil {
		if frames := rio.preConnectAudio.WaitForData(ctx, track.ID()); len(frames) > 0 {
			for _, frame := range frames {
				inputFrame := roomIOInputFrameFromFrame(frame)
				if inputFrame != nil {
					rio.forwardRoomInputFrames(context.Background(), inputStream.Push(inputFrame.Data))
				}
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
				rio.forwardRoomInputFrames(context.Background(), inputStream.Flush())
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

			rio.forwardRoomInputFrames(context.Background(), inputStream.Push(frame.Data))
		}
	}
}

func newRoomIOInputAudioStream() *audio.AudioByteStream {
	samplesPerChannel := roomIOInputSampleRate * roomIOInputFrameSizeMS / 1000
	return audio.NewAudioByteStream(roomIOInputSampleRate, 1, samplesPerChannel)
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
	if sampleRate != 0 && sampleRate != roomIOInputSampleRate {
		resampled, err := audio.ResampleAudioFrame(frame, roomIOInputSampleRate)
		if err != nil {
			logger.Logger.Warnw("room audio input resample failed", err, "from", sampleRate, "to", roomIOInputSampleRate)
			return frame
		}
		frame = resampled
	}
	if frame.NumChannels > 1 {
		mono, err := roomIOMonoAudioFrame(frame)
		if err != nil {
			logger.Logger.Warnw("room audio input downmix failed", err, "channels", frame.NumChannels)
			return frame
		}
		frame = mono
	}
	return frame
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

func (rio *RoomIO) forwardRoomInputFrames(ctx context.Context, frames []*model.AudioFrame) {
	for _, frame := range frames {
		rio.forwardRoomInputFrame(ctx, frame)
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

func (rio *RoomIO) On(eventType string, callback func(RoomEvent)) func() {
	if rio == nil || eventType == "" || callback == nil {
		return func() {}
	}
	rio.mu.Lock()
	if rio.roomEventListeners == nil {
		rio.roomEventListeners = make(map[string]map[uint64]func(RoomEvent))
	}
	if rio.roomEventListeners[eventType] == nil {
		rio.roomEventListeners[eventType] = make(map[uint64]func(RoomEvent))
	}
	id := rio.nextRoomEventListenerID
	rio.nextRoomEventListenerID++
	rio.roomEventListeners[eventType][id] = callback
	rio.mu.Unlock()

	return func() {
		rio.mu.Lock()
		defer rio.mu.Unlock()
		listeners := rio.roomEventListeners[eventType]
		if listeners == nil {
			return
		}
		delete(listeners, id)
		if len(listeners) == 0 {
			delete(rio.roomEventListeners, eventType)
		}
	}
}

func (rio *RoomIO) emitRoomEvent(ev RoomEvent) {
	if rio == nil || ev == nil {
		return
	}
	rio.mu.Lock()
	listenersByID := rio.roomEventListeners[ev.Type()]
	listeners := make([]func(RoomEvent), 0, len(listenersByID))
	for _, listener := range listenersByID {
		listeners = append(listeners, listener)
	}
	rio.mu.Unlock()

	for _, listener := range listeners {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Logger.Warnw("room event listener panicked", nil, "event", ev.Type(), "panic", recovered)
				}
			}()
			listener(ev)
		}()
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
	ctx := context.Background()
	rio.flushAudioOutputTail(ctx)
	rio.waitForAudioOutputDrain(ctx)
	rio.finishPlayback(false, "")
}

func (rio *RoomIO) waitForAudioOutputDrain(ctx context.Context) {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	deadline := rio.audioOutputDeadline
	rio.mu.Unlock()
	if deadline.IsZero() {
		return
	}
	wait := time.Until(deadline)
	if wait <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
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
	rio.audioOutputDeadline = time.Time{}
	rio.playbackAudioFrames = 0
	rio.playbackAudioBytes = 0
	rio.playbackAudioEncoded = 0
	rio.playbackAudioSampleRate = 0
	rio.playbackAudioChannels = 0
	rio.playbackAudioLastError = ""
	rio.playbackTranscript = rio.pendingPlaybackTranscript
	rio.playbackTranscriptSet = rio.pendingPlaybackTranscriptSet
	startedAt := time.Now()
	rio.playbackStartedAt = startedAt
	ev := PlaybackStartedEvent{CreatedAt: startedAt}
	handlers := append([]func(PlaybackStartedEvent){}, rio.playbackStartedHandlers...)
	return ev, handlers, true
}

func (rio *RoomIO) setPlaybackTranscript(transcript string, final bool) {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	if rio.playbackCapturing {
		rio.playbackTranscript = transcript
		rio.playbackTranscriptSet = true
	} else if !final {
		rio.pendingPlaybackTranscript = transcript
		rio.pendingPlaybackTranscriptSet = true
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
	rio.audioOutputCarry = nil
	rio.audioOutputDeadline = time.Time{}
	playbackPosition := rio.playbackPosition
	fullPlaybackPosition := playbackPosition
	if interrupted && !rio.playbackStartedAt.IsZero() {
		elapsed := time.Since(rio.playbackStartedAt)
		if elapsed < playbackPosition {
			playbackPosition = elapsed
		}
	}
	hasSynchronizedTranscript := synchronizedTranscript != ""
	if !hasSynchronizedTranscript && (!interrupted || playbackPosition >= fullPlaybackPosition) && rio.playbackTranscriptSet {
		synchronizedTranscript = rio.playbackTranscript
		hasSynchronizedTranscript = true
	}
	ev := PlaybackFinishedEvent{
		PlaybackPosition:          playbackPosition,
		Interrupted:               interrupted,
		SynchronizedTranscript:    synchronizedTranscript,
		HasSynchronizedTranscript: hasSynchronizedTranscript,
		AudioFrames:               rio.playbackAudioFrames,
		AudioBytes:                rio.playbackAudioBytes,
		AudioEncodedFrames:        rio.playbackAudioEncoded,
		AudioSampleRate:           rio.playbackAudioSampleRate,
		AudioChannels:             rio.playbackAudioChannels,
		AudioLastError:            rio.playbackAudioLastError,
	}
	rio.playbackPosition = 0
	rio.playbackStartedAt = time.Time{}
	rio.playbackAudioFrames = 0
	rio.playbackAudioBytes = 0
	rio.playbackAudioEncoded = 0
	rio.playbackAudioSampleRate = 0
	rio.playbackAudioChannels = 0
	rio.playbackAudioLastError = ""
	rio.playbackTranscript = ""
	rio.playbackTranscriptSet = false
	rio.pendingPlaybackTranscript = ""
	rio.pendingPlaybackTranscriptSet = false
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
	if err := rio.waitForAudioSubscriptionReady(ctx); err != nil {
		return err
	}
	if rio.isClosed() {
		return nil
	}

	rio.mu.Lock()
	track := rio.audioTrack
	encoder := rio.encoder
	rio.mu.Unlock()

	if track == nil {
		rio.recordAudioOutputFrameReceived(frame)
		if rio.Recorder != nil {
			rio.Recorder.RecordOutput(frame)
		}
		rio.recordAudioOutputError(errors.New("room audio output track not started"))
		return nil
	}

	rio.recordAudioOutputFrameReceived(frame)
	if rio.Recorder != nil {
		rio.Recorder.RecordOutput(frame)
	}

	drop, err := rio.waitForAudioOutputResume(ctx)
	if err != nil {
		return err
	}
	if drop {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	var encodeFrames []*model.AudioFrame
	if encoder != nil {
		var err error
		encodeFrames, err = rio.encodeOpusOutputFrames(frame)
		if err != nil {
			rio.recordAudioOutputError(err)
			return err
		}
	}

	started, handlers, ok := rio.startPlayback()
	if ok {
		for _, handler := range handlers {
			callPlaybackStartedHandler(handler, started)
		}
	}
	rio.recordPlaybackInputFrame(frame)

	if encoder != nil {
		for _, encodeFrame := range encodeFrames {
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
			if err := rio.pacePlaybackWrite(ctx, duration); err != nil {
				return err
			}
			if err := track.WriteSample(media.Sample{
				Data:     encoded,
				Duration: duration,
			}, nil); err != nil {
				rio.recordAudioOutputError(err)
				return err
			}
			rio.recordAudioOutputFramePublished(frame, encodeFrame, len(encoded), 1)
			rio.addPlaybackPosition(duration)
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

func (rio *RoomIO) waitForAudioSubscriptionReady(ctx context.Context) error {
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
		logger.Logger.Warnw("room audio output publish subscription wait timed out", nil, "timeout", timeout)
		rio.releaseAudioSubscriptionFallback(ch)
		if rio.AgentSession != nil {
			rio.AgentSession.RefreshUserAwayTimer()
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (rio *RoomIO) markAudioSubscribed() {
	rio.mu.Lock()
	ch := rio.audioSubscribed
	rio.mu.Unlock()
	if ch == nil {
		if rio.AgentSession != nil {
			rio.AgentSession.RefreshUserAwayTimer()
		}
		return
	}
	rio.audioSubOnce.Do(func() {
		rio.mu.Lock()
		rio.audioOutputDiagnostics.TrackSubscribed = true
		rio.mu.Unlock()
		close(ch)
	})
	if rio.AgentSession != nil {
		rio.AgentSession.RefreshUserAwayTimer()
	}
}

func (rio *RoomIO) userAwayTimerBlocked() bool {
	if rio == nil || rio.Options.DisableAudioOutput {
		return false
	}
	rio.mu.Lock()
	ch := rio.audioSubscribed
	rio.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return false
	default:
		return true
	}
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
		rio.releaseAudioSubscriptionFallback(ch)
		if rio.AgentSession != nil {
			rio.AgentSession.RefreshUserAwayTimer()
		}
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

func (rio *RoomIO) isClosed() bool {
	if rio == nil {
		return true
	}
	rio.mu.Lock()
	defer rio.mu.Unlock()
	return rio.closed
}

func (rio *RoomIO) releaseAudioSubscriptionWaiters() {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	ch := rio.audioSubscribed
	rio.mu.Unlock()
	if ch == nil {
		return
	}
	rio.audioSubOnce.Do(func() {
		close(ch)
	})
}

func (rio *RoomIO) releaseAudioSubscriptionFallback(ch chan struct{}) {
	if rio == nil || ch == nil {
		return
	}
	rio.mu.Lock()
	if rio.audioSubscribed == ch {
		rio.audioSubscribed = nil
	}
	rio.mu.Unlock()
	rio.audioSubOnce.Do(func() {
		close(ch)
	})
}

func roomIOResampleMonoForOpus(frame *model.AudioFrame) (*model.AudioFrame, error) {
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
	if encodeFrame.NumChannels > 1 {
		mono, err := roomIOMonoAudioFrame(encodeFrame)
		if err != nil {
			return nil, err
		}
		encodeFrame = mono
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
	return &model.AudioFrame{
		Data:              encodeFrame.Data[:expectedBytes],
		SampleRate:        roomIOOpusClockRate,
		NumChannels:       1,
		SamplesPerChannel: samplesPerChannel,
	}, nil
}

func roomIOChunkOpusWithCarry(carry, data []byte, flushTail bool) ([]*model.AudioFrame, []byte) {
	const bytesPerSample = 2
	buf := data
	if len(carry) > 0 {
		buf = make([]byte, 0, len(carry)+len(data))
		buf = append(buf, carry...)
		buf = append(buf, data...)
	}

	frameBytes := int(roomIOOpusFrameSamples) * bytesPerSample
	frames := make([]*model.AudioFrame, 0, len(buf)/frameBytes+1)
	offset := 0
	for len(buf)-offset >= frameBytes {
		chunk := make([]byte, frameBytes)
		copy(chunk, buf[offset:offset+frameBytes])
		frames = append(frames, &model.AudioFrame{
			Data:              chunk,
			SampleRate:        roomIOOpusClockRate,
			NumChannels:       1,
			SamplesPerChannel: roomIOOpusFrameSamples,
		})
		offset += frameBytes
	}

	leftover := buf[offset:]
	if len(leftover) == 0 {
		return frames, nil
	}
	if flushTail {
		paddedSamples := roomIOValidOpusSamples(uint32(len(leftover) / bytesPerSample))
		chunk := make([]byte, int(paddedSamples)*bytesPerSample)
		copy(chunk, leftover)
		frames = append(frames, &model.AudioFrame{
			Data:              chunk,
			SampleRate:        roomIOOpusClockRate,
			NumChannels:       1,
			SamplesPerChannel: paddedSamples,
		})
		return frames, nil
	}
	newCarry := make([]byte, len(leftover))
	copy(newCarry, leftover)
	return frames, newCarry
}

func (rio *RoomIO) encodeOpusOutputFrames(frame *model.AudioFrame) ([]*model.AudioFrame, error) {
	mono, err := roomIOResampleMonoForOpus(frame)
	if err != nil {
		return nil, err
	}
	var data []byte
	if mono != nil {
		data = mono.Data
	}
	rio.mu.Lock()
	frames, carry := roomIOChunkOpusWithCarry(rio.audioOutputCarry, data, false)
	rio.audioOutputCarry = carry
	rio.mu.Unlock()
	return frames, nil
}

func (rio *RoomIO) flushAudioOutputTail(ctx context.Context) {
	if rio == nil {
		return
	}
	rio.mu.Lock()
	carry := rio.audioOutputCarry
	rio.audioOutputCarry = nil
	encoder := rio.encoder
	track := rio.audioTrack
	rio.mu.Unlock()
	if len(carry) == 0 || encoder == nil || track == nil {
		return
	}
	frames, _ := roomIOChunkOpusWithCarry(nil, carry, true)
	for _, encodeFrame := range frames {
		encoded, err := encoder.Encode(encodeFrame.Data)
		if err != nil {
			rio.recordAudioOutputError(err)
			return
		}
		duration := time.Duration(audio.CalculateFrameDuration(encodeFrame) * float64(time.Second))
		if err := rio.pacePlaybackWrite(ctx, duration); err != nil {
			return
		}
		if err := track.WriteSample(media.Sample{Data: encoded, Duration: duration}, nil); err != nil {
			rio.recordAudioOutputError(err)
			return
		}
		rio.recordAudioOutputFramePublished(encodeFrame, encodeFrame, len(encoded), 1)
		rio.addPlaybackPosition(duration)
	}
}

func (rio *RoomIO) pacePlaybackWrite(ctx context.Context, duration time.Duration) error {
	rio.mu.Lock()
	now := time.Now()
	if rio.audioOutputDeadline.IsZero() || rio.audioOutputDeadline.Before(now) {
		rio.audioOutputDeadline = now
	}
	lead := rio.audioOutputDeadline.Sub(now)
	rio.audioOutputDeadline = rio.audioOutputDeadline.Add(duration)
	rio.mu.Unlock()
	wait := lead - roomIOOutputMaxLead
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

func roomIOMonoAudioFrame(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if frame == nil || frame.NumChannels <= 1 {
		return frame, nil
	}
	if frame.NumChannels == 0 {
		return nil, fmt.Errorf("cannot downmix audio with zero channels")
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("cannot downmix non-16-bit PCM audio")
	}
	bytesPerSample := int(frame.NumChannels * 2)
	if len(frame.Data)%bytesPerSample != 0 {
		return nil, fmt.Errorf("cannot downmix incomplete PCM sample")
	}
	samplesPerChannel := frame.SamplesPerChannel
	if samplesPerChannel == 0 {
		samplesPerChannel = uint32(len(frame.Data) / bytesPerSample)
	}
	expectedBytes := int(samplesPerChannel) * bytesPerSample
	if len(frame.Data) < expectedBytes {
		return nil, fmt.Errorf("audio frame data is shorter than declared sample count")
	}

	channels := int(frame.NumChannels)
	out := make([]byte, int(samplesPerChannel)*2)
	for sample := 0; sample < int(samplesPerChannel); sample++ {
		var sum int32
		base := sample * bytesPerSample
		for ch := 0; ch < channels; ch++ {
			offset := base + ch*2
			v := int16(frame.Data[offset]) | int16(frame.Data[offset+1])<<8
			sum += int32(v)
		}
		mixed := int16(sum / int32(channels))
		out[sample*2] = byte(mixed)
		out[sample*2+1] = byte(mixed >> 8)
	}
	return &model.AudioFrame{
		Data:              out,
		SampleRate:        frame.SampleRate,
		NumChannels:       1,
		SamplesPerChannel: samplesPerChannel,
	}, nil
}

func roomIOValidOpusSamples(samples uint32) uint32 {
	for _, valid := range []uint32{120, 240, 480, roomIOOpusFrameSamples} {
		if samples <= valid {
			return valid
		}
	}
	return roomIOOpusFrameSamples
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
	if rio.AgentSession != nil {
		rio.AgentSession.SetUserAwayTimerGate(nil)
	}
	rio.dropPausedAudioOutput()
	rio.mu.Lock()
	rio.closed = true
	rio.agentStatePublishSeq++
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
	rio.closeAgentTextStream()
	rio.releaseAudioSubscriptionWaiters()
	rio.finishPlayback(true, "")

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
