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
	defaultAWSRealtimeModel         = "amazon.nova-2-sonic-v1:0"
	defaultAWSRealtimeNovaSonic1    = "amazon.nova-sonic-v1:0"
	defaultAWSRealtimeNovaSonic2    = "amazon.nova-2-sonic-v1:0"
	defaultAWSRealtimeVoice         = "tiffany"
	defaultAWSRealtimeTurnDetection = "MEDIUM"
	defaultAWSRealtimeModalities    = "mixed"
	awsRealtimeAudioModalities      = "audio"
	awsRealtimeProvider             = "Amazon"
	defaultAWSRealtimeSystemPrompt  = "Your name is Sonic, and you are a friendly and enthusiastic voice assistant. Keep your responses natural and concise for voice interaction."
)

type AWSRealtimeModel struct {
	model         string
	region        string
	voice         string
	modalities    string
	turnDetection string
	client        awsRealtimeClient
}

type AWSRealtimeOption func(*AWSRealtimeModel)

func NewAWSRealtimeModel(model string, opts ...AWSRealtimeOption) *AWSRealtimeModel {
	provider := &AWSRealtimeModel{
		model:         awsRealtimeModelOrDefault(model),
		region:        awsRegionOrDefault(""),
		voice:         defaultAWSRealtimeVoice,
		modalities:    defaultAWSRealtimeModalities,
		turnDetection: defaultAWSRealtimeTurnDetection,
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

func WithAWSRealtimeClient(client awsRealtimeClient) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.client = client
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
	model      *AWSRealtimeModel
	client     awsRealtimeClient
	builder    *awsRealtimeEventBuilder
	stream     awsRealtimeStream
	eventCh    chan llm.RealtimeEvent
	turns      *awsRealtimeTurnTracker
	generation *awsRealtimeGeneration
	pending    map[string]struct{}
	sent       map[string]struct{}
	mu         sync.Mutex
	closed     bool
}

type awsRealtimeGeneration struct {
	responseID   string
	messageCh    chan llm.MessageGeneration
	functionCh   chan *llm.FunctionCall
	textCh       chan string
	audioCh      chan *model.AudioFrame
	modalitiesCh chan []string
	contentTypes map[string]string
	closeOnce    sync.Once
}

func newAWSRealtimeSession(model *AWSRealtimeModel, client awsRealtimeClient) *awsRealtimeSession {
	session := &awsRealtimeSession{
		model:   model,
		client:  client,
		builder: newAWSRealtimeEventBuilder(uuid.NewString(), uuid.NewString()),
		eventCh: make(chan llm.RealtimeEvent, 16),
		pending: make(map[string]struct{}),
		sent:    make(map[string]struct{}),
	}
	session.turns = newAWSRealtimeTurnTracker(session.emit, session.emitGenerationCreated)
	return session
}

func (s *awsRealtimeSession) start(ctx context.Context) error {
	stream, err := s.client.InvokeModelWithBidirectionalStream(ctx, &bedrockruntime.InvokeModelWithBidirectionalStreamInput{
		ModelId: aws.String(s.model.model),
	})
	if err != nil {
		return err
	}
	s.stream = stream
	initEvents, historyEvents, err := s.builder.createPromptStartBlock(awsRealtimePromptStartOptions{
		voiceID:                s.model.voice,
		outputSampleRate:       defaultAWSRealtimeOutputSampleRate,
		systemContent:          defaultAWSRealtimeSystemPrompt,
		maxTokens:              defaultAWSRealtimeMaxTokens,
		topP:                   defaultAWSRealtimeTopP,
		temperature:            defaultAWSRealtimeTemperature,
		endpointingSensitivity: s.model.turnDetection,
	})
	if err != nil {
		return err
	}
	for _, event := range append(initEvents, historyEvents...) {
		if err := s.sendRawEvent(ctx, event); err != nil {
			return err
		}
	}
	audioStart, err := s.builder.createAudioContentStartEvent(defaultAWSRealtimeInputSampleRate)
	if err != nil {
		return err
	}
	if err := s.sendRawEvent(ctx, audioStart); err != nil {
		return err
	}
	go s.readResponses()
	return nil
}

func (s *awsRealtimeSession) sendRawEvent(ctx context.Context, event string) error {
	if s.stream == nil {
		return errors.New("AWS Nova Sonic realtime stream is not initialized")
	}
	return s.stream.Send(ctx, &awstypes.InvokeModelWithBidirectionalStreamInputMemberChunk{
		Value: awstypes.BidirectionalInputPayloadPart{Bytes: []byte(event)},
	})
}

func (s *awsRealtimeSession) readResponses() {
	if s.stream == nil {
		return
	}
	for event := range s.stream.Events() {
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
}

func (s *awsRealtimeSession) handleResponseEvent(payload map[string]any) {
	if awsRealtimeNestedMap(payload, "event", "completionStart") != nil {
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
			s.emit(llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeAudio,
				Data: data,
			})
		}
	}
	if textContent := awsRealtimeNestedString(payload, "event", "textOutput", "content"); textContent != "" {
		if textContent == awsRealtimeBargeInContent {
			if !s.hasPendingTools() {
				s.closeGeneration()
			}
			return
		}
		if awsRealtimeNestedString(payload, "event", "textOutput", "role") == "ASSISTANT" && textContent != awsRealtimeBargeInContent {
			s.sendGenerationText(awsRealtimeNestedString(payload, "event", "textOutput", "contentId"), textContent)
			s.emit(llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeText,
				Text: textContent,
			})
		}
	}
	if toolUseID := awsRealtimeNestedString(payload, "event", "toolUse", "toolUseId"); toolUseID != "" {
		s.mu.Lock()
		s.pending[toolUseID] = struct{}{}
		s.mu.Unlock()
		s.sendGenerationFunction(&llm.FunctionCall{
			CallID:    toolUseID,
			Name:      awsRealtimeNestedString(payload, "event", "toolUse", "toolName"),
			Arguments: normalizeAWSRealtimeToolArguments(awsRealtimeNestedString(payload, "event", "toolUse", "content")),
		})
		s.emit(llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeFunctionCall,
			Function: &llm.FunctionToolCall{
				CallID:    toolUseID,
				Name:      awsRealtimeNestedString(payload, "event", "toolUse", "toolName"),
				Arguments: normalizeAWSRealtimeToolArguments(awsRealtimeNestedString(payload, "event", "toolUse", "content")),
			},
		})
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
	generation := s.ensureGeneration()
	s.emit(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:  generation.messageCh,
			FunctionCh: generation.functionCh,
			ResponseID: generation.responseID,
		},
	})
}

func (s *awsRealtimeSession) ensureGeneration() *awsRealtimeGeneration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != nil {
		return s.generation
	}
	generation := &awsRealtimeGeneration{
		responseID:   uuid.NewString(),
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
	if awsRealtimeNestedString(payload, "event", "contentStart", "role") != "ASSISTANT" {
		return
	}
	contentID := awsRealtimeNestedString(payload, "event", "contentStart", "contentId")
	if contentID == "" {
		return
	}
	contentType := awsRealtimeNestedString(payload, "event", "contentStart", "type")
	additionalFields := awsRealtimeNestedString(payload, "event", "contentStart", "additionalModelFields")
	var streamType string
	switch {
	case contentType == "AUDIO":
		streamType = "ASSISTANT_AUDIO"
	case contentType == "TEXT" && strings.Contains(additionalFields, "SPECULATIVE"):
		streamType = "ASSISTANT_TEXT"
	case contentType == "TEXT" && strings.Contains(additionalFields, "FINAL"):
		streamType = "ASSISTANT_FINAL"
	default:
		return
	}
	var generation *awsRealtimeGeneration
	if streamType == "ASSISTANT_TEXT" {
		generation = s.ensureGeneration()
	} else {
		s.mu.Lock()
		generation = s.generation
		s.mu.Unlock()
	}
	if generation == nil {
		return
	}
	s.mu.Lock()
	generation.contentTypes[contentID] = streamType
	s.mu.Unlock()
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

func (s *awsRealtimeSession) sendGenerationFunction(call *llm.FunctionCall) {
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	if generation == nil {
		return
	}
	select {
	case generation.functionCh <- call:
	default:
	}
	s.closeGeneration()
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

func (s *awsRealtimeSession) UpdateInstructions(string) error { return nil }
func (s *awsRealtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	if chatCtx == nil {
		return nil
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil
	}
	for _, item := range chatCtx.Items {
		if msg, ok := item.(*llm.ChatMessage); ok && msg.Role == llm.ChatRoleUser {
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
		if pending {
			delete(s.pending, output.CallID)
		}
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
		}
		if err := s.sendToolResult(context.Background(), output.CallID, content); err != nil {
			return err
		}
	}
	return nil
}

func (s *awsRealtimeSession) sendInteractiveUserText(ctx context.Context, msg *llm.ChatMessage) error {
	text := msg.TextContent()
	if msg.ID == "" || text == "" {
		return nil
	}
	s.mu.Lock()
	_, alreadySent := s.sent[msg.ID]
	if !alreadySent {
		s.sent[msg.ID] = struct{}{}
	}
	s.mu.Unlock()
	if alreadySent {
		return nil
	}
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
func (s *awsRealtimeSession) UpdateTools([]llm.Tool) error { return nil }
func (s *awsRealtimeSession) UpdateOptions(llm.RealtimeSessionOptions) error {
	return nil
}
func (s *awsRealtimeSession) GenerateReply(llm.RealtimeGenerateReplyOptions) error { return nil }
func (s *awsRealtimeSession) Say(string) error                                     { return awsRealtimeUnsupported("say") }
func (s *awsRealtimeSession) Truncate(llm.RealtimeTruncateOptions) error {
	return awsRealtimeUnsupported("truncate")
}
func (s *awsRealtimeSession) Interrupt() error { return nil }

func (s *awsRealtimeSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	stream := s.stream
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
	normalized, err := normalizeAWSRealtimeInputFrame(frame)
	if err != nil {
		return err
	}
	event, err := s.builder.createAudioInputEvent(base64.StdEncoding.EncodeToString(normalized.Data))
	if err != nil {
		return err
	}
	return s.sendRawEvent(context.Background(), event)
}

func normalizeAWSRealtimeInputFrame(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if frame == nil {
		return nil, nil
	}
	normalized, err := downmixAWSRealtimeInputFrameToMono(frame)
	if err != nil {
		return nil, err
	}
	if normalized.SampleRate != defaultAWSRealtimeInputSampleRate {
		normalized, err = coreaudio.ResampleAudioFrame(normalized, defaultAWSRealtimeInputSampleRate)
		if err != nil {
			return nil, err
		}
	}
	return normalized, nil
}

func downmixAWSRealtimeInputFrameToMono(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if frame == nil || frame.NumChannels <= 1 {
		return frame, nil
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("cannot downmix non-16-bit PCM audio")
	}
	expectedBytes := int(frame.SamplesPerChannel * frame.NumChannels * 2)
	if len(frame.Data) < expectedBytes {
		return nil, fmt.Errorf("audio frame data is shorter than declared sample count")
	}
	channels := int(frame.NumChannels)
	samples := int(frame.SamplesPerChannel)
	out := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		sum := 0
		for ch := 0; ch < channels; ch++ {
			offset := (i*channels + ch) * 2
			sum += int(int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2])))
		}
		binary.LittleEndian.PutUint16(out[i*2:i*2+2], uint16(int16(sum/channels)))
	}
	return &model.AudioFrame{
		Data:              out,
		SampleRate:        frame.SampleRate,
		NumChannels:       1,
		SamplesPerChannel: frame.SamplesPerChannel,
		ParticipantID:     frame.ParticipantID,
	}, nil
}

func (s *awsRealtimeSession) PushVideo(*images.VideoFrame) error {
	return awsRealtimeUnsupported("push_video")
}
func (s *awsRealtimeSession) CommitAudio() error { return nil }
func (s *awsRealtimeSession) ClearAudio() error  { return nil }

func awsRealtimeUnsupported(operation string) error {
	return fmt.Errorf("%s is not supported by the AWS Nova Sonic realtime model", operation)
}
