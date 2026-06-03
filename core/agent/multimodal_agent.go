package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

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

	PublishAudio func(frame *model.AudioFrame) error
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
	ma.mu.Unlock()

	rtSession, err := ma.model.Session()
	if err != nil {
		return err
	}
	ma.rtSession = rtSession

	if err := ma.rtSession.UpdateTools(ma.realtimeTools()); err != nil {
		logger.Logger.Errorw("failed to update tools on realtime session", err)
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

func (ma *MultimodalAgent) SetPublishAudio(publish func(frame *model.AudioFrame) error) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	ma.PublishAudio = publish
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

	selectedTools, err := resolveToolsByID(sessionRegisteredTools(session), speech.Generation.Tools)
	if err != nil {
		logger.Logger.Errorw("failed to resolve realtime reply tools", err)
		if session != nil {
			session.EmitError(ErrorEvent{Error: err, Source: ma})
		}
		return
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

	options := llm.RealtimeGenerateReplyOptions{
		ToolChoice: speech.Generation.ToolChoice,
		Tools:      selectedTools,
	}
	if speech.Generation.Instructions != nil {
		options.Instructions = speech.Generation.Instructions.AsModality("text").String()
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
			ma.mu.Unlock()
			if rtSession != nil {
				if err := rtSession.PushAudio(frame); err != nil {
					logger.Logger.Errorw("failed to push audio to multimodal session", err)
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
		ma.session.UpdateUserState(UserStateSpeaking)

	case llm.RealtimeEventTypeSpeechStopped:
		logger.Logger.Infow("User stopped speaking (multimodal)")
		ma.session.UpdateUserState(UserStateListening)

	case llm.RealtimeEventTypeAudio:
		ma.session.UpdateAgentState(AgentStateSpeaking)
		if ma.PublishAudio != nil {
			// Decode and publish
			frame := &model.AudioFrame{
				Data:              ev.Data,
				SampleRate:        24000, // Typically 24k for OpenAI realtime
				NumChannels:       1,
				SamplesPerChannel: uint32(len(ev.Data) / 2),
			}
			_ = ma.PublishAudio(frame)
		}

	case llm.RealtimeEventTypeText:
		// Text delta received, we could pipe this to transcript synchronizer
		_ = ev.Text

	case llm.RealtimeEventTypeGenerationCreated:
		if ev.Generation == nil || ev.Generation.UserInitiated || ma.session == nil {
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
		if ev.RemoteItem == nil || ev.RemoteItem.Item == nil || ma.chatCtx == nil {
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
		if ma.session != nil && ev.Metrics != nil {
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
				ma.session.EmitError(ErrorEvent{
					Error:  llm.NewRealtimeModelError(llm.RealtimeLabel(ma.model), ev.Error, false),
					Source: ma.model,
				})
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
		select {
		case <-ctx.Done():
			return
		case message, ok := <-messageCh:
			if !ok {
				messageCh = nil
				continue
			}
			ma.consumeRealtimeMessage(ctx, speech, message)
		case call, ok := <-functionCh:
			if !ok {
				functionCh = nil
				continue
			}
			ma.executeRealtimeFunctionCall(call)
		}
	}
}

func (ma *MultimodalAgent) consumeRealtimeMessage(ctx context.Context, speech *SpeechHandle, message llm.MessageGeneration) {
	var text string
	for message.TextCh != nil || message.AudioCh != nil {
		select {
		case <-ctx.Done():
			return
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
			ma.mu.Unlock()
			if publish != nil {
				_ = publish(frame)
			}
		}
	}
	if text == "" {
		return
	}
	msg := &llm.ChatMessage{
		ID:        message.MessageID,
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: text}},
		CreatedAt: time.Now(),
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

func (ma *MultimodalAgent) realtimeTools() []llm.Tool {
	return sessionRegisteredTools(ma.session)
}

func (ma *MultimodalAgent) executeRealtimeFunctionCall(functionCall *llm.FunctionCall) {
	if functionCall == nil {
		return
	}
	if functionCall.CreatedAt.IsZero() {
		functionCall.CreatedAt = time.Now()
	}
	logger.Logger.Infow("Executing tool (multimodal)", "name", functionCall.Name)

	var foundTool llm.Tool
	for _, tool := range ma.realtimeTools() {
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
	output, err := foundTool.Execute(callCtx, functionCall.Arguments)
	if err != nil {
		var stopResponse llm.StopResponse
		if errors.As(err, &stopResponse) {
			return
		}

		var toolErr llm.ToolError
		outputStr := "An internal error occurred"
		if errors.As(err, &toolErr) {
			outputStr = toolErr.Message
		}
		ma.appendRealtimeToolResult(functionCall, &llm.FunctionCallOutput{
			CallID:    functionCall.CallID,
			Name:      functionCall.Name,
			Output:    outputStr,
			IsError:   true,
			CreatedAt: time.Now(),
		})
		return
	}

	ma.appendRealtimeToolResult(functionCall, &llm.FunctionCallOutput{
		CallID:    functionCall.CallID,
		Name:      functionCall.Name,
		Output:    output,
		IsError:   false,
		CreatedAt: time.Now(),
	})
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
		_ = ma.rtSession.UpdateChatContext(ma.chatCtx)
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
	if ev.HasToolReply() {
		ma.installPendingRealtimeAutoToolReply()
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
