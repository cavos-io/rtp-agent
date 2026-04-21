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
	LoadFn              func(*AgentServer) float64
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
	isDraining bool

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

// GetEntrypointFunc retrieves the registered entrypoint function (for console mode)
func (s *AgentServer) GetEntrypointFunc() func(*JobContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entrypointFnc
}

func (s *AgentServer) NumActiveJobs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.activeJobs)
}

func (s *AgentServer) currentLoad() float32 {
	if s.Options.LoadFn == nil {
		return 0.0
	}

	load := s.Options.LoadFn(s)
	if load < 0 {
		return 0.0
	}
	if load > 1.0 {
		return 1.0
	}
	return float32(load)
}

func (s *AgentServer) sendAvailabilityUpdate() error {
	return s.sendLoadUpdate(s.currentLoad(), false)
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

	// Reconnect loop — if the worker WS drops mid-session, reconnect
	// so the agent stays visible in the LiveKit panel.
	maxRetries := 10
	retryDelay := 2 * time.Second

	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := s.runOnce(ctx, wsURL.String())
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt >= maxRetries {
			return fmt.Errorf("worker WS reconnect failed after %d attempts: %w", maxRetries, err)
		}

		logger.Logger.Warnw("Worker WS disconnected, reconnecting...", err,
			"attempt", attempt+1,
			"retry_in", retryDelay.String(),
		)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}

		// Exponential backoff capped at 30s
		retryDelay = retryDelay * 2
		if retryDelay > 30*time.Second {
			retryDelay = 30 * time.Second
		}
	}
}

func (s *AgentServer) runOnce(ctx context.Context, wsURL string) error {
	// Create JWT token (fresh on each connect/reconnect)
	at := auth.NewAccessToken(s.Options.APIKey, s.Options.APISecret)
	grant := &auth.VideoGrant{Agent: true}
	at.AddGrant(grant).SetValidFor(time.Hour)
	token, err := at.ToJWT()
	if err != nil {
		return err
	}

	logger.Logger.Infow("Dialing WebSocket", "url", wsURL)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, map[string][]string{
		"Authorization": {fmt.Sprintf("Bearer %s", token)},
	})
	if err != nil {
		logger.Logger.Errorw("WebSocket connection failed", err)
		return fmt.Errorf("failed to connect to LiveKit %s: %w", wsURL, err)
	}

	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	defer func() {
		conn.Close()
		s.mu.Lock()
		s.conn = nil
		s.mu.Unlock()
	}()

	// When ctx is cancelled (e.g. Ctrl+C), close the WebSocket so the
	// blocking conn.ReadMessage() call below returns immediately.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	logger.Logger.Infow("Connected to LiveKit Server", "url", wsURL)

	// Send Register request.
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
					logger.Logger.Errorw("Ping marshal error", err)
					return
				}
				s.mu.Lock()
				writeErr := conn.WriteMessage(websocket.BinaryMessage, pb)
				s.mu.Unlock()
				if writeErr != nil {
					// Don't log as error — reconnect loop will handle it
					return
				}
			}
		}
	}()

	// Message loop
	type wsMsg struct {
		msgType int
		data    []byte
		err     error
	}
	msgCh := make(chan wsMsg, 1)

	go func() {
		for {
			mt, data, err := conn.ReadMessage()
			msgCh <- wsMsg{mt, data, err}
			if err != nil {
				return
			}
		}
	}()

	// Re-send JS_RUNNING for any active jobs so LiveKit knows they're still alive
	s.mu.Lock()
	activeJobIDs := make([]string, 0, len(s.activeJobs))
	for id := range s.activeJobs {
		activeJobIDs = append(activeJobIDs, id)
	}
	s.mu.Unlock()
	for _, jobID := range activeJobIDs {
		if err := s.sendJobStatus(jobID, livekit.JobStatus_JS_RUNNING, ""); err != nil {
			logger.Logger.Warnw("Failed to re-send JS_RUNNING on reconnect", err, "jobId", jobID)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-msgCh:
			if result.err != nil {
				return result.err // triggers reconnect
			}
			if result.msgType != websocket.BinaryMessage {
				continue
			}
			msg := &livekit.ServerMessage{}
			if err := proto.Unmarshal(result.data, msg); err != nil {
				logger.Logger.Errorw("Failed to unmarshal server message", err)
				continue
			}
			s.handleMessage(ctx, msg)
		}
	}
}

// Drain stops the worker from accepting new jobs and waits for existing ones to finish.
func (s *AgentServer) Drain(ctx context.Context) error {
	s.mu.Lock()
	if s.isDraining {
		s.mu.Unlock()
		return nil
	}
	s.isDraining = true
	activeCount := len(s.activeJobs)
	s.mu.Unlock()

	logger.Logger.Infow("Draining agent server", "active_jobs", activeCount)

	// Notify LiveKit that we are draining
	s.sendLoadUpdate(1.0, true)

	if activeCount == 0 {
		return nil
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.mu.Lock()
			count := len(s.activeJobs)
			s.mu.Unlock()

			if count == 0 {
				logger.Logger.Infow("All jobs finished, drain complete")
				return nil
			}
			logger.Logger.Infow("Waiting for jobs to finish", "pending_jobs", count)
		}
	}
}

func (s *AgentServer) sendLoadUpdate(load float32, draining bool) error {
	status := livekit.WorkerStatus_WS_AVAILABLE
	// Note: WS_DRAINING is not supported in the current protocol version.
	// We handle draining internally by rejecting new jobs.

	s.mu.Lock()
	jobCount := uint32(len(s.activeJobs))
	conn := s.conn
	s.mu.Unlock()

	if conn == nil {
		return nil
	}

	update := &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_UpdateWorker{
			UpdateWorker: &livekit.UpdateWorkerStatus{
				Status:   &status,
				Load:     load,
				JobCount: jobCount,
			},
		},
	}

	b, err := proto.Marshal(update)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return conn.WriteMessage(websocket.BinaryMessage, b)
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
	logger.Logger.Infow("Received server message", "type", fmt.Sprintf("%T", msg.Message))

	switch m := msg.Message.(type) {
	case *livekit.ServerMessage_Register:
		logger.Logger.Infow("Worker Registered", "workerId", m.Register.WorkerId)
		logger.Logger.Infow("Worker Registered", "workerId", m.Register.WorkerId, "serverInfo", m.Register.ServerInfo)
		if err := s.sendAvailabilityUpdate(); err != nil {
			logger.Logger.Errorw("Failed to send available status", err)
		} else {
			logger.Logger.Infow("Worker status set to AVAILABLE — waiting for jobs...")
		}
	case *livekit.ServerMessage_Availability:
		logger.Logger.Infow("Received availability request", "jobId", m.Availability.Job.Id)
		s.handleAvailability(ctx, m.Availability)
	case *livekit.ServerMessage_Assignment:
		logger.Logger.Infow("Received job assignment", "jobId", m.Assignment.Job.Id)
		s.handleAssignment(ctx, m.Assignment)
	case *livekit.ServerMessage_Termination:
		logger.Logger.Infow("Received job termination", "jobId", m.Termination.JobId)
		s.handleTermination(m.Termination)
	case *livekit.ServerMessage_Pong:
		logger.Logger.Infow("Received WorkerPong", "timestamp", m.Pong.Timestamp)
	default:
		logger.Logger.Warnw("Unhandled message type received", nil, "type", fmt.Sprintf("%T", msg.Message))
	}
}

func (s *AgentServer) handleAvailability(ctx context.Context, req *livekit.AvailabilityRequest) {
	logger.Logger.Infow("Received availability request", "jobId", req.Job.Id)
	logger.Logger.Infow("Received availability request", "jobId", req.Job.Id)

	s.mu.Lock()
	draining := s.isDraining
	s.mu.Unlock()

	// Default to accept unless draining.
	ans := &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_Availability{
			Availability: &livekit.AvailabilityResponse{
				JobId:               req.Job.Id,
				Available:           !draining,
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

func (s *AgentServer) sendJobStatus(jobID string, status livekit.JobStatus, errMsg string) error {
	update := &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_UpdateJob{
			UpdateJob: &livekit.UpdateJobStatus{
				JobId:  jobID,
				Status: status,
				Error:  errMsg,
			},
		},
	}
	b, err := proto.Marshal(update)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, b)
}

func (s *AgentServer) handleAssignment(ctx context.Context, req *livekit.JobAssignment) {
	logger.Logger.Infow("Received job assignment", "jobId", req.Job.Id, "room", req.Job.Room.Name)

	// Spin up a job context here
	jobCtx := NewJobContext(req.Job, s.Options.WSRL, s.Options.APIKey, s.Options.APISecret)

	s.mu.Lock()
	s.activeJobs[req.Job.Id] = jobCtx
	s.mu.Unlock()
	if err := s.sendAvailabilityUpdate(); err != nil {
		logger.Logger.Warnw("Failed to send load update after job assignment", err)
	}

	// Tell LiveKit the job is running — keeps agent visible in the panel
	if err := s.sendJobStatus(req.Job.Id, livekit.JobStatus_JS_RUNNING, ""); err != nil {
		fmt.Printf("❌ [PANEL] Failed to send JS_RUNNING: %v (jobId=%s)\n", err, req.Job.Id)
	} else {
		fmt.Printf("✅ [PANEL] Sent JS_RUNNING — agent should appear in panel (jobId=%s)\n", req.Job.Id)
	}

	if s.entrypointFnc != nil {
		go func() {
			err := s.entrypointFnc(jobCtx)
			if err != nil {
				fmt.Printf("❌ [PANEL] Job failed — sending JS_FAILED — agent will disappear (jobId=%s, err=%v)\n", req.Job.Id, err)
				_ = s.sendJobStatus(req.Job.Id, livekit.JobStatus_JS_FAILED, err.Error())
			} else {
				fmt.Printf("⚠️  [PANEL] Job returned nil — sending JS_SUCCESS — agent will disappear (jobId=%s)\n", req.Job.Id)
				_ = s.sendJobStatus(req.Job.Id, livekit.JobStatus_JS_SUCCESS, "")
			}

			// Job done — clean up activeJobs to free memory
			s.mu.Lock()
			delete(s.activeJobs, req.Job.Id)
			s.mu.Unlock()
			fmt.Printf("   🧹 [PANEL] Job cleaned up: %s\n", req.Job.Id)

			// Signal back to AVAILABLE so the next dispatch can be received
			if err := s.sendAvailabilityUpdate(); err != nil {
				logger.Logger.Warnw("Failed to send available after job", err)
			}
		}()
	}
}

func (s *AgentServer) handleTermination(req *livekit.JobTermination) {
	fmt.Printf("⚠️  [PANEL] Server sent TERMINATION — agent will disappear (jobId=%s, raw=%+v)\n", req.JobId, req)
	logger.Logger.Infow("Received job termination", "jobId", req.JobId, "raw", fmt.Sprintf("%+v", req))

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

		if err := s.sendAvailabilityUpdate(); err != nil {
			logger.Logger.Warnw("Failed to send load update after termination", err)
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
	_ = s.sendAvailabilityUpdate()

	if s.entrypointFnc != nil {
		go func() {
			if err := s.entrypointFnc(jobCtx); err != nil {
				logger.Logger.Errorw("Local job entrypoint failed", err, "jobId", job.Id)
			}
			s.mu.Lock()
			delete(s.activeJobs, job.Id)
			s.mu.Unlock()
			_ = s.sendAvailabilityUpdate()
		}()
	}

	// Block until context is done for local execution
	<-ctx.Done()
	return nil
}
