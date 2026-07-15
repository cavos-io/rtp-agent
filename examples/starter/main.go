package main

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"

	livekitAdapter "github.com/cavos-io/rtp-agent/adapter/livekit"
	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/app"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/interface/cli"
	"github.com/cavos-io/rtp-agent/interface/worker"
	livekitWorker "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/cavos-io/rtp-agent/library/logger"
	_ "github.com/joho/godotenv/autoload"
	protologger "github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

//go:embed INSTRUCTIONS.MD
var instructions string

type lookupWeather struct{}

func (l *lookupWeather) ID() string   { return "lookup_weather" }
func (l *lookupWeather) Name() string { return "lookup_weather" }
func (l *lookupWeather) Description() string {
	return `
		Use this tool to look up current weather information in the given location.

		If the location is not supported by the weather service, the tool will indicate this.
		You must tell the user the location's weather is unavailable.
	`
}
func (l *lookupWeather) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "The location to look up weather information for (e.g. city name)",
			},
		},
		"required": []string{"location"},
	}
}
func (l *lookupWeather) Execute(ctx context.Context, args string) (string, error) {
	logger.Logger.Infow("Looking up weather", "args", args)
	return "sunny with a temperature of 70 degrees.", nil
}

func main() {
	zapLogger, err := protologger.NewZapLogger(&protologger.Config{
		JSON: false,
		ComponentLevels: map[string]string{
			"pion": "error",
		},
	})
	if err != nil {
		panic(err)
	}
	lksdk.SetLogger(zapLogger.WithName("lksdk"))

	config := app.DefaultConfigFromEnv()
	config.Instructions = instructions
	config.WorkerOptions.PrometheusPort = 8083

	baseAgent := agent.NewAgent(instructions)

	baseAgent.VAD = silero.NewSileroVAD()

	provider, err := livekitAdapter.NewLiveKitInferenceLLM(config.LLMModel, config.LiveKitInferenceAPIKey, config.LiveKitInferenceAPISecret)
	if err != nil {
		os.Exit(1)
	}
	baseAgent.LLM = provider
	baseAgent.STT = livekitAdapter.NewSTT(
		config.STTModel,
		config.LiveKitInferenceAPIKey,
		config.LiveKitInferenceAPISecret,
		livekitAdapter.WithSTTLanguage(config.STTLanguage),
	)
	baseAgent.TTS = livekitAdapter.NewTTS(
		config.TTSModel,
		config.LiveKitInferenceAPIKey,
		config.LiveKitInferenceAPISecret,
		livekitAdapter.WithTTSVoice(config.TTSVoice),
	)

	// To add tools, call rtpApp.Agent.UpdateTools.
	// Here's an example that adds a simple weather tool.
	// You also have to import github.com/cavos-io/rtp-agent/core/llm at the top of this file.
	//err = baseAgent.UpdateTools(context.Background(), []llm.Tool{&lookupWeather{}})
	//if err != nil {
	//	logger.Logger.Errorw("failed to update tools", err)
	//}

	server := worker.NewAgentServer(config.WorkerOptions)
	err = server.RTCSession(
		func(jobContext *worker.JobContext) error {
			session := agent.NewAgentSession(baseAgent, jobContext.Room, agent.AgentSessionOptions{})
			jobContext.SetPrimarySession(session)
			session.SetJobContext(jobContext)

			roomIO := livekitWorker.NewRoomIO(nil, session, livekitWorker.RoomOptions{})
			room := jobContext.NewRoom(roomIO.GetCallback())
			if err := jobContext.ConnectPreparedRoom(context.Background(), room); err != nil {
				_ = roomIO.Close()
				logger.Logger.Errorw("failed to connect to room", err)
				return err
			}
			roomIO.AttachRoom(jobContext.Room)

			if err := jobContext.AddShutdownCallback(func() {
				_ = session.Stop(context.Background())
				_ = roomIO.Close()
			}); err != nil {
				logger.Logger.Warnw("failed to register RoomIO teardown on job shutdown", err)
			}

			if jobContext.Report.RecordingOptions.Audio && jobContext.SessionDirectory() != "" {
				err := roomIO.Recorder.Start(filepath.Join(jobContext.SessionDirectory(), livekitWorker.RecordingFileName), 48000)
				if err != nil {
					logger.Logger.Errorw("failed to start audio recorder", err)
					return err
				}
			}
			if jobContext.Room.LocalParticipant != nil && jobContext.Room.ConnectionState() == lksdk.ConnectionStateConnected {
				if err := roomIO.Start(context.Background()); err != nil {
					return err
				}
			}

			ctx := context.Background()
			err := session.Start(ctx)
			if err != nil {
				logger.Logger.Errorw(err.Error(), err)
				return err
			}

			_, err = session.GenerateReplyWithOptions(ctx, agent.GenerateReplyOptions{
				Instructions: "Greet the user in a helpful and friendly manner",
			})
			if err != nil {
				logger.Logger.Errorw(err.Error(), err)
				return err
			}

			return nil
		},
		nil,
		nil,
	)
	if err != nil {
		logger.Logger.Errorw(err.Error(), err)
		os.Exit(1)
	}

	cli.RunAppWithOptions(server, cli.WithLogger(zapLogger))
}
