package nvidia

import (
	"fmt"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultNvidiaRealtimeBaseURL            = "localhost:8998"
	defaultNvidiaRealtimeVoice              = "NATF2"
	defaultNvidiaRealtimeTextPrompt         = "You are a helpful assistant."
	defaultNvidiaRealtimeModel              = "personaplex-7b"
	defaultNvidiaRealtimeSilenceThresholdMS = 500
	defaultNvidiaRealtimeSampleRate         = 24000
	defaultNvidiaRealtimeNumChannels        = 1
	nvidiaPersonaplexURLEnv                 = "PERSONAPLEX_URL"
)

type NvidiaRealtimeModel struct {
	baseURL            string
	voice              string
	textPrompt         string
	seed               *int
	silenceThresholdMS int
	useSSL             bool
}

type NvidiaRealtimeOption func(*NvidiaRealtimeModel)

func WithNvidiaRealtimeBaseURL(baseURL string) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		if baseURL == "" {
			return
		}
		m.baseURL, m.useSSL = normalizeNvidiaRealtimeBaseURL(baseURL)
	}
}

func WithNvidiaRealtimeVoice(voice string) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		if voice != "" {
			m.voice = voice
		}
	}
}

func WithNvidiaRealtimeTextPrompt(prompt string) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		if prompt != "" {
			m.textPrompt = prompt
		}
	}
}

func WithNvidiaRealtimeSeed(seed int) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		m.seed = &seed
	}
}

func WithNvidiaRealtimeSilenceThresholdMS(threshold int) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		m.silenceThresholdMS = threshold
	}
}

func NewNvidiaRealtimeModel(opts ...NvidiaRealtimeOption) *NvidiaRealtimeModel {
	baseURL := os.Getenv(nvidiaPersonaplexURLEnv)
	if baseURL == "" {
		baseURL = defaultNvidiaRealtimeBaseURL
	}
	normalizedBaseURL, useSSL := normalizeNvidiaRealtimeBaseURL(baseURL)
	model := &NvidiaRealtimeModel{
		baseURL:            normalizedBaseURL,
		voice:              defaultNvidiaRealtimeVoice,
		textPrompt:         defaultNvidiaRealtimeTextPrompt,
		silenceThresholdMS: defaultNvidiaRealtimeSilenceThresholdMS,
		useSSL:             useSSL,
	}
	for _, opt := range opts {
		opt(model)
	}
	return model
}

func normalizeNvidiaRealtimeBaseURL(baseURL string) (string, bool) {
	useSSL := strings.HasPrefix(baseURL, "wss://") || strings.HasPrefix(baseURL, "https://")
	for _, prefix := range []string{"ws://", "wss://", "http://", "https://"} {
		baseURL = strings.TrimPrefix(baseURL, prefix)
	}
	return baseURL, useSSL
}

func (m *NvidiaRealtimeModel) Label() string {
	return "personaplex-" + m.voice
}

func (m *NvidiaRealtimeModel) Model() string {
	return defaultNvidiaRealtimeModel
}

func (m *NvidiaRealtimeModel) Provider() string {
	return "nvidia"
}

func (m *NvidiaRealtimeModel) InputSampleRate() int {
	return defaultNvidiaRealtimeSampleRate
}

func (m *NvidiaRealtimeModel) OutputSampleRate() int {
	return defaultNvidiaRealtimeSampleRate
}

func (m *NvidiaRealtimeModel) NumChannels() int {
	return defaultNvidiaRealtimeNumChannels
}

func (m *NvidiaRealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{
		MessageTruncation:       false,
		TurnDetection:           false,
		UserTranscription:       false,
		AutoToolReplyGeneration: false,
		AudioOutput:             true,
		ManualFunctionCalls:     false,
		PerResponseToolChoice:   false,
	}
}

func (m *NvidiaRealtimeModel) Session() (llm.RealtimeSession, error) {
	return nil, fmt.Errorf("nvidia personaplex realtime session is not implemented")
}

func (m *NvidiaRealtimeModel) Close() error {
	return nil
}
