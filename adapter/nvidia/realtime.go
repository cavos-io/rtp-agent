package nvidia

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/images"
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

type nvidiaRealtimeSession struct {
	baseURL            string
	voice              string
	textPrompt         string
	seed               *int
	silenceThresholdMS int
	useSSL             bool
	chatCtx            *llm.ChatContext
	events             chan llm.RealtimeEvent
	closed             bool
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
		m.voice = voice
	}
}

func WithNvidiaRealtimeTextPrompt(prompt string) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		m.textPrompt = prompt
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
		if strings.HasPrefix(baseURL, prefix) {
			baseURL = strings.TrimPrefix(baseURL, prefix)
			break
		}
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

func (m *NvidiaRealtimeModel) websocketURL() string {
	return buildNvidiaRealtimeWebsocketURL(m.useSSL, m.baseURL, m.voice, m.textPrompt, m.seed)
}

func buildNvidiaRealtimeWebsocketURL(useSSL bool, baseURL string, voice string, textPrompt string, seed *int) string {
	scheme := "ws"
	if useSSL {
		scheme = "wss"
	}
	parts := []string{
		"voice_prompt=" + url.QueryEscape(voice+".pt"),
		"text_prompt=" + url.QueryEscape(textPrompt),
	}
	if seed != nil {
		parts = append(parts, "seed="+url.QueryEscape(fmt.Sprintf("%d", *seed)))
	}
	query := strings.ReplaceAll(strings.Join(parts, "&"), "+", "%20")
	return fmt.Sprintf("%s://%s/api/chat?%s", scheme, baseURL, query)
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
	return &nvidiaRealtimeSession{
		baseURL:            m.baseURL,
		voice:              m.voice,
		textPrompt:         m.textPrompt,
		seed:               cloneNvidiaRealtimeSeed(m.seed),
		silenceThresholdMS: m.silenceThresholdMS,
		useSSL:             m.useSSL,
		chatCtx:            llm.EmptyChatContext(),
		events:             make(chan llm.RealtimeEvent),
	}, nil
}

func (m *NvidiaRealtimeModel) Close() error {
	return nil
}

func cloneNvidiaRealtimeSeed(seed *int) *int {
	if seed == nil {
		return nil
	}
	seedValue := *seed
	return &seedValue
}

func (s *nvidiaRealtimeSession) UpdateInstructions(instructions string) error {
	if s.closed {
		return nil
	}
	s.textPrompt = instructions
	return nil
}

func (s *nvidiaRealtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	if s.closed || chatCtx == nil {
		return nil
	}
	s.chatCtx = chatCtx.Copy()
	return nil
}

func (s *nvidiaRealtimeSession) UpdateTools(_ []llm.Tool) error {
	return nil
}

func (s *nvidiaRealtimeSession) UpdateOptions(_ llm.RealtimeSessionOptions) error {
	return nil
}

func (s *nvidiaRealtimeSession) GenerateReply(_ llm.RealtimeGenerateReplyOptions) error {
	return fmt.Errorf("generate_reply is not yet supported by the PersonaPlex realtime model")
}

func (s *nvidiaRealtimeSession) Say(_ string) error {
	return fmt.Errorf("say is not yet supported by the PersonaPlex realtime model")
}

func (s *nvidiaRealtimeSession) Truncate(_ llm.RealtimeTruncateOptions) error {
	return nil
}

func (s *nvidiaRealtimeSession) Interrupt() error {
	return nil
}

func (s *nvidiaRealtimeSession) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.events)
	return nil
}

func (s *nvidiaRealtimeSession) EventCh() <-chan llm.RealtimeEvent {
	return s.events
}

func (s *nvidiaRealtimeSession) PushAudio(_ *model.AudioFrame) error {
	return nil
}

func (s *nvidiaRealtimeSession) PushVideo(_ *images.VideoFrame) error {
	return nil
}

func (s *nvidiaRealtimeSession) CommitAudio() error {
	return nil
}

func (s *nvidiaRealtimeSession) ClearAudio() error {
	return nil
}

func (s *nvidiaRealtimeSession) websocketURL() string {
	return buildNvidiaRealtimeWebsocketURL(s.useSSL, s.baseURL, s.voice, s.textPrompt, s.seed)
}
