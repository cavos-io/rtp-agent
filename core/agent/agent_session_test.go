package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	livekitlogger "github.com/livekit/protocol/logger"
)

func TestAgentSessionGenerateReplyReturnsScheduledSpeechHandle(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReply(context.Background(), "hello")

	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	if handle == nil {
		t.Fatal("GenerateReply handle = nil, want speech handle")
	}
	if !handle.IsScheduled() {
		t.Fatal("GenerateReply returned unscheduled handle")
	}
	if !handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = false, want session default true")
	}
	if got, want := handle.InputDetails.Modality, "text"; got != want {
		t.Fatalf("handle.InputDetails.Modality = %q, want %q", got, want)
	}
}

func TestAgentSessionGenerateReplyUsesAgentAllowInterruptionsDefault(t *testing.T) {
	agent := NewAgent("test")
	agent.AllowInterruptions = true
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Options.AllowInterruptions = false
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReply(context.Background(), "hello")

	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	if !handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = false, want agent default true")
	}
}

func TestAgentSessionGenerateReplyAgentAllowInterruptionsCanDisableSessionDefault(t *testing.T) {
	agent := NewAgent("test")
	agent.AllowInterruptions = false
	agent.AllowInterruptionsSet = true
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReply(context.Background(), "hello")

	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	if handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = true, want agent default false")
	}
}

func TestAgentSessionStartConfiguresTTSStreamPacer(t *testing.T) {
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		TTSStreamPacer: &tts.SentenceStreamPacerOptions{
			MinRemainingAudio: 25 * time.Millisecond,
			MaxTextLength:     42,
		},
	})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	if session.Assistant == nil {
		t.Fatal("Assistant is nil")
	}
	pipeline, ok := session.Assistant.(*PipelineAgent)
	if !ok {
		t.Fatalf("Assistant = %T, want *PipelineAgent", session.Assistant)
	}
	if pipeline.ttsStreamPacer == nil {
		t.Fatal("Assistant ttsStreamPacer is nil")
	}
	if got := pipeline.ttsStreamPacer.MinRemainingAudio; got != 25*time.Millisecond {
		t.Fatalf("MinRemainingAudio = %v, want 25ms", got)
	}
	if got := pipeline.ttsStreamPacer.MaxTextLength; got != 42 {
		t.Fatalf("MaxTextLength = %d, want 42", got)
	}
}

func TestAgentSessionStartCreatesMultimodalAssistantWithRealtimeModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.RealtimeModel = &fakeRealtimeModel{session: &fakeRealtimeSession{}}
	session.VAD = &fakePipelineVAD{}
	session.STT = &fakePipelineSTT{}
	session.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session.TTS = &fakePipelineTTS{}

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())

	if _, ok := session.Assistant.(*MultimodalAgent); !ok {
		t.Fatalf("Assistant = %T, want *MultimodalAgent", session.Assistant)
	}
}

func TestAgentSessionStartEnablesIVRDetectionActivity(t *testing.T) {
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		IVRDetection: true,
	})
	session.Assistant = &fakeSessionAssistant{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	if session.ivrActivity == nil {
		t.Fatal("ivrActivity = nil, want configured IVR activity")
	}
}

func TestAgentSessionStartWithOptionsCapturesOnEnterSpeechRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent := &onEnterSayAgent{Agent: NewAgent("test")}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.session = session
	session.Assistant = &doneScheduledSpeechAssistant{}

	result, err := session.StartWithOptions(ctx, StartOptions{CaptureRun: true})
	if err != nil {
		t.Fatalf("StartWithOptions error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("StartWithOptions result = nil, want captured RunResult")
	}
	if err := result.Wait(ctx); err != nil {
		t.Fatalf("captured RunResult did not complete: %v", err)
	}
	events := result.Events()
	if len(events) != 1 {
		t.Fatalf("RunResult events length = %d, want on-enter assistant message", len(events))
	}
	msgEvent, ok := events[0].(*ChatMessageEvent)
	if !ok {
		t.Fatalf("events[0] = %T, want *ChatMessageEvent", events[0])
	}
	if msgEvent.Item.TextContent() != "hello from on enter" {
		t.Fatalf("message text = %q, want hello from on enter", msgEvent.Item.TextContent())
	}
}

func TestAgentSessionIVRDetectionGeneratesReplyAfterSilence(t *testing.T) {
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		IVRDetection:       true,
		IVRSilenceDuration: 10 * time.Millisecond,
	})
	session.Assistant = &fakeSessionAssistant{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	session.UpdateAgentState(AgentStateIdle)
	session.UpdateUserState(UserStateListening)

	select {
	case ev := <-session.SpeechCreatedEvents():
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated source = %q, want generate_reply", ev.Source)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for IVR silence generated reply")
	}
}

func TestAgentSessionOnVideoFrameSamplesAndForwardsToVideoAssistant(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	assistant := &fakeVideoSessionAssistant{}
	session.Assistant = assistant

	frame := &images.VideoFrame{}
	session.OnVideoFrame(context.Background(), frame)

	if assistant.videoFrames != 1 {
		t.Fatalf("videoFrames = %d, want first sampled frame forwarded", assistant.videoFrames)
	}
}

type fakeSessionAssistant struct{}

func (f *fakeSessionAssistant) Start(context.Context, *AgentSession) error { return nil }
func (f *fakeSessionAssistant) OnAudioFrame(context.Context, *model.AudioFrame) {
}
func (f *fakeSessionAssistant) SetPublishAudio(func(frame *model.AudioFrame) error) {
}

type doneScheduledSpeechAssistant struct {
	fakeSessionAssistant
}

func (d *doneScheduledSpeechAssistant) OnSpeechScheduled(ctx context.Context, speech *SpeechHandle) {
	speech.MarkDone()
}

type onEnterSayAgent struct {
	*Agent
	session *AgentSession
}

func (a *onEnterSayAgent) OnEnter() {
	_, _ = a.session.Say(context.Background(), "hello from on enter")
}

type fakeCloseableSessionAssistant struct {
	fakeSessionAssistant
	closed int
}

func (f *fakeCloseableSessionAssistant) Close() error {
	f.closed++
	return nil
}

type fakeVideoSessionAssistant struct {
	fakeSessionAssistant
	videoFrames int
}

func (f *fakeVideoSessionAssistant) OnVideoFrame(ctx context.Context, frame *images.VideoFrame) {
	f.videoFrames++
}

func TestAgentSessionStartStartsConfiguredAvatar(t *testing.T) {
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}
	avatar := &fakeAvatarProvider{}
	baseAgent.Avatar = avatar

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	if avatar.startCalls != 1 {
		t.Fatalf("avatar startCalls = %d, want 1", avatar.startCalls)
	}
}

func TestAgentSessionStartReturnsAvatarStartError(t *testing.T) {
	errAvatar := errors.New("avatar start failed")
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}
	baseAgent.Avatar = &fakeAvatarProvider{startErr: errAvatar}

	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})

	err := session.Start(context.Background())

	if !errors.Is(err, errAvatar) {
		t.Fatalf("Start error = %v, want avatar error", err)
	}
	if session.started {
		t.Fatal("session started after avatar start error")
	}
}

func TestAgentSessionBackgroundAudioLifecycleWithoutRoom(t *testing.T) {
	player := NewBackgroundAudioPlayer(nil, nil)
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		BackgroundAudio: player,
	})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if player.mixerTaskCancel != nil {
		t.Fatal("background audio started without a room")
	}

	session.UpdateAgentState(AgentStateSpeaking)
	if got := player.targetVolume; got != 0.2 {
		t.Fatalf("targetVolume after speaking = %v, want ducked volume 0.2", got)
	}

	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
}

func TestNewAgentSessionInitializesUserStateListening(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if session.UserState != UserStateListening {
		t.Fatalf("UserState = %q, want %q", session.UserState, UserStateListening)
	}
}

func TestNewAgentSessionInitializesAgentStateInitializing(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if session.AgentState != AgentStateInitializing {
		t.Fatalf("AgentState = %q, want %q", session.AgentState, AgentStateInitializing)
	}
}

func TestNewAgentSessionInitializesUsageCollector(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if session.MetricsCollector == nil {
		t.Fatal("MetricsCollector = nil, want default usage collector")
	}
}

func TestAgentSessionUserdataRequiresValue(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	value, err := session.Userdata()

	if value != nil {
		t.Fatalf("Userdata value = %#v, want nil when unset", value)
	}
	if !errors.Is(err, ErrAgentSessionUserdataNotSet) {
		t.Fatalf("Userdata error = %v, want ErrAgentSessionUserdataNotSet", err)
	}
}

func TestAgentSessionUserdataCanBeSet(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	want := map[string]string{"customer_id": "cust_123"}

	session.SetUserdata(want)
	value, err := session.Userdata()

	if err != nil {
		t.Fatalf("Userdata error = %v, want nil", err)
	}
	got, ok := value.(map[string]string)
	if !ok {
		t.Fatalf("Userdata value = %T, want map[string]string", value)
	}
	if got["customer_id"] != want["customer_id"] {
		t.Fatalf("Userdata customer_id = %q, want %q", got["customer_id"], want["customer_id"])
	}
}

func TestNewAgentSessionAppliesReferenceOptionDefaults(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	opts := session.Options
	if !opts.AllowInterruptions {
		t.Fatal("AllowInterruptions = false, want default true")
	}
	if !opts.DiscardAudioIfUninterruptible {
		t.Fatal("DiscardAudioIfUninterruptible = false, want default true")
	}
	if opts.MinInterruptionDuration != 0.5 {
		t.Fatalf("MinInterruptionDuration = %v, want 0.5", opts.MinInterruptionDuration)
	}
	if opts.MinInterruptionWords != 0 {
		t.Fatalf("MinInterruptionWords = %d, want 0", opts.MinInterruptionWords)
	}
	if opts.MinEndpointingDelay != 0.5 {
		t.Fatalf("MinEndpointingDelay = %v, want 0.5", opts.MinEndpointingDelay)
	}
	if opts.MaxEndpointingDelay != 3.0 {
		t.Fatalf("MaxEndpointingDelay = %v, want 3.0", opts.MaxEndpointingDelay)
	}
	if opts.FalseInterruptionTimeout != 2.0 {
		t.Fatalf("FalseInterruptionTimeout = %v, want 2.0", opts.FalseInterruptionTimeout)
	}
	if !opts.ResumeFalseInterruption {
		t.Fatal("ResumeFalseInterruption = false, want default true")
	}
	if opts.UserAwayTimeout != 15.0 {
		t.Fatalf("UserAwayTimeout = %v, want 15.0", opts.UserAwayTimeout)
	}
	if !opts.PreemptiveGeneration {
		t.Fatal("PreemptiveGeneration = false, want default true")
	}
	if opts.AECWarmupDuration != 3.0 {
		t.Fatalf("AECWarmupDuration = %v, want 3.0", opts.AECWarmupDuration)
	}
	if opts.SessionCloseTranscriptTimeout != 2.0 {
		t.Fatalf("SessionCloseTranscriptTimeout = %v, want 2.0", opts.SessionCloseTranscriptTimeout)
	}
}

func TestAgentSessionGenerateReplyEmitsSpeechCreatedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)
	before := time.Now()

	handle, err := session.GenerateReply(context.Background(), "hello")

	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		if ev.GetType() != "speech_created" {
			t.Fatalf("event type = %q, want speech_created", ev.GetType())
		}
		if ev.SpeechHandle != handle {
			t.Fatalf("SpeechHandle = %#v, want returned handle", ev.SpeechHandle)
		}
		if !ev.UserInitiated {
			t.Fatal("UserInitiated = false, want true for GenerateReply")
		}
		if ev.Source != "generate_reply" {
			t.Fatalf("Source = %q, want generate_reply", ev.Source)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive generate reply speech")
	}
}

func TestAgentSessionSayReturnsScheduledSpeechHandle(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.Say(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}
	if handle == nil {
		t.Fatal("Say handle = nil, want speech handle")
	}
	if !handle.IsScheduled() {
		t.Fatal("Say returned unscheduled handle")
	}
	if !handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = false, want session default true")
	}
	if got, want := handle.InputDetails.Modality, "text"; got != want {
		t.Fatalf("handle.InputDetails.Modality = %q, want %q", got, want)
	}
}

func TestAgentSessionSayUsesAgentAllowInterruptionsDefault(t *testing.T) {
	agent := NewAgent("test")
	agent.AllowInterruptions = true
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Options.AllowInterruptions = false
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.Say(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}
	if !handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = false, want agent default true")
	}
}

func TestAgentSessionSayAgentAllowInterruptionsCanDisableSessionDefault(t *testing.T) {
	agent := NewAgent("test")
	agent.AllowInterruptions = false
	agent.AllowInterruptionsSet = true
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.Say(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}
	if handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = true, want agent default false")
	}
}

func TestAgentSessionSayEmitsSpeechCreatedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	before := time.Now()

	handle, err := session.Say(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		if ev.GetType() != "speech_created" {
			t.Fatalf("event type = %q, want speech_created", ev.GetType())
		}
		if ev.SpeechHandle != handle {
			t.Fatalf("SpeechHandle = %#v, want returned handle", ev.SpeechHandle)
		}
		if !ev.UserInitiated {
			t.Fatal("UserInitiated = false, want true for Say")
		}
		if ev.Source != "say" {
			t.Fatalf("Source = %q, want say", ev.Source)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive say speech")
	}
}

func TestAgentSessionSayAddsAssistantTextToChatContextByDefault(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	if _, err := session.Say(context.Background(), "hello from agent"); err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}

	if len(session.ChatCtx.Items) != 1 {
		t.Fatalf("ChatCtx.Items length = %d, want 1", len(session.ChatCtx.Items))
	}
	msg, ok := session.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("ChatCtx item type = %T, want *llm.ChatMessage", session.ChatCtx.Items[0])
	}
	if msg.Role != llm.ChatRoleAssistant || msg.TextContent() != "hello from agent" {
		t.Fatalf("ChatCtx message = %#v, want assistant message with text", msg)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		if ev.Item != msg {
			t.Fatalf("ConversationItemAdded item = %#v, want committed assistant message", ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive say text")
	}
}

func TestAgentSessionSayOptionsOverrideInterruptionsAndChatContext(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	allowInterruptions := false
	addToChatContext := false

	handle, err := session.SayWithOptions(context.Background(), SayOptions{
		Text:               "private aside",
		AllowInterruptions: &allowInterruptions,
		AddToChatContext:   &addToChatContext,
	})

	if err != nil {
		t.Fatalf("SayWithOptions error = %v, want nil", err)
	}
	if handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = true, want per-call false override")
	}
	if len(session.ChatCtx.Items) != 0 {
		t.Fatalf("ChatCtx.Items length = %d, want 0 when AddToChatContext is false", len(session.ChatCtx.Items))
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		t.Fatalf("ConversationItemAdded event = %#v, want none when AddToChatContext is false", ev)
	default:
	}
}

func TestAgentSessionSayWatchesActiveRunState(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	result := NewRunResult(session.ChatCtx)
	session.runState = result

	handle, err := session.SayWithOptions(context.Background(), SayOptions{
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("SayWithOptions error = %v, want nil", err)
	}
	if result.Done() {
		t.Fatal("run result marked done before watched say speech completed")
	}

	handle.MarkDone()

	if !result.Done() {
		t.Fatal("run result not marked done after say speech completed")
	}
}

func TestAgentSessionGenerateReplyWatchesActiveRunState(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	result := NewRunResult(session.ChatCtx)
	session.runState = result

	handle, err := session.GenerateReply(context.Background(), "hello")
	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	if result.Done() {
		t.Fatal("run result marked done before watched generated speech completed")
	}

	handle.MarkDone()

	if !result.Done() {
		t.Fatal("run result not marked done after generated speech completed")
	}
}

func TestAgentSessionEmitErrorEmitsTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	cause := errors.New("provider failed")
	source := "stt"
	before := time.Now()

	session.EmitError(ErrorEvent{Error: cause, Source: source})

	select {
	case ev := <-session.ErrorEvents():
		if ev.GetType() != "error" {
			t.Fatalf("event type = %q, want error", ev.GetType())
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want %v", ev.Error, cause)
		}
		if ev.Source != source {
			t.Fatalf("Source = %#v, want %q", ev.Source, source)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive error event")
	}
}

func TestAgentSessionRecordsEmittedEvents(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	cause := errors.New("provider failed")

	session.EmitError(ErrorEvent{Error: cause, Source: "llm"})

	events := session.RecordedEvents()
	if len(events) != 1 {
		t.Fatalf("RecordedEvents length = %d, want 1", len(events))
	}
	ev, ok := events[0].(*ErrorEvent)
	if !ok {
		t.Fatalf("RecordedEvents[0] = %T, want *ErrorEvent", events[0])
	}
	if !errors.Is(ev.Error, cause) || ev.Source != "llm" {
		t.Fatalf("recorded error event = %#v, want original error/source", ev)
	}
}

func TestAgentSessionRecordedEventsReturnsCopy(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.EmitError(ErrorEvent{Error: errors.New("failed"), Source: "llm"})

	events := session.RecordedEvents()
	events[0] = nil

	if session.RecordedEvents()[0] == nil {
		t.Fatal("RecordedEvents returned mutable backing storage")
	}
}

func TestAgentSessionRecordsStateChangedEvents(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	session.UpdateAgentState(AgentStateThinking)
	session.UpdateUserState(UserStateSpeaking)

	events := session.RecordedEvents()
	if len(events) != 2 {
		t.Fatalf("RecordedEvents length = %d, want 2", len(events))
	}
	if ev, ok := events[0].(*AgentStateChangedEvent); !ok || ev.NewState != AgentStateThinking {
		t.Fatalf("RecordedEvents[0] = %#v, want agent state changed to thinking", events[0])
	}
	if ev, ok := events[1].(*UserStateChangedEvent); !ok || ev.NewState != UserStateSpeaking {
		t.Fatalf("RecordedEvents[1] = %#v, want user state changed to speaking", events[1])
	}
}

func TestAgentSessionEmitAgentFalseInterruptionEmitsTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	before := time.Now()

	session.EmitAgentFalseInterruption(AgentFalseInterruptionEvent{Resumed: true})

	select {
	case ev := <-session.AgentFalseInterruptionEvents():
		if ev.GetType() != "agent_false_interruption" {
			t.Fatalf("event type = %q, want agent_false_interruption", ev.GetType())
		}
		if !ev.Resumed {
			t.Fatal("Resumed = false, want true")
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("AgentFalseInterruptionEvents did not receive event")
	}
}

func TestAgentSessionEmitUserTurnExceededEmitsTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	before := time.Now()

	session.EmitUserTurnExceeded(UserTurnExceededEvent{
		Transcript:            "latest words",
		AccumulatedTranscript: "all words",
		AccumulatedWordCount:  2,
		Duration:              3 * time.Second,
	})

	select {
	case ev := <-session.UserTurnExceededEvents():
		if ev.GetType() != "user_turn_exceeded" {
			t.Fatalf("event type = %q, want user_turn_exceeded", ev.GetType())
		}
		if ev.Transcript != "latest words" || ev.AccumulatedTranscript != "all words" {
			t.Fatalf("event transcript fields = %#v, want latest/all words", ev)
		}
		if ev.AccumulatedWordCount != 2 || ev.Duration != 3*time.Second {
			t.Fatalf("event accumulation fields = %#v, want 2 words and 3s", ev)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("UserTurnExceededEvents did not receive event")
	}
}

func TestAgentSessionEmitUserTurnExceededDispatchesAgentHook(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	session.EmitUserTurnExceeded(UserTurnExceededEvent{Transcript: "extended user turn"})

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("user turn exceeded hook did not schedule cut-in speech")
		case <-ticker.C:
			session.activity.queueMu.Lock()
			var handle *SpeechHandle
			if len(session.activity.speechQueue) > 0 {
				handle = session.activity.speechQueue[0].speech
			}
			session.activity.queueMu.Unlock()
			if handle == nil {
				continue
			}
			if handle.AllowInterruptions {
				t.Fatal("handle.AllowInterruptions = true, want false for exceeded-turn cut-in")
			}
			if handle.Generation.ToolChoice != "none" {
				t.Fatalf("ToolChoice = %#v, want none", handle.Generation.ToolChoice)
			}
			return
		}
	}
}

func TestAgentSessionEmitOverlappingSpeechEmitsTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	overlapStartedAt := time.Now().Add(-100 * time.Millisecond)
	before := time.Now()

	session.EmitOverlappingSpeech(OverlappingSpeechEvent{
		IsInterruption:   true,
		DetectionDelay:   100 * time.Millisecond,
		OverlapStartedAt: &overlapStartedAt,
		Probability:      0.8,
	})

	select {
	case ev := <-session.OverlappingSpeechEvents():
		if ev.GetType() != "overlapping_speech" {
			t.Fatalf("event type = %q, want overlapping_speech", ev.GetType())
		}
		if !ev.IsInterruption || ev.DetectionDelay != 100*time.Millisecond || ev.Probability != 0.8 {
			t.Fatalf("event fields = %#v, want interruption with delay/probability", ev)
		}
		if ev.OverlapStartedAt == nil || !ev.OverlapStartedAt.Equal(overlapStartedAt) {
			t.Fatalf("OverlapStartedAt = %#v, want %v", ev.OverlapStartedAt, overlapStartedAt)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
		if ev.DetectedAt.Before(before) || ev.DetectedAt.IsZero() {
			t.Fatalf("DetectedAt = %v, want timestamp after %v", ev.DetectedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("OverlappingSpeechEvents did not receive event")
	}
}

func TestAgentSessionGenerateReplyAddsUserInputToChatContext(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	if _, err := session.GenerateReply(context.Background(), "hello"); err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}

	if len(session.ChatCtx.Items) != 1 {
		t.Fatalf("ChatCtx.Items length = %d, want 1", len(session.ChatCtx.Items))
	}
	msg, ok := session.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("ChatCtx item type = %T, want *llm.ChatMessage", session.ChatCtx.Items[0])
	}
	if msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello" {
		t.Fatalf("ChatCtx message = %#v, want user message with text hello", msg)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		if ev.Item != msg {
			t.Fatalf("ConversationItemAdded item = %#v, want committed user message", ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive generated user input")
	}
}

func TestAgentSessionGenerateReplyOptionsAcceptUserMessage(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	userMessage := &llm.ChatMessage{
		ID:        "custom_user",
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "custom input"}},
		CreatedAt: time.Now(),
	}

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserMessage:   userMessage,
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.UserMessage != userMessage {
		t.Fatalf("handle.Generation.UserMessage = %#v, want supplied user message", handle.Generation.UserMessage)
	}
	if len(session.ChatCtx.Items) != 1 || session.ChatCtx.Items[0] != userMessage {
		t.Fatalf("ChatCtx.Items = %#v, want supplied user message", session.ChatCtx.Items)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		if ev.Item != userMessage {
			t.Fatalf("ConversationItemAdded item = %#v, want supplied user message", ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive supplied user message")
	}
}

func TestAgentSessionGenerateReplyOptionsPreferUserMessageOverString(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	userMessage := &llm.ChatMessage{
		ID:        "custom_user",
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "custom input"}},
		CreatedAt: time.Now(),
	}

	_, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "string input",
		UserMessage:   userMessage,
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if len(session.ChatCtx.Items) != 1 || session.ChatCtx.Items[0] != userMessage {
		t.Fatalf("ChatCtx.Items = %#v, want only supplied user message", session.ChatCtx.Items)
	}
}

func TestAgentSessionGenerateReplyOptionsOverrideInterruptionsAndInputModality(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)
	allowInterruptions := false

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:          "hello",
		AllowInterruptions: &allowInterruptions,
		InputModality:      "audio",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = true, want per-call false override")
	}
	if got, want := handle.InputDetails.Modality, "audio"; got != want {
		t.Fatalf("handle.InputDetails.Modality = %q, want %q", got, want)
	}
}

func TestAgentSessionGenerateReplyOptionsPreserveInstructions(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Instructions:  "answer briefly",
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.Instructions == nil {
		t.Fatal("handle.Generation.Instructions = nil, want per-call instructions")
	}
	if got := handle.Generation.Instructions.AsModality("text").String(); got != "answer briefly" {
		t.Fatalf("handle.Generation.Instructions text = %q, want answer briefly", got)
	}
}

func TestAgentSessionGenerateReplyOptionsPreserveToolChoice(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		ToolChoice:    "none",
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.ToolChoice != "none" {
		t.Fatalf("handle.Generation.ToolChoice = %#v, want none", handle.Generation.ToolChoice)
	}
}

func TestAgentSessionUpdateOptionsToolChoiceDefaultsFutureReplies(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	toolChoice := llm.ToolChoice("auto")
	if err := session.UpdateOptions(AgentSessionUpdateOptions{ToolChoice: &toolChoice}); err != nil {
		t.Fatalf("UpdateOptions error = %v, want nil", err)
	}

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.ToolChoice != "auto" {
		t.Fatalf("handle.Generation.ToolChoice = %#v, want auto", handle.Generation.ToolChoice)
	}
}

func TestAgentSessionGenerateReplyFromFunctionToolDefaultsToolChoiceNone(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	ctx := WithRunContext(context.Background(), NewRunContext(session, nil, &llm.FunctionCall{
		CallID: "call_lookup",
		Name:   "lookup",
	}))

	handle, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:     "continue",
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.ToolChoice != "none" {
		t.Fatalf("handle.Generation.ToolChoice = %#v, want none inside function tool context", handle.Generation.ToolChoice)
	}
}

func TestAgentSessionGenerateReplyOptionsPreserveTools(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup"}}
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"lookup"},
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if !stringSlicesEqual(handle.Generation.Tools, []string{"lookup"}) {
		t.Fatalf("handle.Generation.Tools = %q, want [lookup]", handle.Generation.Tools)
	}
}

func TestAgentSessionGenerateReplyOptionsAcceptAgentTools(t *testing.T) {
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"lookup"},
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil for agent tool selector", err)
	}
	if !stringSlicesEqual(handle.Generation.Tools, []string{"lookup"}) {
		t.Fatalf("handle.Generation.Tools = %q, want [lookup]", handle.Generation.Tools)
	}
}

func TestAgentSessionGenerateReplyOptionsPreserveChatContext(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	replyCtx := llm.NewChatContext()
	replyCtx.Append(&llm.ChatMessage{
		ID:      "custom_user",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "custom context"}},
	})

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		ChatCtx:       replyCtx,
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.ChatCtx == nil {
		t.Fatal("handle.Generation.ChatCtx = nil, want per-call chat context")
	}
	if handle.Generation.ChatCtx == replyCtx {
		t.Fatal("handle.Generation.ChatCtx aliases input context, want copy")
	}
	if len(handle.Generation.ChatCtx.Items) != 1 || handle.Generation.ChatCtx.Items[0].GetID() != "custom_user" {
		t.Fatalf("handle.Generation.ChatCtx.Items = %#v, want copied custom context", handle.Generation.ChatCtx.Items)
	}
}

func TestAgentSessionGenerateReplyOptionsRejectUnknownTools(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup"}}
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"missing"},
		InputModality: "text",
	})

	if handle != nil {
		t.Fatalf("GenerateReplyWithOptions handle = %#v, want nil", handle)
	}
	if err == nil {
		t.Fatal("GenerateReplyWithOptions error = nil, want unknown tool error")
	}
}

func TestAgentSessionRunReturnsRunResultWatchingGeneratedSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)

	result, err := session.Run(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Run error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("Run result = nil, want RunResult")
	}
	if got := result.UserInput(); got != "hello" {
		t.Fatalf("UserInput = %q, want hello", got)
	}

	handle := session.activity.speechQueue[0].speech
	msg := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()}
	handle.AddChatItems(msg)
	handle.MarkDone()

	if !result.Done() {
		t.Fatal("Run result not done after generated speech completed")
	}
	events := result.Events()
	if len(events) != 1 {
		t.Fatalf("Events length = %d, want 1", len(events))
	}
	if ev, ok := events[0].(*ChatMessageEvent); !ok || ev.Item != msg {
		t.Fatalf("events[0] = %#v, want recorded assistant message", events[0])
	}
}

func TestAgentSessionFunctionToolsExecutedRecordsActiveRunItems(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	result := NewRunResult(session.ChatCtx)
	session.runState = result
	call := &llm.FunctionCall{
		ID:        "call_item_1",
		CallID:    "call_lookup",
		Name:      "lookup",
		Arguments: `{}`,
		CreatedAt: time.Now(),
	}
	output := &llm.FunctionCallOutput{
		ID:        "output_item_1",
		CallID:    "call_lookup",
		Name:      "lookup",
		Output:    "tool result",
		CreatedAt: call.CreatedAt.Add(time.Millisecond),
	}
	ev, err := NewFunctionToolsExecutedEvent([]*llm.FunctionCall{call}, []*llm.FunctionCallOutput{output})
	if err != nil {
		t.Fatalf("NewFunctionToolsExecutedEvent error = %v, want nil", err)
	}

	session.EmitFunctionToolsExecuted(*ev)

	events := result.Events()
	if len(events) != 2 {
		t.Fatalf("RunResult events length = %d, want function call and output", len(events))
	}
	if callEvent, ok := events[0].(*FunctionCallEvent); !ok || callEvent.Item != call {
		t.Fatalf("events[0] = %#v, want recorded function call", events[0])
	}
	if outputEvent, ok := events[1].(*FunctionCallOutputEvent); !ok || outputEvent.Item != output {
		t.Fatalf("events[1] = %#v, want recorded function call output", events[1])
	}
}

func TestAgentSessionRunWithOptionsPreservesUserInputAndOutputType(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	result, err := session.RunWithOptions(context.Background(), RunOptions{
		UserInput:     "collect name",
		InputModality: "text",
		OutputType:    reflect.TypeOf(""),
	})

	if err != nil {
		t.Fatalf("RunWithOptions error = %v, want nil", err)
	}
	if got := result.UserInput(); got != "collect name" {
		t.Fatalf("UserInput = %q, want collect name", got)
	}
	result.SetFinalOutput(42)
	result.MarkDone()
	output, err := result.FinalOutput()
	if !errors.Is(err, ErrRunResultFinalOutputType) {
		t.Fatalf("FinalOutput error = %v, want ErrRunResultFinalOutputType", err)
	}
	if output != nil {
		t.Fatalf("FinalOutput output = %#v, want nil on type mismatch", output)
	}
}

func TestAgentSessionRunRejectsNestedActiveRun(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	first, err := session.Run(context.Background(), "first")
	if err != nil {
		t.Fatalf("first Run error = %v, want nil", err)
	}

	second, err := session.Run(context.Background(), "second")

	if second != nil {
		t.Fatalf("second Run result = %#v, want nil", second)
	}
	if !errors.Is(err, ErrAgentSessionNestedRun) {
		t.Fatalf("second Run error = %v, want ErrAgentSessionNestedRun", err)
	}

	session.activity.speechQueue[0].speech.MarkDone()
	if !first.Done() {
		t.Fatal("first Run result not done after scheduled speech completed")
	}
}

func TestAgentSessionGenerateReplyRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	handle, err := session.GenerateReply(context.Background(), "hello")

	if handle != nil {
		t.Fatalf("GenerateReply handle = %#v, want nil when session is not running", handle)
	}
	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("GenerateReply error = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionCurrentAgentRequiresRunningSession(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	current, err := session.CurrentAgent()

	if current != nil {
		t.Fatalf("CurrentAgent = %#v, want nil when session is not running", current)
	}
	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("CurrentAgent error = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionCurrentAgentReturnsRunningAgent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.started = true

	current, err := session.CurrentAgent()

	if err != nil {
		t.Fatalf("CurrentAgent error = %v, want nil", err)
	}
	if current != agent {
		t.Fatalf("CurrentAgent = %#v, want session agent %#v", current, agent)
	}
}

func TestAgentSessionCloseSoonStopsRunningSession(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	session.CloseSoon(CloseReasonParticipantDisconnected)

	select {
	case ev := <-session.CloseEvents():
		if ev.Reason != CloseReasonParticipantDisconnected {
			t.Fatalf("CloseEvent.Reason = %q, want participant_disconnected", ev.Reason)
		}
	default:
		t.Fatal("CloseSoon did not emit close event")
	}

	handle, err := session.GenerateReply(context.Background(), "hello")
	if handle != nil {
		t.Fatalf("GenerateReply handle after CloseSoon = %#v, want nil", handle)
	}
	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("GenerateReply error after CloseSoon = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionShutdownClosesWithUserInitiatedReason(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	session.Shutdown()

	select {
	case ev := <-session.CloseEvents():
		if ev.Reason != CloseReasonUserInitiated {
			t.Fatalf("CloseEvent.Reason = %q, want user_initiated", ev.Reason)
		}
	default:
		t.Fatal("Shutdown did not emit close event")
	}

	handle, err := session.GenerateReply(context.Background(), "hello")
	if handle != nil {
		t.Fatalf("GenerateReply handle after Shutdown = %#v, want nil", handle)
	}
	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("GenerateReply error after Shutdown = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionShutdownDrainsByDefaultBeforeClosing(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan struct{}, 1)
	go func() {
		session.Shutdown()
		done <- struct{}{}
	}()

	waitForDraining(t, session.activity)
	select {
	case <-session.CloseEvents():
		t.Fatal("Shutdown emitted close event before active speech drained")
	case <-done:
		t.Fatal("Shutdown returned before active speech drained")
	case <-time.After(20 * time.Millisecond):
	}

	current.MarkDone()

	select {
	case <-done:
	case <-testTimeout():
		t.Fatal("Shutdown did not return after active speech completed")
	}
	select {
	case ev := <-session.CloseEvents():
		if ev.Reason != CloseReasonUserInitiated {
			t.Fatalf("CloseEvent.Reason = %q, want user_initiated", ev.Reason)
		}
	default:
		t.Fatal("Shutdown did not emit close event after draining")
	}
	if session.activity != nil {
		t.Fatalf("session.activity = %#v, want nil after drained shutdown", session.activity)
	}
}

func TestAgentSessionShutdownCanSkipDrain(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	session.Shutdown(false)

	select {
	case ev := <-session.CloseEvents():
		if ev.Reason != CloseReasonUserInitiated {
			t.Fatalf("CloseEvent.Reason = %q, want user_initiated", ev.Reason)
		}
	default:
		t.Fatal("Shutdown(false) did not emit close event")
	}
	if session.activity != nil {
		t.Fatalf("session.activity = %#v, want nil after non-draining shutdown", session.activity)
	}
}

func TestAgentSessionShutdownDoesNotCloseUnstartedSession(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	session.Shutdown()

	select {
	case ev := <-session.CloseEvents():
		t.Fatalf("unexpected close event for unstarted session: %#v", ev)
	default:
	}
}

func TestAgentSessionStopResetsSessionStates(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	session.UserState = UserStateSpeaking
	session.AgentState = AgentStateThinking

	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v, want nil", err)
	}

	if session.UserState != UserStateListening {
		t.Fatalf("UserState after Stop = %q, want %q", session.UserState, UserStateListening)
	}
	if session.AgentState != AgentStateInitializing {
		t.Fatalf("AgentState after Stop = %q, want %q", session.AgentState, AgentStateInitializing)
	}
}

func TestAgentSessionStopAllowsOnExitSessionCallbacks(t *testing.T) {
	agent := &sessionCallbackAgent{Agent: NewAgent("test")}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.session = session
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	done := make(chan error, 1)
	go func() {
		done <- session.Stop(context.Background())
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("Stop deadlocked while OnExit called back into session")
	}
	if !agent.exited {
		t.Fatal("OnExit was not called")
	}
}

func TestAgentSessionStopClosesCloseableAssistant(t *testing.T) {
	agent := NewAgent("test")
	assistant := &fakeCloseableSessionAssistant{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = assistant
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v, want nil", err)
	}
	if assistant.closed != 1 {
		t.Fatalf("assistant closed = %d, want 1", assistant.closed)
	}
}

func TestAgentSessionCurrentSpeechReturnsNilWithoutActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if got := session.CurrentSpeech(); got != nil {
		t.Fatalf("CurrentSpeech = %#v, want nil without activity", got)
	}
}

func TestAgentSessionCurrentSpeechReturnsActivitySpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	if got := session.CurrentSpeech(); got != current {
		t.Fatalf("CurrentSpeech = %#v, want current activity speech %#v", got, current)
	}
}

func TestAgentSessionWaitForInactiveReturnsWithoutActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if err := session.WaitForInactive(context.Background()); err != nil {
		t.Fatalf("WaitForInactive error = %v, want nil without activity", err)
	}
}

func TestAgentSessionWaitForInactiveWaitsForCurrentSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		done <- session.WaitForInactive(context.Background())
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitForInactive returned before current speech completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForInactive error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("WaitForInactive did not return after current speech completed")
	}
}

func TestAgentSessionDrainRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	err := session.Drain(context.Background())

	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("Drain error = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionDrainDelegatesToActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		done <- session.Drain(context.Background())
	}()

	waitForDraining(t, session.activity)
	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Drain error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("Drain did not return after current speech completed")
	}
	if !session.activity.schedulingPaused {
		t.Fatal("schedulingPaused = false after Drain, want true")
	}
}

func TestAgentSessionInterruptRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	err := session.Interrupt(false)

	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("Interrupt error = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionInterruptDelegatesToActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		done <- session.Interrupt(false)
	}()

	waitForInterrupted(t, current)
	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Interrupt error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("Interrupt did not return after current speech was done")
	}
}

func TestAgentSessionClearUserTurnRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	err := session.ClearUserTurn()

	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("ClearUserTurn error = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionCommitUserTurnRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	_, err := session.CommitUserTurn(context.Background(), CommitUserTurnOptions{})

	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("CommitUserTurn error = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionCommitUserTurnDelegatesToActivity(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "manual session turn"}},
	})

	transcript, err := session.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "manual session turn" {
		t.Fatalf("CommitUserTurn transcript = %q, want manual session turn", transcript)
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "manual session turn" {
			t.Fatalf("turn message text = %q, want manual session turn", msg.TextContent())
		}
	case <-testTimeout():
		t.Fatal("OnUserTurnCompleted was not called")
	}
}

func TestAgentSessionCloseSoonCommitsPendingUserTurn(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{SessionCloseTranscriptTimeout: 0.25})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	session.activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "closing turn"}},
	})

	session.CloseSoon(CloseReasonUserInitiated)

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "closing turn" {
			t.Fatalf("turn message text = %q, want closing turn", msg.TextContent())
		}
	case <-testTimeout():
		t.Fatal("OnUserTurnCompleted was not called before close")
	}
}

func TestAgentSessionUpdateAgentBeforeStartSwapsAgentOnly(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})

	session.UpdateAgent(next)

	if session.Agent != next {
		t.Fatalf("session.Agent = %#v, want next agent", session.Agent)
	}
	if session.activity != nil {
		t.Fatalf("session.activity = %#v, want nil before start", session.activity)
	}
	if initial.entered != 0 || initial.exited != 0 || next.entered != 0 || next.exited != 0 {
		t.Fatalf("lifecycle calls initial=%d/%d next=%d/%d, want none", initial.entered, initial.exited, next.entered, next.exited)
	}
}

func TestAgentSessionUpdateAgentPreservesSessionComponentsWhenNextAgentOmitsThem(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	sessionSTT := &fakePipelineSTT{}
	sessionVAD := &fakePipelineVAD{}
	sessionLLM := &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	sessionTTS := &fakePipelineTTS{}
	session.STT = sessionSTT
	session.VAD = sessionVAD
	session.LLM = sessionLLM
	session.TTS = sessionTTS

	session.UpdateAgent(next)

	if session.STT != sessionSTT {
		t.Fatalf("session.STT = %#v, want preserved session STT", session.STT)
	}
	if session.VAD != sessionVAD {
		t.Fatalf("session.VAD = %#v, want preserved session VAD", session.VAD)
	}
	if session.LLM != sessionLLM {
		t.Fatalf("session.LLM = %#v, want preserved session LLM", session.LLM)
	}
	if session.TTS != sessionTTS {
		t.Fatalf("session.TTS = %#v, want preserved session TTS", session.TTS)
	}
}

func TestAgentSessionUpdateAgentUsesNextAgentComponentsWhenProvided(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	nextSTT := &fakePipelineSTT{}
	nextVAD := &fakePipelineVAD{}
	nextLLM := &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	nextTTS := &fakePipelineTTS{}
	next.STT = nextSTT
	next.VAD = nextVAD
	next.LLM = nextLLM
	next.TTS = nextTTS
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	session.STT = &fakePipelineSTT{}
	session.VAD = &fakePipelineVAD{}
	session.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session.TTS = &fakePipelineTTS{}

	session.UpdateAgent(next)

	if session.STT != nextSTT {
		t.Fatalf("session.STT = %#v, want next agent STT", session.STT)
	}
	if session.VAD != nextVAD {
		t.Fatalf("session.VAD = %#v, want next agent VAD", session.VAD)
	}
	if session.LLM != nextLLM {
		t.Fatalf("session.LLM = %#v, want next agent LLM", session.LLM)
	}
	if session.TTS != nextTTS {
		t.Fatalf("session.TTS = %#v, want next agent TTS", session.TTS)
	}
}

func TestAgentSessionUpdateAgentRefreshesPipelineAssistantComponents(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initial.VAD = &fakePipelineVAD{}
	initial.STT = &fakePipelineSTT{}
	initial.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	initial.TTS = &fakePipelineTTS{}
	next := &trackingAgent{Agent: NewAgent("next")}
	nextVAD := &fakePipelineVAD{}
	nextSTT := &fakePipelineSTT{}
	nextLLM := &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	nextTTS := &fakePipelineTTS{}
	next.VAD = nextVAD
	next.STT = nextSTT
	next.LLM = nextLLM
	next.TTS = nextTTS
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	pipeline := NewPipelineAgent(initial.VAD, initial.STT, initial.LLM, initial.TTS, session.ChatCtx)
	session.Assistant = pipeline
	session.activity = NewAgentActivity(initial, session)
	session.started = true

	session.UpdateAgent(next)

	if pipeline.vad != nextVAD {
		t.Fatalf("pipeline.vad = %#v, want next agent VAD", pipeline.vad)
	}
	if pipeline.stt != nextSTT {
		t.Fatalf("pipeline.stt = %#v, want next agent STT", pipeline.stt)
	}
	if pipeline.LLM != nextLLM {
		t.Fatalf("pipeline.LLM = %#v, want next agent LLM", pipeline.LLM)
	}
	if pipeline.tts != nextTTS {
		t.Fatalf("pipeline.tts = %#v, want next agent TTS", pipeline.tts)
	}
}

func TestAgentSessionUpdateAgentWhileRunningStartsNewActivity(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initial.ID = "agent_initial"
	next := &trackingAgent{Agent: NewAgent("next")}
	next.ID = "agent_next"
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	oldActivity := NewAgentActivity(initial, session)
	session.activity = oldActivity
	session.started = true
	result := NewRunResult(session.ChatCtx)
	session.runState = result

	session.UpdateAgent(next)

	if initial.exited != 1 {
		t.Fatalf("initial exits = %d, want 1", initial.exited)
	}
	if next.entered != 1 {
		t.Fatalf("next enters = %d, want 1", next.entered)
	}
	if session.Agent != next {
		t.Fatalf("session.Agent = %#v, want next agent", session.Agent)
	}
	if session.activity == nil || session.activity == oldActivity {
		t.Fatalf("session.activity = %#v, want replacement activity", session.activity)
	}
	if session.activity.Agent != next.Agent {
		t.Fatalf("session.activity.Agent = %#v, want next base agent", session.activity.Agent)
	}
	if initial.GetActivity() != nil {
		t.Fatalf("initial activity = %#v, want cleared", initial.GetActivity())
	}
	if next.GetActivity() != session.activity {
		t.Fatalf("next activity = %#v, want session activity", next.GetActivity())
	}
	var handoff *llm.AgentHandoff
	for _, item := range session.ChatCtx.Items {
		if candidate, ok := item.(*llm.AgentHandoff); ok {
			handoff = candidate
			break
		}
	}
	if handoff == nil {
		t.Fatalf("session ChatCtx items = %#v, want agent handoff item", session.ChatCtx.Items)
	}
	if handoff.OldAgentID == nil || *handoff.OldAgentID != "agent_initial" || handoff.NewAgentID != "agent_next" {
		t.Fatalf("handoff = %#v, want initial to next", handoff)
	}
	events := result.Events()
	if len(events) != 1 {
		t.Fatalf("RunResult events length = %d, want handoff event", len(events))
	}
	handoffEvent, ok := events[0].(*AgentHandoffEvent)
	if !ok {
		t.Fatalf("events[0] = %T, want *AgentHandoffEvent", events[0])
	}
	if handoffEvent.Item != handoff || handoffEvent.OldAgent != initial.Agent || handoffEvent.NewAgent != next.Agent {
		t.Fatalf("handoff event = %#v, want recorded session handoff", handoffEvent)
	}
}

func testTimeout() <-chan time.Time {
	return time.After(time.Second)
}

type trackingAgent struct {
	*Agent
	entered int
	exited  int
}

func (a *trackingAgent) OnEnter() {
	a.entered++
}

func (a *trackingAgent) OnExit() {
	a.exited++
}

type sessionCallbackAgent struct {
	*Agent
	session *AgentSession
	exited  bool
}

func (a *sessionCallbackAgent) OnExit() {
	a.exited = true
	a.session.UpdateUserState(UserStateSpeaking)
}

func TestAgentSessionUpdateAgentStateEmitsTypedTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	before := time.Now()

	session.UpdateAgentState(AgentStateThinking)

	select {
	case ev := <-session.AgentStateChangedCh:
		var event Event = &ev
		if event.GetType() != "agent_state_changed" {
			t.Fatalf("event type = %q, want agent_state_changed", event.GetType())
		}
		if ev.OldState != AgentStateInitializing || ev.NewState != AgentStateThinking {
			t.Fatalf("event states = %q -> %q, want initializing -> thinking", ev.OldState, ev.NewState)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("UpdateAgentState did not emit an event")
	}
}

func TestAgentSessionStartEmitsInitializingThenListening(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- session.Start(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("Start did not return")
	}
	defer func() {
		if err := session.Stop(context.Background()); err != nil {
			t.Fatalf("Stop error = %v, want nil", err)
		}
	}()

	ev := receiveAgentStateChangedEvent(t, session)
	if ev.OldState != AgentStateInitializing || ev.NewState != AgentStateListening {
		t.Fatalf("state event = %q -> %q, want initializing -> listening", ev.OldState, ev.NewState)
	}
	select {
	case extra := <-session.AgentStateChangedCh:
		t.Fatalf("unexpected extra state event = %q -> %q", extra.OldState, extra.NewState)
	default:
	}
}

func TestAgentSessionUpdateUserStateEmitsTypedTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	before := time.Now()

	session.UpdateUserState(UserStateSpeaking)

	select {
	case ev := <-session.UserStateChangedCh:
		var event Event = &ev
		if event.GetType() != "user_state_changed" {
			t.Fatalf("event type = %q, want user_state_changed", event.GetType())
		}
		if ev.OldState != UserStateListening || ev.NewState != UserStateSpeaking {
			t.Fatalf("event states = %q -> %q, want listening -> speaking", ev.OldState, ev.NewState)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("UpdateUserState did not emit an event")
	}
}

func TestAgentSessionMarksUserAwayAfterIdleTimeout(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{UserAwayTimeout: 0.01})

	session.UpdateAgentState(AgentStateListening)

	select {
	case ev := <-session.UserStateChangedCh:
		if ev.OldState != UserStateListening || ev.NewState != UserStateAway {
			t.Fatalf("event states = %q -> %q, want listening -> away", ev.OldState, ev.NewState)
		}
	case <-time.After(time.Second):
		t.Fatal("UserStateChangedCh did not receive away event")
	}
	if session.UserState != UserStateAway {
		t.Fatalf("UserState = %q, want away", session.UserState)
	}
}

func TestAgentSessionCancelsUserAwayTimerWhenUserSpeaks(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{UserAwayTimeout: 0.02})

	session.UpdateAgentState(AgentStateListening)
	session.UpdateUserState(UserStateSpeaking)

	select {
	case ev := <-session.UserStateChangedCh:
		if ev.NewState != UserStateSpeaking {
			t.Fatalf("first user state event = %q, want speaking", ev.NewState)
		}
	case <-time.After(time.Second):
		t.Fatal("UserStateChangedCh did not receive speaking event")
	}
	select {
	case ev := <-session.UserStateChangedCh:
		t.Fatalf("unexpected user state event after speaking = %q -> %q", ev.OldState, ev.NewState)
	case <-time.After(60 * time.Millisecond):
	}
	if session.UserState != UserStateSpeaking {
		t.Fatalf("UserState = %q, want speaking", session.UserState)
	}
}

func receiveAgentStateChangedEvent(t *testing.T, session *AgentSession) AgentStateChangedEvent {
	t.Helper()
	select {
	case ev := <-session.AgentStateChangedCh:
		return ev
	case <-testTimeout():
		t.Fatal("AgentStateChangedCh did not receive event")
	}
	return AgentStateChangedEvent{}
}

func TestAgentSessionEmitMetricsCollectedCollectsUsageAndEmitsEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	recorder := &recordingLogger{}
	oldLogger := logutil.Logger
	logutil.SetLogger(recorder)
	t.Cleanup(func() { logutil.SetLogger(oldLogger) })
	metrics := &telemetry.LLMMetrics{
		PromptTokens:     7,
		CompletionTokens: 11,
	}
	before := time.Now()

	session.EmitMetricsCollected(metrics)

	if got := session.MetricsCollector.GetSummary(); got.LLMPromptTokens != 7 || got.LLMCompletionTokens != 11 {
		t.Fatalf("usage summary = %#v, want prompt=7 completion=11", got)
	}
	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.GetType() != "metrics_collected" {
			t.Fatalf("event type = %q, want metrics_collected", ev.GetType())
		}
		if ev.Metrics != metrics {
			t.Fatalf("Metrics = %#v, want original metrics", ev.Metrics)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive event")
	}
	select {
	case ev := <-session.SessionUsageUpdatedEvents():
		if ev.GetType() != "session_usage_updated" {
			t.Fatalf("event type = %q, want session_usage_updated", ev.GetType())
		}
		if ev.Usage.LLMPromptTokens != 7 || ev.Usage.LLMCompletionTokens != 11 {
			t.Fatalf("usage event summary = %#v, want prompt=7 completion=11", ev.Usage)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("usage event CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("SessionUsageUpdatedEvents did not receive event")
	}
	if !recorder.hasInfo("LLM metrics") {
		t.Fatalf("logged info messages = %#v, want LLM metrics log", recorder.infoMessages)
	}
	if got := recorder.infoValue("LLM metrics", "type"); got != "llm_metrics" {
		t.Fatalf("logged LLM metrics type = %#v, want llm_metrics", got)
	}
}

func TestAgentSessionUsageReturnsCollectedSummary(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	session.EmitMetricsCollected(&telemetry.LLMMetrics{
		PromptTokens:     3,
		CompletionTokens: 5,
	})

	usage := session.Usage()
	if usage.LLMPromptTokens != 3 || usage.LLMCompletionTokens != 5 {
		t.Fatalf("Usage = %#v, want prompt=3 completion=5", usage)
	}
}

type recordingLogger struct {
	infoMessages []string
	infoFields   map[string]map[string]any
}

func (l *recordingLogger) Debugw(msg string, keysAndValues ...any) {}
func (l *recordingLogger) Infow(msg string, keysAndValues ...any) {
	l.infoMessages = append(l.infoMessages, msg)
	if l.infoFields == nil {
		l.infoFields = make(map[string]map[string]any)
	}
	fields := make(map[string]any)
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		key, ok := keysAndValues[i].(string)
		if !ok {
			continue
		}
		fields[key] = keysAndValues[i+1]
	}
	l.infoFields[msg] = fields
}
func (l *recordingLogger) Warnw(msg string, err error, keysAndValues ...any)  {}
func (l *recordingLogger) Errorw(msg string, err error, keysAndValues ...any) {}
func (l *recordingLogger) WithValues(keysAndValues ...any) livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithUnlikelyValues(keysAndValues ...any) livekitlogger.UnlikelyLogger {
	return livekitlogger.GetDiscardLogger().WithUnlikelyValues(keysAndValues...)
}
func (l *recordingLogger) WithName(name string) livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithComponent(component string) livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithCallDepth(depth int) livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithItemSampler() livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithoutSampler() livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithDeferredValues() (livekitlogger.Logger, livekitlogger.DeferredFieldResolver) {
	return livekitlogger.GetDiscardLogger().WithDeferredValues()
}

func (l *recordingLogger) hasInfo(msg string) bool {
	for _, logged := range l.infoMessages {
		if logged == msg {
			return true
		}
	}
	return false
}

func (l *recordingLogger) infoValue(msg, key string) any {
	if l.infoFields == nil {
		return nil
	}
	return l.infoFields[msg][key]
}

type fakeAvatarProvider struct {
	startCalls int
	startErr   error
	state      AvatarState
}

func (f *fakeAvatarProvider) Start(ctx context.Context) error {
	f.startCalls++
	return f.startErr
}

func (f *fakeAvatarProvider) UpdateState(state AvatarState) error {
	f.state = state
	return nil
}
