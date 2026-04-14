package agent

import (
	"context"
	"io"
	"sync"

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

	mu        sync.Mutex
	session   *AgentSession

	ctx    context.Context
	cancel context.CancelFunc
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
		vad:       vad,
		stt:       sttInstance,
		LLM:       llmObj,
		tts:       ttsInstance,
		chatCtx:   chatCtx,
		ctx:       ctx,
		cancel:    cancel,
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
	logger.Logger.Infow("PipelineAgent started")

	vadStream, err := va.vad.Stream(ctx)
	if err != nil {
		logger.Logger.Errorw("failed to start VAD stream", err)
		return
	}

	sttStream, err := va.stt.Stream(ctx, "")
	if err != nil {
		logger.Logger.Errorw("failed to start STT stream", err)
		return
	}

	go va.vadLoop(vadStream)
	go va.sttLoop(sttStream)

	var audioStream <-chan *model.AudioFrame
	if va.session != nil && va.session.Input.Audio != nil {
		audioStream = va.session.Input.Audio.Stream()
	} else {
		logger.Logger.Warnw("AgentInput Audio not configured for pipeline agent", nil)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-audioStream:
			if !ok {
				return
			}
			if vadStream != nil {
				_ = vadStream.PushFrame(frame)
			}
			if sttStream != nil {
				_ = sttStream.PushFrame(frame)
			}
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
			logger.Logger.Infow("User started speaking")
			va.session.UpdateUserState(UserStateSpeaking)
			
			if va.session.Activity != nil {
				va.session.Activity.OnStartOfSpeech(ev)
			}
			
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
			logger.Logger.Infow("User stopped speaking")
			va.session.UpdateUserState(UserStateListening)
			
			if va.session.Activity != nil {
				va.session.Activity.OnEndOfSpeech(ev)
			}
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
			logger.Logger.Infow("Final transcript", "text", transcript)

			if va.session.Activity != nil {
				va.session.Activity.OnFinalTranscript(ev)
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
	session.UpdateAgentState(AgentStateThinking)

	toolsInterface := make([]interface{}, len(session.Tools))
	for i, t := range session.Tools {
		toolsInterface[i] = t
	}
	toolCtx := llm.NewToolContext(toolsInterface)

	// In Python parity, we loop for tool calls
	for {
		if speech.IsInterrupted() {
			session.UpdateAgentState(AgentStateIdle)
			return
		}

		genData, err := PerformLLMInference(ctx, va.LLM, va.chatCtx, session.Tools)
		if err != nil {
			logger.Logger.Errorw("LLM inference failed", err)
			session.UpdateAgentState(AgentStateIdle)
			return
		}

		toolOutCh := PerformToolExecutions(ctx, genData.FunctionCh, toolCtx)

		// Start TTS in parallel with LLM text
		ttsGen, err := PerformTTSInference(ctx, va.tts, genData.TextCh)
		if err != nil {
			logger.Logger.Errorw("TTS inference failed", err)
		} else {
			session.UpdateAgentState(AgentStateSpeaking)
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
		}

		// Wait for tool executions to complete and collect results
		var executedTools bool
		for toolOut := range toolOutCh {
			executedTools = true
			logger.Logger.Infow("Tool executed", "name", toolOut.FncCall.Name)
			
			va.chatCtx.Append(&toolOut.FncCall)
			if toolOut.FncCallOut != nil {
				va.chatCtx.Append(toolOut.FncCallOut)
			}
		}

		// If no tool calls, we're done
		if !executedTools {
			session.UpdateAgentState(AgentStateIdle)
			break
		}
		// Loop back to LLM with tool outputs
	}
}
