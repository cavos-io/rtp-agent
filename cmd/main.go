package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"os/signal"
	"syscall"

	elevenlabsAdapter "github.com/cavos-io/rtp-agent/adapter/elevenlabs"
	openaiAdapter "github.com/cavos-io/rtp-agent/adapter/openai"
	rnnoiseAdapter "github.com/cavos-io/rtp-agent/adapter/rnnoise"
	sileroAdapter "github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/interface/cli"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/joho/godotenv"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
)

func main() {
	godotenv.Load()

	// Start pprof HTTP server for memory profiling.
	pprofAddr := os.Getenv("PPROF_ADDR")
	if pprofAddr == "" {
		pprofAddr = ":6060"
	}
	go func() {
		log.Println("pprof server listening on", pprofAddr)
		if err := http.ListenAndServe(pprofAddr, nil); err != nil {
			log.Println("pprof server error:", err)
		}
	}()

	// Global pre-warm of Silero VAD to catch errors early and warm up library
	fmt.Println("🚀 [Main] Pre-warming Silero VAD...")
	modelPath := os.Getenv("SILERO_VAD_MODEL_PATH")
	if modelPath == "" {
		modelPath = "/models/silero_vad.onnx"
	}
	preWarmVAD, err := sileroAdapter.NewSileroVAD(
		sileroAdapter.WithModelPath(modelPath),
		sileroAdapter.WithMinSpeechDuration(0.05),
		sileroAdapter.WithMinSilenceDuration(0.3),
		sileroAdapter.WithSampleRate(16000),
	)
	if err != nil {
		log.Printf("⚠️ [Main] Failed to initialize pre-warm VAD: %v\n", err)
	} else {
		start := time.Now()
		if err := preWarmVAD.PreWarm(); err != nil {
			log.Printf("⚠️ [Main] Failed to pre-warm Silero VAD: %v\n", err)
		} else {
			fmt.Printf("✅ [Main] Silero VAD pre-warmed in %s\n", time.Since(start))
		}
	}

	opts := worker.WorkerOptions{
		AgentName:  os.Getenv("AGENT_NAME"),
		WorkerType: worker.WorkerTypeRoom,
		WSRL:       os.Getenv("LIVEKIT_URL"),
		APIKey:     os.Getenv("LIVEKIT_API_KEY"),
		APISecret:  os.Getenv("LIVEKIT_API_SECRET"),
	}

	server := worker.NewAgentServer(opts)
	server.RTCSession(
		func(jobCtx *worker.JobContext) error {
			return handleAgent(server, jobCtx)
		},
		nil,
		nil,
	)

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
	startTime := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("🤖 [Agent] Initializing... (jobId=%s, t=0s)\n", jobCtx.Job.Id)
	fmt.Printf("📊 [MEM] Before session: HeapInuse=%dMB Goroutines=%d\n",
		memBefore.HeapInuse/1024/1024, runtime.NumGoroutine())
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

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
	modelPath := os.Getenv("SILERO_VAD_MODEL_PATH")
	if modelPath == "" {
		modelPath = "/models/silero_vad.onnx"
	}
	vadAdapter, err := sileroAdapter.NewSileroVAD(
		sileroAdapter.WithModelPath(modelPath),
		sileroAdapter.WithMinSpeechDuration(0.05),
		sileroAdapter.WithMinSilenceDuration(0.3),
		sileroAdapter.WithSampleRate(16000),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize Silero VAD: %w", err)
	}
	ag.VAD = vadAdapter
	fmt.Println("✅ [Agent] VAD (Silero ONNX) configured")

	// Pre-warm Silero VAD
	start := time.Now()
	if err := vadAdapter.PreWarm(); err != nil {
		return fmt.Errorf("failed to pre-warm Silero VAD: %w", err)
	}
	fmt.Printf("✅ [Agent] VAD (Silero ONNX) pre-warmed in %s\n", time.Since(start))

	// Set up Noise Cancellation (RNNoise)
	if os.Getenv("NOISE_CANCELLATION_ENABLED") == "true" {
		sampleRate := 48000
		if sr := os.Getenv("NOISE_CANCELLATION_SAMPLE_RATE"); sr != "" {
			fmt.Sscanf(sr, "%d", &sampleRate)
		}

		noiseSuppressor, err := rnnoiseAdapter.NewRNNoiseSuppressor(rnnoiseAdapter.RNNoiseOptions{
			SampleRate: uint32(sampleRate),
		})
		if err != nil {
			fmt.Printf("⚠️ [Agent] Failed to initialize RNNoise: %v\n", err)
		} else {
			ag.Noise = noiseSuppressor
			fmt.Printf("✅ [Agent] Noise Cancellation (RNNoise) configured at %dHz\n", sampleRate)
		}
	}


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
	session.Noise = ag.Noise
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

	fmt.Printf("⏳ [Agent] Connecting to LiveKit room... (t=%s)\n", time.Since(startTime).Round(time.Millisecond))
	if err := jobCtx.Connect(ctx, cb); err != nil {
		fmt.Printf("❌ [Agent] Failed to connect to room: %v\n", err)
		return fmt.Errorf("failed to connect to room: %w", err)
	}
	fmt.Printf("✅ [Agent] Connected to LiveKit room (t=%s)\n", time.Since(startTime).Round(time.Millisecond))

	// Create RoomIO — this wires session.Input.Audio and session.Output.Audio automatically.
	rio = worker.NewRoomIO(jobCtx.Room, session, worker.RoomOptions{})

	// Publish agent's audio output track to the room.
	fmt.Printf("⏳ [Agent] Starting RoomIO (publishing audio track)... (t=%s)\n", time.Since(startTime).Round(time.Millisecond))
	if err := rio.Start(ctx); err != nil {
		fmt.Printf("❌ [Agent] Failed to start RoomIO: %v\n", err)
		return fmt.Errorf("failed to start RoomIO: %w", err)
	}
	fmt.Printf("✅ [Agent] RoomIO started (t=%s)\n", time.Since(startTime).Round(time.Millisecond))

	// Start the session pipeline — session.Input.Audio is now set by RoomIO.
	fmt.Printf("⏳ [Agent] Starting session and pipeline... (t=%s)\n", time.Since(startTime).Round(time.Millisecond))
	if err := session.Start(ctx); err != nil {
		fmt.Printf("❌ [Agent] Failed to start session: %v\n", err)
		logger.Logger.Errorw("Failed to start agent session", err)
		return err
	}
	fmt.Printf("✅ [Agent] Session started (t=%s)\n", time.Since(startTime).Round(time.Millisecond))

	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("✅ Agent ready! (total setup: %s)\n", time.Since(startTime).Round(time.Millisecond))
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Block until room disconnects (cb.OnDisconnected cancels ctx).
	<-ctx.Done()
	fmt.Printf("⚠️  [PANEL] handleAgent ctx.Done — agent function returning (jobId=%s, uptime=%s)\n", jobCtx.Job.Id, time.Since(startTime).Round(time.Millisecond))

	// Explicit cleanup: close session, RoomIO, disconnect room, nil all
	// large references so GC can reclaim everything.
	session.Close()
	rio.Close()

	// Clear the console session reference held by the server.
	server.SetConsoleSession(nil)

	// Disconnect the LiveKit room to release WebSocket and WebRTC resources.
	if jobCtx.Room != nil {
		jobCtx.Room.Disconnect()
	}

	// Allow Pion WebRTC resources (TURN/ICE/DTLS/SCTP) to clean up.
	time.Sleep(3 * time.Second)

	// Nil all local references so nothing in this frame keeps objects alive.
	rio = nil
	session = nil
	ag = nil

	// Close idle HTTP connections cached by the default transport (OpenAI SDK
	// and other adapters use http.DefaultTransport).
	http.DefaultClient.CloseIdleConnections()

	runtime.GC()
	debug.FreeOSMemory()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	fmt.Printf("🧹 [MEM] Post-cleanup: HeapInuse=%dMB HeapIdle=%dMB HeapSys=%dMB Goroutines=%d\n",
		memStats.HeapInuse/1024/1024,
		memStats.HeapIdle/1024/1024,
		memStats.HeapSys/1024/1024,
		runtime.NumGoroutine(),
	)
	logger.Logger.Infow("Post-session memory released", "jobId", jobCtx.Job.Id)

	return nil
}
