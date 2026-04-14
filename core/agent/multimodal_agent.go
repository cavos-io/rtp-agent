package agent

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
)

type MultimodalAgent struct {
	agent   *Agent
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

	if err := ma.rtSession.UpdateTools(s.Tools); err != nil {
		logger.Logger.Errorw("failed to update tools on realtime session", err)
	}

	go ma.run(ctx)
	return nil
}

func (ma *MultimodalAgent) run(ctx context.Context) {
	logger.Logger.Infow("MultimodalAgent started")

	eventCh := ma.rtSession.EventCh()

	for {
		select {
		case <-ctx.Done():
			ma.rtSession.Close()
			return
		case frame := <-ma.audioInCh:
			if ma.rtSession != nil {
				if err := ma.rtSession.PushAudio(frame); err != nil {
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

	case llm.RealtimeEventTypeFunctionCall:
		logger.Logger.Infow("Executing tool (multimodal)", "name", ev.Function.Name)

		// Find and execute tool
		var foundTool llm.Tool
		for _, tInterface := range ma.session.Tools {
			if t, ok := tInterface.(llm.Tool); ok {
				if t.Name() == ev.Function.Name {
					foundTool = t
					break
				}
			}
		}

		if foundTool != nil {
			var argsMap map[string]any
			if err := json.Unmarshal([]byte(ev.Function.Arguments), &argsMap); err != nil {
				logger.Logger.Errorw("failed to parse arguments for multimodal tool", err)
			}

			// Execute tool
			result, err := foundTool.Execute(ma.ctx, argsMap)

			if err == llm.ErrStopResponse {
				// Tool requested stop, drop the function call output
				return
			}

			isError := err != nil
			var outputStr string
			if err != nil {
				outputStr = err.Error()
			} else if result != nil {
				if str, ok := result.(string); ok {
					outputStr = str
				} else {
					outBytes, _ := json.Marshal(result)
					outputStr = string(outBytes)
				}
			}

			// Append back to chat context and update realtime session
			ma.chatCtx.Append(&llm.FunctionCallOutput{
				CallID:  ev.Function.CallID,
				Name:    ev.Function.Name,
				Output:  outputStr,
				IsError: isError,
			})
			_ = ma.rtSession.UpdateChatContext(ma.chatCtx)
		}

	case llm.RealtimeEventTypeError:
		if ev.Error != io.EOF {
			logger.Logger.Errorw("Realtime stream error", ev.Error)
		}
	}
}

func (ma *MultimodalAgent) OnAudioFrame(ctx context.Context, frame *model.AudioFrame) {
	select {
	case ma.audioInCh <- frame:
	default:
	}
}
