package app

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/cavos-io/rtp-agent/adapter/anthropic"
	"github.com/cavos-io/rtp-agent/adapter/assemblyai"
	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/interface/worker"
	goopenai "github.com/sashabaranov/go-openai"
)

const (
	providerAnthropic  = "anthropic"
	providerAssemblyAI = "assemblyai"
	providerOpenAI     = "openai"
	providerLiveKit    = "livekit"
)

type AppConfig struct {
	WorkerOptions worker.WorkerOptions
	Instructions  string

	LLMProvider                     string
	LLMModel                        string
	LLMBaseURL                      string
	STTProvider                     string
	STTModel                        string
	STTLanguage                     string
	STTDetectLanguage               bool
	STTPrompt                       string
	STTBaseURL                      string
	STTSampleRate                   *int
	STTMinTurnSilence               *int
	STTMaxTurnSilence               *int
	STTEndOfTurnConfidenceThreshold *float64
	STTFormatTurns                  *bool
	STTLanguageDetection            *bool
	STTContinuousPartials           *bool
	STTInterruptionDelay            *int
	STTKeytermsPrompt               []string
	STTVADThreshold                 *float64
	STTSpeakerLabels                *bool
	STTMaxSpeakers                  *int
	STTDomain                       string
	TTSProvider                     string
	TTSModel                        string
	TTSVoice                        string
	TTSSpeed                        float64
	TTSInstructions                 string
	TTSResponseFormat               string
	TTSBaseURL                      string
	RealtimeProvider                string
	RealtimeModel                   string

	OpenAIAPIKey    string
	AnthropicAPIKey string

	LiveKitInferenceAPIKey    string
	LiveKitInferenceAPISecret string
}

type App struct {
	Server        *worker.AgentServer
	Agent         *agent.Agent
	Session       *agent.AgentSession
	RealtimeModel llm.RealtimeModel
}

func DefaultConfigFromEnv() AppConfig {
	return AppConfig{
		Instructions:                    getenvDefault("RTP_AGENT_INSTRUCTIONS", "You are a helpful realtime voice agent."),
		LLMProvider:                     normalizedEnv("RTP_AGENT_LLM_PROVIDER"),
		LLMModel:                        os.Getenv("RTP_AGENT_LLM_MODEL"),
		LLMBaseURL:                      os.Getenv("RTP_AGENT_LLM_BASE_URL"),
		STTProvider:                     normalizedEnv("RTP_AGENT_STT_PROVIDER"),
		STTModel:                        os.Getenv("RTP_AGENT_STT_MODEL"),
		STTLanguage:                     os.Getenv("RTP_AGENT_STT_LANGUAGE"),
		STTDetectLanguage:               getenvBool("RTP_AGENT_STT_DETECT_LANGUAGE"),
		STTPrompt:                       os.Getenv("RTP_AGENT_STT_PROMPT"),
		STTBaseURL:                      os.Getenv("RTP_AGENT_STT_BASE_URL"),
		STTSampleRate:                   getenvOptionalInt("RTP_AGENT_STT_SAMPLE_RATE"),
		STTMinTurnSilence:               getenvOptionalInt("RTP_AGENT_STT_MIN_TURN_SILENCE"),
		STTMaxTurnSilence:               getenvOptionalInt("RTP_AGENT_STT_MAX_TURN_SILENCE"),
		STTEndOfTurnConfidenceThreshold: getenvOptionalFloat("RTP_AGENT_STT_END_OF_TURN_CONFIDENCE_THRESHOLD"),
		STTFormatTurns:                  getenvOptionalBool("RTP_AGENT_STT_FORMAT_TURNS"),
		STTLanguageDetection:            getenvOptionalBool("RTP_AGENT_STT_LANGUAGE_DETECTION"),
		STTContinuousPartials:           getenvOptionalBool("RTP_AGENT_STT_CONTINUOUS_PARTIALS"),
		STTInterruptionDelay:            getenvOptionalInt("RTP_AGENT_STT_INTERRUPTION_DELAY"),
		STTKeytermsPrompt:               splitEnvList("RTP_AGENT_STT_KEYTERMS_PROMPT"),
		STTVADThreshold:                 getenvOptionalFloat("RTP_AGENT_STT_VAD_THRESHOLD"),
		STTSpeakerLabels:                getenvOptionalBool("RTP_AGENT_STT_SPEAKER_LABELS"),
		STTMaxSpeakers:                  getenvOptionalInt("RTP_AGENT_STT_MAX_SPEAKERS"),
		STTDomain:                       os.Getenv("RTP_AGENT_STT_DOMAIN"),
		TTSProvider:                     normalizedEnv("RTP_AGENT_TTS_PROVIDER"),
		TTSModel:                        os.Getenv("RTP_AGENT_TTS_MODEL"),
		TTSVoice:                        os.Getenv("RTP_AGENT_TTS_VOICE"),
		TTSSpeed:                        getenvFloat("RTP_AGENT_TTS_SPEED"),
		TTSInstructions:                 os.Getenv("RTP_AGENT_TTS_INSTRUCTIONS"),
		TTSResponseFormat:               os.Getenv("RTP_AGENT_TTS_RESPONSE_FORMAT"),
		TTSBaseURL:                      os.Getenv("RTP_AGENT_TTS_BASE_URL"),
		RealtimeProvider:                normalizedEnv("RTP_AGENT_REALTIME_PROVIDER"),
		RealtimeModel:                   os.Getenv("RTP_AGENT_REALTIME_MODEL"),
		OpenAIAPIKey:                    os.Getenv("OPENAI_API_KEY"),
		AnthropicAPIKey:                 os.Getenv("ANTHROPIC_API_KEY"),
	}
}

func Init(cfg AppConfig) (*App, error) {
	return NewApp(cfg)
}

func NewApp(cfg AppConfig) (*App, error) {
	baseAgent := agent.NewAgent(cfg.Instructions)
	if baseAgent.Instructions == "" {
		baseAgent.Instructions = "You are a helpful realtime voice agent."
	}

	realtimeModel, err := configureProviders(cfg, baseAgent)
	if err != nil {
		return nil, err
	}

	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})

	opts := cfg.WorkerOptions
	if opts.AgentName == "" {
		opts.AgentName = "example-agent"
	}
	if opts.WorkerType == "" {
		opts.WorkerType = worker.WorkerTypeRoom
	}
	server := worker.NewAgentServer(opts)

	app := &App{
		Server:        server,
		Agent:         baseAgent,
		Session:       session,
		RealtimeModel: realtimeModel,
	}
	if err := server.RTCSession(app.runSession, nil, nil); err != nil {
		return nil, err
	}
	return app, nil
}

func (a *App) runSession(ctx *worker.JobContext) error {
	if a.Session == nil {
		return fmt.Errorf("agent session is not configured")
	}
	a.Server.SetConsoleSession(a.Session)
	if a.Session.STT == nil && a.Session.LLM == nil && a.Session.TTS == nil && a.RealtimeModel == nil {
		return nil
	}
	return a.Session.Start(context.Background())
}

func configureProviders(cfg AppConfig, a *agent.Agent) (llm.RealtimeModel, error) {
	switch normalizeProvider(cfg.LLMProvider) {
	case "":
	case providerAnthropic:
		llmOpts := []anthropic.AnthropicOption{}
		if cfg.LLMBaseURL != "" {
			llmOpts = append(llmOpts, anthropic.WithAnthropicBaseURL(cfg.LLMBaseURL))
		}
		provider, err := anthropic.NewAnthropicLLM(cfg.AnthropicAPIKey, cfg.LLMModel, llmOpts...)
		if err != nil {
			return nil, err
		}
		a.LLM = provider
	case providerOpenAI:
		provider, err := openai.NewOpenAILLM(cfg.OpenAIAPIKey, cfg.LLMModel)
		if err != nil {
			return nil, err
		}
		a.LLM = provider
	case providerLiveKit:
		provider, err := openai.NewLiveKitInferenceLLM(cfg.LLMModel, cfg.LiveKitInferenceAPIKey, cfg.LiveKitInferenceAPISecret)
		if err != nil {
			return nil, err
		}
		a.LLM = provider
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_LLM_PROVIDER %q", cfg.LLMProvider)
	}

	switch normalizeProvider(cfg.STTProvider) {
	case "":
	case providerAssemblyAI:
		sttOpts := []assemblyai.AssemblyAISTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTModel(cfg.STTModel))
		}
		if cfg.STTMinTurnSilence != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTMinTurnSilence(*cfg.STTMinTurnSilence))
		}
		if cfg.STTMaxTurnSilence != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTMaxTurnSilence(*cfg.STTMaxTurnSilence))
		}
		if cfg.STTEndOfTurnConfidenceThreshold != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTEndOfTurnConfidenceThreshold(*cfg.STTEndOfTurnConfidenceThreshold))
		}
		if cfg.STTFormatTurns != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTFormatTurns(*cfg.STTFormatTurns))
		}
		if cfg.STTLanguageDetection != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTLanguageDetection(*cfg.STTLanguageDetection))
		}
		if cfg.STTContinuousPartials != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTContinuousPartials(*cfg.STTContinuousPartials))
		}
		if cfg.STTInterruptionDelay != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTInterruptionDelay(*cfg.STTInterruptionDelay))
		}
		if len(cfg.STTKeytermsPrompt) > 0 {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTKeytermsPrompt(cfg.STTKeytermsPrompt))
		}
		if cfg.STTPrompt != "" {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTPrompt(cfg.STTPrompt))
		}
		if cfg.STTVADThreshold != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTVADThreshold(*cfg.STTVADThreshold))
		}
		if cfg.STTSpeakerLabels != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTSpeakerLabels(*cfg.STTSpeakerLabels))
		}
		if cfg.STTMaxSpeakers != nil {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTMaxSpeakers(*cfg.STTMaxSpeakers))
		}
		if cfg.STTDomain != "" {
			sttOpts = append(sttOpts, assemblyai.WithAssemblyAISTTDomain(cfg.STTDomain))
		}
		a.STT = assemblyai.NewAssemblyAISTT(os.Getenv("ASSEMBLYAI_API_KEY"), sttOpts...)
	case providerOpenAI:
		sttOpts := []openai.OpenAISTTOption{openai.WithOpenAISTTRealtime(true)}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, openai.WithOpenAISTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTDetectLanguage {
			sttOpts = append(sttOpts, openai.WithOpenAISTTDetectLanguage(true))
		}
		if cfg.STTPrompt != "" {
			sttOpts = append(sttOpts, openai.WithOpenAISTTPrompt(cfg.STTPrompt))
		}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, openai.WithOpenAISTTBaseURL(cfg.STTBaseURL))
		}
		provider, err := openai.NewOpenAISTT(cfg.OpenAIAPIKey, cfg.STTModel, sttOpts...)
		if err != nil {
			return nil, err
		}
		a.STT = provider
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_STT_PROVIDER %q", cfg.STTProvider)
	}

	switch normalizeProvider(cfg.TTSProvider) {
	case "":
	case providerOpenAI:
		ttsOpts := []openai.OpenAITTSOption{}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, openai.WithOpenAITTSModel(goopenai.SpeechModel(cfg.TTSModel)))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, openai.WithOpenAITTSVoice(goopenai.SpeechVoice(cfg.TTSVoice)))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, openai.WithOpenAITTSSpeed(cfg.TTSSpeed))
		}
		if cfg.TTSInstructions != "" {
			ttsOpts = append(ttsOpts, openai.WithOpenAITTSInstructions(cfg.TTSInstructions))
		}
		if cfg.TTSResponseFormat != "" {
			ttsOpts = append(ttsOpts, openai.WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormat(cfg.TTSResponseFormat)))
		}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, openai.WithOpenAITTSBaseURL(cfg.TTSBaseURL))
		}
		provider, err := openai.NewOpenAITTS(cfg.OpenAIAPIKey, "", "", ttsOpts...)
		if err != nil {
			return nil, err
		}
		a.TTS = provider
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_TTS_PROVIDER %q", cfg.TTSProvider)
	}

	switch normalizeProvider(cfg.RealtimeProvider) {
	case "":
		return nil, nil
	case providerOpenAI:
		return openai.NewRealtimeModel(cfg.OpenAIAPIKey, cfg.RealtimeModel), nil
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_REALTIME_PROVIDER %q", cfg.RealtimeProvider)
	}
}

func normalizedEnv(name string) string {
	return normalizeProvider(os.Getenv(name))
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func getenvDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func getenvBool(name string) bool {
	value, err := strconv.ParseBool(os.Getenv(name))
	return err == nil && value
}

func getenvOptionalBool(name string) *bool {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}
	return &value
}

func getenvFloat(name string) float64 {
	value, err := strconv.ParseFloat(os.Getenv(name), 64)
	if err != nil {
		return 0
	}
	return value
}

func getenvOptionalFloat(name string) *float64 {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &value
}

func getenvOptionalInt(name string) *int {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &value
}

func splitEnvList(name string) []string {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}
