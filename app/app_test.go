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
