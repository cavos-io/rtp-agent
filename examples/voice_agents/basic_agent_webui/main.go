package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	basicagent "github.com/cavos-io/rtp-agent/examples/voice_agents/basic_agent/basicagent"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const defaultWebListenAddr = ":8003"

type webConfig struct {
	ListenAddr   string
	LiveKitURL   string
	APIKey       string
	APISecret    string
	AgentName    string
	UserIdentity string
	UserName     string
}

type createRoomResponse struct {
	RoomName string `json:"room_name"`
	URL      string `json:"url"`
	Token    string `json:"token"`
}

type liveKitWebService interface {
	CreateRoom(context.Context, *livekit.CreateRoomRequest) (*livekit.Room, error)
	CreateDispatch(context.Context, *livekit.CreateAgentDispatchRequest) (*livekit.AgentDispatch, error)
}

type liveKitWebClients struct {
	roomService     *lksdk.RoomServiceClient
	dispatchService *lksdk.AgentDispatchClient
}

func newLiveKitWebClients(cfg webConfig) *liveKitWebClients {
	return &liveKitWebClients{
		roomService:     lksdk.NewRoomServiceClient(cfg.LiveKitURL, cfg.APIKey, cfg.APISecret),
		dispatchService: lksdk.NewAgentDispatchServiceClient(cfg.LiveKitURL, cfg.APIKey, cfg.APISecret),
	}
}

func (c *liveKitWebClients) CreateRoom(ctx context.Context, req *livekit.CreateRoomRequest) (*livekit.Room, error) {
	return c.roomService.CreateRoom(ctx, req)
}

func (c *liveKitWebClients) CreateDispatch(ctx context.Context, req *livekit.CreateAgentDispatchRequest) (*livekit.AgentDispatch, error) {
	return c.dispatchService.CreateDispatch(ctx, req)
}

type webServer struct {
	cfg     webConfig
	service liveKitWebService
}

func webConfigFromEnv() webConfig {
	_ = basicagent.ConfigFromEnv()
	cfg := webConfig{
		ListenAddr:   getenvDefault("BASIC_AGENT_WEBUI_ADDR", defaultWebListenAddr),
		LiveKitURL:   os.Getenv("LIVEKIT_URL"),
		APIKey:       os.Getenv("LIVEKIT_API_KEY"),
		APISecret:    os.Getenv("LIVEKIT_API_SECRET"),
		AgentName:    getenvDefault("LIVEKIT_AGENT_NAME", "example-agent"),
		UserIdentity: getenvDefault("BASIC_AGENT_WEBUI_USER_IDENTITY", "web-user"),
		UserName:     getenvDefault("BASIC_AGENT_WEBUI_USER_NAME", "Test User"),
	}
	return cfg
}

func getenvDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func newWebServer(cfg webConfig, service liveKitWebService) *webServer {
	return &webServer{cfg: cfg, service: service}
}

func (s *webServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
	mux.HandleFunc("/create-room-and-dispatch", s.handleCreateRoomAndDispatch)
	return mux
}

func (s *webServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexTemplate.Execute(w, map[string]string{
		"AgentName": s.cfg.AgentName,
	})
}

func (s *webServer) handleFavicon(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *webServer) handleCreateRoomAndDispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.validateDispatchConfig(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	roomName := "web-test-" + randomHex(4)
	ctx := r.Context()
	if _, err := s.service.CreateRoom(ctx, &livekit.CreateRoomRequest{Name: roomName}); err != nil {
		http.Error(w, fmt.Sprintf("create room: %v", err), http.StatusBadGateway)
		return
	}
	if _, err := s.service.CreateDispatch(ctx, &livekit.CreateAgentDispatchRequest{
		Room:      roomName,
		AgentName: s.cfg.AgentName,
	}); err != nil {
		http.Error(w, fmt.Sprintf("dispatch agent: %v", err), http.StatusBadGateway)
		return
	}
	token, err := s.browserToken(roomName)
	if err != nil {
		http.Error(w, fmt.Sprintf("create browser token: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(createRoomResponse{
		RoomName: roomName,
		URL:      s.cfg.LiveKitURL,
		Token:    token,
	})
}

func (s *webServer) validateDispatchConfig() error {
	switch {
	case strings.TrimSpace(s.cfg.LiveKitURL) == "":
		return errors.New("LIVEKIT_URL is required")
	case strings.TrimSpace(s.cfg.APIKey) == "":
		return errors.New("LIVEKIT_API_KEY is required")
	case strings.TrimSpace(s.cfg.APISecret) == "":
		return errors.New("LIVEKIT_API_SECRET is required")
	case strings.TrimSpace(s.cfg.AgentName) == "":
		return errors.New("LIVEKIT_AGENT_NAME is required")
	default:
		return nil
	}
}

func (s *webServer) browserToken(roomName string) (string, error) {
	return auth.NewAccessToken(s.cfg.APIKey, s.cfg.APISecret).
		SetIdentity(s.cfg.UserIdentity).
		SetName(s.cfg.UserName).
		SetValidFor(time.Hour).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin:     true,
			Room:         roomName,
			CanPublish:   boolPtr(true),
			CanSubscribe: boolPtr(true),
		}).
		ToJWT()
}

func boolPtr(value bool) *bool {
	return &value
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func run(ctx context.Context, cfg webConfig) error {
	appCfg := basicagent.ConfigFromEnv()
	appCfg.WorkerOptions.AgentName = cfg.AgentName
	appCfg.WorkerOptions.WSURL = cfg.LiveKitURL
	appCfg.WorkerOptions.APIKey = cfg.APIKey
	appCfg.WorkerOptions.APISecret = cfg.APISecret
	appCfg.WorkerOptions.WorkerType = worker.WorkerTypeRoom

	rtpApp, err := basicagent.NewApp(appCfg)
	if err != nil {
		return err
	}
	defer rtpApp.Close(context.Background())

	workerDone := make(chan error, 1)
	go func() {
		err := rtpApp.Server.Run(ctx)
		if errors.Is(err, context.Canceled) {
			err = nil
		}
		workerDone <- err
	}()

	web := newWebServer(cfg, newLiveKitWebClients(cfg))
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           web.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	httpDone := make(chan error, 1)
	go func() {
		logger.Logger.Infow("Starting basic agent web UI", "addr", cfg.ListenAddr, "agentName", cfg.AgentName)
		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		httpDone <- err
	}()

	select {
	case <-ctx.Done():
	case err := <-workerDone:
		if err != nil {
			return err
		}
	case err := <-httpDone:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	_ = rtpApp.Server.Drain(shutdownCtx)
	return nil
}

func main() {
	cfg := webConfigFromEnv()
	fs := flag.NewFlagSet("basic_agent_webui", flag.ExitOnError)
	fs.StringVar(&cfg.ListenAddr, "addr", cfg.ListenAddr, "HTTP listen address")
	fs.StringVar(&cfg.LiveKitURL, "url", cfg.LiveKitURL, "LiveKit websocket URL")
	fs.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "LiveKit API key")
	fs.StringVar(&cfg.APISecret, "api-secret", cfg.APISecret, "LiveKit API secret")
	fs.StringVar(&cfg.AgentName, "agent-name", cfg.AgentName, "LiveKit agent name to dispatch")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: basic_agent_webui [options]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		logger.Logger.Errorw("Basic agent web UI failed", err)
		fmt.Fprintf(os.Stderr, "basic_agent_webui error: %v\n", err)
		os.Exit(1)
	}
}

var indexTemplate = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Basic Agent Web UI</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f7f8fa;
      --panel: #ffffff;
      --ink: #1f2933;
      --muted: #5d6b7a;
      --line: #d7dde4;
      --accent: #0f766e;
      --accent-dark: #115e59;
      --danger: #b42318;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: var(--bg);
      color: var(--ink);
    }
    main {
      max-width: 860px;
      margin: 0 auto;
      padding: 32px 20px;
    }
    header {
      border-bottom: 1px solid var(--line);
      padding-bottom: 18px;
      margin-bottom: 20px;
    }
    h1 {
      margin: 0 0 6px;
      font-size: 28px;
      line-height: 1.2;
      letter-spacing: 0;
    }
    p {
      margin: 0;
      color: var(--muted);
      line-height: 1.5;
    }
    section {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 18px;
      margin-top: 16px;
    }
    .controls {
      display: flex;
      gap: 10px;
      flex-wrap: wrap;
      align-items: center;
    }
    button {
      min-height: 40px;
      padding: 0 14px;
      border: 1px solid transparent;
      border-radius: 6px;
      font: inherit;
      font-weight: 600;
      cursor: pointer;
    }
    button.primary {
      background: var(--accent);
      color: white;
    }
    button.primary:hover { background: var(--accent-dark); }
    button.secondary {
      background: white;
      color: var(--ink);
      border-color: var(--line);
    }
    button:disabled {
      cursor: not-allowed;
      opacity: 0.55;
    }
    .status {
      margin-top: 14px;
      padding: 12px;
      min-height: 46px;
      border-radius: 6px;
      border: 1px solid var(--line);
      background: #fbfcfd;
      color: var(--ink);
    }
    .status.error {
      border-color: #f3b3ac;
      color: var(--danger);
      background: #fff6f5;
    }
    dl {
      display: grid;
      grid-template-columns: 120px 1fr;
      gap: 8px 12px;
      margin: 0;
    }
    dt {
      color: var(--muted);
    }
    dd {
      margin: 0;
      overflow-wrap: anywhere;
    }
    pre {
      margin: 0;
      min-height: 120px;
      white-space: pre-wrap;
      font-size: 13px;
      line-height: 1.5;
      color: #24313f;
    }
    @media (max-width: 560px) {
      main { padding: 20px 12px; }
      dl { grid-template-columns: 1fr; }
      button { width: 100%; }
    }
  </style>
</head>
<body>
  <main>
    <header>
      <h1>Basic Agent Web UI</h1>
      <p>Runs the Go basic agent worker and connects this browser to a dispatched LiveKit room.</p>
    </header>

    <section>
      <div class="controls">
        <button class="primary" id="connect" type="button">Start Test</button>
        <button class="secondary" id="disconnect" type="button" disabled>End</button>
      </div>
      <div class="status" id="status">Ready</div>
      <div id="audio-container"></div>
    </section>

    <section>
      <dl>
        <dt>Agent</dt>
        <dd>{{ .AgentName }}</dd>
        <dt>Room</dt>
        <dd id="room-name">Not connected</dd>
      </dl>
    </section>

    <section>
      <pre id="log"></pre>
    </section>
  </main>

  <script src="https://cdn.jsdelivr.net/npm/livekit-client/dist/livekit-client.umd.min.js"></script>
  <script>
    let room = null;
    const connectButton = document.getElementById('connect');
    const disconnectButton = document.getElementById('disconnect');
    const statusEl = document.getElementById('status');
    const roomNameEl = document.getElementById('room-name');
    const logEl = document.getElementById('log');
    const audioContainer = document.getElementById('audio-container');

    function log(message) {
      const timestamp = new Date().toLocaleTimeString();
      logEl.textContent += "[" + timestamp + "] " + message + "\n";
    }

    function setStatus(message, isError) {
      statusEl.textContent = message;
      statusEl.classList.toggle('error', Boolean(isError));
      log(message);
    }

    function participantLooksLikeAgent(participant) {
      const identity = participant.identity || "";
      return participant.kind === "agent" || identity.includes("agent") || identity.includes("worker");
    }

    async function connect() {
      try {
        setStatus('Creating room and dispatching agent...');
        connectButton.disabled = true;

        const response = await fetch('/create-room-and-dispatch', { method: 'POST' });
        if (!response.ok) {
          throw new Error(await response.text());
        }
        const data = await response.json();
        roomNameEl.textContent = data.room_name;

        room = new LivekitClient.Room();
        room.on('connected', async () => {
          disconnectButton.disabled = false;
          setStatus('Connected. Enabling microphone...');
          try {
            await room.localParticipant.setMicrophoneEnabled(true);
            setStatus('Microphone enabled. Waiting for agent audio...');
          } catch (error) {
            setStatus('Microphone access denied: ' + error.message, true);
          }
        });

        room.on('participantConnected', (participant) => {
          log('Participant connected: ' + participant.identity);
          if (participantLooksLikeAgent(participant)) {
            setStatus('Agent joined. Ready to talk.');
          }
        });

        room.on('trackSubscribed', (track, publication, participant) => {
          log('Track subscribed: ' + track.kind + ' from ' + participant.identity);
          if (track.kind === 'audio' && participantLooksLikeAgent(participant)) {
            const audioElement = track.attach();
            audioContainer.appendChild(audioElement);
            setStatus('Agent audio connected. Ready to talk.');
          }
        });

        room.on('disconnected', () => {
          setStatus('Disconnected');
          connectButton.disabled = false;
          disconnectButton.disabled = true;
        });

        await room.connect(data.url, data.token);
      } catch (error) {
        setStatus('Failed: ' + error.message.trim(), true);
        connectButton.disabled = false;
        disconnectButton.disabled = true;
      }
    }

    async function disconnect() {
      if (room) {
        await room.disconnect();
        room = null;
      }
      audioContainer.innerHTML = '';
      roomNameEl.textContent = 'Not connected';
      setStatus('Disconnected');
      connectButton.disabled = false;
      disconnectButton.disabled = true;
    }

    connectButton.addEventListener('click', connect);
    disconnectButton.addEventListener('click', disconnect);
  </script>
</body>
</html>`))
