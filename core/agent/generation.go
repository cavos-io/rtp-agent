package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

type LLMGenerationData struct {
	TextCh             chan string
	TextEventCh        chan LLMTextEvent
	FunctionCh         chan *llm.FunctionToolCall
	Done               chan struct{}
	GeneratedText      string
	GeneratedFunctions []llm.FunctionToolCall
	GeneratedExtra     map[string]any
	ID                 string
	TTFT               time.Duration
	Duration           time.Duration
	RequestID          string
	Usage              *llm.CompletionUsage
	StreamErr          error
}

type LLMTextEvent struct {
	Text  string
	Flush bool
}

func PerformLLMInference(
	ctx context.Context,
	l llm.LLM,
	chatCtx *llm.ChatContext,
	tools []llm.Tool,
	options ...llm.ChatOption,
) (*LLMGenerationData, error) {
	return performLLMInference(ctx, l, chatCtx, tools, false, options...)
}

func PerformLLMInferenceWithTextEvents(
	ctx context.Context,
	l llm.LLM,
	chatCtx *llm.ChatContext,
	tools []llm.Tool,
	options ...llm.ChatOption,
) (*LLMGenerationData, error) {
	return performLLMInference(ctx, l, chatCtx, tools, true, options...)
}

func performLLMInference(
	ctx context.Context,
	l llm.LLM,
	chatCtx *llm.ChatContext,
	tools []llm.Tool,
	emitTextEvents bool,
	options ...llm.ChatOption,
) (*LLMGenerationData, error) {
	data := &LLMGenerationData{
		TextCh:         make(chan string, 100),
		FunctionCh:     make(chan *llm.FunctionToolCall, 10),
		Done:           make(chan struct{}),
		GeneratedExtra: make(map[string]any),
		ID:             cavosmath.ShortUUID("item_"),
	}
	if emitTextEvents {
		data.TextEventCh = make(chan LLMTextEvent, 100)
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
		if data.TextEventCh != nil {
			defer close(data.TextEventCh)
		}
		defer close(data.FunctionCh)
		defer close(data.Done)
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
					if data.TextEventCh != nil {
						data.TextEventCh <- LLMTextEvent{Text: chunk.Delta.Content}
					}
				}
				if chunk.Delta.Flush && data.TextEventCh != nil {
					data.TextEventCh <- LLMTextEvent{Flush: true}
				}
				for _, fc := range chunk.Delta.ToolCalls {
					if fc.Type != "function" {
						continue
					}
					f := fc
					f.ID = fmt.Sprintf("%s/fnc_%d", data.ID, len(data.GeneratedFunctions))
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
	AudioCh        chan *model.AudioFrame
	TimedTextCh    chan tts.TimedString
	TTFB           time.Duration
	StreamErr      error
	ForwardedAudio bool
}

type TTSInferenceOptions struct {
	StreamPacer             *tts.SentenceStreamPacerOptions
	TextReplacements        map[string]string
	OrderedTextReplacements []tts.TextReplacement
	TextTransforms          []string
	TextTransformsSet       bool
	DisableTextTransforms   bool
	PreserveTimedTranscript bool
	RequireAudio            bool
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

func WithOrderedTTSTextReplacements(replacements []tts.TextReplacement) TTSInferenceOption {
	return func(options *TTSInferenceOptions) {
		options.OrderedTextReplacements = append([]tts.TextReplacement(nil), replacements...)
	}
}

func WithTTSTextTransformsDisabled() TTSInferenceOption {
	return func(options *TTSInferenceOptions) {
		options.DisableTextTransforms = true
	}
}

func WithTTSTextTransforms(transforms []string) TTSInferenceOption {
	return func(options *TTSInferenceOptions) {
		options.TextTransforms = append([]string(nil), transforms...)
		options.TextTransformsSet = true
	}
}

func WithTTSPreserveTimedTranscript() TTSInferenceOption {
	return func(options *TTSInferenceOptions) {
		options.PreserveTimedTranscript = true
	}
}

func WithTTSRequireAudio() TTSInferenceOption {
	return func(options *TTSInferenceOptions) {
		options.RequireAudio = true
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
	transformBuffer, err := newTTSTextTransformBuffer(options)
	if err != nil {
		return nil, err
	}
	ctx, span := telemetry.NewTTSNodeSpan(ctx, tts.Model(t), tts.Provider(t))

	if !t.Capabilities().Streaming && !options.PreserveTimedTranscript {
		t = tts.NewStreamAdapter(t)
	}

	if !t.Capabilities().Streaming {
		go func() {
			defer close(data.AudioCh)
			defer close(data.TimedTextCh)
			defer span.End()

			var text strings.Builder
			var startTime time.Time
			var startTimeSet bool
			for chunk := range textCh {
				if !startTimeSet {
					startTime = time.Now()
					startTimeSet = true
				}
				text.WriteString(chunk)
			}
			ttsText := text.String()
			if !options.DisableTextTransforms {
				var transformErr error
				ttsText, transformErr = applyTTSTextTransforms(ttsText, options)
				if transformErr != nil {
					data.StreamErr = transformErr
					return
				}
			}
			transformedText := applyTTSTextReplacements(ttsText, options)
			if strings.TrimSpace(transformedText) == "" {
				return
			}

			if !startTimeSet {
				startTime = time.Now()
			}
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
					} else {
						data.StreamErr = fmt.Errorf("no audio frames were pushed for text: %s", transformedText)
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
					} else {
						data.StreamErr = fmt.Errorf("no audio frames were pushed for text: %s", transformedText)
					}
					return
				}
			}
			data.TTFB = time.Since(startTime)
			span.SetAttributes(attribute.Float64(telemetry.AttrResponseTTFB, data.TTFB.Seconds()))
			data.AudioCh <- frame
		}()
		return data, nil
	}

	streamCtx, cancelStream := context.WithCancel(ctx)
	stream, err := t.Stream(streamCtx)
	if err != nil {
		span.End()
		cancelStream()
		return nil, err
	}
	if options.StreamPacer != nil {
		stream = tts.NewSentenceStreamPacerWithOptions(streamCtx, stream, *options.StreamPacer)
	}

	go func() {
		defer close(data.AudioCh)
		defer close(data.TimedTextCh)
		defer stream.Close()
		defer cancelStream()
		defer span.End()

		streamOpenedAt := time.Now()
		startTime := streamOpenedAt
		var startTimeSet bool
		var ttfbObserved bool
		var startTimeMu sync.Mutex
		markStartTime := func() {
			startTimeMu.Lock()
			if !startTimeSet && !ttfbObserved {
				startTime = time.Now()
				startTimeSet = true
			}
			startTimeMu.Unlock()
		}
		timeSinceStart := func() (time.Duration, bool) {
			startTimeMu.Lock()
			defer startTimeMu.Unlock()
			if !startTimeSet {
				startTime = streamOpenedAt
			}
			ttfbObserved = true
			return time.Since(startTime), true
		}
		replaceBuffer := newTTSReplacementBuffer(options)
		var streamErrMu sync.Mutex
		setStreamErr := func(err error) {
			if err == nil {
				return
			}
			streamErrMu.Lock()
			if data.StreamErr == nil {
				data.StreamErr = err
			}
			streamErrMu.Unlock()
			cancelStream()
			_ = stream.Close()
		}
		var pushedTextMu sync.Mutex
		var pushedText strings.Builder
		recordPushedText := func(text string) {
			if text == "" {
				return
			}
			pushedTextMu.Lock()
			pushedText.WriteString(text)
			pushedTextMu.Unlock()
		}
		pushedTextString := func() string {
			pushedTextMu.Lock()
			defer pushedTextMu.Unlock()
			return pushedText.String()
		}
		var audioFramesMu sync.Mutex
		audioFrames := 0
		recordAudioFrame := func() {
			audioFramesMu.Lock()
			audioFrames++
			audioFramesMu.Unlock()
		}
		audioFrameCount := func() int {
			audioFramesMu.Lock()
			defer audioFramesMu.Unlock()
			return audioFrames
		}
		pushChunks := func(chunks []string) bool {
			for _, chunk := range chunks {
				if chunk == "" {
					continue
				}
				if err := stream.PushText(chunk); err != nil {
					setStreamErr(err)
					return false
				}
				recordPushedText(chunk)
			}
			return true
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if options.DisableTextTransforms {
				for text := range textCh {
					markStartTime()
					if !pushChunks(replaceBuffer.Push(text)) {
						return
					}
				}
			} else {
				for text := range textCh {
					markStartTime()
					for _, filteredText := range transformBuffer.Push(text) {
						if !pushChunks(replaceBuffer.Push(filteredText)) {
							return
						}
					}
				}
				for _, filteredText := range transformBuffer.Flush() {
					if !pushChunks(replaceBuffer.Push(filteredText)) {
						return
					}
				}
			}
			if !pushChunks(replaceBuffer.Flush()) {
				return
			}
			if err := tts.EndSynthesizeStreamInput(stream); err != nil {
				setStreamErr(err)
				return
			}
		}()
		go func() {
			defer wg.Done()
			for {
				audio, err := stream.Next()
				if err != nil {
					if err != io.EOF {
						setStreamErr(err)
					}
					return
				}
				if data.TTFB == 0 {
					if ttfb, ok := timeSinceStart(); ok {
						data.TTFB = ttfb
						span.SetAttributes(attribute.Float64(telemetry.AttrResponseTTFB, data.TTFB.Seconds()))
					}
				}
				if audio.Frame != nil {
					recordAudioFrame()
				}
				for _, timedText := range audio.TimedTranscript {
					select {
					case data.TimedTextCh <- timedText:
					case <-streamCtx.Done():
						return
					}
				}
				select {
				case data.AudioCh <- audio.Frame:
				case <-streamCtx.Done():
					return
				}
			}
		}()
		wg.Wait()
		streamErrMu.Lock()
		hasStreamErr := data.StreamErr != nil
		streamErrMu.Unlock()
		if options.RequireAudio && !hasStreamErr && strings.TrimSpace(pushedTextString()) != "" && audioFrameCount() == 0 {
			setStreamErr(fmt.Errorf("no audio frames were pushed for text: %s", pushedTextString()))
		}
	}()

	return data, nil
}

func applyTTSTextReplacements(text string, options TTSInferenceOptions) string {
	buffer := newTTSReplacementBuffer(options)
	chunks := append(buffer.Push(text), buffer.Flush()...)
	return strings.Join(chunks, "")
}

func applyTTSTextTransforms(text string, options TTSInferenceOptions) (string, error) {
	if options.TextTransformsSet {
		return tts.ApplyTextTransformsWithTransforms(text, options.TextTransforms)
	}
	return tts.ApplyTextTransforms(text), nil
}

func newTTSTextTransformBuffer(options TTSInferenceOptions) (*tts.TextTransformBuffer, error) {
	if options.DisableTextTransforms {
		return nil, nil
	}
	if options.TextTransformsSet {
		return tts.NewTextTransformBufferWithTransforms(options.TextTransforms)
	}
	return tts.NewTextTransformBuffer(), nil
}

func newTTSReplacementBuffer(options TTSInferenceOptions) *tts.TextRegexReplaceBuffer {
	if len(options.OrderedTextReplacements) > 0 {
		return tts.NewOrderedTextRegexReplaceBuffer(options.OrderedTextReplacements, false)
	}
	return tts.NewTextRegexReplaceBuffer(options.TextReplacements, false)
}

type ToolExecutionOutput struct {
	FncCall    llm.FunctionCall
	FncCallOut *llm.FunctionCallOutput
	RawOutput  any
	RawError   error
}

type activeToolCall struct {
	call         llm.FunctionCall
	cancel       context.CancelFunc
	cancellable  bool
	speechHandle *SpeechHandle
	done         <-chan struct{}
}

type activeToolRegistry struct {
	mu        sync.Mutex
	callsByID map[string]activeToolCall
}

func (r *activeToolRegistry) set(callID string, call activeToolCall) {
	if r == nil || callID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.callsByID == nil {
		r.callsByID = make(map[string]activeToolCall)
	}
	r.callsByID[callID] = call
}

func (r *activeToolRegistry) get(callID string) (activeToolCall, bool) {
	if r == nil || callID == "" {
		return activeToolCall{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	call, ok := r.callsByID[callID]
	return call, ok
}

func (r *activeToolRegistry) delete(callID string) {
	if r == nil || callID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.callsByID, callID)
}

func (r *activeToolRegistry) snapshot() map[string]activeToolCall {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := make(map[string]activeToolCall, len(r.callsByID))
	for callID, call := range r.callsByID {
		calls[callID] = call
	}
	return calls
}

func (r *activeToolRegistry) drain(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	calls := r.snapshot()
	for _, call := range calls {
		if call.cancellable && call.cancel != nil {
			call.cancel()
		}
	}
	for callID, call := range calls {
		if call.done == nil {
			if call.cancellable {
				r.delete(callID)
			}
			continue
		}
		select {
		case <-call.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
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
		var activeMu sync.Mutex
		activeCallIDs := make(map[string]struct{})
		activeFunctionCalls := make(map[string]map[string]activeToolCall)
		activeCallsByID := make(map[string]activeToolCall)

		for fncCall := range functionCh {
			if options.ToolChoice == "none" {
				continue
			}
			var tool llm.Tool
			if toolCtx != nil {
				tool = toolCtx.GetFunctionTool(fncCall.Name)
			}
			duplicateMode := llm.ToolDuplicateModeFor(tool)
			executionArgs := fncCall.Arguments
			confirmDuplicate := false
			if duplicateMode == llm.ToolDuplicateModeConfirm {
				executionArgs, confirmDuplicate = stripConfirmDuplicateArgument(fncCall.Arguments)
				fncCall = copyFunctionToolCallWithArguments(fncCall, executionArgs)
			}
			if tool != nil {
				canonicalArgs, parseErr := canonicalFunctionCallArguments(executionArgs)
				if parseErr != nil {
					toolErr := llm.NewToolError(fmt.Sprintf("Error parsing arguments for `%s`: %s", fncCall.Name, parseErr.Error()))
					result := llm.MakeToolOutput(makeExecutionFunctionCall(fncCall, executionArgs), nil, toolErr)
					outCh <- ToolExecutionOutput{
						FncCall:    result.FncCall,
						FncCallOut: result.FncCallOut,
						RawOutput:  result.RawOutput,
						RawError:   result.RawError,
					}
					continue
				}
				executionArgs = canonicalArgs
				fncCall = copyFunctionToolCallWithArguments(fncCall, executionArgs)
			}
			functionCall := makeExecutionFunctionCall(fncCall, executionArgs)
			callCtx, callCancel := context.WithCancel(ctx)
			if isToolExecutorSystemTool(fncCall.Name) {
				output, cancel := executeToolExecutorSystemTool(fncCall, functionCall, options.Session, &activeMu, activeCallIDs, activeFunctionCalls, activeCallsByID)
				if cancel != nil {
					cancel()
				}
				callCancel()
				outCh <- output
				continue
			}
			var duplicateNameCalls []llm.FunctionCall
			var replaceCancels []context.CancelFunc
			var duplicateCallID bool
			activeMu.Lock()
			if tracksDuplicateFunctionName(duplicateMode) && len(activeFunctionCalls[fncCall.Name]) > 0 {
				duplicateNameCalls = make([]llm.FunctionCall, 0, len(activeFunctionCalls[fncCall.Name]))
				for _, runningCall := range activeFunctionCalls[fncCall.Name] {
					duplicateNameCalls = append(duplicateNameCalls, runningCall.call)
				}
				sort.Slice(duplicateNameCalls, func(i, j int) bool {
					return duplicateNameCalls[i].CallID < duplicateNameCalls[j].CallID
				})
				if duplicateMode == llm.ToolDuplicateModeConfirm && confirmDuplicate {
					duplicateNameCalls = nil
				}
				if duplicateMode == llm.ToolDuplicateModeReplace {
					allCancellable := true
					for _, runningCall := range activeFunctionCalls[fncCall.Name] {
						if !runningCall.cancellable {
							allCancellable = false
							break
						}
					}
					if allCancellable {
						for callID, runningCall := range activeFunctionCalls[fncCall.Name] {
							if runningCall.cancel != nil {
								replaceCancels = append(replaceCancels, runningCall.cancel)
							}
							delete(activeCallIDs, callID)
							delete(activeCallsByID, callID)
							if options.Session != nil {
								options.Session.toolExecutionRegistry.delete(callID)
							}
						}
						delete(activeFunctionCalls, fncCall.Name)
						duplicateNameCalls = nil
					}
				}
			}
			if len(duplicateNameCalls) == 0 && fncCall.CallID != "" {
				_, duplicateCallID = activeCallIDs[fncCall.CallID]
				if !duplicateCallID {
					activeCallIDs[fncCall.CallID] = struct{}{}
				}
			}
			var activeDone chan struct{}
			if len(duplicateNameCalls) == 0 && !duplicateCallID {
				activeDone = make(chan struct{})
				activeCall := activeToolCall{
					call:         functionCall,
					cancel:       callCancel,
					cancellable:  llm.ToolHasFlag(tool, llm.ToolFlagCancellable),
					speechHandle: options.SpeechHandle,
					done:         activeDone,
				}
				if fncCall.CallID != "" {
					activeCallsByID[fncCall.CallID] = activeCall
					if options.Session != nil {
						options.Session.toolExecutionRegistry.set(fncCall.CallID, activeCall)
					}
				}
				if tracksDuplicateFunctionName(duplicateMode) {
					if activeFunctionCalls[fncCall.Name] == nil {
						activeFunctionCalls[fncCall.Name] = make(map[string]activeToolCall)
					}
					activeFunctionCalls[fncCall.Name][fncCall.CallID] = activeCall
				}
			}
			activeMu.Unlock()
			for _, cancel := range replaceCancels {
				cancel()
			}
			if len(duplicateNameCalls) > 0 {
				callCancel()
				err := llm.NewToolError(duplicateToolMessage(fncCall.Name, duplicateNameCalls, duplicateMode))
				result := llm.MakeToolOutput(functionCall, nil, err)
				outCh <- ToolExecutionOutput{
					FncCall:    result.FncCall,
					FncCallOut: result.FncCallOut,
					RawOutput:  result.RawOutput,
					RawError:   result.RawError,
				}
				continue
			}
			if duplicateCallID {
				callCancel()
				err := llm.NewToolError(fmt.Sprintf("Task already running for call_id: %s", fncCall.CallID))
				result := llm.MakeToolOutput(functionCall, nil, err)
				outCh <- ToolExecutionOutput{
					FncCall:    result.FncCall,
					FncCallOut: result.FncCallOut,
					RawOutput:  result.RawOutput,
					RawError:   result.RawError,
				}
				continue
			}
			trackFunctionName := tracksDuplicateFunctionName(duplicateMode)
			wg.Add(1)
			go func(fc *llm.FunctionToolCall, trackFunctionName bool, callCtx context.Context, callCancel context.CancelFunc, session *AgentSession, activeDone chan struct{}) {
				defer wg.Done()
				defer callCancel()
				if activeDone != nil {
					defer close(activeDone)
				}
				if fc.CallID != "" || trackFunctionName {
					defer func() {
						activeMu.Lock()
						delete(activeCallIDs, fc.CallID)
						delete(activeCallsByID, fc.CallID)
						if session != nil {
							session.toolExecutionRegistry.delete(fc.CallID)
						}
						if calls := activeFunctionCalls[fc.Name]; calls != nil {
							delete(calls, fc.CallID)
							if len(calls) == 0 {
								delete(activeFunctionCalls, fc.Name)
							}
						}
						activeMu.Unlock()
					}()
				}
				execCtx := callCtx
				var runCtx *RunContext
				if options.Session != nil {
					args := fc.Arguments
					if args == "" {
						args = "{}"
					}
					if parsedArgs, err := llm.ParseFunctionArguments(args); err == nil {
						if encodedArgs, err := json.Marshal(parsedArgs); err == nil {
							args = string(encodedArgs)
						}
					}
					functionCall := llm.FunctionCall{
						ID:        fc.ID,
						CallID:    fc.CallID,
						Name:      fc.Name,
						Arguments: args,
						Extra:     fc.Extra,
						CreatedAt: time.Now(),
					}
					runCtx = NewRunContext(options.Session, options.SpeechHandle, &functionCall)
					runCtx.attach()
					execCtx = WithRunContext(execCtx, runCtx)
				}
				executionToolCtx := mockToolContext(execCtx, toolCtx, options.Session, fc.Name)
				var result llm.FunctionCallResult
				if executionToolCtx == nil || executionToolCtx.GetFunctionTool(fc.Name) == nil {
					fncCall := makeExecutionFunctionCall(fc, fc.Arguments)
					result = llm.MakeToolOutput(fncCall, nil, llm.NewToolError(fmt.Sprintf("Unknown function: %s", fc.Name)))
				} else {
					result = llm.ExecuteFunctionCall(execCtx, fc, executionToolCtx)
				}
				if runCtx != nil {
					updates := runCtx.Updates()
					if len(updates) > 0 {
						for _, update := range updates {
							outCh <- toolExecutionOutputFromUpdate(update)
						}
						if finalOutput, ok := makeRunContextFinalToolOutput(runCtx, result); ok {
							outCh <- finalOutput
						}
						runCtx.detach()
						return
					}
					runCtx.detach()
				}
				outCh <- ToolExecutionOutput{
					FncCall:    result.FncCall,
					FncCallOut: result.FncCallOut,
					RawOutput:  result.RawOutput,
					RawError:   result.RawError,
				}
			}(fncCall, trackFunctionName, callCtx, callCancel, options.Session, activeDone)
		}

		wg.Wait()
	}()

	return outCh
}

func toolExecutionOutputFromUpdate(update RunContextUpdate) ToolExecutionOutput {
	output := ToolExecutionOutput{}
	if update.FunctionCall != nil {
		output.FncCall = *update.FunctionCall
	}
	output.FncCallOut = update.FunctionCallOutput
	if update.FunctionCallOutput != nil {
		output.RawOutput = update.FunctionCallOutput.Output
	}
	return output
}

func makeRunContextFinalToolOutput(runCtx *RunContext, result llm.FunctionCallResult) (ToolExecutionOutput, bool) {
	if runCtx == nil || runCtx.FunctionCall == nil || result.FncCallOut == nil {
		return ToolExecutionOutput{}, false
	}
	call := copyRunContextUpdateCall(runCtx.FunctionCall, "_final")
	finalResult := llm.MakeToolOutput(*call, result.RawOutput, result.RawError)
	if finalResult.FncCallOut == nil {
		return ToolExecutionOutput{}, false
	}
	return ToolExecutionOutput{
		FncCall:    finalResult.FncCall,
		FncCallOut: finalResult.FncCallOut,
		RawOutput:  finalResult.RawOutput,
		RawError:   finalResult.RawError,
	}, true
}

func copyFunctionToolCallWithArguments(fc *llm.FunctionToolCall, arguments string) *llm.FunctionToolCall {
	copied := *fc
	copied.Arguments = arguments
	return &copied
}

func makeExecutionFunctionCall(fc *llm.FunctionToolCall, arguments string) llm.FunctionCall {
	return llm.FunctionCall{
		ID:        fc.ID,
		CallID:    fc.CallID,
		Name:      fc.Name,
		Arguments: arguments,
		Extra:     fc.Extra,
		CreatedAt: time.Now(),
	}
}

func canonicalFunctionCallArguments(arguments string) (string, error) {
	args := arguments
	if args == "" {
		args = "{}"
	}
	parsed, err := llm.ParseFunctionArguments(args)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return "", err
	}
	if arguments == "" {
		return arguments, nil
	}
	return string(encoded), nil
}

func stripConfirmDuplicateArgument(arguments string) (string, bool) {
	args := arguments
	if args == "" {
		args = "{}"
	}
	parsed, err := llm.ParseFunctionArguments(args)
	if err != nil {
		return arguments, false
	}
	confirmDuplicate, _ := parsed[llm.ConfirmDuplicateParam].(bool)
	delete(parsed, llm.ConfirmDuplicateParam)
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return arguments, confirmDuplicate
	}
	return string(encoded), confirmDuplicate
}

func tracksDuplicateFunctionName(mode llm.ToolDuplicateMode) bool {
	return mode == llm.ToolDuplicateModeReject || mode == llm.ToolDuplicateModeConfirm || mode == llm.ToolDuplicateModeReplace
}

func duplicateToolMessage(functionName string, calls []llm.FunctionCall, mode llm.ToolDuplicateMode) string {
	lines := make([]string, 0, len(calls))
	for _, call := range calls {
		encoded, err := json.Marshal(call)
		if err != nil {
			continue
		}
		lines = append(lines, string(encoded))
	}
	if mode == llm.ToolDuplicateModeConfirm {
		return fmt.Sprintf("Same tool `%s` is already running:\n%s\nRe-call with confirm duplicate True to run a duplicate if needed,\nor if you want to cancel the existing one, call `lk_agents_cancel_task` with call_id.", functionName, strings.Join(lines, "\n"))
	}
	if mode == llm.ToolDuplicateModeReplace {
		return fmt.Sprintf("cannot replace duplicate call of `%s`: running call is not cancellable (allow_cancellation=False)", functionName)
	}
	return fmt.Sprintf("Same tool `%s` is already running:\n%s\nIf you want to cancel the existing one, call `lk_agents_cancel_task` with call_id.", functionName, strings.Join(lines, "\n"))
}

func isToolExecutorSystemTool(name string) bool {
	return name == getRunningTasksToolName || name == cancelTaskToolName
}

func executeToolExecutorSystemTool(
	fc *llm.FunctionToolCall,
	functionCall llm.FunctionCall,
	session *AgentSession,
	activeMu *sync.Mutex,
	activeCallIDs map[string]struct{},
	activeFunctionCalls map[string]map[string]activeToolCall,
	activeCallsByID map[string]activeToolCall,
) (ToolExecutionOutput, context.CancelFunc) {
	var output any
	var err error
	var cancel context.CancelFunc

	switch fc.Name {
	case getRunningTasksToolName:
		output = runningTasksOutput(activeToolCallsForHelpers(session, activeMu, activeCallsByID))
	case cancelTaskToolName:
		output, cancel, err = cancelRunningTask(fc.Arguments, session, activeMu, activeCallIDs, activeFunctionCalls, activeCallsByID)
	default:
		err = llm.NewToolError(fmt.Sprintf("Unknown function: %s", fc.Name))
	}

	result := llm.MakeToolOutput(functionCall, output, err)
	return ToolExecutionOutput{
		FncCall:    result.FncCall,
		FncCallOut: result.FncCallOut,
		RawOutput:  result.RawOutput,
		RawError:   result.RawError,
	}, cancel
}

func activeToolCallsForHelpers(session *AgentSession, activeMu *sync.Mutex, activeCallsByID map[string]activeToolCall) map[string]activeToolCall {
	if session != nil {
		return session.toolExecutionRegistry.snapshot()
	}
	activeMu.Lock()
	defer activeMu.Unlock()
	calls := make(map[string]activeToolCall, len(activeCallsByID))
	for callID, active := range activeCallsByID {
		calls[callID] = active
	}
	return calls
}

func runningTasksOutput(activeCallsByID map[string]activeToolCall) []map[string]any {
	calls := make([]llm.FunctionCall, 0, len(activeCallsByID))
	for _, active := range activeCallsByID {
		if active.cancellable {
			calls = append(calls, active.call)
		}
	}

	sort.Slice(calls, func(i, j int) bool {
		return calls[i].CallID < calls[j].CallID
	})
	items := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		item := map[string]any{
			"id":        call.ID,
			"call_id":   call.CallID,
			"name":      call.Name,
			"arguments": call.Arguments,
		}
		if len(call.Extra) > 0 {
			item["extra"] = call.Extra
		} else {
			item["extra"] = map[string]any{}
		}
		item["type"] = "function_call"
		item["created_at"] = float64(call.CreatedAt.UnixNano()) / float64(time.Second)
		if call.GroupID != nil {
			item["group_id"] = *call.GroupID
		} else {
			item["group_id"] = nil
		}
		items = append(items, item)
	}
	return items
}

func cancelRunningTask(
	arguments string,
	session *AgentSession,
	activeMu *sync.Mutex,
	activeCallIDs map[string]struct{},
	activeFunctionCalls map[string]map[string]activeToolCall,
	activeCallsByID map[string]activeToolCall,
) (string, context.CancelFunc, error) {
	args := arguments
	if args == "" {
		args = "{}"
	}
	parsed, err := llm.ParseFunctionArguments(args)
	if err != nil {
		return "", nil, llm.NewToolError(fmt.Sprintf("Error parsing arguments for `%s`: %s", cancelTaskToolName, err.Error()))
	}
	callID, _ := parsed["call_id"].(string)
	if callID == "" {
		return "", nil, llm.NewToolError("Task  not found")
	}

	var active activeToolCall
	var ok bool
	if session != nil {
		active, ok = session.toolExecutionRegistry.get(callID)
	}
	if !ok {
		activeMu.Lock()
		active, ok = activeCallsByID[callID]
		activeMu.Unlock()
	}
	if !ok {
		return "", nil, llm.NewToolError(fmt.Sprintf("Task %s not found", callID))
	}
	if !active.cancellable {
		return "", nil, llm.NewToolError(fmt.Sprintf("Tool call %s is not cancellable", callID))
	}
	if !active.speechHandle.AllowsInterruptions() {
		return "", nil, llm.NewToolError(fmt.Sprintf("Tool call %s is not cancellable because interruptions are disallowed", callID))
	}
	activeMu.Lock()
	delete(activeCallIDs, callID)
	delete(activeCallsByID, callID)
	if calls := activeFunctionCalls[active.call.Name]; calls != nil {
		delete(calls, callID)
		if len(calls) == 0 {
			delete(activeFunctionCalls, active.call.Name)
		}
	}
	activeMu.Unlock()
	if session != nil {
		session.toolExecutionRegistry.delete(callID)
	}
	return fmt.Sprintf("Task %s cancelled successfully.", callID), active.cancel, nil
}
