package agent

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"go.opentelemetry.io/otel/attribute"
)

type LLMGenerationData struct {
	TextCh             chan string
	FunctionCh         chan *llm.FunctionToolCall
	GeneratedText      string
	GeneratedFunctions []llm.FunctionToolCall
	GeneratedExtra     map[string]any
	TTFT               time.Duration
	Duration           time.Duration
	RequestID          string
	Usage              *llm.CompletionUsage
	StreamErr          error
}

func PerformLLMInference(
	ctx context.Context,
	l llm.LLM,
	chatCtx *llm.ChatContext,
	tools []llm.Tool,
	options ...llm.ChatOption,
) (*LLMGenerationData, error) {
	data := &LLMGenerationData{
		TextCh:         make(chan string, 100),
		FunctionCh:     make(chan *llm.FunctionToolCall, 10),
		GeneratedExtra: make(map[string]any),
	}

	chatOptions := make([]llm.ChatOption, 0, len(options)+1)
	toolItems := make([]interface{}, 0, len(tools))
	for _, tool := range tools {
		toolItems = append(toolItems, tool)
	}
	toolCtx := llm.NewToolContext(toolItems)
	chatOptions = append(chatOptions, llm.WithTools(toolCtx.Flatten()))
	chatOptions = append(chatOptions, options...)
	ctx, span := telemetry.NewLLMSpan(ctx, llm.Model(l), llm.Provider(l))
	span.SetAttributes(llmToolSpanAttributes(toolCtx)...)
	telemetry.AddChatTraceEvents(span, llmChatTraceEvents(chatCtx))
	stream, err := l.Chat(ctx, chatCtx, chatOptions...)
	if err != nil {
		span.End()
		return nil, err
	}

	go func() {
		defer close(data.TextCh)
		defer close(data.FunctionCh)
		defer stream.Close()
		defer span.End()

		startTime := time.Now()
		for {
			chunk, err := stream.Next()
			if err != nil {
				if err != io.EOF {
					data.StreamErr = err
				}
				break
			}

			if data.TTFT == 0 {
				data.TTFT = time.Since(startTime)
			}
			data.Duration = time.Since(startTime)
			if chunk.ID != "" {
				data.RequestID = chunk.ID
			}
			if chunk.Usage != nil {
				usage := *chunk.Usage
				data.Usage = &usage
			}

			if chunk.Delta != nil {
				for key, value := range chunk.Delta.Extra {
					data.GeneratedExtra[key] = value
				}
				if chunk.Delta.Content != "" {
					data.GeneratedText += chunk.Delta.Content
					data.TextCh <- chunk.Delta.Content
				}
				for _, fc := range chunk.Delta.ToolCalls {
					if fc.Type != "" && fc.Type != "function" {
						continue
					}
					f := fc
					data.GeneratedFunctions = append(data.GeneratedFunctions, f)
					data.FunctionCh <- &f
				}
			}
		}
	}()

	return data, nil
}

func llmChatTraceEvents(chatCtx *llm.ChatContext) []telemetry.ChatTraceEvent {
	if chatCtx == nil {
		return nil
	}
	events := make([]telemetry.ChatTraceEvent, 0, len(chatCtx.Items))
	for _, item := range chatCtx.Items {
		switch it := item.(type) {
		case *llm.ChatMessage:
			eventName := llmChatMessageTraceEventName(it.Role)
			if eventName == "" {
				continue
			}
			events = append(events, telemetry.ChatTraceEvent{
				Name:       eventName,
				Attributes: []attribute.KeyValue{attribute.String("content", it.TextContent())},
			})
		case *llm.FunctionCall:
			toolCall := map[string]any{
				"function": map[string]any{"name": it.Name, "arguments": it.Arguments},
				"id":       it.CallID,
				"type":     "function",
			}
			encoded, err := json.Marshal(toolCall)
			if err != nil {
				continue
			}
			events = append(events, telemetry.ChatTraceEvent{
				Name: telemetry.EventGenAIAssistantMessage,
				Attributes: []attribute.KeyValue{
					attribute.String("role", "assistant"),
					attribute.StringSlice("tool_calls", []string{string(encoded)}),
				},
			})
		case *llm.FunctionCallOutput:
			events = append(events, telemetry.ChatTraceEvent{
				Name: telemetry.EventGenAIToolMessage,
				Attributes: []attribute.KeyValue{
					attribute.String("content", it.Output),
					attribute.String("name", it.Name),
					attribute.String("id", it.CallID),
				},
			})
		}
	}
	return events
}

func llmChatMessageTraceEventName(role llm.ChatRole) string {
	switch role {
	case llm.ChatRoleSystem, llm.ChatRoleDeveloper:
		return telemetry.EventGenAISystemMessage
	case llm.ChatRoleUser:
		return telemetry.EventGenAIUserMessage
	case llm.ChatRoleAssistant:
		return telemetry.EventGenAIAssistantMessage
	default:
		return ""
	}
}

func llmToolSpanAttributes(toolCtx *llm.ToolContext) []attribute.KeyValue {
	if toolCtx == nil {
		return nil
	}

	functionTools := toolCtx.FunctionTools()
	functionToolNames := make([]string, 0, len(functionTools))
	for name := range functionTools {
		functionToolNames = append(functionToolNames, name)
	}
	sort.Strings(functionToolNames)

	providerTools := toolCtx.ProviderTools()
	providerToolTypes := make([]string, 0, len(providerTools))
	for _, tool := range providerTools {
		providerToolTypes = append(providerToolTypes, typeName(tool))
	}

	toolsets := toolCtx.Toolsets()
	toolsetTypes := make([]string, 0, len(toolsets))
	for _, toolset := range toolsets {
		toolsetTypes = append(toolsetTypes, typeName(toolset))
	}

	return []attribute.KeyValue{
		attribute.StringSlice(telemetry.AttrFunctionTools, functionToolNames),
		attribute.StringSlice(telemetry.AttrProviderTools, providerToolTypes),
		attribute.StringSlice(telemetry.AttrToolSets, toolsetTypes),
	}
}

func typeName(v any) string {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil {
		return ""
	}
	return t.Name()
}

type TTSGenerationData struct {
	AudioCh     chan *model.AudioFrame
	TimedTextCh chan tts.TimedString
	TTFB        time.Duration
	StreamErr   error
}

type TTSInferenceOptions struct {
	StreamPacer             *tts.SentenceStreamPacerOptions
	TextReplacements        map[string]string
	PreserveTimedTranscript bool
}

type TTSInferenceOption func(*TTSInferenceOptions)

func WithTTSStreamPacer(opts tts.SentenceStreamPacerOptions) TTSInferenceOption {
	return func(options *TTSInferenceOptions) {
		options.StreamPacer = &opts
	}
}

func WithTTSTextReplacements(replacements map[string]string) TTSInferenceOption {
	return func(options *TTSInferenceOptions) {
		options.TextReplacements = replacements
	}
}

func WithTTSPreserveTimedTranscript() TTSInferenceOption {
	return func(options *TTSInferenceOptions) {
		options.PreserveTimedTranscript = true
	}
}

func PerformTTSInference(ctx context.Context, t tts.TTS, textCh <-chan string, opts ...TTSInferenceOption) (*TTSGenerationData, error) {
	data := &TTSGenerationData{
		AudioCh:     make(chan *model.AudioFrame, 100),
		TimedTextCh: make(chan tts.TimedString, 100),
	}

	var options TTSInferenceOptions
	for _, opt := range opts {
		opt(&options)
	}

	if !t.Capabilities().Streaming {
		go func() {
			defer close(data.AudioCh)
			defer close(data.TimedTextCh)

			var text strings.Builder
			for chunk := range textCh {
				text.WriteString(chunk)
			}
			transformedText := strings.TrimSpace(applyTTSTextReplacements(tts.ApplyTextTransforms(text.String()), options.TextReplacements))
			if transformedText == "" {
				return
			}

			startTime := time.Now()
			stream, err := t.Synthesize(ctx, transformedText)
			if err != nil {
				data.StreamErr = err
				return
			}
			var frame *model.AudioFrame
			if options.PreserveTimedTranscript {
				var timedTranscript []tts.TimedString
				frame, timedTranscript, err = tts.CollectWithTimedTranscript(stream)
				if err != nil || frame == nil {
					if err != nil {
						data.StreamErr = err
					}
					return
				}
				for _, timedText := range timedTranscript {
					data.TimedTextCh <- timedText
				}
			} else {
				frame, err = tts.Collect(stream)
				if err != nil || frame == nil {
					if err != nil {
						data.StreamErr = err
					}
					return
				}
			}
			data.TTFB = time.Since(startTime)
			data.AudioCh <- frame
		}()
		return data, nil
	}

	stream, err := t.Stream(ctx)
	if err != nil {
		return nil, err
	}
	if options.StreamPacer != nil {
		if options.StreamPacer.MaxTextLength == 0 {
			stream = tts.NewSentenceStreamPacer(ctx, stream, options.StreamPacer.MinRemainingAudio)
		} else {
			stream = tts.NewSentenceStreamPacerWithOptions(ctx, stream, *options.StreamPacer)
		}
	}

	go func() {
		defer close(data.AudioCh)
		defer close(data.TimedTextCh)
		defer stream.Close()

		startTime := time.Now()
		transformBuffer := tts.NewTextTransformBuffer()

		for text := range textCh {
			for _, filteredText := range transformBuffer.Push(text) {
				filteredText = applyTTSTextReplacements(filteredText, options.TextReplacements)
				_ = stream.PushText(filteredText)
			}
		}
		for _, filteredText := range transformBuffer.Flush() {
			filteredText = applyTTSTextReplacements(filteredText, options.TextReplacements)
			_ = stream.PushText(filteredText)
		}
		_ = tts.EndSynthesizeStreamInput(stream)

		for {
			audio, err := stream.Next()
			if err != nil {
				if err != io.EOF {
					data.StreamErr = err
				}
				break
			}
			if data.TTFB == 0 {
				data.TTFB = time.Since(startTime)
			}
			for _, timedText := range audio.TimedTranscript {
				data.TimedTextCh <- timedText
			}
			data.AudioCh <- audio.Frame
		}
	}()

	return data, nil
}

func applyTTSTextReplacements(text string, replacements map[string]string) string {
	return tokenize.ReplaceWords(text, replacements)
}

type ToolExecutionOutput struct {
	FncCall    llm.FunctionCall
	FncCallOut *llm.FunctionCallOutput
	RawOutput  any
	RawError   error
}

type ToolExecutionOptions struct {
	Session      *AgentSession
	SpeechHandle *SpeechHandle
	ToolChoice   llm.ToolChoice
}

type ToolExecutionOption func(*ToolExecutionOptions)

func WithToolExecutionSession(session *AgentSession) ToolExecutionOption {
	return func(opts *ToolExecutionOptions) {
		opts.Session = session
	}
}

func WithToolExecutionSpeechHandle(speechHandle *SpeechHandle) ToolExecutionOption {
	return func(opts *ToolExecutionOptions) {
		opts.SpeechHandle = speechHandle
	}
}

func WithToolExecutionToolChoice(toolChoice llm.ToolChoice) ToolExecutionOption {
	return func(opts *ToolExecutionOptions) {
		opts.ToolChoice = toolChoice
	}
}

func PerformToolExecutions(
	ctx context.Context,
	functionCh <-chan *llm.FunctionToolCall,
	toolCtx *llm.ToolContext,
	opts ...ToolExecutionOption,
) <-chan ToolExecutionOutput {
	outCh := make(chan ToolExecutionOutput, 10)
	if toolCtx != nil {
		toolCtx = toolCtx.Copy()
	}
	var options ToolExecutionOptions
	for _, opt := range opts {
		opt(&options)
	}

	go func() {
		defer close(outCh)
		var wg sync.WaitGroup

		for fncCall := range functionCh {
			if options.ToolChoice == "none" {
				continue
			}
			wg.Add(1)
			go func(fc *llm.FunctionToolCall) {
				defer wg.Done()
				execCtx := ctx
				if options.Session != nil {
					functionCall := llm.FunctionCall{
						CallID:    fc.CallID,
						Name:      fc.Name,
						Arguments: fc.Arguments,
						Extra:     fc.Extra,
					}
					execCtx = WithRunContext(execCtx, NewRunContext(options.Session, options.SpeechHandle, &functionCall))
				}
				result := llm.ExecuteFunctionCall(execCtx, fc, toolCtx)
				outCh <- ToolExecutionOutput{
					FncCall:    result.FncCall,
					FncCallOut: result.FncCallOut,
					RawOutput:  result.RawOutput,
					RawError:   result.RawError,
				}
			}(fncCall)
		}

		wg.Wait()
	}()

	return outCh
}
