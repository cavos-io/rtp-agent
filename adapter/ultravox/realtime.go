package ultravox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"

	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/gorilla/websocket"
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
	ultravoxGenerateReplyTimeout    = 5 * time.Second
	ultravoxClientBufferSizeMs      = 30000
	ultravoxClientEventQueueSize    = 2048
	ultravoxRealtimeEventQueueSize  = 2048
	ultravoxOutboundQueueSize       = 2048
	ultravoxFunctionCallQueueSize   = 2048
	ultravoxTextDeltaQueueSize      = 2048
	ultravoxOutputAudioQueueSize    = 2048
)

var ultravoxRealtimeMonotonicStart = time.Now()

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
	dialWebsocket       ultravoxRealtimeWebsocketDialer
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
		dialWebsocket:    defaultUltravoxRealtimeWebsocketDialer,
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
		m.systemPrompt = prompt
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
		m.languageHint = languageHint
		m.languageHintSet = true
	}
}

func WithRealtimeMaxDuration(maxDuration string) RealtimeOption {
	return func(m *RealtimeModel) {
		m.maxDuration = maxDuration
		m.maxDurationSet = true
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
		m.firstSpeaker = firstSpeaker
		m.firstSpeakerSet = true
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
			_ = session.updateOptions(llm.RealtimeSessionOptions{
				OutputMedium:    *update.outputMedium,
				OutputMediumSet: true,
			}, true)
		}
	}
}

func (m *RealtimeModel) Session() (llm.RealtimeSession, error) {
	session := &realtimeSession{
		eventCh:               make(chan llm.RealtimeEvent, ultravoxRealtimeEventQueueSize),
		audioCh:               make(chan []byte, 256),
		clientEventCh:         make(chan map[string]any, ultravoxClientEventQueueSize),
		outboundCh:            make(chan ultravoxRealtimeOutboundMessage, ultravoxOutboundQueueSize),
		model:                 m,
		inputSampleRate:       uint32(m.inputSampleRate),
		outputSampleRate:      uint32(m.outputSampleRate),
		audioOutput:           m.outputMedium == "voice",
		systemPrompt:          m.systemPrompt,
		generateReplyTimeout:  ultravoxGenerateReplyTimeout,
		recoverableErrorDelay: time.Second,
		audioStream:           coreaudio.NewAudioByteStream(uint32(m.inputSampleRate), ultravoxRealtimeInputChannels, uint32(m.inputSampleRate)/10),
		toolNames:             make(map[string]struct{}),
		toolResults:           make(map[string]struct{}),
		contextItems:          make(map[string]struct{}),
	}
	if m.outputMedium == "text" {
		event := map[string]any{
			"type":   "set_output_medium",
			"medium": "text",
		}
		session.mu.Lock()
		_ = session.enqueueClientEventLocked(event)
		session.mu.Unlock()
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
	responseID string
	createdAt  time.Time
	firstToken time.Time
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
	ToolName      string
	InvocationID  string
	Parameters    map[string]any
	RawParameters json.RawMessage
}

type ultravoxRealtimeServerEventEnvelope struct {
	Type string `json:"type"`
}

type ultravoxRealtimeOutboundMessage struct {
	Audio []byte
	Event map[string]any
}

const (
	ultravoxRealtimeWebsocketTextFrame   = websocket.TextMessage
	ultravoxRealtimeWebsocketBinaryFrame = websocket.BinaryMessage
)

type ultravoxRealtimeWebsocketWriter interface {
	WriteMessage(messageType int, data []byte) error
}

type ultravoxRealtimeWebsocketConn interface {
	ultravoxRealtimeWebsocketWriter
	ReadMessage() (messageType int, p []byte, err error)
	Close() error
}

type ultravoxRealtimeWebsocketDialer func(context.Context, string, http.Header) (ultravoxRealtimeWebsocketConn, error)

type ultravoxRealtimeHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type realtimeSession struct {
	mu                    sync.Mutex
	eventCh               chan llm.RealtimeEvent
	audioCh               chan []byte
	clientEventCh         chan map[string]any
	outboundCh            chan ultravoxRealtimeOutboundMessage
	model                 *RealtimeModel
	inputSampleRate       uint32
	outputSampleRate      uint32
	audioOutput           bool
	systemPrompt          string
	audioStream           *coreaudio.AudioByteStream
	inputNormalizer       ultravoxRealtimeInputNormalizer
	generation            *ultravoxRealtimeGeneration
	generationSeq         uint64
	tools                 []llm.Tool
	toolNames             map[string]struct{}
	toolResults           map[string]struct{}
	contextItems          map[string]struct{}
	pendingReply          bool
	pendingReplyAt        time.Time
	pendingReplySeq       uint64
	pendingReplyTimer     *time.Timer
	generateReplyTimeout  time.Duration
	recoverableErrorDelay time.Duration
	restartCount          uint64
	lastUserFinalAt       time.Time
	closed                bool
	closeOnce             sync.Once
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
	s.markRestartNeededLocked()
	s.mu.Unlock()
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
	nextTools := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			return errors.New("ultravox realtime update tools received nil tool")
		}
		nextTools = append(nextTools, tool)
		nextToolNames[tool.Name()] = struct{}{}
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if ultravoxRealtimeToolNameSetsEqual(s.toolNames, nextToolNames) {
		s.tools = nextTools
		s.mu.Unlock()
		return nil
	}
	s.tools = nextTools
	s.toolNames = nextToolNames
	s.markRestartNeededLocked()
	s.mu.Unlock()
	return nil
}
func (s *realtimeSession) UpdateOptions(options llm.RealtimeSessionOptions) error {
	return s.updateOptions(options, false)
}
func (s *realtimeSession) updateOptions(options llm.RealtimeSessionOptions, updateLocalModalities bool) error {
	if !options.OutputMediumSet {
		return nil
	}
	if err := s.sendClientEvent(map[string]any{
		"type":   "set_output_medium",
		"medium": options.OutputMedium,
	}); err != nil {
		return err
	}
	if !updateLocalModalities {
		return nil
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
	if err := s.sendClientEvent(map[string]any{
		"type":          "user_text_message",
		"text":          text,
		"deferResponse": false,
	}); err != nil {
		return err
	}
	s.mu.Lock()
	if !s.closed {
		s.pendingReply = true
		s.pendingReplyAt = time.Now()
		s.pendingReplySeq++
		s.startGenerateReplyTimerLocked(s.pendingReplySeq)
	}
	s.mu.Unlock()
	return nil
}
func (s *realtimeSession) Say(string) error {
	return errors.New("*ultravox.realtimeSession does not implement say(). use a TTS model instead")
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
	s.finishGeneration(generation, true)
	return err
}
func (s *realtimeSession) Close() error {
	var generation *ultravoxRealtimeGeneration
	s.closeOnce.Do(func() {
		s.mu.Lock()
		generation = s.generation
		s.closed = true
		s.generation = nil
		s.stopGenerateReplyTimerLocked()
		close(s.audioCh)
		close(s.clientEventCh)
		close(s.outboundCh)
		s.mu.Unlock()
		s.finishGeneration(generation, true)
		close(s.eventCh)
	})
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

	audioFrame, err := s.inputNormalizer.Normalize(frame, s.inputSampleRate)
	if err != nil {
		return nil
	}
	if audioFrame == nil || len(audioFrame.Data) == 0 {
		return nil
	}
	for _, chunk := range s.audioStream.Push(audioFrame.Data) {
		audioData := append([]byte(nil), chunk.Data...)
		s.enqueueAudioDataLocked(audioData)
	}
	return nil
}

func (s *realtimeSession) enqueueAudioDataLocked(audioData []byte) {
	select {
	case s.audioCh <- audioData:
		s.enqueueOutboundAudioLocked(audioData)
		return
	default:
	}
	nextCap := cap(s.audioCh) * 2
	if nextCap == 0 {
		nextCap = 1
	}
	next := make(chan []byte, nextCap)
	for {
		select {
		case queued := <-s.audioCh:
			next <- queued
		default:
			next <- audioData
			s.audioCh = next
			s.enqueueOutboundAudioLocked(audioData)
			return
		}
	}
}

func (s *realtimeSession) PushVideo(*images.VideoFrame) error {
	return nil
}
func (s *realtimeSession) CommitAudio() error {
	return nil
}
func (s *realtimeSession) ClearAudio() error {
	return nil
}

var _ llm.RealtimeSession = (*realtimeSession)(nil)

func (s *realtimeSession) createCallRequest() (string, map[string]string, map[string]any) {
	s.mu.Lock()
	tools := append([]llm.Tool(nil), s.tools...)
	systemPrompt := s.systemPrompt
	inputSampleRate := int(s.inputSampleRate)
	outputSampleRate := int(s.outputSampleRate)
	s.mu.Unlock()

	m := s.model
	createCallURL := strings.TrimRight(m.baseURL, "/") + "/calls"
	if enableGreeting, ok := m.EnableGreetingPrompt(); !ok || !enableGreeting {
		createCallURL += "?enableGreetingPrompt=false"
	}
	headers := map[string]string{
		"User-Agent":   "LiveKit Agents",
		"X-API-Key":    m.apiKey,
		"Content-Type": "application/json",
	}
	payload := map[string]any{
		"systemPrompt": systemPrompt,
		"model":        m.model,
		"voice":        m.voice,
		"medium": map[string]any{
			"serverWebSocket": map[string]any{
				"inputSampleRate":    inputSampleRate,
				"outputSampleRate":   outputSampleRate,
				"clientBufferSizeMs": ultravoxClientBufferSizeMs,
			},
		},
		"selectedTools": ultravoxRealtimeToolPayloads(tools),
	}
	if temperature, ok := m.Temperature(); ok {
		payload["temperature"] = temperature
	}
	if languageHint, ok := m.LanguageHint(); ok {
		payload["languageHint"] = languageHint
	}
	if maxDuration, ok := m.MaxDuration(); ok {
		payload["maxDuration"] = maxDuration
	}
	if timeExceededMessage, ok := m.TimeExceededMessage(); ok {
		payload["timeExceededMessage"] = timeExceededMessage
	}
	if firstSpeaker, ok := m.FirstSpeaker(); ok {
		payload["firstSpeaker"] = firstSpeaker
	}
	return createCallURL, headers, payload
}

func ultravoxRealtimeCreateCallJoinURL(data []byte) (string, error) {
	var response struct {
		JoinURL string `json:"joinUrl"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", err
	}
	if response.JoinURL == "" {
		return "", ultravoxRealtimeMissingJoinURLError
	}
	return response.JoinURL, nil
}

type ultravoxRealtimeConnectionError string

func (e ultravoxRealtimeConnectionError) Error() string { return string(e) }

const (
	ultravoxRealtimeMissingJoinURLError      ultravoxRealtimeConnectionError = "Ultravox call created, but no joinUrl received."
	ultravoxRealtimeUnexpectedWebsocketClose ultravoxRealtimeConnectionError = "Ultravox S2S connection closed unexpectedly"
)

var errUltravoxRealtimeReconnectAfterSend = errors.New("ultravox realtime reconnect after send error")

func (s *realtimeSession) createCall(ctx context.Context, client ultravoxRealtimeHTTPDoer) (string, error) {
	createCallURL, headers, payload := s.createCallRequest()
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, createCallURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		reason := http.StatusText(resp.StatusCode)
		if reason == "" {
			reason = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return "", llm.NewAPIError(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, reason), nil, false)
	}
	return ultravoxRealtimeCreateCallJoinURL(data)
}

func (s *realtimeSession) connectRealtimeWebsocket(ctx context.Context, client ultravoxRealtimeHTTPDoer) (ultravoxRealtimeWebsocketConn, error) {
	joinURL, err := s.createCall(ctx, client)
	if err != nil {
		return nil, err
	}
	return s.model.dialRealtimeWebsocket(ctx, joinURL)
}

func (s *realtimeSession) runRealtimeOnce(ctx context.Context, client ultravoxRealtimeHTTPDoer) error {
	conn, err := s.connectRealtimeWebsocket(ctx, client)
	if err != nil {
		return err
	}
	return s.runRealtimeConnection(conn)
}

func (s *realtimeSession) runRealtimeRestartLoop(ctx context.Context, client ultravoxRealtimeHTTPDoer) error {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil
		}
		restartCount := s.restartCount
		s.mu.Unlock()

		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.runRealtimeOnce(ctx, client); err != nil {
			if errors.Is(err, errUltravoxRealtimeReconnectAfterSend) {
				continue
			}
			recoverable := ultravoxRealtimeRecoverableError(err)
			s.emitRealtimeModelError(err, recoverable)
			if !recoverable {
				return nil
			}
			if err := s.waitRecoverableErrorDelay(ctx); err != nil {
				return err
			}
			continue
		}

		s.mu.Lock()
		closed := s.closed
		restarted := s.restartCount != restartCount
		s.mu.Unlock()
		if closed || !restarted {
			return nil
		}
	}
}

func (s *realtimeSession) waitRecoverableErrorDelay(ctx context.Context) error {
	delay := s.recoverableErrorDelay
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *realtimeSession) emitRealtimeModelError(err error, recoverable bool) {
	label := ""
	if s.model != nil {
		label = s.model.Label()
	}
	event := llm.RealtimeEvent{
		Type:  llm.RealtimeEventTypeError,
		Error: llm.NewRealtimeModelError(label, ultravoxRealtimeAPIError(err), recoverable),
	}
	select {
	case s.eventCh <- event:
	default:
	}
}

func ultravoxRealtimeRecoverableError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

func ultravoxRealtimeAPIError(err error) error {
	if err == nil {
		return nil
	}
	var apiError *llm.APIError
	if errors.As(err, &apiError) {
		return err
	}
	var connectionErr ultravoxRealtimeConnectionError
	if errors.As(err, &connectionErr) {
		return llm.NewAPIConnectionError(string(connectionErr))
	}
	return llm.NewAPIConnectionError("Connection failed: " + err.Error())
}

func (m *RealtimeModel) dialRealtimeWebsocket(ctx context.Context, joinURL string) (ultravoxRealtimeWebsocketConn, error) {
	return m.dialWebsocket(ctx, joinURL, http.Header{})
}

func defaultUltravoxRealtimeWebsocketDialer(ctx context.Context, endpoint string, headers http.Header) (ultravoxRealtimeWebsocketConn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func writeUltravoxRealtimeOutboundMessage(writer ultravoxRealtimeWebsocketWriter, message ultravoxRealtimeOutboundMessage) error {
	if len(message.Audio) > 0 {
		return writer.WriteMessage(ultravoxRealtimeWebsocketBinaryFrame, message.Audio)
	}
	if message.Event == nil {
		return nil
	}
	data, err := json.Marshal(message.Event)
	if err != nil {
		return err
	}
	return writer.WriteMessage(ultravoxRealtimeWebsocketTextFrame, data)
}

func (s *realtimeSession) sendOutboundMessages(writer ultravoxRealtimeWebsocketWriter) error {
	return s.sendOutboundMessagesWithContext(context.Background(), writer)
}

func (s *realtimeSession) sendOutboundMessagesWithContext(ctx context.Context, writer ultravoxRealtimeWebsocketWriter) error {
	s.mu.Lock()
	outboundCh := s.outboundCh
	restartCount := s.restartCount
	s.mu.Unlock()
	return s.sendOutboundMessagesFromContext(ctx, writer, outboundCh, restartCount)
}

func (s *realtimeSession) sendOutboundMessagesFrom(writer ultravoxRealtimeWebsocketWriter, outboundCh <-chan ultravoxRealtimeOutboundMessage, restartCount uint64) error {
	return s.sendOutboundMessagesFromContext(context.Background(), writer, outboundCh, restartCount)
}

func (s *realtimeSession) sendOutboundMessagesFromContext(ctx context.Context, writer ultravoxRealtimeWebsocketWriter, outboundCh <-chan ultravoxRealtimeOutboundMessage, restartCount uint64) error {
	for {
		var message ultravoxRealtimeOutboundMessage
		select {
		case <-ctx.Done():
			return nil
		case next, ok := <-outboundCh:
			if !ok {
				return nil
			}
			message = next
		}
		s.mu.Lock()
		restarted := s.restartCount != restartCount
		s.mu.Unlock()
		if restarted {
			return nil
		}
		if err := writeUltravoxRealtimeOutboundMessage(writer, message); err != nil {
			return errUltravoxRealtimeReconnectAfterSend
		}
	}
	return nil
}

func (s *realtimeSession) receiveRealtimeMessages(conn ultravoxRealtimeWebsocketConn) error {
	s.mu.Lock()
	restartCount := s.restartCount
	s.mu.Unlock()
	return s.receiveRealtimeMessagesFrom(conn, restartCount)
}

func (s *realtimeSession) receiveRealtimeMessagesFrom(conn ultravoxRealtimeWebsocketConn, restartCount uint64) error {
	for {
		s.mu.Lock()
		restarted := s.restartCount != restartCount
		s.mu.Unlock()
		if restarted {
			return nil
		}
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			if errors.Is(err, io.EOF) {
				return ultravoxRealtimeUnexpectedWebsocketClose
			}
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				return ultravoxRealtimeUnexpectedWebsocketClose
			}
			return err
		}
		switch messageType {
		case ultravoxRealtimeWebsocketTextFrame:
			if err := s.handleServerTextMessage(data); err != nil {
				return err
			}
		case ultravoxRealtimeWebsocketBinaryFrame:
			s.handleOutputAudio(data)
		}
	}
}

func (s *realtimeSession) runRealtimeConnection(conn ultravoxRealtimeWebsocketConn) error {
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sendErrCh := make(chan error, 1)
	receiveErrCh := make(chan error, 1)
	go func() {
		sendErrCh <- s.sendOutboundMessagesWithContext(ctx, conn)
	}()
	go func() {
		receiveErrCh <- s.receiveRealtimeMessages(conn)
	}()
	select {
	case err := <-sendErrCh:
		cancel()
		return err
	case err := <-receiveErrCh:
		cancel()
		return err
	}
}

func ultravoxRealtimeToolPayloads(tools []llm.Tool) []map[string]any {
	payloads := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		payloads = append(payloads, map[string]any{
			"temporaryTool": map[string]any{
				"modelToolName":     tool.Name(),
				"description":       tool.Description(),
				"dynamicParameters": ultravoxRealtimeToolDynamicParameters(tool),
				"client":            map[string]any{},
			},
		})
	}
	return payloads
}

func ultravoxRealtimeToolDynamicParameters(tool llm.Tool) []map[string]any {
	parameters := llm.ToolParameters(tool)
	properties, _ := parameters["properties"].(map[string]any)
	required := ultravoxRealtimeRequiredParameterSet(parameters["required"])

	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)

	dynamicParameters := make([]map[string]any, 0, len(names))
	for _, name := range names {
		prop, _ := properties[name].(map[string]any)
		schema := any(prop)
		if _, ok := prop["type"]; !ok {
			schemaMap := map[string]any{
				"type": ultravoxRealtimeParameterType(prop),
			}
			if description, ok := prop["description"]; ok && description != "" {
				schemaMap["description"] = description
			}
			schema = schemaMap
		}
		_, isRequired := required[name]
		dynamicParameters = append(dynamicParameters, map[string]any{
			"name":     name,
			"location": "PARAMETER_LOCATION_BODY",
			"schema":   schema,
			"required": isRequired,
		})
	}
	return dynamicParameters
}

func ultravoxRealtimeRequiredParameterSet(required any) map[string]struct{} {
	set := make(map[string]struct{})
	switch values := required.(type) {
	case []string:
		for _, value := range values {
			set[value] = struct{}{}
		}
	case []any:
		for _, value := range values {
			if name, ok := value.(string); ok {
				set[name] = struct{}{}
			}
		}
	}
	return set
}

func ultravoxRealtimeParameterType(prop map[string]any) string {
	if typ, ok := prop["type"].(string); ok {
		return typ
	}
	if _, ok := prop["enum"]; ok {
		return "string"
	}
	if _, ok := prop["items"]; ok {
		return "array"
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if typ := ultravoxRealtimeVariantType(prop[key]); typ != "" {
			return typ
		}
	}
	return "string"
}

func ultravoxRealtimeVariantType(value any) string {
	switch variants := value.(type) {
	case []any:
		for _, variant := range variants {
			if schema, ok := variant.(map[string]any); ok {
				if typ, ok := schema["type"].(string); ok {
					return typ
				}
			}
		}
	case []map[string]any:
		for _, schema := range variants {
			if typ, ok := schema["type"].(string); ok {
				return typ
			}
		}
	}
	return ""
}

func (s *realtimeSession) sendClientEvent(event map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	return s.enqueueClientEventLocked(event)
}

func (s *realtimeSession) enqueueClientEventLocked(event map[string]any) error {
	select {
	case s.clientEventCh <- event:
		s.enqueueOutboundEventLocked(event)
		return nil
	default:
	}
	nextCap := cap(s.clientEventCh) * 2
	if nextCap == 0 {
		nextCap = 1
	}
	next := make(chan map[string]any, nextCap)
	for {
		select {
		case queued := <-s.clientEventCh:
			next <- queued
		default:
			next <- event
			s.clientEventCh = next
			s.enqueueOutboundEventLocked(event)
			return nil
		}
	}
}

func (s *realtimeSession) markRestartNeededLocked() {
	s.restartCount++
	s.pendingReply = false
	s.pendingReplyAt = time.Time{}
	s.pendingReplySeq++
	s.stopGenerateReplyTimerLocked()
	close(s.clientEventCh)
	s.clientEventCh = make(chan map[string]any, cap(s.clientEventCh))
	close(s.outboundCh)
	s.outboundCh = make(chan ultravoxRealtimeOutboundMessage, cap(s.outboundCh))
	if !s.audioOutput {
		_ = s.enqueueClientEventLocked(map[string]any{
			"type":   "set_output_medium",
			"medium": "text",
		})
	}
	close(s.audioCh)
	s.audioCh = make(chan []byte, cap(s.audioCh))
}

func (s *realtimeSession) enqueueOutboundAudioLocked(audioData []byte) {
	s.enqueueOutboundMessageLocked(ultravoxRealtimeOutboundMessage{Audio: append([]byte(nil), audioData...)})
}

func (s *realtimeSession) enqueueOutboundEventLocked(event map[string]any) {
	s.enqueueOutboundMessageLocked(ultravoxRealtimeOutboundMessage{Event: cloneUltravoxRealtimeMap(event)})
}

func (s *realtimeSession) enqueueOutboundMessageLocked(message ultravoxRealtimeOutboundMessage) {
	select {
	case s.outboundCh <- message:
		return
	default:
	}
	nextCap := cap(s.outboundCh) * 2
	if nextCap == 0 {
		nextCap = 1
	}
	next := make(chan ultravoxRealtimeOutboundMessage, nextCap)
	for {
		select {
		case queued := <-s.outboundCh:
			next <- queued
		default:
			next <- message
			s.outboundCh = next
			return
		}
	}
}

func cloneUltravoxRealtimeMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
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
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	generation := s.ensureGenerationLocked()
	if ultravoxRealtimeAudioHasSignal(audioData) && generation.firstToken.IsZero() {
		generation.firstToken = time.Now()
	}
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
		return nil
	}

	switch envelope.Type {
	case "transcript":
		var event struct {
			Role    string `json:"role"`
			Medium  string `json:"medium"`
			Text    string `json:"text"`
			Delta   string `json:"delta"`
			Final   *bool  `json:"final"`
			Ordinal *int   `json:"ordinal"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return nil
		}
		if !ultravoxRealtimeValidTranscriptEnvelope(event.Role, event.Medium, event.Final, event.Ordinal) {
			return nil
		}
		s.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
			Role:    event.Role,
			Text:    event.Text,
			Delta:   event.Delta,
			Final:   *event.Final,
			Ordinal: *event.Ordinal,
		})
	case "state":
		var event struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return nil
		}
		s.handleStateEvent(ultravoxRealtimeStateEvent{State: event.State})
	case "client_tool_invocation":
		var event struct {
			ToolName      string          `json:"toolName"`
			InvocationID  string          `json:"invocationId"`
			RawParameters json.RawMessage `json:"parameters"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return nil
		}
		if event.ToolName == "" || event.InvocationID == "" || len(event.RawParameters) == 0 {
			return nil
		}
		parameters := map[string]any{}
		if err := json.Unmarshal(event.RawParameters, &parameters); err != nil {
			return nil
		}
		if parameters == nil {
			return nil
		}
		s.handleToolInvocationEvent(ultravoxRealtimeToolInvocationEvent{
			ToolName:      event.ToolName,
			InvocationID:  event.InvocationID,
			Parameters:    parameters,
			RawParameters: event.RawParameters,
		})
	case "playback_clear_buffer":
		s.handlePlaybackClearBufferEvent()
	case "pong":
		var event struct {
			Timestamp *float64 `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return nil
		}
		if event.Timestamp == nil {
			return nil
		}
		s.handlePongEvent(*event.Timestamp)
	case "call_started", "debug":
		return nil
	default:
		return nil
	}
	return nil
}

func ultravoxRealtimeValidTranscriptEnvelope(role string, medium string, final *bool, ordinal *int) bool {
	if final == nil || ordinal == nil {
		return false
	}
	if role != "user" && role != "agent" {
		return false
	}
	return medium == "text" || medium == "voice"
}

func (s *realtimeSession) ensureGenerationLocked() *ultravoxRealtimeGeneration {
	return s.ensureGenerationLockedWithPending(true)
}

func (s *realtimeSession) ensureGenerationLockedWithPending(consumePendingReply bool) *ultravoxRealtimeGeneration {
	if s.generation != nil && !s.generation.done {
		return s.generation
	}
	generation := &ultravoxRealtimeGeneration{
		messageCh:  make(chan llm.MessageGeneration, 1),
		functionCh: make(chan *llm.FunctionCall, ultravoxFunctionCallQueueSize),
		textCh:     make(chan string, ultravoxTextDeltaQueueSize),
		audioCh:    make(chan *model.AudioFrame, ultravoxOutputAudioQueueSize),
		createdAt:  s.pickGenerationCreatedAtLocked(),
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
	generation.responseID = messageID
	userInitiated := false
	if consumePendingReply {
		userInitiated = s.pendingReply && !s.pendingReplyAt.IsZero() && time.Since(s.pendingReplyAt) <= ultravoxGenerateReplyTimeout
		s.pendingReply = false
		s.pendingReplyAt = time.Time{}
		s.pendingReplySeq++
		s.stopGenerateReplyTimerLocked()
	}
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
			MessageCh:     generation.messageCh,
			FunctionCh:    generation.functionCh,
			ResponseID:    messageID,
			UserInitiated: userInitiated,
		},
	}
	select {
	case s.eventCh <- event:
	default:
	}
	return generation
}

func (s *realtimeSession) startGenerateReplyTimerLocked(seq uint64) {
	s.stopGenerateReplyTimerLocked()
	timeout := s.generateReplyTimeout
	if timeout <= 0 {
		timeout = ultravoxGenerateReplyTimeout
	}
	s.pendingReplyTimer = time.AfterFunc(timeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed || s.pendingReplySeq != seq {
			return
		}
		s.pendingReply = false
		s.pendingReplyAt = time.Time{}
	})
}

func (s *realtimeSession) stopGenerateReplyTimerLocked() {
	if s.pendingReplyTimer == nil {
		return
	}
	s.pendingReplyTimer.Stop()
	s.pendingReplyTimer = nil
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
	if event.Final {
		s.lastUserFinalAt = time.Now()
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

func (s *realtimeSession) pickGenerationCreatedAtLocked() time.Time {
	now := time.Now()
	if !s.lastUserFinalAt.IsZero() {
		since := now.Sub(s.lastUserFinalAt)
		if since >= 0 && since <= 10*time.Second {
			return s.lastUserFinalAt
		}
	}
	return now
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
		if generation.firstToken.IsZero() {
			generation.firstToken = time.Now()
		}
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
		s.finishGeneration(generation, false)
	}
}

func (s *realtimeSession) finishGeneration(generation *ultravoxRealtimeGeneration, cancelled bool) {
	var metrics *telemetry.RealtimeModelMetrics
	s.mu.Lock()
	if generation == nil || generation.done {
		s.mu.Unlock()
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
	metrics = s.generationMetricsLocked(generation, cancelled)
	s.mu.Unlock()
	if metrics == nil {
		return
	}
	select {
	case s.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeMetricsCollected, Metrics: metrics}:
	default:
	}
}

func (s *realtimeSession) generationMetricsLocked(generation *ultravoxRealtimeGeneration, cancelled bool) *telemetry.RealtimeModelMetrics {
	if generation == nil || (generation.firstToken.IsZero() && generation.outputText.Len() == 0) {
		return nil
	}
	now := time.Now()
	createdAt := generation.createdAt
	if createdAt.IsZero() {
		createdAt = now
	}
	ttft := -1.0
	if !generation.firstToken.IsZero() {
		ttft = generation.firstToken.Sub(createdAt).Seconds()
		if ttft < 0 {
			ttft = 0
		}
	}
	modelName := ""
	modelProvider := ""
	label := ""
	if s.model != nil {
		modelName = s.model.model
		modelProvider = s.model.Provider()
		label = s.model.Label()
	}
	return &telemetry.RealtimeModelMetrics{
		Timestamp: createdAt,
		RequestID: generation.responseID,
		Label:     label,
		TTFT:      ttft,
		Duration:  now.Sub(createdAt).Seconds(),
		Cancelled: cancelled,
		Metadata: &telemetry.Metadata{
			ModelName:     modelName,
			ModelProvider: modelProvider,
		},
	}
}

func ultravoxRealtimeAudioHasSignal(audioData []byte) bool {
	for _, b := range audioData {
		if b != 0 {
			return true
		}
	}
	return false
}

func (s *realtimeSession) handleStateEvent(event ultravoxRealtimeStateEvent) {
	switch event.State {
	case "listening":
		s.mu.Lock()
		generation := s.generation
		s.mu.Unlock()
		s.finishGeneration(generation, true)
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
	arguments, err := ultravoxRealtimeToolArguments(event.Parameters, event.RawParameters)
	if err != nil {
		arguments = "{}"
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	generation := s.ensureGenerationLockedWithPending(false)
	functionCall := &llm.FunctionCall{
		CallID:    event.InvocationID,
		Name:      event.ToolName,
		Arguments: arguments,
	}
	s.mu.Unlock()

	select {
	case generation.functionCh <- functionCall:
	default:
	}
	s.finishGeneration(generation, true)
}

func ultravoxRealtimeToolArguments(parameters map[string]any, rawParameters json.RawMessage) (string, error) {
	if len(rawParameters) > 0 {
		return ultravoxRealtimePythonJSONSpacingFromRaw(rawParameters)
	}
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(parameters); err != nil {
		return "", err
	}
	return ultravoxRealtimePythonJSONSpacing(strings.TrimSpace(b.String())), nil
}

func ultravoxRealtimePythonJSONSpacingFromRaw(raw json.RawMessage) (string, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	return ultravoxRealtimePythonJSONSpacingRaw(string(raw)), nil
}

func ultravoxRealtimePythonJSONSpacingRaw(value string) string {
	var out strings.Builder
	out.Grow(len(value) + 8)
	inString := false
	escaped := false
	unicodeEscapeRemaining := 0
	for _, r := range value {
		if unicodeEscapeRemaining > 0 {
			out.WriteRune(unicode.ToLower(r))
			unicodeEscapeRemaining--
			continue
		}
		if !inString && (r == ' ' || r == '\n' || r == '\r' || r == '\t') {
			continue
		}
		if inString && !escaped && r > 0x7f {
			writeUltravoxRealtimePythonJSONUnicodeEscape(&out, r)
			continue
		}
		out.WriteRune(r)
		if escaped {
			if r == 'u' {
				unicodeEscapeRemaining = 4
			}
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if !inString && (r == ':' || r == ',') {
			out.WriteByte(' ')
		}
	}
	return out.String()
}

func writeUltravoxRealtimePythonJSONUnicodeEscape(out *strings.Builder, r rune) {
	if r <= 0xffff {
		fmt.Fprintf(out, "\\u%04x", r)
		return
	}
	high, low := utf16.EncodeRune(r)
	fmt.Fprintf(out, "\\u%04x\\u%04x", high, low)
}

func ultravoxRealtimePythonJSONSpacing(value string) string {
	var out strings.Builder
	out.Grow(len(value) + 8)
	inString := false
	escaped := false
	for _, r := range value {
		out.WriteRune(r)
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if !inString && (r == ':' || r == ',') {
			out.WriteByte(' ')
		}
	}
	return out.String()
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
		"timestamp": time.Since(ultravoxRealtimeMonotonicStart).Seconds(),
	})
}

type ultravoxRealtimeInputNormalizer struct {
	inputRate       uint32
	outputRate      uint32
	bufferStart     uint64
	totalInput      uint64
	nextOutputNumer uint64
	mono            []byte
	lastParticipant string
}

func (n *ultravoxRealtimeInputNormalizer) Normalize(frame *model.AudioFrame, sampleRate uint32) (*model.AudioFrame, error) {
	if frame == nil || sampleRate == 0 || len(frame.Data) == 0 {
		return nil, nil
	}
	if frame.SampleRate == 0 {
		return nil, errors.New("ultravox realtime audio input has zero sample rate")
	}
	mono, samplesPerChannel, err := ultravoxRealtimeDownmixInput(frame)
	if err != nil {
		return nil, err
	}
	if samplesPerChannel == 0 {
		return nil, nil
	}
	if frame.SampleRate == sampleRate {
		n.Reset()
		return &model.AudioFrame{
			Data:              mono,
			SampleRate:        sampleRate,
			NumChannels:       ultravoxRealtimeInputChannels,
			SamplesPerChannel: samplesPerChannel,
			ParticipantID:     frame.ParticipantID,
		}, nil
	}
	if n.inputRate != frame.SampleRate || n.outputRate != sampleRate {
		n.Reset()
		n.inputRate = frame.SampleRate
		n.outputRate = sampleRate
	}
	n.lastParticipant = frame.ParticipantID
	n.mono = append(n.mono, mono...)
	n.totalInput += uint64(samplesPerChannel)

	output := make([]byte, 0, int(uint64(samplesPerChannel)*uint64(sampleRate)/uint64(frame.SampleRate)+1)*2)
	for {
		sourceIndex := n.nextOutputNumer / uint64(n.outputRate)
		if sourceIndex >= n.totalInput {
			break
		}
		relative := sourceIndex - n.bufferStart
		offset := int(relative) * 2
		if offset+2 > len(n.mono) {
			break
		}
		output = append(output, n.mono[offset:offset+2]...)
		n.nextOutputNumer += uint64(n.inputRate)
	}
	n.dropConsumed()
	if len(output) == 0 {
		return nil, nil
	}
	return &model.AudioFrame{
		Data:              output,
		SampleRate:        sampleRate,
		NumChannels:       ultravoxRealtimeInputChannels,
		SamplesPerChannel: uint32(len(output) / 2),
		ParticipantID:     n.lastParticipant,
	}, nil
}

func (n *ultravoxRealtimeInputNormalizer) Reset() {
	n.inputRate = 0
	n.outputRate = 0
	n.bufferStart = 0
	n.totalInput = 0
	n.nextOutputNumer = 0
	n.mono = nil
	n.lastParticipant = ""
}

func (n *ultravoxRealtimeInputNormalizer) dropConsumed() {
	if n.outputRate == 0 {
		return
	}
	nextSource := n.nextOutputNumer / uint64(n.outputRate)
	if nextSource <= n.bufferStart {
		return
	}
	dropSamples := nextSource - n.bufferStart
	dropBytes := int(dropSamples) * 2
	if dropBytes > len(n.mono) {
		dropBytes = len(n.mono)
		dropSamples = uint64(dropBytes / 2)
	}
	n.mono = append([]byte(nil), n.mono[dropBytes:]...)
	n.bufferStart += dropSamples
}

func ultravoxRealtimeDownmixInput(frame *model.AudioFrame) ([]byte, uint32, error) {
	if len(frame.Data)%2 != 0 {
		return nil, 0, errors.New("ultravox realtime audio input must be 16-bit PCM")
	}
	if frame.NumChannels == 0 {
		return nil, 0, errors.New("ultravox realtime audio input has zero channels")
	}
	channels := int(frame.NumChannels)
	samplesPerChannel := int(frame.SamplesPerChannel)
	if samplesPerChannel == 0 {
		samplesPerChannel = (len(frame.Data) / 2) / channels
	}
	expectedBytes := samplesPerChannel * channels * 2
	if len(frame.Data) < expectedBytes {
		return nil, 0, errors.New("ultravox realtime audio input is shorter than declared sample count")
	}

	mono := make([]byte, samplesPerChannel*2)
	if channels == ultravoxRealtimeInputChannels {
		copy(mono, frame.Data[:expectedBytes])
		return mono, uint32(samplesPerChannel), nil
	}
	for sample := 0; sample < samplesPerChannel; sample++ {
		var sum int32
		for channel := 0; channel < channels; channel++ {
			offset := (sample*channels + channel) * 2
			sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset:])))
		}
		binary.LittleEndian.PutUint16(mono[sample*2:], uint16(int16(sum/int32(channels))))
	}
	return mono, uint32(samplesPerChannel), nil
}
