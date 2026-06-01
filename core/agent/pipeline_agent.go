package agent

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
)

type PipelineAgent struct {
	agent   *Agent
	vad     vad.VAD
	stt     stt.STT
	LLM     llm.LLM
	tts     tts.TTS
	chatCtx *llm.ChatContext

	audioInCh chan *model.AudioFrame
	mu        sync.Mutex
	session   *AgentSession

	ctx    context.Context
	cancel context.CancelFunc

	PublishAudio func(frame *model.AudioFrame) error
}

type pipelineReplyOptions struct {
	Instructions string
	ToolChoice   llm.ToolChoice
	Tools        []string
	ChatCtx      *llm.ChatContext
}

func NewPipelineAgent(
	vad vad.VAD,
	stt stt.STT,
	llmObj llm.LLM,
	tts tts.TTS,
	chatCtx *llm.ChatContext,
) *PipelineAgent {
	if chatCtx == nil {
		chatCtx = llm.NewChatContext()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &PipelineAgent{
		vad:       vad,
		stt:       stt,
		LLM:       llmObj,
		tts:       tts,
		chatCtx:   chatCtx,
		audioInCh: make(chan *model.AudioFrame, 100),
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

	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-va.audioInCh:
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
				va.emitError(err, va.vad)
			}
			return
		}

		if ev.Type == vad.VADEventStartOfSpeech {
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
				va.emitError(err, va.stt)
			}
			return
		}

		if ev.Type != stt.SpeechEventInterimTranscript && ev.Type != stt.SpeechEventFinalTranscript {
			continue
		}
		if len(ev.Alternatives) == 0 {
			continue
		}

		alternative := ev.Alternatives[0]
		va.mu.Lock()
		session := va.session
		va.mu.Unlock()
		if session != nil {
			session.EmitUserInputTranscribed(UserInputTranscribedEvent{
				Language:   alternative.Language,
				Transcript: alternative.Text,
				IsFinal:    ev.Type == stt.SpeechEventFinalTranscript,
				SpeakerID:  alternative.SpeakerID,
			})
		}

		if ev.Type == stt.SpeechEventFinalTranscript {
			transcript := alternative.Text
			logger.Logger.Infow("Final transcript", "text", transcript)

			msg := &llm.ChatMessage{
				Role: llm.ChatRoleUser,
				Content: []llm.ChatContent{
					{Text: transcript},
				},
			}
			va.chatCtx.Append(msg)
			if session != nil {
				session.EmitConversationItemAdded(msg)
			}

			go va.generateReply()
		}
	}
}

func (va *PipelineAgent) emitError(err error, source any) {
	if err == nil {
		return
	}
	va.mu.Lock()
	session := va.session
	va.mu.Unlock()
	if session == nil {
		return
	}
	session.EmitError(ErrorEvent{
		Error:  err,
		Source: source,
	})
}

func (va *PipelineAgent) generateReply() {
	va.generateReplyWithOptions(pipelineReplyOptions{})
}

func (va *PipelineAgent) generateReplyWithOptions(opts pipelineReplyOptions) {
	va.mu.Lock()
	session := va.session
	ctx := va.ctx
	va.mu.Unlock()

	if session == nil {
		return
	}

	logger.Logger.Infow("Generating reply")
	session.UpdateAgentState(AgentStateThinking)

	selectedTools, err := resolveToolsByID(session.Tools, opts.Tools)
	if err != nil {
		logger.Logger.Errorw("failed to resolve reply tools", err)
		session.EmitError(ErrorEvent{Error: err, Source: va})
		session.UpdateAgentState(AgentStateIdle)
		return
	}

	toolsInterface := make([]interface{}, len(selectedTools))
	for i, t := range selectedTools {
		toolsInterface[i] = t
	}
	toolCtx := llm.NewToolContext(toolsInterface)

	// In Python parity, we loop for tool calls
	toolSteps := 0
	for {
		inferenceCtx := va.chatCtx
		if opts.ChatCtx != nil {
			inferenceCtx = opts.ChatCtx.Copy()
		}
		if opts.Instructions != "" {
			if inferenceCtx == va.chatCtx {
				inferenceCtx = va.chatCtx.Copy()
			}
			inferenceCtx.Items = append([]llm.ChatItem{&llm.ChatMessage{
				Role:    llm.ChatRoleSystem,
				Content: []llm.ChatContent{{Text: opts.Instructions}},
			}}, inferenceCtx.Items...)
		}

		var chatOptions []llm.ChatOption
		if opts.ToolChoice != nil {
			chatOptions = append(chatOptions, llm.WithToolChoice(opts.ToolChoice))
		}
		if session.Options.MaxToolSteps > 0 && toolSteps >= session.Options.MaxToolSteps {
			chatOptions = append(chatOptions, llm.WithToolChoice("none"))
		}
		genData, err := PerformLLMInference(ctx, va.LLM, inferenceCtx, selectedTools, chatOptions...)
		if err != nil {
			logger.Logger.Errorw("LLM inference failed", err)
			session.UpdateAgentState(AgentStateIdle)
			return
		}

		// Start TTS in parallel with LLM text
		ttsGen, err := PerformTTSInference(ctx, va.tts, genData.TextCh)
		if err != nil {
			logger.Logger.Errorw("TTS inference failed", err)
		} else {
			session.UpdateAgentState(AgentStateSpeaking)
			for frame := range ttsGen.AudioCh {
				select {
				case <-ctx.Done():
					return
				default:
					if va.PublishAudio != nil {
						_ = va.PublishAudio(frame)
					}
				}
			}
		}

		if genData.GeneratedText != "" {
			args := llm.ChatMessageArgs{
				Role: llm.ChatRoleAssistant,
				Text: genData.GeneratedText,
			}
			if len(genData.GeneratedExtra) > 0 {
				args.Extra = genData.GeneratedExtra
			}
			msg := va.chatCtx.AddMessage(args)
			session.EmitConversationItemAdded(msg)
		}

		if len(genData.GeneratedFunctions) > 0 {
			session.UpdateAgentState(AgentStateThinking)
		}

		toolOutCh := PerformToolExecutions(ctx, genData.FunctionCh, toolCtx)

		// Wait for tool executions to complete and collect results
		var executedTools bool
		var functionCalls []*llm.FunctionCall
		var functionCallOutputs []*llm.FunctionCallOutput
		for toolOut := range toolOutCh {
			executedTools = true
			logger.Logger.Infow("Tool executed", "name", toolOut.FncCall.Name)

			fncCall := toolOut.FncCall
			functionCalls = append(functionCalls, &fncCall)
			functionCallOutputs = append(functionCallOutputs, toolOut.FncCallOut)
			va.chatCtx.Append(&fncCall)
			if toolOut.FncCallOut != nil {
				va.chatCtx.Append(toolOut.FncCallOut)
			}
		}

		if executedTools {
			if ev, err := NewFunctionToolsExecutedEvent(functionCalls, functionCallOutputs); err == nil {
				for _, out := range functionCallOutputs {
					if out != nil {
						ev.ReplyRequired = true
						break
					}
				}
				session.EmitFunctionToolsExecuted(*ev)
			}
		}

		// If no tool calls, we're done
		if !executedTools {
			session.UpdateAgentState(AgentStateIdle)
			break
		}
		toolSteps++
		// Loop back to LLM with tool outputs
	}
}

func resolveToolsByID(tools []llm.Tool, ids []string) ([]llm.Tool, error) {
	if len(ids) == 0 {
		return tools, nil
	}

	byID := make(map[string]llm.Tool, len(tools))
	available := make([]string, 0, len(tools))
	for _, tool := range tools {
		id := tool.ID()
		if id == "" {
			id = tool.Name()
		}
		byID[id] = tool
		available = append(available, id)
	}

	resolved := make([]llm.Tool, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		tool, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("tool %q not found in registered tools: %v", id, available)
		}
		resolved = append(resolved, tool)
		seen[id] = struct{}{}
	}
	return resolved, nil
}

func (va *PipelineAgent) OnAudioFrame(ctx context.Context, frame *model.AudioFrame) {
	select {
	case va.audioInCh <- frame:
	default:
	}
}
