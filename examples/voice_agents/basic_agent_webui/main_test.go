package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/livekit/protocol/livekit"
)

func TestWebConfigDefaultsMatchBasicAgentExample(t *testing.T) {
	t.Setenv("LIVEKIT_URL", "wss://livekit.example")
	t.Setenv("LIVEKIT_API_KEY", "api-key")
	t.Setenv("LIVEKIT_API_SECRET", "api-secret")

	cfg := webConfigFromEnv()

	if cfg.ListenAddr != "127.0.0.1:8003" {
		t.Fatalf("ListenAddr = %q, want loopback-only default", cfg.ListenAddr)
	}
	if cfg.AgentName != "example-agent" {
		t.Fatalf("AgentName = %q, want default basic agent name", cfg.AgentName)
	}
	if cfg.LiveKitURL != "wss://livekit.example" {
		t.Fatalf("LiveKitURL = %q, want environment value", cfg.LiveKitURL)
	}
}

func TestCreateRoomAndDispatchCreatesRoomDispatchesAgentAndReturnsToken(t *testing.T) {
	service := &fakeLiveKitWebService{}
	server := newWebServer(webConfig{
		LiveKitURL:   "wss://livekit.example",
		APIKey:       "api-key",
		APISecret:    "api-secret",
		AgentName:    "kelly-agent",
		UserIdentity: "browser-user",
		UserName:     "Browser User",
	}, service)

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8003/create-room-and-dispatch", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8003")
	rec := httptest.NewRecorder()

	server.handleCreateRoomAndDispatch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got createRoomResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response JSON error = %v; body=%s", err, rec.Body.String())
	}
	if !strings.HasPrefix(got.RoomName, "web-test-") {
		t.Fatalf("RoomName = %q, want web-test prefix", got.RoomName)
	}
	if got.URL != "wss://livekit.example" {
		t.Fatalf("URL = %q, want LiveKit URL", got.URL)
	}
	if got.Token == "" {
		t.Fatal("Token is empty")
	}
	if len(service.createdRooms) != 1 || service.createdRooms[0] != got.RoomName {
		t.Fatalf("createdRooms = %#v, want response room", service.createdRooms)
	}
	if len(service.dispatches) != 1 {
		t.Fatalf("dispatch count = %d, want 1", len(service.dispatches))
	}
	if service.dispatches[0].Room != got.RoomName {
		t.Fatalf("dispatch room = %q, want response room", service.dispatches[0].Room)
	}
	if service.dispatches[0].AgentName != "kelly-agent" {
		t.Fatalf("dispatch agent = %q, want kelly-agent", service.dispatches[0].AgentName)
	}
}

func TestCreateRoomAndDispatchRejectsMissingCredentials(t *testing.T) {
	server := newWebServer(webConfig{
		LiveKitURL: "wss://livekit.example",
		APIKey:     "api-key",
		AgentName:  "kelly-agent",
	}, &fakeLiveKitWebService{})

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8003/create-room-and-dispatch", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8003")
	rec := httptest.NewRecorder()

	server.handleCreateRoomAndDispatch(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "LIVEKIT_API_SECRET") {
		t.Fatalf("body = %q, want missing secret message", rec.Body.String())
	}
}

func TestCreateRoomAndDispatchRejectsCrossOriginRequest(t *testing.T) {
	service := &fakeLiveKitWebService{}
	server := newWebServer(webConfig{}, service)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8003/create-room-and-dispatch", nil)
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()

	server.handleCreateRoomAndDispatch(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(service.createdRooms) != 0 {
		t.Fatalf("createdRooms = %#v, want no side effects", service.createdRooms)
	}
}

func TestValidateWebListenAddrRequiresExplicitRemoteOptIn(t *testing.T) {
	if err := validateWebListenAddr("0.0.0.0:8003", false); err == nil {
		t.Fatal("validateWebListenAddr() error = nil, want remote binding rejection")
	}
	if err := validateWebListenAddr("0.0.0.0:8003", true); err != nil {
		t.Fatalf("validateWebListenAddr() with opt-in error = %v", err)
	}
	if err := validateWebListenAddr("127.0.0.1:8003", false); err != nil {
		t.Fatalf("validateWebListenAddr() loopback error = %v", err)
	}
}

func TestIndexServesLiveKitBrowserClientAndControls(t *testing.T) {
	server := newWebServer(webConfig{}, &fakeLiveKitWebService{})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	server.handleIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"livekit-client@",
		"integrity=\"sha384-",
		"Start Test",
		"/create-room-and-dispatch",
		"setMicrophoneEnabled(true)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("index body missing %q", want)
		}
	}
}

func TestMainGoFileShowsHelpStandalone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", "main.go", "--help")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("go run main.go --help timed out\n%s", out)
	}
	if err != nil {
		t.Fatalf("go run main.go --help error = %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Usage: basic_agent_webui") {
		t.Fatalf("help output = %q, want webui usage", string(out))
	}
}

type fakeLiveKitWebService struct {
	createdRooms []string
	dispatches   []*livekit.CreateAgentDispatchRequest
}

func (f *fakeLiveKitWebService) CreateRoom(_ context.Context, req *livekit.CreateRoomRequest) (*livekit.Room, error) {
	f.createdRooms = append(f.createdRooms, req.Name)
	return &livekit.Room{Name: req.Name}, nil
}

func (f *fakeLiveKitWebService) CreateDispatch(_ context.Context, req *livekit.CreateAgentDispatchRequest) (*livekit.AgentDispatch, error) {
	f.dispatches = append(f.dispatches, req)
	return &livekit.AgentDispatch{Room: req.Room, AgentName: req.AgentName}, nil
}
