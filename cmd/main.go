package main

import (
	"context"
	"fmt"
	"os"

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

	}, nil, nil)

	cli.RunApp(server)
}
