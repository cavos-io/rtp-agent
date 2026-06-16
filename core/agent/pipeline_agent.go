package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

// AudioInputHook is called for each incoming audio frame before it is forwarded
// to VAD and STT processing. Returning nil drops the frame from both pipelines.
type AudioInputHook func(ctx context.Context, frame *model.AudioFrame) *model.AudioFrame

type PipelineAgent struct {
	vad     vad.VAD
	stt     stt.STT
	LLM     llm.LLM
	tts     tts.TTS
	chatCtx *llm.ChatContext

	ttsStreamPacer *tts.SentenceStreamPacerOptions

	audioInCh      chan *model.AudioFrame
	audioInputHook atomic.Pointer[AudioInputHook]
	mu             sync.Mutex
	session        *AgentSession

	ctx    context.Context
	cancel context.CancelFunc

	PublishAudio func(ctx context.Context, frame *model.AudioFrame) error
}

type pipelineReplyOptions struct {
	Instructions       string
	ToolChoice         llm.ToolChoice
	Tools              []string
	IgnoreOnEnterTools bool
	ChatCtx            *llm.ChatContext
	UserMessage        *llm.ChatMessage
	SpeechHandle       *SpeechHandle
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

func (va *PipelineAgent) SetPublishAudio(publish func(ctx context.Context, frame *model.AudioFrame) error) {
	va.mu.Lock()
	defer va.mu.Unlock()
	va.PublishAudio = publish
}

// SetAudioInputHook registers a hook that intercepts incoming audio before VAD
// and STT. Passing nil clears the hook.
func (va *PipelineAgent) SetAudioInputHook(hook AudioInputHook) {
	if hook == nil {
		va.audioInputHook.Store(nil)
		return
	}
	va.audioInputHook.Store(&hook)
}

func (va *PipelineAgent) UpdateComponents(vadObj vad.VAD, sttObj stt.STT, llmObj llm.LLM, ttsObj tts.TTS) {
	va.mu.Lock()
	defer va.mu.Unlock()
	va.vad = vadObj
	va.stt = sttObj
	va.LLM = llmObj
	va.tts = ttsObj
}

func (va *PipelineAgent) run(ctx context.Context) {
	logger.Logger.Infow("PipelineAgent started")

	vadStream, err := va.vad.Stream(ctx)
	if err != nil {
		logger.Logger.Errorw("failed to start VAD stream", err)
		va.emitError(err, va.vad)
		return
	}

	sttStream, err := va.stt.Stream(ctx, "")
	if err != nil {
		logger.Logger.Errorw("failed to start STT stream", err)
		label := "stt"
		if va.stt != nil {
			label = va.stt.Label()
		}
		va.emitError(stt.NewSTTError(label, err, false), va.stt)
		return
	}

	go va.vadLoop(vadStream)
	go va.sttLoop(sttStream)

	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-va.audioInCh:
			if hookPtr := va.audioInputHook.Load(); hookPtr != nil {
				frame = (*hookPtr)(ctx, frame)
			}
			if frame == nil {
				continue
			}
			if err := vadStream.PushFrame(frame); err != nil {
				if !isSpeechStreamShutdownError(err) {
					logger.Logger.Errorw("VAD push frame failed", err)
					va.emitError(err, va.vad)
				}
			}
			sttFrame := frame
			if va.session != nil && va.session.shouldSilenceInputAudio() {
				sttFrame = audio.SilenceFrameLike(frame)
			}
			if err := sttStream.PushFrame(sttFrame); err != nil {
				if !isSpeechStreamShutdownError(err) {
					logger.Logger.Errorw("STT push frame failed", err)
					label := "stt"
					if va.stt != nil {
						label = va.stt.Label()
					}
					va.emitError(stt.NewSTTError(label, err, false), va.stt)
				}
			}
		}
	}
}

func (va *PipelineAgent) vadLoop(stream vad.VADStream) {
	for {
		ev, err := stream.Next()
		if err != nil {
			if !isSpeechStreamShutdownError(err) {
				logger.Logger.Errorw("VAD stream error", err)
				va.emitError(err, va.vad)
			}
			return
		}

		if ev.Type == vad.VADEventStartOfSpeech {
			logger.Logger.Infow("User started speaking")
			if va.session != nil && va.session.activity != nil {
				va.session.activity.OnStartOfSpeech(ev)
			} else if va.session != nil {
				va.session.UpdateUserState(UserStateSpeaking)
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
			if va.session != nil && va.session.activity != nil {
				va.session.activity.OnEndOfSpeech(ev)
			} else if va.session != nil {
				va.session.UpdateUserState(UserStateListening)
			}
		} else if ev.Type == vad.VADEventInferenceDone {
			if va.session != nil && va.session.activity != nil {
				va.session.activity.OnVADInferenceDone(ev)
			}
		}
	}
}

func (va *PipelineAgent) sttLoop(stream stt.RecognizeStream) {
	for {
		ev, err := stream.Next()
		if err != nil {
			if !isSpeechStreamShutdownError(err) {
				logger.Logger.Errorw("STT stream error", err)
				label := "stt"
				if va.stt != nil {
					label = va.stt.Label()
				}
				va.emitError(stt.NewSTTError(label, err, false), va.stt)
			}
			return
		}

		if ev.Type == stt.SpeechEventRecognitionUsage {
			va.emitSTTMetrics(ev)
			continue
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
		ctx := va.ctx
		va.mu.Unlock()
		if session != nil && session.activity != nil {
			if ev.Type == stt.SpeechEventInterimTranscript {
				session.activity.OnInterimTranscript(ev)
				continue
			}
			session.activity.OnFinalTranscript(ev)
			if session.activity.turnDetectionMode() != TurnDetectionModeSTT {
				if ctx == nil {
					ctx = context.Background()
				}
				if _, err := session.activity.CommitUserTurn(ctx, CommitUserTurnOptions{}); err != nil {
					va.emitError(err, va)
				}
			}
			continue
		}
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

func isSpeechStreamShutdownError(err error) bool {
	return err == io.EOF || errors.Is(err, context.Canceled)
}

func (va *PipelineAgent) emitSTTMetrics(ev *stt.SpeechEvent) {
	if va == nil || ev == nil || ev.RecognitionUsage == nil {
		return
	}
	metrics := &telemetry.STTMetrics{
		Label:         "stt",
		RequestID:     ev.RequestID,
		Timestamp:     time.Now(),
		AudioDuration: ev.RecognitionUsage.AudioDuration,
		InputTokens:   ev.RecognitionUsage.InputTokens,
		OutputTokens:  ev.RecognitionUsage.OutputTokens,
		Streamed:      true,
		Metadata: &telemetry.Metadata{
			ModelName:     stt.Model(va.stt),
			ModelProvider: stt.Provider(va.stt),
		},
	}
	if va.stt != nil {
		metrics.Label = va.stt.Label()
	}
	va.mu.Lock()
	session := va.session
	va.mu.Unlock()
	if session == nil {
		return
	}
	if session.activity != nil {
		session.activity.OnMetricsCollected(metrics)
		return
	}
	session.EmitMetricsCollected(metrics)
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

func (va *PipelineAgent) OnSpeechScheduled(ctx context.Context, speech *SpeechHandle) {
	if speech == nil {
		return
	}
	defer speech.MarkDone()
	speech.AuthorizeGeneration()

	if speech.Generation.Text != "" {
		va.mu.Lock()
		session := va.session
		va.mu.Unlock()
		if session == nil {
			return
		}
		if _, err := va.synthesizeSpeech(ctx, session, singleTextChannel(speech.Generation.Text)); err != nil {
			logger.Logger.Errorw("TTS inference failed", err)
			va.emitTTSError(session, err)
		}
		insertChatItemIfMissing(va.chatCtx, speech.Generation.AssistantMessage)
		_ = speech.MarkGenerationDone()
		session.UpdateAgentState(AgentStateIdle)
		return
	}

	options := pipelineReplyOptions{
		Tools:              append([]string(nil), speech.Generation.Tools...),
		IgnoreOnEnterTools: speech.Generation.IgnoreOnEnterTools,
		ChatCtx:            speech.Generation.ChatCtx,
		UserMessage:        speech.Generation.UserMessage,
		SpeechHandle:       speech,
	}
	if speech.Generation.Instructions != nil {
		options.Instructions = speech.Generation.Instructions.AsModality(speech.InputDetails.Modality).String()
	}
	if speech.Generation.ToolChoice != nil {
		options.ToolChoice = speech.Generation.ToolChoice
	}
	va.generateReplyWithOptions(options)
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

	registeredTools, err := sessionRegisteredTools(ctx, session)
	if err != nil {
		logger.Logger.Errorw("failed to register reply tools", err)
		session.EmitError(ErrorEvent{Error: err, Source: va})
		session.UpdateAgentState(AgentStateIdle)
		return
	}
	selectedTools, err := resolveToolsByID(registeredTools, opts.Tools)
	if err != nil {
		logger.Logger.Errorw("failed to resolve reply tools", err)
		session.EmitError(ErrorEvent{Error: err, Source: va})
		session.UpdateAgentState(AgentStateIdle)
		return
	}
	if opts.IgnoreOnEnterTools {
		selectedTools = filterOnEnterIgnoredTools(selectedTools)
	}

	toolsInterface := make([]interface{}, len(selectedTools))
	for i, t := range selectedTools {
		toolsInterface[i] = t
	}
	toolCtx := llm.NewToolContext(toolsInterface)

	replyCtx := va.chatCtx
	if opts.ChatCtx != nil {
		replyCtx = opts.ChatCtx.Copy()
	}
	if opts.UserMessage != nil {
		insertChatItemIfMissing(va.chatCtx, opts.UserMessage)
		if replyCtx == va.chatCtx {
			replyCtx = va.chatCtx.Copy()
		} else {
			insertChatItemIfMissing(replyCtx, opts.UserMessage)
		}
	}

	// In Python parity, we loop for tool calls
	toolSteps := 0
	for {
		inferenceCtx := replyCtx
		inputModality := ""
		if opts.SpeechHandle != nil {
			inputModality = opts.SpeechHandle.InputDetails.Modality
		}
		if opts.Instructions != "" {
			if inferenceCtx == replyCtx {
				inferenceCtx = replyCtx.Copy()
			}
			inferenceCtx.Items = append([]llm.ChatItem{&llm.ChatMessage{
				Role:    llm.ChatRoleSystem,
				Content: []llm.ChatContent{{Text: opts.Instructions}},
			}}, inferenceCtx.Items...)
		}
		if inputModality != "" {
			if inferenceCtx == replyCtx {
				inferenceCtx = replyCtx.Copy()
			}
			applyAgentInstructionsModality(inferenceCtx, inputModality)
		}

		var chatOptions []llm.ChatOption
		var activeToolChoice llm.ToolChoice
		if opts.ToolChoice != nil {
			activeToolChoice = opts.ToolChoice
			chatOptions = append(chatOptions, llm.WithToolChoice(opts.ToolChoice))
		}
		if toolSteps > session.Options.MaxToolSteps {
			activeToolChoice = "none"
			chatOptions = append(chatOptions, llm.WithToolChoice("none"))
		}
		if session.Options.LLMParallelToolCalls != nil {
			chatOptions = append(chatOptions, llm.WithParallelToolCalls(*session.Options.LLMParallelToolCalls))
		}
		if len(session.Options.LLMExtraParams) > 0 {
			chatOptions = append(chatOptions, llm.WithExtraParams(session.Options.LLMExtraParams))
		}
		if len(session.Options.LLMResponseFormat) > 0 {
			chatOptions = append(chatOptions, llm.WithResponseFormat(session.Options.LLMResponseFormat))
		}
		genData, err := PerformLLMInference(ctx, va.LLM, inferenceCtx, selectedTools, chatOptions...)
		if err != nil {
			logger.Logger.Errorw("LLM inference failed", err)
			va.emitLLMError(session, err)
			session.UpdateAgentState(AgentStateIdle)
			return
		}

		ttsGen, err := va.synthesizeSpeech(ctx, session, genData.TextCh)
		if err != nil {
			logger.Logger.Errorw("TTS inference failed", err)
			va.emitTTSError(session, err)
		}
		if genData.StreamErr != nil {
			logger.Logger.Errorw("LLM stream failed", genData.StreamErr)
			va.emitLLMError(session, genData.StreamErr)
			session.UpdateAgentState(AgentStateIdle)
			return
		}
		va.emitLLMMetrics(session, genData)

		if genData.GeneratedText != "" {
			args := llm.ChatMessageArgs{
				ID:   genData.ID,
				Role: llm.ChatRoleAssistant,
				Text: genData.GeneratedText,
			}
			if len(genData.GeneratedExtra) > 0 {
				args.Extra = genData.GeneratedExtra
			}
			metrics := map[string]any{
				"llm_metadata": map[string]any{
					"model_name":     llm.Model(va.LLM),
					"model_provider": llm.Provider(va.LLM),
				},
				"tts_metadata": map[string]any{
					"model_name":     tts.Model(va.tts),
					"model_provider": tts.Provider(va.tts),
				},
			}
			if genData.TTFT > 0 {
				metrics["llm_node_ttft"] = genData.TTFT.Seconds()
			}
			if ttsGen != nil && ttsGen.TTFB > 0 {
				metrics["tts_node_ttfb"] = ttsGen.TTFB.Seconds()
			}
			args.Metrics = metrics
			msg := va.chatCtx.AddMessage(args)
			session.EmitConversationItemAdded(msg)
			if opts.SpeechHandle != nil {
				opts.SpeechHandle.AddChatItems(msg)
			}
		}

		if opts.SpeechHandle != nil {
			_ = opts.SpeechHandle.MarkGenerationDone()
		}

		if len(genData.GeneratedFunctions) > 0 {
			session.UpdateAgentState(AgentStateThinking)
		}

		toolOutCh := PerformToolExecutions(ctx, genData.FunctionCh, toolCtx,
			WithToolExecutionSession(session),
			WithToolExecutionSpeechHandle(opts.SpeechHandle),
			WithToolExecutionToolChoice(activeToolChoice),
		)

		// Wait for tool executions to complete and collect results
		var executedTools bool
		var replyRequired bool
		var functionCalls []*llm.FunctionCall
		var functionCallOutputs []*llm.FunctionCallOutput
		for toolOut := range toolOutCh {
			executedTools = true
			logger.Logger.Infow("Tool executed", "name", toolOut.FncCall.Name)

			fncCall := toolOut.FncCall
			functionCalls = append(functionCalls, &fncCall)
			functionCallOutputs = append(functionCallOutputs, toolOut.FncCallOut)
			if toolOut.FncCallOut != nil {
				replyRequired = true
				va.chatCtx.Append(&fncCall)
				va.chatCtx.Append(toolOut.FncCallOut)
				if replyCtx != va.chatCtx {
					replyCtx.Append(&fncCall)
					replyCtx.Append(toolOut.FncCallOut)
				}
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
			if activeAgent := session.Agent.GetAgent(); activeAgent != nil {
				if err := updateAgentInstructionsMessage(replyCtx, agentInstructionVariants(activeAgent), false); err != nil {
					logger.Logger.Warnw("failed to refresh reply instructions", err)
				}
			}
		}

		// If no tool calls, we're done
		if !executedTools {
			session.UpdateAgentState(AgentStateIdle)
			break
		}
		if !replyRequired {
			session.UpdateAgentState(AgentStateIdle)
			break
		}
		if opts.SpeechHandle != nil {
			opts.SpeechHandle.IncrementStep()
			opts.SpeechHandle.AuthorizeGeneration()
		}
		toolSteps++
		// Loop back to LLM with tool outputs
	}
}

func (va *PipelineAgent) emitLLMMetrics(session *AgentSession, genData *LLMGenerationData) {
	if session == nil || genData == nil || genData.Usage == nil {
		return
	}
	metrics := &telemetry.LLMMetrics{
		Label:              llm.Label(va.LLM),
		RequestID:          genData.RequestID,
		Timestamp:          time.Now(),
		Duration:           genData.Duration.Seconds(),
		TTFT:               genData.TTFT.Seconds(),
		CompletionTokens:   genData.Usage.CompletionTokens,
		PromptTokens:       genData.Usage.PromptTokens,
		PromptCachedTokens: llmCachedPromptTokens(genData.Usage),
		TotalTokens:        genData.Usage.TotalTokens,
		Metadata: &telemetry.Metadata{
			ModelName:     llm.Model(va.LLM),
			ModelProvider: llm.Provider(va.LLM),
		},
	}
	if metrics.Duration > 0 {
		metrics.TokensPerSecond = float64(metrics.CompletionTokens) / metrics.Duration
	}
	if session.activity != nil {
		session.activity.OnMetricsCollected(metrics)
		return
	}
	session.EmitMetricsCollected(metrics)
}

func (va *PipelineAgent) emitLLMError(session *AgentSession, err error) {
	if session == nil || err == nil {
		return
	}
	session.EmitError(ErrorEvent{
		Error:  llm.NewLLMError(llm.Label(va.LLM), err, false),
		Source: va.LLM,
	})
}

func (va *PipelineAgent) emitTTSError(session *AgentSession, err error) {
	if session == nil || err == nil {
		return
	}
	label := "tts"
	if va.tts != nil {
		label = va.tts.Label()
	}
	ttsErr := tts.TTSError{Label: label, Err: err, Recoverable: false}
	if session.activity != nil {
		session.activity.OnError(ttsErr, va.tts)
		return
	}
	session.EmitError(ErrorEvent{Error: ttsErr, Source: va.tts})
}

func llmCachedPromptTokens(usage *llm.CompletionUsage) int {
	if usage == nil {
		return 0
	}
	return usage.PromptCachedTokens + usage.CacheCreationTokens + usage.CacheReadTokens
}

func (va *PipelineAgent) synthesizeSpeech(ctx context.Context, session *AgentSession, textCh <-chan string) (*TTSGenerationData, error) {
	transcriptSync := NewTranscriptSynchronizer(0)
	useAlignedTranscript := va.useTTSAlignedTranscript(session)
	var transcriptionDone <-chan struct{}
	ttsTextCh := textCh
	if useAlignedTranscript {
		transcriptionDone = closedChannel()
	} else {
		transcriptionDone = va.forwardAgentOutputTranscription(session, transcriptSync)
		ttsTextCh = va.forwardTextToTranscriptSynchronizer(ctx, textCh, transcriptSync)
	}
	ttsGen, err := PerformTTSInference(ctx, va.tts, ttsTextCh, va.ttsInferenceOptions(session)...)
	if err != nil {
		transcriptSync.Close()
		<-transcriptionDone
		return nil, err
	}
	if useAlignedTranscript {
		transcriptionDone = va.forwardAlignedAgentOutputTranscription(session, ttsGen.TimedTextCh)
	}

	session.UpdateAgentState(AgentStateSpeaking)
	for frame := range ttsGen.AudioCh {
		select {
		case <-ctx.Done():
			transcriptSync.Close()
			<-transcriptionDone
			return ttsGen, ctx.Err()
		default:
			transcriptSync.PushAudio(frame)
			if va.PublishAudio != nil {
				if err := va.PublishAudio(ctx, frame); err != nil {
					transcriptSync.Close()
					<-transcriptionDone
					return ttsGen, err
				}
			}
		}
	}
	transcriptSync.Close()
	<-transcriptionDone
	if ttsGen.StreamErr != nil {
		return ttsGen, ttsGen.StreamErr
	}
	return ttsGen, nil
}

func (va *PipelineAgent) useTTSAlignedTranscript(session *AgentSession) bool {
	if session == nil || va.tts == nil {
		return false
	}
	enabled := false
	if session.activity != nil {
		enabled = session.activity.UseTTSAlignedTranscript()
	} else {
		enabled = session.Options.UseTTSAlignedTranscript
	}
	if session.activity == nil && session.Agent != nil {
		if agent := session.Agent.GetAgent(); agent != nil {
			if agent.UseTTSAlignedTranscriptSet || agent.UseTTSAlignedTranscript {
				enabled = agent.UseTTSAlignedTranscript
			}
		}
	}
	if !enabled {
		return false
	}
	capabilities := va.tts.Capabilities()
	return capabilities.AlignedTranscript || !capabilities.Streaming
}

func (va *PipelineAgent) ttsInferenceOptions(session *AgentSession) []TTSInferenceOption {
	var opts []TTSInferenceOption
	if va.ttsStreamPacer != nil {
		opts = append(opts, WithTTSStreamPacer(*va.ttsStreamPacer))
	}
	if session != nil && len(session.Options.TTSTextReplacements) > 0 {
		opts = append(opts, WithTTSTextReplacements(session.Options.TTSTextReplacements))
	}
	if session != nil && session.Options.DisableTTSTextTransforms {
		opts = append(opts, WithTTSTextTransformsDisabled())
	} else if session != nil && session.Options.TTSTextTransformsSet {
		opts = append(opts, WithTTSTextTransforms(session.Options.TTSTextTransforms))
	}
	if va.useTTSAlignedTranscript(session) {
		opts = append(opts, WithTTSPreserveTimedTranscript())
	}
	return opts
}

func singleTextChannel(text string) <-chan string {
	ch := make(chan string, 1)
	ch <- text
	close(ch)
	return ch
}

func (va *PipelineAgent) forwardTextToTranscriptSynchronizer(ctx context.Context, textCh <-chan string, syncer *TranscriptSynchronizer) <-chan string {
	out := make(chan string, 100)
	go func() {
		defer close(out)
		for text := range textCh {
			syncer.PushText(text)
			select {
			case out <- text:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (va *PipelineAgent) forwardAgentOutputTranscription(session *AgentSession, syncer *TranscriptSynchronizer) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var transcript strings.Builder
		for text := range syncer.EventCh() {
			if text == "" {
				continue
			}
			transcript.WriteString(text)
			session.EmitAgentOutputTranscribed(AgentOutputTranscribedEvent{
				Transcript: text,
			})
		}
		if transcript.Len() > 0 {
			session.EmitAgentOutputTranscribed(AgentOutputTranscribedEvent{
				Transcript: strings.TrimSpace(transcript.String()),
				IsFinal:    true,
			})
		}
	}()
	return done
}

func (va *PipelineAgent) forwardAlignedAgentOutputTranscription(session *AgentSession, timedTextCh <-chan tts.TimedString) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var transcript strings.Builder
		for timedText := range timedTextCh {
			if timedText.Text == "" {
				continue
			}
			transcript.WriteString(timedText.Text)
			session.EmitAgentOutputTranscribed(AgentOutputTranscribedEvent{
				Transcript: timedText.Text,
			})
		}
		if transcript.Len() > 0 {
			session.EmitAgentOutputTranscribed(AgentOutputTranscribedEvent{
				Transcript: strings.TrimSpace(transcript.String()),
				IsFinal:    true,
			})
		}
	}()
	return done
}

func closedChannel() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func sessionRegisteredTools(ctx context.Context, session *AgentSession) ([]llm.Tool, error) {
	if session == nil {
		return nil, nil
	}
	tools := make([]llm.Tool, 0, len(session.Tools))
	for idx, tool := range session.Tools {
		if isNilAgentTool(tool) {
			return nil, fmt.Errorf("session tool at index %d: nil tool", idx)
		}
		tools = append(tools, tool)
	}
	if session.Agent != nil && session.Agent.GetAgent() != nil {
		for idx, tool := range session.Agent.GetAgent().Tools {
			if isNilAgentTool(tool) {
				return nil, fmt.Errorf("agent tool at index %d: nil tool", idx)
			}
			tools = append(tools, tool)
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for idx, server := range session.MCPServers() {
		if server == nil {
			continue
		}
		toolset := llm.NewMCPToolset(fmt.Sprintf("mcp_toolset_%d", idx), server)
		toolsetID := toolset.ID()
		if _, err := toolset.Setup(ctx, false); err != nil {
			return nil, fmt.Errorf("setup MCP toolset %q: %w", toolsetID, err)
		}
		toolset.FilterTools(func(tool llm.Tool) bool { return tool != nil })
		tools = append(tools, toolset.Tools()...)
	}
	return appendToolExecutorHelperTools(tools), nil
}

func insertChatItemIfMissing(chatCtx *llm.ChatContext, item llm.ChatItem) {
	if chatCtx == nil || item == nil {
		return
	}
	for _, existing := range chatCtx.Items {
		if existing == item {
			return
		}
		if item.GetID() != "" && existing.GetID() == item.GetID() && existing.GetType() == item.GetType() {
			return
		}
	}
	chatCtx.Insert(item)
}

func resolveToolsByID(tools []llm.Tool, ids []string) ([]llm.Tool, error) {
	toolItems := make([]interface{}, 0, len(tools))
	for _, tool := range tools {
		toolItems = append(toolItems, tool)
	}
	toolCtx := llm.EmptyToolContext()
	if err := toolCtx.UpdateTools(toolItems); err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		return toolCtx.Flatten(), nil
	}

	byID := make(map[string]llm.Tool, len(tools))
	available := make([]string, 0, len(tools))
	for _, tool := range toolCtx.FunctionTools() {
		id := tool.ID()
		if id == "" {
			id = tool.Name()
		}
		byID[id] = tool
		available = append(available, id)
	}
	for _, tool := range toolCtx.ProviderTools() {
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
			sort.Strings(available)
			return nil, fmt.Errorf("tool '%s' not found in agent's registered tools. Available tools: %s", id, formatPythonStringList(available))
		}
		resolved = append(resolved, tool)
		seen[id] = struct{}{}
	}
	return resolved, nil
}

func filterOnEnterIgnoredTools(tools []llm.Tool) []llm.Tool {
	if len(tools) == 0 {
		return nil
	}
	filtered := tools[:0]
	for _, tool := range tools {
		if llm.ToolHasFlag(tool, llm.ToolFlagIgnoreOnEnter) {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func formatPythonStringList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = formatPythonStringRepr(value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func formatPythonStringRepr(value string) string {
	quote := "'"
	if strings.Contains(value, "'") && !strings.Contains(value, `"`) {
		quote = `"`
	}
	var escaped strings.Builder
	for _, r := range value {
		switch {
		case r == '\\':
			escaped.WriteString(`\\`)
		case r == '\n':
			escaped.WriteString(`\n`)
		case r == '\r':
			escaped.WriteString(`\r`)
		case r == '\t':
			escaped.WriteString(`\t`)
		case r < 0x20:
			escaped.WriteString(fmt.Sprintf(`\x%02x`, r))
		case r < 0x100 && !unicode.IsPrint(r):
			escaped.WriteString(fmt.Sprintf(`\x%02x`, r))
		case r < 0x10000 && !unicode.IsPrint(r):
			escaped.WriteString(fmt.Sprintf(`\u%04x`, r))
		case !unicode.IsPrint(r):
			escaped.WriteString(fmt.Sprintf(`\U%08x`, r))
		case quote == "'" && r == '\'':
			escaped.WriteString(`\'`)
		case quote == `"` && r == '"':
			escaped.WriteString(`\"`)
		default:
			escaped.WriteRune(r)
		}
	}
	return quote + escaped.String() + quote
}

func (va *PipelineAgent) OnAudioFrame(ctx context.Context, frame *model.AudioFrame) {
	select {
	case va.audioInCh <- frame:
	default:
	}
}
