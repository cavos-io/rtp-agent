package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
)

type LLMGenerationData struct {
	TextCh        chan string
	FunctionCh    chan *llm.FunctionToolCall
	GeneratedText string
	TTFT          time.Duration
}

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
	}
	logger.Logger.Debugw("LLM chat stream created, starting goroutine")

	go func() {
		defer close(data.TextCh)
		defer close(data.FunctionCh)
		defer stream.Close()
		defer logger.Logger.Debugw("LLM inference goroutine exited")

		startTime := time.Now()
		toolCalls := make([]*llm.FunctionToolCall, 0)
		toolCallsByID := make(map[string]*llm.FunctionToolCall)
		toolCallsByIndex := make(map[int]*llm.FunctionToolCall)

		var chunkCount int
		for {
			chunk, err := stream.Next()
			if err != nil {
				logger.Logger.Debugw("LLM stream ended",
					"chunks_received", chunkCount,
					"text_length", len(data.GeneratedText),
					"tool_calls_accumulated", len(toolCalls),
					"reason", err.Error(),
				)
				break
			}
			chunkCount++

			if data.TTFT == 0 {
				data.TTFT = time.Since(startTime)
				logger.Logger.Debugw("LLM first token received", "ttft_ms", data.TTFT.Milliseconds())
			}

			if chunk.Delta != nil {
				if chunk.Delta.Content != "" {
					data.GeneratedText += chunk.Delta.Content
					data.TextCh <- chunk.Delta.Content
				}
				if len(chunk.Delta.ToolCalls) > 0 {
					logger.Logger.Debugw("LLM tool call delta received",
						"tool_calls_in_delta", len(chunk.Delta.ToolCalls),
						"accumulated_so_far", len(toolCalls),
					)
					for _, fc := range chunk.Delta.ToolCalls {
						mergeToolCallDelta(&toolCalls, toolCallsByID, toolCallsByIndex, fc)
					}
				}
			}
		}

		logger.Logger.Infow("LLM stream complete",
			"total_text_length", len(data.GeneratedText),
			"total_tool_calls", len(toolCalls),
			"total_chunks", chunkCount,
			"ttft_ms", data.TTFT.Milliseconds(),
			"elapsed_ms", time.Since(startTime).Milliseconds(),
		)

		if data.GeneratedText != "" {
			logger.Logger.Debugw("Emitting chat message run event", "text_length", len(data.GeneratedText))
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

		for idx, fc := range toolCalls {
			finalized := finalizeToolCall(fc, idx)
			if finalized == nil {
				logger.Logger.Debugw("Tool call skipped after finalization", "index", idx)
				continue
			}
			logger.Logger.Debugw("Emitting finalized tool call",
				"name", finalized.Name,
				"call_id", finalized.CallID,
				"arguments_length", len(finalized.Arguments),
			)
			data.FunctionCh <- finalized

			// Capture event in RunResult if attached to SpeechHandle
			if rc := GetRunContext(ctx); rc != nil && rc.SpeechHandle != nil && rc.SpeechHandle.RunResult != nil {
				logger.Logger.Debugw("Emitting function call run event", "name", finalized.Name, "call_id", finalized.CallID)
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

	stream, err := t.Stream(ctx)
	if err != nil {
		return nil, err
	}

	go func() {
		defer close(data.AudioCh)
		defer close(data.AlignedTextCh)
		defer stream.Close()
		defer logger.Logger.Debugw("TTS inference goroutine exited")

		startTime := time.Now()

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
				break
			}
			if data.TTFB == 0 {
				data.TTFB = time.Since(startTime)
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
	}()

	return outCh
}
