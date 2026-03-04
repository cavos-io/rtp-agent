package agent

import (
	"context"
	"fmt"
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
					f := fc
					data.FunctionCh <- &f
				}
			}
		}
	}()

	return data, nil
}

type TTSGenerationData struct {
	AudioCh chan *model.AudioFrame
	TTFB    time.Duration
}

func PerformTTSInference(ctx context.Context, t tts.TTS, textCh <-chan string) (*TTSGenerationData, error) {
	data := &TTSGenerationData{
		AudioCh: make(chan *model.AudioFrame, 100),
	}

	stream, err := t.Stream(ctx)
	if err != nil {
		return nil, err
	}

	go func() {
		defer close(data.AudioCh)
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
				
				call := llm.FunctionCall{
					CallID:    fc.CallID,
					Name:      fc.Name,
					Arguments: fc.Arguments,
					Extra:     fc.Extra,
					CreatedAt: time.Now(),
				}

				tool := toolCtx.GetFunctionTool(fc.Name)
				if tool == nil {
					outCh <- ToolExecutionOutput{
						FncCall: call,
						FncCallOut: &llm.FunctionCallOutput{
							CallID:    fc.CallID,
							Name:      fc.Name,
							Output:    fmt.Sprintf("Unknown function: %s", fc.Name),
							IsError:   true,
							CreatedAt: time.Now(),
						},
						RawError: fmt.Errorf("unknown function: %s", fc.Name),
					}
					return
				}

				result, err := tool.Execute(ctx, fc.Arguments)
				isError := err != nil
				outputStr := result
				if err != nil {
					outputStr = err.Error()
				}

				outCh <- ToolExecutionOutput{
					FncCall: call,
					FncCallOut: &llm.FunctionCallOutput{
						CallID:    fc.CallID,
						Name:      fc.Name,
						Output:    outputStr,
						IsError:   isError,
						CreatedAt: time.Now(),
					},
					RawOutput: result,
					RawError:  err,
				}
			}(fncCall)
		}
		
		wg.Wait()
	}()

	return outCh
}

