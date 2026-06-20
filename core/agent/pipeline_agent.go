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

	audioInCh        chan *model.AudioFrame
	audioInputHook   atomic.Pointer[AudioInputHook]
	mu               sync.Mutex
	session          *AgentSession
	vadStream        vad.VADStream
	vadSpeechStarted bool
	sttStream        stt.RecognizeStream
	lastSTTFrame     *model.AudioFrame

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

func (va *PipelineAgent) FlushInputTranscription(ctx context.Context, duration time.Duration) error {
	if va == nil {
		return nil
	}
	va.mu.Lock()
	stream := va.sttStream
	shape := va.lastSTTFrame
	va.mu.Unlock()
	if stream == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if duration > 0 && shape != nil && shape.SampleRate > 0 && shape.NumChannels > 0 {
		const silenceChunk = 200 * time.Millisecond
		for remaining := duration; remaining > 0; remaining -= silenceChunk {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			chunk := silenceChunk
			if remaining < chunk {
				chunk = remaining
			}
			frame := audio.SilenceFrame(chunk.Seconds(), shape.SampleRate, shape.NumChannels)
			if err := stream.PushFrame(frame); err != nil {
				return err
			}
		}
	}
	return stream.Flush()
}

func (va *PipelineAgent) ClearInputTranscription() error {
	if va == nil {
		return nil
	}
	va.mu.Lock()
	oldStream := va.sttStream
	sttObj := va.stt
	ctx := va.ctx
	va.mu.Unlock()
	if oldStream != nil {
		if err := oldStream.Close(); err != nil && !isSpeechStreamShutdownError(err) {
			return err
		}
	}
	if sttObj == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stream, err := sttObj.Stream(ctx, "")
	if err != nil {
		return err
	}
	va.mu.Lock()
	va.sttStream = stream
	va.lastSTTFrame = nil
	va.mu.Unlock()
	go va.sttLoop(stream)
	return nil
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

	var vadStream vad.VADStream
	if va.vad != nil {
		stream, err := va.vad.Stream(ctx)
		if err != nil {
			if !isSpeechStreamShutdownError(err) {
				logger.Logger.Errorw("failed to start VAD stream", err)
				va.emitError(err, va.vad)
			}
			return
		}
		vadStream = stream
		va.mu.Lock()
		va.vadStream = stream
		va.vadSpeechStarted = false
		va.mu.Unlock()
		defer func() {
			if err := vadStream.Close(); err != nil && !isSpeechStreamShutdownError(err) {
				logger.Logger.Errorw("failed to close VAD stream", err)
				va.emitError(err, va.vad)
			}
			va.mu.Lock()
			if va.vadStream == vadStream {
				va.vadStream = nil
				va.vadSpeechStarted = false
			}
			va.mu.Unlock()
		}()
		go va.vadLoop(vadStream)
	}

	var sttStream stt.RecognizeStream
	if va.stt != nil {
		stream, err := va.stt.Stream(ctx, "")
		if err != nil {
			if !isSpeechStreamShutdownError(err) {
				logger.Logger.Errorw("failed to start STT stream", err)
				label := "stt"
				if va.stt != nil {
					label = va.stt.Label()
				}
				va.emitError(stt.NewSTTError(label, err, false), va.stt)
			}
			return
		}
		sttStream = stream
		va.mu.Lock()
		va.sttStream = stream
		va.mu.Unlock()
		defer va.closeInputTranscriptionStream()
		go va.sttLoop(sttStream)
	}

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
			if vadStream != nil {
				if err := vadStream.PushFrame(frame); err != nil {
					if !isSpeechStreamShutdownError(err) {
						logger.Logger.Errorw("VAD push frame failed", err)
						va.emitError(err, va.vad)
					}
				}
			}
			if err := va.pushSTTFrame(frame); err != nil {
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

func (va *PipelineAgent) pushSTTFrame(frame *model.AudioFrame) error {
	if va == nil || frame == nil {
		return nil
	}
	va.mu.Lock()
	sttStream := va.sttStream
	sttObj := va.stt
	session := va.session
	va.mu.Unlock()
	if sttStream == nil {
		return nil
	}
	sttFrame := frame
	if session != nil && session.shouldSilenceInputAudio() {
		sttFrame = audio.SilenceFrameLike(frame)
	}
	if targetRate := stt.InputSampleRate(sttObj); targetRate > 0 && sttFrame.SampleRate != targetRate {
		if resampled, err := audio.ResampleAudioFrame(sttFrame, targetRate); err == nil {
			sttFrame = resampled
		} else {
			logger.Logger.Warnw("STT resample failed", err, "from", sttFrame.SampleRate, "to", targetRate)
		}
	}
	va.mu.Lock()
	va.lastSTTFrame = &model.AudioFrame{
		SampleRate:        sttFrame.SampleRate,
		NumChannels:       sttFrame.NumChannels,
		SamplesPerChannel: sttFrame.SamplesPerChannel,
	}
	va.mu.Unlock()
	return sttStream.PushFrame(sttFrame)
}

func (va *PipelineAgent) closeInputTranscriptionStream() {
	va.mu.Lock()
	stream := va.sttStream
	sttObj := va.stt
	va.sttStream = nil
	va.lastSTTFrame = nil
	va.mu.Unlock()
	if stream == nil {
		return
	}
	if err := stream.Close(); err != nil && !isSpeechStreamShutdownError(err) {
		logger.Logger.Errorw("failed to close STT stream", err)
		label := "stt"
		if sttObj != nil {
			label = sttObj.Label()
		}
		va.emitError(stt.NewSTTError(label, err, false), sttObj)
	}
}

func (va *PipelineAgent) vadLoop(stream vad.VADStream) {
	defer va.endActiveVADSpeechOnStreamExit()
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
			va.mu.Lock()
			va.vadSpeechStarted = true
			va.mu.Unlock()
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
			va.mu.Lock()
			va.vadSpeechStarted = false
			va.mu.Unlock()
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

func (va *PipelineAgent) endActiveVADSpeechOnStreamExit() {
	va.mu.Lock()
	started := va.vadSpeechStarted
	va.vadSpeechStarted = false
	session := va.session
	va.mu.Unlock()
	if !started {
		return
	}
	if session != nil && session.activity != nil {
		session.activity.OnEndOfSpeech(nil)
		return
	}
	if session != nil {
		session.UpdateUserState(UserStateListening)
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
		if ev.Type == stt.SpeechEventStartOfSpeech || ev.Type == stt.SpeechEventEndOfSpeech {
			va.mu.Lock()
			session := va.session
			va.mu.Unlock()
			if session != nil && session.activity != nil {
				if session.activity.turnDetectionMode() == TurnDetectionModeSTT {
					if ev.Type == stt.SpeechEventStartOfSpeech {
						session.activity.OnSTTStartOfSpeech(ev)
					} else {
						va.flushActiveVADSegment()
						session.activity.OnEndOfSpeech(nil)
					}
				}
			}
			continue
		}
		if ev.Type != stt.SpeechEventInterimTranscript && ev.Type != stt.SpeechEventPreflightTranscript && ev.Type != stt.SpeechEventFinalTranscript {
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
			if ev.Type == stt.SpeechEventInterimTranscript || ev.Type == stt.SpeechEventPreflightTranscript {
				session.activity.OnInterimTranscript(ev)
				continue
			}
			session.activity.OnFinalTranscript(ev)
			if session.activity.turnDetectionMode() != TurnDetectionModeSTT && !session.activity.vadBasedTurnDetection() {
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

func (va *PipelineAgent) flushActiveVADSegment() {
	va.mu.Lock()
	stream := va.vadStream
	started := va.vadSpeechStarted
	va.mu.Unlock()
	if stream == nil || !started {
		return
	}
	if err := stream.Flush(); err != nil && !isSpeechStreamShutdownError(err) {
		logger.Logger.Warnw("failed to flush VAD stream after STT end-of-speech", err)
		va.emitError(err, va.vad)
	}
}

func isSpeechStreamShutdownError(err error) bool {
	return err == io.EOF || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, context.Canceled)
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

func (va *PipelineAgent) speechOptions(speech *SpeechHandle) pipelineReplyOptions {
	if speech == nil {
		return pipelineReplyOptions{}
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
	return options
}

func (va *PipelineAgent) OnSpeechPreemptive(ctx context.Context, speech *SpeechHandle) {
	if speech == nil || speech.IsInterrupted() || speech.IsDone() {
		return
	}
	va.mu.Lock()
	session := va.session
	va.mu.Unlock()
	if session == nil || va.LLM == nil {
		return
	}
	precomputeCtx, cancel := context.WithCancel(ctx)
	speech.setPrecomputedGenerationCancel(cancel)
	genData, err := va.precomputeLLMGeneration(precomputeCtx, session, va.speechOptions(speech))
	if err != nil {
		cancel()
		if !suppressContextCanceledError(precomputeCtx, speech, err) {
			logger.Logger.Errorw("preemptive LLM inference failed", err)
			va.emitLLMError(session, err)
		}
		return
	}
	speech.setPrecomputedLLMGeneration(genData)
	if va.tts != nil && session.Options.PreemptiveGenerationPreemptiveTTS && genData.TextEventCh != nil {
		go func() {
			_ = drainLLMText(precomputeCtx, genData.TextCh, nil)
		}()
		segments := va.startSegmentedTTSGeneration(precomputeCtx, session, genData.TextEventCh)
		speech.setPrecomputedTTSSegments(segments)
		return
	}
	if va.tts != nil && session.Options.PreemptiveGenerationPreemptiveTTS {
		ttsGen, err := va.startTTSGeneration(precomputeCtx, session, genData.TextCh)
		if err != nil {
			cancel()
			if !suppressContextCanceledError(precomputeCtx, speech, err) {
				logger.Logger.Errorw("preemptive TTS inference failed", err)
				va.emitTTSError(session, err)
			}
			return
		}
		speech.setPrecomputedTTSGeneration(ttsGen)
	}
}

func (va *PipelineAgent) OnSpeechScheduled(ctx context.Context, speech *SpeechHandle) {
	if speech == nil {
		return
	}
	defer speech.MarkDone()
	speech.AuthorizeGeneration()
	if speech.IsInterrupted() {
		return
	}

	if speech.Generation.Text != "" {
		va.mu.Lock()
		session := va.session
		va.mu.Unlock()
		if session == nil {
			return
		}
		speechCtx, speechCancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-speech.interruptCh:
				speechCancel()
			case <-speechCtx.Done():
			}
		}()
		ttsGen, err := va.synthesizeSpeech(speechCtx, session, singleTextChannel(speech.Generation.Text), speech)
		speechCancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Logger.Errorw("TTS inference failed", err)
			va.emitTTSError(session, err)
		}
		playoutOK := true
		if err == nil && !speech.IsInterrupted() {
			playoutOK = va.waitForAssistantPlayout(ctx, session, speech)
		}
		canCommit := (!speech.IsInterrupted() && err == nil && playoutOK) || (speech.IsInterrupted() && ttsGen != nil && ttsGen.ForwardedAudio)
		if canCommit {
			forwardedText := speech.Generation.Text
			if speech.IsInterrupted() {
				forwardedText = va.forwardedAssistantTextAfterInterruption(ctx, session, speech, speech.Generation.Text)
			}
			if forwardedText != "" {
				if speech.Generation.AssistantMessage != nil {
					speech.Generation.AssistantMessage.Interrupted = speech.IsInterrupted()
					speech.Generation.AssistantMessage.Content = []llm.ChatContent{{Text: forwardedText}}
				}
				insertChatItemIfMissing(va.chatCtx, speech.Generation.AssistantMessage)
				addSpeechChatItemIfMissing(speech, speech.Generation.AssistantMessage)
				session.EmitConversationItemAdded(speech.Generation.AssistantMessage)
			}
		}
		_ = speech.MarkGenerationDone()
		session.UpdateAgentState(AgentStateListening)
		return
	}

	va.generateReplyWithOptions(va.speechOptions(speech))
}

func (va *PipelineAgent) generateReplyWithOptions(opts pipelineReplyOptions) {
	va.mu.Lock()
	session := va.session
	ctx := va.ctx
	va.mu.Unlock()

	if session == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.SpeechHandle != nil {
		replyCtx, replyCancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-opts.SpeechHandle.interruptCh:
				replyCancel()
			case <-replyCtx.Done():
			}
		}()
		defer replyCancel()
		ctx = replyCtx
	}

	logger.Logger.Infow("Generating reply")
	session.UpdateAgentState(AgentStateThinking)

	registeredTools, err := sessionRegisteredTools(ctx, session)
	if err != nil {
		logger.Logger.Errorw("failed to register reply tools", err)
		session.EmitError(ErrorEvent{Error: err, Source: va})
		session.UpdateAgentState(AgentStateListening)
		return
	}
	selectedTools, err := resolveToolsByID(registeredTools, opts.Tools)
	if err != nil {
		logger.Logger.Errorw("failed to resolve reply tools", err)
		session.EmitError(ErrorEvent{Error: err, Source: va})
		session.UpdateAgentState(AgentStateListening)
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
	appendToolOutput := func(toolOut ToolExecutionOutput, functionCalls *[]*llm.FunctionCall, functionCallOutputs *[]*llm.FunctionCallOutput) bool {
		logger.Logger.Infow("Tool executed", "name", toolOut.FncCall.Name)
		fncCall := toolOut.FncCall
		*functionCalls = append(*functionCalls, &fncCall)
		*functionCallOutputs = append(*functionCallOutputs, toolOut.FncCallOut)
		if toolOut.FncCallOut != nil {
			va.chatCtx.Append(&fncCall)
			va.chatCtx.Append(toolOut.FncCallOut)
			if replyCtx != va.chatCtx {
				replyCtx.Append(&fncCall)
				replyCtx.Append(toolOut.FncCallOut)
			}
		}
		return toolOut.FncCallOut != nil
	}

	// In Python parity, we loop for tool calls
	toolSteps := 0
	var pendingToolOutCh <-chan ToolExecutionOutput
	var pendingToolUpdateReplyDone chan struct{}
	closePendingToolUpdateReplyDone := func() {
		if pendingToolUpdateReplyDone != nil {
			close(pendingToolUpdateReplyDone)
			pendingToolUpdateReplyDone = nil
		}
	}
	for {
		replyDone := pendingToolUpdateReplyDone
		pendingToolUpdateReplyDone = nil
		closeReplyDone := func() {
			if replyDone != nil {
				close(replyDone)
				replyDone = nil
			}
		}
		inferenceCtx := replyCtx
		inputModality := ""
		if opts.SpeechHandle != nil {
			inputModality = opts.SpeechHandle.InputDetails.Modality
		}
		if opts.Instructions != "" {
			if inferenceCtx == replyCtx {
				inferenceCtx = replyCtx.Copy()
			}
			inferenceCtx.Items = append(inferenceCtx.Items, &llm.ChatMessage{
				Role:    llm.ChatRoleSystem,
				Content: []llm.ChatContent{{Text: opts.Instructions}},
			})
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
		var genData *LLMGenerationData
		if opts.SpeechHandle != nil && toolSteps == 0 {
			genData = opts.SpeechHandle.takePrecomputedLLMGeneration()
		}
		if genData == nil {
			var err error
			genData, err = PerformLLMInferenceWithTextEvents(ctx, va.LLM, inferenceCtx, selectedTools, chatOptions...)
			if err != nil {
				if !suppressContextCanceledError(ctx, opts.SpeechHandle, err) {
					logger.Logger.Errorw("LLM inference failed", err)
					if opts.SpeechHandle != nil {
						opts.SpeechHandle.SetRunFinalOutput(err)
					}
					va.emitLLMError(session, err)
				}
				closeReplyDone()
				session.UpdateAgentState(AgentStateListening)
				return
			}
		}

		var ttsGen *TTSGenerationData
		if va.tts == nil {
			err = drainLLMGenerationText(ctx, genData, opts.SpeechHandle)
		} else {
			if opts.SpeechHandle != nil && toolSteps == 0 {
				ttsGen = opts.SpeechHandle.takePrecomputedTTSGeneration()
			}
			var precomputedSegments <-chan precomputedTTSSegment
			if opts.SpeechHandle != nil && toolSteps == 0 {
				precomputedSegments = opts.SpeechHandle.takePrecomputedTTSSegments()
			}
			if precomputedSegments != nil {
				ttsGen, err = va.playPrecomputedTTSSegments(ctx, session, precomputedSegments, opts.SpeechHandle)
			} else if ttsGen != nil {
				ttsGen, err = va.playTTSGeneration(ctx, session, ttsGen, opts.SpeechHandle)
			} else if genData.TextEventCh != nil {
				done := make(chan struct{})
				go func() {
					defer close(done)
					_ = drainLLMText(ctx, genData.TextCh, nil)
				}()
				ttsGen, err = va.synthesizeSegmentedSpeech(ctx, session, genData.TextEventCh, opts.SpeechHandle)
				<-done
			} else {
				ttsGen, err = va.synthesizeSpeech(ctx, session, genData.TextCh, opts.SpeechHandle)
			}
		}
		if err != nil {
			if suppressReplyContextCanceledError(ctx, opts.SpeechHandle, err) {
				closeReplyDone()
				session.UpdateAgentState(AgentStateListening)
				return
			}
			if !suppressContextCanceledError(ctx, opts.SpeechHandle, err) {
				logger.Logger.Errorw("TTS inference failed", err)
				va.emitTTSError(session, err)
				closeReplyDone()
				session.UpdateAgentState(AgentStateListening)
				return
			}
		}
		waitForLLMGenerationDone(genData)
		if genData.StreamErr != nil {
			if !suppressContextCanceledError(ctx, opts.SpeechHandle, genData.StreamErr) {
				logger.Logger.Errorw("LLM stream failed", genData.StreamErr)
				if opts.SpeechHandle != nil {
					opts.SpeechHandle.SetRunFinalOutput(genData.StreamErr)
				}
				va.emitLLMError(session, genData.StreamErr)
			}
			closeReplyDone()
			session.UpdateAgentState(AgentStateListening)
			return
		}
		va.emitLLMMetrics(session, genData)

		playoutOK := va.waitForAssistantPlayout(ctx, session, opts.SpeechHandle)
		forwardedText := genData.GeneratedText
		if opts.SpeechHandle != nil && opts.SpeechHandle.IsInterrupted() {
			if ttsGen == nil && session.AudioPlaybackController() == nil {
				forwardedText = ""
			} else {
				forwardedText = va.forwardedAssistantTextAfterInterruption(ctx, session, opts.SpeechHandle, genData.GeneratedText)
			}
		} else if !playoutOK {
			forwardedText = ""
		}
		if forwardedText != "" {
			args := llm.ChatMessageArgs{
				ID:          genData.ID,
				Role:        llm.ChatRoleAssistant,
				Text:        forwardedText,
				Interrupted: opts.SpeechHandle != nil && opts.SpeechHandle.IsInterrupted(),
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
			if va.tts == nil {
				delete(metrics, "tts_metadata")
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
		closeReplyDone()

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
		var releasedByUpdate bool
		for toolOut := range toolOutCh {
			executedTools = true
			if appendToolOutput(toolOut, &functionCalls, &functionCallOutputs) {
				replyRequired = true
			}
			if toolOut.RunContextUpdate {
				releasedByUpdate = true
				pendingToolUpdateReplyDone = toolOut.RunContextUpdateDone
				break
			}
		}
		if releasedByUpdate {
			pendingToolOutCh = toolOutCh
		}
		if !executedTools && pendingToolOutCh != nil {
			for toolOut := range pendingToolOutCh {
				executedTools = true
				if appendToolOutput(toolOut, &functionCalls, &functionCallOutputs) {
					replyRequired = true
				}
				if toolOut.RunContextUpdate {
					releasedByUpdate = true
					pendingToolUpdateReplyDone = toolOut.RunContextUpdateDone
					break
				}
			}
			if !releasedByUpdate {
				pendingToolOutCh = nil
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
				emitted := session.EmitFunctionToolsExecuted(*ev)
				replyRequired = emitted.HasToolReply()
			}
			if activeAgent := session.Agent.GetAgent(); activeAgent != nil {
				if err := updateAgentInstructionsMessage(replyCtx, agentInstructionVariants(activeAgent), false); err != nil {
					logger.Logger.Warnw("failed to refresh reply instructions", err)
				}
			}
		}

		// If no tool calls, we're done
		if !executedTools {
			session.UpdateAgentState(AgentStateListening)
			break
		}
		if !replyRequired {
			closePendingToolUpdateReplyDone()
			if pendingToolOutCh != nil {
				executedTools = false
				replyRequired = false
				functionCalls = nil
				functionCallOutputs = nil
				for toolOut := range pendingToolOutCh {
					executedTools = true
					if appendToolOutput(toolOut, &functionCalls, &functionCallOutputs) {
						replyRequired = true
					}
					if toolOut.RunContextUpdate {
						pendingToolUpdateReplyDone = toolOut.RunContextUpdateDone
					}
				}
				pendingToolOutCh = nil
				if executedTools {
					if ev, err := NewFunctionToolsExecutedEvent(functionCalls, functionCallOutputs); err == nil {
						for _, out := range functionCallOutputs {
							if out != nil {
								ev.ReplyRequired = true
								break
							}
						}
						emitted := session.EmitFunctionToolsExecuted(*ev)
						replyRequired = emitted.HasToolReply()
					}
					if activeAgent := session.Agent.GetAgent(); activeAgent != nil {
						if err := updateAgentInstructionsMessage(replyCtx, agentInstructionVariants(activeAgent), false); err != nil {
							logger.Logger.Warnw("failed to refresh reply instructions", err)
						}
					}
				}
			}
			if !executedTools || !replyRequired {
				closePendingToolUpdateReplyDone()
				session.UpdateAgentState(AgentStateListening)
				break
			}
		}
		if opts.SpeechHandle != nil {
			opts.SpeechHandle.IncrementStep()
			opts.SpeechHandle.AuthorizeGeneration()
		}
		toolSteps++
		// Loop back to LLM with tool outputs
	}
}

func drainLLMGenerationText(ctx context.Context, genData *LLMGenerationData, speech *SpeechHandle) error {
	if genData == nil {
		return nil
	}
	if genData.TextEventCh != nil {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = drainLLMText(ctx, genData.TextCh, nil)
		}()
		err := drainLLMTextEvents(ctx, genData.TextEventCh, speech)
		<-done
		return err
	}
	return drainLLMText(ctx, genData.TextCh, speech)
}

func drainLLMTextEvents(ctx context.Context, textCh <-chan LLMTextEvent, speech *SpeechHandle) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if speech != nil && speech.IsInterrupted() {
			return nil
		}
		select {
		case _, ok := <-textCh:
			if !ok {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func drainLLMText(ctx context.Context, textCh <-chan string, speech *SpeechHandle) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if speech != nil && speech.IsInterrupted() {
			return nil
		}
		select {
		case _, ok := <-textCh:
			if !ok {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type precomputedTTSSegment struct {
	ttsGen            *TTSGenerationData
	transcriptSync    *TranscriptSynchronizer
	transcriptionDone <-chan struct{}
	err               error
}

func (va *PipelineAgent) synthesizeSegmentedSpeech(ctx context.Context, session *AgentSession, textCh <-chan LLMTextEvent, speech *SpeechHandle) (*TTSGenerationData, error) {
	var segment *activeTTSSegment
	var firstGen *TTSGenerationData

	startSegment := func() error {
		if segment != nil {
			return nil
		}
		next, err := va.startActiveTTSSegment(ctx, session)
		if err != nil {
			return err
		}
		segment = next
		if firstGen == nil {
			firstGen = next.ttsGen
		}
		return nil
	}

	playSegment := func() error {
		if segment == nil {
			return nil
		}
		current := segment
		segment = nil
		close(current.input)
		_, err := va.playTTSGenerationWithTranscript(ctx, session, current.ttsGen, current.transcriptSync, current.transcriptionDone, speech)
		if firstGen != nil && current.ttsGen != nil && current.ttsGen.ForwardedAudio {
			firstGen.ForwardedAudio = true
		}
		return err
	}

	for {
		select {
		case event, ok := <-textCh:
			if !ok {
				return firstGen, playSegment()
			}
			if event.Flush {
				if err := playSegment(); err != nil {
					return firstGen, err
				}
				continue
			}
			if event.Text == "" {
				continue
			}
			if err := startSegment(); err != nil {
				return firstGen, err
			}
			select {
			case segment.input <- event.Text:
			case <-ctx.Done():
				return firstGen, ctx.Err()
			}
		case <-ctx.Done():
			return firstGen, ctx.Err()
		}
	}
}

type activeTTSSegment struct {
	input             chan string
	ttsGen            *TTSGenerationData
	transcriptSync    *TranscriptSynchronizer
	transcriptionDone <-chan struct{}
}

func (va *PipelineAgent) startActiveTTSSegment(ctx context.Context, session *AgentSession) (*activeTTSSegment, error) {
	input := make(chan string, 100)
	transcriptSync := NewTranscriptSynchronizer(0)
	useAlignedTranscript := va.useTTSAlignedTranscript(session)
	var transcriptionDone <-chan struct{}
	ttsTextCh := (<-chan string)(input)
	if useAlignedTranscript {
		transcriptionDone = closedChannel()
	} else {
		transcriptionDone = va.forwardAgentOutputTranscription(session, transcriptSync)
		ttsTextCh = va.forwardTextToTranscriptSynchronizer(ctx, input, transcriptSync)
	}
	ttsGen, err := PerformTTSInference(ctx, va.tts, ttsTextCh, va.ttsInferenceOptions(session)...)
	if err != nil {
		close(input)
		transcriptSync.Close()
		<-transcriptionDone
		return nil, err
	}
	if useAlignedTranscript {
		transcriptionDone = va.forwardAlignedAgentOutputTranscription(session, ttsGen.TimedTextCh)
	}
	return &activeTTSSegment{
		input:             input,
		ttsGen:            ttsGen,
		transcriptSync:    transcriptSync,
		transcriptionDone: transcriptionDone,
	}, nil
}

func (va *PipelineAgent) startSegmentedTTSGeneration(ctx context.Context, session *AgentSession, textCh <-chan LLMTextEvent) <-chan precomputedTTSSegment {
	out := make(chan precomputedTTSSegment, 10)
	go func() {
		defer close(out)
		var segment *activeTTSSegment
		startSegment := func() bool {
			if segment != nil {
				return true
			}
			next, err := va.startActiveTTSSegment(ctx, session)
			if err != nil {
				out <- precomputedTTSSegment{err: err}
				return false
			}
			segment = next
			out <- precomputedTTSSegment{
				ttsGen:            next.ttsGen,
				transcriptSync:    next.transcriptSync,
				transcriptionDone: next.transcriptionDone,
			}
			return true
		}
		closeSegment := func() {
			if segment == nil {
				return
			}
			close(segment.input)
			segment = nil
		}
		defer closeSegment()
		for {
			select {
			case event, ok := <-textCh:
				if !ok {
					return
				}
				if event.Flush {
					closeSegment()
					continue
				}
				if event.Text == "" {
					continue
				}
				if !startSegment() {
					return
				}
				select {
				case segment.input <- event.Text:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (va *PipelineAgent) playPrecomputedTTSSegments(ctx context.Context, session *AgentSession, segments <-chan precomputedTTSSegment, speech *SpeechHandle) (*TTSGenerationData, error) {
	var firstGen *TTSGenerationData
	for segment := range segments {
		if segment.err != nil {
			return firstGen, segment.err
		}
		if firstGen == nil {
			firstGen = segment.ttsGen
		}
		if _, err := va.playTTSGenerationWithTranscript(ctx, session, segment.ttsGen, segment.transcriptSync, segment.transcriptionDone, speech); err != nil {
			return firstGen, err
		}
		if firstGen != nil && segment.ttsGen != nil && segment.ttsGen.ForwardedAudio {
			firstGen.ForwardedAudio = true
		}
	}
	return firstGen, nil
}

func waitForLLMGenerationDone(genData *LLMGenerationData) {
	if genData == nil || genData.Done == nil {
		return
	}
	<-genData.Done
}

func (va *PipelineAgent) waitForAssistantPlayout(ctx context.Context, session *AgentSession, speech *SpeechHandle) bool {
	if session == nil || (speech != nil && speech.IsInterrupted()) {
		return true
	}
	playback := session.AudioPlaybackController()
	if playback == nil {
		return true
	}
	if _, err := playback.WaitForPlayout(ctx); err != nil {
		if suppressContextCanceledError(ctx, speech, err) {
			return false
		}
		logger.Logger.Warnw("failed to wait for assistant playback", err)
		return false
	}
	if session.activity != nil {
		session.activity.holdUserTranscriptsUntil(time.Now())
	}
	return true
}

func suppressInterruptedCanceledError(speech *SpeechHandle, err error) bool {
	return speech != nil && speech.IsInterrupted() && errors.Is(err, context.Canceled)
}

func suppressContextCanceledError(ctx context.Context, speech *SpeechHandle, err error) bool {
	if !errors.Is(err, context.Canceled) {
		return false
	}
	if suppressInterruptedCanceledError(speech, err) {
		return true
	}
	return ctx != nil && errors.Is(ctx.Err(), context.Canceled)
}

func suppressReplyContextCanceledError(ctx context.Context, speech *SpeechHandle, err error) bool {
	return errors.Is(err, context.Canceled) &&
		ctx != nil &&
		errors.Is(ctx.Err(), context.Canceled) &&
		(speech == nil || !speech.IsInterrupted())
}

func (va *PipelineAgent) flushAssistantPlayback(session *AgentSession) {
	if session == nil {
		return
	}
	playback := session.AudioPlaybackController()
	if playback == nil {
		return
	}
	if flusher, ok := playback.(interface{ Flush() }); ok {
		flusher.Flush()
	}
}

func (va *PipelineAgent) forwardedAssistantTextAfterInterruption(ctx context.Context, session *AgentSession, speech *SpeechHandle, generatedText string) string {
	if generatedText == "" || speech == nil || !speech.IsInterrupted() || session == nil {
		return generatedText
	}
	playback := session.AudioPlaybackController()
	if playback == nil {
		return generatedText
	}
	playback.ClearBuffer()
	playoutCtx := context.WithoutCancel(ctx)
	ev, err := playback.WaitForPlayout(playoutCtx)
	if err != nil {
		logger.Logger.Warnw("failed to wait for interrupted playback", err)
		return ""
	}
	if session.activity != nil {
		session.activity.holdUserTranscriptsUntil(time.Now())
	}
	if ev.SynchronizedTranscript != "" {
		return ev.SynchronizedTranscript
	}
	if ev.PlaybackPosition <= 0 {
		return ""
	}
	return generatedText
}

func (va *PipelineAgent) precomputeLLMGeneration(ctx context.Context, session *AgentSession, opts pipelineReplyOptions) (*LLMGenerationData, error) {
	registeredTools, err := sessionRegisteredTools(ctx, session)
	if err != nil {
		return nil, err
	}
	selectedTools, err := resolveToolsByID(registeredTools, opts.Tools)
	if err != nil {
		return nil, err
	}
	if opts.IgnoreOnEnterTools {
		selectedTools = filterOnEnterIgnoredTools(selectedTools)
	}

	replyCtx := va.chatCtx
	if opts.ChatCtx != nil {
		replyCtx = opts.ChatCtx.Copy()
	}
	if opts.UserMessage != nil {
		if replyCtx == va.chatCtx {
			replyCtx = va.chatCtx.Copy()
		}
		insertChatItemIfMissing(replyCtx, opts.UserMessage)
	}

	inferenceCtx := replyCtx
	inputModality := ""
	if opts.SpeechHandle != nil {
		inputModality = opts.SpeechHandle.InputDetails.Modality
	}
	if opts.Instructions != "" {
		if inferenceCtx == replyCtx {
			inferenceCtx = replyCtx.Copy()
		}
		inferenceCtx.Items = append(inferenceCtx.Items, &llm.ChatMessage{
			Role:    llm.ChatRoleSystem,
			Content: []llm.ChatContent{{Text: opts.Instructions}},
		})
	}
	if inputModality != "" {
		if inferenceCtx == replyCtx {
			inferenceCtx = replyCtx.Copy()
		}
		applyAgentInstructionsModality(inferenceCtx, inputModality)
	}

	var chatOptions []llm.ChatOption
	if opts.ToolChoice != nil {
		chatOptions = append(chatOptions, llm.WithToolChoice(opts.ToolChoice))
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
	return PerformLLMInferenceWithTextEvents(ctx, va.LLM, inferenceCtx, selectedTools, chatOptions...)
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

func (va *PipelineAgent) synthesizeSpeech(ctx context.Context, session *AgentSession, textCh <-chan string, speech *SpeechHandle) (*TTSGenerationData, error) {
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
	return va.playTTSGenerationWithTranscript(ctx, session, ttsGen, transcriptSync, transcriptionDone, speech)
}

func (va *PipelineAgent) startTTSGeneration(ctx context.Context, session *AgentSession, textCh <-chan string) (*TTSGenerationData, error) {
	return PerformTTSInference(ctx, va.tts, textCh, va.ttsInferenceOptions(session)...)
}

func (va *PipelineAgent) playTTSGeneration(ctx context.Context, session *AgentSession, ttsGen *TTSGenerationData, speech *SpeechHandle) (*TTSGenerationData, error) {
	if ttsGen == nil {
		return nil, nil
	}
	transcriptSync := NewTranscriptSynchronizer(0)
	transcriptionDone := va.forwardAgentOutputTranscription(session, transcriptSync)
	if va.useTTSAlignedTranscript(session) {
		transcriptSync.Close()
		<-transcriptionDone
		transcriptionDone = va.forwardAlignedAgentOutputTranscription(session, ttsGen.TimedTextCh)
	}
	return va.playTTSGenerationWithTranscript(ctx, session, ttsGen, transcriptSync, transcriptionDone, speech)
}

func (va *PipelineAgent) playTTSGenerationWithTranscript(ctx context.Context, session *AgentSession, ttsGen *TTSGenerationData, transcriptSync *TranscriptSynchronizer, transcriptionDone <-chan struct{}, speech *SpeechHandle) (*TTSGenerationData, error) {
	startedSpeaking := false
	for frame := range ttsGen.AudioCh {
		select {
		case <-ctx.Done():
			transcriptSync.Close()
			<-transcriptionDone
			return ttsGen, ctx.Err()
		default:
			if speech != nil && speech.IsInterrupted() {
				transcriptSync.Close()
				<-transcriptionDone
				return ttsGen, nil
			}
			transcriptSync.PushAudio(frame)
			if va.PublishAudio != nil {
				if err := va.PublishAudio(ctx, frame); err != nil {
					transcriptSync.Close()
					<-transcriptionDone
					return ttsGen, err
				}
			}
			ttsGen.ForwardedAudio = true
			if !startedSpeaking {
				session.UpdateAgentState(AgentStateSpeaking)
				startedSpeaking = true
			}
			if speech != nil && speech.IsInterrupted() {
				transcriptSync.Close()
				<-transcriptionDone
				return ttsGen, nil
			}
		}
	}
	transcriptSync.Close()
	<-transcriptionDone
	va.flushAssistantPlayback(session)
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
	if session != nil && session.AudioOutputController() != nil {
		opts = append(opts, WithTTSRequireAudio())
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

func addSpeechChatItemIfMissing(speech *SpeechHandle, item llm.ChatItem) {
	if speech == nil || item == nil {
		return
	}
	for _, existing := range speech.ChatItems() {
		if existing == item {
			return
		}
	}
	speech.AddChatItems(item)
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
