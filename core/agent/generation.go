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
	FunctionCh         chan *llm.FunctionToolCall
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
		ID:             cavosmath.ShortUUID("item_"),
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
	AudioCh     chan *model.AudioFrame
	TimedTextCh chan tts.TimedString
	TTFB        time.Duration
	StreamErr   error
}

type TTSInferenceOptions struct {
	StreamPacer             *tts.SentenceStreamPacerOptions
	TextReplacements        map[string]string
	OrderedTextReplacements []tts.TextReplacement
	TextTransforms          []string
	TextTransformsSet       bool
	DisableTextTransforms   bool
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

	if !t.Capabilities().Streaming {
		go func() {
			defer close(data.AudioCh)
			defer close(data.TimedTextCh)
			defer span.End()

			var text strings.Builder
			for chunk := range textCh {
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

	stream, err := t.Stream(ctx)
	if err != nil {
		span.End()
		return nil, err
	}
	if options.StreamPacer != nil {
		stream = tts.NewSentenceStreamPacerWithOptions(ctx, stream, *options.StreamPacer)
	}

	go func() {
		defer close(data.AudioCh)
		defer close(data.TimedTextCh)
		defer stream.Close()
		defer span.End()

		startTime := time.Now()
		replaceBuffer := newTTSReplacementBuffer(options)

		if options.DisableTextTransforms {
			for text := range textCh {
				if !pushTTSReplacementChunks(stream, replaceBuffer.Push(text), data) {
					return
				}
			}
		} else {
			for text := range textCh {
				for _, filteredText := range transformBuffer.Push(text) {
					if !pushTTSReplacementChunks(stream, replaceBuffer.Push(filteredText), data) {
						return
					}
				}
			}
			for _, filteredText := range transformBuffer.Flush() {
				if !pushTTSReplacementChunks(stream, replaceBuffer.Push(filteredText), data) {
					return
				}
			}
		}
		if !pushTTSReplacementChunks(stream, replaceBuffer.Flush(), data) {
			return
		}
		if err := tts.EndSynthesizeStreamInput(stream); err != nil {
			data.StreamErr = err
			return
		}

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
				span.SetAttributes(attribute.Float64(telemetry.AttrResponseTTFB, data.TTFB.Seconds()))
			}
			for _, timedText := range audio.TimedTranscript {
				data.TimedTextCh <- timedText
			}
			data.AudioCh <- audio.Frame
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

func pushTTSReplacementChunks(stream tts.SynthesizeStream, chunks []string, data *TTSGenerationData) bool {
	for _, chunk := range chunks {
		if chunk == "" {
			continue
		}
		if err := stream.PushText(chunk); err != nil {
			data.StreamErr = err
			return false
		}
	}
	return true
}

type ToolExecutionOutput struct {
	FncCall    llm.FunctionCall
	FncCallOut *llm.FunctionCallOutput
	RawOutput  any
	RawError   error
}

type activeToolCall struct {
	call               llm.FunctionCall
	cancel             context.CancelFunc
	cancellable        bool
	allowInterruptions bool
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
			functionCall := makeExecutionFunctionCall(fncCall, executionArgs)
			callCtx, callCancel := context.WithCancel(ctx)
			if isToolExecutorSystemTool(fncCall.Name) {
				output, cancel := executeToolExecutorSystemTool(fncCall, functionCall, &activeMu, activeCallIDs, activeFunctionCalls, activeCallsByID)
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
			if len(duplicateNameCalls) == 0 && !duplicateCallID {
				allowInterruptions := options.SpeechHandle == nil || options.SpeechHandle.AllowInterruptions
				activeCall := activeToolCall{
					call:               functionCall,
					cancel:             callCancel,
					cancellable:        llm.ToolHasFlag(tool, llm.ToolFlagCancellable),
					allowInterruptions: allowInterruptions,
				}
				if fncCall.CallID != "" {
					activeCallsByID[fncCall.CallID] = activeCall
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
			go func(fc *llm.FunctionToolCall, trackFunctionName bool, callCtx context.Context, callCancel context.CancelFunc) {
				defer wg.Done()
				defer callCancel()
				if fc.CallID != "" || trackFunctionName {
					defer func() {
						activeMu.Lock()
						delete(activeCallIDs, fc.CallID)
						delete(activeCallsByID, fc.CallID)
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
				result := llm.FunctionCallResult{}
				if executionToolCtx == nil || executionToolCtx.GetFunctionTool(fc.Name) == nil {
					fncCall := makeExecutionFunctionCall(fc, fc.Arguments)
					result = llm.MakeToolOutput(fncCall, nil, llm.NewToolError(fmt.Sprintf("Unknown function: %s", fc.Name)))
				} else {
					result = llm.ExecuteFunctionCall(execCtx, fc, executionToolCtx)
				}
				if runCtx != nil {
					updates := runCtx.Updates()
					runCtx.detach()
					if len(updates) > 0 {
						result.FncCall = *updates[0].FunctionCall
						result.FncCallOut = updates[0].FunctionCallOutput
						if result.FncCallOut != nil {
							result.RawOutput = result.FncCallOut.Output
						}
					}
				}
				outCh <- ToolExecutionOutput{
					FncCall:    result.FncCall,
					FncCallOut: result.FncCallOut,
					RawOutput:  result.RawOutput,
					RawError:   result.RawError,
				}
			}(fncCall, trackFunctionName, callCtx, callCancel)
		}

		wg.Wait()
	}()

	return outCh
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
		output, err = runningTasksJSON(activeMu, activeCallsByID)
	case cancelTaskToolName:
		output, cancel, err = cancelRunningTask(fc.Arguments, activeMu, activeCallIDs, activeFunctionCalls, activeCallsByID)
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

func runningTasksJSON(activeMu *sync.Mutex, activeCallsByID map[string]activeToolCall) (string, error) {
	activeMu.Lock()
	calls := make([]llm.FunctionCall, 0, len(activeCallsByID))
	for _, active := range activeCallsByID {
		if active.cancellable {
			calls = append(calls, active.call)
		}
	}
	activeMu.Unlock()

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
		}
		items = append(items, item)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func cancelRunningTask(
	arguments string,
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

	activeMu.Lock()
	active, ok := activeCallsByID[callID]
	activeMu.Unlock()
	if !ok {
		return "", nil, llm.NewToolError(fmt.Sprintf("Task %s not found", callID))
	}
	if !active.cancellable {
		return "", nil, llm.NewToolError(fmt.Sprintf("Tool call %s is not cancellable", callID))
	}
	if !active.allowInterruptions {
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
	return fmt.Sprintf("Task %s cancelled successfully.", callID), active.cancel, nil
}
