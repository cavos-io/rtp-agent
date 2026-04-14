package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cavos-io/rtp-agent/adapter/elevenlabs"
	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/adapter/tenvad"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/interface/cli"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/library/logger"
)

func main() {
	// Simple internal .env loader to avoid external dependencies/vendoring issues
	loadDotEnv(".env")

	// Initialize Worker Server
	server := worker.NewAgentServer(worker.WorkerOptions{
		AgentName:  "cavos-agent",
		WorkerType: worker.WorkerTypeRoom,
		WSRL:       os.Getenv("LIVEKIT_URL"),
		APIKey:     os.Getenv("LIVEKIT_API_KEY"),
		APISecret:  os.Getenv("LIVEKIT_API_SECRET"),
	})

	server.RTCSession(agentHandler, nil, nil)

	fmt.Println("Running CLI App...")
	cli.RunApp(server)
}

// loadDotEnv reads a .env file and sets environment variables manually
func loadDotEnv(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return // Ignore if file doesn't exist
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove quotes if present
			value = strings.Trim(value, `"'`)
			os.Setenv(key, value)
		}
	}
	fmt.Println("Loaded configuration from .env")
}

func agentHandler(jobCtx *worker.JobContext) error {
	logger.Logger.Infow("Agent session starting", "room", jobCtx.Job.Room.Name)

	openaiKey := os.Getenv("OPENAI_API_KEY")
	elevenKey := os.Getenv("ELEVENLABS_API_KEY")

	// 1. Initialize VAD (Default: Silero)
	vadType := os.Getenv("VAD_TYPE")
	if vadType == "" {
		vadType = "silero"
	}
	v := createSelectedVAD(vadType)

	// 2. Initialize Agent
	a := agent.NewAgent("You are a friendly and helpful AI assistant.")
	a.VAD = v
	
	// Create STT with StreamAdapter for real-time VAD-gated recognition
	sttBase := openai.NewOpenAISTT(openaiKey, "")
	a.STT = stt.NewStreamAdapter(sttBase, v)
	
	a.LLM = openai.NewOpenAILLM(openaiKey, "gpt-4o-mini")
	
	ttsProvider, err := elevenlabs.NewElevenLabsTTS(elevenKey, "21m00Tcm4TlvDq8ikWAM", "eleven_turbo_v2_5")
	if err != nil {
		return fmt.Errorf("failed to init TTS: %w", err)
	}
	a.TTS = ttsProvider

	// 3. Setup Session
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to room
	if err := jobCtx.Connect(ctx, nil); err != nil {
		return fmt.Errorf("failed to connect to room: %w", err)
	}

	session := agent.NewAgentSession(a, jobCtx.Room, agent.AgentSessionOptions{
		AllowInterruptions: true,
	})

	// Start Session
	if err := session.Start(ctx); err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}

	// Wait for completion/termination
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigChan:
		logger.Logger.Infow("Signal received, stopping session")
	case <-ctx.Done():
		logger.Logger.Infow("Context done, stopping session")
	}

	jobCtx.Shutdown("agent session ended")
	return nil
}

func createSelectedVAD(vadType string) vad.VAD {
	switch vadType {
	case "silero":
		logger.Logger.Infow("Using Silero VAD (Default)")
		return silero.NewSileroVAD(
			silero.WithModelPath("silero_vad.onnx"),
		)
	case "tenvad":
		logger.Logger.Infow("Using TEN-VAD")
		return tenvad.NewTenVAD()
	default:
		logger.Logger.Infow("Using Simple VAD (Fallback)")
		return vad.NewSimpleVAD(0.0005)
	}
}
