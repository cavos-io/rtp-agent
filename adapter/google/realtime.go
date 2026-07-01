package google

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultGoogleRealtimeGeminiModel = "gemini-2.5-flash-native-audio-preview-12-2025"
	defaultGoogleRealtimeVertexModel = "gemini-live-2.5-flash-native-audio"
	defaultGoogleRealtimeVoice       = "Puck"
	defaultGoogleRealtimeLocation    = "us-central1"
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
}

type GoogleRealtimeOption func(*googleRealtimeOptions)

type googleRealtimeOptions struct {
	model                     string
	instructions              string
	voice                     string
	language                  string
	vertexAI                  *bool
	project                   string
	location                  string
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
		if voice != "" {
			options.voice = voice
		}
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
	}
}

func WithGoogleRealtimeLocation(location string) GoogleRealtimeOption {
	return func(options *googleRealtimeOptions) {
		options.location = location
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
	if project == "" {
		project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	location := options.location
	if location == "" {
		location = os.Getenv("GOOGLE_CLOUD_LOCATION")
	}
	if vertexAI {
		if location == "" {
			location = defaultGoogleRealtimeLocation
		}
		if project == "" {
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
	if voice == "" {
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
	if options.voice != "" {
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
