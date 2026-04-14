package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"

	"github.com/cavos-io/conversation-worker/adapter/elevenlabs"
	oaiadapter "github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/interface/cli"
	"github.com/cavos-io/conversation-worker/interface/worker"
	"github.com/cavos-io/conversation-worker/library/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
)

func main() {
	// Read credentials from environment variables
	lkURL := os.Getenv("LIVEKIT_URL")
	lkAPIKey := os.Getenv("LIVEKIT_API_KEY")
	lkSecret := os.Getenv("LIVEKIT_API_SECRET")
	openaiKey := os.Getenv("OPENAI_API_KEY")
	elevenKey := os.Getenv("ELEVENLABS_API_KEY")

	// Setup Worker
	server := worker.NewAgentServer(worker.WorkerOptions{
		AgentName:  "cavos-agent",
		WorkerType: worker.WorkerTypeRoom,
		WSRL:       lkURL,
		APIKey:     lkAPIKey,
		APISecret:  lkSecret,
	})
	// Load .env file if present
	loadDotEnv(".env")

	// Start pprof HTTP server for resource monitoring
	pprofAddr := envOrDefault("PPROF_ADDR", ":6060")
	go func() {
		slog.Info("pprof server listening", "addr", pprofAddr)
		if err := http.ListenAndServe(pprofAddr, nil); err != nil {
			slog.Error("pprof server failed", "err", err)
		}
	}()

	// ============================================================
	// CREDENTIALS
	// ============================================================
	livekitURL := envOrDefault("LIVEKIT_URL", "")
	livekitAPIKey := envOrDefault("LIVEKIT_API_KEY", "")
	livekitAPISecret := envOrDefault("LIVEKIT_API_SECRET", "")
	openaiAPIKey := envOrDefault("OPENAI_API_KEY", "")
	elevenLabsAPIKey := envOrDefault("ELEVENLABS_API_KEY", "")

	// ============================================================
	// 1. Setup Worker Options
	// ============================================================
	opts := worker.WorkerOptions{
		AgentName:  "cavos-voice-agent",
		WorkerType: worker.WorkerTypeRoom,
		WSRL:       livekitURL,
		APIKey:     livekitAPIKey,
		APISecret:  livekitAPISecret,
	}

	// Register RTC session handler
	server.RTCSession(func(jobCtx *worker.JobContext) error {
		logger.Logger.Infow("agent started", "room", jobCtx.Job.Room.Name)

		// LLM
		llmProvider := oaiadapter.NewOpenAILLM(openaiKey, "gpt-4o")

		// STT (Whisper + VAD)
		simpleVAD := vad.NewSimpleVAD(0.0005)
		sttProvider := stt.NewStreamAdapter(oaiadapter.NewOpenAISTT(openaiKey, ""), simpleVAD)

		// TTS (ElevenLabs)
		ttsProvider, err := elevenlabs.NewElevenLabsTTS(
			elevenKey,
			"21m00Tcm4TlvDq8ikWAM", // voice: Rachel
			"eleven_turbo_v2_5",
		)
		if err != nil {
			return fmt.Errorf("TTS init: %w", err)
		}

		// System prompt
		chatCtx := llm.NewChatContext()
		chatCtx.Append(&llm.ChatMessage{
			Role: llm.ChatRoleSystem,
			Content: []llm.ChatContent{
				{Text: `You are an AI assistant named "Cavos Agent."
Answer concisely and naturally, 2-3 sentences maximum.`},
			},
		})

		// Agent definition
		agentDef := agent.NewAgent("You are a friendly voice-based AI assistant.")
		agentDef.STT = sttProvider
		agentDef.VAD = simpleVAD
		agentDef.LLM = llmProvider
		agentDef.TTS = ttsProvider
		agentDef.ChatCtx = chatCtx
		agentDef.TurnDetection = agent.TurnDetectionModeVAD
		agentDef.AllowInterruptions = true
		agentDef.MinEndpointingDelay = 0.5
		agentDef.MaxEndpointingDelay = 3.0

		// Connect to room (buffer early tracks before RoomIO is ready)
		type earlyTrack struct {
			track *webrtc.TrackRemote
			pub   *lksdk.RemoteTrackPublication
			rp    *lksdk.RemoteParticipant
		}
		var earlyTracks []earlyTrack
		var roomIO *worker.RoomIO

		cb := lksdk.NewRoomCallback()
		cb.OnTrackSubscribed = func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
			if roomIO != nil {
				roomIO.GetCallback().OnTrackSubscribed(track, pub, rp)
			} else {
				earlyTracks = append(earlyTracks, earlyTrack{track, pub, rp})
			}
		}

		if err := jobCtx.Connect(context.Background(), cb); err != nil {
			return fmt.Errorf("connect: %w", err)
		}

		// Create session
		session := agent.NewAgentSession(agentDef, jobCtx.Room, agent.AgentSessionOptions{
			AllowInterruptions:  true,
			MinEndpointingDelay: 0.5,
			MaxEndpointingDelay: 3.0,
		})
		session.ChatCtx = chatCtx

		// Create RoomIO and replay buffered tracks
		roomIO = worker.NewRoomIO(jobCtx.Room, session, worker.RoomOptions{})
		for _, et := range earlyTracks {
			roomIO.GetCallback().OnTrackSubscribed(et.track, et.pub, et.rp)
		}

		// Start audio I/O and agent pipeline
		if err := roomIO.Start(context.Background()); err != nil {
			return fmt.Errorf("roomIO start: %w", err)
		}
		if err := session.Start(context.Background()); err != nil {
			return fmt.Errorf("session start: %w", err)
		}

		logger.Logger.Infow("voice pipeline ready", "room", jobCtx.Job.Room.Name)
		select {} // block until context done

	fmt.Println("✅ Agent server created")
	fmt.Printf("   LiveKit URL: %s\n", livekitURL)
	fmt.Printf("   Agent Name:  %s\n", opts.AgentName)
	fmt.Println("   Connecting to LiveKit...")

	// ============================================================
	// 2. Register RTC Session Entrypoint
	// ============================================================
	server.RTCSession(func(jobCtx *worker.JobContext) error {
		logger.Logger.Infow("🚀 Agent entrypoint started",
			"jobId", jobCtx.Job.Id,
			"room", jobCtx.Job.Room.Name,
		)

		// ----------------------------------------------------------
		// 2a. Initialize AI providers
		// ----------------------------------------------------------

		// LLM: OpenAI GPT-4o
		llmProvider := oaiadapter.NewOpenAILLM(openaiAPIKey, "gpt-4o")

		// STT: OpenAI Whisper (non-streaming) wrapped with StreamAdapter + SimpleVAD
		openaiSTT := oaiadapter.NewOpenAISTT(openaiAPIKey, "")
		simpleVAD := vad.NewSimpleVAD(0.0005) // Very low threshold for Playground's quiet audio
		sttProvider := stt.NewStreamAdapter(openaiSTT, simpleVAD)

		// TTS: ElevenLabs (streaming via WebSocket)
		ttsProvider, err := elevenlabs.NewElevenLabsTTS(
			elevenLabsAPIKey,
			"21m00Tcm4TlvDq8ikWAM", // Rachel voice
			"eleven_turbo_v2_5",    // Turbo model for low latency
		)
		if err != nil {
			logger.Logger.Errorw("Failed to create ElevenLabs TTS", err)
			return err
		}

		// ----------------------------------------------------------
		// 2b. Create Chat Context (system prompt)
		// ----------------------------------------------------------
		chatCtx := llm.NewChatContext()
		chatCtx.Append(&llm.ChatMessage{
			Role: llm.ChatRoleSystem,
			Content: []llm.ChatContent{
				{Text: `Kamu adalah asisten AI bernama "Cavos Agent". 
Kamu ramah, membantu, dan berbicara dalam Bahasa Indonesia. 
Jawab dengan ringkas dan natural seperti percakapan sehari-hari.
Jangan bertele-tele, maksimal 2-3 kalimat per respons.`},
			},
		})

		// ----------------------------------------------------------
		// 2c. Create Agent + Session properly
		// ----------------------------------------------------------
		agentDef := agent.NewAgent("Kamu adalah asisten AI percakapan suara yang ramah.")
		agentDef.STT = sttProvider
		agentDef.VAD = simpleVAD
		agentDef.LLM = llmProvider
		agentDef.TTS = ttsProvider
		agentDef.ChatCtx = chatCtx
		agentDef.TurnDetection = agent.TurnDetectionModeVAD
		agentDef.AllowInterruptions = true
		agentDef.MinEndpointingDelay = 0.5
		agentDef.MaxEndpointingDelay = 3.0

		// ----------------------------------------------------------
		// 2d. Connect to Room FIRST
		// ----------------------------------------------------------
		fmt.Println("🔌 Connecting to room...")

		// Buffer early track subscriptions (before RoomIO exists)
		type earlyTrack struct {
			track *webrtc.TrackRemote
			pub   *lksdk.RemoteTrackPublication
			rp    *lksdk.RemoteParticipant
		}
		var earlyTracks []earlyTrack
		var roomIO *worker.RoomIO

		disconnectCh := make(chan struct{})

		cb := lksdk.NewRoomCallback()
		cb.OnTrackSubscribed = func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
			fmt.Printf("📡 Track subscribed: participant=%s kind=%s\n", rp.Identity(), track.Kind().String())
			if roomIO != nil {
				roomIO.GetCallback().OnTrackSubscribed(track, pub, rp)
			} else {
				fmt.Println("   ⏳ Buffering track (RoomIO not ready yet)")
				earlyTracks = append(earlyTracks, earlyTrack{track, pub, rp})
			}
		}
		cb.OnDisconnected = func() {
			fmt.Println("🔌 Room disconnected — shutting down agent session")
			close(disconnectCh)
		}

		if err := jobCtx.Connect(context.Background(), cb); err != nil {
			fmt.Printf("❌ Failed to connect to room: %v\n", err)
			return err
		}
		fmt.Printf("✅ Connected to room: %s\n", jobCtx.Job.Room.Name)

		// ----------------------------------------------------------
		// 2e. Create Session with NewAgentSession
		// ----------------------------------------------------------
		session := agent.NewAgentSession(agentDef, jobCtx.Room, agent.AgentSessionOptions{
			AllowInterruptions:  true,
			MinEndpointingDelay: 0.5,
			MaxEndpointingDelay: 3.0,
		})
		session.ChatCtx = chatCtx

		// ----------------------------------------------------------
		// 2f. Create RoomIO + replay buffered tracks
		// ----------------------------------------------------------
		roomIO = worker.NewRoomIO(jobCtx.Room, session, worker.RoomOptions{})

		// Replay any tracks that were subscribed during Connect
		if len(earlyTracks) > 0 {
			fmt.Printf("🔄 Replaying %d buffered track(s)...\n", len(earlyTracks))
			for _, et := range earlyTracks {
				roomIO.GetCallback().OnTrackSubscribed(et.track, et.pub, et.rp)
			}
		}

		fmt.Println("🎤 Starting audio I/O...")
		if err := roomIO.Start(context.Background()); err != nil {
			fmt.Printf("❌ Failed to start RoomIO: %v\n", err)
			return err
		}

		fmt.Println("🧠 Starting agent pipeline...")
		if err := session.Start(context.Background()); err != nil {
			fmt.Printf("❌ Failed to start AgentSession: %v\n", err)
			return err
		}

		fmt.Println("✅ Voice agent pipeline started!")
		fmt.Println("   LLM: openai/gpt-4o")
		fmt.Println("   STT: openai/whisper+vad")
		fmt.Println("   TTS: elevenlabs/turbo_v2_5")
		fmt.Printf("   Room: %s\n", jobCtx.Job.Room.Name)
		fmt.Println("   🎧 Listening for speech...")

		// Block until room disconnects
		<-disconnectCh
		fmt.Println("🔌 Room disconnected — shutting down session...")
		session.Stop(context.Background())
		roomIO.Close()
		fmt.Printf("✅ Agent session ended for room: %s\n", jobCtx.Job.Room.Name)
		return nil

	}, nil, nil)

	// ============================================================
	// 3. Run CLI
	// ============================================================
	cli.RunApp(server)
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// loadDotEnv reads a .env file and sets env vars (skips already-set vars).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // file not found is fine
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Only set if not already in environment
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}
