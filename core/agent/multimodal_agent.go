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

func (ma *MultimodalAgent) OnSpeechScheduled(ctx context.Context, speech *SpeechHandle) {
	if speech == nil {
		return
	}
	defer speech.MarkDone()

	ma.mu.Lock()
	rtSession := ma.rtSession
	session := ma.session
	ma.mu.Unlock()
	if rtSession == nil {
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
		ma.session.EmitSpeechCreated(SpeechCreatedEvent{
			UserInitiated: false,
			Source:        "generate_reply",
			SpeechHandle:  handle,
		})

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
		logger.Logger.Infow("Executing tool (multimodal)", "name", ev.Function.Name)

		// Find and execute tool
		var foundTool llm.Tool
		for _, tool := range ma.realtimeTools() {
			if tool.Name() == ev.Function.Name {
				foundTool = tool
				break
			}
		}

		if foundTool == nil {
			ma.appendRealtimeToolOutput(&llm.FunctionCallOutput{
				CallID:    ev.Function.CallID,
				Name:      ev.Function.Name,
				Output:    fmt.Sprintf("Unknown function: %s", ev.Function.Name),
				IsError:   true,
				CreatedAt: time.Now(),
			})
			return
		}

		output, err := foundTool.Execute(ma.ctx, ev.Function.Arguments)
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
			ma.appendRealtimeToolOutput(&llm.FunctionCallOutput{
				CallID:    ev.Function.CallID,
				Name:      ev.Function.Name,
				Output:    outputStr,
				IsError:   true,
				CreatedAt: time.Now(),
			})
			return
		}

		ma.appendRealtimeToolOutput(&llm.FunctionCallOutput{
			CallID:    ev.Function.CallID,
			Name:      ev.Function.Name,
			Output:    output,
			IsError:   false,
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

func (ma *MultimodalAgent) realtimeTools() []llm.Tool {
	return sessionRegisteredTools(ma.session)
}

func (ma *MultimodalAgent) appendRealtimeToolOutput(output *llm.FunctionCallOutput) {
	if output == nil {
		return
	}
	ma.chatCtx.Append(output)
	_ = ma.rtSession.UpdateChatContext(ma.chatCtx)
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
