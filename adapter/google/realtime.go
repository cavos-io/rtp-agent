package google

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"google.golang.org/genai"
)

const (
	defaultGoogleRealtimeGeminiModel = "gemini-2.5-flash-native-audio-preview-12-2025"
	defaultGoogleRealtimeVertexModel = "gemini-live-2.5-flash-native-audio"
	defaultGoogleRealtimeVoice       = "Puck"
	defaultGoogleRealtimeLocation    = "us-central1"
	googleRealtimeInputSampleRate    = 16000
	googleRealtimeInputChannels      = 1
)

var (
	knownGoogleRealtimeGeminiModels = map[string]struct{}{
		"gemini-3.1-flash-live-preview":                 {},
		"gemini-2.5-flash-native-audio-preview-12-2025": {},
	}
	knownGoogleRealtimeVertexModels = map[string]struct{}{
		"gemini-live-2.5-flash-native-audio": {},
	}
)

type RealtimeModel struct {
	apiKey                    string
	instructions              string
	model                     string
	voice                     string
	language                  string
	vertexAI                  bool
	project                   string
	location                  string
	modalities                []string
	turnDetection             bool
	inputAudioTranscription   bool
	outputAudioTranscription  bool
	candidateCount            int
	maxOutputTokens           int
	maxOutputTokensSet        bool
	topP                      float64
	topPSet                   bool
	topK                      int
	topKSet                   bool
	presencePenalty           float64
	presencePenaltySet        bool
	frequencyPenalty          float64
	frequencyPenaltySet       bool
	temperature               float64
	temperatureSet            bool
	toolBehavior              any
	toolBehaviorSet           bool
	toolResponseScheduling    any
	toolResponseSchedulingSet bool
	connector                 googleRealtimeConnector
}

type GoogleRealtimeOption func(*googleRealtimeOptions)

type googleRealtimeOptions struct {
	model                     string
	instructions              string
	voice                     string
	voiceSet                  bool
	language                  string
	vertexAI                  *bool
	project                   string
	projectSet                bool
	location                  string
	locationSet               bool
	modalities                []string
	turnDetection             *bool
	inputAudioTranscription   *bool
	outputAudioTranscription  *bool
	candidateCount            int
	maxOutputTokens           int
	maxOutputTokensSet        bool
	topP                      float64
	topPSet                   bool
	topK                      int
	topKSet                   bool
	presencePenalty           float64
	presencePenaltySet        bool
	frequencyPenalty          float64
	frequencyPenaltySet       bool
	temperature               float64
	temperatureSet            bool
	toolBehavior              any
	toolBehaviorSet           bool
	toolResponseScheduling    any
	toolResponseSchedulingSet bool
	connector                 googleRealtimeConnector
}

type googleRealtimeConnector interface {
	Connect(context.Context, string, *genai.LiveConnectConfig) (googleRealtimeLiveSession, error)
}

type googleRealtimeLiveSession interface {
	SendRealtimeInput(genai.LiveRealtimeInput) error
	SendClientContent(genai.LiveClientContentInput) error
	Receive() (*genai.LiveServerMessage, error)
	Close() error
}

func WithGoogleRealtimeModel(model string) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		if model != "" {
			options.model = model
		}
	}
}

func WithGoogleRealtimeInstructions(instructions string) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.instructions = instructions
	}
}

func WithGoogleRealtimeVoice(voice string) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.voice = voice
		options.voiceSet = true
	}
}

func WithGoogleRealtimeLanguage(language string) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.language = language
	}
}

func WithGoogleRealtimeVertexAI(enabled bool) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.vertexAI = &enabled
	}
}

func WithGoogleRealtimeProject(project string) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.project = project
		options.projectSet = true
	}
}

func WithGoogleRealtimeLocation(location string) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.location = location
		options.locationSet = true
	}
}

func WithGoogleRealtimeModalities(modalities []string) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.modalities = append([]string(nil), modalities...)
	}
}

func WithGoogleRealtimeTurnDetection(enabled bool) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.turnDetection = &enabled
	}
}

func WithGoogleRealtimeInputAudioTranscription(enabled bool) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.inputAudioTranscription = &enabled
	}
}

func WithGoogleRealtimeOutputAudioTranscription(enabled bool) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.outputAudioTranscription = &enabled
	}
}

func WithGoogleRealtimeCandidateCount(count int) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.candidateCount = count
	}
}

func WithGoogleRealtimeMaxOutputTokens(tokens int) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.maxOutputTokens = tokens
		options.maxOutputTokensSet = true
	}
}

func WithGoogleRealtimeTopP(topP float64) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.topP = topP
		options.topPSet = true
	}
}

func WithGoogleRealtimeTopK(topK int) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.topK = topK
		options.topKSet = true
	}
}

func WithGoogleRealtimePresencePenalty(penalty float64) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.presencePenalty = penalty
		options.presencePenaltySet = true
	}
}

func WithGoogleRealtimeFrequencyPenalty(penalty float64) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.frequencyPenalty = penalty
		options.frequencyPenaltySet = true
	}
}

func WithGoogleRealtimeTemperature(temperature float64) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.temperature = temperature
		options.temperatureSet = true
	}
}

func WithGoogleRealtimeToolBehavior(behavior any) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.toolBehavior = behavior
		options.toolBehaviorSet = true
	}
}

func WithGoogleRealtimeToolResponseScheduling(scheduling any) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.toolResponseScheduling = scheduling
		options.toolResponseSchedulingSet = true
	}
}

func WithGoogleRealtimeConnector(connector googleRealtimeConnector) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.connector = connector
	}
}

func NewRealtimeModel(apiKey string, opts ...GoogleRealtimeOption) (*RealtimeModel, error) {
	options := googleRealtimeOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	vertexAI := googleRealtimeDefaultVertexAI()
	if options.vertexAI != nil {
		vertexAI = *options.vertexAI
	}
	modelName := options.model
	if modelName == "" {
		if vertexAI {
			modelName = defaultGoogleRealtimeVertexModel
		} else {
			modelName = defaultGoogleRealtimeGeminiModel
		}
	}
	if err := validateGoogleRealtimeModelAPI(modelName, vertexAI); err != nil {
		return nil, err
	}
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	project := options.project
	if !options.projectSet {
		project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	location := options.location
	if !options.locationSet {
		location = os.Getenv("GOOGLE_CLOUD_LOCATION")
	}
	if vertexAI {
		if location == "" && !options.locationSet {
			location = defaultGoogleRealtimeLocation
		}
		if project == "" || location == "" {
			return nil, errors.New("Project is required for VertexAI via project option or GOOGLE_CLOUD_PROJECT environment variable")
		}
		apiKey = ""
	} else {
		project = ""
		location = ""
		if apiKey == "" {
			return nil, errors.New("API key is required for Google API either via api_key or GOOGLE_API_KEY environment variable")
		}
	}
	voice := options.voice
	if !options.voiceSet {
		voice = defaultGoogleRealtimeVoice
	}
	modalities := options.modalities
	if len(modalities) == 0 {
		modalities = []string{"AUDIO"}
	} else {
		modalities = append([]string(nil), modalities...)
	}
	turnDetection := true
	if options.turnDetection != nil {
		turnDetection = *options.turnDetection
	}
	inputAudioTranscription := true
	if options.inputAudioTranscription != nil {
		inputAudioTranscription = *options.inputAudioTranscription
	}
	outputAudioTranscription := true
	if options.outputAudioTranscription != nil {
		outputAudioTranscription = *options.outputAudioTranscription
	}
	candidateCount := options.candidateCount
	if candidateCount == 0 {
		candidateCount = 1
	}
	return &RealtimeModel{
		apiKey:                    apiKey,
		instructions:              options.instructions,
		model:                     modelName,
		voice:                     voice,
		language:                  options.language,
		vertexAI:                  vertexAI,
		project:                   project,
		location:                  location,
		modalities:                modalities,
		turnDetection:             turnDetection,
		inputAudioTranscription:   inputAudioTranscription,
		outputAudioTranscription:  outputAudioTranscription,
		candidateCount:            candidateCount,
		maxOutputTokens:           options.maxOutputTokens,
		maxOutputTokensSet:        options.maxOutputTokensSet,
		topP:                      options.topP,
		topPSet:                   options.topPSet,
		topK:                      options.topK,
		topKSet:                   options.topKSet,
		presencePenalty:           options.presencePenalty,
		presencePenaltySet:        options.presencePenaltySet,
		frequencyPenalty:          options.frequencyPenalty,
		frequencyPenaltySet:       options.frequencyPenaltySet,
		temperature:               options.temperature,
		temperatureSet:            options.temperatureSet,
		toolBehavior:              options.toolBehavior,
		toolBehaviorSet:           options.toolBehaviorSet,
		toolResponseScheduling:    options.toolResponseScheduling,
		toolResponseSchedulingSet: options.toolResponseSchedulingSet,
		connector:                 options.connector,
	}, nil
}

func (m *RealtimeModel) UpdateOptions(opts ...GoogleRealtimeOption) {
	if m == nil {
		return
	}
	options := googleRealtimeOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	if options.voiceSet {
		m.voice = options.voice
	}
	if options.temperatureSet {
		m.temperature = options.temperature
		m.temperatureSet = true
	}
	if options.toolBehaviorSet {
		m.toolBehavior = options.toolBehavior
		m.toolBehaviorSet = true
	}
	if options.toolResponseSchedulingSet {
		m.toolResponseScheduling = options.toolResponseScheduling
		m.toolResponseSchedulingSet = true
	}
}

func googleRealtimeDefaultVertexAI() bool {
	value := strings.ToLower(os.Getenv("GOOGLE_GENAI_USE_VERTEXAI"))
	return value == "true" || value == "1"
}

func validateGoogleRealtimeModelAPI(model string, vertexAI bool) error {
	if vertexAI {
		if _, ok := knownGoogleRealtimeGeminiModels[model]; ok {
			return fmt.Errorf("Model %q is a Gemini API model, but vertexai=True", model)
		}
		return nil
	}
	if _, ok := knownGoogleRealtimeVertexModels[model]; ok {
		return fmt.Errorf("Model %q is a VertexAI model, but vertexai=False", model)
	}
	return nil
}

func (m *RealtimeModel) Model() string {
	if m == nil || m.model == "" {
		return defaultGoogleRealtimeGeminiModel
	}
	return m.model
}

func (m *RealtimeModel) Provider() string {
	if m != nil && m.vertexAI {
		return "Vertex AI"
	}
	return "Gemini"
}

func (m *RealtimeModel) Capabilities() llm.RealtimeCapabilities {
	mutable := true
	if m != nil && strings.Contains(m.model, "3.1") {
		mutable = false
	}
	return llm.RealtimeCapabilities{
		MessageTruncation:       false,
		TurnDetection:           m == nil || m.turnDetection,
		UserTranscription:       m == nil || m.inputAudioTranscription,
		AutoToolReplyGeneration: true,
		AudioOutput:             m == nil || googleRealtimeHasAudioModality(m.modalities),
		ManualFunctionCalls:     false,
		MutableChatContext:      mutable,
		MutableInstructions:     mutable,
		MutableTools:            false,
		PerResponseToolChoice:   false,
	}
}

func googleRealtimeHasAudioModality(modalities []string) bool {
	for _, modality := range modalities {
		if strings.EqualFold(modality, "AUDIO") {
			return true
		}
	}
	return false
}

func (m *RealtimeModel) Session() (llm.RealtimeSession, error) {
	if m == nil {
		return nil, errors.New("google realtime model is nil")
	}
	connector := m.connector
	if connector == nil {
		connector = googleRealtimeDefaultConnector{model: m}
	}
	ctx, cancel := context.WithCancel(context.Background())
	liveSession, err := connector.Connect(ctx, m.Model(), m.liveConnectConfig())
	if err != nil {
		cancel()
		return nil, err
	}
	session := &googleRealtimeSession{
		ctx:         ctx,
		cancel:      cancel,
		liveSession: liveSession,
		eventCh:     make(chan llm.RealtimeEvent),
		audioStream: audio.NewAudioByteStream(googleRealtimeInputSampleRate, googleRealtimeInputChannels, googleRealtimeInputSampleRate/20),
	}
	go session.receiveLoop()
	return session, nil
}

func (m *RealtimeModel) Close() error { return nil }

func (m *RealtimeModel) liveConnectConfig() *genai.LiveConnectConfig {
	config := &genai.LiveConnectConfig{
		ResponseModalities: googleRealtimeModalities(m.modalities),
		SpeechConfig: &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: m.voice},
			},
			LanguageCode: m.language,
		},
	}
	if m.instructions != "" {
		config.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: m.instructions}}}
	}
	if m.inputAudioTranscription {
		config.InputAudioTranscription = &genai.AudioTranscriptionConfig{}
	}
	if m.outputAudioTranscription {
		config.OutputAudioTranscription = &genai.AudioTranscriptionConfig{}
	}
	if m.temperatureSet {
		value := float32(m.temperature)
		config.Temperature = &value
	}
	if m.topPSet {
		value := float32(m.topP)
		config.TopP = &value
	}
	if m.topKSet {
		value := float32(m.topK)
		config.TopK = &value
	}
	if m.maxOutputTokensSet {
		config.MaxOutputTokens = int32(m.maxOutputTokens)
	}
	return config
}

func googleRealtimeModalities(modalities []string) []genai.Modality {
	out := make([]genai.Modality, 0, len(modalities))
	for _, modality := range modalities {
		switch strings.ToUpper(modality) {
		case "AUDIO":
			out = append(out, genai.ModalityAudio)
		case "TEXT":
			out = append(out, genai.ModalityText)
		}
	}
	return out
}

type googleRealtimeDefaultConnector struct {
	model *RealtimeModel
}

func (c googleRealtimeDefaultConnector) Connect(ctx context.Context, modelName string, config *genai.LiveConnectConfig) (googleRealtimeLiveSession, error) {
	clientConfig := &genai.ClientConfig{
		APIKey: c.model.apiKey,
		HTTPOptions: genai.HTTPOptions{
			APIVersion: "v1beta",
		},
	}
	if c.model.vertexAI {
		clientConfig.Backend = genai.BackendVertexAI
		clientConfig.Project = c.model.project
		clientConfig.Location = c.model.location
	} else {
		clientConfig.Backend = genai.BackendGeminiAPI
	}
	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, err
	}
	return client.Live.Connect(ctx, modelName, config)
}

type googleRealtimeSession struct {
	ctx         context.Context
	cancel      context.CancelFunc
	liveSession googleRealtimeLiveSession
	eventCh     chan llm.RealtimeEvent
	audioStream *audio.AudioByteStream
	closeOnce   sync.Once
	closeErr    error
	closed      bool
	mu          sync.Mutex
}

func (s *googleRealtimeSession) UpdateInstructions(string) error {
	return errors.New("google realtime session instruction update is not implemented")
}
func (s *googleRealtimeSession) UpdateChatContext(*llm.ChatContext) error {
	return errors.New("google realtime session chat context update is not implemented")
}
func (s *googleRealtimeSession) UpdateTools([]llm.Tool) error {
	return errors.New("google realtime session tool update is not implemented")
}
func (s *googleRealtimeSession) UpdateOptions(llm.RealtimeSessionOptions) error {
	return errors.New("google realtime session option update is not implemented")
}
func (s *googleRealtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	if s == nil || s.liveSession == nil || s.isClosed() {
		return nil
	}
	turns := make([]*genai.Content, 0, 2)
	if options.Instructions != "" {
		turns = append(turns, &genai.Content{
			Role:  "model",
			Parts: []*genai.Part{{Text: options.Instructions}},
		})
	}
	turns = append(turns, &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: "."}},
	})
	turnComplete := true
	return s.liveSession.SendClientContent(genai.LiveClientContentInput{
		Turns:        turns,
		TurnComplete: &turnComplete,
	})
}
func (s *googleRealtimeSession) Say(text string) error {
	if s == nil || s.liveSession == nil || text == "" || s.isClosed() {
		return nil
	}
	return s.liveSession.SendRealtimeInput(genai.LiveRealtimeInput{Text: text})
}
func (s *googleRealtimeSession) Truncate(llm.RealtimeTruncateOptions) error { return nil }
func (s *googleRealtimeSession) Interrupt() error {
	if s == nil || s.liveSession == nil || s.isClosed() {
		return nil
	}
	return s.liveSession.SendRealtimeInput(genai.LiveRealtimeInput{
		ActivityStart: &genai.ActivityStart{},
	})
}
func (s *googleRealtimeSession) EventCh() <-chan llm.RealtimeEvent { return s.eventCh }

func (s *googleRealtimeSession) receiveLoop() {
	for {
		if s == nil || s.liveSession == nil || s.isClosed() {
			return
		}
		message, err := s.liveSession.Receive()
		if err != nil {
			return
		}
		s.handleServerMessage(message)
	}
}

func (s *googleRealtimeSession) handleServerMessage(message *genai.LiveServerMessage) {
	if message == nil || message.ServerContent == nil {
		return
	}
	if message.ServerContent.ModelTurn != nil {
		for _, part := range message.ServerContent.ModelTurn.Parts {
			if part == nil || part.Thought {
				continue
			}
			if part.Text != "" {
				s.emitEvent(llm.RealtimeEvent{
					Type: llm.RealtimeEventTypeText,
					Text: part.Text,
				})
			}
			if part.InlineData != nil && len(part.InlineData.Data) > 0 {
				s.emitEvent(llm.RealtimeEvent{
					Type: llm.RealtimeEventTypeAudio,
					Data: part.InlineData.Data,
				})
			}
		}
	}
	if message.ServerContent.OutputTranscription != nil && message.ServerContent.OutputTranscription.Text != "" {
		s.emitEvent(llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeText,
			Text: message.ServerContent.OutputTranscription.Text,
		})
	}
}

func (s *googleRealtimeSession) emitEvent(event llm.RealtimeEvent) {
	if s == nil || s.isClosed() {
		return
	}
	select {
	case s.eventCh <- event:
	case <-s.ctx.Done():
	}
}

func (s *googleRealtimeSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		if s.cancel != nil {
			s.cancel()
		}
		close(s.eventCh)
		if s.liveSession != nil {
			s.closeErr = s.liveSession.Close()
		}
	})
	return s.closeErr
}

func (s *googleRealtimeSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *googleRealtimeSession) PushAudio(frame *model.AudioFrame) error {
	if s == nil || s.liveSession == nil || frame == nil || len(frame.Data) == 0 || s.isClosed() {
		return nil
	}
	resampled, err := audio.ResampleAudioFrame(frame, googleRealtimeInputSampleRate)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chunk := range s.audioStream.Write(resampled.Data) {
		if err := s.liveSession.SendRealtimeInput(genai.LiveRealtimeInput{
			Audio: &genai.Blob{
				Data:     chunk.Data,
				MIMEType: "audio/pcm;rate=16000",
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *googleRealtimeSession) PushVideo(*images.VideoFrame) error {
	return errors.New("google realtime session video input is not implemented")
}
func (s *googleRealtimeSession) CommitAudio() error { return nil }
func (s *googleRealtimeSession) ClearAudio() error  { return nil }

var _ llm.RealtimeModel = (*RealtimeModel)(nil)
var _ llm.RealtimeSession = (*googleRealtimeSession)(nil)
