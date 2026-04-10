package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/model"
)

type LLMGenerationData struct {
	TextCh     chan string
	FunctionCh chan *llm.FunctionToolCall
	FullTextCh chan string // receives the complete assembled text when streaming is done
	Usage      *llm.CompletionUsage
}

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
	}

	go func() {
		defer close(data.TextCh)
		defer close(data.FunctionCh)
		defer stream.Close()

		var sb strings.Builder
		for {
			chunk, err := stream.Next()
			if err != nil {
				break
			}

			if chunk.Delta != nil {
				if chunk.Delta.Content != "" {
					sb.WriteString(chunk.Delta.Content)
					data.TextCh <- chunk.Delta.Content
				}
				for _, tc := range chunk.Delta.ToolCalls {
					data.FunctionCh <- &tc
				}
			}
			if chunk.Usage != nil {
				data.Usage = chunk.Usage
			}
		}
		// Non-blocking: buffered channel holds the result for the consumer.
		data.FullTextCh <- sb.String()
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

		// Push text concurrently while reading audio
		go func() {
			textCount := 0
			for text := range textCh {
				filteredText := tts.ApplyTextTransforms(text)
				if filteredText != "" {
					textCount++
					fmt.Printf("📝 [TTS] Text #%d: '%s'\n", textCount, filteredText)
					_ = stream.PushText(filteredText)
				}
			}
			fmt.Printf("📝 [TTS] All text pushed (%d chunks), flushing...\n", textCount)
			_ = stream.Flush()
		}()

		// Read audio chunks concurrently
		audioCount := 0
		for {
			audio, err := stream.Next()
			if err != nil {
				fmt.Printf("🔊 [TTS] Stream ended: %v (%d chunks received)\n", err, audioCount)
				break
			}
			if audio.Frame == nil || len(audio.Frame.Data) == 0 {
				continue
			}
			audioCount++
			if data.TTFB == 0 {
				data.TTFB = time.Since(startTime)
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
	}()
	return outCh
}
