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
	"github.com/cavos-io/conversation-worker/model"
)

type LLMGenerationData struct {
	TextCh        chan string
	FunctionCh    chan *llm.FunctionToolCall
	GeneratedText string
	TTFT          time.Duration
}

func PerformLLMInference(ctx context.Context, l llm.LLM, chatCtx *llm.ChatContext, tools []llm.Tool) (*LLMGenerationData, error) {
	data := &LLMGenerationData{
		TextCh:     make(chan string, 100),
		FunctionCh: make(chan *llm.FunctionToolCall, 10),
	}

	stream, err := l.Chat(ctx, chatCtx, llm.WithTools(tools))
	if err != nil {
		return nil, err
	}

	go func() {
		defer close(data.TextCh)
		defer close(data.FunctionCh)
		defer stream.Close()

		startTime := time.Now()
		toolCalls := make([]*llm.FunctionToolCall, 0)
		toolCallsByID := make(map[string]*llm.FunctionToolCall)
		toolCallsByIndex := make(map[int]*llm.FunctionToolCall)

		for {
			chunk, err := stream.Next()
			if err != nil {
				break
			}

			if data.TTFT == 0 {
				data.TTFT = time.Since(startTime)
			}

			if chunk.Delta != nil {
				if chunk.Delta.Content != "" {
					data.GeneratedText += chunk.Delta.Content
					data.TextCh <- chunk.Delta.Content
				}
				for _, fc := range chunk.Delta.ToolCalls {
					mergeToolCallDelta(&toolCalls, toolCallsByID, toolCallsByIndex, fc)
				}
			}
		}

		for idx, fc := range toolCalls {
			finalized := finalizeToolCall(fc, idx)
			if finalized == nil {
				continue
			}
			data.FunctionCh <- finalized
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
		RawError: err,
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

		startTime := time.Now()

		for text := range textCh {
			filteredText := tts.ApplyTextTransforms(text)
			if filteredText != "" {
				_ = stream.PushText(filteredText)
			}
		}
		_ = stream.Flush()

		for {
			audio, err := stream.Next()
			if err != nil {
				break
			}
			if data.TTFB == 0 {
				data.TTFB = time.Since(startTime)
			}
			data.AudioCh <- audio.Frame
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
	FncCall    llm.FunctionCall
	FncCallOut *llm.FunctionCallOutput
	RawOutput  any
	RawError   error
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

				var argsMap map[string]any
				if err := json.Unmarshal([]byte(args), &argsMap); err != nil {
					outCh <- buildToolErrorOutput(call, fmt.Errorf("failed to parse tool arguments: %w", err))
					return
				}

				result, err := tool.Execute(execCtx, argsMap)
				
				var fncCallOut *llm.FunctionCallOutput
				if err == llm.ErrStopResponse {
					fncCallOut = nil
				} else {
					isError := err != nil
					var outputStr string
					if err != nil {
						outputStr = err.Error()
					} else if result != nil {
						// Best effort formatting for the chat context
						if str, ok := result.(string); ok {
							outputStr = str
						} else {
							outBytes, _ := json.Marshal(result)
							outputStr = string(outBytes)
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

				outCh <- ToolExecutionOutput{
					FncCall:    call,
					FncCallOut: fncCallOut,
					RawOutput:  result,
					RawError:   err,
				}
			}(fncCall)
		}

		wg.Wait()
	}()

	return outCh
}
