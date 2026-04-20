package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	elevenlabsAdapter "github.com/cavos-io/conversation-worker/adapter/elevenlabs"
	openaiAdapter "github.com/cavos-io/conversation-worker/adapter/openai"
	sileroAdapter "github.com/cavos-io/conversation-worker/adapter/silero"
	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/interface/cli"
	"github.com/cavos-io/conversation-worker/interface/worker"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/joho/godotenv"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"os/signal"
	"syscall"
)

func main() {
	godotenv.Load()

	opts := worker.WorkerOptions{
		AgentName:  os.Getenv("AGENT_NAME"),
		WorkerType: worker.WorkerTypeRoom,
		WSRL:       os.Getenv("LIVEKIT_URL"),
		APIKey:     os.Getenv("LIVEKIT_API_KEY"),
		APISecret:  os.Getenv("LIVEKIT_API_SECRET"),
	}

	server := worker.NewAgentServer(opts)

	// Setup signal handling for graceful shutdown
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		cli.RunApp(server)
	}()

	// Wait for context cancellation (signal or server exit)
	<-sigCtx.Done()
	logger.Logger.Infow("Shutdown signal received, draining...")
	
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()
	
	if err := server.Drain(drainCtx); err != nil {
		logger.Logger.Errorw("Drain failed", err)
	}
}

func handleAgent(server *worker.AgentServer, jobCtx *worker.JobContext) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	ag.LLM = openaiAdapter.NewOpenAILLM(apiKey, "gpt-4o")
	fmt.Println("✅ [Agent] LLM (GPT-4o-mini) configured")

	// Set up STT provider (OpenAI Whisper)
	ag.STT = openaiAdapter.NewOpenAISTT(apiKey, "")
	fmt.Println("✅ [Agent] STT (Whisper) configured")

	// Set up VAD (required for speech start/end detection and STT segmentation)
	ag.VAD = sileroAdapter.NewSileroVAD()
	fmt.Println("✅ [Agent] VAD configured")

	// Set up TTS provider (ElevenLabs)
	elevenlabsAPIKey := os.Getenv("ELEVENLABS_API_KEY")
	elevenlabsTTS, err := elevenlabsAdapter.NewElevenLabsTTS(elevenlabsAPIKey, "21m00Tcm4TlvDq8ikWAM", "eleven_turbo_v2_5")
	if err != nil {
		log.Println("❌ [Agent] Failed to create ElevenLabs TTS:", err.Error())
		return fmt.Errorf("failed to create ElevenLabs TTS: %w", err)
	}
	ag.TTS = elevenlabsTTS
	// ag.TTS = openaiAdapter.NewOpenAITTS(apiKey, openai.TTSModel1, openai.VoiceAlloy)
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

	// Create session (do not start yet — RoomIO must be wired first)
	session := agent.NewAgentSession(ag, nil, sessionOpts)
	fmt.Println("✅ [Agent] Session created")

	// Register session with server for console UI to access
	server.SetConsoleSession(session)
	fmt.Println("✅ [Agent] Session registered with server")

	// Connect to LiveKit room.
	// The LiveKit SDK snapshots (Merges) the callback fields at ConnectToRoom time,
	// so we MUST set non-nil callbacks BEFORE calling Connect. RoomIO is created
	// after Connect (it needs jobCtx.Room), so the callbacks delegate through a
	// closure that reads the eventual *RoomIO via a shared pointer.
	var rio *worker.RoomIO
	cb := lksdk.NewRoomCallback()
	cb.OnDisconnected = func() { cancel() }
	cb.OnTrackSubscribed = func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
		if rio == nil {
			return
		}
		rio.GetCallback().OnTrackSubscribed(track, pub, rp)
	}
	cb.OnTrackUnsubscribed = func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
		if rio == nil {
			return
		}
		rio.GetCallback().OnTrackUnsubscribed(track, pub, rp)
	}
	cb.OnParticipantDisconnected = func(rp *lksdk.RemoteParticipant) {
		if rio == nil {
			return
		}
		rio.GetCallback().OnParticipantDisconnected(rp)
	}

	fmt.Println("⏳ [Agent] Connecting to LiveKit room...")
	if err := jobCtx.Connect(ctx, cb); err != nil {
		fmt.Printf("❌ [Agent] Failed to connect to room: %v\n", err)
		return fmt.Errorf("failed to connect to room: %w", err)
	}
	fmt.Println("✅ [Agent] Connected to LiveKit room")

	// Create RoomIO — this wires session.Input.Audio and session.Output.Audio automatically.
	rio = worker.NewRoomIO(jobCtx.Room, session, worker.RoomOptions{})
	defer rio.Close()

	// Publish agent's audio output track to the room.
	fmt.Println("⏳ [Agent] Starting RoomIO (publishing audio track)...")
	if err := rio.Start(ctx); err != nil {
		fmt.Printf("❌ [Agent] Failed to start RoomIO: %v\n", err)
		return fmt.Errorf("failed to start RoomIO: %w", err)
	}
	fmt.Println("✅ [Agent] RoomIO started")

	// Start the session pipeline — session.Input.Audio is now set by RoomIO.
	fmt.Println("⏳ [Agent] Starting session and pipeline...")
	if err := session.Start(ctx); err != nil {
		fmt.Printf("❌ [Agent] Failed to start session: %v\n", err)
		logger.Logger.Errorw("Failed to start agent session", err)
		return err
	}
	fmt.Println("✅ [Agent] Session started successfully")

	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("✅ Agent session initialized and ready!")
	fmt.Println("Waiting for room disconnect...")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// Block until room disconnects (cb.OnDisconnected cancels ctx).
	<-ctx.Done()

	return nil
}
