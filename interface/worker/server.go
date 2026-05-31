package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/agent"
	workeripc "github.com/cavos-io/conversation-worker/interface/worker/ipc"
	"github.com/cavos-io/conversation-worker/library/logger"
	mathutil "github.com/cavos-io/conversation-worker/library/math"
	"github.com/cavos-io/conversation-worker/library/telemetry"
	"github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
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
	defaultProdLogLevel   = "INFO"
	defaultDevLogLevel    = "DEBUG"
	defaultProdHTTPPort   = 8081
	defaultDevHTTPPort    = 0

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

type WorkerInfo struct {
	HTTPPort    int
	CloudAgents bool
}

type WorkerOptions struct {
	AgentName      string
	AgentNameIsEnv bool
	WorkerType     WorkerType
	MaxRetry       int
	Version        string
	Host           string
	Port           int
	WSURL          string
	LoadFunc       func(*AgentServer) float64
	// WSRL is kept for backward compatibility. Prefer WSURL for new code.
	WSRL                            string
	APIKey                          string
	APISecret                       string
	WorkerToken                     string
	HTTPProxy                       string
	DevMode                         bool
	LogLevel                        string
	PrometheusPort                  int
	PrometheusMultiprocDir          string
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
	httpServer         *http.Server
	httpPort           int
	prometheusServer   *telemetry.HttpServer
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

func (s *AgentServer) WorkerInfo() WorkerInfo {
	s.mu.Lock()
	httpPort := s.httpPort
	s.mu.Unlock()

	return WorkerInfo{
		HTTPPort:    httpPort,
		CloudAgents: s.Options.WorkerToken != "",
	}
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

func (s *AgentServer) ActiveRunningJobs() []workeripc.RunningJobInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs := make([]workeripc.RunningJobInfo, 0, len(s.activeJobs))
	for _, jobCtx := range s.activeJobs {
		info := runningJobInfoFromContext(jobCtx)
		if info.WorkerID == "" {
			info.WorkerID = s.workerID
		}
		jobs = append(jobs, info)
	}
	return jobs
}

func runningJobInfoFromContext(jobCtx *JobContext) workeripc.RunningJobInfo {
	return workeripc.RunningJobInfo{
		AcceptArguments: workeripc.JobAcceptArguments{
			Name:       jobCtx.AcceptArguments.Name,
			Identity:   jobCtx.AcceptArguments.Identity,
			Metadata:   jobCtx.AcceptArguments.Metadata,
			Attributes: jobCtx.AcceptArguments.Attributes,
		},
		Job:      jobCtx.Job,
		URL:      jobCtx.url,
		Token:    jobCtx.token,
		WorkerID: jobCtx.WorkerID,
		FakeJob:  jobCtx.fakeJob,
	}
}

func refreshRunningJobTokenForReload(info workeripc.RunningJobInfo, apiSecret string, now time.Time) (workeripc.RunningJobInfo, error) {
	if apiSecret == "" {
		return workeripc.RunningJobInfo{}, fmt.Errorf("api_secret is required to reload jobs")
	}
	tok, err := jwt.ParseSigned(info.Token)
	if err != nil {
		return workeripc.RunningJobInfo{}, err
	}
	standardClaims := jwt.Claims{}
	grants := auth.ClaimGrants{}
	if err := tok.Claims([]byte(apiSecret), &standardClaims, &grants); err != nil {
		return workeripc.RunningJobInfo{}, err
	}
	standardClaims.Expiry = jwt.NewNumericDate(now.Add(time.Hour))

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(apiSecret)}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return workeripc.RunningJobInfo{}, err
	}
	token, err := jwt.Signed(signer).Claims(standardClaims).Claims(grants).CompactSerialize()
	if err != nil {
		return workeripc.RunningJobInfo{}, err
	}
	info.Token = token
	return info, nil
}

func refreshRunningJobsForReload(jobs []workeripc.RunningJobInfo, apiSecret string, now time.Time) ([]workeripc.RunningJobInfo, error) {
	refreshed := make([]workeripc.RunningJobInfo, 0, len(jobs))
	for _, job := range jobs {
		info, err := refreshRunningJobTokenForReload(job, apiSecret, now)
		if err != nil {
			return nil, err
		}
		refreshed = append(refreshed, info)
	}
	return refreshed, nil
}

func (s *AgentServer) ReloadRunningJobs(ctx context.Context, jobs []workeripc.RunningJobInfo, now time.Time) error {
	refreshed, err := refreshRunningJobsForReload(jobs, s.Options.APISecret, now)
	if err != nil {
		return err
	}

	for _, info := range refreshed {
		if info.Job == nil {
			continue
		}

		jobURL := s.Options.WSRL
		if jobURL == "" {
			jobURL = info.URL
		}
		jobCtx := NewJobContext(info.Job, jobURL, s.Options.APIKey, s.Options.APISecret)
		jobCtx.token = info.Token
		jobCtx.WorkerID = info.WorkerID
		jobCtx.AcceptArguments = JobAcceptArguments{
			Name:       info.AcceptArguments.Name,
			Identity:   info.AcceptArguments.Identity,
			Metadata:   info.AcceptArguments.Metadata,
			Attributes: info.AcceptArguments.Attributes,
		}
		jobCtx.fakeJob = info.FakeJob

		s.mu.Lock()
		if jobCtx.WorkerID == "" {
			jobCtx.WorkerID = s.workerID
		}
		s.activeJobs[info.Job.Id] = jobCtx
		s.mu.Unlock()

		s.launchReloadedJob(ctx, jobCtx)
	}

	return nil
}

func (s *AgentServer) launchReloadedJob(ctx context.Context, jobCtx *JobContext) {
	if s.entrypointFnc == nil {
		return
	}

	go func() {
		status := livekit.JobStatus_JS_SUCCESS
		if err := s.entrypointFnc(jobCtx); err != nil {
			logger.Logger.Errorw("Reloaded job entrypoint failed", err, "jobId", jobCtx.JobID())
			status = livekit.JobStatus_JS_FAILED
		}
		select {
		case <-ctx.Done():
			logger.Logger.Debugw("reload job status skipped after context cancellation", "jobId", jobCtx.JobID())
		default:
			if err := s.sendWorkerMessage(jobStatusMessage(jobCtx.JobID(), status)); err != nil {
				logger.Logger.Errorw("failed to update reloaded job status", err, "jobId", jobCtx.JobID())
			}
		}
		s.finishJob(jobCtx)
	}()
}

func (s *AgentServer) handleReloadMessage(ctx context.Context, payload any, reloadCount int, now time.Time) (any, bool, error) {
	switch msg := payload.(type) {
	case *workeripc.ActiveJobsRequest, workeripc.ActiveJobsRequest:
		return &workeripc.ActiveJobsResponse{
			Jobs:        s.ActiveRunningJobs(),
			ReloadCount: reloadCount,
		}, true, nil
	case *workeripc.ReloadJobsResponse:
		if err := s.ReloadRunningJobs(ctx, msg.Jobs, now); err != nil {
			return nil, true, err
		}
		return &workeripc.Reloaded{}, true, nil
	case workeripc.ReloadJobsResponse:
		if err := s.ReloadRunningJobs(ctx, msg.Jobs, now); err != nil {
			return nil, true, err
		}
		return &workeripc.Reloaded{}, true, nil
	default:
		return nil, false, nil
	}
}

func (s *AgentServer) handleReloadIPCMessage(ctx context.Context, r io.Reader, out io.Writer, reloadCount int, now time.Time) (bool, error) {
	msg, err := workeripc.ReadMessage(r)
	if err != nil {
		return false, err
	}
	payload, err := workeripc.DecodePayload(msg)
	if err != nil {
		return false, err
	}

	resp, handled, err := s.handleReloadMessage(ctx, payload, reloadCount, now)
	if err != nil {
		return handled, err
	}
	if !handled || resp == nil {
		return handled, nil
	}

	responseMsg, err := workeripc.NewMessage(resp)
	if err != nil {
		return true, err
	}
	if err := workeripc.WriteMessage(out, responseMsg); err != nil {
		return true, err
	}
	return true, nil
}

func (s *AgentServer) processReloadIPCMessages(ctx context.Context, r io.Reader, out io.Writer, reloadCount int, now time.Time) error {
	for {
		_, err := s.handleReloadIPCMessage(ctx, r, out, reloadCount, now)
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func (s *AgentServer) runReloadIPCSession(ctx context.Context, rw io.ReadWriter, reloadCount int, now time.Time) error {
	msg, err := workeripc.NewMessage(&workeripc.ReloadJobsRequest{})
	if err != nil {
		return err
	}
	if err := workeripc.WriteMessage(rw, msg); err != nil {
		return err
	}
	return s.processReloadIPCMessages(ctx, rw, rw, reloadCount, now)
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
		current.AgentNameIsEnv = false
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
	if next.Host != "" {
		current.Host = next.Host
	}
	if next.Port != 0 {
		current.Port = next.Port
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
	if next.PrometheusPort != 0 {
		current.PrometheusPort = next.PrometheusPort
	}
	if next.PrometheusMultiprocDir != "" {
		current.PrometheusMultiprocDir = next.PrometheusMultiprocDir
	}
	if next.DevMode {
		current.DevMode = true
	}
	if next.LogLevel != "" {
		current.LogLevel = next.LogLevel
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
	if !opts.DevMode {
		opts.DevMode = liveKitDevModeEnabled(os.Getenv("LIVEKIT_DEV_MODE"))
	}
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
	if opts.LogLevel == "" {
		opts.LogLevel = os.Getenv("LIVEKIT_LOG_LEVEL")
	}
	if opts.LogLevel == "" {
		if opts.DevMode {
			opts.LogLevel = defaultDevLogLevel
		} else {
			opts.LogLevel = defaultProdLogLevel
		}
	}
	opts.LogLevel = strings.ToUpper(opts.LogLevel)
	if opts.Port == 0 && !opts.DevMode {
		opts.Port = defaultProdHTTPPort
	}
	if opts.LoadThreshold == 0 {
		if opts.DevMode {
			opts.LoadThreshold = math.Inf(1)
		} else {
			opts.LoadThreshold = defaultLoadThreshold
		}
	}
	if opts.NumIdleProcesses == 0 && !opts.DevMode {
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
		opts.AgentNameIsEnv = opts.AgentName != ""
	}
	if opts.HTTPProxy == "" {
		opts.HTTPProxy = os.Getenv("HTTPS_PROXY")
		if opts.HTTPProxy == "" {
			opts.HTTPProxy = os.Getenv("HTTP_PROXY")
		}
	}

	return opts
}

type workerMetadataResponse struct {
	AgentName      string  `json:"agent_name"`
	AgentNameIsEnv bool    `json:"agent_name_is_env"`
	WorkerType     string  `json:"worker_type"`
	WorkerLoad     float64 `json:"worker_load"`
	ActiveJobs     int     `json:"active_jobs"`
	SDKVersion     string  `json:"sdk_version"`
	ProjectType    string  `json:"project_type"`
}

func (s *AgentServer) workerHTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/worker", func(w http.ResponseWriter, r *http.Request) {
		body := workerMetadataResponse{
			AgentName:      s.Options.AgentName,
			AgentNameIsEnv: s.Options.AgentNameIsEnv,
			WorkerType:     livekit.JobType_name[int32(workerTypeToJobType(s.Options.WorkerType))],
			WorkerLoad:     s.currentLoad(),
			ActiveJobs:     s.activeJobCount(),
			SDKVersion:     s.Options.Version,
			ProjectType:    "go",
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			logger.Logger.Errorw("failed to encode worker metadata", err)
		}
	})
	return mux
}

func (s *AgentServer) startWorkerHTTPServer() (*http.Server, error) {
	host := s.Options.Host
	addr := fmt.Sprintf("%s:%d", host, s.Options.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("unexpected HTTP listener address %T", ln.Addr())
	}

	srv := &http.Server{Handler: s.workerHTTPHandler()}
	s.mu.Lock()
	s.httpServer = srv
	s.httpPort = tcpAddr.Port
	s.mu.Unlock()

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Logger.Errorw("worker HTTP server error", err)
		}
	}()

	return srv, nil
}

func (s *AgentServer) startPrometheusServer() (*telemetry.HttpServer, error) {
	if s.Options.PrometheusPort == 0 {
		return nil, nil
	}
	server := telemetry.NewHttpServer(s.Options.Host, s.Options.PrometheusPort)
	if err := server.Start(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.prometheusServer = server
	s.mu.Unlock()
	return server, nil
}

func (s *AgentServer) configurePrometheusMultiprocDir() error {
	if s.Options.PrometheusMultiprocDir == "" {
		if dir := os.Getenv("PROMETHEUS_MULTIPROC_DIR"); dir != "" {
			s.Options.PrometheusMultiprocDir = dir
		}
		return nil
	}
	if err := os.MkdirAll(s.Options.PrometheusMultiprocDir, 0o755); err != nil {
		return err
	}
	return os.Setenv("PROMETHEUS_MULTIPROC_DIR", s.Options.PrometheusMultiprocDir)
}

func liveKitDevModeEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func validWorkerLogLevel(logLevel string) bool {
	switch strings.ToUpper(strings.TrimSpace(logLevel)) {
	case "TRACE", "DEBUG", "INFO", "WARN", "ERROR", "CRITICAL":
		return true
	default:
		return false
	}
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
		return s.DrainWithTimeout(ctx, time.Duration(s.Options.DrainTimeoutSeconds)*time.Second)
	}
	return s.drain(ctx)
}

func (s *AgentServer) DrainWithTimeout(ctx context.Context, timeout time.Duration) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return s.drain(ctx)
}

func (s *AgentServer) drain(ctx context.Context) error {
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
	if math.IsInf(threshold, 1) {
		return true
	}
	return s.effectiveLoad() < threshold
}

func (s *AgentServer) validateRunPreconditions() error {
	s.Options = resolveWorkerOptions(s.Options)
	if s.Options.WorkerToken != "" {
		s.Options.LoadFunc = nil
		s.Options.LoadThreshold = defaultLoadThreshold
	}
	if s.Options.LoadThreshold > 1 && !math.IsInf(s.Options.LoadThreshold, 1) && !s.Options.DevMode {
		return fmt.Errorf("load_threshold in prod env must be less than 1, current value: %v", s.Options.LoadThreshold)
	}
	if !validWorkerLogLevel(s.Options.LogLevel) {
		return fmt.Errorf("invalid log_level %q, valid levels: CRITICAL, DEBUG, ERROR, INFO, TRACE, WARN", s.Options.LogLevel)
	}
	if s.entrypointFnc == nil {
		return fmt.Errorf("No RTC session entrypoint has been registered")
	}
	if s.Options.WSRL == "" {
		return fmt.Errorf("ws_url is required, or set LIVEKIT_URL environment variable")
	}
	if s.Options.APIKey == "" {
		return fmt.Errorf("api_key is required, or set LIVEKIT_API_KEY environment variable")
	}
	if s.Options.APISecret == "" {
		return fmt.Errorf("api_secret is required, or set LIVEKIT_API_SECRET environment variable")
	}
	return nil
}

func (s *AgentServer) Run(ctx context.Context) error {
	if err := s.validateRunPreconditions(); err != nil {
		return err
	}
	os.Setenv("LIVEKIT_URL", s.Options.WSRL)
	os.Setenv("LIVEKIT_API_KEY", s.Options.APIKey)
	os.Setenv("LIVEKIT_API_SECRET", s.Options.APISecret)

	httpServer, err := s.startWorkerHTTPServer()
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			_ = httpServer.Close()
		}
		s.mu.Lock()
		s.httpServer = nil
		s.httpPort = 0
		s.mu.Unlock()
	}()

	if err := s.configurePrometheusMultiprocDir(); err != nil {
		return err
	}
	prometheusServer, err := s.startPrometheusServer()
	if err != nil {
		return err
	}
	if prometheusServer != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := prometheusServer.Stop(shutdownCtx); err != nil {
				logger.Logger.Errorw("failed to stop Prometheus server", err)
			}
			s.mu.Lock()
			s.prometheusServer = nil
			s.mu.Unlock()
		}()
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
	jobCtx.WorkerID = s.workerID

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
		Id: mathutil.ShortUUID("mock-job-"),
		Room: &livekit.Room{
			Name: roomName,
			Sid:  mathutil.ShortUUID("SRM_"),
		},
		Type: livekit.JobType_JT_ROOM,
	}

	if participantIdentity == "" {
		participantIdentity = mathutil.ShortUUID("fake-agent-")
	}
	jobCtx := NewJobContext(job, opts.WSRL, opts.APIKey, opts.APISecret)
	jobCtx.AcceptArguments = JobAcceptArguments{Identity: participantIdentity}
	jobCtx.fakeJob = true
	if opts.APIKey != "" && opts.APISecret != "" {
		token, err := auth.NewAccessToken(opts.APIKey, opts.APISecret).
			SetIdentity(participantIdentity).
			SetKind(livekit.ParticipantInfo_AGENT).
			SetVideoGrant(&auth.VideoGrant{
				RoomJoin: true,
				Room:     roomName,
				Agent:    true,
			}).
			SetValidFor(time.Hour).
			ToJWT()
		if err == nil {
			jobCtx.token = token
		}
	}
	return jobCtx
}
