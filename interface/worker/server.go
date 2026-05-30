package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/library/logger"
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
	AgentName  string
	WorkerType WorkerType
	MaxRetry   int
	WSURL      string
	// WSRL is kept for backward compatibility. Prefer WSURL for new code.
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
	opts = resolveWorkerOptions(opts)
	return &AgentServer{
		Options:    opts,
		activeJobs: make(map[string]*JobContext),
	}
}

func resolveWorkerOptions(opts WorkerOptions) WorkerOptions {
	if opts.WorkerType == "" {
		opts.WorkerType = WorkerTypeRoom
	}
	if opts.WSURL == "" {
		opts.WSURL = opts.WSRL
	}
	if opts.WSURL == "" {
		opts.WSURL = os.Getenv("LIVEKIT_URL")
	}
	opts.WSRL = opts.WSURL

	if opts.APIKey == "" {
		opts.APIKey = os.Getenv("LIVEKIT_API_KEY")
	}
	if opts.APISecret == "" {
		opts.APISecret = os.Getenv("LIVEKIT_API_SECRET")
	}
	if opts.AgentName == "" {
		opts.AgentName = os.Getenv("LIVEKIT_AGENT_NAME")
	}
	if opts.HTTPProxy == "" {
		opts.HTTPProxy = os.Getenv("HTTPS_PROXY")
		if opts.HTTPProxy == "" {
			opts.HTTPProxy = os.Getenv("HTTP_PROXY")
		}
	}

	return opts
}

func workerTypeToJobType(workerType WorkerType) livekit.JobType {
	switch workerType {
	case WorkerTypePublisher:
		return livekit.JobType_JT_PUBLISHER
	default:
		return livekit.JobType_JT_ROOM
	}
}

func agentIdentityForJobID(jobID string) string {
	return "agent-" + jobID
}

func (s *AgentServer) registerWorkerRequest() *livekit.WorkerMessage {
	return &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_Register{
			Register: &livekit.RegisterWorkerRequest{
				Type:      workerTypeToJobType(s.Options.WorkerType),
				AgentName: s.Options.AgentName,
				Version:   "1.0.0",
			},
		},
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
	s.Options = resolveWorkerOptions(s.Options)

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
	dialer := *websocket.DefaultDialer
	if s.Options.HTTPProxy != "" {
		proxyURL, err := url.Parse(s.Options.HTTPProxy)
		if err != nil {
			return fmt.Errorf("invalid HTTP proxy URL: %w", err)
		}
		dialer.Proxy = http.ProxyURL(proxyURL)
	}

	conn, res, err := dialer.DialContext(ctx, wsURL.String(), map[string][]string{
		"Authorization": {fmt.Sprintf("Bearer %s", token)},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to LiveKit %s: %w", wsURL.String(), err)
	}
	_ = res
	s.conn = conn
	defer conn.Close()

	logger.Logger.Infow("Connected to LiveKit Server", "url", s.Options.WSRL)

	// Send Register request
	req := s.registerWorkerRequest()
	b, err := proto.Marshal(req)
	if err != nil {
		return err
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
		return err
	}

	// Message Loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				return err
			}

			if msgType != websocket.BinaryMessage {
				continue
			}

			msg := &livekit.ServerMessage{}
			if err := proto.Unmarshal(data, msg); err != nil {
				logger.Logger.Errorw("Failed to unmarshal server message", err)
				continue
			}

			s.handleMessage(ctx, msg)
		}
	}
}

func (s *AgentServer) handleMessage(ctx context.Context, msg *livekit.ServerMessage) {
	switch m := msg.Message.(type) {
	case *livekit.ServerMessage_Register:
		logger.Logger.Infow("Worker Registered", "workerId", m.Register.WorkerId, "serverInfo", m.Register.ServerInfo)
	case *livekit.ServerMessage_Availability:
		s.handleAvailability(ctx, m.Availability)
	case *livekit.ServerMessage_Assignment:
		s.handleAssignment(ctx, m.Assignment)
	case *livekit.ServerMessage_Termination:
		s.handleTermination(m.Termination)
	default:
		logger.Logger.Warnw("Unhandled message type received", nil)
	}
}

func (s *AgentServer) handleAvailability(ctx context.Context, req *livekit.AvailabilityRequest) {
	logger.Logger.Infow("Received availability request", "jobId", req.Job.Id)

	// Default to accept
	ans := &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_Availability{
			Availability: &livekit.AvailabilityResponse{
				JobId:               req.Job.Id,
				Available:           true,
				ParticipantIdentity: agentIdentityForJobID(req.Job.Id),
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
