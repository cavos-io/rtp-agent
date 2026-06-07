package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/app"
	"github.com/cavos-io/rtp-agent/core/agent"
	betatools "github.com/cavos-io/rtp-agent/core/beta/tools"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
)

const basicAgentGreetingInstruction = "greet the user and introduce yourself"

const basicAgentInstructions = "Your name is Kelly, built by LiveKit. You would interact with users via voice." +
	"with that in mind keep your responses concise and to the point." +
	"do not use emojis, asterisks, markdown, or other special characters in your responses." +
	"You are curious and friendly, and have a sense of humor." +
	"you will speak english to the user"

type basicAgent struct {
	*agent.Agent
}

func newBasicAgent(session *agent.AgentSession) *basicAgent {
	base := agent.NewAgent(basicAgentInstructions)
	base.Tools = []llm.Tool{
		betatools.NewSessionEndCallTool(session, betatools.EndCallToolOptions{}),
		lookupWeatherTool{},
	}
	return &basicAgent{Agent: base}
}

func (a *basicAgent) OnEnter() {
	activity := a.GetActivity()
	if activity == nil || activity.Session == nil {
		return
	}
	_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
		Instructions: basicAgentGreetingInstruction,
	})
}

type lookupWeatherTool struct{}

func (lookupWeatherTool) ID() string   { return "lookup_weather" }
func (lookupWeatherTool) Name() string { return "lookup_weather" }
func (lookupWeatherTool) Description() string {
	return `Called when the user asks for weather related information.
Ensure the user's location (city or region) is provided.
When given a location, please estimate the latitude and longitude of the location and
do not ask the user for them.`
}
func (lookupWeatherTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "The location they are asking for",
			},
			"latitude": map[string]any{
				"type":        "string",
				"description": "The latitude of the location, do not ask user for it",
			},
			"longitude": map[string]any{
				"type":        "string",
				"description": "The longitude of the location, do not ask user for it",
			},
		},
		"required": []string{"location", "latitude", "longitude"},
	}
}
func (lookupWeatherTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Location  string `json:"location"`
		Latitude  string `json:"latitude"`
		Longitude string `json:"longitude"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	logger.Logger.Infow("Looking up weather", "location", params.Location, "latitude", params.Latitude, "longitude", params.Longitude)
	return "sunny with a temperature of 70 degrees.", nil
}

func basicAgentConfigFromEnv() app.AppConfig {
	_ = loadBasicAgentDotEnv(".env")
	cfg := app.DefaultConfigFromEnv()
	cfg.Instructions = basicAgentInstructions
	cfg.LLMProvider = "livekit"
	cfg.LLMModel = "openai/gpt-4.1-mini"
	cfg.STTProvider = "livekit"
	cfg.STTModel = "deepgram/nova-3"
	cfg.STTLanguage = "multi"
	cfg.VADProvider = "silero"
	cfg.TTSProvider = "livekit"
	cfg.TTSModel = "cartesia/sonic-3"
	cfg.TTSVoice = "9626c31c-bec5-4cca-baa8-f8ba9e84c8bc"
	if cfg.TTSTextReplacements == nil {
		cfg.TTSTextReplacements = make(map[string]string)
	}
	cfg.TTSTextReplacements["LiveKit"] = "<<ˈ|l|aɪ|v>> <<ˈ|k|ɪ|t>>"
	return cfg
}

func loadBasicAgentDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := parseBasicAgentDotEnvLine(scanner.Text())
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func parseBasicAgentDotEnvLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		quote := value[0]
		if (quote == '\'' || quote == '"') && value[len(value)-1] == quote {
			value = value[1 : len(value)-1]
		}
	}
	return key, value, true
}

func basicAgentSessionOptions() agent.AgentSessionOptions {
	return agent.AgentSessionOptions{
		PreemptiveGeneration:     true,
		AECWarmupDuration:        3.0,
		ResumeFalseInterruption:  true,
		FalseInterruptionTimeout: 1.0,
		TTSTextReplacements: map[string]string{
			"LiveKit": "<<ˈ|l|aɪ|v>> <<ˈ|k|ɪ|t>>",
		},
	}
}

func newBasicAgentApp(cfg app.AppConfig) (*app.App, error) {
	rtpApp, err := app.Init(cfg)
	if err != nil {
		return nil, err
	}
	if rtpApp.Session == nil {
		_ = rtpApp.Close(context.Background())
		return nil, fmt.Errorf("agent session is not configured")
	}
	rtpApp.Session.Options = mergeBasicAgentSessionOptions(rtpApp.Session.Options)
	kelly := newBasicAgent(rtpApp.Session)
	if rtpApp.Agent != nil {
		copyRuntime(kelly.Agent, rtpApp.Agent)
	}
	rtpApp.Session.UpdateAgent(kelly)
	rtpApp.Agent = kelly.Agent
	return rtpApp, nil
}

func mergeBasicAgentSessionOptions(existing agent.AgentSessionOptions) agent.AgentSessionOptions {
	reference := basicAgentSessionOptions()
	existing.PreemptiveGeneration = reference.PreemptiveGeneration
	existing.AECWarmupDuration = reference.AECWarmupDuration
	existing.ResumeFalseInterruption = reference.ResumeFalseInterruption
	existing.FalseInterruptionTimeout = reference.FalseInterruptionTimeout
	if existing.TTSTextReplacements == nil {
		existing.TTSTextReplacements = make(map[string]string)
	}
	for from, to := range reference.TTSTextReplacements {
		existing.TTSTextReplacements[from] = to
	}
	return existing
}

func copyRuntime(dst *agent.Agent, src *agent.Agent) {
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
	dst.RealtimeModel = src.RealtimeModel
	dst.TTS = src.TTS
	dst.AllowInterruptions = src.AllowInterruptions
	dst.AllowInterruptionsSet = src.AllowInterruptionsSet
	dst.MinConsecutiveSpeechDelay = src.MinConsecutiveSpeechDelay
	dst.UseTTSAlignedTranscript = src.UseTTSAlignedTranscript
	dst.UseTTSAlignedTranscriptSet = src.UseTTSAlignedTranscriptSet
	dst.MinEndpointingDelay = src.MinEndpointingDelay
	dst.MaxEndpointingDelay = src.MaxEndpointingDelay
}
