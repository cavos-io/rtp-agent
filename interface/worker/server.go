package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
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

	defaultWorkerVersion  = "1.0.0"
	defaultMaxRetry       = 16
	defaultJobMemoryWarn  = 500
	defaultDrainTimeout   = 1800
	defaultSessionEnd     = 300
	defaultProcessTimeout = 10
	defaultLoadThreshold  = 0.7

	participantAttributeAgentName = "lk.agent.name"
)

var assignmentTimeout = 7500 * time.Millisecond

const workerStatusUpdateInterval = 2500 * time.Millisecond

var workerDialContext = func(ctx context.Context, dialer *websocket.Dialer, url string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return dialer.DialContext(ctx, url, headers)
}

var workerRetrySleep = func(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type WorkerPermissions struct {
	CanPublish        bool
	CanSubscribe      bool
	CanPublishData    bool
	CanUpdateMetadata bool
	CanPublishSources []livekit.TrackSource
	Hidden            bool
}

type WorkerStartedHandler func()

type WorkerRegisteredHandler func(workerID string, serverInfo *livekit.ServerInfo)

type WorkerOptions struct {
	AgentName  string
	WorkerType WorkerType
	MaxRetry   int
	Version    string
	WSURL      string
	LoadFunc   func(*AgentServer) float64
	// WSRL is kept for backward compatibility. Prefer WSURL for new code.
	WSRL                            string
	APIKey                          string
	APISecret                       string
	WorkerToken                     string
	HTTPProxy                       string
	LoadThreshold                   float64
	JobMemoryWarnMB                 float64
	JobMemoryLimitMB                float64
	NumIdleProcesses                int
	DrainTimeoutSeconds             int
	SessionEndTimeoutSeconds        float64
	ShutdownProcessTimeoutSeconds   float64
	InitializeProcessTimeoutSeconds float64
	Permissions                     *WorkerPermissions
}

type AgentServer struct {
	Options WorkerOptions

	entrypointFnc func(*JobContext) error
	requestFnc    func(*JobRequest) error
	sessionEndFnc func(*JobContext) error

	activeJobs         map[string]*JobContext
	pendingAccepts     map[string]JobAcceptArguments
	pendingTimers      map[string]*time.Timer
	reservedSlots      int
	draining           bool
	mu                 sync.Mutex
	conn               *websocket.Conn
	workerMessageSink  func(*livekit.WorkerMessage) error
	workerID           string
	startedHandlers    []WorkerStartedHandler
	registeredHandlers []WorkerRegisteredHandler

	consoleSession any // Store local session for CLI console
}

func NewAgentServer(opts WorkerOptions) *AgentServer {
	opts = resolveWorkerOptions(opts)
	return &AgentServer{
		Options:        opts,
		activeJobs:     make(map[string]*JobContext),
		pendingAccepts: make(map[string]JobAcceptArguments),
		pendingTimers:  make(map[string]*time.Timer),
	}
}

func (s *AgentServer) OnWorkerStarted(handler WorkerStartedHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.startedHandlers = append(s.startedHandlers, handler)
}

func (s *AgentServer) OnWorkerRegistered(handler WorkerRegisteredHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.registeredHandlers = append(s.registeredHandlers, handler)
}

func (s *AgentServer) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workerID
}

func (s *AgentServer) ActiveJobs() []*JobContext {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs := make([]*JobContext, 0, len(s.activeJobs))
	for _, jobCtx := range s.activeJobs {
		jobs = append(jobs, jobCtx)
	}
	return jobs
}

func (s *AgentServer) UpdateOptions(opts WorkerOptions) error {
	s.mu.Lock()
	if s.conn != nil {
		s.mu.Unlock()
		return fmt.Errorf("cannot update options after starting the server")
	}
	current := s.Options
	s.mu.Unlock()

	updated := mergeWorkerOptions(current, opts)
	updated = resolveWorkerOptions(updated)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return fmt.Errorf("cannot update options after starting the server")
	}
	s.Options = updated
	return nil
}

func mergeWorkerOptions(current WorkerOptions, next WorkerOptions) WorkerOptions {
	if next.AgentName != "" {
		current.AgentName = next.AgentName
	}
	if next.WorkerType != "" {
		current.WorkerType = next.WorkerType
	}
	if next.MaxRetry != 0 {
		current.MaxRetry = next.MaxRetry
	}
	if next.Version != "" {
		current.Version = next.Version
	}
	if next.WSURL != "" {
		current.WSURL = next.WSURL
		current.WSRL = next.WSURL
	} else if next.WSRL != "" {
		current.WSURL = next.WSRL
		current.WSRL = next.WSRL
	}
	if next.LoadFunc != nil {
		current.LoadFunc = next.LoadFunc
	}
	if next.APIKey != "" {
		current.APIKey = next.APIKey
	}
	if next.APISecret != "" {
		current.APISecret = next.APISecret
	}
	if next.WorkerToken != "" {
		current.WorkerToken = next.WorkerToken
	}
	if next.HTTPProxy != "" {
		current.HTTPProxy = next.HTTPProxy
	}
	if next.LoadThreshold != 0 {
		current.LoadThreshold = next.LoadThreshold
	}
	if next.JobMemoryWarnMB != 0 {
		current.JobMemoryWarnMB = next.JobMemoryWarnMB
	}
	if next.JobMemoryLimitMB != 0 {
		current.JobMemoryLimitMB = next.JobMemoryLimitMB
	}
	if next.NumIdleProcesses != 0 {
		current.NumIdleProcesses = next.NumIdleProcesses
	}
	if next.DrainTimeoutSeconds != 0 {
		current.DrainTimeoutSeconds = next.DrainTimeoutSeconds
	}
	if next.SessionEndTimeoutSeconds != 0 {
		current.SessionEndTimeoutSeconds = next.SessionEndTimeoutSeconds
	}
	if next.ShutdownProcessTimeoutSeconds != 0 {
		current.ShutdownProcessTimeoutSeconds = next.ShutdownProcessTimeoutSeconds
	}
	if next.InitializeProcessTimeoutSeconds != 0 {
		current.InitializeProcessTimeoutSeconds = next.InitializeProcessTimeoutSeconds
	}
	if next.Permissions != nil {
		current.Permissions = next.Permissions
	}
	return current
}

func resolveWorkerOptions(opts WorkerOptions) WorkerOptions {
	if opts.WorkerType == "" {
		opts.WorkerType = WorkerTypeRoom
	}
	if opts.Version == "" {
		opts.Version = defaultWorkerVersion
	}
	if opts.MaxRetry == 0 {
		opts.MaxRetry = defaultMaxRetry
	}
	if opts.JobMemoryWarnMB == 0 {
		opts.JobMemoryWarnMB = defaultJobMemoryWarn
	}
	if opts.DrainTimeoutSeconds == 0 {
		opts.DrainTimeoutSeconds = defaultDrainTimeout
	}
	if opts.SessionEndTimeoutSeconds == 0 {
		opts.SessionEndTimeoutSeconds = defaultSessionEnd
	}
	if opts.ShutdownProcessTimeoutSeconds == 0 {
		opts.ShutdownProcessTimeoutSeconds = defaultProcessTimeout
	}
	if opts.InitializeProcessTimeoutSeconds == 0 {
		opts.InitializeProcessTimeoutSeconds = defaultProcessTimeout
	}
	if opts.LoadThreshold == 0 {
		opts.LoadThreshold = defaultLoadThreshold
	}
	if opts.NumIdleProcesses == 0 {
		opts.NumIdleProcesses = defaultNumIdleProcesses()
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
	if opts.WorkerToken == "" {
		opts.WorkerToken = os.Getenv("LIVEKIT_WORKER_TOKEN")
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

func defaultNumIdleProcesses() int {
	cpus := runtime.NumCPU()
	if cpus > 4 {
		return 4
	}
	return cpus
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

func agentWebSocketURL(rawURL string, workerToken string) (string, error) {
	wsURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	if wsURL.Scheme == "http" {
		wsURL.Scheme = "ws"
	} else if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	}

	basePath := strings.TrimRight(wsURL.Path, "/")
	wsURL.Path = basePath + "/agent"
	if basePath == "" {
		wsURL.Path = "/agent"
	}

	values := url.Values{}
	if workerToken != "" {
		values.Set("worker_token", workerToken)
	}
	wsURL.RawQuery = values.Encode()

	return wsURL.String(), nil
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
				Version:   s.Options.Version,
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
	if status == livekit.WorkerStatus_WS_AVAILABLE && !s.availableForJob() {
		status = livekit.WorkerStatus_WS_FULL
	}
	return &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_UpdateWorker{
			UpdateWorker: &livekit.UpdateWorkerStatus{
				Status:   &status,
				Load:     float32(s.currentLoad()),
				JobCount: jobCount,
			},
		},
	}
}

func (s *AgentServer) drainingWorkerStatusMessage() *livekit.WorkerMessage {
	status := livekit.WorkerStatus_WS_FULL
	return &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_UpdateWorker{
			UpdateWorker: &livekit.UpdateWorkerStatus{
				Status:   &status,
				JobCount: uint32(s.activeJobCount()),
			},
		},
	}
}

func jobStatusMessage(jobID string, status livekit.JobStatus) *livekit.WorkerMessage {
	return &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_UpdateJob{
			UpdateJob: &livekit.UpdateJobStatus{
				JobId:  jobID,
				Status: status,
			},
		},
	}
}

func migrateJobMessage(jobIDs []string) *livekit.WorkerMessage {
	return &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_MigrateJob{
			MigrateJob: &livekit.MigrateJobRequest{JobIds: jobIDs},
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
		if err := s.sendWorkerMessage(s.drainingWorkerStatusMessage()); err != nil {
			return err
		}
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.inflightJobCount() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *AgentServer) inflightJobCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.activeJobs) + len(s.pendingAccepts)
}

func (s *AgentServer) activeJobCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.activeJobs)
}

func (s *AgentServer) currentLoad() float64 {
	if s.Options.LoadFunc == nil {
		return 0
	}
	load := s.Options.LoadFunc(s)
	if load < 0 {
		return 0
	}
	return load
}

func (s *AgentServer) effectiveLoad() float64 {
	load := s.currentLoad()
	threshold := s.Options.LoadThreshold
	if threshold <= 0 {
		return load
	}

	s.mu.Lock()
	activeCount := len(s.activeJobs)
	pendingCount := len(s.pendingAccepts)
	reservedSlots := s.reservedSlots
	s.mu.Unlock()

	var jobLoad float64
	if activeCount > 0 {
		jobLoad = load / float64(activeCount)
	} else {
		idleProcesses := s.Options.NumIdleProcesses
		if idleProcesses <= 0 {
			idleProcesses = 1
		}
		jobLoad = threshold / float64(idleProcesses)
	}

	return load + float64(pendingCount+reservedSlots)*jobLoad
}

func (s *AgentServer) availableForJob() bool {
	if s.Draining() {
		return false
	}
	threshold := s.Options.LoadThreshold
	if threshold <= 0 {
		return true
	}
	return s.effectiveLoad() < threshold
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

	agentURL, err := agentWebSocketURL(s.Options.WSRL, s.Options.WorkerToken)
	if err != nil {
		return err
	}

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

	conn, res, err := s.connectWorkerWebSocket(ctx, &dialer, agentURL, http.Header{
		"Authorization": []string{fmt.Sprintf("Bearer %s", token)},
	})
	if err != nil {
		return err
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

	msgType, data, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	if msgType != websocket.BinaryMessage {
		return fmt.Errorf("expected register response as first message")
	}

	msg := &livekit.ServerMessage{}
	if err := proto.Unmarshal(data, msg); err != nil {
		return err
	}
	if err := s.handleInitialRegisterMessage(ctx, msg); err != nil {
		return err
	}

	statusCtx, stopStatusUpdates := context.WithCancel(ctx)
	defer stopStatusUpdates()
	go s.runWorkerStatusUpdates(statusCtx, workerStatusUpdateInterval)
	s.emitWorkerStarted()

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

func (s *AgentServer) emitWorkerStarted() {
	s.mu.Lock()
	handlers := append([]WorkerStartedHandler(nil), s.startedHandlers...)
	s.mu.Unlock()

	for _, handler := range handlers {
		handler()
	}
}

func (s *AgentServer) runWorkerStatusUpdates(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.sendWorkerStatusUpdate(); err != nil {
				logger.Logger.Errorw("failed to update worker status", err)
				return
			}
		}
	}
}

func (s *AgentServer) sendWorkerStatusUpdate() error {
	if s.Draining() {
		return s.sendWorkerMessage(s.drainingWorkerStatusMessage())
	}
	return s.sendWorkerMessage(s.workerStatusMessage(livekit.WorkerStatus_WS_AVAILABLE))
}

func (s *AgentServer) connectWorkerWebSocket(ctx context.Context, dialer *websocket.Dialer, agentURL string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	retryCount := 0
	for {
		conn, res, err := workerDialContext(ctx, dialer, agentURL, headers)
		if err == nil {
			return conn, res, nil
		}

		if retryCount >= s.Options.MaxRetry {
			return nil, nil, fmt.Errorf("failed to connect to LiveKit after %d attempts %s: %w", retryCount, agentURL, err)
		}

		delay := workerRetryDelay(retryCount)
		retryCount++
		if err := workerRetrySleep(ctx, delay); err != nil {
			return nil, nil, err
		}
	}
}

func workerRetryDelay(retryCount int) time.Duration {
	delaySeconds := retryCount * 2
	if delaySeconds > 10 {
		delaySeconds = 10
	}
	return time.Duration(delaySeconds) * time.Second
}

func (s *AgentServer) handleInitialRegisterMessage(ctx context.Context, msg *livekit.ServerMessage) error {
	if msg.GetRegister() == nil {
		return fmt.Errorf("expected register response as first message")
	}
	s.handleMessage(ctx, msg)
	return nil
}

func (s *AgentServer) handleMessage(ctx context.Context, msg *livekit.ServerMessage) {
	switch m := msg.Message.(type) {
	case *livekit.ServerMessage_Register:
		logger.Logger.Infow("Worker Registered", "workerId", m.Register.WorkerId, "serverInfo", m.Register.ServerInfo)
		s.mu.Lock()
		s.workerID = m.Register.WorkerId
		s.mu.Unlock()
		s.emitWorkerRegistered(m.Register.WorkerId, m.Register.ServerInfo)
		s.reportActiveJobs()
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

func (s *AgentServer) emitWorkerRegistered(workerID string, serverInfo *livekit.ServerInfo) {
	s.mu.Lock()
	handlers := append([]WorkerRegisteredHandler(nil), s.registeredHandlers...)
	s.mu.Unlock()

	for _, handler := range handlers {
		handler(workerID, serverInfo)
	}
}

func (s *AgentServer) reportActiveJobs() {
	s.mu.Lock()
	jobIDs := make([]string, 0, len(s.activeJobs))
	for jobID := range s.activeJobs {
		jobIDs = append(jobIDs, jobID)
	}
	s.mu.Unlock()

	if len(jobIDs) == 0 {
		return
	}

	sort.Strings(jobIDs)
	if err := s.sendWorkerMessage(migrateJobMessage(jobIDs)); err != nil {
		logger.Logger.Errorw("failed to report active jobs", err, "jobIds", jobIDs)
	}
}

func (s *AgentServer) handleAvailability(ctx context.Context, req *livekit.AvailabilityRequest) {
	go s.answerAvailability(ctx, req)
}

func (s *AgentServer) answerAvailability(ctx context.Context, req *livekit.AvailabilityRequest) {
	logger.Logger.Infow("Received availability request", "jobId", req.Job.Id)

	if !s.availableForJob() {
		if err := s.sendWorkerMessage(availabilityResponseForReject(req, JobRejectArguments{Terminate: false})); err != nil {
			logger.Logger.Errorw("failed to reject availability while unavailable", err, "jobId", req.Job.Id)
		}
		return
	}

	s.reserveAvailabilitySlot()
	defer s.releaseAvailabilitySlot()

	answered := false
	jobReq := &JobRequest{
		Job: req.Job,
		acceptFnc: func(args JobAcceptArguments) error {
			if args.Name == "" {
				args.Name = s.Options.AgentName
			}
			answered = true
			s.storePendingAccept(req.Job.Id, args)
			if err := s.sendWorkerMessage(availabilityResponseForAccept(req, args, s.Options.AgentName)); err != nil {
				return err
			}
			return nil
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
		_ = jobReq.Reject(JobRejectArguments{Terminate: false})
	}
}

func (s *AgentServer) reserveAvailabilitySlot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reservedSlots++
}

func (s *AgentServer) releaseAvailabilitySlot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reservedSlots > 0 {
		s.reservedSlots--
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

func (s *AgentServer) storePendingAccept(jobID string, args JobAcceptArguments) {
	s.mu.Lock()
	if timer, ok := s.pendingTimers[jobID]; ok {
		timer.Stop()
	}
	s.pendingAccepts[jobID] = args
	var timer *time.Timer
	timer = time.AfterFunc(assignmentTimeout, func() {
		s.mu.Lock()
		if s.pendingTimers[jobID] != timer {
			s.mu.Unlock()
			return
		}
		delete(s.pendingAccepts, jobID)
		delete(s.pendingTimers, jobID)
		s.mu.Unlock()
		logger.Logger.Warnw("assignment timed out after availability accept", nil, "jobId", jobID)
	})
	s.pendingTimers[jobID] = timer
	s.mu.Unlock()
}

func (s *AgentServer) handleAssignment(ctx context.Context, req *livekit.JobAssignment) {
	logger.Logger.Infow("Received job assignment", "jobId", req.Job.Id)

	// Spin up a job context here
	jobURL := s.Options.WSRL
	if req.GetUrl() != "" {
		jobURL = req.GetUrl()
	}
	jobCtx := NewJobContext(req.Job, jobURL, s.Options.APIKey, s.Options.APISecret)
	jobCtx.token = req.GetToken()

	s.mu.Lock()
	args, accepted := s.pendingAccepts[req.Job.Id]
	if !accepted {
		s.mu.Unlock()
		logger.Logger.Warnw("received assignment for unknown job", nil, "jobId", req.Job.Id)
		return
	}

	jobCtx.WorkerID = s.workerID
	jobCtx.AcceptArguments = args
	delete(s.pendingAccepts, req.Job.Id)
	if timer, ok := s.pendingTimers[req.Job.Id]; ok {
		timer.Stop()
		delete(s.pendingTimers, req.Job.Id)
	}
	s.activeJobs[req.Job.Id] = jobCtx
	s.mu.Unlock()

	if err := s.sendWorkerMessage(jobStatusMessage(req.Job.Id, livekit.JobStatus_JS_RUNNING)); err != nil {
		logger.Logger.Errorw("failed to update job status", err, "jobId", req.Job.Id)
	}

	if s.entrypointFnc != nil {
		go func() {
			status := livekit.JobStatus_JS_SUCCESS
			if err := s.entrypointFnc(jobCtx); err != nil {
				logger.Logger.Errorw("Job entrypoint failed", err, "jobId", req.Job.Id)
				status = livekit.JobStatus_JS_FAILED
			}
			if err := s.sendWorkerMessage(jobStatusMessage(req.Job.Id, status)); err != nil {
				logger.Logger.Errorw("failed to update job status", err, "jobId", req.Job.Id)
			}
			s.finishJob(jobCtx)
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
		s.runSessionEnd(jobCtx)

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

	s.runSessionEnd(jobCtx)

	jobCtx.Shutdown("")
}

func (s *AgentServer) runSessionEnd(jobCtx *JobContext) {
	if s.sessionEndFnc == nil {
		return
	}

	timeout := time.Duration(s.Options.SessionEndTimeoutSeconds * float64(time.Second))
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- s.sessionEndFnc(jobCtx)
	}()

	if timeout <= 0 {
		if err := <-doneCh; err != nil {
			logger.Logger.Errorw("Session end callback failed", err, "jobId", jobCtx.Job.Id)
		}
		return
	}

	select {
	case err := <-doneCh:
		if err != nil {
			logger.Logger.Errorw("Session end callback failed", err, "jobId", jobCtx.Job.Id)
		}
	case <-time.After(timeout):
		logger.Logger.Errorw("Session end callback timed out", nil, "jobId", jobCtx.Job.Id, "timeout", timeout)
	}
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
	jobCtx.fakeJob = true
	return jobCtx
}
