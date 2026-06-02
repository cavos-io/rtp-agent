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
	"github.com/cavos-io/rtp-agent/adapter/azure"
	"github.com/cavos-io/rtp-agent/adapter/baseten"
	"github.com/cavos-io/rtp-agent/adapter/cambai"
	"github.com/cavos-io/rtp-agent/adapter/cartesia"
	"github.com/cavos-io/rtp-agent/adapter/cerebras"
	"github.com/cavos-io/rtp-agent/adapter/clova"
	"github.com/cavos-io/rtp-agent/adapter/deepgram"
	"github.com/cavos-io/rtp-agent/adapter/elevenlabs"
	"github.com/cavos-io/rtp-agent/adapter/fal"
	"github.com/cavos-io/rtp-agent/adapter/fishaudio"
	adaptergoogle "github.com/cavos-io/rtp-agent/adapter/google"
	"github.com/cavos-io/rtp-agent/adapter/groq"
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
	providerAzure      = "azure"
	providerBaseten    = "baseten"
	providerCambai     = "cambai"
	providerCartesia   = "cartesia"
	providerCerebras   = "cerebras"
	providerClova      = "clova"
	providerDeepgram   = "deepgram"
	providerElevenLabs = "elevenlabs"
	providerFal        = "fal"
	providerFishAudio  = "fishaudio"
	providerGoogle     = "google"
	providerGroq       = "groq"
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
	STTEncoding                     string
	STTChainID                      string
	STTDetectLanguage               bool
	STTPunctuate                    *bool
	STTSpokenPunctuation            *bool
	STTProfanityFilter              *bool
	STTTagAudioEvents               *bool
	STTIncludeTimestamps            *bool
	STTInterimResults               *bool
	STTSmartFormat                  *bool
	STTNoDelay                      *bool
	STTEndpointingMS                *int
	STTDiarization                  *bool
	STTFillerWords                  *bool
	STTVADEvents                    *bool
	STTNumerals                     *bool
	STTMIPOptOut                    *bool
	STTKeywords                     []deepgram.DeepgramKeyword
	STTRedact                       []string
	STTTags                         []string
	STTTask                         string
	STTChunkLevel                   string
	STTVersion                      string
	STTPrompt                       string
	STTBaseURL                      string
	STTSampleRate                   *int
	STTBufferSizeSeconds            *float64
	STTAudioChunkDurationMS         *int
	STTMinTurnSilence               *int
	STTMaxTurnSilence               *int
	STTEndOfTurnConfidenceThreshold *float64
	STTFormatTurns                  *bool
	STTLanguageDetection            *bool
	STTContinuousPartials           *bool
	STTInterruptionDelay            *int
	STTKeytermsPrompt               []string
	STTVADThreshold                 *float64
	STTVADSilenceThresholdSeconds   *float64
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
	TTSTemperature                  *float64
	TTSMaxTokens                    *int
	TTSBufferSize                   *int
	TTSEnhanceNamedEntities         *bool
	TTSEnableSSMLParsing            *bool
	TTSAPIVersion                   string
	TTSWordTimestamps               *bool
	TTSVoiceEmbedding               []float64
	TTSEmotion                      string
	TTSVolume                       *float64
	TTSPronunciationDictID          string
	TTSMIPOptOut                    *bool
	TTSLatencyMode                  string
	TTSChunkLength                  *int
	TTSInstructions                 string
	TTSResponseFormat               string
	TTSBaseURL                      string
	TTSTextType                     string
	RealtimeProvider                string
	RealtimeModel                   string

	OpenAIAPIKey      string
	AnthropicAPIKey   string
	GoogleAPIKey      string
	ElevenLabsAPIKey  string
	GroqAPIKey        string
	CerebrasAPIKey    string
	ClovaSTTSecret    string
	ClovaSTTInvokeURL string
	ClovaClientID     string
	ClovaClientSecret string
	FalAPIKey         string
	FishAudioAPIKey   string

	GoogleCredentialsFile string

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
		STTEncoding:                     os.Getenv("RTP_AGENT_STT_ENCODING"),
		STTChainID:                      os.Getenv("RTP_AGENT_STT_CHAIN_ID"),
		STTDetectLanguage:               getenvBool("RTP_AGENT_STT_DETECT_LANGUAGE"),
		STTPunctuate:                    getenvOptionalBool("RTP_AGENT_STT_PUNCTUATE"),
		STTSpokenPunctuation:            getenvOptionalBool("RTP_AGENT_STT_SPOKEN_PUNCTUATION"),
		STTProfanityFilter:              getenvOptionalBool("RTP_AGENT_STT_PROFANITY_FILTER"),
		STTTagAudioEvents:               getenvOptionalBool("RTP_AGENT_STT_TAG_AUDIO_EVENTS"),
		STTIncludeTimestamps:            getenvOptionalBool("RTP_AGENT_STT_INCLUDE_TIMESTAMPS"),
		STTInterimResults:               getenvOptionalBool("RTP_AGENT_STT_INTERIM_RESULTS"),
		STTSmartFormat:                  getenvOptionalBool("RTP_AGENT_STT_SMART_FORMAT"),
		STTNoDelay:                      getenvOptionalBool("RTP_AGENT_STT_NO_DELAY"),
		STTEndpointingMS:                getenvOptionalInt("RTP_AGENT_STT_ENDPOINTING_MS"),
		STTDiarization:                  getenvOptionalBool("RTP_AGENT_STT_DIARIZATION"),
		STTFillerWords:                  getenvOptionalBool("RTP_AGENT_STT_FILLER_WORDS"),
		STTVADEvents:                    getenvOptionalBool("RTP_AGENT_STT_VAD_EVENTS"),
		STTNumerals:                     getenvOptionalBool("RTP_AGENT_STT_NUMERALS"),
		STTMIPOptOut:                    getenvOptionalBool("RTP_AGENT_STT_MIP_OPT_OUT"),
		STTKeywords:                     splitEnvDeepgramKeywords("RTP_AGENT_STT_KEYWORDS"),
		STTRedact:                       splitEnvList("RTP_AGENT_STT_REDACT"),
		STTTags:                         splitEnvList("RTP_AGENT_STT_TAGS"),
		STTTask:                         os.Getenv("RTP_AGENT_STT_TASK"),
		STTChunkLevel:                   os.Getenv("RTP_AGENT_STT_CHUNK_LEVEL"),
		STTVersion:                      os.Getenv("RTP_AGENT_STT_VERSION"),
		STTPrompt:                       os.Getenv("RTP_AGENT_STT_PROMPT"),
		STTBaseURL:                      os.Getenv("RTP_AGENT_STT_BASE_URL"),
		STTSampleRate:                   getenvOptionalInt("RTP_AGENT_STT_SAMPLE_RATE"),
		STTBufferSizeSeconds:            getenvOptionalFloat("RTP_AGENT_STT_BUFFER_SIZE_SECONDS"),
		STTAudioChunkDurationMS:         getenvOptionalInt("RTP_AGENT_STT_AUDIO_CHUNK_DURATION_MS"),
		STTMinTurnSilence:               getenvOptionalInt("RTP_AGENT_STT_MIN_TURN_SILENCE"),
		STTMaxTurnSilence:               getenvOptionalInt("RTP_AGENT_STT_MAX_TURN_SILENCE"),
		STTEndOfTurnConfidenceThreshold: getenvOptionalFloat("RTP_AGENT_STT_END_OF_TURN_CONFIDENCE_THRESHOLD"),
		STTFormatTurns:                  getenvOptionalBool("RTP_AGENT_STT_FORMAT_TURNS"),
		STTLanguageDetection:            getenvOptionalBool("RTP_AGENT_STT_LANGUAGE_DETECTION"),
		STTContinuousPartials:           getenvOptionalBool("RTP_AGENT_STT_CONTINUOUS_PARTIALS"),
		STTInterruptionDelay:            getenvOptionalInt("RTP_AGENT_STT_INTERRUPTION_DELAY"),
		STTKeytermsPrompt:               splitEnvList("RTP_AGENT_STT_KEYTERMS_PROMPT"),
		STTVADThreshold:                 getenvOptionalFloat("RTP_AGENT_STT_VAD_THRESHOLD"),
		STTVADSilenceThresholdSeconds:   getenvOptionalFloat("RTP_AGENT_STT_VAD_SILENCE_THRESHOLD_SECONDS"),
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
		TTSTemperature:                  getenvOptionalFloat("RTP_AGENT_TTS_TEMPERATURE"),
		TTSMaxTokens:                    getenvOptionalInt("RTP_AGENT_TTS_MAX_TOKENS"),
		TTSBufferSize:                   getenvOptionalInt("RTP_AGENT_TTS_BUFFER_SIZE"),
		TTSEnhanceNamedEntities:         getenvOptionalBool("RTP_AGENT_TTS_ENHANCE_NAMED_ENTITIES"),
		TTSEnableSSMLParsing:            getenvOptionalBool("RTP_AGENT_TTS_ENABLE_SSML_PARSING"),
		TTSAPIVersion:                   os.Getenv("RTP_AGENT_TTS_API_VERSION"),
		TTSWordTimestamps:               getenvOptionalBool("RTP_AGENT_TTS_WORD_TIMESTAMPS"),
		TTSVoiceEmbedding:               splitEnvFloatList("RTP_AGENT_TTS_VOICE_EMBEDDING"),
		TTSEmotion:                      os.Getenv("RTP_AGENT_TTS_EMOTION"),
		TTSVolume:                       getenvOptionalFloat("RTP_AGENT_TTS_VOLUME"),
		TTSPronunciationDictID:          os.Getenv("RTP_AGENT_TTS_PRONUNCIATION_DICT_ID"),
		TTSMIPOptOut:                    getenvOptionalBool("RTP_AGENT_TTS_MIP_OPT_OUT"),
		TTSLatencyMode:                  os.Getenv("RTP_AGENT_TTS_LATENCY_MODE"),
		TTSChunkLength:                  getenvOptionalInt("RTP_AGENT_TTS_CHUNK_LENGTH"),
		TTSInstructions:                 os.Getenv("RTP_AGENT_TTS_INSTRUCTIONS"),
		TTSResponseFormat:               os.Getenv("RTP_AGENT_TTS_RESPONSE_FORMAT"),
		TTSBaseURL:                      os.Getenv("RTP_AGENT_TTS_BASE_URL"),
		TTSTextType:                     os.Getenv("RTP_AGENT_TTS_TEXT_TYPE"),
		RealtimeProvider:                normalizedEnv("RTP_AGENT_REALTIME_PROVIDER"),
		RealtimeModel:                   os.Getenv("RTP_AGENT_REALTIME_MODEL"),
		OpenAIAPIKey:                    os.Getenv("OPENAI_API_KEY"),
		AnthropicAPIKey:                 os.Getenv("ANTHROPIC_API_KEY"),
		GoogleAPIKey:                    os.Getenv("GOOGLE_API_KEY"),
		ElevenLabsAPIKey:                firstEnv("ELEVENLABS_API_KEY", "ELEVEN_API_KEY"),
		GroqAPIKey:                      os.Getenv("GROQ_API_KEY"),
		CerebrasAPIKey:                  os.Getenv("CEREBRAS_API_KEY"),
		ClovaSTTSecret:                  os.Getenv("CLOVA_STT_SECRET"),
		ClovaSTTInvokeURL:               os.Getenv("CLOVA_STT_INVOKE_URL"),
		ClovaClientID:                   os.Getenv("CLOVA_CLIENT_ID"),
		ClovaClientSecret:               os.Getenv("CLOVA_CLIENT_SECRET"),
		FalAPIKey:                       firstEnv("FAL_KEY", "FAL_API_KEY"),
		FishAudioAPIKey:                 firstEnv("FISHAUDIO_API_KEY", "FISH_AUDIO_API_KEY"),
		GoogleCredentialsFile:           firstEnv("RTP_AGENT_GOOGLE_CREDENTIALS_FILE", "GOOGLE_APPLICATION_CREDENTIALS"),
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
	case providerBaseten:
		provider, err := baseten.NewBasetenLLM("", cfg.LLMModel)
		if err != nil {
			return nil, err
		}
		a.LLM = provider
	case providerGoogle:
		provider, err := adaptergoogle.NewGoogleLLM(cfg.GoogleAPIKey, cfg.LLMModel)
		if err != nil {
			return nil, err
		}
		a.LLM = provider
	case providerGroq:
		a.LLM = groq.NewGroqLLM(cfg.GroqAPIKey, cfg.LLMModel)
	case providerCerebras:
		a.LLM = cerebras.NewCerebrasLLM(cfg.CerebrasAPIKey, cfg.LLMModel)
	case providerFal:
		a.LLM = fal.NewFalLLM(cfg.FalAPIKey, cfg.LLMModel)
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
	case providerAzure:
		provider, err := azure.NewAzureSTT("", "")
		if err != nil {
			return nil, err
		}
		a.STT = provider
	case providerBaseten:
		sttOpts := []baseten.BasetenSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, baseten.WithBasetenSTTModelEndpoint(cfg.STTBaseURL))
		}
		if cfg.STTChainID != "" {
			sttOpts = append(sttOpts, baseten.WithBasetenSTTChainID(cfg.STTChainID))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, baseten.WithBasetenSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTEncoding != "" {
			sttOpts = append(sttOpts, baseten.WithBasetenSTTEncoding(cfg.STTEncoding))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, baseten.WithBasetenSTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTBufferSizeSeconds != nil {
			sttOpts = append(sttOpts, baseten.WithBasetenSTTBufferSizeSeconds(*cfg.STTBufferSizeSeconds))
		}
		if cfg.STTVADThreshold != nil {
			sttOpts = append(sttOpts, baseten.WithBasetenSTTVADThreshold(*cfg.STTVADThreshold))
		}
		provider, err := baseten.NewBasetenSTT("", cfg.STTModel, sttOpts...)
		if err != nil {
			return nil, err
		}
		a.STT = provider
	case providerGoogle:
		sttOpts := []adaptergoogle.GoogleSTTOption{}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, adaptergoogle.WithGoogleSTTModel(cfg.STTModel))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, adaptergoogle.WithGoogleSTTSampleRate(int32(*cfg.STTSampleRate)))
		}
		if cfg.STTPunctuate != nil {
			sttOpts = append(sttOpts, adaptergoogle.WithGoogleSTTPunctuate(*cfg.STTPunctuate))
		}
		if cfg.STTSpokenPunctuation != nil {
			sttOpts = append(sttOpts, adaptergoogle.WithGoogleSTTSpokenPunctuation(*cfg.STTSpokenPunctuation))
		}
		if cfg.STTProfanityFilter != nil {
			sttOpts = append(sttOpts, adaptergoogle.WithGoogleSTTProfanityFilter(*cfg.STTProfanityFilter))
		}
		provider, err := adaptergoogle.NewGoogleSTT(cfg.GoogleCredentialsFile, sttOpts...)
		if err != nil {
			return nil, err
		}
		a.STT = provider
	case providerElevenLabs:
		sttOpts := []elevenlabs.ElevenLabsSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, elevenlabs.WithElevenLabsSTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, elevenlabs.WithElevenLabsSTTModel(cfg.STTModel))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, elevenlabs.WithElevenLabsSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTTagAudioEvents != nil {
			sttOpts = append(sttOpts, elevenlabs.WithElevenLabsSTTTagAudioEvents(*cfg.STTTagAudioEvents))
		}
		if cfg.STTIncludeTimestamps != nil {
			sttOpts = append(sttOpts, elevenlabs.WithElevenLabsSTTIncludeTimestamps(*cfg.STTIncludeTimestamps))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, elevenlabs.WithElevenLabsSTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTVADThreshold != nil || cfg.STTVADSilenceThresholdSeconds != nil {
			sttOpts = append(sttOpts, elevenlabs.WithElevenLabsSTTServerVAD(elevenlabs.ElevenLabsVADOptions{
				VADSilenceThresholdSecs: cfg.STTVADSilenceThresholdSeconds,
				VADThreshold:            cfg.STTVADThreshold,
				MinSpeechDurationMS:     cfg.STTMinTurnSilence,
				MinSilenceDurationMS:    cfg.STTMaxTurnSilence,
			}))
		}
		if len(cfg.STTKeytermsPrompt) > 0 {
			sttOpts = append(sttOpts, elevenlabs.WithElevenLabsSTTKeyterms(cfg.STTKeytermsPrompt))
		}
		a.STT = elevenlabs.NewElevenLabsSTT(cfg.ElevenLabsAPIKey, sttOpts...)
	case providerCartesia:
		sttOpts := []cartesia.CartesiaSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, cartesia.WithCartesiaSTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, cartesia.WithCartesiaSTTModel(cfg.STTModel))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, cartesia.WithCartesiaSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, cartesia.WithCartesiaSTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTEncoding != "" {
			sttOpts = append(sttOpts, cartesia.WithCartesiaSTTEncoding(cfg.STTEncoding))
		}
		if cfg.STTAudioChunkDurationMS != nil {
			sttOpts = append(sttOpts, cartesia.WithCartesiaSTTAudioChunkDurationMS(*cfg.STTAudioChunkDurationMS))
		}
		a.STT = cartesia.NewCartesiaSTT("", sttOpts...)
	case providerClova:
		sttOpts := []clova.ClovaSTTOption{}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, clova.WithClovaSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTVADThreshold != nil {
			sttOpts = append(sttOpts, clova.WithClovaSTTThreshold(*cfg.STTVADThreshold))
		}
		a.STT = clova.NewClovaSTT(cfg.ClovaSTTSecret, cfg.ClovaSTTInvokeURL, sttOpts...)
	case providerDeepgram:
		sttOpts := []deepgram.DeepgramSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTInterimResults != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTInterimResults(*cfg.STTInterimResults))
		}
		if cfg.STTPunctuate != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTPunctuate(*cfg.STTPunctuate))
		}
		if cfg.STTSmartFormat != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTSmartFormat(*cfg.STTSmartFormat))
		}
		if cfg.STTNoDelay != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTNoDelay(*cfg.STTNoDelay))
		}
		if cfg.STTEndpointingMS != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTEndpointing(*cfg.STTEndpointingMS))
		}
		if cfg.STTDiarization != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTDiarization(*cfg.STTDiarization))
		}
		if cfg.STTFillerWords != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTFillerWords(*cfg.STTFillerWords))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTNumberOfChannels != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTNumChannels(*cfg.STTNumberOfChannels))
		}
		if cfg.STTVADEvents != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTVADEvents(*cfg.STTVADEvents))
		}
		if cfg.STTProfanityFilter != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTProfanityFilter(*cfg.STTProfanityFilter))
		}
		if cfg.STTNumerals != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTNumerals(*cfg.STTNumerals))
		}
		if cfg.STTMIPOptOut != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTMipOptOut(*cfg.STTMIPOptOut))
		}
		if len(cfg.STTKeywords) > 0 {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTKeywords(cfg.STTKeywords))
		}
		if len(cfg.STTKeytermsPrompt) > 0 {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTKeyterms(cfg.STTKeytermsPrompt))
		}
		if len(cfg.STTRedact) > 0 {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTRedact(cfg.STTRedact))
		}
		if len(cfg.STTTags) > 0 {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTTags(cfg.STTTags))
		}
		a.STT = deepgram.NewDeepgramSTT("", cfg.STTModel, sttOpts...)
	case providerFal:
		sttOpts := []fal.FalSTTOption{}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, fal.WithFalSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTTask != "" {
			sttOpts = append(sttOpts, fal.WithFalSTTTask(cfg.STTTask))
		}
		if cfg.STTChunkLevel != "" {
			sttOpts = append(sttOpts, fal.WithFalSTTChunkLevel(cfg.STTChunkLevel))
		}
		if cfg.STTVersion != "" {
			sttOpts = append(sttOpts, fal.WithFalSTTVersion(cfg.STTVersion))
		}
		a.STT = fal.NewFalSTT(cfg.FalAPIKey, sttOpts...)
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
	case providerAzure:
		provider, err := azure.NewAzureTTS("", "", cfg.TTSVoice)
		if err != nil {
			return nil, err
		}
		a.TTS = provider
	case providerBaseten:
		ttsOpts := []baseten.BasetenTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, baseten.WithBasetenTTSModelEndpoint(cfg.TTSBaseURL))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, baseten.WithBasetenTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, baseten.WithBasetenTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSTemperature != nil {
			ttsOpts = append(ttsOpts, baseten.WithBasetenTTSTemperature(*cfg.TTSTemperature))
		}
		if cfg.TTSMaxTokens != nil {
			ttsOpts = append(ttsOpts, baseten.WithBasetenTTSMaxTokens(*cfg.TTSMaxTokens))
		}
		if cfg.TTSBufferSize != nil {
			ttsOpts = append(ttsOpts, baseten.WithBasetenTTSBufferSize(*cfg.TTSBufferSize))
		}
		provider, err := baseten.NewBasetenTTS("", cfg.TTSModel, ttsOpts...)
		if err != nil {
			return nil, err
		}
		a.TTS = provider
	case providerGoogle:
		provider, err := adaptergoogle.NewGoogleTTS(cfg.GoogleCredentialsFile)
		if err != nil {
			return nil, err
		}
		a.TTS = provider
	case providerElevenLabs:
		ttsOpts := []elevenlabs.ElevenLabsTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, elevenlabs.WithElevenLabsBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, elevenlabs.WithElevenLabsLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSEnableSSMLParsing != nil {
			ttsOpts = append(ttsOpts, elevenlabs.WithElevenLabsEnableSSMLParsing(*cfg.TTSEnableSSMLParsing))
		}
		if cfg.TTSEncoding != "" {
			ttsOpts = append(ttsOpts, elevenlabs.WithElevenLabsEncoding(cfg.TTSEncoding))
		}
		provider, err := elevenlabs.NewElevenLabsTTS(cfg.ElevenLabsAPIKey, cfg.TTSVoice, cfg.TTSModel, ttsOpts...)
		if err != nil {
			return nil, err
		}
		a.TTS = provider
	case providerGroq:
		ttsOpts := []groq.GroqTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, groq.WithGroqTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, groq.WithGroqTTSModel(cfg.TTSModel))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, groq.WithGroqTTSVoice(cfg.TTSVoice))
		}
		a.TTS = groq.NewGroqTTS(cfg.GroqAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerCartesia:
		ttsOpts := []cartesia.CartesiaTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, cartesia.WithCartesiaBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, cartesia.WithCartesiaLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSEncoding != "" || cfg.TTSSampleRate != nil {
			sampleRate := 0
			if cfg.TTSSampleRate != nil {
				sampleRate = *cfg.TTSSampleRate
			}
			ttsOpts = append(ttsOpts, cartesia.WithCartesiaAudioFormat(cfg.TTSEncoding, sampleRate))
		}
		if cfg.TTSAPIVersion != "" {
			ttsOpts = append(ttsOpts, cartesia.WithCartesiaAPIVersion(cfg.TTSAPIVersion))
		}
		if cfg.TTSWordTimestamps != nil {
			ttsOpts = append(ttsOpts, cartesia.WithCartesiaWordTimestamps(*cfg.TTSWordTimestamps))
		}
		if len(cfg.TTSVoiceEmbedding) > 0 {
			ttsOpts = append(ttsOpts, cartesia.WithCartesiaVoiceEmbedding(cfg.TTSVoiceEmbedding))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, cartesia.WithCartesiaSpeed(cfg.TTSSpeed))
		}
		if cfg.TTSEmotion != "" {
			ttsOpts = append(ttsOpts, cartesia.WithCartesiaEmotion(cfg.TTSEmotion))
		}
		if cfg.TTSVolume != nil {
			ttsOpts = append(ttsOpts, cartesia.WithCartesiaVolume(*cfg.TTSVolume))
		}
		if cfg.TTSPronunciationDictID != "" {
			ttsOpts = append(ttsOpts, cartesia.WithCartesiaPronunciationDictID(cfg.TTSPronunciationDictID))
		}
		a.TTS = cartesia.NewCartesiaTTS("", cfg.TTSVoice, cfg.TTSModel, ttsOpts...)
	case providerClova:
		a.TTS = clova.NewClovaTTS(cfg.ClovaClientID, cfg.ClovaClientSecret, cfg.TTSVoice)
	case providerDeepgram:
		ttsOpts := []deepgram.DeepgramTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, deepgram.WithDeepgramTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSMIPOptOut != nil {
			ttsOpts = append(ttsOpts, deepgram.WithDeepgramTTSMipOptOut(*cfg.TTSMIPOptOut))
		}
		if cfg.TTSEncoding != "" || cfg.TTSSampleRate != nil {
			sampleRate := 0
			if cfg.TTSSampleRate != nil {
				sampleRate = *cfg.TTSSampleRate
			}
			ttsOpts = append(ttsOpts, deepgram.WithDeepgramTTSAudioFormat(cfg.TTSEncoding, sampleRate))
		}
		a.TTS = deepgram.NewDeepgramTTS("", cfg.TTSModel, ttsOpts...)
	case providerFishAudio:
		ttsOpts := []fishaudio.FishAudioTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, fishaudio.WithFishAudioTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, fishaudio.WithFishAudioTTSModel(cfg.TTSModel))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, fishaudio.WithFishAudioTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSResponseFormat != "" {
			ttsOpts = append(ttsOpts, fishaudio.WithFishAudioTTSOutputFormat(cfg.TTSResponseFormat))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, fishaudio.WithFishAudioTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSLatencyMode != "" {
			ttsOpts = append(ttsOpts, fishaudio.WithFishAudioTTSLatencyMode(cfg.TTSLatencyMode))
		}
		if cfg.TTSChunkLength != nil {
			ttsOpts = append(ttsOpts, fishaudio.WithFishAudioTTSChunkLength(*cfg.TTSChunkLength))
		}
		a.TTS = fishaudio.NewFishAudioTTS(cfg.FishAudioAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerCambai:
		ttsOpts := []cambai.CambaiTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, cambai.WithCambaiTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSVoice != "" {
			if voiceID, err := strconv.Atoi(cfg.TTSVoice); err == nil {
				ttsOpts = append(ttsOpts, cambai.WithCambaiTTSVoiceID(voiceID))
			}
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, cambai.WithCambaiTTSModel(cfg.TTSModel))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, cambai.WithCambaiTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSEncoding != "" {
			ttsOpts = append(ttsOpts, cambai.WithCambaiTTSOutputFormat(cfg.TTSEncoding))
		}
		if cfg.TTSInstructions != "" {
			ttsOpts = append(ttsOpts, cambai.WithCambaiTTSUserInstructions(cfg.TTSInstructions))
		}
		if cfg.TTSEnhanceNamedEntities != nil {
			ttsOpts = append(ttsOpts, cambai.WithCambaiTTSEnhanceNamedEntities(*cfg.TTSEnhanceNamedEntities))
		}
		provider, err := cambai.NewCambaiTTS("", "", ttsOpts...)
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

func splitEnvFloatList(name string) []float64 {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]float64, 0, len(parts))
	for _, part := range parts {
		rawValue := strings.TrimSpace(part)
		if rawValue == "" {
			continue
		}
		value, err := strconv.ParseFloat(rawValue, 64)
		if err == nil {
			values = append(values, value)
		}
	}
	return values
}

func splitEnvDeepgramKeywords(name string) []deepgram.DeepgramKeyword {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	keywords := make([]deepgram.DeepgramKeyword, 0, len(parts))
	for _, part := range parts {
		rawValue := strings.TrimSpace(part)
		if rawValue == "" {
			continue
		}
		keyword := deepgram.DeepgramKeyword{Keyword: rawValue}
		if name, boost, ok := strings.Cut(rawValue, ":"); ok {
			if parsedBoost, err := strconv.ParseFloat(strings.TrimSpace(boost), 64); err == nil {
				keyword.Keyword = strings.TrimSpace(name)
				keyword.Boost = parsedBoost
			}
		}
		if keyword.Keyword != "" {
			keywords = append(keywords, keyword)
		}
	}
	return keywords
}
