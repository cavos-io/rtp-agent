package main

import (
	"context"
	"fmt"
	"os"

	openaiAdapter "github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/interface/cli"
	"github.com/cavos-io/conversation-worker/interface/worker"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/sashabaranov/go-openai"
)

func main() {
	opts := worker.WorkerOptions{
		AgentName:  "example-agent",
		WorkerType: worker.WorkerTypeRoom,
	}

	server := worker.NewAgentServer(opts)

	server.RTCSession(func(ctx *worker.JobContext) error {
		return handleAgent(server, ctx)
	}, nil, nil)

	cli.RunApp(server)
}

func handleAgent(server *worker.AgentServer, jobCtx *worker.JobContext) error {
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🤖 [Agent] Initializing...")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Get OpenAI API key from env
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("❌ [Agent] OPENAI_API_KEY not set")
		return fmt.Errorf("OPENAI_API_KEY env var is required")
	}
	fmt.Println("✅ [Agent] OpenAI API key loaded")

	// Create agent with instructions
	ag := agent.NewAgent("You are a helpful AI assistant. Respond concisely and naturally.")
	fmt.Println("✅ [Agent] Agent created")

	// Set up LLM provider (OpenAI)
	ag.LLM = openaiAdapter.NewOpenAILLM(apiKey, "gpt-4o-mini")
	fmt.Println("✅ [Agent] LLM (GPT-4o-mini) configured")

	// Set up STT provider (OpenAI Whisper)
	ag.STT = openaiAdapter.NewOpenAISTT(apiKey, "")
	fmt.Println("✅ [Agent] STT (Whisper) configured")

	// Set up TTS provider (OpenAI)
	ag.TTS = openaiAdapter.NewOpenAITTS(apiKey, openai.TTSModel1, openai.VoiceAlloy)
	fmt.Println("✅ [Agent] TTS (Alloy) configured")

	ag.TurnDetection = agent.TurnDetectionModeSTT
	fmt.Println("✅ [Agent] Turn detection: STT-based")

	// Create agent session options
	sessionOpts := agent.AgentSessionOptions{
		AllowInterruptions:        true,
		MinEndpointingDelay:       0.4,
		MaxEndpointingDelay:       1.0,
		MinConsecutiveSpeechDelay: 0.1,
	}

	// Create and start session
	session := agent.NewAgentSession(ag, nil, sessionOpts)
	fmt.Println("✅ [Agent] Session created")

	// Register session with server for console UI to access
	server.SetConsoleSession(session)
	fmt.Println("✅ [Agent] Session registered with server")

	// Start the session - this initializes Activity
	fmt.Println("⏳ [Agent] Starting session and pipeline...")
	if err := session.Start(context.Background()); err != nil {
		fmt.Printf("❌ [Agent] Failed to start session: %v\n", err)
		logger.Logger.Errorw("Failed to start agent session", err)
		return err
	}
	fmt.Println("✅ [Agent] Session started successfully")

	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("✅ Agent session initialized and ready!")
	fmt.Println("Waiting for user input...")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// For console mode, just keep the session alive
	// For production mode, would connect to LiveKit room
	<-context.Background().Done()

	return nil
}
