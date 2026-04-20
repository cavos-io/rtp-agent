package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
<<<<<<< HEAD
	"sync"
=======
>>>>>>> origin/main
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
<<<<<<< HEAD
	"github.com/cavos-io/rtp-agent/library/logger"
=======
>>>>>>> origin/main
	"github.com/cavos-io/rtp-agent/model"
)

type LLMGenerationData struct {
	TextCh     chan string
	FunctionCh chan *llm.FunctionToolCall
	FullTextCh chan string // receives the complete assembled text when streaming is done
	Usage      *llm.CompletionUsage
}

<<<<<<< HEAD
// prepareFunctionArguments mimics LiveKit Python's strict argument binding
func prepareFunctionArguments(tool llm.Tool, argsJSON string) (any, error) {
	var argsMap map[string]any
	if argsJSON == "" {
		argsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(argsJSON), &argsMap); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	params := tool.Parameters()
	if params != nil {
		if reqs, ok := params["required"].([]any); ok {
			for _, r := range reqs {
				if reqStr, ok := r.(string); ok {
					if _, exists := argsMap[reqStr]; !exists {
						return nil, fmt.Errorf("missing required argument: %s", reqStr)
					}
				}
			}
		} else if reqsStr, ok := params["required"].([]string); ok {
			for _, reqStr := range reqsStr {
				if _, exists := argsMap[reqStr]; !exists {
					return nil, fmt.Errorf("missing required argument: %s", reqStr)
				}
			}
		}

		if props, ok := params["properties"].(map[string]any); ok {
			for k, v := range props {
				if propDef, ok := v.(map[string]any); ok {
					if defVal, hasDef := propDef["default"]; hasDef {
						if val, exists := argsMap[k]; !exists || val == nil {
							argsMap[k] = defVal
						}
					}
				}
			}
		}
	}

	if ta, ok := tool.(llm.ToolWithArgs); ok {
		typedArgs := ta.Args()
		if typedArgs != nil {
			patchedBytes, err := json.Marshal(argsMap)
			if err != nil {
				return nil, fmt.Errorf("failed to re-marshal patched args: %w", err)
			}
			if err := json.Unmarshal(patchedBytes, typedArgs); err != nil {
				return nil, fmt.Errorf("failed to bind tool arguments to %T: %w", typedArgs, err)
			}
			return typedArgs, nil
		}
	}

	return argsMap, nil
}

func PerformLLMInference(ctx context.Context, l llm.LLM, chatCtx *llm.ChatContext, tools []interface{}) (*LLMGenerationData, error) {
	logger.Logger.Debugw("LLM inference starting", "tools_count", len(tools))

	data := &LLMGenerationData{
		TextCh:     make(chan string, 100),
		FunctionCh: make(chan *llm.FunctionToolCall, 10),
	}

	stream, err := l.Chat(ctx, chatCtx, llm.WithTools(llm.FlattenTools(tools)))
	if err != nil {
		logger.Logger.Errorw("LLM chat stream creation failed", err)
		return nil, err
=======
func PerformLLMInference(ctx context.Context, l llm.LLM, chatCtx *llm.ChatContext, tools []llm.Tool) (*LLMGenerationData, error) {
	opts := []llm.ChatOption{}
	if len(tools) > 0 {
		opts = append(opts, llm.WithTools(tools))
	}

	stream, err := l.Chat(ctx, chatCtx, opts...)
	if err != nil {
		return nil, err
	}

	data := &LLMGenerationData{
		TextCh:     make(chan string, 100),
		FunctionCh: make(chan *llm.FunctionToolCall, 10),
		FullTextCh: make(chan string, 1),
>>>>>>> origin/main
	}
	logger.Logger.Debugw("LLM chat stream created, starting goroutine")

	go func() {
		defer close(data.TextCh)
		defer close(data.FunctionCh)
		defer close(data.FullTextCh) // must close so drain goroutines can exit
		defer stream.Close()
		defer logger.Logger.Debugw("LLM inference goroutine exited")

<<<<<<< HEAD
		startTime := time.Now()
		toolCalls := make([]*llm.FunctionToolCall, 0)
		toolCallsByID := make(map[string]*llm.FunctionToolCall)
		toolCallsByIndex := make(map[int]*llm.FunctionToolCall)
		emittedByIndex := make(map[int]bool)

		// Settle delay logic
		const settleDelay = 50 * time.Millisecond
		settleTimers := make(map[int]*time.Timer)
		var timersMu sync.Mutex

		finalizeAndEmit := func(idx int) {
			timersMu.Lock()
			if timer, ok := settleTimers[idx]; ok {
				timer.Stop()
				delete(settleTimers, idx)
			}
			if emittedByIndex[idx] {
				timersMu.Unlock()
				return
			}
			emittedByIndex[idx] = true
			timersMu.Unlock()

			fc := toolCallsByIndex[idx]
			finalized := finalizeToolCall(fc, idx)
			if finalized == nil {
				return
			}

			logger.Logger.Debugw("Emitting finalized tool call (pipelined)",
				"name", finalized.Name,
				"call_id", finalized.CallID,
				"index", idx,
			)
			data.FunctionCh <- finalized

			// Capture event in RunResult
			if rc := GetRunContext(ctx); rc != nil && rc.SpeechHandle != nil && rc.SpeechHandle.RunResult != nil {
				rc.SpeechHandle.RunResult.AddEvent(&FunctionCallRunEvent{
					Item: &llm.FunctionCall{
						CallID:    finalized.CallID,
						Name:      finalized.Name,
						Arguments: finalized.Arguments,
						Extra:     finalized.Extra,
						CreatedAt: time.Now(),
					},
				})
			}
		}

		var chunkCount int
=======
		var sb strings.Builder
>>>>>>> origin/main
		for {
			chunk, err := stream.Next()
			if err != nil {
				break
			}
			chunkCount++

			if chunk.Delta != nil {
				if chunk.Delta.Content != "" {
					sb.WriteString(chunk.Delta.Content)
					data.TextCh <- chunk.Delta.Content
				}
<<<<<<< HEAD
				if len(chunk.Delta.ToolCalls) > 0 {
					for _, fc := range chunk.Delta.ToolCalls {
						mergeToolCallDelta(&toolCalls, toolCallsByID, toolCallsByIndex, fc)
						
						idx, hasIndex := extractToolCallIndex(fc.Extra)
						if hasIndex {
							timersMu.Lock()
							if timer, ok := settleTimers[idx]; ok {
								timer.Stop()
							}
							
							// Reset/Start settle timer for this index
							idxVal := idx
							settleTimers[idx] = time.AfterFunc(settleDelay, func() {
								finalizeAndEmit(idxVal)
							})
							timersMu.Unlock()
						}
					}
=======
				for _, tc := range chunk.Delta.ToolCalls {
					data.FunctionCh <- &tc
>>>>>>> origin/main
				}
			}
			if chunk.Usage != nil {
				data.Usage = chunk.Usage
			}
		}
<<<<<<< HEAD

		// Flush remaining timers and emit any pending tools
		timersMu.Lock()
		activeIndices := make([]int, 0, len(settleTimers))
		for idx := range settleTimers {
			activeIndices = append(activeIndices, idx)
		}
		timersMu.Unlock()

		for _, idx := range activeIndices {
			finalizeAndEmit(idx)
		}

		// Ensure all tools are emitted if any were missed by the stream logic/timers
		for idx := range toolCallsByIndex {
			if !emittedByIndex[idx] {
				finalizeAndEmit(idx)
			}
		}

		if data.GeneratedText != "" {
			if rc := GetRunContext(ctx); rc != nil && rc.SpeechHandle != nil && rc.SpeechHandle.RunResult != nil {
				rc.SpeechHandle.RunResult.AddEvent(&ChatMessageRunEvent{
					Item: &llm.ChatMessage{
						Role:      llm.ChatRoleAssistant,
						Content:   []llm.ChatContent{{Text: data.GeneratedText}},
						CreatedAt: time.Now(),
					},
				})
			}
		}
=======
		// Non-blocking: buffered channel holds the result for the consumer.
		data.FullTextCh <- sb.String()
>>>>>>> origin/main
	}()

	return data, nil
}

func mergeToolCallDelta(
	ordered *[]*llm.FunctionToolCall,
	byID map[string]*llm.FunctionToolCall,
	byIndex map[int]*llm.FunctionToolCall,
	delta llm.FunctionToolCall,
) {
	idx, hasIndex := extractToolCallIndex(delta.Extra)

	var call *llm.FunctionToolCall
	if delta.CallID != "" {
		call = byID[delta.CallID]
	}
	if call == nil && hasIndex {
		call = byIndex[idx]
	}
	if call == nil {
		call = &llm.FunctionToolCall{}
		*ordered = append(*ordered, call)
	}

	if delta.CallID != "" {
		byID[delta.CallID] = call
	}
	if hasIndex {
		byIndex[idx] = call
	}

	if call.Type == "" && delta.Type != "" {
		call.Type = delta.Type
	}
	if call.Name == "" && delta.Name != "" {
		call.Name = delta.Name
	}
	if delta.Arguments != "" {
		call.Arguments += delta.Arguments
	}
	if call.CallID == "" && delta.CallID != "" {
		call.CallID = delta.CallID
	}
	if call.Extra == nil {
		call.Extra = make(map[string]any)
	}
	for k, v := range delta.Extra {
		if _, exists := call.Extra[k]; !exists || call.Extra[k] == nil {
			call.Extra[k] = v
		}
	}
}

func finalizeToolCall(call *llm.FunctionToolCall, fallbackIndex int) *llm.FunctionToolCall {
	if call == nil {
		return nil
	}

	out := *call
	out.Name = strings.TrimSpace(out.Name)
	out.Arguments = strings.TrimSpace(out.Arguments)

	if out.Name == "" {
		return nil
	}
	if out.Arguments == "" {
		out.Arguments = "{}"
	}
	if out.CallID == "" {
		out.CallID = fmt.Sprintf("%s_%d", out.Name, fallbackIndex)
	}
	if out.Extra == nil {
		out.Extra = make(map[string]any)
	}
	if _, ok := out.Extra["index"]; !ok {
		out.Extra["index"] = fallbackIndex
	}

	return &out
}

func extractToolCallIndex(extra map[string]any) (int, bool) {
	if extra == nil {
		return 0, false
	}

	value, ok := extra["index"]
	if !ok || value == nil {
		return 0, false
	}

	switch v := value.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float32:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func validateToolArguments(args string) (string, error) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "{}", nil
	}

	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("invalid tool arguments: not valid JSON")
	}

	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", fmt.Errorf("invalid tool arguments: %w", err)
	}

	if _, ok := payload.(map[string]any); !ok {
		return "", fmt.Errorf("invalid tool arguments: expected JSON object")
	}

	return trimmed, nil
}

func buildToolCallID(name string) string {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = "tool"
	}
	return fmt.Sprintf("%s_%d", trimmedName, time.Now().UnixNano())
}

func buildToolErrorOutput(call llm.FunctionCall, err error) ToolExecutionOutput {
	return ToolExecutionOutput{
		FncCall: call,
		FncCallOut: &llm.FunctionCallOutput{
			CallID:    call.CallID,
			Name:      call.Name,
			Output:    err.Error(),
			IsError:   true,
			CreatedAt: time.Now(),
		},
		RawError:      err,
		ReplyRequired: true,
	}
}

type TTSGenerationData struct {
	AudioCh       chan *model.AudioFrame
	AlignedTextCh chan string
	TTFB          time.Duration
}

func PerformTTSInference(ctx context.Context, t tts.TTS, textCh <-chan string) (*TTSGenerationData, error) {
	data := &TTSGenerationData{
		AudioCh:       make(chan *model.AudioFrame, 100),
		AlignedTextCh: make(chan string, 100),
	}

	// ── Wait for the first non-empty text token before opening the WebSocket ──
	// Opening ElevenLabs before text is ready causes an idle gap that triggers
	// input_timeout_exceeded (ElevenLabs closes the connection after ~3s with
	// no text input).
	var firstText string
	for raw := range textCh {
		filtered := tts.ApplyTextTransforms(raw)
		if filtered != "" {
			firstText = filtered
			break
		}
	}
	if firstText == "" {
		// LLM produced nothing (cancelled / all filtered out) — nothing to say.
		close(data.AudioCh)
		return data, nil
	}

	stream, err := t.Stream(ctx)
	if err != nil {
		close(data.AudioCh)
		return nil, err
	}

	go func() {
		defer close(data.AudioCh)
		defer close(data.AlignedTextCh)
		defer stream.Close()
		defer logger.Logger.Debugw("TTS inference goroutine exited")

		startTime := time.Now()
<<<<<<< HEAD

		for text := range textCh {
			filteredText := tts.ApplyTextTransforms(text)
			if filteredText != "" {
				_ = stream.PushText(filteredText)
			}
		}
		_ = stream.Flush()

		var audioFrames int
		for {
			audio, err := stream.Next()
			if err != nil {
				logger.Logger.Debugw("TTS audio stream ended",
					"audio_frames", audioFrames,
					"ttfb_ms", data.TTFB.Milliseconds(),
					"elapsed_ms", time.Since(startTime).Milliseconds(),
					"reason", err.Error(),
				)
=======

		// Push text concurrently while reading audio.
		// First token is already available — push it immediately, then drain
		// the rest of textCh.
		go func() {
			textCount := 1
			fmt.Printf("📝 [TTS] Text #1: '%s'\n", firstText)
			_ = stream.PushText(firstText)

			for {
				select {
				case <-ctx.Done():
					return
				case raw, ok := <-textCh:
					if !ok {
						fmt.Printf("📝 [TTS] All text pushed (%d chunks), flushing...\n", textCount)
						_ = stream.Flush()
						return
					}
					filtered := tts.ApplyTextTransforms(raw)
					if filtered != "" {
						textCount++
						fmt.Printf("📝 [TTS] Text #%d: '%s'\n", textCount, filtered)
						_ = stream.PushText(filtered)
					}
				}
			}
		}()

		// Read audio chunks concurrently
		audioCount := 0
		for {
			audio, err := stream.Next()
			if err != nil {
				fmt.Printf("🔊 [TTS] Stream ended: %v (%d chunks received)\n", err, audioCount)
>>>>>>> origin/main
				break
			}
			if audio.Frame == nil || len(audio.Frame.Data) == 0 {
				continue
			}
			audioCount++
			if data.TTFB == 0 {
				data.TTFB = time.Since(startTime)
<<<<<<< HEAD
				logger.Logger.Debugw("TTS first audio frame received", "ttfb_ms", data.TTFB.Milliseconds())
			}
			audioFrames++
			if audio.Frame != nil {
				data.AudioCh <- audio.Frame
			}
			if audio.DeltaText != "" {
				select {
				case data.AlignedTextCh <- audio.DeltaText:
				default:
				}
=======
				fmt.Printf("🔊 [TTS] TTFB: %v\n", data.TTFB)
			}

			// Chunk large audio into 20ms frames for RTP
			sr := audio.Frame.SampleRate
			if sr == 0 {
				sr = 24000
			}
			bytesPerFrame := int(sr/50) * 2 // 20ms of 16-bit PCM
			frameData := audio.Frame.Data

			if len(frameData) <= bytesPerFrame {
				data.AudioCh <- audio.Frame
			} else {
				chunkCount := 0
				for off := 0; off < len(frameData); off += bytesPerFrame {
					end := off + bytesPerFrame
					if end > len(frameData) {
						end = len(frameData)
					}
					chunkCount++
					data.AudioCh <- &model.AudioFrame{
						Data:              frameData[off:end],
						SampleRate:        sr,
						NumChannels:       audio.Frame.NumChannels,
						SamplesPerChannel: uint32((end - off) / 2),
					}
				}
				fmt.Printf("🔊 [TTS] Chunk #%d: %d bytes → %d x 20ms frames\n", audioCount, len(frameData), chunkCount)
>>>>>>> origin/main
			}
		}
	}()

	return data, nil
}

type ToolExecutionOutput struct {
	FncCall       llm.FunctionCall
	FncCallOut    *llm.FunctionCallOutput
	RawOutput     any
	RawError      error
	ReplyRequired bool
	AgentTask     AgentInterface
}

func PerformToolExecutions(
	ctx context.Context,
	functionCh <-chan *llm.FunctionToolCall,
	toolCtx *llm.ToolContext,
) <-chan ToolExecutionOutput {
	outCh := make(chan ToolExecutionOutput, 10)

	go func() {
		defer close(outCh)
<<<<<<< HEAD
		var wg sync.WaitGroup

		for fncCall := range functionCh {
			wg.Add(1)
			go func(fc *llm.FunctionToolCall) {
				defer wg.Done()

				if fc == nil {
					return
				}

				callID := strings.TrimSpace(fc.CallID)
				if callID == "" {
					callID = buildToolCallID(fc.Name)
				}
				name := strings.TrimSpace(fc.Name)
				args, argsErr := validateToolArguments(fc.Arguments)

				call := llm.FunctionCall{
					CallID:    callID,
					Name:      name,
					Arguments: args,
					Extra:     fc.Extra,
					CreatedAt: time.Now(),
				}

				if name == "" {
					outCh <- buildToolErrorOutput(call, fmt.Errorf("empty function name"))
					return
				}

				if argsErr != nil {
					call.Arguments = fc.Arguments
					outCh <- buildToolErrorOutput(call, argsErr)
					return
				}

				tool := toolCtx.GetFunctionTool(name)
				if tool == nil {
					outCh <- buildToolErrorOutput(call, fmt.Errorf("unknown function: %s", name))
					return
				}

				if _, isProvider := tool.(llm.ProviderTool); isProvider {
					outCh <- buildToolErrorOutput(call, fmt.Errorf("provider tool executed directly by llm wrapper: %s", name))
					return
				}

				// Inject RunContext if available in the parent context
				rc := GetRunContext(ctx)
				var execCtx context.Context
				if rc != nil {
					callRC := &RunContext{
						Session:      rc.Session,
						SpeechHandle: rc.SpeechHandle,
						FunctionCall: &call,
					}
					execCtx = WithRunContext(ctx, callRC)
				} else {
					execCtx = ctx
				}

				// Support strict typed binding and validation (prepare_function_arguments equivalent)
				finalArgs, err := prepareFunctionArguments(tool, args)
				if err != nil {
					outCh <- buildToolErrorOutput(call, fmt.Errorf("failed to bind tool arguments: %w", err))
					return
				}

				result, err := tool.Execute(execCtx, finalArgs)

				var agentTask AgentInterface
				var fncOut any = result

				// LiveKit Python parity: split out AgentTask from other outputs if list
				if list, ok := result.([]any); ok {
					var agentTasks []AgentInterface
					var otherOutputs []any

					for _, item := range list {
						if t, isAgent := item.(AgentInterface); isAgent {
							agentTasks = append(agentTasks, t)
						} else if pt, isProvider := item.(llm.ProviderTool); isProvider {
							if t, isAgent := pt.(AgentInterface); isAgent {
								agentTasks = append(agentTasks, t)
							} else {
								otherOutputs = append(otherOutputs, item)
							}
						} else {
							otherOutputs = append(otherOutputs, item)
						}
					}

					if len(agentTasks) > 1 {
						logger.Logger.Errorw(fmt.Sprintf("AI function `%s` returned multiple AgentTask instances, ignoring the output", call.Name), nil, "call_id", call.CallID)
						outCh <- buildToolErrorOutput(call, fmt.Errorf("multiple AgentTask instances returned"))
						return
					}

					if len(agentTasks) > 0 {
						agentTask = agentTasks[0]
					}

					if agentTask == nil {
						fncOut = otherOutputs
					} else if len(otherOutputs) == 0 {
						fncOut = nil
					} else if len(otherOutputs) == 1 {
						fncOut = otherOutputs[0]
					} else {
						fncOut = otherOutputs
					}
				} else if task, ok := result.(AgentInterface); ok {
					agentTask = task
					fncOut = nil
				} else if pt, ok := result.(llm.ProviderTool); ok {
					if task, ok := pt.(AgentInterface); ok {
						agentTask = task
						fncOut = nil
					}
				}

				replyRequired := fncOut != nil
				if tr, ok := tool.(llm.ToolWithReply); ok {
					replyRequired = tr.IsReplyRequired()
				}

				var fncCallOut *llm.FunctionCallOutput
				if err == llm.ErrStopResponse {
					fncCallOut = nil
					replyRequired = false
				} else {
					isError := err != nil
					var outputStr string
					if err != nil {
						outputStr = err.Error()
					} else if fncOut != nil {
						if str, ok := fncOut.(string); ok {
							outputStr = str
						} else {
							outBytes, _ := json.Marshal(fncOut)
							outputStr = string(outBytes)
						}
					} else {
						// Parity with llm_utils.make_function_call_output for nil output
						outputStr = "User has been transferred to a new agent."
						if agentTask == nil {
							outputStr = "Function executed successfully."
						}
					}
					fncCallOut = &llm.FunctionCallOutput{
						CallID:    call.CallID,
						Name:      call.Name,
						Output:    outputStr,
						IsError:   isError,
						CreatedAt: time.Now(),
					}
				}

				if fncCallOut != nil && rc != nil && rc.SpeechHandle != nil && rc.SpeechHandle.RunResult != nil {
					rc.SpeechHandle.RunResult.AddEvent(&FunctionCallOutputRunEvent{
						Item: fncCallOut,
					})
				}

				outCh <- ToolExecutionOutput{
					FncCall:       call,
					FncCallOut:    fncCallOut,
					RawOutput:     result,
					RawError:      err,
					ReplyRequired: replyRequired,
					AgentTask:     agentTask,
				}
			}(fncCall)
		}

		wg.Wait()
=======
		for fnCall := range functionCh {
			if fnCall == nil || fnCall.Name == "" {
				continue
			}
			fncCall := llm.FunctionCall{
				CallID:    fnCall.CallID,
				Name:      fnCall.Name,
				Arguments: fnCall.Arguments,
			}
			tool := toolCtx.GetFunctionTool(fnCall.Name)
			var fncOut *llm.FunctionCallOutput
			if tool == nil {
				fncOut = &llm.FunctionCallOutput{
					CallID: fnCall.CallID,
					Output: fmt.Sprintf("Error: tool '%s' not found", fnCall.Name),
				}
			} else {
				result, err := tool.Execute(ctx, fnCall.Arguments)
				if err != nil {
					fncOut = &llm.FunctionCallOutput{
						CallID: fnCall.CallID,
						Output: fmt.Sprintf("Error: %v", err),
					}
				} else {
					fncOut = &llm.FunctionCallOutput{
						CallID: fnCall.CallID,
						Output: result,
					}
				}
			}
			outCh <- ToolExecutionOutput{
				FncCall:    fncCall,
				FncCallOut: fncOut,
			}
		}
>>>>>>> origin/main
	}()
	return outCh
}
