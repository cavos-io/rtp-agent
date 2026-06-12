package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPerformLLMInferenceIgnoresNonFunctionToolCalls(t *testing.T) {
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{ToolCalls: []llm.FunctionToolCall{
					{Type: "custom", Name: "ignored", CallID: "call_ignored"},
					{Name: "missing_type", CallID: "call_missing_type"},
					{Type: "function", Name: "lookup", CallID: "call_lookup"},
				}}},
			},
		},
	}

	data, err := PerformLLMInference(context.Background(), l, llm.NewChatContext(), nil)
	if err != nil {
		t.Fatalf("PerformLLMInference error = %v, want nil", err)
	}

	got := drainFunctionCalls(data.FunctionCh)
	if len(got) != 1 {
		t.Fatalf("len(FunctionCh) = %d, want 1 function tool call", len(got))
	}
	if got[0].Name != "lookup" || got[0].CallID != "call_lookup" {
		t.Fatalf("function call = %#v, want lookup/call_lookup", got[0])
	}
	if len(data.GeneratedFunctions) != 1 {
		t.Fatalf("len(GeneratedFunctions) = %d, want 1", len(data.GeneratedFunctions))
	}
	if data.GeneratedFunctions[0].Name != "lookup" || data.GeneratedFunctions[0].CallID != "call_lookup" {
		t.Fatalf("GeneratedFunctions[0] = %#v, want lookup/call_lookup", data.GeneratedFunctions[0])
	}
}

func TestPerformLLMInferenceUsesReferenceFunctionCallIDs(t *testing.T) {
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{ToolCalls: []llm.FunctionToolCall{
					{ID: "provider_tool_id", Type: "function", Name: "lookup", CallID: "call_lookup"},
				}}},
			},
		},
	}

	data, err := PerformLLMInference(context.Background(), l, llm.NewChatContext(), nil)
	if err != nil {
		t.Fatalf("PerformLLMInference error = %v, want nil", err)
	}

	got := drainFunctionCalls(data.FunctionCh)
	if len(got) != 1 {
		t.Fatalf("len(FunctionCh) = %d, want 1 function tool call", len(got))
	}
	wantID := data.ID + "/fnc_0"
	if got[0].ID != wantID {
		t.Fatalf("FunctionCh[0].ID = %q, want generated reference ID %q", got[0].ID, wantID)
	}
	if data.GeneratedFunctions[0].ID != wantID {
		t.Fatalf("GeneratedFunctions[0].ID = %q, want generated reference ID %q", data.GeneratedFunctions[0].ID, wantID)
	}
	if got[0].CallID != "call_lookup" {
		t.Fatalf("FunctionCh[0].CallID = %q, want provider call_id", got[0].CallID)
	}
}

func TestPerformLLMInferenceTracksGeneratedExtra(t *testing.T) {
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Extra: map[string]any{
					"trace_id": "first",
					"score":    1,
				}}},
				{Delta: &llm.ChoiceDelta{Extra: map[string]any{
					"trace_id": "second",
					"model":    "test-model",
				}}},
			},
		},
	}

	data, err := PerformLLMInference(context.Background(), l, llm.NewChatContext(), nil)
	if err != nil {
		t.Fatalf("PerformLLMInference error = %v, want nil", err)
	}

	drainStrings(data.TextCh)
	if got := data.GeneratedExtra["trace_id"]; got != "second" {
		t.Fatalf("GeneratedExtra[trace_id] = %#v, want second", got)
	}
	if got := data.GeneratedExtra["score"]; got != 1 {
		t.Fatalf("GeneratedExtra[score] = %#v, want 1", got)
	}
	if got := data.GeneratedExtra["model"]; got != "test-model" {
		t.Fatalf("GeneratedExtra[model] = %#v, want test-model", got)
	}
}

func TestPerformLLMInferenceFlattensToolsBeforeChat(t *testing.T) {
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{},
	}
	tools := []llm.Tool{
		&fakeGenerationTool{name: "zebra"},
		&fakeGenerationTool{name: "alpha"},
	}

	data, err := PerformLLMInference(context.Background(), l, llm.NewChatContext(), tools)
	if err != nil {
		t.Fatalf("PerformLLMInference error = %v, want nil", err)
	}
	drainStrings(data.TextCh)
	if len(l.calls) != 1 {
		t.Fatalf("len(Chat calls) = %d, want 1", len(l.calls))
	}
	gotTools := l.calls[0].Tools
	if len(gotTools) != 2 {
		t.Fatalf("len(Chat tools) = %d, want 2", len(gotTools))
	}
	if gotTools[0].Name() != "alpha" || gotTools[1].Name() != "zebra" {
		t.Fatalf("Chat tools = [%s, %s], want flattened alpha/zebra order", gotTools[0].Name(), gotTools[1].Name())
	}
}

func TestPerformLLMInferenceRecordsLLMSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	oldTracer := telemetry.Tracer
	telemetry.Tracer = provider.Tracer("test")
	t.Cleanup(func() {
		telemetry.Tracer = oldTracer
		_ = provider.Shutdown(context.Background())
	})

	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "system prompt"}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "hi there"}}})
	chatCtx.Append(&llm.FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Jakarta"}`})
	chatCtx.Append(&llm.FunctionCallOutput{CallID: "call_lookup", Name: "lookup", Output: "sunny"})

	l := &fakeGenerationLLM{
		model:    "test-model",
		provider: "test-provider",
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{{Delta: &llm.ChoiceDelta{Content: "hello"}}},
		},
	}

	data, err := PerformLLMInference(context.Background(), l, chatCtx, nil)
	if err != nil {
		t.Fatalf("PerformLLMInference error = %v, want nil", err)
	}
	drainStrings(data.TextCh)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	if spans[0].Name() != "llm_inference" {
		t.Fatalf("span name = %q, want llm_inference", spans[0].Name())
	}
	attrs := spanAttributes(spans[0].Attributes())
	if attrs[telemetry.AttrGenAIRequestModel] != "test-model" {
		t.Fatalf("span model attr = %q, want test-model", attrs[telemetry.AttrGenAIRequestModel])
	}
	if attrs[telemetry.AttrGenAIProviderName] != "test-provider" {
		t.Fatalf("span provider attr = %q, want test-provider", attrs[telemetry.AttrGenAIProviderName])
	}

	events := spans[0].Events()
	if len(events) != 5 {
		t.Fatalf("span events = %d, want 5 chat context events: %#v", len(events), events)
	}
	assertSpanEvent(t, events[0], telemetry.EventGenAISystemMessage, map[string]string{"content": "system prompt"})
	assertSpanEvent(t, events[1], telemetry.EventGenAIUserMessage, map[string]string{"content": "hello"})
	assertSpanEvent(t, events[2], telemetry.EventGenAIAssistantMessage, map[string]string{"content": "hi there"})
	assertSpanEvent(t, events[3], telemetry.EventGenAIAssistantMessage, map[string]string{"role": "assistant"})
	toolCalls := spanEventAttributeValues(events[3].Attributes)["tool_calls"].AsStringSlice()
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls event attribute = %#v, want one lookup call JSON", toolCalls)
	}
	var toolCall map[string]any
	if err := json.Unmarshal([]byte(toolCalls[0]), &toolCall); err != nil {
		t.Fatalf("tool call JSON unmarshal error = %v; payload %s", err, toolCalls[0])
	}
	function, ok := toolCall["function"].(map[string]any)
	if !ok || function["name"] != "lookup" || function["arguments"] != `{"city":"Jakarta"}` || toolCall["id"] != "call_lookup" || toolCall["type"] != "function" {
		t.Fatalf("tool call event = %#v, want lookup function call", toolCall)
	}
	assertSpanEvent(t, events[4], telemetry.EventGenAIToolMessage, map[string]string{"content": "sunny", "name": "lookup", "id": "call_lookup"})
}

func TestPerformTTSInferenceRecordsTTSNodeSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	oldTracer := telemetry.Tracer
	telemetry.Tracer = provider.Tracer("test")
	t.Cleanup(func() {
		telemetry.Tracer = oldTracer
		_ = provider.Shutdown(context.Background())
	})

	textCh := make(chan string, 1)
	textCh <- "hello"
	close(textCh)
	ttsProvider := &fakeGenerationTTS{
		model:    "test-voice",
		provider: "test-provider",
		stream: &fakeGenerationTTSStream{
			audio: []*tts.SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{1, 2}}}},
		},
	}

	data, err := PerformTTSInference(context.Background(), ttsProvider, textCh)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}
	drainAudioFrames(data.AudioCh)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	if spans[0].Name() != "tts_node" {
		t.Fatalf("span name = %q, want tts_node", spans[0].Name())
	}
	attrs := spanAttributes(spans[0].Attributes())
	if attrs[telemetry.AttrGenAIRequestModel] != "test-voice" {
		t.Fatalf("span model attr = %q, want test-voice", attrs[telemetry.AttrGenAIRequestModel])
	}
	if attrs[telemetry.AttrGenAIProviderName] != "test-provider" {
		t.Fatalf("span provider attr = %q, want test-provider", attrs[telemetry.AttrGenAIProviderName])
	}
	attrValues := spanAttributeValues(spans[0].Attributes())
	if got := attrValues[telemetry.AttrResponseTTFB].AsFloat64(); got <= 0 {
		t.Fatalf("span ttfb attr = %v, want first audio latency", got)
	}
}

func TestLLMToolSpanAttributesIncludeToolContextGroups(t *testing.T) {
	lookup := &fakeGenerationTool{name: "lookup"}
	search := &fakeGenerationTool{name: "search"}
	providerTool := &fakeGenerationProviderTool{fakeGenerationTool: fakeGenerationTool{name: "provider"}}
	toolset := &fakeGenerationToolset{id: "tools", tools: []llm.Tool{search}}
	toolCtx := llm.NewToolContext([]interface{}{lookup, providerTool, toolset})

	attrs := spanAttributeValues(llmToolSpanAttributes(toolCtx))

	if got := attrs[telemetry.AttrFunctionTools].AsStringSlice(); strings.Join(got, ",") != "lookup,search" {
		t.Fatalf("function tools attr = %v, want lookup/search", got)
	}
	if got := attrs[telemetry.AttrProviderTools].AsStringSlice(); len(got) != 1 || got[0] != "fakeGenerationProviderTool" {
		t.Fatalf("provider tools attr = %v, want fakeGenerationProviderTool", got)
	}
	if got := attrs[telemetry.AttrToolSets].AsStringSlice(); len(got) != 1 || got[0] != "fakeGenerationToolset" {
		t.Fatalf("toolsets attr = %v, want fakeGenerationToolset", got)
	}
}

func TestPerformToolExecutionsUsesToolErrorMessage(t *testing.T) {
	output := executeOneToolCall(t, &fakeGenerationTool{
		name: "lookup",
		err:  llm.NewToolError("visible failure"),
	})

	if output.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want error output")
	}
	if !output.FncCallOut.IsError || output.FncCallOut.Output != "visible failure" {
		t.Fatalf("FncCallOut = %#v, want visible ToolError output", output.FncCallOut)
	}
}

func TestPerformToolExecutionsMasksInternalErrors(t *testing.T) {
	output := executeOneToolCall(t, &fakeGenerationTool{
		name: "lookup",
		err:  errors.New("database password leaked"),
	})

	if output.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want error output")
	}
	if !output.FncCallOut.IsError || output.FncCallOut.Output != "An internal error occurred" {
		t.Fatalf("FncCallOut = %#v, want masked internal error", output.FncCallOut)
	}
}

func TestPerformToolExecutionsReportsUnknownFunctionAsToolError(t *testing.T) {
	toolCtx := llm.NewToolContext([]interface{}{&fakeGenerationTool{name: "lookup", result: "ignored"}})
	functionCh := make(chan *llm.FunctionToolCall, 1)
	functionCh <- &llm.FunctionToolCall{
		ID:        "reply-a/fnc_missing",
		Name:      "missing",
		CallID:    "call_missing",
		Arguments: `{bad`,
	}
	close(functionCh)

	outCh := PerformToolExecutions(context.Background(), functionCh, toolCtx)
	output, ok := <-outCh
	if !ok {
		t.Fatal("PerformToolExecutions closed without output")
	}
	if output.FncCall.ID != "reply-a/fnc_missing" || output.FncCall.Name != "missing" || output.FncCall.Arguments != `{bad` {
		t.Fatalf("FncCall = %#v, want unknown call with generated id and raw arguments", output.FncCall)
	}
	if output.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want unknown function output")
	}
	if !output.FncCallOut.IsError || output.FncCallOut.Output != "Unknown function: missing" {
		t.Fatalf("FncCallOut = %#v, want unknown function ToolError output", output.FncCallOut)
	}
	var toolErr llm.ToolError
	if !errors.As(output.RawError, &toolErr) {
		t.Fatalf("RawError = %T %v, want ToolError", output.RawError, output.RawError)
	}
	if toolErr.Message != "Unknown function: missing" {
		t.Fatalf("ToolError.Message = %q, want unknown function message", toolErr.Message)
	}
}

func TestPerformToolExecutionsSuppressesOutputForStopResponse(t *testing.T) {
	output := executeOneToolCall(t, &fakeGenerationTool{
		name: "lookup",
		err:  llm.StopResponse{},
	})

	if output.FncCallOut != nil {
		t.Fatalf("FncCallOut = %#v, want nil for StopResponse", output.FncCallOut)
	}
	if output.RawError == nil {
		t.Fatal("RawError = nil, want StopResponse")
	}
}

func TestPerformToolExecutionsIgnoresCallsWhenToolChoiceNone(t *testing.T) {
	toolCtx := llm.NewToolContext([]interface{}{&fakeGenerationTool{name: "lookup", result: "ignored"}})
	functionCh := make(chan *llm.FunctionToolCall, 1)
	functionCh <- &llm.FunctionToolCall{
		Name:      "lookup",
		CallID:    "call_lookup",
		Arguments: `{}`,
	}
	close(functionCh)

	outCh := PerformToolExecutions(context.Background(), functionCh, toolCtx, WithToolExecutionToolChoice("none"))

	if output, ok := <-outCh; ok {
		t.Fatalf("PerformToolExecutions emitted %#v, want no output when tool_choice is none", output)
	}
}

func TestPerformTTSInferenceEndsStreamInput(t *testing.T) {
	providerStream := newEndInputGenerationTTSStream()
	provider := &fakeGenerationTTS{stream: providerStream}
	textCh := make(chan string, 1)
	textCh <- "hello"
	close(textCh)

	data, err := PerformTTSInference(context.Background(), provider, textCh)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}

	select {
	case frame, ok := <-data.AudioCh:
		if !ok {
			t.Fatal("AudioCh closed before audio, want audio after EndInput")
		}
		if frame == nil {
			t.Fatal("audio frame = nil, want synthesized audio frame")
		}
		if string(frame.Data) != "audio" {
			t.Fatalf("audio data = %q, want audio", frame.Data)
		}
		if frame.SampleRate != 24000 || frame.NumChannels != 1 || frame.SamplesPerChannel != 2 {
			t.Fatalf("audio format = %d/%d/%d, want 24000/1/2", frame.SampleRate, frame.NumChannels, frame.SamplesPerChannel)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for TTS audio after input end")
	}

	wantCalls := []string{"push:hello", "end_input"}
	if got := providerStream.calls; len(got) != len(wantCalls) || got[0] != wantCalls[0] || got[1] != wantCalls[1] {
		t.Fatalf("stream calls = %#v, want %#v", got, wantCalls)
	}
}

func TestPerformTTSInferenceRecordsPushTextError(t *testing.T) {
	cause := errors.New("push text failed")
	providerStream := newEndInputGenerationTTSStream()
	providerStream.pushErr = cause
	provider := &fakeGenerationTTS{stream: providerStream}
	textCh := make(chan string, 1)
	textCh <- "hello"
	close(textCh)

	data, err := PerformTTSInference(context.Background(), provider, textCh)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v, want nil startup error", err)
	}

	select {
	case _, ok := <-data.AudioCh:
		if ok {
			t.Fatal("AudioCh emitted audio after PushText error")
		}
	case <-time.After(time.Second):
		t.Fatal("AudioCh did not close after PushText error")
	}
	if !errors.Is(data.StreamErr, cause) {
		t.Fatalf("StreamErr = %v, want %v", data.StreamErr, cause)
	}
}

func TestPerformTTSInferenceRecordsEndInputError(t *testing.T) {
	cause := errors.New("end input failed")
	providerStream := newEndInputGenerationTTSStream()
	providerStream.endErr = cause
	provider := &fakeGenerationTTS{stream: providerStream}
	textCh := make(chan string, 1)
	textCh <- "hello"
	close(textCh)

	data, err := PerformTTSInference(context.Background(), provider, textCh)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v, want nil startup error", err)
	}

	select {
	case _, ok := <-data.AudioCh:
		if ok {
			t.Fatal("AudioCh emitted audio after EndInput error")
		}
	case <-time.After(time.Second):
		t.Fatal("AudioCh did not close after EndInput error")
	}
	if !errors.Is(data.StreamErr, cause) {
		t.Fatalf("StreamErr = %v, want %v", data.StreamErr, cause)
	}
}

func TestPerformTTSInferenceFiltersMarkdownAcrossChunks(t *testing.T) {
	providerStream := newEndInputGenerationTTSStream()
	provider := &fakeGenerationTTS{stream: providerStream}
	textCh := make(chan string, 2)
	textCh <- "Say **bo"
	textCh <- "ld** now"
	close(textCh)

	data, err := PerformTTSInference(context.Background(), provider, textCh)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}
	<-data.AudioCh

	got := providerStream.calls
	if len(got) == 0 || got[len(got)-1] != "end_input" {
		t.Fatalf("stream calls = %#v, want final end_input", got)
	}
	var pushed strings.Builder
	for _, call := range got[:len(got)-1] {
		if !strings.HasPrefix(call, "push:") {
			t.Fatalf("stream calls = %#v, want only push calls before end_input", got)
		}
		if strings.Contains(call, "**") {
			t.Fatalf("stream calls = %#v leaked markdown markers", got)
		}
		pushed.WriteString(strings.TrimPrefix(call, "push:"))
	}
	if want := "Say bold now"; pushed.String() != want {
		t.Fatalf("pushed text = %q, want %q; calls = %#v", pushed.String(), want, got)
	}
}

func TestPerformTTSInferenceCanDisableTextTransforms(t *testing.T) {
	providerStream := newEndInputGenerationTTSStream()
	provider := &fakeGenerationTTS{stream: providerStream}
	textCh := make(chan string, 2)
	textCh <- "Say **bo"
	textCh <- "ld** now"
	close(textCh)

	data, err := PerformTTSInference(context.Background(), provider, textCh, WithTTSTextTransformsDisabled())
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}
	<-data.AudioCh

	got := providerStream.calls
	if len(got) == 0 || got[len(got)-1] != "end_input" {
		t.Fatalf("stream calls = %#v, want final end_input", got)
	}
	var pushed strings.Builder
	for _, call := range got[:len(got)-1] {
		if !strings.HasPrefix(call, "push:") {
			t.Fatalf("stream calls = %#v, want only push calls before end_input", got)
		}
		pushed.WriteString(strings.TrimPrefix(call, "push:"))
	}
	if want := "Say **bold** now"; pushed.String() != want {
		t.Fatalf("pushed text = %q, want %q; calls = %#v", pushed.String(), want, got)
	}
}

func TestPerformTTSInferenceAppliesTextReplacements(t *testing.T) {
	providerStream := newEndInputGenerationTTSStream()
	provider := &fakeGenerationTTS{stream: providerStream}
	textCh := make(chan string, 2)
	textCh <- "Hello, "
	textCh <- "WORLD!"
	close(textCh)

	data, err := PerformTTSInference(
		context.Background(),
		provider,
		textCh,
		WithTTSTextReplacements(map[string]string{
			"hello": "hi",
			"world": "there",
		}),
	)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}
	<-data.AudioCh

	got := providerStream.calls
	if len(got) == 0 || got[len(got)-1] != "end_input" {
		t.Fatalf("stream calls = %#v, want final end_input", got)
	}
	var pushed strings.Builder
	for _, call := range got[:len(got)-1] {
		if !strings.HasPrefix(call, "push:") {
			t.Fatalf("stream calls = %#v, want only push calls before end_input", got)
		}
		pushed.WriteString(strings.TrimPrefix(call, "push:"))
	}
	if want := "hi, there!"; pushed.String() != want {
		t.Fatalf("pushed text = %q, want %q; calls = %#v", pushed.String(), want, got)
	}
}

func TestPerformTTSInferenceBuffersTextReplacementsAcrossRawChunks(t *testing.T) {
	providerStream := newEndInputGenerationTTSStream()
	provider := &fakeGenerationTTS{stream: providerStream}
	textCh := make(chan string, 2)
	textCh <- "Use Li"
	textCh <- "veKit now."
	close(textCh)

	data, err := PerformTTSInference(
		context.Background(),
		provider,
		textCh,
		WithTTSTextTransformsDisabled(),
		WithTTSTextReplacements(map[string]string{
			"LiveKit": "Cavos",
		}),
	)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}
	<-data.AudioCh

	got := providerStream.calls
	if len(got) == 0 || got[len(got)-1] != "end_input" {
		t.Fatalf("stream calls = %#v, want final end_input", got)
	}
	var pushed strings.Builder
	for _, call := range got[:len(got)-1] {
		if !strings.HasPrefix(call, "push:") {
			t.Fatalf("stream calls = %#v, want only push calls before end_input", got)
		}
		pushed.WriteString(strings.TrimPrefix(call, "push:"))
	}
	if want := "Use Cavos now."; pushed.String() != want {
		t.Fatalf("pushed text = %q, want %q; calls = %#v", pushed.String(), want, got)
	}
}

func TestPerformTTSInferenceUsesSynthesizeForNonStreamingTTS(t *testing.T) {
	provider := &fakeGenerationChunkedTTS{
		stream: &fakeGenerationChunkedStream{
			frames: []*model.AudioFrame{
				{
					Data:              []byte("chunked"),
					SampleRate:        24000,
					NumChannels:       1,
					SamplesPerChannel: 3,
				},
			},
		},
	}
	textCh := make(chan string, 2)
	textCh <- "Say **he"
	textCh <- "llo** 😊"
	close(textCh)

	data, err := PerformTTSInference(context.Background(), provider, textCh)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}

	frame, ok := <-data.AudioCh
	if !ok {
		t.Fatal("AudioCh closed before audio, want synthesized audio frame")
	}
	if string(frame.Data) != "chunked" {
		t.Fatalf("audio data = %q, want chunked", frame.Data)
	}
	if provider.synthesizeText != "Say hello" {
		t.Fatalf("synthesize text = %q, want transformed text", provider.synthesizeText)
	}
	if !provider.stream.closed {
		t.Fatal("chunked stream was not closed")
	}
	if _, ok := <-data.AudioCh; ok {
		t.Fatal("AudioCh produced extra frame")
	}
}

func TestPerformTTSInferenceErrorsWhenNonStreamingTTSProducesNoAudio(t *testing.T) {
	provider := &fakeGenerationChunkedTTS{
		stream: &fakeGenerationChunkedStream{},
	}
	textCh := make(chan string, 1)
	textCh <- "hello"
	close(textCh)

	data, err := PerformTTSInference(context.Background(), provider, textCh)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}

	if _, ok := <-data.AudioCh; ok {
		t.Fatal("AudioCh emitted audio, want closed stream")
	}
	if data.StreamErr == nil {
		t.Fatal("StreamErr = nil, want no-audio error")
	}
	if !strings.Contains(data.StreamErr.Error(), "no audio frames") {
		t.Fatalf("StreamErr = %v, want no-audio error", data.StreamErr)
	}
	if provider.synthesizeText != "hello" {
		t.Fatalf("synthesize text = %q, want hello", provider.synthesizeText)
	}
	if !provider.stream.closed {
		t.Fatal("chunked stream was not closed")
	}
}

func TestPerformTTSInferenceAllowsEmptyTransformedTextWithoutAudio(t *testing.T) {
	provider := &fakeGenerationChunkedTTS{
		stream: &fakeGenerationChunkedStream{},
	}
	textCh := make(chan string, 1)
	textCh <- "   "
	close(textCh)

	data, err := PerformTTSInference(context.Background(), provider, textCh)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}

	if _, ok := <-data.AudioCh; ok {
		t.Fatal("AudioCh emitted audio, want closed stream")
	}
	if data.StreamErr != nil {
		t.Fatalf("StreamErr = %v, want nil for empty transformed text", data.StreamErr)
	}
	if provider.synthesizeText != "" {
		t.Fatalf("synthesize text = %q, want no synthesis call", provider.synthesizeText)
	}
}

func TestPerformTTSInferencePreservesNonStreamingTimedTranscript(t *testing.T) {
	timed := tts.TimedString{Text: "aligned chunk", StartTime: 0.25, EndTime: 0.5}
	provider := &fakeGenerationChunkedTTS{
		stream: &fakeGenerationChunkedStream{
			frames: []*model.AudioFrame{{
				Data:              []byte("chunked"),
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 3,
			}},
			timedTranscripts: [][]tts.TimedString{{timed}},
		},
	}
	textCh := make(chan string, 1)
	textCh <- "hello"
	close(textCh)

	data, err := PerformTTSInference(context.Background(), provider, textCh, WithTTSPreserveTimedTranscript())
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}

	if _, ok := <-data.AudioCh; !ok {
		t.Fatal("AudioCh closed before audio, want synthesized audio frame")
	}
	got, ok := <-data.TimedTextCh
	if !ok {
		t.Fatal("TimedTextCh closed before transcript, want provider timed transcript")
	}
	if got != timed {
		t.Fatalf("timed transcript = %#v, want %#v", got, timed)
	}
}

func TestPerformTTSInferenceUsesSentenceStreamPacerWhenEnabled(t *testing.T) {
	providerStream := newPacedGenerationTTSStream()
	provider := &fakeGenerationTTS{stream: providerStream}
	textCh := make(chan string, 2)
	textCh <- "This is the first sentence. "
	textCh <- "This is the second sentence."
	close(textCh)

	data, err := PerformTTSInference(
		context.Background(),
		provider,
		textCh,
		WithTTSStreamPacer(tts.SentenceStreamPacerOptions{
			MinRemainingAudio: time.Nanosecond,
			MaxTextLength:     100,
		}),
	)
	if err != nil {
		t.Fatalf("PerformTTSInference error = %v", err)
	}

	for range data.AudioCh {
	}

	got := providerStream.calls
	if len(got) < 3 {
		t.Fatalf("stream calls = %#v, want at least first sentence, second sentence, end input", got)
	}
	if got[0] != "push:This is the first sentence." {
		t.Fatalf("first stream call = %q, want first complete sentence; calls = %#v", got[0], got)
	}
	if got[len(got)-1] != "end_input" {
		t.Fatalf("last stream call = %q, want end_input; calls = %#v", got[len(got)-1], got)
	}
	if !strings.Contains(strings.Join(got[1:len(got)-1], " "), "This is the second sentence") {
		t.Fatalf("stream calls = %#v, want second sentence before end_input", got)
	}
}

func TestPerformToolExecutionsProvidesRunContext(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	jobCtx := struct{ jobID string }{jobID: "job-a"}
	session.SetJobContext(jobCtx)
	tool := &runContextRecordingTool{}
	toolCtx := llm.NewToolContext([]interface{}{tool})
	functionCh := make(chan *llm.FunctionToolCall, 1)
	functionCh <- &llm.FunctionToolCall{
		ID:        "reply-a/fnc_0",
		Name:      tool.Name(),
		CallID:    "call_lookup",
		Arguments: `{"city": "Jakarta"}`,
		Extra:     map[string]any{"provider": "test"},
	}
	close(functionCh)

	outputs := PerformToolExecutions(context.Background(), functionCh, toolCtx, WithToolExecutionSession(session))
	output, ok := <-outputs
	if !ok {
		t.Fatal("PerformToolExecutions closed without output")
	}
	if output.RawError != nil {
		t.Fatalf("RawError = %v, want nil", output.RawError)
	}
	if tool.runContext == nil {
		t.Fatal("tool run context is nil")
	}
	if tool.runContext.Session != session {
		t.Fatal("tool run context Session was not set")
	}
	if tool.runContext.FunctionCall == nil {
		t.Fatal("tool run context FunctionCall is nil")
	}
	if tool.runContext.FunctionCall.ID != "reply-a/fnc_0" {
		t.Fatalf("tool run context FunctionCall.ID = %q, want generated item id", tool.runContext.FunctionCall.ID)
	}
	if tool.runContext.FunctionCall.CallID != "call_lookup" {
		t.Fatalf("tool run context FunctionCall.CallID = %q, want call_lookup", tool.runContext.FunctionCall.CallID)
	}
	if tool.runContext.FunctionCall.Arguments != `{"city":"Jakarta"}` {
		t.Fatalf("tool run context FunctionCall.Arguments = %q, want canonical JSON", tool.runContext.FunctionCall.Arguments)
	}
	if got := tool.runContext.FunctionCall.Extra["provider"]; got != "test" {
		t.Fatalf("tool run context FunctionCall.Extra[provider] = %#v, want test", got)
	}
	if tool.runContext.FunctionCall.CreatedAt.IsZero() {
		t.Fatal("tool run context FunctionCall.CreatedAt is zero")
	}
	if got, err := tool.runContext.JobContext(); err != nil || got != jobCtx {
		t.Fatalf("tool run context JobContext() = %#v, %v; want %#v, nil", got, err, jobCtx)
	}
}

func TestPerformToolExecutionsUsesFirstRunContextUpdateAsOutput(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	tool := &runContextUpdatingTool{}
	toolCtx := llm.NewToolContext([]interface{}{tool})
	functionCh := make(chan *llm.FunctionToolCall, 1)
	functionCh <- &llm.FunctionToolCall{
		ID:        "reply-a/fnc_0",
		Name:      tool.Name(),
		CallID:    "call_lookup",
		Arguments: `{"city": "Jakarta"}`,
		Extra:     map[string]any{"provider": "test"},
	}
	close(functionCh)

	outputs := PerformToolExecutions(context.Background(), functionCh, toolCtx, WithToolExecutionSession(session))
	output, ok := <-outputs
	if !ok {
		t.Fatal("PerformToolExecutions closed without output")
	}
	if output.RawError != nil {
		t.Fatalf("RawError = %v, want nil", output.RawError)
	}

	want := "The tool `lookup` has updated, message: searching\nThe task is still running, so DON'T make up or give information not included in the message above."
	if output.RawOutput != want {
		t.Fatalf("RawOutput = %#v, want first update message %q", output.RawOutput, want)
	}
	if output.FncCall.CallID != "call_lookup" || output.FncCall.Name != "lookup" || output.FncCall.Arguments != `{"city":"Jakarta"}` {
		t.Fatalf("FncCall = %#v, want update call with original call id and canonical arguments", output.FncCall)
	}
	if output.FncCallOut == nil || output.FncCallOut.CallID != "call_lookup" || output.FncCallOut.Output != want || output.FncCallOut.IsError {
		t.Fatalf("FncCallOut = %#v, want successful first update output", output.FncCallOut)
	}
	if tool.runContext == nil || tool.runContext.FunctionCall == nil {
		t.Fatal("tool run context/function call was not captured")
	}
	if got := tool.runContext.FunctionCall.Extra["__livekit_agents_tool_non_blocking"]; got != true {
		t.Fatalf("run context nonblocking extra = %#v, want true after first update", got)
	}
}

func TestPerformToolExecutionsRejectsDuplicateInFlightCallID(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	tool := &blockingGenerationTool{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	toolCtx := llm.NewToolContext([]interface{}{tool})
	functionCh := make(chan *llm.FunctionToolCall, 2)
	functionCh <- &llm.FunctionToolCall{Name: tool.Name(), CallID: "call_lookup", Arguments: `{}`}
	functionCh <- &llm.FunctionToolCall{Name: tool.Name(), CallID: "call_lookup", Arguments: `{}`}
	close(functionCh)

	outputs := PerformToolExecutions(context.Background(), functionCh, toolCtx, WithToolExecutionSession(session))
	select {
	case <-tool.started:
	case <-testTimeout():
		t.Fatal("first tool call did not start")
	}
	select {
	case <-tool.started:
		t.Fatal("duplicate in-flight tool call started; want duplicate call_id rejected before execution")
	case <-time.After(20 * time.Millisecond):
	}

	close(tool.release)
	var got []ToolExecutionOutput
	for output := range outputs {
		got = append(got, output)
	}
	if len(got) != 2 {
		t.Fatalf("outputs len = %d, want success plus duplicate rejection", len(got))
	}
	var duplicateErr bool
	var success bool
	for _, output := range got {
		if output.RawError != nil && strings.Contains(output.RawError.Error(), "Task already running for call_id: call_lookup") {
			duplicateErr = true
		}
		if output.RawOutput == "ok" && output.RawError == nil {
			success = true
		}
	}
	if !duplicateErr {
		t.Fatalf("outputs = %#v, want duplicate call_id error", got)
	}
	if !success {
		t.Fatalf("outputs = %#v, want first call to complete successfully", got)
	}
}

func TestPerformToolExecutionsRejectsDuplicateInFlightFunctionName(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	tool := &blockingGenerationTool{
		duplicateMode: llm.ToolDuplicateModeReject,
		started:       make(chan struct{}, 2),
		release:       make(chan struct{}),
	}
	toolCtx := llm.NewToolContext([]interface{}{tool})
	functionCh := make(chan *llm.FunctionToolCall, 2)
	functionCh <- &llm.FunctionToolCall{Name: tool.Name(), CallID: "call_lookup_a", Arguments: `{}`}
	functionCh <- &llm.FunctionToolCall{Name: tool.Name(), CallID: "call_lookup_b", Arguments: `{}`}
	close(functionCh)

	outputs := PerformToolExecutions(context.Background(), functionCh, toolCtx, WithToolExecutionSession(session))
	select {
	case <-tool.started:
	case <-testTimeout():
		t.Fatal("first tool call did not start")
	}
	select {
	case <-tool.started:
		t.Fatal("duplicate in-flight tool call started; want same-name reject mode to block execution")
	case <-time.After(20 * time.Millisecond):
	}

	close(tool.release)
	var got []ToolExecutionOutput
	for output := range outputs {
		got = append(got, output)
	}
	if len(got) != 2 {
		t.Fatalf("outputs len = %d, want success plus duplicate rejection", len(got))
	}
	var duplicateErr bool
	var success bool
	for _, output := range got {
		if output.RawOutput == "ok" && output.RawError == nil {
			success = true
		}
		if output.RawError != nil && strings.Contains(output.RawError.Error(), "Same tool `lookup` is already running") {
			duplicateErr = true
			if output.FncCallOut == nil || !output.FncCallOut.IsError || !strings.Contains(output.FncCallOut.Output, "call_lookup_a") {
				t.Fatalf("duplicate output = %#v, want visible duplicate details", output.FncCallOut)
			}
		}
	}
	if !duplicateErr {
		t.Fatalf("outputs = %#v, want same-name duplicate rejection", got)
	}
	if !success {
		t.Fatalf("outputs = %#v, want first call to complete successfully", got)
	}
}

func TestPerformToolExecutionsDetachesRunContextAfterReturn(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	tool := &runContextRecordingTool{}
	toolCtx := llm.NewToolContext([]interface{}{tool})
	functionCh := make(chan *llm.FunctionToolCall, 1)
	functionCh <- &llm.FunctionToolCall{
		ID:        "reply-a/fnc_0",
		Name:      tool.Name(),
		CallID:    "call_lookup",
		Arguments: `{}`,
	}
	close(functionCh)

	outputs := PerformToolExecutions(context.Background(), functionCh, toolCtx, WithToolExecutionSession(session))
	output, ok := <-outputs
	if !ok {
		t.Fatal("PerformToolExecutions closed without output")
	}
	if output.RawError != nil || output.RawOutput != "ok" {
		t.Fatalf("output = %#v, want successful tool result", output)
	}
	if tool.runContext == nil {
		t.Fatal("tool run context was not captured")
	}

	if err := tool.runContext.Update("late progress"); err != nil {
		t.Fatalf("late run context update returned error: %v", err)
	}
	if updates := tool.runContext.Updates(); len(updates) != 0 {
		t.Fatalf("late run context updates = %#v, want detached context to ignore updates", updates)
	}
}

func TestPerformToolExecutionsUsesScopedMockTool(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	toolCtx := llm.NewToolContext([]interface{}{&fakeGenerationTool{name: "lookup", result: "real"}})
	functionCh := make(chan *llm.FunctionToolCall, 1)
	functionCh <- &llm.FunctionToolCall{
		Name:      "lookup",
		CallID:    "call_lookup",
		Arguments: `{"city":"Jakarta"}`,
	}
	close(functionCh)
	ctx := MockTools(context.Background(), session.Agent, map[string]MockToolFunc{
		"lookup": func(ctx context.Context, args string) (string, error) {
			if GetRunContext(ctx) == nil {
				t.Fatal("mock tool run context is nil")
			}
			if args != `{"city":"Jakarta"}` {
				t.Fatalf("mock args = %q, want canonical JSON arguments", args)
			}
			return "mocked", nil
		},
	})

	outputs := PerformToolExecutions(ctx, functionCh, toolCtx, WithToolExecutionSession(session))
	output, ok := <-outputs
	if !ok {
		t.Fatal("PerformToolExecutions closed without output")
	}
	if output.RawError != nil {
		t.Fatalf("RawError = %v, want nil", output.RawError)
	}
	if output.RawOutput != "mocked" {
		t.Fatalf("RawOutput = %v, want mocked", output.RawOutput)
	}
	if output.FncCallOut == nil || output.FncCallOut.Output != "mocked" || output.FncCallOut.IsError {
		t.Fatalf("FncCallOut = %#v, want mocked successful output", output.FncCallOut)
	}
}

func TestPerformToolExecutionsSnapshotsToolContext(t *testing.T) {
	original := &fakeGenerationTool{name: "lookup", result: "original"}
	replacement := &fakeGenerationTool{name: "lookup", result: "replacement"}
	toolCtx := llm.NewToolContext([]interface{}{original})
	functionCh := make(chan *llm.FunctionToolCall)

	outputs := PerformToolExecutions(context.Background(), functionCh, toolCtx)
	if err := toolCtx.UpdateTools([]interface{}{replacement}); err != nil {
		t.Fatalf("UpdateTools() error = %v", err)
	}
	functionCh <- &llm.FunctionToolCall{
		Name:      "lookup",
		CallID:    "call_lookup",
		Arguments: `{}`,
	}
	close(functionCh)

	output, ok := <-outputs
	if !ok {
		t.Fatal("PerformToolExecutions closed without output")
	}
	if output.RawError != nil {
		t.Fatalf("RawError = %v, want nil", output.RawError)
	}
	if output.RawOutput != "original" {
		t.Fatalf("RawOutput = %v, want original snapshot result", output.RawOutput)
	}
}

func executeOneToolCall(t *testing.T, tool llm.Tool) ToolExecutionOutput {
	t.Helper()

	toolCtx := llm.NewToolContext([]interface{}{tool})
	functionCh := make(chan *llm.FunctionToolCall, 1)
	functionCh <- &llm.FunctionToolCall{
		Name:      tool.Name(),
		CallID:    "call_lookup",
		Arguments: `{}`,
	}
	close(functionCh)

	outCh := PerformToolExecutions(context.Background(), functionCh, toolCtx)
	output, ok := <-outCh
	if !ok {
		t.Fatal("PerformToolExecutions closed without output")
	}
	return output
}

func assertSpanEvent(t *testing.T, event sdktrace.Event, wantName string, wantAttrs map[string]string) {
	t.Helper()
	if event.Name != wantName {
		t.Fatalf("event name = %q, want %q", event.Name, wantName)
	}
	attrs := spanEventAttributes(event.Attributes)
	for key, want := range wantAttrs {
		if attrs[key] != want {
			t.Fatalf("event %q attr %q = %q, want %q; attrs=%#v", event.Name, key, attrs[key], want, attrs)
		}
	}
}

func spanEventAttributes(attrs []attribute.KeyValue) map[string]string {
	values := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		values[string(attr.Key)] = attr.Value.AsString()
	}
	return values
}

func spanEventAttributeValues(attrs []attribute.KeyValue) map[string]attribute.Value {
	values := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		values[string(attr.Key)] = attr.Value
	}
	return values
}

func spanAttributes(attrs []attribute.KeyValue) map[string]string {
	values := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		values[string(attr.Key)] = attr.Value.AsString()
	}
	return values
}

func spanAttributeValues(attrs []attribute.KeyValue) map[string]attribute.Value {
	values := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		values[string(attr.Key)] = attr.Value
	}
	return values
}

type fakeGenerationTool struct {
	name          string
	result        string
	err           error
	flags         llm.ToolFlag
	duplicateMode llm.ToolDuplicateMode
}

func (f *fakeGenerationTool) ID() string { return f.name }

func (f *fakeGenerationTool) Name() string { return f.name }

func (f *fakeGenerationTool) Description() string { return "" }

func (f *fakeGenerationTool) Parameters() map[string]any { return nil }

func (f *fakeGenerationTool) Execute(context.Context, string) (string, error) {
	return f.result, f.err
}

func (f *fakeGenerationTool) ToolFlags() llm.ToolFlag { return f.flags }

func (f *fakeGenerationTool) ToolDuplicateMode() llm.ToolDuplicateMode {
	return f.duplicateMode
}

type fakeGenerationProviderTool struct {
	fakeGenerationTool
}

func (f *fakeGenerationProviderTool) IsProviderTool() bool { return true }

type fakeGenerationToolset struct {
	id    string
	tools []llm.Tool
}

func (f *fakeGenerationToolset) ID() string { return f.id }

func (f *fakeGenerationToolset) Tools() []llm.Tool { return f.tools }

type runContextRecordingTool struct {
	runContext *RunContext
}

func (r *runContextRecordingTool) ID() string { return "lookup" }

func (r *runContextRecordingTool) Name() string { return "lookup" }

func (r *runContextRecordingTool) Description() string { return "" }

func (r *runContextRecordingTool) Parameters() map[string]any { return nil }

func (r *runContextRecordingTool) Execute(ctx context.Context, args string) (string, error) {
	r.runContext = GetRunContext(ctx)
	return "ok", nil
}

type runContextUpdatingTool struct {
	runContext *RunContext
}

func (r *runContextUpdatingTool) ID() string { return "lookup" }

func (r *runContextUpdatingTool) Name() string { return "lookup" }

func (r *runContextUpdatingTool) Description() string { return "" }

func (r *runContextUpdatingTool) Parameters() map[string]any { return nil }

func (r *runContextUpdatingTool) Execute(ctx context.Context, args string) (string, error) {
	r.runContext = GetRunContext(ctx)
	if r.runContext != nil {
		if err := r.runContext.Update("searching"); err != nil {
			return "", err
		}
	}
	return "final result", nil
}

type blockingGenerationTool struct {
	duplicateMode llm.ToolDuplicateMode
	started       chan struct{}
	release       chan struct{}
}

func (b *blockingGenerationTool) ID() string { return "lookup" }

func (b *blockingGenerationTool) Name() string { return "lookup" }

func (b *blockingGenerationTool) Description() string { return "" }

func (b *blockingGenerationTool) Parameters() map[string]any { return nil }

func (b *blockingGenerationTool) ToolDuplicateMode() llm.ToolDuplicateMode {
	return b.duplicateMode
}

func (b *blockingGenerationTool) Execute(context.Context, string) (string, error) {
	b.started <- struct{}{}
	<-b.release
	return "ok", nil
}

type fakeGenerationLLM struct {
	llm.MetricsEmitter
	llm.ErrorEmitter

	stream       llm.LLMStream
	streams      []llm.LLMStream
	calls        []llm.ChatOptions
	chatContexts []*llm.ChatContext
	model        string
	provider     string
}

func (f *fakeGenerationLLM) Chat(_ context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	var options llm.ChatOptions
	for _, opt := range opts {
		opt(&options)
	}
	f.calls = append(f.calls, options)
	f.chatContexts = append(f.chatContexts, chatCtx)
	if len(f.streams) > 0 {
		stream := f.streams[0]
		f.streams = f.streams[1:]
		return stream, nil
	}
	return f.stream, nil
}

func (f *fakeGenerationLLM) Model() string { return f.model }

func (f *fakeGenerationLLM) Provider() string { return f.provider }

type fakeGenerationLLMStream struct {
	chunks []*llm.ChatChunk
	index  int
	err    error
}

func (f *fakeGenerationLLMStream) Next() (*llm.ChatChunk, error) {
	if f.index >= len(f.chunks) {
		if f.err != nil {
			err := f.err
			f.err = nil
			return nil, err
		}
		return nil, io.EOF
	}
	chunk := f.chunks[f.index]
	f.index++
	return chunk, nil
}

func (f *fakeGenerationLLMStream) Close() error { return nil }

type fakeGenerationTTS struct {
	model    string
	provider string
	stream   tts.SynthesizeStream
}

func (f *fakeGenerationTTS) Label() string { return "fake-generation-tts" }

func (f *fakeGenerationTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}

func (f *fakeGenerationTTS) SampleRate() int { return 24000 }

func (f *fakeGenerationTTS) NumChannels() int { return 1 }

func (f *fakeGenerationTTS) Model() string { return f.model }

func (f *fakeGenerationTTS) Provider() string { return f.provider }

func (f *fakeGenerationTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, nil
}

func (f *fakeGenerationTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	return f.stream, nil
}

type fakeGenerationTTSStream struct {
	audio  []*tts.SynthesizedAudio
	index  int
	closed bool
}

func (f *fakeGenerationTTSStream) PushText(string) error { return nil }

func (f *fakeGenerationTTSStream) Flush() error { return nil }

func (f *fakeGenerationTTSStream) Close() error {
	f.closed = true
	return nil
}

func (f *fakeGenerationTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if f.index >= len(f.audio) {
		return nil, io.EOF
	}
	audio := f.audio[f.index]
	f.index++
	return audio, nil
}

type fakeGenerationChunkedTTS struct {
	stream         *fakeGenerationChunkedStream
	synthesizeText string
}

func (f *fakeGenerationChunkedTTS) Label() string { return "fake-generation-chunked-tts" }

func (f *fakeGenerationChunkedTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false}
}

func (f *fakeGenerationChunkedTTS) SampleRate() int { return 24000 }

func (f *fakeGenerationChunkedTTS) NumChannels() int { return 1 }

func (f *fakeGenerationChunkedTTS) Synthesize(_ context.Context, text string) (tts.ChunkedStream, error) {
	f.synthesizeText = text
	return f.stream, nil
}

func (f *fakeGenerationChunkedTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	return nil, errors.New("stream should not be called")
}

type fakeGenerationChunkedStream struct {
	frames           []*model.AudioFrame
	timedTranscripts [][]tts.TimedString
	index            int
	closed           bool
}

func (s *fakeGenerationChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.index >= len(s.frames) {
		return nil, io.EOF
	}
	frame := s.frames[s.index]
	var timedTranscript []tts.TimedString
	if s.index < len(s.timedTranscripts) {
		timedTranscript = s.timedTranscripts[s.index]
	}
	s.index++
	return &tts.SynthesizedAudio{Frame: frame, TimedTranscript: timedTranscript}, nil
}

func (s *fakeGenerationChunkedStream) Close() error {
	s.closed = true
	return nil
}

type endInputGenerationTTSStream struct {
	calls   []string
	ended   chan struct{}
	closed  bool
	emitted bool
	pushErr error
	endErr  error
}

func newEndInputGenerationTTSStream() *endInputGenerationTTSStream {
	return &endInputGenerationTTSStream{
		ended: make(chan struct{}),
	}
}

func (s *endInputGenerationTTSStream) PushText(text string) error {
	s.calls = append(s.calls, "push:"+text)
	if s.pushErr != nil {
		return s.pushErr
	}
	return nil
}

func (s *endInputGenerationTTSStream) Flush() error {
	s.calls = append(s.calls, "flush")
	return nil
}

func (s *endInputGenerationTTSStream) EndInput() error {
	s.calls = append(s.calls, "end_input")
	if s.endErr != nil {
		return s.endErr
	}
	select {
	case <-s.ended:
	default:
		close(s.ended)
	}
	return nil
}

func (s *endInputGenerationTTSStream) Close() error {
	s.closed = true
	select {
	case <-s.ended:
	default:
		close(s.ended)
	}
	return nil
}

func (s *endInputGenerationTTSStream) Next() (*tts.SynthesizedAudio, error) {
	<-s.ended
	if s.emitted || s.closed {
		return nil, io.EOF
	}
	s.emitted = true
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              []byte("audio"),
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		},
	}, nil
}

type pacedGenerationTTSStream struct {
	calls  []string
	events chan string
	ended  bool
	closed bool
}

func newPacedGenerationTTSStream() *pacedGenerationTTSStream {
	return &pacedGenerationTTSStream{
		events: make(chan string, 10),
	}
}

func (s *pacedGenerationTTSStream) PushText(text string) error {
	s.calls = append(s.calls, "push:"+text)
	s.events <- "audio"
	return nil
}

func (s *pacedGenerationTTSStream) Flush() error {
	s.calls = append(s.calls, "flush")
	return nil
}

func (s *pacedGenerationTTSStream) EndInput() error {
	s.calls = append(s.calls, "end_input")
	s.ended = true
	close(s.events)
	return nil
}

func (s *pacedGenerationTTSStream) Close() error {
	if !s.closed {
		s.closed = true
		if !s.ended {
			close(s.events)
		}
	}
	return nil
}

func (s *pacedGenerationTTSStream) Next() (*tts.SynthesizedAudio, error) {
	_, ok := <-s.events
	if !ok {
		return nil, io.EOF
	}
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              []byte("audio"),
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 1,
		},
	}, nil
}

func drainFunctionCalls(ch <-chan *llm.FunctionToolCall) []*llm.FunctionToolCall {
	calls := make([]*llm.FunctionToolCall, 0)
	for call := range ch {
		calls = append(calls, call)
	}
	return calls
}

func drainStrings(ch <-chan string) []string {
	values := make([]string, 0)
	for value := range ch {
		values = append(values, value)
	}
	return values
}

func drainAudioFrames(ch <-chan *model.AudioFrame) []*model.AudioFrame {
	frames := make([]*model.AudioFrame, 0)
	for frame := range ch {
		frames = append(frames, frame)
	}
	return frames
}
