package agent

import (
	"context"
	"fmt"
<<<<<<< HEAD
	"sync"
=======
	"io"
	"sync"
	"sync/atomic"
>>>>>>> origin/main
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
<<<<<<< HEAD
	"github.com/google/uuid"
=======
>>>>>>> origin/main
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

<<<<<<< HEAD
	ctx       context.Context
	cancel    context.CancelFunc
	runCancel context.CancelFunc
	runWG     sync.WaitGroup
=======
	ctx    context.Context
	cancel context.CancelFunc

	PublishAudio func(frame *model.AudioFrame) error

	greetingInProgress bool // suppress VAD interruptions during initial greeting
>>>>>>> origin/main
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
	fmt.Println("🔧 [Pipeline] PipelineAgent.run() started")
	logger.Logger.Infow("PipelineAgent started")
<<<<<<< HEAD
	// Audio forwarding is now handled by AgentSession
	<-ctx.Done()
=======

	vadStream, err := va.vad.Stream(ctx)
	if err != nil {
		fmt.Printf("❌ [Pipeline] Failed to start VAD stream: %v\n", err)
		logger.Logger.Errorw("failed to start VAD stream", err)
		return
	}
	defer vadStream.Close()
	fmt.Println("✅ [Pipeline] VAD stream started")

	sttStream, err := va.stt.Stream(ctx, "")
	if err != nil {
		fmt.Printf("❌ [Pipeline] Failed to start STT stream: %v\n", err)
		logger.Logger.Errorw("failed to start STT stream", err)
		return
	}
	defer sttStream.Close()
	fmt.Println("✅ [Pipeline] STT stream started")

	go va.vadLoop(vadStream)
	go va.sttLoop(sttStream)

	// Send initial greeting — runs synchronously in a goroutine so the
	// audio processing loop can start immediately. During the greeting,
	// VAD interruptions are suppressed to prevent silence/noise from
	// cancelling the greeting before audio is published.
	go func() {
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return // session ended before greeting
		}
		fmt.Println("👋 [Pipeline] Sending initial greeting (interruptions suppressed)...")

		// Suppress VAD interruptions during greeting
		va.mu.Lock()
		va.greetingInProgress = true
		va.mu.Unlock()

		va.chatCtx.Append(&llm.ChatMessage{
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: "Halo, perkenalkan dirimu secara singkat."},
			},
		})
		va.generateReply()

		va.mu.Lock()
		va.greetingInProgress = false
		va.mu.Unlock()
		fmt.Println("👋 [Pipeline] Greeting complete, interruptions enabled")
	}()

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
			_ = vadStream.PushFrame(frame)
			_ = sttStream.PushFrame(frame)
		}
	}
>>>>>>> origin/main
}

func (va *PipelineAgent) GenerateReply(speech *SpeechHandle) {
	defer speech.MarkDone()

<<<<<<< HEAD
=======
		if ev.Type == vad.VADEventStartOfSpeech {
			fmt.Println("🗣️  [VAD] User started speaking")
			logger.Logger.Infow("User started speaking")
			va.session.UpdateUserState(UserStateSpeaking)

			// Interrupt ongoing agent speech/generation — but NOT during greeting
			va.mu.Lock()
			if va.greetingInProgress {
				fmt.Println("🛡️ [VAD] Interrupt suppressed — greeting in progress")
				va.mu.Unlock()
			} else if va.cancel != nil {
				va.cancel()
				ctx, cancel := context.WithCancel(context.Background())
				va.ctx = ctx
				va.cancel = cancel
				va.mu.Unlock()
			} else {
				va.mu.Unlock()
			}
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

			// Publish user transcript to Playground
			va.mu.Lock()
			sess := va.session
			va.mu.Unlock()
			if sess != nil {
				go sess.PublishUserTranscript(transcript)
			}

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
>>>>>>> origin/main
	va.mu.Lock()
	session := va.session
	ctx := va.ctx
	va.mu.Unlock()

	if session == nil {
		logger.Logger.Infow("GenerateReply called but session is nil, skipping")
		return
	}

<<<<<<< HEAD
	logger.Logger.Infow("Generating reply",
		"tools_count", len(session.Tools),
		"has_audio_output", session.Output.Audio != nil,
		"has_transcription", session.Output.Transcription != nil,
		"use_tts_aligned_transcript", session.Options.UseTTSAlignedTranscript,
		"max_tool_steps", session.Options.MaxToolSteps,
	)
=======
	fmt.Println("🤖 [LLM] Generating reply...")
	logger.Logger.Infow("Generating reply")
>>>>>>> origin/main
	session.UpdateAgentState(AgentStateThinking)

	toolsInterface := make([]interface{}, len(session.Tools))
	for i, t := range session.Tools {
		toolsInterface[i] = t
	}
	toolCtx := llm.NewToolContext(toolsInterface)

<<<<<<< HEAD
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
=======
	// In Python parity, we loop for tool calls
	for {
		fmt.Println("📤 [LLM] Calling PerformLLMInference...")
		genData, err := PerformLLMInference(ctx, va.LLM, va.chatCtx, session.Tools)
		if err != nil {
			fmt.Printf("❌ [LLM] Inference failed: %v\n", err)
			logger.Logger.Errorw("LLM inference failed", err)
			session.UpdateAgentState(AgentStateIdle)
>>>>>>> origin/main
			return
		}
		fmt.Println("✅ [LLM] Inference started, waiting for text...")

		// Handle Playback and Transcription (Same as default loop but simplified)
		va.handlePlaybackAndTranscription(ctx, speech, ttsGen)
		return
	}

<<<<<<< HEAD
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
=======
		// Start TTS with the original text channel — no tee, no extra goroutines.
		fmt.Println("🔊 [TTS] Starting TTS inference...")
		ttsGen, err := PerformTTSInference(ctx, va.tts, genData.TextCh)
		if err != nil {
			fmt.Printf("❌ [TTS] Inference failed: %v\n", err)
			logger.Logger.Errorw("TTS inference failed", err)
			// Drain TextCh so the LLM goroutine can finish and close FunctionCh.
			// Without this, LLM goroutine blocks on TextCh → FunctionCh never closes
			// → toolOutCh never closes → this goroutine deadlocks below.
			go func() { for range genData.TextCh {} }()
		} else {
			fmt.Println("✅ [TTS] TTS started, publishing audio...")
			session.UpdateAgentState(AgentStateSpeaking)
			frameCount := 0
			interrupted := false
		playoutLoop:
			for frame := range ttsGen.AudioCh {
				select {
				case <-ctx.Done():
					fmt.Println("⚠️ [TTS] Context cancelled during playout")
					interrupted = true
					// Drain remaining audio so the TTS goroutine can exit and
					// close the ElevenLabs WebSocket stream. Without draining,
					// the goroutine blocks on AudioCh → stream never closes →
					// ElevenLabs keeps the connection open → next call may fail.
					go func() { for range ttsGen.AudioCh {} }()
					break playoutLoop
				default:
					if va.PublishAudio != nil {
						_ = va.PublishAudio(frame)
						frameCount++
					}
				}
			}
			fmt.Printf("✅ [TTS] Published %d audio frames\n", frameCount)

			if interrupted {
				// Also drain FullTextCh so LLM goroutine is not left blocked.
				go func() { for range genData.FullTextCh {} }()
				return
			}

			// Normal completion: LLM goroutine is done → FullTextCh is ready.
			select {
			case agentText := <-genData.FullTextCh:
				if agentText != "" {
					go session.PublishAgentTranscript(agentText)
				}
			case <-ctx.Done():
				return
			}
>>>>>>> origin/main
		}

		// Wait for tool executions to complete and collect results.
		// Use select so a VAD interruption can cancel this wait.
		var executedTools bool
<<<<<<< HEAD
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
=======
	toolLoop:
		for {
			select {
			case toolOut, ok := <-toolOutCh:
				if !ok {
					break toolLoop
				}
				executedTools = true
				fmt.Printf("🔧 [Tool] Executed: %s\n", toolOut.FncCall.Name)
				logger.Logger.Infow("Tool executed", "name", toolOut.FncCall.Name)
				va.chatCtx.Append(&toolOut.FncCall)
				if toolOut.FncCallOut != nil {
					va.chatCtx.Append(toolOut.FncCallOut)
				}
			case <-ctx.Done():
				return
			}
		}

		// If no tool calls, we're done
		if !executedTools {
			fmt.Println("✅ [LLM] Reply complete, returning to idle")
			session.UpdateAgentState(AgentStateIdle)
>>>>>>> origin/main
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

