package runway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestRunwayPluginMetadataMatchesReference(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.runway" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.runway", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("plugin version = %q, want 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.runway" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.runway", PluginPackage)
	}
}

func TestNewRunwayAvatarRequiresExactlyOneAvatarSource(t *testing.T) {
	if _, err := NewRunwayAvatar("test-key"); err == nil || !strings.Contains(err.Error(), "either avatar_id or preset_id must be provided") {
		t.Fatalf("NewRunwayAvatar missing source error = %v", err)
	}
	_, err := NewRunwayAvatar("test-key", WithRunwayAvatarID("avatar-123"), WithRunwayPresetID("preset-123"))
	if err == nil || !strings.Contains(err.Error(), "provide avatar_id or preset_id, not both") {
		t.Fatalf("NewRunwayAvatar duplicate source error = %v", err)
	}
}

func TestNewRunwayAvatarUsesReferenceDefaults(t *testing.T) {
	t.Setenv(runwayAPISecretEnv, "env-secret")

	avatar, err := NewRunwayAvatar("explicit-secret", WithRunwayAvatarID("avatar-123"))
	if err != nil {
		t.Fatalf("NewRunwayAvatar error = %v", err)
	}

	if avatar.apiKey != "explicit-secret" {
		t.Fatalf("apiKey = %q, want explicit secret", avatar.apiKey)
	}
	if avatar.apiURL != "https://api.dev.runwayml.com" {
		t.Fatalf("apiURL = %q, want reference API URL", avatar.apiURL)
	}
	if avatar.avatar["type"] != "custom" || avatar.avatar["avatarId"] != "avatar-123" {
		t.Fatalf("avatar = %#v, want custom avatar ID", avatar.avatar)
	}
	if avatar.sampleRate != 16000 {
		t.Fatalf("sampleRate = %d, want reference sample rate", avatar.sampleRate)
	}
	if avatar.Provider() != "runway" {
		t.Fatalf("Provider() = %q, want runway", avatar.Provider())
	}
	if avatar.AvatarIdentity() != "runway-avatar-agent" {
		t.Fatalf("AvatarIdentity() = %q, want reference identity", avatar.AvatarIdentity())
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestNewRunwayAvatarUsesEnvironmentAPIConfigAndPreset(t *testing.T) {
	t.Setenv(runwayAPISecretEnv, "env-secret")
	t.Setenv(runwayBaseURLEnv, "https://runway.example")

	avatar, err := NewRunwayAvatar("", WithRunwayPresetID("preset-123"))
	if err != nil {
		t.Fatalf("NewRunwayAvatar error = %v", err)
	}

	if avatar.apiKey != "env-secret" {
		t.Fatalf("apiKey = %q, want env secret", avatar.apiKey)
	}
	if avatar.apiURL != "https://runway.example" {
		t.Fatalf("apiURL = %q, want env API URL", avatar.apiURL)
	}
	if avatar.avatar["type"] != "runway-preset" || avatar.avatar["presetId"] != "preset-123" {
		t.Fatalf("avatar = %#v, want preset avatar", avatar.avatar)
	}
}

func TestBuildRunwayCreateSessionRequestMatchesReferencePayload(t *testing.T) {
	avatar, err := NewRunwayAvatar("test-secret",
		WithRunwayAvatarID("avatar-123"),
		WithRunwayMaxDuration(120),
	)
	if err != nil {
		t.Fatalf("NewRunwayAvatar error = %v", err)
	}

	endpoint, headers, body, err := buildRunwayCreateSessionRequest(avatar, agent.AvatarStartInfo{
		LiveKitURL:    "wss://livekit.example",
		LiveKitToken:  "livekit-token",
		RoomName:      "support-room",
		AgentIdentity: "agent-identity",
	})
	if err != nil {
		t.Fatalf("buildRunwayCreateSessionRequest error = %v", err)
	}
	if endpoint != "/v1/realtime_sessions" {
		t.Fatalf("endpoint = %q, want realtime session endpoint", endpoint)
	}
	if headers["Authorization"] != "Bearer test-secret" {
		t.Fatalf("Authorization = %q, want Bearer test-secret", headers["Authorization"])
	}
	if headers["X-Runway-Version"] != "2024-11-06" {
		t.Fatalf("X-Runway-Version = %q, want reference API version", headers["X-Runway-Version"])
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if payload["model"] != "gwm1_avatars" {
		t.Fatalf("model = %#v, want gwm1_avatars", payload["model"])
	}
	if payload["maxDuration"] != float64(120) {
		t.Fatalf("maxDuration = %#v, want 120", payload["maxDuration"])
	}
	livekit := payload["livekit"].(map[string]any)
	if livekit["url"] != "wss://livekit.example" {
		t.Fatalf("livekit.url = %#v, want LiveKit URL", livekit["url"])
	}
	if livekit["token"] != "livekit-token" {
		t.Fatalf("livekit.token = %#v, want LiveKit token", livekit["token"])
	}
	if livekit["roomName"] != "support-room" {
		t.Fatalf("livekit.roomName = %#v, want room name", livekit["roomName"])
	}
	if livekit["agentIdentity"] != "agent-identity" {
		t.Fatalf("livekit.agentIdentity = %#v, want agent identity", livekit["agentIdentity"])
	}
}

func TestRunwayAvatarStartCreatesRealtimeSessionWhenLiveKitInfoIsPresent(t *testing.T) {
	var sawRequest bool
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawRequest = true
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.String() != "https://runway.test/v1/realtime_sessions" {
			t.Fatalf("URL = %s, want Runway realtime sessions endpoint", r.URL.String())
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll body error = %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		livekit := payload["livekit"].(map[string]any)
		if livekit["roomName"] != "support-room" {
			t.Fatalf("roomName = %#v, want support-room", livekit["roomName"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"id":"session-123"}`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() {
		http.DefaultClient = originalClient
	})

	avatar, err := NewRunwayAvatar("test-secret",
		WithRunwayAvatarID("avatar-123"),
		WithRunwayAPIURL("https://runway.test"),
	)
	if err != nil {
		t.Fatalf("NewRunwayAvatar error = %v", err)
	}
	ctx := agent.ContextWithAvatarStartInfo(context.Background(), agent.AvatarStartInfo{
		LiveKitURL:    "wss://livekit.example",
		LiveKitToken:  "livekit-token",
		RoomName:      "support-room",
		AgentIdentity: "agent-identity",
	})

	if err := avatar.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if avatar.realtimeSessionID != "session-123" {
		t.Fatalf("realtimeSessionID = %q, want session-123", avatar.realtimeSessionID)
	}
	if !avatar.started {
		t.Fatal("started = false, want true")
	}
	if !sawRequest {
		t.Fatal("Start did not create a Runway realtime session")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
