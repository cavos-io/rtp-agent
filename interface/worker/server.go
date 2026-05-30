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

	participantAttributeAgentName = "lk.agent.name"
)

type WorkerPermissions struct {
	CanPublish        bool
	CanSubscribe      bool
	CanPublishData    bool
	CanUpdateMetadata bool
	CanPublishSources []livekit.TrackSource
	Hidden            bool
}

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
	Permissions         *WorkerPermissions
}

type AgentServer struct {
	Options WorkerOptions

	entrypointFnc func(*JobContext) error
	requestFnc    func(*JobRequest) error
	sessionEndFnc func(*JobContext) error

	activeJobs        map[string]*JobContext
	draining          bool
	mu                sync.Mutex
	conn              *websocket.Conn
	workerMessageSink func(*livekit.WorkerMessage) error

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
	if opts.Permissions == nil {
		permissions := resolveWorkerPermissions(nil)
		opts.Permissions = &permissions
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

func resolveWorkerPermissions(permissions *WorkerPermissions) WorkerPermissions {
	if permissions == nil {
		return WorkerPermissions{
			CanPublish:        true,
			CanSubscribe:      true,
			CanPublishData:    true,
			CanUpdateMetadata: true,
		}
	}
	return *permissions
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

func availabilityResponseForAccept(req *livekit.AvailabilityRequest, args JobAcceptArguments, agentName string) *livekit.WorkerMessage {
	if args.Identity == "" {
		args.Identity = agentIdentityForJobID(req.Job.Id)
	}
	attributes := make(map[string]string, len(args.Attributes)+1)
	if agentName != "" {
		attributes[participantAttributeAgentName] = agentName
	}
	for key, value := range args.Attributes {
		attributes[key] = value
	}

	return &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_Availability{
			Availability: &livekit.AvailabilityResponse{
				JobId:                 req.Job.Id,
				Available:             true,
				ParticipantIdentity:   args.Identity,
				ParticipantName:       args.Name,
				ParticipantMetadata:   args.Metadata,
				ParticipantAttributes: attributes,
			},
		},
	}
}

func availabilityResponseForReject(req *livekit.AvailabilityRequest, args JobRejectArguments) *livekit.WorkerMessage {
	return &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_Availability{
			Availability: &livekit.AvailabilityResponse{
				JobId:     req.Job.Id,
				Available: false,
				Terminate: args.Terminate,
			},
		},
	}
}

func (s *AgentServer) registerWorkerRequest() *livekit.WorkerMessage {
	permissions := resolveWorkerPermissions(s.Options.Permissions)
	return &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_Register{
			Register: &livekit.RegisterWorkerRequest{
				Type:      workerTypeToJobType(s.Options.WorkerType),
				AgentName: s.Options.AgentName,
				Version:   "1.0.0",
				AllowedPermissions: &livekit.ParticipantPermission{
					CanPublish:        permissions.CanPublish,
					CanSubscribe:      permissions.CanSubscribe,
					CanPublishData:    permissions.CanPublishData,
					CanUpdateMetadata: permissions.CanUpdateMetadata,
					CanPublishSources: permissions.CanPublishSources,
					Hidden:            permissions.Hidden,
					Agent:             true,
				},
			},
		},
	}
}

func (s *AgentServer) workerStatusMessage(status livekit.WorkerStatus) *livekit.WorkerMessage {
	jobCount := uint32(s.activeJobCount())
	return &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_UpdateWorker{
			UpdateWorker: &livekit.UpdateWorkerStatus{
				Status:   &status,
				JobCount: jobCount,
			},
		},
	}
}

func (s *AgentServer) RTCSession(
	entrypoint func(*JobContext) error,
	request func(*JobRequest) error,
	sessionEnd func(*JobContext) error,
) error {
	if s.entrypointFnc != nil {
		return fmt.Errorf("the AgentServer currently only supports registering one rtc_session")
	}
	s.entrypointFnc = entrypoint
	s.requestFnc = request
	s.sessionEndFnc = sessionEnd
	return nil
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

func (s *AgentServer) Draining() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.draining
}

func (s *AgentServer) Drain(ctx context.Context) error {
	if s.Options.DrainTimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(s.Options.DrainTimeoutSeconds)*time.Second)
		defer cancel()
	}

	s.mu.Lock()
	if s.draining {
		s.mu.Unlock()
		return nil
	}
	s.draining = true
	connected := s.conn != nil
	s.mu.Unlock()

	if connected {
		if err := s.sendWorkerMessage(s.workerStatusMessage(livekit.WorkerStatus_WS_FULL)); err != nil {
			return err
		}
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.activeJobCount() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *AgentServer) activeJobCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.activeJobs)
}

func (s *AgentServer) validateRunPreconditions() error {
	s.Options = resolveWorkerOptions(s.Options)

	if s.entrypointFnc == nil {
		return fmt.Errorf("No RTC session entrypoint has been registered")
	}
	if s.Options.WSRL == "" || s.Options.APIKey == "" || s.Options.APISecret == "" {
		return fmt.Errorf("missing LiveKit credentials")
	}
	return nil
}

func (s *AgentServer) Run(ctx context.Context) error {
	if err := s.validateRunPreconditions(); err != nil {
		return err
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

	if s.Draining() {
		if err := s.sendWorkerMessage(availabilityResponseForReject(req, JobRejectArguments{Terminate: false})); err != nil {
			logger.Logger.Errorw("failed to reject availability while draining", err, "jobId", req.Job.Id)
		}
		return
	}

	answered := false
	jobReq := &JobRequest{
		Job: req.Job,
		acceptFnc: func(args JobAcceptArguments) error {
			if args.Name == "" {
				args.Name = s.Options.AgentName
			}
			answered = true
			return s.sendWorkerMessage(availabilityResponseForAccept(req, args, s.Options.AgentName))
		},
		rejectFnc: func(args JobRejectArguments) error {
			answered = true
			return s.sendWorkerMessage(availabilityResponseForReject(req, args))
		},
	}

	if s.requestFnc != nil {
		if err := s.requestFnc(jobReq); err != nil {
			logger.Logger.Errorw("availability request callback failed", err, "jobId", req.Job.Id)
		}
	} else {
		_ = jobReq.Accept(JobAcceptArguments{})
	}

	if !answered {
		_ = jobReq.Accept(JobAcceptArguments{})
	}
}

func (s *AgentServer) sendWorkerMessage(msg *livekit.WorkerMessage) error {
	if s.workerMessageSink != nil {
		return s.workerMessageSink(msg)
	}

	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return fmt.Errorf("worker websocket is not connected")
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, b)
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

		jobCtx.Shutdown("")

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
	jobCtx := newLocalJobContext(roomName, participantIdentity, s.Options)

	// For local execution, we want to connect immediately
	// For basic parity, we just trigger the entrypoint directly.

	s.mu.Lock()
	s.activeJobs[jobCtx.Job.Id] = jobCtx
	s.mu.Unlock()

	if s.entrypointFnc != nil {
		go func() {
			if err := s.entrypointFnc(jobCtx); err != nil {
				logger.Logger.Errorw("Local job entrypoint failed", err, "jobId", jobCtx.Job.Id)
			}
		}()
	}

	// Block until context is done for local execution
	<-ctx.Done()
	s.finishJob(jobCtx)
	return nil
}

func (s *AgentServer) finishJob(jobCtx *JobContext) {
	if jobCtx == nil || jobCtx.Job == nil {
		return
	}

	s.mu.Lock()
	delete(s.activeJobs, jobCtx.Job.Id)
	s.mu.Unlock()

	if s.sessionEndFnc != nil {
		if err := s.sessionEndFnc(jobCtx); err != nil {
			logger.Logger.Errorw("Session end callback failed", err, "jobId", jobCtx.Job.Id)
		}
	}

	jobCtx.Shutdown("")
}

func newLocalJobContext(roomName string, participantIdentity string, opts WorkerOptions) *JobContext {
	opts = resolveWorkerOptions(opts)
	job := &livekit.Job{
		Id: "local-job-" + time.Now().Format("20060102150405"),
		Room: &livekit.Room{
			Name: roomName,
			Sid:  "RM_local",
		},
		Type: livekit.JobType_JT_ROOM,
	}

	if participantIdentity == "" {
		participantIdentity = agentIdentityForJobID(job.Id)
	}
	jobCtx := NewJobContext(job, opts.WSRL, opts.APIKey, opts.APISecret)
	jobCtx.AcceptArguments = JobAcceptArguments{Identity: participantIdentity}
	return jobCtx
}
