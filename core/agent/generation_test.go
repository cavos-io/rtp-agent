package agent

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestPerformLLMInferenceIgnoresNonFunctionToolCalls(t *testing.T) {
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{ToolCalls: []llm.FunctionToolCall{
					{Type: "custom", Name: "ignored", CallID: "call_ignored"},
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
		if string(frame.Data) != "audio" {
			t.Fatalf("audio data = %q, want audio", frame.Data)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for TTS audio after input end")
	}

	wantCalls := []string{"push:hello", "end_input"}
	if got := providerStream.calls; len(got) != len(wantCalls) || got[0] != wantCalls[0] || got[1] != wantCalls[1] {
		t.Fatalf("stream calls = %#v, want %#v", got, wantCalls)
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

type fakeGenerationTool struct {
	name   string
	result string
	err    error
}

func (f *fakeGenerationTool) ID() string { return f.name }

func (f *fakeGenerationTool) Name() string { return f.name }

func (f *fakeGenerationTool) Description() string { return "" }

func (f *fakeGenerationTool) Parameters() map[string]any { return nil }

func (f *fakeGenerationTool) Execute(context.Context, string) (string, error) {
	return f.result, f.err
}

type fakeGenerationLLM struct {
	stream  llm.LLMStream
	streams []llm.LLMStream
	calls   []llm.ChatOptions
}

func (f *fakeGenerationLLM) Chat(_ context.Context, _ *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	var options llm.ChatOptions
	for _, opt := range opts {
		opt(&options)
	}
	f.calls = append(f.calls, options)
	if len(f.streams) > 0 {
		stream := f.streams[0]
		f.streams = f.streams[1:]
		return stream, nil
	}
	return f.stream, nil
}

type fakeGenerationLLMStream struct {
	chunks []*llm.ChatChunk
	index  int
}

func (f *fakeGenerationLLMStream) Next() (*llm.ChatChunk, error) {
	if f.index >= len(f.chunks) {
		return nil, io.EOF
	}
	chunk := f.chunks[f.index]
	f.index++
	return chunk, nil
}

func (f *fakeGenerationLLMStream) Close() error { return nil }

type fakeGenerationTTS struct {
	stream tts.SynthesizeStream
}

func (f *fakeGenerationTTS) Label() string { return "fake-generation-tts" }

func (f *fakeGenerationTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}

func (f *fakeGenerationTTS) SampleRate() int { return 24000 }

func (f *fakeGenerationTTS) NumChannels() int { return 1 }

func (f *fakeGenerationTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, nil
}

func (f *fakeGenerationTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	return f.stream, nil
}

type endInputGenerationTTSStream struct {
	calls   []string
	ended   chan struct{}
	closed  bool
	emitted bool
}

func newEndInputGenerationTTSStream() *endInputGenerationTTSStream {
	return &endInputGenerationTTSStream{
		ended: make(chan struct{}),
	}
}

func (s *endInputGenerationTTSStream) PushText(text string) error {
	s.calls = append(s.calls, "push:"+text)
	return nil
}

func (s *endInputGenerationTTSStream) Flush() error {
	s.calls = append(s.calls, "flush")
	return nil
}

func (s *endInputGenerationTTSStream) EndInput() error {
	s.calls = append(s.calls, "end_input")
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
