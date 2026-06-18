package main

import (
	"context"
	_ "embed"

	"github.com/cavos-io/rtp-agent/app"
	"github.com/cavos-io/rtp-agent/interface/cli"
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

	rtpApp, err := app.Init(config)
	if err != nil {
		panic(err)
	}
	defer rtpApp.Close(context.Background())

	// To add tools, call rtpApp.Agent.UpdateTools.
	// Here's an example that adds a simple weather tool.
	// You also have to import github.com/cavos-io/rtp-agent/core/llm at the top of this file.
	//err = rtpApp.Agent.UpdateTools(context.Background(), []llm.Tool{&lookupWeather{}})
	//if err != nil {
	//	logger.Logger.Errorw("failed to update tools", err)
	//}

	cli.RunApp(rtpApp.Server)
}
