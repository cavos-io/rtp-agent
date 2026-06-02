package app

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	awspollytypes "github.com/aws/aws-sdk-go-v2/service/polly/types"
	awstranscribetypes "github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
	"github.com/cavos-io/rtp-agent/adapter/anthropic"
	"github.com/cavos-io/rtp-agent/adapter/assemblyai"
	"github.com/cavos-io/rtp-agent/adapter/asyncai"
	adapteraws "github.com/cavos-io/rtp-agent/adapter/aws"
	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/interface/worker"
	goopenai "github.com/sashabaranov/go-openai"
)

const (
	providerAnthropic  = "anthropic"
	providerAssemblyAI = "assemblyai"
	providerAsyncAI    = "asyncai"
	providerAWS        = "aws"
	providerOpenAI     = "openai"
	providerLiveKit    = "livekit"
)

type AppConfig struct {
	WorkerOptions worker.WorkerOptions
	Instructions  string

	AWSRegion                       string
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
	STTVocabularyName               string
	STTSessionID                    string
	STTVocabularyFilterMethod       string
	STTVocabularyFilterName         string
	STTEnableChannelIdentification  *bool
	STTNumberOfChannels             *int
	STTEnablePartialStabilization   *bool
	STTPartialResultsStability      string
	STTLanguageModelName            string
	STTIdentifyLanguage             *bool
	STTIdentifyMultipleLanguages    *bool
	STTLanguageOptions              string
	STTPreferredLanguage            string
	STTVocabularyNames              string
	STTVocabularyFilterNames        string
	TTSProvider                     string
	TTSModel                        string
	TTSVoice                        string
	TTSLanguage                     string
	TTSEncoding                     string
	TTSSampleRate                   *int
	TTSSpeed                        float64
	TTSInstructions                 string
	TTSResponseFormat               string
	TTSBaseURL                      string
	TTSTextType                     string
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
		AWSRegion:                       firstEnv("RTP_AGENT_AWS_REGION", "AWS_REGION"),
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
		STTVocabularyName:               os.Getenv("RTP_AGENT_STT_VOCABULARY_NAME"),
		STTSessionID:                    os.Getenv("RTP_AGENT_STT_SESSION_ID"),
		STTVocabularyFilterMethod:       os.Getenv("RTP_AGENT_STT_VOCABULARY_FILTER_METHOD"),
		STTVocabularyFilterName:         os.Getenv("RTP_AGENT_STT_VOCABULARY_FILTER_NAME"),
		STTEnableChannelIdentification:  getenvOptionalBool("RTP_AGENT_STT_ENABLE_CHANNEL_IDENTIFICATION"),
		STTNumberOfChannels:             getenvOptionalInt("RTP_AGENT_STT_NUMBER_OF_CHANNELS"),
		STTEnablePartialStabilization:   getenvOptionalBool("RTP_AGENT_STT_ENABLE_PARTIAL_RESULTS_STABILIZATION"),
		STTPartialResultsStability:      os.Getenv("RTP_AGENT_STT_PARTIAL_RESULTS_STABILITY"),
		STTLanguageModelName:            os.Getenv("RTP_AGENT_STT_LANGUAGE_MODEL_NAME"),
		STTIdentifyLanguage:             getenvOptionalBool("RTP_AGENT_STT_IDENTIFY_LANGUAGE"),
		STTIdentifyMultipleLanguages:    getenvOptionalBool("RTP_AGENT_STT_IDENTIFY_MULTIPLE_LANGUAGES"),
		STTLanguageOptions:              os.Getenv("RTP_AGENT_STT_LANGUAGE_OPTIONS"),
		STTPreferredLanguage:            os.Getenv("RTP_AGENT_STT_PREFERRED_LANGUAGE"),
		STTVocabularyNames:              os.Getenv("RTP_AGENT_STT_VOCABULARY_NAMES"),
		STTVocabularyFilterNames:        os.Getenv("RTP_AGENT_STT_VOCABULARY_FILTER_NAMES"),
		TTSProvider:                     normalizedEnv("RTP_AGENT_TTS_PROVIDER"),
		TTSModel:                        os.Getenv("RTP_AGENT_TTS_MODEL"),
		TTSVoice:                        os.Getenv("RTP_AGENT_TTS_VOICE"),
		TTSLanguage:                     os.Getenv("RTP_AGENT_TTS_LANGUAGE"),
		TTSEncoding:                     os.Getenv("RTP_AGENT_TTS_ENCODING"),
		TTSSampleRate:                   getenvOptionalInt("RTP_AGENT_TTS_SAMPLE_RATE"),
		TTSSpeed:                        getenvFloat("RTP_AGENT_TTS_SPEED"),
		TTSInstructions:                 os.Getenv("RTP_AGENT_TTS_INSTRUCTIONS"),
		TTSResponseFormat:               os.Getenv("RTP_AGENT_TTS_RESPONSE_FORMAT"),
		TTSBaseURL:                      os.Getenv("RTP_AGENT_TTS_BASE_URL"),
		TTSTextType:                     os.Getenv("RTP_AGENT_TTS_TEXT_TYPE"),
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
	case providerAWS:
		provider, err := adapteraws.NewAWSLLM(context.Background(), cfg.AWSRegion, cfg.LLMModel)
		if err != nil {
			return nil, err
		}
		a.LLM = provider
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
	case providerAWS:
		sttOpts := []adapteraws.AWSSTTOption{}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTSampleRate(int32(*cfg.STTSampleRate)))
		}
		if cfg.STTVocabularyName != "" {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTVocabularyName(cfg.STTVocabularyName))
		}
		if cfg.STTSessionID != "" {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTSessionID(cfg.STTSessionID))
		}
		if cfg.STTVocabularyFilterMethod != "" {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTVocabularyFilterMethod(awstranscribetypes.VocabularyFilterMethod(cfg.STTVocabularyFilterMethod)))
		}
		if cfg.STTVocabularyFilterName != "" {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTVocabularyFilterName(cfg.STTVocabularyFilterName))
		}
		if cfg.STTSpeakerLabels != nil {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTShowSpeakerLabel(*cfg.STTSpeakerLabels))
		}
		if cfg.STTEnableChannelIdentification != nil {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTEnableChannelIdentification(*cfg.STTEnableChannelIdentification))
		}
		if cfg.STTNumberOfChannels != nil {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTNumberOfChannels(int32(*cfg.STTNumberOfChannels)))
		}
		if cfg.STTEnablePartialStabilization != nil {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTEnablePartialResultsStabilization(*cfg.STTEnablePartialStabilization))
		}
		if cfg.STTPartialResultsStability != "" {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTPartialResultsStability(awstranscribetypes.PartialResultsStability(cfg.STTPartialResultsStability)))
		}
		if cfg.STTLanguageModelName != "" {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTLanguageModelName(cfg.STTLanguageModelName))
		}
		if cfg.STTIdentifyLanguage != nil {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTIdentifyLanguage(*cfg.STTIdentifyLanguage))
		}
		if cfg.STTIdentifyMultipleLanguages != nil {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTIdentifyMultipleLanguages(*cfg.STTIdentifyMultipleLanguages))
		}
		if cfg.STTLanguageOptions != "" {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTLanguageOptions(cfg.STTLanguageOptions))
		}
		if cfg.STTPreferredLanguage != "" {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTPreferredLanguage(awstranscribetypes.LanguageCode(cfg.STTPreferredLanguage)))
		}
		if cfg.STTVocabularyNames != "" {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTVocabularyNames(cfg.STTVocabularyNames))
		}
		if cfg.STTVocabularyFilterNames != "" {
			sttOpts = append(sttOpts, adapteraws.WithAWSSTTVocabularyFilterNames(cfg.STTVocabularyFilterNames))
		}
		provider, err := adapteraws.NewAWSSTT(context.Background(), cfg.AWSRegion, sttOpts...)
		if err != nil {
			return nil, err
		}
		a.STT = provider
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
	case providerAWS:
		ttsOpts := []adapteraws.AWSTTSOption{}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, adapteraws.WithAWSTTSVoice(awspollytypes.VoiceId(cfg.TTSVoice)))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, adapteraws.WithAWSTTSEngine(awspollytypes.Engine(cfg.TTSModel)))
		}
		if cfg.TTSTextType != "" {
			ttsOpts = append(ttsOpts, adapteraws.WithAWSTTSTextType(awspollytypes.TextType(cfg.TTSTextType)))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, adapteraws.WithAWSTTSLanguage(awspollytypes.LanguageCode(cfg.TTSLanguage)))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, adapteraws.WithAWSTTSSampleRate(*cfg.TTSSampleRate))
		}
		provider, err := adapteraws.NewAWSTTS(context.Background(), cfg.AWSRegion, cfg.TTSVoice, ttsOpts...)
		if err != nil {
			return nil, err
		}
		a.TTS = provider
	case providerAsyncAI:
		ttsOpts := []asyncai.AsyncAITTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, asyncai.WithAsyncAITTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, asyncai.WithAsyncAITTSModel(cfg.TTSModel))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, asyncai.WithAsyncAITTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, asyncai.WithAsyncAITTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSEncoding != "" {
			ttsOpts = append(ttsOpts, asyncai.WithAsyncAITTSEncoding(cfg.TTSEncoding))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, asyncai.WithAsyncAITTSSampleRate(*cfg.TTSSampleRate))
		}
		a.TTS = asyncai.NewAsyncAITTS(os.Getenv("ASYNCAI_API_KEY"), cfg.TTSVoice, ttsOpts...)
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

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
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
