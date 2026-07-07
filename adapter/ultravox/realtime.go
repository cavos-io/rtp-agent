package ultravox

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

const (
	defaultRealtimeBaseURL          = "https://api.ultravox.ai/api"
	defaultRealtimeModel            = "fixie-ai/ultravox"
	defaultRealtimeVoice            = "Mark"
	defaultRealtimeSystemPrompt     = "You are a helpful assistant."
	defaultRealtimeInputSampleRate  = 16000
	defaultRealtimeOutputSampleRate = 24000
	defaultRealtimeOutputMedium     = "voice"
	defaultRealtimeFirstSpeaker     = "FIRST_SPEAKER_USER"
	ultravoxRealtimeInputChannels   = 1
)

type RealtimeModel struct {
	apiKey              string
	model               string
	voice               string
	baseURL             string
	systemPrompt        string
	outputMedium        string
	inputSampleRate     int
	outputSampleRate    int
	temperature         float64
	temperatureSet      bool
	languageHint        string
	languageHintSet     bool
	maxDuration         string
	maxDurationSet      bool
	timeExceededMessage string
	timeExceededSet     bool
	enableGreeting      bool
	enableGreetingSet   bool
	firstSpeaker        string
	firstSpeakerSet     bool
	sessionsMu          sync.Mutex
	sessions            map[*realtimeSession]struct{}
}

type RealtimeOption func(*RealtimeModel)
type RealtimeUpdateOption func(*realtimeUpdateOptions)

type realtimeUpdateOptions struct {
	outputMedium *string
}

func NewRealtimeModel(apiKey string, opts ...RealtimeOption) (*RealtimeModel, error) {
	if apiKey == "" {
		apiKey = os.Getenv("ULTRAVOX_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("ultravox API key is required. Provide it via api_key parameter or ULTRAVOX_API_KEY environment variable")
	}
	model := &RealtimeModel{
		apiKey:           apiKey,
		model:            defaultRealtimeModel,
		voice:            defaultRealtimeVoice,
		baseURL:          defaultRealtimeBaseURL,
		systemPrompt:     defaultRealtimeSystemPrompt,
		outputMedium:     defaultRealtimeOutputMedium,
		inputSampleRate:  defaultRealtimeInputSampleRate,
		outputSampleRate: defaultRealtimeOutputSampleRate,
		firstSpeaker:     defaultRealtimeFirstSpeaker,
		firstSpeakerSet:  true,
	}
	for _, opt := range opts {
		opt(model)
	}
	return model, nil
}

func WithRealtimeModel(model string) RealtimeOption {
	return func(m *RealtimeModel) {
		if model != "" {
			m.model = model
		}
	}
}

func WithRealtimeVoice(voice string) RealtimeOption {
	return func(m *RealtimeModel) {
		if voice != "" {
			m.voice = voice
		}
	}
}

func WithRealtimeBaseURL(baseURL string) RealtimeOption {
	return func(m *RealtimeModel) {
		if baseURL != "" {
			m.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithRealtimeSystemPrompt(prompt string) RealtimeOption {
	return func(m *RealtimeModel) {
		if prompt != "" {
			m.systemPrompt = prompt
		}
	}
}

func WithRealtimeOutputMedium(outputMedium string) RealtimeOption {
	return func(m *RealtimeModel) {
		if outputMedium != "" {
			m.outputMedium = outputMedium
		}
	}
}

func WithRealtimeInputSampleRate(sampleRate int) RealtimeOption {
	return func(m *RealtimeModel) {
		if sampleRate > 0 {
			m.inputSampleRate = sampleRate
		}
	}
}

func WithRealtimeOutputSampleRate(sampleRate int) RealtimeOption {
	return func(m *RealtimeModel) {
		if sampleRate > 0 {
			m.outputSampleRate = sampleRate
		}
	}
}

func WithRealtimeTemperature(temperature float64) RealtimeOption {
	return func(m *RealtimeModel) {
		m.temperature = temperature
		m.temperatureSet = true
	}
}

func WithRealtimeLanguageHint(languageHint string) RealtimeOption {
	return func(m *RealtimeModel) {
		if languageHint != "" {
			m.languageHint = languageHint
			m.languageHintSet = true
		}
	}
}

func WithRealtimeMaxDuration(maxDuration string) RealtimeOption {
	return func(m *RealtimeModel) {
		if maxDuration != "" {
			m.maxDuration = maxDuration
			m.maxDurationSet = true
		}
	}
}

func WithRealtimeTimeExceededMessage(message string) RealtimeOption {
	return func(m *RealtimeModel) {
		if message != "" {
			m.timeExceededMessage = message
			m.timeExceededSet = true
		}
	}
}

func WithRealtimeEnableGreetingPrompt(enable bool) RealtimeOption {
	return func(m *RealtimeModel) {
		m.enableGreeting = enable
		m.enableGreetingSet = true
	}
}

func WithRealtimeFirstSpeaker(firstSpeaker string) RealtimeOption {
	return func(m *RealtimeModel) {
		if firstSpeaker != "" {
			m.firstSpeaker = firstSpeaker
			m.firstSpeakerSet = true
		}
	}
}

func WithRealtimeUpdateOutputMedium(outputMedium string) RealtimeUpdateOption {
	return func(opts *realtimeUpdateOptions) {
		if outputMedium != "" {
			opts.outputMedium = &outputMedium
		}
	}
}

func (m *RealtimeModel) Label() string { return "ultravox-" + m.model }
func (m *RealtimeModel) Model() string { return m.model }
func (m *RealtimeModel) Provider() string {
	return "Ultravox"
}
func (m *RealtimeModel) APIKey() string               { return m.apiKey }
func (m *RealtimeModel) Voice() string                { return m.voice }
func (m *RealtimeModel) BaseURL() string              { return m.baseURL }
func (m *RealtimeModel) SystemPrompt() string         { return m.systemPrompt }
func (m *RealtimeModel) OutputMedium() string         { return m.outputMedium }
func (m *RealtimeModel) InputSampleRate() int         { return m.inputSampleRate }
func (m *RealtimeModel) OutputSampleRate() int        { return m.outputSampleRate }
func (m *RealtimeModel) Temperature() (float64, bool) { return m.temperature, m.temperatureSet }
func (m *RealtimeModel) LanguageHint() (string, bool) { return m.languageHint, m.languageHintSet }
func (m *RealtimeModel) MaxDuration() (string, bool)  { return m.maxDuration, m.maxDurationSet }
func (m *RealtimeModel) TimeExceededMessage() (string, bool) {
	return m.timeExceededMessage, m.timeExceededSet
}
func (m *RealtimeModel) EnableGreetingPrompt() (bool, bool) {
	return m.enableGreeting, m.enableGreetingSet
}
func (m *RealtimeModel) FirstSpeaker() (string, bool) { return m.firstSpeaker, m.firstSpeakerSet }

func (m *RealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{
		MessageTruncation:       true,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             m.outputMedium == "voice",
		ManualFunctionCalls:     false,
		PerResponseToolChoice:   false,
	}
}

func (m *RealtimeModel) UpdateOptions(opts ...RealtimeUpdateOption) {
	var update realtimeUpdateOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&update)
		}
	}
	if update.outputMedium != nil {
		m.outputMedium = *update.outputMedium
		sessions := m.realtimeSessions()
		for _, session := range sessions {
			_ = session.UpdateOptions(llm.RealtimeSessionOptions{
				OutputMedium:    *update.outputMedium,
				OutputMediumSet: true,
			})
		}
	}
}

func (m *RealtimeModel) Session() (llm.RealtimeSession, error) {
	session := &realtimeSession{
		eventCh:          make(chan llm.RealtimeEvent, 16),
		audioCh:          make(chan []byte, 256),
		clientEventCh:    make(chan map[string]any, 256),
		model:            m,
		inputSampleRate:  uint32(m.inputSampleRate),
		outputSampleRate: uint32(m.outputSampleRate),
		audioOutput:      m.outputMedium == "voice",
		systemPrompt:     m.systemPrompt,
		audioStream:      coreaudio.NewAudioByteStream(uint32(m.inputSampleRate), ultravoxRealtimeInputChannels, uint32(m.inputSampleRate)/10),
		toolNames:        make(map[string]struct{}),
		toolResults:      make(map[string]struct{}),
		contextItems:     make(map[string]struct{}),
	}
	if m.outputMedium == "text" {
		session.clientEventCh <- map[string]any{
			"type":   "set_output_medium",
			"medium": "text",
		}
	}
	m.registerRealtimeSession(session)
	return session, nil
}

func (m *RealtimeModel) Close() error { return nil }

func (m *RealtimeModel) registerRealtimeSession(session *realtimeSession) {
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()
	if m.sessions == nil {
		m.sessions = make(map[*realtimeSession]struct{})
	}
	m.sessions[session] = struct{}{}
}

func (m *RealtimeModel) unregisterRealtimeSession(session *realtimeSession) {
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()
	delete(m.sessions, session)
}

func (m *RealtimeModel) realtimeSessions() []*realtimeSession {
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()
	sessions := make([]*realtimeSession, 0, len(m.sessions))
	for session := range m.sessions {
		sessions = append(sessions, session)
	}
	return sessions
}

type ultravoxRealtimeGeneration struct {
	messageCh  chan llm.MessageGeneration
	functionCh chan *llm.FunctionCall
	textCh     chan string
	audioCh    chan *model.AudioFrame
	outputText strings.Builder
	done       bool
}

type ultravoxRealtimeTranscriptEvent struct {
	Role    string
	Text    string
	Delta   string
	Final   bool
	Ordinal int
}

type ultravoxRealtimeStateEvent struct {
	State string
}

type ultravoxRealtimeToolInvocationEvent struct {
	ToolName     string
	InvocationID string
	Parameters   map[string]any
}

type ultravoxRealtimeServerEventEnvelope struct {
	Type string `json:"type"`
}

type realtimeSession struct {
	mu               sync.Mutex
	eventCh          chan llm.RealtimeEvent
	audioCh          chan []byte
	clientEventCh    chan map[string]any
	model            *RealtimeModel
	inputSampleRate  uint32
	outputSampleRate uint32
	audioOutput      bool
	systemPrompt     string
	audioStream      *coreaudio.AudioByteStream
	generation       *ultravoxRealtimeGeneration
	generationSeq    uint64
	toolNames        map[string]struct{}
	toolResults      map[string]struct{}
	contextItems     map[string]struct{}
	restartCount     uint64
	closed           bool
	closeOnce        sync.Once
}

func (s *realtimeSession) UpdateInstructions(instructions string) error {
	s.mu.Lock()
	if s.closed || s.systemPrompt == instructions {
		s.mu.Unlock()
		return nil
	}
	s.systemPrompt = instructions
	if s.model != nil {
		s.model.systemPrompt = instructions
	}
	generation := s.markRestartNeededLocked()
	s.mu.Unlock()
	s.finishGeneration(generation)
	return nil
}
func (s *realtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	if chatCtx == nil {
		return nil
	}
	nextContextItems := make(map[string]struct{})
	nextToolResults := make(map[string]struct{})
	for _, item := range chatCtx.Items {
		switch item := item.(type) {
		case *llm.ChatMessage:
			if key := ultravoxRealtimeChatMessageKey(item); key != "" {
				nextContextItems[key] = struct{}{}
			}
		case *llm.FunctionCallOutput:
			if key := ultravoxRealtimeToolResultKey(item); key != "" {
				nextToolResults[key] = struct{}{}
			}
		}
	}
	for _, item := range chatCtx.Items {
		switch item := item.(type) {
		case *llm.ChatMessage:
			if err := s.sendChatContextMessage(item); err != nil {
				return err
			}
		case *llm.FunctionCallOutput:
			if err := s.sendToolResult(item); err != nil {
				return err
			}
		}
	}
	s.mu.Lock()
	if !s.closed {
		s.contextItems = nextContextItems
		s.toolResults = nextToolResults
	}
	s.mu.Unlock()
	return nil
}

func (s *realtimeSession) sendChatContextMessage(message *llm.ChatMessage) error {
	if message == nil {
		return nil
	}
	text := message.TextContent()
	if text == "" {
		return nil
	}

	eventText := ""
	switch message.Role {
	case llm.ChatRoleSystem, llm.ChatRoleDeveloper:
		eventText = "<instruction>" + text + "</instruction>"
	case llm.ChatRoleUser:
		eventText = text
	default:
		return nil
	}

	key := ultravoxRealtimeChatMessageKey(message)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if _, ok := s.contextItems[key]; ok {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if err := s.sendClientEvent(map[string]any{
		"type":          "user_text_message",
		"text":          eventText,
		"deferResponse": true,
	}); err != nil {
		return err
	}
	s.mu.Lock()
	if !s.closed {
		s.contextItems[key] = struct{}{}
	}
	s.mu.Unlock()
	return nil
}

func (s *realtimeSession) sendToolResult(output *llm.FunctionCallOutput) error {
	if output == nil || output.CallID == "" {
		return nil
	}
	key := ultravoxRealtimeToolResultKey(output)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if _, ok := s.toolResults[key]; ok {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	event := map[string]any{
		"type":          "client_tool_result",
		"invocationId":  output.CallID,
		"agentReaction": "speaks",
		"responseType":  "tool-response",
	}
	if output.IsError {
		event["errorType"] = "implementation-error"
		event["errorMessage"] = output.Output
	} else {
		event["result"] = output.Output
	}
	if err := s.sendClientEvent(event); err != nil {
		return err
	}
	s.mu.Lock()
	if !s.closed {
		s.toolResults[key] = struct{}{}
	}
	s.mu.Unlock()
	return nil
}

func ultravoxRealtimeChatMessageKey(message *llm.ChatMessage) string {
	if message == nil {
		return ""
	}
	if message.ID != "" {
		return message.ID
	}
	return string(message.Role) + ":" + message.TextContent()
}

func ultravoxRealtimeToolResultKey(output *llm.FunctionCallOutput) string {
	if output == nil {
		return ""
	}
	if output.ID != "" {
		return output.ID
	}
	return output.CallID
}

func (s *realtimeSession) UpdateTools(tools []llm.Tool) error {
	nextToolNames := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool == nil {
			return errors.New("ultravox realtime update tools received nil tool")
		}
		nextToolNames[tool.Name()] = struct{}{}
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if ultravoxRealtimeToolNameSetsEqual(s.toolNames, nextToolNames) {
		s.mu.Unlock()
		return nil
	}
	s.toolNames = nextToolNames
	generation := s.markRestartNeededLocked()
	s.mu.Unlock()
	s.finishGeneration(generation)
	return nil
}
func (s *realtimeSession) UpdateOptions(options llm.RealtimeSessionOptions) error {
	if !options.OutputMediumSet {
		return nil
	}
	if err := s.sendClientEvent(map[string]any{
		"type":   "set_output_medium",
		"medium": options.OutputMedium,
	}); err != nil {
		return err
	}
	s.mu.Lock()
	if options.OutputMedium == "voice" {
		s.audioOutput = true
	} else if options.OutputMedium == "text" {
		s.audioOutput = false
	}
	s.mu.Unlock()
	return nil
}
func (s *realtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	text := ""
	if options.InstructionsSet {
		text = "<instruction>" + options.Instructions + "</instruction>"
	}
	return s.sendClientEvent(map[string]any{
		"type":          "user_text_message",
		"text":          text,
		"deferResponse": false,
	})
}
func (s *realtimeSession) Say(string) error {
	return ultravoxRealtimeSessionUnsupported("say")
}
func (s *realtimeSession) Truncate(llm.RealtimeTruncateOptions) error { return nil }
func (s *realtimeSession) Interrupt() error {
	s.mu.Lock()
	generation := s.generation
	if s.closed || generation == nil || generation.done {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	err := s.sendClientEvent(map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"urgency":       "immediate",
		"deferResponse": true,
	})
	s.finishGeneration(generation)
	return err
}
func (s *realtimeSession) Close() error {
	var generation *ultravoxRealtimeGeneration
	s.closeOnce.Do(func() {
		s.mu.Lock()
		generation = s.generation
		s.closed = true
		s.generation = nil
		close(s.eventCh)
		close(s.audioCh)
		close(s.clientEventCh)
		s.mu.Unlock()
	})
	s.finishGeneration(generation)
	if s.model != nil {
		s.model.unregisterRealtimeSession(s)
	}
	return nil
}
func (s *realtimeSession) EventCh() <-chan llm.RealtimeEvent { return s.eventCh }
func (s *realtimeSession) PushAudio(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}

	audioFrame, err := ultravoxRealtimeInputAudioFrame(frame, s.inputSampleRate)
	if err != nil {
		return err
	}
	if audioFrame == nil || len(audioFrame.Data) == 0 {
		return nil
	}
	for _, chunk := range s.audioStream.Push(audioFrame.Data) {
		audioData := append([]byte(nil), chunk.Data...)
		select {
		case s.audioCh <- audioData:
		default:
			return errors.New("ultravox realtime audio queue is full")
		}
	}
	return nil
}
func (s *realtimeSession) PushVideo(*images.VideoFrame) error {
	return ultravoxRealtimeSessionUnsupported("push_video")
}
func (s *realtimeSession) CommitAudio() error {
	return nil
}
func (s *realtimeSession) ClearAudio() error {
	return nil
}

var _ llm.RealtimeSession = (*realtimeSession)(nil)

func ultravoxRealtimeSessionUnsupported(operation string) error {
	return errors.New(operation + " is not implemented by the Ultravox realtime session")
}

func (s *realtimeSession) sendClientEvent(event map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	select {
	case s.clientEventCh <- event:
		return nil
	default:
		return errors.New("ultravox realtime client event queue is full")
	}
}

func (s *realtimeSession) markRestartNeededLocked() *ultravoxRealtimeGeneration {
	s.restartCount++
	generation := s.generation
	s.generation = nil
	return generation
}

func ultravoxRealtimeToolNameSetsEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for name := range a {
		if _, ok := b[name]; !ok {
			return false
		}
	}
	return true
}

func (s *realtimeSession) handleOutputAudio(audioData []byte) {
	if len(audioData) == 0 {
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	generation := s.ensureGenerationLocked()
	frame := &model.AudioFrame{
		Data:              append([]byte(nil), audioData...),
		SampleRate:        s.outputSampleRate,
		NumChannels:       ultravoxRealtimeInputChannels,
		SamplesPerChannel: uint32(len(audioData)) / (2 * ultravoxRealtimeInputChannels),
	}
	s.mu.Unlock()

	select {
	case generation.audioCh <- frame:
	default:
	}
}

func (s *realtimeSession) handleServerTextMessage(data []byte) error {
	var envelope ultravoxRealtimeServerEventEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}

	switch envelope.Type {
	case "transcript":
		var event struct {
			Role    string `json:"role"`
			Text    string `json:"text"`
			Delta   string `json:"delta"`
			Final   bool   `json:"final"`
			Ordinal int    `json:"ordinal"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		s.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
			Role:    event.Role,
			Text:    event.Text,
			Delta:   event.Delta,
			Final:   event.Final,
			Ordinal: event.Ordinal,
		})
	case "state":
		var event struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		s.handleStateEvent(ultravoxRealtimeStateEvent{State: event.State})
	case "client_tool_invocation":
		var event struct {
			ToolName     string         `json:"toolName"`
			InvocationID string         `json:"invocationId"`
			Parameters   map[string]any `json:"parameters"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		if event.Parameters == nil {
			event.Parameters = map[string]any{}
		}
		s.handleToolInvocationEvent(ultravoxRealtimeToolInvocationEvent{
			ToolName:     event.ToolName,
			InvocationID: event.InvocationID,
			Parameters:   event.Parameters,
		})
	case "playback_clear_buffer":
		s.handlePlaybackClearBufferEvent()
	case "pong":
		var event struct {
			Timestamp float64 `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		s.handlePongEvent(event.Timestamp)
	case "call_started", "debug":
		return nil
	default:
		return fmt.Errorf("unhandled Ultravox event type %q", envelope.Type)
	}
	return nil
}

func (s *realtimeSession) ensureGenerationLocked() *ultravoxRealtimeGeneration {
	if s.generation != nil && !s.generation.done {
		return s.generation
	}
	generation := &ultravoxRealtimeGeneration{
		messageCh:  make(chan llm.MessageGeneration, 1),
		functionCh: make(chan *llm.FunctionCall, 1),
		textCh:     make(chan string, 16),
		audioCh:    make(chan *model.AudioFrame, 16),
	}
	modalitiesCh := make(chan []string, 1)
	if s.audioOutput {
		modalitiesCh <- []string{"audio", "text"}
	} else {
		modalitiesCh <- []string{"text"}
	}
	close(modalitiesCh)
	s.generationSeq++
	messageID := fmt.Sprintf("ultravox-turn-%d", s.generationSeq)
	generation.messageCh <- llm.MessageGeneration{
		MessageID:    messageID,
		TextCh:       generation.textCh,
		AudioCh:      generation.audioCh,
		ModalitiesCh: modalitiesCh,
	}
	s.generation = generation
	event := llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:  generation.messageCh,
			FunctionCh: generation.functionCh,
		},
	}
	select {
	case s.eventCh <- event:
	default:
	}
	return generation
}

func (s *realtimeSession) handleTranscriptEvent(event ultravoxRealtimeTranscriptEvent) {
	switch event.Role {
	case "user":
		s.handleUserTranscriptEvent(event)
	case "agent":
		s.handleAgentTranscriptEvent(event)
	}
}

func (s *realtimeSession) handleUserTranscriptEvent(event ultravoxRealtimeTranscriptEvent) {
	if event.Text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	realtimeEvent := llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     fmt.Sprintf("msg_user_%d", event.Ordinal),
			Transcript: event.Text,
			IsFinal:    event.Final,
		},
	}
	select {
	case s.eventCh <- realtimeEvent:
	default:
	}
}

func (s *realtimeSession) handleAgentTranscriptEvent(event ultravoxRealtimeTranscriptEvent) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	generation := s.ensureGenerationLocked()
	incrementalText := event.Delta
	if incrementalText == "" && !event.Final {
		incrementalText = event.Text
	}
	if incrementalText != "" {
		generation.outputText.WriteString(incrementalText)
	}
	final := event.Final
	s.mu.Unlock()

	if incrementalText != "" {
		select {
		case generation.textCh <- incrementalText:
		default:
		}
	}
	if final {
		s.finishGeneration(generation)
	}
}

func (s *realtimeSession) finishGeneration(generation *ultravoxRealtimeGeneration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation == nil || generation.done {
		return
	}
	generation.done = true
	close(generation.textCh)
	close(generation.audioCh)
	close(generation.functionCh)
	close(generation.messageCh)
	if s.generation == generation {
		s.generation = nil
	}
}

func (s *realtimeSession) handleStateEvent(event ultravoxRealtimeStateEvent) {
	switch event.State {
	case "listening":
		s.mu.Lock()
		generation := s.generation
		s.mu.Unlock()
		s.finishGeneration(generation)
	case "thinking":
		s.mu.Lock()
		if !s.closed && (s.generation == nil || s.generation.done) {
			s.ensureGenerationLocked()
		}
		s.mu.Unlock()
	case "speaking":
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		if s.generation == nil || s.generation.done {
			s.ensureGenerationLocked()
		}
		event := llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeSpeechStopped,
			SpeechStopped: &llm.InputSpeechStoppedEvent{
				UserTranscriptionEnabled: false,
			},
		}
		s.mu.Unlock()
		select {
		case s.eventCh <- event:
		default:
		}
	}
}

func (s *realtimeSession) handleToolInvocationEvent(event ultravoxRealtimeToolInvocationEvent) {
	arguments, err := json.Marshal(event.Parameters)
	if err != nil {
		arguments = []byte("{}")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	generation := s.ensureGenerationLocked()
	functionCall := &llm.FunctionCall{
		CallID:    event.InvocationID,
		Name:      event.ToolName,
		Arguments: string(arguments),
	}
	s.mu.Unlock()

	select {
	case generation.functionCh <- functionCall:
	default:
	}
	s.finishGeneration(generation)
}

func (s *realtimeSession) handlePlaybackClearBufferEvent() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted}:
	default:
	}
}

func (s *realtimeSession) handlePongEvent(float64) {
	_ = s.sendClientEvent(map[string]any{
		"type":      "ping",
		"timestamp": float64(time.Now().UnixNano()) / float64(time.Second),
	})
}

func ultravoxRealtimeInputAudioFrame(frame *model.AudioFrame, sampleRate uint32) (*model.AudioFrame, error) {
	resampled, err := coreaudio.ResampleAudioFrame(frame, sampleRate)
	if err != nil {
		return nil, err
	}
	if resampled == nil || resampled.NumChannels <= ultravoxRealtimeInputChannels {
		return resampled, nil
	}
	if len(resampled.Data)%2 != 0 {
		return nil, errors.New("ultravox realtime audio input must be 16-bit PCM")
	}
	channels := int(resampled.NumChannels)
	samplesPerChannel := int(resampled.SamplesPerChannel)
	if samplesPerChannel == 0 {
		samplesPerChannel = (len(resampled.Data) / 2) / channels
	}
	expectedBytes := samplesPerChannel * channels * 2
	if len(resampled.Data) < expectedBytes {
		return nil, errors.New("ultravox realtime audio input is shorter than declared sample count")
	}

	mono := make([]byte, samplesPerChannel*2)
	for sample := 0; sample < samplesPerChannel; sample++ {
		var sum int32
		for channel := 0; channel < channels; channel++ {
			offset := (sample*channels + channel) * 2
			sum += int32(int16(binary.LittleEndian.Uint16(resampled.Data[offset:])))
		}
		binary.LittleEndian.PutUint16(mono[sample*2:], uint16(int16(sum/int32(channels))))
	}
	return &model.AudioFrame{
		Data:              mono,
		SampleRate:        sampleRate,
		NumChannels:       ultravoxRealtimeInputChannels,
		SamplesPerChannel: uint32(samplesPerChannel),
		ParticipantID:     resampled.ParticipantID,
	}, nil
}
