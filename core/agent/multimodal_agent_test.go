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
	"github.com/cavos-io/rtp-agent/core/tts"
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

func TestMultimodalAgentTTSFallbackMarksSpeakingAfterFirstAudioFrame(t *testing.T) {
	releaseAudio := make(chan struct{})
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.TTS = &blockingRealtimeFallbackTTS{release: releaseAudio}
	ma := &MultimodalAgent{
		session: session,
		ctx:     context.Background(),
		PublishAudio: func(context.Context, *model.AudioFrame) error {
			return nil
		},
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := ma.publishTTSFallbackForRealtimeText(context.Background(), nil, "hello")
		done <- err
	}()

	select {
	case ev := <-session.AgentStateChangedCh:
		t.Fatalf("agent state changed before first fallback audio frame: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseAudio)
	select {
	case ev := <-session.AgentStateChangedCh:
		if ev.NewState != AgentStateSpeaking {
			t.Fatalf("agent state event = %#v, want speaking after first fallback audio frame", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not enter speaking after first fallback audio frame")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("publishTTSFallbackForRealtimeText error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("publishTTSFallbackForRealtimeText did not finish")
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

func TestMultimodalAgentRealtimeAudioInputPushCancelSuppressesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	audioCh := make(chan *model.AudioFrame, 1)
	eventCh := make(chan llm.RealtimeEvent)
	rtSession := &fakeRealtimeSession{
		eventCh:      eventCh,
		audioCh:      audioCh,
		pushAudioErr: context.Canceled,
	}
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
	case <-audioCh:
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive input audio")
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestMultimodalAgentEmitsRealtimeErrorWhenEventAudioPublishFails(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("publish realtime audio failed")
	ma := &MultimodalAgent{
		session: session,
	}
	ma.PublishAudio = func(context.Context, *model.AudioFrame) error {
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

func TestMultimodalAgentRealtimeAudioPublishCancelSuppressesError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	ma := &MultimodalAgent{
		session: session,
	}
	ma.PublishAudio = func(context.Context, *model.AudioFrame) error {
		return context.Canceled
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeAudio,
		Data: []byte{0, 1},
	})

	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	case ev := <-session.AgentStateChangedCh:
		t.Fatalf("agent state changed after canceled publish: %#v", ev)
	default:
	}
}

func TestMultimodalAgentRealtimeAudioMarksSpeakingAfterPublish(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	publishStarted := make(chan struct{})
	releasePublish := make(chan struct{})
	ma := &MultimodalAgent{session: session}
	ma.PublishAudio = func(context.Context, *model.AudioFrame) error {
		close(publishStarted)
		<-releasePublish
		return nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ma.handleRealtimeEvent(llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeAudio,
			Data: []byte{0, 1},
		})
	}()

	select {
	case <-publishStarted:
	case <-time.After(time.Second):
		t.Fatal("PublishAudio was not called")
	}
	select {
	case ev := <-session.AgentStateChangedCh:
		t.Fatalf("agent state changed before realtime audio publish completed: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	close(releasePublish)
	select {
	case ev := <-session.AgentStateChangedCh:
		if ev.NewState != AgentStateSpeaking {
			t.Fatalf("agent state event = %#v, want speaking after realtime audio publish", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not enter speaking after realtime audio publish")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleRealtimeEvent did not finish")
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
	ma := NewMultimodalAgent(&fakeRealtimeModel{
		session:      rtSession,
		capabilities: llm.RealtimeCapabilities{PerResponseToolChoice: true},
	}, llm.NewChatContext())
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

func TestMultimodalAgentGenerateReplyChatContextSyncCancelSuppressesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{
		generateCh: make(chan llm.RealtimeGenerateReplyOptions, 1),
	}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma
	errorEvents := session.ErrorEvents()

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	initialUpdates := rtSession.chatContextUpdates
	rtSession.updateChatContextErr = context.Canceled
	handle, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:     "hello",
		InputModality: "text",
	})
	if err != nil {
		t.Fatalf("GenerateReplyWithOptions returned error: %v", err)
	}

	deadline := time.After(time.Second)
	for rtSession.chatContextUpdates <= initialUpdates {
		select {
		case <-deadline:
			t.Fatalf("UpdateChatContext calls = %d, want more than startup count %d", rtSession.chatContextUpdates, initialUpdates)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, time.Second)
	defer waitCancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("speech handle did not finish after canceled realtime chat context sync: %v", err)
	}
	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	default:
	}
	select {
	case opts := <-rtSession.generateCh:
		t.Fatalf("realtime session received GenerateReply after canceled chat context sync: %#v", opts)
	default:
	}
}

func TestMultimodalAgentGenerateReplyUsesSessionToolChoiceWhenPerResponseUnsupported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{generateCh: make(chan llm.RealtimeGenerateReplyOptions, 1)}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	sessionToolChoice := llm.ToolChoice("auto")
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{ToolChoice: sessionToolChoice})
	session.Assistant = ma

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	_, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		ToolChoice: "none",
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
	if opts.ToolChoice != nil {
		t.Fatalf("GenerateReply ToolChoice = %#v, want nil when per-response tool_choice is unsupported", opts.ToolChoice)
	}
	if len(rtSession.optionUpdates) != 2 {
		t.Fatalf("UpdateOptions calls = %d, want temporary tool_choice and reset", len(rtSession.optionUpdates))
	}
	if rtSession.optionUpdates[0].ToolChoice != "none" || !rtSession.optionUpdates[0].ToolChoiceSet {
		t.Fatalf("first UpdateOptions = %#v, want explicit none", rtSession.optionUpdates[0])
	}
	if rtSession.optionUpdates[1].ToolChoice != "auto" || !rtSession.optionUpdates[1].ToolChoiceSet {
		t.Fatalf("second UpdateOptions = %#v, want reset to stored auto", rtSession.optionUpdates[1])
	}
}

func TestMultimodalAgentGenerateReplyToolChoiceUpdateCancelSuppressesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{
		generateCh:       make(chan llm.RealtimeGenerateReplyOptions, 1),
		updateOptionsErr: context.Canceled,
	}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma
	errorEvents := session.ErrorEvents()

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	handle, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		ToolChoice: "none",
	})
	if err != nil {
		t.Fatalf("GenerateReplyWithOptions returned error: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, time.Second)
	defer waitCancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("speech handle did not finish after canceled realtime tool-choice update: %v", err)
	}
	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	default:
	}
	select {
	case opts := <-rtSession.generateCh:
		t.Fatalf("realtime session received GenerateReply after canceled tool-choice update: %#v", opts)
	default:
	}
}

func TestMultimodalAgentGenerateReplyUsesSessionToolsWhenPerResponseUnsupported(t *testing.T) {
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
	_, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		Tools: []string{"lookup"},
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
	if len(opts.Tools) != 0 {
		t.Fatalf("GenerateReply Tools = %#v, want none when per-response tools are unsupported", toolNames(opts.Tools))
	}
	if len(rtSession.toolUpdateSets) < 3 {
		t.Fatalf("UpdateTools calls = %d, want startup, temporary tools, and reset", len(rtSession.toolUpdateSets))
	}
	temporaryTools := rtSession.toolUpdateSets[len(rtSession.toolUpdateSets)-2]
	if got, want := toolNames(temporaryTools), []string{"lookup"}; !equalStrings(got, want) {
		t.Fatalf("temporary UpdateTools = %#v, want %#v", got, want)
	}
	resetTools := rtSession.toolUpdateSets[len(rtSession.toolUpdateSets)-1]
	if got, want := toolNames(resetTools), []string{"lookup", "calendar"}; !equalStrings(got, want) {
		t.Fatalf("reset UpdateTools = %#v, want %#v", got, want)
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

func TestMultimodalAgentRealtimeGenerateReplyErrorIsRecoverable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{
		generateCh:  make(chan llm.RealtimeGenerateReplyOptions, 1),
		generateErr: errors.New("realtime generate failed"),
	}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma
	errorEvents := session.ErrorEvents()

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	handle, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:     "hello",
		InputModality: "text",
	})
	if err != nil {
		t.Fatalf("GenerateReplyWithOptions returned error: %v", err)
	}

	select {
	case <-rtSession.generateCh:
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive GenerateReply")
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, time.Second)
	defer waitCancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("speech handle did not finish after realtime GenerateReply error: %v", err)
	}

	select {
	case ev := <-errorEvents:
		rtErr, ok := ev.Error.(*llm.RealtimeModelError)
		if !ok {
			t.Fatalf("ErrorEvents error = %T, want *llm.RealtimeModelError", ev.Error)
		}
		if !rtErr.Recoverable || !strings.Contains(rtErr.Error(), "realtime generate failed") {
			t.Fatalf("RealtimeModelError = %#v, want recoverable generate failure", rtErr)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime GenerateReply error")
	}
}

func TestMultimodalAgentRealtimeGenerateReplyCancelSuppressesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{
		generateCh:  make(chan llm.RealtimeGenerateReplyOptions, 1),
		generateErr: context.Canceled,
	}
	ma := NewMultimodalAgent(&fakeRealtimeModel{session: rtSession}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma
	errorEvents := session.ErrorEvents()

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	handle, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:     "hello",
		InputModality: "text",
	})
	if err != nil {
		t.Fatalf("GenerateReplyWithOptions returned error: %v", err)
	}

	select {
	case <-rtSession.generateCh:
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive GenerateReply")
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, time.Second)
	defer waitCancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("speech handle did not finish after canceled realtime GenerateReply: %v", err)
	}
	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	default:
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

func TestMultimodalAgentSayUsesTTSWhenRealtimeSayAndTTSAvailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ttsStream := newEndInputGenerationTTSStream()
	rtSession := &fakeRealtimeSession{sayCh: make(chan string, 1)}
	ma := NewMultimodalAgent(&fakeRealtimeModel{
		session:      rtSession,
		capabilities: llm.RealtimeCapabilities{SupportsSay: true},
	}, llm.NewChatContext())
	var published []*model.AudioFrame
	ma.PublishAudio = func(ctx context.Context, frame *model.AudioFrame) error {
		published = append(published, frame)
		return nil
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.TTS = &fakeGenerationTTS{stream: ttsStream}
	session.Assistant = ma

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	handle, err := session.Say(ctx, "hello from tts")
	if err != nil {
		t.Fatalf("Say returned error: %v", err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, time.Second)
	defer waitCancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("speech handle did not finish after TTS say: %v", err)
	}

	select {
	case text := <-rtSession.sayCh:
		t.Fatalf("realtime session received native Say %q, want TTS path", text)
	default:
	}
	if len(published) != 1 || string(published[0].Data) != "audio" {
		t.Fatalf("published frames = %#v, want one synthesized TTS audio frame", published)
	}
	if !strings.Contains(strings.Join(ttsStream.calls, ","), "push:hello from") ||
		!strings.Contains(strings.Join(ttsStream.calls, ","), "push: tts") ||
		ttsStream.calls[len(ttsStream.calls)-1] != "end_input" {
		t.Fatalf("TTS stream calls = %#v, want say text pushed before end_input", ttsStream.calls)
	}
	msg := findChatMessage(session.ChatCtx, llm.ChatRoleAssistant, "hello from tts")
	if msg == nil {
		t.Fatalf("session chat context items = %#v, want TTS say assistant message", session.ChatCtx.Items)
	}
}

func TestMultimodalAgentRealtimeSayErrorSkipsAssistantChatCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{
		sayCh:  make(chan string, 1),
		sayErr: errors.New("realtime say failed"),
	}
	ma := NewMultimodalAgent(&fakeRealtimeModel{
		session:      rtSession,
		capabilities: llm.RealtimeCapabilities{SupportsSay: true},
	}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma
	conversationEvents := session.ConversationItemAddedEvents()
	errorEvents := session.ErrorEvents()

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	handle, err := session.Say(ctx, "failed realtime say")
	if err != nil {
		t.Fatalf("Say returned error: %v", err)
	}

	select {
	case text := <-rtSession.sayCh:
		if text != "failed realtime say" {
			t.Fatalf("Say text = %q, want failed realtime say", text)
		}
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive Say")
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, time.Second)
	defer waitCancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("speech handle did not finish after realtime Say error: %v", err)
	}
	caseError := false
	select {
	case ev := <-errorEvents:
		caseError = ev.Error != nil && strings.Contains(ev.Error.Error(), "realtime say failed")
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime Say error")
	}
	if !caseError {
		t.Fatalf("ErrorEvents did not include realtime Say error")
	}
	if msg := findChatMessage(session.ChatCtx, llm.ChatRoleAssistant, "failed realtime say"); msg != nil {
		t.Fatalf("session chat context contains failed realtime Say message = %#v", msg)
	}
	if len(handle.ChatItems()) != 0 {
		t.Fatalf("handle chat items = %#v, want none after realtime Say error", handle.ChatItems())
	}
	select {
	case ev := <-conversationEvents:
		t.Fatalf("ConversationItemAdded event = %#v, want none after realtime Say error", ev)
	default:
	}
}

func TestMultimodalAgentRealtimeSayCancelSuppressesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rtSession := &fakeRealtimeSession{
		sayCh:  make(chan string, 1),
		sayErr: context.Canceled,
	}
	ma := NewMultimodalAgent(&fakeRealtimeModel{
		session:      rtSession,
		capabilities: llm.RealtimeCapabilities{SupportsSay: true},
	}, llm.NewChatContext())
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = ma
	conversationEvents := session.ConversationItemAddedEvents()
	errorEvents := session.ErrorEvents()

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	handle, err := session.Say(ctx, "canceled realtime say")
	if err != nil {
		t.Fatalf("Say returned error: %v", err)
	}

	select {
	case text := <-rtSession.sayCh:
		if text != "canceled realtime say" {
			t.Fatalf("Say text = %q, want canceled realtime say", text)
		}
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive Say")
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, time.Second)
	defer waitCancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("speech handle did not finish after canceled realtime Say: %v", err)
	}
	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	default:
	}
	if msg := findChatMessage(session.ChatCtx, llm.ChatRoleAssistant, "canceled realtime say"); msg != nil {
		t.Fatalf("session chat context contains canceled realtime Say message = %#v", msg)
	}
	if len(handle.ChatItems()) != 0 {
		t.Fatalf("handle chat items = %#v, want none after canceled realtime Say", handle.ChatItems())
	}
	select {
	case ev := <-conversationEvents:
		t.Fatalf("ConversationItemAdded event = %#v, want none after canceled realtime Say", ev)
	default:
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

func TestMultimodalAgentRealtimeVideoPushCancelSuppressesError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	rtSession := &fakeRealtimeSession{pushVideoErr: context.Canceled}
	ma := &MultimodalAgent{
		session:   session,
		rtSession: rtSession,
	}

	ma.OnVideoFrame(context.Background(), &images.VideoFrame{})

	if rtSession.videoFrames != 1 {
		t.Fatalf("videoFrames = %d, want realtime video push", rtSession.videoFrames)
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	default:
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

func TestMultimodalAgentRealtimeToolResultSyncCancelSuppressesError(t *testing.T) {
	chatCtx := llm.NewChatContext()
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "agent result"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	rtSession := &fakeRealtimeSession{updateChatContextErr: context.Canceled}
	ma := &MultimodalAgent{
		model:     &fakeRealtimeModel{label: "test.RealtimeModel"},
		session:   session,
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`},
	})

	if rtSession.chatContextUpdates != 1 {
		t.Fatalf("UpdateChatContext calls = %d, want 1", rtSession.chatContextUpdates)
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	default:
	}
}

func TestMultimodalAgentRealtimeToolResultSyncFailureStillEmitsEventAndReply(t *testing.T) {
	chatCtx := llm.NewChatContext()
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "agent result"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	rtSession := &fakeRealtimeSession{
		updateChatContextErr: errors.New("update chat context failed"),
		generateCh:           make(chan llm.RealtimeGenerateReplyOptions, 1),
	}
	ma := &MultimodalAgent{
		model:     &fakeRealtimeModel{},
		session:   session,
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`},
	})

	select {
	case ev := <-session.FunctionToolsExecutedEvents():
		if len(ev.FunctionCalls) != 1 || ev.FunctionCalls[0].CallID != "call_lookup" {
			t.Fatalf("FunctionCalls = %#v, want emitted lookup call", ev.FunctionCalls)
		}
		if len(ev.FunctionCallOutputs) != 1 || ev.FunctionCallOutputs[0].Output != "agent result" {
			t.Fatalf("FunctionCallOutputs = %#v, want emitted tool result", ev.FunctionCallOutputs)
		}
	case <-time.After(time.Second):
		t.Fatal("FunctionToolsExecutedEvents did not receive realtime tool result after sync failure")
	}
	select {
	case opts := <-rtSession.generateCh:
		if opts.ToolChoice != "auto" {
			t.Fatalf("GenerateReply ToolChoice = %q, want auto", opts.ToolChoice)
		}
	case <-time.After(time.Second):
		t.Fatal("GenerateReply was not requested after realtime tool result sync failure")
	}
	if rtSession.interrupted != 1 {
		t.Fatalf("realtime session interrupts = %d, want 1 before tool reply", rtSession.interrupted)
	}
}

func TestMultimodalToolExecutionSuppressesStopResponse(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := &AgentSession{Tools: []llm.Tool{&fakeGenerationTool{name: "lookup", err: llm.StopResponse{}}}}
	ma := &MultimodalAgent{
		session:   session,
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
	select {
	case ev := <-session.FunctionToolsExecutedEvents():
		if len(ev.FunctionCalls) != 1 || ev.FunctionCalls[0].CallID != "call_lookup" {
			t.Fatalf("FunctionCalls = %#v, want call_lookup event", ev.FunctionCalls)
		}
		if len(ev.FunctionCallOutputs) != 1 || ev.FunctionCallOutputs[0] != nil {
			t.Fatalf("FunctionCallOutputs = %#v, want nil output for StopResponse", ev.FunctionCallOutputs)
		}
		if ev.HasToolReply() {
			t.Fatal("HasToolReply() = true, want false for StopResponse")
		}
	case <-time.After(time.Second):
		t.Fatal("FunctionToolsExecutedEvents did not receive StopResponse event")
	}
}

func TestMultimodalToolExecutionUsesScopedMockTool(t *testing.T) {
	chatCtx := llm.NewChatContext()
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "real"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	ctx := MockTools(context.Background(), session.Agent, map[string]MockToolFunc{
		"lookup": func(ctx context.Context, args string) (string, error) {
			runCtx := GetRunContext(ctx)
			if runCtx == nil {
				t.Fatal("mock tool run context is nil")
			}
			if runCtx.Session != session {
				t.Fatalf("mock tool run context Session = %#v, want session", runCtx.Session)
			}
			if runCtx.FunctionCall == nil || runCtx.FunctionCall.CallID != "call_lookup" || runCtx.FunctionCall.Name != "lookup" {
				t.Fatalf("mock tool run context FunctionCall = %#v, want lookup call_lookup", runCtx.FunctionCall)
			}
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

func TestMultimodalToolExecutionUsesFirstRunContextUpdateAsOutput(t *testing.T) {
	chatCtx := llm.NewChatContext()
	tool := &runContextUpdatingTool{}
	session := &AgentSession{Tools: []llm.Tool{tool}}
	ma := &MultimodalAgent{
		session:   session,
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{},
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{
			Name:      "lookup",
			CallID:    "call_lookup",
			Arguments: `{"city":"Jakarta"}`,
		},
	})

	if len(chatCtx.Items) < 2 {
		t.Fatalf("chat context items = %#v, want at least function call and first update output", chatCtx.Items)
	}
	call, ok := chatCtx.Items[0].(*llm.FunctionCall)
	if !ok {
		t.Fatalf("first item = %T, want FunctionCall", chatCtx.Items[0])
	}
	if call.CallID != "call_lookup" || call.Name != "lookup" {
		t.Fatalf("function call = %#v, want lookup call_lookup", call)
	}
	if call.Arguments != `{"city":"Jakarta"}` {
		t.Fatalf("function call arguments = %q, want canonical JSON arguments", call.Arguments)
	}
	output, ok := chatCtx.Items[1].(*llm.FunctionCallOutput)
	if !ok {
		t.Fatalf("second item = %T, want FunctionCallOutput", chatCtx.Items[1])
	}
	const wantOutput = "The tool `lookup` has updated, message: searching\nThe task is still running, so DON'T make up or give information not included in the message above."
	if output.IsError || output.Output != wantOutput {
		t.Fatalf("function output = %#v, want first update success", output)
	}
	if output.CallID != "call_lookup" || output.Name != "lookup" {
		t.Fatalf("function output identity = %#v, want lookup call_lookup", output)
	}
	if tool.runContext == nil {
		t.Fatal("tool run context is nil")
	}
	if got := tool.runContext.FunctionCall.Extra["__livekit_agents_tool_non_blocking"]; got != true {
		t.Fatalf("nonblocking extra = %#v, want true", got)
	}
}

func TestMultimodalToolExecutionPreservesFinalReturnAfterRunContextUpdate(t *testing.T) {
	chatCtx := llm.NewChatContext()
	tool := &runContextUpdatingTool{}
	rtSession := &fakeRealtimeSession{generateCh: make(chan llm.RealtimeGenerateReplyOptions, 1)}
	session := &AgentSession{Tools: []llm.Tool{tool}}
	ma := &MultimodalAgent{
		session:   session,
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{
			Name:      "lookup",
			CallID:    "call_lookup",
			Arguments: `{}`,
		},
	})

	select {
	case <-rtSession.generateCh:
	case <-time.After(time.Second):
		t.Fatal("GenerateReply was not requested for realtime tool output")
	}
	var outputs []*llm.FunctionCallOutput
	for _, item := range chatCtx.Items {
		if output, ok := item.(*llm.FunctionCallOutput); ok {
			outputs = append(outputs, output)
		}
	}
	if len(outputs) != 2 {
		t.Fatalf("function outputs = %#v, want update and final return outputs", outputs)
	}
	if outputs[0].CallID != "call_lookup" || outputs[0].Output == "final result" {
		t.Fatalf("first output = %#v, want progress update for original call", outputs[0])
	}
	if outputs[1].CallID != "call_lookup_final" || outputs[1].Output != "final result" {
		t.Fatalf("second output = %#v, want synthetic final return output", outputs[1])
	}
	if rtSession.generatedWithChatCtx == nil {
		t.Fatal("GenerateReply did not capture updated chat context")
	}
	gotFinal := false
	for _, item := range rtSession.generatedWithChatCtx.Items {
		if output, ok := item.(*llm.FunctionCallOutput); ok && output.CallID == "call_lookup_final" && output.Output == "final result" {
			gotFinal = true
		}
	}
	if !gotFinal {
		t.Fatalf("generated chat context items = %#v, want final return output", rtSession.generatedWithChatCtx.Items)
	}
}

func TestMultimodalToolExecutionDetachesRunContextAfterReturn(t *testing.T) {
	chatCtx := llm.NewChatContext()
	tool := &runContextRecordingTool{}
	session := &AgentSession{Tools: []llm.Tool{tool}}
	ma := &MultimodalAgent{
		session:   session,
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{},
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`},
	})

	output := lastFunctionOutput(t, chatCtx)
	if output.IsError || output.Output != "ok" {
		t.Fatalf("function output = %#v, want successful tool result", output)
	}
	if tool.runContext == nil {
		t.Fatal("tool run context was not captured")
	}

	if err := tool.runContext.Update("late progress"); err != nil {
		t.Fatalf("late run context update returned error: %v", err)
	}
	updates := tool.runContext.Updates()
	if len(updates) != 1 {
		t.Fatalf("late run context updates = %#v, want detached context to record standalone update", updates)
	}
	if got := updates[0].FunctionCall.CallID; got != "call_lookup" {
		t.Fatalf("late update call_id = %q, want original call id", got)
	}
	if got := updates[0].FunctionCallOutput.Output; got != "The tool `lookup` has updated, message: late progress\nThe task is still running, so DON'T make up or give information not included in the message above." {
		t.Fatalf("late update output = %q, want reference standalone update", got)
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

func TestMultimodalToolExecutionRunsCancellationHelpers(t *testing.T) {
	chatCtx := llm.NewChatContext()
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", flags: llm.ToolFlagCancellable}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	cancelled := make(chan struct{}, 1)
	session.toolExecutionRegistry.set("call_lookup_a", activeToolCall{
		call: llm.FunctionCall{
			ID:        "item_lookup",
			CallID:    "call_lookup_a",
			Name:      "lookup",
			Arguments: `{"city":"Paris"}`,
			CreatedAt: time.Now(),
		},
		cancel: func() {
			cancelled <- struct{}{}
		},
		cancellable:  true,
		speechHandle: NewSpeechHandle(true, DefaultInputDetails()),
	})
	ma := &MultimodalAgent{
		session:   session,
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{},
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: getRunningTasksToolName, CallID: "call_get_running", Arguments: `{}`},
	})
	runningOutput := lastFunctionOutput(t, chatCtx)
	if runningOutput.IsError || !strings.Contains(runningOutput.Output, "call_lookup_a") {
		t.Fatalf("get running output = %#v, want visible active call", runningOutput)
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: cancelTaskToolName, CallID: "call_cancel", Arguments: `{"call_id":"call_lookup_a"}`},
	})
	cancelOutput := lastFunctionOutput(t, chatCtx)
	if cancelOutput.IsError || cancelOutput.Output != "Task call_lookup_a cancelled successfully." {
		t.Fatalf("cancel output = %#v, want successful cancellation", cancelOutput)
	}
	select {
	case <-cancelled:
	case <-testTimeout():
		t.Fatal("cancel helper did not invoke active tool cancellation")
	}
	if _, ok := session.toolExecutionRegistry.get("call_lookup_a"); ok {
		t.Fatal("cancel helper left active call in session registry")
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

func TestMultimodalAgentScheduledRealtimeGenerationAuthorizesSpeech(t *testing.T) {
	messageCh := make(chan llm.MessageGeneration)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.Generation.RealtimeGeneration = &llm.GenerationCreatedEvent{
		ResponseID: "response_authorized",
		MessageCh:  messageCh,
	}
	ma := &MultimodalAgent{session: NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ma.OnSpeechScheduled(context.Background(), speech)
	}()

	authCtx, authCancel := context.WithTimeout(context.Background(), time.Second)
	defer authCancel()
	if err := speech.WaitForAuthorization(authCtx); err != nil {
		t.Fatalf("scheduled realtime speech was not authorized: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer waitCancel()
	if err := speech.WaitForGeneration(waitCtx, -1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForGeneration while realtime stream is open = %v, want deadline exceeded", err)
	}

	close(messageCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduled realtime speech did not finish after generation stream closed")
	}
	doneCtx, doneCancel := context.WithTimeout(context.Background(), time.Second)
	defer doneCancel()
	if err := speech.WaitForGeneration(doneCtx, -1); err != nil {
		t.Fatalf("WaitForGeneration after realtime stream close = %v, want nil", err)
	}
}

func TestMultimodalAgentEmitsRealtimeErrorWhenMessageAudioPublishFails(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("publish generated audio failed")
	ma := &MultimodalAgent{session: session}
	ma.PublishAudio = func(context.Context, *model.AudioFrame) error {
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

func TestMultimodalAgentRealtimeMessageAudioPublishCancelSuppressesError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	ma := &MultimodalAgent{session: session}
	ma.PublishAudio = func(context.Context, *model.AudioFrame) error {
		return context.Canceled
	}
	audioCh := make(chan *model.AudioFrame, 1)
	audioCh <- &model.AudioFrame{
		Data:              []byte{0, 1},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	close(audioCh)

	if consumed := ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(false, DefaultInputDetails()), llm.MessageGeneration{
		AudioCh: audioCh,
	}); consumed {
		t.Fatal("consumeRealtimeMessage consumed canceled audio publish, want interrupted cleanup path")
	}

	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	case ev := <-session.AgentStateChangedCh:
		t.Fatalf("agent state changed after canceled publish: %#v", ev)
	default:
	}
}

func TestMultimodalAgentRealtimeMessageAudioMarksSpeakingAfterPublish(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	publishStarted := make(chan struct{})
	releasePublish := make(chan struct{})
	ma := &MultimodalAgent{session: session}
	ma.PublishAudio = func(context.Context, *model.AudioFrame) error {
		close(publishStarted)
		<-releasePublish
		return nil
	}
	audioCh := make(chan *model.AudioFrame, 1)
	audioCh <- &model.AudioFrame{
		Data:              []byte{0, 1},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	close(audioCh)

	done := make(chan struct{})
	go func() {
		defer close(done)
		ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(false, DefaultInputDetails()), llm.MessageGeneration{
			AudioCh: audioCh,
		})
	}()

	select {
	case <-publishStarted:
	case <-time.After(time.Second):
		t.Fatal("PublishAudio was not called for realtime message audio")
	}
	select {
	case ev := <-session.AgentStateChangedCh:
		t.Fatalf("agent state changed before realtime message audio publish completed: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	close(releasePublish)
	select {
	case ev := <-session.AgentStateChangedCh:
		if ev.NewState != AgentStateSpeaking {
			t.Fatalf("agent state event = %#v, want speaking after realtime message audio publish", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not enter speaking after realtime message audio publish")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumeRealtimeMessage did not finish")
	}
}

func TestMultimodalAgentRealtimeMessageReturnsListeningAfterPlayout(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	waitStarted := make(chan struct{})
	releaseWait := make(chan struct{})
	session.SetAudioPlaybackController(&fakePipelinePlaybackController{
		waitStarted: waitStarted,
		releaseWait: releaseWait,
		result: AudioPlaybackResult{
			PlaybackPosition: 200 * time.Millisecond,
		},
	})
	ma := &MultimodalAgent{
		session: session,
		PublishAudio: func(context.Context, *model.AudioFrame) error {
			return nil
		},
	}
	audioCh := make(chan *model.AudioFrame, 1)
	audioCh <- &model.AudioFrame{
		Data:              []byte{0, 1},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	close(audioCh)

	done := make(chan struct{})
	go func() {
		defer close(done)
		ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(false, DefaultInputDetails()), llm.MessageGeneration{
			AudioCh: audioCh,
		})
	}()

	select {
	case ev := <-session.AgentStateChangedCh:
		if ev.NewState != AgentStateSpeaking {
			t.Fatalf("first agent state event = %#v, want speaking", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not enter speaking for realtime message audio")
	}
	select {
	case <-waitStarted:
	case <-time.After(time.Second):
		t.Fatal("playback wait did not start")
	}
	select {
	case ev := <-session.AgentStateChangedCh:
		t.Fatalf("agent returned to listening before realtime playback completed: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseWait)
	select {
	case ev := <-session.AgentStateChangedCh:
		if ev.NewState != AgentStateListening {
			t.Fatalf("agent state event = %#v, want listening after realtime playback", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not return to listening after realtime playback")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumeRealtimeMessage did not finish")
	}
}

func TestMultimodalAgentRealtimeMessageWaitsForPlayoutBeforeCommit(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	waitStarted := make(chan struct{})
	releaseWait := make(chan struct{})
	session.SetAudioPlaybackController(&fakePipelinePlaybackController{
		waitStarted: waitStarted,
		releaseWait: releaseWait,
		result: AudioPlaybackResult{
			PlaybackPosition: 200 * time.Millisecond,
		},
	})
	ma := &MultimodalAgent{
		session: session,
		chatCtx: llm.NewChatContext(),
		PublishAudio: func(context.Context, *model.AudioFrame) error {
			return nil
		},
	}
	textCh := make(chan string, 1)
	textCh <- "played realtime message"
	close(textCh)
	audioCh := make(chan *model.AudioFrame, 1)
	audioCh <- &model.AudioFrame{
		Data:              []byte{0, 1},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	close(audioCh)
	events := session.ConversationItemAddedEvents()

	done := make(chan struct{})
	go func() {
		defer close(done)
		ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(false, DefaultInputDetails()), llm.MessageGeneration{
			MessageID: "msg_played",
			TextCh:    textCh,
			AudioCh:   audioCh,
		})
	}()

	select {
	case <-waitStarted:
	case ev := <-events:
		t.Fatalf("conversation item emitted before playback wait started: %#v", ev)
	case <-time.After(time.Second):
		t.Fatal("playback wait did not start")
	}
	select {
	case ev := <-events:
		t.Fatalf("conversation item emitted before playback completed: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-done:
		t.Fatal("consumeRealtimeMessage finished before playback completed")
	default:
	}

	close(releaseWait)
	select {
	case ev := <-events:
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok || msg.TextContent() != "played realtime message" {
			t.Fatalf("conversation item = %#v, want played realtime assistant message", ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive realtime assistant message")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumeRealtimeMessage did not finish after playback completed")
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

func TestMultimodalAgentAutoToolReplySyncFailureDoesNotKeepRunOpen(t *testing.T) {
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

	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AutoToolReplyGeneration: true,
		}},
		session: session,
		chatCtx: llm.NewChatContext(),
		rtSession: &fakeRealtimeSession{
			updateChatContextErr: errors.New("update chat context failed"),
		},
		ctx: context.Background(),
	}
	session.Assistant = ma

	ma.executeRealtimeFunctionCall(&llm.FunctionCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`})
	select {
	case <-session.FunctionToolsExecutedEvents():
	case <-time.After(time.Second):
		t.Fatal("FunctionToolsExecutedEvents did not receive realtime function execution")
	}

	currentSpeech.MarkDone()
	if !result.Done() {
		t.Fatal("RunResult kept waiting for auto tool reply after realtime chat context sync failed")
	}
}

func TestMultimodalAgentTruncatesInterruptedRealtimeMessageToPlayedTranscript(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	playback := &fakePipelinePlaybackController{
		result: AudioPlaybackResult{
			PlaybackPosition:          420 * time.Millisecond,
			Interrupted:               true,
			SynchronizedTranscript:    "heard words",
			HasSynchronizedTranscript: true,
		},
	}
	session.SetAudioPlaybackController(playback)
	chatCtx := llm.NewChatContext()
	rtSession := &fakeRealtimeSession{}
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			MessageTruncation: true,
			AudioOutput:       true,
		}},
		session:   session,
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}
	textCh := make(chan string, 1)
	textCh <- "heard words unheard words"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"audio"}
	close(modalitiesCh)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(true); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}

	ma.consumeRealtimeMessage(context.Background(), speech, llm.MessageGeneration{
		MessageID:    "msg_realtime_1",
		TextCh:       textCh,
		ModalitiesCh: modalitiesCh,
	})

	if playback.clearCalls != 1 {
		t.Fatalf("ClearBuffer calls = %d, want 1 for interrupted realtime speech", playback.clearCalls)
	}
	if len(rtSession.truncates) != 1 {
		t.Fatalf("Truncate calls = %#v, want one realtime truncation", rtSession.truncates)
	}
	truncate := rtSession.truncates[0]
	if truncate.MessageID != "msg_realtime_1" || truncate.AudioEndMillis != 420 {
		t.Fatalf("truncate options = %#v, want msg_realtime_1 at 420ms", truncate)
	}
	if len(truncate.Modalities) != 1 || truncate.Modalities[0] != "audio" {
		t.Fatalf("truncate modalities = %#v, want audio", truncate.Modalities)
	}
	if truncate.AudioTranscript == nil || *truncate.AudioTranscript != "heard words" {
		t.Fatalf("truncate transcript = %#v, want heard words", truncate.AudioTranscript)
	}
	if len(chatCtx.Items) != 1 {
		t.Fatalf("chat context items = %#v, want one heard assistant message", chatCtx.Items)
	}
	msg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("chat context item = %T, want ChatMessage", chatCtx.Items[0])
	}
	if msg.TextContent() != "heard words" || !msg.Interrupted {
		t.Fatalf("assistant message text/interrupted = %q/%v, want heard words/true", msg.TextContent(), msg.Interrupted)
	}
}

func TestMultimodalAgentInterruptedRealtimeMessageSkipsTextWhenPlayoutWaitCanceled(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(&fakePipelinePlaybackController{
		err: context.Canceled,
	})
	chatCtx := llm.NewChatContext()
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AudioOutput: true,
		}},
		session: session,
		chatCtx: chatCtx,
		ctx:     context.Background(),
	}
	textCh := make(chan string, 1)
	textCh <- "unconfirmed realtime text"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"audio"}
	close(modalitiesCh)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(true); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}

	ma.consumeRealtimeMessage(context.Background(), speech, llm.MessageGeneration{
		MessageID:    "msg_realtime_wait_cancel",
		TextCh:       textCh,
		ModalitiesCh: modalitiesCh,
	})

	if len(chatCtx.Items) != 0 {
		t.Fatalf("chat context items = %#v, want no assistant message when interrupted playout wait is canceled", chatCtx.Items)
	}
	if len(speech.ChatItems()) != 0 {
		t.Fatalf("speech chat items = %#v, want no committed assistant message", speech.ChatItems())
	}
}

func TestMultimodalAgentInterruptedRealtimeMessageSkipsTruncateWhenPlayoutWaitCanceled(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(&fakePipelinePlaybackController{
		err: context.Canceled,
	})
	rtSession := &fakeRealtimeSession{}
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AudioOutput:       true,
			MessageTruncation: true,
		}},
		session:   session,
		chatCtx:   llm.NewChatContext(),
		rtSession: rtSession,
		ctx:       context.Background(),
	}
	textCh := make(chan string, 1)
	textCh <- "unconfirmed realtime text"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"audio"}
	close(modalitiesCh)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(true); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}

	ma.consumeRealtimeMessage(context.Background(), speech, llm.MessageGeneration{
		MessageID:    "msg_realtime_wait_cancel_truncate",
		TextCh:       textCh,
		ModalitiesCh: modalitiesCh,
	})

	if len(rtSession.truncates) != 0 {
		t.Fatalf("Truncate calls = %#v, want no truncate without confirmed played audio", rtSession.truncates)
	}
}

func TestMultimodalAgentInterruptedRealtimeMessageWaitsForPlaybackAfterContextCanceled(t *testing.T) {
	playback := &fakePipelinePlaybackController{
		result: AudioPlaybackResult{
			PlaybackPosition:          420 * time.Millisecond,
			Interrupted:               true,
			SynchronizedTranscript:    "heard words",
			HasSynchronizedTranscript: true,
		},
		respectContext: true,
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(playback)
	ma := &MultimodalAgent{
		session: session,
		ctx:     context.Background(),
	}
	replyCtx, cancel := context.WithCancel(context.Background())
	cancel()

	forwarded, playbackResult := ma.forwardedRealtimeTextAfterInterruption(replyCtx, "heard words unheard words")

	if playback.clearCalls != 1 {
		t.Fatalf("ClearBuffer calls = %d, want 1 for interrupted realtime speech", playback.clearCalls)
	}
	if forwarded != "heard words" {
		t.Fatalf("forwarded text = %q, want synchronized transcript after canceled reply context", forwarded)
	}
	if playbackResult.PlaybackPosition != 420*time.Millisecond {
		t.Fatalf("playback position = %v, want 420ms", playbackResult.PlaybackPosition)
	}
}

func TestMultimodalAgentInterruptedRealtimeMessageUsesEmptySynchronizedTranscript(t *testing.T) {
	playback := &fakePipelinePlaybackController{
		result: AudioPlaybackResult{
			PlaybackPosition:          420 * time.Millisecond,
			Interrupted:               true,
			SynchronizedTranscript:    "",
			HasSynchronizedTranscript: true,
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(playback)
	ma := &MultimodalAgent{
		session: session,
		ctx:     context.Background(),
	}

	forwarded, playbackResult := ma.forwardedRealtimeTextAfterInterruption(context.Background(), "heard words unheard words")

	if playback.clearCalls != 1 {
		t.Fatalf("ClearBuffer calls = %d, want 1 for interrupted realtime speech", playback.clearCalls)
	}
	if forwarded != "" {
		t.Fatalf("forwarded text = %q, want empty synchronized transcript to suppress full fallback", forwarded)
	}
	if playbackResult.PlaybackPosition != 420*time.Millisecond {
		t.Fatalf("playback position = %v, want 420ms", playbackResult.PlaybackPosition)
	}
}

func TestMultimodalAgentInterruptedRealtimeMessageFallsBackWhenSynchronizedTranscriptMissing(t *testing.T) {
	playback := &fakePipelinePlaybackController{
		result: AudioPlaybackResult{
			PlaybackPosition: 420 * time.Millisecond,
			Interrupted:      true,
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(playback)
	ma := &MultimodalAgent{
		session: session,
		ctx:     context.Background(),
	}

	forwarded, playbackResult := ma.forwardedRealtimeTextAfterInterruption(context.Background(), "heard words unheard words")

	if playback.clearCalls != 1 {
		t.Fatalf("ClearBuffer calls = %d, want 1 for interrupted realtime speech", playback.clearCalls)
	}
	if forwarded != "heard words unheard words" {
		t.Fatalf("forwarded text = %q, want generated text when synchronized transcript is missing", forwarded)
	}
	if playbackResult.PlaybackPosition != 420*time.Millisecond {
		t.Fatalf("playback position = %v, want 420ms", playbackResult.PlaybackPosition)
	}
}

func TestMultimodalAgentRealtimeMessageSkipsCommitWhenPlayoutWaitFails(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(&fakePipelinePlaybackController{
		err: errors.New("playout failed"),
	})
	chatCtx := llm.NewChatContext()
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AudioOutput: true,
		}},
		session: session,
		chatCtx: chatCtx,
		ctx:     context.Background(),
		PublishAudio: func(context.Context, *model.AudioFrame) error {
			return nil
		},
	}
	textCh := make(chan string, 1)
	textCh <- "unconfirmed realtime text"
	close(textCh)
	audioCh := make(chan *model.AudioFrame, 1)
	audioCh <- &model.AudioFrame{Data: []byte{1}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
	close(audioCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"audio"}
	close(modalitiesCh)
	speech := NewSpeechHandle(true, DefaultInputDetails())

	ma.consumeRealtimeMessage(context.Background(), speech, llm.MessageGeneration{
		MessageID:    "msg_realtime_playout_failed",
		TextCh:       textCh,
		AudioCh:      audioCh,
		ModalitiesCh: modalitiesCh,
	})

	if len(chatCtx.Items) != 0 {
		t.Fatalf("chat context items = %#v, want no assistant message when playout wait fails", chatCtx.Items)
	}
	if len(speech.ChatItems()) != 0 {
		t.Fatalf("speech chat items = %#v, want no committed assistant message", speech.ChatItems())
	}
}

func TestMultimodalAgentPartialRealtimeMessageDoesNotSyncChatContext(t *testing.T) {
	playback := &fakePipelinePlaybackController{
		result: AudioPlaybackResult{
			PlaybackPosition:          420 * time.Millisecond,
			Interrupted:               true,
			SynchronizedTranscript:    "heard words",
			HasSynchronizedTranscript: true,
		},
		waitStarted: make(chan struct{}),
		releaseWait: make(chan struct{}),
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(playback)
	chatCtx := llm.NewChatContext()
	rtSession := &fakeRealtimeSession{}
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AudioOutput:        true,
			MessageTruncation:  true,
			MutableChatContext: true,
		}},
		session:   session,
		chatCtx:   chatCtx,
		rtSession: rtSession,
		PublishAudio: func(context.Context, *model.AudioFrame) error {
			return nil
		},
		ctx: context.Background(),
	}
	textCh := make(chan string, 1)
	textCh <- "heard words unheard words"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"audio"}
	close(modalitiesCh)
	audioCh := make(chan *model.AudioFrame, 1)
	audioCh <- &model.AudioFrame{Data: []byte("audio")}
	close(audioCh)
	messageCh := make(chan llm.MessageGeneration, 1)
	messageCh <- llm.MessageGeneration{
		MessageID:    "msg_realtime_partial",
		TextCh:       textCh,
		AudioCh:      audioCh,
		ModalitiesCh: modalitiesCh,
	}
	close(messageCh)
	functionCh := make(chan *llm.FunctionCall)
	close(functionCh)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ma.consumeRealtimeGeneration(context.Background(), speech, &llm.GenerationCreatedEvent{
			MessageCh:  messageCh,
			FunctionCh: functionCh,
		})
	}()

	select {
	case <-playback.waitStarted:
	case <-time.After(time.Second):
		t.Fatal("consumeRealtimeGeneration did not wait for realtime playout")
	}
	if err := speech.Interrupt(true); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	close(playback.releaseWait)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumeRealtimeGeneration did not finish after interrupted playout")
	}

	if rtSession.updated != nil {
		t.Fatalf("updated chat context = %#v, want no sync for partial played message", rtSession.updated)
	}
	if len(rtSession.truncates) != 1 {
		t.Fatalf("Truncate calls = %#v, want one truncate for partial played message", rtSession.truncates)
	}
}

func TestMultimodalAgentSkipsRealtimeMessagesAfterSpeechInterrupted(t *testing.T) {
	chatCtx := llm.NewChatContext()
	rtSession := &fakeRealtimeSession{}
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			MutableChatContext: true,
		}},
		session:   NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{}),
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}
	textCh := make(chan string, 1)
	textCh <- "never heard"
	close(textCh)
	messageCh := make(chan llm.MessageGeneration, 1)
	messageCh <- llm.MessageGeneration{MessageID: "msg_skipped", TextCh: textCh}
	close(messageCh)
	functionCh := make(chan *llm.FunctionCall)
	close(functionCh)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(true); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}

	ma.consumeRealtimeGeneration(context.Background(), speech, &llm.GenerationCreatedEvent{
		MessageCh:  messageCh,
		FunctionCh: functionCh,
	})

	if len(chatCtx.Items) != 0 {
		t.Fatalf("chat context items = %#v, want no never-heard assistant message", chatCtx.Items)
	}
	if rtSession.updated != chatCtx {
		t.Fatalf("updated chat context = %#v, want local context sync after skipped realtime messages", rtSession.updated)
	}
}

func TestMultimodalAgentInterruptedFunctionOnlyGenerationDoesNotSyncChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	rtSession := &fakeRealtimeSession{}
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			MutableChatContext: true,
		}},
		session:   NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{}),
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}
	messageCh := make(chan llm.MessageGeneration)
	close(messageCh)
	functionCh := make(chan *llm.FunctionCall, 1)
	functionCh <- &llm.FunctionCall{CallID: "call_lookup", Name: "lookup"}
	close(functionCh)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(true); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}

	ma.consumeRealtimeGeneration(context.Background(), speech, &llm.GenerationCreatedEvent{
		MessageCh:  messageCh,
		FunctionCh: functionCh,
	})

	if rtSession.updated != nil {
		t.Fatalf("updated chat context = %#v, want no sync without skipped realtime messages", rtSession.updated)
	}
}

func TestMultimodalAgentUsesTTSFallbackForTextOnlyRealtimeMessage(t *testing.T) {
	ttsStream := newEndInputGenerationTTSStream()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.TTS = &fakeGenerationTTS{stream: ttsStream}
	chatCtx := llm.NewChatContext()
	var published []*model.AudioFrame
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AudioOutput: true,
		}},
		session: session,
		chatCtx: chatCtx,
		PublishAudio: func(ctx context.Context, frame *model.AudioFrame) error {
			published = append(published, frame)
			return nil
		},
	}
	textCh := make(chan string, 1)
	textCh <- "spoken fallback"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"text"}
	close(modalitiesCh)
	speech := NewSpeechHandle(true, DefaultInputDetails())

	ma.consumeRealtimeMessage(context.Background(), speech, llm.MessageGeneration{
		MessageID:    "msg_text_only",
		TextCh:       textCh,
		ModalitiesCh: modalitiesCh,
	})

	if len(published) != 1 || string(published[0].Data) != "audio" {
		t.Fatalf("published frames = %#v, want one synthesized TTS audio frame", published)
	}
	if !strings.Contains(strings.Join(ttsStream.calls, ","), "push:spoken") ||
		!strings.Contains(strings.Join(ttsStream.calls, ","), "push: fallback") ||
		ttsStream.calls[len(ttsStream.calls)-1] != "end_input" {
		t.Fatalf("TTS stream calls = %#v, want text pushed before end_input", ttsStream.calls)
	}
	if len(chatCtx.Items) != 1 {
		t.Fatalf("chat context items = %#v, want assistant text message", chatCtx.Items)
	}
	msg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || msg.TextContent() != "spoken fallback" {
		t.Fatalf("assistant message = %#v, want spoken fallback", chatCtx.Items[0])
	}
}

func TestMultimodalAgentUsesTTSFallbackForRealtimeMessageWithoutAudioModality(t *testing.T) {
	ttsStream := newEndInputGenerationTTSStream()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.TTS = &fakeGenerationTTS{stream: ttsStream}
	var published []*model.AudioFrame
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AudioOutput: true,
		}},
		session: session,
		chatCtx: llm.NewChatContext(),
		PublishAudio: func(ctx context.Context, frame *model.AudioFrame) error {
			published = append(published, frame)
			return nil
		},
	}
	textCh := make(chan string, 1)
	textCh <- "spoken fallback"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{}
	close(modalitiesCh)

	ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(true, DefaultInputDetails()), llm.MessageGeneration{
		MessageID:    "msg_empty_modalities",
		TextCh:       textCh,
		ModalitiesCh: modalitiesCh,
	})

	if len(published) != 1 || string(published[0].Data) != "audio" {
		t.Fatalf("published frames = %#v, want one synthesized TTS audio frame", published)
	}
	if !strings.Contains(strings.Join(ttsStream.calls, ","), "push:spoken") ||
		!strings.Contains(strings.Join(ttsStream.calls, ","), "push: fallback") ||
		ttsStream.calls[len(ttsStream.calls)-1] != "end_input" {
		t.Fatalf("TTS stream calls = %#v, want text pushed before end_input", ttsStream.calls)
	}
}

func TestMultimodalAgentTTSFallbackWaitsForPlayoutBeforeCommit(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.TTS = &fakeGenerationTTS{stream: newEndInputGenerationTTSStream()}
	waitStarted := make(chan struct{})
	releaseWait := make(chan struct{})
	session.SetAudioPlaybackController(&fakePipelinePlaybackController{
		waitStarted: waitStarted,
		releaseWait: releaseWait,
		result: AudioPlaybackResult{
			PlaybackPosition: 200 * time.Millisecond,
		},
	})
	ma := &MultimodalAgent{
		session: session,
		chatCtx: llm.NewChatContext(),
		PublishAudio: func(context.Context, *model.AudioFrame) error {
			return nil
		},
	}
	textCh := make(chan string, 1)
	textCh <- "spoken fallback"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"text"}
	close(modalitiesCh)
	events := session.ConversationItemAddedEvents()

	done := make(chan struct{})
	go func() {
		defer close(done)
		ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(true, DefaultInputDetails()), llm.MessageGeneration{
			MessageID:    "msg_text_only",
			TextCh:       textCh,
			ModalitiesCh: modalitiesCh,
		})
	}()

	select {
	case <-waitStarted:
	case ev := <-events:
		t.Fatalf("conversation item emitted before fallback playback wait started: %#v", ev)
	case <-time.After(time.Second):
		t.Fatal("fallback playback wait did not start")
	}
	select {
	case ev := <-events:
		t.Fatalf("conversation item emitted before fallback playback completed: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseWait)
	select {
	case ev := <-events:
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok || msg.TextContent() != "spoken fallback" {
			t.Fatalf("conversation item = %#v, want spoken fallback assistant message", ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive fallback assistant message")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumeRealtimeMessage did not finish after fallback playback completed")
	}
}

func TestMultimodalAgentTTSFallbackStreamErrorReturnsListening(t *testing.T) {
	cause := errors.New("fallback tts stream failed")
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.TTS = &fakeGenerationTTS{stream: &failingAfterAudioRealtimeFallbackTTSStream{err: cause}}
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AudioOutput: true,
		}},
		session: session,
		PublishAudio: func(context.Context, *model.AudioFrame) error {
			return nil
		},
	}
	textCh := make(chan string, 1)
	textCh <- "spoken fallback"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"text"}
	close(modalitiesCh)

	ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(true, DefaultInputDetails()), llm.MessageGeneration{
		MessageID:    "msg_text_only_error",
		TextCh:       textCh,
		ModalitiesCh: modalitiesCh,
	})

	select {
	case ev := <-session.AgentStateChangedCh:
		if ev.NewState != AgentStateSpeaking {
			t.Fatalf("first agent state event = %#v, want speaking", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not enter speaking for fallback audio")
	}
	select {
	case ev := <-session.AgentStateChangedCh:
		if ev.NewState != AgentStateListening {
			t.Fatalf("second agent state event = %#v, want listening after fallback stream error", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not return to listening after fallback stream error")
	}
	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("ErrorEvents error = %v, want %v", ev.Error, cause)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive fallback stream error")
	}
}

func TestMultimodalAgentTTSFallbackStreamErrorSourcesTTS(t *testing.T) {
	cause := errors.New("fallback tts stream failed")
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.TTS = &fakeGenerationTTS{stream: &failingAfterAudioRealtimeFallbackTTSStream{err: cause}}
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AudioOutput: true,
		}},
		session: session,
		PublishAudio: func(context.Context, *model.AudioFrame) error {
			return nil
		},
	}
	textCh := make(chan string, 1)
	textCh <- "spoken fallback"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"text"}
	close(modalitiesCh)

	ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(true, DefaultInputDetails()), llm.MessageGeneration{
		MessageID:    "msg_text_only_error_source",
		TextCh:       textCh,
		ModalitiesCh: modalitiesCh,
	})

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("ErrorEvents error = %v, want %v", ev.Error, cause)
		}
		if ev.Source != session.TTS {
			t.Fatalf("ErrorEvents source = %T, want session TTS", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive fallback stream error")
	}
}

func TestMultimodalAgentTTSFallbackPublishErrorSourcesAgent(t *testing.T) {
	cause := errors.New("fallback publish failed")
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.TTS = &fakeGenerationTTS{stream: newEndInputGenerationTTSStream()}
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AudioOutput: true,
		}},
		session: session,
		PublishAudio: func(context.Context, *model.AudioFrame) error {
			return cause
		},
	}
	textCh := make(chan string, 1)
	textCh <- "spoken fallback"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"text"}
	close(modalitiesCh)

	ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(true, DefaultInputDetails()), llm.MessageGeneration{
		MessageID:    "msg_text_only_publish_error_source",
		TextCh:       textCh,
		ModalitiesCh: modalitiesCh,
	})

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("ErrorEvents error = %v, want %v", ev.Error, cause)
		}
		if ev.Source != ma {
			t.Fatalf("ErrorEvents source = %#v, want multimodal agent", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive fallback publish error")
	}
}

func TestMultimodalAgentTTSFallbackPublishCancelSuppressesError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.TTS = &fakeGenerationTTS{stream: newEndInputGenerationTTSStream()}
	ma := &MultimodalAgent{
		model: &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{
			AudioOutput: true,
		}},
		session: session,
		PublishAudio: func(context.Context, *model.AudioFrame) error {
			return context.Canceled
		},
	}
	textCh := make(chan string, 1)
	textCh <- "spoken fallback"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"text"}
	close(modalitiesCh)

	ma.consumeRealtimeMessage(context.Background(), NewSpeechHandle(true, DefaultInputDetails()), llm.MessageGeneration{
		MessageID:    "msg_text_only_publish_cancel",
		TextCh:       textCh,
		ModalitiesCh: modalitiesCh,
	})

	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	case ev := <-session.AgentStateChangedCh:
		t.Fatalf("agent state changed after canceled fallback publish: %#v", ev)
	default:
	}
}

func TestMultimodalAgentTTSFallbackInterruptedBeforeAudioSkipsText(t *testing.T) {
	nextStarted := make(chan struct{})
	releaseAudio := make(chan struct{})
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.TTS = &fakeGenerationTTS{stream: &interruptibleRealtimeFallbackTTSStream{
		nextStarted: nextStarted,
		release:     releaseAudio,
	}}
	chatCtx := llm.NewChatContext()
	var published []*model.AudioFrame
	ma := &MultimodalAgent{
		session: session,
		chatCtx: chatCtx,
		PublishAudio: func(_ context.Context, frame *model.AudioFrame) error {
			published = append(published, frame)
			return nil
		},
	}
	textCh := make(chan string, 1)
	textCh <- "unheard fallback"
	close(textCh)
	modalitiesCh := make(chan []string, 1)
	modalitiesCh <- []string{"text"}
	close(modalitiesCh)
	speech := NewSpeechHandle(true, DefaultInputDetails())

	done := make(chan bool, 1)
	go func() {
		done <- ma.consumeRealtimeMessage(context.Background(), speech, llm.MessageGeneration{
			MessageID:    "msg_unheard_fallback",
			TextCh:       textCh,
			ModalitiesCh: modalitiesCh,
		})
	}()

	select {
	case <-nextStarted:
	case <-time.After(time.Second):
		t.Fatal("fallback TTS stream did not wait for first audio")
	}
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt(false) error = %v", err)
	}
	close(releaseAudio)

	select {
	case skipped := <-done:
		if !skipped {
			t.Fatal("consumeRealtimeMessage skipped = false, want true for unheard interrupted fallback")
		}
	case <-time.After(time.Second):
		t.Fatal("consumeRealtimeMessage did not finish after interruption")
	}
	if len(published) != 0 {
		t.Fatalf("published frames = %#v, want no fallback audio after interruption", published)
	}
	if len(chatCtx.Items) != 0 {
		t.Fatalf("chat context items = %#v, want no unheard assistant message", chatCtx.Items)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		t.Fatalf("conversation item emitted for unheard fallback: %#v", ev)
	case <-time.After(50 * time.Millisecond):
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

func TestMultimodalAgentCancelToolReplyEventSkipsExplicitReply(t *testing.T) {
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "agent result"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.On("function_tools_executed", func(ev Event) {
		if toolsEv, ok := ev.(*FunctionToolsExecutedEvent); ok {
			toolsEv.CancelToolReply()
		}
	})
	rtSession := &fakeRealtimeSession{generateCh: make(chan llm.RealtimeGenerateReplyOptions, 1)}
	ma := &MultimodalAgent{
		model:     &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{AutoToolReplyGeneration: false}},
		session:   session,
		chatCtx:   llm.NewChatContext(),
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
		t.Fatalf("realtime session received explicit tool reply after CancelToolReply: %#v", opts)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestMultimodalAgentToolReplyGenerateErrorIsRecoverable(t *testing.T) {
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "agent result"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	chatCtx := llm.NewChatContext()
	rtSession := &fakeRealtimeSession{
		generateCh:  make(chan llm.RealtimeGenerateReplyOptions, 1),
		generateErr: errors.New("tool reply generate failed"),
	}
	ma := &MultimodalAgent{
		model:     &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{AutoToolReplyGeneration: false}},
		session:   session,
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}
	session.Assistant = ma
	errorEvents := session.ErrorEvents()

	ma.executeRealtimeFunctionCall(&llm.FunctionCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`})

	select {
	case <-rtSession.generateCh:
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive explicit tool reply GenerateReply")
	}
	select {
	case ev := <-errorEvents:
		rtErr, ok := ev.Error.(*llm.RealtimeModelError)
		if !ok {
			t.Fatalf("ErrorEvents error = %T, want *llm.RealtimeModelError", ev.Error)
		}
		if !rtErr.Recoverable || !strings.Contains(rtErr.Error(), "tool reply generate failed") {
			t.Fatalf("RealtimeModelError = %#v, want recoverable tool reply failure", rtErr)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime tool reply GenerateReply error")
	}
}

func TestMultimodalAgentToolReplyGenerateCancelSuppressesError(t *testing.T) {
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "agent result"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	chatCtx := llm.NewChatContext()
	rtSession := &fakeRealtimeSession{
		generateCh:  make(chan llm.RealtimeGenerateReplyOptions, 1),
		generateErr: context.Canceled,
	}
	ma := &MultimodalAgent{
		model:     &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{AutoToolReplyGeneration: false}},
		session:   session,
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}
	session.Assistant = ma
	errorEvents := session.ErrorEvents()

	ma.executeRealtimeFunctionCall(&llm.FunctionCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`})

	select {
	case <-rtSession.generateCh:
	case <-time.After(time.Second):
		t.Fatal("realtime session did not receive explicit tool reply GenerateReply")
	}
	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want cancellation suppressed", ev)
	default:
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

type blockingRealtimeFallbackTTS struct {
	tts.MetricsEmitter
	tts.ErrorEmitter

	release <-chan struct{}
}

func (b *blockingRealtimeFallbackTTS) Label() string { return "blocking-realtime-fallback" }

func (b *blockingRealtimeFallbackTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}

func (b *blockingRealtimeFallbackTTS) SampleRate() int { return 24000 }

func (b *blockingRealtimeFallbackTTS) NumChannels() int { return 1 }

func (b *blockingRealtimeFallbackTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, errors.New("unexpected synthesize call")
}

func (b *blockingRealtimeFallbackTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	return &blockingRealtimeFallbackTTSStream{release: b.release}, nil
}

type blockingRealtimeFallbackTTSStream struct {
	release <-chan struct{}
	sent    bool
}

func (s *blockingRealtimeFallbackTTSStream) PushText(string) error { return nil }

func (s *blockingRealtimeFallbackTTSStream) Flush() error { return nil }

func (s *blockingRealtimeFallbackTTSStream) Close() error { return nil }

func (s *blockingRealtimeFallbackTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if s.sent {
		return nil, io.EOF
	}
	<-s.release
	s.sent = true
	return &tts.SynthesizedAudio{Frame: &model.AudioFrame{
		Data:              []byte{1, 2},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}}, nil
}

type failingAfterAudioRealtimeFallbackTTSStream struct {
	sent bool
	err  error
}

func (s *failingAfterAudioRealtimeFallbackTTSStream) PushText(string) error { return nil }

func (s *failingAfterAudioRealtimeFallbackTTSStream) Flush() error { return nil }

func (s *failingAfterAudioRealtimeFallbackTTSStream) Close() error { return nil }

func (s *failingAfterAudioRealtimeFallbackTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if s.sent {
		return nil, s.err
	}
	s.sent = true
	return &tts.SynthesizedAudio{Frame: &model.AudioFrame{
		Data:              []byte("audio"),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}}, nil
}

type interruptibleRealtimeFallbackTTSStream struct {
	nextStarted chan<- struct{}
	release     <-chan struct{}
	sent        bool
}

func (s *interruptibleRealtimeFallbackTTSStream) PushText(string) error { return nil }

func (s *interruptibleRealtimeFallbackTTSStream) Flush() error { return nil }

func (s *interruptibleRealtimeFallbackTTSStream) Close() error { return nil }

func (s *interruptibleRealtimeFallbackTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if s.sent {
		return nil, io.EOF
	}
	close(s.nextStarted)
	<-s.release
	s.sent = true
	return &tts.SynthesizedAudio{Frame: &model.AudioFrame{
		Data:              []byte("audio"),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}}, nil
}

type fakeRealtimeModel struct {
	label          string
	model          string
	provider       string
	session        *fakeRealtimeSession
	sessionErr     error
	capabilities   llm.RealtimeCapabilities
	sessionStarted chan struct{}
	sessionRelease chan struct{}
}

func (f *fakeRealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return f.capabilities
}

func (f *fakeRealtimeModel) Session() (llm.RealtimeSession, error) {
	if f.sessionStarted != nil {
		close(f.sessionStarted)
	}
	if f.sessionRelease != nil {
		<-f.sessionRelease
	}
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
	toolUpdateSets        [][]llm.Tool
	instructions          string
	options               llm.RealtimeSessionOptions
	optionUpdates         []llm.RealtimeSessionOptions
	generateCh            chan llm.RealtimeGenerateReplyOptions
	generateErr           error
	sayCh                 chan string
	eventCh               chan llm.RealtimeEvent
	audioCh               chan *model.AudioFrame
	videoFrames           int
	instructionUpdates    int
	chatContextUpdates    int
	toolUpdates           int
	updateInstructionsErr error
	updateChatContextErr  error
	updateOptionsErr      error
	updateToolsErr        error
	pushAudioErr          error
	pushVideoErr          error
	closed                int
	interrupted           int
	cleared               int
	truncates             []llm.RealtimeTruncateOptions
	sayErr                error
}

func (f *fakeRealtimeSession) UpdateInstructions(instructions string) error {
	f.instructions = instructions
	f.instructionUpdates++
	if f.updateInstructionsErr != nil {
		return f.updateInstructionsErr
	}
	return nil
}

func (f *fakeRealtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	f.updated = chatCtx
	f.chatContextUpdates++
	if f.updateChatContextErr != nil {
		return f.updateChatContextErr
	}
	return nil
}

func (f *fakeRealtimeSession) UpdateTools(tools []llm.Tool) error {
	f.tools = append([]llm.Tool(nil), tools...)
	f.toolUpdateSets = append(f.toolUpdateSets, append([]llm.Tool(nil), tools...))
	f.toolUpdates++
	if f.updateToolsErr != nil {
		return f.updateToolsErr
	}
	return nil
}

func (f *fakeRealtimeSession) UpdateOptions(options llm.RealtimeSessionOptions) error {
	f.options = options
	f.optionUpdates = append(f.optionUpdates, options)
	if f.updateOptionsErr != nil {
		return f.updateOptionsErr
	}
	return nil
}

func (f *fakeRealtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	if f.updated != nil {
		f.generatedWithChatCtx = f.updated.Copy()
	}
	if f.generateCh != nil {
		f.generateCh <- options
	}
	if f.generateErr != nil {
		return f.generateErr
	}
	return nil
}

func (f *fakeRealtimeSession) Say(text string) error {
	if f.sayCh != nil {
		f.sayCh <- text
	}
	if f.sayErr != nil {
		return f.sayErr
	}
	return nil
}

func (f *fakeRealtimeSession) Truncate(options llm.RealtimeTruncateOptions) error {
	f.truncates = append(f.truncates, options)
	return nil
}

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

func (f *fakeRealtimeSession) ClearAudio() error {
	f.cleared++
	return nil
}
