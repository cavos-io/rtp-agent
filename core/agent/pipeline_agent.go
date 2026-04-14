package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
)

type PipelineAgent struct {
	agent   *Agent
	vad     vad.VAD
	stt     stt.STT
	LLM     llm.LLM
	tts     tts.TTS
	chatCtx *llm.ChatContext

	mu      sync.Mutex
	session *AgentSession

	ctx       context.Context
	cancel    context.CancelFunc
	runCancel context.CancelFunc
	runWG     sync.WaitGroup
}

func NewPipelineAgent(
	vad vad.VAD,
	sttInstance stt.STT,
	llmObj llm.LLM,
	ttsInstance tts.TTS,
	chatCtx *llm.ChatContext,
) *PipelineAgent {
	if chatCtx == nil {
		chatCtx = llm.NewChatContext()
	}

	if sttInstance != nil && !sttInstance.Capabilities().Streaming {
		sttInstance = stt.NewStreamAdapter(sttInstance, vad)
	}

	if ttsInstance != nil && !ttsInstance.Capabilities().Streaming {
		ttsInstance = tts.NewStreamAdapter(ttsInstance)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &PipelineAgent{
		vad:     vad,
		stt:     sttInstance,
		LLM:     llmObj,
		tts:     ttsInstance,
		chatCtx: chatCtx,
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (va *PipelineAgent) Start(ctx context.Context, s *AgentSession) error {
	runCtx, runCancel := context.WithCancel(ctx)

	va.mu.Lock()
	va.session = s
	va.runCancel = runCancel
	va.mu.Unlock()

	va.runWG.Add(1)
	go func() {
		defer va.runWG.Done()
		va.run(runCtx)
	}()
	return nil
}

func (va *PipelineAgent) Stop() {
	va.mu.Lock()
	runCancel := va.runCancel
	va.runCancel = nil
	generateCancel := va.cancel
	va.mu.Unlock()

	if runCancel != nil {
		runCancel()
	}
	if generateCancel != nil {
		generateCancel()
	}

	va.runWG.Wait()
}

func (va *PipelineAgent) run(ctx context.Context) {
	logger.Logger.Infow("PipelineAgent started")

	// Start with nil audioStream; we'll check for Input.Audio dynamically
	var audioStream <-chan *model.AudioFrame
	var warmupUntil time.Time

	logger.Logger.Infow("PipelineAgent started, waiting for audio input...")

	for {
		// Check if Input.Audio has been set (it might be attached after pipeline starts)
		if va.session != nil && va.session.Input.Audio != nil && audioStream == nil {
			audioStream = va.session.Input.Audio.Stream()
			if va.session.Options.AECWarmupDuration > 0 {
				warmupUntil = time.Now().Add(time.Duration(va.session.Options.AECWarmupDuration * float64(time.Second)))
			}
			logger.Logger.Infow("✅ Audio stream connected to pipeline!")
		}

		select {
		case <-ctx.Done():
			return
		case frame, ok := <-audioStream:
			if !ok {
				logger.Logger.Infow("Audio stream closed")
				return
			}
			if !warmupUntil.IsZero() && time.Now().Before(warmupUntil) {
				continue
			}
			if va.session != nil && va.session.Activity != nil {
				_ = va.session.Activity.PushAudio(frame)
			}
		case <-time.After(100 * time.Millisecond):
			// Check periodically if audio has been attached
			if va.session != nil && va.session.Input.Audio != nil && audioStream == nil {
				audioStream = va.session.Input.Audio.Stream()
				if va.session.Options.AECWarmupDuration > 0 {
					warmupUntil = time.Now().Add(time.Duration(va.session.Options.AECWarmupDuration * float64(time.Second)))
				}
				logger.Logger.Infow("✅ Audio stream NOW connected to pipeline!")
			}
		}
	}
}

func (va *PipelineAgent) GenerateReply(speech *SpeechHandle) {
	defer speech.MarkDone()

	va.mu.Lock()
	session := va.session
	ctx := va.ctx
	va.mu.Unlock()

	if session == nil {
		return
	}

	logger.Logger.Infow("Generating reply")
	if session.Timeline != nil {
		session.Timeline.Add("reply_generation_started", nil)
	}
	session.UpdateAgentState(AgentStateThinking)

	toolsInterface := make([]interface{}, len(session.Tools))
	for i, t := range session.Tools {
		toolsInterface[i] = t
	}
	toolCtx := llm.NewToolContext(toolsInterface)

	// In Python parity, we loop for tool calls
	steps := 1
	for {
		if speech.IsInterrupted() {
			if session.Timeline != nil {
				session.Timeline.Add("reply_generation_interrupted", nil)
			}
			if session.Output.Audio == nil {
				session.UpdateAgentState(AgentStateIdle)
			}
			return
		}

		genData, err := PerformLLMInference(ctx, va.LLM, va.chatCtx, session.Tools)
		if err != nil {
			logger.Logger.Errorw("LLM inference failed", err)
			if session.Timeline != nil {
				session.Timeline.Add("llm_inference_failed", map[string]any{
					"error": err.Error(),
				})
			}
			if session.Output.Audio == nil {
				session.UpdateAgentState(AgentStateIdle)
			}
			return
		}

		toolOutCh := PerformToolExecutions(ctx, genData.FunctionCh, toolCtx)

		// Start TTS in parallel with LLM text
		ttsGen, err := PerformTTSInference(ctx, va.tts, genData.TextCh)
		if err != nil {
			logger.Logger.Errorw("TTS inference failed", err)
			if session.Timeline != nil {
				session.Timeline.Add("tts_inference_failed", map[string]any{
					"error": err.Error(),
				})
			}
		} else {
			if session.Timeline != nil {
				session.Timeline.Add("tts_playout_started", nil)
			}
			var alignedWG sync.WaitGroup
			if session.Options.UseTTSAlignedTranscript && session.Output.Transcription != nil {
				alignedWG.Add(1)
				go func() {
					defer alignedWG.Done()
					for deltaText := range ttsGen.AlignedTextCh {
						if deltaText == "" {
							continue
						}
						_ = session.Output.Transcription.CaptureText(deltaText)
					}
					session.Output.Transcription.Flush()
				}()
			}

			if session.Output.Audio == nil {
				session.UpdateAgentState(AgentStateSpeaking)
			}
			for frame := range ttsGen.AudioCh {
				if speech.IsInterrupted() {
					break
				}
				select {
				case <-ctx.Done():
					return
				default:
					if session.Output.Audio != nil {
						_ = session.Output.Audio.CaptureFrame(frame)
					}
				}
			}
			if session.Output.Audio != nil {
				session.Output.Audio.Flush()
				if speech.IsInterrupted() {
					session.Output.Audio.ClearBuffer()
				} else {
					_ = session.Output.Audio.WaitForPlayout(ctx)
				}
			}
			if session.Timeline != nil {
				session.Timeline.Add("tts_playout_finished", nil)
			}
			alignedWG.Wait()
		}

		// Wait for tool executions to complete and collect results
		var executedTools bool
		for toolOut := range toolOutCh {
			executedTools = true
			logger.Logger.Infow("Tool executed", "name", toolOut.FncCall.Name)
			if session.Timeline != nil {
				session.Timeline.Add("tool_executed", map[string]any{
					"name":     toolOut.FncCall.Name,
					"is_error": toolOut.RawError != nil,
				})
			}

			va.chatCtx.Append(&toolOut.FncCall)
			if toolOut.FncCallOut != nil {
				va.chatCtx.Append(toolOut.FncCallOut)
			}
		}

		// If no tool calls, we're done
		if !executedTools {
			if session.Timeline != nil {
				session.Timeline.Add("reply_generation_completed", nil)
			}
			if session.Output.Audio == nil {
				session.UpdateAgentState(AgentStateIdle)
			}
			break
		}

		steps++
		if session.Options.MaxToolSteps > 0 && steps > session.Options.MaxToolSteps+1 {
			logger.Logger.Infow("maximum number of tool steps reached", "maxToolSteps", session.Options.MaxToolSteps)
			if session.Timeline != nil {
				session.Timeline.Add("tool_step_limit_reached", map[string]any{
					"max_tool_steps": session.Options.MaxToolSteps,
				})
			}
			if session.Output.Audio == nil {
				session.UpdateAgentState(AgentStateIdle)
			}
			break
		}

		// Loop back to LLM with tool outputs
	}
}
