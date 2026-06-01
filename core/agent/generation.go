package agent

import (
	"context"
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
					if fc.Type != "" && fc.Type != "function" {
						continue
					}
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
				result := llm.ExecuteFunctionCall(ctx, fc, toolCtx)
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
