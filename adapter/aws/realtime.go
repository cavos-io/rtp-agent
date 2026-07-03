package aws

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
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
	defaultAWSRealtimeModel          = "amazon.nova-2-sonic-v1:0"
	defaultAWSRealtimeNovaSonic1     = "amazon.nova-sonic-v1:0"
	defaultAWSRealtimeNovaSonic2     = "amazon.nova-2-sonic-v1:0"
	defaultAWSRealtimeVoice          = "tiffany"
	defaultAWSRealtimeTurnDetection  = "MEDIUM"
	defaultAWSRealtimeModalities     = "mixed"
	defaultAWSRealtimeMaxMessages    = 40
	defaultAWSRealtimeMaxMessageSize = 1024
	defaultAWSRealtimeMaxRestarts    = 3
	defaultAWSRealtimeMaxSession     = 6 * time.Minute
	defaultAWSRealtimeRecycleQuiet   = time.Second
	defaultAWSRealtimeToolRecycle    = 150 * time.Millisecond
	awsRealtimeAudioModalities       = "audio"
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

type AWSRealtimeModel struct {
	model              string
	region             string
	voice              string
	modalities         string
	turnDetection      string
	maxTokens          int
	topP               float64
	temperature        float64
	maxSession         time.Duration
	recycleQuietPeriod time.Duration
	toolRecycleDelay   time.Duration
	maxTokensSet       bool
	topPSet            bool
	temperatureSet     bool
	client             awsRealtimeClient
}

type AWSRealtimeOption func(*AWSRealtimeModel)

func NewAWSRealtimeModel(model string, opts ...AWSRealtimeOption) *AWSRealtimeModel {
	provider := &AWSRealtimeModel{
		model:              awsRealtimeModelOrDefault(model),
		region:             awsRegionOrDefault(""),
		voice:              defaultAWSRealtimeVoice,
		modalities:         defaultAWSRealtimeModalities,
		turnDetection:      defaultAWSRealtimeTurnDetection,
		maxSession:         defaultAWSRealtimeMaxSession,
		recycleQuietPeriod: defaultAWSRealtimeRecycleQuiet,
		toolRecycleDelay:   defaultAWSRealtimeToolRecycle,
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

func awsRealtimeModelOrDefault(model string) string {
	if model != "" {
		return model
	}
	return defaultAWSRealtimeModel
}

func (m *AWSRealtimeModel) Label() string         { return "aws.RealtimeModel" }
func (m *AWSRealtimeModel) Model() string         { return m.model }
func (m *AWSRealtimeModel) Provider() string      { return awsRealtimeProvider }
func (m *AWSRealtimeModel) Region() string        { return m.region }
func (m *AWSRealtimeModel) Voice() string         { return m.voice }
func (m *AWSRealtimeModel) Modalities() string    { return m.modalities }
func (m *AWSRealtimeModel) TurnDetection() string { return m.turnDetection }

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
	}
	session := newAWSRealtimeSession(m, client)
	if err := session.start(context.Background()); err != nil {
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
	client *bedrockruntime.Client
}

func newAWSRealtimeSDKClient(ctx context.Context, region string) (*awsRealtimeSDKClient, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(awsRegionOrDefault(region)))
	if err != nil {
		return nil, err
	}
	return &awsRealtimeSDKClient{client: bedrockruntime.NewFromConfig(cfg)}, nil
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
	eventCh                  chan llm.RealtimeEvent
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
	currentUserContentID     string
	audioBStream             *coreaudio.AudioByteStream
	audioNorm                awsRealtimeInputAudioNormalizer
	mu                       sync.Mutex
	closed                   bool
}

type awsRealtimeGeneration struct {
	responseID   string
	messageCh    chan llm.MessageGeneration
	functionCh   chan *llm.FunctionCall
	textCh       chan string
	audioCh      chan *model.AudioFrame
	modalitiesCh chan []string
	contentTypes map[string]string
	restarts     int
	closeOnce    sync.Once
}

type awsRealtimeStartOptions struct {
	restart bool
}

func newAWSRealtimeSession(model *AWSRealtimeModel, client awsRealtimeClient) *awsRealtimeSession {
	session := &awsRealtimeSession{
		model:                    model,
		client:                   client,
		builder:                  newAWSRealtimeEventBuilder(uuid.NewString(), uuid.NewString()),
		eventCh:                  make(chan llm.RealtimeEvent, 16),
		pending:                  make(map[string]struct{}),
		sent:                     make(map[string]struct{}),
		audioMessages:            make(map[string]struct{}),
		noGenerationContentRoles: make(map[string]string),
		audioBStream: coreaudio.NewAudioByteStream(
			defaultAWSRealtimeInputSampleRate,
			defaultAWSRealtimeChannels,
			defaultAWSRealtimeInputChunkSize,
		),
	}
	session.turns = newAWSRealtimeTurnTracker(session.emit, session.emitGenerationCreated)
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
		maxTokensSet:           s.model.maxTokensSet,
		topPSet:                s.model.topPSet,
		temperatureSet:         s.model.temperatureSet,
		endpointingSensitivity: s.model.turnDetection,
	})
	if err != nil {
		return err
	}
	for _, event := range append(initEvents, historyEvents...) {
		if err := s.sendRawEvent(ctx, event); err != nil {
			s.closeStartupStream()
			return awsRealtimeStartupSendError(err)
		}
	}
	s.markChatContextUserMessagesSent(chatCtx)
	audioStart, err := s.builder.createAudioContentStartEvent(defaultAWSRealtimeInputSampleRate)
	if err != nil {
		return err
	}
	if err := s.sendRawEvent(ctx, audioStart); err != nil {
		s.closeStartupStream()
		return awsRealtimeStartupSendError(err)
	}
	if interactiveUserText != "" {
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
	go s.readResponses()
	return nil
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
	duration := s.model.maxSession
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
		activeGeneration := s.generation != nil
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
	if s.stream == nil {
		return errors.New("AWS Nova Sonic realtime stream is not initialized")
	}
	return sendAWSRealtimeRawEvent(ctx, s.stream, event)
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

func (s *awsRealtimeSession) readResponses() {
	stream := s.stream
	if stream == nil {
		return
	}
	for event := range stream.Events() {
		chunk, ok := event.(*awstypes.InvokeModelWithBidirectionalStreamOutputMemberChunk)
		if !ok || len(chunk.Value.Bytes) == 0 {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(chunk.Value.Bytes, &payload); err != nil {
			s.emit(llm.RealtimeEvent{
				Type:  llm.RealtimeEventTypeError,
				Error: llm.NewRealtimeError("failed to decode AWS Nova Sonic realtime event", err),
			})
			continue
		}
		s.handleResponseEvent(payload)
	}
	if err := stream.Err(); err != nil {
		if s.restartAfterRecoverableReadError(stream, err) {
			return
		}
		s.closeGeneration()
		var apiErr error = llm.NewAPIConnectionError(fmt.Sprintf("AWS Nova Sonic realtime stream failed: %v", err))
		if errors.Is(err, context.DeadlineExceeded) {
			apiErr = llm.NewAPITimeoutError(err.Error())
		}
		s.emit(llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: apiErr,
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

func (s *awsRealtimeSession) handleResponseEvent(payload map[string]any) {
	if completionStart := awsRealtimeNestedMap(payload, "event", "completionStart"); completionStart != nil {
		s.emitGenerationCreated()
	}
	if s.turns != nil {
		s.turns.feed(payload)
	}
	s.trackGenerationContentStart(payload)
	if audioContent := awsRealtimeNestedString(payload, "event", "audioOutput", "content"); audioContent != "" {
		data, err := base64.StdEncoding.DecodeString(audioContent)
		if err != nil {
			s.emit(llm.RealtimeEvent{
				Type:  llm.RealtimeEventTypeError,
				Error: llm.NewRealtimeError("failed to decode AWS Nova Sonic audio output", err),
			})
			return
		}
		if s.sendGenerationAudio(awsRealtimeNestedString(payload, "event", "audioOutput", "contentId"), data) {
			s.markLastAudioOutput()
			s.emit(llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeAudio,
				Data: data,
			})
		}
	}
	if textContent := awsRealtimeNestedString(payload, "event", "textOutput", "content"); textContent != "" {
		contentID := awsRealtimeNestedString(payload, "event", "textOutput", "contentId")
		role := awsRealtimeNestedString(payload, "event", "textOutput", "role")
		if textContent == awsRealtimeBargeInContent {
			if !s.hasPendingTools() {
				s.closeGeneration()
			}
			return
		}
		if s.shouldStoreProviderUserText(contentID, role) {
			s.updateProviderTextHistory(llm.ChatRoleUser, textContent, contentID)
		}
		if role == "ASSISTANT" && textContent != awsRealtimeBargeInContent {
			if s.isProviderAssistantText(contentID) {
				s.updateProviderTextHistory(llm.ChatRoleAssistant, textContent, "")
			}
			s.sendGenerationText(contentID, textContent)
		}
	}
	if toolUseID := awsRealtimeNestedString(payload, "event", "toolUse", "toolUseId"); toolUseID != "" {
		if !s.sendGenerationFunction(&llm.FunctionCall{
			CallID:    toolUseID,
			Name:      awsRealtimeNestedString(payload, "event", "toolUse", "toolName"),
			Arguments: normalizeAWSRealtimeToolArguments(awsRealtimeNestedString(payload, "event", "toolUse", "content")),
		}) {
			return
		}
		s.mu.Lock()
		s.pending[toolUseID] = struct{}{}
		s.mu.Unlock()
	}
	if usage := awsRealtimeNestedMap(payload, "event", "usageEvent"); usage != nil {
		s.emitUsageMetrics(usage)
	}
	if awsRealtimeNestedMap(payload, "event", "completionEnd") != nil {
		s.closeGeneration()
	}
	if awsRealtimeNestedString(payload, "event", "contentEnd", "type") == "AUDIO" &&
		awsRealtimeNestedString(payload, "event", "contentEnd", "stopReason") == "END_TURN" {
		s.closeGeneration()
	}
}

func (s *awsRealtimeSession) hasPendingTools() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0
}

func (s *awsRealtimeSession) emitGenerationCreated() {
	s.emitGenerationCreatedWithResponseID("")
}

func (s *awsRealtimeSession) emitGenerationCreatedWithResponseID(responseID string) {
	generation := s.ensureGeneration(responseID)
	s.emit(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:  generation.messageCh,
			FunctionCh: generation.functionCh,
			ResponseID: generation.responseID,
		},
	})
}

func (s *awsRealtimeSession) ensureGeneration(responseID string) *awsRealtimeGeneration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != nil {
		return s.generation
	}
	if responseID == "" {
		responseID = uuid.NewString()
	}
	generation := &awsRealtimeGeneration{
		responseID:   responseID,
		messageCh:    make(chan llm.MessageGeneration, 1),
		functionCh:   make(chan *llm.FunctionCall, 8),
		textCh:       make(chan string, 16),
		audioCh:      make(chan *model.AudioFrame, 16),
		modalitiesCh: make(chan []string, 1),
		contentTypes: make(map[string]string),
	}
	generation.modalitiesCh <- []string{"audio", "text"}
	generation.messageCh <- llm.MessageGeneration{
		MessageID:    generation.responseID,
		TextCh:       generation.textCh,
		AudioCh:      generation.audioCh,
		ModalitiesCh: generation.modalitiesCh,
	}
	s.generation = generation
	return generation
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
	if streamType == "ASSISTANT_TEXT" {
		generation = s.ensureGeneration("")
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

func (s *awsRealtimeSession) shouldStoreProviderUserText(contentID string, role string) bool {
	if role != "USER" {
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

func (s *awsRealtimeSession) updateProviderTextHistory(role llm.ChatRole, text string, contentID string) {
	if text == "" {
		return
	}
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
	select {
	case generation.audioCh <- frame:
	default:
	}
	return true
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
	}
	s.mu.Unlock()
	if generation == nil || streamType != "ASSISTANT_TEXT" {
		return
	}
	select {
	case generation.textCh <- text:
	default:
	}
}

func (s *awsRealtimeSession) sendGenerationFunction(call *llm.FunctionCall) bool {
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	if generation == nil {
		return false
	}
	select {
	case generation.functionCh <- call:
	default:
	}
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
		close(generation.textCh)
		close(generation.audioCh)
		close(generation.modalitiesCh)
		close(generation.messageCh)
		close(generation.functionCh)
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
	s.emit(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeMetricsCollected,
		Metrics: &telemetry.RealtimeModelMetrics{
			Label:        s.model.Label(),
			RequestID:    awsRealtimeMapString(usage, "completionId"),
			Timestamp:    time.Now(),
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
	select {
	case s.eventCh <- event:
	default:
	}
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
	s.mu.Lock()
	closed := s.closed
	if closed {
		s.mu.Unlock()
		return nil
	}
	started := s.stream != nil
	s.chatCtx = chatCtx.Copy()
	s.mu.Unlock()
	if !started {
		return nil
	}
	for _, item := range chatCtx.Items {
		if msg, ok := item.(*llm.ChatMessage); ok && msg.Role == llm.ChatRoleUser {
			if s.isAudioTranscriptMessage(msg.ID) {
				continue
			}
			if err := s.sendInteractiveUserText(context.Background(), msg); err != nil {
				return err
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
		if err := s.sendToolResult(context.Background(), output.CallID, content); err != nil {
			return err
		}
		s.mu.Lock()
		delete(s.pending, output.CallID)
		shouldRecycle := len(s.pending) == 0 && s.recycleToolsAfterPending
		if shouldRecycle {
			s.recycleToolsAfterPending = false
		}
		s.mu.Unlock()
		if shouldRecycle {
			return s.recycleForUpdatedTools(context.Background())
		}
	}
	return nil
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
		if _, ok := parsed.(string); !ok {
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
	s.mu.Lock()
	_, alreadySent := s.sent[msg.ID]
	s.mu.Unlock()
	if alreadySent {
		return nil
	}
	events, err := s.builder.createInteractiveTextContentBlock(uuid.NewString(), "USER", text)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sent[msg.ID] = struct{}{}
	s.mu.Unlock()
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
	hasPendingTools := len(s.pending) > 0
	if started && changed && hasPendingTools {
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
	if started && changed && hasPendingTools {
		return nil
	}
	if started && changed {
		return s.recycleForUpdatedTools(context.Background())
	}
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
	s.mu.Unlock()
	if stream != nil {
		for _, event := range closeEvents {
			if err := sendAWSRealtimeRawEvent(ctx, stream, event); err != nil {
				return err
			}
		}
		if err := stream.Close(); err != nil {
			return err
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
		return nil
	}
	msg := &llm.ChatMessage{
		ID:      uuid.NewString(),
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: options.Instructions}},
	}
	return s.sendInteractiveUserText(context.Background(), msg)
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

func (s *awsRealtimeSession) Say(string) error { return awsRealtimeUnsupported("say") }
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

	s.closeGeneration()
	if stream != nil {
		closeEvents, err := s.builder.createPromptEndBlock()
		if err != nil {
			return err
		}
		for _, event := range closeEvents {
			if err := s.sendRawEvent(context.Background(), event); err != nil {
				return err
			}
		}
		if err := stream.Close(); err != nil {
			return err
		}
	}
	close(s.eventCh)
	return nil
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
		if err := s.sendRawEvent(context.Background(), event); err != nil {
			return err
		}
	}
	return nil
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

func awsRealtimeUnsupported(operation string) error {
	return fmt.Errorf("%s is not supported by the AWS Nova Sonic realtime model", operation)
}
