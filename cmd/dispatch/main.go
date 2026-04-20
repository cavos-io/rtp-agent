package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func main() {
	loadDotEnv("../../.env")

	apiKey := envOrDefault("LIVEKIT_API_KEY", "")
	apiSecret := envOrDefault("LIVEKIT_API_SECRET", "")
	livekitURL := envOrDefault("LIVEKIT_URL", "")
	agentName := envOrDefault("AGENT_NAME", "cavos-voice-agent")

	if apiKey == "" || apiSecret == "" || livekitURL == "" {
		fmt.Println("❌ LIVEKIT_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET harus di-set di .env")
		os.Exit(1)
	}

	// Convert wss:// → https:// for REST API calls
	httpURL := strings.Replace(livekitURL, "wss://", "https://", 1)

	if len(os.Args) < 2 {
		fmt.Println("Usage: dispatch <room_name>")
		fmt.Println("  Dispatches the agent to a specific room")
		os.Exit(1)
	}
	roomName := os.Args[1]

	client := lksdk.NewAgentDispatchServiceClient(httpURL, apiKey, apiSecret)

	dispatch, err := client.CreateDispatch(context.Background(), &livekit.CreateAgentDispatchRequest{
		AgentName: agentName,
		Room:      roomName,
	})
	if err != nil {
		fmt.Printf("❌ Failed to create dispatch: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Agent dispatched!\n")
	fmt.Printf("   Room:     %s\n", roomName)
	fmt.Printf("   Agent:    %s\n", agentName)
	fmt.Printf("   Dispatch: %v\n", dispatch)
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
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
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}
