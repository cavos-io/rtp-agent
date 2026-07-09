package speechmatics

import (
	"encoding/base64"
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
func (m *RealtimeModel) APIKey() string   { return m.apiKey }
func (m *RealtimeModel) BaseURL() string  { return m.baseURL }
func (m *RealtimeModel) Voice() string    { return m.voice }

func (m *RealtimeModel) InputSampleRate() int {
	if m == nil {
		return 0
	}
	return m.inputSampleRate
}

func (m *RealtimeModel) OutputSampleRate() int {
	if m == nil {
		return 0
	}
	return m.outputSampleRate
}

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
		closedCh:         make(chan struct{}),
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
	closedCh         chan struct{}
	pendingCommands  []map[string]any
	owner            *RealtimeModel
	apiKey           string
	baseURL          string
	model            string
	voice            string
	instructions     string
	inputSampleRate  int
	outputSampleRate int
	closed           bool
	drainingCommands bool
	closeOnce        sync.Once
	generation       *speechmaticsRealtimeGeneration
}

type speechmaticsRealtimeGeneration struct {
	responseID string
	messageCh  chan llm.MessageGeneration
	functionCh chan *llm.FunctionCall
	messages   map[string]*speechmaticsRealtimeMessage
	done       bool
}

type speechmaticsRealtimeMessage struct {
	textCh       chan string
	audioCh      chan *model.AudioFrame
	modalitiesCh chan []string
	closed       bool
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
		s.pendingCommands = nil
		s.closeCurrentGenerationLocked()
		close(s.closedCh)
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
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if len(s.pendingCommands) == 0 {
		select {
		case s.commandCh <- command:
			s.mu.Unlock()
			return nil
		default:
		}
	}
	s.pendingCommands = append(s.pendingCommands, command)
	if !s.drainingCommands {
		s.drainingCommands = true
		go s.drainCommandQueue()
	}
	s.mu.Unlock()
	return nil
}

func (s *speechmaticsRealtimeSession) handleServerEvent(event map[string]any) bool {
	eventType, _ := event["type"].(string)
	switch eventType {
	case "input_audio_buffer.speech_started":
		return s.emitRealtimeEvent(llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted})
	case "input_audio_buffer.speech_stopped":
		return s.emitRealtimeEvent(llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeSpeechStopped,
			SpeechStopped: &llm.InputSpeechStoppedEvent{
				UserTranscriptionEnabled: true,
			},
		})
	case "conversation.item.input_audio_transcription.delta":
		itemID := speechmaticsRealtimeString(event, "item_id")
		delta := speechmaticsRealtimeString(event, "delta")
		if delta == "" {
			return false
		}
		return s.emitRealtimeEvent(llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:       itemID,
				ContentIndex: speechmaticsRealtimeInt(event, "content_index"),
				Transcript:   delta,
				IsFinal:      false,
			},
		})
	case "conversation.item.input_audio_transcription.completed":
		return s.emitRealtimeEvent(llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:       speechmaticsRealtimeString(event, "item_id"),
				ContentIndex: speechmaticsRealtimeInt(event, "content_index"),
				Transcript:   speechmaticsRealtimeString(event, "transcript"),
				IsFinal:      true,
			},
		})
	case "response.created":
		return s.handleResponseCreated(event)
	case "response.output_item.added":
		return s.handleResponseOutputItemAdded(event)
	case "response.output_text.delta", "response.text.delta", "response.output_audio_transcript.delta", "response.audio_transcript.delta":
		return s.handleResponseTextDelta(event)
	case "response.output_audio.delta", "response.audio.delta":
		return s.handleResponseAudioDelta(event)
	case "response.output_item.done":
		return s.handleResponseOutputItemDone(event)
	case "response.done":
		s.mu.Lock()
		s.closeCurrentGenerationLocked()
		s.mu.Unlock()
		return true
	}
	return false
}

func (s *speechmaticsRealtimeSession) handleResponseCreated(event map[string]any) bool {
	responseID := speechmaticsRealtimeString(event, "response_id")
	if responseID == "" {
		if response, _ := event["response"].(map[string]any); response != nil {
			responseID = speechmaticsRealtimeString(response, "id")
		}
	}
	if responseID == "" {
		responseID = "speechmatics-response"
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.closeCurrentGenerationLocked()
	generation := &speechmaticsRealtimeGeneration{
		responseID: responseID,
		messageCh:  make(chan llm.MessageGeneration, 1),
		functionCh: make(chan *llm.FunctionCall, 1),
		messages:   make(map[string]*speechmaticsRealtimeMessage),
	}
	s.generation = generation
	s.mu.Unlock()
	return s.emitRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:  generation.messageCh,
			FunctionCh: generation.functionCh,
			ResponseID: responseID,
		},
	})
}

func (s *speechmaticsRealtimeSession) handleResponseOutputItemAdded(event map[string]any) bool {
	itemID := speechmaticsRealtimeString(event, "item_id")
	if itemID == "" {
		if item, _ := event["item"].(map[string]any); item != nil {
			itemID = speechmaticsRealtimeString(item, "id")
		}
	}
	if itemID == "" {
		return false
	}
	s.mu.Lock()
	generation := s.generation
	if s.closed || generation == nil || generation.done {
		s.mu.Unlock()
		return false
	}
	message := &speechmaticsRealtimeMessage{
		textCh:       make(chan string, 16),
		audioCh:      make(chan *model.AudioFrame, 16),
		modalitiesCh: make(chan []string, 1),
	}
	message.modalitiesCh <- []string{"audio", "text"}
	close(message.modalitiesCh)
	generation.messages[itemID] = message
	s.mu.Unlock()
	select {
	case generation.messageCh <- llm.MessageGeneration{
		MessageID:    itemID,
		TextCh:       message.textCh,
		AudioCh:      message.audioCh,
		ModalitiesCh: message.modalitiesCh,
	}:
	default:
	}
	return true
}

func (s *speechmaticsRealtimeSession) handleResponseTextDelta(event map[string]any) bool {
	itemID := speechmaticsRealtimeString(event, "item_id")
	delta := speechmaticsRealtimeString(event, "delta")
	if itemID == "" || delta == "" {
		return false
	}
	s.mu.Lock()
	message := s.realtimeMessageLocked(itemID)
	s.mu.Unlock()
	if message == nil {
		return false
	}
	select {
	case message.textCh <- delta:
	default:
	}
	return true
}

func (s *speechmaticsRealtimeSession) handleResponseAudioDelta(event map[string]any) bool {
	itemID := speechmaticsRealtimeString(event, "item_id")
	encoded := speechmaticsRealtimeString(event, "delta")
	if itemID == "" || encoded == "" {
		return false
	}
	audio, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	s.mu.Lock()
	message := s.realtimeMessageLocked(itemID)
	sampleRate := s.outputSampleRate
	s.mu.Unlock()
	if message == nil {
		return false
	}
	select {
	case message.audioCh <- &model.AudioFrame{
		Data:              append([]byte(nil), audio...),
		SampleRate:        uint32(sampleRate),
		NumChannels:       1,
		SamplesPerChannel: uint32(len(audio) / 2),
	}:
	default:
	}
	return true
}

func (s *speechmaticsRealtimeSession) handleResponseOutputItemDone(event map[string]any) bool {
	itemID := speechmaticsRealtimeString(event, "item_id")
	if itemID == "" {
		if item, _ := event["item"].(map[string]any); item != nil {
			itemID = speechmaticsRealtimeString(item, "id")
		}
	}
	if itemID == "" {
		return false
	}
	s.mu.Lock()
	message := s.realtimeMessageLocked(itemID)
	if message != nil && !message.closed {
		message.closed = true
		close(message.textCh)
		close(message.audioCh)
	}
	s.mu.Unlock()
	return message != nil
}

func (s *speechmaticsRealtimeSession) realtimeMessageLocked(itemID string) *speechmaticsRealtimeMessage {
	if s.closed || s.generation == nil || s.generation.done {
		return nil
	}
	return s.generation.messages[itemID]
}

func (s *speechmaticsRealtimeSession) closeCurrentGenerationLocked() {
	if s.generation == nil || s.generation.done {
		return
	}
	generation := s.generation
	generation.done = true
	for _, message := range generation.messages {
		if message != nil && !message.closed {
			message.closed = true
			close(message.textCh)
			close(message.audioCh)
		}
	}
	close(generation.messageCh)
	close(generation.functionCh)
	s.generation = nil
}

func (s *speechmaticsRealtimeSession) emitRealtimeEvent(event llm.RealtimeEvent) bool {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return false
	}
	select {
	case s.eventCh <- event:
		return true
	default:
		return false
	}
}

func speechmaticsRealtimeString(event map[string]any, key string) string {
	value, _ := event[key].(string)
	return value
}

func speechmaticsRealtimeInt(event map[string]any, key string) int {
	switch value := event[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}

func (s *speechmaticsRealtimeSession) drainCommandQueue() {
	for {
		s.mu.Lock()
		if s.closed {
			s.drainingCommands = false
			s.mu.Unlock()
			return
		}
		if len(s.pendingCommands) == 0 {
			s.drainingCommands = false
			s.mu.Unlock()
			return
		}
		command := s.pendingCommands[0]
		s.mu.Unlock()

		select {
		case s.commandCh <- command:
		case <-s.closedCh:
			return
		}

		s.mu.Lock()
		if len(s.pendingCommands) > 0 {
			copy(s.pendingCommands, s.pendingCommands[1:])
			s.pendingCommands[len(s.pendingCommands)-1] = nil
			s.pendingCommands = s.pendingCommands[:len(s.pendingCommands)-1]
		}
		s.mu.Unlock()
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
