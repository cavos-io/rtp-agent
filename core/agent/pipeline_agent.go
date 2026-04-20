package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/google/uuid"
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
	// Audio forwarding is now handled by AgentSession
	<-ctx.Done()
}

func (va *PipelineAgent) GenerateReply(speech *SpeechHandle) {
	defer speech.MarkDone()

	va.mu.Lock()
	session := va.session
	ctx := va.ctx
	va.mu.Unlock()

	if session == nil {
		logger.Logger.Infow("GenerateReply called but session is nil, skipping")
		return
	}

	logger.Logger.Infow("Generating reply",
		"tools_count", len(session.Tools),
		"has_audio_output", session.Output.Audio != nil,
		"has_transcription", session.Output.Transcription != nil,
		"use_tts_aligned_transcript", session.Options.UseTTSAlignedTranscript,
		"max_tool_steps", session.Options.MaxToolSteps,
	)
	session.UpdateAgentState(AgentStateThinking)

	toolsInterface := make([]interface{}, len(session.Tools))
	for i, t := range session.Tools {
		toolsInterface[i] = t
	}
	toolCtx := llm.NewToolContext(toolsInterface)

	// Check for manual speech injection (GAP-004)
	if speech.ManualText != "" {
		logger.Logger.Infow("Processing manual speech injection", "text", speech.ManualText)
		
		textCh := make(chan string, 1)
		textCh <- speech.ManualText
		close(textCh)

		// Start TTS directly
		ttsGen, err := PerformTTSInference(ctx, va.tts, textCh)
		if err != nil {
			logger.Logger.Errorw("Manual TTS inference failed", err)
			return
		}

		// Handle Playback and Transcription (Same as default loop but simplified)
		va.handlePlaybackAndTranscription(ctx, speech, ttsGen)
		return
	}

	// In Python parity, we loop for tool calls
	steps := 1
	for {
		logger.Logger.Debugw("GenerateReply loop iteration", "step", steps)

		if speech.IsInterrupted() {
			logger.Logger.Infow("Speech interrupted before LLM inference", "step", steps)
			if session.Output.Audio == nil {
				session.UpdateAgentState(AgentStateIdle)
			}
			return
		}

		var genData *LLMGenerationData
		var err error
		baseAgent := session.Agent.GetAgent()
		if baseAgent != nil && baseAgent.LLMNode != nil {
			logger.Logger.Debugw("Using custom LLM node", "step", steps)
			genData, err = baseAgent.LLMNode(ctx, va.LLM, va.chatCtx, session.Tools)
		} else {
			logger.Logger.Debugw("Using default LLM inference", "step", steps)
			genData, err = PerformLLMInference(ctx, va.LLM, va.chatCtx, session.Tools)
		}

		if err != nil {
			logger.Logger.Errorw("LLM inference failed", err, "step", steps)
			if session.Timeline != nil {
				session.Timeline.AddEvent(&ErrorEvent{
					Error:     err,
					Source:    va.LLM,
					CreatedAt: time.Now(),
				})
			}
			if session.Output.Audio == nil {
				session.UpdateAgentState(AgentStateIdle)
			}
			return
		}
		logger.Logger.Debugw("LLM inference succeeded", "step", steps)

		toolOutCh := PerformToolExecutions(ctx, genData.FunctionCh, toolCtx)
		ttsGen, err := PerformTTSInference(ctx, va.tts, genData.TextCh)

		if err != nil {
			logger.Logger.Errorw("TTS inference failed", err, "step", steps)
			if session.Timeline != nil {
				session.Timeline.AddEvent(&ErrorEvent{
					Error:     err,
					Source:    va.tts,
					CreatedAt: time.Now(),
				})
			}
		} else {
			va.handlePlaybackAndTranscription(ctx, speech, ttsGen)
		}

		// Wait for tool executions to complete and collect results
		var executedTools bool
		var stopResponse bool
		var replyRequired bool
		var agentTask AgentInterface

		var toolCalls []llm.FunctionCall
		var toolOutputs []*llm.FunctionCallOutput

		for toolOut := range toolOutCh {
			executedTools = true
			if toolOut.RawError == llm.ErrStopResponse {
				stopResponse = true
				logger.Logger.Infow("Tool returned stop response", "name", toolOut.FncCall.Name, "step", steps)
			}
			if toolOut.ReplyRequired {
				replyRequired = true
			}
			if toolOut.AgentTask != nil {
				agentTask = toolOut.AgentTask
				stopResponse = true // Handoff implies stopping the current generation
			}
			logger.Logger.Infow("Tool executed",
				"name", toolOut.FncCall.Name,
				"reply_required", toolOut.ReplyRequired,
				"has_agent_task", toolOut.AgentTask != nil,
				"has_error", toolOut.RawError != nil,
				"step", steps,
			)

			toolCalls = append(toolCalls, toolOut.FncCall)
			toolOutputs = append(toolOutputs, toolOut.FncCallOut)

			va.chatCtx.Append(&toolOut.FncCall)
			if toolOut.FncCallOut != nil {
				va.chatCtx.Append(toolOut.FncCallOut)
			}
		}

		logger.Logger.Debugw("Tool executions complete",
			"executed_tools", executedTools,
			"tool_count", len(toolCalls),
			"stop_response", stopResponse,
			"reply_required", replyRequired,
			"has_agent_task", agentTask != nil,
			"step", steps,
		)

		if agentTask != nil {
			logger.Logger.Infow("Agent handoff triggered by tool",
				"old_agent", session.Agent.GetAgent().ID,
				"new_agent", agentTask.GetAgent().ID,
				"step", steps,
			)
			handoff := &llm.AgentHandoff{
				ID:         "handoff_" + uuid.NewString()[:8],
				NewAgentID: agentTask.GetAgent().ID,
				CreatedAt:  time.Now(),
			}

			if session.Timeline != nil {
				session.Timeline.AddEvent(&AgentHandoffEvent{
					Handoff:    handoff,
					OldAgent:   session.Agent,
					NewAgent:   agentTask,
					OldAgentID: session.Agent.GetAgent().ID,
					NewAgentID: agentTask.GetAgent().ID,
					CreatedAt:  time.Now(),
				})
			}

			if speech.RunResult != nil {
				speech.RunResult.AddEvent(&AgentHandoffRunEvent{
					Item:     handoff,
					OldAgent: session.Agent,
					NewAgent: agentTask,
				})
			}

			// Trigger the session update
			taskDone := make(chan struct{})
			if speech.RunResult != nil {
				speech.RunResult.WatchTask(taskDone)
			}
			go func() {
				defer close(taskDone)
				logger.Logger.Debugw("Executing agent update for handoff", "new_agent", agentTask.GetAgent().ID)
				_ = session.UpdateAgent(agentTask, nil)
				logger.Logger.Debugw("Agent update for handoff complete", "new_agent", agentTask.GetAgent().ID)
			}()
		}

		if executedTools {
			if session.Timeline != nil {
				session.Timeline.AddEvent(&FunctionToolsExecutedEvent{
					FunctionCalls:       toolCalls,
					FunctionCallOutputs: toolOutputs,
					CreatedAt:           time.Now(),
					HasToolReply:        replyRequired,
					HasAgentHandoff:     agentTask != nil,
				})
			}
		}

		// If no tool calls or StopResponse, we're done
		if !executedTools || stopResponse || !replyRequired {
			if stopResponse {
				speech.FinalOutput = "stop_response"
			} else if genData.GeneratedText != "" {
				speech.FinalOutput = genData.GeneratedText
			} else if len(toolCalls) > 0 {
				speech.FinalOutput = toolCalls
			}

			logger.Logger.Infow("GenerateReply complete",
				"stop_response", stopResponse,
				"executed_tools", executedTools,
				"reply_required", replyRequired,
				"total_steps", steps,
				"final_output", fmt.Sprintf("%v", speech.FinalOutput),
				"final_output_type", fmt.Sprintf("%T", speech.FinalOutput),
			)

			if session.Output.Audio == nil {
				session.UpdateAgentState(AgentStateIdle)
			}
			break
		}

		steps++
		if session.Options.MaxToolSteps > 0 && steps > session.Options.MaxToolSteps+1 {
			logger.Logger.Infow("Maximum number of tool steps reached",
				"max_tool_steps", session.Options.MaxToolSteps,
				"steps_taken", steps,
			)
			if session.Timeline != nil {
				session.Timeline.AddEvent(&ErrorEvent{
					Error:     fmt.Errorf("maximum number of tool steps reached: %d", session.Options.MaxToolSteps),
					Source:    "pipeline_agent",
					CreatedAt: time.Now(),
				})
			}
			if session.Output.Audio == nil {
				session.UpdateAgentState(AgentStateIdle)
			}
			break
		}

		logger.Logger.Debugw("Looping back to LLM with tool outputs", "step", steps, "tool_count", len(toolCalls))
		// Loop back to LLM with tool outputs
	}
}

func (va *PipelineAgent) handlePlaybackAndTranscription(ctx context.Context, speech *SpeechHandle, ttsGen *TTSGenerationData) {
	va.mu.Lock()
	session := va.session
	va.mu.Unlock()

	if session == nil {
		return
	}

	baseAgent := session.Agent.GetAgent()

	logger.Logger.Debugw("Starting audio playback and transcription")
	var alignedWG sync.WaitGroup
	if session.Options.UseTTSAlignedTranscript && session.Output.Transcription != nil {
		logger.Logger.Debugw("TTS aligned transcript enabled, starting transcription goroutine")
		var textCh <-chan string = ttsGen.AlignedTextCh
		if baseAgent != nil && baseAgent.TranscriptionNode != nil {
			logger.Logger.Debugw("Using custom transcription node")
			var nodeErr error
			textCh, nodeErr = baseAgent.TranscriptionNode(ctx, textCh)
			if nodeErr != nil {
				logger.Logger.Errorw("Transcription node failed", nodeErr)
			}
		}

		if textCh != nil {
			alignedWG.Add(1)
			go func() {
				defer alignedWG.Done()
				var capturedChunks int
				for deltaText := range textCh {
					if deltaText == "" {
						continue
					}
					_ = session.Output.Transcription.CaptureText(deltaText)
					capturedChunks++
				}
				session.Output.Transcription.Flush()
				logger.Logger.Debugw("Transcription goroutine finished", "chunks_captured", capturedChunks)
			}()
		}
	}

	if session.Output.Audio == nil {
		session.UpdateAgentState(AgentStateSpeaking)
	}

	var audioCh <-chan *model.AudioFrame = ttsGen.AudioCh
	if baseAgent != nil && baseAgent.RealtimeAudioOutputNode != nil {
		logger.Logger.Debugw("Using custom realtime audio output node")
		var nodeErr error
		audioCh, nodeErr = baseAgent.RealtimeAudioOutputNode(ctx, audioCh)
		if nodeErr != nil {
			logger.Logger.Errorw("Realtime audio output node failed", nodeErr)
		}
	}

	if audioCh != nil {
		var frameCount int
		for frame := range audioCh {
			if speech.IsInterrupted() {
				logger.Logger.Infow("Speech interrupted during audio playback", "frames_sent", frameCount)
				break
			}
			select {
			case <-ctx.Done():
				logger.Logger.Infow("Context cancelled during audio playback", "frames_sent", frameCount)
				return
			default:
				if session.Output.Audio != nil {
					_ = session.Output.Audio.CaptureFrame(frame)
					frameCount++
				}
			}
		}
		logger.Logger.Debugw("Audio channel drained", "frames_sent", frameCount)
	}

	if session.Output.Audio != nil {
		session.Output.Audio.Flush()
		if speech.IsInterrupted() {
			logger.Logger.Infow("Speech interrupted after audio flush, clearing buffer")
			session.Output.Audio.ClearBuffer()
		} else {
			logger.Logger.Debugw("Waiting for audio playout")
			_ = session.Output.Audio.WaitForPlayout(ctx)
			logger.Logger.Debugw("Audio playout complete")
		}
	}
	alignedWG.Wait()
}

