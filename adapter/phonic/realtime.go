package phonic

import (
	"errors"
	"os"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

const (
	phonicAPIKeyEnv = "PHONIC_API_KEY"
)

type RealtimeModel struct {
	apiKey string
	opts   realtimeOptions
}

type realtimeOptions struct {
	apiKey                        string
	phonicAgent                   string
	voice                         string
	welcomeMessage                *string
	generateWelcomeMessage        *bool
	project                       *string
	defaultLanguage               string
	additionalLanguages           []string
	multilingualMode              string
	audioSpeed                    *float64
	phonicTools                   []string
	boostedKeywords               []string
	minWordsToInterrupt           *int
	generateNoInputPokeText       *bool
	noInputPokeSeconds            *float64
	noInputPokeText               string
	noInputEndConversationSeconds *float64
}

type RealtimeModelOption func(*realtimeOptions)

func NewRealtimeModel(apiKey string, opts ...RealtimeModelOption) (*RealtimeModel, error) {
	if apiKey == "" {
		apiKey = os.Getenv(phonicAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, errors.New("phonic API key is required. Provide api_key or set PHONIC_API_KEY environment variable")
	}
	options := realtimeOptions{apiKey: apiKey}
	for _, opt := range opts {
		opt(&options)
	}
	return &RealtimeModel{apiKey: apiKey, opts: options}, nil
}

func WithPhonicAgent(agent string) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.phonicAgent = agent }
}

func WithPhonicVoice(voice string) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.voice = voice }
}

func WithPhonicWelcomeMessage(message string) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.welcomeMessage = &message }
}

func WithPhonicGenerateWelcomeMessage(generate bool) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.generateWelcomeMessage = &generate }
}

func WithPhonicProject(project string) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.project = &project }
}

func WithPhonicDefaultLanguage(language string) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.defaultLanguage = language }
}

func WithPhonicAdditionalLanguages(languages []string) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.additionalLanguages = append([]string(nil), languages...) }
}

func WithPhonicLanguages(languages []string) RealtimeModelOption {
	return func(opts *realtimeOptions) {
		if len(languages) == 0 || opts.defaultLanguage != "" || len(opts.additionalLanguages) > 0 {
			return
		}
		opts.defaultLanguage = languages[0]
		opts.additionalLanguages = append([]string(nil), languages[1:]...)
	}
}

func WithPhonicMultilingualMode(mode string) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.multilingualMode = mode }
}

func WithPhonicAudioSpeed(speed float64) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.audioSpeed = &speed }
}

func WithPhonicTools(tools []string) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.phonicTools = append([]string(nil), tools...) }
}

func WithPhonicBoostedKeywords(keywords []string) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.boostedKeywords = append([]string(nil), keywords...) }
}

func WithPhonicMinWordsToInterrupt(minWords int) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.minWordsToInterrupt = &minWords }
}

func WithPhonicGenerateNoInputPokeText(generate bool) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.generateNoInputPokeText = &generate }
}

func WithPhonicNoInputPokeSeconds(seconds float64) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.noInputPokeSeconds = &seconds }
}

func WithPhonicNoInputPokeText(text string) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.noInputPokeText = text }
}

func WithPhonicNoInputEndConversationSeconds(seconds float64) RealtimeModelOption {
	return func(opts *realtimeOptions) { opts.noInputEndConversationSeconds = &seconds }
}

func (m *RealtimeModel) Model() string    { return "phonic" }
func (m *RealtimeModel) Provider() string { return "phonic" }

func (m *RealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{
		MessageTruncation:       false,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             true,
		ManualFunctionCalls:     false,
		PerResponseToolChoice:   false,
		SupportsSay:             true,
	}
}

func (m *RealtimeModel) Session() (llm.RealtimeSession, error) {
	return &realtimeSession{eventCh: make(chan llm.RealtimeEvent)}, nil
}

func (m *RealtimeModel) Close() error { return nil }

func buildPhonicConfigPayload(opts realtimeOptions, instructions string, systemPromptPostfix string) map[string]any {
	payload := map[string]any{
		"type":          "config",
		"system_prompt": instructions + systemPromptPostfix,
		"input_format":  "pcm_44100",
		"output_format": "pcm_44100",
	}
	setPhonicString(payload, "agent", opts.phonicAgent)
	setPhonicString(payload, "voice_id", opts.voice)
	setPhonicString(payload, "default_language", opts.defaultLanguage)
	setPhonicString(payload, "multilingual_mode", opts.multilingualMode)
	setPhonicString(payload, "no_input_poke_text", opts.noInputPokeText)
	if opts.welcomeMessage != nil {
		payload["welcome_message"] = *opts.welcomeMessage
	}
	if opts.generateWelcomeMessage != nil {
		payload["generate_welcome_message"] = *opts.generateWelcomeMessage
	}
	if opts.project != nil {
		payload["project"] = *opts.project
	}
	if len(opts.additionalLanguages) > 0 {
		payload["additional_languages"] = append([]string(nil), opts.additionalLanguages...)
	}
	if opts.audioSpeed != nil {
		payload["audio_speed"] = *opts.audioSpeed
	}
	if len(opts.phonicTools) > 0 {
		payload["tools"] = append([]string(nil), opts.phonicTools...)
	}
	if len(opts.boostedKeywords) > 0 {
		payload["boosted_keywords"] = append([]string(nil), opts.boostedKeywords...)
	}
	if opts.minWordsToInterrupt != nil {
		payload["min_words_to_interrupt"] = *opts.minWordsToInterrupt
	}
	if opts.generateNoInputPokeText != nil {
		payload["generate_no_input_poke_text"] = *opts.generateNoInputPokeText
	}
	if opts.noInputPokeSeconds != nil {
		payload["no_input_poke_sec"] = *opts.noInputPokeSeconds
	}
	if opts.noInputEndConversationSeconds != nil {
		payload["no_input_end_conversation_sec"] = *opts.noInputEndConversationSeconds
	}
	return payload
}

func setPhonicString(payload map[string]any, key string, value string) {
	if value != "" {
		payload[key] = value
	}
}

type realtimeSession struct {
	eventCh chan llm.RealtimeEvent
}

func (s *realtimeSession) UpdateInstructions(string) error          { return nil }
func (s *realtimeSession) UpdateChatContext(*llm.ChatContext) error { return nil }
func (s *realtimeSession) UpdateTools([]llm.Tool) error             { return nil }
func (s *realtimeSession) UpdateOptions(llm.RealtimeSessionOptions) error {
	return phonicUnsupported("update_options")
}
func (s *realtimeSession) GenerateReply(llm.RealtimeGenerateReplyOptions) error { return nil }
func (s *realtimeSession) Say(string) error                                     { return nil }
func (s *realtimeSession) Truncate(llm.RealtimeTruncateOptions) error {
	return phonicUnsupported("truncate")
}
func (s *realtimeSession) Interrupt() error { return phonicUnsupported("interrupt") }
func (s *realtimeSession) Close() error {
	close(s.eventCh)
	return nil
}
func (s *realtimeSession) EventCh() <-chan llm.RealtimeEvent  { return s.eventCh }
func (s *realtimeSession) PushAudio(*model.AudioFrame) error  { return nil }
func (s *realtimeSession) PushVideo(*images.VideoFrame) error { return phonicUnsupported("push_video") }
func (s *realtimeSession) CommitAudio() error                 { return phonicUnsupported("commit_audio") }
func (s *realtimeSession) ClearAudio() error                  { return phonicUnsupported("clear_audio") }

func phonicUnsupported(operation string) error {
	return errors.New(operation + " is not supported by the Phonic realtime model")
}

var _ llm.RealtimeModel = (*RealtimeModel)(nil)
var _ llm.RealtimeSession = (*realtimeSession)(nil)
