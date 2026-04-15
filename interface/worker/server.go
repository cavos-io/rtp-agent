package worker

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/gorilla/websocket"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

type WorkerType string

const (
	WorkerTypeRoom      WorkerType = "room"
	WorkerTypePublisher WorkerType = "publisher"
)

type WorkerOptions struct {
	AgentName           string
	WorkerType          WorkerType
	MaxRetry            int
	WSRL                string
	APIKey              string
	APISecret           string
	HTTPProxy           string
	JobMemoryWarnMB     float64
	JobMemoryLimitMB    float64
	NumIdleProcesses    int
	DrainTimeoutSeconds int
}

type AgentServer struct {
	Options WorkerOptions

	entrypointFnc func(*JobContext) error
	requestFnc    func(*JobRequest) error
	sessionEndFnc func(*JobContext) error

	activeJobs map[string]*JobContext
	mu         sync.Mutex
	conn       *websocket.Conn

	consoleSession any // Store local session for CLI console
}

func NewAgentServer(opts WorkerOptions) *AgentServer {
	return &AgentServer{
		Options:    opts,
		activeJobs: make(map[string]*JobContext),
	}
}

func (s *AgentServer) RTCSession(
	entrypoint func(*JobContext) error,
	request func(*JobRequest) error,
	sessionEnd func(*JobContext) error,
) {
	s.entrypointFnc = entrypoint
	s.requestFnc = request
	s.sessionEndFnc = sessionEnd
}

// SetConsoleSession allows entrypoints to register their session for console interaction
func (s *AgentServer) SetConsoleSession(session any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consoleSession = session
}

// GetConsoleSession retrieves the active local console session
func (s *AgentServer) GetConsoleSession() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consoleSession
}

func (s *AgentServer) Run(ctx context.Context) error {
	if s.Options.WSRL == "" || s.Options.APIKey == "" || s.Options.APISecret == "" {
		return fmt.Errorf("missing LiveKit credentials")
	}

	wsURL, err := url.Parse(s.Options.WSRL)
	if err != nil {
		return err
	}

	if wsURL.Scheme == "http" {
		wsURL.Scheme = "ws"
	} else if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	}
	wsURL.Path = "/agent"

	// Create JWT token
	at := auth.NewAccessToken(s.Options.APIKey, s.Options.APISecret)
	grant := &auth.VideoGrant{Agent: true}
	at.AddGrant(grant).SetValidFor(time.Hour)
	token, err := at.ToJWT()
	if err != nil {
		return err
	}

	// Connect WS
	// A robust implementation should include retries and proxy handling
	fmt.Printf("   Dialing WebSocket: %s\n", wsURL.String())
	conn, res, err := websocket.DefaultDialer.DialContext(ctx, wsURL.String(), map[string][]string{
		"Authorization": {fmt.Sprintf("Bearer %s", token)},
	})
	if err != nil {
		fmt.Printf("   ❌ WebSocket connection failed: %v\n", err)
		return fmt.Errorf("failed to connect to LiveKit %s: %w", wsURL.String(), err)
	}
	_ = res
	s.conn = conn
	defer conn.Close()

	// When ctx is cancelled (e.g. Ctrl+C), close the WebSocket so the
	// blocking conn.ReadMessage() call below returns immediately.
	go func() {
		<-ctx.Done()
		fmt.Println("   🛑 Shutting down worker...")
		conn.Close()
	}()

	fmt.Println("   ✅ Connected to LiveKit Server!")
	fmt.Println("   Registering worker...")
	logger.Logger.Infow("Connected to LiveKit Server", "url", s.Options.WSRL)

	// Send Register request.
	// PingInterval tells the server how often (in seconds) we will send
	// application-level WorkerPing messages. Without this the server considers
	// the worker unhealthy and never dispatches jobs.
	pingIntervalSec := uint32(5)
	ns := "" // empty = default namespace
	req := &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_Register{
			Register: &livekit.RegisterWorkerRequest{
				Type:         livekit.JobType_JT_ROOM,
				AgentName:    s.Options.AgentName,
				Version:      "1.0.0",
				PingInterval: pingIntervalSec,
				Namespace:    &ns,
				AllowedPermissions: &livekit.ParticipantPermission{
					CanPublish:     true,
					CanSubscribe:   true,
					CanPublishData: true,
					Agent:          true,
				},
			},
		},
	}

	b, err := proto.Marshal(req)
	if err != nil {
		return err
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
		return err
	}

	// Application-level ping loop.
	// LiveKit agent protocol uses protobuf WorkerPing messages (NOT WebSocket
	// control-frame pings). The server echoes back a WorkerPong. Without these
	// pings the server marks the worker as dead and stops sending jobs.
	go func() {
		ticker := time.NewTicker(time.Duration(pingIntervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ping := &livekit.WorkerMessage{
					Message: &livekit.WorkerMessage_Ping{
						Ping: &livekit.WorkerPing{
							Timestamp: time.Now().UnixMilli(),
						},
					},
				}
				pb, err := proto.Marshal(ping)
				if err != nil {
					fmt.Printf("   ❌ Ping marshal error: %v\n", err)
					return
				}
				s.mu.Lock()
				err = conn.WriteMessage(websocket.BinaryMessage, pb)
				s.mu.Unlock()
				if err != nil {
					fmt.Printf("   ❌ Ping send error: %v\n", err)
					return
				}
				fmt.Printf("   🏓 WorkerPing sent (ts=%d)\n", time.Now().UnixMilli())
			}
		}
	}()

	// Message Loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				fmt.Printf("   ❌ WebSocket read error: %v\n", err)
				return err
			}

			fmt.Printf("   📨 WS message received: type=%d len=%d\n", msgType, len(data))

			if msgType != websocket.BinaryMessage {
				continue
			}

			msg := &livekit.ServerMessage{}
			if err := proto.Unmarshal(data, msg); err != nil {
				logger.Logger.Errorw("Failed to unmarshal server message", err)
				continue
			}

			fmt.Printf("   📬 Parsed message type: %T\n", msg.Message)
			s.handleMessage(ctx, msg)
		}
	}
}

// sendAvailable broadcasts UpdateWorkerStatus(WS_AVAILABLE) so the LiveKit
// server knows this worker is ready to receive job dispatches.
func (s *AgentServer) sendAvailable() error {
	status := livekit.WorkerStatus_WS_AVAILABLE
	update := &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_UpdateWorker{
			UpdateWorker: &livekit.UpdateWorkerStatus{
				Status:   &status,
				Load:     0.0,
				JobCount: 0,
			},
		},
	}
	b, err := proto.Marshal(update)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, b)
}

func (s *AgentServer) handleMessage(ctx context.Context, msg *livekit.ServerMessage) {
	switch m := msg.Message.(type) {
	case *livekit.ServerMessage_Register:
		fmt.Printf("   ✅ Worker Registered! ID: %s\n", m.Register.WorkerId)
		logger.Logger.Infow("Worker Registered", "workerId", m.Register.WorkerId, "serverInfo", m.Register.ServerInfo)
		// Signal to the server that this worker is ready to accept jobs.
		// Without this UpdateWorkerStatus the server keeps the worker in an
		// "initializing" state and will never assign dispatches to it.
		if err := s.sendAvailable(); err != nil {
			fmt.Printf("   ❌ Failed to send available status: %v\n", err)
		} else {
			fmt.Println("   ✅ Worker status set to AVAILABLE — waiting for jobs...")
		}
	case *livekit.ServerMessage_Availability:
		s.handleAvailability(ctx, m.Availability)
	case *livekit.ServerMessage_Assignment:
		s.handleAssignment(ctx, m.Assignment)
	case *livekit.ServerMessage_Termination:
		s.handleTermination(m.Termination)
	case *livekit.ServerMessage_Pong:
		// Application-level keepalive response — worker is confirmed alive
		fmt.Printf("   🏓 WorkerPong received (lastTs=%d serverTs=%d)\n",
			m.Pong.LastTimestamp, m.Pong.Timestamp)
	default:
		fmt.Printf("   ⚠️ Unhandled server message type: %T\n", msg.Message)
		logger.Logger.Warnw("Unhandled message type received", nil, "type", fmt.Sprintf("%T", msg.Message))
	}
}

func (s *AgentServer) handleAvailability(ctx context.Context, req *livekit.AvailabilityRequest) {
	fmt.Printf("   📥 Received availability request: job=%s\n", req.Job.Id)
	logger.Logger.Infow("Received availability request", "jobId", req.Job.Id)

	// Default to accept
	ans := &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_Availability{
			Availability: &livekit.AvailabilityResponse{
				JobId:               req.Job.Id,
				Available:           true,
				ParticipantIdentity: "agent-" + req.Job.Id[:8],
				ParticipantName:     s.Options.AgentName,
			},
		},
	}

	b, err := proto.Marshal(ans)
	if err != nil {
		logger.Logger.Errorw("failed to marshal availability", err)
		return
	}

	s.mu.Lock()
	err = s.conn.WriteMessage(websocket.BinaryMessage, b)
	s.mu.Unlock()
	if err != nil {
		logger.Logger.Errorw("failed to send availability", err)
	}
}

func (s *AgentServer) handleAssignment(ctx context.Context, req *livekit.JobAssignment) {
	fmt.Printf("   🚀 Received job assignment: job=%s room=%s\n", req.Job.Id, req.Job.Room.Name)
	logger.Logger.Infow("Received job assignment", "jobId", req.Job.Id)

	// Spin up a job context here
	jobCtx := NewJobContext(req.Job, s.Options.WSRL, s.Options.APIKey, s.Options.APISecret)

	s.mu.Lock()
	s.activeJobs[req.Job.Id] = jobCtx
	s.mu.Unlock()

	if s.entrypointFnc != nil {
		go func() {
			if err := s.entrypointFnc(jobCtx); err != nil {
				logger.Logger.Errorw("Job entrypoint failed", err, "jobId", req.Job.Id)
			}
			// Job done — clean up activeJobs to free memory
			s.mu.Lock()
			delete(s.activeJobs, req.Job.Id)
			s.mu.Unlock()
			fmt.Printf("   🧹 Job cleaned up: %s\n", req.Job.Id)

			// Signal back to AVAILABLE so the next dispatch can be received
			if err := s.sendAvailable(); err != nil {
				logger.Logger.Warnw("Failed to send available after job", err)
			}
		}()
	}
}

func (s *AgentServer) handleTermination(req *livekit.JobTermination) {
	logger.Logger.Infow("Received job termination", "jobId", req.JobId)

	s.mu.Lock()
	jobCtx, exists := s.activeJobs[req.JobId]
	if exists {
		delete(s.activeJobs, req.JobId)
	}
	s.mu.Unlock()

	if exists {
		if s.sessionEndFnc != nil {
			s.sessionEndFnc(jobCtx)
		}

		if jobCtx.Report != nil {
			go func() {
				err := agent.UploadSessionReport(
					s.Options.WSRL,
					s.Options.APIKey,
					s.Options.APISecret,
					s.Options.AgentName,
					jobCtx.Report,
				)
				if err != nil {
					logger.Logger.Errorw("failed to upload session report", err, "jobId", req.JobId)
				}
			}()
		}
	}
}

// ExecuteLocalJob runs a job locally without connecting to the worker service, useful for the CLI console
func (s *AgentServer) ExecuteLocalJob(ctx context.Context, roomName string, participantIdentity string) error {
	job := &livekit.Job{
		Id: "local-job-" + time.Now().Format("20060102150405"),
		Room: &livekit.Room{
			Name: roomName,
			Sid:  "RM_local",
		},
		Type: livekit.JobType_JT_ROOM,
	}

	jobCtx := NewJobContext(job, s.Options.WSRL, s.Options.APIKey, s.Options.APISecret)

	// For local execution, we want to connect immediately
	// For basic parity, we just trigger the entrypoint directly.

	s.mu.Lock()
	s.activeJobs[job.Id] = jobCtx
	s.mu.Unlock()

	if s.entrypointFnc != nil {
		go func() {
			if err := s.entrypointFnc(jobCtx); err != nil {
				logger.Logger.Errorw("Local job entrypoint failed", err, "jobId", job.Id)
			}
		}()
	}

	// Block until context is done for local execution
	<-ctx.Done()
	return nil
}
