package rawfunction

import (
	"context"
	"encoding/json"

	"github.com/cavos-io/rtp-agent/app"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
)

const instructions = "You are a helpful assistant"

func NewAgent() *agent.Agent {
	a := agent.NewAgent(instructions)
	a.Tools = []llm.Tool{openGateTool{}}
	return a
}

func ConfigFromEnv() app.AppConfig {
	cfg := app.DefaultConfigFromEnv()
	cfg.Instructions = instructions
	cfg.RealtimeProvider = "openai"
	return cfg
}

func NewApp(cfg app.AppConfig) (*app.App, error) {
	rtpApp, err := app.Init(cfg)
	if err != nil {
		return nil, err
	}
	rawAgent := NewAgent()
	if rtpApp.Agent != nil {
		copyRuntime(rawAgent, rtpApp.Agent)
	}
	if rtpApp.Session != nil {
		rtpApp.Session.UpdateAgent(rawAgent)
	}
	rtpApp.Agent = rawAgent
	return rtpApp, nil
}

type openGateTool struct{}

func (openGateTool) ID() string { return "open_gate" }

func (openGateTool) Name() string { return "open_gate" }

func (openGateTool) Description() string {
	return "Opens a specified gate from a predefined set of access points."
}

func (openGateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"gate_id": map[string]any{
				"type": "string",
				"description": "Identifier of the gate to open. Must be one of the " +
					"system's predefined access points.",
				"enum": []any{
					"main_entrance",
					"north_parking",
					"loading_dock",
					"side_gate",
					"service_entry",
				},
			},
		},
		"required": []any{"gate_id"},
	}
}

func (openGateTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		GateID string `json:"gate_id"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	logger.Logger.Infow("Opening gate", "gate_id", params.GateID)
	return "Gate " + params.GateID + " opened successfully", nil
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
