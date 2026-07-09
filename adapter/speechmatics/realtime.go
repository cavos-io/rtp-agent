package speechmatics

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

const (
	defaultSpeechmaticsRealtimeBaseURL          = "wss://flow.api.speechmatics.com/v1"
	defaultSpeechmaticsRealtimeModel            = "flow"
	defaultSpeechmaticsRealtimeVoice            = "sarah"
	defaultSpeechmaticsRealtimeSystemPrompt     = "You are a helpful assistant."
	defaultSpeechmaticsRealtimeInputSampleRate  = 16000
	defaultSpeechmaticsRealtimeOutputSampleRate = 16000
	speechmaticsFlowURLEnv                      = "SPEECHMATICS_FLOW_URL"
)

type RealtimeModel struct {
	mu               sync.Mutex
	sessions         map[*speechmaticsRealtimeSession]struct{}
	apiKey           string
	baseURL          string
	model            string
	voice            string
	systemPrompt     string
	inputSampleRate  int
	outputSampleRate int
	closed           bool
}

type RealtimeOption func(*RealtimeModel)

func NewRealtimeModel(apiKey string, opts ...RealtimeOption) (*RealtimeModel, error) {
	if apiKey == "" {
		apiKey = os.Getenv(speechmaticsAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("speechmatics API key is required. Pass one in via the apiKey parameter, or set %s", speechmaticsAPIKeyEnv)
	}
	baseURL := os.Getenv(speechmaticsFlowURLEnv)
	if baseURL == "" {
		baseURL = defaultSpeechmaticsRealtimeBaseURL
	}
	model := &RealtimeModel{
		apiKey:           apiKey,
		baseURL:          baseURL,
		model:            defaultSpeechmaticsRealtimeModel,
		voice:            defaultSpeechmaticsRealtimeVoice,
		systemPrompt:     defaultSpeechmaticsRealtimeSystemPrompt,
		inputSampleRate:  defaultSpeechmaticsRealtimeInputSampleRate,
		outputSampleRate: defaultSpeechmaticsRealtimeOutputSampleRate,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(model)
		}
	}
	return model, nil
}

func WithRealtimeBaseURL(baseURL string) RealtimeOption {
	return func(m *RealtimeModel) {
		m.baseURL = baseURL
	}
}

func WithRealtimeModel(model string) RealtimeOption {
	return func(m *RealtimeModel) {
		m.model = model
	}
}

func WithRealtimeVoice(voice string) RealtimeOption {
	return func(m *RealtimeModel) {
		m.voice = voice
	}
}

func WithRealtimeSystemPrompt(prompt string) RealtimeOption {
	return func(m *RealtimeModel) {
		m.systemPrompt = prompt
	}
}

func WithRealtimeInputSampleRate(sampleRate int) RealtimeOption {
	return func(m *RealtimeModel) {
		m.inputSampleRate = sampleRate
	}
}

func WithRealtimeOutputSampleRate(sampleRate int) RealtimeOption {
	return func(m *RealtimeModel) {
		m.outputSampleRate = sampleRate
	}
}

func (m *RealtimeModel) Label() string    { return "speechmatics.RealtimeModel" }
func (m *RealtimeModel) Model() string    { return m.model }
func (m *RealtimeModel) Provider() string { return "Speechmatics" }

func (m *RealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{
		MessageTruncation:       false,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             true,
		ManualFunctionCalls:     false,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   false,
		SupportsSay:             true,
	}
}

func (m *RealtimeModel) Session() (llm.RealtimeSession, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	session := &speechmaticsRealtimeSession{
		eventCh:          make(chan llm.RealtimeEvent, 16),
		commandCh:        make(chan map[string]any, 256),
		owner:            m,
		apiKey:           m.apiKey,
		baseURL:          m.baseURL,
		model:            m.model,
		voice:            m.voice,
		instructions:     m.systemPrompt,
		inputSampleRate:  m.inputSampleRate,
		outputSampleRate: m.outputSampleRate,
	}
	if m.sessions == nil {
		m.sessions = make(map[*speechmaticsRealtimeSession]struct{})
	}
	m.sessions[session] = struct{}{}
	m.mu.Unlock()

	session.enqueueCommand(map[string]any{
		"type":               "session.create",
		"model":              session.model,
		"voice":              session.voice,
		"instructions":       session.instructions,
		"input_sample_rate":  session.inputSampleRate,
		"output_sample_rate": session.outputSampleRate,
	})
	return session, nil
}

func (m *RealtimeModel) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	sessions := make([]*speechmaticsRealtimeSession, 0, len(m.sessions))
	for session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = nil
	m.mu.Unlock()
	for _, session := range sessions {
		_ = session.Close()
	}
	return nil
}

func (m *RealtimeModel) unregisterRealtimeSession(session *speechmaticsRealtimeSession) {
	if m == nil || session == nil {
		return
	}
	m.mu.Lock()
	delete(m.sessions, session)
	m.mu.Unlock()
}

type speechmaticsRealtimeSession struct {
	mu               sync.Mutex
	eventCh          chan llm.RealtimeEvent
	commandCh        chan map[string]any
	owner            *RealtimeModel
	apiKey           string
	baseURL          string
	model            string
	voice            string
	instructions     string
	inputSampleRate  int
	outputSampleRate int
	closed           bool
	closeOnce        sync.Once
}

func (s *speechmaticsRealtimeSession) UpdateInstructions(instructions string) error {
	if s.isClosed() {
		return nil
	}
	s.mu.Lock()
	s.instructions = instructions
	s.mu.Unlock()
	return s.enqueueCommand(map[string]any{
		"type":         "session.update",
		"instructions": instructions,
	})
}

func (s *speechmaticsRealtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	if chatCtx == nil {
		return nil
	}
	for _, item := range chatCtx.Items {
		switch item := item.(type) {
		case *llm.ChatMessage:
			text := item.TextContent()
			if text == "" {
				continue
			}
			if err := s.enqueueCommand(map[string]any{
				"type": "conversation.item.create",
				"role": string(item.Role),
				"text": text,
			}); err != nil {
				return err
			}
		case *llm.FunctionCallOutput:
			if err := s.enqueueCommand(map[string]any{
				"type":     "function_call_output",
				"call_id":  item.CallID,
				"output":   item.Output,
				"is_error": item.IsError,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *speechmaticsRealtimeSession) UpdateTools(tools []llm.Tool) error {
	payload, err := speechmaticsRealtimeTools(tools)
	if err != nil {
		return err
	}
	return s.enqueueCommand(map[string]any{
		"type":  "session.update",
		"tools": payload,
	})
}

func (s *speechmaticsRealtimeSession) UpdateOptions(options llm.RealtimeSessionOptions) error {
	if !options.VoiceSet {
		return nil
	}
	if s.isClosed() {
		return nil
	}
	s.mu.Lock()
	s.voice = options.Voice
	s.mu.Unlock()
	return s.enqueueCommand(map[string]any{
		"type":  "session.update",
		"voice": options.Voice,
	})
}

func (s *speechmaticsRealtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	command := map[string]any{
		"type": "response.create",
	}
	if options.InstructionsSet {
		command["instructions"] = options.Instructions
	}
	if len(options.Tools) > 0 {
		tools, err := speechmaticsRealtimeTools(options.Tools)
		if err != nil {
			return err
		}
		command["tools"] = tools
	}
	if options.ToolChoice != nil {
		command["tool_choice"] = options.ToolChoice
	}
	return s.enqueueCommand(command)
}

func (s *speechmaticsRealtimeSession) Say(text string) error {
	return s.enqueueCommand(map[string]any{
		"type": "response.create",
		"text": text,
	})
}

func (s *speechmaticsRealtimeSession) Truncate(llm.RealtimeTruncateOptions) error {
	return nil
}

func (s *speechmaticsRealtimeSession) Interrupt() error {
	return s.enqueueCommand(map[string]any{"type": "response.cancel"})
}

func (s *speechmaticsRealtimeSession) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.commandCh)
		s.mu.Unlock()
		close(s.eventCh)
		if s.owner != nil {
			s.owner.unregisterRealtimeSession(s)
		}
	})
	return nil
}

func (s *speechmaticsRealtimeSession) EventCh() <-chan llm.RealtimeEvent { return s.eventCh }

func (s *speechmaticsRealtimeSession) PushAudio(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return s.enqueueCommand(map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": append([]byte(nil), frame.Data...),
	})
}

func (s *speechmaticsRealtimeSession) PushVideo(*images.VideoFrame) error {
	if s.isClosed() {
		return nil
	}
	return speechmaticsRealtimeUnsupported("push_video")
}

func (s *speechmaticsRealtimeSession) CommitAudio() error {
	return s.enqueueCommand(map[string]any{"type": "input_audio_buffer.commit"})
}

func (s *speechmaticsRealtimeSession) ClearAudio() error {
	return s.enqueueCommand(map[string]any{"type": "input_audio_buffer.clear"})
}

func (s *speechmaticsRealtimeSession) enqueueCommand(command map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	select {
	case s.commandCh <- command:
		return nil
	default:
		return errors.New("speechmatics realtime command queue is full")
	}
}

func (s *speechmaticsRealtimeSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func speechmaticsRealtimeUnsupported(operation string) error {
	return errors.New(operation + " is not supported by the Speechmatics realtime model")
}

func speechmaticsRealtimeTools(tools []llm.Tool) ([]map[string]any, error) {
	payload := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			return nil, errors.New("speechmatics realtime tools received nil tool")
		}
		if _, ok := tool.(llm.ProviderTool); ok {
			continue
		}
		payload = append(payload, map[string]any{
			"type":        "function",
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  llm.ToolParameters(tool),
		})
	}
	return payload, nil
}

var _ llm.RealtimeModel = (*RealtimeModel)(nil)
var _ llm.RealtimeSession = (*speechmaticsRealtimeSession)(nil)
