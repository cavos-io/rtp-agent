package did

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestDIDPluginMetadataMatchesReference(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.did" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.did", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("plugin version = %q, want 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.did" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.did", PluginPackage)
	}
}

func TestNewDIDAvatarUsesReferenceDefaults(t *testing.T) {
	t.Setenv(didAPIKeyEnv, "env-key")

	avatar := NewDIDAvatar("agent-123", "explicit-key")

	if avatar.agentID != "agent-123" {
		t.Fatalf("agentID = %q, want explicit agent id", avatar.agentID)
	}
	if avatar.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", avatar.apiKey)
	}
	if avatar.apiURL != "https://api.d-id.com" {
		t.Fatalf("apiURL = %q, want reference API URL", avatar.apiURL)
	}
	if avatar.audioConfig.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want reference default", avatar.audioConfig.SampleRate)
	}
	if avatar.Provider() != "d-id" {
		t.Fatalf("Provider() = %q, want d-id", avatar.Provider())
	}
	if avatar.AvatarIdentity() != "d-id-avatar-agent" {
		t.Fatalf("AvatarIdentity() = %q, want reference identity", avatar.AvatarIdentity())
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestNewDIDAvatarUsesEnvironmentConfig(t *testing.T) {
	t.Setenv(didAPIKeyEnv, "env-key")
	t.Setenv(didAPIURLEnv, "https://did.example")
	t.Setenv(didAgentIDEnv, "env-agent")

	avatar := NewDIDAvatar("", "")

	if avatar.agentID != "env-agent" {
		t.Fatalf("agentID = %q, want env agent id", avatar.agentID)
	}
	if avatar.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", avatar.apiKey)
	}
	if avatar.apiURL != "https://did.example" {
		t.Fatalf("apiURL = %q, want env API URL", avatar.apiURL)
	}
}

func TestBuildDIDJoinSessionRequestMatchesReferencePayload(t *testing.T) {
	avatar := NewDIDAvatar("agent-123", "test-key")

	endpoint, headers, body, err := buildDIDJoinSessionRequest(avatar, agent.AvatarStartInfo{
		LiveKitURL:   "wss://livekit.example",
		LiveKitToken: "livekit-token",
		RoomName:     "support-room",
	})
	if err != nil {
		t.Fatalf("buildDIDJoinSessionRequest error = %v", err)
	}
	if endpoint != "/v2/agents/agent-123/sessions/join" {
		t.Fatalf("endpoint = %q, want reference join endpoint", endpoint)
	}
	if headers["Authorization"] != "Basic test-key" {
		t.Fatalf("Authorization = %q, want Basic test-key", headers["Authorization"])
	}
	if headers["Content-Type"] != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", headers["Content-Type"])
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	transport := payload["transport"].(map[string]any)
	if transport["provider"] != "livekit" {
		t.Fatalf("transport.provider = %#v, want livekit", transport["provider"])
	}
	if transport["server_url"] != "wss://livekit.example" {
		t.Fatalf("transport.server_url = %#v, want LiveKit URL", transport["server_url"])
	}
	if transport["token"] != "livekit-token" {
		t.Fatalf("transport.token = %#v, want LiveKit token", transport["token"])
	}
	if transport["room_name"] != "support-room" {
		t.Fatalf("transport.room_name = %#v, want room name", transport["room_name"])
	}
	audioConfig := payload["audio_config"].(map[string]any)
	if audioConfig["sample_rate"] != float64(24000) {
		t.Fatalf("audio_config.sample_rate = %#v, want 24000", audioConfig["sample_rate"])
	}
}

func TestDIDAvatarStartRequiresAPIKeyAndAgentID(t *testing.T) {
	t.Setenv(didAPIKeyEnv, "")
	t.Setenv(didAgentIDEnv, "")

	avatar := NewDIDAvatar("", "")
	err := avatar.Start(context.Background())

	if err == nil || !strings.Contains(err.Error(), "DID_API_KEY") {
		t.Fatalf("Start error = %v, want missing API key error", err)
	}

	avatar = NewDIDAvatar("", "test-key")
	err = avatar.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "DID_AGENT_ID") {
		t.Fatalf("Start error = %v, want missing agent ID error", err)
	}
}

func TestDIDAvatarStartCreatesJoinSessionWhenLiveKitInfoIsPresent(t *testing.T) {
	var sawRequest bool
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawRequest = true
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.String() != "https://did.test/v2/agents/agent-123/sessions/join" {
			t.Fatalf("URL = %s, want D-ID join endpoint", r.URL.String())
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll body error = %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		transport := payload["transport"].(map[string]any)
		if transport["room_name"] != "support-room" {
			t.Fatalf("room_name = %#v, want support-room", transport["room_name"])
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
	t.Setenv(didAPIURLEnv, "https://did.test")

	avatar := NewDIDAvatar("agent-123", "test-key")
	ctx := agent.ContextWithAvatarStartInfo(context.Background(), agent.AvatarStartInfo{
		LiveKitURL:   "wss://livekit.example",
		LiveKitToken: "livekit-token",
		RoomName:     "support-room",
	})

	if err := avatar.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if avatar.sessionID != "session-123" {
		t.Fatalf("sessionID = %q, want session-123", avatar.sessionID)
	}
	if !avatar.started {
		t.Fatal("started = false, want true")
	}
	if !sawRequest {
		t.Fatal("Start did not create a D-ID join session")
	}
}

func TestDIDAvatarUpdateStateRecordsLatestState(t *testing.T) {
	avatar := NewDIDAvatar("agent-123", "test-key")

	if err := avatar.UpdateState(agent.AvatarStateSpeaking); err != nil {
		t.Fatalf("UpdateState error = %v", err)
	}
	if avatar.state != agent.AvatarStateSpeaking {
		t.Fatalf("state = %q, want speaking", avatar.state)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
