package aws

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	bedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	awstypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/google/uuid"
)

const (
	AWSRealtimeModelNovaSonic1       = "amazon.nova-sonic-v1:0"
	AWSRealtimeModelNovaSonic2       = "amazon.nova-2-sonic-v1:0"
	AWSRealtimeModalitiesAudio       = "audio"
	AWSRealtimeModalitiesMixed       = "mixed"
	AWSRealtimeTurnDetectionHigh     = "HIGH"
	AWSRealtimeTurnDetectionMedium   = "MEDIUM"
	AWSRealtimeTurnDetectionLow      = "LOW"
	defaultAWSRealtimeModel          = AWSRealtimeModelNovaSonic2
	defaultAWSRealtimeNovaSonic1     = AWSRealtimeModelNovaSonic1
	defaultAWSRealtimeNovaSonic2     = AWSRealtimeModelNovaSonic2
	defaultAWSRealtimeVoice          = "tiffany"
	defaultAWSRealtimeTurnDetection  = AWSRealtimeTurnDetectionMedium
	defaultAWSRealtimeModalities     = AWSRealtimeModalitiesMixed
	defaultAWSRealtimeMaxMessages    = 40
	defaultAWSRealtimeMaxMessageSize = 1024
	defaultAWSRealtimeMaxRestarts    = 3
	defaultAWSRealtimeMaxSession     = 6 * time.Minute
	defaultAWSRealtimeRecycleQuiet   = time.Second
	defaultAWSRealtimeToolRecycle    = 150 * time.Millisecond
	defaultAWSRealtimeGenerateReply  = 10 * time.Second
	defaultAWSRealtimeTextStartDelay = 50 * time.Millisecond
	defaultAWSRealtimeHistoryDelay   = 10 * time.Millisecond
	awsRealtimeCredentialExpirySlack = 3 * time.Minute
	awsRealtimeMinRecycleDuration    = 10 * time.Second
	awsRealtimeAudioModalities       = AWSRealtimeModalitiesAudio
	awsRealtimeProvider              = "Amazon"
	defaultAWSRealtimeSystemPrompt   = "Your name is Sonic, and you are a friendly and enthusiastic voice assistant. " +
		"You love helping people and having natural conversations. " +
		"Be warm, conversational, and engaging. " +
		"Keep your responses natural and concise for voice interaction. " +
		"Do not repeat yourself. " +
		"If you are not sure what the user means, ask them to confirm or clarify. " +
		"If after asking for clarification you still do not understand, be honest and tell them you do not understand. " +
		"Do not make up information or make assumptions. If you do not know the answer, say so. " +
		"When making tool calls, inform the user that you are using a tool to generate the response. " +
		"Avoid formatted lists or numbering and keep your output as a spoken transcript. " +
		"\n\n" +
		"CRITICAL LANGUAGE MIRRORING RULES:\n" +
		"- Always reply in the language the user speaks. DO NOT mix with English unless the user does.\n" +
		"- If the user talks in English, reply in English.\n" +
		"- Please respond in the language the user is talking to you in. If you have a question or suggestion, ask it in the language the user is talking in.\n" +
		"- Ensure that our communication remains in the same language as the user."
)

var awsRealtimeRecoverableReadErrorMessages = []string{
	"InternalErrorCode=531::RST_STREAM closed stream. HTTP/2 error code: NO_ERROR",
	"System instability detected. Please retry your request.",
	"ThrottlingException",
	"ModelNotReadyException",
	"ModelErrorException",
	"ModelStreamErrorException",
	"ModelTimeoutException",
	"InvalidEventBytes",
}

var awsRealtimeSonic1Voices = []string{
	"matthew",
	"tiffany",
	"amy",
	"lupe",
	"carlos",
	"ambre",
	"florian",
	"greta",
	"lennart",
	"beatrice",
	"lorenzo",
}

var awsRealtimeSonic2Voices = []string{
	"matthew",
	"tiffany",
	"amy",
	"olivia",
	"lupe",
	"carlos",
	"ambre",
	"florian",
	"tina",
	"lennart",
	"beatrice",
	"lorenzo",
	"carolina",
	"leo",
	"arjun",
	"kiara",
}

func AWSRealtimeSonic1Voices() []string {
	return append([]string(nil), awsRealtimeSonic1Voices...)
}

func AWSRealtimeSonic2Voices() []string {
	return append([]string(nil), awsRealtimeSonic2Voices...)
}

type AWSRealtimeModel struct {
	model                string
	region               string
	voice                string
	modalities           string
	turnDetection        string
	maxTokens            int
	topP                 float64
	temperature          float64
	toolChoice           llm.ToolChoice
	maxSession           time.Duration
	recycleQuietPeriod   time.Duration
	toolRecycleDelay     time.Duration
	generateReplyTimeout time.Duration
	interactiveTextDelay time.Duration
	credentialExpiry     func() (time.Time, bool)
	maxTokensSet         bool
	topPSet              bool
	temperatureSet       bool
	client               awsRealtimeClient
}

type AWSRealtimeOption func(*AWSRealtimeModel)

func NewAWSRealtimeModel(model string, opts ...AWSRealtimeOption) *AWSRealtimeModel {
	provider := &AWSRealtimeModel{
		model:                awsRealtimeModelOrDefault(model),
		region:               awsRegionOrDefault(""),
		voice:                defaultAWSRealtimeVoice,
		modalities:           defaultAWSRealtimeModalities,
		turnDetection:        defaultAWSRealtimeTurnDetection,
		maxTokens:            defaultAWSRealtimeMaxTokens,
		topP:                 defaultAWSRealtimeTopP,
		temperature:          defaultAWSRealtimeTemperature,
		maxSession:           awsRealtimeMaxSessionFromEnv(),
		recycleQuietPeriod:   defaultAWSRealtimeRecycleQuiet,
		toolRecycleDelay:     defaultAWSRealtimeToolRecycle,
		generateReplyTimeout: defaultAWSRealtimeGenerateReply,
		interactiveTextDelay: defaultAWSRealtimeTextStartDelay,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func NewAWSRealtimeModelWithNovaSonic1(opts ...AWSRealtimeOption) *AWSRealtimeModel {
	provider := NewAWSRealtimeModel(defaultAWSRealtimeNovaSonic1, opts...)
	if provider.model == "" {
		provider.model = defaultAWSRealtimeNovaSonic1
	}
	provider.modalities = awsRealtimeAudioModalities
	return provider
}

func NewAWSRealtimeModelWithNovaSonic2(opts ...AWSRealtimeOption) *AWSRealtimeModel {
	provider := NewAWSRealtimeModel(defaultAWSRealtimeNovaSonic2, opts...)
	if provider.model == "" {
		provider.model = defaultAWSRealtimeNovaSonic2
	}
	provider.modalities = defaultAWSRealtimeModalities
	return provider
}

func WithAWSRealtimeModel(model string) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		if model != "" {
			provider.model = model
		}
	}
}

func WithAWSRealtimeRegion(region string) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.region = awsRegionOrDefault(region)
	}
}

func WithAWSRealtimeVoice(voice string) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		if voice != "" {
			provider.voice = voice
		}
	}
}

func WithAWSRealtimeTurnDetection(turnDetection string) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		if turnDetection != "" {
			provider.turnDetection = turnDetection
		}
	}
}

func WithAWSRealtimeMaxTokens(maxTokens int) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.maxTokens = maxTokens
		provider.maxTokensSet = true
	}
}

func WithAWSRealtimeTopP(topP float64) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.topP = topP
		provider.topPSet = true
	}
}

func WithAWSRealtimeTemperature(temperature float64) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.temperature = temperature
		provider.temperatureSet = true
	}
}

func WithAWSRealtimeToolChoice(toolChoice llm.ToolChoice) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.toolChoice = toolChoice
	}
}

func WithAWSRealtimeGenerateReplyTimeout(timeout time.Duration) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.generateReplyTimeout = timeout
	}
}

func WithAWSRealtimeClient(client awsRealtimeClient) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.client = client
	}
}

func WithAWSRealtimeMaxSessionDuration(duration time.Duration) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.maxSession = duration
	}
}

func WithAWSRealtimeCredentialExpiry(expiry func() (time.Time, bool)) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.credentialExpiry = expiry
	}
}

func (m *AWSRealtimeModel) sessionRecycleDuration(now time.Time) time.Duration {
	if m == nil {
		return 0
	}
	duration := m.maxSession
	if m.credentialExpiry != nil {
		if expiry, ok := m.credentialExpiry(); ok {
			untilExpiry := expiry.Sub(now) - awsRealtimeCredentialExpirySlack
			if duration <= 0 || untilExpiry < duration {
				duration = untilExpiry
			}
			if duration < 30*time.Second {
				duration = max(duration, awsRealtimeMinRecycleDuration)
			}
		}
	}
	return duration
}

func awsRealtimeModelOrDefault(model string) string {
	if model != "" {
		return model
	}
	return defaultAWSRealtimeModel
}

func awsRealtimeMaxSessionFromEnv() time.Duration {
	raw := os.Getenv("LK_SESSION_MAX_DURATION")
	if raw == "" {
		return defaultAWSRealtimeMaxSession
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return defaultAWSRealtimeMaxSession
	}
	return time.Duration(seconds) * time.Second
}

func (m *AWSRealtimeModel) Label() string         { return "aws.RealtimeModel" }
func (m *AWSRealtimeModel) Model() string         { return m.model }
func (m *AWSRealtimeModel) Provider() string      { return awsRealtimeProvider }
func (m *AWSRealtimeModel) Region() string        { return m.region }
func (m *AWSRealtimeModel) Voice() string         { return m.voice }
func (m *AWSRealtimeModel) Modalities() string    { return m.modalities }
func (m *AWSRealtimeModel) TurnDetection() string { return m.turnDetection }
func (m *AWSRealtimeModel) MaxTokens() (int, bool) {
	if m == nil {
		return 0, false
	}
	return m.maxTokens, m.maxTokensSet
}
func (m *AWSRealtimeModel) TopP() (float64, bool) {
	if m == nil {
		return 0, false
	}
	return m.topP, m.topPSet
}
func (m *AWSRealtimeModel) Temperature() (float64, bool) {
	if m == nil {
		return 0, false
	}
	return m.temperature, m.temperatureSet
}
func (m *AWSRealtimeModel) ToolChoice() llm.ToolChoice {
	if m == nil {
		return nil
	}
	return m.toolChoice
}
func (m *AWSRealtimeModel) GenerateReplyTimeout() time.Duration {
	if m == nil {
		return 0
	}
	return m.generateReplyTimeout
}

func (m *AWSRealtimeModel) Capabilities() llm.RealtimeCapabilities {
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
	}
}

func (m *AWSRealtimeModel) Session() (llm.RealtimeSession, error) {
	client := m.client
	if client == nil {
		resolved, err := newAWSRealtimeSDKClient(context.Background(), m.region)
		if err != nil {
			return nil, err
		}
		client = resolved
		if m.credentialExpiry == nil {
			m.credentialExpiry = resolved.credentialExpiry
		}
	}
	session := newAWSRealtimeSession(m, client)
	if err := session.start(context.Background()); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

func (m *AWSRealtimeModel) Close() error {
	return nil
}

type awsRealtimeClient interface {
	InvokeModelWithBidirectionalStream(context.Context, *bedrockruntime.InvokeModelWithBidirectionalStreamInput) (awsRealtimeStream, error)
}

type awsRealtimeStream interface {
	Send(context.Context, awstypes.InvokeModelWithBidirectionalStreamInput) error
	Events() <-chan awstypes.InvokeModelWithBidirectionalStreamOutput
	Close() error
	Err() error
}

type awsRealtimeSDKClient struct {
	client           *bedrockruntime.Client
	credentialExpiry func() (time.Time, bool)
}

func newAWSRealtimeSDKClient(ctx context.Context, region string) (*awsRealtimeSDKClient, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(awsRegionOrDefault(region)))
	if err != nil {
		return nil, err
	}
	return &awsRealtimeSDKClient{
		client:           bedrockruntime.NewFromConfig(cfg),
		credentialExpiry: awsRealtimeCredentialExpiry(ctx, cfg.Credentials),
	}, nil
}

func awsRealtimeCredentialExpiry(ctx context.Context, provider aws.CredentialsProvider) func() (time.Time, bool) {
	return func() (time.Time, bool) {
		if provider == nil {
			return time.Time{}, false
		}
		credentials, err := provider.Retrieve(ctx)
		if err != nil || !credentials.CanExpire || credentials.Expires.IsZero() {
			return time.Time{}, false
		}
		return credentials.Expires, true
	}
}

func (c *awsRealtimeSDKClient) InvokeModelWithBidirectionalStream(ctx context.Context, input *bedrockruntime.InvokeModelWithBidirectionalStreamInput) (awsRealtimeStream, error) {
	output, err := c.client.InvokeModelWithBidirectionalStream(ctx, input)
	if err != nil {
		return nil, err
	}
	return output.GetStream(), nil
}

type awsRealtimeSession struct {
	model                    *AWSRealtimeModel
	client                   awsRealtimeClient
	builder                  *awsRealtimeEventBuilder
	stream                   awsRealtimeStream
	eventCh                  <-chan llm.RealtimeEvent
	eventStream              *awsRealtimeQueuedStream[llm.RealtimeEvent]
	turns                    *awsRealtimeTurnTracker
	generation               *awsRealtimeGeneration
	chatCtx                  *llm.ChatContext
	instructions             string
	tools                    []llm.Tool
	pending                  map[string]struct{}
	sent                     map[string]struct{}
	audioMessages            map[string]struct{}
	noGenerationContentRoles map[string]string
	recycleToolsAfterPending bool
	toolRecycleVersion       int
	recycleTimer             *time.Timer
	recycleVersion           int
	lastAudioOutput          time.Time
	awaitingAudioEndTurn     bool
	currentUserContentID     string
	audioBStream             *coreaudio.AudioByteStream
	audioNorm                awsRealtimeInputAudioNormalizer
	audioMu                  sync.Mutex
	audioCond                *sync.Cond
	audioQueue               []awsRealtimeAudioInputEvent
	audioClosed              bool
	audioCtx                 context.Context
	audioCancel              context.CancelFunc
	audioSenderDone          chan struct{}
	pendingGenerationStart   *awsRealtimePendingGenerationStart
	mu                       sync.Mutex
	closed                   bool
}

type awsRealtimeGeneration struct {
	responseID     string
	createdAt      time.Time
	firstTokenAt   time.Time
	messageCh      chan llm.MessageGeneration
	functionCh     <-chan *llm.FunctionCall
	functionStream *awsRealtimeQueuedStream[*llm.FunctionCall]
	textCh         <-chan string
	textStream     *awsRealtimeQueuedStream[string]
	audioCh        <-chan *model.AudioFrame
	audioStream    *awsRealtimeQueuedStream[*model.AudioFrame]
	modalitiesCh   chan []string
	contentTypes   map[string]string
	restarts       int
	closeOnce      sync.Once
}

type awsRealtimeQueuedStream[T any] struct {
	out       chan T
	wake      chan struct{}
	mu        sync.Mutex
	queue     []T
	closed    bool
	outClosed bool
	sending   bool
}

func newAWSRealtimeQueuedStream[T any]() *awsRealtimeQueuedStream[T] {
	stream := &awsRealtimeQueuedStream[T]{
		out:  make(chan T),
		wake: make(chan struct{}, 1),
	}
	go stream.run()
	return stream
}

func (s *awsRealtimeQueuedStream[T]) Chan() chan T {
	if s == nil {
		return nil
	}
	return s.out
}

func (s *awsRealtimeQueuedStream[T]) Send(value T) bool {
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

func (s *awsRealtimeQueuedStream[T]) TryPopQueued() (T, bool) {
	var zero T
	if s == nil {
		return zero, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return zero, false
	}
	value := s.queue[0]
	s.queue[0] = zero
	s.queue = s.queue[1:]
	if len(s.queue) == 0 && s.closed && !s.sending {
		s.closeOutLocked()
		s.notifyLocked()
	}
	return value, true
}

func (s *awsRealtimeQueuedStream[T]) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if len(s.queue) == 0 && !s.sending {
		s.closeOutLocked()
		s.notifyLocked()
		return
	}
	s.notifyLocked()
}

func (s *awsRealtimeQueuedStream[T]) closeOutLocked() {
	if s.outClosed {
		return
	}
	s.outClosed = true
	close(s.out)
}

func (s *awsRealtimeQueuedStream[T]) notifyLocked() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *awsRealtimeQueuedStream[T]) run() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.closed {
			s.mu.Unlock()
			<-s.wake
			s.mu.Lock()
		}
		if len(s.queue) == 0 && s.closed {
			s.closeOutLocked()
			s.mu.Unlock()
			return
		}
		value := s.queue[0]
		var zero T
		s.queue[0] = zero
		s.queue = s.queue[1:]
		s.sending = true
		s.mu.Unlock()
		s.out <- value
		s.mu.Lock()
		s.sending = false
		if len(s.queue) == 0 && s.closed {
			s.closeOutLocked()
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

type awsRealtimePendingGenerationStart struct {
	done chan struct{}
	err  error
	once sync.Once
	mu   sync.Mutex
}

func newAWSRealtimePendingGenerationStart() *awsRealtimePendingGenerationStart {
	return &awsRealtimePendingGenerationStart{done: make(chan struct{})}
}

func (p *awsRealtimePendingGenerationStart) resolve(err error) {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		close(p.done)
	})
}

func (p *awsRealtimePendingGenerationStart) wait() error {
	if p == nil {
		return nil
	}
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

type awsRealtimeStartOptions struct {
	restart bool
}

func newAWSRealtimeSession(model *AWSRealtimeModel, client awsRealtimeClient) *awsRealtimeSession {
	audioCtx, audioCancel := context.WithCancel(context.Background())
	session := &awsRealtimeSession{
		model:                    model,
		client:                   client,
		builder:                  newAWSRealtimeEventBuilder(uuid.NewString(), uuid.NewString()),
		eventStream:              newAWSRealtimeQueuedStream[llm.RealtimeEvent](),
		pending:                  make(map[string]struct{}),
		sent:                     make(map[string]struct{}),
		audioMessages:            make(map[string]struct{}),
		noGenerationContentRoles: make(map[string]string),
		audioBStream: coreaudio.NewAudioByteStream(
			defaultAWSRealtimeInputSampleRate,
			defaultAWSRealtimeChannels,
			defaultAWSRealtimeInputChunkSize,
		),
		audioCtx:        audioCtx,
		audioCancel:     audioCancel,
		audioSenderDone: make(chan struct{}),
	}
	session.eventCh = session.eventStream.Chan()
	session.audioCond = sync.NewCond(&session.audioMu)
	session.turns = newAWSRealtimeTurnTracker(session.emit, session.emitGenerationCreated)
	go session.runAudioInputSender()
	return session
}

func (s *awsRealtimeSession) start(ctx context.Context) error {
	return s.startWithOptions(ctx, awsRealtimeStartOptions{})
}

func (s *awsRealtimeSession) startWithOptions(ctx context.Context, options awsRealtimeStartOptions) error {
	stream, err := s.client.InvokeModelWithBidirectionalStream(ctx, &bedrockruntime.InvokeModelWithBidirectionalStreamInput{
		ModelId: aws.String(s.model.model),
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(fmt.Sprintf("AWS Nova Sonic realtime stream start failed: %v", err))
	}
	s.stream = stream
	s.mu.Lock()
	systemPrompt := s.instructions
	chatCtx := s.chatCtx
	tools := append([]llm.Tool(nil), s.tools...)
	s.mu.Unlock()
	if systemPrompt == "" {
		systemPrompt = defaultAWSRealtimeSystemPrompt
	}
	if chatCtx != nil && len(chatCtx.Items) > defaultAWSRealtimeMaxMessages {
		chatCtx = chatCtx.Copy()
		chatCtx.Truncate(defaultAWSRealtimeMaxMessages)
		s.mu.Lock()
		s.chatCtx = chatCtx
		s.mu.Unlock()
	}
	interactiveUserText := ""
	if options.restart {
		chatCtx, interactiveUserText = awsRealtimeRestartChatContext(chatCtx)
	}
	initEvents, historyEvents, err := s.builder.createPromptStartBlock(awsRealtimePromptStartOptions{
		voiceID:                s.model.voice,
		outputSampleRate:       defaultAWSRealtimeOutputSampleRate,
		systemContent:          systemPrompt,
		chatCtx:                chatCtx,
		tools:                  tools,
		maxTokens:              s.model.maxTokens,
		topP:                   s.model.topP,
		temperature:            s.model.temperature,
		toolChoice:             s.model.toolChoice,
		maxTokensSet:           s.model.maxTokensSet,
		topPSet:                s.model.topPSet,
		temperatureSet:         s.model.temperatureSet,
		endpointingSensitivity: s.model.turnDetection,
	})
	if err != nil {
		return err
	}
	for _, event := range initEvents {
		if err := s.sendRawEvent(ctx, event); err != nil {
			s.closeStartupStream()
			return awsRealtimeStartupSendError(err)
		}
	}
	for _, event := range historyEvents {
		if err := s.sendRawEvent(ctx, event); err != nil {
			s.closeStartupStream()
			return awsRealtimeStartupSendError(err)
		}
		if err := waitAWSRealtimeHistoryDelay(ctx); err != nil {
			s.closeStartupStream()
			return awsRealtimeStartupSendError(err)
		}
	}
	s.markChatContextUserMessagesSent(chatCtx)
	responseStarted := make(chan struct{})
	go s.readResponses(responseStarted)
	<-responseStarted
	audioStart, err := s.builder.createAudioContentStartEvent(defaultAWSRealtimeInputSampleRate)
	if err != nil {
		return err
	}
	audioStartSent := s.enqueueAudioInputEvent(audioStart)
	if err := waitAWSRealtimeAudioInputEventStarted(ctx, audioStartSent); err != nil {
		s.closeStartupStream()
		return awsRealtimeStartupSendError(err)
	}
	if interactiveUserText != "" {
		if err := s.waitInteractiveTextDelay(ctx); err != nil {
			s.closeStartupStream()
			return awsRealtimeStartupSendError(err)
		}
		if err := s.sendInteractiveUserText(ctx, &llm.ChatMessage{
			ID:      uuid.NewString(),
			Role:    llm.ChatRoleUser,
			Content: []llm.ChatContent{{Text: interactiveUserText}},
		}); err != nil {
			s.closeStartupStream()
			return awsRealtimeStartupSendError(err)
		}
	}
	s.startSessionRecycleTimer()
	return nil
}

func (s *awsRealtimeSession) waitInteractiveTextDelay(ctx context.Context) error {
	delay := time.Duration(0)
	if s.model != nil {
		delay = s.model.interactiveTextDelay
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitAWSRealtimeHistoryDelay(ctx context.Context) error {
	timer := time.NewTimer(defaultAWSRealtimeHistoryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *awsRealtimeSession) startSessionRecycleTimer() {
	if s.model == nil || s.model.maxSession <= 0 {
		return
	}
	s.mu.Lock()
	if s.recycleTimer != nil {
		s.recycleTimer.Stop()
	}
	s.recycleVersion++
	version := s.recycleVersion
	duration := s.model.sessionRecycleDuration(time.Now())
	s.recycleTimer = time.AfterFunc(duration, func() {
		s.recycleAfterSessionDuration(context.Background(), version)
	})
	s.mu.Unlock()
}

func (s *awsRealtimeSession) recycleAfterSessionDuration(ctx context.Context, version int) {
	if !s.waitForSessionRecycleTurnBoundary(ctx, version) {
		return
	}
	if !s.waitForSessionRecycleAudioQuiet(ctx, version) {
		return
	}
	s.mu.Lock()
	if s.closed || version != s.recycleVersion || s.stream == nil {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if err := s.recycleForUpdatedTools(ctx); err != nil {
		s.emit(llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: err,
		})
		return
	}
	s.emit(llm.RealtimeEvent{
		Type:      llm.RealtimeEventTypeSessionReconnected,
		Reconnect: &llm.RealtimeSessionReconnectedEvent{},
	})
}

func (s *awsRealtimeSession) waitForSessionRecycleTurnBoundary(ctx context.Context, version int) bool {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		done := s.closed || version != s.recycleVersion
		activeGeneration := s.generation != nil || s.awaitingAudioEndTurn
		s.mu.Unlock()
		if done {
			return false
		}
		if !activeGeneration {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (s *awsRealtimeSession) waitForSessionRecycleAudioQuiet(ctx context.Context, version int) bool {
	for {
		s.mu.Lock()
		done := s.closed || version != s.recycleVersion
		lastAudioOutput := s.lastAudioOutput
		quietPeriod := time.Duration(0)
		if s.model != nil {
			quietPeriod = s.model.recycleQuietPeriod
		}
		s.mu.Unlock()
		if done {
			return false
		}
		if lastAudioOutput.IsZero() || quietPeriod <= 0 {
			return true
		}
		wait := quietPeriod - time.Since(lastAudioOutput)
		if wait <= 0 {
			return true
		}
		if wait > 10*time.Millisecond {
			wait = 10 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func awsRealtimeRestartChatContext(chatCtx *llm.ChatContext) (*llm.ChatContext, string) {
	if chatCtx == nil || len(chatCtx.Items) == 0 {
		return chatCtx, ""
	}
	restartCtx := chatCtx
	if first, ok := restartCtx.Items[0].(*llm.ChatMessage); ok && first.Role == llm.ChatRoleAssistant {
		restartCtx = restartCtx.Copy()
		dummy := &llm.ChatMessage{
			ID:      uuid.NewString(),
			Role:    llm.ChatRoleUser,
			Content: []llm.ChatContent{{Text: "[Resuming conversation]"}},
		}
		restartCtx.Items = append([]llm.ChatItem{dummy}, restartCtx.Items...)
	}
	last, ok := restartCtx.Items[len(restartCtx.Items)-1].(*llm.ChatMessage)
	if !ok || last.Role != llm.ChatRoleUser {
		return restartCtx, ""
	}
	text := strings.TrimSpace(last.TextContent())
	if text == "" {
		return restartCtx, ""
	}
	if restartCtx == chatCtx {
		restartCtx = restartCtx.Copy()
	}
	restartCtx.Items = restartCtx.Items[:len(restartCtx.Items)-1]
	return restartCtx, text
}

func awsRealtimeStartupSendError(err error) error {
	var timeoutErr *llm.APITimeoutError
	if errors.As(err, &timeoutErr) {
		return timeoutErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(err.Error())
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("AWS Nova Sonic realtime startup send failed: %v", err))
}

func (s *awsRealtimeSession) closeStartupStream() {
	if s.stream != nil {
		_ = s.stream.Close()
		s.stream = nil
	}
}

func (s *awsRealtimeSession) sendRawEvent(ctx context.Context, event string) error {
	s.mu.Lock()
	stream := s.stream
	s.mu.Unlock()
	if stream == nil {
		return errors.New("AWS Nova Sonic realtime stream is not initialized")
	}
	return sendAWSRealtimeRawEvent(ctx, stream, event)
}

func sendAWSRealtimeRawEvent(ctx context.Context, stream awsRealtimeStream, event string) error {
	if stream == nil {
		return errors.New("AWS Nova Sonic realtime stream is not initialized")
	}
	if err := stream.Send(ctx, &awstypes.InvokeModelWithBidirectionalStreamInputMemberChunk{
		Value: awstypes.BidirectionalInputPayloadPart{Bytes: []byte(event)},
	}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(fmt.Sprintf("AWS Nova Sonic realtime send failed: %v", err))
	}
	return nil
}

func (s *awsRealtimeSession) readResponses(started chan<- struct{}) {
	stream := s.stream
	if stream == nil {
		if started != nil {
			close(started)
		}
		return
	}
	events := stream.Events()
	if started != nil {
		close(started)
	}
	for event := range events {
		chunk, ok := event.(*awstypes.InvokeModelWithBidirectionalStreamOutputMemberChunk)
		if !ok || len(chunk.Value.Bytes) == 0 {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(chunk.Value.Bytes, &payload); err != nil {
			continue
		}
		if !s.handleResponseEvent(payload) {
			return
		}
	}
	if err := stream.Err(); err != nil {
		if isAWSRealtimeGracefulClosedReadError(err) {
			s.closeGeneration()
			return
		}
		if isAWSRealtimeToolResponseParsingError(err) {
			s.closeGeneration()
			s.clearPendingTools()
			s.emit(llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeError,
				Error: llm.NewRealtimeModelError(
					s.model.Label(),
					llm.NewAPIStatusErrorWithRetryable(err.Error(), 400, "", err, false),
					true,
				),
			})
			return
		}
		if s.restartAfterRecoverableReadError(stream, err) {
			return
		}
		if isAWSRealtimeValidationError(err) {
			s.closeGeneration()
			s.emit(llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeError,
				Error: llm.NewRealtimeModelError(
					s.model.Label(),
					llm.NewAPIStatusErrorWithRetryable(err.Error(), 400, "", err, false),
					false,
				),
			})
			return
		}
		s.closeGeneration()
		if errors.Is(err, context.DeadlineExceeded) {
			s.emit(llm.RealtimeEvent{
				Type:  llm.RealtimeEventTypeError,
				Error: llm.NewAPITimeoutError(err.Error()),
			})
			return
		}
		s.emit(llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeError,
			Error: llm.NewRealtimeModelError(
				s.model.Label(),
				llm.NewAPIStatusErrorWithRetryable(err.Error(), 500, "", err, false),
				false,
			),
		})
		return
	}
	s.closeGeneration()
}

func (s *awsRealtimeSession) restartAfterRecoverableReadError(stream awsRealtimeStream, err error) bool {
	if !isAWSRealtimeRecoverableReadError(err) {
		return false
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	if s.generation != nil {
		if s.generation.restarts >= defaultAWSRealtimeMaxRestarts {
			s.mu.Unlock()
			_ = stream.Close()
			s.closeGeneration()
			s.emit(llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeError,
				Error: llm.NewAPIStatusErrorWithRetryable(
					fmt.Sprintf("Max restart attempts exceeded: %v", err),
					500,
					"",
					err,
					false,
				),
			})
			return true
		}
		s.generation.restarts++
	}
	if s.stream == stream {
		s.stream = nil
	}
	s.builder = newAWSRealtimeEventBuilder(uuid.NewString(), uuid.NewString())
	s.mu.Unlock()

	_ = stream.Close()
	if startErr := s.startWithOptions(context.Background(), awsRealtimeStartOptions{restart: true}); startErr != nil {
		s.closeGeneration()
		s.emit(llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: startErr,
		})
		return true
	}
	s.emit(llm.RealtimeEvent{
		Type:      llm.RealtimeEventTypeSessionReconnected,
		Reconnect: &llm.RealtimeSessionReconnectedEvent{},
	})
	return true
}

func isAWSRealtimeRecoverableReadError(err error) bool {
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := err.Error()
	for _, recoverable := range awsRealtimeRecoverableReadErrorMessages {
		if strings.Contains(message, recoverable) {
			return true
		}
	}
	return false
}

func isAWSRealtimeGracefulClosedReadError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return message == "I/O operation on closed file." || strings.Contains(message, "stream already closed")
}

func isAWSRealtimeToolResponseParsingError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Tool Response parsing error")
}

func isAWSRealtimeValidationError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "ValidationException")
}

func (s *awsRealtimeSession) clearPendingTools() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = make(map[string]struct{})
}

func (s *awsRealtimeSession) handleResponseEvent(payload map[string]any) bool {
	if completionStart := awsRealtimeNestedMap(payload, "event", "completionStart"); completionStart != nil {
		s.emitGenerationCreatedIfNew()
	}
	if contentStart := awsRealtimeNestedMap(payload, "event", "contentStart"); contentStart != nil {
		if _, ok := awsRealtimeRequiredMapString(contentStart, "contentId"); !ok {
			s.handleMalformedContentStart(contentStart)
			return true
		}
	}
	s.trackGenerationContentStart(payload)
	if audioOutput := awsRealtimeNestedMap(payload, "event", "audioOutput"); audioOutput != nil {
		contentID, ok := awsRealtimeRequiredMapString(audioOutput, "contentId")
		if !ok {
			return true
		}
		if !s.isProviderAssistantAudio(contentID) {
			return true
		}
		audioContent, ok := awsRealtimeRequiredMapString(audioOutput, "content")
		if !ok {
			return true
		}
		data, err := decodeAWSRealtimeAudioContent(audioContent)
		if err != nil {
			s.closeGeneration()
			s.emit(llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeError,
				Error: llm.NewRealtimeModelError(
					s.model.Label(),
					llm.NewAPIStatusErrorWithRetryable(err.Error(), 500, "", err, false),
					false,
				),
			})
			return false
		}
		if s.sendGenerationAudio(contentID, data) {
			s.markLastAudioOutput()
			s.emit(llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeAudio,
				Data: data,
			})
		}
	}
	if textOutput := awsRealtimeNestedMap(payload, "event", "textOutput"); textOutput != nil {
		contentID, ok := awsRealtimeRequiredMapString(textOutput, "contentId")
		if !ok {
			return true
		}
		textContent, ok := awsRealtimeRequiredMapString(textOutput, "content")
		if !ok {
			return true
		}
		role := awsRealtimeMapString(textOutput, "role")
		if textContent == awsRealtimeBargeInContent {
			if s.hasGeneration() {
				s.markLastAssistantMessageInterrupted()
				if !s.hasPendingTools() {
					s.closeGeneration()
				}
			}
		} else {
			if s.shouldStoreProviderUserText(contentID, role) {
				s.updateProviderTextHistory(llm.ChatRoleUser, textContent, contentID)
			}
			if s.isProviderAssistantText(contentID) {
				s.updateProviderTextHistory(llm.ChatRoleAssistant, textContent, "")
				s.sendGenerationText(contentID, textContent)
			}
		}
	}
	if toolUse := awsRealtimeNestedMap(payload, "event", "toolUse"); toolUse != nil {
		toolUseID, ok := awsRealtimeRequiredMapString(toolUse, "toolUseId")
		if !ok {
			return true
		}
		toolName, ok := awsRealtimeRequiredMapString(toolUse, "toolName")
		if !ok {
			return true
		}
		content, ok := awsRealtimeRequiredMapString(toolUse, "content")
		if !ok {
			return true
		}
		if !s.sendGenerationFunction(&llm.FunctionCall{
			CallID:    toolUseID,
			Name:      toolName,
			Arguments: normalizeAWSRealtimeToolArguments(content),
		}) {
			return true
		}
	}
	if usage := awsRealtimeNestedMap(payload, "event", "usageEvent"); usage != nil {
		s.emitUsageMetrics(usage)
	}
	if awsRealtimeNestedMap(payload, "event", "completionEnd") != nil {
		s.closeGeneration()
	}
	if contentEnd := awsRealtimeNestedMap(payload, "event", "contentEnd"); contentEnd != nil &&
		awsRealtimeMapString(contentEnd, "stopReason") == "END_TURN" &&
		awsRealtimeMapString(contentEnd, "type") == "AUDIO" {
		s.markAudioEndTurnReceived()
		s.closeGeneration()
	}
	if s.turns != nil {
		s.turns.feed(payload)
	}
	return true
}

func decodeAWSRealtimeAudioContent(content string) ([]byte, error) {
	if content == "" {
		return []byte{}, nil
	}
	filtered := make([]byte, 0, len(content))
	dataChars := 0
	for i := 0; i < len(content); i++ {
		c := content[i]
		switch {
		case c >= 'A' && c <= 'Z',
			c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			c == '+', c == '/':
			filtered = append(filtered, c)
			dataChars++
		case c == '=':
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 || dataChars == 0 {
		return []byte{}, nil
	}
	return base64.StdEncoding.DecodeString(string(filtered))
}

func (s *awsRealtimeSession) handleMalformedContentStart(contentStart map[string]any) {
	contentType := awsRealtimeMapString(contentStart, "type")
	role := awsRealtimeMapString(contentStart, "role")
	additionalFields := awsRealtimeMapString(contentStart, "additionalModelFields")
	if contentType == "TOOL" ||
		(role == "ASSISTANT" && contentType == "TEXT" && strings.Contains(additionalFields, "SPECULATIVE")) {
		s.emitGenerationCreatedIfNew()
	}
}

func (s *awsRealtimeSession) hasPendingTools() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0
}

func (s *awsRealtimeSession) hasGeneration() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation != nil
}

func (s *awsRealtimeSession) emitGenerationCreated() {
	s.emitGenerationCreatedWithResponseID("")
}

func (s *awsRealtimeSession) emitGenerationCreatedWithResponseID(responseID string) {
	generation, _ := s.ensureGenerationWithCreated(responseID)
	s.emit(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:  generation.messageCh,
			FunctionCh: generation.functionCh,
			ResponseID: generation.responseID,
		},
	})
	s.resolvePendingGenerationStart()
}

func (s *awsRealtimeSession) emitGenerationCreatedIfNew() {
	generation, created := s.ensureGenerationWithCreated("")
	if !created {
		return
	}
	s.emit(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:  generation.messageCh,
			FunctionCh: generation.functionCh,
			ResponseID: generation.responseID,
		},
	})
	s.resolvePendingGenerationStart()
}

func (s *awsRealtimeSession) ensureGenerationWithCreated(responseID string) (*awsRealtimeGeneration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != nil {
		return s.generation, false
	}
	if responseID == "" {
		responseID = uuid.NewString()
	}
	functionStream := newAWSRealtimeQueuedStream[*llm.FunctionCall]()
	textStream := newAWSRealtimeQueuedStream[string]()
	audioStream := newAWSRealtimeQueuedStream[*model.AudioFrame]()
	generation := &awsRealtimeGeneration{
		responseID:     responseID,
		createdAt:      time.Now(),
		messageCh:      make(chan llm.MessageGeneration, 1),
		functionCh:     functionStream.Chan(),
		functionStream: functionStream,
		textCh:         textStream.Chan(),
		textStream:     textStream,
		audioCh:        audioStream.Chan(),
		audioStream:    audioStream,
		modalitiesCh:   make(chan []string, 1),
		contentTypes:   make(map[string]string),
	}
	generation.modalitiesCh <- []string{"audio", "text"}
	generation.messageCh <- llm.MessageGeneration{
		MessageID:    generation.responseID,
		TextCh:       generation.textCh,
		AudioCh:      generation.audioCh,
		ModalitiesCh: generation.modalitiesCh,
	}
	s.generation = generation
	return generation, true
}

func (s *awsRealtimeSession) trackGenerationContentStart(payload map[string]any) {
	contentID := awsRealtimeNestedString(payload, "event", "contentStart", "contentId")
	if contentID == "" {
		return
	}
	contentType := awsRealtimeNestedString(payload, "event", "contentStart", "type")
	additionalFields := awsRealtimeNestedString(payload, "event", "contentStart", "additionalModelFields")
	role := awsRealtimeNestedString(payload, "event", "contentStart", "role")
	var streamType string
	switch {
	case contentType == "AUDIO":
		streamType = "ASSISTANT_AUDIO"
		s.markAwaitingAudioEndTurn()
	case contentType == "TOOL":
		streamType = "TOOL"
	case role == "USER" && contentType == "TEXT":
		streamType = "USER_ASR"
	case role == "ASSISTANT" &&
		contentType == "TEXT" && strings.Contains(additionalFields, "SPECULATIVE"):
		streamType = "ASSISTANT_TEXT"
	case role == "ASSISTANT" &&
		contentType == "TEXT" && strings.Contains(additionalFields, "FINAL"):
		streamType = "ASSISTANT_FINAL"
	default:
		return
	}
	var generation *awsRealtimeGeneration
	if streamType == "ASSISTANT_TEXT" || streamType == "TOOL" {
		created := false
		generation, created = s.ensureGenerationWithCreated("")
		if created {
			s.emit(llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeGenerationCreated,
				Generation: &llm.GenerationCreatedEvent{
					MessageCh:  generation.messageCh,
					FunctionCh: generation.functionCh,
					ResponseID: generation.responseID,
				},
			})
			s.resolvePendingGenerationStart()
		}
	} else {
		s.mu.Lock()
		generation = s.generation
		s.mu.Unlock()
	}
	if generation == nil {
		s.mu.Lock()
		if role != "" {
			s.noGenerationContentRoles[contentID] = role
		}
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	generation.contentTypes[contentID] = streamType
	s.mu.Unlock()
}

func (s *awsRealtimeSession) markAwaitingAudioEndTurn() {
	s.mu.Lock()
	s.awaitingAudioEndTurn = true
	s.mu.Unlock()
}

func (s *awsRealtimeSession) markAudioEndTurnReceived() {
	s.mu.Lock()
	s.awaitingAudioEndTurn = false
	s.mu.Unlock()
}

func (s *awsRealtimeSession) shouldStoreProviderUserText(contentID string, role string) bool {
	if role != "" && role != "USER" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != nil {
		return s.generation.contentTypes[contentID] == "USER_ASR"
	}
	trackedRole, tracked := s.noGenerationContentRoles[contentID]
	return !tracked || trackedRole == "USER"
}

func (s *awsRealtimeSession) isProviderAssistantText(contentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != nil && s.generation.contentTypes[contentID] == "ASSISTANT_TEXT" {
		return true
	}
	return s.noGenerationContentRoles[contentID] == "ASSISTANT"
}

func (s *awsRealtimeSession) isProviderAssistantAudio(contentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation != nil && s.generation.contentTypes[contentID] == "ASSISTANT_AUDIO"
}

func (s *awsRealtimeSession) updateProviderTextHistory(role llm.ChatRole, text string, contentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chatCtx == nil {
		s.chatCtx = llm.NewChatContext()
	}
	forceNew := false
	if role == llm.ChatRoleUser && contentID != "" && s.currentUserContentID != contentID {
		forceNew = true
		s.currentUserContentID = contentID
	}
	if len(s.chatCtx.Items) > 0 && !forceNew {
		if msg, ok := s.chatCtx.Items[len(s.chatCtx.Items)-1].(*llm.ChatMessage); ok && msg.Role == role {
			current := msg.TextContent()
			if role == llm.ChatRoleUser && contentID != "" && s.currentUserContentID == contentID && strings.HasPrefix(text, current) {
				msg.Content = []llm.ChatContent{{Text: text}}
				return
			}
			if len(current)+len(text) < defaultAWSRealtimeMaxMessageSize {
				msg.Content = []llm.ChatContent{{Text: current + "\n" + text}}
				return
			}
		}
	}
	msg := s.chatCtx.AddMessage(llm.ChatMessageArgs{Role: role, Text: text})
	if role == llm.ChatRoleUser {
		s.audioMessages[msg.ID] = struct{}{}
	}
	s.chatCtx.Truncate(defaultAWSRealtimeMaxMessages)
}

func (s *awsRealtimeSession) markLastAssistantMessageInterrupted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chatCtx == nil {
		return
	}
	for i := len(s.chatCtx.Items) - 1; i >= 0; i-- {
		msg, ok := s.chatCtx.Items[i].(*llm.ChatMessage)
		if !ok {
			continue
		}
		if msg.Role == llm.ChatRoleAssistant {
			msg.Interrupted = true
		}
		return
	}
}

func (s *awsRealtimeSession) sendGenerationAudio(contentID string, data []byte) bool {
	s.mu.Lock()
	generation := s.generation
	streamType := ""
	if generation != nil {
		streamType = generation.contentTypes[contentID]
	}
	s.mu.Unlock()
	if generation == nil || streamType != "ASSISTANT_AUDIO" {
		return false
	}
	frame := &model.AudioFrame{
		Data:              data,
		SampleRate:        defaultAWSRealtimeOutputSampleRate,
		NumChannels:       defaultAWSRealtimeChannels,
		SamplesPerChannel: uint32(len(data) / 2),
	}
	return generation.audioStream.Send(frame)
}

func (s *awsRealtimeSession) markLastAudioOutput() {
	s.mu.Lock()
	s.lastAudioOutput = time.Now()
	s.mu.Unlock()
}

func (s *awsRealtimeSession) sendGenerationText(contentID string, text string) {
	s.mu.Lock()
	generation := s.generation
	streamType := ""
	if generation != nil {
		streamType = generation.contentTypes[contentID]
		if streamType == "ASSISTANT_TEXT" && generation.firstTokenAt.IsZero() {
			generation.firstTokenAt = time.Now()
		}
	}
	s.mu.Unlock()
	if generation == nil || streamType != "ASSISTANT_TEXT" {
		return
	}
	generation.textStream.Send(text)
}

func (s *awsRealtimeSession) sendGenerationFunction(call *llm.FunctionCall) bool {
	s.mu.Lock()
	generation := s.generation
	if generation != nil && call != nil && call.CallID != "" {
		s.pending[call.CallID] = struct{}{}
	}
	s.mu.Unlock()
	if generation == nil {
		return false
	}
	generation.functionStream.Send(call)
	s.closeGeneration()
	return true
}

func (s *awsRealtimeSession) closeGeneration() {
	s.mu.Lock()
	generation := s.generation
	if generation != nil {
		s.generation = nil
	}
	s.mu.Unlock()
	if generation == nil {
		return
	}
	generation.closeOnce.Do(func() {
		generation.textStream.Close()
		generation.audioStream.Close()
		close(generation.modalitiesCh)
		close(generation.messageCh)
		generation.functionStream.Close()
	})
}

func normalizeAWSRealtimeToolArguments(content string) string {
	if content == "" {
		return content
	}
	var outer any
	if err := json.Unmarshal([]byte(content), &outer); err != nil {
		return content
	}
	innerString, ok := outer.(string)
	if !ok {
		return content
	}
	var inner map[string]any
	if err := json.Unmarshal([]byte(innerString), &inner); err != nil {
		return content
	}
	return innerString
}

func (s *awsRealtimeSession) emitUsageMetrics(usage map[string]any) {
	input := awsRealtimeNestedMap(usage, "details", "delta", "input")
	output := awsRealtimeNestedMap(usage, "details", "delta", "output")
	inputSpeech := awsRealtimeNumberAsInt(input["speechTokens"])
	inputText := awsRealtimeNumberAsInt(input["textTokens"])
	outputSpeech := awsRealtimeNumberAsInt(output["speechTokens"])
	outputText := awsRealtimeNumberAsInt(output["textTokens"])
	inputTokens := inputSpeech + inputText
	outputTokens := outputSpeech + outputText
	ttft := 0.0
	s.mu.Lock()
	generation := s.generation
	if generation != nil && !generation.createdAt.IsZero() && !generation.firstTokenAt.IsZero() {
		ttft = generation.firstTokenAt.Sub(generation.createdAt).Seconds()
	}
	s.mu.Unlock()
	s.emit(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeMetricsCollected,
		Metrics: &telemetry.RealtimeModelMetrics{
			Label:        s.model.Label(),
			RequestID:    awsRealtimeMapString(usage, "completionId"),
			Timestamp:    time.Now(),
			TTFT:         ttft,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  inputTokens + outputTokens,
			InputTokenDetails: telemetry.InputTokenDetails{
				TextTokens:  inputText,
				AudioTokens: inputSpeech,
			},
			OutputTokenDetails: telemetry.OutputTokenDetails{
				TextTokens:  outputText,
				AudioTokens: outputSpeech,
			},
			Metadata: &telemetry.Metadata{
				ModelName:     s.model.Model(),
				ModelProvider: s.model.Provider(),
			},
		},
	})
}

func awsRealtimeNestedMap(root map[string]any, path ...string) map[string]any {
	var current any = root
	for _, key := range path {
		asMap, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = asMap[key]
	}
	asMap, _ := current.(map[string]any)
	return asMap
}

func awsRealtimeMapString(root map[string]any, key string) string {
	value, _ := root[key].(string)
	return value
}

func awsRealtimeRequiredMapString(root map[string]any, key string) (string, bool) {
	value, ok := root[key].(string)
	return value, ok
}

func awsRealtimeNumberAsInt(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func (s *awsRealtimeSession) emit(event llm.RealtimeEvent) {
	s.mu.Lock()
	if event.Type == llm.RealtimeEventTypeInputAudioTranscriptionCompleted &&
		event.InputTranscription != nil &&
		event.InputTranscription.IsFinal &&
		event.InputTranscription.ItemID != "" {
		s.audioMessages[event.InputTranscription.ItemID] = struct{}{}
	}
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	s.eventStream.Send(event)
}

func (s *awsRealtimeSession) UpdateInstructions(instructions string) error {
	s.mu.Lock()
	s.instructions = instructions
	s.mu.Unlock()
	return nil
}
func (s *awsRealtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	if chatCtx == nil {
		return nil
	}
	chatCtx = awsRealtimeSanitizeChatContext(chatCtx)
	s.mu.Lock()
	closed := s.closed
	if closed {
		s.mu.Unlock()
		return nil
	}
	started := s.stream != nil
	if s.chatCtx == nil {
		s.chatCtx = awsRealtimeStripLeadingAssistant(chatCtx.Copy())
	} else {
		s.chatCtx = chatCtx.Copy()
	}
	s.mu.Unlock()
	if !started {
		return nil
	}
	for _, item := range chatCtx.Items {
		if msg, ok := item.(*llm.ChatMessage); ok && msg.Role == llm.ChatRoleUser {
			if s.isAudioTranscriptMessage(msg.ID) {
				continue
			}
			if strings.TrimSpace(msg.TextContent()) == "" {
				continue
			}
			if s.markInteractiveUserTextSent(msg.ID) {
				pending := s.createPendingGenerationStart()
				go s.sendInteractiveUserTextAsync(msg, pending)
			}
			continue
		}
		output, ok := item.(*llm.FunctionCallOutput)
		if !ok {
			continue
		}
		s.mu.Lock()
		_, pending := s.pending[output.CallID]
		s.mu.Unlock()
		if !pending {
			continue
		}
		content := output.Output
		if output.IsError {
			data, err := json.Marshal(map[string]string{"error": output.Output})
			if err != nil {
				return err
			}
			content = string(data)
		} else {
			content = normalizeAWSRealtimeToolResult(content)
		}
		s.mu.Lock()
		delete(s.pending, output.CallID)
		shouldRecycle := len(s.pending) == 0 && s.recycleToolsAfterPending
		if shouldRecycle {
			s.recycleToolsAfterPending = false
		}
		s.mu.Unlock()
		if err := s.sendToolResult(context.Background(), output.CallID, content); err != nil {
			return err
		}
		if shouldRecycle {
			return s.recycleForUpdatedTools(context.Background())
		}
	}
	return nil
}

func awsRealtimeSanitizeChatContext(chatCtx *llm.ChatContext) *llm.ChatContext {
	return chatCtx.Copy(llm.ChatContextCopyOptions{
		ExcludeHandoff:      true,
		ExcludeInstructions: true,
		ExcludeEmptyMessage: true,
		ExcludeConfigUpdate: true,
	})
}

func awsRealtimeStripLeadingAssistant(chatCtx *llm.ChatContext) *llm.ChatContext {
	if chatCtx == nil || len(chatCtx.Items) == 0 {
		return chatCtx
	}
	msg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || msg.Role != llm.ChatRoleAssistant {
		return chatCtx
	}
	chatCtx.Items = chatCtx.Items[1:]
	return chatCtx
}

func (s *awsRealtimeSession) isAudioTranscriptMessage(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.audioMessages[id]
	return ok
}

func normalizeAWSRealtimeToolResult(content string) string {
	var parsed any
	if err := json.Unmarshal([]byte(content), &parsed); err == nil {
		if text, ok := parsed.(string); ok {
			return text
		}
		if parsed != nil {
			return content
		}
	}
	data, err := json.Marshal(map[string]string{"tool_result": content})
	if err != nil {
		return content
	}
	return string(data)
}

func (s *awsRealtimeSession) sendInteractiveUserText(ctx context.Context, msg *llm.ChatMessage) error {
	text := msg.TextContent()
	if msg.ID == "" || text == "" {
		return nil
	}
	if !s.markInteractiveUserTextSent(msg.ID) {
		return nil
	}
	return s.sendInteractiveUserTextEvents(ctx, text)
}

func (s *awsRealtimeSession) markInteractiveUserTextSent(messageID string) bool {
	if messageID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, alreadySent := s.sent[messageID]; alreadySent {
		return false
	}
	s.sent[messageID] = struct{}{}
	return true
}

func (s *awsRealtimeSession) sendInteractiveUserTextAsync(msg *llm.ChatMessage, pending *awsRealtimePendingGenerationStart) {
	if err := s.sendInteractiveUserTextEvents(context.Background(), msg.TextContent()); err != nil {
		s.clearPendingGenerationStart(pending)
		pending.resolve(err)
	}
}

func (s *awsRealtimeSession) sendInteractiveUserTextEvents(ctx context.Context, text string) error {
	events, err := s.builder.createInteractiveTextContentBlock(uuid.NewString(), "USER", text)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := s.sendRawEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *awsRealtimeSession) markChatContextUserMessagesSent(chatCtx *llm.ChatContext) {
	if chatCtx == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range chatCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok || msg.Role != llm.ChatRoleUser || msg.ID == "" || msg.TextContent() == "" {
			continue
		}
		s.sent[msg.ID] = struct{}{}
	}
}

func (s *awsRealtimeSession) sendToolResult(ctx context.Context, toolUseID string, result string) error {
	events, err := s.builder.createToolContentBlock(uuid.NewString(), toolUseID, result)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := s.sendRawEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
func (s *awsRealtimeSession) UpdateTools(tools []llm.Tool) error {
	s.mu.Lock()
	changed := awsRealtimeToolNamesChanged(s.tools, tools)
	s.tools = append([]llm.Tool(nil), tools...)
	started := s.stream != nil && !s.closed
	if started && changed {
		s.recycleToolsAfterPending = true
		s.toolRecycleVersion++
		version := s.toolRecycleVersion
		delay := defaultAWSRealtimeToolRecycle
		if s.model != nil {
			delay = s.model.toolRecycleDelay
		}
		if delay <= 0 {
			delay = defaultAWSRealtimeToolRecycle
		}
		time.AfterFunc(delay, func() {
			s.recycleAfterStalePendingTools(context.Background(), version)
		})
	}
	s.mu.Unlock()
	return nil
}

func awsRealtimeToolNamesChanged(oldTools []llm.Tool, newTools []llm.Tool) bool {
	if len(oldTools) != len(newTools) {
		return true
	}
	for i := range oldTools {
		if oldTools[i] == nil || newTools[i] == nil {
			if oldTools[i] != newTools[i] {
				return true
			}
			continue
		}
		if oldTools[i].Name() != newTools[i].Name() {
			return true
		}
	}
	return false
}

func (s *awsRealtimeSession) recycleAfterStalePendingTools(ctx context.Context, version int) {
	s.mu.Lock()
	if s.closed || !s.recycleToolsAfterPending || version != s.toolRecycleVersion {
		s.mu.Unlock()
		return
	}
	s.pending = make(map[string]struct{})
	s.recycleToolsAfterPending = false
	s.mu.Unlock()

	if err := s.recycleForUpdatedTools(ctx); err != nil {
		s.emit(llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: err,
		})
		return
	}
	s.emit(llm.RealtimeEvent{
		Type:      llm.RealtimeEventTypeSessionReconnected,
		Reconnect: &llm.RealtimeSessionReconnectedEvent{},
	})
}

func (s *awsRealtimeSession) recycleForUpdatedTools(ctx context.Context) error {
	s.closeGeneration()
	s.mu.Lock()
	stream := s.stream
	closeEvents, err := s.builder.createPromptEndBlock()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.stream = nil
	s.builder = newAWSRealtimeEventBuilder(uuid.NewString(), uuid.NewString())
	s.pending = make(map[string]struct{})
	s.recycleToolsAfterPending = false
	s.awaitingAudioEndTurn = false
	s.mu.Unlock()
	if stream != nil {
		var closeErr error
		for _, event := range closeEvents {
			if err := sendAWSRealtimeRawEvent(ctx, stream, event); err != nil {
				closeErr = err
				break
			}
		}
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return s.start(ctx)
}

func (s *awsRealtimeSession) UpdateOptions(llm.RealtimeSessionOptions) error {
	return nil
}
func (s *awsRealtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	if s.model == nil {
		return nil
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil
	}
	if s.model.modalities != defaultAWSRealtimeModalities {
		s.emitEmptyGenerationCreated(true)
		return nil
	}
	if options.Instructions == "" {
		s.mu.Lock()
		pending := s.pendingGenerationStart
		s.mu.Unlock()
		if pending == nil {
			return s.waitForMissingGenerationStart()
		}
		return s.waitForGenerationStart(pending)
	}
	pending := s.createPendingGenerationStart()
	msg := &llm.ChatMessage{
		ID:      uuid.NewString(),
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: options.Instructions}},
	}
	timeout := s.model.generateReplyTimeout
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout >= 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := s.sendInteractiveUserText(ctx, msg); err != nil {
		s.clearPendingGenerationStart(pending)
		return err
	}
	return s.waitForGenerationStart(pending)
}

func (s *awsRealtimeSession) createPendingGenerationStart() *awsRealtimePendingGenerationStart {
	pending := newAWSRealtimePendingGenerationStart()
	s.mu.Lock()
	s.pendingGenerationStart = pending
	s.mu.Unlock()
	return pending
}

func (s *awsRealtimeSession) resolvePendingGenerationStart() {
	s.mu.Lock()
	pending := s.pendingGenerationStart
	if pending != nil {
		s.pendingGenerationStart = nil
	}
	s.mu.Unlock()
	if pending != nil {
		pending.resolve(nil)
	}
}

func (s *awsRealtimeSession) rejectPendingGenerationStart(err error) {
	s.mu.Lock()
	pending := s.pendingGenerationStart
	if pending != nil {
		s.pendingGenerationStart = nil
	}
	s.mu.Unlock()
	if pending != nil {
		pending.resolve(err)
	}
}

func (s *awsRealtimeSession) clearPendingGenerationStart(pending *awsRealtimePendingGenerationStart) {
	s.mu.Lock()
	if s.pendingGenerationStart == pending {
		s.pendingGenerationStart = nil
	}
	s.mu.Unlock()
}

func (s *awsRealtimeSession) waitForGenerationStart(pending *awsRealtimePendingGenerationStart) error {
	timeout := time.Duration(0)
	if s.model != nil {
		timeout = s.model.generateReplyTimeout
	}
	if timeout < 0 {
		return pending.wait()
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-pending.done:
		return pending.wait()
	case <-timer.C:
		s.clearPendingGenerationStart(pending)
		return llm.NewRealtimeError("generate_reply timed out waiting for generation", nil)
	}
}

func (s *awsRealtimeSession) waitForMissingGenerationStart() error {
	timeout := time.Duration(0)
	if s.model != nil {
		timeout = s.model.generateReplyTimeout
	}
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		<-timer.C
	}
	return llm.NewRealtimeError("generate_reply timed out waiting for generation", nil)
}

func (s *awsRealtimeSession) emitEmptyGenerationCreated(userInitiated bool) {
	messageCh := make(chan llm.MessageGeneration)
	functionCh := make(chan *llm.FunctionCall)
	close(messageCh)
	close(functionCh)
	s.emit(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:     messageCh,
			FunctionCh:    functionCh,
			UserInitiated: userInitiated,
		},
	})
}

func (s *awsRealtimeSession) Say(string) error {
	return fmt.Errorf("awsRealtimeSession does not implement say(). use a TTS model instead")
}
func (s *awsRealtimeSession) Truncate(llm.RealtimeTruncateOptions) error {
	return nil
}
func (s *awsRealtimeSession) Interrupt() error {
	return nil
}

func (s *awsRealtimeSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	stream := s.stream
	if s.recycleTimer != nil {
		s.recycleTimer.Stop()
		s.recycleTimer = nil
	}
	s.mu.Unlock()

	s.rejectPendingGenerationStart(llm.NewRealtimeError("Session closed while waiting for generation", nil))
	s.closeAudioInputSender()
	s.closeGeneration()
	var closeErr error
	if stream != nil {
		closeEvents, err := s.builder.createPromptEndBlock()
		if err != nil {
			closeErr = err
		} else {
			for _, event := range closeEvents {
				if err := sendAWSRealtimeRawEvent(context.Background(), stream, event); err != nil {
					closeErr = err
					break
				}
			}
		}
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	s.eventStream.Close()
	return closeErr
}

func (s *awsRealtimeSession) EventCh() <-chan llm.RealtimeEvent { return s.eventCh }

func (s *awsRealtimeSession) PushAudio(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil
	}
	normalized, err := s.audioNorm.normalize(frame)
	if err != nil {
		return err
	}
	for _, chunk := range s.audioBStream.Write(normalized.Data) {
		event, err := s.builder.createAudioInputEvent(base64.StdEncoding.EncodeToString(chunk.Data))
		if err != nil {
			return err
		}
		s.enqueueAudioInputEvent(event)
	}
	return nil
}

type awsRealtimeAudioInputEvent struct {
	event   string
	started chan struct{}
}

func (s *awsRealtimeSession) enqueueAudioInputEvent(event string) <-chan struct{} {
	started := make(chan struct{})
	s.audioMu.Lock()
	defer s.audioMu.Unlock()
	if s.audioClosed {
		close(started)
		return started
	}
	s.audioQueue = append(s.audioQueue, awsRealtimeAudioInputEvent{event: event, started: started})
	s.audioCond.Signal()
	return started
}

func (s *awsRealtimeSession) runAudioInputSender() {
	defer close(s.audioSenderDone)
	for {
		s.audioMu.Lock()
		for len(s.audioQueue) == 0 && !s.audioClosed {
			s.audioCond.Wait()
		}
		if len(s.audioQueue) == 0 && s.audioClosed {
			s.audioMu.Unlock()
			return
		}
		item := s.audioQueue[0]
		s.audioQueue[0] = awsRealtimeAudioInputEvent{}
		s.audioQueue = s.audioQueue[1:]
		ctx := s.audioCtx
		s.audioMu.Unlock()

		if ctx.Err() != nil {
			close(item.started)
			return
		}
		close(item.started)
		_ = s.sendRawEvent(ctx, item.event)
		if ctx.Err() != nil {
			return
		}
	}
}

func waitAWSRealtimeAudioInputEventStarted(ctx context.Context, started <-chan struct{}) error {
	if started == nil {
		return nil
	}
	select {
	case <-started:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *awsRealtimeSession) closeAudioInputSender() {
	s.audioMu.Lock()
	if !s.audioClosed {
		s.audioClosed = true
		for _, item := range s.audioQueue {
			close(item.started)
		}
		s.audioQueue = nil
		s.audioCond.Broadcast()
	}
	cancel := s.audioCancel
	s.audioMu.Unlock()
	if cancel != nil {
		cancel()
	}
	<-s.audioSenderDone
}

type awsRealtimeInputAudioNormalizer struct {
	sampleRate    uint32
	numChannels   uint32
	inputSamples  uint64
	outputSamples uint64
	remainder     uint64
	lastSample    int16
	hasTail       bool
}

func (n *awsRealtimeInputAudioNormalizer) normalize(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if frame == nil {
		return nil, nil
	}
	if frame.SampleRate == 0 || frame.NumChannels == 0 {
		return frame, nil
	}
	if frame.SampleRate == defaultAWSRealtimeInputSampleRate && frame.NumChannels == defaultAWSRealtimeChannels {
		n.reset()
		return frame, nil
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("aws realtime input audio must be 16-bit PCM")
	}
	samplesPerChannel := frame.SamplesPerChannel
	if samplesPerChannel == 0 {
		samplesPerChannel = uint32(len(frame.Data)) / frame.NumChannels / 2
	}
	expectedBytes := int(samplesPerChannel * frame.NumChannels * 2)
	if len(frame.Data) < expectedBytes {
		return nil, fmt.Errorf("aws realtime input audio data is shorter than declared sample count")
	}
	if n.sampleRate != frame.SampleRate || n.numChannels != frame.NumChannels {
		n.sampleRate = frame.SampleRate
		n.numChannels = frame.NumChannels
		n.inputSamples = 0
		n.outputSamples = 0
		n.remainder = 0
		n.hasTail = false
	}
	if samplesPerChannel == 0 {
		return &model.AudioFrame{
			SampleRate:        defaultAWSRealtimeInputSampleRate,
			NumChannels:       defaultAWSRealtimeChannels,
			SamplesPerChannel: 0,
		}, nil
	}

	prevInputSamples := n.inputSamples
	nextInputSamples := prevInputSamples + uint64(samplesPerChannel)
	prevOutputSamples := n.outputSamples
	nextOutputSamples := nextInputSamples * uint64(defaultAWSRealtimeInputSampleRate) / uint64(frame.SampleRate)
	outSamples := uint32(nextOutputSamples - prevOutputSamples)
	out := make([]byte, int(outSamples*defaultAWSRealtimeChannels*2))
	channelCount := int(frame.NumChannels)
	previousSample := n.lastSample
	hasPrevious := n.hasTail
	n.hasTail = false
	for outIdx := uint32(0); outIdx < outSamples; outIdx++ {
		srcPos := (prevOutputSamples + uint64(outIdx)) * uint64(frame.SampleRate)
		srcGlobalIdx := srcPos / uint64(defaultAWSRealtimeInputSampleRate)
		frac := srcPos % uint64(defaultAWSRealtimeInputSampleRate)
		sample := awsRealtimeInterpolatedInputSample(frame, srcGlobalIdx, frac, uint64(defaultAWSRealtimeInputSampleRate), prevInputSamples, samplesPerChannel, channelCount, previousSample, hasPrevious)
		binary.LittleEndian.PutUint16(out[int(outIdx)*2:int(outIdx)*2+2], uint16(sample))
	}
	n.inputSamples = nextInputSamples
	n.outputSamples = nextOutputSamples
	n.remainder = nextInputSamples * uint64(defaultAWSRealtimeInputSampleRate) % uint64(frame.SampleRate)
	n.lastSample = awsRealtimeInputSample(frame, uint64(samplesPerChannel-1), samplesPerChannel, channelCount)
	n.hasTail = n.remainder > 0

	return &model.AudioFrame{
		Data:              out,
		SampleRate:        defaultAWSRealtimeInputSampleRate,
		NumChannels:       defaultAWSRealtimeChannels,
		SamplesPerChannel: outSamples,
		ParticipantID:     frame.ParticipantID,
	}, nil
}

func (n *awsRealtimeInputAudioNormalizer) reset() {
	n.sampleRate = 0
	n.numChannels = 0
	n.inputSamples = 0
	n.outputSamples = 0
	n.remainder = 0
	n.lastSample = 0
	n.hasTail = false
}

func awsRealtimeInputSample(frame *model.AudioFrame, sampleIndex uint64, samplesPerChannel uint32, channelCount int) int16 {
	if sampleIndex >= uint64(samplesPerChannel) {
		sampleIndex = uint64(samplesPerChannel - 1)
	}
	var sum int32
	for ch := 0; ch < channelCount; ch++ {
		offset := (int(sampleIndex)*channelCount + ch) * 2
		sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2])))
	}
	return int16(sum / int32(channelCount))
}

func awsRealtimeInterpolatedInputSample(frame *model.AudioFrame, sampleIndex uint64, frac uint64, denom uint64, frameStart uint64, samplesPerChannel uint32, channelCount int, previousSample int16, hasPrevious bool) int16 {
	current := awsRealtimeInputSampleAt(frame, sampleIndex, frameStart, samplesPerChannel, channelCount, previousSample, hasPrevious)
	if frac == 0 || denom == 0 {
		return current
	}
	next := awsRealtimeInputSampleAt(frame, sampleIndex+1, frameStart, samplesPerChannel, channelCount, previousSample, hasPrevious)
	return int16((int64(current)*int64(denom-frac) + int64(next)*int64(frac)) / int64(denom))
}

func awsRealtimeInputSampleAt(frame *model.AudioFrame, sampleIndex uint64, frameStart uint64, samplesPerChannel uint32, channelCount int, previousSample int16, hasPrevious bool) int16 {
	if sampleIndex < frameStart {
		if hasPrevious {
			return previousSample
		}
		return awsRealtimeInputSample(frame, 0, samplesPerChannel, channelCount)
	}
	localIdx := sampleIndex - frameStart
	if localIdx >= uint64(samplesPerChannel) {
		localIdx = uint64(samplesPerChannel - 1)
	}
	return awsRealtimeInputSample(frame, localIdx, samplesPerChannel, channelCount)
}

func (s *awsRealtimeSession) PushVideo(*images.VideoFrame) error {
	return nil
}
func (s *awsRealtimeSession) CommitAudio() error {
	return nil
}
func (s *awsRealtimeSession) ClearAudio() error {
	return nil
}
