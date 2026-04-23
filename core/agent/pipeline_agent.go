package agent

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/cavos-io/conversation-worker/core/audio"
	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
)

type PipelineAgent struct {
	agent       *Agent
	vad         vad.VAD
	stt         stt.STT
	LLM         llm.LLM
	tts         tts.TTS
	chatCtx     *llm.ChatContext
	noiseFilter audio.NoiseFilter // optional

	audioInCh chan *model.AudioFrame
	mu        sync.Mutex
	session   *AgentSession

	ctx    context.Context
	cancel context.CancelFunc

	PublishAudio func(frame *model.AudioFrame) error
}

func NewPipelineAgent(
	vad vad.VAD,
	stt stt.STT,
	llmObj llm.LLM,
	tts tts.TTS,
	chatCtx *llm.ChatContext,
	noiseFilter audio.NoiseFilter,
) *PipelineAgent {
	if chatCtx == nil {
		chatCtx = llm.NewChatContext()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &PipelineAgent{
		vad:         vad,
		stt:         stt,
		LLM:         llmObj,
		tts:         tts,
		chatCtx:     chatCtx,
		noiseFilter: noiseFilter,
		audioInCh:   make(chan *model.AudioFrame, 100),
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (va *PipelineAgent) Start(ctx context.Context, s *AgentSession) error {
	va.mu.Lock()
	va.session = s
	va.mu.Unlock()

	go va.run(ctx)
	return nil
}

func (va *PipelineAgent) run(ctx context.Context) {
	fmt.Println("🔧 [Pipeline] PipelineAgent.run() started")
	logger.Logger.Infow("PipelineAgent started")

	vadStream, err := va.vad.Stream(ctx)
	if err != nil {
		fmt.Printf("❌ [Pipeline] Failed to start VAD stream: %v\n", err)
		logger.Logger.Errorw("failed to start VAD stream", err)
		return
	}
	fmt.Println("✅ [Pipeline] VAD stream started")

	sttStream, err := va.stt.Stream(ctx, "")
	if err != nil {
		fmt.Printf("❌ [Pipeline] Failed to start STT stream: %v\n", err)
		logger.Logger.Errorw("failed to start STT stream", err)
		return
	}
	fmt.Println("✅ [Pipeline] STT stream started")

	go va.vadLoop(vadStream)
	go va.sttLoop(sttStream)

	fmt.Println("🎧 [Pipeline] Audio processing loop running...")
	var frameCount atomic.Int64
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-va.audioInCh:
			c := frameCount.Add(1)
			if c == 1 || c%500 == 0 {
				fmt.Printf("🔊 [Pipeline] Audio frames received: %d (latest: %d bytes)\n", c, len(frame.Data))
			}
			if va.noiseFilter != nil {
				if filtered, err := va.noiseFilter.Process(frame); err == nil {
					frame = filtered
				}
			}
			_ = vadStream.PushFrame(frame)
			_ = sttStream.PushFrame(frame)
		}
	}
}

func (va *PipelineAgent) vadLoop(stream vad.VADStream) {
	for {
		ev, err := stream.Next()
		if err != nil {
			if err != io.EOF {
				logger.Logger.Errorw("VAD stream error", err)
			}
			return
		}

		if ev.Type == vad.VADEventStartOfSpeech {
			fmt.Println("🗣️  [VAD] User started speaking")
			logger.Logger.Infow("User started speaking")
			va.session.UpdateUserState(UserStateSpeaking)

			// Interrupt ongoing agent speech/generation
			va.mu.Lock()
			if va.cancel != nil {
				va.cancel()
				ctx, cancel := context.WithCancel(context.Background())
				va.ctx = ctx
				va.cancel = cancel
			}
			va.mu.Unlock()
		} else if ev.Type == vad.VADEventEndOfSpeech {
			fmt.Println("🔇 [VAD] User stopped speaking")
			logger.Logger.Infow("User stopped speaking")
			va.session.UpdateUserState(UserStateListening)
		}
	}
}

func (va *PipelineAgent) sttLoop(stream stt.RecognizeStream) {
	for {
		ev, err := stream.Next()
		if err != nil {
			if err != io.EOF {
				logger.Logger.Errorw("STT stream error", err)
			}
			return
		}

		if ev.Type == stt.SpeechEventFinalTranscript {
			transcript := ev.Alternatives[0].Text
			fmt.Printf("📝 [STT] Transcript: %s\n", transcript)
			logger.Logger.Infow("Final transcript", "text", transcript)

			va.chatCtx.Append(&llm.ChatMessage{
				Role: llm.ChatRoleUser,
				Content: []llm.ChatContent{
					{Text: transcript},
				},
			})

			go va.generateReply()
		}
	}
}

func (va *PipelineAgent) generateReply() {
	va.mu.Lock()
	session := va.session
	ctx := va.ctx
	va.mu.Unlock()

	if session == nil {
		return
	}

	fmt.Println("🤖 [LLM] Generating reply...")
	logger.Logger.Infow("Generating reply")
	session.UpdateAgentState(AgentStateThinking)

	toolsInterface := make([]interface{}, len(session.Tools))
	for i, t := range session.Tools {
		toolsInterface[i] = t
	}
	toolCtx := llm.NewToolContext(toolsInterface)

	// In Python parity, we loop for tool calls
	for {
		fmt.Println("📤 [LLM] Calling PerformLLMInference...")
		genData, err := PerformLLMInference(ctx, va.LLM, va.chatCtx, session.Tools)
		if err != nil {
			fmt.Printf("❌ [LLM] Inference failed: %v\n", err)
			logger.Logger.Errorw("LLM inference failed", err)
			session.UpdateAgentState(AgentStateIdle)
			return
		}
		fmt.Println("✅ [LLM] Inference started, waiting for text...")

		toolOutCh := PerformToolExecutions(ctx, genData.FunctionCh, toolCtx)

		// Start TTS in parallel with LLM text
		fmt.Println("🔊 [TTS] Starting TTS inference...")
		ttsGen, err := PerformTTSInference(ctx, va.tts, genData.TextCh)
		if err != nil {
			fmt.Printf("❌ [TTS] Inference failed: %v\n", err)
			logger.Logger.Errorw("TTS inference failed", err)
		} else {
			fmt.Println("✅ [TTS] TTS started, publishing audio...")
			session.UpdateAgentState(AgentStateSpeaking)
			frameCount := 0
			for frame := range ttsGen.AudioCh {
				select {
				case <-ctx.Done():
					fmt.Println("⚠️ [TTS] Context cancelled during playout")
					return
				default:
					if va.PublishAudio != nil {
						_ = va.PublishAudio(frame)
						frameCount++
					}
				}
			}
			fmt.Printf("✅ [TTS] Published %d audio frames\n", frameCount)
		}

		// Wait for tool executions to complete and collect results
		var executedTools bool
		for toolOut := range toolOutCh {
			executedTools = true
			fmt.Printf("🔧 [Tool] Executed: %s\n", toolOut.FncCall.Name)
			logger.Logger.Infow("Tool executed", "name", toolOut.FncCall.Name)

			va.chatCtx.Append(&toolOut.FncCall)
			if toolOut.FncCallOut != nil {
				va.chatCtx.Append(toolOut.FncCallOut)
			}
		}

		// If no tool calls, we're done
		if !executedTools {
			fmt.Println("✅ [LLM] Reply complete, returning to idle")
			session.UpdateAgentState(AgentStateIdle)
			break
		}
		// Loop back to LLM with tool outputs
	}
}

func (va *PipelineAgent) OnAudioFrame(ctx context.Context, frame *model.AudioFrame) {
	select {
	case va.audioInCh <- frame:
	default:
	}
}
