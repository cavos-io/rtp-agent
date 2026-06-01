package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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

func TestAgentSessionUpdateAgentWhileRunningStartsNewActivity(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	oldActivity := NewAgentActivity(initial, session)
	session.activity = oldActivity
	session.started = true

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
