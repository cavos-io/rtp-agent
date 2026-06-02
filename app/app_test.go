package app

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestDefaultConfigFromEnvSelectsOpenAIProviders(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_REALTIME_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "gpt-test")
	t.Setenv("RTP_AGENT_STT_MODEL", "gpt-transcribe-test")
	t.Setenv("RTP_AGENT_TTS_MODEL", "gpt-4o-mini-tts")
	t.Setenv("RTP_AGENT_TTS_VOICE", "alloy")
	t.Setenv("RTP_AGENT_REALTIME_MODEL", "gpt-realtime-test")

	cfg := DefaultConfigFromEnv()

	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "gpt-test" {
		t.Fatalf("LLM model = %q, want gpt-test", got)
	}
	if app.Session.STT == nil {
		t.Fatal("STT is nil")
	}
	if got := tts.Provider(app.Session.TTS); got != "openai" {
		t.Fatalf("TTS provider = %q, want openai", got)
	}
	if app.RealtimeModel == nil {
		t.Fatal("RealtimeModel is nil")
	}
	if got := llm.RealtimeModelName(app.RealtimeModel); got != "gpt-realtime-test" {
		t.Fatalf("Realtime model = %q, want gpt-realtime-test", got)
	}
}

func TestDefaultConfigFromEnvSelectsAssemblyAISTT(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "test-assemblyai-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "assemblyai")
	t.Setenv("RTP_AGENT_STT_MODEL", "u3-pro")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://streaming.eu.assemblyai.com/")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "8000")
	t.Setenv("RTP_AGENT_STT_SPEAKER_LABELS", "true")
	t.Setenv("RTP_AGENT_STT_MAX_SPEAKERS", "2")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := app.Session.STT.Label(); got != "assemblyai.STT" {
		t.Fatalf("STT label = %q, want assemblyai.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Diarization {
		t.Fatalf("STT Capabilities().Diarization = false, want true")
	}
}

func TestDefaultConfigFromEnvSelectsAsyncAITTS(t *testing.T) {
	t.Setenv("ASYNCAI_API_KEY", "test-asyncai-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "asyncai")
	t.Setenv("RTP_AGENT_TTS_MODEL", "async_test_model")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://async.example/")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_mulaw")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "8000")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "asyncai.TTS" {
		t.Fatalf("TTS label = %q, want asyncai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 8000 {
		t.Fatalf("TTS sample rate = %d, want 8000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming {
		t.Fatalf("TTS Capabilities().Streaming = false, want true")
	}
}

func TestDefaultConfigFromEnvSelectsCambaiTTS(t *testing.T) {
	t.Setenv("CAMB_API_KEY", "test-cambai-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "cambai")
	t.Setenv("RTP_AGENT_TTS_MODEL", "mars-pro")
	t.Setenv("RTP_AGENT_TTS_VOICE", "123")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-us")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_s16le")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://cambai.example/apis")
	t.Setenv("RTP_AGENT_TTS_INSTRUCTIONS", "speak clearly")
	t.Setenv("RTP_AGENT_TTS_ENHANCE_NAMED_ENTITIES", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "cambai.TTS" {
		t.Fatalf("TTS label = %q, want cambai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
}

func TestDefaultConfigFromEnvSelectsElevenLabsSpeechProviders(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "test-elevenlabs-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "elevenlabs")
	t.Setenv("RTP_AGENT_STT_MODEL", "scribe_v2_realtime")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://elevenlabs.example/v1")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "alpha,beta")
	t.Setenv("RTP_AGENT_STT_VAD_THRESHOLD", "0.6")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "elevenlabs")
	t.Setenv("RTP_AGENT_TTS_MODEL", "eleven_turbo_v2_5")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_24000")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://elevenlabs.example/v1")
	t.Setenv("RTP_AGENT_TTS_ENABLE_SSML_PARSING", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "elevenlabs.STT" {
		t.Fatalf("STT label = %q, want elevenlabs.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || caps.AlignedTranscript != "" {
		t.Fatalf("STT capabilities = %+v, want streaming without timestamps", caps)
	}
	if got := app.Session.TTS.Label(); got != "elevenlabs.TTS" {
		t.Fatalf("TTS label = %q, want elevenlabs.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || !caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsCartesiaSpeechProviders(t *testing.T) {
	t.Setenv("CARTESIA_API_KEY", "test-cartesia-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "cartesia")
	t.Setenv("RTP_AGENT_STT_MODEL", "ink-2")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://cartesia.example")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_ENCODING", "pcm_s16le")
	t.Setenv("RTP_AGENT_STT_AUDIO_CHUNK_DURATION_MS", "120")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "cartesia")
	t.Setenv("RTP_AGENT_TTS_MODEL", "sonic-3")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_s16le")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "44100")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://cartesia.example")
	t.Setenv("RTP_AGENT_TTS_API_VERSION", "2025-04-16")
	t.Setenv("RTP_AGENT_TTS_WORD_TIMESTAMPS", "false")
	t.Setenv("RTP_AGENT_TTS_VOICE_EMBEDDING", "0.1,0.2")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_EMOTION", "positivity")
	t.Setenv("RTP_AGENT_TTS_VOLUME", "0.8")
	t.Setenv("RTP_AGENT_TTS_PRONUNCIATION_DICT_ID", "dict-test")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "cartesia.STT" {
		t.Fatalf("STT label = %q, want cartesia.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults {
		t.Fatalf("STT capabilities = %+v, want streaming interim results", caps)
	}
	if got := app.Session.TTS.Label(); got != "cartesia.TTS" {
		t.Fatalf("TTS label = %q, want cartesia.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 44100 {
		t.Fatalf("TTS sample rate = %d, want 44100", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsClovaSpeechProviders(t *testing.T) {
	t.Setenv("CLOVA_STT_SECRET", "test-clova-stt-secret")
	t.Setenv("CLOVA_STT_INVOKE_URL", "https://clova.example/stt")
	t.Setenv("CLOVA_CLIENT_ID", "test-clova-client-id")
	t.Setenv("CLOVA_CLIENT_SECRET", "test-clova-client-secret")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "clova")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "ko")
	t.Setenv("RTP_AGENT_STT_VAD_THRESHOLD", "0.6")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "clova")
	t.Setenv("RTP_AGENT_TTS_VOICE", "nara")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "clova.STT" {
		t.Fatalf("STT label = %q, want clova.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); caps.Streaming || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want offline recognize only", caps)
	}
	if got := app.Session.TTS.Label(); got != "clova.TTS" {
		t.Fatalf("TTS label = %q, want clova.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
}

func TestDefaultConfigFromEnvSelectsDeepgramSpeechProviders(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "test-deepgram-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_MODEL", "nova-3")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://deepgram.example/v1/listen")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_NUMBER_OF_CHANNELS", "2")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "true")
	t.Setenv("RTP_AGENT_STT_PUNCTUATE", "true")
	t.Setenv("RTP_AGENT_STT_SMART_FORMAT", "true")
	t.Setenv("RTP_AGENT_STT_NO_DELAY", "true")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_MS", "75")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "true")
	t.Setenv("RTP_AGENT_STT_FILLER_WORDS", "true")
	t.Setenv("RTP_AGENT_STT_VAD_EVENTS", "true")
	t.Setenv("RTP_AGENT_STT_PROFANITY_FILTER", "true")
	t.Setenv("RTP_AGENT_STT_NUMERALS", "true")
	t.Setenv("RTP_AGENT_STT_MIP_OPT_OUT", "true")
	t.Setenv("RTP_AGENT_STT_KEYWORDS", "agent:1.5,voice")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "alpha,beta")
	t.Setenv("RTP_AGENT_STT_REDACT", "pci,ssn")
	t.Setenv("RTP_AGENT_STT_TAGS", "test,app")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_TTS_MODEL", "aura-2-andromeda-en")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://deepgram.example/v1/speak")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "linear16")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "32000")
	t.Setenv("RTP_AGENT_TTS_MIP_OPT_OUT", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "deepgram.STT" {
		t.Fatalf("STT label = %q, want deepgram.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.Diarization || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming diarization offline recognize", caps)
	}
	if got := app.Session.TTS.Label(); got != "deepgram.TTS" {
		t.Fatalf("TTS label = %q, want deepgram.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 32000 {
		t.Fatalf("TTS sample rate = %d, want 32000", got)
	}
}

func TestDefaultConfigFromEnvSelectsFishAudioTTS(t *testing.T) {
	t.Setenv("FISHAUDIO_API_KEY", "test-fishaudio-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "fishaudio")
	t.Setenv("RTP_AGENT_TTS_MODEL", "s2-pro")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://fishaudio.example")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "opus")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "48000")
	t.Setenv("RTP_AGENT_TTS_LATENCY_MODE", "balanced")
	t.Setenv("RTP_AGENT_TTS_CHUNK_LENGTH", "120")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "fishaudio.TTS" {
		t.Fatalf("TTS label = %q, want fishaudio.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming {
		t.Fatalf("TTS capabilities = %+v, want streaming", caps)
	}
}

func TestDefaultConfigFromEnvSelectsAWSProviders(t *testing.T) {
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "aws")
	t.Setenv("RTP_AGENT_LLM_MODEL", "amazon.nova-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "aws")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_STT_SPEAKER_LABELS", "true")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "aws")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Joanna")
	t.Setenv("RTP_AGENT_TTS_MODEL", "standard")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "22050")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "AWS Bedrock" {
		t.Fatalf("LLM provider = %q, want AWS Bedrock", got)
	}
	if got := app.Session.STT.Label(); got != "aws.STT" {
		t.Fatalf("STT label = %q, want aws.STT", got)
	}
	if got := app.Session.TTS.Label(); got != "aws.TTS" {
		t.Fatalf("TTS label = %q, want aws.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 22050 {
		t.Fatalf("TTS sample rate = %d, want 22050", got)
	}
}

func TestDefaultConfigFromEnvSelectsAzureSpeechProviders(t *testing.T) {
	t.Setenv("AZURE_SPEECH_KEY", "test-azure-key")
	t.Setenv("AZURE_SPEECH_REGION", "eastus")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "azure")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "azure")
	t.Setenv("RTP_AGENT_TTS_VOICE", "en-US-AvaNeural")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "azure.STT" {
		t.Fatalf("STT label = %q, want azure.STT", got)
	}
	if got := app.Session.TTS.Label(); got != "azure.TTS" {
		t.Fatalf("TTS label = %q, want azure.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
}

func TestDefaultConfigFromEnvSelectsBasetenProviders(t *testing.T) {
	t.Setenv("BASETEN_API_KEY", "test-baseten-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "baseten")
	t.Setenv("RTP_AGENT_LLM_MODEL", "llama-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "baseten")
	t.Setenv("RTP_AGENT_STT_MODEL", "stt-test")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_ENCODING", "pcm_s16le")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_BUFFER_SIZE_SECONDS", "0.064")
	t.Setenv("RTP_AGENT_STT_VAD_THRESHOLD", "0.7")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "baseten")
	t.Setenv("RTP_AGENT_TTS_MODEL", "tts-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "tara")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_TEMPERATURE", "0.6")
	t.Setenv("RTP_AGENT_TTS_MAX_TOKENS", "2000")
	t.Setenv("RTP_AGENT_TTS_BUFFER_SIZE", "10")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "Baseten" {
		t.Fatalf("LLM provider = %q, want Baseten", got)
	}
	if got := app.Session.STT.Label(); got != "baseten.STT" {
		t.Fatalf("STT label = %q, want baseten.STT", got)
	}
	if got := app.Session.TTS.Label(); got != "baseten.TTS" {
		t.Fatalf("TTS label = %q, want baseten.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
}

func TestDefaultConfigFromEnvSelectsGoogleLLM(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-google-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "google")
	t.Setenv("RTP_AGENT_LLM_MODEL", "gemini-test")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "google" {
		t.Fatalf("LLM provider = %q, want google", got)
	}
	if got := llm.Model(app.Session.LLM); got != "gemini-test" {
		t.Fatalf("LLM model = %q, want gemini-test", got)
	}
}

func TestDefaultConfigFromEnvSelectsGroqProviders(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "test-groq-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "groq")
	t.Setenv("RTP_AGENT_LLM_MODEL", "llama3-70b-8192")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "groq")
	t.Setenv("RTP_AGENT_TTS_MODEL", "canopylabs/orpheus-v1-english")
	t.Setenv("RTP_AGENT_TTS_VOICE", "autumn")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://groq.example/openai/v1")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "groq" {
		t.Fatalf("LLM provider = %q, want groq", got)
	}
	if got := llm.Model(app.Session.LLM); got != "llama3-70b-8192" {
		t.Fatalf("LLM model = %q, want llama3-70b-8192", got)
	}
	if got := app.Session.TTS.Label(); got != "groq.TTS" {
		t.Fatalf("TTS label = %q, want groq.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
}

func TestDefaultConfigFromEnvSelectsCerebrasLLM(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "test-cerebras-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "cerebras")
	t.Setenv("RTP_AGENT_LLM_MODEL", "llama3.1-test")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "cerebras" {
		t.Fatalf("LLM provider = %q, want cerebras", got)
	}
	if got := llm.Model(app.Session.LLM); got != "llama3.1-test" {
		t.Fatalf("LLM model = %q, want llama3.1-test", got)
	}
}

func TestDefaultConfigFromEnvSelectsLiveKitInferenceLLM(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "test-livekit-key")
	t.Setenv("LIVEKIT_API_SECRET", "test-livekit-secret")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "livekit")
	t.Setenv("RTP_AGENT_LLM_MODEL", "openai/gpt-4.1-mini")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "livekit" {
		t.Fatalf("LLM provider = %q, want livekit", got)
	}
}

func TestDefaultConfigFromEnvSelectsAnthropicLLM(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "anthropic")
	t.Setenv("RTP_AGENT_LLM_MODEL", "claude-test")
	t.Setenv("RTP_AGENT_LLM_BASE_URL", "https://anthropic.example/")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "anthropic" {
		t.Fatalf("LLM provider = %q, want anthropic", got)
	}
	if got := llm.Model(app.Session.LLM); got != "claude-test" {
		t.Fatalf("LLM model = %q, want claude-test", got)
	}
}

func TestInitRegistersWorkerEntrypoint(t *testing.T) {
	app, err := Init(AppConfig{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if app.Server == nil {
		t.Fatal("Server is nil")
	}
	err = app.Server.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want missing ws_url precondition error")
	}
	if err.Error() != "ws_url is required, or set LIVEKIT_URL environment variable" {
		t.Fatalf("Run() error = %q, want missing ws_url after registered entrypoint", err.Error())
	}
}
