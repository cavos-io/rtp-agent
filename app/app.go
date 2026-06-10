package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	awspollytypes "github.com/aws/aws-sdk-go-v2/service/polly/types"
	awstranscribetypes "github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
	"github.com/cavos-io/rtp-agent/adapter/anam"
	"github.com/cavos-io/rtp-agent/adapter/anthropic"
	"github.com/cavos-io/rtp-agent/adapter/assemblyai"
	"github.com/cavos-io/rtp-agent/adapter/asyncai"
	"github.com/cavos-io/rtp-agent/adapter/avatario"
	"github.com/cavos-io/rtp-agent/adapter/avatartalk"
	adapteraws "github.com/cavos-io/rtp-agent/adapter/aws"
	"github.com/cavos-io/rtp-agent/adapter/azure"
	"github.com/cavos-io/rtp-agent/adapter/baseten"
	"github.com/cavos-io/rtp-agent/adapter/bey"
	"github.com/cavos-io/rtp-agent/adapter/bithuman"
	"github.com/cavos-io/rtp-agent/adapter/blingfire"
	"github.com/cavos-io/rtp-agent/adapter/browser"
	"github.com/cavos-io/rtp-agent/adapter/cambai"
	"github.com/cavos-io/rtp-agent/adapter/cartesia"
	"github.com/cavos-io/rtp-agent/adapter/cerebras"
	"github.com/cavos-io/rtp-agent/adapter/clova"
	"github.com/cavos-io/rtp-agent/adapter/deepgram"
	"github.com/cavos-io/rtp-agent/adapter/did"
	"github.com/cavos-io/rtp-agent/adapter/elevenlabs"
	"github.com/cavos-io/rtp-agent/adapter/fal"
	"github.com/cavos-io/rtp-agent/adapter/fireworksai"
	"github.com/cavos-io/rtp-agent/adapter/fishaudio"
	"github.com/cavos-io/rtp-agent/adapter/gladia"
	"github.com/cavos-io/rtp-agent/adapter/gnani"
	adaptergoogle "github.com/cavos-io/rtp-agent/adapter/google"
	"github.com/cavos-io/rtp-agent/adapter/gradium"
	"github.com/cavos-io/rtp-agent/adapter/groq"
	"github.com/cavos-io/rtp-agent/adapter/hamming"
	"github.com/cavos-io/rtp-agent/adapter/hedra"
	"github.com/cavos-io/rtp-agent/adapter/hume"
	"github.com/cavos-io/rtp-agent/adapter/inworld"
	"github.com/cavos-io/rtp-agent/adapter/keyframe"
	"github.com/cavos-io/rtp-agent/adapter/krisp"
	"github.com/cavos-io/rtp-agent/adapter/langchain"
	"github.com/cavos-io/rtp-agent/adapter/lemonslice"
	"github.com/cavos-io/rtp-agent/adapter/liveavatar"
	"github.com/cavos-io/rtp-agent/adapter/lmnt"
	"github.com/cavos-io/rtp-agent/adapter/minimal"
	"github.com/cavos-io/rtp-agent/adapter/minimax"
	"github.com/cavos-io/rtp-agent/adapter/mistralai"
	"github.com/cavos-io/rtp-agent/adapter/murf"
	"github.com/cavos-io/rtp-agent/adapter/neuphonic"
	"github.com/cavos-io/rtp-agent/adapter/nltk"
	"github.com/cavos-io/rtp-agent/adapter/nvidia"
	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/adapter/perplexity"
	"github.com/cavos-io/rtp-agent/adapter/phonic"
	"github.com/cavos-io/rtp-agent/adapter/resemble"
	"github.com/cavos-io/rtp-agent/adapter/respeecher"
	"github.com/cavos-io/rtp-agent/adapter/rime"
	"github.com/cavos-io/rtp-agent/adapter/rtzr"
	"github.com/cavos-io/rtp-agent/adapter/runway"
	"github.com/cavos-io/rtp-agent/adapter/sarvam"
	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/adapter/simli"
	"github.com/cavos-io/rtp-agent/adapter/simplismart"
	"github.com/cavos-io/rtp-agent/adapter/slng"
	"github.com/cavos-io/rtp-agent/adapter/smallestai"
	"github.com/cavos-io/rtp-agent/adapter/soniox"
	"github.com/cavos-io/rtp-agent/adapter/speechify"
	"github.com/cavos-io/rtp-agent/adapter/speechmatics"
	"github.com/cavos-io/rtp-agent/adapter/spitch"
	"github.com/cavos-io/rtp-agent/adapter/tavus"
	"github.com/cavos-io/rtp-agent/adapter/telnyx"
	"github.com/cavos-io/rtp-agent/adapter/trugen"
	"github.com/cavos-io/rtp-agent/adapter/turndetector"
	"github.com/cavos-io/rtp-agent/adapter/ultravox"
	"github.com/cavos-io/rtp-agent/adapter/upliftai"
	"github.com/cavos-io/rtp-agent/adapter/xai"
	"github.com/cavos-io/rtp-agent/core/agent"
	beta "github.com/cavos-io/rtp-agent/core/beta"
	betatools "github.com/cavos-io/rtp-agent/core/beta/tools"
	"github.com/cavos-io/rtp-agent/core/beta/workflows"
	"github.com/cavos-io/rtp-agent/core/evals"
	"github.com/cavos-io/rtp-agent/core/inference"
	"github.com/cavos-io/rtp-agent/core/llm"
	corestt "github.com/cavos-io/rtp-agent/core/stt"
	coretts "github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/interface/worker"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/plugin"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/livekit/protocol/livekit"
	livekitlogger "github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	goopenai "github.com/sashabaranov/go-openai"
)

func init() {
	plugin.RegisterPluginMetadata(anam.PluginTitle, anam.PluginVersion, anam.PluginPackage)
	plugin.RegisterPluginMetadata(anthropic.PluginTitle, anthropic.PluginVersion, anthropic.PluginPackage)
	plugin.RegisterPluginMetadata(assemblyai.PluginTitle, assemblyai.PluginVersion, assemblyai.PluginPackage)
	plugin.RegisterPluginMetadata(asyncai.PluginTitle, asyncai.PluginVersion, asyncai.PluginPackage)
	plugin.RegisterPluginMetadata(avatario.PluginTitle, avatario.PluginVersion, avatario.PluginPackage)
	plugin.RegisterPluginMetadata(avatartalk.PluginTitle, avatartalk.PluginVersion, avatartalk.PluginPackage)
	plugin.RegisterPluginMetadata(adapteraws.PluginTitle, adapteraws.PluginVersion, adapteraws.PluginPackage)
	plugin.RegisterPluginMetadata(azure.PluginTitle, azure.PluginVersion, azure.PluginPackage)
	plugin.RegisterPluginMetadata(baseten.PluginTitle, baseten.PluginVersion, baseten.PluginPackage)
	plugin.RegisterPluginMetadata(bey.PluginTitle, bey.PluginVersion, bey.PluginPackage)
	plugin.RegisterPluginMetadata(bithuman.PluginTitle, bithuman.PluginVersion, bithuman.PluginPackage)
	plugin.RegisterPluginDownloader(browser.PluginTitle, browser.PluginVersion, browser.PluginPackage, browser.Plugin{}.DownloadFiles)
	plugin.RegisterPluginMetadata(cambai.PluginTitle, cambai.PluginVersion, cambai.PluginPackage)
	plugin.RegisterPluginMetadata(cartesia.PluginTitle, cartesia.PluginVersion, cartesia.PluginPackage)
	plugin.RegisterPluginMetadata(cerebras.PluginTitle, cerebras.PluginVersion, cerebras.PluginPackage)
	plugin.RegisterPluginDownloader(clova.PluginTitle, clova.PluginVersion, clova.PluginPackage, clova.Plugin{}.DownloadFiles)
	plugin.RegisterPluginMetadata(deepgram.PluginTitle, deepgram.PluginVersion, deepgram.PluginPackage)
	plugin.RegisterPluginMetadata(did.PluginTitle, did.PluginVersion, did.PluginPackage)
	plugin.RegisterPluginMetadata(elevenlabs.PluginTitle, elevenlabs.PluginVersion, elevenlabs.PluginPackage)
	plugin.RegisterPluginMetadata(fal.PluginTitle, fal.PluginVersion, fal.PluginPackage)
	plugin.RegisterPluginMetadata(fireworksai.PluginTitle, fireworksai.PluginVersion, fireworksai.PluginPackage)
	plugin.RegisterPluginMetadata(fishaudio.PluginTitle, fishaudio.PluginVersion, fishaudio.PluginPackage)
	plugin.RegisterPluginMetadata(gladia.PluginTitle, gladia.PluginVersion, gladia.PluginPackage)
	plugin.RegisterPluginMetadata(gnani.PluginTitle, gnani.PluginVersion, gnani.PluginPackage)
	plugin.RegisterPluginMetadata(adaptergoogle.PluginTitle, adaptergoogle.PluginVersion, adaptergoogle.PluginPackage)
	plugin.RegisterPluginMetadata(gradium.PluginTitle, gradium.PluginVersion, gradium.PluginPackage)
	plugin.RegisterPluginMetadata(groq.PluginTitle, groq.PluginVersion, groq.PluginPackage)
	plugin.RegisterPluginMetadata(hamming.PluginTitle, hamming.PluginVersion, hamming.PluginPackage)
	plugin.RegisterPluginMetadata(hedra.PluginTitle, hedra.PluginVersion, hedra.PluginPackage)
	plugin.RegisterPluginMetadata(hume.PluginTitle, hume.PluginVersion, hume.PluginPackage)
	plugin.RegisterPluginMetadata(inworld.PluginTitle, inworld.PluginVersion, inworld.PluginPackage)
	plugin.RegisterPluginMetadata(keyframe.PluginTitle, keyframe.PluginVersion, keyframe.PluginPackage)
	plugin.RegisterPluginMetadata(krisp.PluginTitle, krisp.PluginVersion, krisp.PluginPackage)
	plugin.RegisterPluginMetadata(langchain.PluginTitle, langchain.PluginVersion, langchain.PluginPackage)
	plugin.RegisterPluginMetadata(lemonslice.PluginTitle, lemonslice.PluginVersion, lemonslice.PluginPackage)
	plugin.RegisterPluginMetadata(liveavatar.PluginTitle, liveavatar.PluginVersion, liveavatar.PluginPackage)
	plugin.RegisterPluginMetadata(lmnt.PluginTitle, lmnt.PluginVersion, lmnt.PluginPackage)
	plugin.RegisterPluginMetadata(minimal.PluginTitle, minimal.PluginVersion, minimal.PluginPackage)
	plugin.RegisterPluginMetadata(minimax.PluginTitle, minimax.PluginVersion, minimax.PluginPackage)
	plugin.RegisterPluginMetadata(mistralai.PluginTitle, mistralai.PluginVersion, mistralai.PluginPackage)
	plugin.RegisterPluginMetadata(murf.PluginTitle, murf.PluginVersion, murf.PluginPackage)
	plugin.RegisterPluginMetadata(neuphonic.PluginTitle, neuphonic.PluginVersion, neuphonic.PluginPackage)
	plugin.RegisterPluginDownloader(nltk.PluginTitle, nltk.PluginVersion, nltk.PluginPackage, nltk.Plugin{}.DownloadFiles)
	plugin.RegisterPluginMetadata(nvidia.PluginTitle, nvidia.PluginVersion, nvidia.PluginPackage)
	plugin.RegisterPluginMetadata(openai.PluginTitle, openai.PluginVersion, openai.PluginPackage)
	plugin.RegisterPluginMetadata(perplexity.PluginTitle, perplexity.PluginVersion, perplexity.PluginPackage)
	plugin.RegisterPluginMetadata(phonic.PluginTitle, phonic.PluginVersion, phonic.PluginPackage)
	plugin.RegisterPluginMetadata(resemble.PluginTitle, resemble.PluginVersion, resemble.PluginPackage)
	plugin.RegisterPluginMetadata(respeecher.PluginTitle, respeecher.PluginVersion, respeecher.PluginPackage)
	plugin.RegisterPluginMetadata(rime.PluginTitle, rime.PluginVersion, rime.PluginPackage)
	plugin.RegisterPluginMetadata(rtzr.PluginTitle, rtzr.PluginVersion, rtzr.PluginPackage)
	plugin.RegisterPluginMetadata(runway.PluginTitle, runway.PluginVersion, runway.PluginPackage)
	plugin.RegisterPluginMetadata(sarvam.PluginTitle, sarvam.PluginVersion, sarvam.PluginPackage)
	plugin.RegisterPluginDownloader(silero.PluginTitle, silero.PluginVersion, silero.PluginPackage, silero.Plugin{}.DownloadFiles)
	plugin.RegisterPluginMetadata(simli.PluginTitle, simli.PluginVersion, simli.PluginPackage)
	plugin.RegisterPluginMetadata(simplismart.PluginTitle, simplismart.PluginVersion, simplismart.PluginPackage)
	plugin.RegisterPluginMetadata(slng.PluginTitle, slng.PluginVersion, slng.PluginPackage)
	plugin.RegisterPluginMetadata(smallestai.PluginTitle, smallestai.PluginVersion, smallestai.PluginPackage)
	plugin.RegisterPluginMetadata(soniox.PluginTitle, soniox.PluginVersion, soniox.PluginPackage)
	plugin.RegisterPluginMetadata(speechify.PluginTitle, speechify.PluginVersion, speechify.PluginPackage)
	plugin.RegisterPluginMetadata(speechmatics.PluginTitle, speechmatics.PluginVersion, speechmatics.PluginPackage)
	plugin.RegisterPluginMetadata(spitch.PluginTitle, spitch.PluginVersion, spitch.PluginPackage)
	plugin.RegisterPluginMetadata(tavus.PluginTitle, tavus.PluginVersion, tavus.PluginPackage)
	plugin.RegisterPluginMetadata(telnyx.PluginTitle, telnyx.PluginVersion, telnyx.PluginPackage)
	plugin.RegisterPluginMetadata(trugen.PluginTitle, trugen.PluginVersion, trugen.PluginPackage)
	plugin.RegisterPluginMetadata(turndetector.PluginTitle, turndetector.PluginVersion, turndetector.PluginPackage)
	plugin.RegisterPluginMetadata(ultravox.PluginTitle, ultravox.PluginVersion, ultravox.PluginPackage)
	plugin.RegisterPluginMetadata(upliftai.PluginTitle, upliftai.PluginVersion, upliftai.PluginPackage)
	plugin.RegisterPluginMetadata(xai.PluginTitle, xai.PluginVersion, xai.PluginPackage)
}

var (
	appInitLoggerProvider     = telemetry.InitLoggerProvider
	appShutdownLoggerProvider = telemetry.ShutdownLoggerProvider
	appNewMCPServerHTTP       = llm.NewMCPServerHTTP
)

const (
	providerAnam         = "anam"
	providerAnthropic    = "anthropic"
	providerAssemblyAI   = "assemblyai"
	providerAsyncAI      = "asyncai"
	providerAvatario     = "avatario"
	providerAvatarTalk   = "avatartalk"
	providerAWS          = "aws"
	providerAzure        = "azure"
	providerBaseten      = "baseten"
	providerBey          = "bey"
	providerBitHuman     = "bithuman"
	providerCambai       = "cambai"
	providerCartesia     = "cartesia"
	providerCerebras     = "cerebras"
	providerClova        = "clova"
	providerDeepgram     = "deepgram"
	providerDID          = "did"
	providerElevenLabs   = "elevenlabs"
	providerFal          = "fal"
	providerFireworks    = "fireworks"
	providerFishAudio    = "fishaudio"
	providerGladia       = "gladia"
	providerGnani        = "gnani"
	providerGoogle       = "google"
	providerGradium      = "gradium"
	providerGroq         = "groq"
	providerHedra        = "hedra"
	providerHume         = "hume"
	providerInworld      = "inworld"
	providerKeyframe     = "keyframe"
	providerLangChain    = "langchain"
	providerLemonSlice   = "lemonslice"
	providerLiveAvatar   = "liveavatar"
	providerLMNT         = "lmnt"
	providerMinimal      = "minimal"
	providerMinimax      = "minimax"
	providerMistralAI    = "mistralai"
	providerMurf         = "murf"
	providerNeuphonic    = "neuphonic"
	providerNvidia       = "nvidia"
	providerOpenAI       = "openai"
	providerPerplexity   = "perplexity"
	providerPhonic       = "phonic"
	providerResemble     = "resemble"
	providerRespeecher   = "respeecher"
	providerRime         = "rime"
	providerRtzr         = "rtzr"
	providerRunway       = "runway"
	providerSarvam       = "sarvam"
	providerSilero       = "silero"
	providerSimli        = "simli"
	providerSimplismart  = "simplismart"
	providerSLNG         = "slng"
	providerSmallestAI   = "smallestai"
	providerSoniox       = "soniox"
	providerSpeechify    = "speechify"
	providerSpeechmatics = "speechmatics"
	providerSpitch       = "spitch"
	providerTavus        = "tavus"
	providerTelnyx       = "telnyx"
	providerTrugen       = "trugen"
	providerUltravox     = "ultravox"
	providerUpliftAI     = "upliftai"
	providerXAI          = "xai"
	providerLiveKit      = "livekit"
)

type AppConfig struct {
	WorkerOptions   worker.WorkerOptions
	Logger          livekitlogger.Logger
	MetricsRegistry *telemetry.MetricRegistry
	Instructions    string

	TelemetryLogsEndpoint string
	TelemetryLogsHeaders  map[string]string

	InitialChatContext                      map[string]any
	AWSRegion                               string
	LLMProvider                             string
	LLMModel                                string
	LLMBaseURL                              string
	LLMExtraHeaders                         map[string]string
	LLMExtraBody                            map[string]any
	LLMFallbackProviders                    []string
	LLMParallelToolCalls                    *bool
	LLMResponseFormat                       map[string]any
	STTProvider                             string
	STTFallbackProviders                    []string
	STTModel                                string
	STTLanguage                             string
	STTEncoding                             string
	STTChainID                              string
	STTDetectLanguage                       bool
	STTPunctuate                            *bool
	STTSpokenPunctuation                    *bool
	STTProfanityFilter                      *bool
	STTTagAudioEvents                       *bool
	STTIncludeTimestamps                    *bool
	STTWordTimestamps                       *bool
	STTInterimResults                       *bool
	STTSmartFormat                          *bool
	STTNoDelay                              *bool
	STTEndpointingMS                        *int
	STTDiarization                          *bool
	STTMultiSpeaker                         *bool
	STTFillerWords                          *bool
	STTVADEvents                            *bool
	STTNumerals                             *bool
	STTMIPOptOut                            *bool
	STTKeywords                             []deepgram.DeepgramKeyword
	STTRedact                               []string
	STTTags                                 []string
	STTTask                                 string
	STTChunkLevel                           string
	STTVersion                              string
	STTTemperature                          *float64
	STTSkipVAD                              *bool
	STTVADKwargs                            map[string]any
	STTTextTimeoutSeconds                   *float64
	STTTimestampGranularities               []string
	STTCodeSwitching                        *bool
	STTBitDepth                             *int
	STTEndpointingSeconds                   *float64
	STTMaxDurationWithoutEndpointingSeconds *float64
	STTRegion                               string
	STTCustomVocabulary                     []any
	STTCustomSpelling                       map[string][]string
	STTTranslationTargetLanguages           []string
	STTTranslationSourceLanguages           []string
	STTTranslationModel                     string
	STTOutputLocale                         string
	STTOperatingPoint                       string
	STTTranslationMatchOriginalUtterances   *bool
	STTTranslationLipsync                   *bool
	STTTranslationContextAdaptation         *bool
	STTTranslationContext                   string
	STTTranslationInformal                  *bool
	STTPreProcessingAudioEnhancer           *bool
	STTPreProcessingSpeechThreshold         *float64
	STTPrompt                               string
	STTBaseURL                              string
	STTStreamingURL                         string
	STTSampleRate                           *int
	STTBufferSizeSeconds                    *float64
	STTAudioChunkDurationMS                 *int
	STTMinTurnSilence                       *int
	STTMaxTurnSilence                       *int
	STTEndOfTurnConfidenceThreshold         *float64
	STTFormatTurns                          *bool
	STTLanguageDetection                    *bool
	STTContinuousPartials                   *bool
	STTInterruptionDelay                    *int
	STTKeytermsPrompt                       []string
	STTVADThreshold                         *float64
	STTVADSilenceThresholdSeconds           *float64
	STTSpeakerLabels                        *bool
	STTMaxSpeakers                          *int
	STTDomain                               string
	STTVocabularyName                       string
	STTSessionID                            string
	STTVocabularyFilterMethod               string
	STTVocabularyFilterName                 string
	STTEnableChannelIdentification          *bool
	STTNumberOfChannels                     *int
	STTEnablePartialStabilization           *bool
	STTPartialResultsStability              string
	STTLanguageModelName                    string
	STTIdentifyLanguage                     *bool
	STTIdentifyMultipleLanguages            *bool
	STTLanguageOptions                      string
	STTPreferredLanguage                    string
	STTVocabularyNames                      string
	STTVocabularyFilterNames                string
	STTOrganizationID                       string
	STTUserID                               string
	STTVADBucket                            *int
	STTVADFlush                             *bool
	STTVoiceProfile                         *bool
	STTVoiceProfileTopN                     *int
	STTMinEndOfTurnSilenceWhenConfident     *int
	STTMinSpeakers                          *int
	STTModelOptions                         map[string]any
	STTPositiveSpeechThreshold              *float64
	STTNegativeSpeechThreshold              *float64
	STTMinSpeechFrames                      *int
	STTFirstTurnMinSpeechFrames             *int
	STTNegativeFramesCount                  *int
	STTNegativeFramesWindow                 *int
	STTStartSpeechVolumeThreshold           *float64
	STTInterruptMinSpeechFrames             *int
	STTPreSpeechPadFrames                   *int
	STTNumInitialIgnoredFrames              *int
	STTPreferCurrentSpeaker                 *bool
	VADProvider                             string
	VADMinSpeechDuration                    *float64
	VADMinSilenceDuration                   *float64
	VADPrefixPaddingDuration                *float64
	VADPaddingDuration                      *float64
	VADMaxBufferedSpeech                    *float64
	VADActivationThreshold                  *float64
	VADDeactivationThreshold                *float64
	VADUpdateInterval                       *float64
	VADSampleRate                           *int
	AvatarProvider                          string
	TurnDetectorProvider                    string
	BackgroundAudioAmbient                  string
	BackgroundAudioThinking                 string
	TTSProvider                             string
	TTSFallbackProviders                    []string
	TTSModel                                string
	TTSVoice                                string
	TTSRefAudio                             string
	TTSVoiceID                              string
	TTSVoiceProvider                        string
	TTSLanguage                             string
	TTSEncoding                             string
	TTSSampleRate                           *int
	TTSSpeed                                float64
	TTSTemperature                          *float64
	TTSTopP                                 *float64
	TTSMaxTokens                            *int
	TTSBufferSize                           *int
	TTSEnhanceNamedEntities                 *bool
	TTSEnableSSMLParsing                    *bool
	TTSAPIVersion                           string
	TTSWordTimestamps                       *bool
	TTSVoiceEmbedding                       []float64
	TTSEmotion                              string
	TTSVolume                               *float64
	TTSPronunciationDictID                  string
	TTSMIPOptOut                            *bool
	TTSLatencyMode                          string
	TTSChunkLength                          *int
	TTSInstructions                         string
	TTSResponseFormat                       string
	TTSBaseURL                              string
	TTSWebsocketURL                         string
	TTSTextType                             string
	TTSNumberOfChannels                     *int
	TTSSampleWidth                          *int
	TTSJSONConfig                           map[string]any
	TTSBitRate                              *int
	TTSSpeakingRate                         *float64
	TTSTrailingSilence                      *float64
	TTSInstantMode                          *bool
	TTSPitch                                *int
	TTSTimestampType                        string
	TTSLoudnessNormalization                *bool
	TTSTextNormalization                    *bool
	TTSDeliveryMode                         string
	TTSTokenizerProvider                    string
	TTSTokenizerLanguage                    string
	TTSTokenizerMinSentenceLen              *int
	TTSTokenizerStreamContextLen            *int
	TTSTextReplacements                     map[string]string
	DisableTTSTextTransforms                bool
	WordTokenizerProvider                   string
	WordTokenizerLanguage                   string
	TTSStreamPacerEnabled                   bool
	TTSStreamPacerMinRemainingAudioMS       *int
	TTSStreamPacerMaxTextLength             *int
	TTSTimestampTransportStrategy           string
	TTSBufferCharThreshold                  *int
	TTSMaxBufferDelayMS                     *int
	TTSContextGenerationID                  string
	TTSContextUtterances                    []hume.HumeTTSUtterance
	TTSRegion                               string
	TTSModelOptions                         map[string]any
	RealtimeProvider                        string
	RealtimeModel                           string

	OpenAIAPIKey                string
	AnamAPIKey                  string
	AnthropicAPIKey             string
	AvatarioAPIKey              string
	AvatarTalkAPIKey            string
	BeyAPIKey                   string
	BitHumanAPIKey              string
	GoogleAPIKey                string
	ElevenLabsAPIKey            string
	GroqAPIKey                  string
	CerebrasAPIKey              string
	ClovaSTTSecret              string
	ClovaSTTInvokeURL           string
	ClovaClientID               string
	ClovaClientSecret           string
	DIDAPIKey                   string
	DIDAgentID                  string
	FalAPIKey                   string
	FireworksAPIKey             string
	FishAudioAPIKey             string
	GladiaAPIKey                string
	GnaniAPIKey                 string
	GradiumAPIKey               string
	HedraAPIKey                 string
	HumeAPIKey                  string
	InworldAPIKey               string
	KeyframeAPIKey              string
	LangChainAPIKey             string
	LemonSliceAPIKey            string
	LiveAvatarAPIKey            string
	LMNTAPIKey                  string
	MinimalAPIKey               string
	MinimaxAPIKey               string
	MistralAPIKey               string
	MurfAPIKey                  string
	NeuphonicAPIKey             string
	NvidiaAPIKey                string
	PerplexityAPIKey            string
	PhonicAPIKey                string
	ResembleAPIKey              string
	RespeecherAPIKey            string
	RimeAPIKey                  string
	RtzrClientID                string
	RtzrClientSecret            string
	RtzrAccessToken             string
	RunwayAPISecret             string
	RunwayAvatarID              string
	RunwayPresetID              string
	RunwayMaxDuration           *int
	SarvamAPIKey                string
	SimliAPIKey                 string
	SimplismartAPIKey           string
	SmallestAIAPIKey            string
	SLNGAPIKey                  string
	SonioxAPIKey                string
	SpeechifyAPIKey             string
	SpeechmaticsAPIKey          string
	SpitchAPIKey                string
	TavusAPIKey                 string
	TelnyxAPIKey                string
	TrugenAPIKey                string
	UltravoxAPIKey              string
	UpliftAIAPIKey              string
	XAIAPIKey                   string
	AnthropicTools              []string
	AnthropicComputerWidth      *int
	AnthropicComputerHeight     *int
	XAITools                    []string
	XAIAllowedXHandles          []string
	XAIFileSearchVectorStoreIDs []string
	XAIFileSearchMaxResults     *int

	GoogleCredentialsFile string

	LiveKitInferenceAPIKey                string
	LiveKitInferenceAPISecret             string
	AppTools                              []string
	MCPStdioServers                       []MCPStdioServerConfig
	MCPHTTPServers                        []MCPHTTPServerConfig
	IVRDetection                          bool
	IVRSilenceDurationSeconds             *float64
	WorkflowTask                          string
	WorkflowRequireConfirmation           bool
	WorkflowAddressPersona                string
	WorkflowAddressExtraInstructions      string
	WorkflowEmailPersona                  string
	WorkflowEmailExtraInstructions        string
	WorkflowDtmfNumDigits                 *int
	WorkflowDtmfAskConfirmation           *bool
	WorkflowDtmfInputTimeoutSeconds       *float64
	WorkflowDtmfStopEvent                 string
	WorkflowDtmfExtraInstructions         string
	WorkflowPhoneNumberExtraInstructions  string
	WorkflowDOBExtraInstructions          string
	WorkflowDOBIncludeTime                bool
	WorkflowNameFirstName                 *bool
	WorkflowNameMiddleName                *bool
	WorkflowNameLastName                  *bool
	WorkflowNameFormat                    string
	WorkflowNameVerifySpelling            bool
	WorkflowNameExtraInstructions         string
	WorkflowWarmTransferSipCallTo         string
	WorkflowWarmTransferSipTrunkID        string
	WorkflowWarmTransferSipConnection     *livekit.SIPOutboundConfig
	WorkflowWarmTransferSipNumber         string
	WorkflowWarmTransferSipHeaders        map[string]string
	WorkflowWarmTransferDTMF              string
	WorkflowWarmTransferRingingTimeout    *float64
	WorkflowWarmTransferHoldAudio         string
	WorkflowWarmTransferDisableHoldAudio  bool
	WorkflowWarmTransferPersona           string
	WorkflowWarmTransferExtraInstructions string
	WorkflowTaskGroupTasks                []string
	EvalJudges                            []string
}

type MCPStdioServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Cwd     string            `json:"cwd"`
}

type MCPHTTPServerConfig struct {
	URL           string            `json:"url"`
	TransportType string            `json:"transportType"`
	AllowedTools  []string          `json:"allowedTools"`
	Headers       map[string]string `json:"headers"`
}

type App struct {
	Server          *worker.AgentServer
	Agent           *agent.Agent
	Session         *agent.AgentSession
	RealtimeModel   llm.RealtimeModel
	Evaluator       *evals.JudgeGroup
	MCPServers      []llm.MCPServer
	MetricsRegistry *telemetry.MetricRegistry
	RoomIO          *worker.RoomIO
	RoomOptions     worker.RoomOptions
	Config          AppConfig
	telemetryLogs   bool
}

type EvaluationSummary struct {
	Result         *evals.EvaluationResult
	Score          float64
	AllPassed      bool
	AnyPassed      bool
	MajorityPassed bool
	NoneFailed     bool
}

func DefaultConfigFromEnv() AppConfig {
	return AppConfig{
		Instructions:                            getenvDefault("RTP_AGENT_INSTRUCTIONS", "You are a helpful realtime voice agent."),
		TelemetryLogsEndpoint:                   os.Getenv("RTP_AGENT_OTLP_LOGS_ENDPOINT"),
		TelemetryLogsHeaders:                    splitEnvStringMap("RTP_AGENT_OTLP_LOGS_HEADERS"),
		InitialChatContext:                      jsonEnvMap("RTP_AGENT_CHAT_CONTEXT_JSON"),
		AWSRegion:                               firstEnv("RTP_AGENT_AWS_REGION", "AWS_REGION"),
		LLMProvider:                             normalizedEnv("RTP_AGENT_LLM_PROVIDER"),
		LLMModel:                                os.Getenv("RTP_AGENT_LLM_MODEL"),
		LLMBaseURL:                              os.Getenv("RTP_AGENT_LLM_BASE_URL"),
		LLMExtraHeaders:                         splitEnvStringMap("RTP_AGENT_LLM_EXTRA_HEADERS"),
		LLMExtraBody:                            splitEnvMap("RTP_AGENT_LLM_JSON_CONFIG"),
		LLMFallbackProviders:                    splitEnvList("RTP_AGENT_LLM_FALLBACK_PROVIDERS"),
		LLMParallelToolCalls:                    getenvOptionalBool("RTP_AGENT_LLM_PARALLEL_TOOL_CALLS"),
		LLMResponseFormat:                       splitEnvMap("RTP_AGENT_LLM_RESPONSE_FORMAT"),
		STTProvider:                             normalizedEnv("RTP_AGENT_STT_PROVIDER"),
		STTFallbackProviders:                    splitEnvList("RTP_AGENT_STT_FALLBACK_PROVIDERS"),
		STTModel:                                os.Getenv("RTP_AGENT_STT_MODEL"),
		STTLanguage:                             os.Getenv("RTP_AGENT_STT_LANGUAGE"),
		STTEncoding:                             os.Getenv("RTP_AGENT_STT_ENCODING"),
		STTChainID:                              os.Getenv("RTP_AGENT_STT_CHAIN_ID"),
		STTDetectLanguage:                       getenvBool("RTP_AGENT_STT_DETECT_LANGUAGE"),
		STTPunctuate:                            getenvOptionalBool("RTP_AGENT_STT_PUNCTUATE"),
		STTSpokenPunctuation:                    getenvOptionalBool("RTP_AGENT_STT_SPOKEN_PUNCTUATION"),
		STTProfanityFilter:                      getenvOptionalBool("RTP_AGENT_STT_PROFANITY_FILTER"),
		STTTagAudioEvents:                       getenvOptionalBool("RTP_AGENT_STT_TAG_AUDIO_EVENTS"),
		STTIncludeTimestamps:                    getenvOptionalBool("RTP_AGENT_STT_INCLUDE_TIMESTAMPS"),
		STTWordTimestamps:                       getenvOptionalBool("RTP_AGENT_STT_WORD_TIMESTAMPS"),
		STTInterimResults:                       getenvOptionalBool("RTP_AGENT_STT_INTERIM_RESULTS"),
		STTSmartFormat:                          getenvOptionalBool("RTP_AGENT_STT_SMART_FORMAT"),
		STTNoDelay:                              getenvOptionalBool("RTP_AGENT_STT_NO_DELAY"),
		STTEndpointingMS:                        getenvOptionalInt("RTP_AGENT_STT_ENDPOINTING_MS"),
		STTDiarization:                          getenvOptionalBool("RTP_AGENT_STT_DIARIZATION"),
		STTMultiSpeaker:                         getenvOptionalBool("RTP_AGENT_STT_MULTI_SPEAKER"),
		STTFillerWords:                          getenvOptionalBool("RTP_AGENT_STT_FILLER_WORDS"),
		STTVADEvents:                            getenvOptionalBool("RTP_AGENT_STT_VAD_EVENTS"),
		STTNumerals:                             getenvOptionalBool("RTP_AGENT_STT_NUMERALS"),
		STTMIPOptOut:                            getenvOptionalBool("RTP_AGENT_STT_MIP_OPT_OUT"),
		STTKeywords:                             splitEnvDeepgramKeywords("RTP_AGENT_STT_KEYWORDS"),
		STTRedact:                               splitEnvList("RTP_AGENT_STT_REDACT"),
		STTTags:                                 splitEnvList("RTP_AGENT_STT_TAGS"),
		STTTask:                                 os.Getenv("RTP_AGENT_STT_TASK"),
		STTChunkLevel:                           os.Getenv("RTP_AGENT_STT_CHUNK_LEVEL"),
		STTVersion:                              os.Getenv("RTP_AGENT_STT_VERSION"),
		STTTemperature:                          getenvOptionalFloat("RTP_AGENT_STT_TEMPERATURE"),
		STTSkipVAD:                              getenvOptionalBool("RTP_AGENT_STT_SKIP_VAD"),
		STTVADKwargs:                            splitEnvMap("RTP_AGENT_STT_VAD_KWARGS"),
		STTTextTimeoutSeconds:                   getenvOptionalFloat("RTP_AGENT_STT_TEXT_TIMEOUT_SECONDS"),
		STTTimestampGranularities:               splitEnvList("RTP_AGENT_STT_TIMESTAMP_GRANULARITIES"),
		STTCodeSwitching:                        getenvOptionalBool("RTP_AGENT_STT_CODE_SWITCHING"),
		STTBitDepth:                             getenvOptionalInt("RTP_AGENT_STT_BIT_DEPTH"),
		STTEndpointingSeconds:                   getenvOptionalFloat("RTP_AGENT_STT_ENDPOINTING_SECONDS"),
		STTMaxDurationWithoutEndpointingSeconds: getenvOptionalFloat("RTP_AGENT_STT_MAX_DURATION_WITHOUT_ENDPOINTING_SECONDS"),
		STTRegion:                               os.Getenv("RTP_AGENT_STT_REGION"),
		STTCustomVocabulary:                     splitEnvAnyList("RTP_AGENT_STT_CUSTOM_VOCABULARY"),
		STTCustomSpelling:                       splitEnvStringSliceMap("RTP_AGENT_STT_CUSTOM_SPELLING"),
		STTTranslationTargetLanguages:           splitEnvList("RTP_AGENT_STT_TRANSLATION_TARGET_LANGUAGES"),
		STTTranslationSourceLanguages:           splitEnvList("RTP_AGENT_STT_TRANSLATION_SOURCE_LANGUAGES"),
		STTTranslationModel:                     os.Getenv("RTP_AGENT_STT_TRANSLATION_MODEL"),
		STTOutputLocale:                         os.Getenv("RTP_AGENT_STT_OUTPUT_LOCALE"),
		STTOperatingPoint:                       os.Getenv("RTP_AGENT_STT_OPERATING_POINT"),
		STTTranslationMatchOriginalUtterances:   getenvOptionalBool("RTP_AGENT_STT_TRANSLATION_MATCH_ORIGINAL_UTTERANCES"),
		STTTranslationLipsync:                   getenvOptionalBool("RTP_AGENT_STT_TRANSLATION_LIPSYNC"),
		STTTranslationContextAdaptation:         getenvOptionalBool("RTP_AGENT_STT_TRANSLATION_CONTEXT_ADAPTATION"),
		STTTranslationContext:                   os.Getenv("RTP_AGENT_STT_TRANSLATION_CONTEXT"),
		STTTranslationInformal:                  getenvOptionalBool("RTP_AGENT_STT_TRANSLATION_INFORMAL"),
		STTPreProcessingAudioEnhancer:           getenvOptionalBool("RTP_AGENT_STT_PRE_PROCESSING_AUDIO_ENHANCER"),
		STTPreProcessingSpeechThreshold:         getenvOptionalFloat("RTP_AGENT_STT_PRE_PROCESSING_SPEECH_THRESHOLD"),
		STTPrompt:                               os.Getenv("RTP_AGENT_STT_PROMPT"),
		STTBaseURL:                              os.Getenv("RTP_AGENT_STT_BASE_URL"),
		STTStreamingURL:                         os.Getenv("RTP_AGENT_STT_STREAMING_URL"),
		STTSampleRate:                           getenvOptionalInt("RTP_AGENT_STT_SAMPLE_RATE"),
		STTBufferSizeSeconds:                    getenvOptionalFloat("RTP_AGENT_STT_BUFFER_SIZE_SECONDS"),
		STTAudioChunkDurationMS:                 getenvOptionalInt("RTP_AGENT_STT_AUDIO_CHUNK_DURATION_MS"),
		STTMinTurnSilence:                       getenvOptionalInt("RTP_AGENT_STT_MIN_TURN_SILENCE"),
		STTMaxTurnSilence:                       getenvOptionalInt("RTP_AGENT_STT_MAX_TURN_SILENCE"),
		STTEndOfTurnConfidenceThreshold:         getenvOptionalFloat("RTP_AGENT_STT_END_OF_TURN_CONFIDENCE_THRESHOLD"),
		STTFormatTurns:                          getenvOptionalBool("RTP_AGENT_STT_FORMAT_TURNS"),
		STTLanguageDetection:                    getenvOptionalBool("RTP_AGENT_STT_LANGUAGE_DETECTION"),
		STTContinuousPartials:                   getenvOptionalBool("RTP_AGENT_STT_CONTINUOUS_PARTIALS"),
		STTInterruptionDelay:                    getenvOptionalInt("RTP_AGENT_STT_INTERRUPTION_DELAY"),
		STTKeytermsPrompt:                       splitEnvList("RTP_AGENT_STT_KEYTERMS_PROMPT"),
		STTVADThreshold:                         getenvOptionalFloat("RTP_AGENT_STT_VAD_THRESHOLD"),
		STTVADSilenceThresholdSeconds:           getenvOptionalFloat("RTP_AGENT_STT_VAD_SILENCE_THRESHOLD_SECONDS"),
		STTSpeakerLabels:                        getenvOptionalBool("RTP_AGENT_STT_SPEAKER_LABELS"),
		STTMaxSpeakers:                          getenvOptionalInt("RTP_AGENT_STT_MAX_SPEAKERS"),
		STTDomain:                               os.Getenv("RTP_AGENT_STT_DOMAIN"),
		STTVocabularyName:                       os.Getenv("RTP_AGENT_STT_VOCABULARY_NAME"),
		STTSessionID:                            os.Getenv("RTP_AGENT_STT_SESSION_ID"),
		STTVocabularyFilterMethod:               os.Getenv("RTP_AGENT_STT_VOCABULARY_FILTER_METHOD"),
		STTVocabularyFilterName:                 os.Getenv("RTP_AGENT_STT_VOCABULARY_FILTER_NAME"),
		STTEnableChannelIdentification:          getenvOptionalBool("RTP_AGENT_STT_ENABLE_CHANNEL_IDENTIFICATION"),
		STTNumberOfChannels:                     getenvOptionalInt("RTP_AGENT_STT_NUMBER_OF_CHANNELS"),
		STTEnablePartialStabilization:           getenvOptionalBool("RTP_AGENT_STT_ENABLE_PARTIAL_RESULTS_STABILIZATION"),
		STTPartialResultsStability:              os.Getenv("RTP_AGENT_STT_PARTIAL_RESULTS_STABILITY"),
		STTLanguageModelName:                    os.Getenv("RTP_AGENT_STT_LANGUAGE_MODEL_NAME"),
		STTIdentifyLanguage:                     getenvOptionalBool("RTP_AGENT_STT_IDENTIFY_LANGUAGE"),
		STTIdentifyMultipleLanguages:            getenvOptionalBool("RTP_AGENT_STT_IDENTIFY_MULTIPLE_LANGUAGES"),
		STTLanguageOptions:                      os.Getenv("RTP_AGENT_STT_LANGUAGE_OPTIONS"),
		STTPreferredLanguage:                    os.Getenv("RTP_AGENT_STT_PREFERRED_LANGUAGE"),
		STTVocabularyNames:                      os.Getenv("RTP_AGENT_STT_VOCABULARY_NAMES"),
		STTVocabularyFilterNames:                os.Getenv("RTP_AGENT_STT_VOCABULARY_FILTER_NAMES"),
		STTOrganizationID:                       os.Getenv("RTP_AGENT_STT_ORGANIZATION_ID"),
		STTUserID:                               os.Getenv("RTP_AGENT_STT_USER_ID"),
		STTVADBucket:                            getenvOptionalInt("RTP_AGENT_STT_VAD_BUCKET"),
		STTVADFlush:                             getenvOptionalBool("RTP_AGENT_STT_VAD_FLUSH"),
		STTVoiceProfile:                         getenvOptionalBool("RTP_AGENT_STT_VOICE_PROFILE"),
		STTVoiceProfileTopN:                     getenvOptionalInt("RTP_AGENT_STT_VOICE_PROFILE_TOP_N"),
		STTMinEndOfTurnSilenceWhenConfident:     getenvOptionalInt("RTP_AGENT_STT_MIN_END_OF_TURN_SILENCE_WHEN_CONFIDENT"),
		STTMinSpeakers:                          getenvOptionalInt("RTP_AGENT_STT_MIN_SPEAKERS"),
		STTModelOptions:                         splitEnvMap("RTP_AGENT_STT_MODEL_OPTIONS"),
		STTPositiveSpeechThreshold:              getenvOptionalFloat("RTP_AGENT_STT_POSITIVE_SPEECH_THRESHOLD"),
		STTNegativeSpeechThreshold:              getenvOptionalFloat("RTP_AGENT_STT_NEGATIVE_SPEECH_THRESHOLD"),
		STTMinSpeechFrames:                      getenvOptionalInt("RTP_AGENT_STT_MIN_SPEECH_FRAMES"),
		STTFirstTurnMinSpeechFrames:             getenvOptionalInt("RTP_AGENT_STT_FIRST_TURN_MIN_SPEECH_FRAMES"),
		STTNegativeFramesCount:                  getenvOptionalInt("RTP_AGENT_STT_NEGATIVE_FRAMES_COUNT"),
		STTNegativeFramesWindow:                 getenvOptionalInt("RTP_AGENT_STT_NEGATIVE_FRAMES_WINDOW"),
		STTStartSpeechVolumeThreshold:           getenvOptionalFloat("RTP_AGENT_STT_START_SPEECH_VOLUME_THRESHOLD"),
		STTInterruptMinSpeechFrames:             getenvOptionalInt("RTP_AGENT_STT_INTERRUPT_MIN_SPEECH_FRAMES"),
		STTPreSpeechPadFrames:                   getenvOptionalInt("RTP_AGENT_STT_PRE_SPEECH_PAD_FRAMES"),
		STTNumInitialIgnoredFrames:              getenvOptionalInt("RTP_AGENT_STT_NUM_INITIAL_IGNORED_FRAMES"),
		STTPreferCurrentSpeaker:                 getenvOptionalBool("RTP_AGENT_STT_PREFER_CURRENT_SPEAKER"),
		VADProvider:                             normalizedEnv("RTP_AGENT_VAD_PROVIDER"),
		VADMinSpeechDuration:                    getenvOptionalFloat("RTP_AGENT_VAD_MIN_SPEECH_DURATION"),
		VADMinSilenceDuration:                   getenvOptionalFloat("RTP_AGENT_VAD_MIN_SILENCE_DURATION"),
		VADPrefixPaddingDuration:                getenvOptionalFloat("RTP_AGENT_VAD_PREFIX_PADDING_DURATION"),
		VADPaddingDuration:                      getenvOptionalFloat("RTP_AGENT_VAD_PADDING_DURATION"),
		VADMaxBufferedSpeech:                    getenvOptionalFloat("RTP_AGENT_VAD_MAX_BUFFERED_SPEECH"),
		VADActivationThreshold:                  getenvOptionalFloat("RTP_AGENT_VAD_ACTIVATION_THRESHOLD"),
		VADDeactivationThreshold:                getenvOptionalFloat("RTP_AGENT_VAD_DEACTIVATION_THRESHOLD"),
		VADUpdateInterval:                       getenvOptionalFloat("RTP_AGENT_VAD_UPDATE_INTERVAL"),
		VADSampleRate:                           getenvOptionalInt("RTP_AGENT_VAD_SAMPLE_RATE"),
		AvatarProvider:                          normalizedEnv("RTP_AGENT_AVATAR_PROVIDER"),
		TurnDetectorProvider:                    normalizedEnv("RTP_AGENT_TURN_DETECTOR_PROVIDER"),
		BackgroundAudioAmbient:                  os.Getenv("RTP_AGENT_BACKGROUND_AUDIO_AMBIENT"),
		BackgroundAudioThinking:                 os.Getenv("RTP_AGENT_BACKGROUND_AUDIO_THINKING"),
		TTSProvider:                             normalizedEnv("RTP_AGENT_TTS_PROVIDER"),
		TTSFallbackProviders:                    splitEnvList("RTP_AGENT_TTS_FALLBACK_PROVIDERS"),
		TTSModel:                                os.Getenv("RTP_AGENT_TTS_MODEL"),
		TTSVoice:                                os.Getenv("RTP_AGENT_TTS_VOICE"),
		TTSRefAudio:                             os.Getenv("RTP_AGENT_TTS_REF_AUDIO"),
		TTSVoiceID:                              os.Getenv("RTP_AGENT_TTS_VOICE_ID"),
		TTSVoiceProvider:                        os.Getenv("RTP_AGENT_TTS_VOICE_PROVIDER"),
		TTSLanguage:                             os.Getenv("RTP_AGENT_TTS_LANGUAGE"),
		TTSEncoding:                             os.Getenv("RTP_AGENT_TTS_ENCODING"),
		TTSSampleRate:                           getenvOptionalInt("RTP_AGENT_TTS_SAMPLE_RATE"),
		TTSSpeed:                                getenvFloat("RTP_AGENT_TTS_SPEED"),
		TTSTemperature:                          getenvOptionalFloat("RTP_AGENT_TTS_TEMPERATURE"),
		TTSTopP:                                 getenvOptionalFloat("RTP_AGENT_TTS_TOP_P"),
		TTSMaxTokens:                            getenvOptionalInt("RTP_AGENT_TTS_MAX_TOKENS"),
		TTSBufferSize:                           getenvOptionalInt("RTP_AGENT_TTS_BUFFER_SIZE"),
		TTSEnhanceNamedEntities:                 getenvOptionalBool("RTP_AGENT_TTS_ENHANCE_NAMED_ENTITIES"),
		TTSEnableSSMLParsing:                    getenvOptionalBool("RTP_AGENT_TTS_ENABLE_SSML_PARSING"),
		TTSAPIVersion:                           os.Getenv("RTP_AGENT_TTS_API_VERSION"),
		TTSWordTimestamps:                       getenvOptionalBool("RTP_AGENT_TTS_WORD_TIMESTAMPS"),
		TTSVoiceEmbedding:                       splitEnvFloatList("RTP_AGENT_TTS_VOICE_EMBEDDING"),
		TTSEmotion:                              os.Getenv("RTP_AGENT_TTS_EMOTION"),
		TTSVolume:                               getenvOptionalFloat("RTP_AGENT_TTS_VOLUME"),
		TTSPronunciationDictID:                  os.Getenv("RTP_AGENT_TTS_PRONUNCIATION_DICT_ID"),
		TTSMIPOptOut:                            getenvOptionalBool("RTP_AGENT_TTS_MIP_OPT_OUT"),
		TTSLatencyMode:                          os.Getenv("RTP_AGENT_TTS_LATENCY_MODE"),
		TTSChunkLength:                          getenvOptionalInt("RTP_AGENT_TTS_CHUNK_LENGTH"),
		TTSInstructions:                         os.Getenv("RTP_AGENT_TTS_INSTRUCTIONS"),
		TTSResponseFormat:                       os.Getenv("RTP_AGENT_TTS_RESPONSE_FORMAT"),
		TTSBaseURL:                              os.Getenv("RTP_AGENT_TTS_BASE_URL"),
		TTSWebsocketURL:                         os.Getenv("RTP_AGENT_TTS_WEBSOCKET_URL"),
		TTSTextType:                             os.Getenv("RTP_AGENT_TTS_TEXT_TYPE"),
		TTSNumberOfChannels:                     getenvOptionalInt("RTP_AGENT_TTS_NUMBER_OF_CHANNELS"),
		TTSSampleWidth:                          getenvOptionalInt("RTP_AGENT_TTS_SAMPLE_WIDTH"),
		TTSJSONConfig:                           splitEnvMap("RTP_AGENT_TTS_JSON_CONFIG"),
		TTSBitRate:                              getenvOptionalInt("RTP_AGENT_TTS_BIT_RATE"),
		TTSSpeakingRate:                         getenvOptionalFloat("RTP_AGENT_TTS_SPEAKING_RATE"),
		TTSTrailingSilence:                      getenvOptionalFloat("RTP_AGENT_TTS_TRAILING_SILENCE"),
		TTSInstantMode:                          getenvOptionalBool("RTP_AGENT_TTS_INSTANT_MODE"),
		TTSPitch:                                getenvOptionalInt("RTP_AGENT_TTS_PITCH"),
		TTSTimestampType:                        os.Getenv("RTP_AGENT_TTS_TIMESTAMP_TYPE"),
		TTSLoudnessNormalization:                getenvOptionalBool("RTP_AGENT_TTS_LOUDNESS_NORMALIZATION"),
		TTSTextNormalization:                    getenvOptionalBool("RTP_AGENT_TTS_TEXT_NORMALIZATION"),
		TTSDeliveryMode:                         os.Getenv("RTP_AGENT_TTS_DELIVERY_MODE"),
		TTSTokenizerProvider:                    normalizedEnv("RTP_AGENT_TTS_TOKENIZER_PROVIDER"),
		TTSTokenizerLanguage:                    os.Getenv("RTP_AGENT_TTS_TOKENIZER_LANGUAGE"),
		TTSTokenizerMinSentenceLen:              getenvOptionalInt("RTP_AGENT_TTS_TOKENIZER_MIN_SENTENCE_LEN"),
		TTSTokenizerStreamContextLen:            getenvOptionalInt("RTP_AGENT_TTS_TOKENIZER_STREAM_CONTEXT_LEN"),
		TTSTextReplacements:                     splitEnvStringMap("RTP_AGENT_TTS_TEXT_REPLACEMENTS"),
		DisableTTSTextTransforms:                getenvBool("RTP_AGENT_DISABLE_TTS_TEXT_TRANSFORMS"),
		WordTokenizerProvider:                   normalizedEnv("RTP_AGENT_WORD_TOKENIZER_PROVIDER"),
		WordTokenizerLanguage:                   os.Getenv("RTP_AGENT_WORD_TOKENIZER_LANGUAGE"),
		TTSStreamPacerEnabled:                   getenvBool("RTP_AGENT_TTS_STREAM_PACER_ENABLED"),
		TTSStreamPacerMinRemainingAudioMS:       getenvOptionalInt("RTP_AGENT_TTS_STREAM_PACER_MIN_REMAINING_AUDIO_MS"),
		TTSStreamPacerMaxTextLength:             getenvOptionalInt("RTP_AGENT_TTS_STREAM_PACER_MAX_TEXT_LENGTH"),
		TTSTimestampTransportStrategy:           os.Getenv("RTP_AGENT_TTS_TIMESTAMP_TRANSPORT_STRATEGY"),
		TTSBufferCharThreshold:                  getenvOptionalInt("RTP_AGENT_TTS_BUFFER_CHAR_THRESHOLD"),
		TTSMaxBufferDelayMS:                     getenvOptionalInt("RTP_AGENT_TTS_MAX_BUFFER_DELAY_MS"),
		TTSContextGenerationID:                  os.Getenv("RTP_AGENT_TTS_CONTEXT_GENERATION_ID"),
		TTSContextUtterances:                    splitEnvHumeTTSUtterances("RTP_AGENT_TTS_CONTEXT_UTTERANCES"),
		TTSRegion:                               os.Getenv("RTP_AGENT_TTS_REGION"),
		TTSModelOptions:                         splitEnvMap("RTP_AGENT_TTS_MODEL_OPTIONS"),
		RealtimeProvider:                        normalizedEnv("RTP_AGENT_REALTIME_PROVIDER"),
		RealtimeModel:                           os.Getenv("RTP_AGENT_REALTIME_MODEL"),
		OpenAIAPIKey:                            os.Getenv("OPENAI_API_KEY"),
		AnamAPIKey:                              os.Getenv("ANAM_API_KEY"),
		AnthropicAPIKey:                         os.Getenv("ANTHROPIC_API_KEY"),
		AvatarioAPIKey:                          os.Getenv("AVATARIO_API_KEY"),
		AvatarTalkAPIKey:                        os.Getenv("AVATARTALK_API_KEY"),
		BeyAPIKey:                               os.Getenv("BEY_API_KEY"),
		BitHumanAPIKey:                          os.Getenv("BITHUMAN_API_KEY"),
		GoogleAPIKey:                            os.Getenv("GOOGLE_API_KEY"),
		ElevenLabsAPIKey:                        firstEnv("ELEVENLABS_API_KEY", "ELEVEN_API_KEY"),
		GroqAPIKey:                              os.Getenv("GROQ_API_KEY"),
		CerebrasAPIKey:                          os.Getenv("CEREBRAS_API_KEY"),
		ClovaSTTSecret:                          os.Getenv("CLOVA_STT_SECRET"),
		ClovaSTTInvokeURL:                       os.Getenv("CLOVA_STT_INVOKE_URL"),
		ClovaClientID:                           os.Getenv("CLOVA_CLIENT_ID"),
		ClovaClientSecret:                       os.Getenv("CLOVA_CLIENT_SECRET"),
		DIDAPIKey:                               os.Getenv("DID_API_KEY"),
		DIDAgentID:                              os.Getenv("DID_AGENT_ID"),
		FalAPIKey:                               firstEnv("FAL_KEY", "FAL_API_KEY"),
		FireworksAPIKey:                         os.Getenv("FIREWORKS_API_KEY"),
		FishAudioAPIKey:                         firstEnv("FISHAUDIO_API_KEY", "FISH_AUDIO_API_KEY"),
		GladiaAPIKey:                            os.Getenv("GLADIA_API_KEY"),
		GnaniAPIKey:                             os.Getenv("GNANI_API_KEY"),
		GradiumAPIKey:                           os.Getenv("GRADIUM_API_KEY"),
		HedraAPIKey:                             os.Getenv("HEDRA_API_KEY"),
		HumeAPIKey:                              os.Getenv("HUME_API_KEY"),
		InworldAPIKey:                           os.Getenv("INWORLD_API_KEY"),
		KeyframeAPIKey:                          os.Getenv("KEYFRAME_API_KEY"),
		LangChainAPIKey:                         os.Getenv("LANGCHAIN_API_KEY"),
		LemonSliceAPIKey:                        os.Getenv("LEMONSLICE_API_KEY"),
		LiveAvatarAPIKey:                        os.Getenv("LIVEAVATAR_API_KEY"),
		LMNTAPIKey:                              os.Getenv("LMNT_API_KEY"),
		MinimalAPIKey:                           os.Getenv("MINIMAL_API_KEY"),
		MinimaxAPIKey:                           os.Getenv("MINIMAX_API_KEY"),
		MistralAPIKey:                           os.Getenv("MISTRAL_API_KEY"),
		MurfAPIKey:                              os.Getenv("MURF_API_KEY"),
		NeuphonicAPIKey:                         os.Getenv("NEUPHONIC_API_KEY"),
		NvidiaAPIKey:                            os.Getenv("NVIDIA_API_KEY"),
		PerplexityAPIKey:                        os.Getenv("PERPLEXITY_API_KEY"),
		PhonicAPIKey:                            os.Getenv("PHONIC_API_KEY"),
		ResembleAPIKey:                          os.Getenv("RESEMBLE_API_KEY"),
		RespeecherAPIKey:                        os.Getenv("RESPEECHER_API_KEY"),
		RimeAPIKey:                              os.Getenv("RIME_API_KEY"),
		RtzrClientID:                            os.Getenv("RTZR_CLIENT_ID"),
		RtzrClientSecret:                        os.Getenv("RTZR_CLIENT_SECRET"),
		RtzrAccessToken:                         os.Getenv("RTZR_ACCESS_TOKEN"),
		RunwayAPISecret:                         os.Getenv("RUNWAYML_API_SECRET"),
		RunwayAvatarID:                          os.Getenv("RTP_AGENT_RUNWAY_AVATAR_ID"),
		RunwayPresetID:                          os.Getenv("RTP_AGENT_RUNWAY_PRESET_ID"),
		RunwayMaxDuration:                       getenvOptionalInt("RTP_AGENT_RUNWAY_MAX_DURATION"),
		SarvamAPIKey:                            os.Getenv("SARVAM_API_KEY"),
		SimliAPIKey:                             os.Getenv("SIMLI_API_KEY"),
		SimplismartAPIKey:                       os.Getenv("SIMPLISMART_API_KEY"),
		SmallestAIAPIKey:                        os.Getenv("SMALLESTAI_API_KEY"),
		SLNGAPIKey:                              os.Getenv("SLNG_API_KEY"),
		SonioxAPIKey:                            os.Getenv("SONIOX_API_KEY"),
		SpeechifyAPIKey:                         os.Getenv("SPEECHIFY_API_KEY"),
		SpeechmaticsAPIKey:                      os.Getenv("SPEECHMATICS_API_KEY"),
		SpitchAPIKey:                            os.Getenv("SPITCH_API_KEY"),
		TavusAPIKey:                             os.Getenv("TAVUS_API_KEY"),
		TelnyxAPIKey:                            os.Getenv("TELNYX_API_KEY"),
		TrugenAPIKey:                            os.Getenv("TRUGEN_API_KEY"),
		UltravoxAPIKey:                          os.Getenv("ULTRAVOX_API_KEY"),
		UpliftAIAPIKey:                          os.Getenv("UPLIFTAI_API_KEY"),
		XAIAPIKey:                               os.Getenv("XAI_API_KEY"),
		AnthropicTools:                          splitEnvList("RTP_AGENT_ANTHROPIC_TOOLS"),
		AnthropicComputerWidth:                  getenvOptionalInt("RTP_AGENT_ANTHROPIC_COMPUTER_WIDTH"),
		AnthropicComputerHeight:                 getenvOptionalInt("RTP_AGENT_ANTHROPIC_COMPUTER_HEIGHT"),
		XAITools:                                splitEnvList("RTP_AGENT_XAI_TOOLS"),
		XAIAllowedXHandles:                      splitEnvList("RTP_AGENT_XAI_ALLOWED_X_HANDLES"),
		XAIFileSearchVectorStoreIDs:             splitEnvList("RTP_AGENT_XAI_FILE_SEARCH_VECTOR_STORE_IDS"),
		XAIFileSearchMaxResults:                 getenvOptionalInt("RTP_AGENT_XAI_FILE_SEARCH_MAX_RESULTS"),
		GoogleCredentialsFile:                   firstEnv("RTP_AGENT_GOOGLE_CREDENTIALS_FILE", "GOOGLE_APPLICATION_CREDENTIALS"),
		LiveKitInferenceAPIKey:                  os.Getenv("LIVEKIT_API_KEY"),
		LiveKitInferenceAPISecret:               os.Getenv("LIVEKIT_API_SECRET"),
		AppTools:                                splitEnvList("RTP_AGENT_TOOLS"),
		MCPStdioServers:                         mcpStdioServersFromEnv("RTP_AGENT_MCP_STDIO_SERVERS"),
		MCPHTTPServers:                          mcpHTTPServersFromEnv("RTP_AGENT_MCP_HTTP_SERVERS"),
		IVRDetection:                            getenvBool("RTP_AGENT_IVR_DETECTION"),
		IVRSilenceDurationSeconds:               getenvOptionalFloat("RTP_AGENT_IVR_SILENCE_DURATION_SECONDS"),
		WorkflowTask:                            normalizedEnv("RTP_AGENT_WORKFLOW_TASK"),
		WorkflowRequireConfirmation:             getenvBool("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION"),
		WorkflowAddressPersona:                  os.Getenv("RTP_AGENT_WORKFLOW_ADDRESS_PERSONA"),
		WorkflowAddressExtraInstructions:        os.Getenv("RTP_AGENT_WORKFLOW_ADDRESS_EXTRA_INSTRUCTIONS"),
		WorkflowEmailPersona:                    os.Getenv("RTP_AGENT_WORKFLOW_EMAIL_PERSONA"),
		WorkflowEmailExtraInstructions:          os.Getenv("RTP_AGENT_WORKFLOW_EMAIL_EXTRA_INSTRUCTIONS"),
		WorkflowDtmfNumDigits:                   getenvOptionalInt("RTP_AGENT_WORKFLOW_DTMF_NUM_DIGITS"),
		WorkflowDtmfAskConfirmation:             getenvOptionalBool("RTP_AGENT_WORKFLOW_DTMF_ASK_CONFIRMATION"),
		WorkflowDtmfInputTimeoutSeconds:         getenvOptionalFloat("RTP_AGENT_WORKFLOW_DTMF_INPUT_TIMEOUT_SECONDS"),
		WorkflowDtmfStopEvent:                   os.Getenv("RTP_AGENT_WORKFLOW_DTMF_STOP_EVENT"),
		WorkflowDtmfExtraInstructions:           os.Getenv("RTP_AGENT_WORKFLOW_DTMF_EXTRA_INSTRUCTIONS"),
		WorkflowPhoneNumberExtraInstructions:    os.Getenv("RTP_AGENT_WORKFLOW_PHONE_NUMBER_EXTRA_INSTRUCTIONS"),
		WorkflowDOBExtraInstructions:            os.Getenv("RTP_AGENT_WORKFLOW_DOB_EXTRA_INSTRUCTIONS"),
		WorkflowDOBIncludeTime:                  getenvBool("RTP_AGENT_WORKFLOW_DOB_INCLUDE_TIME"),
		WorkflowNameFirstName:                   getenvOptionalBool("RTP_AGENT_WORKFLOW_NAME_FIRST_NAME"),
		WorkflowNameMiddleName:                  getenvOptionalBool("RTP_AGENT_WORKFLOW_NAME_MIDDLE_NAME"),
		WorkflowNameLastName:                    getenvOptionalBool("RTP_AGENT_WORKFLOW_NAME_LAST_NAME"),
		WorkflowNameFormat:                      os.Getenv("RTP_AGENT_WORKFLOW_NAME_FORMAT"),
		WorkflowNameVerifySpelling:              getenvBool("RTP_AGENT_WORKFLOW_NAME_VERIFY_SPELLING"),
		WorkflowNameExtraInstructions:           os.Getenv("RTP_AGENT_WORKFLOW_NAME_EXTRA_INSTRUCTIONS"),
		WorkflowWarmTransferSipCallTo:           os.Getenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CALL_TO"),
		WorkflowWarmTransferSipTrunkID:          os.Getenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_TRUNK_ID"),
		WorkflowWarmTransferSipConnection:       sipOutboundConfigFromEnv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CONNECTION_JSON"),
		WorkflowWarmTransferSipNumber:           os.Getenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_NUMBER"),
		WorkflowWarmTransferSipHeaders:          splitEnvStringMap("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_HEADERS"),
		WorkflowWarmTransferDTMF:                os.Getenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_DTMF"),
		WorkflowWarmTransferRingingTimeout:      getenvOptionalFloat("RTP_AGENT_WORKFLOW_WARM_TRANSFER_RINGING_TIMEOUT_SECONDS"),
		WorkflowWarmTransferHoldAudio:           os.Getenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_HOLD_AUDIO"),
		WorkflowWarmTransferDisableHoldAudio:    getenvBool("RTP_AGENT_WORKFLOW_WARM_TRANSFER_DISABLE_HOLD_AUDIO"),
		WorkflowWarmTransferPersona:             os.Getenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_PERSONA"),
		WorkflowWarmTransferExtraInstructions:   os.Getenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_EXTRA_INSTRUCTIONS"),
		WorkflowTaskGroupTasks:                  splitEnvList("RTP_AGENT_WORKFLOW_TASK_GROUP_TASKS"),
		EvalJudges:                              splitEnvList("RTP_AGENT_EVAL_JUDGES"),
	}
}

func Init(cfg AppConfig) (*App, error) {
	return NewApp(cfg)
}

func NewApp(cfg AppConfig) (*App, error) {
	if cfg.Logger != nil {
		logutil.SetLogger(cfg.Logger)
	}
	metricsRegistry := cfg.MetricsRegistry
	if metricsRegistry == nil {
		metricsRegistry = telemetry.NewMetricRegistry()
	}

	baseAgent := agent.NewAgent(cfg.Instructions)
	if baseAgent.Instructions == "" {
		baseAgent.Instructions = "You are a helpful realtime voice agent."
	}
	if len(cfg.InitialChatContext) > 0 {
		chatCtx, err := llm.ChatContextFromDict(cfg.InitialChatContext)
		if err != nil {
			return nil, fmt.Errorf("invalid RTP_AGENT_CHAT_CONTEXT_JSON: %w", err)
		}
		baseAgent.ChatCtx = chatCtx
	}

	if err := configureVAD(cfg, baseAgent); err != nil {
		return nil, err
	}
	realtimeModel, err := configureProviders(cfg, baseAgent)
	if err != nil {
		return nil, err
	}
	if err := configureSTTAdapters(cfg, baseAgent); err != nil {
		return nil, err
	}
	if err := configureTurnDetector(cfg, baseAgent); err != nil {
		return nil, err
	}
	if normalizeProvider(cfg.LLMProvider) == providerXAI {
		baseAgent.Tools = append(baseAgent.Tools, xaiProviderTools(cfg)...)
	}
	if normalizeProvider(cfg.LLMProvider) == providerAnthropic {
		baseAgent.Tools = append(baseAgent.Tools, anthropicProviderTools(cfg)...)
	}
	if err := configureAvatar(cfg, baseAgent); err != nil {
		return nil, err
	}
	sessionAgent, err := workflowAgentFromConfig(cfg, baseAgent)
	if err != nil {
		return nil, err
	}

	sessionOptions, err := agentSessionOptionsFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	session := agent.NewAgentSession(sessionAgent, nil, sessionOptions)
	if baseAgent.ChatCtx != nil {
		session.ChatCtx = baseAgent.ChatCtx.Copy()
	}
	if realtimeModel != nil {
		session.RealtimeModel = realtimeModel
		session.Assistant = agent.NewMultimodalAgent(realtimeModel, session.ChatCtx)
	}
	mcpServers, err := configureMCPTools(context.Background(), cfg, sessionAgent.GetAgent())
	if err != nil {
		return nil, err
	}
	session.SetMCPServers(mcpServers)
	if err := configureAppTools(cfg, sessionAgent.GetAgent(), session); err != nil {
		closeMCPServers(mcpServers)
		return nil, err
	}
	evaluator, err := evaluatorFromConfig(cfg, session.LLM)
	if err != nil {
		closeMCPServers(mcpServers)
		return nil, err
	}

	opts := cfg.WorkerOptions
	if opts.AgentName == "" {
		opts.AgentName = "example-agent"
	}
	if opts.WorkerType == "" {
		opts.WorkerType = worker.WorkerTypeRoom
	}
	session.MetricsCollector = metricsRegistry.GetUsageCollector(telemetry.MetricLabels{AgentName: opts.AgentName})
	server := worker.NewAgentServer(opts)

	app := &App{
		Server:          server,
		Agent:           sessionAgent.GetAgent(),
		Session:         session,
		RealtimeModel:   realtimeModel,
		Evaluator:       evaluator,
		MCPServers:      mcpServers,
		MetricsRegistry: metricsRegistry,
		Config:          cfg,
	}
	if cfg.TelemetryLogsEndpoint != "" {
		if err := appInitLoggerProvider(context.Background(), cfg.TelemetryLogsEndpoint, cfg.TelemetryLogsHeaders); err != nil {
			app.closeMCPServers()
			return nil, err
		}
		app.telemetryLogs = true
	}
	if err := server.RTCSession(app.runSession, nil, nil); err != nil {
		_ = app.Close(context.Background())
		return nil, err
	}
	return app, nil
}

func (a *App) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.closeMCPServers()
	if a.telemetryLogs {
		a.telemetryLogs = false
		return appShutdownLoggerProvider(ctx)
	}
	return nil
}

func (a *App) EvaluateSession(ctx context.Context, reference *llm.ChatContext) (*EvaluationSummary, error) {
	if a == nil || a.Evaluator == nil {
		return nil, fmt.Errorf("evaluation is not configured")
	}
	if a.Session == nil {
		return nil, fmt.Errorf("agent session is not configured")
	}
	ctx = a.evaluationContext(ctx)
	result, err := a.Evaluator.Evaluate(ctx, a.Session.ChatCtx, reference)
	if err != nil {
		return nil, err
	}
	return evaluationSummaryFromResult(result), nil
}

type evaluationTagger interface {
	Tagger() *agent.Tagger
}

func (a *App) evaluationContext(ctx context.Context) context.Context {
	jobCtx, err := a.Session.JobContext()
	if err != nil {
		return ctx
	}
	taggerSource, ok := jobCtx.(evaluationTagger)
	if !ok || taggerSource.Tagger() == nil {
		return ctx
	}
	return evals.WithEvaluationResultHandler(ctx, func(result *evals.EvaluationResult) {
		taggerSource.Tagger().Evaluation(evaluationResultForTagger(result))
	})
}

func evaluationResultForTagger(result *evals.EvaluationResult) *agent.EvaluationResult {
	out := &agent.EvaluationResult{Judgments: make(map[string]string)}
	if result == nil {
		return out
	}
	for name, judgment := range result.Judgments {
		if judgment != nil {
			out.Judgments[name] = string(judgment.Verdict)
		}
	}
	return out
}

func evaluationSummaryFromResult(result *evals.EvaluationResult) *EvaluationSummary {
	if result == nil {
		result = &evals.EvaluationResult{}
	}
	return &EvaluationSummary{
		Result:         result,
		Score:          result.Score(),
		AllPassed:      result.AllPassed(),
		AnyPassed:      result.AnyPassed(),
		MajorityPassed: result.MajorityPassed(),
		NoneFailed:     result.NoneFailed(),
	}
}

func workflowAgentFromConfig(cfg AppConfig, baseAgent *agent.Agent) (agent.AgentInterface, error) {
	task := normalizeProvider(cfg.WorkflowTask)
	if task == "" {
		return baseAgent, nil
	}

	var selected agent.AgentInterface
	switch task {
	case "address", "get_address":
		selected = workflows.NewGetAddressTask(workflows.GetAddressOptions{
			RequireConfirmation:    cfg.WorkflowRequireConfirmation,
			RequireConfirmationSet: true,
			Instructions:           workflowInstructionParts(cfg.WorkflowAddressPersona, cfg.WorkflowAddressExtraInstructions),
		})
	case "email", "get_email":
		selected = workflows.NewGetEmailTask(workflows.GetEmailOptions{
			RequireConfirmation:    cfg.WorkflowRequireConfirmation,
			RequireConfirmationSet: true,
			Instructions:           workflowInstructionParts(cfg.WorkflowEmailPersona, cfg.WorkflowEmailExtraInstructions),
		})
	case "phone_number", "phone-number", "phone", "get_phone_number":
		selected = workflows.NewGetPhoneNumberTask(workflows.GetPhoneNumberOptions{
			RequireConfirmation:    cfg.WorkflowRequireConfirmation,
			RequireConfirmationSet: true,
			ExtraInstructions:      cfg.WorkflowPhoneNumberExtraInstructions,
		})
	case "dob", "date_of_birth", "date-of-birth", "get_dob":
		selected = workflows.NewGetDOBTask(workflows.GetDOBOptions{
			RequireConfirmation:    cfg.WorkflowRequireConfirmation,
			RequireConfirmationSet: true,
			ExtraInstructions:      cfg.WorkflowDOBExtraInstructions,
			IncludeTime:            cfg.WorkflowDOBIncludeTime,
		})
	case "name", "get_name":
		nameOpts, err := workflowNameOptionsFromConfig(cfg)
		if err != nil {
			return nil, err
		}
		selected = workflows.NewGetNameTask(nameOpts)
	case "card_number", "card-number", "get_card_number":
		selected = workflows.NewGetCardNumberTask(cfg.WorkflowRequireConfirmation)
	case "security_code", "security-code", "get_security_code":
		selected = workflows.NewGetSecurityCodeTask(cfg.WorkflowRequireConfirmation)
	case "expiration_date", "expiration-date", "get_expiration_date":
		selected = workflows.NewGetExpirationDateTask(cfg.WorkflowRequireConfirmation)
	case "credit_card", "credit-card", "get_credit_card":
		selected = workflows.NewGetCreditCardTask(cfg.WorkflowRequireConfirmation)
	case "dtmf", "get_dtmf":
		task, err := workflows.NewGetDtmfTaskWithOptions(workflowDtmfOptionsFromConfig(cfg))
		if err != nil {
			return nil, err
		}
		selected = task
	case "warm_transfer", "warm-transfer":
		sipCallTo := strings.TrimSpace(cfg.WorkflowWarmTransferSipCallTo)
		if sipCallTo == "" {
			return nil, fmt.Errorf("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CALL_TO is required for warm_transfer workflow")
		}
		task, err := workflows.NewWarmTransferTaskWithOptions(workflows.WarmTransferOptions{
			TargetPhone:      sipCallTo,
			TrunkID:          strings.TrimSpace(cfg.WorkflowWarmTransferSipTrunkID),
			SipConnection:    cfg.WorkflowWarmTransferSipConnection,
			SipNumber:        cfg.WorkflowWarmTransferSipNumber,
			HoldAudio:        workflowWarmTransferHoldAudio(cfg),
			DisableHoldAudio: cfg.WorkflowWarmTransferDisableHoldAudio,
			ChatContext:      baseAgent.ChatCtx,
			Instructions:     workflowInstructionParts(cfg.WorkflowWarmTransferPersona, cfg.WorkflowWarmTransferExtraInstructions),
		})
		if err != nil {
			return nil, err
		}
		applyWarmTransferOptions(task, cfg)
		selected = task
	case "task_group", "task-group":
		selectedGroup, err := workflowTaskGroupFromConfig(cfg, baseAgent)
		if err != nil {
			return nil, err
		}
		selected = selectedGroup
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_WORKFLOW_TASK %q", cfg.WorkflowTask)
	}
	copyAgentRuntime(selected.GetAgent(), baseAgent)
	return selected, nil
}

func workflowTaskGroupFromConfig(cfg AppConfig, baseAgent *agent.Agent) (*workflows.TaskGroup, error) {
	if len(cfg.WorkflowTaskGroupTasks) == 0 {
		return nil, fmt.Errorf("RTP_AGENT_WORKFLOW_TASK_GROUP_TASKS is required for task_group workflow")
	}
	group := workflows.NewTaskGroup(true, false)
	for _, taskName := range cfg.WorkflowTaskGroupTasks {
		info, err := workflowTaskFactoryFromName(cfg, baseAgent, taskName)
		if err != nil {
			return nil, err
		}
		group.Add(info.ID, info.Description, info.TaskFactory)
	}
	return group, nil
}

func workflowInstructionParts(persona, extra string) *beta.InstructionParts {
	persona = strings.TrimSpace(persona)
	extra = strings.TrimSpace(extra)
	if persona == "" && extra == "" {
		return nil
	}
	parts := &beta.InstructionParts{Extra: extra}
	if persona != "" {
		parts.Persona = &persona
	}
	return parts
}

func workflowNameOptionsFromConfig(cfg AppConfig) (workflows.GetNameOptions, error) {
	firstName := true
	if cfg.WorkflowNameFirstName != nil {
		firstName = *cfg.WorkflowNameFirstName
	}
	middleName := false
	if cfg.WorkflowNameMiddleName != nil {
		middleName = *cfg.WorkflowNameMiddleName
	}
	lastName := true
	if cfg.WorkflowNameLastName != nil {
		lastName = *cfg.WorkflowNameLastName
	}
	if !firstName && !middleName && !lastName {
		return workflows.GetNameOptions{}, fmt.Errorf("%s", "At least one of first_name, middle_name, or last_name must be True")
	}
	return workflows.GetNameOptions{
		FirstName:              firstName,
		MiddleName:             middleName,
		LastName:               lastName,
		NameFormat:             cfg.WorkflowNameFormat,
		VerifySpelling:         cfg.WorkflowNameVerifySpelling,
		RequireConfirmation:    cfg.WorkflowRequireConfirmation,
		RequireConfirmationSet: true,
		ExtraInstructions:      cfg.WorkflowNameExtraInstructions,
	}, nil
}

func workflowDtmfOptionsFromConfig(cfg AppConfig) workflows.GetDtmfOptions {
	numDigits := 1
	if cfg.WorkflowDtmfNumDigits != nil {
		numDigits = *cfg.WorkflowDtmfNumDigits
	}
	askConfirmation := cfg.WorkflowRequireConfirmation
	if cfg.WorkflowDtmfAskConfirmation != nil {
		askConfirmation = *cfg.WorkflowDtmfAskConfirmation
	}
	opts := workflows.GetDtmfOptions{
		NumDigits:          numDigits,
		AskForConfirmation: askConfirmation,
		ExtraInstructions:  cfg.WorkflowDtmfExtraInstructions,
	}
	if cfg.WorkflowDtmfInputTimeoutSeconds != nil {
		opts.DtmfInputTimeout = time.Duration(*cfg.WorkflowDtmfInputTimeoutSeconds * float64(time.Second))
	}
	if stopEvent := strings.TrimSpace(cfg.WorkflowDtmfStopEvent); stopEvent != "" {
		opts.DtmfStopEvent = beta.DtmfEvent(stopEvent)
	}
	return opts
}

func workflowWarmTransferHoldAudio(cfg AppConfig) interface{} {
	if cfg.WorkflowWarmTransferDisableHoldAudio {
		return nil
	}
	return backgroundAudioSource(cfg.WorkflowWarmTransferHoldAudio)
}

func workflowTaskFactoryFromName(cfg AppConfig, baseAgent *agent.Agent, taskName string) (workflows.FactoryInfo, error) {
	task := normalizeProvider(taskName)
	factory := func(taskFactory func() agent.AgentInterface) func() agent.AgentInterface {
		return func() agent.AgentInterface {
			selected := taskFactory()
			copyAgentRuntime(selected.GetAgent(), baseAgent)
			return selected
		}
	}
	switch task {
	case "address", "get_address":
		return workflows.FactoryInfo{
			ID:          "address",
			Description: "Collect and confirm the user's mailing address.",
			TaskFactory: factory(func() agent.AgentInterface {
				return workflows.NewGetAddressTask(workflows.GetAddressOptions{
					RequireConfirmation:    cfg.WorkflowRequireConfirmation,
					RequireConfirmationSet: true,
					Instructions:           workflowInstructionParts(cfg.WorkflowAddressPersona, cfg.WorkflowAddressExtraInstructions),
				})
			}),
		}, nil
	case "email", "get_email":
		return workflows.FactoryInfo{
			ID:          "email",
			Description: "Collect and confirm the user's email address.",
			TaskFactory: factory(func() agent.AgentInterface {
				return workflows.NewGetEmailTask(workflows.GetEmailOptions{
					RequireConfirmation:    cfg.WorkflowRequireConfirmation,
					RequireConfirmationSet: true,
					Instructions:           workflowInstructionParts(cfg.WorkflowEmailPersona, cfg.WorkflowEmailExtraInstructions),
				})
			}),
		}, nil
	case "phone_number", "phone-number", "phone", "get_phone_number":
		return workflows.FactoryInfo{
			ID:          "phone_number",
			Description: "Collect and confirm the user's phone number.",
			TaskFactory: factory(func() agent.AgentInterface {
				return workflows.NewGetPhoneNumberTask(workflows.GetPhoneNumberOptions{
					RequireConfirmation:    cfg.WorkflowRequireConfirmation,
					RequireConfirmationSet: true,
					ExtraInstructions:      cfg.WorkflowPhoneNumberExtraInstructions,
				})
			}),
		}, nil
	case "dob", "date_of_birth", "date-of-birth", "get_dob":
		return workflows.FactoryInfo{
			ID:          "dob",
			Description: "Collect and confirm the user's date of birth.",
			TaskFactory: factory(func() agent.AgentInterface {
				return workflows.NewGetDOBTask(workflows.GetDOBOptions{
					RequireConfirmation:    cfg.WorkflowRequireConfirmation,
					RequireConfirmationSet: true,
					ExtraInstructions:      cfg.WorkflowDOBExtraInstructions,
					IncludeTime:            cfg.WorkflowDOBIncludeTime,
				})
			}),
		}, nil
	case "name", "get_name":
		nameOpts, err := workflowNameOptionsFromConfig(cfg)
		if err != nil {
			return workflows.FactoryInfo{}, err
		}
		return workflows.FactoryInfo{
			ID:          "name",
			Description: "Collect and confirm the user's name.",
			TaskFactory: factory(func() agent.AgentInterface {
				return workflows.NewGetNameTask(nameOpts)
			}),
		}, nil
	case "card_number", "card-number", "get_card_number":
		return workflows.FactoryInfo{
			ID:          "card_number",
			Description: "Collect and validate the user's credit card number.",
			TaskFactory: factory(func() agent.AgentInterface {
				return workflows.NewGetCardNumberTask(cfg.WorkflowRequireConfirmation)
			}),
		}, nil
	case "security_code", "security-code", "get_security_code":
		return workflows.FactoryInfo{
			ID:          "security_code",
			Description: "Collect and validate the user's card security code.",
			TaskFactory: factory(func() agent.AgentInterface {
				return workflows.NewGetSecurityCodeTask(cfg.WorkflowRequireConfirmation)
			}),
		}, nil
	case "expiration_date", "expiration-date", "get_expiration_date":
		return workflows.FactoryInfo{
			ID:          "expiration_date",
			Description: "Collect and validate the user's card expiration date.",
			TaskFactory: factory(func() agent.AgentInterface {
				return workflows.NewGetExpirationDateTask(cfg.WorkflowRequireConfirmation)
			}),
		}, nil
	case "credit_card", "credit-card", "get_credit_card":
		return workflows.FactoryInfo{
			ID:          "credit_card",
			Description: "Collect and validate the user's credit card details.",
			TaskFactory: factory(func() agent.AgentInterface {
				return workflows.NewGetCreditCardTask(cfg.WorkflowRequireConfirmation)
			}),
		}, nil
	case "dtmf", "get_dtmf":
		opts := workflowDtmfOptionsFromConfig(cfg)
		if err := workflows.ValidateDtmfNumDigits(opts.NumDigits); err != nil {
			return workflows.FactoryInfo{}, err
		}
		return workflows.FactoryInfo{
			ID:          "dtmf",
			Description: "Collect DTMF inputs from the user.",
			TaskFactory: factory(func() agent.AgentInterface {
				task, err := workflows.NewGetDtmfTaskWithOptions(opts)
				if err != nil {
					panic(fmt.Sprintf("validated DTMF task config rejected: %v", err))
				}
				return task
			}),
		}, nil
	case "warm_transfer", "warm-transfer":
		sipCallTo := strings.TrimSpace(cfg.WorkflowWarmTransferSipCallTo)
		if sipCallTo == "" {
			return workflows.FactoryInfo{}, fmt.Errorf("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CALL_TO is required for warm_transfer task group entry")
		}
		sipTrunkID := strings.TrimSpace(cfg.WorkflowWarmTransferSipTrunkID)
		if sipTrunkID == "" && cfg.WorkflowWarmTransferSipConnection == nil {
			sipTrunkID = strings.TrimSpace(os.Getenv("LIVEKIT_SIP_OUTBOUND_TRUNK"))
		}
		if _, err := workflows.NewWarmTransferTaskWithOptions(workflows.WarmTransferOptions{
			TargetPhone:      sipCallTo,
			TrunkID:          sipTrunkID,
			SipConnection:    cfg.WorkflowWarmTransferSipConnection,
			SipNumber:        cfg.WorkflowWarmTransferSipNumber,
			HoldAudio:        workflowWarmTransferHoldAudio(cfg),
			DisableHoldAudio: cfg.WorkflowWarmTransferDisableHoldAudio,
			ChatContext:      baseAgent.ChatCtx,
			Instructions:     workflowInstructionParts(cfg.WorkflowWarmTransferPersona, cfg.WorkflowWarmTransferExtraInstructions),
		}); err != nil {
			return workflows.FactoryInfo{}, err
		}
		return workflows.FactoryInfo{
			ID:          "warm_transfer",
			Description: "Transfer the caller to a human agent by SIP.",
			TaskFactory: factory(func() agent.AgentInterface {
				task, err := workflows.NewWarmTransferTaskWithOptions(workflows.WarmTransferOptions{
					TargetPhone:      sipCallTo,
					TrunkID:          sipTrunkID,
					SipConnection:    cfg.WorkflowWarmTransferSipConnection,
					SipNumber:        cfg.WorkflowWarmTransferSipNumber,
					HoldAudio:        workflowWarmTransferHoldAudio(cfg),
					DisableHoldAudio: cfg.WorkflowWarmTransferDisableHoldAudio,
					ChatContext:      baseAgent.ChatCtx,
					Instructions:     workflowInstructionParts(cfg.WorkflowWarmTransferPersona, cfg.WorkflowWarmTransferExtraInstructions),
				})
				if err != nil {
					panic(fmt.Sprintf("validated warm transfer task config rejected: %v", err))
				}
				applyWarmTransferOptions(task, cfg)
				return task
			}),
		}, nil
	default:
		return workflows.FactoryInfo{}, fmt.Errorf("unsupported RTP_AGENT_WORKFLOW_TASK_GROUP_TASKS entry %q", taskName)
	}
}

func applyWarmTransferOptions(task *workflows.WarmTransferTask, cfg AppConfig) {
	if len(cfg.WorkflowWarmTransferSipHeaders) > 0 {
		task.SipHeaders = cfg.WorkflowWarmTransferSipHeaders
	}
	task.Dtmf = strings.TrimSpace(cfg.WorkflowWarmTransferDTMF)
	if cfg.WorkflowWarmTransferRingingTimeout != nil {
		task.RingingTimeout = time.Duration(*cfg.WorkflowWarmTransferRingingTimeout * float64(time.Second))
	}
}

func evaluatorFromConfig(cfg AppConfig, evaluatorLLM llm.LLM) (*evals.JudgeGroup, error) {
	if len(cfg.EvalJudges) == 0 {
		return nil, nil
	}
	judges := make([]evals.Evaluator, 0, len(cfg.EvalJudges))
	for _, judgeName := range cfg.EvalJudges {
		judge, err := evalJudgeFromName(judgeName, evaluatorLLM)
		if err != nil {
			return nil, err
		}
		judges = append(judges, judge)
	}
	return evals.NewJudgeGroup(evaluatorLLM, judges), nil
}

func evalJudgeFromName(name string, evaluatorLLM llm.LLM) (evals.Evaluator, error) {
	switch normalizeProvider(name) {
	case "task_completion", "task-completion":
		return evals.TaskCompletionJudge(evaluatorLLM), nil
	case "accuracy":
		return evals.AccuracyJudge(evaluatorLLM), nil
	case "relevancy", "relevance":
		return evals.RelevancyJudge(evaluatorLLM), nil
	case "safety":
		return evals.SafetyJudge(evaluatorLLM), nil
	case "coherence":
		return evals.CoherenceJudge(evaluatorLLM), nil
	case "conciseness":
		return evals.ConcisenessJudge(evaluatorLLM), nil
	case "handoff":
		return evals.HandoffJudge(evaluatorLLM), nil
	case "tool_use", "tool-use":
		return evals.ToolUseJudge(evaluatorLLM), nil
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_EVAL_JUDGES entry %q", name)
	}
}

func copyAgentRuntime(dst *agent.Agent, src *agent.Agent) {
	if dst == nil || src == nil {
		return
	}
	dst.ChatCtx = src.ChatCtx
	dst.TurnDetection = src.TurnDetection
	dst.TurnDetector = src.TurnDetector
	dst.Avatar = src.Avatar
	dst.STT = src.STT
	dst.VAD = src.VAD
	dst.LLM = src.LLM
	dst.TTS = src.TTS
	dst.AllowInterruptions = src.AllowInterruptions
	dst.MinConsecutiveSpeechDelay = src.MinConsecutiveSpeechDelay
	dst.UseTTSAlignedTranscript = src.UseTTSAlignedTranscript
	dst.MinEndpointingDelay = src.MinEndpointingDelay
	dst.MaxEndpointingDelay = src.MaxEndpointingDelay
	dst.Tools = append(src.Tools, dst.Tools...)
}

func (a *App) runSession(ctx *worker.JobContext) error {
	if a.Session == nil {
		return fmt.Errorf("agent session is not configured")
	}
	defer a.closeMCPServers()
	if ctx != nil {
		ctx.SetPrimarySession(a.Session)
		a.Session.SetJobContext(ctx)
	}
	a.configureMetricsCollector(ctx)
	a.Server.SetConsoleSession(a.Session)
	if a.Session.STT == nil && a.Session.LLM == nil && a.Session.TTS == nil && a.RealtimeModel == nil {
		if ctx != nil {
			if _, err := ctx.MakeSessionReport(a.Session); err != nil {
				return err
			}
		}
		return nil
	}
	if ctx != nil {
		roomOptions := a.RoomOptions
		if roomOptions.DeleteRoom == nil {
			roomOptions.DeleteRoom = func(deleteCtx context.Context, roomName string) error {
				_, err := ctx.DeleteRoom(deleteCtx, roomName)
				return err
			}
		}
		var roomIO *worker.RoomIO
		if ctx.Room == nil {
			roomIO = worker.NewRoomIO(nil, a.Session, roomOptions)
			if err := ctx.Connect(context.Background(), roomIO.GetCallback()); err != nil {
				_ = roomIO.Close()
				return err
			}
			roomIO.AttachRoom(ctx.Room)
		}
		if ctx.Room != nil {
			a.Session.Room = ctx.Room
			if roomIO == nil {
				roomIO = worker.NewRoomIO(ctx.Room, a.Session, roomOptions)
			}
			a.RoomIO = roomIO
			if err := a.startAudioRecorder(ctx, roomIO); err != nil {
				return err
			}
			if err := configureRoomTools(a.Config, a.Agent, roomIO); err != nil {
				return err
			}
			if ctx.Room.LocalParticipant != nil && ctx.Room.ConnectionState() == lksdk.ConnectionStateConnected {
				if err := roomIO.Start(context.Background()); err != nil {
					return err
				}
			}
		}
	}
	sessionCtx := context.Background()
	if ctx != nil {
		info := ctx.AvatarStartInfo()
		if info.LiveKitURL != "" && info.LiveKitToken != "" {
			sessionCtx = agent.ContextWithAvatarStartInfo(sessionCtx, info)
		}
	}
	if err := a.Session.Start(sessionCtx); err != nil {
		return err
	}
	if ctx != nil {
		a.populateRecorderSessionReport(ctx)
		if _, err := ctx.MakeSessionReport(a.Session); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) startAudioRecorder(ctx *worker.JobContext, roomIO *worker.RoomIO) error {
	if ctx == nil || roomIO == nil || roomIO.Recorder == nil || ctx.Report == nil {
		return nil
	}
	if !ctx.Report.RecordingOptions.Audio || ctx.SessionDirectory() == "" {
		return nil
	}
	return roomIO.Recorder.Start(filepath.Join(ctx.SessionDirectory(), "audio.ogg"), 48000)
}

func (a *App) populateRecorderSessionReport(ctx *worker.JobContext) {
	if ctx == nil || ctx.Report == nil || a == nil || a.RoomIO == nil || a.RoomIO.Recorder == nil {
		return
	}
	a.RoomIO.Recorder.PopulateSessionReport(ctx.Report)
}

func (a *App) configureMetricsCollector(ctx *worker.JobContext) {
	if a == nil || a.Session == nil || a.Server == nil || a.MetricsRegistry == nil {
		return
	}
	labels := telemetry.MetricLabels{AgentName: a.Server.Options.AgentName}
	if ctx != nil && ctx.Job != nil {
		if room := ctx.Job.GetRoom(); room != nil {
			labels.RoomName = room.GetName()
		}
		labels.ParticipantIdentity = ctx.ParticipantIdentity()
	}
	a.Session.MetricsCollector = a.MetricsRegistry.GetUsageCollector(labels)
}

func (a *App) closeMCPServers() {
	if a == nil {
		return
	}
	closeMCPServers(a.MCPServers)
	a.MCPServers = nil
}

func closeMCPServers(servers []llm.MCPServer) {
	for _, server := range servers {
		if server != nil {
			_ = server.Close()
		}
	}
}

func configureAvatar(cfg AppConfig, a *agent.Agent) error {
	switch normalizeProvider(cfg.AvatarProvider) {
	case "":
		return nil
	case providerAnam:
		a.Avatar = anam.NewAnamAvatar(cfg.AnamAPIKey)
		return nil
	case providerAvatario:
		a.Avatar = avatario.NewAvatarioAvatar(cfg.AvatarioAPIKey)
		return nil
	case providerAvatarTalk:
		a.Avatar = avatartalk.NewAvatartalkAvatar(cfg.AvatarTalkAPIKey)
		return nil
	case providerBey:
		avatar, err := bey.NewBeyAvatar(cfg.BeyAPIKey)
		if err != nil {
			return err
		}
		a.Avatar = avatar
		return nil
	case providerBitHuman:
		a.Avatar = bithuman.NewBithumanAvatar(cfg.BitHumanAPIKey)
		return nil
	case providerDID:
		a.Avatar = did.NewDIDAvatar(cfg.DIDAgentID, cfg.DIDAPIKey)
		return nil
	case providerHedra:
		a.Avatar = hedra.NewHedraAvatar(cfg.HedraAPIKey)
		return nil
	case providerKeyframe:
		a.Avatar = keyframe.NewKeyframeAgent(cfg.KeyframeAPIKey)
		return nil
	case providerLemonSlice:
		a.Avatar = lemonslice.NewLemonsliceAvatar(cfg.LemonSliceAPIKey)
		return nil
	case providerLiveAvatar:
		a.Avatar = liveavatar.NewLiveAvatar(cfg.LiveAvatarAPIKey)
		return nil
	case providerRunway:
		opts := []runway.RunwayAvatarOption{}
		if cfg.RunwayAvatarID != "" {
			opts = append(opts, runway.WithRunwayAvatarID(cfg.RunwayAvatarID))
		}
		if cfg.RunwayPresetID != "" {
			opts = append(opts, runway.WithRunwayPresetID(cfg.RunwayPresetID))
		}
		if cfg.RunwayMaxDuration != nil {
			opts = append(opts, runway.WithRunwayMaxDuration(*cfg.RunwayMaxDuration))
		}
		avatar, err := runway.NewRunwayAvatar(cfg.RunwayAPISecret, opts...)
		if err != nil {
			return err
		}
		a.Avatar = avatar
		return nil
	case providerSimli:
		a.Avatar = simli.NewSimliAvatar(cfg.SimliAPIKey)
		return nil
	case providerTavus:
		a.Avatar = tavus.NewTavusAvatar(cfg.TavusAPIKey)
		return nil
	case providerTrugen:
		a.Avatar = trugen.NewTrugenAvatar(cfg.TrugenAPIKey)
		return nil
	default:
		return fmt.Errorf("unsupported RTP_AGENT_AVATAR_PROVIDER %q", cfg.AvatarProvider)
	}
}

func configureVAD(cfg AppConfig, a *agent.Agent) error {
	switch normalizeProvider(cfg.VADProvider) {
	case "":
		return nil
	case providerSilero:
		vadOpts := []silero.VADOption{}
		if cfg.VADMinSpeechDuration != nil {
			vadOpts = append(vadOpts, silero.WithMinSpeechDuration(*cfg.VADMinSpeechDuration))
		}
		if cfg.VADMinSilenceDuration != nil {
			vadOpts = append(vadOpts, silero.WithMinSilenceDuration(*cfg.VADMinSilenceDuration))
		}
		if cfg.VADPrefixPaddingDuration != nil {
			vadOpts = append(vadOpts, silero.WithPrefixPaddingDuration(*cfg.VADPrefixPaddingDuration))
		}
		if cfg.VADPaddingDuration != nil {
			vadOpts = append(vadOpts, silero.WithPaddingDuration(*cfg.VADPaddingDuration))
		}
		if cfg.VADMaxBufferedSpeech != nil {
			vadOpts = append(vadOpts, silero.WithMaxBufferedSpeech(*cfg.VADMaxBufferedSpeech))
		}
		if cfg.VADActivationThreshold != nil {
			vadOpts = append(vadOpts, silero.WithActivationThreshold(*cfg.VADActivationThreshold))
		}
		if cfg.VADDeactivationThreshold != nil {
			vadOpts = append(vadOpts, silero.WithDeactivationThreshold(*cfg.VADDeactivationThreshold))
		}
		if cfg.VADSampleRate != nil {
			vadOpts = append(vadOpts, silero.WithSampleRate(*cfg.VADSampleRate))
		}
		if cfg.VADUpdateInterval != nil {
			vadOpts = append(vadOpts, silero.WithUpdateInterval(*cfg.VADUpdateInterval))
		}
		if len(vadOpts) == 0 {
			a.VAD = silero.NewSileroVAD()
			return nil
		}
		detector, err := silero.NewSileroVADWithOptions(vadOpts...)
		if err != nil {
			return err
		}
		a.VAD = detector
		return nil
	default:
		return fmt.Errorf("unsupported RTP_AGENT_VAD_PROVIDER %q", cfg.VADProvider)
	}
}

func configureTurnDetector(cfg AppConfig, a *agent.Agent) error {
	switch normalizeProvider(cfg.TurnDetectorProvider) {
	case "":
		return nil
	case "llm":
		if a.LLM == nil {
			return fmt.Errorf("RTP_AGENT_TURN_DETECTOR_PROVIDER=llm requires RTP_AGENT_LLM_PROVIDER")
		}
		a.TurnDetector = agent.NewLLMTurnDetector(a.LLM)
		return nil
	default:
		return fmt.Errorf("unsupported RTP_AGENT_TURN_DETECTOR_PROVIDER %q", cfg.TurnDetectorProvider)
	}
}

func configureSTTAdapters(cfg AppConfig, a *agent.Agent) error {
	if cfg.STTMultiSpeaker == nil || !*cfg.STTMultiSpeaker {
		return nil
	}
	if a.STT == nil {
		return fmt.Errorf("RTP_AGENT_STT_MULTI_SPEAKER=true requires RTP_AGENT_STT_PROVIDER")
	}
	adapter, err := corestt.NewDefaultMultiSpeakerAdapter(a.STT)
	if err != nil {
		return err
	}
	a.STT = adapter
	return nil
}

func configureLLMFallbacks(cfg AppConfig, a *agent.Agent) error {
	if len(cfg.LLMFallbackProviders) == 0 {
		return nil
	}
	if a.LLM == nil {
		return fmt.Errorf("RTP_AGENT_LLM_FALLBACK_PROVIDERS requires RTP_AGENT_LLM_PROVIDER")
	}
	llms := make([]llm.LLM, 0, len(cfg.LLMFallbackProviders)+1)
	llms = append(llms, a.LLM)
	for _, provider := range cfg.LLMFallbackProviders {
		fallback, err := fallbackLLMFromProvider(cfg, provider)
		if err != nil {
			return err
		}
		llms = append(llms, fallback)
	}
	a.LLM = llm.NewFallbackAdapter(llms)
	return nil
}

func fallbackLLMFromProvider(cfg AppConfig, provider string) (llm.LLM, error) {
	switch normalizeProvider(provider) {
	case providerMinimal:
		return minimal.NewMinimalLLM(cfg.MinimalAPIKey, cfg.LLMModel), nil
	case providerOpenAI:
		return openai.NewOpenAILLM(cfg.OpenAIAPIKey, cfg.LLMModel)
	case providerGroq:
		return groq.NewGroqLLM(cfg.GroqAPIKey, cfg.LLMModel), nil
	case providerXAI:
		return xai.NewXaiLLM(cfg.XAIAPIKey, cfg.LLMModel), nil
	case providerLiveKit:
		return openai.NewLiveKitInferenceLLM(cfg.LLMModel, cfg.LiveKitInferenceAPIKey, cfg.LiveKitInferenceAPISecret)
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_LLM_FALLBACK_PROVIDERS entry %q", provider)
	}
}

func configureSTTFallbacks(cfg AppConfig, a *agent.Agent) error {
	if len(cfg.STTFallbackProviders) == 0 {
		return nil
	}
	if a.STT == nil {
		return fmt.Errorf("RTP_AGENT_STT_FALLBACK_PROVIDERS requires RTP_AGENT_STT_PROVIDER")
	}
	stts := make([]corestt.STT, 0, len(cfg.STTFallbackProviders)+1)
	stts = append(stts, a.STT)
	for _, provider := range cfg.STTFallbackProviders {
		fallback, err := fallbackSTTFromProvider(cfg, provider)
		if err != nil {
			return err
		}
		stts = append(stts, fallback)
	}
	if a.VAD != nil {
		a.STT = corestt.NewFallbackAdapterWithVAD(stts, a.VAD)
		return nil
	}
	a.STT = corestt.NewFallbackAdapter(stts)
	return nil
}

func fallbackSTTFromProvider(cfg AppConfig, provider string) (corestt.STT, error) {
	switch normalizeProvider(provider) {
	case providerDeepgram:
		sttOpts := []deepgram.DeepgramSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTInterimResults != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTInterimResults(*cfg.STTInterimResults))
		}
		if cfg.STTDiarization != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTDiarization(*cfg.STTDiarization))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTNumberOfChannels != nil {
			sttOpts = append(sttOpts, deepgram.WithDeepgramSTTNumChannels(*cfg.STTNumberOfChannels))
		}
		return deepgram.NewDeepgramSTT("", cfg.STTModel, sttOpts...), nil
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
		return openai.NewOpenAISTT(cfg.OpenAIAPIKey, cfg.STTModel, sttOpts...)
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
		return elevenlabs.NewElevenLabsSTT(cfg.ElevenLabsAPIKey, sttOpts...), nil
	case providerSLNG:
		sttOpts := []slng.STTOption{}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, slng.WithSTTModel(cfg.STTModel))
		}
		if cfg.STTBaseURL != "" {
			if strings.HasPrefix(cfg.STTBaseURL, "ws://") || strings.HasPrefix(cfg.STTBaseURL, "wss://") || strings.HasPrefix(cfg.STTBaseURL, "http://") || strings.HasPrefix(cfg.STTBaseURL, "https://") {
				sttOpts = append(sttOpts, slng.WithSTTEndpoint(cfg.STTBaseURL))
			} else {
				sttOpts = append(sttOpts, slng.WithSTTBaseURL(cfg.STTBaseURL))
			}
		}
		if cfg.STTRegion != "" {
			sttOpts = append(sttOpts, slng.WithSTTRegionOverride(cfg.STTRegion))
		}
		if cfg.STTEncoding != "" {
			sttOpts = append(sttOpts, slng.WithSTTEncoding(cfg.STTEncoding))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, slng.WithSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTInterimResults != nil {
			sttOpts = append(sttOpts, slng.WithSTTPartialTranscripts(*cfg.STTInterimResults))
		}
		return slng.NewSTT(cfg.SLNGAPIKey, sttOpts...), nil
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_STT_FALLBACK_PROVIDERS entry %q", provider)
	}
}

func configureTTSFallbacks(cfg AppConfig, a *agent.Agent) error {
	if len(cfg.TTSFallbackProviders) == 0 {
		return nil
	}
	if a.TTS == nil {
		return fmt.Errorf("RTP_AGENT_TTS_FALLBACK_PROVIDERS requires RTP_AGENT_TTS_PROVIDER")
	}
	ttss := make([]coretts.TTS, 0, len(cfg.TTSFallbackProviders)+1)
	ttss = append(ttss, a.TTS)
	for _, provider := range cfg.TTSFallbackProviders {
		fallback, err := fallbackTTSFromProvider(cfg, provider)
		if err != nil {
			return err
		}
		ttss = append(ttss, fallback)
	}
	a.TTS = coretts.NewFallbackAdapter(ttss)
	return nil
}

func fallbackTTSFromProvider(cfg AppConfig, provider string) (coretts.TTS, error) {
	switch normalizeProvider(provider) {
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
		return openai.NewOpenAITTS(cfg.OpenAIAPIKey, "", "", ttsOpts...)
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
		return cartesia.NewCartesiaTTS("", cfg.TTSVoice, cfg.TTSModel, ttsOpts...), nil
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
		return deepgram.NewDeepgramTTS("", cfg.TTSModel, ttsOpts...), nil
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
		return elevenlabs.NewElevenLabsTTS(cfg.ElevenLabsAPIKey, cfg.TTSVoice, cfg.TTSModel, ttsOpts...)
	case providerSLNG:
		ttsOpts := []slng.TTSOption{}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, slng.WithTTSModel(cfg.TTSModel))
		}
		if cfg.TTSBaseURL != "" {
			if strings.HasPrefix(cfg.TTSBaseURL, "ws://") || strings.HasPrefix(cfg.TTSBaseURL, "wss://") || strings.HasPrefix(cfg.TTSBaseURL, "http://") || strings.HasPrefix(cfg.TTSBaseURL, "https://") {
				ttsOpts = append(ttsOpts, slng.WithTTSEndpoint(cfg.TTSBaseURL))
			} else {
				ttsOpts = append(ttsOpts, slng.WithTTSBaseURL(cfg.TTSBaseURL))
			}
		}
		if cfg.TTSRegion != "" {
			ttsOpts = append(ttsOpts, slng.WithTTSRegionOverride(cfg.TTSRegion))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, slng.WithTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, slng.WithTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, slng.WithTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, slng.WithTTSSpeed(cfg.TTSSpeed))
		}
		return slng.NewTTS(cfg.SLNGAPIKey, ttsOpts...), nil
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_TTS_FALLBACK_PROVIDERS entry %q", provider)
	}
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
	case providerGradium:
		a.LLM = gradium.NewGradiumLLM(cfg.GradiumAPIKey, cfg.LLMModel)
	case providerGroq:
		a.LLM = groq.NewGroqLLM(cfg.GroqAPIKey, cfg.LLMModel)
	case providerHedra:
		a.LLM = hedra.NewHedraLLM(cfg.HedraAPIKey, cfg.LLMModel)
	case providerHume:
		a.LLM = hume.NewHumeLLM(cfg.HumeAPIKey, cfg.LLMModel)
	case providerInworld:
		a.LLM = inworld.NewInworldLLM(cfg.InworldAPIKey, cfg.LLMModel)
	case providerLangChain:
		a.LLM = langchain.NewLangchainLLM(cfg.LangChainAPIKey, cfg.LLMModel)
	case providerLemonSlice:
		a.LLM = lemonslice.NewLemonSliceLLM(cfg.LemonSliceAPIKey, cfg.LLMModel)
	case providerMinimal:
		a.LLM = minimal.NewMinimalLLM(cfg.MinimalAPIKey, cfg.LLMModel)
	case providerMinimax:
		a.LLM = minimax.NewMinimaxLLM(cfg.MinimaxAPIKey, cfg.LLMModel)
	case providerMistralAI:
		a.LLM = mistralai.NewMistralLLM(cfg.MistralAPIKey, cfg.LLMModel)
	case providerSarvam:
		llmOpts := []sarvam.SarvamLLMOption{}
		if cfg.LLMBaseURL != "" {
			llmOpts = append(llmOpts, sarvam.WithSarvamLLMBaseURL(cfg.LLMBaseURL))
		}
		if len(cfg.LLMExtraHeaders) > 0 {
			llmOpts = append(llmOpts, sarvam.WithSarvamLLMExtraHeaders(cfg.LLMExtraHeaders))
		}
		if len(cfg.LLMExtraBody) > 0 {
			llmOpts = append(llmOpts, sarvam.WithSarvamLLMExtraBody(cfg.LLMExtraBody))
		}
		provider := sarvam.NewSarvamLLM(cfg.SarvamAPIKey, cfg.LLMModel, llmOpts...)
		if provider == nil {
			return nil, fmt.Errorf("invalid sarvam LLM configuration")
		}
		a.LLM = provider
	case providerSimli:
		a.LLM = simli.NewSimliLLM(cfg.SimliAPIKey, cfg.LLMModel)
	case providerSimplismart:
		a.LLM = simplismart.NewSimplismartLLM(cfg.SimplismartAPIKey, cfg.LLMModel)
	case providerSmallestAI:
		a.LLM = smallestai.NewSmallestAILLM(cfg.SmallestAIAPIKey, cfg.LLMModel)
	case providerTelnyx:
		a.LLM = telnyx.NewTelnyxLLM(cfg.TelnyxAPIKey, cfg.LLMModel)
	case providerTrugen:
		a.LLM = trugen.NewTrugenLLM(cfg.TrugenAPIKey, cfg.LLMModel)
	case providerUpliftAI:
		a.LLM = upliftai.NewUpliftAILLM(cfg.UpliftAIAPIKey, cfg.LLMModel)
	case providerXAI:
		a.LLM = xai.NewXaiLLM(cfg.XAIAPIKey, cfg.LLMModel)
	case providerCerebras:
		a.LLM = cerebras.NewCerebrasLLM(cfg.CerebrasAPIKey, cfg.LLMModel)
	case providerFal:
		a.LLM = fal.NewFalLLM(cfg.FalAPIKey, cfg.LLMModel)
	case providerFireworks:
		a.LLM = fireworksai.NewFireworksLLM(cfg.FireworksAPIKey, cfg.LLMModel)
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
	case providerNvidia:
		a.LLM = nvidia.NewNvidiaLLM(cfg.NvidiaAPIKey, cfg.LLMModel)
	case providerPerplexity:
		a.LLM = perplexity.NewPerplexityLLM(cfg.PerplexityAPIKey, cfg.LLMModel)
	case providerLiveKit:
		provider, err := openai.NewLiveKitInferenceLLM(cfg.LLMModel, cfg.LiveKitInferenceAPIKey, cfg.LiveKitInferenceAPISecret)
		if err != nil {
			return nil, err
		}
		a.LLM = provider
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_LLM_PROVIDER %q", cfg.LLMProvider)
	}
	if err := configureLLMFallbacks(cfg, a); err != nil {
		return nil, err
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
	case providerFireworks:
		sttOpts := []fireworksai.FireworksSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, fireworksai.WithFireworksBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, fireworksai.WithFireworksModel(cfg.STTModel))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, fireworksai.WithFireworksLanguage(cfg.STTLanguage))
		}
		if cfg.STTPrompt != "" {
			sttOpts = append(sttOpts, fireworksai.WithFireworksPrompt(cfg.STTPrompt))
		}
		if cfg.STTTemperature != nil {
			sttOpts = append(sttOpts, fireworksai.WithFireworksTemperature(*cfg.STTTemperature))
		}
		if cfg.STTSkipVAD != nil {
			sttOpts = append(sttOpts, fireworksai.WithFireworksSkipVAD(*cfg.STTSkipVAD))
		}
		if len(cfg.STTVADKwargs) > 0 {
			sttOpts = append(sttOpts, fireworksai.WithFireworksVADKwargs(cfg.STTVADKwargs))
		}
		if cfg.STTTextTimeoutSeconds != nil {
			sttOpts = append(sttOpts, fireworksai.WithFireworksTextTimeoutSeconds(*cfg.STTTextTimeoutSeconds))
		}
		if len(cfg.STTTimestampGranularities) > 0 {
			sttOpts = append(sttOpts, fireworksai.WithFireworksTimestampGranularities(cfg.STTTimestampGranularities))
		}
		a.STT = fireworksai.NewFireworksSTT(cfg.FireworksAPIKey, sttOpts...)
	case providerGladia:
		sttOpts := []gladia.GladiaSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, gladia.WithGladiaBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, gladia.WithGladiaModel(cfg.STTModel))
		}
		if cfg.STTInterimResults != nil {
			sttOpts = append(sttOpts, gladia.WithGladiaInterimResults(*cfg.STTInterimResults))
		}
		if cfg.STTLanguageOptions != "" {
			sttOpts = append(sttOpts, gladia.WithGladiaLanguages(splitStringList(cfg.STTLanguageOptions)))
		}
		if cfg.STTCodeSwitching != nil {
			sttOpts = append(sttOpts, gladia.WithGladiaCodeSwitching(*cfg.STTCodeSwitching))
		}
		sampleRate := 0
		if cfg.STTSampleRate != nil {
			sampleRate = *cfg.STTSampleRate
		}
		bitDepth := 0
		if cfg.STTBitDepth != nil {
			bitDepth = *cfg.STTBitDepth
		}
		channels := 0
		if cfg.STTNumberOfChannels != nil {
			channels = *cfg.STTNumberOfChannels
		}
		if sampleRate != 0 || bitDepth != 0 || channels != 0 || cfg.STTEncoding != "" {
			sttOpts = append(sttOpts, gladia.WithGladiaAudioFormat(sampleRate, bitDepth, channels, cfg.STTEncoding))
		}
		if cfg.STTEndpointingSeconds != nil || cfg.STTMaxDurationWithoutEndpointingSeconds != nil {
			endpointing := -1.0
			if cfg.STTEndpointingSeconds != nil {
				endpointing = *cfg.STTEndpointingSeconds
			}
			maxDuration := 0.0
			if cfg.STTMaxDurationWithoutEndpointingSeconds != nil {
				maxDuration = *cfg.STTMaxDurationWithoutEndpointingSeconds
			}
			sttOpts = append(sttOpts, gladia.WithGladiaEndpointing(endpointing, maxDuration))
		}
		if cfg.STTRegion != "" {
			sttOpts = append(sttOpts, gladia.WithGladiaRegion(cfg.STTRegion))
		}
		if len(cfg.STTCustomVocabulary) > 0 {
			sttOpts = append(sttOpts, gladia.WithGladiaCustomVocabulary(cfg.STTCustomVocabulary))
		}
		if len(cfg.STTCustomSpelling) > 0 {
			sttOpts = append(sttOpts, gladia.WithGladiaCustomSpelling(cfg.STTCustomSpelling))
		}
		if len(cfg.STTTranslationTargetLanguages) > 0 {
			matchOriginal := boolValue(cfg.STTTranslationMatchOriginalUtterances)
			lipsync := boolValue(cfg.STTTranslationLipsync)
			contextAdaptation := boolValue(cfg.STTTranslationContextAdaptation)
			informal := boolValue(cfg.STTTranslationInformal)
			if cfg.STTTranslationModel != "" || cfg.STTTranslationContext != "" || matchOriginal || lipsync || contextAdaptation || informal {
				sttOpts = append(sttOpts, gladia.WithGladiaTranslationConfig(
					cfg.STTTranslationTargetLanguages,
					cfg.STTTranslationModel,
					matchOriginal,
					lipsync,
					contextAdaptation,
					cfg.STTTranslationContext,
					informal,
				))
			} else {
				sttOpts = append(sttOpts, gladia.WithGladiaTranslation(cfg.STTTranslationTargetLanguages))
			}
		}
		if cfg.STTPreProcessingAudioEnhancer != nil || cfg.STTPreProcessingSpeechThreshold != nil {
			speechThreshold := 0.0
			if cfg.STTPreProcessingSpeechThreshold != nil {
				speechThreshold = *cfg.STTPreProcessingSpeechThreshold
			}
			sttOpts = append(sttOpts, gladia.WithGladiaPreProcessing(boolValue(cfg.STTPreProcessingAudioEnhancer), speechThreshold))
		}
		a.STT = gladia.NewGladiaSTT(cfg.GladiaAPIKey, sttOpts...)
	case providerGnani:
		sttOpts := []gnani.STTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, gnani.WithSTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, gnani.WithSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, gnani.WithSTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTOrganizationID != "" {
			sttOpts = append(sttOpts, gnani.WithSTTOrganizationID(cfg.STTOrganizationID))
		}
		if cfg.STTUserID != "" {
			sttOpts = append(sttOpts, gnani.WithSTTUserID(cfg.STTUserID))
		}
		a.STT = gnani.NewSTT(cfg.GnaniAPIKey, sttOpts...)
	case providerGradium:
		sttOpts := []gradium.GradiumSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, gradium.WithGradiumSTTModelEndpoint(cfg.STTBaseURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, gradium.WithGradiumSTTModelName(cfg.STTModel))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, gradium.WithGradiumSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTTemperature != nil {
			sttOpts = append(sttOpts, gradium.WithGradiumSTTTemperature(*cfg.STTTemperature))
		}
		if cfg.STTVADBucket != nil {
			sttOpts = append(sttOpts, gradium.WithGradiumSTTVADBucket(cfg.STTVADBucket))
		}
		if cfg.STTVADFlush != nil {
			sttOpts = append(sttOpts, gradium.WithGradiumSTTVADFlush(*cfg.STTVADFlush))
		}
		if cfg.STTBufferSizeSeconds != nil {
			sttOpts = append(sttOpts, gradium.WithGradiumSTTBufferSizeSeconds(*cfg.STTBufferSizeSeconds))
		}
		a.STT = gradium.NewGradiumSTT(cfg.GradiumAPIKey, sttOpts...)
	case providerInworld:
		sttOpts := []inworld.InworldSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, inworld.WithInworldSTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, inworld.WithInworldSTTModel(cfg.STTModel))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, inworld.WithInworldSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, inworld.WithInworldSTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTNumberOfChannels != nil {
			sttOpts = append(sttOpts, inworld.WithInworldSTTNumChannels(*cfg.STTNumberOfChannels))
		}
		if cfg.STTVoiceProfile != nil {
			sttOpts = append(sttOpts, inworld.WithInworldSTTVoiceProfile(*cfg.STTVoiceProfile))
		}
		if cfg.STTVoiceProfileTopN != nil {
			sttOpts = append(sttOpts, inworld.WithInworldSTTVoiceProfileTopN(*cfg.STTVoiceProfileTopN))
		}
		if cfg.STTVADThreshold != nil {
			sttOpts = append(sttOpts, inworld.WithInworldSTTVADThreshold(*cfg.STTVADThreshold))
		}
		if cfg.STTMinEndOfTurnSilenceWhenConfident != nil {
			sttOpts = append(sttOpts, inworld.WithInworldSTTMinEndOfTurnSilenceWhenConfident(*cfg.STTMinEndOfTurnSilenceWhenConfident))
		}
		if cfg.STTEndOfTurnConfidenceThreshold != nil {
			sttOpts = append(sttOpts, inworld.WithInworldSTTEndOfTurnConfidenceThreshold(*cfg.STTEndOfTurnConfidenceThreshold))
		}
		a.STT = inworld.NewInworldSTT(cfg.InworldAPIKey, sttOpts...)
	case providerMistralAI:
		sttOpts := []mistralai.MistralAISTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, mistralai.WithMistralAISTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, mistralai.WithMistralAISTTModel(cfg.STTModel))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, mistralai.WithMistralAISTTLanguage(cfg.STTLanguage))
		}
		if len(cfg.STTKeytermsPrompt) > 0 {
			sttOpts = append(sttOpts, mistralai.WithMistralAISTTContextBias(cfg.STTKeytermsPrompt))
		}
		a.STT = mistralai.NewMistralAISTT(cfg.MistralAPIKey, sttOpts...)
	case providerSLNG:
		sttOpts := []slng.STTOption{}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, slng.WithSTTModel(cfg.STTModel))
		}
		if cfg.STTBaseURL != "" {
			if strings.HasPrefix(cfg.STTBaseURL, "ws://") || strings.HasPrefix(cfg.STTBaseURL, "wss://") || strings.HasPrefix(cfg.STTBaseURL, "http://") || strings.HasPrefix(cfg.STTBaseURL, "https://") {
				sttOpts = append(sttOpts, slng.WithSTTEndpoint(cfg.STTBaseURL))
			} else {
				sttOpts = append(sttOpts, slng.WithSTTBaseURL(cfg.STTBaseURL))
			}
		}
		if cfg.STTRegion != "" {
			sttOpts = append(sttOpts, slng.WithSTTRegionOverride(cfg.STTRegion))
		}
		if cfg.STTEncoding != "" {
			sttOpts = append(sttOpts, slng.WithSTTEncoding(cfg.STTEncoding))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, slng.WithSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTInterimResults != nil {
			sttOpts = append(sttOpts, slng.WithSTTPartialTranscripts(*cfg.STTInterimResults))
		}
		if cfg.STTDiarization != nil {
			minSpeakers := 0
			if cfg.STTMinSpeakers != nil {
				minSpeakers = *cfg.STTMinSpeakers
			}
			maxSpeakers := 0
			if cfg.STTMaxSpeakers != nil {
				maxSpeakers = *cfg.STTMaxSpeakers
			}
			sttOpts = append(sttOpts, slng.WithSTTDiarization(*cfg.STTDiarization, minSpeakers, maxSpeakers))
		}
		if len(cfg.STTModelOptions) > 0 {
			sttOpts = append(sttOpts, slng.WithSTTModelOptions(cfg.STTModelOptions))
		}
		a.STT = slng.NewSTT(cfg.SLNGAPIKey, sttOpts...)
	case providerSarvam:
		sttOpts := []sarvam.SarvamSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTStreamingURL != "" {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTStreamingURL(cfg.STTStreamingURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTModel(cfg.STTModel))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTTask != "" {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTMode(cfg.STTTask))
		}
		if cfg.STTPrompt != "" {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTPrompt(cfg.STTPrompt))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTVADEvents != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTHighVADSensitivity(*cfg.STTVADEvents))
		}
		if cfg.STTVADFlush != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTFlushSignal(*cfg.STTVADFlush))
		}
		if cfg.STTEncoding != "" {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTInputAudioCodec(cfg.STTEncoding))
		}
		if cfg.STTPositiveSpeechThreshold != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTPositiveSpeechThreshold(*cfg.STTPositiveSpeechThreshold))
		}
		if cfg.STTNegativeSpeechThreshold != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTNegativeSpeechThreshold(*cfg.STTNegativeSpeechThreshold))
		}
		if cfg.STTMinSpeechFrames != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTMinSpeechFrames(*cfg.STTMinSpeechFrames))
		}
		if cfg.STTFirstTurnMinSpeechFrames != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTFirstTurnMinSpeechFrames(*cfg.STTFirstTurnMinSpeechFrames))
		}
		if cfg.STTNegativeFramesCount != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTNegativeFramesCount(*cfg.STTNegativeFramesCount))
		}
		if cfg.STTNegativeFramesWindow != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTNegativeFramesWindow(*cfg.STTNegativeFramesWindow))
		}
		if cfg.STTStartSpeechVolumeThreshold != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTStartSpeechVolumeThreshold(*cfg.STTStartSpeechVolumeThreshold))
		}
		if cfg.STTInterruptMinSpeechFrames != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTInterruptMinSpeechFrames(*cfg.STTInterruptMinSpeechFrames))
		}
		if cfg.STTPreSpeechPadFrames != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTPreSpeechPadFrames(*cfg.STTPreSpeechPadFrames))
		}
		if cfg.STTNumInitialIgnoredFrames != nil {
			sttOpts = append(sttOpts, sarvam.WithSarvamSTTNumInitialIgnoredFrames(*cfg.STTNumInitialIgnoredFrames))
		}
		provider := sarvam.NewSarvamSTT(cfg.SarvamAPIKey, sttOpts...)
		if provider == nil {
			return nil, fmt.Errorf("invalid sarvam STT configuration")
		}
		a.STT = provider
	case providerRtzr:
		sttOpts := []rtzr.RtzrSTTOption{}
		if cfg.RtzrClientSecret != "" {
			sttOpts = append(sttOpts, rtzr.WithRtzrClientSecret(cfg.RtzrClientSecret))
		}
		if cfg.RtzrAccessToken != "" {
			sttOpts = append(sttOpts, rtzr.WithRtzrAccessToken(cfg.RtzrAccessToken))
		}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, rtzr.WithRtzrAPIBase(cfg.STTBaseURL))
		}
		if cfg.STTStreamingURL != "" {
			sttOpts = append(sttOpts, rtzr.WithRtzrWSBase(cfg.STTStreamingURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, rtzr.WithRtzrModel(cfg.STTModel))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, rtzr.WithRtzrLanguage(cfg.STTLanguage))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, rtzr.WithRtzrSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTDomain != "" {
			sttOpts = append(sttOpts, rtzr.WithRtzrDomain(cfg.STTDomain))
		}
		if cfg.STTEndpointingSeconds != nil {
			sttOpts = append(sttOpts, rtzr.WithRtzrEPDTime(*cfg.STTEndpointingSeconds))
		}
		if cfg.STTVADThreshold != nil {
			sttOpts = append(sttOpts, rtzr.WithRtzrNoiseThreshold(*cfg.STTVADThreshold))
		}
		if cfg.STTEndOfTurnConfidenceThreshold != nil {
			sttOpts = append(sttOpts, rtzr.WithRtzrActiveThreshold(*cfg.STTEndOfTurnConfidenceThreshold))
		}
		if cfg.STTPunctuate != nil {
			sttOpts = append(sttOpts, rtzr.WithRtzrUsePunctuation(*cfg.STTPunctuate))
		}
		if len(cfg.STTKeytermsPrompt) > 0 {
			sttOpts = append(sttOpts, rtzr.WithRtzrKeywords(cfg.STTKeytermsPrompt))
		}
		a.STT = rtzr.NewRtzrSTT(cfg.RtzrClientID, sttOpts...)
	case providerSimplismart:
		sttOpts := []simplismart.SimplismartSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, simplismart.WithSimplismartSTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTInterimResults != nil {
			sttOpts = append(sttOpts, simplismart.WithSimplismartSTTStreaming(*cfg.STTInterimResults))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, simplismart.WithSimplismartSTTModel(cfg.STTModel))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, simplismart.WithSimplismartSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTTask != "" {
			sttOpts = append(sttOpts, simplismart.WithSimplismartSTTTask(cfg.STTTask))
		}
		if cfg.STTIncludeTimestamps != nil {
			sttOpts = append(sttOpts, simplismart.WithSimplismartSTTWithoutTimestamps(!*cfg.STTIncludeTimestamps))
		}
		if len(cfg.STTKeytermsPrompt) > 0 {
			sttOpts = append(sttOpts, simplismart.WithSimplismartSTTHotwords(strings.Join(cfg.STTKeytermsPrompt, ",")))
		}
		if cfg.STTMaxSpeakers != nil {
			sttOpts = append(sttOpts, simplismart.WithSimplismartSTTNumSpeakers(*cfg.STTMaxSpeakers))
		}
		a.STT = simplismart.NewSimplismartSTT(cfg.SimplismartAPIKey, sttOpts...)
	case providerSmallestAI:
		sttOpts := []smallestai.SmallestAISTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, smallestai.WithSmallestAISTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, smallestai.WithSmallestAISTTModel(cfg.STTModel))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, smallestai.WithSmallestAISTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, smallestai.WithSmallestAISTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTEncoding != "" {
			sttOpts = append(sttOpts, smallestai.WithSmallestAISTTEncoding(cfg.STTEncoding))
		}
		if cfg.STTWordTimestamps != nil {
			sttOpts = append(sttOpts, smallestai.WithSmallestAISTTWordTimestamps(*cfg.STTWordTimestamps))
		}
		if cfg.STTDiarization != nil {
			sttOpts = append(sttOpts, smallestai.WithSmallestAISTTDiarize(*cfg.STTDiarization))
		}
		if cfg.STTEndpointingMS != nil {
			sttOpts = append(sttOpts, smallestai.WithSmallestAISTTEOUTimeoutMS(*cfg.STTEndpointingMS))
		}
		a.STT = smallestai.NewSmallestAISTT(cfg.SmallestAIAPIKey, sttOpts...)
	case providerSoniox:
		sttOpts := []soniox.SonioxSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, soniox.WithSonioxBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, soniox.WithSonioxModel(cfg.STTModel))
		}
		if cfg.STTLanguageOptions != "" {
			sttOpts = append(sttOpts, soniox.WithSonioxLanguageHints(splitStringList(cfg.STTLanguageOptions)))
		} else if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, soniox.WithSonioxLanguageHints([]string{cfg.STTLanguage}))
		}
		if strict := modelOptionBool(cfg.STTModelOptions, "language_hints_strict"); strict != nil {
			sttOpts = append(sttOpts, soniox.WithSonioxLanguageHintsStrict(*strict))
		}
		if cfg.STTNumberOfChannels != nil {
			sttOpts = append(sttOpts, soniox.WithSonioxNumChannels(*cfg.STTNumberOfChannels))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, soniox.WithSonioxSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTDiarization != nil {
			sttOpts = append(sttOpts, soniox.WithSonioxSpeakerDiarization(*cfg.STTDiarization))
		}
		if cfg.STTLanguageDetection != nil {
			sttOpts = append(sttOpts, soniox.WithSonioxLanguageIdentification(*cfg.STTLanguageDetection))
		}
		if cfg.STTEndpointingMS != nil {
			sttOpts = append(sttOpts, soniox.WithSonioxMaxEndpointDelayMS(*cfg.STTEndpointingMS))
		}
		if cfg.STTSessionID != "" {
			sttOpts = append(sttOpts, soniox.WithSonioxClientReferenceID(cfg.STTSessionID))
		}
		if context, ok := sonioxContextObjectFromModelOptions(cfg.STTModelOptions); ok {
			sttOpts = append(sttOpts, soniox.WithSonioxContextObject(context))
		} else if cfg.STTPrompt != "" {
			sttOpts = append(sttOpts, soniox.WithSonioxContextText(cfg.STTPrompt))
		}
		if len(cfg.STTTranslationSourceLanguages) > 0 && len(cfg.STTTranslationTargetLanguages) > 0 {
			sttOpts = append(sttOpts, soniox.WithSonioxTwoWayTranslation(cfg.STTTranslationSourceLanguages[0], cfg.STTTranslationTargetLanguages[0]))
		} else if len(cfg.STTTranslationTargetLanguages) > 0 {
			sttOpts = append(sttOpts, soniox.WithSonioxOneWayTranslation(cfg.STTTranslationTargetLanguages[0]))
		}
		a.STT = soniox.NewSonioxSTT(cfg.SonioxAPIKey, sttOpts...)
	case providerSpeechmatics:
		sttOpts := []speechmatics.SpeechmaticsSTTOption{}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTEncoding != "" {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTAudioEncoding(cfg.STTEncoding))
		}
		if cfg.STTDomain != "" {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTDomain(cfg.STTDomain))
		}
		if cfg.STTOutputLocale != "" {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTOutputLocale(cfg.STTOutputLocale))
		}
		if cfg.STTInterimResults != nil {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTIncludePartials(*cfg.STTInterimResults))
		}
		if cfg.STTDiarization != nil {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTEnableDiarization(*cfg.STTDiarization))
		}
		if len(cfg.STTKeytermsPrompt) > 0 {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTAdditionalVocab(speechmaticsAdditionalVocab(cfg.STTKeytermsPrompt)))
		}
		focusSpeakers := modelOptionStringList(cfg.STTModelOptions, "focus_speakers")
		ignoreSpeakers := modelOptionStringList(cfg.STTModelOptions, "ignore_speakers")
		focusMode := modelOptionString(cfg.STTModelOptions, "focus_mode")
		if len(focusSpeakers) > 0 || len(ignoreSpeakers) > 0 || focusMode != "" {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTSpeakerFocus(focusSpeakers, ignoreSpeakers, focusMode))
		}
		if speakers := speechmaticsKnownSpeakers(modelOptionString(cfg.STTModelOptions, "known_speakers")); len(speakers) > 0 {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTKnownSpeakers(speakers))
		}
		if cfg.STTOperatingPoint != "" {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTOperatingPoint(cfg.STTOperatingPoint))
		}
		if cfg.STTTextTimeoutSeconds != nil {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTMaxDelay(*cfg.STTTextTimeoutSeconds))
		}
		if cfg.STTVADSilenceThresholdSeconds != nil {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTEndOfUtteranceSilenceTrigger(*cfg.STTVADSilenceThresholdSeconds))
		}
		if cfg.STTMaxDurationWithoutEndpointingSeconds != nil {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTEndOfUtteranceMaxDelay(*cfg.STTMaxDurationWithoutEndpointingSeconds))
		}
		if overrides := speechmaticsPunctuationOverrides(cfg.STTModelOptions); len(overrides) > 0 {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTPunctuationOverrides(overrides))
		}
		if sensitivity := modelOptionFloat(cfg.STTModelOptions, "speaker_sensitivity"); sensitivity != nil {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTSpeakerSensitivity(*sensitivity))
		}
		if cfg.STTMaxSpeakers != nil {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTMaxSpeakers(*cfg.STTMaxSpeakers))
		}
		if cfg.STTPreferCurrentSpeaker != nil {
			sttOpts = append(sttOpts, speechmatics.WithSpeechmaticsSTTPreferCurrentSpeaker(*cfg.STTPreferCurrentSpeaker))
		}
		a.STT = speechmatics.NewSpeechmaticsSTT(cfg.SpeechmaticsAPIKey, sttOpts...)
	case providerSpitch:
		a.STT = spitch.NewSpitchSTT(cfg.SpitchAPIKey)
	case providerTelnyx:
		sttOpts := []telnyx.TelnyxSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, telnyx.WithTelnyxSTTBaseURL(cfg.STTBaseURL))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, telnyx.WithTelnyxSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTModel != "" {
			sttOpts = append(sttOpts, telnyx.WithTelnyxSTTTranscriptionEngine(cfg.STTModel))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, telnyx.WithTelnyxSTTSampleRate(*cfg.STTSampleRate))
		}
		a.STT = telnyx.NewTelnyxSTT(cfg.TelnyxAPIKey, sttOpts...)
	case providerXAI:
		sttOpts := []xai.XaiSTTOption{}
		if cfg.STTBaseURL != "" {
			sttOpts = append(sttOpts, xai.WithXaiSTTRestURL(cfg.STTBaseURL))
		}
		if cfg.STTStreamingURL != "" {
			sttOpts = append(sttOpts, xai.WithXaiSTTWebsocketURL(cfg.STTStreamingURL))
		}
		if cfg.STTSampleRate != nil {
			sttOpts = append(sttOpts, xai.WithXaiSTTSampleRate(*cfg.STTSampleRate))
		}
		if cfg.STTLanguage != "" {
			sttOpts = append(sttOpts, xai.WithXaiSTTLanguage(cfg.STTLanguage))
		}
		if cfg.STTInterimResults != nil {
			sttOpts = append(sttOpts, xai.WithXaiSTTInterimResults(*cfg.STTInterimResults))
		}
		if cfg.STTDiarization != nil {
			sttOpts = append(sttOpts, xai.WithXaiSTTDiarization(*cfg.STTDiarization))
		}
		if cfg.STTEndpointingMS != nil {
			sttOpts = append(sttOpts, xai.WithXaiSTTEndpointing(*cfg.STTEndpointingMS))
		}
		a.STT = xai.NewXaiSTT(cfg.XAIAPIKey, sttOpts...)
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
	case providerLiveKit:
		a.STT = inference.NewSTT(cfg.STTModel, cfg.LiveKitInferenceAPIKey, cfg.LiveKitInferenceAPISecret)
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_STT_PROVIDER %q", cfg.STTProvider)
	}
	if err := configureSTTFallbacks(cfg, a); err != nil {
		return nil, err
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
		provider, err := azure.NewAzureTTS("", "", cfg.TTSVoice, cfg.TTSLanguage)
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
		ttsOpts := []adaptergoogle.GoogleTTSOption{}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, adaptergoogle.WithGoogleTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, adaptergoogle.WithGoogleTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, adaptergoogle.WithGoogleTTSModel(cfg.TTSModel))
		}
		provider, err := adaptergoogle.NewGoogleTTS(cfg.GoogleCredentialsFile, ttsOpts...)
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
	case providerGnani:
		ttsOpts := []gnani.Option{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, gnani.WithBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, gnani.WithVoice(cfg.TTSVoice))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, gnani.WithModel(cfg.TTSModel))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, gnani.WithSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSEncoding != "" {
			ttsOpts = append(ttsOpts, gnani.WithEncoding(cfg.TTSEncoding))
		}
		if cfg.TTSResponseFormat != "" {
			ttsOpts = append(ttsOpts, gnani.WithContainer(cfg.TTSResponseFormat))
		}
		if cfg.TTSNumberOfChannels != nil {
			ttsOpts = append(ttsOpts, gnani.WithNumChannels(*cfg.TTSNumberOfChannels))
		}
		if cfg.TTSSampleWidth != nil {
			ttsOpts = append(ttsOpts, gnani.WithSampleWidth(*cfg.TTSSampleWidth))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, gnani.WithLanguage(cfg.TTSLanguage))
		}
		a.TTS = gnani.NewTTS(cfg.GnaniAPIKey, ttsOpts...)
	case providerGradium:
		ttsOpts := []gradium.GradiumTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, gradium.WithGradiumTTSModelEndpoint(cfg.TTSBaseURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, gradium.WithGradiumTTSModelName(cfg.TTSModel))
		}
		if cfg.TTSVoiceID != "" {
			ttsOpts = append(ttsOpts, gradium.WithGradiumTTSVoiceID(cfg.TTSVoiceID))
		}
		if cfg.TTSPronunciationDictID != "" {
			ttsOpts = append(ttsOpts, gradium.WithGradiumTTSPronunciationID(cfg.TTSPronunciationDictID))
		}
		if len(cfg.TTSJSONConfig) > 0 {
			ttsOpts = append(ttsOpts, gradium.WithGradiumTTSJSONConfig(cfg.TTSJSONConfig))
		}
		a.TTS = gradium.NewGradiumTTS(cfg.GradiumAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerHume:
		ttsOpts := []hume.HumeTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSModelVersion(cfg.TTSModel))
		}
		if cfg.TTSVoiceID != "" {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSVoiceID(cfg.TTSVoiceID, cfg.TTSVoiceProvider))
		} else if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSVoiceName(cfg.TTSVoice, cfg.TTSVoiceProvider))
		}
		if cfg.TTSInstructions != "" {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSDescription(cfg.TTSInstructions))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSSpeed(cfg.TTSSpeed))
		}
		if cfg.TTSTrailingSilence != nil {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSTrailingSilence(*cfg.TTSTrailingSilence))
		}
		if cfg.TTSInstantMode != nil {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSInstantMode(*cfg.TTSInstantMode))
		}
		if cfg.TTSResponseFormat != "" {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSAudioFormat(cfg.TTSResponseFormat))
		}
		if cfg.TTSContextGenerationID != "" {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSContextGenerationID(cfg.TTSContextGenerationID))
		} else if len(cfg.TTSContextUtterances) > 0 {
			ttsOpts = append(ttsOpts, hume.WithHumeTTSContextUtterances(cfg.TTSContextUtterances))
		}
		a.TTS = hume.NewHumeTTS(cfg.HumeAPIKey, "", ttsOpts...)
	case providerInworld:
		ttsOpts := []inworld.InworldTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSWebsocketURL != "" {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSWebsocketURL(cfg.TTSWebsocketURL))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSModel(cfg.TTSModel))
		}
		if cfg.TTSEncoding != "" {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSEncoding(cfg.TTSEncoding))
		}
		if cfg.TTSBitRate != nil {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSBitRate(*cfg.TTSBitRate))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSSpeakingRate != nil {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSSpeakingRate(*cfg.TTSSpeakingRate))
		}
		if cfg.TTSTemperature != nil {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSTemperature(*cfg.TTSTemperature))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSTimestampType != "" {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSTimestampType(cfg.TTSTimestampType))
		}
		if cfg.TTSTextNormalization != nil {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSTextNormalization(*cfg.TTSTextNormalization))
		}
		if cfg.TTSDeliveryMode != "" {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSDeliveryMode(cfg.TTSDeliveryMode))
		}
		if cfg.TTSTimestampTransportStrategy != "" {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSTimestampTransportStrategy(cfg.TTSTimestampTransportStrategy))
		}
		if cfg.TTSBufferCharThreshold != nil {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSBufferCharThreshold(*cfg.TTSBufferCharThreshold))
		}
		if cfg.TTSMaxBufferDelayMS != nil {
			ttsOpts = append(ttsOpts, inworld.WithInworldTTSMaxBufferDelayMS(*cfg.TTSMaxBufferDelayMS))
		}
		a.TTS = inworld.NewInworldTTS(cfg.InworldAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerMinimax:
		ttsOpts := []minimax.MinimaxTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSModel(cfg.TTSModel))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSBitRate != nil {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSBitrate(*cfg.TTSBitRate))
		}
		if cfg.TTSResponseFormat != "" {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSAudioFormat(cfg.TTSResponseFormat))
		}
		if cfg.TTSEmotion != "" {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSEmotion(cfg.TTSEmotion))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSSpeed(cfg.TTSSpeed))
		}
		if cfg.TTSVolume != nil {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSVolume(*cfg.TTSVolume))
		}
		if cfg.TTSPitch != nil {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSPitch(*cfg.TTSPitch))
		}
		if cfg.TTSTextNormalization != nil {
			ttsOpts = append(ttsOpts, minimax.WithMinimaxTTSTextNormalization(*cfg.TTSTextNormalization))
		}
		a.TTS = minimax.NewMinimaxTTS(cfg.MinimaxAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerMistralAI:
		ttsOpts := []mistralai.MistralAITTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, mistralai.WithMistralAITTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, mistralai.WithMistralAITTSModel(cfg.TTSModel))
		}
		if cfg.TTSRefAudio != "" {
			ttsOpts = append(ttsOpts, mistralai.WithMistralAITTSRefAudio(cfg.TTSRefAudio))
		} else if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, mistralai.WithMistralAITTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSResponseFormat != "" {
			ttsOpts = append(ttsOpts, mistralai.WithMistralAITTSResponseFormat(cfg.TTSResponseFormat))
		}
		provider, err := mistralai.NewMistralAITTS(cfg.MistralAPIKey, "", ttsOpts...)
		if err != nil {
			return nil, err
		}
		a.TTS = provider
	case providerMurf:
		ttsOpts := []murf.MurfTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, murf.WithMurfTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, murf.WithMurfTTSModel(cfg.TTSModel))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, murf.WithMurfTTSLocale(cfg.TTSLanguage))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, murf.WithMurfTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSInstructions != "" {
			ttsOpts = append(ttsOpts, murf.WithMurfTTSStyle(cfg.TTSInstructions))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, murf.WithMurfTTSSpeed(int(cfg.TTSSpeed)))
		}
		if cfg.TTSPitch != nil {
			ttsOpts = append(ttsOpts, murf.WithMurfTTSPitch(*cfg.TTSPitch))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, murf.WithMurfTTSSampleRate(*cfg.TTSSampleRate))
		}
		a.TTS = murf.NewMurfTTS(cfg.MurfAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerNvidia:
		provider, err := nvidia.NewNvidiaTTS(cfg.NvidiaAPIKey, cfg.TTSVoice)
		if err != nil {
			return nil, err
		}
		a.TTS = provider
	case providerLMNT:
		ttsOpts := []lmnt.LMNTTTSOption{}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, lmnt.WithLMNTTTSModel(cfg.TTSModel))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, lmnt.WithLMNTTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, lmnt.WithLMNTTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSResponseFormat != "" {
			ttsOpts = append(ttsOpts, lmnt.WithLMNTTTSFormat(cfg.TTSResponseFormat))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, lmnt.WithLMNTTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSTemperature != nil {
			ttsOpts = append(ttsOpts, lmnt.WithLMNTTTSTemperature(*cfg.TTSTemperature))
		}
		if cfg.TTSTopP != nil {
			ttsOpts = append(ttsOpts, lmnt.WithLMNTTTSTopP(*cfg.TTSTopP))
		}
		a.TTS = lmnt.NewLMNTTTS(cfg.LMNTAPIKey, "", ttsOpts...)
	case providerNeuphonic:
		ttsOpts := []neuphonic.NeuphonicTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, neuphonic.WithNeuphonicTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, neuphonic.WithNeuphonicTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, neuphonic.WithNeuphonicTTSLangCode(cfg.TTSLanguage))
		}
		if cfg.TTSEncoding != "" {
			ttsOpts = append(ttsOpts, neuphonic.WithNeuphonicTTSEncoding(cfg.TTSEncoding))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, neuphonic.WithNeuphonicTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, neuphonic.WithNeuphonicTTSSpeed(cfg.TTSSpeed))
		}
		a.TTS = neuphonic.NewNeuphonicTTS(cfg.NeuphonicAPIKey, "", ttsOpts...)
	case providerResemble:
		ttsOpts := []resemble.ResembleTTSOption{}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, resemble.WithResembleTTSModel(cfg.TTSModel))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, resemble.WithResembleTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, resemble.WithResembleTTSSampleRate(*cfg.TTSSampleRate))
		}
		a.TTS = resemble.NewResembleTTS(cfg.ResembleAPIKey, "", ttsOpts...)
	case providerRespeecher:
		ttsOpts := []respeecher.RespeecherTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, respeecher.WithRespeecherTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, respeecher.WithRespeecherTTSModel(cfg.TTSModel))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, respeecher.WithRespeecherTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, respeecher.WithRespeecherTTSSampleRate(*cfg.TTSSampleRate))
		}
		if len(cfg.TTSJSONConfig) > 0 {
			ttsOpts = append(ttsOpts, respeecher.WithRespeecherTTSSamplingParams(cfg.TTSJSONConfig))
		}
		a.TTS = respeecher.NewRespeecherTTS(cfg.RespeecherAPIKey, "", ttsOpts...)
	case providerRime:
		ttsOpts := []rime.RimeTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, rime.WithRimeTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSWebsocketURL != "" {
			ttsOpts = append(ttsOpts, rime.WithRimeTTSBaseURL(cfg.TTSWebsocketURL), rime.WithRimeTTSWebsocket(true))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, rime.WithRimeTTSModel(cfg.TTSModel))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, rime.WithRimeTTSLang(cfg.TTSLanguage))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, rime.WithRimeTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, rime.WithRimeTTSTimeScaleFactor(cfg.TTSSpeed))
		}
		if cfg.TTSDeliveryMode != "" {
			ttsOpts = append(ttsOpts, rime.WithRimeTTSSegment(cfg.TTSDeliveryMode))
		}
		a.TTS = rime.NewRimeTTS(cfg.RimeAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerSarvam:
		ttsOpts := []sarvam.SarvamTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSWebsocketURL != "" {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSWSURL(cfg.TTSWebsocketURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSModel(cfg.TTSModel))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSTemperature != nil {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSTemperature(*cfg.TTSTemperature))
		}
		if cfg.TTSPitch != nil {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSPitch(float64(*cfg.TTSPitch)))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSPace(cfg.TTSSpeed))
		}
		if cfg.TTSVolume != nil {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSLoudness(*cfg.TTSVolume))
		}
		if cfg.TTSBitRate != nil {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSOutputAudioBitrate(strconv.Itoa(*cfg.TTSBitRate)))
		}
		if cfg.TTSBufferSize != nil {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSMinBufferSize(*cfg.TTSBufferSize))
		}
		if cfg.TTSChunkLength != nil {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSMaxChunkLength(*cfg.TTSChunkLength))
		}
		if cfg.TTSEnhanceNamedEntities != nil {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSEnablePreprocessing(*cfg.TTSEnhanceNamedEntities))
		}
		if cfg.TTSInstantMode != nil {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSEnableCachedResponses(*cfg.TTSInstantMode))
		}
		if cfg.TTSPronunciationDictID != "" {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSDictID(cfg.TTSPronunciationDictID))
		}
		if cfg.TTSEncoding != "" {
			ttsOpts = append(ttsOpts, sarvam.WithSarvamTTSOutputAudioCodec(cfg.TTSEncoding))
		}
		a.TTS = sarvam.NewSarvamTTS(cfg.SarvamAPIKey, "", ttsOpts...)
	case providerSimplismart:
		a.TTS = simplismart.NewSimplismartTTS(cfg.SimplismartAPIKey, cfg.TTSVoice)
	case providerSmallestAI:
		ttsOpts := []smallestai.SmallestAITTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, smallestai.WithSmallestAITTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSWebsocketURL != "" {
			ttsOpts = append(ttsOpts, smallestai.WithSmallestAITTSWebsocketURL(cfg.TTSWebsocketURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, smallestai.WithSmallestAITTSModel(cfg.TTSModel))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, smallestai.WithSmallestAITTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, smallestai.WithSmallestAITTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, smallestai.WithSmallestAITTSSpeed(cfg.TTSSpeed))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, smallestai.WithSmallestAITTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSResponseFormat != "" {
			ttsOpts = append(ttsOpts, smallestai.WithSmallestAITTSOutputFormat(cfg.TTSResponseFormat))
		}
		a.TTS = smallestai.NewSmallestAITTS(cfg.SmallestAIAPIKey, "", ttsOpts...)
	case providerSoniox:
		ttsOpts := []soniox.SonioxTTSOption{}
		if cfg.TTSWebsocketURL != "" {
			ttsOpts = append(ttsOpts, soniox.WithSonioxTTSWebsocketURL(cfg.TTSWebsocketURL))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, soniox.WithSonioxTTSModel(cfg.TTSModel))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, soniox.WithSonioxTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, soniox.WithSonioxTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSEncoding != "" {
			ttsOpts = append(ttsOpts, soniox.WithSonioxTTSAudioFormat(cfg.TTSEncoding))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, soniox.WithSonioxTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSBitRate != nil {
			ttsOpts = append(ttsOpts, soniox.WithSonioxTTSBitrate(*cfg.TTSBitRate))
		}
		a.TTS = soniox.NewSonioxTTS(cfg.SonioxAPIKey, ttsOpts...)
	case providerSpeechify:
		ttsOpts := []speechify.SpeechifyTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, speechify.WithSpeechifyTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, speechify.WithSpeechifyTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSEncoding != "" {
			ttsOpts = append(ttsOpts, speechify.WithSpeechifyTTSEncoding(cfg.TTSEncoding))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, speechify.WithSpeechifyTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, speechify.WithSpeechifyTTSModel(cfg.TTSModel))
		}
		if cfg.TTSLoudnessNormalization != nil {
			ttsOpts = append(ttsOpts, speechify.WithSpeechifyTTSLoudnessNormalization(*cfg.TTSLoudnessNormalization))
		}
		if cfg.TTSTextNormalization != nil {
			ttsOpts = append(ttsOpts, speechify.WithSpeechifyTTSTextNormalization(*cfg.TTSTextNormalization))
		}
		a.TTS = speechify.NewSpeechifyTTS(cfg.SpeechifyAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerSpeechmatics:
		ttsOpts := []speechmatics.SpeechmaticsTTSOption{}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, speechmatics.WithSpeechmaticsTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, speechmatics.WithSpeechmaticsTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, speechmatics.WithSpeechmaticsTTSBaseURL(cfg.TTSBaseURL))
		}
		a.TTS = speechmatics.NewSpeechmaticsTTS(cfg.SpeechmaticsAPIKey, ttsOpts...)
	case providerSpitch:
		ttsOpts := []spitch.SpitchTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, spitch.WithSpitchTTSBaseURL(cfg.TTSBaseURL))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, spitch.WithSpitchTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, spitch.WithSpitchTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSResponseFormat != "" {
			ttsOpts = append(ttsOpts, spitch.WithSpitchTTSOutputFormat(cfg.TTSResponseFormat))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, spitch.WithSpitchTTSSampleRate(*cfg.TTSSampleRate))
		}
		a.TTS = spitch.NewSpitchTTS(cfg.SpitchAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerTelnyx:
		ttsOpts := []telnyx.TelnyxTTSOption{}
		if cfg.TTSBaseURL != "" {
			ttsOpts = append(ttsOpts, telnyx.WithTelnyxTTSBaseURL(cfg.TTSBaseURL))
		}
		a.TTS = telnyx.NewTelnyxTTS(cfg.TelnyxAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerUltravox:
		a.TTS = ultravox.NewUltravoxTTS(cfg.UltravoxAPIKey, cfg.TTSVoice)
	case providerUpliftAI:
		a.TTS = upliftai.NewUpliftAITTS(cfg.UpliftAIAPIKey, cfg.TTSVoice)
	case providerXAI:
		ttsOpts := []xai.XaiTTSOption{}
		if cfg.TTSWebsocketURL != "" {
			ttsOpts = append(ttsOpts, xai.WithXaiTTSWebsocketURL(cfg.TTSWebsocketURL))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, xai.WithXaiTTSLanguage(cfg.TTSLanguage))
		}
		a.TTS = xai.NewXaiTTS(cfg.XAIAPIKey, cfg.TTSVoice, ttsOpts...)
	case providerSLNG:
		ttsOpts := []slng.TTSOption{}
		if cfg.TTSModel != "" {
			ttsOpts = append(ttsOpts, slng.WithTTSModel(cfg.TTSModel))
		}
		if cfg.TTSBaseURL != "" {
			if strings.HasPrefix(cfg.TTSBaseURL, "ws://") || strings.HasPrefix(cfg.TTSBaseURL, "wss://") || strings.HasPrefix(cfg.TTSBaseURL, "http://") || strings.HasPrefix(cfg.TTSBaseURL, "https://") {
				ttsOpts = append(ttsOpts, slng.WithTTSEndpoint(cfg.TTSBaseURL))
			} else {
				ttsOpts = append(ttsOpts, slng.WithTTSBaseURL(cfg.TTSBaseURL))
			}
		}
		if cfg.TTSRegion != "" {
			ttsOpts = append(ttsOpts, slng.WithTTSRegionOverride(cfg.TTSRegion))
		}
		if cfg.TTSVoice != "" {
			ttsOpts = append(ttsOpts, slng.WithTTSVoice(cfg.TTSVoice))
		}
		if cfg.TTSLanguage != "" {
			ttsOpts = append(ttsOpts, slng.WithTTSLanguage(cfg.TTSLanguage))
		}
		if cfg.TTSSampleRate != nil {
			ttsOpts = append(ttsOpts, slng.WithTTSSampleRate(*cfg.TTSSampleRate))
		}
		if cfg.TTSSpeed != 0 {
			ttsOpts = append(ttsOpts, slng.WithTTSSpeed(cfg.TTSSpeed))
		}
		if len(cfg.TTSModelOptions) > 0 {
			ttsOpts = append(ttsOpts, slng.WithTTSModelOptions(cfg.TTSModelOptions))
		}
		a.TTS = slng.NewTTS(cfg.SLNGAPIKey, ttsOpts...)
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
	case providerLiveKit:
		ttsOpts := []inference.TTSOption{}
		tokenizer, err := ttsSentenceTokenizer(cfg)
		if err != nil {
			return nil, err
		}
		if tokenizer != nil {
			ttsOpts = append(ttsOpts, inference.WithSentenceTokenizer(tokenizer))
		}
		a.TTS = inference.NewTTS(cfg.TTSModel, cfg.LiveKitInferenceAPIKey, cfg.LiveKitInferenceAPISecret, ttsOpts...)
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_TTS_PROVIDER %q", cfg.TTSProvider)
	}
	if err := configureTTSFallbacks(cfg, a); err != nil {
		return nil, err
	}

	switch normalizeProvider(cfg.RealtimeProvider) {
	case "":
		return nil, nil
	case providerOpenAI:
		return openai.NewRealtimeModel(cfg.OpenAIAPIKey, cfg.RealtimeModel), nil
	case providerPhonic:
		return phonic.NewRealtimeModel(cfg.PhonicAPIKey)
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

func agentSessionOptionsFromConfig(cfg AppConfig) (agent.AgentSessionOptions, error) {
	opts := agent.AgentSessionOptions{}
	if cfg.TTSStreamPacerEnabled {
		pacer := coretts.SentenceStreamPacerOptions{}
		if cfg.TTSStreamPacerMinRemainingAudioMS != nil {
			pacer.MinRemainingAudio = time.Duration(*cfg.TTSStreamPacerMinRemainingAudioMS) * time.Millisecond
		}
		if cfg.TTSStreamPacerMaxTextLength != nil {
			pacer.MaxTextLength = *cfg.TTSStreamPacerMaxTextLength
		}
		opts.TTSStreamPacer = &pacer
	}
	opts.TTSTextReplacements = cfg.TTSTextReplacements
	opts.DisableTTSTextTransforms = cfg.DisableTTSTextTransforms
	opts.LLMParallelToolCalls = cfg.LLMParallelToolCalls
	opts.LLMExtraParams = cfg.LLMExtraBody
	opts.LLMResponseFormat = cfg.LLMResponseFormat
	if cfg.BackgroundAudioAmbient != "" || cfg.BackgroundAudioThinking != "" {
		opts.BackgroundAudio = agent.NewBackgroundAudioPlayer(
			backgroundAudioSource(cfg.BackgroundAudioAmbient),
			backgroundAudioSource(cfg.BackgroundAudioThinking),
		)
	}
	opts.IVRDetection = cfg.IVRDetection
	if cfg.IVRSilenceDurationSeconds != nil {
		opts.IVRSilenceDuration = time.Duration(*cfg.IVRSilenceDurationSeconds * float64(time.Second))
	}
	wordTokenizer, err := wordTokenizerFromConfig(cfg)
	if err != nil {
		return opts, err
	}
	opts.WordTokenizer = wordTokenizer
	return opts, nil
}

func wordTokenizerFromConfig(cfg AppConfig) (tokenize.WordTokenizer, error) {
	provider := normalizeProvider(cfg.WordTokenizerProvider)
	if provider == "" {
		return nil, nil
	}
	switch provider {
	case "basic":
		return tokenize.NewBasicWordTokenizer(), nil
	case "blingfire":
		return blingfire.NewWordTokenizer(cfg.WordTokenizerLanguage), nil
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_WORD_TOKENIZER_PROVIDER %q", cfg.WordTokenizerProvider)
	}
}

func backgroundAudioSource(value string) interface{} {
	switch strings.TrimSpace(value) {
	case "":
		return nil
	case string(agent.CityAmbience):
		return agent.CityAmbience
	case string(agent.ForestAmbience):
		return agent.ForestAmbience
	case string(agent.OfficeAmbience):
		return agent.OfficeAmbience
	case string(agent.CrowdedRoom):
		return agent.CrowdedRoom
	case string(agent.KeyboardTyping):
		return agent.KeyboardTyping
	case string(agent.KeyboardTyping2):
		return agent.KeyboardTyping2
	case string(agent.HoldMusic):
		return agent.HoldMusic
	default:
		return value
	}
}

func ttsSentenceTokenizer(cfg AppConfig) (tokenize.SentenceTokenizer, error) {
	provider := normalizeProvider(cfg.TTSTokenizerProvider)
	if provider == "" {
		return nil, nil
	}

	minSentenceLen := 0
	if cfg.TTSTokenizerMinSentenceLen != nil {
		minSentenceLen = *cfg.TTSTokenizerMinSentenceLen
	}
	streamContextLen := 0
	if cfg.TTSTokenizerStreamContextLen != nil {
		streamContextLen = *cfg.TTSTokenizerStreamContextLen
	}

	switch provider {
	case "advanced":
		return tokenize.NewAdvancedSentenceTokenizer(), nil
	case "blingfire":
		return blingfire.NewSentenceTokenizer(cfg.TTSTokenizerLanguage, minSentenceLen, streamContextLen), nil
	case "nltk":
		return nltk.NewSentenceTokenizer(cfg.TTSTokenizerLanguage, minSentenceLen, streamContextLen), nil
	default:
		return nil, fmt.Errorf("unsupported RTP_AGENT_TTS_TOKENIZER_PROVIDER %q", cfg.TTSTokenizerProvider)
	}
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
	return splitStringList(raw)
}

func mcpStdioServersFromEnv(name string) []MCPStdioServerConfig {
	raw := os.Getenv(name)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var servers []MCPStdioServerConfig
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		return nil
	}
	return servers
}

func mcpHTTPServersFromEnv(name string) []MCPHTTPServerConfig {
	raw := os.Getenv(name)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var servers []MCPHTTPServerConfig
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		return nil
	}
	return servers
}

func sipOutboundConfigFromEnv(name string) *livekit.SIPOutboundConfig {
	raw := os.Getenv(name)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	config := &livekit.SIPOutboundConfig{}
	if err := json.Unmarshal([]byte(raw), config); err != nil {
		return nil
	}
	return config
}

func jsonEnvMap(name string) map[string]any {
	raw := os.Getenv(name)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func splitStringList(raw string) []string {
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

func splitEnvAnyList(name string) []any {
	values := splitEnvList(name)
	if len(values) == 0 {
		return nil
	}
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	return items
}

func splitEnvStringSliceMap(name string) map[string][]string {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	values := map[string][]string{}
	for _, part := range strings.Split(raw, ",") {
		key, rawValues, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		items := make([]string, 0)
		for _, value := range strings.Split(rawValues, "|") {
			value = strings.TrimSpace(value)
			if value != "" {
				items = append(items, value)
			}
		}
		if len(items) > 0 {
			values[key] = items
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func modelOptionString(options map[string]any, key string) string {
	value, ok := options[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func modelOptionBool(options map[string]any, key string) *bool {
	value, ok := options[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case bool:
		return &typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return nil
		}
		return &parsed
	case float64:
		parsed := typed != 0
		return &parsed
	default:
		return nil
	}
}

func modelOptionFloat(options map[string]any, key string) *float64 {
	value, ok := options[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case float64:
		return &typed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return nil
		}
		return &parsed
	default:
		return nil
	}
}

func modelOptionStringList(options map[string]any, key string) []string {
	raw := modelOptionString(options, key)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "|")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func sonioxContextObjectFromModelOptions(options map[string]any) (soniox.SonioxContextObject, bool) {
	var context soniox.SonioxContextObject
	context.Text = modelOptionString(options, "context_text")
	context.Terms = modelOptionStringList(options, "context_terms")
	context.General = sonioxContextGeneralItems(modelOptionString(options, "context_general"))
	context.TranslationTerms = sonioxContextTranslationTerms(modelOptionString(options, "context_translation_terms"))
	ok := context.Text != "" || len(context.Terms) > 0 || len(context.General) > 0 || len(context.TranslationTerms) > 0
	return context, ok
}

func sonioxContextGeneralItems(raw string) []soniox.SonioxContextGeneralItem {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "|")
	items := make([]soniox.SonioxContextGeneralItem, 0, len(parts))
	for _, part := range parts {
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			items = append(items, soniox.SonioxContextGeneralItem{Key: key, Value: value})
		}
	}
	return items
}

func sonioxContextTranslationTerms(raw string) []soniox.SonioxContextTranslationTerm {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "|")
	terms := make([]soniox.SonioxContextTranslationTerm, 0, len(parts))
	for _, part := range parts {
		source, target, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		source = strings.TrimSpace(source)
		target = strings.TrimSpace(target)
		if source != "" && target != "" {
			terms = append(terms, soniox.SonioxContextTranslationTerm{Source: source, Target: target})
		}
	}
	return terms
}

func speechmaticsAdditionalVocab(values []string) []speechmatics.SpeechmaticsAdditionalVocabEntry {
	entries := make([]speechmatics.SpeechmaticsAdditionalVocabEntry, 0, len(values))
	for _, value := range values {
		content, soundsLike, _ := strings.Cut(value, ":")
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		entry := speechmatics.SpeechmaticsAdditionalVocabEntry{Content: content}
		if soundsLike != "" {
			entry.SoundsLike = splitPipeList(soundsLike)
		}
		entries = append(entries, entry)
	}
	return entries
}

func speechmaticsKnownSpeakers(raw string) []speechmatics.SpeechmaticsSpeakerIdentifier {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "|")
	speakers := make([]speechmatics.SpeechmaticsSpeakerIdentifier, 0, len(parts))
	for _, part := range parts {
		label, speakerID, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		label = strings.TrimSpace(label)
		speakerID = strings.TrimSpace(speakerID)
		if label != "" && speakerID != "" {
			speakers = append(speakers, speechmatics.SpeechmaticsSpeakerIdentifier{Label: label, SpeakerID: speakerID})
		}
	}
	return speakers
}

func speechmaticsPunctuationOverrides(options map[string]any) map[string]interface{} {
	marks := modelOptionStringList(options, "permitted_marks")
	if len(marks) == 0 {
		return nil
	}
	return map[string]interface{}{"permitted_marks": marks}
}

func anthropicProviderTools(cfg AppConfig) []llm.Tool {
	tools := make([]llm.Tool, 0, len(cfg.AnthropicTools))
	for _, tool := range cfg.AnthropicTools {
		switch normalizeProvider(tool) {
		case "computer", "computer_use", "computeruse":
			width := 1024
			if cfg.AnthropicComputerWidth != nil {
				width = *cfg.AnthropicComputerWidth
			}
			height := 768
			if cfg.AnthropicComputerHeight != nil {
				height = *cfg.AnthropicComputerHeight
			}
			computer := anthropic.NewComputerTool(browser.NewPageActions(), width, height)
			tools = append(tools, computer.Tools()...)
		}
	}
	return tools
}

func configureAppTools(cfg AppConfig, a *agent.Agent, session *agent.AgentSession) error {
	if len(cfg.AppTools) == 0 {
		return nil
	}
	tools := make([]llm.Tool, 0, len(cfg.AppTools))
	for _, tool := range cfg.AppTools {
		switch normalizeProvider(tool) {
		case "end_call", "endcall":
			tools = append(tools, betatools.NewSessionEndCallTool(session, betatools.EndCallToolOptions{}))
		case "send_dtmf", "send_dtmf_events", "senddtmf":
			continue
		default:
			return fmt.Errorf("unsupported RTP_AGENT_TOOLS value %q", tool)
		}
	}
	a.Tools = append(a.Tools, tools...)
	return nil
}

func configureMCPTools(ctx context.Context, cfg AppConfig, a *agent.Agent) ([]llm.MCPServer, error) {
	if len(cfg.MCPStdioServers) == 0 && len(cfg.MCPHTTPServers) == 0 {
		return nil, nil
	}
	if a == nil {
		return nil, fmt.Errorf("MCP tools require an agent")
	}

	servers := make([]llm.MCPServer, 0, len(cfg.MCPStdioServers)+len(cfg.MCPHTTPServers))
	for _, serverConfig := range cfg.MCPStdioServers {
		command := strings.TrimSpace(serverConfig.Command)
		if command == "" {
			closeMCPServers(servers)
			return nil, fmt.Errorf("RTP_AGENT_MCP_STDIO_SERVERS entry requires command")
		}
		server := llm.NewMCPServerStdio(command, serverConfig.Args)
		server.Env = serverConfig.Env
		server.Cwd = serverConfig.Cwd

		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := server.Initialize(initCtx)
		cancel()
		if err != nil {
			_ = server.Close()
			closeMCPServers(servers)
			return nil, err
		}

		listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		tools, err := server.ListTools(listCtx)
		cancel()
		if err != nil {
			_ = server.Close()
			closeMCPServers(servers)
			return nil, err
		}

		a.Tools = appendMissingToolsByID(a.Tools, tools...)
		servers = append(servers, server)
	}
	for _, serverConfig := range cfg.MCPHTTPServers {
		url := strings.TrimSpace(serverConfig.URL)
		if url == "" {
			closeMCPServers(servers)
			return nil, fmt.Errorf("RTP_AGENT_MCP_HTTP_SERVERS entry requires url")
		}
		server := appNewMCPServerHTTP(url)
		server.TransportType = serverConfig.TransportType
		server.AllowedTools = serverConfig.AllowedTools
		server.Headers = serverConfig.Headers

		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := server.Initialize(initCtx)
		cancel()
		if err != nil {
			_ = server.Close()
			closeMCPServers(servers)
			return nil, err
		}

		listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		tools, err := server.ListTools(listCtx)
		cancel()
		if err != nil {
			_ = server.Close()
			closeMCPServers(servers)
			return nil, err
		}

		a.Tools = appendMissingToolsByID(a.Tools, tools...)
		servers = append(servers, server)
	}
	return servers, nil
}

func configureRoomTools(cfg AppConfig, a *agent.Agent, publisher betatools.DtmfPublisher) error {
	if len(cfg.AppTools) == 0 && !cfg.IVRDetection {
		return nil
	}
	tools := make([]llm.Tool, 0, len(cfg.AppTools))
	if cfg.IVRDetection {
		tools = append(tools, betatools.NewSendDTMFTool(publisher))
	}
	for _, tool := range cfg.AppTools {
		switch normalizeProvider(tool) {
		case "send_dtmf", "send_dtmf_events", "senddtmf":
			tools = append(tools, betatools.NewSendDTMFTool(publisher))
		case "end_call", "endcall":
			continue
		default:
			return fmt.Errorf("unsupported RTP_AGENT_TOOLS value %q", tool)
		}
	}
	a.Tools = appendMissingToolsByID(a.Tools, tools...)
	return nil
}

func appendMissingToolsByID(existing []llm.Tool, additions ...llm.Tool) []llm.Tool {
	seen := make(map[string]struct{}, len(existing)+len(additions))
	for _, tool := range existing {
		if tool != nil {
			seen[tool.ID()] = struct{}{}
		}
	}
	for _, tool := range additions {
		if tool == nil {
			continue
		}
		if _, ok := seen[tool.ID()]; ok {
			continue
		}
		existing = append(existing, tool)
		seen[tool.ID()] = struct{}{}
	}
	return existing
}

func xaiProviderTools(cfg AppConfig) []llm.Tool {
	tools := make([]llm.Tool, 0, len(cfg.XAITools))
	for _, tool := range cfg.XAITools {
		switch normalizeProvider(tool) {
		case "web_search", "websearch", "xai_web_search":
			tools = append(tools, &xai.WebSearchTool{})
		case "x_search", "xsearch", "xai_x_search":
			tools = append(tools, &xai.XSearchTool{AllowedHandles: cfg.XAIAllowedXHandles})
		case "file_search", "filesearch", "xai_file_search":
			item := &xai.FileSearchTool{VectorStoreIDs: cfg.XAIFileSearchVectorStoreIDs}
			if cfg.XAIFileSearchMaxResults != nil {
				item.MaxNumResults = *cfg.XAIFileSearchMaxResults
			}
			tools = append(tools, item)
		}
	}
	return tools
}

func splitPipeList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "|")
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

func splitEnvHumeTTSUtterances(name string) []hume.HumeTTSUtterance {
	values := splitEnvList(name)
	if len(values) == 0 {
		return nil
	}
	utterances := make([]hume.HumeTTSUtterance, 0, len(values))
	for _, value := range values {
		utterances = append(utterances, hume.HumeTTSUtterance{Text: value})
	}
	return utterances
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

func splitEnvMap(name string) map[string]any {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	values := map[string]any{}
	for _, part := range strings.Split(raw, ",") {
		key, rawValue, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		rawValue = strings.TrimSpace(rawValue)
		if key == "" || rawValue == "" {
			continue
		}
		if value, err := strconv.ParseFloat(rawValue, 64); err == nil {
			values[key] = value
		} else {
			values[key] = rawValue
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func splitEnvStringMap(name string) map[string]string {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	values := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		key, rawValue, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		rawValue = strings.TrimSpace(rawValue)
		if key != "" && rawValue != "" {
			values[key] = rawValue
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}
