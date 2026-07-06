package ultravox

import (
	"fmt"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultRealtimeBaseURL          = "https://api.ultravox.ai/api"
	defaultRealtimeModel            = "fixie-ai/ultravox"
	defaultRealtimeVoice            = "Mark"
	defaultRealtimeSystemPrompt     = "You are a helpful assistant."
	defaultRealtimeInputSampleRate  = 16000
	defaultRealtimeOutputSampleRate = 24000
	defaultRealtimeOutputMedium     = "voice"
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
}

type RealtimeOption func(*RealtimeModel)

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

func (m *RealtimeModel) Session() (llm.RealtimeSession, error) {
	return nil, fmt.Errorf("ultravox realtime session is not implemented")
}

func (m *RealtimeModel) Close() error { return nil }
