package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

func TestMultimodalToolExecutionMasksInternalErrors(t *testing.T) {
	chatCtx := llm.NewChatContext()
	rtSession := &fakeRealtimeSession{}
	ma := &MultimodalAgent{
		session:   &AgentSession{Tools: []llm.Tool{&fakeGenerationTool{name: "lookup", err: errors.New("database password leaked")}}},
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`},
	})

	output := lastFunctionOutput(t, chatCtx)
	if !output.IsError || output.Output != "An internal error occurred" {
		t.Fatalf("function output = %#v, want masked internal error", output)
	}
	if rtSession.updated != chatCtx {
		t.Fatalf("updated chat context = %#v, want current context", rtSession.updated)
	}
}

func TestMultimodalAgentStartsRealtimeSessionAndAcceptsAudio(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chatCtx := llm.NewChatContext()
	ma := NewMultimodalAgent(&fakeRealtimeModel{}, chatCtx)
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})

	if ma.chatCtx != chatCtx {
		t.Fatalf("chatCtx = %#v, want provided chat context", ma.chatCtx)
	}
	if err := ma.Start(ctx, session); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if ma.session != session {
		t.Fatalf("session = %#v, want started session", ma.session)
	}
	if ma.rtSession == nil {
		t.Fatal("rtSession is nil after Start")
	}

	ma.OnAudioFrame(context.Background(), &model.AudioFrame{
		Data:              []byte{0, 1},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	})
	cancel()
}

func TestMultimodalAgentSendsSilenceToRealtimeDuringAECWarmup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{AECWarmupDuration: 0.05})
	session.UpdateAgentState(AgentStateSpeaking)
	rtSession := &fakeRealtimeSession{
		eventCh: make(chan llm.RealtimeEvent),
		audioCh: make(chan *model.AudioFrame, 1),
	}
	ma := NewMultimodalAgent(&fakeRealtimeModel{}, llm.NewChatContext())
	ma.session = session
	ma.rtSession = rtSession
	go ma.run(ctx, rtSession)

	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}
	ma.OnAudioFrame(context.Background(), frame)

	got := receiveRealtimeAudioFrame(t, rtSession.audioCh)
	if got == frame {
		t.Fatal("realtime audio reused original frame during AEC warmup")
	}
	if got.SampleRate != frame.SampleRate || got.NumChannels != frame.NumChannels || got.SamplesPerChannel != frame.SamplesPerChannel {
		t.Fatalf("realtime silence shape = rate %d channels %d samples %d, want rate %d channels %d samples %d",
			got.SampleRate, got.NumChannels, got.SamplesPerChannel,
			frame.SampleRate, frame.NumChannels, frame.SamplesPerChannel)
	}
	if !bytes.Equal(got.Data, make([]byte, len(frame.Data))) {
		t.Fatalf("realtime audio data = %v, want silence", got.Data)
	}
}

func TestMultimodalAgentSendsSilenceToRealtimeDuringUninterruptibleSpeech(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{DiscardAudioIfUninterruptible: true})
	activity := NewAgentActivity(NewAgent("test"), session)
	activity.currentSpeech = NewSpeechHandle(false, DefaultInputDetails())
	session.activity = activity
	rtSession := &fakeRealtimeSession{
		eventCh: make(chan llm.RealtimeEvent),
		audioCh: make(chan *model.AudioFrame, 1),
	}
	ma := NewMultimodalAgent(&fakeRealtimeModel{}, llm.NewChatContext())
	ma.session = session
	ma.rtSession = rtSession
	go ma.run(ctx, rtSession)

	frame := &model.AudioFrame{
		Data:              []byte{5, 6, 7, 8},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}
	ma.OnAudioFrame(context.Background(), frame)

	got := receiveRealtimeAudioFrame(t, rtSession.audioCh)
	if got == frame {
		t.Fatal("realtime audio reused original frame during uninterruptible speech")
	}
	if !bytes.Equal(got.Data, make([]byte, len(frame.Data))) {
		t.Fatalf("realtime audio data = %v, want silence", got.Data)
	}
}

func TestMultimodalAgentEmitsRealtimeErrorWhenAudioPushFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("push audio failed")
	eventCh := make(chan llm.RealtimeEvent)
	rtSession := &fakeRealtimeSession{eventCh: eventCh, pushAudioErr: cause}
	ma := NewMultimodalAgent(&fakeRealtimeModel{}, llm.NewChatContext())
	ma.session = session
	ma.rtSession = rtSession
	go ma.run(ctx, rtSession)

	ma.OnAudioFrame(context.Background(), &model.AudioFrame{
		Data:              []byte{0, 1},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	})

	select {
	case ev := <-session.ErrorEvents():
		rtErr, ok := ev.Error.(llm.RealtimeError)
		if !ok {
			t.Fatalf("Error = %T, want llm.RealtimeError", ev.Error)
		}
		if !errors.Is(rtErr, cause) {
			t.Fatalf("RealtimeError unwrap = %v, want %v", rtErr, cause)
		}
		if rtErr.Message != "failed to push audio to realtime session" {
			t.Fatalf("RealtimeError message = %q", rtErr.Message)
		}
		if ev.Source != rtSession {
			t.Fatalf("Source = %#v, want realtime session", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime push audio error")
	}
}

func TestMultimodalAgentEmitsRealtimeErrorWhenEventAudioPublishFails(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("publish realtime audio failed")
	ma := &MultimodalAgent{
		session: session,
	}
	ma.PublishAudio = func(*model.AudioFrame) error {
		return cause
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeAudio,
		Data: []byte{0, 1},
	})

	select {
	case ev := <-session.ErrorEvents():
		rtErr, ok := ev.Error.(llm.RealtimeError)
		if !ok {
			t.Fatalf("Error = %T, want llm.RealtimeError", ev.Error)
		}
		if !errors.Is(rtErr, cause) {
			t.Fatalf("RealtimeError unwrap = %v, want %v", rtErr, cause)
		}
		if rtErr.Message != "failed to publish realtime audio" {
			t.Fatalf("RealtimeError message = %q", rtErr.Message)
		}
		if ev.Source != ma {
			t.Fatalf("Source = %#v, want multimodal agent", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime event audio publish error")
	}
}

func TestMultimodalAgentStartUpdatesRealtimeSessionWithSessionAndAgentTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "agent_tool"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "session_tool"}}
	rtSession := &fakeRealtimeSession{}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())

	if err := ma.Start(ctx, session); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if got, want := toolNames(rtSession.tools), []string{"session_tool", "agent_tool"}; !equalStrings(got, want) {
		t.Fatalf("updated realtime tools = %#v, want %#v", got, want)
	}
}

func TestMultimodalAgentStartReturnsToolRegistrationError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup"}}
	rtSession := &fakeRealtimeSession{}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())

	err := ma.Start(ctx, session)
	if err == nil || !strings.Contains(err.Error(), "duplicate function name: lookup") {
		t.Fatalf("Start error = %v, want duplicate function name error", err)
	}
	if rtSession.closed != 1 {
		t.Fatalf("realtime session closed = %d, want 1", rtSession.closed)
	}
	if len(rtSession.tools) != 0 {
		t.Fatalf("updated realtime tools = %#v, want no tools on registration error", toolNames(rtSession.tools))
	}
}

func TestMultimodalAgentStartInitializesRealtimeSessionConfiguration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent := NewAgent("be helpful")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "session_tool"}}
	chatCtx := llm.NewChatContext()
	chatCtx.Items = append(chatCtx.Items, &llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser})
	rtSession := &fakeRealtimeSession{}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, chatCtx)

	if err := ma.Start(ctx, session); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if rtSession.instructions != "be helpful" {
		t.Fatalf("realtime instructions = %q, want be helpful", rtSession.instructions)
	}
	if rtSession.updated != chatCtx {
		t.Fatalf("realtime chat context = %#v, want provided chat context", rtSession.updated)
	}
	if got, want := toolNames(rtSession.tools), []string{"session_tool"}; !equalStrings(got, want) {
		t.Fatalf("updated realtime tools = %#v, want %#v", got, want)
	}
}

func TestMultimodalAgentStartReturnsRealtimeInitializationError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cause := errors.New("update instructions failed")
	agent := NewAgent("be helpful")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	rtSession := &fakeRealtimeSession{updateInstructionsErr: cause}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())

	err := ma.Start(ctx, session)
	if !errors.Is(err, cause) {
		t.Fatalf("Start error = %v, want %v", err, cause)
	}
	if rtSession.closed != 1 {
		t.Fatalf("realtime session closed = %d, want 1", rtSession.closed)
	}
	if ma.rtSession != nil {
		t.Fatalf("rtSession = %#v, want nil after initialization failure", ma.rtSession)
	}
}

func TestAgentUpdateInstructionsUpdatesRealtimeSession(t *testing.T) {
	baseAgent := NewAgent("initial instructions")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	rtSession := &fakeRealtimeSession{}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	ma.rtSession = rtSession
	session.Assistant = ma
	activity := NewAgentActivity(baseAgent, session)
	session.activity = activity

	if err := baseAgent.UpdateInstructions(context.Background(), "new instructions"); err != nil {
		t.Fatalf("UpdateInstructions error = %v, want nil", err)
	}

	if rtSession.instructions != "new instructions" {
		t.Fatalf("realtime instructions = %q, want new instructions", rtSession.instructions)
	}
}

func TestAgentUpdateToolsUpdatesRealtimeSession(t *testing.T) {
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "session_tool"}}
	rtSession := &fakeRealtimeSession{}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	ma.rtSession = rtSession
	ma.session = session
	session.Assistant = ma
	activity := NewAgentActivity(baseAgent, session)
	session.activity = activity

	if err := baseAgent.UpdateTools(context.Background(), []llm.Tool{&fakeGenerationTool{name: "agent_tool"}}); err != nil {
		t.Fatalf("UpdateTools error = %v, want nil", err)
	}

	if got, want := toolNames(rtSession.tools), []string{"session_tool", "agent_tool"}; !equalStrings(got, want) {
		t.Fatalf("updated realtime tools = %#v, want %#v", got, want)
	}
}

func TestAgentUpdateChatContextUpdatesRealtimeSession(t *testing.T) {
	baseAgent := NewAgent("be helpful")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	rtSession := &fakeRealtimeSession{}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	ma.rtSession = rtSession
	session.Assistant = ma
	activity := NewAgentActivity(baseAgent, session)
	session.activity = activity
	source := llm.NewChatContext()
	source.Append(&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}})

	if err := baseAgent.UpdateChatCtx(context.Background(), source); err != nil {
		t.Fatalf("UpdateChatCtx error = %v, want nil", err)
	}

	if got := chatItemIDs(baseAgent.ChatCtx.Items); !stringSlicesEqual(got, []string{agentInstructionsMessageID, "user"}) {
		t.Fatalf("agent ChatCtx item IDs = %q, want instructions then user", got)
	}
	if baseAgent.ChatCtx == source {
		t.Fatal("agent ChatCtx reused source context, want copied context")
	}
	if rtSession.updated == nil {
		t.Fatal("realtime chat context was not updated")
	}
	if got := chatItemIDs(rtSession.updated.Items); !stringSlicesEqual(got, []string{"user"}) {
		t.Fatalf("realtime ChatCtx item IDs = %q, want user without synthetic instructions", got)
	}
}

func TestAgentSessionUpdateOptionsUpdatesRealtimeToolChoice(t *testing.T) {
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	rtSession := &fakeRealtimeSession{}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	ma.rtSession = rtSession
	session.Assistant = ma

	toolChoice := llm.ToolChoice("auto")
	if err := session.UpdateOptions(AgentSessionUpdateOptions{ToolChoice: &toolChoice}); err != nil {
		t.Fatalf("UpdateOptions error = %v, want nil", err)
	}

	if rtSession.options.ToolChoice != "auto" {
		t.Fatalf("realtime ToolChoice = %#v, want auto", rtSession.options.ToolChoice)
	}
	if !rtSession.options.ToolChoiceSet {
		t.Fatal("realtime ToolChoiceSet = false, want true for explicit tool choice update")
	}
}

func TestMultimodalAgentGenerateReplySendsRealtimeOverrides(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lookup := &fakeGenerationTool{name: "lookup"}
	calendar := &fakeGenerationTool{name: "calendar"}
	rtSession := &fakeRealtimeSession{generateCh: make(chan llm.RealtimeGenerateReplyOptions, 1)}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma
	session.Tools = []llm.Tool{lookup, calendar}

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	handle, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:     "hello",
		Instructions:  "answer tersely",
		ToolChoice:    "none",
		Tools:         []string{"lookup"},
		InputModality: "text",
	})
	if err != nil {
		t.Fatalf("GenerateReplyWithOptions returned error: %v", err)
	}

	var opts llm.RealtimeGenerateReplyOptions
	select {
	case opts = <-rtSession.generateCh:
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive GenerateReply")
	}
	if opts.Instructions != "answer tersely" {
		t.Fatalf("Instructions = %q, want answer tersely", opts.Instructions)
	}
	if opts.ToolChoice != "none" {
		t.Fatalf("ToolChoice = %#v, want none", opts.ToolChoice)
	}
	if got, want := toolNames(opts.Tools), []string{"lookup"}; !equalStrings(got, want) {
		t.Fatalf("Tools = %#v, want %#v", got, want)
	}
	if rtSession.generatedWithChatCtx == nil {
		t.Fatal("GenerateReply saw nil chat context, want user input applied before generation")
	}
	if len(rtSession.generatedWithChatCtx.Items) != 1 {
		t.Fatalf("GenerateReply chat context items = %#v, want one user message", rtSession.generatedWithChatCtx.Items)
	}
	msg, ok := rtSession.generatedWithChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("GenerateReply chat context item = %T, want *llm.ChatMessage", rtSession.generatedWithChatCtx.Items[0])
	}
	if msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello" {
		t.Fatalf("GenerateReply chat context message = %#v, want user hello", msg)
	}
	if !handle.IsDone() {
		t.Fatal("speech handle is not done after realtime GenerateReply")
	}
}

func TestMultimodalAgentGenerateReplyAppliesInstructionInputModality(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{generateCh: make(chan llm.RealtimeGenerateReplyOptions, 1)}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	_, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:           "hello",
		InstructionVariants: llm.NewInstructions("speak plainly", "write tersely"),
		InputModality:       "audio",
	})
	if err != nil {
		t.Fatalf("GenerateReplyWithOptions returned error: %v", err)
	}

	select {
	case opts := <-rtSession.generateCh:
		if opts.Instructions != "speak plainly" {
			t.Fatalf("Instructions = %q, want audio-specific instructions", opts.Instructions)
		}
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive GenerateReply")
	}
}

func TestMultimodalAgentStartAppliesAgentInstructionVariants(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{}
	baseAgent := NewAgent("")
	baseAgent.InstructionVariants = llm.NewInstructions("speak plainly", "write tersely")
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	session.Assistant = ma

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if rtSession.instructions != "speak plainly" {
		t.Fatalf("realtime instructions = %q, want audio-specific agent instructions", rtSession.instructions)
	}
}

func TestMultimodalAgentGenerateReplyIgnoresFalseAllowInterruptionsWithTurnDetection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{generateCh: make(chan llm.RealtimeGenerateReplyOptions, 1)}
	ma := NewMultimodalAgent(&fakeRealtimeModel{
		session:      rtSession,
		capabilities: llm.RealtimeCapabilities{TurnDetection: true},
	}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{AllowInterruptions: true})
	session.Assistant = ma

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	allowInterruptions := false
	handle, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:          "hello",
		AllowInterruptions: &allowInterruptions,
		InputModality:      "text",
	})
	if err != nil {
		t.Fatalf("GenerateReplyWithOptions returned error: %v", err)
	}

	select {
	case <-rtSession.generateCh:
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive GenerateReply")
	}
	if !handle.AllowInterruptions {
		t.Fatal("SpeechHandle.AllowInterruptions = false, want session default true for realtime turn detection")
	}
}

func TestMultimodalAgentSayUsesRealtimeSessionWhenSupported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{sayCh: make(chan string, 1)}
	ma := NewMultimodalAgent(&fakeRealtimeModel{
		session:      rtSession,
		capabilities: llm.RealtimeCapabilities{SupportsSay: true},
	}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma

	handle, err := session.Say(ctx, "hello from realtime")
	if err == nil {
		t.Fatalf("Say before Start error = nil, handle = %#v; want not running", handle)
	}
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	addToChatContext := false
	handle, err = session.SayWithOptions(ctx, SayOptions{
		Text:             "hello from realtime",
		AddToChatContext: &addToChatContext,
	})
	if err != nil {
		t.Fatalf("Say returned error: %v", err)
	}

	select {
	case text := <-rtSession.sayCh:
		if text != "hello from realtime" {
			t.Fatalf("Say text = %q, want hello from realtime", text)
		}
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive Say")
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, time.Second)
	defer waitCancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("speech handle did not finish after realtime Say: %v", err)
	}
	msg := findChatMessage(session.ChatCtx, llm.ChatRoleAssistant, "hello from realtime")
	if msg == nil {
		t.Fatalf("session chat context items = %#v, want realtime say assistant message", session.ChatCtx.Items)
	}
	if len(handle.ChatItems()) != 1 || handle.ChatItems()[0] != msg {
		t.Fatalf("handle chat items = %#v, want realtime say assistant message", handle.ChatItems())
	}
}

func TestMultimodalAgentSayIgnoresFalseAllowInterruptionsWithTurnDetection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{sayCh: make(chan string, 1)}
	ma := NewMultimodalAgent(&fakeRealtimeModel{
		session: rtSession,
		capabilities: llm.RealtimeCapabilities{
			TurnDetection: true,
			SupportsSay:   true,
		},
	}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{AllowInterruptions: true})
	session.Assistant = ma

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	allowInterruptions := false
	handle, err := session.SayWithOptions(ctx, SayOptions{
		Text:               "hello from realtime",
		AllowInterruptions: &allowInterruptions,
	})
	if err != nil {
		t.Fatalf("SayWithOptions returned error: %v", err)
	}

	select {
	case <-rtSession.sayCh:
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive Say")
	}
	if !handle.AllowInterruptions {
		t.Fatal("SpeechHandle.AllowInterruptions = false, want session default true for realtime turn detection")
	}
}

func TestAgentSessionStopClosesMultimodalRealtimeSession(t *testing.T) {
	rtSession := &fakeRealtimeSession{}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
	if rtSession.closed != 1 {
		t.Fatalf("realtime session closed = %d, want 1", rtSession.closed)
	}
}

func TestMultimodalAgentPushesVideoToRealtimeSession(t *testing.T) {
	rtSession := &fakeRealtimeSession{}
	ma := &MultimodalAgent{rtSession: rtSession}

	ma.OnVideoFrame(context.Background(), &images.VideoFrame{})

	if rtSession.videoFrames != 1 {
		t.Fatalf("videoFrames = %d, want realtime video push", rtSession.videoFrames)
	}
}

func TestMultimodalAgentEmitsRealtimeErrorWhenVideoPushFails(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("push video failed")
	rtSession := &fakeRealtimeSession{pushVideoErr: cause}
	ma := &MultimodalAgent{
		session:   session,
		rtSession: rtSession,
	}

	ma.OnVideoFrame(context.Background(), &images.VideoFrame{})

	select {
	case ev := <-session.ErrorEvents():
		rtErr, ok := ev.Error.(llm.RealtimeError)
		if !ok {
			t.Fatalf("Error = %T, want llm.RealtimeError", ev.Error)
		}
		if !errors.Is(rtErr, cause) {
			t.Fatalf("RealtimeError unwrap = %v, want %v", rtErr, cause)
		}
		if rtErr.Message != "failed to push video to realtime session" {
			t.Fatalf("RealtimeError message = %q", rtErr.Message)
		}
		if ev.Source != rtSession {
			t.Fatalf("Source = %#v, want realtime session", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime push video error")
	}
}

func TestMultimodalAgentExecutesAgentToolFunctionCall(t *testing.T) {
	chatCtx := llm.NewChatContext()
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "agent result"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	rtSession := &fakeRealtimeSession{}
	ma := &MultimodalAgent{
		session:   session,
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`},
	})

	output := lastFunctionOutput(t, chatCtx)
	if output.IsError || output.Output != "agent result" {
		t.Fatalf("function output = %#v, want agent tool result", output)
	}
	select {
	case ev := <-session.FunctionToolsExecutedEvents():
		if len(ev.FunctionCalls) != 1 || ev.FunctionCalls[0].Name != "lookup" || ev.FunctionCalls[0].CallID != "call_lookup" {
			t.Fatalf("FunctionCalls = %#v, want lookup call_lookup", ev.FunctionCalls)
		}
		if len(ev.FunctionCallOutputs) != 1 || ev.FunctionCallOutputs[0] != output {
			t.Fatalf("FunctionCallOutputs = %#v, want emitted realtime output", ev.FunctionCallOutputs)
		}
		if !ev.HasToolReply() {
			t.Fatal("HasToolReply() = false, want true when realtime tool returned output")
		}
		if !chatContextContainsItem(session.ChatCtx, output) {
			t.Fatalf("session ChatCtx items = %#v, want emitted realtime output", session.ChatCtx.Items)
		}
	case <-time.After(time.Second):
		t.Fatal("FunctionToolsExecutedEvents did not receive realtime function execution")
	}
}

func TestMultimodalAgentEmitsErrorWhenRealtimeToolResultSyncFails(t *testing.T) {
	chatCtx := llm.NewChatContext()
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "agent result"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	model := &fakeRealtimeModel{label: "test.RealtimeModel"}
	cause := errors.New("update chat context failed")
	ma := &MultimodalAgent{
		model:     model,
		session:   session,
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{updateChatContextErr: cause},
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`},
	})

	select {
	case ev := <-session.ErrorEvents():
		rtErr, ok := ev.Error.(*llm.RealtimeModelError)
		if !ok {
			t.Fatalf("Error = %T, want *llm.RealtimeModelError", ev.Error)
		}
		if !errors.Is(rtErr, cause) {
			t.Fatalf("RealtimeModelError unwrap = %v, want %v", rtErr, cause)
		}
		if rtErr.Label != "test.RealtimeModel" || rtErr.Recoverable {
			t.Fatalf("RealtimeModelError = %#v, want label test.RealtimeModel recoverable false", rtErr)
		}
		if ev.Source != model {
			t.Fatalf("Source = %#v, want realtime model", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime tool result sync error")
	}
}

func TestMultimodalToolExecutionSuppressesStopResponse(t *testing.T) {
	chatCtx := llm.NewChatContext()
	ma := &MultimodalAgent{
		session:   &AgentSession{Tools: []llm.Tool{&fakeGenerationTool{name: "lookup", err: llm.StopResponse{}}}},
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{},
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`},
	})

	if len(chatCtx.Items) != 0 {
		t.Fatalf("chat items = %#v, want no output for StopResponse", chatCtx.Items)
	}
}

func TestMultimodalToolExecutionUsesScopedMockTool(t *testing.T) {
	chatCtx := llm.NewChatContext()
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "real"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	ctx := MockTools(context.Background(), session.Agent, map[string]MockToolFunc{
		"lookup": func(ctx context.Context, args string) (string, error) {
			return "mocked realtime", nil
		},
	})
	ma := &MultimodalAgent{
		session:   session,
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{},
		ctx:       ctx,
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`},
	})

	output := lastFunctionOutput(t, chatCtx)
	if output.IsError || output.Output != "mocked realtime" {
		t.Fatalf("function output = %#v, want mocked realtime success", output)
	}
}

func TestMultimodalToolExecutionRepairsArgumentsBeforeToolCall(t *testing.T) {
	chatCtx := llm.NewChatContext()
	tool := &recordingRealtimeTool{name: "lookup", result: "agent result"}
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{tool}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	ma := &MultimodalAgent{
		session:   session,
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{},
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{city:"Paris",limit:3,}`},
	})

	if tool.args != `{"city":"Paris","limit":3}` {
		t.Fatalf("tool args = %q, want repaired JSON object", tool.args)
	}
	if len(chatCtx.Items) < 2 {
		t.Fatalf("chat context items = %#v, want function call and output", chatCtx.Items)
	}
	call, ok := chatCtx.Items[len(chatCtx.Items)-2].(*llm.FunctionCall)
	if !ok {
		t.Fatalf("function call item = %T, want FunctionCall", chatCtx.Items[len(chatCtx.Items)-2])
	}
	if call.Arguments != `{"city":"Paris","limit":3}` {
		t.Fatalf("chat function call args = %q, want repaired JSON object", call.Arguments)
	}
	output := lastFunctionOutput(t, chatCtx)
	if output.IsError || output.Output != "agent result" {
		t.Fatalf("function output = %#v, want successful agent result", output)
	}
}

func TestMultimodalToolExecutionReportsUnknownFunction(t *testing.T) {
	chatCtx := llm.NewChatContext()
	ma := &MultimodalAgent{
		session:   &AgentSession{},
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{},
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "missing", CallID: "call_missing", Arguments: `{}`},
	})

	output := lastFunctionOutput(t, chatCtx)
	if !output.IsError || output.Output != "Unknown function: missing" {
		t.Fatalf("function output = %#v, want unknown function error", output)
	}
}

func TestMultimodalAgentEmitsErrorEventForRealtimeError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	model := &fakeRealtimeModel{label: "test.RealtimeModel"}
	cause := errors.New("realtime failed")
	ma := &MultimodalAgent{
		model:   model,
		session: session,
		chatCtx: llm.NewChatContext(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:  llm.RealtimeEventTypeError,
		Error: cause,
	})

	select {
	case ev := <-session.ErrorEvents():
		rtErr, ok := ev.Error.(*llm.RealtimeModelError)
		if !ok {
			t.Fatalf("Error = %T, want *llm.RealtimeModelError", ev.Error)
		}
		if !errors.Is(rtErr, cause) {
			t.Fatalf("RealtimeModelError unwrap = %v, want %v", rtErr, cause)
		}
		if rtErr.Label != "test.RealtimeModel" || rtErr.Recoverable {
			t.Fatalf("RealtimeModelError = %#v, want label test.RealtimeModel recoverable false", rtErr)
		}
		if ev.Source != model {
			t.Fatalf("Source = %#v, want realtime model", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime error")
	}
}

func TestMultimodalAgentRoutesRealtimeErrorThroughActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	model := &fakeRealtimeModel{label: "test.RealtimeModel"}
	cause := errors.New("realtime failed")
	ma := &MultimodalAgent{
		model:   model,
		session: session,
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:  llm.RealtimeEventTypeError,
		Error: cause,
	})

	select {
	case ev := <-session.ErrorEvents():
		rtErr, ok := ev.Error.(*llm.RealtimeModelError)
		if !ok {
			t.Fatalf("Error = %T, want *llm.RealtimeModelError", ev.Error)
		}
		if !errors.Is(rtErr, cause) {
			t.Fatalf("RealtimeModelError unwrap = %v, want %v", rtErr, cause)
		}
		if rtErr.Label != "test.RealtimeModel" || rtErr.Recoverable {
			t.Fatalf("RealtimeModelError = %#v, want label test.RealtimeModel recoverable false", rtErr)
		}
		if ev.Source != model {
			t.Fatalf("Source = %#v, want realtime model", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive routed realtime error")
	}
}

func TestMultimodalAgentDoesNotEmitErrorEventForRealtimeEOF(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	model := &fakeRealtimeModel{}
	ma := &MultimodalAgent{
		model:   model,
		session: session,
		chatCtx: llm.NewChatContext(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:  llm.RealtimeEventTypeError,
		Error: io.EOF,
	})

	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("unexpected realtime EOF error event: %#v", ev)
	default:
	}
}

func TestMultimodalAgentEmitsSpeechCreatedForServerGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{AllowInterruptions: true})
	activity := NewAgentActivity(NewAgent("test"), session)
	session.activity = activity
	go activity.schedulingTask()
	defer activity.Stop()
	messageCh := make(chan llm.MessageGeneration)
	ma := &MultimodalAgent{session: session}
	session.Assistant = ma

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:     messageCh,
			ResponseID:    "response_1",
			UserInitiated: false,
		},
	})

	var handle *SpeechHandle
	select {
	case ev := <-session.SpeechCreatedEvents():
		if ev.GetType() != "speech_created" {
			t.Fatalf("event type = %q, want speech_created", ev.GetType())
		}
		if ev.UserInitiated {
			t.Fatal("UserInitiated = true, want false for server realtime generation")
		}
		if ev.Source != "generate_reply" {
			t.Fatalf("Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechHandle = nil, want handle for server realtime generation")
		}
		handle = ev.SpeechHandle
		if !ev.SpeechHandle.AllowInterruptions {
			t.Fatal("SpeechHandle.AllowInterruptions = false, want session default true")
		}
		if ev.SpeechHandle.InputDetails.Modality != "audio" {
			t.Fatalf("SpeechHandle.InputDetails.Modality = %q, want audio", ev.SpeechHandle.InputDetails.Modality)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive server realtime generation")
	}
	scheduleCtx, scheduleCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer scheduleCancel()
	if err := handle.WaitForScheduled(scheduleCtx); err != nil {
		t.Fatalf("server realtime generation was not scheduled: %v", err)
	}
	if handle.IsDone() {
		t.Fatal("server realtime generation handle is done before generation stream closes")
	}
	close(messageCh)
	doneCtx, doneCancel := context.WithTimeout(ctx, time.Second)
	defer doneCancel()
	if err := handle.Wait(doneCtx); err != nil {
		t.Fatalf("server realtime generation handle did not complete after stream close: %v", err)
	}
}

func TestMultimodalAgentEmitsRealtimeErrorWhenMessageAudioPublishFails(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("publish generated audio failed")
	ma := &MultimodalAgent{session: session}
	ma.PublishAudio = func(*model.AudioFrame) error {
		return cause
	}
	audioCh := make(chan *model.AudioFrame, 1)
	audioCh <- &model.AudioFrame{
		Data:              []byte{0, 1},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	close(audioCh)

	ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(false, DefaultInputDetails()), llm.MessageGeneration{
		AudioCh: audioCh,
	})

	select {
	case ev := <-session.ErrorEvents():
		rtErr, ok := ev.Error.(llm.RealtimeError)
		if !ok {
			t.Fatalf("Error = %T, want llm.RealtimeError", ev.Error)
		}
		if !errors.Is(rtErr, cause) {
			t.Fatalf("RealtimeError unwrap = %v, want %v", rtErr, cause)
		}
		if rtErr.Message != "failed to publish realtime audio" {
			t.Fatalf("RealtimeError message = %q", rtErr.Message)
		}
		if ev.Source != ma {
			t.Fatalf("Source = %#v, want multimodal agent", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime message audio publish error")
	}
}

func TestMultimodalAgentSkipsServerGenerationWhenActivitySchedulingPaused(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.PauseScheduling()
	ma := &MultimodalAgent{session: session}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			ResponseID:    "response_1",
			UserInitiated: false,
		},
	})

	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("unexpected SpeechCreated event while scheduling paused: %#v", ev)
	default:
	}
	if len(activity.speechQueue) != 0 {
		t.Fatalf("speechQueue length = %d, want no scheduled speech", len(activity.speechQueue))
	}
}

func TestMultimodalAgentConsumesServerGenerationFunctionCalls(t *testing.T) {
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "agent result"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	go activity.schedulingTask()
	defer activity.Stop()

	messageCh := make(chan llm.MessageGeneration)
	functionCh := make(chan *llm.FunctionCall, 1)
	chatCtx := llm.NewChatContext()
	rtSession := &fakeRealtimeSession{}
	ma := &MultimodalAgent{
		session:   session,
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}
	session.Assistant = ma

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:     messageCh,
			FunctionCh:    functionCh,
			ResponseID:    "response_1",
			UserInitiated: false,
		},
	})

	functionCh <- &llm.FunctionCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`}
	close(functionCh)
	close(messageCh)

	select {
	case ev := <-session.FunctionToolsExecutedEvents():
		if len(ev.FunctionCalls) != 1 || ev.FunctionCalls[0].Name != "lookup" || ev.FunctionCalls[0].CallID != "call_lookup" {
			t.Fatalf("FunctionCalls = %#v, want lookup call_lookup", ev.FunctionCalls)
		}
		if len(ev.FunctionCallOutputs) != 1 {
			t.Fatalf("FunctionCallOutputs = %#v, want one output", ev.FunctionCallOutputs)
		}
		output := ev.FunctionCallOutputs[0]
		if output.IsError || output.Output != "agent result" {
			t.Fatalf("function output = %#v, want agent result", output)
		}
		if !chatContextContainsItem(session.ChatCtx, output) {
			t.Fatalf("session ChatCtx items = %#v, want emitted realtime output", session.ChatCtx.Items)
		}
		if rtSession.updated != chatCtx {
			t.Fatalf("updated chat context = %#v, want realtime generation chat context", rtSession.updated)
		}
	case <-time.After(time.Second):
		t.Fatal("FunctionToolsExecutedEvents did not receive server generation function execution")
	}
}

func TestMultimodalAgentKeepsRunOpenForRealtimeAutoToolReply(t *testing.T) {
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "agent result"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	go activity.schedulingTask()
	defer activity.Stop()

	result := NewRunResult(session.ChatCtx)
	session.runState = result
	currentSpeech := NewSpeechHandle(true, DefaultInputDetails())
	result.WatchSpeechHandle(currentSpeech)

	chatCtx := llm.NewChatContext()
	ma := &MultimodalAgent{
		model:     &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{AutoToolReplyGeneration: true}},
		session:   session,
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{},
		ctx:       context.Background(),
	}
	session.Assistant = ma

	ma.executeRealtimeFunctionCall(&llm.FunctionCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`})
	select {
	case <-session.FunctionToolsExecutedEvents():
	case <-time.After(time.Second):
		t.Fatal("FunctionToolsExecutedEvents did not receive realtime function execution")
	}

	currentSpeech.MarkDone()
	if result.Done() {
		t.Fatal("RunResult marked done before realtime auto tool reply generation arrived")
	}

	textCh := make(chan string, 1)
	textCh <- "tool answer"
	close(textCh)
	messageCh := make(chan llm.MessageGeneration, 1)
	messageCh <- llm.MessageGeneration{MessageID: "msg_auto_reply", TextCh: textCh}
	close(messageCh)

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:     messageCh,
			ResponseID:    "response_auto_reply",
			UserInitiated: false,
		},
	})

	var replyHandle *SpeechHandle
	select {
	case ev := <-session.SpeechCreatedEvents():
		replyHandle = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive realtime auto tool reply generation")
	}

	doneCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := replyHandle.Wait(doneCtx); err != nil {
		t.Fatalf("realtime auto tool reply handle did not complete: %v", err)
	}
	if !result.Done() {
		t.Fatal("RunResult did not complete after realtime auto tool reply generation completed")
	}
	events := result.Events()
	if len(events) != 3 {
		t.Fatalf("RunResult events length = %d, want function call, output, and assistant reply", len(events))
	}
	if msgEvent, ok := events[2].(*ChatMessageEvent); !ok || msgEvent.Item.GetID() != "msg_auto_reply" {
		t.Fatalf("events[2] = %#v, want auto tool reply assistant message", events[2])
	}
}

func TestMultimodalAgentGeneratesToolReplyWhenRealtimeDoesNotAutoReply(t *testing.T) {
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "agent result"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	chatCtx := llm.NewChatContext()
	rtSession := &fakeRealtimeSession{generateCh: make(chan llm.RealtimeGenerateReplyOptions, 1)}
	ma := &MultimodalAgent{
		model:     &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{AutoToolReplyGeneration: false}},
		session:   session,
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}
	session.Assistant = ma

	ma.executeRealtimeFunctionCall(&llm.FunctionCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`})

	select {
	case <-session.FunctionToolsExecutedEvents():
	case <-time.After(time.Second):
		t.Fatal("FunctionToolsExecutedEvents did not receive realtime function execution")
	}
	select {
	case opts := <-rtSession.generateCh:
		if opts.ToolChoice != "auto" {
			t.Fatalf("GenerateReply ToolChoice = %#v, want auto", opts.ToolChoice)
		}
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive explicit tool reply GenerateReply")
	}
	if rtSession.interrupted != 1 {
		t.Fatalf("realtime session interrupts = %d, want 1 before explicit tool reply", rtSession.interrupted)
	}
	if rtSession.generatedWithChatCtx == nil || !containsFunctionOutput(rtSession.generatedWithChatCtx, "call_lookup", "agent result") {
		t.Fatalf("generated chat context = %#v, want tool output before explicit reply", rtSession.generatedWithChatCtx)
	}
}

func TestMultimodalAgentSkipsSpeechCreatedForUserInitiatedGeneration(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	ma := &MultimodalAgent{session: session}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			ResponseID:    "response_1",
			UserInitiated: true,
		},
	})

	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("unexpected speech_created event for user-initiated realtime generation: %#v", ev)
	default:
	}
}

func TestMultimodalAgentEmitsFinalInputTranscriptionAndCommitsUserMessage(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	userTranscriptEvents := session.UserInputTranscribedEvents()
	ma := &MultimodalAgent{
		session: session,
		chatCtx: chatCtx,
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_user_1",
			Transcript: "hello realtime",
			IsFinal:    true,
		},
	})

	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "hello realtime" || !ev.IsFinal {
			t.Fatalf("transcription event = %#v, want final hello realtime", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive realtime transcript")
	}

	select {
	case ev := <-session.ConversationItemAddedEvents():
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok {
			t.Fatalf("event item = %T, want *llm.ChatMessage", ev.Item)
		}
		if msg.ID != "item_user_1" || msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello realtime" {
			t.Fatalf("message = %#v, want committed user message with realtime transcript", msg)
		}
		if chatCtx.GetByID("item_user_1") != msg {
			t.Fatalf("chat context item = %#v, want committed message", chatCtx.GetByID("item_user_1"))
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive realtime user message")
	}
}

func TestMultimodalAgentEmitsInterimInputTranscriptionWithoutCommittingMessage(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	userTranscriptEvents := session.UserInputTranscribedEvents()
	ma := &MultimodalAgent{
		session: session,
		chatCtx: chatCtx,
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_user_1",
			Transcript: "hello",
			IsFinal:    false,
		},
	})

	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "hello" || ev.IsFinal {
			t.Fatalf("transcription event = %#v, want interim hello", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive interim realtime transcript")
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		t.Fatalf("unexpected conversation item for interim transcript: %#v", ev)
	default:
	}
	if len(chatCtx.Items) != 0 {
		t.Fatalf("chat context items = %#v, want no interim transcript message", chatCtx.Items)
	}
}

func TestMultimodalAgentRoutesInputAudioTranscriptionThroughActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	userTranscriptEvents := session.UserInputTranscribedEvents()
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	ma := &MultimodalAgent{session: session, chatCtx: session.ChatCtx}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_user_1",
			Transcript: "hello activity",
			IsFinal:    true,
		},
	})

	transcriptEvent := receiveUserInputTranscribedEvent(t, userTranscriptEvents)
	if transcriptEvent.Transcript != "hello activity" || !transcriptEvent.IsFinal {
		t.Fatalf("UserInputTranscribedEvent = %#v, want final hello activity", transcriptEvent)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok {
			t.Fatalf("ConversationItemAdded item = %T, want *llm.ChatMessage", ev.Item)
		}
		if agent.ChatCtx.GetByID("item_user_1") != msg {
			t.Fatalf("agent chat context item = %#v, want routed message", agent.ChatCtx.GetByID("item_user_1"))
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive realtime user message")
	}
}

func TestMultimodalAgentRoutesRealtimeSpeechStoppedThroughActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	ma := &MultimodalAgent{session: session, chatCtx: llm.NewChatContext()}
	userTranscriptEvents := session.UserInputTranscribedEvents()
	session.UpdateUserState(UserStateSpeaking)

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeSpeechStopped,
		SpeechStopped: &llm.InputSpeechStoppedEvent{
			UserTranscriptionEnabled: true,
		},
	})

	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() = %q, want %q", got, UserStateListening)
	}
	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "" || ev.IsFinal {
			t.Fatalf("UserInputTranscribedEvent = %#v, want empty interim transcript", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive empty interim transcript")
	}
}

func TestMultimodalAgentForwardsRealtimeMetrics(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	metrics := &telemetry.RealtimeModelMetrics{RequestID: "req_1"}
	ma := &MultimodalAgent{session: session, chatCtx: llm.NewChatContext()}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:    llm.RealtimeEventTypeMetricsCollected,
		Metrics: metrics,
	})

	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.Metrics != metrics {
			t.Fatalf("Metrics = %#v, want original realtime metrics", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive realtime metrics")
	}
}

func TestMultimodalAgentRoutesRealtimeMetricsThroughActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	metrics := &telemetry.RealtimeModelMetrics{RequestID: "req_1", InputTokens: 2}
	ma := &MultimodalAgent{session: session}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:    llm.RealtimeEventTypeMetricsCollected,
		Metrics: metrics,
	})

	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.Metrics != metrics {
			t.Fatalf("MetricsCollectedEvent metrics = %#v, want original metrics", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive realtime metrics")
	}
	select {
	case ev := <-session.SessionUsageUpdatedEvents():
		if ev.Usage.LLMInputTokens() != 2 {
			t.Fatalf("SessionUsageUpdatedEvent usage = %#v, want routed realtime usage", ev.Usage)
		}
	case <-time.After(time.Second):
		t.Fatal("SessionUsageUpdatedEvents did not receive realtime usage")
	}
}

func TestMultimodalAgentAddsServerRemoteItemPlaceholder(t *testing.T) {
	existing := &llm.ChatMessage{
		ID:        "item_user_1",
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "hello"}},
		CreatedAt: time.Now(),
	}
	remote := &llm.ChatMessage{
		ID:        "item_assistant_1",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "hi"}},
		CreatedAt: existing.CreatedAt.Add(time.Second),
	}
	chatCtx := llm.NewChatContext()
	chatCtx.Insert(existing)
	ma := &MultimodalAgent{chatCtx: chatCtx}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeRemoteItemAdded,
		RemoteItem: &llm.RemoteItemAddedEvent{
			PreviousItemID: "item_user_1",
			Item:           remote,
		},
	})

	if len(chatCtx.Items) != 2 || chatCtx.Items[1] != remote {
		t.Fatalf("chat context items = %#v, want remote item appended after previous item", chatCtx.Items)
	}
}

func TestMultimodalAgentSkipsDuplicateRemoteItemPlaceholder(t *testing.T) {
	remote := &llm.ChatMessage{
		ID:        "item_assistant_1",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "hi"}},
		CreatedAt: time.Now(),
	}
	chatCtx := llm.NewChatContext()
	chatCtx.Insert(remote)
	ma := &MultimodalAgent{chatCtx: chatCtx}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeRemoteItemAdded,
		RemoteItem: &llm.RemoteItemAddedEvent{
			Item: remote,
		},
	})

	if len(chatCtx.Items) != 1 {
		t.Fatalf("chat context items = %#v, want duplicate remote item skipped", chatCtx.Items)
	}
}

func TestMultimodalAgentRoutesRemoteItemAddedThroughActivity(t *testing.T) {
	existing := &llm.ChatMessage{
		ID:        "item_user_1",
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "hello"}},
		CreatedAt: time.Now(),
	}
	remote := &llm.ChatMessage{
		ID:        "item_assistant_1",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "hi"}},
		CreatedAt: existing.CreatedAt.Add(time.Second),
	}
	agent := NewAgent("test")
	agent.ChatCtx.Insert(existing)
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	ma := &MultimodalAgent{session: session}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeRemoteItemAdded,
		RemoteItem: &llm.RemoteItemAddedEvent{
			PreviousItemID: "item_user_1",
			Item:           remote,
		},
	})

	if len(agent.ChatCtx.Items) != 2 || agent.ChatCtx.Items[1] != remote {
		t.Fatalf("agent chat context items = %#v, want remote item routed through activity", agent.ChatCtx.Items)
	}
}

func lastFunctionOutput(t *testing.T, chatCtx *llm.ChatContext) *llm.FunctionCallOutput {
	t.Helper()
	if len(chatCtx.Items) == 0 {
		t.Fatal("chat context has no items")
	}
	output, ok := chatCtx.Items[len(chatCtx.Items)-1].(*llm.FunctionCallOutput)
	if !ok {
		t.Fatalf("last item = %T, want FunctionCallOutput", chatCtx.Items[len(chatCtx.Items)-1])
	}
	return output
}

type recordingRealtimeTool struct {
	name   string
	args   string
	result string
	err    error
}

func (t *recordingRealtimeTool) ID() string { return t.name }

func (t *recordingRealtimeTool) Name() string { return t.name }

func (t *recordingRealtimeTool) Description() string { return "" }

func (t *recordingRealtimeTool) Parameters() map[string]any { return nil }

func (t *recordingRealtimeTool) Execute(_ context.Context, args string) (string, error) {
	t.args = args
	return t.result, t.err
}

func receiveRealtimeAudioFrame(t *testing.T, ch <-chan *model.AudioFrame) *model.AudioFrame {
	t.Helper()

	select {
	case frame := <-ch:
		return frame
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive audio frame")
	}
	return nil
}

func findChatMessage(chatCtx *llm.ChatContext, role llm.ChatRole, text string) *llm.ChatMessage {
	if chatCtx == nil {
		return nil
	}
	for _, item := range chatCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if ok && msg.Role == role && msg.TextContent() == text {
			return msg
		}
	}
	return nil
}

func chatContextContainsItem(chatCtx *llm.ChatContext, item llm.ChatItem) bool {
	if chatCtx == nil {
		return false
	}
	for _, existing := range chatCtx.Items {
		if existing == item {
			return true
		}
	}
	return false
}

func containsFunctionOutput(chatCtx *llm.ChatContext, callID, output string) bool {
	if chatCtx == nil {
		return false
	}
	for _, item := range chatCtx.Items {
		callOutput, ok := item.(*llm.FunctionCallOutput)
		if ok && callOutput.CallID == callID && callOutput.Output == output {
			return true
		}
	}
	return false
}

func toolNames(tools []llm.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type fakeRealtimeModel struct {
	label        string
	model        string
	provider     string
	session      *fakeRealtimeSession
	sessionErr   error
	capabilities llm.RealtimeCapabilities
}

func (f *fakeRealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return f.capabilities
}

func (f *fakeRealtimeModel) Session() (llm.RealtimeSession, error) {
	if f.sessionErr != nil {
		return nil, f.sessionErr
	}
	if f.session != nil {
		return f.session, nil
	}
	return &fakeRealtimeSession{}, nil
}

func (f *fakeRealtimeModel) Close() error { return nil }

func (f *fakeRealtimeModel) Label() string { return f.label }

func (f *fakeRealtimeModel) Model() string { return f.model }

func (f *fakeRealtimeModel) Provider() string { return f.provider }

type fakeRealtimeSession struct {
	updated               *llm.ChatContext
	generatedWithChatCtx  *llm.ChatContext
	tools                 []llm.Tool
	instructions          string
	options               llm.RealtimeSessionOptions
	generateCh            chan llm.RealtimeGenerateReplyOptions
	sayCh                 chan string
	eventCh               chan llm.RealtimeEvent
	audioCh               chan *model.AudioFrame
	videoFrames           int
	updateInstructionsErr error
	updateChatContextErr  error
	pushAudioErr          error
	pushVideoErr          error
	closed                int
	interrupted           int
}

func (f *fakeRealtimeSession) UpdateInstructions(instructions string) error {
	f.instructions = instructions
	if f.updateInstructionsErr != nil {
		return f.updateInstructionsErr
	}
	return nil
}

func (f *fakeRealtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	f.updated = chatCtx
	if f.updateChatContextErr != nil {
		return f.updateChatContextErr
	}
	return nil
}

func (f *fakeRealtimeSession) UpdateTools(tools []llm.Tool) error {
	f.tools = append([]llm.Tool(nil), tools...)
	return nil
}

func (f *fakeRealtimeSession) UpdateOptions(options llm.RealtimeSessionOptions) error {
	f.options = options
	return nil
}

func (f *fakeRealtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	if f.updated != nil {
		f.generatedWithChatCtx = f.updated.Copy()
	}
	if f.generateCh != nil {
		f.generateCh <- options
	}
	return nil
}

func (f *fakeRealtimeSession) Say(text string) error {
	if f.sayCh != nil {
		f.sayCh <- text
	}
	return nil
}

func (f *fakeRealtimeSession) Truncate(llm.RealtimeTruncateOptions) error { return nil }

func (f *fakeRealtimeSession) Interrupt() error {
	f.interrupted++
	return nil
}

func (f *fakeRealtimeSession) Close() error {
	f.closed++
	return nil
}

func (f *fakeRealtimeSession) EventCh() <-chan llm.RealtimeEvent {
	if f.eventCh != nil {
		return f.eventCh
	}
	ch := make(chan llm.RealtimeEvent)
	close(ch)
	return ch
}

func (f *fakeRealtimeSession) PushAudio(frame *model.AudioFrame) error {
	if f.audioCh != nil {
		f.audioCh <- frame
	}
	return f.pushAudioErr
}

func (f *fakeRealtimeSession) PushVideo(*images.VideoFrame) error {
	f.videoFrames++
	return f.pushVideoErr
}

func (f *fakeRealtimeSession) CommitAudio() error { return nil }

func (f *fakeRealtimeSession) ClearAudio() error { return nil }
