package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent/ivr"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/proto"
)

type GenerateReplyOpts struct {
	AllowInterruptions bool
}

// sendChatToPlayground tries multiple approaches to get a message
// into the Playground chat panel. We send all three approaches
// simultaneously to find out which one the Playground accepts.
func sendChatToPlayground(publisher MediaPublisher, msgID string, text string, senderIdentity string) {
	topic := "lk.chat"

	payload, _ := json.Marshal(map[string]interface{}{
		"id":           msgID,
		"message":      text,
		"timestamp":    time.Now().UnixMilli(),
		"generated_by": senderIdentity,
	})
	_ = publisher.PublishData(payload, topic, nil)
}

type AgentSessionOptions struct {
	AllowInterruptions            bool
	DiscardAudioIfUninterruptible bool
	MinInterruptionDuration       float64
	MinInterruptionWords          int
	MinEndpointingDelay           float64
	MaxEndpointingDelay           float64
	MaxToolSteps                  int
	UserAwayTimeout               float64
	FalseInterruptionTimeout      float64
	ResumeFalseInterruption       bool
	MinConsecutiveSpeechDelay     float64
	UseTTSAlignedTranscript       bool
	PreemptiveGeneration          bool
	AECWarmupDuration             float64
	SpeakingRate                  float64
	TranscriptRefreshRate         time.Duration
	LinkedParticipant             lksdk.Participant
	IVRDetection                  bool
}

type AgentSession struct {
	Options AgentSessionOptions

	ChatCtx   *llm.ChatContext
	Agent     AgentInterface
	STT       stt.STT
	VAD       vad.VAD
	LLM       llm.LLM
	TTS       tts.TTS
	Tools     []interface{}
	Assistant *PipelineAgent
	Room      *lksdk.Room

	Input  AgentInput
	Output AgentOutput

	MetricsCollector *telemetry.UsageCollector
	Timeline         *EventTimeline

	UserState  UserState
	AgentState AgentState

	// Transcript attribution — set by RoomIO when tracks are established.
	RemoteUserIdentity string
	RemoteTrackSID     string
	AgentTrackSID      string

	mu       sync.Mutex
	Activity *AgentActivity
	started  bool
	closing  bool

	transitionMu sync.Mutex
	transitionCancel context.CancelFunc

	// Event channels
	AgentStateChangedCh chan AgentStateChangedEvent
	UserStateChangedCh  chan UserStateChangedEvent

	clientEvents *ClientEventsDispatcher
	ivrActivity  *ivr.IVRActivity

	ctx    context.Context
	cancel context.CancelFunc
}

func (s *AgentSession) GetPublisher() interface {
	Identity() string
	PublishData(data []byte, topic string, destinationSIDs []string) error
} {
	return s.Output.Publisher
}

// transcriptionPacket wraps livekit.Transcription to implement the DataPacket interface.
type transcriptionPacket struct {
	t *livekit.Transcription
}

func (p *transcriptionPacket) ToProto() *livekit.DataPacket {
	return &livekit.DataPacket{
		Value: &livekit.DataPacket_Transcription{Transcription: p.t},
	}
}

func (s *AgentSession) OnAudioFrame(ctx context.Context, frame *model.AudioFrame) {
	s.mu.Lock()
	assistant := s.Assistant
	s.mu.Unlock()

	if assistant != nil {
		assistant.OnAudioFrame(ctx, frame)
	}
}

func NewAgentSession(agent AgentInterface, room *lksdk.Room, opts AgentSessionOptions) *AgentSession {
	baseAgent := agent.GetAgent()
	chatCtx := baseAgent.ChatCtx
	if chatCtx == nil {
		chatCtx = llm.NewChatContext()
		baseAgent.ChatCtx = chatCtx
	}

	tools := make([]interface{}, len(baseAgent.Tools))
	copy(tools, baseAgent.Tools)

	timeline := NewEventTimeline()

	if opts.SpeakingRate <= 0 {
		opts.SpeakingRate = 3.83
	}

	if opts.TranscriptRefreshRate <= 0 {
		opts.TranscriptRefreshRate = 50 * time.Millisecond
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &AgentSession{
		Agent:               agent,
		Room:                room,
		STT:                 baseAgent.STT,
		VAD:                 baseAgent.VAD,
		LLM:                 baseAgent.LLM,
		TTS:                 baseAgent.TTS,
		Options:             opts,
		ChatCtx:             chatCtx,
		Tools:               tools,
		Timeline:            timeline,
		AgentStateChangedCh: make(chan AgentStateChangedEvent, 10),
		UserStateChangedCh:  make(chan UserStateChangedEvent, 10),
		ctx:                 ctx,
		cancel:              cancel,
	}

	if chatCtx != nil {
		chatCtx.OnItemAdded = func(item llm.ChatItem) {
			if s.Timeline != nil {
				s.Timeline.AddEvent(&ConversationItemAddedEvent{
					Item:      item,
					CreatedAt: time.Now(),
				})
			}
		}
	}

	if room != nil {
		s.clientEvents = NewClientEventsDispatcher(room, s)
	}

	if opts.IVRDetection {
		s.ivrActivity = ivr.NewIVRActivity(s)
		s.Tools = append(s.Tools, s.ivrActivity.Tools()...)
		s.ivrActivity.Start()
	}

	return s
}

// SetRoom wires the LiveKit room to the session after connection.
// This initialises the ClientEventsDispatcher (RPC handlers, state broadcasting)
// so the Playground can discover the agent's audio track and state.
func (s *AgentSession) SetRoom(room *lksdk.Room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Room = room
	if room != nil && s.clientEvents == nil {
		s.clientEvents = NewClientEventsDispatcher(room, s)
	}
}

func (s *AgentSession) SetAudioOutput(out AudioOutput) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Output.Audio = out
	if out != nil {
		out.OnPlaybackStarted(func(ev PlaybackStartedEvent) {
			s.UpdateAgentState(AgentStateSpeaking)
		})
		out.OnPlaybackFinished(func(ev PlaybackFinishedEvent) {
			if !ev.Interrupted {
				s.UpdateAgentState(AgentStateListening)
			}
		})
	}
}

func (s *AgentSession) SetVideoOutput(out VideoOutput) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Output.Video = out
}

func (s *AgentSession) Start(ctx context.Context) error {
	s.mu.Lock()

	if s.started {
		s.mu.Unlock()
		return nil
	}

	if s.VAD == nil {
		s.VAD = vad.NewSimpleVAD(0.01)
	}

	// AudioRecognition consumes session.STT directly. Ensure it is stream-capable.
	if s.STT != nil && !s.STT.Capabilities().Streaming {
		s.STT = stt.NewStreamAdapter(s.STT, s.VAD)
	}

	// Keep base agent fields aligned with effective session components.
	if base := s.Agent.GetAgent(); base != nil {
		base.VAD = s.VAD
		base.STT = s.STT
	}

	s.Activity = NewAgentActivity(s.Agent, s)
	// Sync the activity onto the base *Agent so Agent.OnUserTurnCompleted
	// can reach it via a.activity (it guards on a.activity == nil).
	if base := s.Agent.GetAgent(); base != nil {
		base.activity = s.Activity
	}

	if s.Assistant == nil {
		s.Assistant = NewPipelineAgent(s.VAD, s.STT, s.LLM, s.TTS, s.ChatCtx)
	}

	if err := s.Assistant.Start(ctx, s); err != nil {
		s.Activity = nil
		s.mu.Unlock()
		return err
	}

	// Trigger periodic usage metrics reporting
	if s.MetricsCollector != nil {
		go s.reportUsageLoop(ctx)
	}

	s.started = true
	s.mu.Unlock()

	// Activity.Start() must be called AFTER releasing s.mu because it
	// synchronously calls UpdateUserState which also acquires s.mu.
	s.Activity.Start()

	go s.forwardAudioLoop(ctx)
	go s.forwardVideoLoop(ctx)

	s.UpdateAgentState(AgentStateListening)

	return nil
}

func (s *AgentSession) Close() error {
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()

	s.mu.Lock()
	if !s.started || s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	s.started = false
	activity := s.Activity
	assistant := s.Assistant
	s.mu.Unlock()

	s.cancel()

	if activity != nil {
		activity.Stop()
	}

	if assistant != nil {
		assistant.Stop()
	}

	if s.Timeline != nil {
		s.Timeline.AddEvent(&CloseEvent{
			Reason:    CloseReasonUserInitiated,
			CreatedAt: time.Now(),
		})
	}
	s.UpdateAgentState(AgentStateIdle)

	return nil
}

type TransitionActivityAction string

const (
	TransitionActivityClose  TransitionActivityAction = "close"
	TransitionActivityPause  TransitionActivityAction = "pause"
	TransitionActivityStart  TransitionActivityAction = "start"
	TransitionActivityResume TransitionActivityAction = "resume"
)

type UpdateAgentOpts struct {
	PreviousActivity TransitionActivityAction
	NewActivity      TransitionActivityAction
}

func (s *AgentSession) UpdateAgent(agent AgentInterface, opts *UpdateAgentOpts) error {
	if opts == nil {
		opts = &UpdateAgentOpts{
			PreviousActivity: TransitionActivityClose,
			NewActivity:      TransitionActivityStart,
		}
	}

	s.transitionMu.Lock()
	if s.transitionCancel != nil {
		s.transitionCancel()
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.transitionCancel = cancel
	s.transitionMu.Unlock()

	// Ensure we release the transition lock when done
	defer func() {
		s.transitionMu.Lock()
		if reflect.ValueOf(s.transitionCancel).Pointer() == reflect.ValueOf(cancel).Pointer() {
			s.transitionCancel = nil
		}
		s.transitionMu.Unlock()
	}()

	s.mu.Lock()
	if !s.started || s.closing {
		s.mu.Unlock()
		return fmt.Errorf("session not started or closing")
	}
	oldActivity := s.Activity

	var newActivity *AgentActivity
	if opts.NewActivity == TransitionActivityStart {
		if agent.GetActivity() != nil && (agent.GetActivity() != oldActivity || opts.PreviousActivity != TransitionActivityClose) {
			s.mu.Unlock()
			return fmt.Errorf("cannot start agent: an activity is already running")
		}
		newActivity = NewAgentActivity(agent, s)
	} else if opts.NewActivity == TransitionActivityResume {
		if agent.GetActivity() == nil {
			s.mu.Unlock()
			return fmt.Errorf("cannot resume agent: no existing active activity to resume")
		}
		newActivity = agent.GetActivity()
	}

	s.Agent = agent
	s.Activity = newActivity
	s.mu.Unlock()

	if oldActivity != nil && oldActivity != newActivity {
		if opts.PreviousActivity == TransitionActivityClose {
			// Drain with the transition context to allow cancellation
			if err := oldActivity.Drain(ctx); err != nil {
				return err
			}
			oldActivity.AClose()
		} else if opts.PreviousActivity == TransitionActivityPause {
			_ = oldActivity.Pause()
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if opts.NewActivity == TransitionActivityStart {
		newActivity.Start()
	} else if opts.NewActivity == TransitionActivityResume {
		_ = newActivity.Resume()
	}

	return nil
}

func (s *AgentSession) ClearUserTurn() {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()
	if activity != nil {
		activity.ClearUserTurn()
	}
}

func (s *AgentSession) CommitUserTurn(opts *CommitUserTurnOpts) {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()
	if activity != nil {
		activity.CommitUserTurn(opts)
	}
}

func (s *AgentSession) Pause() error {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()
	if activity != nil {
		return activity.Pause()
	}
	return nil
}

func (s *AgentSession) Resume() error {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()
	if activity != nil {
		return activity.Resume()
	}
	return nil
}

func (s *AgentSession) UpdateOptions(opts AgentSessionOptions) {
	s.mu.Lock()
	s.Options = opts
	activity := s.Activity
	s.mu.Unlock()
	if activity != nil {
		activity.UpdateOptions(opts)
	}
}


func (s *AgentSession) reportUsageLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.MetricsCollector != nil && s.ChatCtx != nil {
				summary := s.MetricsCollector.GetSummary()
				s.ChatCtx.Append(&llm.MetricsReport{Usage: summary})
				
				if s.Timeline != nil {
					s.Timeline.AddEvent(&MetricsCollectedEvent{
						Metrics:   &summary,
						CreatedAt: time.Now(),
					})
				}
			}
		}
	}
}

func (s *AgentSession) UpdateAgentState(state AgentState) {
	s.mu.Lock()
	oldState := s.AgentState
	s.AgentState = state
	s.mu.Unlock()

	if oldState != state {
		logger.Logger.Debugw("Agent state changed", "old", oldState, "new", state)
		if s.Timeline != nil {
			s.Timeline.AddEvent(&AgentStateChangedEvent{
				OldState:  oldState,
				NewState:  state,
				CreatedAt: time.Now(),
			})
		}

		if s.clientEvents != nil {
			s.clientEvents.DispatchAgentState(state)
		}

		if s.ivrActivity != nil {
			s.ivrActivity.OnAgentStateChanged(ivr.AgentState(oldState), ivr.AgentState(state))
		}

		// GAP-008: Use abstracted Publisher to update attributes
		if s.Output.Publisher != nil {
			_ = s.Output.Publisher.SetAttributes(map[string]string{
				"lk.agent.state": string(state),
			})
		}

		select {
		case s.AgentStateChangedCh <- AgentStateChangedEvent{
			OldState:  oldState,
			NewState:  state,
			CreatedAt: time.Now(),
		}:
		default:
			// Channel full, ignore
		}
	}
}

func (s *AgentSession) UpdateUserState(state UserState) {
	s.mu.Lock()
	oldState := s.UserState
	s.UserState = state
	s.mu.Unlock()

	if oldState != state {
		logger.Logger.Debugw("User state changed", "old", oldState, "new", state)
		if s.Timeline != nil {
			s.Timeline.AddEvent(&UserStateChangedEvent{
				OldState:  oldState,
				NewState:  state,
				CreatedAt: time.Now(),
			})
		}
		
		if s.clientEvents != nil {
			s.clientEvents.DispatchUserState(state)
		}

		if s.ivrActivity != nil {
			s.ivrActivity.OnUserStateChanged(ivr.UserState(oldState), ivr.UserState(state))
		}

		select {
		case s.UserStateChangedCh <- UserStateChangedEvent{
			OldState:  oldState,
			NewState:  state,
			CreatedAt: time.Now(),
		}:
		default:
			// Channel full, ignore
		}
	}
}

func GenerateTypedReply[T any](ctx context.Context, s *AgentSession, userInput string, opts *GenerateReplyOpts) (*RunResult[T], error) {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()

	if activity == nil {
		return nil, fmt.Errorf("agent activity not started")
	}

	// Trigger the pipeline
	logger.Logger.Infow("Generating reply", "userInput", userInput)

	allowInterruptions := s.Options.AllowInterruptions
	if opts != nil {
		allowInterruptions = opts.AllowInterruptions
	}

	// Create a speech handle
	handle := NewSpeechHandle(allowInterruptions, DefaultInputDetails())

	participantID := ""
	if s.Room != nil && s.Room.LocalParticipant != nil {
		participantID = s.Room.LocalParticipant.Identity()
	}

	if s.Timeline != nil {
		s.Timeline.AddEvent(&SpeechCreatedEvent{
			UserInitiated: true,
			Source:        "generate_reply",
			SpeechHandle:  handle,
			ParticipantID: participantID,
			CreatedAt:     time.Now(),
		})
	}

	// Create run result and watch the new handle
	runResult := NewRunResult[T](s.ChatCtx)
	runResult.WatchHandle(ctx, handle)
	handle.RunResult = runResult

	// Add user message to ChatContext if provided
	if userInput != "" {
		s.ChatCtx.Append(&llm.ChatMessage{
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: userInput},
			},
			CreatedAt: time.Now(),
		})
	}

	// Schedule the speech
	err := activity.ScheduleSpeech(handle, SpeechPriorityNormal, false)
	if err != nil {
		return nil, err
	}

	return runResult, nil
}

func (s *AgentSession) GenerateReply(ctx context.Context, userInput string, allowInterruptions bool) (any, error) {
	return GenerateTypedReply[any](ctx, s, userInput, &GenerateReplyOpts{AllowInterruptions: allowInterruptions})
}

func (s *AgentSession) Say(text string, allowInterruptions bool) (*SpeechHandle, error) {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()

	if activity == nil {
		return nil, fmt.Errorf("session activity not started")
	}

	handle := NewSpeechHandle(allowInterruptions, InputDetails{Modality: "text"})
	handle.ManualText = text

	// Add the manual speech to the chat context so the LLM remains aware of its own output
	s.ChatCtx.Append(&llm.ChatMessage{
		Role: llm.ChatRoleAssistant,
		Content: []llm.ChatContent{
			{Text: text},
		},
		CreatedAt: time.Now(),
	})

	err := activity.ScheduleSpeech(handle, SpeechPriorityNormal, false)
	if err != nil {
		return nil, err
	}

	return handle, nil
}

func (s *AgentSession) forwardAudioLoop(ctx context.Context) {
	if s.Input.Audio == nil {
		logger.Logger.Infow("[STT-PIPE] forwardAudioLoop: Input.Audio is nil — no audio will be forwarded")
		return
	}
	logger.Logger.Infow("[STT-PIPE] forwardAudioLoop started", "audioInput", s.Input.Audio.Label())
	stream := s.Input.Audio.Stream()

	var warmupUntil time.Time
	if s.Options.AECWarmupDuration > 0 {
		warmupUntil = time.Now().Add(time.Duration(s.Options.AECWarmupDuration * float64(time.Second)))
	}

	var frameCount int
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-stream:
			if !ok {
				logger.Logger.Infow("[STT-PIPE] forwardAudioLoop: audio stream closed")
				return
			}
			if !warmupUntil.IsZero() && time.Now().Before(warmupUntil) {
				continue
			}
			s.mu.Lock()
			activity := s.Activity
			s.mu.Unlock()
			if activity == nil {
				continue
			}
			frameCount++
			if frameCount%100 == 1 {
				logger.Logger.Infow("[STT-PIPE] forwardAudioLoop forwarding frames to activity", "frameCount", frameCount)
			}
			_ = activity.PushAudio(frame)
		}
	}
}

func (s *AgentSession) forwardVideoLoop(ctx context.Context) {
	if s.Input.Video == nil {
		return
	}
	stream := s.Input.Video.Stream()
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-stream:
			if !ok {
				return
			}
			s.mu.Lock()
			activity := s.Activity
			s.mu.Unlock()
			if activity != nil {
				_ = activity.PushVideo(frame)
			}
		}
	}
}

func (s *AgentSession) TimelineSnapshot() []*AgentEvent {
	if s == nil || s.Timeline == nil {
		return nil
	}
	return s.Timeline.Snapshot()
}

func (s *AgentSession) Interrupt(ctx context.Context) error {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()

	if activity != nil {
		return activity.Interrupt(true)
	}
	return nil
}

func (s *AgentSession) SetRemoteUserIdentity(identity string) {
	s.mu.Lock()
	s.RemoteUserIdentity = identity
	s.mu.Unlock()
}

func (s *AgentSession) SetRemoteTrackSID(sid string) {
	s.mu.Lock()
	s.RemoteTrackSID = sid
	s.mu.Unlock()
}

func (s *AgentSession) SetAgentTrackSID(sid string) {
	s.mu.Lock()
	s.AgentTrackSID = sid
	s.mu.Unlock()
}

func (s *AgentSession) GetAgentTrackSID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.AgentTrackSID
}

// PublishUserTranscript publishes the user's STT transcript to the Playground.
// Sends both a ChatMessage (chat panel) and a Transcription packet (transcript overlay).
func (s *AgentSession) PublishUserTranscript(text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	userIdentity := s.RemoteUserIdentity
	userTrackSID := s.RemoteTrackSID
	s.mu.Unlock()

	if s.Output.Publisher == nil {
		return
	}

	now := time.Now()
	sendChatToPlayground(s.Output.Publisher, fmt.Sprintf("usr-%d", now.UnixNano()), text, userIdentity)

	// Transcription packet — displayed as real-time subtitle overlay.
	nowMs := uint64(now.UnixMilli())
	tpkt := &livekit.Transcription{
		TranscribedParticipantIdentity: userIdentity,
		TrackId:                        userTrackSID,
		Segments: []*livekit.TranscriptionSegment{{
			Id:        fmt.Sprintf("seg-usr-%d", now.UnixNano()),
			Text:      text,
			StartTime: nowMs,
			EndTime:   nowMs,
			Final:     true,
		}},
	}
	
	// GAP-008: Use abstracted Publisher to publish transcription packet
	data, _ := proto.Marshal(tpkt)
	if err := s.Output.Publisher.PublishData(data, "lk.transcription", nil); err != nil {
		logger.Logger.Warnw("Failed to publish user transcription", err)
	} else {
		logger.Logger.Debugw("User transcription published", "identity", userIdentity, "text", text)
	}
}

// PublishAgentTranscript publishes the agent's LLM response to the Playground.
// Sends both a ChatMessage (chat panel) and a Transcription packet (transcript overlay).
func (s *AgentSession) PublishAgentTranscript(text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	agentTrackSID := s.AgentTrackSID
	s.mu.Unlock()

	if s.Output.Publisher == nil {
		return
	}

	agentIdentity := s.Output.Publisher.Identity()
	now := time.Now()
	sendChatToPlayground(s.Output.Publisher, fmt.Sprintf("agt-%d", now.UnixNano()), text, agentIdentity)

	// Transcription packet — displayed as real-time subtitle overlay.
	nowMs := uint64(now.UnixMilli())
	tpkt := &livekit.Transcription{
		TranscribedParticipantIdentity: agentIdentity,
		TrackId:                        agentTrackSID,
		Segments: []*livekit.TranscriptionSegment{{
			Id:        fmt.Sprintf("seg-agt-%d", now.UnixNano()),
			Text:      text,
			StartTime: nowMs,
			EndTime:   nowMs,
			Final:     true,
		}},
	}
	
	data, _ := proto.Marshal(tpkt)
	if err := s.Output.Publisher.PublishData(data, "lk.transcription", nil); err != nil {
		logger.Logger.Warnw("Failed to publish agent transcription", err)
	} else {
		logger.Logger.Debugw("Agent transcription published", "text", text)
	}
}

func (s *AgentSession) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	activity := s.Activity
	assistant := s.Assistant
	s.mu.Unlock()

	if activity != nil {
		activity.PauseScheduling()
		if err := activity.Drain(ctx); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			logger.Logger.Errorw("failed draining agent activity", err)
		}
		activity.Stop()
	}

	if assistant != nil {
		assistant.Stop()
	}

	if s.Timeline != nil {
		s.Timeline.AddEvent(&CloseEvent{
			Reason:    CloseReasonJobShutdown,
			CreatedAt: time.Now(),
		})
	}

	s.mu.Lock()
	if s.Activity == activity {
		s.Activity = nil
	}
	s.mu.Unlock()

	return nil
}

