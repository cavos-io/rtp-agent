package speechmatics

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/gorilla/websocket"
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
	dialWebsocket    SpeechmaticsRealtimeWebsocketDialer
	connect          llm.APIConnectOptions
	disableWebsocket bool
	closed           bool
}

type SpeechmaticsRealtimeWebsocketDialer func(string, http.Header) (*websocket.Conn, *http.Response, error)

type speechmaticsRealtimeDialResult struct {
	conn *websocket.Conn
	err  error
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
		dialWebsocket:    defaultSpeechmaticsRealtimeWebsocketDialer,
		connect:          llm.DefaultAPIConnectOptions(),
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

func WithRealtimeWebsocketDialer(dialer SpeechmaticsRealtimeWebsocketDialer) RealtimeOption {
	return func(m *RealtimeModel) {
		if dialer != nil {
			m.dialWebsocket = dialer
			m.disableWebsocket = false
		}
	}
}

func WithRealtimeConnectOptions(options llm.APIConnectOptions) RealtimeOption {
	return func(m *RealtimeModel) {
		m.connect = options
	}
}

func WithRealtimeWebsocketDisabled() RealtimeOption {
	return func(m *RealtimeModel) {
		m.disableWebsocket = true
	}
}

func (m *RealtimeModel) Label() string    { return "speechmatics.RealtimeModel" }
func (m *RealtimeModel) Model() string    { return m.model }
func (m *RealtimeModel) Provider() string { return "Speechmatics" }
func (m *RealtimeModel) APIKey() string   { return m.apiKey }
func (m *RealtimeModel) BaseURL() string  { return m.baseURL }
func (m *RealtimeModel) Voice() string    { return m.voice }

func defaultSpeechmaticsRealtimeWebsocketDialer(endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.Dial(endpoint, headers)
}

func buildSpeechmaticsRealtimeHeaders(apiKey string) http.Header {
	header := http.Header{}
	header.Add("User-Agent", "LiveKit Agents")
	header.Add("Authorization", "Bearer "+apiKey)
	return header
}

func buildSpeechmaticsRealtimeWebsocketURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	return u.String()
}

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
		dialWebsocket:    m.dialWebsocket,
		connect:          m.connect,
		disableWebsocket: m.disableWebsocket,
	}
	if m.sessions == nil {
		m.sessions = make(map[*speechmaticsRealtimeSession]struct{})
	}
	m.sessions[session] = struct{}{}
	m.mu.Unlock()

	initialCommand := map[string]any{
		"type":               "session.create",
		"model":              session.model,
		"voice":              session.voice,
		"instructions":       session.instructions,
		"input_sample_rate":  session.inputSampleRate,
		"output_sample_rate": session.outputSampleRate,
	}
	if !session.disableWebsocket {
		if err := session.connectRealtimeWebsocket(initialCommand); err != nil {
			m.unregisterRealtimeSession(session)
			return nil, err
		}
		return session, nil
	}
	session.enqueueCommand(initialCommand)
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
	tools            []map[string]any
	chatCommands     []map[string]any
	inputSampleRate  int
	outputSampleRate int
	dialWebsocket    SpeechmaticsRealtimeWebsocketDialer
	connect          llm.APIConnectOptions
	disableWebsocket bool
	ctx              context.Context
	cancel           context.CancelFunc
	conn             *websocket.Conn
	reconnecting     bool
	closed           bool
	drainingCommands bool
	closeOnce        sync.Once
	generation       *speechmaticsRealtimeGeneration
	inputTranscripts map[string]map[int]string
}

type speechmaticsRealtimeGeneration struct {
	responseID string
	messageCh  chan llm.MessageGeneration
	functionCh chan *llm.FunctionCall
	messages   map[string]*speechmaticsRealtimeMessage
	createdAt  time.Time
	firstToken time.Time
	done       bool
}

type speechmaticsRealtimeMessage struct {
	textCh       chan string
	timedTextCh  chan llm.RealtimeTimedText
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
	commands := make([]map[string]any, 0, len(chatCtx.Items))
	for _, item := range chatCtx.Items {
		switch item := item.(type) {
		case *llm.ChatMessage:
			text := item.TextContent()
			if text == "" {
				continue
			}
			commands = append(commands, map[string]any{
				"type": "conversation.item.create",
				"role": string(item.Role),
				"text": text,
			})
		case *llm.FunctionCallOutput:
			commands = append(commands, map[string]any{
				"type":     "function_call_output",
				"call_id":  item.CallID,
				"output":   item.Output,
				"is_error": item.IsError,
			})
		}
	}
	s.mu.Lock()
	s.chatCommands = cloneSpeechmaticsRealtimeCommands(commands)
	s.mu.Unlock()
	for _, command := range commands {
		if err := s.enqueueCommand(command); err != nil {
			return err
		}
	}
	return nil
}

func (s *speechmaticsRealtimeSession) UpdateTools(tools []llm.Tool) error {
	payload, err := speechmaticsRealtimeTools(tools)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.tools = cloneSpeechmaticsRealtimeTools(payload)
	s.mu.Unlock()
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
		cancel := s.cancel
		conn := s.conn
		close(s.closedCh)
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if conn != nil {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			_ = conn.Close()
		}
		close(s.eventCh)
		if s.owner != nil {
			s.owner.unregisterRealtimeSession(s)
		}
	})
	return nil
}

func (s *speechmaticsRealtimeSession) EventCh() <-chan llm.RealtimeEvent { return s.eventCh }

func (s *speechmaticsRealtimeSession) connectRealtimeWebsocket(initialCommand map[string]any) error {
	if s.dialWebsocket == nil {
		s.dialWebsocket = defaultSpeechmaticsRealtimeWebsocketDialer
	}
	conn, err := s.dialRealtimeWebsocket(context.Background(), buildSpeechmaticsRealtimeWebsocketURL(s.baseURL), buildSpeechmaticsRealtimeHeaders(s.apiKey))
	if err != nil {
		var timeoutErr *llm.APITimeoutError
		if errors.As(err, &timeoutErr) {
			return timeoutErr
		}
		return llm.NewAPIConnectionError(fmt.Sprintf("failed to connect to Speechmatics realtime: %v", err))
	}
	if err := conn.WriteJSON(initialCommand); err != nil {
		_ = conn.Close()
		return llm.NewAPIConnectionError(fmt.Sprintf("failed to initialize Speechmatics realtime session: %v", err))
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.ctx = ctx
	s.cancel = cancel
	s.conn = conn
	s.mu.Unlock()
	go s.websocketWriteLoop()
	go s.websocketReadLoop(conn)
	return nil
}

func (s *speechmaticsRealtimeSession) dialRealtimeWebsocket(ctx context.Context, endpoint string, header http.Header) (*websocket.Conn, error) {
	var err error
	for attempt := 0; attempt <= s.connect.MaxRetry; attempt++ {
		var conn *websocket.Conn
		conn, err = s.dialRealtimeWebsocketAttempt(ctx, endpoint, header)
		if err == nil {
			return conn, nil
		}
		if attempt == s.connect.MaxRetry {
			return nil, err
		}
		interval := s.connect.IntervalForRetry(attempt)
		if interval <= 0 {
			continue
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, err
}

func (s *speechmaticsRealtimeSession) dialRealtimeWebsocketAttempt(ctx context.Context, endpoint string, header http.Header) (*websocket.Conn, error) {
	attemptCtx := ctx
	cancel := func() {}
	if s.connect.Timeout > 0 {
		attemptCtx, cancel = context.WithTimeout(ctx, s.connect.Timeout)
	} else if ctx != nil {
		attemptCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	resultCh := make(chan speechmaticsRealtimeDialResult, 1)
	go func() {
		conn, _, err := s.dialWebsocket(endpoint, header)
		select {
		case resultCh <- speechmaticsRealtimeDialResult{conn: conn, err: err}:
		case <-attemptCtx.Done():
			if conn != nil {
				_ = conn.Close()
			}
		}
	}()
	select {
	case result := <-resultCh:
		return result.conn, result.err
	case <-attemptCtx.Done():
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError("Speechmatics realtime connection timed out")
		}
		return nil, attemptCtx.Err()
	}
}

func (s *speechmaticsRealtimeSession) websocketWriteLoop() {
	for {
		select {
		case <-s.closedCh:
			return
		case command := <-s.commandCh:
			if command == nil {
				continue
			}
			conn := s.currentRealtimeConn()
			if conn == nil {
				continue
			}
			if err := conn.WriteJSON(command); err != nil {
				s.reconnectRealtimeWebsocket()
			}
		}
	}
}

func (s *speechmaticsRealtimeSession) websocketReadLoop(conn *websocket.Conn) {
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if s.isClosed() {
				return
			}
			go s.reconnectRealtimeWebsocket()
			return
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		s.handleServerJSON(data)
	}
}

func (s *speechmaticsRealtimeSession) currentRealtimeConn() *websocket.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

func (s *speechmaticsRealtimeSession) reconnectRealtimeWebsocket() bool {
	s.mu.Lock()
	if s.closed || s.reconnecting {
		s.mu.Unlock()
		return false
	}
	s.reconnecting = true
	oldConn := s.conn
	s.mu.Unlock()
	if oldConn != nil {
		_ = oldConn.Close()
	}

	conn, err := s.dialRealtimeWebsocket(context.Background(), buildSpeechmaticsRealtimeWebsocketURL(s.baseURL), buildSpeechmaticsRealtimeHeaders(s.apiKey))
	if err != nil {
		s.mu.Lock()
		s.reconnecting = false
		s.mu.Unlock()
		s.emitRealtimeEvent(llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: llm.NewAPIConnectionError(fmt.Sprintf("Speechmatics realtime reconnect failed: %v", err)),
		})
		_ = s.Close()
		return false
	}
	if err := conn.WriteJSON(s.sessionCreateCommand()); err != nil {
		s.mu.Lock()
		s.reconnecting = false
		s.mu.Unlock()
		_ = conn.Close()
		s.emitRealtimeEvent(llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: llm.NewAPIConnectionError(fmt.Sprintf("failed to reinitialize Speechmatics realtime session: %v", err)),
		})
		_ = s.Close()
		return false
	}
	for _, command := range s.reconnectSessionCommands() {
		if err := conn.WriteJSON(command); err != nil {
			s.mu.Lock()
			s.reconnecting = false
			s.mu.Unlock()
			_ = conn.Close()
			s.emitRealtimeEvent(llm.RealtimeEvent{
				Type:  llm.RealtimeEventTypeError,
				Error: llm.NewAPIConnectionError(fmt.Sprintf("failed to restore Speechmatics realtime session: %v", err)),
			})
			_ = s.Close()
			return false
		}
	}

	s.mu.Lock()
	s.conn = conn
	s.reconnecting = false
	s.inputTranscripts = nil
	s.closeCurrentGenerationLocked()
	s.mu.Unlock()
	go s.websocketReadLoop(conn)
	s.emitRealtimeEvent(llm.RealtimeEvent{
		Type:      llm.RealtimeEventTypeSessionReconnected,
		Reconnect: &llm.RealtimeSessionReconnectedEvent{},
	})
	return true
}

func (s *speechmaticsRealtimeSession) reconnectSessionCommands() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	commands := make([]map[string]any, 0, 1+len(s.chatCommands))
	if len(s.tools) > 0 {
		commands = append(commands, map[string]any{
			"type":  "session.update",
			"tools": cloneSpeechmaticsRealtimeTools(s.tools),
		})
	}
	commands = append(commands, cloneSpeechmaticsRealtimeCommands(s.chatCommands)...)
	return commands
}

func (s *speechmaticsRealtimeSession) sessionCreateCommand() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"type":               "session.create",
		"model":              s.model,
		"voice":              s.voice,
		"instructions":       s.instructions,
		"input_sample_rate":  s.inputSampleRate,
		"output_sample_rate": s.outputSampleRate,
	}
}

func cloneSpeechmaticsRealtimeTools(tools []map[string]any) []map[string]any {
	if tools == nil {
		return nil
	}
	cloned := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		next := make(map[string]any, len(tool))
		for key, value := range tool {
			next[key] = value
		}
		cloned = append(cloned, next)
	}
	return cloned
}

func cloneSpeechmaticsRealtimeCommands(commands []map[string]any) []map[string]any {
	if commands == nil {
		return nil
	}
	cloned := make([]map[string]any, 0, len(commands))
	for _, command := range commands {
		next := make(map[string]any, len(command))
		for key, value := range command {
			next[key] = value
		}
		cloned = append(cloned, next)
	}
	return cloned
}

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

func (s *speechmaticsRealtimeSession) handleServerJSON(data []byte) bool {
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		return false
	}
	return s.handleServerEvent(event)
}

func (s *speechmaticsRealtimeSession) handleServerEvent(event map[string]any) bool {
	eventType, _ := event["type"].(string)
	switch eventType {
	case "error":
		return s.handleProviderError(event)
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
		return s.handleInputTranscriptDelta(event)
	case "conversation.item.input_audio_transcription.completed":
		return s.handleInputTranscriptCompleted(event)
	case "conversation.item.input_audio_transcription.failed":
		return s.handleInputTranscriptFailed(event)
	case "conversation.item.added", "conversation.item.created":
		return s.handleConversationItemAdded(event)
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
		return s.handleResponseDone(event)
	}
	return false
}

func (s *speechmaticsRealtimeSession) handleConversationItemAdded(event map[string]any) bool {
	item, _ := event["item"].(map[string]any)
	if item == nil {
		return false
	}
	chatItem, ok := speechmaticsRealtimeChatItem(item)
	if !ok {
		return false
	}
	previousItemID, previousItemIDSet := event["previous_item_id"].(string)
	return s.emitRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeRemoteItemAdded,
		RemoteItem: &llm.RemoteItemAddedEvent{
			PreviousItemID:    previousItemID,
			PreviousItemIDSet: previousItemIDSet,
			Item:              chatItem,
		},
	})
}

func speechmaticsRealtimeChatItem(item map[string]any) (llm.ChatItem, bool) {
	switch speechmaticsRealtimeString(item, "type") {
	case "message":
		return speechmaticsRealtimeChatMessage(item)
	case "function_call":
		return speechmaticsRealtimeFunctionCallItem(item)
	case "function_call_output":
		return speechmaticsRealtimeFunctionCallOutputItem(item)
	default:
		return nil, false
	}
}

func speechmaticsRealtimeChatMessage(item map[string]any) (*llm.ChatMessage, bool) {
	roleRaw := speechmaticsRealtimeString(item, "role")
	role := llm.ChatRole(roleRaw)
	switch role {
	case llm.ChatRoleSystem, llm.ChatRoleDeveloper, llm.ChatRoleUser, llm.ChatRoleAssistant:
	default:
		return nil, false
	}
	contents, ok := item["content"].([]any)
	if !ok {
		return nil, false
	}
	return &llm.ChatMessage{
		ID:      speechmaticsRealtimeString(item, "id"),
		Role:    role,
		Content: speechmaticsRealtimeChatContent(role, contents),
	}, true
}

func speechmaticsRealtimeChatContent(role llm.ChatRole, contents []any) []llm.ChatContent {
	out := make([]llm.ChatContent, 0, len(contents))
	for _, content := range contents {
		part, ok := content.(map[string]any)
		if !ok {
			continue
		}
		switch speechmaticsRealtimeString(part, "type") {
		case "input_text", "output_text":
			text, hasText := part["text"].(string)
			if text != "" || (hasText && role == llm.ChatRoleUser) {
				out = append(out, llm.ChatContent{Text: text})
			}
		case "input_audio":
			transcript, hasTranscript := part["transcript"].(string)
			if hasTranscript && role == llm.ChatRoleUser {
				out = append(out, llm.ChatContent{Text: transcript})
			}
		case "input_image":
			imageURL, hasImageURL := part["image_url"].(string)
			if hasImageURL && role == llm.ChatRoleUser {
				out = append(out, llm.ChatContent{Image: &llm.ImageContent{Image: imageURL}})
			}
		}
	}
	return out
}

func speechmaticsRealtimeFunctionCallItem(item map[string]any) (*llm.FunctionCall, bool) {
	call := &llm.FunctionCall{
		ID:        speechmaticsRealtimeString(item, "id"),
		CallID:    speechmaticsRealtimeString(item, "call_id"),
		Name:      speechmaticsRealtimeString(item, "name"),
		Arguments: speechmaticsRealtimeString(item, "arguments"),
	}
	if call.ID == "" || call.CallID == "" || call.Name == "" || call.Arguments == "" {
		return nil, false
	}
	return call, true
}

func speechmaticsRealtimeFunctionCallOutputItem(item map[string]any) (*llm.FunctionCallOutput, bool) {
	output, hasOutput := item["output"].(string)
	callID := speechmaticsRealtimeString(item, "call_id")
	if callID == "" || !hasOutput {
		return nil, false
	}
	return &llm.FunctionCallOutput{
		ID:      speechmaticsRealtimeString(item, "id"),
		CallID:  callID,
		Output:  output,
		IsError: false,
	}, true
}

func (s *speechmaticsRealtimeSession) handleInputTranscriptDelta(event map[string]any) bool {
	itemID := speechmaticsRealtimeString(event, "item_id")
	delta := speechmaticsRealtimeString(event, "delta")
	if itemID == "" || delta == "" {
		return false
	}
	contentIndex := speechmaticsRealtimeInt(event, "content_index")
	s.mu.Lock()
	if s.inputTranscripts == nil {
		s.inputTranscripts = make(map[string]map[int]string)
	}
	byIndex := s.inputTranscripts[itemID]
	if byIndex == nil {
		byIndex = make(map[int]string)
		s.inputTranscripts[itemID] = byIndex
	}
	accumulated := byIndex[contentIndex] + delta
	byIndex[contentIndex] = accumulated
	s.mu.Unlock()
	return s.emitRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:       itemID,
			ContentIndex: contentIndex,
			Transcript:   accumulated,
			IsFinal:      false,
		},
	})
}

func (s *speechmaticsRealtimeSession) handleInputTranscriptCompleted(event map[string]any) bool {
	itemID := speechmaticsRealtimeString(event, "item_id")
	contentIndex := speechmaticsRealtimeInt(event, "content_index")
	s.clearInputTranscriptAccumulator(itemID, contentIndex)
	return s.emitRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:       itemID,
			ContentIndex: contentIndex,
			Transcript:   speechmaticsRealtimeString(event, "transcript"),
			IsFinal:      true,
		},
	})
}

func (s *speechmaticsRealtimeSession) handleInputTranscriptFailed(event map[string]any) bool {
	itemID := speechmaticsRealtimeString(event, "item_id")
	contentIndex := speechmaticsRealtimeInt(event, "content_index")
	partial := s.clearInputTranscriptAccumulator(itemID, contentIndex)
	if partial == "" {
		return false
	}
	return s.emitRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:       itemID,
			ContentIndex: contentIndex,
			Transcript:   partial,
			IsFinal:      true,
		},
	})
}

func (s *speechmaticsRealtimeSession) clearInputTranscriptAccumulator(itemID string, contentIndex int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputTranscripts == nil {
		return ""
	}
	byIndex := s.inputTranscripts[itemID]
	if byIndex == nil {
		return ""
	}
	partial := byIndex[contentIndex]
	delete(byIndex, contentIndex)
	if len(byIndex) == 0 {
		delete(s.inputTranscripts, itemID)
	}
	return partial
}

func (s *speechmaticsRealtimeSession) handleProviderError(event map[string]any) bool {
	errorBody, _ := event["error"].(map[string]any)
	if strings.HasPrefix(speechmaticsRealtimeString(errorBody, "message"), "Cancellation failed") {
		return false
	}
	return s.emitRealtimeEvent(llm.RealtimeEvent{
		Type:  llm.RealtimeEventTypeError,
		Error: llm.NewAPIError("Speechmatics returned an error", errorBody, true),
	})
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
		createdAt:  time.Now(),
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
	if item, _ := event["item"].(map[string]any); item != nil {
		itemType := speechmaticsRealtimeString(item, "type")
		if itemType != "" && itemType != "message" {
			return true
		}
	}
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
		timedTextCh:  make(chan llm.RealtimeTimedText, 16),
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
		TimedTextCh:  message.timedTextCh,
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
	s.recordFirstToken(itemID)
	select {
	case message.textCh <- delta:
	default:
	}
	if startTime, ok := speechmaticsRealtimeOptionalFloat(event, "start_time"); ok {
		select {
		case message.timedTextCh <- llm.RealtimeTimedText{Text: delta, StartTime: startTime}:
		default:
		}
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
	s.recordFirstToken(itemID)
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
	if item, _ := event["item"].(map[string]any); item != nil {
		if speechmaticsRealtimeString(item, "type") == "function_call" {
			return s.handleResponseFunctionCall(item)
		}
	}
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
		close(message.timedTextCh)
		close(message.audioCh)
	}
	s.mu.Unlock()
	return message != nil
}

func (s *speechmaticsRealtimeSession) handleResponseDone(event map[string]any) bool {
	response, _ := event["response"].(map[string]any)
	s.mu.Lock()
	metrics := s.responseDoneMetricsLocked(response)
	errorEvent := speechmaticsRealtimeResponseDoneError(response)
	s.closeCurrentGenerationLocked()
	s.mu.Unlock()
	ok := true
	if metrics == nil {
		ok = true
	} else if !s.emitRealtimeEvent(llm.RealtimeEvent{
		Type:    llm.RealtimeEventTypeMetricsCollected,
		Metrics: metrics,
	}) {
		ok = false
	}
	if errorEvent != nil && !s.emitRealtimeEvent(*errorEvent) {
		ok = false
	}
	return ok
}

func (s *speechmaticsRealtimeSession) handleResponseFunctionCall(item map[string]any) bool {
	call := &llm.FunctionCall{
		ID:        speechmaticsRealtimeString(item, "id"),
		CallID:    speechmaticsRealtimeString(item, "call_id"),
		Name:      speechmaticsRealtimeString(item, "name"),
		Arguments: speechmaticsRealtimeString(item, "arguments"),
	}
	if call.ID == "" || call.CallID == "" || call.Name == "" || call.Arguments == "" {
		return false
	}
	s.mu.Lock()
	generation := s.generation
	if s.closed || generation == nil || generation.done {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	select {
	case generation.functionCh <- call:
	default:
	}
	return true
}

func (s *speechmaticsRealtimeSession) recordFirstToken(itemID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.generation == nil || s.generation.done || s.generation.messages[itemID] == nil {
		return
	}
	if s.generation.firstToken.IsZero() {
		s.generation.firstToken = time.Now()
	}
}

func (s *speechmaticsRealtimeSession) responseDoneMetricsLocked(response map[string]any) *telemetry.RealtimeModelMetrics {
	if s.generation == nil {
		return nil
	}
	generation := s.generation
	now := time.Now()
	createdAt := generation.createdAt
	if createdAt.IsZero() {
		createdAt = now
	}
	requestID := generation.responseID
	status := ""
	var usage map[string]any
	if response != nil {
		if id := speechmaticsRealtimeString(response, "id"); id != "" {
			requestID = id
		}
		status = speechmaticsRealtimeString(response, "status")
		usage, _ = response["usage"].(map[string]any)
	}
	duration := now.Sub(createdAt).Seconds()
	if duration < 0 {
		duration = 0
	}
	ttft := -1.0
	if !generation.firstToken.IsZero() {
		ttft = generation.firstToken.Sub(createdAt).Seconds()
		if ttft < 0 {
			ttft = 0
		}
	}
	outputTokens := speechmaticsRealtimeInt(usage, "output_tokens")
	tokensPerSecond := 0.0
	if duration > 0 {
		tokensPerSecond = float64(outputTokens) / duration
	}
	return &telemetry.RealtimeModelMetrics{
		Timestamp:       createdAt,
		RequestID:       requestID,
		Label:           "speechmatics.RealtimeModel",
		Duration:        duration,
		TTFT:            ttft,
		Cancelled:       status == "cancelled",
		InputTokens:     speechmaticsRealtimeInt(usage, "input_tokens"),
		OutputTokens:    outputTokens,
		TotalTokens:     speechmaticsRealtimeInt(usage, "total_tokens"),
		TokensPerSecond: tokensPerSecond,
		Metadata: &telemetry.Metadata{
			ModelName:     s.model,
			ModelProvider: "Speechmatics",
		},
	}
}

func speechmaticsRealtimeResponseDoneError(response map[string]any) *llm.RealtimeEvent {
	if speechmaticsRealtimeString(response, "status") != "failed" {
		return nil
	}
	var errorBody any
	message := "Speechmatics response failed with unknown error"
	statusDetails, _ := response["status_details"].(map[string]any)
	if statusDetails != nil {
		if body, _ := statusDetails["error"].(map[string]any); body != nil {
			errorBody = body
			if errorType := speechmaticsRealtimeString(body, "type"); errorType != "" {
				message = fmt.Sprintf("Speechmatics response failed with error type: %s", errorType)
			}
		}
	}
	return &llm.RealtimeEvent{
		Type:  llm.RealtimeEventTypeError,
		Error: llm.NewAPIError(message, errorBody, true),
	}
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
			close(message.timedTextCh)
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

func speechmaticsRealtimeOptionalFloat(event map[string]any, key string) (float64, bool) {
	switch value := event[key].(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	default:
		return 0, false
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
