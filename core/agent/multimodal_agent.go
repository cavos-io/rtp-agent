package agent

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

type MultimodalAgent struct {
	model   llm.RealtimeModel
	session *AgentSession
	chatCtx *llm.ChatContext

	rtSession llm.RealtimeSession
	audioInCh chan *model.AudioFrame

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc

	pendingAutoToolReply      *SpeechHandle
	pendingAutoToolReplyTimer *time.Timer

	PublishAudio func(ctx context.Context, frame *model.AudioFrame) error
}

func NewMultimodalAgent(
	m llm.RealtimeModel,
	chatCtx *llm.ChatContext,
) *MultimodalAgent {
	if chatCtx == nil {
		chatCtx = llm.NewChatContext()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &MultimodalAgent{
		model:     m,
		chatCtx:   chatCtx,
		audioInCh: make(chan *model.AudioFrame, 100),
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (ma *MultimodalAgent) Start(ctx context.Context, s *AgentSession) error {
	ma.mu.Lock()
	ma.session = s
	ma.ctx = ctx
	ma.mu.Unlock()

	rtSession, err := ma.model.Session()
	if err != nil {
		return err
	}
	ma.mu.Lock()
	ma.rtSession = rtSession
	ma.mu.Unlock()

	if err := ma.initializeRealtimeSession(rtSession); err != nil {
		logger.Logger.Errorw("failed to initialize realtime session", err)
		_ = rtSession.Close()
		ma.mu.Lock()
		if ma.rtSession == rtSession {
			ma.rtSession = nil
		}
		ma.mu.Unlock()
		return err
	}

	go ma.run(ctx, rtSession)
	return nil
}

func (ma *MultimodalAgent) Close() error {
	ma.mu.Lock()
	rtSession := ma.rtSession
	ma.rtSession = nil
	ma.mu.Unlock()

	if rtSession != nil {
		return rtSession.Close()
	}
	return nil
}

func (ma *MultimodalAgent) SetPublishAudio(publish func(ctx context.Context, frame *model.AudioFrame) error) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	ma.PublishAudio = publish
}

func (ma *MultimodalAgent) UpdateInstructions(ctx context.Context, instructions string) error {
	ma.mu.Lock()
	rtSession := ma.rtSession
	ma.mu.Unlock()
	if rtSession == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return rtSession.UpdateInstructions(instructions)
}

func (ma *MultimodalAgent) UpdateTools(ctx context.Context) error {
	ma.mu.Lock()
	rtSession := ma.rtSession
	ma.mu.Unlock()
	if rtSession == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	tools, err := ma.realtimeTools()
	if err != nil {
		return err
	}
	return rtSession.UpdateTools(tools)
}

func (ma *MultimodalAgent) UpdateChatContext(ctx context.Context, chatCtx *llm.ChatContext) error {
	if chatCtx == nil {
		chatCtx = llm.NewChatContext()
	}
	ma.mu.Lock()
	rtSession := ma.rtSession
	ma.chatCtx = chatCtx
	ma.mu.Unlock()
	if rtSession == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return rtSession.UpdateChatContext(chatCtx)
}

func (ma *MultimodalAgent) UpdateOptions(ctx context.Context, options llm.RealtimeSessionOptions) error {
	ma.mu.Lock()
	rtSession := ma.rtSession
	ma.mu.Unlock()
	if rtSession == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return rtSession.UpdateOptions(options)
}

func (ma *MultimodalAgent) CommitAudio() error {
	ma.mu.Lock()
	rtSession := ma.rtSession
	ma.mu.Unlock()
	if rtSession == nil {
		return nil
	}
	return rtSession.CommitAudio()
}

func (ma *MultimodalAgent) ClearAudio() error {
	ma.mu.Lock()
	rtSession := ma.rtSession
	ma.mu.Unlock()
	if rtSession == nil {
		return nil
	}
	return rtSession.ClearAudio()
}

func (ma *MultimodalAgent) Interrupt() error {
	ma.mu.Lock()
	rtSession := ma.rtSession
	ma.mu.Unlock()
	if rtSession == nil {
		return nil
	}
	return rtSession.Interrupt()
}

func (ma *MultimodalAgent) UpdateRealtimeModel(ctx context.Context, model llm.RealtimeModel) error {
	if model == nil {
		return nil
	}

	ma.mu.Lock()
	if ma.model == model {
		rtSession := ma.rtSession
		ma.mu.Unlock()
		if rtSession == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := rtSession.Interrupt(); err != nil {
			return err
		}
		if err := rtSession.ClearAudio(); err != nil {
			return err
		}
		return ma.configureRealtimeSession(rtSession, model.Capabilities())
	}
	oldSession := ma.rtSession
	runCtx := ma.ctx
	ma.mu.Unlock()

	if runCtx == nil {
		runCtx = ctx
	}
	if oldSession == nil {
		ma.mu.Lock()
		ma.model = model
		ma.mu.Unlock()
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	rtSession, err := model.Session()
	if err != nil {
		return err
	}
	if err := ma.initializeRealtimeSession(rtSession); err != nil {
		_ = rtSession.Close()
		return err
	}

	ma.mu.Lock()
	oldSession = ma.rtSession
	ma.model = model
	ma.rtSession = rtSession
	ma.mu.Unlock()

	if oldSession != nil {
		_ = oldSession.Close()
	}
	go ma.run(runCtx, rtSession)
	return nil
}

func (ma *MultimodalAgent) initializeRealtimeSession(rtSession llm.RealtimeSession) error {
	return ma.configureRealtimeSession(rtSession, llm.RealtimeCapabilities{
		MutableChatContext:  true,
		MutableInstructions: true,
		MutableTools:        true,
	})
}

func (ma *MultimodalAgent) configureRealtimeSession(rtSession llm.RealtimeSession, capabilities llm.RealtimeCapabilities) error {
	ma.mu.Lock()
	session := ma.session
	chatCtx := ma.chatCtx
	ma.mu.Unlock()

	if capabilities.MutableInstructions && session != nil && session.Agent != nil {
		instructions := agentInstructionsText(session.Agent.GetAgent())
		if instructions != "" {
			if err := rtSession.UpdateInstructions(instructions); err != nil {
				return err
			}
		}
	}
	if capabilities.MutableChatContext && chatCtx != nil {
		if err := rtSession.UpdateChatContext(chatCtx); err != nil {
			return err
		}
	}
	if !capabilities.MutableTools {
		return nil
	}
	tools, err := ma.realtimeTools()
	if err != nil {
		return err
	}
	return rtSession.UpdateTools(tools)
}

func (ma *MultimodalAgent) SupportsNativeSay() bool {
	return ma.model != nil && ma.model.Capabilities().SupportsSay
}

func (ma *MultimodalAgent) RealtimeCapabilities() llm.RealtimeCapabilities {
	if ma.model == nil {
		return llm.RealtimeCapabilities{}
	}
	return ma.model.Capabilities()
}

func (ma *MultimodalAgent) OnSpeechScheduled(ctx context.Context, speech *SpeechHandle) {
	if speech == nil {
		return
	}
	defer speech.MarkDone()

	if speech.Generation.RealtimeGeneration != nil {
		ma.consumeRealtimeGeneration(ctx, speech, speech.Generation.RealtimeGeneration)
		return
	}

	ma.mu.Lock()
	rtSession := ma.rtSession
	session := ma.session
	ma.mu.Unlock()
	if rtSession == nil {
		return
	}

	if speech.Generation.Text != "" && ma.model.Capabilities().SupportsSay {
		if err := rtSession.Say(speech.Generation.Text); err != nil {
			logger.Logger.Errorw("failed to say text with realtime session", err)
			if session != nil {
				session.EmitError(ErrorEvent{
					Error:  llm.NewRealtimeModelError(llm.RealtimeLabel(ma.model), err, false),
					Source: ma.model,
				})
			}
		}
		return
	}

	registeredTools, err := sessionRegisteredTools(ctx, session)
	if err != nil {
		logger.Logger.Errorw("failed to register realtime reply tools", err)
		if session != nil {
			session.EmitError(ErrorEvent{Error: err, Source: ma})
		}
		return
	}
	selectedTools, err := resolveToolsByID(registeredTools, speech.Generation.Tools)
	if err != nil {
		logger.Logger.Errorw("failed to resolve realtime reply tools", err)
		if session != nil {
			session.EmitError(ErrorEvent{Error: err, Source: ma})
		}
		return
	}
	if speech.Generation.IgnoreOnEnterTools {
		selectedTools = filterOnEnterIgnoredTools(selectedTools)
	}
	if speech.Generation.UserMessage != nil && ma.chatCtx != nil {
		if err := ma.chatCtx.UpsertItem(speech.Generation.UserMessage, llm.ChatContextUpsertOptions{AllowTypeMismatch: true}); err != nil {
			logger.Logger.Errorw("failed to update realtime chat context", err)
			if session != nil {
				session.EmitError(ErrorEvent{Error: err, Source: ma})
			}
			return
		}
		if err := rtSession.UpdateChatContext(ma.chatCtx); err != nil {
			logger.Logger.Errorw("failed to update realtime session chat context", err)
			if session != nil {
				session.EmitError(ErrorEvent{
					Error:  llm.NewRealtimeModelError(llm.RealtimeLabel(ma.model), err, false),
					Source: ma.model,
				})
			}
			return
		}
	}

	emitRealtimeError := func(message string, err error) {
		logger.Logger.Errorw(message, err)
		if session != nil {
			session.EmitError(ErrorEvent{
				Error:  llm.NewRealtimeModelError(llm.RealtimeLabel(ma.model), err, false),
				Source: ma.model,
			})
		}
	}

	options := llm.RealtimeGenerateReplyOptions{}
	if ma.model.Capabilities().PerResponseToolChoice {
		options.ToolChoice = speech.Generation.ToolChoice
		options.Tools = selectedTools
	} else {
		var storedToolChoice llm.ToolChoice
		resetToolChoice := false
		var storedTools []llm.Tool
		resetTools := false
		defer func() {
			if resetToolChoice {
				if err := rtSession.UpdateOptions(llm.RealtimeSessionOptions{
					ToolChoice:    storedToolChoice,
					ToolChoiceSet: true,
				}); err != nil {
					emitRealtimeError("failed to reset realtime tool choice", err)
				}
			}
			if resetTools {
				if err := rtSession.UpdateTools(storedTools); err != nil {
					emitRealtimeError("failed to reset realtime tools", err)
				}
			}
		}()
		if session != nil {
			storedToolChoice = session.Options.ToolChoice
		}
		if speech.Generation.ToolChoice != nil && !reflect.DeepEqual(speech.Generation.ToolChoice, storedToolChoice) {
			if err := rtSession.UpdateOptions(llm.RealtimeSessionOptions{
				ToolChoice:    speech.Generation.ToolChoice,
				ToolChoiceSet: true,
			}); err != nil {
				emitRealtimeError("failed to update realtime tool choice", err)
				return
			}
			resetToolChoice = true
		}
		if len(speech.Generation.Tools) > 0 {
			var err error
			storedTools, err = resolveToolsByID(registeredTools, nil)
			if err != nil {
				emitRealtimeError("failed to resolve realtime tools for reset", err)
				return
			}
			if err := rtSession.UpdateTools(selectedTools); err != nil {
				emitRealtimeError("failed to update realtime tools", err)
				return
			}
			resetTools = true
		}
	}
	if speech.Generation.Instructions != nil {
		options.Instructions = speech.Generation.Instructions.AsModality(speech.InputDetails.Modality).String()
	}
	if err := rtSession.GenerateReply(options); err != nil {
		logger.Logger.Errorw("failed to generate realtime reply", err)
		if session != nil {
			session.EmitError(ErrorEvent{
				Error:  llm.NewRealtimeModelError(llm.RealtimeLabel(ma.model), err, false),
				Source: ma.model,
			})
		}
	}
}

func (ma *MultimodalAgent) run(ctx context.Context, rtSession llm.RealtimeSession) {
	logger.Logger.Infow("MultimodalAgent started")

	eventCh := rtSession.EventCh()

	for {
		select {
		case <-ctx.Done():
			_ = ma.Close()
			return
		case frame := <-ma.audioInCh:
			ma.mu.Lock()
			rtSession := ma.rtSession
			session := ma.session
			ma.mu.Unlock()
			if rtSession != nil {
				rtFrame := frame
				if session != nil && session.shouldSilenceInputAudio() {
					rtFrame = audio.SilenceFrameLike(frame)
				}
				if err := rtSession.PushAudio(rtFrame); err != nil {
					logger.Logger.Errorw("failed to push audio to multimodal session", err)
					ma.mu.Lock()
					session := ma.session
					ma.mu.Unlock()
					if session != nil {
						session.EmitError(ErrorEvent{
							Error:  llm.NewRealtimeError("failed to push audio to realtime session", err),
							Source: rtSession,
						})
					}
				}
			}
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			ma.handleRealtimeEvent(ev)
		}
	}
}

func (ma *MultimodalAgent) handleRealtimeEvent(ev llm.RealtimeEvent) {
	switch ev.Type {
	case llm.RealtimeEventTypeSpeechStarted:
		logger.Logger.Infow("User started speaking (multimodal)")
		if ma.session != nil && ma.session.activity != nil {
			ma.session.activity.OnInputSpeechStarted()
		} else if ma.session != nil {
			ma.session.UpdateUserState(UserStateSpeaking)
		}

	case llm.RealtimeEventTypeSpeechStopped:
		logger.Logger.Infow("User stopped speaking (multimodal)")
		if ma.session != nil && ma.session.activity != nil {
			speechStopped := llm.InputSpeechStoppedEvent{}
			if ev.SpeechStopped != nil {
				speechStopped = *ev.SpeechStopped
			}
			ma.session.activity.OnInputSpeechStopped(speechStopped)
		} else if ma.session != nil {
			ma.session.UpdateUserState(UserStateListening)
		}

	case llm.RealtimeEventTypeAudio:
		if ma.PublishAudio != nil {
			// Decode and publish
			frame := &model.AudioFrame{
				Data:              ev.Data,
				SampleRate:        24000, // Typically 24k for OpenAI realtime
				NumChannels:       1,
				SamplesPerChannel: uint32(len(ev.Data) / 2),
			}
			if err := ma.PublishAudio(context.Background(), frame); err != nil {
				if ma.session != nil {
					ma.session.EmitError(ErrorEvent{
						Error:  llm.NewRealtimeError("failed to publish realtime audio", err),
						Source: ma,
					})
				}
			} else if ma.session != nil {
				ma.session.UpdateAgentState(AgentStateSpeaking)
			}
		}

	case llm.RealtimeEventTypeText:
		// Text delta received, we could pipe this to transcript synchronizer
		_ = ev.Text

	case llm.RealtimeEventTypeGenerationCreated:
		if ev.Generation == nil || ev.Generation.UserInitiated || ma.session == nil {
			return
		}
		if ma.session.activity != nil {
			if _, err := ma.session.activity.OnGenerationCreated(*ev.Generation, ma.attachPendingRealtimeAutoToolReply); err != nil {
				logger.Logger.Warnw("failed to schedule realtime generation", err, "response_id", ev.Generation.ResponseID)
			}
			return
		}
		handle := NewSpeechHandle(ma.session.Options.AllowInterruptions, DefaultInputDetails())
		handle.Generation.RealtimeGeneration = ev.Generation
		ma.session.EmitSpeechCreated(SpeechCreatedEvent{
			UserInitiated: false,
			Source:        "generate_reply",
			SpeechHandle:  handle,
		})
		ma.attachPendingRealtimeAutoToolReply(handle)
		if ma.session.activity != nil {
			if err := ma.session.activity.ScheduleSpeech(handle, SpeechPriorityNormal, false); err != nil {
				logger.Logger.Warnw("failed to schedule realtime generation", err, "response_id", ev.Generation.ResponseID)
			}
		}

	case llm.RealtimeEventTypeInputAudioTranscriptionCompleted:
		if ev.InputTranscription == nil || ma.session == nil {
			return
		}
		if ma.session.activity != nil {
			ma.session.activity.OnInputAudioTranscriptionCompleted(*ev.InputTranscription)
			return
		}
		ma.session.EmitUserInputTranscribed(UserInputTranscribedEvent{
			Transcript: ev.InputTranscription.Transcript,
			IsFinal:    ev.InputTranscription.IsFinal,
		})
		if ev.InputTranscription.IsFinal {
			msg := &llm.ChatMessage{
				ID:   ev.InputTranscription.ItemID,
				Role: llm.ChatRoleUser,
				Content: []llm.ChatContent{
					{Text: ev.InputTranscription.Transcript},
				},
				CreatedAt: time.Now(),
			}
			if ma.chatCtx != nil {
				_ = ma.chatCtx.UpsertItem(msg, llm.ChatContextUpsertOptions{AllowTypeMismatch: true})
			}
			ma.session.EmitConversationItemAdded(msg)
		}

	case llm.RealtimeEventTypeRemoteItemAdded:
		if ev.RemoteItem == nil || ev.RemoteItem.Item == nil {
			return
		}
		if ma.session != nil && ma.session.activity != nil {
			ma.session.activity.OnRemoteItemAdded(*ev.RemoteItem)
			return
		}
		if ma.chatCtx == nil {
			return
		}
		item := ev.RemoteItem.Item
		if item.GetID() != "" && ma.chatCtx.GetByID(item.GetID()) != nil {
			return
		}
		lastItemID := ""
		if len(ma.chatCtx.Items) > 0 {
			lastItemID = ma.chatCtx.Items[len(ma.chatCtx.Items)-1].GetID()
		}
		if ev.RemoteItem.PreviousItemID == "" || ev.RemoteItem.PreviousItemID == lastItemID {
			ma.chatCtx.Items = append(ma.chatCtx.Items, item)
		}

	case llm.RealtimeEventTypeMetricsCollected:
		if ma.session == nil || ev.Metrics == nil {
			return
		}
		if ma.session.activity != nil {
			ma.session.activity.OnMetricsCollected(ev.Metrics)
		} else {
			ma.session.EmitMetricsCollected(ev.Metrics)
		}

	case llm.RealtimeEventTypeFunctionCall:
		if ev.Function == nil {
			return
		}
		ma.executeRealtimeFunctionCall(&llm.FunctionCall{
			CallID:    ev.Function.CallID,
			Name:      ev.Function.Name,
			Arguments: ev.Function.Arguments,
			Extra:     ev.Function.Extra,
			CreatedAt: time.Now(),
		})

	case llm.RealtimeEventTypeError:
		if ev.Error != io.EOF {
			logger.Logger.Errorw("Realtime stream error", ev.Error)
			if ma.session != nil && ev.Error != nil {
				err := llm.NewRealtimeModelError(llm.RealtimeLabel(ma.model), ev.Error, false)
				if ma.session.activity != nil {
					ma.session.activity.OnError(err, ma.model)
				} else {
					ma.session.EmitError(ErrorEvent{
						Error:  err,
						Source: ma.model,
					})
				}
			}
		}
	}
}

func (ma *MultimodalAgent) consumeRealtimeGeneration(ctx context.Context, speech *SpeechHandle, generation *llm.GenerationCreatedEvent) {
	if generation == nil || (generation.MessageCh == nil && generation.FunctionCh == nil) {
		return
	}
	messageCh := generation.MessageCh
	functionCh := generation.FunctionCh
	for messageCh != nil || functionCh != nil {
		if speech != nil && speech.IsInterrupted() {
			ma.syncRealtimeChatContextAfterSkippedMessages()
			return
		}
		select {
		case <-ctx.Done():
			return
		case message, ok := <-messageCh:
			if !ok {
				messageCh = nil
				continue
			}
			if speech != nil && speech.IsInterrupted() {
				ma.syncRealtimeChatContextAfterSkippedMessages()
				return
			}
			ma.consumeRealtimeMessage(ctx, speech, message)
			if speech != nil && speech.IsInterrupted() {
				ma.syncRealtimeChatContextAfterSkippedMessages()
				return
			}
		case call, ok := <-functionCh:
			if !ok {
				functionCh = nil
				continue
			}
			if speech != nil && speech.IsInterrupted() {
				ma.syncRealtimeChatContextAfterSkippedMessages()
				return
			}
			ma.executeRealtimeFunctionCall(call)
		}
	}
}

func (ma *MultimodalAgent) consumeRealtimeMessage(ctx context.Context, speech *SpeechHandle, message llm.MessageGeneration) {
	var text string
	var modalities []string
	startedSpeaking := false
	modalitiesCh := message.ModalitiesCh
	for message.TextCh != nil || message.AudioCh != nil || modalitiesCh != nil {
		select {
		case <-ctx.Done():
			return
		case values, ok := <-modalitiesCh:
			if !ok {
				modalitiesCh = nil
				continue
			}
			modalities = append([]string(nil), values...)
		case delta, ok := <-message.TextCh:
			if !ok {
				message.TextCh = nil
				continue
			}
			text += delta
		case frame, ok := <-message.AudioCh:
			if !ok {
				message.AudioCh = nil
				continue
			}
			ma.mu.Lock()
			publish := ma.PublishAudio
			session := ma.session
			ma.mu.Unlock()
			if publish != nil {
				if err := publish(ctx, frame); err != nil {
					if session != nil {
						session.EmitError(ErrorEvent{
							Error:  llm.NewRealtimeError("failed to publish realtime audio", err),
							Source: ma,
						})
					}
					return
				}
				if !startedSpeaking && session != nil {
					session.UpdateAgentState(AgentStateSpeaking)
					startedSpeaking = true
				}
			}
		}
	}
	interrupted := speech != nil && speech.IsInterrupted()
	if interrupted {
		var playback AudioPlaybackResult
		text, playback = ma.forwardedRealtimeTextAfterInterruption(ctx, text)
		ma.truncateInterruptedRealtimeMessage(message.MessageID, modalities, text, playback)
	}
	if text != "" && len(modalities) > 0 && !realtimeModalitiesContain(modalities, "audio") {
		if err := ma.publishTTSFallbackForRealtimeText(ctx, speech, text); err != nil {
			logger.Logger.Errorw("failed to synthesize text-only realtime response", err)
			if ma.session != nil {
				ma.session.EmitError(ErrorEvent{
					Error:  err,
					Source: ma,
				})
			}
			return
		}
	}
	if text == "" {
		return
	}
	msg := &llm.ChatMessage{
		ID:          message.MessageID,
		Role:        llm.ChatRoleAssistant,
		Content:     []llm.ChatContent{{Text: text}},
		Interrupted: interrupted,
		CreatedAt:   time.Now(),
	}
	if ma.chatCtx != nil {
		if err := ma.chatCtx.UpsertItem(msg, llm.ChatContextUpsertOptions{AllowTypeMismatch: true}); err != nil {
			logger.Logger.Errorw("failed to update realtime chat context with generated message", err)
		}
	}
	if ma.session != nil {
		ma.session.EmitConversationItemAdded(msg)
	}
	speech.AddChatItems(msg)
}

func (ma *MultimodalAgent) publishTTSFallbackForRealtimeText(ctx context.Context, speech *SpeechHandle, text string) error {
	ma.mu.Lock()
	session := ma.session
	publish := ma.PublishAudio
	ma.mu.Unlock()
	if session == nil || session.TTS == nil || publish == nil {
		return nil
	}
	textCh := make(chan string, 1)
	textCh <- text
	close(textCh)
	ttsGen, err := PerformTTSInference(ctx, session.TTS, textCh, multimodalTTSInferenceOptions(session)...)
	if err != nil {
		return err
	}
	startedSpeaking := false
	for frame := range ttsGen.AudioCh {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if speech != nil && speech.IsInterrupted() {
				return nil
			}
			if err := publish(ctx, frame); err != nil {
				return err
			}
			if !startedSpeaking {
				session.UpdateAgentState(AgentStateSpeaking)
				startedSpeaking = true
			}
		}
	}
	return ttsGen.StreamErr
}

func multimodalTTSInferenceOptions(session *AgentSession) []TTSInferenceOption {
	if session == nil {
		return nil
	}
	var opts []TTSInferenceOption
	if session.Options.TTSStreamPacer != nil {
		opts = append(opts, WithTTSStreamPacer(*session.Options.TTSStreamPacer))
	}
	if len(session.Options.TTSTextReplacements) > 0 {
		opts = append(opts, WithTTSTextReplacements(session.Options.TTSTextReplacements))
	}
	if session.Options.DisableTTSTextTransforms {
		opts = append(opts, WithTTSTextTransformsDisabled())
	} else if session.Options.TTSTextTransformsSet {
		opts = append(opts, WithTTSTextTransforms(session.Options.TTSTextTransforms))
	}
	return opts
}

func realtimeModalitiesContain(modalities []string, target string) bool {
	for _, modality := range modalities {
		if modality == target {
			return true
		}
	}
	return false
}

func (ma *MultimodalAgent) forwardedRealtimeTextAfterInterruption(ctx context.Context, generatedText string) (string, AudioPlaybackResult) {
	ma.mu.Lock()
	session := ma.session
	ma.mu.Unlock()
	if session == nil {
		return generatedText, AudioPlaybackResult{}
	}
	playback := session.AudioPlaybackController()
	if playback == nil {
		return generatedText, AudioPlaybackResult{}
	}
	playback.ClearBuffer()
	ev, err := playback.WaitForPlayout(ctx)
	if err != nil {
		logger.Logger.Warnw("failed to wait for interrupted realtime playback", err)
		return generatedText, AudioPlaybackResult{}
	}
	if ev.SynchronizedTranscript != "" {
		if session.activity != nil {
			session.activity.holdUserTranscriptsUntil(time.Now())
		}
		return ev.SynchronizedTranscript, ev
	}
	if ev.PlaybackPosition <= 0 {
		if session.activity != nil {
			session.activity.holdUserTranscriptsUntil(time.Now())
		}
		return "", ev
	}
	if session.activity != nil {
		session.activity.holdUserTranscriptsUntil(time.Now())
	}
	return generatedText, ev
}

func (ma *MultimodalAgent) truncateInterruptedRealtimeMessage(messageID string, modalities []string, transcript string, playback AudioPlaybackResult) {
	if messageID == "" || !ma.realtimeCapabilities().MessageTruncation {
		return
	}
	ma.mu.Lock()
	rtSession := ma.rtSession
	ma.mu.Unlock()
	if rtSession == nil {
		return
	}
	audioTranscript := transcript
	if err := rtSession.Truncate(llm.RealtimeTruncateOptions{
		MessageID:       messageID,
		Modalities:      append([]string(nil), modalities...),
		AudioEndMillis:  int(playback.PlaybackPosition / time.Millisecond),
		AudioTranscript: &audioTranscript,
	}); err != nil {
		logger.Logger.Warnw("failed to truncate interrupted realtime message", err, "message_id", messageID)
	}
}

func (ma *MultimodalAgent) syncRealtimeChatContextAfterSkippedMessages() {
	if !ma.realtimeCapabilities().MutableChatContext {
		return
	}
	ma.mu.Lock()
	rtSession := ma.rtSession
	chatCtx := ma.chatCtx
	ma.mu.Unlock()
	if rtSession == nil || chatCtx == nil {
		return
	}
	if err := rtSession.UpdateChatContext(chatCtx); err != nil {
		logger.Logger.Warnw("failed to sync realtime chat context after skipped messages", err)
	}
}

func (ma *MultimodalAgent) realtimeTools() ([]llm.Tool, error) {
	tools, err := sessionRegisteredTools(context.Background(), ma.session)
	if err != nil {
		return nil, err
	}
	toolCtx := llm.EmptyToolContext()
	if err := toolCtx.UpdateTools(agentToolsAsInterfaces(tools)); err != nil {
		return nil, err
	}
	return tools, nil
}

func (ma *MultimodalAgent) executeRealtimeFunctionCall(functionCall *llm.FunctionCall) {
	if functionCall == nil {
		return
	}
	if functionCall.CreatedAt.IsZero() {
		functionCall.CreatedAt = time.Now()
	}
	logger.Logger.Infow("Executing tool (multimodal)", "name", functionCall.Name)

	tools, err := ma.realtimeTools()
	if err != nil {
		logger.Logger.Errorw("failed to register realtime tools", err)
		if ma.session != nil {
			ma.session.EmitError(ErrorEvent{Error: err, Source: ma})
		}
		return
	}
	var foundTool llm.Tool
	for _, tool := range tools {
		if tool.Name() == functionCall.Name {
			foundTool = tool
			break
		}
	}

	if foundTool == nil {
		ma.appendRealtimeToolResult(functionCall, &llm.FunctionCallOutput{
			CallID:    functionCall.CallID,
			Name:      functionCall.Name,
			Output:    fmt.Sprintf("Unknown function: %s", functionCall.Name),
			IsError:   true,
			CreatedAt: time.Now(),
		})
		return
	}

	callCtx := ma.ctx
	if callCtx == nil {
		callCtx = context.Background()
	}
	callCtx = contextWithSessionMockTools(callCtx, ma.session)
	runCtx := NewRunContext(ma.session, nil, functionCall)
	runCtx.attach()
	callCtx = WithRunContext(callCtx, runCtx)
	toolCtx := llm.NewToolContext([]interface{}{foundTool})
	executionToolCtx := mockToolContext(callCtx, toolCtx, ma.session, functionCall.Name)
	result := llm.ExecuteFunctionCall(callCtx, &llm.FunctionToolCall{
		ID:        functionCall.ID,
		CallID:    functionCall.CallID,
		Name:      functionCall.Name,
		Arguments: functionCall.Arguments,
		Extra:     functionCall.Extra,
	}, executionToolCtx)
	functionCall.Arguments = result.FncCall.Arguments
	resultCall := functionCall
	updates := runCtx.Updates()
	runCtx.detach()
	if len(updates) > 0 {
		if updates[0].FunctionCall != nil {
			resultCall = updates[0].FunctionCall
		}
		result.FncCallOut = updates[0].FunctionCallOutput
	}
	if result.FncCallOut == nil {
		return
	}

	ma.appendRealtimeToolResult(resultCall, result.FncCallOut)
}

func (ma *MultimodalAgent) appendRealtimeToolResult(call *llm.FunctionCall, output *llm.FunctionCallOutput) {
	if output == nil {
		return
	}
	if ma.chatCtx != nil {
		if call != nil {
			ma.chatCtx.Append(call)
		}
		ma.chatCtx.Append(output)
	}
	if ma.rtSession != nil && ma.chatCtx != nil {
		if err := ma.rtSession.UpdateChatContext(ma.chatCtx); err != nil {
			logger.Logger.Errorw("failed to update realtime session chat context with tool result", err)
			if ma.session != nil {
				ma.session.EmitError(ErrorEvent{
					Error:  llm.NewRealtimeModelError(llm.RealtimeLabel(ma.model), err, false),
					Source: ma.model,
				})
			}
			return
		}
	}
	if ma.session == nil || call == nil {
		return
	}
	ev, err := NewFunctionToolsExecutedEvent([]*llm.FunctionCall{call}, []*llm.FunctionCallOutput{output})
	if err != nil {
		logger.Logger.Errorw("failed to create realtime function tools executed event", err)
		return
	}
	ev.ReplyRequired = true
	ma.session.EmitFunctionToolsExecuted(*ev)
	if !ev.HasToolReply() {
		return
	}
	if ma.realtimeCapabilities().AutoToolReplyGeneration {
		ma.installPendingRealtimeAutoToolReply()
		return
	}
	ma.generateRealtimeToolReply()
}

func (ma *MultimodalAgent) realtimeCapabilities() llm.RealtimeCapabilities {
	if ma.model == nil {
		return llm.RealtimeCapabilities{}
	}
	return ma.model.Capabilities()
}

func (ma *MultimodalAgent) generateRealtimeToolReply() {
	ma.mu.Lock()
	rtSession := ma.rtSession
	ma.mu.Unlock()
	if rtSession == nil {
		return
	}
	if err := rtSession.Interrupt(); err != nil {
		logger.Logger.Warnw("failed to interrupt realtime session before tool reply", err)
	}
	if err := rtSession.GenerateReply(llm.RealtimeGenerateReplyOptions{ToolChoice: "auto"}); err != nil {
		logger.Logger.Errorw("failed to generate realtime tool reply", err)
		if ma.session != nil {
			ma.session.EmitError(ErrorEvent{
				Error:  llm.NewRealtimeModelError(llm.RealtimeLabel(ma.model), err, false),
				Source: ma.model,
			})
		}
	}
}

func (ma *MultimodalAgent) installPendingRealtimeAutoToolReply() {
	ma.mu.Lock()
	if ma.pendingAutoToolReply != nil {
		ma.mu.Unlock()
		return
	}
	session := ma.session
	if session == nil {
		ma.mu.Unlock()
		return
	}
	pending := NewSpeechHandle(false, DefaultInputDetails())
	ma.pendingAutoToolReply = pending
	ma.mu.Unlock()

	if !session.watchActiveRunSpeechHandle(pending) {
		ma.releasePendingRealtimeAutoToolReply(pending)
		return
	}

	timer := time.AfterFunc(5*time.Second, func() {
		logger.Logger.Warnw("timed out waiting for realtime auto tool reply", nil)
		ma.releasePendingRealtimeAutoToolReply(pending)
	})
	ma.mu.Lock()
	if ma.pendingAutoToolReply == pending {
		ma.pendingAutoToolReplyTimer = timer
		ma.mu.Unlock()
		return
	}
	ma.mu.Unlock()
	timer.Stop()
}

func (ma *MultimodalAgent) attachPendingRealtimeAutoToolReply(handle *SpeechHandle) {
	ma.mu.Lock()
	pending := ma.pendingAutoToolReply
	timer := ma.pendingAutoToolReplyTimer
	if pending != nil {
		ma.pendingAutoToolReply = nil
		ma.pendingAutoToolReplyTimer = nil
	}
	session := ma.session
	ma.mu.Unlock()

	if pending == nil {
		return
	}
	if timer != nil {
		timer.Stop()
	}
	if session != nil {
		session.watchActiveRunSpeechHandle(handle)
	}
	pending.MarkDone()
}

func (ma *MultimodalAgent) releasePendingRealtimeAutoToolReply(pending *SpeechHandle) {
	ma.mu.Lock()
	if ma.pendingAutoToolReply != pending {
		ma.mu.Unlock()
		return
	}
	timer := ma.pendingAutoToolReplyTimer
	ma.pendingAutoToolReply = nil
	ma.pendingAutoToolReplyTimer = nil
	ma.mu.Unlock()

	if timer != nil {
		timer.Stop()
	}
	pending.MarkDone()
}

func (ma *MultimodalAgent) OnAudioFrame(ctx context.Context, frame *model.AudioFrame) {
	select {
	case ma.audioInCh <- frame:
	default:
	}
}

func (ma *MultimodalAgent) OnVideoFrame(ctx context.Context, frame *images.VideoFrame) {
	ma.mu.Lock()
	rtSession := ma.rtSession
	session := ma.session
	ma.mu.Unlock()
	if rtSession == nil {
		return
	}
	if err := rtSession.PushVideo(frame); err != nil {
		logger.Logger.Errorw("failed to push video to multimodal session", err)
		if session != nil {
			session.EmitError(ErrorEvent{
				Error:  llm.NewRealtimeError("failed to push video to realtime session", err),
				Source: rtSession,
			})
		}
	}
}
