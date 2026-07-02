package google

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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
	googleRealtimeOutputSampleRate   = 24000
	googleRealtimeOutputChannels     = 1
)

var googleRealtimeGenerateReplyTimeout = 5 * time.Second

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
	mu                        sync.Mutex
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
	proactivity               bool
	proactivitySet            bool
	affectiveDialog           bool
	affectiveDialogSet        bool
	contextWindowCompression  *genai.ContextWindowCompressionConfig
	thinkingConfig            *genai.ThinkingConfig
	mediaResolution           genai.MediaResolution
	httpOptions               *genai.HTTPOptions
	connectOptions            llm.APIConnectOptions
	realtimeInputConfig       *genai.RealtimeInputConfig
	sessionResumptionHandle   string
	apiVersion                string
	connector                 googleRealtimeConnector
	sessions                  map[*googleRealtimeSession]struct{}
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
	proactivity               bool
	proactivitySet            bool
	affectiveDialog           bool
	affectiveDialogSet        bool
	contextWindowCompression  *genai.ContextWindowCompressionConfig
	thinkingConfig            *genai.ThinkingConfig
	mediaResolution           genai.MediaResolution
	httpOptions               *genai.HTTPOptions
	connectOptions            *llm.APIConnectOptions
	realtimeInputConfig       *genai.RealtimeInputConfig
	sessionResumptionHandle   string
	connector                 googleRealtimeConnector
}

type googleRealtimeConnector interface {
	Connect(context.Context, string, *genai.LiveConnectConfig) (googleRealtimeLiveSession, error)
}

type googleRealtimeLiveSession interface {
	SendRealtimeInput(genai.LiveRealtimeInput) error
	SendClientContent(genai.LiveClientContentInput) error
	SendToolResponse(genai.LiveToolResponseInput) error
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

func WithGoogleRealtimeProactivity(enabled bool) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.proactivity = enabled
		options.proactivitySet = true
	}
}

func WithGoogleRealtimeAffectiveDialog(enabled bool) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.affectiveDialog = enabled
		options.affectiveDialogSet = true
	}
}

func WithGoogleRealtimeContextWindowCompression(config *genai.ContextWindowCompressionConfig) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.contextWindowCompression = config
	}
}

func WithGoogleRealtimeThinkingConfig(config *genai.ThinkingConfig) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.thinkingConfig = config
	}
}

func WithGoogleRealtimeMediaResolution(resolution genai.MediaResolution) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.mediaResolution = resolution
	}
}

func WithGoogleRealtimeHTTPOptions(httpOptions *genai.HTTPOptions) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.httpOptions = httpOptions
	}
}

func WithGoogleRealtimeConnectOptions(connectOptions llm.APIConnectOptions) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.connectOptions = &connectOptions
	}
}

func WithGoogleRealtimeInputConfig(config *genai.RealtimeInputConfig) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.realtimeInputConfig = config
	}
}

func WithGoogleRealtimeSessionResumptionHandle(handle string) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.sessionResumptionHandle = handle
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
	if googleRealtimeManualActivityDetection(options.realtimeInputConfig) {
		turnDetection = false
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
	connectOptions := llm.DefaultAPIConnectOptions()
	if options.connectOptions != nil {
		connectOptions = *options.connectOptions
	}
	if err := connectOptions.Validate(); err != nil {
		return nil, err
	}
	apiVersion := "v1beta"
	if !vertexAI && ((options.proactivitySet && options.proactivity) || (options.affectiveDialogSet && options.affectiveDialog)) {
		apiVersion = "v1alpha"
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
		proactivity:               options.proactivity,
		proactivitySet:            options.proactivitySet,
		affectiveDialog:           options.affectiveDialog,
		affectiveDialogSet:        options.affectiveDialogSet,
		contextWindowCompression:  options.contextWindowCompression,
		thinkingConfig:            options.thinkingConfig,
		mediaResolution:           options.mediaResolution,
		httpOptions:               options.httpOptions,
		connectOptions:            connectOptions,
		realtimeInputConfig:       options.realtimeInputConfig,
		sessionResumptionHandle:   options.sessionResumptionHandle,
		apiVersion:                apiVersion,
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
	sessions := make(map[*googleRealtimeSession]struct{})
	m.mu.Lock()
	if options.voiceSet {
		m.voice = options.voice
		for session := range m.sessions {
			sessions[session] = struct{}{}
		}
	}
	if options.temperatureSet {
		m.temperature = options.temperature
		m.temperatureSet = true
		for session := range m.sessions {
			sessions[session] = struct{}{}
		}
	}
	if options.toolBehaviorSet {
		m.toolBehavior = options.toolBehavior
		m.toolBehaviorSet = true
		for session := range m.sessions {
			sessions[session] = struct{}{}
		}
	}
	if options.toolResponseSchedulingSet {
		m.toolResponseScheduling = options.toolResponseScheduling
		m.toolResponseSchedulingSet = true
		for session := range m.sessions {
			sessions[session] = struct{}{}
		}
	}
	m.mu.Unlock()
	for session := range sessions {
		if options.voiceSet || options.temperatureSet || options.toolBehaviorSet {
			_ = session.reconnectWithModelOptions(googleRealtimeReconnectOptions{
				voice:           options.voice,
				voiceSet:        options.voiceSet,
				temperature:     options.temperature,
				temperatureSet:  options.temperatureSet,
				toolBehavior:    options.toolBehavior,
				toolBehaviorSet: options.toolBehaviorSet,
			})
		}
		if options.toolResponseSchedulingSet {
			session.updateToolResponseScheduling(options.toolResponseScheduling)
		}
	}
}

type googleRealtimeReconnectOptions struct {
	voice           string
	voiceSet        bool
	temperature     float64
	temperatureSet  bool
	toolBehavior    any
	toolBehaviorSet bool
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
	config := m.liveConnectConfig()
	liveSession, err := googleRealtimeConnectWithRetry(ctx, connector, m.Model(), config, m.connectOptions)
	if err != nil {
		cancel()
		return nil, err
	}
	capabilities := m.Capabilities()
	session := &googleRealtimeSession{
		owner:                   m,
		ctx:                     ctx,
		cancel:                  cancel,
		connector:               connector,
		liveSession:             liveSession,
		liveConfig:              config,
		modelName:               m.Model(),
		provider:                m.Provider(),
		instructions:            m.instructions,
		voice:                   m.voice,
		temperature:             m.temperature,
		temperatureSet:          m.temperatureSet,
		vertexAI:                m.vertexAI,
		audioOutput:             capabilities.AudioOutput,
		mutableInstructions:     capabilities.MutableInstructions,
		mutableChatContext:      capabilities.MutableChatContext,
		chatCtx:                 llm.EmptyChatContext(),
		toolBehavior:            m.toolBehavior,
		toolResponseScheduling:  m.toolResponseScheduling,
		eventCh:                 make(chan llm.RealtimeEvent, 16),
		audioStream:             audio.NewAudioByteStream(googleRealtimeInputSampleRate, googleRealtimeInputChannels, googleRealtimeInputSampleRate/20),
		sessionResumptionHandle: m.sessionResumptionHandle,
		manualActivityDetection: googleRealtimeManualActivityDetection(m.realtimeInputConfig) || !m.turnDetection,
		suppressActivityStart:   googleRealtimeNoInterruption(m.realtimeInputConfig),
	}
	m.registerSession(session)
	go session.receiveLoop(liveSession)
	return session, nil
}

func googleRealtimeConnectWithRetry(ctx context.Context, connector googleRealtimeConnector, modelName string, config *genai.LiveConnectConfig, options llm.APIConnectOptions) (googleRealtimeLiveSession, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		liveSession, err := connector.Connect(ctx, modelName, config)
		if err == nil {
			return liveSession, nil
		}
		lastErr = err
		if attempt >= options.MaxRetry {
			return nil, lastErr
		}
		interval := options.IntervalForRetry(attempt)
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
}

func (m *RealtimeModel) registerSession(session *googleRealtimeSession) {
	if m == nil || session == nil {
		return
	}
	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = make(map[*googleRealtimeSession]struct{})
	}
	m.sessions[session] = struct{}{}
	m.mu.Unlock()
}

func (m *RealtimeModel) unregisterSession(session *googleRealtimeSession) {
	if m == nil || session == nil {
		return
	}
	m.mu.Lock()
	delete(m.sessions, session)
	m.mu.Unlock()
}

func (m *RealtimeModel) Close() error { return nil }

func (m *RealtimeModel) liveConnectConfig() *genai.LiveConnectConfig {
	config := &genai.LiveConnectConfig{
		HTTPOptions:        googleRealtimeHTTPOptions(m.httpOptions, m.apiVersion, m.connectOptions),
		ResponseModalities: googleRealtimeModalities(m.modalities),
		SpeechConfig: &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: m.voice},
			},
			LanguageCode: m.language,
		},
		SessionResumption: &genai.SessionResumptionConfig{Handle: m.sessionResumptionHandle},
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
	if m.proactivitySet {
		value := m.proactivity
		config.Proactivity = &genai.ProactivityConfig{ProactiveAudio: &value}
	}
	if m.affectiveDialogSet {
		value := m.affectiveDialog
		config.EnableAffectiveDialog = &value
	}
	if m.contextWindowCompression != nil {
		config.ContextWindowCompression = m.contextWindowCompression
	}
	if m.realtimeInputConfig != nil {
		config.RealtimeInputConfig = m.realtimeInputConfig
	} else if !m.turnDetection {
		config.RealtimeInputConfig = &genai.RealtimeInputConfig{
			AutomaticActivityDetection: &genai.AutomaticActivityDetection{Disabled: true},
		}
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
	if m.thinkingConfig != nil {
		config.ThinkingConfig = m.thinkingConfig
	}
	if m.mediaResolution != "" {
		config.MediaResolution = m.mediaResolution
	}
	return config
}

func googleRealtimeHTTPOptions(options *genai.HTTPOptions, apiVersion string, connectOptions llm.APIConnectOptions) *genai.HTTPOptions {
	var clone genai.HTTPOptions
	if options != nil {
		clone = *options
		if options.Timeout != nil {
			timeout := *options.Timeout
			clone.Timeout = &timeout
		}
		if options.Headers != nil {
			clone.Headers = make(http.Header, len(options.Headers)+1)
			for key, values := range options.Headers {
				for _, value := range values {
					clone.Headers.Add(key, value)
				}
			}
		}
		if options.ExtraBody != nil {
			clone.ExtraBody = make(map[string]any, len(options.ExtraBody))
			for key, value := range options.ExtraBody {
				clone.ExtraBody[key] = value
			}
		}
	}
	if clone.APIVersion == "" {
		clone.APIVersion = apiVersion
	}
	if clone.APIVersion == "" {
		clone.APIVersion = "v1beta"
	}
	if clone.Timeout == nil {
		timeout := connectOptions.Timeout
		clone.Timeout = &timeout
	}
	if clone.Headers == nil {
		clone.Headers = http.Header{}
	}
	for key := range clone.Headers {
		if strings.EqualFold(key, "x-goog-api-client") {
			delete(clone.Headers, key)
		}
	}
	clone.Headers.Set("x-goog-api-client", "livekit-agents/"+PluginVersion)
	return &clone
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

func googleRealtimeChatContextTurns(chatCtx *llm.ChatContext) ([]*genai.Content, error) {
	injectDummy := false
	contentCtx := chatCtx.Copy(llm.ChatContextCopyOptions{ExcludeFunctionCall: true})
	turnsMap, _ := contentCtx.ToProviderFormat("google", llm.ChatContextProviderFormatOptions{
		InjectDummyUserMessage: &injectDummy,
	})
	turns := make([]*genai.Content, 0, len(turnsMap))
	for _, turnMap := range turnsMap {
		data, err := json.Marshal(turnMap)
		if err != nil {
			return nil, err
		}
		var turn genai.Content
		if err := json.Unmarshal(data, &turn); err != nil {
			return nil, err
		}
		if len(turn.Parts) > 0 {
			turns = append(turns, &turn)
		}
	}
	return turns, nil
}

func googleRealtimeToolResponses(chatCtx *llm.ChatContext, vertexAI bool, scheduling any) []*genai.FunctionResponse {
	responses := make([]*genai.FunctionResponse, 0)
	for _, item := range chatCtx.Items {
		output, ok := item.(*llm.FunctionCallOutput)
		if !ok {
			continue
		}
		response := &genai.FunctionResponse{
			Name:     output.Name,
			Response: map[string]any{"output": output.Output},
		}
		if !vertexAI {
			response.ID = output.CallID
			switch value := scheduling.(type) {
			case genai.FunctionResponseScheduling:
				response.Scheduling = value
			case string:
				response.Scheduling = genai.FunctionResponseScheduling(value)
			}
		}
		responses = append(responses, response)
	}
	return responses
}

type googleRealtimeDefaultConnector struct {
	model *RealtimeModel
}

func (c googleRealtimeDefaultConnector) Connect(ctx context.Context, modelName string, config *genai.LiveConnectConfig) (googleRealtimeLiveSession, error) {
	apiVersion := "v1beta"
	if c.model != nil && c.model.apiVersion != "" {
		apiVersion = c.model.apiVersion
	}
	httpOptions := googleRealtimeHTTPOptions(c.model.httpOptions, apiVersion, c.model.connectOptions)
	clientConfig := &genai.ClientConfig{
		APIKey:      c.model.apiKey,
		HTTPOptions: *httpOptions,
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
	owner                   *RealtimeModel
	ctx                     context.Context
	cancel                  context.CancelFunc
	connector               googleRealtimeConnector
	liveSession             googleRealtimeLiveSession
	liveConfig              *genai.LiveConnectConfig
	modelName               string
	provider                string
	instructions            string
	voice                   string
	temperature             float64
	temperatureSet          bool
	vertexAI                bool
	audioOutput             bool
	mutableInstructions     bool
	mutableChatContext      bool
	chatCtx                 *llm.ChatContext
	tools                   []llm.Tool
	toolBehavior            any
	toolResponseScheduling  any
	eventCh                 chan llm.RealtimeEvent
	audioStream             *audio.AudioByteStream
	generation              *googleRealtimeGeneration
	responseSeq             int
	functionSeq             int
	pendingReply            bool
	pendingReplyAt          time.Time
	sessionResumptionHandle string
	inputID                 string
	inputSeq                int
	inputText               string
	manualActivityDetection bool
	inUserActivity          bool
	suppressActivityStart   bool
	closeOnce               sync.Once
	closed                  bool
	mu                      sync.Mutex
}

type googleRealtimeGeneration struct {
	responseID    string
	messageCh     chan llm.MessageGeneration
	functionCh    chan *llm.FunctionCall
	textCh        chan string
	audioCh       chan *model.AudioFrame
	modalitiesCh  chan []string
	outputText    string
	audioChClosed bool
	createdAt     time.Time
	firstTokenAt  time.Time
	completedAt   time.Time
	closed        bool
}

func (s *googleRealtimeSession) UpdateInstructions(instructions string) error {
	if s == nil {
		return nil
	}
	if s.instructions == instructions {
		return nil
	}
	s.instructions = instructions
	googleRealtimeSetConfigInstructions(s.liveConfig, instructions)
	if !s.mutableInstructions || s.liveSession == nil || s.isClosed() {
		return nil
	}
	role := ""
	if s.vertexAI {
		role = "model"
	}
	turnComplete := false
	return s.liveSession.SendClientContent(genai.LiveClientContentInput{
		Turns: []*genai.Content{{
			Role:  role,
			Parts: []*genai.Part{{Text: instructions}},
		}},
		TurnComplete: &turnComplete,
	})
}
func (s *googleRealtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	if s == nil || chatCtx == nil {
		return nil
	}
	nextCtx := chatCtx.Copy(llm.ChatContextCopyOptions{
		ExcludeHandoff:      true,
		ExcludeInstructions: true,
		ExcludeEmptyMessage: true,
		ExcludeConfigUpdate: true,
	})
	if s.chatCtx == nil {
		s.chatCtx = llm.EmptyChatContext()
	}
	diffOps := llm.ComputeChatCtxDiff(s.chatCtx, nextCtx)
	appendCtx := llm.EmptyChatContext()
	for _, create := range diffOps.ToCreate {
		itemID := create[1]
		if itemID == nil {
			continue
		}
		if item := nextCtx.GetByID(*itemID); item != nil {
			appendCtx.Items = append(appendCtx.Items, item)
		}
	}
	s.chatCtx = nextCtx
	if len(appendCtx.Items) == 0 || s.liveSession == nil || s.isClosed() {
		return nil
	}
	if s.mutableChatContext {
		turns, err := googleRealtimeChatContextTurns(appendCtx)
		if err != nil {
			return err
		}
		if len(turns) > 0 {
			turnComplete := false
			if err := s.liveSession.SendClientContent(genai.LiveClientContentInput{
				Turns:        turns,
				TurnComplete: &turnComplete,
			}); err != nil {
				return err
			}
		}
	}
	responses := googleRealtimeToolResponses(appendCtx, s.vertexAI, s.currentToolResponseScheduling())
	if len(responses) == 0 {
		return nil
	}
	return s.liveSession.SendToolResponse(genai.LiveToolResponseInput{FunctionResponses: responses})
}
func (s *googleRealtimeSession) UpdateTools(tools []llm.Tool) error {
	return s.reconnectWithTools(tools)
}
func (s *googleRealtimeSession) UpdateOptions(options llm.RealtimeSessionOptions) error {
	if googleRealtimeSessionOptionsNoop(options) {
		return nil
	}
	if options.VoiceSet &&
		!options.SpeedSet &&
		!options.MaxResponseOutputTokensSet &&
		!options.TruncationSet &&
		!options.TracingSet &&
		!options.ReasoningSet &&
		!options.TurnDetectionSet &&
		!options.InputAudioTranscriptionSet &&
		!options.InputAudioNoiseReductionSet {
		return s.reconnectWithVoice(options.Voice)
	}
	return errors.New("google realtime session option update is not implemented")
}

func googleRealtimeSessionOptionsNoop(options llm.RealtimeSessionOptions) bool {
	return !options.VoiceSet &&
		!options.SpeedSet &&
		!options.MaxResponseOutputTokensSet &&
		!options.TruncationSet &&
		!options.TracingSet &&
		!options.ReasoningSet &&
		!options.TurnDetectionSet &&
		!options.InputAudioTranscriptionSet &&
		!options.InputAudioNoiseReductionSet
}

func (s *googleRealtimeSession) reconnectWithVoice(voice string) error {
	if s == nil || s.isClosed() {
		return nil
	}
	s.mu.Lock()
	if s.voice == voice {
		s.mu.Unlock()
		return nil
	}
	connector := s.connector
	modelName := s.modelName
	config := googleRealtimeCloneLiveConfig(s.liveConfig)
	oldSession := s.liveSession
	s.mu.Unlock()
	if connector == nil || config == nil {
		return errors.New("google realtime session option update is not implemented")
	}
	googleRealtimeSetConfigVoice(config, voice)
	if oldSession != nil {
		_ = oldSession.Close()
	}
	s.closeGeneration()
	nextSession, err := connector.Connect(s.ctx, modelName, config)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = nextSession.Close()
		return nil
	}
	s.liveSession = nextSession
	s.liveConfig = config
	s.voice = voice
	s.mu.Unlock()
	go s.receiveLoop(nextSession)
	return nil
}

func (s *googleRealtimeSession) reconnectWithModelOptions(options googleRealtimeReconnectOptions) error {
	if s == nil || s.isClosed() {
		return nil
	}
	s.mu.Lock()
	changed := false
	if options.voiceSet && s.voice != options.voice {
		changed = true
	}
	if options.temperatureSet && (!s.temperatureSet || s.temperature != options.temperature) {
		changed = true
	}
	if options.toolBehaviorSet && googleRealtimeToolBehavior(s.toolBehavior) != googleRealtimeToolBehavior(options.toolBehavior) {
		changed = true
	}
	if !changed {
		s.mu.Unlock()
		return nil
	}
	connector := s.connector
	modelName := s.modelName
	config := googleRealtimeCloneLiveConfig(s.liveConfig)
	oldSession := s.liveSession
	tools := append([]llm.Tool(nil), s.tools...)
	s.mu.Unlock()
	if connector == nil || config == nil {
		return errors.New("google realtime session option update is not implemented")
	}
	if options.voiceSet {
		googleRealtimeSetConfigVoice(config, options.voice)
	}
	if options.temperatureSet {
		googleRealtimeSetConfigTemperature(config, options.temperature)
	}
	if options.toolBehaviorSet {
		config.Tools = googleRealtimeToolsConfig(tools, options.toolBehavior)
	}
	if oldSession != nil {
		_ = oldSession.Close()
	}
	s.closeGeneration()
	nextSession, err := connector.Connect(s.ctx, modelName, config)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = nextSession.Close()
		return nil
	}
	s.liveSession = nextSession
	s.liveConfig = config
	if options.voiceSet {
		s.voice = options.voice
	}
	if options.temperatureSet {
		s.temperature = options.temperature
		s.temperatureSet = true
	}
	if options.toolBehaviorSet {
		s.toolBehavior = options.toolBehavior
	}
	s.mu.Unlock()
	go s.receiveLoop(nextSession)
	return nil
}

func (s *googleRealtimeSession) reconnectWithTools(tools []llm.Tool) error {
	if s == nil || s.isClosed() {
		return nil
	}
	tools = append([]llm.Tool(nil), tools...)
	s.mu.Lock()
	if googleRealtimeSameTools(s.tools, tools) {
		s.mu.Unlock()
		return nil
	}
	connector := s.connector
	modelName := s.modelName
	config := googleRealtimeCloneLiveConfig(s.liveConfig)
	oldSession := s.liveSession
	behavior := s.toolBehavior
	s.mu.Unlock()
	if connector == nil || config == nil {
		return errors.New("google realtime session tool update is not implemented")
	}
	config.Tools = googleRealtimeToolsConfig(tools, behavior)
	if oldSession != nil {
		_ = oldSession.Close()
	}
	s.closeGeneration()
	nextSession, err := connector.Connect(s.ctx, modelName, config)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = nextSession.Close()
		return nil
	}
	s.liveSession = nextSession
	s.liveConfig = config
	s.tools = tools
	s.mu.Unlock()
	go s.receiveLoop(nextSession)
	return nil
}

func googleRealtimeSameTools(left, right []llm.Tool) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !googleRealtimeSameTool(left[i], right[i]) {
			return false
		}
	}
	return true
}

func googleRealtimeSameTool(left, right llm.Tool) bool {
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() {
		return !leftValue.IsValid() && !rightValue.IsValid()
	}
	if leftValue.Type() != rightValue.Type() {
		return false
	}
	if leftValue.Kind() != reflect.Ptr && leftValue.Kind() != reflect.UnsafePointer {
		return false
	}
	return leftValue.Pointer() == rightValue.Pointer()
}

func (s *googleRealtimeSession) updateToolResponseScheduling(scheduling any) {
	if s == nil || s.isClosed() {
		return
	}
	s.mu.Lock()
	s.toolResponseScheduling = scheduling
	s.mu.Unlock()
}

func (s *googleRealtimeSession) currentToolResponseScheduling() any {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolResponseScheduling
}

func googleRealtimeCloneLiveConfig(config *genai.LiveConnectConfig) *genai.LiveConnectConfig {
	if config == nil {
		return nil
	}
	clone := *config
	if config.SpeechConfig != nil {
		speechConfig := *config.SpeechConfig
		clone.SpeechConfig = &speechConfig
		if config.SpeechConfig.VoiceConfig != nil {
			voiceConfig := *config.SpeechConfig.VoiceConfig
			clone.SpeechConfig.VoiceConfig = &voiceConfig
			if config.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig != nil {
				prebuilt := *config.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig
				clone.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig = &prebuilt
			}
		}
	}
	return &clone
}

func googleRealtimeSetConfigVoice(config *genai.LiveConnectConfig, voice string) {
	if config.SpeechConfig == nil {
		config.SpeechConfig = &genai.SpeechConfig{}
	}
	if config.SpeechConfig.VoiceConfig == nil {
		config.SpeechConfig.VoiceConfig = &genai.VoiceConfig{}
	}
	config.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig = &genai.PrebuiltVoiceConfig{VoiceName: voice}
}

func googleRealtimeSetConfigTemperature(config *genai.LiveConnectConfig, temperature float64) {
	value := float32(temperature)
	config.Temperature = &value
}

func googleRealtimeSetConfigInstructions(config *genai.LiveConnectConfig, instructions string) {
	if config == nil {
		return
	}
	if instructions == "" {
		config.SystemInstruction = nil
		return
	}
	config.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: instructions}}}
}

func googleRealtimeSetConfigSessionResumption(config *genai.LiveConnectConfig, handle string) {
	if config == nil {
		return
	}
	config.SessionResumption = &genai.SessionResumptionConfig{Handle: handle}
}

func googleRealtimeToolsConfig(tools []llm.Tool, behavior any) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	declarations := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		declaration := buildGoogleFunctionDeclaration(tool)
		if behavior := googleRealtimeToolBehavior(behavior); behavior != "" {
			declaration.Behavior = behavior
		}
		declarations = append(declarations, declaration)
	}
	return []*genai.Tool{{FunctionDeclarations: declarations}}
}

func googleRealtimeToolBehavior(value any) genai.Behavior {
	switch typed := value.(type) {
	case genai.Behavior:
		return typed
	case string:
		return genai.Behavior(typed)
	default:
		return ""
	}
}

func (s *googleRealtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	if s == nil || s.liveSession == nil || s.isClosed() {
		return nil
	}
	if !s.mutableChatContext {
		return fmt.Errorf("generate_reply is not compatible with %q", s.modelName)
	}
	if err := s.endManualActivity(); err != nil {
		return err
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
	s.setPendingReply(true)
	if err := s.liveSession.SendClientContent(genai.LiveClientContentInput{
		Turns:        turns,
		TurnComplete: &turnComplete,
	}); err != nil {
		s.setPendingReply(false)
		return err
	}
	return nil
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
	s.mu.Lock()
	if s.suppressActivityStart || !s.manualActivityDetection || s.inUserActivity {
		s.mu.Unlock()
		return nil
	}
	s.inUserActivity = true
	s.mu.Unlock()
	return s.liveSession.SendRealtimeInput(genai.LiveRealtimeInput{
		ActivityStart: &genai.ActivityStart{},
	})
}

func googleRealtimeManualActivityDetection(config *genai.RealtimeInputConfig) bool {
	return config != nil && config.AutomaticActivityDetection != nil && config.AutomaticActivityDetection.Disabled
}

func googleRealtimeNoInterruption(config *genai.RealtimeInputConfig) bool {
	return config != nil && config.ActivityHandling == genai.ActivityHandlingNoInterruption
}

func (s *googleRealtimeSession) endManualActivity() error {
	s.mu.Lock()
	if !s.manualActivityDetection || !s.inUserActivity {
		s.mu.Unlock()
		return nil
	}
	s.inUserActivity = false
	s.mu.Unlock()
	return s.liveSession.SendRealtimeInput(genai.LiveRealtimeInput{
		ActivityEnd: &genai.ActivityEnd{},
	})
}
func (s *googleRealtimeSession) EventCh() <-chan llm.RealtimeEvent { return s.eventCh }

func (s *googleRealtimeSession) receiveLoop(liveSession googleRealtimeLiveSession) {
	defer func() {
		if s != nil && s.activeLiveSession() == liveSession {
			s.closeGeneration()
		}
	}()
	for {
		if s == nil || liveSession == nil || s.isClosed() || s.activeLiveSession() != liveSession {
			return
		}
		message, err := liveSession.Receive()
		if err != nil {
			if !errors.Is(err, context.Canceled) && s.activeLiveSession() == liveSession && !s.isClosed() {
				s.reconnectActiveSession(liveSession,
					fmt.Sprintf("google realtime receive failed: %v", err),
					"failed to reconnect Google realtime after receive error: %v",
				)
			}
			return
		}
		s.handleServerMessage(message)
	}
}

func (s *googleRealtimeSession) reconnectActiveSession(liveSession googleRealtimeLiveSession, unavailableMessage string, reconnectMessage string) {
	if s == nil || s.isClosed() {
		return
	}
	s.mu.Lock()
	if s.liveSession != liveSession {
		s.mu.Unlock()
		return
	}
	connector := s.connector
	modelName := s.modelName
	config := googleRealtimeCloneLiveConfig(s.liveConfig)
	connectOptions := llm.DefaultAPIConnectOptions()
	if s.owner != nil {
		connectOptions = s.owner.connectOptions
	}
	s.mu.Unlock()
	if connector == nil || config == nil {
		s.emitEvent(llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: llm.NewAPIConnectionError(unavailableMessage),
		})
		return
	}
	_ = liveSession.Close()
	s.closeGeneration()
	nextSession, err := googleRealtimeConnectWithRetry(s.ctx, connector, modelName, config, connectOptions)
	if err != nil {
		s.emitEvent(llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: llm.NewAPIConnectionError(fmt.Sprintf(reconnectMessage, err)),
		})
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = nextSession.Close()
		return
	}
	s.liveSession = nextSession
	s.liveConfig = config
	var chatCtx *llm.ChatContext
	if s.chatCtx != nil {
		chatCtx = s.chatCtx.Copy(llm.ChatContextCopyOptions{})
	}
	s.mu.Unlock()
	if err := s.replayChatContext(nextSession, chatCtx); err != nil {
		s.emitEvent(llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: llm.NewAPIConnectionError(fmt.Sprintf("failed to replay Google realtime chat context after reconnect: %v", err)),
		})
		_ = nextSession.Close()
		return
	}
	s.emitEvent(llm.RealtimeEvent{
		Type:      llm.RealtimeEventTypeSessionReconnected,
		Reconnect: &llm.RealtimeSessionReconnectedEvent{},
	})
	go s.receiveLoop(nextSession)
}

func (s *googleRealtimeSession) replayChatContext(liveSession googleRealtimeLiveSession, chatCtx *llm.ChatContext) error {
	if liveSession == nil || chatCtx == nil || len(chatCtx.Items) == 0 || !s.mutableChatContext {
		return nil
	}
	turns, err := googleRealtimeChatContextTurns(chatCtx)
	if err != nil {
		return err
	}
	if len(turns) == 0 {
		return nil
	}
	turnComplete := false
	return liveSession.SendClientContent(genai.LiveClientContentInput{
		Turns:        turns,
		TurnComplete: &turnComplete,
	})
}

func (s *googleRealtimeSession) activeLiveSession() googleRealtimeLiveSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.liveSession
}

func (s *googleRealtimeSession) handleServerMessage(message *genai.LiveServerMessage) {
	if message == nil {
		return
	}
	if update := message.SessionResumptionUpdate; update != nil && update.Resumable && update.NewHandle != "" {
		s.sessionResumptionHandle = update.NewHandle
		googleRealtimeSetConfigSessionResumption(s.liveConfig, update.NewHandle)
	}
	if message.ServerContent != nil {
		if s.isNewGenerationMessage(message) {
			s.ensureGeneration()
		}
		if message.ServerContent.ModelTurn != nil {
			for _, part := range message.ServerContent.ModelTurn.Parts {
				if part == nil || part.Thought {
					continue
				}
				if part.Text != "" {
					s.sendGenerationText(part.Text)
					s.emitEvent(llm.RealtimeEvent{
						Type: llm.RealtimeEventTypeText,
						Text: part.Text,
					})
				}
				if part.InlineData != nil && len(part.InlineData.Data) > 0 {
					s.sendGenerationAudio(part.InlineData.Data)
					s.emitEvent(llm.RealtimeEvent{
						Type: llm.RealtimeEventTypeAudio,
						Data: part.InlineData.Data,
					})
				}
			}
		}
		if message.ServerContent.InputTranscription != nil && message.ServerContent.InputTranscription.Text != "" {
			text := message.ServerContent.InputTranscription.Text
			if s.inputText == "" {
				text = strings.TrimLeft(text, " \t\r\n")
			}
			s.inputText += text
			s.emitInputTranscription(false)
		}
		if message.ServerContent.OutputTranscription != nil && message.ServerContent.OutputTranscription.Text != "" {
			s.sendGenerationText(message.ServerContent.OutputTranscription.Text)
			s.emitEvent(llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeText,
				Text: message.ServerContent.OutputTranscription.Text,
			})
		}
		if message.ServerContent.GenerationComplete || message.ServerContent.TurnComplete {
			s.markGenerationCompleted()
		}
		if message.ServerContent.Interrupted && !s.hasPendingReply() {
			s.emitEvent(llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted})
		}
		if message.ServerContent.TurnComplete {
			s.emitEvent(llm.RealtimeEvent{
				Type:          llm.RealtimeEventTypeSpeechStopped,
				SpeechStopped: &llm.InputSpeechStoppedEvent{UserTranscriptionEnabled: false},
			})
			if s.inputText != "" {
				s.emitInputTranscription(true)
			}
			s.commitCompletedTranscripts()
			s.closeGeneration()
		}
	}
	if message.ToolCall != nil {
		s.ensureGeneration()
		s.handleToolCalls(message.ToolCall)
		s.emitEvent(llm.RealtimeEvent{
			Type:          llm.RealtimeEventTypeSpeechStopped,
			SpeechStopped: &llm.InputSpeechStoppedEvent{UserTranscriptionEnabled: false},
		})
		s.closeGeneration()
	}
	if message.UsageMetadata != nil {
		s.emitUsageMetrics(message.UsageMetadata)
	}
	if message.GoAway != nil {
		s.reconnectActiveSession(s.activeLiveSession(),
			"google realtime received go_away",
			"failed to reconnect Google realtime after go_away: %v",
		)
	}
}

func (s *googleRealtimeSession) markGenerationCompleted() {
	if s.generation == nil || s.generation.closed || !s.generation.completedAt.IsZero() {
		return
	}
	s.generation.completedAt = time.Now()
}

func (s *googleRealtimeSession) handleToolCalls(toolCall *genai.LiveServerToolCall) {
	if s.generation == nil || s.generation.closed || toolCall == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	for _, functionCall := range toolCall.FunctionCalls {
		if functionCall == nil {
			continue
		}
		arguments, err := json.Marshal(functionCall.Args)
		if err != nil {
			arguments = []byte("{}")
		}
		callID := functionCall.ID
		if callID == "" {
			s.functionSeq++
			callID = fmt.Sprintf("fnc-call-%d", s.functionSeq)
		}
		select {
		case s.generation.functionCh <- &llm.FunctionCall{
			CallID:    callID,
			Name:      functionCall.Name,
			Arguments: string(arguments),
		}:
		default:
		}
	}
}

func (s *googleRealtimeSession) isNewGenerationMessage(message *genai.LiveServerMessage) bool {
	if message.ToolCall != nil {
		return true
	}
	if message.ServerContent == nil {
		return false
	}
	content := message.ServerContent
	if content.ModelTurn != nil {
		return true
	}
	if content.OutputTranscription != nil && content.OutputTranscription.Text != "" {
		return true
	}
	return content.InputTranscription != nil && content.InputTranscription.Text != ""
}

func (s *googleRealtimeSession) ensureGeneration() {
	if s.generation != nil && !s.generation.closed {
		return
	}
	s.responseSeq++
	responseID := fmt.Sprintf("GR_%d", s.responseSeq)
	generation := &googleRealtimeGeneration{
		responseID:   responseID,
		messageCh:    make(chan llm.MessageGeneration, 1),
		functionCh:   make(chan *llm.FunctionCall, 16),
		textCh:       make(chan string, 16),
		audioCh:      make(chan *model.AudioFrame, 16),
		modalitiesCh: make(chan []string, 1),
		createdAt:    time.Now(),
	}
	modalities := []string{"text"}
	if s.audioOutput {
		modalities = []string{"audio", "text"}
	} else {
		close(generation.audioCh)
		generation.audioChClosed = true
	}
	generation.modalitiesCh <- modalities
	generation.messageCh <- llm.MessageGeneration{
		MessageID:    responseID,
		TextCh:       generation.textCh,
		AudioCh:      generation.audioCh,
		ModalitiesCh: generation.modalitiesCh,
	}
	s.generation = generation
	userInitiated := s.consumePendingReply()
	if !userInitiated {
		s.emitEvent(llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted})
	}
	s.emitEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:     generation.messageCh,
			FunctionCh:    generation.functionCh,
			ResponseID:    responseID,
			UserInitiated: userInitiated,
		},
	})
}

func (s *googleRealtimeSession) setPendingReply(pending bool) {
	s.mu.Lock()
	s.pendingReply = pending
	if pending {
		s.pendingReplyAt = time.Now()
	} else {
		s.pendingReplyAt = time.Time{}
	}
	s.mu.Unlock()
}

func (s *googleRealtimeSession) consumePendingReply() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingReplyExpiredLocked() {
		return false
	}
	pending := s.pendingReply
	s.pendingReply = false
	s.pendingReplyAt = time.Time{}
	return pending
}

func (s *googleRealtimeSession) hasPendingReply() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingReplyExpiredLocked() {
		return false
	}
	return s.pendingReply
}

func (s *googleRealtimeSession) pendingReplyExpiredLocked() bool {
	if !s.pendingReply || s.pendingReplyAt.IsZero() || time.Since(s.pendingReplyAt) < googleRealtimeGenerateReplyTimeout {
		return false
	}
	s.pendingReply = false
	s.pendingReplyAt = time.Time{}
	return true
}

func (s *googleRealtimeSession) sendGenerationText(text string) {
	if s.generation == nil || s.generation.closed || text == "" {
		return
	}
	defer func() {
		_ = recover()
	}()
	s.generation.outputText += text
	if s.generation.firstTokenAt.IsZero() {
		s.generation.firstTokenAt = time.Now()
	}
	select {
	case s.generation.textCh <- text:
	default:
	}
}

func (s *googleRealtimeSession) sendGenerationAudio(data []byte) {
	if s.generation == nil || s.generation.closed || s.generation.audioChClosed || len(data) == 0 {
		return
	}
	defer func() {
		_ = recover()
	}()
	if s.generation.firstTokenAt.IsZero() {
		s.generation.firstTokenAt = time.Now()
	}
	frame := &model.AudioFrame{
		Data:              data,
		SampleRate:        googleRealtimeOutputSampleRate,
		NumChannels:       googleRealtimeOutputChannels,
		SamplesPerChannel: uint32(len(data) / (2 * googleRealtimeOutputChannels)),
	}
	select {
	case s.generation.audioCh <- frame:
	default:
	}
}

func (s *googleRealtimeSession) commitCompletedTranscripts() {
	if s.chatCtx == nil {
		s.chatCtx = llm.EmptyChatContext()
	}
	if s.inputText != "" {
		s.chatCtx.AddMessage(llm.ChatMessageArgs{
			ID:   s.currentInputID(),
			Role: llm.ChatRoleUser,
			Text: s.inputText,
		})
		s.inputID = ""
		s.inputText = ""
	}
	if s.generation != nil && s.generation.outputText != "" {
		s.chatCtx.AddMessage(llm.ChatMessageArgs{
			ID:   s.generation.responseID,
			Role: llm.ChatRoleAssistant,
			Text: s.generation.outputText,
		})
	}
}

func (s *googleRealtimeSession) closeGeneration() {
	if s.generation == nil || s.generation.closed {
		return
	}
	defer func() {
		_ = recover()
	}()
	s.markGenerationCompleted()
	s.generation.closed = true
	close(s.generation.textCh)
	if !s.generation.audioChClosed {
		close(s.generation.audioCh)
		s.generation.audioChClosed = true
	}
	close(s.generation.modalitiesCh)
	close(s.generation.messageCh)
	close(s.generation.functionCh)
}

func (s *googleRealtimeSession) emitUsageMetrics(usage *genai.UsageMetadata) {
	if s.generation == nil || usage == nil {
		return
	}
	now := time.Now()
	durationEnd := now
	if !s.generation.completedAt.IsZero() {
		durationEnd = s.generation.completedAt
	}
	duration := durationEnd.Sub(s.generation.createdAt).Seconds()
	ttft := -1.0
	if !s.generation.firstTokenAt.IsZero() {
		ttft = s.generation.firstTokenAt.Sub(s.generation.createdAt).Seconds()
	}
	tokensPerSecond := 0.0
	if duration > 0 {
		tokensPerSecond = float64(usage.ResponseTokenCount) / duration
	}
	s.emitEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeMetricsCollected,
		Metrics: &telemetry.RealtimeModelMetrics{
			RequestID:       s.generation.responseID,
			Timestamp:       s.generation.createdAt,
			Duration:        duration,
			TTFT:            ttft,
			Cancelled:       false,
			InputTokens:     int(usage.PromptTokenCount),
			OutputTokens:    int(usage.ResponseTokenCount),
			TotalTokens:     int(usage.TotalTokenCount),
			TokensPerSecond: tokensPerSecond,
			InputTokenDetails: telemetry.InputTokenDetails{
				AudioTokens:         googleRealtimeModalityTokens(usage.PromptTokensDetails, genai.MediaModalityAudio),
				TextTokens:          googleRealtimeModalityTokens(usage.PromptTokensDetails, genai.MediaModalityText),
				ImageTokens:         googleRealtimeModalityTokens(usage.PromptTokensDetails, genai.MediaModalityImage),
				CachedTokens:        googleRealtimeTokenCountTotal(usage.CacheTokensDetails),
				CachedTokensDetails: googleRealtimeCachedTokenDetails(usage.CacheTokensDetails),
			},
			OutputTokenDetails: telemetry.OutputTokenDetails{
				AudioTokens: googleRealtimeModalityTokens(usage.ResponseTokensDetails, genai.MediaModalityAudio),
				TextTokens:  googleRealtimeModalityTokens(usage.ResponseTokensDetails, genai.MediaModalityText),
				ImageTokens: googleRealtimeModalityTokens(usage.ResponseTokensDetails, genai.MediaModalityImage),
			},
			Metadata: &telemetry.Metadata{ModelName: s.modelName, ModelProvider: s.provider},
		},
	})
}

func googleRealtimeModalityTokens(details []*genai.ModalityTokenCount, modality genai.MediaModality) int {
	total := 0
	for _, detail := range details {
		if detail == nil || detail.Modality != modality {
			continue
		}
		total += int(detail.TokenCount)
	}
	return total
}

func googleRealtimeTokenCountTotal(details []*genai.ModalityTokenCount) int {
	total := 0
	for _, detail := range details {
		if detail == nil {
			continue
		}
		total += int(detail.TokenCount)
	}
	return total
}

func googleRealtimeCachedTokenDetails(details []*genai.ModalityTokenCount) *telemetry.CachedTokenDetails {
	return &telemetry.CachedTokenDetails{
		AudioTokens: googleRealtimeModalityTokens(details, genai.MediaModalityAudio),
		TextTokens:  googleRealtimeModalityTokens(details, genai.MediaModalityText),
		ImageTokens: googleRealtimeModalityTokens(details, genai.MediaModalityImage),
	}
}

func (s *googleRealtimeSession) emitEvent(event llm.RealtimeEvent) {
	if s == nil || s.isClosed() {
		return
	}
	defer func() {
		_ = recover()
	}()
	select {
	case s.eventCh <- event:
	case <-s.ctx.Done():
	}
}

func (s *googleRealtimeSession) emitInputTranscription(final bool) {
	itemID := s.currentInputID()
	s.emitEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     itemID,
			Transcript: s.inputText,
			IsFinal:    final,
		},
	})
}

func (s *googleRealtimeSession) currentInputID() string {
	if s.inputID == "" {
		s.inputSeq++
		s.inputID = fmt.Sprintf("GI_%d", s.inputSeq)
	}
	return s.inputID
}

func (s *googleRealtimeSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.owner != nil {
			s.owner.unregisterSession(s)
		}
		s.mu.Lock()
		s.closed = true
		s.closeGeneration()
		s.mu.Unlock()
		if s.cancel != nil {
			s.cancel()
		}
		close(s.eventCh)
		if s.liveSession != nil {
			_ = s.liveSession.Close()
		}
	})
	return nil
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
	resampled, err = googleRealtimeMonoAudioFrame(resampled)
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

func googleRealtimeMonoAudioFrame(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if frame == nil || frame.NumChannels == googleRealtimeInputChannels {
		return frame, nil
	}
	if frame.NumChannels == 0 {
		return nil, fmt.Errorf("cannot downmix audio with zero channels")
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("cannot downmix non-16-bit PCM audio")
	}
	expectedBytes := int(frame.SamplesPerChannel * frame.NumChannels * 2)
	if len(frame.Data) < expectedBytes {
		return nil, fmt.Errorf("audio frame data is shorter than declared sample count")
	}
	if frame.SamplesPerChannel == 0 {
		return &model.AudioFrame{
			SampleRate:        frame.SampleRate,
			NumChannels:       googleRealtimeInputChannels,
			SamplesPerChannel: 0,
			ParticipantID:     frame.ParticipantID,
		}, nil
	}

	channelCount := int(frame.NumChannels)
	out := make([]byte, int(frame.SamplesPerChannel*2))
	for sample := 0; sample < int(frame.SamplesPerChannel); sample++ {
		var sum int32
		for ch := 0; ch < channelCount; ch++ {
			offset := (sample*channelCount + ch) * 2
			sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset:])))
		}
		avg := int16(sum / int32(channelCount))
		binary.LittleEndian.PutUint16(out[sample*2:], uint16(avg))
	}

	return &model.AudioFrame{
		Data:              out,
		SampleRate:        frame.SampleRate,
		NumChannels:       googleRealtimeInputChannels,
		SamplesPerChannel: frame.SamplesPerChannel,
		ParticipantID:     frame.ParticipantID,
	}, nil
}

func (s *googleRealtimeSession) PushVideo(frame *images.VideoFrame) error {
	if s == nil || s.liveSession == nil || frame == nil || s.isClosed() {
		return nil
	}
	data, err := images.Encode(frame, images.NewEncodeOptions())
	if err != nil {
		return err
	}
	return s.liveSession.SendRealtimeInput(genai.LiveRealtimeInput{
		Video: &genai.Blob{
			Data:     data,
			MIMEType: "image/jpeg",
		},
	})
}
func (s *googleRealtimeSession) CommitAudio() error { return nil }
func (s *googleRealtimeSession) ClearAudio() error  { return nil }

var _ llm.RealtimeModel = (*RealtimeModel)(nil)
var _ llm.RealtimeSession = (*googleRealtimeSession)(nil)
