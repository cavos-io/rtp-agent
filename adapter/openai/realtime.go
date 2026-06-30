package openai

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/gorilla/websocket"
)

type RealtimeModel struct {
	apiKey                      string
	azureADToken                string
	model                       string
	baseURL                     string
	azureDeployment             string
	apiVersion                  string
	isAzure                     bool
	dialWebsocket               OpenAIRealtimeWebsocketDialer
	toolFormatter               OpenAIRealtimeToolFormatter
	inputTranscriptionFinalHook OpenAIRealtimeInputTranscriptionFinalHook
	remoteItemAddedHook         OpenAIRealtimeRemoteItemAddedHook
	functionCallFilter          OpenAIRealtimeFunctionCallFilter
	sessionCloseMetricsHook     OpenAIRealtimeSessionCloseMetricsHook
	mu                          sync.Mutex
	options                     llm.RealtimeSessionOptions
	modalities                  []string
	modalitiesSet               bool
	maxSession                  time.Duration
	connect                     llm.APIConnectOptions
	sessions                    map[*realtimeSession]struct{}
}

type OpenAIRealtimeWebsocketDialer func(string, http.Header) (*websocket.Conn, *http.Response, error)

type openAIRealtimeWebsocketDialer = OpenAIRealtimeWebsocketDialer

type OpenAIRealtimeToolFormatter func([]llm.Tool) []map[string]any

type OpenAIRealtimeInputTranscriptionFinalHook func(*llm.ChatMessage, *llm.InputTranscriptionCompleted)

type OpenAIRealtimeRemoteItemAddedHook func(*llm.RemoteChatContext, *llm.RemoteItemAddedEvent)

type OpenAIRealtimeFunctionCallFilter func([]llm.Tool, string) bool

type OpenAIRealtimeSessionCloseMetricsHook func(time.Duration) *telemetry.RealtimeModelMetrics

type openAIRealtimeDialResult struct {
	conn *websocket.Conn
	err  error
}

type openAIRealtimeModelOptions struct {
	sessionOptions              llm.RealtimeSessionOptions
	modalities                  []string
	modalitiesSet               bool
	baseURL                     string
	maxSession                  time.Duration
	maxSessionSet               bool
	connect                     *llm.APIConnectOptions
	dialWebsocket               OpenAIRealtimeWebsocketDialer
	toolFormatter               OpenAIRealtimeToolFormatter
	inputTranscriptionFinalHook OpenAIRealtimeInputTranscriptionFinalHook
	remoteItemAddedHook         OpenAIRealtimeRemoteItemAddedHook
	functionCallFilter          OpenAIRealtimeFunctionCallFilter
	sessionCloseMetricsHook     OpenAIRealtimeSessionCloseMetricsHook
}

type OpenAIRealtimeOption func(*openAIRealtimeModelOptions)

func WithOpenAIRealtimeVoice(voice string) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.Voice = voice
		options.sessionOptions.VoiceSet = true
	}
}

func WithOpenAIRealtimeSpeed(speed float64) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.Speed = speed
		options.sessionOptions.SpeedSet = true
	}
}

func WithOpenAIRealtimeToolChoice(toolChoice llm.ToolChoice) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.ToolChoice = toolChoice
		options.sessionOptions.ToolChoiceSet = true
	}
}

func WithOpenAIRealtimeTracing(tracing any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.Tracing = tracing
		options.sessionOptions.TracingSet = true
	}
}

func WithOpenAIRealtimeReasoning(reasoning any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.Reasoning = reasoning
		options.sessionOptions.ReasoningSet = true
	}
}

func WithOpenAIRealtimeTruncation(truncation any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.Truncation = truncation
		options.sessionOptions.TruncationSet = true
	}
}

func WithOpenAIRealtimeInputAudioTranscription(transcription any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.InputAudioTranscription = transcription
		options.sessionOptions.InputAudioTranscriptionSet = true
	}
}

func WithOpenAIRealtimeInputAudioNoiseReduction(noiseReduction any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.InputAudioNoiseReduction = noiseReduction
		options.sessionOptions.InputAudioNoiseReductionSet = true
	}
}

func WithOpenAIRealtimeTurnDetection(turnDetection any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.TurnDetection = turnDetection
		options.sessionOptions.TurnDetectionSet = true
	}
}

func WithOpenAIRealtimeModalities(modalities []string) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.modalities = append([]string(nil), modalities...)
		options.modalitiesSet = true
	}
}

func WithOpenAIRealtimeMaxResponseOutputTokens(tokens any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.MaxResponseOutputTokens = tokens
		options.sessionOptions.MaxResponseOutputTokensSet = true
	}
}

func WithOpenAIRealtimeMaxSessionDuration(duration time.Duration) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.maxSession = duration
		options.maxSessionSet = true
	}
}

func WithOpenAIRealtimeConnectOptions(connectOptions llm.APIConnectOptions) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.connect = &connectOptions
	}
}

func WithOpenAIRealtimeBaseURL(baseURL string) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.baseURL = baseURL
	}
}

func WithOpenAIRealtimeWebsocketDialer(dialer OpenAIRealtimeWebsocketDialer) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.dialWebsocket = dialer
	}
}

func WithOpenAIRealtimeToolFormatter(formatter OpenAIRealtimeToolFormatter) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.toolFormatter = formatter
	}
}

func WithOpenAIRealtimeInputTranscriptionFinalHook(hook OpenAIRealtimeInputTranscriptionFinalHook) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.inputTranscriptionFinalHook = hook
	}
}

func WithOpenAIRealtimeRemoteItemAddedHook(hook OpenAIRealtimeRemoteItemAddedHook) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.remoteItemAddedHook = hook
	}
}

func WithOpenAIRealtimeFunctionCallFilter(filter OpenAIRealtimeFunctionCallFilter) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.functionCallFilter = filter
	}
}

func WithOpenAIRealtimeSessionCloseMetricsHook(hook OpenAIRealtimeSessionCloseMetricsHook) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionCloseMetricsHook = hook
	}
}

func NewRealtimeModel(apiKey, model string, opts ...OpenAIRealtimeOption) *RealtimeModel {
	return newRealtimeModel(apiKey, model, openAIRealtimeApplyModelOptions(opts...))
}

func openAIRealtimeApplyModelOptions(opts ...OpenAIRealtimeOption) openAIRealtimeModelOptions {
	options := openAIRealtimeModelOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

func newRealtimeModel(apiKey, model string, options openAIRealtimeModelOptions) *RealtimeModel {
	if model == "" {
		model = "gpt-realtime"
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if options.baseURL != "" {
		baseURL = options.baseURL
	}
	connectOptions := llm.DefaultAPIConnectOptions()
	if options.connect != nil {
		connectOptions = *options.connect
	}
	dialWebsocket := OpenAIRealtimeWebsocketDialer(defaultOpenAIRealtimeWebsocketDialer)
	if options.dialWebsocket != nil {
		dialWebsocket = options.dialWebsocket
	}
	maxSession := options.maxSession
	if !options.maxSessionSet {
		maxSession = openAIRealtimeDefaultMaxSessionDuration
	}
	return &RealtimeModel{
		apiKey:                      apiKey,
		model:                       model,
		baseURL:                     openAIRealtimeBaseURL(baseURL),
		dialWebsocket:               dialWebsocket,
		toolFormatter:               options.toolFormatter,
		inputTranscriptionFinalHook: options.inputTranscriptionFinalHook,
		remoteItemAddedHook:         options.remoteItemAddedHook,
		functionCallFilter:          options.functionCallFilter,
		sessionCloseMetricsHook:     options.sessionCloseMetricsHook,
		options:                     options.sessionOptions,
		modalities:                  options.modalities,
		modalitiesSet:               options.modalitiesSet,
		maxSession:                  maxSession,
		connect:                     connectOptions,
	}
}

func NewAzureOpenAIRealtimeModel(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...OpenAIRealtimeOption) (*RealtimeModel, error) {
	options := openAIRealtimeApplyModelOptions(opts...)
	if model == "" {
		model = "gpt-realtime"
	}
	if options.baseURL != "" && azureEndpoint != "" {
		return nil, fmt.Errorf("base_url and azure_endpoint are mutually exclusive")
	}
	if azureEndpoint == "" {
		azureEndpoint = os.Getenv(azureOpenAIEndpointEnv)
	}
	if apiVersion == "" {
		apiVersion = os.Getenv(openAIAPIVersionEnv)
	}
	if apiKey == "" {
		apiKey = os.Getenv(azureOpenAIAPIKeyEnv)
	}
	if azureADToken == "" {
		azureADToken = os.Getenv(azureOpenAIADTokenEnv)
	}
	if azureEndpoint == "" && options.baseURL == "" {
		return nil, fmt.Errorf("%s is required for Azure OpenAI realtime", azureOpenAIEndpointEnv)
	}
	if apiKey == "" && azureADToken == "" {
		return nil, fmt.Errorf("%s or %s is required for Azure OpenAI realtime", azureOpenAIAPIKeyEnv, azureOpenAIADTokenEnv)
	}
	if azureDeployment == "" {
		azureDeployment = model
	}
	provider := newRealtimeModel(apiKey, model, options)
	provider.apiKey = apiKey
	if options.baseURL != "" {
		provider.baseURL = openAIRealtimeWebsocketBaseURL(options.baseURL)
	} else {
		provider.baseURL = openAIRealtimeAzureBaseURL(azureEndpoint)
	}
	provider.azureADToken = azureADToken
	provider.azureDeployment = azureDeployment
	provider.apiVersion = apiVersion
	provider.isAzure = true
	if !provider.options.InputAudioTranscriptionSet && provider.options.InputAudioTranscription == nil {
		provider.options.InputAudioTranscription = map[string]any{"model": "whisper-1"}
		provider.options.InputAudioTranscriptionSet = true
	}
	if !provider.options.TurnDetectionSet && provider.options.TurnDetection == nil {
		provider.options.TurnDetection = map[string]any{
			"type":                "server_vad",
			"threshold":           0.5,
			"prefix_padding_ms":   300,
			"silence_duration_ms": 200,
			"create_response":     true,
		}
		provider.options.TurnDetectionSet = true
	}
	return provider, nil
}

func (m *RealtimeModel) Model() string {
	return m.model
}

func (m *RealtimeModel) initialRealtimeModalities() [][]string {
	if m == nil || !m.modalitiesSet {
		return nil
	}
	return [][]string{append([]string(nil), m.modalities...)}
}

func (m *RealtimeModel) Provider() string {
	u, err := url.Parse(m.baseURL)
	if err != nil || u.Host == "" {
		return "openai"
	}
	return u.Host
}

func (m *RealtimeModel) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	sessions := make([]*realtimeSession, 0, len(m.sessions))
	for session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[*realtimeSession]struct{})
	m.mu.Unlock()

	var closeErr error
	for _, session := range sessions {
		if err := session.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (m *RealtimeModel) UpdateOptions(options llm.RealtimeSessionOptions) error {
	m.mu.Lock()
	openAIRealtimeMergeSessionOptions(&m.options, options)
	activeOptions := openAIRealtimeModelActiveSessionOptions(options, m.options)
	sessions := make([]*realtimeSession, 0, len(m.sessions))
	for session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	for _, session := range sessions {
		if err := session.UpdateOptions(activeOptions); err != nil {
			return err
		}
	}
	return nil
}

func (m *RealtimeModel) Capabilities() llm.RealtimeCapabilities {
	m.mu.Lock()
	options := m.options
	modalities := append([]string(nil), m.modalities...)
	modalitiesSet := m.modalitiesSet
	m.mu.Unlock()

	return llm.RealtimeCapabilities{
		MessageTruncation:       true,
		TurnDetection:           !(options.TurnDetectionSet && options.TurnDetection == nil),
		UserTranscription:       !(options.InputAudioTranscriptionSet && options.InputAudioTranscription == nil),
		AutoToolReplyGeneration: false,
		AudioOutput:             !modalitiesSet || realtimeModalitiesInclude(modalities, "audio"),
		ManualFunctionCalls:     true,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   true,
	}
}

type realtimeSession struct {
	model                 *RealtimeModel
	conn                  *websocket.Conn
	ctx                   context.Context
	cancel                context.CancelFunc
	mu                    sync.Mutex
	closeOnce             sync.Once
	closeErr              error
	chatContextMu         sync.Mutex
	toolsMu               sync.Mutex
	eventCh               chan llm.RealtimeEvent
	eventStream           *openAIRealtimeQueuedStream[llm.RealtimeEvent]
	remote                *llm.RemoteChatContext
	inputTranscripts      *utils.BoundedDict[inputTranscriptKey, realtimeInputTranscript]
	generation            *realtimeGeneration
	pendingResponses      map[string]struct{}
	pendingDeleteItemIDs  []string
	pendingCreateAcks     map[string]chan struct{}
	pendingDeleteAcks     map[string]chan struct{}
	instructions          string
	instructionsSet       bool
	tools                 []llm.Tool
	audioBStream          *audio.AudioByteStream
	audioNormalizer       openAIRealtimeInputAudioNormalizer
	pushedDuration        float64
	optionsState          map[string]any
	connectedAt           time.Time
	lastGeneration        realtimeGenerationTiming
	responseCreateTimeout time.Duration
	chatContextAckTimeout time.Duration
}

type realtimeGenerationTiming struct {
	createdAt    time.Time
	firstTokenAt time.Time
}

const maxRealtimeInputTranscripts = 1024
const openAIRealtimeInputSampleRate = 24000
const openAIRealtimeInputNumChannels = 1
const openAIRealtimeDefaultVoice = "marin"
const openAIRealtimeDefaultSpeed = 1.0
const openAIRealtimeDefaultMaxOutputTokens = "inf"
const openAIRealtimeDefaultMaxSessionDuration = 20 * time.Minute
const openAIRealtimeActiveGenerationRecyclePoll = 100 * time.Millisecond
const openAIRealtimeResponseCreateTimeout = 10 * time.Second
const openAIRealtimeChatContextAckTimeout = 5 * time.Second

type inputTranscriptKey struct {
	itemID       string
	contentIndex int
}

type realtimeInputTranscript struct {
	itemID       string
	contentIndex int
	transcript   string
}

type realtimeGeneration struct {
	messageCh      chan llm.MessageGeneration
	functionCh     chan *llm.FunctionCall
	messageStream  *openAIRealtimeQueuedStream[llm.MessageGeneration]
	functionStream *openAIRealtimeQueuedStream[*llm.FunctionCall]
	messages       map[string]*realtimeMessageGeneration
	timing         realtimeGenerationTiming
}

type realtimeMessageGeneration struct {
	textStream    *openAIRealtimeQueuedStream[string]
	timedText     *openAIRealtimeQueuedStream[llm.RealtimeTimedText]
	audioStream   *openAIRealtimeQueuedStream[*model.AudioFrame]
	modalitiesCh  chan []string
	modalities    []string
	transcript    string
	audioClosed   bool
	streamsClosed bool
}

type openAIRealtimeQueuedStream[T any] struct {
	out    chan T
	wake   chan struct{}
	mu     sync.Mutex
	queue  []T
	closed bool
}

func newOpenAIRealtimeQueuedStream[T any]() *openAIRealtimeQueuedStream[T] {
	stream := &openAIRealtimeQueuedStream[T]{
		out:  make(chan T),
		wake: make(chan struct{}, 1),
	}
	go stream.run()
	return stream
}

func (s *openAIRealtimeQueuedStream[T]) Chan() <-chan T {
	if s == nil {
		return nil
	}
	return s.out
}

func (s *openAIRealtimeQueuedStream[T]) rawChan() chan T {
	if s == nil {
		return nil
	}
	return s.out
}

func (s *openAIRealtimeQueuedStream[T]) Send(value T) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.queue = append(s.queue, value)
	s.notifyLocked()
	return true
}

func (s *openAIRealtimeQueuedStream[T]) pending() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func (s *openAIRealtimeQueuedStream[T]) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.notifyLocked()
}

func (s *openAIRealtimeQueuedStream[T]) notifyLocked() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *openAIRealtimeQueuedStream[T]) run() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.closed {
			s.mu.Unlock()
			<-s.wake
			s.mu.Lock()
		}
		if len(s.queue) == 0 && s.closed {
			s.mu.Unlock()
			close(s.out)
			return
		}
		value := s.queue[0]
		var zero T
		s.queue[0] = zero
		s.queue = s.queue[1:]
		s.mu.Unlock()
		s.out <- value
	}
}

func (m *RealtimeModel) Session() (llm.RealtimeSession, error) {
	if m.apiKey == "" && (!m.isAzure || m.azureADToken == "") {
		return nil, fmt.Errorf("%s", "The api_key client option must be set either by passing api_key to the client or by setting the OPENAI_API_KEY environment variable")
	}
	wsURL := m.realtimeSessionURL()

	header := m.realtimeHeaders()

	conn, err := m.dialRealtimeWebsocket(context.Background(), wsURL, header)
	if err != nil {
		var timeoutErr *llm.APITimeoutError
		if errors.As(err, &timeoutErr) {
			return nil, timeoutErr
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to connect to OpenAI realtime: %v", err))
	}

	ctx, cancel := context.WithCancel(context.Background())
	initialSession := openAIRealtimeInitialSession(m.model, m.initialRealtimeModalities()...)
	m.mu.Lock()
	initialOptions := m.options
	m.mu.Unlock()
	if msg := openAIRealtimeUpdateOptionsMessage(initialOptions); msg != nil {
		if session, ok := msg["session"].(map[string]any); ok {
			openAIRealtimeMergeSessionPayload(initialSession, session)
		}
	}
	if m.isAzure && !initialOptions.InputAudioNoiseReductionSet && initialOptions.InputAudioNoiseReduction == nil {
		openAIRealtimeRemoveInputNoiseReduction(initialSession)
	}
	eventStream := newOpenAIRealtimeQueuedStream[llm.RealtimeEvent]()
	s := &realtimeSession{
		model:            m,
		conn:             conn,
		ctx:              ctx,
		cancel:           cancel,
		eventCh:          eventStream.rawChan(),
		eventStream:      eventStream,
		remote:           llm.NewRemoteChatContext(),
		pendingResponses: make(map[string]struct{}),
		audioBStream:     newOpenAIRealtimeAudioByteStream(),
		connectedAt:      time.Now(),
	}

	go s.eventLoop()
	s.startMaxSessionRecycle(conn)
	s.optionsState = openAIRealtimeOptionEntries(initialSession)
	if err := s.sendMsg(openAIRealtimeInitialSessionUpdateMessage(initialSession)); err != nil {
		_ = s.Close()
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to initialize OpenAI realtime session: %v", err))
	}
	m.registerSession(s)

	return s, nil
}

func (m *RealtimeModel) realtimeHeaders() http.Header {
	header := http.Header{}
	header.Add("User-Agent", "LiveKit Agents")
	if m.isAzure {
		if m.azureADToken != "" {
			header.Add("Authorization", "Bearer "+m.azureADToken)
		}
		if m.apiKey != "" {
			header.Add("api-key", m.apiKey)
		}
		return header
	}
	header.Add("Authorization", "Bearer "+m.apiKey)
	return header
}

func (m *RealtimeModel) realtimeSessionURL() string {
	if m.isAzure {
		return openAIRealtimeAzureSessionURL(m.baseURL, m.model, m.azureDeployment, m.apiVersion)
	}
	return openAIRealtimeSessionURL(m.baseURL, m.model)
}

func (m *RealtimeModel) registerSession(session *realtimeSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = make(map[*realtimeSession]struct{})
	}
	m.sessions[session] = struct{}{}
}

func (m *RealtimeModel) unregisterSession(session *realtimeSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, session)
}

func (m *RealtimeModel) dialRealtimeWebsocket(ctx context.Context, wsURL string, header http.Header) (*websocket.Conn, error) {
	var (
		conn *websocket.Conn
		err  error
	)
	for attempt := 0; attempt <= m.connect.MaxRetry; attempt++ {
		conn, err = m.dialRealtimeWebsocketAttempt(ctx, wsURL, header)
		if err == nil {
			return conn, nil
		}
		if attempt == m.connect.MaxRetry {
			return nil, err
		}
		retryInterval := m.connect.IntervalForRetry(attempt)
		if retryInterval <= 0 {
			continue
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, err
}

func (m *RealtimeModel) dialRealtimeWebsocketAttempt(ctx context.Context, wsURL string, header http.Header) (*websocket.Conn, error) {
	attemptCtx := ctx
	cancel := func() {}
	if m.connect.Timeout > 0 {
		attemptCtx, cancel = context.WithTimeout(ctx, m.connect.Timeout)
	} else if ctx != nil {
		attemptCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	resultCh := make(chan openAIRealtimeDialResult, 1)
	go func() {
		conn, _, err := m.dialWebsocket(wsURL, header)
		result := openAIRealtimeDialResult{conn: conn, err: err}
		select {
		case resultCh <- result:
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
			return nil, llm.NewAPITimeoutError("OpenAI realtime connection timed out")
		}
		return nil, attemptCtx.Err()
	}
}

func openAIRealtimeBaseURL(rawURL string) string {
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
	path := strings.TrimRight(u.Path, "/")
	switch path {
	case "", "/v1", "/openai", "/openai/v1":
		u.Path = path + "/realtime"
	default:
		u.Path = path
	}
	return u.String()
}

func openAIRealtimeAzureBaseURL(rawURL string) string {
	u, err := url.Parse(strings.TrimRight(rawURL, "/") + "/openai")
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

func openAIRealtimeWebsocketBaseURL(rawURL string) string {
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
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

func openAIRealtimeSessionURL(baseURL, model string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Sprintf("%s?model=%s", baseURL, url.QueryEscape(model))
	}
	query := u.Query()
	if query.Get("model") == "" {
		query.Set("model", model)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func openAIRealtimeAzureSessionURL(baseURL, model, deployment, apiVersion string) string {
	if deployment == "" {
		deployment = model
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	path := strings.TrimRight(u.Path, "/")
	switch path {
	case "", "/openai":
		if apiVersion != "" {
			u.Path = path + "/realtime"
		} else {
			u.Path = path + "/v1/realtime"
		}
	case "/openai/v1":
		u.Path = "/openai/v1/realtime"
	default:
		u.Path = strings.TrimRight(u.Path, "/")
	}
	query := u.Query()
	query.Del("api-version")
	if apiVersion != "" {
		query.Set("api-version", apiVersion)
		if deployment != "" {
			query.Set("deployment", deployment)
		}
	} else if query.Get("model") == "" && deployment != "" {
		query.Set("model", deployment)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func defaultOpenAIRealtimeWebsocketDialer(endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.Dial(endpoint, headers)
}

func openAIRealtimeInitialSession(model string, modalities ...[]string) map[string]any {
	audioFormat := map[string]any{
		"type": "audio/pcm",
		"rate": openAIRealtimeInputSampleRate,
	}
	outputModality := "audio"
	if len(modalities) > 0 && !realtimeModalitiesInclude(modalities[0], "audio") {
		outputModality = "text"
	}
	return map[string]any{
		"type":              "realtime",
		"model":             model,
		"output_modalities": []string{outputModality},
		"audio": map[string]any{
			"input": map[string]any{
				"format": audioFormat,
				"transcription": map[string]any{
					"model": "gpt-4o-mini-transcribe",
				},
				"turn_detection": map[string]any{
					"type":               "semantic_vad",
					"create_response":    true,
					"eagerness":          "medium",
					"interrupt_response": true,
				},
			},
			"output": map[string]any{
				"format": audioFormat,
				"speed":  openAIRealtimeDefaultSpeed,
				"voice":  openAIRealtimeDefaultVoice,
			},
		},
		"max_output_tokens": openAIRealtimeDefaultMaxOutputTokens,
		"tool_choice":       "auto",
	}
}

func openAIRealtimeInitialSessionUpdateMessage(session map[string]any) map[string]any {
	return map[string]any{
		"type":     "session.update",
		"event_id": cavosmath.ShortUUID("session_update_"),
		"session":  session,
	}
}

func (s *realtimeSession) EventCh() <-chan llm.RealtimeEvent {
	if s.eventStream != nil {
		return s.eventStream.Chan()
	}
	return s.eventCh
}

func (s *realtimeSession) sendRealtimeEvent(ev llm.RealtimeEvent) bool {
	if s == nil {
		return false
	}
	if s.eventStream != nil {
		return s.eventStream.Send(ev)
	}
	if s.eventCh == nil {
		return false
	}
	s.eventCh <- ev
	return true
}

func (s *realtimeSession) closeRealtimeEventStream() {
	if s == nil {
		return
	}
	if s.eventStream != nil {
		s.eventStream.Close()
		return
	}
	if s.eventCh != nil {
		close(s.eventCh)
	}
}

func (s *realtimeSession) UpdateInstructions(instructions string) error {
	msg := map[string]any{
		"type":     "session.update",
		"event_id": cavosmath.ShortUUID("instructions_update_"),
		"session": map[string]any{
			"type":         "realtime",
			"instructions": instructions,
		},
	}
	s.mu.Lock()
	s.instructions = instructions
	s.instructionsSet = true
	s.mu.Unlock()
	if err := s.sendMsg(msg); err != nil {
		return err
	}
	return nil
}

func (s *realtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	s.chatContextMu.Lock()
	defer s.chatContextMu.Unlock()

	syncedChatCtx := openAIRealtimeSyncedChatContext(chatCtx)
	var (
		msgs []map[string]any
		err  error
	)
	if s.remote == nil {
		msgs, err = openAIRealtimeChatContextCreateMessages(syncedChatCtx)
	} else {
		msgs, err = openAIRealtimeChatContextUpdateMessages(s.remote.ToChatCtx(), syncedChatCtx)
	}
	if err != nil {
		return err
	}
	acks := make([]<-chan struct{}, 0, len(msgs))
	for _, msg := range msgs {
		if ack := s.addRealtimeChatContextAck(msg); ack != nil {
			acks = append(acks, ack)
		}
		if err := s.sendMsg(msg); err != nil {
			s.clearRealtimeChatContextAcks(msgs)
			return err
		}
	}
	if err := s.waitRealtimeChatContextAcks(acks); err != nil {
		s.clearRealtimeChatContextAcks(msgs)
		return err
	}
	s.remote = openAIRealtimeRemoteSnapshot(syncedChatCtx)
	return nil
}

func (s *realtimeSession) addRealtimeChatContextAck(msg map[string]any) <-chan struct{} {
	if s == nil {
		return nil
	}
	typ, _ := msg["type"].(string)
	var (
		itemID string
		target *map[string]chan struct{}
	)
	switch typ {
	case "conversation.item.create":
		item, _ := msg["item"].(map[string]any)
		itemID, _ = item["id"].(string)
		target = &s.pendingCreateAcks
	case "conversation.item.delete":
		itemID, _ = msg["item_id"].(string)
		target = &s.pendingDeleteAcks
	default:
		return nil
	}
	ack := make(chan struct{})
	s.mu.Lock()
	if *target == nil {
		*target = make(map[string]chan struct{})
	}
	(*target)[itemID] = ack
	s.mu.Unlock()
	return ack
}

func (s *realtimeSession) waitRealtimeChatContextAcks(acks []<-chan struct{}) error {
	if len(acks) == 0 {
		return nil
	}
	timeout := s.chatContextAckTimeout
	if timeout == 0 {
		timeout = openAIRealtimeChatContextAckTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, ack := range acks {
		select {
		case <-ack:
		case <-timer.C:
			return llm.NewRealtimeError("update_chat_ctx timed out.", nil)
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
	return nil
}

func (s *realtimeSession) clearRealtimeChatContextAcks(msgs []map[string]any) {
	if s == nil || len(msgs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msg := range msgs {
		typ, _ := msg["type"].(string)
		switch typ {
		case "conversation.item.create":
			item, _ := msg["item"].(map[string]any)
			itemID, _ := item["id"].(string)
			delete(s.pendingCreateAcks, itemID)
		case "conversation.item.delete":
			itemID, _ := msg["item_id"].(string)
			delete(s.pendingDeleteAcks, itemID)
		}
	}
}

func (s *realtimeSession) UpdateTools(tools []llm.Tool) error {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()

	formattedTools := s.model.realtimeTools(tools)
	retainedTools := openAIRealtimeRetainedTools(tools, formattedTools)
	msg := map[string]any{
		"type":     "session.update",
		"event_id": cavosmath.ShortUUID("tools_update_"),
		"session": map[string]any{
			"type":  "realtime",
			"model": s.model.model,
			"tools": formattedTools,
		},
	}
	s.mu.Lock()
	s.tools = retainedTools
	s.mu.Unlock()
	if err := s.sendMsg(msg); err != nil {
		return err
	}
	return nil
}

func (m *RealtimeModel) realtimeTools(tools []llm.Tool) []map[string]any {
	if m != nil && m.toolFormatter != nil {
		return m.toolFormatter(tools)
	}
	return openAIRealtimeTools(tools)
}

func openAIRealtimeTools(tools []llm.Tool) []map[string]any {
	var oaTools []map[string]any
	for _, t := range tools {
		if _, ok := t.(llm.ProviderTool); ok {
			continue
		}
		oaTools = append(oaTools, map[string]any{
			"type":        "function",
			"name":        t.Name(),
			"description": t.Description(),
			"parameters":  llm.ToolParameters(t),
		})
	}
	return oaTools
}

func openAIRealtimeRetainedTools(tools []llm.Tool, emitted []map[string]any) []llm.Tool {
	emittedNames := make(map[string]struct{}, len(emitted))
	for _, tool := range emitted {
		name, _ := tool["name"].(string)
		if name != "" {
			emittedNames[name] = struct{}{}
		}
	}
	retained := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		if _, ok := tool.(llm.ProviderTool); ok {
			retained = append(retained, tool)
			continue
		}
		if _, ok := emittedNames[tool.Name()]; ok {
			retained = append(retained, tool)
		}
	}
	return retained
}

func openAIRealtimeChatContextCreateMessages(chatCtx *llm.ChatContext) ([]map[string]any, error) {
	return openAIRealtimeChatContextUpdateMessages(llm.NewChatContext(), openAIRealtimeSyncedChatContext(chatCtx))
}

func openAIRealtimeChatContextUpdateMessages(oldCtx, newCtx *llm.ChatContext) ([]map[string]any, error) {
	if oldCtx == nil {
		oldCtx = llm.NewChatContext()
	}
	if newCtx == nil {
		newCtx = llm.NewChatContext()
	}
	newCtx = openAIRealtimeFilterEmptyLocalMessages(oldCtx, newCtx)
	diff := llm.ComputeChatCtxDiff(oldCtx, newCtx)
	msgs := make([]map[string]any, 0, len(diff.ToRemove)+len(diff.ToCreate)+len(diff.ToUpdate)*2)
	for _, itemID := range diff.ToRemove {
		if openAIRealtimeEmptyMessage(oldCtx.GetByID(itemID)) {
			continue
		}
		msgs = append(msgs, openAIRealtimeDeleteChatItemMessage(itemID))
	}
	for _, item := range diff.ToCreate {
		msg, err := openAIRealtimeCreateChatItemMessage(newCtx, item[0], item[1])
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	for _, item := range diff.ToUpdate {
		if item[1] == nil {
			continue
		}
		if openAIRealtimeEmptyMessage(oldCtx.GetByID(*item[1])) {
			continue
		}
		msgs = append(msgs, openAIRealtimeDeleteChatItemMessage(*item[1]))
		msg, err := openAIRealtimeCreateChatItemMessage(newCtx, item[0], item[1])
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func openAIRealtimeFilterEmptyLocalMessages(oldCtx, newCtx *llm.ChatContext) *llm.ChatContext {
	filtered := llm.NewChatContext()
	for _, item := range newCtx.Items {
		if openAIRealtimeEmptyMessage(item) && oldCtx.GetByID(item.GetID()) == nil {
			continue
		}
		filtered.Items = append(filtered.Items, item)
	}
	return filtered
}

func openAIRealtimeEmptyMessage(item llm.ChatItem) bool {
	msg, ok := item.(*llm.ChatMessage)
	return ok && len(msg.Content) == 0
}

func openAIRealtimeSyncedChatContext(chatCtx *llm.ChatContext) *llm.ChatContext {
	if chatCtx == nil {
		return llm.NewChatContext()
	}
	synced := chatCtx.Copy(llm.ChatContextCopyOptions{
		ExcludeFunctionCall: true,
		ExcludeHandoff:      true,
		ExcludeConfigUpdate: true,
	})
	items := synced.Items[:0]
	for _, item := range synced.Items {
		if openAIRealtimeInstructionItem(item) {
			continue
		}
		items = append(items, item)
	}
	synced.Items = items
	return synced
}

func openAIRealtimeInstructionItem(item llm.ChatItem) bool {
	msg, ok := item.(*llm.ChatMessage)
	if !ok || (msg.Role != llm.ChatRoleSystem && msg.Role != llm.ChatRoleDeveloper) {
		return false
	}
	for _, content := range msg.Content {
		if content.Instructions != nil {
			return true
		}
	}
	return false
}

func openAIRealtimeRemoteSnapshot(chatCtx *llm.ChatContext) *llm.RemoteChatContext {
	remote := llm.NewRemoteChatContext()
	if chatCtx == nil {
		return remote
	}
	var previous *string
	for _, item := range chatCtx.Items {
		if err := remote.Insert(previous, item); err != nil {
			return remote
		}
		itemID := item.GetID()
		previous = &itemID
	}
	return remote
}

func openAIRealtimeDeleteChatItemMessage(itemID string) map[string]any {
	return map[string]any{
		"type":     "conversation.item.delete",
		"event_id": cavosmath.ShortUUID("chat_ctx_delete_"),
		"item_id":  itemID,
	}
}

func openAIRealtimeCreateChatItemMessage(chatCtx *llm.ChatContext, previousItemID *string, itemID *string) (map[string]any, error) {
	if itemID == nil {
		return nil, fmt.Errorf("missing realtime chat item id")
	}
	item := chatCtx.GetByID(*itemID)
	if item == nil {
		return nil, fmt.Errorf("realtime chat item %q not found", *itemID)
	}
	openAIItem, err := openAIRealtimeChatItemMessage(item)
	if err != nil {
		return nil, err
	}
	previous := any("root")
	if previousItemID != nil {
		previous = *previousItemID
	}
	return map[string]any{
		"type":             "conversation.item.create",
		"event_id":         cavosmath.ShortUUID("chat_ctx_create_"),
		"previous_item_id": previous,
		"item":             openAIItem,
	}, nil
}

func openAIRealtimeChatItemMessage(item llm.ChatItem) (map[string]any, error) {
	switch it := item.(type) {
	case *llm.ChatMessage:
		switch it.Role {
		case llm.ChatRoleSystem, llm.ChatRoleDeveloper, llm.ChatRoleUser, llm.ChatRoleAssistant:
		default:
			return nil, fmt.Errorf("unsupported role: %s", it.Role)
		}
		content, err := openAIRealtimeChatMessageContent(it)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"id":      it.ID,
			"type":    "message",
			"role":    string(openAIRealtimeMessageRole(it.Role)),
			"content": content,
		}, nil
	case *llm.FunctionCall:
		return map[string]any{
			"id":        it.ID,
			"type":      "function_call",
			"call_id":   it.CallID,
			"name":      it.Name,
			"arguments": it.Arguments,
		}, nil
	case *llm.FunctionCallOutput:
		return map[string]any{
			"id":      it.ID,
			"type":    "function_call_output",
			"call_id": it.CallID,
			"output":  it.Output,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported realtime chat item %T", item)
	}
}

func openAIRealtimeMessageRole(role llm.ChatRole) llm.ChatRole {
	if role == llm.ChatRoleDeveloper {
		return llm.ChatRoleSystem
	}
	return role
}

func openAIRealtimeChatMessageContent(msg *llm.ChatMessage) ([]map[string]any, error) {
	content := make([]map[string]any, 0, len(msg.Content))
	for _, part := range msg.Content {
		if part.Text != "" || (part.Image == nil && part.Audio == nil && part.Instructions == nil) {
			partType := "input_text"
			if msg.Role == llm.ChatRoleAssistant {
				partType = "output_text"
			}
			content = append(content, map[string]any{
				"type": partType,
				"text": part.Text,
			})
		}
		if msg.Role == llm.ChatRoleUser && part.Image != nil {
			imagePart, err := openAIRealtimeImageContent(part.Image)
			if err != nil {
				return nil, err
			}
			if imagePart != nil {
				content = append(content, imagePart)
			}
		}
		if msg.Role == llm.ChatRoleUser && part.Audio != nil {
			content = append(content, map[string]any{
				"type":       "input_audio",
				"audio":      openAIRealtimeAudioContent(part.Audio),
				"transcript": part.Audio.Transcript,
			})
		}
	}
	return content, nil
}

func openAIRealtimeAudioContent(audio *llm.AudioContent) string {
	if audio == nil || len(audio.Frames) == 0 {
		return ""
	}
	var data []byte
	for _, frame := range audio.Frames {
		switch f := frame.(type) {
		case *model.AudioFrame:
			if f != nil {
				data = append(data, f.Data...)
			}
		case model.AudioFrame:
			data = append(data, f.Data...)
		}
	}
	if len(data) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

func openAIRealtimeImageContent(image *llm.ImageContent) (map[string]any, error) {
	img, err := llm.SerializeImage(image)
	if err != nil {
		return nil, err
	}
	if img.ExternalURL != "" {
		return nil, nil
	}
	return map[string]any{
		"type":      "input_image",
		"image_url": fmt.Sprintf("data:%s;base64,%s", img.MIMEType, base64.StdEncoding.EncodeToString(img.DataBytes)),
	}, nil
}

func (s *realtimeSession) UpdateOptions(options llm.RealtimeSessionOptions) error {
	msg := openAIRealtimeUpdateOptionsMessageWithEventID(options, cavosmath.ShortUUID("options_update_"))
	if msg == nil {
		return nil
	}
	session, _ := msg["session"].(map[string]any)
	s.mu.Lock()
	changedSession, changedOptions := openAIRealtimeChangedOptionsSession(session, s.optionsState)
	if len(changedSession) == 0 {
		s.mu.Unlock()
		return nil
	}
	if s.optionsState == nil {
		s.optionsState = make(map[string]any, len(changedOptions))
	}
	for key, value := range changedOptions {
		s.optionsState[key] = value
	}
	s.mu.Unlock()
	msg["session"] = changedSession
	changedSession["type"] = "realtime"
	if err := s.sendMsg(msg); err != nil {
		return err
	}
	return nil
}

func openAIRealtimeUpdateOptionsMessage(options llm.RealtimeSessionOptions) map[string]any {
	return openAIRealtimeUpdateOptionsMessageWithEventID(options, "")
}

func openAIRealtimeUpdateOptionsMessageWithEventID(options llm.RealtimeSessionOptions, eventID string) map[string]any {
	session := make(map[string]any)
	if toolChoice := openAIRealtimeToolChoice(options.ToolChoice); toolChoice != nil {
		session["tool_choice"] = toolChoice
	} else if options.ToolChoiceSet {
		session["tool_choice"] = "auto"
	}
	if options.MaxResponseOutputTokensSet || options.MaxResponseOutputTokens != nil {
		session["max_output_tokens"] = options.MaxResponseOutputTokens
	}
	if options.TruncationSet || options.Truncation != nil {
		session["truncation"] = options.Truncation
	}
	if options.TracingSet || options.Tracing != nil {
		session["tracing"] = options.Tracing
	}
	if options.ReasoningSet || options.Reasoning != nil {
		session["reasoning"] = options.Reasoning
	}
	input := make(map[string]any)
	if options.TurnDetectionSet || options.TurnDetection != nil {
		input["turn_detection"] = options.TurnDetection
	}
	if options.InputAudioTranscriptionSet || options.InputAudioTranscription != nil {
		input["transcription"] = options.InputAudioTranscription
	}
	if options.InputAudioNoiseReductionSet || options.InputAudioNoiseReduction != nil {
		input["noise_reduction"] = openAIRealtimeNoiseReduction(options.InputAudioNoiseReduction)
	}
	output := make(map[string]any)
	if options.VoiceSet || options.Voice != "" {
		output["voice"] = options.Voice
	}
	if options.SpeedSet || options.Speed > 0 {
		output["speed"] = options.Speed
	}
	if len(input) > 0 || len(output) > 0 {
		audio := make(map[string]any)
		if len(input) > 0 {
			audio["input"] = input
		}
		if len(output) > 0 {
			audio["output"] = output
		}
		session["audio"] = audio
	}
	if len(session) == 0 {
		return nil
	}
	msg := map[string]any{
		"type":    "session.update",
		"session": session,
	}
	if eventID != "" {
		msg["event_id"] = eventID
	}
	return msg
}

func openAIRealtimeNoiseReduction(noiseReduction any) any {
	if noiseReduction == nil {
		return nil
	}
	if kind, ok := noiseReduction.(string); ok {
		return map[string]any{"type": kind}
	}
	return noiseReduction
}

func openAIRealtimeChangedOptionsSession(session map[string]any, current map[string]any) (map[string]any, map[string]any) {
	options := openAIRealtimeOptionEntries(session)
	changedOptions := make(map[string]any)
	for key, value := range options {
		if current != nil && reflect.DeepEqual(current[key], value) {
			continue
		}
		changedOptions[key] = value
	}
	return openAIRealtimeSessionFromOptionEntries(changedOptions), changedOptions
}

func openAIRealtimeMergeSessionOptions(dst *llm.RealtimeSessionOptions, src llm.RealtimeSessionOptions) {
	if src.ToolChoiceSet || src.ToolChoice != nil {
		dst.ToolChoice = src.ToolChoice
		dst.ToolChoiceSet = true
	}
	if src.MaxResponseOutputTokensSet || src.MaxResponseOutputTokens != nil {
		dst.MaxResponseOutputTokens = src.MaxResponseOutputTokens
		dst.MaxResponseOutputTokensSet = true
	}
	if src.TruncationSet || src.Truncation != nil {
		dst.Truncation = src.Truncation
		dst.TruncationSet = true
	}
	if src.TracingSet || src.Tracing != nil {
		dst.Tracing = src.Tracing
		dst.TracingSet = true
	}
	if src.ReasoningSet || src.Reasoning != nil {
		dst.Reasoning = src.Reasoning
		dst.ReasoningSet = true
	}
	if src.TurnDetectionSet || src.TurnDetection != nil {
		dst.TurnDetection = src.TurnDetection
		dst.TurnDetectionSet = true
	}
	if src.InputAudioTranscriptionSet || src.InputAudioTranscription != nil {
		dst.InputAudioTranscription = src.InputAudioTranscription
		dst.InputAudioTranscriptionSet = true
	}
	if src.InputAudioNoiseReductionSet || src.InputAudioNoiseReduction != nil {
		dst.InputAudioNoiseReduction = src.InputAudioNoiseReduction
		dst.InputAudioNoiseReductionSet = true
	}
	if src.VoiceSet || src.Voice != "" {
		dst.Voice = src.Voice
		dst.VoiceSet = true
	}
	if src.SpeedSet || src.Speed > 0 {
		dst.Speed = src.Speed
		dst.SpeedSet = true
	}
}

func openAIRealtimeModelActiveSessionOptions(update, stored llm.RealtimeSessionOptions) llm.RealtimeSessionOptions {
	active := update
	if stored.TurnDetectionSet || stored.TurnDetection != nil {
		active.TurnDetection = openAIRealtimeCloneOptionValue(stored.TurnDetection)
		active.TurnDetectionSet = true
	}
	if stored.InputAudioTranscriptionSet || stored.InputAudioTranscription != nil {
		active.InputAudioTranscription = openAIRealtimeCloneOptionValue(stored.InputAudioTranscription)
		active.InputAudioTranscriptionSet = true
	}
	if stored.InputAudioNoiseReductionSet || stored.InputAudioNoiseReduction != nil {
		active.InputAudioNoiseReduction = openAIRealtimeCloneOptionValue(stored.InputAudioNoiseReduction)
		active.InputAudioNoiseReductionSet = true
	}
	return active
}

func openAIRealtimeMergeSessionPayload(dst map[string]any, src map[string]any) {
	for key, value := range src {
		if key != "audio" {
			dst[key] = value
			continue
		}
		dstAudio, _ := dst["audio"].(map[string]any)
		if dstAudio == nil {
			dstAudio = make(map[string]any)
			dst["audio"] = dstAudio
		}
		srcAudio, _ := value.(map[string]any)
		for audioKey, audioValue := range srcAudio {
			dstConfig, _ := dstAudio[audioKey].(map[string]any)
			if dstConfig == nil {
				dstConfig = make(map[string]any)
				dstAudio[audioKey] = dstConfig
			}
			srcConfig, _ := audioValue.(map[string]any)
			for configKey, configValue := range srcConfig {
				dstConfig[configKey] = configValue
			}
		}
	}
}

func openAIRealtimeRemoveInputNoiseReduction(session map[string]any) {
	audio, _ := session["audio"].(map[string]any)
	input, _ := audio["input"].(map[string]any)
	delete(input, "noise_reduction")
}

func openAIRealtimeOptionEntries(session map[string]any) map[string]any {
	entries := make(map[string]any)
	if value, ok := session["tool_choice"]; ok {
		entries["tool_choice"] = value
	}
	if value, ok := session["max_output_tokens"]; ok {
		entries["max_output_tokens"] = value
	}
	if value, ok := session["truncation"]; ok {
		entries["truncation"] = openAIRealtimeCloneOptionValue(value)
	}
	if value, ok := session["tracing"]; ok {
		entries["tracing"] = openAIRealtimeCloneOptionValue(value)
	}
	if value, ok := session["reasoning"]; ok {
		entries["reasoning"] = openAIRealtimeCloneOptionValue(value)
	}
	audio, _ := session["audio"].(map[string]any)
	input, _ := audio["input"].(map[string]any)
	if value, ok := input["turn_detection"]; ok {
		entries["audio.input.turn_detection"] = openAIRealtimeCloneOptionValue(value)
	}
	if value, ok := input["transcription"]; ok {
		entries["audio.input.transcription"] = openAIRealtimeCloneOptionValue(value)
	}
	if value, ok := input["noise_reduction"]; ok {
		entries["audio.input.noise_reduction"] = openAIRealtimeCloneOptionValue(value)
	}
	output, _ := audio["output"].(map[string]any)
	if value, ok := output["voice"]; ok {
		entries["audio.output.voice"] = value
	}
	if value, ok := output["speed"]; ok {
		entries["audio.output.speed"] = value
	}
	return entries
}

func openAIRealtimeCloneOptionValue(value any) any {
	if value == nil {
		return nil
	}
	clone, ok := openAIRealtimeCloneReflectValue(reflect.ValueOf(value))
	if !ok {
		return value
	}
	return clone.Interface()
}

func openAIRealtimeCloneReflectValue(value reflect.Value) (reflect.Value, bool) {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return value, true
		}
		clone, ok := openAIRealtimeCloneReflectValue(value.Elem())
		if !ok {
			return value, false
		}
		if clone.Type().AssignableTo(value.Type()) {
			return clone, true
		}
		if clone.Type().ConvertibleTo(value.Type()) {
			return clone.Convert(value.Type()), true
		}
		return value, false
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type()), true
		}
		clone := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			entry := iter.Value()
			clonedEntry, ok := openAIRealtimeCloneReflectValue(entry)
			if !ok {
				clonedEntry = entry
			}
			if clonedEntry.Type().AssignableTo(value.Type().Elem()) {
				clone.SetMapIndex(iter.Key(), clonedEntry)
				continue
			}
			if clonedEntry.Type().ConvertibleTo(value.Type().Elem()) {
				clone.SetMapIndex(iter.Key(), clonedEntry.Convert(value.Type().Elem()))
				continue
			}
			clone.SetMapIndex(iter.Key(), entry)
		}
		return clone, true
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type()), true
		}
		clone := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			entry := value.Index(i)
			clonedEntry, ok := openAIRealtimeCloneReflectValue(entry)
			if !ok {
				clonedEntry = entry
			}
			if clonedEntry.Type().AssignableTo(value.Type().Elem()) {
				clone.Index(i).Set(clonedEntry)
				continue
			}
			if clonedEntry.Type().ConvertibleTo(value.Type().Elem()) {
				clone.Index(i).Set(clonedEntry.Convert(value.Type().Elem()))
				continue
			}
			clone.Index(i).Set(entry)
		}
		return clone, true
	default:
		return value, false
	}
}

func openAIRealtimeSessionFromOptionEntries(entries map[string]any) map[string]any {
	session := make(map[string]any)
	input := make(map[string]any)
	output := make(map[string]any)
	for key, value := range entries {
		switch key {
		case "tool_choice", "max_output_tokens", "truncation", "tracing", "reasoning":
			session[key] = value
		case "audio.input.turn_detection":
			input["turn_detection"] = value
		case "audio.input.transcription":
			input["transcription"] = value
		case "audio.input.noise_reduction":
			input["noise_reduction"] = value
		case "audio.output.voice":
			output["voice"] = value
		case "audio.output.speed":
			output["speed"] = value
		}
	}
	if len(input) > 0 || len(output) > 0 {
		audio := make(map[string]any)
		if len(input) > 0 {
			audio["input"] = input
		}
		if len(output) > 0 {
			audio["output"] = output
		}
		session["audio"] = audio
	}
	return session
}

func openAIRealtimeToolChoice(choice llm.ToolChoice) any {
	switch tc := choice.(type) {
	case nil:
		return nil
	case string:
		return tc
	case map[string]any:
		if tc["type"] != "function" {
			return nil
		}
		function, ok := tc["function"].(map[string]any)
		if !ok {
			return nil
		}
		name, ok := function["name"].(string)
		if !ok || name == "" {
			return nil
		}
		return map[string]any{
			"type": "function",
			"name": name,
		}
	default:
		return nil
	}
}

func (s *realtimeSession) Interrupt() error {
	s.mu.Lock()
	hasGeneration := s.generation != nil
	hasPendingResponse := len(s.pendingResponses) > 0
	active := hasGeneration || hasPendingResponse
	s.mu.Unlock()
	if !active {
		return nil
	}
	msg := map[string]any{
		"type": "response.cancel",
	}
	if err := s.sendMsg(msg); err != nil {
		return err
	}
	return nil
}

func (s *realtimeSession) PushAudio(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	frame, err := s.audioNormalizer.normalize(frame)
	if err != nil {
		return err
	}
	if s.audioBStream == nil {
		s.audioBStream = newOpenAIRealtimeAudioByteStream()
	}

	for _, chunk := range s.audioBStream.Write(frame.Data) {
		if err := s.appendAudioChunk(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *realtimeSession) appendAudioChunk(chunk *model.AudioFrame) error {
	msg := map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString(chunk.Data),
	}
	if err := s.sendMsg(msg); err != nil {
		return err
	}
	if chunk.SampleRate > 0 {
		s.pushedDuration += float64(chunk.SamplesPerChannel) / float64(chunk.SampleRate)
	}
	return nil
}

type openAIRealtimeInputAudioNormalizer struct {
	sampleRate  uint32
	numChannels uint32
	remainder   uint64
	lastSample  int16
	hasTail     bool
}

func (n *openAIRealtimeInputAudioNormalizer) normalize(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if frame == nil {
		return nil, nil
	}
	if frame.SampleRate == 0 || frame.NumChannels == 0 {
		return frame, nil
	}
	if frame.SampleRate == openAIRealtimeInputSampleRate && frame.NumChannels == openAIRealtimeInputNumChannels {
		n.reset()
		return frame, nil
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("openai realtime input audio must be 16-bit PCM")
	}
	samplesPerChannel := frame.SamplesPerChannel
	if samplesPerChannel == 0 {
		samplesPerChannel = uint32(len(frame.Data)) / frame.NumChannels / 2
	}
	expectedBytes := int(samplesPerChannel * frame.NumChannels * 2)
	if len(frame.Data) < expectedBytes {
		return nil, fmt.Errorf("openai realtime input audio data is shorter than declared sample count")
	}
	if n.sampleRate != frame.SampleRate || n.numChannels != frame.NumChannels {
		n.sampleRate = frame.SampleRate
		n.numChannels = frame.NumChannels
		n.remainder = 0
	}
	if samplesPerChannel == 0 {
		return &model.AudioFrame{
			SampleRate:        openAIRealtimeInputSampleRate,
			NumChannels:       openAIRealtimeInputNumChannels,
			SamplesPerChannel: 0,
		}, nil
	}

	scaledSamples := uint64(samplesPerChannel)*uint64(openAIRealtimeInputSampleRate) + n.remainder
	outSamples := uint32(scaledSamples / uint64(frame.SampleRate))
	n.remainder = scaledSamples % uint64(frame.SampleRate)
	out := make([]byte, int(outSamples*openAIRealtimeInputNumChannels*2))
	channelCount := int(frame.NumChannels)
	n.hasTail = false
	for outIdx := uint32(0); outIdx < outSamples; outIdx++ {
		srcIdx := uint32(uint64(outIdx) * uint64(frame.SampleRate) / uint64(openAIRealtimeInputSampleRate))
		if srcIdx >= samplesPerChannel {
			srcIdx = samplesPerChannel - 1
		}
		var sum int32
		for ch := 0; ch < channelCount; ch++ {
			offset := (int(srcIdx)*channelCount + ch) * 2
			sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2])))
		}
		sample := sum / int32(channelCount)
		binary.LittleEndian.PutUint16(out[int(outIdx)*2:int(outIdx)*2+2], uint16(int16(sample)))
	}
	if n.remainder > 0 {
		srcIdx := samplesPerChannel - 1
		var sum int32
		for ch := 0; ch < channelCount; ch++ {
			offset := (int(srcIdx)*channelCount + ch) * 2
			sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2])))
		}
		n.lastSample = int16(sum / int32(channelCount))
		n.hasTail = true
	}

	return &model.AudioFrame{
		Data:              out,
		SampleRate:        openAIRealtimeInputSampleRate,
		NumChannels:       openAIRealtimeInputNumChannels,
		SamplesPerChannel: outSamples,
	}, nil
}

func (n *openAIRealtimeInputAudioNormalizer) flush() *model.AudioFrame {
	if n == nil || n.remainder == 0 || !n.hasTail || n.sampleRate == 0 {
		return nil
	}
	out := make([]byte, openAIRealtimeInputNumChannels*2)
	binary.LittleEndian.PutUint16(out, uint16(n.lastSample))
	n.remainder = 0
	n.hasTail = false
	return &model.AudioFrame{
		Data:              out,
		SampleRate:        openAIRealtimeInputSampleRate,
		NumChannels:       openAIRealtimeInputNumChannels,
		SamplesPerChannel: 1,
	}
}

func (n *openAIRealtimeInputAudioNormalizer) reset() {
	n.sampleRate = 0
	n.numChannels = 0
	n.remainder = 0
	n.lastSample = 0
	n.hasTail = false
}

func newOpenAIRealtimeAudioByteStream() *audio.AudioByteStream {
	return audio.NewAudioByteStream(
		openAIRealtimeInputSampleRate,
		openAIRealtimeInputNumChannels,
		openAIRealtimeInputSampleRate/10,
	)
}

func (s *realtimeSession) PushVideo(frame *images.VideoFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	data, err := images.Encode(frame, images.NewEncodeOptions())
	if err != nil {
		return err
	}
	image := &llm.ImageContent{
		Image:    fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(data)),
		MimeType: "image/jpeg",
	}
	msg, err := openAIRealtimeVideoMessage(image)
	if err != nil {
		return err
	}
	return s.sendMsg(msg)
}

func openAIRealtimeVideoMessage(image *llm.ImageContent) (map[string]any, error) {
	img, err := llm.SerializeImage(image)
	if err != nil {
		return nil, err
	}
	if img.ExternalURL != "" {
		return nil, fmt.Errorf("openai realtime input_image does not support external image URLs")
	}
	url := fmt.Sprintf("data:%s;base64,%s", img.MIMEType, base64.StdEncoding.EncodeToString(img.DataBytes))
	return map[string]any{
		"type":     "conversation.item.create",
		"event_id": cavosmath.ShortUUID("video_"),
		"item": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{
					"type":      "input_image",
					"image_url": url,
				},
			},
		},
	}, nil
}

func (s *realtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	s.mu.Lock()
	instructions := s.instructions
	s.mu.Unlock()
	msg := openAIRealtimeGenerateReplyMessageWithSessionInstructions(options, instructions)
	eventID, _ := msg["event_id"].(string)
	s.addPendingRealtimeResponse(eventID)
	if err := s.sendMsg(msg); err != nil {
		s.clearPendingRealtimeResponse(eventID)
		return err
	}
	s.startResponseCreateTimeout(eventID)
	return nil
}

func (s *realtimeSession) addPendingRealtimeResponse(eventID string) {
	if eventID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingResponses == nil {
		s.pendingResponses = make(map[string]struct{})
	}
	s.pendingResponses[eventID] = struct{}{}
}

func (s *realtimeSession) clearPendingRealtimeResponse(eventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingResponses) == 0 {
		return
	}
	if eventID != "" {
		delete(s.pendingResponses, eventID)
		return
	}
	for pendingID := range s.pendingResponses {
		delete(s.pendingResponses, pendingID)
		return
	}
}

func (s *realtimeSession) consumePendingRealtimeResponse(eventID string) bool {
	if eventID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pendingResponses[eventID]; !ok {
		return false
	}
	delete(s.pendingResponses, eventID)
	return true
}

func (s *realtimeSession) startResponseCreateTimeout(eventID string) {
	if eventID == "" {
		return
	}
	timeout := s.responseCreateTimeout
	if timeout == 0 {
		timeout = openAIRealtimeResponseCreateTimeout
	}
	timer := time.NewTimer(timeout)
	go func() {
		defer timer.Stop()
		select {
		case <-timer.C:
			s.clearPendingRealtimeResponse(eventID)
		case <-s.ctx.Done():
		}
	}()
}

func (s *realtimeSession) Say(text string) error {
	return llm.NewRealtimeError("openai realtime session does not support say", nil)
}

func openAIRealtimeGenerateReplyMessageWithSessionInstructions(options llm.RealtimeGenerateReplyOptions, sessionInstructions string) map[string]any {
	response := make(map[string]any)
	eventID := cavosmath.ShortUUID("response_create_")
	response["metadata"] = map[string]any{"client_event_id": eventID}
	if sessionInstructions != "" && options.Instructions != "" {
		options.Instructions = sessionInstructions + "\n" + options.Instructions
	}
	if options.Instructions != "" {
		response["instructions"] = options.Instructions
	}
	if toolChoice := openAIRealtimeToolChoice(options.ToolChoice); toolChoice != nil {
		response["tool_choice"] = toolChoice
	}
	if options.Tools != nil {
		response["tools"] = openAIRealtimeTools(options.Tools)
	}

	return map[string]any{
		"type":     "response.create",
		"event_id": eventID,
		"response": response,
	}
}

func (s *realtimeSession) Truncate(options llm.RealtimeTruncateOptions) error {
	if realtimeModalitiesInclude(options.Modalities, "audio") {
		return s.sendMsg(openAIRealtimeTruncateMessage(options))
	}
	if options.AudioTranscript == nil {
		return nil
	}
	if s.remote == nil {
		s.remote = llm.NewRemoteChatContext()
	}
	msgs, err := openAIRealtimeTruncateTranscriptUpdateMessages(s.remote.ToChatCtx(), options)
	if err != nil {
		return err
	}
	for _, msg := range msgs {
		if err := s.sendMsg(msg); err != nil {
			return err
		}
	}
	s.remote = openAIRealtimeRemoteSnapshot(openAIRealtimeTruncatedTranscriptChatContext(s.remote.ToChatCtx(), options))
	return nil
}

func openAIRealtimeTruncateMessage(options llm.RealtimeTruncateOptions) map[string]any {
	return map[string]any{
		"type":          "conversation.item.truncate",
		"content_index": 0,
		"item_id":       options.MessageID,
		"audio_end_ms":  options.AudioEndMillis,
	}
}

func realtimeModalitiesInclude(modalities []string, target string) bool {
	for _, modality := range modalities {
		if modality == target {
			return true
		}
	}
	return false
}

func openAIRealtimeTruncateTranscriptUpdateMessages(oldCtx *llm.ChatContext, options llm.RealtimeTruncateOptions) ([]map[string]any, error) {
	newCtx := openAIRealtimeTruncatedTranscriptChatContext(oldCtx, options)
	return openAIRealtimeChatContextUpdateMessages(oldCtx, newCtx)
}

func openAIRealtimeTruncatedTranscriptChatContext(oldCtx *llm.ChatContext, options llm.RealtimeTruncateOptions) *llm.ChatContext {
	newCtx := openAIRealtimeSyncedChatContext(oldCtx)
	if options.AudioTranscript == nil {
		return newCtx
	}
	idx := newCtx.IndexByID(options.MessageID)
	if idx == nil {
		return newCtx
	}
	msg, ok := newCtx.Items[*idx].(*llm.ChatMessage)
	if !ok {
		return newCtx
	}
	updated := *msg
	updated.Content = []llm.ChatContent{{Text: *options.AudioTranscript}}
	newCtx.Items[*idx] = &updated
	return newCtx
}

func (s *realtimeSession) CommitAudio() error {
	if tail := s.audioNormalizer.flush(); tail != nil {
		if s.audioBStream == nil {
			s.audioBStream = newOpenAIRealtimeAudioByteStream()
		}
		for _, chunk := range s.audioBStream.Push(tail.Data) {
			if err := s.appendAudioChunk(chunk); err != nil {
				return err
			}
		}
	}
	if s.audioBStream != nil {
		for _, chunk := range s.audioBStream.Flush() {
			if err := s.appendAudioChunk(chunk); err != nil {
				return err
			}
		}
	}
	if s.pushedDuration <= 0.1 {
		return nil
	}
	if err := s.sendMsg(openAIRealtimeCommitAudioMessage()); err != nil {
		return err
	}
	s.pushedDuration = 0
	return nil
}

func openAIRealtimeCommitAudioMessage() map[string]any {
	return map[string]any{
		"type": "input_audio_buffer.commit",
	}
}

func (s *realtimeSession) ClearAudio() error {
	if err := s.sendMsg(openAIRealtimeClearAudioMessage()); err != nil {
		return err
	}
	if s.audioBStream != nil {
		s.audioBStream.Clear()
	}
	s.audioNormalizer.reset()
	s.pushedDuration = 0
	return nil
}

func openAIRealtimeClearAudioMessage() map[string]any {
	return map[string]any{
		"type": "input_audio_buffer.clear",
	}
}

func (s *realtimeSession) Close() error {
	s.closeOnce.Do(func() {
		if s.model != nil {
			s.model.unregisterSession(s)
		}
		s.emitSessionCloseMetrics()
		s.closeRealtimeGeneration()
		s.cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closeErr = s.conn.Close()
	})
	return s.closeErr
}

func (s *realtimeSession) emitSessionCloseMetrics() {
	if s == nil || s.model == nil || s.model.sessionCloseMetricsHook == nil || s.connectedAt.IsZero() {
		return
	}
	metrics := s.model.sessionCloseMetricsHook(time.Since(s.connectedAt))
	if metrics == nil {
		return
	}
	s.sendRealtimeEvent(llm.RealtimeEvent{Type: llm.RealtimeEventTypeMetricsCollected, Metrics: metrics})
}

func (s *realtimeSession) sendMsg(msg any) error {
	s.mu.Lock()
	if s.ctx != nil && s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil
	}
	msg = s.prepareClientMessage(msg)
	s.trackPendingRealtimeDelete(msg)
	conn := s.conn
	s.mu.Unlock()
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if conn == nil {
		return fmt.Errorf("OpenAI realtime websocket is not connected")
	}
	return conn.WriteMessage(websocket.TextMessage, b)
}

func (s *realtimeSession) trackPendingRealtimeDelete(msg any) {
	payload, ok := msg.(map[string]any)
	if !ok || payload["type"] != "conversation.item.delete" {
		return
	}
	itemID, ok := payload["item_id"].(string)
	if !ok {
		return
	}
	s.pendingDeleteItemIDs = append(s.pendingDeleteItemIDs, itemID)
}

func (s *realtimeSession) prepareClientMessage(msg any) any {
	if s == nil || s.model == nil || !s.model.isLegacyAzureRealtime() {
		return msg
	}
	payload, ok := msg.(map[string]any)
	if !ok {
		return msg
	}
	return openAIRealtimeLegacyAzureClientMessage(payload)
}

func (m *RealtimeModel) isLegacyAzureRealtime() bool {
	return m != nil && m.isAzure && m.apiVersion != ""
}

func openAIRealtimeLegacyAzureClientMessage(msg map[string]any) map[string]any {
	if openAIRealtimeString(msg["type"]) != "session.update" {
		if openAIRealtimeString(msg["type"]) == "conversation.item.create" {
			return openAIRealtimeLegacyAzureConversationItemMessage(msg)
		}
		return msg
	}
	session, ok := msg["session"].(map[string]any)
	if !ok {
		return msg
	}
	converted := make(map[string]any, len(msg))
	for key, value := range msg {
		if key == "session" {
			converted[key] = openAIRealtimeLegacyAzureSession(session)
			continue
		}
		converted[key] = value
	}
	return converted
}

func openAIRealtimeLegacyAzureConversationItemMessage(msg map[string]any) map[string]any {
	item, ok := msg["item"].(map[string]any)
	if !ok {
		return msg
	}
	converted := make(map[string]any, len(msg))
	for key, value := range msg {
		converted[key] = value
	}
	converted["item"] = openAIRealtimeLegacyAzureConversationItem(item)
	return converted
}

func openAIRealtimeLegacyAzureConversationItem(item map[string]any) map[string]any {
	converted := make(map[string]any, len(item))
	for key, value := range item {
		converted[key] = value
	}
	content, ok := item["content"].([]map[string]any)
	if ok {
		converted["content"] = openAIRealtimeLegacyAzureContent(content)
		return converted
	}
	contentAny, ok := item["content"].([]any)
	if !ok {
		return converted
	}
	parts := make([]map[string]any, 0, len(contentAny))
	for _, part := range contentAny {
		if typed, ok := part.(map[string]any); ok {
			parts = append(parts, typed)
		}
	}
	converted["content"] = openAIRealtimeLegacyAzureContent(parts)
	return converted
}

func openAIRealtimeLegacyAzureContent(content []map[string]any) []map[string]any {
	converted := make([]map[string]any, 0, len(content))
	for _, part := range content {
		next := make(map[string]any, len(part))
		for key, value := range part {
			next[key] = value
		}
		if next["type"] == "output_text" {
			next["type"] = "text"
		}
		converted = append(converted, next)
	}
	return converted
}

func openAIRealtimeLegacyAzureSession(session map[string]any) map[string]any {
	mapped := make(map[string]any)
	for _, key := range []string{"model", "instructions", "tools", "tool_choice", "tracing", "reasoning"} {
		if value, ok := session[key]; ok {
			if value == nil {
				continue
			}
			mapped[key] = value
		}
	}
	if modalities, ok := session["output_modalities"]; ok {
		mapped["modalities"] = openAIRealtimeLegacyAzureModalities(modalities)
		mapped["input_audio_format"] = "pcm16"
		mapped["output_audio_format"] = "pcm16"
	}
	if maxTokens, ok := session["max_output_tokens"]; ok {
		if maxTokens != nil {
			mapped["max_response_output_tokens"] = maxTokens
		}
	}
	audio, _ := session["audio"].(map[string]any)
	input, _ := audio["input"].(map[string]any)
	if value, ok := input["noise_reduction"]; ok {
		if value != nil {
			mapped["input_audio_noise_reduction"] = value
		}
	}
	if value, ok := input["transcription"]; ok {
		if value != nil {
			mapped["input_audio_transcription"] = value
		}
	}
	if value, ok := input["turn_detection"]; ok {
		if value != nil {
			mapped["turn_detection"] = value
		}
	}
	output, _ := audio["output"].(map[string]any)
	if value, ok := output["voice"]; ok {
		if value != nil {
			mapped["voice"] = value
		}
	}
	if value, ok := output["speed"]; ok {
		if value != nil {
			mapped["speed"] = value
		}
	}
	return mapped
}

func openAIRealtimeLegacyAzureModalities(value any) []string {
	raw, ok := value.([]string)
	if ok && realtimeModalitiesInclude(raw, "audio") {
		return []string{"audio", "text"}
	}
	if ok {
		return append([]string(nil), raw...)
	}
	values, _ := value.([]any)
	modalities := make([]string, 0, len(values))
	for _, item := range values {
		if modality, ok := item.(string); ok {
			modalities = append(modalities, modality)
		}
	}
	if realtimeModalitiesInclude(modalities, "audio") {
		return []string{"audio", "text"}
	}
	return modalities
}

func (s *realtimeSession) eventLoop() {
	defer s.closeRealtimeEventStream()
	defer s.closeRealtimeGeneration()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			_, msg, err := s.conn.ReadMessage()
			if err != nil {
				if s.ctx.Err() != nil {
					return
				}
				if reconnectErr := s.reconnectAfterDisconnect(); reconnectErr != nil {
					logger.Logger.Errorw("OpenAI realtime disconnected", reconnectErr)
					s.cancel()
					return
				}
				continue
			}

			var ev map[string]any
			if err := json.Unmarshal(msg, &ev); err != nil {
				continue
			}

			if openAIRealtimeString(ev["type"]) == "response.done" && s.generation == nil {
				continue
			}

			trackedEvent, trackedOK := s.trackOpenAIRealtimeEvent(ev)
			realtimeEvent, ok := openAIRealtimeEvent(ev)
			if !ok {
				if trackedOK {
					s.sendRealtimeEvent(trackedEvent)
				}
				continue
			}
			if realtimeEvent.Type == llm.RealtimeEventTypeError {
				logger.Logger.Errorw("OpenAI realtime error", nil, "payload", string(msg))
			}
			if realtimeEvent.Type == llm.RealtimeEventTypeSpeechStopped && realtimeEvent.SpeechStopped != nil {
				realtimeEvent.SpeechStopped.UserTranscriptionEnabled = s.inputAudioTranscriptionEnabled()
			}
			if realtimeEvent.Type == llm.RealtimeEventTypeGenerationCreated && realtimeEvent.Generation != nil {
				if response, _ := ev["response"].(map[string]any); response != nil {
					if clientEventID, ok := openAIRealtimeResponseClientEventID(response); ok && s.consumePendingRealtimeResponse(clientEventID) {
						realtimeEvent.Generation.UserInitiated = true
					}
				}
			}
			if realtimeEvent.Type == llm.RealtimeEventTypeFunctionCall && realtimeEvent.Function != nil && !s.acceptRealtimeFunctionCall(realtimeEvent.Function.Name) {
				continue
			}
			realtimeEvent = s.trackRealtimeEvent(realtimeEvent)
			s.sendRealtimeEvent(realtimeEvent)
			if trackedOK {
				s.sendRealtimeEvent(trackedEvent)
			}
		}
	}
}

func (s *realtimeSession) startMaxSessionRecycle(conn *websocket.Conn) {
	if s == nil || s.model == nil || s.model.maxSession <= 0 || conn == nil {
		return
	}
	duration := s.model.maxSession
	go func() {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
		}
		ticker := time.NewTicker(openAIRealtimeActiveGenerationRecyclePoll)
		defer ticker.Stop()
		for {
			s.mu.Lock()
			current := s.conn == conn
			active := s.generation != nil
			s.mu.Unlock()
			if !current {
				return
			}
			if !active {
				_ = conn.Close()
				return
			}
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *realtimeSession) reconnectAfterDisconnect() error {
	if s.model == nil {
		return fmt.Errorf("OpenAI realtime disconnected")
	}
	wsURL := s.model.realtimeSessionURL()
	header := s.model.realtimeHeaders()

	conn, err := s.model.dialRealtimeWebsocket(s.ctx, wsURL, header)
	if err != nil {
		return fmt.Errorf("failed to reconnect to OpenAI realtime: %w", err)
	}

	s.mu.Lock()
	oldConn := s.conn
	instructions := s.instructions
	instructionsSet := s.instructionsSet
	tools := append([]llm.Tool(nil), s.tools...)
	optionsState := make(map[string]any, len(s.optionsState))
	for key, value := range s.optionsState {
		optionsState[key] = value
	}
	var remoteCtx *llm.ChatContext
	if s.remote != nil {
		remoteCtx = s.remote.ToChatCtx()
	}
	s.mu.Unlock()

	initialSession := openAIRealtimeInitialSession(s.model.model, s.model.initialRealtimeModalities()...)
	openAIRealtimeMergeSessionPayload(initialSession, openAIRealtimeSessionFromOptionEntries(optionsState))
	if s.model.isAzure {
		if _, ok := optionsState["audio.input.noise_reduction"]; !ok {
			openAIRealtimeRemoveInputNoiseReduction(initialSession)
		}
	}
	if instructionsSet {
		initialSession["instructions"] = instructions
	}
	msg := openAIRealtimeInitialSessionUpdateMessage(initialSession)
	if s.model.isLegacyAzureRealtime() {
		msg = openAIRealtimeLegacyAzureClientMessage(msg)
	}
	b, err := json.Marshal(msg)
	if err == nil {
		err = conn.WriteMessage(websocket.TextMessage, b)
	}
	if err == nil && len(tools) > 0 {
		toolsMsg := map[string]any{
			"type":     "session.update",
			"event_id": cavosmath.ShortUUID("tools_update_"),
			"session": map[string]any{
				"type":  "realtime",
				"model": s.model.model,
				"tools": s.model.realtimeTools(tools),
			},
		}
		if s.model.isLegacyAzureRealtime() {
			toolsMsg = openAIRealtimeLegacyAzureClientMessage(toolsMsg)
		}
		b, err = json.Marshal(toolsMsg)
		if err == nil {
			err = conn.WriteMessage(websocket.TextMessage, b)
		}
	}
	var chatMsgs []map[string]any
	if err == nil && remoteCtx != nil {
		chatMsgs, err = openAIRealtimeChatContextCreateMessages(remoteCtx)
		for _, chatMsg := range chatMsgs {
			if err != nil {
				break
			}
			if s.model.isLegacyAzureRealtime() {
				chatMsg = openAIRealtimeLegacyAzureClientMessage(chatMsg)
			}
			b, err = json.Marshal(chatMsg)
			if err == nil {
				err = conn.WriteMessage(websocket.TextMessage, b)
			}
		}
	}
	if err != nil {
		_ = conn.Close()
		return err
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		_ = conn.Close()
		return s.ctx.Err()
	}
	s.conn = conn
	s.pendingResponses = make(map[string]struct{})
	s.inputTranscripts = nil
	if s.audioBStream != nil {
		s.audioBStream.Clear()
	}
	s.audioNormalizer.reset()
	s.pushedDuration = 0
	s.startMaxSessionRecycle(conn)
	s.mu.Unlock()

	_ = oldConn.Close()
	s.closeRealtimeGeneration()
	s.sendRealtimeEvent(llm.RealtimeEvent{
		Type:      llm.RealtimeEventTypeSessionReconnected,
		Reconnect: &llm.RealtimeSessionReconnectedEvent{},
	})
	return nil
}

func (s *realtimeSession) inputAudioTranscriptionEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.optionsState["audio.input.transcription"]
	return ok && value != nil
}

func (s *realtimeSession) acceptRealtimeFunctionCall(name string) bool {
	if s == nil || s.model == nil || s.model.functionCallFilter == nil {
		return true
	}
	s.mu.Lock()
	tools := append([]llm.Tool(nil), s.tools...)
	s.mu.Unlock()
	return s.model.functionCallFilter(tools, name)
}

func (s *realtimeSession) trackOpenAIRealtimeEvent(ev map[string]any) (llm.RealtimeEvent, bool) {
	evType, _ := ev["type"].(string)
	switch evType {
	case "response.output_item.added":
		item, _ := ev["item"].(map[string]any)
		itemID, hasItemID := item["id"].(string)
		itemType, _ := item["type"].(string)
		if !hasItemID || itemType != "message" || s.generation == nil {
			return llm.RealtimeEvent{}, false
		}
		msg := &realtimeMessageGeneration{
			textStream:   newOpenAIRealtimeQueuedStream[string](),
			timedText:    newOpenAIRealtimeQueuedStream[llm.RealtimeTimedText](),
			audioStream:  newOpenAIRealtimeQueuedStream[*model.AudioFrame](),
			modalitiesCh: make(chan []string, 1),
		}
		if s.model != nil && !s.model.Capabilities().AudioOutput {
			msg.modalities = []string{"text"}
			msg.modalitiesCh <- msg.modalities
			s.closeRealtimeMessageAudioStream(msg)
		}
		s.generation.messages[itemID] = msg
		message := llm.MessageGeneration{
			MessageID:    itemID,
			TextCh:       msg.textStream.Chan(),
			TimedTextCh:  msg.timedText.Chan(),
			AudioCh:      msg.audioStream.Chan(),
			ModalitiesCh: msg.modalitiesCh,
		}
		if !s.generation.sendMessage(message) {
			logger.Logger.Warnw("dropping OpenAI realtime message generation for full stream", nil, "item_id", itemID)
		}
	case "response.content_part.added":
		itemID, hasItemID := ev["item_id"].(string)
		part, _ := ev["part"].(map[string]any)
		partType, hasPartType := part["type"].(string)
		if !hasItemID || !hasPartType {
			return llm.RealtimeEvent{}, false
		}
		if partType == "text" {
			s.setRealtimeMessageModalities(itemID, []string{"text"})
		} else {
			s.setRealtimeMessageModalities(itemID, []string{"audio", "text"})
		}
	case "response.output_item.done":
		item, _ := ev["item"].(map[string]any)
		itemID, hasItemID := item["id"].(string)
		itemType, _ := item["type"].(string)
		if !hasItemID || s.generation == nil {
			return llm.RealtimeEvent{}, false
		}
		switch itemType {
		case "message":
			if msg := s.generation.messages[itemID]; msg != nil {
				s.setRealtimeMessageModalities(itemID, s.defaultRealtimeMessageModalities())
				s.closeRealtimeMessageStreams(msg)
			}
		case "function_call":
			call, err := openAIRealtimeFunctionCall(item)
			if err != nil {
				return llm.RealtimeEvent{}, false
			}
			if !s.acceptRealtimeFunctionCall(call.Name) {
				return llm.RealtimeEvent{}, false
			}
			if !s.generation.sendFunction(call) {
				logger.Logger.Warnw("dropping OpenAI realtime function call for full stream", nil, "item_id", itemID)
			}
		}
	case "response.output_text.delta", "response.text.delta":
		itemID, hasItemID := ev["item_id"].(string)
		if !hasItemID {
			return llm.RealtimeEvent{}, false
		}
		delta, _ := ev["delta"].(string)
		s.trackRealtimeOutputTranscript(itemID, delta)
	case "response.output_audio_transcript.delta", "response.audio_transcript.delta":
		itemID, hasItemID := ev["item_id"].(string)
		if !hasItemID {
			return llm.RealtimeEvent{}, false
		}
		delta, _ := ev["delta"].(string)
		s.trackRealtimeOutputTranscript(itemID, delta)
	case "response.output_audio.delta", "response.audio.delta":
		itemID, hasItemID := ev["item_id"].(string)
		if !hasItemID {
			return llm.RealtimeEvent{}, false
		}
		s.trackRealtimeOutputAudioDelta(itemID)
	case "response.done":
		hadGeneration := s.generation != nil
		response, _ := ev["response"].(map[string]any)
		s.persistRealtimeAudioTranscripts()
		s.closeRealtimeGeneration()
		if hadGeneration {
			return openAIRealtimeResponseDoneError(response)
		}
	case "conversation.item.deleted":
		itemID, hasItemID := ev["item_id"].(string)
		if !hasItemID {
			return llm.RealtimeEvent{}, false
		}
		itemID = s.resolveRealtimeDeletedItemID(itemID)
		if s.remote == nil {
			s.remote = llm.NewRemoteChatContext()
		}
		if err := s.remote.Delete(itemID); err != nil {
			logger.Logger.Warnw("failed to track OpenAI realtime deleted item", err, "item_id", itemID)
		}
		s.clearRealtimeInputTranscripts(itemID)
		s.resolveRealtimeChatContextDeleteAck(itemID)
	case "conversation.item.added", "conversation.item.created":
		item, _ := ev["item"].(map[string]any)
		itemID, _ := item["id"].(string)
		s.resolveRealtimeChatContextCreateAck(itemID)
	case "conversation.item.input_audio_transcription.failed":
		itemID, hasItemID := ev["item_id"].(string)
		if !hasItemID {
			return llm.RealtimeEvent{}, false
		}
		contentIndex := openAIRealtimeInt(ev["content_index"])
		logger.Logger.Errorw("OpenAI realtime input audio transcription failed", nil, "item_id", itemID, "error", ev["error"])
		partial, ok := s.clearRealtimeInputTranscript(itemID, contentIndex)
		if !ok {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:       itemID,
				ContentIndex: contentIndex,
				Transcript:   partial,
				IsFinal:      true,
			},
		}, true
	}
	return llm.RealtimeEvent{}, false
}

func (s *realtimeSession) trackRealtimeOutputTranscript(itemID, delta string) {
	if s.generation == nil {
		return
	}
	if msg := s.generation.messages[itemID]; msg != nil {
		msg.transcript += delta
	}
}

func (s *realtimeSession) trackRealtimeOutputAudioDelta(itemID string) {
	if s.generation == nil {
		return
	}
	if msg := s.generation.messages[itemID]; msg != nil {
		if s.generation.timing.firstTokenAt.IsZero() {
			s.generation.timing.firstTokenAt = time.Now()
		}
		s.setRealtimeMessageModalities(itemID, []string{"audio", "text"})
	}
}

func (s *realtimeSession) resolveRealtimeDeletedItemID(itemID string) string {
	if itemID == "" && len(s.pendingDeleteItemIDs) > 0 {
		itemID = s.pendingDeleteItemIDs[0]
	}
	for i, pendingItemID := range s.pendingDeleteItemIDs {
		if pendingItemID == itemID {
			s.pendingDeleteItemIDs = append(s.pendingDeleteItemIDs[:i], s.pendingDeleteItemIDs[i+1:]...)
			break
		}
	}
	return itemID
}

func (s *realtimeSession) persistRealtimeAudioTranscripts() {
	if s.generation == nil || s.remote == nil {
		return
	}
	for itemID, generation := range s.generation.messages {
		msg, ok := s.remote.Get(itemID).(*llm.ChatMessage)
		if !ok {
			continue
		}
		msg.Content = append(msg.Content, llm.ChatContent{Text: generation.transcript})
	}
}

func openAIRealtimeResponseDoneError(response map[string]any) (llm.RealtimeEvent, bool) {
	status, _ := response["status"].(string)
	if status != "failed" {
		return llm.RealtimeEvent{}, false
	}
	rawDetails := response["status_details"]
	statusDetails, _ := rawDetails.(map[string]any)
	errorBody, hasError := statusDetails["error"].(map[string]any)
	message := fmt.Sprintf("OpenAI Realtime API response %s with unknown error", status)
	var body any
	if hasError {
		errorType := openAIRealtimeString(errorBody["type"])
		if errorType == "" {
			errorType = "unknown"
		}
		message = fmt.Sprintf("OpenAI Realtime API response %s with error type: %s", status, errorType)
		body = errorBody
	}
	return llm.RealtimeEvent{
		Type:  llm.RealtimeEventTypeError,
		Error: llm.NewAPIError(message, body, true),
	}, true
}

func (s *realtimeSession) trackRealtimeEvent(ev llm.RealtimeEvent) llm.RealtimeEvent {
	if ev.Type == llm.RealtimeEventTypeGenerationCreated {
		return s.trackRealtimeGenerationCreated(ev)
	}
	if ev.Type == llm.RealtimeEventTypeText {
		s.trackRealtimeText(ev)
		return ev
	}
	if ev.Type == llm.RealtimeEventTypeAudio {
		s.trackRealtimeAudio(ev)
		return ev
	}
	if ev.Type == llm.RealtimeEventTypeRemoteItemAdded {
		s.trackRealtimeRemoteItemAdded(ev)
		return ev
	}
	if ev.Type == llm.RealtimeEventTypeInputAudioTranscriptionCompleted {
		return s.trackRealtimeInputTranscription(ev)
	}
	if ev.Type == llm.RealtimeEventTypeMetricsCollected {
		return s.trackRealtimeMetrics(ev)
	}
	return ev
}

func (s *realtimeSession) trackRealtimeGenerationCreated(ev llm.RealtimeEvent) llm.RealtimeEvent {
	if ev.Generation == nil {
		return ev
	}
	messageStream := newOpenAIRealtimeQueuedStream[llm.MessageGeneration]()
	functionStream := newOpenAIRealtimeQueuedStream[*llm.FunctionCall]()
	generation := &realtimeGeneration{
		messageCh:      messageStream.rawChan(),
		functionCh:     functionStream.rawChan(),
		messageStream:  messageStream,
		functionStream: functionStream,
		messages:       make(map[string]*realtimeMessageGeneration),
		timing: realtimeGenerationTiming{
			createdAt: time.Now(),
		},
	}
	s.closeRealtimeGeneration()
	s.generation = generation
	ev.Generation.MessageCh = generation.messageCh
	ev.Generation.FunctionCh = generation.functionCh
	return ev
}

func (g *realtimeGeneration) sendMessage(message llm.MessageGeneration) bool {
	if g == nil {
		return false
	}
	if g.messageStream != nil {
		return g.messageStream.Send(message)
	}
	select {
	case g.messageCh <- message:
		return true
	default:
		return false
	}
}

func (g *realtimeGeneration) sendFunction(call *llm.FunctionCall) bool {
	if g == nil {
		return false
	}
	if g.functionStream != nil {
		return g.functionStream.Send(call)
	}
	select {
	case g.functionCh <- call:
		return true
	default:
		return false
	}
}

func (g *realtimeGeneration) closeStreams() {
	if g == nil {
		return
	}
	if g.messageStream != nil {
		g.messageStream.Close()
	} else {
		close(g.messageCh)
	}
	if g.functionStream != nil {
		g.functionStream.Close()
	} else {
		close(g.functionCh)
	}
}

func (s *realtimeSession) trackRealtimeText(ev llm.RealtimeEvent) {
	if s.generation == nil {
		return
	}
	msg := s.generation.messages[ev.ItemID]
	if msg == nil {
		return
	}
	if msg.audioClosed && s.generation.timing.firstTokenAt.IsZero() {
		s.generation.timing.firstTokenAt = time.Now()
	}
	if !msg.textStream.Send(ev.Text) {
		logger.Logger.Warnw("dropping OpenAI realtime text delta for full message stream", nil, "item_id", ev.ItemID)
	}
	if ev.TimedText == nil {
		return
	}
	if !msg.timedText.Send(*ev.TimedText) {
		logger.Logger.Warnw("dropping OpenAI realtime timed text delta for full message stream", nil, "item_id", ev.ItemID)
	}
}

func (s *realtimeSession) trackRealtimeAudio(ev llm.RealtimeEvent) {
	if s.generation == nil {
		return
	}
	msg := s.generation.messages[ev.ItemID]
	if msg == nil {
		return
	}
	if s.generation.timing.firstTokenAt.IsZero() {
		s.generation.timing.firstTokenAt = time.Now()
	}
	frame := &model.AudioFrame{
		Data:              ev.Data,
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(ev.Data) / 2),
	}
	s.setRealtimeMessageModalities(ev.ItemID, []string{"audio", "text"})
	if !msg.audioStream.Send(frame) {
		logger.Logger.Warnw("dropping OpenAI realtime audio delta for full message stream", nil, "item_id", ev.ItemID)
	}
}

func (s *realtimeSession) trackRealtimeMetrics(ev llm.RealtimeEvent) llm.RealtimeEvent {
	if ev.Metrics == nil {
		return ev
	}
	if ev.Metrics.Metadata == nil && s.model != nil {
		ev.Metrics.Metadata = &telemetry.Metadata{
			ModelName:     llm.RealtimeModelName(s.model),
			ModelProvider: llm.RealtimeProvider(s.model),
		}
	}
	timing := s.lastGeneration
	if s.generation != nil && !s.generation.timing.createdAt.IsZero() {
		timing = s.generation.timing
	}
	if timing.createdAt.IsZero() {
		return ev
	}
	now := time.Now()
	ev.Metrics.Timestamp = timing.createdAt
	ev.Metrics.Duration = now.Sub(timing.createdAt).Seconds()
	if timing.firstTokenAt.IsZero() {
		ev.Metrics.TTFT = -1
	} else {
		ev.Metrics.TTFT = timing.firstTokenAt.Sub(timing.createdAt).Seconds()
	}
	if ev.Metrics.Duration > 0 {
		ev.Metrics.TokensPerSecond = float64(ev.Metrics.OutputTokens) / ev.Metrics.Duration
	}
	s.lastGeneration = realtimeGenerationTiming{}
	return ev
}

func (s *realtimeSession) setRealtimeMessageModalities(itemID string, modalities []string) {
	if s.generation == nil {
		return
	}
	msg := s.generation.messages[itemID]
	if msg == nil || msg.streamsClosed || msg.modalities != nil {
		return
	}
	msg.modalities = append([]string(nil), modalities...)
	select {
	case msg.modalitiesCh <- msg.modalities:
	default:
		logger.Logger.Warnw("dropping OpenAI realtime message modalities for full stream", nil, "item_id", itemID)
	}
}

func (s *realtimeSession) defaultRealtimeMessageModalities() []string {
	if s != nil && s.model != nil && s.model.modalitiesSet {
		return append([]string(nil), s.model.modalities...)
	}
	return []string{"text", "audio"}
}

func (s *realtimeSession) closeRealtimeGeneration() {
	if s.generation == nil {
		return
	}
	s.lastGeneration = s.generation.timing
	for _, msg := range s.generation.messages {
		s.closeRealtimeMessageStreams(msg)
	}
	s.generation.closeStreams()
	s.generation = nil
}

func (s *realtimeSession) closeRealtimeMessageStreams(msg *realtimeMessageGeneration) {
	if msg == nil || msg.streamsClosed {
		return
	}
	if msg.modalities == nil {
		msg.modalities = s.defaultRealtimeMessageModalities()
		select {
		case msg.modalitiesCh <- msg.modalities:
		default:
		}
	}
	msg.textStream.Close()
	msg.timedText.Close()
	s.closeRealtimeMessageAudioStream(msg)
	close(msg.modalitiesCh)
	msg.streamsClosed = true
}

func (s *realtimeSession) closeRealtimeMessageAudioStream(msg *realtimeMessageGeneration) {
	if msg == nil || msg.audioClosed {
		return
	}
	msg.audioStream.Close()
	msg.audioClosed = true
}

func (s *realtimeSession) trackRealtimeRemoteItemAdded(ev llm.RealtimeEvent) {
	if ev.RemoteItem == nil || ev.RemoteItem.Item == nil {
		return
	}
	if s.remote == nil {
		s.remote = llm.NewRemoteChatContext()
	}
	if s.model != nil && s.model.remoteItemAddedHook != nil {
		s.model.remoteItemAddedHook(s.remote, ev.RemoteItem)
	}
	var previousItemID *string
	if ev.RemoteItem.PreviousItemIDSet {
		previousItemID = &ev.RemoteItem.PreviousItemID
	} else if items := s.remote.ToChatCtx().Items; len(items) > 0 {
		itemID := items[len(items)-1].GetID()
		previousItemID = &itemID
	}
	if err := s.remote.Insert(previousItemID, ev.RemoteItem.Item); err != nil {
		logger.Logger.Warnw("failed to track OpenAI realtime remote item", err, "item_id", ev.RemoteItem.Item.GetID())
	}
}

func (s *realtimeSession) trackRealtimeInputTranscription(ev llm.RealtimeEvent) llm.RealtimeEvent {
	transcription := ev.InputTranscription
	if transcription == nil {
		return ev
	}
	if !transcription.IsFinal {
		if s.inputTranscripts == nil {
			s.inputTranscripts = utils.NewBoundedDict[inputTranscriptKey, realtimeInputTranscript](maxRealtimeInputTranscripts)
		}
		key := inputTranscriptKey{itemID: transcription.ItemID, contentIndex: transcription.ContentIndex}
		accumulated := s.inputTranscripts.SetOrUpdate(key, func() realtimeInputTranscript {
			return realtimeInputTranscript{itemID: transcription.ItemID, contentIndex: transcription.ContentIndex}
		}, func(value realtimeInputTranscript) realtimeInputTranscript {
			value.transcript += transcription.Transcript
			return value
		})
		transcription.Transcript = accumulated.transcript
		return ev
	}
	s.clearRealtimeInputTranscript(transcription.ItemID, transcription.ContentIndex)
	if s.remote == nil {
		return ev
	}
	msg, ok := s.remote.Get(transcription.ItemID).(*llm.ChatMessage)
	if !ok {
		return ev
	}
	if s.model != nil && s.model.inputTranscriptionFinalHook != nil {
		s.model.inputTranscriptionFinalHook(msg, transcription)
	}
	if len(msg.Content) == 1 && msg.Content[0].Text == transcription.Transcript &&
		msg.Content[0].Image == nil && msg.Content[0].Audio == nil && msg.Content[0].Instructions == nil {
		msg.TranscriptConfidence = transcription.Confidence
		return ev
	}
	msg.Content = append(msg.Content, llm.ChatContent{Text: transcription.Transcript})
	msg.TranscriptConfidence = transcription.Confidence
	return ev
}

func (s *realtimeSession) clearRealtimeInputTranscript(itemID string, contentIndex int) (string, bool) {
	if s.inputTranscripts == nil {
		return "", false
	}
	_, partial, ok := s.inputTranscripts.PopIf(func(value realtimeInputTranscript) bool {
		return value.itemID == itemID && value.contentIndex == contentIndex
	})
	return partial.transcript, ok
}

func (s *realtimeSession) clearRealtimeInputTranscripts(itemID string) {
	if s.inputTranscripts == nil {
		return
	}
	for {
		_, _, ok := s.inputTranscripts.PopIf(func(value realtimeInputTranscript) bool {
			return value.itemID == itemID
		})
		if !ok {
			return
		}
	}
}

func openAIRealtimeEvent(ev map[string]any) (llm.RealtimeEvent, bool) {
	evType, _ := ev["type"].(string)
	switch evType {
	case "error":
		errorBody, _ := ev["error"].(map[string]any)
		if strings.HasPrefix(openAIRealtimeString(errorBody["message"]), "Cancellation failed") {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: llm.NewAPIError("OpenAI Realtime API returned an error", errorBody, true),
		}, true
	case "response.output_text.delta", "response.text.delta":
		itemID, hasItemID := ev["item_id"].(string)
		if delta, ok := ev["delta"].(string); ok && hasItemID {
			return llm.RealtimeEvent{
				Type:         llm.RealtimeEventTypeText,
				ItemID:       itemID,
				ContentIndex: openAIRealtimeInt(ev["content_index"]),
				Text:         delta,
			}, true
		}
	case "response.output_audio_transcript.delta", "response.audio_transcript.delta":
		itemID, hasItemID := ev["item_id"].(string)
		if delta, ok := ev["delta"].(string); ok && hasItemID {
			event := llm.RealtimeEvent{
				Type:         llm.RealtimeEventTypeText,
				ItemID:       itemID,
				ContentIndex: openAIRealtimeInt(ev["content_index"]),
				Text:         delta,
			}
			if startTime, ok := openAIRealtimeOptionalFloat(ev["start_time"]); ok {
				event.TimedText = &llm.RealtimeTimedText{
					Text:      delta,
					StartTime: startTime,
				}
			}
			return event, true
		}
	case "response.output_audio.delta", "response.audio.delta":
		itemID, hasItemID := ev["item_id"].(string)
		if delta, ok := ev["delta"].(string); ok && hasItemID {
			data, err := openAIRealtimeDecodeAudioBase64(delta)
			if err != nil {
				return llm.RealtimeEvent{}, false
			}
			return llm.RealtimeEvent{
				Type:         llm.RealtimeEventTypeAudio,
				ItemID:       itemID,
				ContentIndex: openAIRealtimeInt(ev["content_index"]),
				Data:         data,
			}, true
		}
	case "conversation.item.input_audio_transcription.completed":
		itemID, hasItemID := ev["item_id"].(string)
		if !hasItemID {
			return llm.RealtimeEvent{}, false
		}
		contentIndex := openAIRealtimeInt(ev["content_index"])
		transcript, hasTranscript := ev["transcript"].(string)
		if !hasTranscript {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:       itemID,
				ContentIndex: contentIndex,
				Transcript:   transcript,
				IsFinal:      true,
				Confidence:   openAIRealtimeConfidenceFromLogprobs(ev["logprobs"]),
			},
		}, true
	case "conversation.item.input_audio_transcription.delta":
		itemID, hasItemID := ev["item_id"].(string)
		if !hasItemID {
			return llm.RealtimeEvent{}, false
		}
		contentIndex := openAIRealtimeInt(ev["content_index"])
		delta, _ := ev["delta"].(string)
		if delta == "" {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:       itemID,
				ContentIndex: contentIndex,
				Transcript:   delta,
				IsFinal:      false,
			},
		}, true
	case "response.created":
		response, _ := ev["response"].(map[string]any)
		responseID, hasResponseID := response["id"].(string)
		if !hasResponseID {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeGenerationCreated,
			Generation: &llm.GenerationCreatedEvent{
				ResponseID: responseID,
			},
		}, true
	case "conversation.item.added", "conversation.item.created":
		item, ok := ev["item"].(map[string]any)
		if !ok {
			return llm.RealtimeEvent{}, false
		}
		chatItem, err := openAIRealtimeChatItem(item)
		if err != nil {
			return llm.RealtimeEvent{}, false
		}
		previousItemID, previousItemIDSet := ev["previous_item_id"].(string)
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeRemoteItemAdded,
			RemoteItem: &llm.RemoteItemAddedEvent{
				PreviousItemID:    previousItemID,
				PreviousItemIDSet: previousItemIDSet,
				Item:              chatItem,
			},
		}, true
	case "response.done":
		response, _ := ev["response"].(map[string]any)
		metrics, ok := openAIRealtimeMetrics(response)
		if !ok {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type:    llm.RealtimeEventTypeMetricsCollected,
			Metrics: metrics,
		}, true
	case "input_audio_buffer.speech_started":
		return llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted}, true
	case "input_audio_buffer.speech_stopped":
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeSpeechStopped,
			SpeechStopped: &llm.InputSpeechStoppedEvent{
				UserTranscriptionEnabled: true,
			},
		}, true
	}
	return llm.RealtimeEvent{}, false
}

func openAIRealtimeDecodeAudioBase64(value string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err == nil {
		return data, nil
	}
	filtered := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '+' || c == '/' || c == '=' {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	hasData := false
	for _, c := range filtered {
		if c != '=' {
			hasData = true
			break
		}
	}
	if !hasData {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(string(filtered))
}

func (s *realtimeSession) resolveRealtimeChatContextCreateAck(itemID string) {
	s.mu.Lock()
	ack, ok := s.pendingCreateAcks[itemID]
	if ok {
		delete(s.pendingCreateAcks, itemID)
	}
	s.mu.Unlock()
	if ok {
		close(ack)
	}
}

func (s *realtimeSession) resolveRealtimeChatContextDeleteAck(itemID string) {
	s.mu.Lock()
	ack, ok := s.pendingDeleteAcks[itemID]
	if ok {
		delete(s.pendingDeleteAcks, itemID)
	}
	s.mu.Unlock()
	if ok {
		close(ack)
	}
}

func openAIRealtimeMetrics(response map[string]any) (*telemetry.RealtimeModelMetrics, bool) {
	requestID, _ := response["id"].(string)
	usage, _ := response["usage"].(map[string]any)
	inputDetails, _ := usage["input_token_details"].(map[string]any)
	outputDetails, _ := usage["output_token_details"].(map[string]any)
	cachedDetails, _ := inputDetails["cached_tokens_details"].(map[string]any)
	status, _ := response["status"].(string)
	return &telemetry.RealtimeModelMetrics{
		RequestID:       requestID,
		Cancelled:       status == "cancelled",
		InputTokens:     openAIRealtimeInt(usage["input_tokens"]),
		OutputTokens:    openAIRealtimeInt(usage["output_tokens"]),
		TotalTokens:     openAIRealtimeInt(usage["total_tokens"]),
		TokensPerSecond: openAIRealtimeFloat(usage["tokens_per_second"]),
		InputTokenDetails: telemetry.InputTokenDetails{
			AudioTokens:  openAIRealtimeInt(inputDetails["audio_tokens"]),
			TextTokens:   openAIRealtimeInt(inputDetails["text_tokens"]),
			ImageTokens:  openAIRealtimeInt(inputDetails["image_tokens"]),
			CachedTokens: openAIRealtimeInt(inputDetails["cached_tokens"]),
			CachedTokensDetails: &telemetry.CachedTokenDetails{
				TextTokens:  openAIRealtimeInt(cachedDetails["text_tokens"]),
				AudioTokens: openAIRealtimeInt(cachedDetails["audio_tokens"]),
				ImageTokens: openAIRealtimeInt(cachedDetails["image_tokens"]),
			},
		},
		OutputTokenDetails: telemetry.OutputTokenDetails{
			TextTokens:  openAIRealtimeInt(outputDetails["text_tokens"]),
			AudioTokens: openAIRealtimeInt(outputDetails["audio_tokens"]),
			ImageTokens: openAIRealtimeInt(outputDetails["image_tokens"]),
		},
	}, true
}

func openAIRealtimeChatItem(item map[string]any) (llm.ChatItem, error) {
	if _, ok := item["id"].(string); !ok {
		return nil, fmt.Errorf("id is None")
	}
	itemType, _ := item["type"].(string)
	switch itemType {
	case "message":
		return openAIRealtimeChatMessage(item)
	case "function_call":
		return openAIRealtimeFunctionCall(item)
	case "function_call_output":
		return openAIRealtimeFunctionCallOutput(item)
	default:
		return nil, fmt.Errorf("unsupported item type: %s", itemType)
	}
}

func openAIRealtimeFunctionCallOutput(item map[string]any) (*llm.FunctionCallOutput, error) {
	id, _ := item["id"].(string)
	callID, hasCallID := item["call_id"].(string)
	output, hasOutput := item["output"].(string)
	if !hasCallID {
		return nil, fmt.Errorf("call_id is None")
	}
	if !hasOutput {
		return nil, fmt.Errorf("output is None")
	}
	return &llm.FunctionCallOutput{
		ID:      id,
		CallID:  callID,
		Output:  output,
		IsError: false,
	}, nil
}

func openAIRealtimeFunctionCall(item map[string]any) (*llm.FunctionCall, error) {
	id, hasID := item["id"].(string)
	callID, hasCallID := item["call_id"].(string)
	name, hasName := item["name"].(string)
	arguments, hasArguments := item["arguments"].(string)
	if !hasID {
		return nil, fmt.Errorf("id is None")
	}
	if !hasCallID {
		return nil, fmt.Errorf("call_id is None")
	}
	if !hasName {
		return nil, fmt.Errorf("name is None")
	}
	if !hasArguments {
		return nil, fmt.Errorf("arguments is None")
	}
	return &llm.FunctionCall{
		ID:        id,
		CallID:    callID,
		Name:      name,
		Arguments: arguments,
	}, nil
}

func openAIRealtimeChatMessage(item map[string]any) (*llm.ChatMessage, error) {
	id, _ := item["id"].(string)
	roleRaw, hasRole := item["role"].(string)
	if !hasRole {
		return nil, fmt.Errorf("role is None")
	}
	role := llm.ChatRole(roleRaw)
	switch role {
	case llm.ChatRoleSystem, llm.ChatRoleDeveloper, llm.ChatRoleUser, llm.ChatRoleAssistant:
	default:
		return nil, fmt.Errorf("unsupported role: %s", roleRaw)
	}
	contents, ok := item["content"].([]any)
	if !ok {
		return nil, fmt.Errorf("content is None")
	}
	return &llm.ChatMessage{
		ID:      id,
		Role:    role,
		Content: openAIRealtimeChatContent(role, contents),
	}, nil
}

func openAIRealtimeChatContent(role llm.ChatRole, contents []any) []llm.ChatContent {
	out := make([]llm.ChatContent, 0, len(contents))
	for _, content := range contents {
		part, ok := content.(map[string]any)
		if !ok {
			continue
		}
		partType, _ := part["type"].(string)
		switch partType {
		case "input_text", "output_text":
			text, hasText := part["text"].(string)
			if text != "" || (hasText && role == llm.ChatRoleUser && partType == "input_text") {
				out = append(out, llm.ChatContent{Text: text})
			}
		case "input_image":
			if imageURL, hasImageURL := part["image_url"].(string); hasImageURL && role == llm.ChatRoleUser {
				out = append(out, llm.ChatContent{
					Image: &llm.ImageContent{Image: imageURL},
				})
			}
		case "input_audio":
			if transcript, hasTranscript := part["transcript"].(string); hasTranscript && role == llm.ChatRoleUser {
				out = append(out, llm.ChatContent{Text: transcript})
			}
		}
	}
	return out
}

func openAIRealtimeConfidenceFromLogprobs(v any) *float64 {
	logprobs, ok := v.([]any)
	if !ok || len(logprobs) == 0 {
		return nil
	}
	total := 0.0
	for _, item := range logprobs {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil
		}
		logprob := openAIRealtimeFloat(entry["logprob"])
		total += logprob
	}
	confidence := math.Exp(total / float64(len(logprobs)))
	return &confidence
}

func openAIRealtimeFloat(v any) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		f, _ := value.Float64()
		return f
	default:
		return 0
	}
}

func openAIRealtimeOptionalFloat(v any) (float64, bool) {
	switch value := v.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case json.Number:
		f, err := value.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func openAIRealtimeString(v any) string {
	value, _ := v.(string)
	return value
}

func openAIRealtimeInt(v any) int {
	return int(openAIRealtimeFloat(v))
}

func openAIRealtimeResponseClientEventID(response map[string]any) (string, bool) {
	metadata, ok := response["metadata"].(map[string]any)
	if !ok {
		return "", false
	}
	clientEventID, ok := metadata["client_event_id"].(string)
	if !ok || clientEventID == "" {
		return "", false
	}
	return clientEventID, true
}
