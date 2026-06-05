package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/beta/workflows"
	"github.com/cavos-io/rtp-agent/core/evals"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/interface/worker"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/plugin"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/livekit/protocol/livekit"
	livekitlogger "github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestAppRegistersSLNGPluginMetadata(t *testing.T) {
	for _, registered := range plugin.RegisteredPlugins() {
		if registered.Package() != "livekit.plugins.slng" {
			continue
		}
		if registered.Title() != "livekit.plugins.slng" {
			t.Fatalf("plugin title = %q, want livekit.plugins.slng", registered.Title())
		}
		if registered.Version() != "1.5.15" {
			t.Fatalf("plugin version = %q, want reference version", registered.Version())
		}
		return
	}
	t.Fatal("SLNG plugin metadata was not registered")
}

func TestNewAppInstallsConfiguredLogger(t *testing.T) {
	previous := logutil.Logger
	t.Cleanup(func() { logutil.Logger = previous })

	recorder := &appRecordingLogger{}
	app, err := NewApp(AppConfig{Logger: recorder})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app == nil {
		t.Fatal("NewApp() returned nil app")
	}
	if logutil.Logger != recorder {
		t.Fatal("NewApp() did not install configured logger")
	}
}

func TestNewAppUsesConfiguredMetricsRegistry(t *testing.T) {
	registry := telemetry.NewMetricRegistry()
	app, err := NewApp(AppConfig{
		WorkerOptions:   worker.WorkerOptions{AgentName: "metrics-agent"},
		MetricsRegistry: registry,
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	want := registry.GetUsageCollector(telemetry.MetricLabels{AgentName: "metrics-agent"})
	if app.Session.MetricsCollector != want {
		t.Fatal("Session MetricsCollector was not allocated from configured registry")
	}
}

func TestDefaultConfigFromEnvConfiguresTelemetryLogs(t *testing.T) {
	t.Setenv("RTP_AGENT_OTLP_LOGS_ENDPOINT", "otel.example:4318")
	t.Setenv("RTP_AGENT_OTLP_LOGS_HEADERS", "Authorization=Bearer token,X-Scope=agent")

	cfg := DefaultConfigFromEnv()

	if cfg.TelemetryLogsEndpoint != "otel.example:4318" {
		t.Fatalf("TelemetryLogsEndpoint = %q, want otel.example:4318", cfg.TelemetryLogsEndpoint)
	}
	if got := cfg.TelemetryLogsHeaders["Authorization"]; got != "Bearer token" {
		t.Fatalf("TelemetryLogsHeaders[Authorization] = %q, want Bearer token", got)
	}
	if got := cfg.TelemetryLogsHeaders["X-Scope"]; got != "agent" {
		t.Fatalf("TelemetryLogsHeaders[X-Scope] = %q, want agent", got)
	}
}

func TestNewAppInitializesAndClosesTelemetryLogs(t *testing.T) {
	var initializedEndpoint string
	var initializedHeaders map[string]string
	var shutdownCalled bool
	oldInit := appInitLoggerProvider
	oldShutdown := appShutdownLoggerProvider
	appInitLoggerProvider = func(ctx context.Context, endpoint string, headers map[string]string) error {
		initializedEndpoint = endpoint
		initializedHeaders = headers
		return nil
	}
	appShutdownLoggerProvider = func(ctx context.Context) error {
		shutdownCalled = true
		return nil
	}
	t.Cleanup(func() {
		appInitLoggerProvider = oldInit
		appShutdownLoggerProvider = oldShutdown
	})

	app, err := NewApp(AppConfig{
		TelemetryLogsEndpoint: "otel.example:4318",
		TelemetryLogsHeaders:  map[string]string{"Authorization": "Bearer token"},
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if initializedEndpoint != "otel.example:4318" {
		t.Fatalf("initialized endpoint = %q, want otel.example:4318", initializedEndpoint)
	}
	if initializedHeaders["Authorization"] != "Bearer token" {
		t.Fatalf("initialized headers = %#v, want Authorization header", initializedHeaders)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !shutdownCalled {
		t.Fatal("Close() did not shut down telemetry log provider")
	}
}

func TestRunSessionUsesJobMetricLabels(t *testing.T) {
	registry := telemetry.NewMetricRegistry()
	app, err := NewApp(AppConfig{
		WorkerOptions:   worker.WorkerOptions{AgentName: "metrics-agent"},
		MetricsRegistry: registry,
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	jobCtx := worker.NewJobContext(&livekit.Job{
		Id:   "job_metrics",
		Room: &livekit.Room{Name: "metrics-room"},
	}, "", "", "")
	if err := app.runSession(jobCtx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}

	want := registry.GetUsageCollector(telemetry.MetricLabels{
		AgentName:           "metrics-agent",
		RoomName:            "metrics-room",
		ParticipantIdentity: "agent-job_metrics",
	})
	if app.Session.MetricsCollector != want {
		t.Fatal("Session MetricsCollector was not allocated from job metric labels")
	}
}

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
	if _, ok := app.Session.Assistant.(*agent.MultimodalAgent); !ok {
		t.Fatalf("Session assistant = %T, want *agent.MultimodalAgent", app.Session.Assistant)
	}
}

func TestDefaultConfigFromEnvSelectsPerplexityLLM(t *testing.T) {
	t.Setenv("PERPLEXITY_API_KEY", "test-perplexity-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "perplexity")
	t.Setenv("RTP_AGENT_LLM_MODEL", "sonar")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "sonar" {
		t.Fatalf("LLM model = %q, want sonar", got)
	}
}

func TestDefaultConfigFromEnvSelectsNvidiaLLM(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "test-nvidia-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "nvidia")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "nvidia.NvidiaLLM" {
		t.Fatalf("LLM label = %q, want nvidia.NvidiaLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsLangChainLLM(t *testing.T) {
	t.Setenv("LANGCHAIN_API_KEY", "test-langchain-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "langchain")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "langchain.LangchainLLM" {
		t.Fatalf("LLM label = %q, want langchain.LangchainLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsMinimalLLM(t *testing.T) {
	t.Setenv("MINIMAL_API_KEY", "test-minimal-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "minimal")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "minimal.MinimalLLM" {
		t.Fatalf("LLM label = %q, want minimal.MinimalLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsSimliLLM(t *testing.T) {
	t.Setenv("SIMLI_API_KEY", "test-simli-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "simli")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "simli.SimliLLM" {
		t.Fatalf("LLM label = %q, want simli.SimliLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsHedraLLM(t *testing.T) {
	t.Setenv("HEDRA_API_KEY", "test-hedra-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "hedra")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "hedra.HedraLLM" {
		t.Fatalf("LLM label = %q, want hedra.HedraLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsLemonSliceLLM(t *testing.T) {
	t.Setenv("LEMONSLICE_API_KEY", "test-lemonslice-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "lemonslice")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "lemonslice.LemonSliceLLM" {
		t.Fatalf("LLM label = %q, want lemonslice.LemonSliceLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsTrugenLLM(t *testing.T) {
	t.Setenv("TRUGEN_API_KEY", "test-trugen-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "trugen")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "trugen.TrugenLLM" {
		t.Fatalf("LLM label = %q, want trugen.TrugenLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsUpliftAIProviders(t *testing.T) {
	t.Setenv("UPLIFTAI_API_KEY", "test-upliftai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "upliftai")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "upliftai")
	t.Setenv("RTP_AGENT_TTS_VOICE", "bright")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "upliftai.UpliftAILLM" {
		t.Fatalf("LLM label = %q, want upliftai.UpliftAILLM", got)
	}
	if got := app.Session.TTS.Label(); got != "upliftai.TTS" {
		t.Fatalf("TTS label = %q, want upliftai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsUltravoxTTS(t *testing.T) {
	t.Setenv("ULTRAVOX_API_KEY", "test-ultravox-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "ultravox")
	t.Setenv("RTP_AGENT_TTS_VOICE", "alloy")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "ultravox.TTS" {
		t.Fatalf("TTS label = %q, want ultravox.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSileroVAD(t *testing.T) {
	t.Setenv("RTP_AGENT_VAD_PROVIDER", "silero")
	t.Setenv("RTP_AGENT_VAD_SAMPLE_RATE", "8000")
	t.Setenv("RTP_AGENT_VAD_MIN_SPEECH_DURATION", "0.08")
	t.Setenv("RTP_AGENT_VAD_MIN_SILENCE_DURATION", "0.2")
	t.Setenv("RTP_AGENT_VAD_PREFIX_PADDING_DURATION", "0.1")
	t.Setenv("RTP_AGENT_VAD_MAX_BUFFERED_SPEECH", "2.5")
	t.Setenv("RTP_AGENT_VAD_ACTIVATION_THRESHOLD", "0.7")
	t.Setenv("RTP_AGENT_VAD_DEACTIVATION_THRESHOLD", "0.4")
	t.Setenv("RTP_AGENT_VAD_UPDATE_INTERVAL", "0.064")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.VAD == nil {
		t.Fatal("Session VAD is nil")
	}
	if got := app.Session.VAD.Label(); got != "silero.VAD" {
		t.Fatalf("VAD label = %q, want silero.VAD", got)
	}
	if caps := app.Session.VAD.Capabilities(); caps.UpdateInterval != 0.064 {
		t.Fatalf("VAD capabilities = %+v, want update interval 0.064", caps)
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

func TestDefaultConfigFromEnvWrapsSTTWithMultiSpeakerAdapter(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "test-assemblyai-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "assemblyai")
	t.Setenv("RTP_AGENT_STT_SPEAKER_LABELS", "true")
	t.Setenv("RTP_AGENT_STT_MULTI_SPEAKER", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	wrapped, ok := app.Session.STT.(*stt.MultiSpeakerAdapter)
	if !ok {
		t.Fatalf("Session STT = %T, want *stt.MultiSpeakerAdapter", app.Session.STT)
	}
	if caps := wrapped.Capabilities(); !caps.Streaming || !caps.Diarization {
		t.Fatalf("wrapped STT capabilities = %+v, want streaming diarization", caps)
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

func TestDefaultConfigFromEnvSelectsFalProviders(t *testing.T) {
	t.Setenv("FAL_KEY", "test-fal-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "fal")
	t.Setenv("RTP_AGENT_LLM_MODEL", "fal-ai/llm-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "fal")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_TASK", "translate")
	t.Setenv("RTP_AGENT_STT_CHUNK_LEVEL", "word")
	t.Setenv("RTP_AGENT_STT_VERSION", "3")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "fal" {
		t.Fatalf("LLM provider = %q, want fal", got)
	}
	if got := llm.Model(app.Session.LLM); got != "fal-ai/llm-test" {
		t.Fatalf("LLM model = %q, want fal-ai/llm-test", got)
	}
	if got := app.Session.STT.Label(); got != "fal.STT" {
		t.Fatalf("STT label = %q, want fal.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); caps.Streaming || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want offline recognize only", caps)
	}
}

func TestDefaultConfigFromEnvSelectsFireworksProviders(t *testing.T) {
	t.Setenv("FIREWORKS_API_KEY", "test-fireworks-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "fireworks")
	t.Setenv("RTP_AGENT_LLM_MODEL", "accounts/fireworks/models/firefunction-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "fireworks")
	t.Setenv("RTP_AGENT_STT_MODEL", "whisper-test")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://fireworks.example/v1")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_PROMPT", "domain prompt")
	t.Setenv("RTP_AGENT_STT_TEMPERATURE", "0.2")
	t.Setenv("RTP_AGENT_STT_SKIP_VAD", "true")
	t.Setenv("RTP_AGENT_STT_TEXT_TIMEOUT_SECONDS", "2.5")
	t.Setenv("RTP_AGENT_STT_TIMESTAMP_GRANULARITIES", "word,segment")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "fireworks" {
		t.Fatalf("LLM provider = %q, want fireworks", got)
	}
	if got := llm.Model(app.Session.LLM); got != "accounts/fireworks/models/firefunction-test" {
		t.Fatalf("LLM model = %q, want accounts/fireworks/models/firefunction-test", got)
	}
	if got := app.Session.STT.Label(); got != "fireworks.STT" {
		t.Fatalf("STT label = %q, want fireworks.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim-only", caps)
	}
}

func TestDefaultConfigFromEnvSelectsGladiaSTT(t *testing.T) {
	t.Setenv("GLADIA_API_KEY", "test-gladia-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "gladia")
	t.Setenv("RTP_AGENT_STT_MODEL", "solaria-1")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://gladia.example/v2/live")
	t.Setenv("RTP_AGENT_STT_LANGUAGE_OPTIONS", "en,fr")
	t.Setenv("RTP_AGENT_STT_CODE_SWITCHING", "true")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "false")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_BIT_DEPTH", "16")
	t.Setenv("RTP_AGENT_STT_NUMBER_OF_CHANNELS", "1")
	t.Setenv("RTP_AGENT_STT_ENCODING", "wav/pcm")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_SECONDS", "0.1")
	t.Setenv("RTP_AGENT_STT_MAX_DURATION_WITHOUT_ENDPOINTING_SECONDS", "4")
	t.Setenv("RTP_AGENT_STT_REGION", "eu-west")
	t.Setenv("RTP_AGENT_STT_CUSTOM_VOCABULARY", "LiveKit,Agents")
	t.Setenv("RTP_AGENT_STT_CUSTOM_SPELLING", "livekit=live kit|live-kit")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_TARGET_LANGUAGES", "es,de")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_MODEL", "base")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_MATCH_ORIGINAL_UTTERANCES", "true")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_LIPSYNC", "true")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_CONTEXT_ADAPTATION", "true")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_CONTEXT", "support call")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_INFORMAL", "true")
	t.Setenv("RTP_AGENT_STT_PRE_PROCESSING_AUDIO_ENHANCER", "true")
	t.Setenv("RTP_AGENT_STT_PRE_PROCESSING_SPEECH_THRESHOLD", "0.7")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := app.Session.STT.Label(); got != "gladia.STT" {
		t.Fatalf("STT label = %q, want gladia.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || caps.InterimResults || caps.AlignedTranscript != "word" {
		t.Fatalf("STT capabilities = %+v, want streaming word-aligned without interim results", caps)
	}
}

func TestDefaultConfigFromEnvSelectsGnaniSpeechProviders(t *testing.T) {
	t.Setenv("GNANI_API_KEY", "test-gnani-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "gnani")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://gnani.example")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en-IN")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_ORGANIZATION_ID", "org-test")
	t.Setenv("RTP_AGENT_STT_USER_ID", "user-test")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "gnani")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://gnani.example")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Karan")
	t.Setenv("RTP_AGENT_TTS_MODEL", "vachana-voice-v3")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "22050")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "linear_pcm")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "wav")
	t.Setenv("RTP_AGENT_TTS_NUMBER_OF_CHANNELS", "1")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_WIDTH", "2")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "hi")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "gnani.STT" {
		t.Fatalf("STT label = %q, want gnani.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming with offline recognize", caps)
	}
	if got := app.Session.TTS.Label(); got != "gnani.TTS" {
		t.Fatalf("TTS label = %q, want gnani.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 22050 {
		t.Fatalf("TTS sample rate = %d, want 22050", got)
	}
}

func TestDefaultConfigFromEnvSelectsGradiumProviders(t *testing.T) {
	t.Setenv("GRADIUM_API_KEY", "test-gradium-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "gradium")
	t.Setenv("RTP_AGENT_LLM_MODEL", "gradium-llm-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "gradium")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://gradium.example/asr")
	t.Setenv("RTP_AGENT_STT_MODEL", "asr-test")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_TEMPERATURE", "0.3")
	t.Setenv("RTP_AGENT_STT_BUFFER_SIZE_SECONDS", "0.12")
	t.Setenv("RTP_AGENT_STT_VAD_BUCKET", "3")
	t.Setenv("RTP_AGENT_STT_VAD_FLUSH", "false")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "gradium")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "wss://gradium.example/tts")
	t.Setenv("RTP_AGENT_TTS_MODEL", "tts-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_VOICE_ID", "voice-id-test")
	t.Setenv("RTP_AGENT_TTS_PRONUNCIATION_DICT_ID", "pronunciation-test")
	t.Setenv("RTP_AGENT_TTS_JSON_CONFIG", "style=clear,pace=1.2")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "gradium.STT" {
		t.Fatalf("STT label = %q, want gradium.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim-only", caps)
	}
	if got := app.Session.TTS.Label(); got != "gradium.TTS" {
		t.Fatalf("TTS label = %q, want gradium.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
}

func TestDefaultConfigFromEnvSelectsInworldProviders(t *testing.T) {
	t.Setenv("INWORLD_API_KEY", "test-inworld-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "inworld")
	t.Setenv("RTP_AGENT_LLM_MODEL", "inworld-llm-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "inworld")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://inworld.example/")
	t.Setenv("RTP_AGENT_STT_MODEL", "inworld-stt-test")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_NUMBER_OF_CHANNELS", "1")
	t.Setenv("RTP_AGENT_STT_VOICE_PROFILE", "false")
	t.Setenv("RTP_AGENT_STT_VOICE_PROFILE_TOP_N", "2")
	t.Setenv("RTP_AGENT_STT_VAD_THRESHOLD", "0.4")
	t.Setenv("RTP_AGENT_STT_MIN_END_OF_TURN_SILENCE_WHEN_CONFIDENT", "180")
	t.Setenv("RTP_AGENT_STT_END_OF_TURN_CONFIDENCE_THRESHOLD", "0.45")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "inworld")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://inworld.example/")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://inworld.example/")
	t.Setenv("RTP_AGENT_TTS_MODEL", "inworld-tts-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Ashley")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "PCM")
	t.Setenv("RTP_AGENT_TTS_BIT_RATE", "64000")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "22050")
	t.Setenv("RTP_AGENT_TTS_SPEAKING_RATE", "1.1")
	t.Setenv("RTP_AGENT_TTS_TEMPERATURE", "0.8")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_TTS_TIMESTAMP_TYPE", "WORD")
	t.Setenv("RTP_AGENT_TTS_TEXT_NORMALIZATION", "true")
	t.Setenv("RTP_AGENT_TTS_DELIVERY_MODE", "STREAM")
	t.Setenv("RTP_AGENT_TTS_TIMESTAMP_TRANSPORT_STRATEGY", "ASYNC")
	t.Setenv("RTP_AGENT_TTS_BUFFER_CHAR_THRESHOLD", "90")
	t.Setenv("RTP_AGENT_TTS_MAX_BUFFER_DELAY_MS", "1200")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "inworld.STT" {
		t.Fatalf("STT label = %q, want inworld.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim-only", caps)
	}
	if got := app.Session.TTS.Label(); got != "inworld.TTS" {
		t.Fatalf("TTS label = %q, want inworld.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 22050 {
		t.Fatalf("TTS sample rate = %d, want 22050", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || !caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsHumeProviders(t *testing.T) {
	t.Setenv("HUME_API_KEY", "test-hume-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "hume")
	t.Setenv("RTP_AGENT_LLM_MODEL", "hume-evi-test")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "hume")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://hume.example")
	t.Setenv("RTP_AGENT_TTS_MODEL", "2")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Ava")
	t.Setenv("RTP_AGENT_TTS_VOICE_ID", "voice-id-test")
	t.Setenv("RTP_AGENT_TTS_VOICE_PROVIDER", "HUME_AI")
	t.Setenv("RTP_AGENT_TTS_INSTRUCTIONS", "warm and calm")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_TRAILING_SILENCE", "0.25")
	t.Setenv("RTP_AGENT_TTS_INSTANT_MODE", "false")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "wav")
	t.Setenv("RTP_AGENT_TTS_CONTEXT_UTTERANCES", "hello there,how are you")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.TTS.Label(); got != "hume.TTS" {
		t.Fatalf("TTS label = %q, want hume.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsMinimaxProviders(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "test-minimax-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "minimax")
	t.Setenv("RTP_AGENT_LLM_MODEL", "abab-test")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "minimax")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://minimax.example")
	t.Setenv("RTP_AGENT_TTS_MODEL", "speech-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "32000")
	t.Setenv("RTP_AGENT_TTS_BIT_RATE", "96000")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "pcm")
	t.Setenv("RTP_AGENT_TTS_EMOTION", "happy")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.2")
	t.Setenv("RTP_AGENT_TTS_VOLUME", "0.9")
	t.Setenv("RTP_AGENT_TTS_PITCH", "2")
	t.Setenv("RTP_AGENT_TTS_TEXT_NORMALIZATION", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.TTS.Label(); got != "minimax.TTS" {
		t.Fatalf("TTS label = %q, want minimax.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 32000 {
		t.Fatalf("TTS sample rate = %d, want 32000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsMistralAIProviders(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "test-mistral-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "mistralai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "ministral-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "mistralai")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://mistral.example/v1")
	t.Setenv("RTP_AGENT_STT_MODEL", "voxtral-mini-test")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "LiveKit,Agents")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "mistralai")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://mistral.example/v1")
	t.Setenv("RTP_AGENT_TTS_MODEL", "voxtral-tts-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "en_paul_neutral")
	t.Setenv("RTP_AGENT_TTS_REF_AUDIO", "https://example.com/reference.wav")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "pcm")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "mistralai.STT" {
		t.Fatalf("STT label = %q, want mistralai.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); caps.Streaming || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want offline recognize only", caps)
	}
	if got := app.Session.TTS.Label(); got != "mistralai.TTS" {
		t.Fatalf("TTS label = %q, want mistralai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsMurfTTS(t *testing.T) {
	t.Setenv("MURF_API_KEY", "test-murf-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "murf")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://murf.example")
	t.Setenv("RTP_AGENT_TTS_MODEL", "FALCON")
	t.Setenv("RTP_AGENT_TTS_VOICE", "en-US-matthew")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_TTS_INSTRUCTIONS", "Conversation")
	t.Setenv("RTP_AGENT_TTS_SPEED", "4")
	t.Setenv("RTP_AGENT_TTS_PITCH", "2")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "44100")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "murf.TTS" {
		t.Fatalf("TTS label = %q, want murf.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 44100 {
		t.Fatalf("TTS sample rate = %d, want 44100", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsLMNTTTS(t *testing.T) {
	t.Setenv("LMNT_API_KEY", "test-lmnt-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "lmnt")
	t.Setenv("RTP_AGENT_TTS_MODEL", "blizzard")
	t.Setenv("RTP_AGENT_TTS_VOICE", "leah")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "wav")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "48000")
	t.Setenv("RTP_AGENT_TTS_TEMPERATURE", "0.7")
	t.Setenv("RTP_AGENT_TTS_TOP_P", "0.9")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "lmnt.TTS" {
		t.Fatalf("TTS label = %q, want lmnt.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsNeuphonicTTS(t *testing.T) {
	t.Setenv("NEUPHONIC_API_KEY", "test-neuphonic-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "neuphonic")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://neuphonic.example")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-id")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_linear")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "44100")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "neuphonic.TTS" {
		t.Fatalf("TTS label = %q, want neuphonic.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 44100 {
		t.Fatalf("TTS sample rate = %d, want 44100", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsResembleTTS(t *testing.T) {
	t.Setenv("RESEMBLE_API_KEY", "test-resemble-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "resemble")
	t.Setenv("RTP_AGENT_TTS_MODEL", "chatterbox-turbo")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-uuid")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "24000")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "resemble.TTS" {
		t.Fatalf("TTS label = %q, want resemble.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsRespeecherTTS(t *testing.T) {
	t.Setenv("RESPEECHER_API_KEY", "test-respeecher-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "respeecher")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://respeecher.example/v1")
	t.Setenv("RTP_AGENT_TTS_MODEL", "/public/tts/ua-rt")
	t.Setenv("RTP_AGENT_TTS_VOICE", "olesia-conversation")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "48000")
	t.Setenv("RTP_AGENT_TTS_JSON_CONFIG", "temperature=0.4")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "respeecher.TTS" {
		t.Fatalf("TTS label = %q, want respeecher.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsRimeTTS(t *testing.T) {
	t.Setenv("RIME_API_KEY", "test-rime-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "rime")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://rime.example/v1/rime-tts")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://rime.example")
	t.Setenv("RTP_AGENT_TTS_MODEL", "mist")
	t.Setenv("RTP_AGENT_TTS_VOICE", "cove")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "eng")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "44100")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_DELIVERY_MODE", "bySentence")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "rime.TTS" {
		t.Fatalf("TTS label = %q, want rime.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 44100 {
		t.Fatalf("TTS sample rate = %d, want 44100", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || !caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming with aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSarvamProviders(t *testing.T) {
	t.Setenv("SARVAM_API_KEY", "test-sarvam-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "sarvam")
	t.Setenv("RTP_AGENT_LLM_MODEL", "sarvam-30b")
	t.Setenv("RTP_AGENT_LLM_BASE_URL", "https://sarvam.example/v1")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "sarvam")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://sarvam.example/speech-to-text")
	t.Setenv("RTP_AGENT_STT_STREAMING_URL", "wss://sarvam.example/speech-to-text/ws")
	t.Setenv("RTP_AGENT_STT_MODEL", "saarika:v2.5")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "hi-IN")
	t.Setenv("RTP_AGENT_STT_TASK", "transcribe")
	t.Setenv("RTP_AGENT_STT_PROMPT", "domain words")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_VAD_EVENTS", "true")
	t.Setenv("RTP_AGENT_STT_VAD_FLUSH", "true")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "sarvam")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://sarvam.example/text-to-speech")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://sarvam.example/text-to-speech/ws")
	t.Setenv("RTP_AGENT_TTS_MODEL", "bulbul:v2")
	t.Setenv("RTP_AGENT_TTS_VOICE", "anushka")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "hi-IN")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "22050")
	t.Setenv("RTP_AGENT_TTS_TEMPERATURE", "0.4")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_PITCH", "2")
	t.Setenv("RTP_AGENT_TTS_BIT_RATE", "128000")
	t.Setenv("RTP_AGENT_TTS_BUFFER_SIZE", "20")
	t.Setenv("RTP_AGENT_TTS_CHUNK_LENGTH", "120")
	t.Setenv("RTP_AGENT_TTS_ENHANCE_NAMED_ENTITIES", "true")
	t.Setenv("RTP_AGENT_TTS_INSTANT_MODE", "false")
	t.Setenv("RTP_AGENT_TTS_PRONUNCIATION_DICT_ID", "dict-1")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "wav")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "sarvam.STT" {
		t.Fatalf("STT label = %q, want sarvam.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim offline", caps)
	}
	if got := app.Session.TTS.Label(); got != "sarvam.TTS" {
		t.Fatalf("TTS label = %q, want sarvam.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 22050 {
		t.Fatalf("TTS sample rate = %d, want 22050", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsRtzrSTT(t *testing.T) {
	t.Setenv("RTZR_CLIENT_ID", "client-id")
	t.Setenv("RTZR_CLIENT_SECRET", "client-secret")
	t.Setenv("RTZR_ACCESS_TOKEN", "access-token")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "rtzr")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://rtzr.example")
	t.Setenv("RTP_AGENT_STT_STREAMING_URL", "wss://rtzr.example")
	t.Setenv("RTP_AGENT_STT_MODEL", "sommers_ko")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "ko")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_DOMAIN", "CALL")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_SECONDS", "0.7")
	t.Setenv("RTP_AGENT_STT_VAD_THRESHOLD", "0.6")
	t.Setenv("RTP_AGENT_STT_END_OF_TURN_CONFIDENCE_THRESHOLD", "0.8")
	t.Setenv("RTP_AGENT_STT_PUNCTUATE", "true")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "livekit,agents")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := app.Session.STT.Label(); got != "rtzr.STT" {
		t.Fatalf("STT label = %q, want rtzr.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim without offline recognize", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSimplismartProviders(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "test-simplismart-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "simplismart")
	t.Setenv("RTP_AGENT_LLM_MODEL", "simplismart-chat")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "simplismart")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://simplismart.example/predict")
	t.Setenv("RTP_AGENT_STT_MODEL", "openai/whisper-large-v3-turbo")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_TASK", "transcribe")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "true")
	t.Setenv("RTP_AGENT_STT_INCLUDE_TIMESTAMPS", "false")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "livekit,agents")
	t.Setenv("RTP_AGENT_STT_MAX_SPEAKERS", "2")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "simplismart")
	t.Setenv("RTP_AGENT_TTS_VOICE", "default_voice")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "simplismart.STT" {
		t.Fatalf("STT label = %q, want simplismart.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "word" || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming diarization word-aligned offline", caps)
	}
	if got := app.Session.TTS.Label(); got != "simplismart.TTS" {
		t.Fatalf("TTS label = %q, want simplismart.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSmallestAISpeechProviders(t *testing.T) {
	t.Setenv("SMALLESTAI_API_KEY", "test-smallestai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "smallestai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "smallestai-chat")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "smallestai")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://smallest.example/waves/v1")
	t.Setenv("RTP_AGENT_STT_MODEL", "pulse")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_ENCODING", "linear16")
	t.Setenv("RTP_AGENT_STT_WORD_TIMESTAMPS", "true")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "true")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_MS", "500")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "smallestai")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://smallest.example/waves/v1")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://smallest.example/waves/v1/tts/live")
	t.Setenv("RTP_AGENT_TTS_MODEL", "lightning_v3.1_pro")
	t.Setenv("RTP_AGENT_TTS_VOICE", "meher")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "24000")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "pcm")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "smallestai.STT" {
		t.Fatalf("STT label = %q, want smallestai.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "word" || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming diarization word-aligned offline", caps)
	}
	if got := app.Session.TTS.Label(); got != "smallestai.TTS" {
		t.Fatalf("TTS label = %q, want smallestai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSLNGSpeechProviders(t *testing.T) {
	t.Setenv("SLNG_API_KEY", "test-slng-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "slng")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://slng.example/stt")
	t.Setenv("RTP_AGENT_STT_MODEL", "deepgram/nova:3")
	t.Setenv("RTP_AGENT_STT_REGION", "us")
	t.Setenv("RTP_AGENT_STT_ENCODING", "pcm_s16le")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "false")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "true")
	t.Setenv("RTP_AGENT_STT_MIN_SPEAKERS", "1")
	t.Setenv("RTP_AGENT_STT_MAX_SPEAKERS", "2")
	t.Setenv("RTP_AGENT_STT_MODEL_OPTIONS", "punctuate=true,tier=enhanced")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "slng")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "wss://slng.example/tts")
	t.Setenv("RTP_AGENT_TTS_MODEL", "deepgram/aura:2")
	t.Setenv("RTP_AGENT_TTS_REGION", "eu")
	t.Setenv("RTP_AGENT_TTS_VOICE", "athena")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "32000")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.2")
	t.Setenv("RTP_AGENT_TTS_MODEL_OPTIONS", "encoding=linear16")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "slng.STT" {
		t.Fatalf("STT label = %q, want slng.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.Diarization {
		t.Fatalf("STT capabilities = %+v, want streaming diarization", caps)
	}
	if got := app.Session.TTS.Label(); got != "slng.TTS" {
		t.Fatalf("TTS label = %q, want slng.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 32000 {
		t.Fatalf("TTS sample rate = %d, want 32000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSonioxSpeechProviders(t *testing.T) {
	t.Setenv("SONIOX_API_KEY", "test-soniox-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "soniox")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://soniox.example/stt")
	t.Setenv("RTP_AGENT_STT_MODEL", "stt-rt-v4")
	t.Setenv("RTP_AGENT_STT_LANGUAGE_OPTIONS", "en,es")
	t.Setenv("RTP_AGENT_STT_LANGUAGE_DETECTION", "false")
	t.Setenv("RTP_AGENT_STT_NUMBER_OF_CHANNELS", "2")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "8000")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "true")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_MS", "750")
	t.Setenv("RTP_AGENT_STT_SESSION_ID", "client-1")
	t.Setenv("RTP_AGENT_STT_MODEL_OPTIONS", "language_hints_strict=true,context_text=domain terms,context_terms=LiveKit|Cavos,context_general=product:rtp-agent,context_translation_terms=agent:agente")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_SOURCE_LANGUAGES", "en")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_TARGET_LANGUAGES", "es")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "soniox")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://soniox.example/tts")
	t.Setenv("RTP_AGENT_TTS_MODEL", "tts-rt-v1-preview")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "es")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Adrian")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "mp3")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "48000")
	t.Setenv("RTP_AGENT_TTS_BIT_RATE", "128000")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "soniox.STT" {
		t.Fatalf("STT label = %q, want soniox.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "chunk" || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim diarization chunk-aligned without offline recognize", caps)
	}
	if got := app.Session.TTS.Label(); got != "soniox.TTS" {
		t.Fatalf("TTS label = %q, want soniox.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSpeechifyTTS(t *testing.T) {
	t.Setenv("SPEECHIFY_API_KEY", "test-speechify-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "speechify")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://speechify.example/v1")
	t.Setenv("RTP_AGENT_TTS_VOICE", "cliff")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "mp3_48000")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_TTS_MODEL", "simba-english")
	t.Setenv("RTP_AGENT_TTS_LOUDNESS_NORMALIZATION", "true")
	t.Setenv("RTP_AGENT_TTS_TEXT_NORMALIZATION", "false")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "speechify.TTS" {
		t.Fatalf("TTS label = %q, want speechify.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSpeechmaticsSpeechProviders(t *testing.T) {
	t.Setenv("SPEECHMATICS_API_KEY", "test-speechmatics-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "speechmatics")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://speechmatics.example/v2")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "de")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "48000")
	t.Setenv("RTP_AGENT_STT_ENCODING", "pcm_f32le")
	t.Setenv("RTP_AGENT_STT_DOMAIN", "finance")
	t.Setenv("RTP_AGENT_STT_OUTPUT_LOCALE", "de-DE")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "false")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "false")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "LiveKit:live kit,Cavos")
	t.Setenv("RTP_AGENT_STT_OPERATING_POINT", "enhanced")
	t.Setenv("RTP_AGENT_STT_TEXT_TIMEOUT_SECONDS", "1.2")
	t.Setenv("RTP_AGENT_STT_VAD_SILENCE_THRESHOLD_SECONDS", "0.6")
	t.Setenv("RTP_AGENT_STT_MAX_DURATION_WITHOUT_ENDPOINTING_SECONDS", "1.8")
	t.Setenv("RTP_AGENT_STT_MODEL_OPTIONS", "focus_speakers=agent,ignore_speakers=customer,focus_mode=ignore,known_speakers=agent:spk-1,permitted_marks=.|?,speaker_sensitivity=0.7")
	t.Setenv("RTP_AGENT_STT_MAX_SPEAKERS", "4")
	t.Setenv("RTP_AGENT_STT_PREFER_CURRENT_SPEAKER", "true")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "speechmatics")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://tts.speechmatics.example")
	t.Setenv("RTP_AGENT_TTS_VOICE", "theo")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "24000")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "speechmatics.STT" {
		t.Fatalf("STT label = %q, want speechmatics.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "chunk" || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim diarization chunk-aligned without offline recognize", caps)
	}
	if got := app.Session.TTS.Label(); got != "speechmatics.TTS" {
		t.Fatalf("TTS label = %q, want speechmatics.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSpitchSpeechProviders(t *testing.T) {
	t.Setenv("SPITCH_API_KEY", "test-spitch-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "spitch")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "spitch")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://spitch.example")
	t.Setenv("RTP_AGENT_TTS_VOICE", "amina")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "fr")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "wav")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "16000")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "spitch.STT" {
		t.Fatalf("STT label = %q, want spitch.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); caps.Streaming || caps.InterimResults || caps.Diarization || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want offline recognize only", caps)
	}
	if got := app.Session.TTS.Label(); got != "spitch.TTS" {
		t.Fatalf("TTS label = %q, want spitch.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 16000 {
		t.Fatalf("TTS sample rate = %d, want 16000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsTelnyxProviders(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "test-telnyx-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "telnyx")
	t.Setenv("RTP_AGENT_LLM_MODEL", "telnyx-chat")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "telnyx")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://telnyx.example/transcription")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "es")
	t.Setenv("RTP_AGENT_STT_MODEL", "deepgram")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "8000")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "telnyx")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "wss://telnyx.example/speech")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Telnyx.NaturalHD.astra")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "telnyx.STT" {
		t.Fatalf("STT label = %q, want telnyx.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.Diarization || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim offline recognize without diarization", caps)
	}
	if got := app.Session.TTS.Label(); got != "telnyx.TTS" {
		t.Fatalf("TTS label = %q, want telnyx.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 16000 {
		t.Fatalf("TTS sample rate = %d, want 16000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsXAIProviders(t *testing.T) {
	t.Setenv("XAI_API_KEY", "test-xai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "xai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "grok-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "xai")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://xai.example/v1/stt")
	t.Setenv("RTP_AGENT_STT_STREAMING_URL", "wss://xai.example/v1/stt")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "8000")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "es")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "false")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "true")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_MS", "250")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "xai")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://xai.example/v1/tts")
	t.Setenv("RTP_AGENT_TTS_VOICE", "ara")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "es")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "xai.STT" {
		t.Fatalf("STT label = %q, want xai.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "word" || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming diarization word-aligned offline without interim", caps)
	}
	if got := app.Session.TTS.Label(); got != "xai.TTS" {
		t.Fatalf("TTS label = %q, want xai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvAddsXAIProviderTools(t *testing.T) {
	t.Setenv("XAI_API_KEY", "test-xai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "xai")
	t.Setenv("RTP_AGENT_XAI_TOOLS", "web_search,x_search,file_search")
	t.Setenv("RTP_AGENT_XAI_ALLOWED_X_HANDLES", "cavos_io,livekit")
	t.Setenv("RTP_AGENT_XAI_FILE_SEARCH_VECTOR_STORE_IDS", "vs_1,vs_2")
	t.Setenv("RTP_AGENT_XAI_FILE_SEARCH_MAX_RESULTS", "3")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if len(app.Agent.Tools) != 3 {
		t.Fatalf("len(Agent.Tools) = %d, want 3", len(app.Agent.Tools))
	}
	if got := app.Agent.Tools[0].Name(); got != "xai_web_search" {
		t.Fatalf("tool[0].Name() = %q, want xai_web_search", got)
	}
	if got := app.Agent.Tools[1].Name(); got != "xai_x_search" {
		t.Fatalf("tool[1].Name() = %q, want xai_x_search", got)
	}
	if got := app.Agent.Tools[2].Name(); got != "xai_file_search" {
		t.Fatalf("tool[2].Name() = %q, want xai_file_search", got)
	}
}

func TestDefaultConfigFromEnvAddsMCPStdioTools(t *testing.T) {
	servers := []MCPStdioServerConfig{{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHelperProcess"},
		Env:     map[string]string{"GO_WANT_MCP_HELPER": "1"},
	}}
	encoded, err := json.Marshal(servers)
	if err != nil {
		t.Fatalf("marshal MCP config: %v", err)
	}
	t.Setenv("RTP_AGENT_MCP_STDIO_SERVERS", string(encoded))

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	defer app.closeMCPServers()
	if len(app.Agent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1 MCP tool", len(app.Agent.Tools))
	}
	if got := app.Agent.Tools[0].Name(); got != "lookup" {
		t.Fatalf("tool name = %q, want lookup", got)
	}
}

func TestDefaultConfigFromEnvAddsMCPHTTPTools(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization header = %q, want bearer token", got)
		}
		var req appMCPJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode MCP request: %v", err)
		}
		switch req.Method {
		case "initialize":
			writeAppMCPHTTPResponse(t, w, req.ID, map[string]any{"protocolVersion": "2024-11-05"})
		case "initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeAppMCPHTTPResponse(t, w, req.ID, map[string]any{
				"tools": []map[string]any{
					{"name": "lookup", "description": "lookup tool", "inputSchema": map[string]any{"type": "object"}},
				},
			})
		default:
			t.Fatalf("unexpected MCP method %q", req.Method)
		}
	}))
	defer httpServer.Close()

	servers := []map[string]any{{
		"url":           httpServer.URL,
		"transportType": "streamable_http",
		"allowedTools":  []string{"lookup"},
		"headers":       map[string]string{"Authorization": "Bearer token"},
	}}
	encoded, err := json.Marshal(servers)
	if err != nil {
		t.Fatalf("marshal MCP HTTP config: %v", err)
	}
	t.Setenv("RTP_AGENT_MCP_HTTP_SERVERS", string(encoded))

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	defer app.closeMCPServers()
	if len(app.Agent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1 MCP HTTP tool", len(app.Agent.Tools))
	}
	if got := len(app.Session.MCPServers()); got != 1 {
		t.Fatalf("len(Session.MCPServers()) = %d, want 1 MCP HTTP server", got)
	}
	if got := app.Agent.Tools[0].Name(); got != "lookup" {
		t.Fatalf("tool name = %q, want lookup", got)
	}
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil || request.ID == 0 {
			continue
		}
		switch request.Method {
		case "initialize":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "fake", "version": "1"},
				},
			})
		case "tools/list":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "lookup",
						"description": "Look up information",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
					}},
				},
			})
		}
	}
	os.Exit(0)
}

func TestDefaultConfigFromEnvAddsEndCallTool(t *testing.T) {
	t.Setenv("RTP_AGENT_TOOLS", "end_call")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if len(app.Agent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1", len(app.Agent.Tools))
	}
	if got := app.Agent.Tools[0].Name(); got != "end_call" {
		t.Fatalf("tool[0].Name() = %q, want end_call", got)
	}
}

type appMCPJSONRPCRequest struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
}

func writeAppMCPHTTPResponse(t *testing.T, w http.ResponseWriter, id int64, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}); err != nil {
		t.Fatalf("encode MCP response: %v", err)
	}
}

func TestDefaultConfigFromEnvSelectsDtmfWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "dtmf")
	t.Setenv("RTP_AGENT_WORKFLOW_DTMF_NUM_DIGITS", "4")
	t.Setenv("RTP_AGENT_WORKFLOW_DTMF_ASK_CONFIRMATION", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetDtmfTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetDtmfTask", app.Session.Agent)
	}
	if task.NumDigits != 4 {
		t.Fatalf("NumDigits = %d, want 4", task.NumDigits)
	}
	if !task.AskForConfirmation {
		t.Fatal("AskForConfirmation = false, want true")
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected workflow agent")
	}
	if len(app.Agent.Tools) != 1 || app.Agent.Tools[0].Name() != "confirm_inputs" {
		t.Fatalf("workflow tools = %#v, want confirm_inputs", app.Agent.Tools)
	}
}

func TestDefaultConfigFromEnvSelectsEmailWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "email")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetEmailTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetEmailTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected workflow agent")
	}
	if len(app.Agent.Tools) != 3 {
		t.Fatalf("workflow tools = %d, want email update/confirm/decline tools", len(app.Agent.Tools))
	}
}

func TestDefaultConfigFromEnvSelectsCardNumberWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "card_number")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetCardNumberTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetCardNumberTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected card-number workflow agent")
	}
	if len(app.Agent.Tools) != 3 {
		t.Fatalf("workflow tools = %d, want record/decline/restart tools", len(app.Agent.Tools))
	}
}

func TestDefaultConfigFromEnvSelectsWarmTransferWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "warm_transfer")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CALL_TO", "+15550100")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_TRUNK_ID", "trunk_123")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_EXTRA_INSTRUCTIONS", "\nKeep the handoff concise.")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.WarmTransferTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.WarmTransferTask", app.Session.Agent)
	}
	if task.TargetPhoneNumber != "+15550100" {
		t.Fatalf("TargetPhoneNumber = %q, want +15550100", task.TargetPhoneNumber)
	}
	if task.SipTrunkID != "trunk_123" {
		t.Fatalf("SipTrunkID = %q, want trunk_123", task.SipTrunkID)
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected warm transfer agent")
	}
	if len(app.Agent.Tools) != 3 {
		t.Fatalf("workflow tools = %d, want connect/decline/voicemail tools", len(app.Agent.Tools))
	}
}

func TestDefaultConfigFromEnvSelectsTaskGroupWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "task_group")
	t.Setenv("RTP_AGENT_WORKFLOW_TASK_GROUP_TASKS", "address,email,dtmf,card_number")
	t.Setenv("RTP_AGENT_WORKFLOW_DTMF_NUM_DIGITS", "4")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	group, ok := app.Session.Agent.(*workflows.TaskGroup)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.TaskGroup", app.Session.Agent)
	}
	if app.Agent != group.GetAgent() {
		t.Fatal("App.Agent does not point at selected task group agent")
	}
	if len(group.RegisteredTasks) != 4 {
		t.Fatalf("RegisteredTasks = %d, want 4", len(group.RegisteredTasks))
	}
	wantIDs := []string{"address", "email", "dtmf", "card_number"}
	for i, want := range wantIDs {
		if got := group.RegisteredTasks[i].ID; got != want {
			t.Fatalf("RegisteredTasks[%d].ID = %q, want %q", i, got, want)
		}
	}
}

func TestDefaultConfigFromEnvEnablesIVRDetection(t *testing.T) {
	t.Setenv("RTP_AGENT_IVR_DETECTION", "true")
	t.Setenv("RTP_AGENT_IVR_SILENCE_DURATION_SECONDS", "0.25")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if !app.Session.Options.IVRDetection {
		t.Fatal("IVRDetection = false, want true")
	}
	if got := app.Session.Options.IVRSilenceDuration; got != 250*time.Millisecond {
		t.Fatalf("IVRSilenceDuration = %v, want 250ms", got)
	}
}

func TestDefaultConfigFromEnvConfiguresEvaluationJudges(t *testing.T) {
	t.Setenv("RTP_AGENT_EVAL_JUDGES", "task_completion,accuracy,safety")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Evaluator == nil {
		t.Fatal("Evaluator = nil, want configured judge group")
	}
	if len(app.Evaluator.Judges) != 3 {
		t.Fatalf("Evaluator.Judges = %d, want 3", len(app.Evaluator.Judges))
	}
	wantNames := []string{"task_completion", "accuracy", "safety"}
	for i, want := range wantNames {
		if got := app.Evaluator.Judges[i].Name(); got != want {
			t.Fatalf("Evaluator.Judges[%d].Name() = %q, want %q", i, got, want)
		}
	}
}

func TestDefaultConfigFromEnvWrapsLLMFallbackProviders(t *testing.T) {
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "minimal")
	t.Setenv("RTP_AGENT_LLM_FALLBACK_PROVIDERS", "openai")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := llm.Label(app.Agent.LLM); got != "FallbackAdapter(minimal.MinimalLLM)" {
		t.Fatalf("LLM label = %q, want fallback adapter around primary minimal LLM", got)
	}
}

func TestDefaultConfigFromEnvConfiguresLLMChatOptions(t *testing.T) {
	t.Setenv("RTP_AGENT_LLM_PARALLEL_TOOL_CALLS", "true")
	t.Setenv("RTP_AGENT_LLM_JSON_CONFIG", "temperature=0.2")
	t.Setenv("RTP_AGENT_LLM_RESPONSE_FORMAT", "type=json_object")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session.Options.LLMParallelToolCalls == nil || !*app.Session.Options.LLMParallelToolCalls {
		t.Fatalf("LLMParallelToolCalls = %#v, want true", app.Session.Options.LLMParallelToolCalls)
	}
	if got := app.Session.Options.LLMExtraParams["temperature"]; got != 0.2 {
		t.Fatalf("LLMExtraParams[temperature] = %#v, want 0.2", got)
	}
	if got := app.Session.Options.LLMResponseFormat["type"]; got != "json_object" {
		t.Fatalf("LLMResponseFormat[type] = %#v, want json_object", got)
	}
}

func TestDefaultConfigFromEnvRestoresInitialChatContext(t *testing.T) {
	t.Setenv("RTP_AGENT_CHAT_CONTEXT_JSON", `{"items":[{"id":"seed-user","type":"message","role":"user","content":["hello from history"]}]}`)

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	for _, item := range app.Session.ChatCtx.Items {
		message, ok := item.(*llm.ChatMessage)
		if ok && message.ID == "seed-user" && message.TextContent() == "hello from history" {
			return
		}
	}
	t.Fatalf("session chat context items = %#v, want restored seed-user message", app.Session.ChatCtx.Items)
}

func TestDefaultConfigFromEnvWrapsSTTFallbackProviders(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "slng")
	t.Setenv("SLNG_API_KEY", "test-slng-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
}

func TestDefaultConfigFromEnvWrapsNonStreamingSTTFallbackWithVAD(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "elevenlabs")
	t.Setenv("RTP_AGENT_VAD_PROVIDER", "silero")
	t.Setenv("ELEVENLABS_API_KEY", "test-elevenlabs-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
	if app.Session.VAD == nil {
		t.Fatal("Session VAD is nil")
	}
}

func TestDefaultConfigFromEnvWrapsTTSFallbackProviders(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "cartesia")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestEvaluateSessionReturnsEvaluationSummary(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	session.ChatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "hello"}},
	})
	evaluatorLLM := &fakeEvalLLM{
		stream: &fakeEvalLLMStream{chunks: []*llm.ChatChunk{{
			Delta: &llm.ChoiceDelta{ToolCalls: []llm.FunctionToolCall{{
				Name:      "submit_verdict",
				Arguments: `{"verdict":"pass","reasoning":"met the criteria"}`,
			}}},
		}}},
	}
	app := &App{
		Session:   session,
		Evaluator: evals.NewJudgeGroup(evaluatorLLM, []evals.Evaluator{evals.AccuracyJudge(evaluatorLLM)}),
	}

	summary, err := app.EvaluateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("EvaluateSession() error = %v", err)
	}
	if summary.Score != 1 || !summary.AllPassed || !summary.AnyPassed || !summary.MajorityPassed || !summary.NoneFailed {
		t.Fatalf("summary = %+v, want passing evaluation summary", summary)
	}
}

func TestRunSessionRegistersPrimarySessionOnJobContext(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	server := worker.NewAgentServer(worker.WorkerOptions{AgentName: "support-agent"})
	application := &App{
		Server:          server,
		Agent:           baseAgent,
		Session:         session,
		MetricsRegistry: telemetry.NewMetricRegistry(),
	}
	ctx := worker.NewJobContext(
		&livekit.Job{
			Id: "job_primary_session",
			Room: &livekit.Room{
				Sid:  "RM_primary",
				Name: "room-primary",
			},
		},
		"wss://livekit.example",
		"key",
		"secret",
	)

	if err := application.runSession(ctx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}
	primary, err := ctx.PrimarySession()
	if err != nil {
		t.Fatalf("PrimarySession() error = %v", err)
	}
	if primary != session {
		t.Fatal("PrimarySession() did not return app session")
	}
}

func TestConfigureRoomToolsAddsSendDTMFTool(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	publisher := &fakeAppDtmfPublisher{}

	err := configureRoomTools(AppConfig{AppTools: []string{"send_dtmf"}}, baseAgent, publisher)
	if err != nil {
		t.Fatalf("configureRoomTools() error = %v", err)
	}
	if len(baseAgent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1", len(baseAgent.Tools))
	}
	if got := baseAgent.Tools[0].Name(); got != "send_dtmf_events" {
		t.Fatalf("tool[0].Name() = %q, want send_dtmf_events", got)
	}
}

func TestDefaultConfigFromEnvAddsAnthropicComputerTool(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "anthropic")
	t.Setenv("RTP_AGENT_ANTHROPIC_TOOLS", "computer")
	t.Setenv("RTP_AGENT_ANTHROPIC_COMPUTER_WIDTH", "1280")
	t.Setenv("RTP_AGENT_ANTHROPIC_COMPUTER_HEIGHT", "720")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if len(app.Agent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1", len(app.Agent.Tools))
	}
	tool := app.Agent.Tools[0]
	if tool.ID() != "computer" || tool.Name() != "computer_use" {
		t.Fatalf("tool identity = %q/%q, want computer/computer_use", tool.ID(), tool.Name())
	}
	if specProvider, ok := tool.(interface {
		AnthropicToolSpec() map[string]interface{}
	}); ok {
		spec := specProvider.AnthropicToolSpec()
		if spec["display_width_px"] != 1280 || spec["display_height_px"] != 720 {
			t.Fatalf("computer display spec = %#v, want 1280x720", spec)
		}
	} else {
		t.Fatal("computer tool does not expose AnthropicToolSpec")
	}
}

func TestDefaultConfigFromEnvSelectsAvatarProvider(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		keyEnv     string
		wantAvatar string
	}{
		{name: "anam", provider: "anam", keyEnv: "ANAM_API_KEY", wantAvatar: "*anam.AnamAvatar"},
		{name: "avatario", provider: "avatario", keyEnv: "AVATARIO_API_KEY", wantAvatar: "*avatario.AvatarioAvatar"},
		{name: "avatartalk", provider: "avatartalk", keyEnv: "AVATARTALK_API_KEY", wantAvatar: "*avatartalk.AvatartalkAvatar"},
		{name: "bey", provider: "bey", keyEnv: "BEY_API_KEY", wantAvatar: "*bey.BeyAvatar"},
		{name: "bithuman", provider: "bithuman", keyEnv: "BITHUMAN_API_KEY", wantAvatar: "*bithuman.BithumanAvatar"},
		{name: "hedra", provider: "hedra", keyEnv: "HEDRA_API_KEY", wantAvatar: "*hedra.HedraAvatar"},
		{name: "keyframe", provider: "keyframe", keyEnv: "KEYFRAME_API_KEY", wantAvatar: "*keyframe.KeyframeAgent"},
		{name: "lemonslice", provider: "lemonslice", keyEnv: "LEMONSLICE_API_KEY", wantAvatar: "*lemonslice.LemonsliceAvatar"},
		{name: "liveavatar", provider: "liveavatar", keyEnv: "LIVEAVATAR_API_KEY", wantAvatar: "*liveavatar.LiveAvatar"},
		{name: "simli", provider: "simli", keyEnv: "SIMLI_API_KEY", wantAvatar: "*simli.SimliAvatar"},
		{name: "tavus", provider: "tavus", keyEnv: "TAVUS_API_KEY", wantAvatar: "*tavus.TavusAvatar"},
		{name: "trugen", provider: "trugen", keyEnv: "TRUGEN_API_KEY", wantAvatar: "*trugen.TrugenAvatar"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.keyEnv, "test-avatar-key")
			t.Setenv("RTP_AGENT_AVATAR_PROVIDER", tc.provider)

			app, err := NewApp(DefaultConfigFromEnv())
			if err != nil {
				t.Fatalf("NewApp() error = %v", err)
			}
			if app.Agent == nil {
				t.Fatal("Agent is nil")
			}
			if app.Agent.Avatar == nil {
				t.Fatal("Agent Avatar is nil")
			}
			if got := fmt.Sprintf("%T", app.Agent.Avatar); got != tc.wantAvatar {
				t.Fatalf("Agent Avatar type = %q, want %s", got, tc.wantAvatar)
			}
		})
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
	t.Setenv("RTP_AGENT_STT_PROVIDER", "livekit")
	t.Setenv("RTP_AGENT_STT_MODEL", "deepgram/nova-3")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "livekit")
	t.Setenv("RTP_AGENT_TTS_MODEL", "cartesia/sonic-3")

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
	if app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := app.Session.STT.Label(); got != "livekit.STT" {
		t.Fatalf("STT label = %q, want livekit.STT", got)
	}
	if app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "livekit.TTS" {
		t.Fatalf("TTS label = %q, want livekit.TTS", got)
	}
}

func TestDefaultConfigFromEnvSelectsLiveKitTTSTokenizer(t *testing.T) {
	cases := []struct {
		name         string
		provider     string
		wantTypeName string
	}{
		{name: "advanced", provider: "advanced", wantTypeName: "*tokenize.AdvancedSentenceTokenizer"},
		{name: "blingfire", provider: "blingfire", wantTypeName: "*blingfire.SentenceTokenizer"},
		{name: "nltk", provider: "nltk", wantTypeName: "*nltk.SentenceTokenizer"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LIVEKIT_API_KEY", "test-livekit-key")
			t.Setenv("LIVEKIT_API_SECRET", "test-livekit-secret")
			t.Setenv("RTP_AGENT_TTS_PROVIDER", "livekit")
			t.Setenv("RTP_AGENT_TTS_TOKENIZER_PROVIDER", tc.provider)

			app, err := NewApp(DefaultConfigFromEnv())
			if err != nil {
				t.Fatalf("NewApp() error = %v", err)
			}
			if app.Session == nil || app.Session.TTS == nil {
				t.Fatal("Session TTS is nil")
			}
			field := reflect.ValueOf(app.Session.TTS).Elem().FieldByName("sentenceTokenizer")
			if !field.IsValid() {
				t.Fatal("livekit TTS sentenceTokenizer field is missing")
			}
			if field.IsNil() {
				t.Fatal("livekit TTS sentenceTokenizer is nil")
			}
			if got := field.Elem().Type().String(); got != tc.wantTypeName {
				t.Fatalf("sentenceTokenizer type = %q, want %s", got, tc.wantTypeName)
			}
		})
	}
}

func TestDefaultConfigFromEnvSelectsWordTokenizer(t *testing.T) {
	cases := []struct {
		name         string
		provider     string
		wantTypeName string
	}{
		{name: "basic", provider: "basic", wantTypeName: "*tokenize.BasicWordTokenizer"},
		{name: "blingfire", provider: "blingfire", wantTypeName: "*blingfire.WordTokenizer"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RTP_AGENT_WORD_TOKENIZER_PROVIDER", tc.provider)

			app, err := NewApp(DefaultConfigFromEnv())
			if err != nil {
				t.Fatalf("NewApp() error = %v", err)
			}
			if app.Session == nil {
				t.Fatal("Session is nil")
			}
			if app.Session.Options.WordTokenizer == nil {
				t.Fatal("WordTokenizer is nil")
			}
			if got := reflect.TypeOf(app.Session.Options.WordTokenizer).String(); got != tc.wantTypeName {
				t.Fatalf("WordTokenizer type = %q, want %s", got, tc.wantTypeName)
			}
		})
	}
}

func TestDefaultConfigFromEnvConfiguresTTSStreamPacer(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_STREAM_PACER_ENABLED", "true")
	t.Setenv("RTP_AGENT_TTS_STREAM_PACER_MIN_REMAINING_AUDIO_MS", "250")
	t.Setenv("RTP_AGENT_TTS_STREAM_PACER_MAX_TEXT_LENGTH", "120")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.Options.TTSStreamPacer == nil {
		t.Fatal("Session TTSStreamPacer is nil")
	}
	if got := app.Session.Options.TTSStreamPacer.MinRemainingAudio; got != 250*time.Millisecond {
		t.Fatalf("MinRemainingAudio = %v, want 250ms", got)
	}
	if got := app.Session.Options.TTSStreamPacer.MaxTextLength; got != 120 {
		t.Fatalf("MaxTextLength = %d, want 120", got)
	}
}

func TestDefaultConfigFromEnvConfiguresTTSTextReplacements(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_TEXT_REPLACEMENTS", "OpenAI=Open A I,world=there")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Config.TTSTextReplacements["OpenAI"]; got != "Open A I" {
		t.Fatalf("Config.TTSTextReplacements[OpenAI] = %q, want Open A I", got)
	}
	if got := app.Session.Options.TTSTextReplacements["world"]; got != "there" {
		t.Fatalf("Session.Options.TTSTextReplacements[world] = %q, want there", got)
	}
}

func TestDefaultConfigFromEnvConfiguresBackgroundAudio(t *testing.T) {
	t.Setenv("RTP_AGENT_BACKGROUND_AUDIO_AMBIENT", "city-ambience.ogg")
	t.Setenv("RTP_AGENT_BACKGROUND_AUDIO_THINKING", "/tmp/thinking.wav")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.Options.BackgroundAudio == nil {
		t.Fatal("Session BackgroundAudio is nil")
	}
	if _, ok := backgroundAudioSource("city-ambience.ogg").(agent.BuiltinAudioClip); !ok {
		t.Fatalf("backgroundAudioSource(city-ambience.ogg) = %T, want BuiltinAudioClip", backgroundAudioSource("city-ambience.ogg"))
	}
	if got := backgroundAudioSource("/tmp/thinking.wav"); got != "/tmp/thinking.wav" {
		t.Fatalf("backgroundAudioSource(/tmp/thinking.wav) = %#v, want path string", got)
	}
}

func TestRunSessionConnectsRoomIOToSession(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	baseAgent.VAD = &fakeAppVAD{}
	baseAgent.STT = &fakeAppSTT{}
	baseAgent.LLM = &fakeAppLLM{}
	baseAgent.TTS = &fakeAppTTS{}
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	app := &App{
		Session:     session,
		Server:      worker.NewAgentServer(worker.WorkerOptions{}),
		RoomOptions: worker.RoomOptions{DisablePreConnectAudio: true, DisableTextInput: true},
	}
	jobCtx := &worker.JobContext{Room: lksdk.NewRoom(nil)}

	if err := app.runSession(jobCtx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}

	if app.RoomIO == nil {
		t.Fatal("RoomIO is nil")
	}
	if session.Room != jobCtx.Room {
		t.Fatal("session Room was not set from job context")
	}
	pipeline, ok := session.Assistant.(*agent.PipelineAgent)
	if !ok {
		t.Fatalf("session assistant = %T, want *agent.PipelineAgent", session.Assistant)
	}
	if pipeline.PublishAudio == nil {
		t.Fatal("session assistant PublishAudio was not connected to RoomIO")
	}
}

func TestRunSessionWiresRoomDeleteToJobContext(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	baseAgent.VAD = &fakeAppVAD{}
	baseAgent.STT = &fakeAppSTT{}
	baseAgent.LLM = &fakeAppLLM{}
	baseAgent.TTS = &fakeAppTTS{}
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	app := &App{
		Session:     session,
		Server:      worker.NewAgentServer(worker.WorkerOptions{}),
		RoomOptions: worker.RoomOptions{DisablePreConnectAudio: true, DisableTextInput: true},
	}
	jobCtx := worker.NewJobContext(&livekit.Job{Id: "job_delete_room", Room: &livekit.Room{Name: "room-a"}}, "", "", "")
	jobCtx.Room = lksdk.NewRoom(nil)

	if err := app.runSession(jobCtx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}

	if app.RoomIO == nil {
		t.Fatal("RoomIO is nil")
	}
	if app.RoomIO.Options.DeleteRoom == nil {
		t.Fatal("RoomIO DeleteRoom option = nil, want JobContext.DeleteRoom wiring")
	}
	if err := app.RoomIO.Options.DeleteRoom(context.Background(), "room-a"); err != nil {
		t.Fatalf("RoomIO DeleteRoom() error = %v, want best-effort nil", err)
	}
}

func TestRunSessionStartsAudioRecorderForRecordedJob(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	baseAgent.VAD = &fakeAppVAD{}
	baseAgent.STT = &fakeAppSTT{}
	baseAgent.LLM = &fakeAppLLM{}
	baseAgent.TTS = &fakeAppTTS{}
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	app := &App{
		Session:     session,
		Server:      worker.NewAgentServer(worker.WorkerOptions{}),
		RoomOptions: worker.RoomOptions{DisablePreConnectAudio: true, DisableTextInput: true},
	}
	jobCtx := worker.NewJobContext(&livekit.Job{Id: "job_record_audio", Room: &livekit.Room{Name: "room-a"}}, "", "", "")
	jobCtx.Room = lksdk.NewRoom(nil)
	sessionDir := t.TempDir()
	jobCtx.SetSessionDirectory(sessionDir)
	jobCtx.Report.RecordingOptions.Audio = true

	if err := app.runSession(jobCtx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}
	t.Cleanup(func() {
		if app.RoomIO != nil && app.RoomIO.Recorder != nil {
			_ = app.RoomIO.Recorder.Stop()
		}
	})

	if jobCtx.Report.AudioRecordingPath == nil {
		t.Fatal("AudioRecordingPath = nil, want recorder output path")
	}
	if got, want := *jobCtx.Report.AudioRecordingPath, filepath.Join(sessionDir, "audio.ogg"); got != want {
		t.Fatalf("AudioRecordingPath = %q, want %q", got, want)
	}
}

func TestRunSessionInstallsJobContextOnSession(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	app := &App{
		Session: session,
		Server:  worker.NewAgentServer(worker.WorkerOptions{}),
	}
	jobCtx := worker.NewJobContext(&livekit.Job{Id: "job_run_context", Room: &livekit.Room{Name: "room-a"}}, "", "", "")

	if err := app.runSession(jobCtx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}

	value, err := session.JobContext()
	if err != nil {
		t.Fatalf("session JobContext() error = %v, want nil", err)
	}
	if value != jobCtx {
		t.Fatalf("session JobContext() = %#v, want active job context", value)
	}
}

func TestDefaultConfigFromEnvConfiguresLLMTurnDetector(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "gpt-4o-mini")
	t.Setenv("RTP_AGENT_TURN_DETECTOR_PROVIDER", "llm")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if got := fmt.Sprintf("%T", app.Agent.TurnDetector); got != "*agent.LLMTurnDetector" {
		t.Fatalf("TurnDetector type = %q, want *agent.LLMTurnDetector", got)
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

type fakeAppVAD struct{}

func (f *fakeAppVAD) Label() string { return "fake-vad" }
func (f *fakeAppVAD) Model() string { return "fake-vad" }
func (f *fakeAppVAD) Provider() string {
	return "fake"
}
func (f *fakeAppVAD) Capabilities() vad.VADCapabilities { return vad.VADCapabilities{} }
func (f *fakeAppVAD) OnMetricsCollected(vad.VADMetricsHandler) func() {
	return func() {}
}
func (f *fakeAppVAD) Stream(context.Context) (vad.VADStream, error) {
	return &fakeAppVADStream{}, nil
}

type fakeAppVADStream struct{}

func (f *fakeAppVADStream) PushFrame(*model.AudioFrame) error { return nil }
func (f *fakeAppVADStream) Flush() error                      { return nil }
func (f *fakeAppVADStream) EndInput() error                   { return nil }
func (f *fakeAppVADStream) Close() error                      { return nil }
func (f *fakeAppVADStream) Next() (*vad.VADEvent, error)      { return nil, io.EOF }

type fakeAppSTT struct{}

func (f *fakeAppSTT) Label() string { return "fake-stt" }
func (f *fakeAppSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true}
}
func (f *fakeAppSTT) Stream(context.Context, string) (stt.RecognizeStream, error) {
	return &fakeAppSTTStream{}, nil
}
func (f *fakeAppSTT) Recognize(context.Context, []*model.AudioFrame, string) (*stt.SpeechEvent, error) {
	return nil, nil
}

type fakeAppSTTStream struct{}

func (f *fakeAppSTTStream) PushFrame(*model.AudioFrame) error { return nil }
func (f *fakeAppSTTStream) Flush() error                      { return nil }
func (f *fakeAppSTTStream) Close() error                      { return nil }
func (f *fakeAppSTTStream) Next() (*stt.SpeechEvent, error)   { return nil, io.EOF }

type fakeAppLLM struct{}

func (f *fakeAppLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	return &fakeAppLLMStream{}, nil
}

type fakeAppLLMStream struct{}

func (f *fakeAppLLMStream) Next() (*llm.ChatChunk, error) { return nil, io.EOF }
func (f *fakeAppLLMStream) Close() error                  { return nil }

type fakeEvalLLM struct {
	stream llm.LLMStream
}

func (f *fakeEvalLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	return f.stream, nil
}

type fakeEvalLLMStream struct {
	chunks []*llm.ChatChunk
	index  int
}

func (f *fakeEvalLLMStream) Next() (*llm.ChatChunk, error) {
	if f.index >= len(f.chunks) {
		return nil, io.EOF
	}
	chunk := f.chunks[f.index]
	f.index++
	return chunk, nil
}
func (f *fakeEvalLLMStream) Close() error { return nil }

type fakeAppTTS struct{}

func (f *fakeAppTTS) Label() string { return "fake-tts" }
func (f *fakeAppTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}
func (f *fakeAppTTS) SampleRate() int  { return 24000 }
func (f *fakeAppTTS) NumChannels() int { return 1 }
func (f *fakeAppTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, nil
}
func (f *fakeAppTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	return &fakeAppTTSStream{}, nil
}

type fakeAppTTSStream struct{}

func (f *fakeAppTTSStream) PushText(string) error { return nil }
func (f *fakeAppTTSStream) Flush() error          { return nil }
func (f *fakeAppTTSStream) Close() error          { return nil }
func (f *fakeAppTTSStream) Next() (*tts.SynthesizedAudio, error) {
	return nil, io.EOF
}

type fakeAppDtmfPublisher struct{}

func (f *fakeAppDtmfPublisher) PublishDTMF(code int32, digit string) error {
	return nil
}

type appRecordingLogger struct{}

func (l *appRecordingLogger) Debugw(msg string, keysAndValues ...any)            {}
func (l *appRecordingLogger) Infow(msg string, keysAndValues ...any)             {}
func (l *appRecordingLogger) Warnw(msg string, err error, keysAndValues ...any)  {}
func (l *appRecordingLogger) Errorw(msg string, err error, keysAndValues ...any) {}
func (l *appRecordingLogger) WithValues(keysAndValues ...any) livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithUnlikelyValues(keysAndValues ...any) livekitlogger.UnlikelyLogger {
	return livekitlogger.GetDiscardLogger().WithUnlikelyValues(keysAndValues...)
}
func (l *appRecordingLogger) WithName(name string) livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithComponent(component string) livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithCallDepth(depth int) livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithItemSampler() livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithoutSampler() livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithDeferredValues() (livekitlogger.Logger, livekitlogger.DeferredFieldResolver) {
	return livekitlogger.GetDiscardLogger().WithDeferredValues()
}
