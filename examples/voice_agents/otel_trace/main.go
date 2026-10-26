// Example modeled on LiveKit Agents 1.8.8 examples/voice_agents/otel_trace.py.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	inference "github.com/cavos-io/rtp-agent/adapter/livekit"
	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/app"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/interface/cli"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	_ "github.com/joho/godotenv/autoload"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// The exporter honors OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS,
// and their OTEL_EXPORTER_OTLP_TRACES_* overrides. AgentServer owns finalization.
func setupOTel(ctx context.Context) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter)), nil
}

type weatherTool struct{}

func (*weatherTool) ID() string   { return "lookup_weather" }
func (*weatherTool) Name() string { return "lookup_weather" }
func (*weatherTool) Description() string {
	return "Return example weather for a location. This is simulated weather, not a live forecast."
}
func (*weatherTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"location": map[string]any{"type": "string"}}, "required": []string{"location"}}
}
func (*weatherTool) Execute(ctx context.Context, args string) (string, error) {
	var input struct {
		Location string `json:"location"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil || strings.TrimSpace(input.Location) == "" {
		return "", llm.NewToolError("A location is required")
	}
	_, span := telemetry.StartSpan(ctx, "weather_lookup", trace.WithAttributes(attribute.String("lk.pii.weather.location", input.Location)))
	defer span.End()
	return "Example weather: sunny with a temperature of 70 degrees Fahrenheit.", nil
}

type handoffTool struct {
	name    string
	session *agent.AgentSession
	target  func() (agent.AgentInterface, error)
}

func (t *handoffTool) ID() string   { return t.name }
func (t *handoffTool) Name() string { return t.name }
func (t *handoffTool) Description() string {
	return "Transfer this conversation to the other voice agent."
}
func (*handoffTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *handoffTool) Execute(_ context.Context, _ string) (string, error) {
	next, err := t.target()
	if err != nil {
		return "", err
	}
	t.session.UpdateAgent(next)
	return "", llm.StopResponse{}
}

type exampleAgent struct {
	*agent.Agent
	session *agent.AgentSession
}

func (a *exampleAgent) OnEnter() {
	_, err := a.session.GenerateReplyWithOptions(a.session.TraceContext(), agent.GenerateReplyOptions{Instructions: "Briefly introduce yourself and offer to help."})
	if err != nil {
		a.session.Logger().Warnw("example greeting failed", err)
	}
}

func newKelly(config app.AppConfig, session *agent.AgentSession) (agent.AgentInterface, error) {
	primary, err := inference.NewLLM(config.LLMModel, config.LiveKitInferenceAPIKey, config.LiveKitInferenceAPISecret)
	if err != nil {
		return nil, err
	}
	secondary, err := inference.NewLLM(envOr("RTP_AGENT_LLM_FALLBACK_MODEL", "google/gemini-2.5-flash"), config.LiveKitInferenceAPIKey, config.LiveKitInferenceAPISecret)
	if err != nil {
		return nil, err
	}
	base := agent.NewAgent("Your name is Kelly. Be concise and helpful. Weather is simulated. Use transfer_to_alloy when the user asks to speak to Alloy.")
	base.ID = "Kelly"
	base.LLM = llm.NewFallbackAdapter([]llm.LLM{primary, secondary})
	base.STT = stt.NewFallbackAdapter([]stt.STT{
		inference.NewSTT(config.STTModel, config.LiveKitInferenceAPIKey, config.LiveKitInferenceAPISecret, inference.WithSTTLanguage(config.STTLanguage)),
		inference.NewSTT(envOr("RTP_AGENT_STT_FALLBACK_MODEL", "cartesia/ink-whisper"), config.LiveKitInferenceAPIKey, config.LiveKitInferenceAPISecret),
	})
	base.TTS = tts.NewFallbackAdapter([]tts.TTS{
		inference.NewTTS(config.TTSModel, config.LiveKitInferenceAPIKey, config.LiveKitInferenceAPISecret, inference.WithTTSVoice(config.TTSVoice)),
		inference.NewTTS(envOr("RTP_AGENT_TTS_FALLBACK_MODEL", "rime/coda"), config.LiveKitInferenceAPIKey, config.LiveKitInferenceAPISecret, inference.WithTTSVoice(envOr("RTP_AGENT_TTS_FALLBACK_VOICE", "astra"))),
	})
	base.Tools = []llm.Tool{&weatherTool{}, &handoffTool{name: "transfer_to_alloy", session: session, target: func() (agent.AgentInterface, error) { return newAlloy(config, session) }}}
	return &exampleAgent{Agent: base, session: session}, nil
}

func newAlloy(config app.AppConfig, session *agent.AgentSession) (agent.AgentInterface, error) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required to transfer to Alloy")
	}
	base := agent.NewAgent("Your name is Alloy. Be concise and helpful. Weather is simulated. Use transfer_to_kelly when the user asks to speak to Kelly.")
	base.ID = "Alloy"
	base.RealtimeModel = openai.NewRealtimeModel(os.Getenv("OPENAI_API_KEY"), envOr("OPENAI_REALTIME_MODEL", "gpt-realtime"), openai.WithOpenAIRealtimeVoice("alloy"))
	base.Tools = []llm.Tool{&weatherTool{}, &handoffTool{name: "transfer_to_kelly", session: session, target: func() (agent.AgentInterface, error) { return newKelly(config, session) }}}
	return &exampleAgent{Agent: base, session: session}, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func main() {
	config := app.DefaultConfigFromEnv()
	if config.WorkerOptions.AgentName == "" {
		config.WorkerOptions.AgentName = "otel-trace-example"
	}
	server := worker.NewAgentServer(config.WorkerOptions)
	provider, err := setupOTel(context.Background())
	if err != nil {
		logger.Logger.Errorw("failed to initialize tracing", err)
		os.Exit(1)
	}
	if err := server.SetTracerProvider(provider); err != nil {
		_ = provider.Shutdown(context.Background())
		logger.Logger.Errorw("failed to configure tracing", err)
		os.Exit(1)
	}
	err = server.RTCSession(func(job *worker.JobContext) error {
		ctx := context.Background()
		if err := job.SetTraceMetadata(attribute.String("session.id", job.JobID()), attribute.String("langfuse.session.id", job.JobID())); err != nil {
			return err
		}
		detector, err := silero.NewVADWithOptions(silero.WithONNXRuntime())
		if err != nil {
			return fmt.Errorf("initialize Silero ONNX VAD: %w", err)
		}
		if err := job.AddShutdownCallback(func() {
			if err := detector.Close(); err != nil {
				logger.Logger.Errorw("failed to close Silero VAD", err)
			}
		}); err != nil {
			_ = detector.Close()
			return err
		}
		initial := agent.NewAgent("initializing")
		initial.VAD = detector
		session := agent.NewAgentSession(initial, job.Room, agent.AgentSessionOptions{})
		kelly, err := newKelly(config, session)
		if err != nil {
			return err
		}
		session.UpdateAgent(kelly)
		session.On("metrics_collected", func(event agent.Event) {
			if metrics, ok := event.(*agent.MetricsCollectedEvent); ok {
				logger.Logger.Debugw("example metrics collected", "type", metrics.Metrics.GetType())
			}
		})
		// Job teardown flushes completed spans; the server owns provider shutdown.
		return job.StartSession(ctx, session)
	}, nil, nil)
	if err != nil {
		_ = server.Close(context.Background())
		logger.Logger.Errorw("failed to configure tracing example", err)
		os.Exit(1)
	}
	cli.RunAppWithOptions(server)
}
