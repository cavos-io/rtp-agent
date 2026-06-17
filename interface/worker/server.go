package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	workeripc "github.com/cavos-io/rtp-agent/interface/worker/ipc"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/cavos-io/rtp-agent/library/logger"
	mathutil "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils"
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

	rtcSessionRequiredMessage = "No RTC session entrypoint has been registered.\n" +
		"Define one using the @server.rtc_session() decorator, for example:\n" +
		"    @server.rtc_session(agent_name=\"my_agent\")\n" +
		"    async def my_agent(ctx: JobContext):\n" +
		"        ...\n"

	duplicateRTCSessionMessage = "The AgentServer currently only supports registering only one rtc_session"
)

type workerReferenceError string

func (e workerReferenceError) Error() string {
	return string(e)
}

var localEntrypointCloseWait = 15 * time.Second

var newLocalJobExecutor = func(id string, entrypoint func() error) workeripc.JobExecutor {
	return workeripc.NewThreadJobExecutor(id, entrypoint)
}

type localJobPool interface {
	Start(ctx context.Context) error
	LaunchRunningJob(ctx context.Context, info workeripc.RunningJobInfo) error
	GetByJobID(jobID string) workeripc.JobExecutor
	SetTargetIdleProcesses(numIdleProcesses int)
	SetCloseTimeout(timeout time.Duration)
	Close() error
}

var newLocalProcPool = func(maxProcesses int, executorType workeripc.ExecutorType, entrypoint func() error) localJobPool {
	return workeripc.NewProcPool(maxProcesses, executorType, entrypoint)
}

var assignmentTimeout = 7500 * time.Millisecond

var uploadSessionReport = agent.UploadSessionReport

const workerStatusUpdateInterval = 2500 * time.Millisecond
const WorkerProtocolVersion = 1

var defaultWorkerLoadMu sync.Mutex

var defaultWorkerLoadCalc *workerLoadCalculator

var defaultSystemCPUSampler = newSystemCPUSampler()

var defaultWorkerLoadSample = defaultSystemCPUSampler.Sample

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

var workerListen = net.Listen
var workerPrometheusListen = net.Listen
var workerReloadIPCDial = net.Dial

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

type LocalJobOptions struct {
	FakeJob           bool
	RoomInfo          *livekit.Room
	Token             string
	RecordingOptions  agent.RecordingOptions
	SessionReportPath string
	SessionDirectory  string
}

type WorkerOptions struct {
	AgentName      string
	AgentNameIsEnv bool
	WorkerType     WorkerType
	Transport      WorkerTransport
	Agora          AgoraOptions
	MaxRetry       int
	MaxRetrySet    bool
	Version        string
	Host           string
	Port           int
	PortSet        bool
	WSURL          string
	LoadFunc       func(*AgentServer) float64
	HealthCheck    func(*AgentServer) error
	SetupFunc      func(*JobProcess) error
	// WSRL is kept for backward compatibility. Prefer WSURL for new code.
	WSRL                               string
	APIKey                             string
	APISecret                          string
	WorkerToken                        string
	HTTPProxy                          string
	HTTPProxySet                       bool
	UserArguments                      any
	DevMode                            bool
	LogLevel                           string
	PrometheusPort                     int
	PrometheusPortSet                  bool
	PrometheusMultiprocDir             string
	LoadThreshold                      float64
	LoadThresholdSet                   bool
	JobMemoryWarnMB                    float64
	JobMemoryWarnMBSet                 bool
	JobMemoryLimitMB                   float64
	JobMemoryLimitMBSet                bool
	NumIdleProcesses                   int
	NumIdleProcessesSet                bool
	DrainTimeoutSeconds                int
	DrainTimeoutSecondsSet             bool
	SessionEndTimeoutSeconds           float64
	SessionEndTimeoutSecondsSet        bool
	ShutdownProcessTimeoutSeconds      float64
	ShutdownProcessTimeoutSecondsSet   bool
	InitializeProcessTimeoutSeconds    float64
	InitializeProcessTimeoutSecondsSet bool
	Permissions                        *WorkerPermissions
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
	running            bool
	mu                 sync.Mutex
	conn               *websocket.Conn
	httpServer         *http.Server
	httpPort           int
	prometheusServer   *telemetry.HttpServer
	workerMessageSink  func(*livekit.WorkerMessage) error
	workerID           string
	connectionFailed   bool
	startedHandlers    []WorkerStartedHandler
	registeredHandlers []WorkerRegisteredHandler

	consoleSession any // Store local session for CLI console
	transportRun   func(context.Context) error
}

func NewAgentServer(opts WorkerOptions) *AgentServer {
	opts = resolveWorkerOptions(opts)
	return &AgentServer{
		Options:        opts,
		activeJobs:     make(map[string]*JobContext),
		pendingAccepts: make(map[string]JobAcceptArguments),
		pendingTimers:  make(map[string]*time.Timer),
		workerID:       "unregistered",
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

func (s *AgentServer) SetTransportRunFunc(fn func(context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transportRun = fn
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

func (s *AgentServer) setConnectionFailed(failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connectionFailed = failed
}

func (s *AgentServer) hasConnectionFailed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connectionFailed
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
			Attributes: maps.Clone(jobCtx.AcceptArguments.Attributes),
		},
		Job:      jobCtx.Job,
		URL:      jobCtx.url,
		Token:    jobCtx.token,
		WorkerID: jobCtx.WorkerID(),
		FakeJob:  jobCtx.fakeJob,
	}
}

func jobLogValues(jobCtx *JobContext, values ...any) []any {
	if jobCtx == nil {
		if current, ok := GetCurrentJobContext(); ok {
			jobCtx = current
		} else {
			return values
		}
	}
	fields := jobCtx.LogContextFields()
	logValues := make([]any, 0, len(values)+len(fields)*2)
	logValues = append(logValues, values...)
	for key, value := range fields {
		if key == "" || value == nil {
			continue
		}
		logValues = append(logValues, key, value)
	}
	return logValues
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
	if apiSecret == "" {
		return nil, fmt.Errorf("api_secret is required to reload jobs")
	}
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
		jobCtx.process = s.newJobProcess()
		if info.Job.GetEnableRecording() {
			jobCtx.InitRecording(allRecordingOptions())
		}
		jobCtx.token = info.Token
		jobCtx.workerID = info.WorkerID
		jobCtx.AcceptArguments = JobAcceptArguments{
			Name:       info.AcceptArguments.Name,
			Identity:   info.AcceptArguments.Identity,
			Metadata:   info.AcceptArguments.Metadata,
			Attributes: info.AcceptArguments.Attributes,
		}
		jobCtx.fakeJob = info.FakeJob

		s.mu.Lock()
		if jobCtx.WorkerID() == "" {
			jobCtx.workerID = s.workerID
		}
		s.activeJobs[info.Job.Id] = jobCtx
		s.mu.Unlock()

		s.launchReloadedJob(ctx, jobCtx)
	}

	return nil
}

func (s *AgentServer) ExecuteRunningJob(ctx context.Context, info workeripc.RunningJobInfo) error {
	if info.Job == nil {
		return fmt.Errorf("running job info must include a job")
	}
	if s.entrypointFnc == nil {
		return workerReferenceError(rtcSessionRequiredMessage)
	}

	jobURL := info.URL
	if s.Options.WSRL != "" {
		jobURL = s.Options.WSRL
	}
	jobCtx := NewJobContext(info.Job, jobURL, s.Options.APIKey, s.Options.APISecret)
	jobCtx.process = s.newJobProcess()
	if info.Job.GetEnableRecording() {
		jobCtx.InitRecording(allRecordingOptions())
	}
	jobCtx.token = info.Token
	jobCtx.workerID = info.WorkerID
	jobCtx.AcceptArguments = JobAcceptArguments{
		Name:       info.AcceptArguments.Name,
		Identity:   info.AcceptArguments.Identity,
		Metadata:   info.AcceptArguments.Metadata,
		Attributes: info.AcceptArguments.Attributes,
	}
	jobCtx.fakeJob = info.FakeJob
	if jobCtx.WorkerID() == "" {
		jobCtx.workerID = s.workerID
	}

	s.mu.Lock()
	s.activeJobs[info.Job.Id] = jobCtx
	s.mu.Unlock()

	doneCh := make(chan error, 1)
	jobCtx.markEntrypointStarted()
	go func() {
		defer jobCtx.markEntrypointDone()
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Logger.Errorw("Running job entrypoint panicked", fmt.Errorf("%v", recovered), "jobId", info.Job.Id)
				doneCh <- fmt.Errorf("running job entrypoint panicked: %v", recovered)
			}
		}()
		doneCh <- s.runJobEntrypoint(jobCtx)
	}()

	select {
	case err := <-doneCh:
		if err != nil {
			logger.Logger.Errorw("Running job entrypoint failed", err, "jobId", info.Job.Id)
			s.finishJob(jobCtx)
			return err
		}
		select {
		case <-jobCtx.ShutdownDone():
		case <-ctx.Done():
			jobCtx.Shutdown("")
			s.finishJob(jobCtx)
			return ctx.Err()
		}
		s.finishJob(jobCtx)
		return nil
	case <-ctx.Done():
		jobCtx.Shutdown("")
		if !jobCtx.waitForEntrypointDone(localEntrypointCloseWait) {
			logger.Logger.Warnw("running job entrypoint did not exit before context cancellation finalized", nil, "jobId", info.Job.Id)
		}
		s.finishJob(jobCtx)
		return ctx.Err()
	}
}

func (s *AgentServer) launchReloadedJob(ctx context.Context, jobCtx *JobContext) {
	if s.entrypointFnc == nil {
		return
	}
	if jobCtx.process == nil {
		jobCtx.process = s.newJobProcess()
	}

	jobCtx.markEntrypointStarted()
	go func() {
		status := livekit.JobStatus_JS_SUCCESS
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Logger.Errorw("Reloaded job entrypoint panicked", fmt.Errorf("%v", recovered), "jobId", jobCtx.JobID())
				status = livekit.JobStatus_JS_FAILED
			}
			if status == livekit.JobStatus_JS_SUCCESS {
				select {
				case <-jobCtx.ShutdownDone():
				case <-ctx.Done():
					jobCtx.Shutdown("")
				}
				if !s.finishJob(jobCtx) {
					return
				}
			} else {
				s.finishJob(jobCtx)
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
		defer jobCtx.markEntrypointDone()
		if err := s.runJobEntrypoint(jobCtx); err != nil {
			logger.Logger.Errorw("Reloaded job entrypoint failed", err, "jobId", jobCtx.JobID())
			status = livekit.JobStatus_JS_FAILED
		}
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

func (s *AgentServer) startReloadIPCSessionFromEnv(ctx context.Context) {
	path := os.Getenv("RTP_AGENT_RELOAD_IPC")
	if path == "" {
		return
	}
	go func() {
		conn, err := workerReloadIPCDial("unix", path)
		if err != nil {
			logger.Logger.Errorw("failed to connect reload IPC", err, "path", path)
			return
		}
		defer conn.Close()
		if err := s.runReloadIPCSession(ctx, conn, 0, time.Now()); err != nil && !errors.Is(err, context.Canceled) {
			logger.Logger.Errorw("reload IPC session failed", err, "path", path)
		}
	}()
}

func (s *AgentServer) UpdateOptions(opts WorkerOptions) error {
	s.mu.Lock()
	if s.started() {
		s.mu.Unlock()
		return fmt.Errorf("cannot update options after starting the server")
	}
	current := s.Options
	s.mu.Unlock()

	updated := mergeWorkerOptions(current, opts)
	updated = resolveWorkerOptions(updated)
	if !validWorkerLogLevel(updated.LogLevel) {
		return invalidWorkerLogLevelError(updated.LogLevel)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started() {
		return fmt.Errorf("cannot update options after starting the server")
	}
	s.Options = updated
	return nil
}

func (s *AgentServer) started() bool {
	return s.running || s.conn != nil || s.httpServer != nil
}

func (s *AgentServer) beginRun() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started() {
		return fmt.Errorf("worker is already running")
	}
	s.running = true
	return nil
}

func (s *AgentServer) finishRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
}

func mergeWorkerOptions(current WorkerOptions, next WorkerOptions) WorkerOptions {
	if next.AgentName != "" {
		current.AgentName = next.AgentName
		current.AgentNameIsEnv = false
	}
	if next.WorkerType != "" {
		current.WorkerType = next.WorkerType
	}
	if next.MaxRetrySet || next.MaxRetry != 0 {
		current.MaxRetry = next.MaxRetry
		current.MaxRetrySet = true
	}
	if next.Version != "" {
		current.Version = next.Version
	}
	if next.Host != "" {
		current.Host = next.Host
	}
	if next.PortSet || next.Port != 0 {
		current.Port = next.Port
		current.PortSet = true
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
	if next.HealthCheck != nil {
		current.HealthCheck = next.HealthCheck
	}
	if next.SetupFunc != nil {
		current.SetupFunc = next.SetupFunc
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
	if next.HTTPProxySet || next.HTTPProxy != "" {
		current.HTTPProxy = next.HTTPProxy
		current.HTTPProxySet = true
	}
	if next.UserArguments != nil {
		current.UserArguments = next.UserArguments
	}
	if next.PrometheusPortSet || next.PrometheusPort != 0 {
		current.PrometheusPort = next.PrometheusPort
		current.PrometheusPortSet = true
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
	if next.LoadThresholdSet || next.LoadThreshold != 0 {
		current.LoadThreshold = next.LoadThreshold
		current.LoadThresholdSet = true
	}
	if next.JobMemoryWarnMBSet || next.JobMemoryWarnMB != 0 {
		current.JobMemoryWarnMB = next.JobMemoryWarnMB
		current.JobMemoryWarnMBSet = true
	}
	if next.JobMemoryLimitMBSet || next.JobMemoryLimitMB != 0 {
		current.JobMemoryLimitMB = next.JobMemoryLimitMB
		current.JobMemoryLimitMBSet = true
	}
	if next.NumIdleProcessesSet || next.NumIdleProcesses != 0 {
		current.NumIdleProcesses = next.NumIdleProcesses
		current.NumIdleProcessesSet = true
	}
	if next.DrainTimeoutSecondsSet || next.DrainTimeoutSeconds != 0 {
		current.DrainTimeoutSeconds = next.DrainTimeoutSeconds
		current.DrainTimeoutSecondsSet = true
	}
	if next.SessionEndTimeoutSecondsSet || next.SessionEndTimeoutSeconds != 0 {
		current.SessionEndTimeoutSeconds = next.SessionEndTimeoutSeconds
		current.SessionEndTimeoutSecondsSet = true
	}
	if next.ShutdownProcessTimeoutSecondsSet || next.ShutdownProcessTimeoutSeconds != 0 {
		current.ShutdownProcessTimeoutSeconds = next.ShutdownProcessTimeoutSeconds
		current.ShutdownProcessTimeoutSecondsSet = true
	}
	if next.InitializeProcessTimeoutSecondsSet || next.InitializeProcessTimeoutSeconds != 0 {
		current.InitializeProcessTimeoutSeconds = next.InitializeProcessTimeoutSeconds
		current.InitializeProcessTimeoutSecondsSet = true
	}
	if next.Permissions != nil {
		current.Permissions = next.Permissions
	}
	return current
}

func resolveWorkerOptions(opts WorkerOptions) WorkerOptions {
	opts.Transport = NormalizeWorkerTransport(string(opts.Transport))
	if !opts.DevMode {
		opts.DevMode = utils.IsDevMode()
	}
	if opts.WorkerType == "" {
		opts.WorkerType = WorkerTypeRoom
	}
	if opts.Version == "" {
		opts.Version = defaultWorkerVersion
	}
	if opts.MaxRetry == 0 && !opts.MaxRetrySet {
		opts.MaxRetry = defaultMaxRetry
	}
	if opts.JobMemoryWarnMB == 0 && !opts.JobMemoryWarnMBSet {
		opts.JobMemoryWarnMB = defaultJobMemoryWarn
	}
	if opts.DrainTimeoutSeconds == 0 && !opts.DrainTimeoutSecondsSet {
		opts.DrainTimeoutSeconds = defaultDrainTimeout
	}
	if opts.SessionEndTimeoutSeconds == 0 && !opts.SessionEndTimeoutSecondsSet {
		opts.SessionEndTimeoutSeconds = defaultSessionEnd
	}
	if opts.ShutdownProcessTimeoutSeconds == 0 && !opts.ShutdownProcessTimeoutSecondsSet {
		opts.ShutdownProcessTimeoutSeconds = defaultProcessTimeout
	}
	if opts.InitializeProcessTimeoutSeconds == 0 && !opts.InitializeProcessTimeoutSecondsSet {
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
	if validWorkerLogLevel(opts.LogLevel) {
		opts.LogLevel = strings.ToUpper(opts.LogLevel)
	}
	if opts.Port == 0 && !opts.DevMode && !opts.PortSet {
		opts.Port = defaultProdHTTPPort
	}
	if opts.LoadThreshold == 0 && !opts.LoadThresholdSet {
		if opts.DevMode {
			opts.LoadThreshold = math.Inf(1)
		} else {
			opts.LoadThreshold = defaultLoadThreshold
		}
	}
	if opts.NumIdleProcesses == 0 && !opts.DevMode && !opts.NumIdleProcessesSet {
		opts.NumIdleProcesses = defaultNumIdleProcesses()
	}
	if opts.LoadFunc == nil && !opts.DevMode {
		opts.LoadFunc = defaultWorkerLoadFunc
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
	if opts.HTTPProxy == "" && !opts.HTTPProxySet {
		opts.HTTPProxy = os.Getenv("HTTPS_PROXY")
		if opts.HTTPProxy == "" {
			opts.HTTPProxy = os.Getenv("HTTP_PROXY")
		}
	}

	return opts
}

type workerMetadataResponse struct {
	AgentName       string  `json:"agent_name"`
	AgentNameIsEnv  bool    `json:"agent_name_is_env"`
	WorkerType      string  `json:"worker_type"`
	WorkerLoad      float64 `json:"worker_load"`
	ActiveJobs      int     `json:"active_jobs"`
	SDKVersion      string  `json:"sdk_version"`
	ProtocolVersion int     `json:"protocol_version"`
	ProjectType     string  `json:"project_type"`
	NodeName        string  `json:"node_name"`
	Hosted          bool    `json:"hosted"`
}

func (s *AgentServer) workerHTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if s.hasConnectionFailed() {
			http.Error(w, "failed to connect to livekit", http.StatusServiceUnavailable)
			return
		}
		if s.Options.HealthCheck != nil {
			if err := s.Options.HealthCheck(s); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/worker", func(w http.ResponseWriter, r *http.Request) {
		body := workerMetadataResponse{
			AgentName:       s.Options.AgentName,
			AgentNameIsEnv:  s.Options.AgentNameIsEnv,
			WorkerType:      livekit.JobType_name[int32(workerTypeToJobType(s.Options.WorkerType))],
			WorkerLoad:      s.currentLoad(),
			ActiveJobs:      s.activeJobCount(),
			SDKVersion:      s.Options.Version,
			ProtocolVersion: WorkerProtocolVersion,
			ProjectType:     "go",
			NodeName:        utils.NodeName(),
			Hosted:          utils.IsHosted(),
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
	ln, err := workerListen("tcp", addr)
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
	if s.Options.PrometheusPort == 0 && !s.Options.PrometheusPortSet {
		return nil, nil
	}
	server := telemetry.NewHttpServerWithListen(s.Options.Host, s.Options.PrometheusPort, workerPrometheusListen)
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
	}
	if s.Options.PrometheusMultiprocDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.Options.PrometheusMultiprocDir, 0o755); err != nil {
		return err
	}
	if err := os.Setenv("PROMETHEUS_MULTIPROC_DIR", s.Options.PrometheusMultiprocDir); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.Options.PrometheusMultiprocDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(s.Options.PrometheusMultiprocDir, entry.Name())
		if err := os.Remove(path); err != nil {
			logger.Logger.Warnw("failed to remove Prometheus multiprocess file", err, "path", path)
		}
	}
	return nil
}

func validWorkerLogLevel(logLevel string) bool {
	switch strings.ToUpper(strings.TrimSpace(logLevel)) {
	case "TRACE", "DEBUG", "INFO", "WARN", "ERROR", "CRITICAL":
		return true
	default:
		return false
	}
}

func invalidWorkerLogLevelError(logLevel string) error {
	return fmt.Errorf("%s '%s'. Valid levels: CRITICAL, DEBUG, ERROR, INFO, TRACE, WARN", "Invalid log level", logLevel)
}

func defaultNumIdleProcesses() int {
	cpus := runtime.NumCPU()
	if cpus > 4 {
		return 4
	}
	return cpus
}

type workerLoadCalculator struct {
	mu      sync.Mutex
	average *mathutil.MovingAverage
	sample  func() float64
}

func newWorkerLoadCalculator(sample func() float64) *workerLoadCalculator {
	if sample == nil {
		sample = func() float64 { return 0 }
	}
	return &workerLoadCalculator{
		average: mathutil.NewMovingAverage(5),
		sample:  sample,
	}
}

func (c *workerLoadCalculator) Load() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	sample := c.sample()
	if sample < 0 || math.IsNaN(sample) || math.IsInf(sample, 0) {
		c.average.Reset()
		return 0
	}
	c.average.AddSample(sample)
	return c.average.GetAvg()
}

func defaultWorkerLoadFunc(*AgentServer) float64 {
	defaultWorkerLoadMu.Lock()
	if defaultWorkerLoadCalc == nil {
		defaultWorkerLoadCalc = newWorkerLoadCalculator(defaultWorkerLoadSample)
	}
	calc := defaultWorkerLoadCalc
	defaultWorkerLoadMu.Unlock()
	return calc.Load()
}

type systemCPUSampler struct {
	mu    sync.Mutex
	total uint64
	idle  uint64
}

func newSystemCPUSampler() *systemCPUSampler {
	return &systemCPUSampler{}
}

func (s *systemCPUSampler) Sample() float64 {
	idle, total, err := readSystemCPUTimes()
	if err != nil {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	prevIdle, prevTotal := s.idle, s.total
	s.idle, s.total = idle, total
	if prevTotal == 0 || total <= prevTotal {
		return 0
	}
	totalDelta := total - prevTotal
	idleDelta := uint64(0)
	if idle > prevIdle {
		idleDelta = idle - prevIdle
	}
	if idleDelta >= totalDelta {
		return 0
	}
	return float64(totalDelta-idleDelta) / float64(totalDelta)
}

func readSystemCPUTimes() (idle uint64, total uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("unexpected /proc/stat cpu line")
	}
	for i, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		total += value
		if i == 3 || i == 4 {
			idle += value
		}
	}
	return idle, total, nil
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
	return workerlivekit.AgentWebSocketURL(rawURL, workerToken)
}

func agentIdentityForJobID(jobID string) string {
	return "agent-" + jobID
}

func availabilityResponseForAccept(req *livekit.AvailabilityRequest, args JobAcceptArguments, agentName string) *livekit.WorkerMessage {
	if args.Identity == "" {
		args.Identity = agentIdentityForJobID(req.Job.Id)
	}
	attributes := make(map[string]string, len(args.Attributes)+1)
	attributes[participantAttributeAgentName] = agentName
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
	load := s.currentLoad()
	if status == livekit.WorkerStatus_WS_AVAILABLE && !s.availableForJobWithLoad(load) {
		status = livekit.WorkerStatus_WS_FULL
	}
	return &livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_UpdateWorker{
			UpdateWorker: &livekit.UpdateWorkerStatus{
				Status:   &status,
				Load:     float32(load),
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
		return workerReferenceError(duplicateRTCSessionMessage)
	}
	s.entrypointFnc = entrypoint
	s.requestFnc = request
	s.sessionEndFnc = sessionEnd
	if agentName := os.Getenv("LIVEKIT_AGENT_NAME_OVERRIDE"); agentName != "" {
		s.Options.AgentName = agentName
		s.Options.AgentNameIsEnv = true
	} else if s.Options.AgentName == "" {
		if agentName := os.Getenv("LIVEKIT_AGENT_NAME"); agentName != "" {
			s.Options.AgentName = agentName
			s.Options.AgentNameIsEnv = true
		} else {
			s.Options.AgentNameIsEnv = false
		}
	}
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
	return len(s.activeJobs) + len(s.pendingAccepts) + s.reservedSlots
}

func (s *AgentServer) activeJobCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, jobCtx := range s.activeJobs {
		if jobCtx == nil || jobCtx.IsFakeJob() {
			continue
		}
		count++
	}
	return count
}

func (s *AgentServer) currentLoad() float64 {
	if s.Options.LoadFunc == nil {
		return 0
	}
	return s.Options.LoadFunc(s)
}

func (s *AgentServer) effectiveLoadWithLoad(load float64) float64 {
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
	return s.effectiveLoadWithLoad(s.currentLoad()) < threshold
}

func (s *AgentServer) availableForJobWithLoad(load float64) bool {
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
	return s.effectiveLoadWithLoad(load) < threshold
}

func (s *AgentServer) validateRunPreconditions() error {
	s.Options = resolveWorkerOptions(s.Options)
	if s.Options.WorkerToken != "" {
		s.Options.LoadFunc = defaultWorkerLoadFunc
		s.Options.LoadThreshold = defaultLoadThreshold
	}
	if s.Options.LoadThreshold > 1 && !math.IsInf(s.Options.LoadThreshold, 1) && !s.Options.DevMode {
		logger.Logger.Warnw("load_threshold in prod env should be less than 1", nil, "currentValue", s.Options.LoadThreshold)
	}
	if !validWorkerLogLevel(s.Options.LogLevel) {
		return invalidWorkerLogLevelError(s.Options.LogLevel)
	}
	if s.entrypointFnc == nil {
		return workerReferenceError(rtcSessionRequiredMessage)
	}
	transport := NormalizeWorkerTransport(string(s.Options.Transport))
	if err := ValidateWorkerTransport(transport); err != nil {
		return err
	}
	if transport == WorkerTransportAgora {
		return s.Options.Agora.Validate()
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

func (s *AgentServer) validateUnregisteredRunPreconditions() error {
	s.Options = resolveWorkerOptions(s.Options)
	if !validWorkerLogLevel(s.Options.LogLevel) {
		return invalidWorkerLogLevelError(s.Options.LogLevel)
	}
	if s.entrypointFnc == nil {
		return workerReferenceError(rtcSessionRequiredMessage)
	}
	return nil
}

func (s *AgentServer) Run(ctx context.Context) error {
	if err := s.beginRun(); err != nil {
		return err
	}
	defer s.finishRun()

	if NormalizeWorkerTransport(string(s.Options.Transport)) == WorkerTransportAgora {
		s.mu.Lock()
		transportRun := s.transportRun
		s.mu.Unlock()
		if transportRun != nil {
			return transportRun(ctx)
		}
		return fmt.Errorf("agora transport run function is required")
	}

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
	at.SetVideoGrant(&auth.VideoGrant{Agent: true}).SetValidFor(time.Hour)
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
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
	}()

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
	s.startReloadIPCSessionFromEnv(ctx)

	return s.runWorkerMessageLoop(ctx, conn.ReadMessage, conn.Close)
}

func (s *AgentServer) RunUnregistered(ctx context.Context) error {
	if err := s.beginRun(); err != nil {
		return err
	}
	defer s.finishRun()

	if err := s.validateUnregisteredRunPreconditions(); err != nil {
		return err
	}

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

	s.emitWorkerStarted()
	<-ctx.Done()
	return ctx.Err()
}

func (s *AgentServer) runWorkerMessageLoop(ctx context.Context, readMessage func() (int, []byte, error), closeConn func() error) error {
	for {
		readDone := make(chan struct {
			msgType int
			data    []byte
			err     error
		}, 1)
		go func() {
			msgType, data, err := readMessage()
			readDone <- struct {
				msgType int
				data    []byte
				err     error
			}{msgType: msgType, data: data, err: err}
		}()

		select {
		case <-ctx.Done():
			if closeConn != nil {
				_ = closeConn()
			}
			return ctx.Err()
		case result := <-readDone:
			if result.err != nil {
				return result.err
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

func (s *AgentServer) emitWorkerStarted() {
	s.mu.Lock()
	handlers := append([]WorkerStartedHandler(nil), s.startedHandlers...)
	s.mu.Unlock()

	for _, handler := range handlers {
		callWorkerStartedHandler(handler)
	}
}

func callWorkerStartedHandler(handler WorkerStartedHandler) {
	defer func() {
		if r := recover(); r != nil {
			logger.Logger.Errorw("Worker started handler failed", fmt.Errorf("panic: %v", r))
		}
	}()
	handler()
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
			s.setConnectionFailed(false)
			return conn, res, nil
		}

		if retryCount >= s.Options.MaxRetry {
			s.setConnectionFailed(true)
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
		callWorkerRegisteredHandler(handler, workerID, serverInfo)
	}
}

func callWorkerRegisteredHandler(handler WorkerRegisteredHandler, workerID string, serverInfo *livekit.ServerInfo) {
	defer func() {
		if r := recover(); r != nil {
			logger.Logger.Errorw("Worker registered handler failed", fmt.Errorf("panic: %v", r), "workerId", workerID)
		}
	}()
	handler(workerID, serverInfo)
}

func (s *AgentServer) reportActiveJobs() {
	s.mu.Lock()
	jobIDs := make([]string, 0, len(s.activeJobs))
	for jobID, jobCtx := range s.activeJobs {
		if jobCtx.IsFakeJob() {
			continue
		}
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
	jobCtx.process = s.newJobProcess()
	if req.Job.GetEnableRecording() {
		jobCtx.InitRecording(allRecordingOptions())
	}
	jobCtx.token = req.GetToken()

	s.mu.Lock()
	args, accepted := s.pendingAccepts[req.Job.Id]
	if !accepted {
		s.mu.Unlock()
		logger.Logger.Warnw("received assignment for unknown job", nil, "jobId", req.Job.Id)
		return
	}

	jobCtx.workerID = s.workerID
	jobCtx.AcceptArguments = args
	jobCtx.LogContextFields()["worker_id"] = jobCtx.WorkerID()
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
		jobCtx.markEntrypointStarted()
		go func() {
			status := livekit.JobStatus_JS_SUCCESS
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Logger.Errorw("Job entrypoint panicked", fmt.Errorf("%v", recovered), jobLogValues(jobCtx, "jobId", req.Job.Id)...)
					status = livekit.JobStatus_JS_FAILED
				}
				if jobCtx.Terminated() {
					s.finishJob(jobCtx)
					return
				}
				if status == livekit.JobStatus_JS_SUCCESS {
					select {
					case <-jobCtx.ShutdownDone():
					case <-ctx.Done():
						jobCtx.Shutdown("")
					}
					if !s.finishJob(jobCtx) {
						return
					}
				} else {
					if err := s.sendWorkerMessage(jobStatusMessage(req.Job.Id, status)); err != nil {
						logger.Logger.Errorw("failed to update job status", err, jobLogValues(jobCtx, "jobId", req.Job.Id)...)
					}
					s.finishJob(jobCtx)
					return
				}
				if err := s.sendWorkerMessage(jobStatusMessage(req.Job.Id, status)); err != nil {
					logger.Logger.Errorw("failed to update job status", err, jobLogValues(jobCtx, "jobId", req.Job.Id)...)
				}
			}()
			defer jobCtx.markEntrypointDone()

			if err := s.runJobEntrypoint(jobCtx); err != nil {
				logger.Logger.Errorw("Job entrypoint failed", err, jobLogValues(jobCtx, "jobId", req.Job.Id)...)
				status = livekit.JobStatus_JS_FAILED
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
		jobCtx.markTerminated()
		jobCtx.Shutdown("")
		if !jobCtx.waitForEntrypointDone(localEntrypointCloseWait) {
			logger.Logger.Warnw("job entrypoint did not exit before termination finalized", nil, "jobId", req.JobId)
		}
		s.finishJob(jobCtx)
	}
}

// ExecuteLocalJob runs a job locally without connecting to the worker service, useful for the CLI console
func (s *AgentServer) ExecuteLocalJob(ctx context.Context, roomName string, participantIdentity string) error {
	return s.ExecuteLocalJobWithOptions(ctx, roomName, participantIdentity, LocalJobOptions{FakeJob: true})
}

func (s *AgentServer) ExecuteLocalJobWithOptions(ctx context.Context, roomName string, participantIdentity string, options LocalJobOptions) error {
	if options.Token != "" {
		verifier, err := auth.ParseAPIToken(options.Token)
		if err != nil {
			return fmt.Errorf("invalid local job token: %w", err)
		}
		participantIdentity = verifier.Identity()
	}
	if !options.FakeJob && participantIdentity == "" && options.Token == "" {
		return fmt.Errorf("agent_identity is None but fake_job is False")
	}
	if !options.FakeJob && options.RoomInfo == nil {
		return fmt.Errorf("room_info is None but fake_job is False")
	}
	if s.entrypointFnc == nil {
		return workerReferenceError(rtcSessionRequiredMessage)
	}
	jobCtx := newLocalJobContextWithOptions(roomName, participantIdentity, s.Options, options)
	if options == (LocalJobOptions{FakeJob: true}) {
		jobCtx = newLocalJobContext(roomName, participantIdentity, s.Options)
	}
	jobCtx.workerID = s.workerID
	jobCtx.LogContextFields()["worker_id"] = jobCtx.WorkerID()
	shutdownCh := make(chan struct{})
	_ = jobCtx.AddShutdownCallback(func() {
		close(shutdownCh)
	})
	entrypointDone := make(chan struct{})

	s.mu.Lock()
	s.activeJobs[jobCtx.Job.Id] = jobCtx
	s.mu.Unlock()

	entrypoint := func() error {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Logger.Errorw("Local job entrypoint panicked", fmt.Errorf("%v", recovered), jobLogValues(jobCtx, "jobId", jobCtx.Job.Id)...)
				jobCtx.Shutdown("job crashed")
				panic(recovered)
			}
		}()
		if err := s.runJobEntrypoint(jobCtx); err != nil {
			logger.Logger.Errorw("Local job entrypoint failed", err, jobLogValues(jobCtx, "jobId", jobCtx.Job.Id)...)
			jobCtx.Shutdown("job failed")
			return err
		}
		return nil
	}
	if err := s.launchLocalJobExecutor(ctx, jobCtx, entrypoint, entrypointDone); err != nil {
		s.finishJob(jobCtx)
		return err
	}

	// Block until the local job is canceled or the job context shuts down.
	select {
	case <-ctx.Done():
		jobCtx.Shutdown("")
		waitForLocalEntrypoint(entrypointDone)
	case <-shutdownCh:
	}
	s.finishJob(jobCtx)
	if options.SessionReportPath != "" {
		return saveSessionReport(options.SessionReportPath, jobCtx.Report)
	}
	if jobCtx.SessionDirectory() != "" {
		return saveSessionReport(filepath.Join(jobCtx.SessionDirectory(), "session_report.json"), jobCtx.Report)
	}
	return nil
}

func (s *AgentServer) launchLocalJobExecutor(ctx context.Context, jobCtx *JobContext, entrypoint func() error, entrypointDone chan<- struct{}) error {
	info := runningJobInfoFromContext(jobCtx)
	if s.Options.NumIdleProcessesSet && s.Options.NumIdleProcesses > 0 {
		pool := newLocalProcPool(s.Options.NumIdleProcesses, workeripc.ExecutorTypeThread, entrypoint)
		pool.SetTargetIdleProcesses(s.Options.NumIdleProcesses)
		pool.SetCloseTimeout(time.Duration(s.Options.ShutdownProcessTimeoutSeconds * float64(time.Second)))
		if err := pool.Start(ctx); err != nil {
			_ = pool.Close()
			return err
		}
		if err := pool.LaunchRunningJob(ctx, info); err != nil {
			_ = pool.Close()
			return err
		}
		go func() {
			if executor := pool.GetByJobID(jobCtx.Job.Id); executor != nil {
				_ = executor.Close(context.Background())
			}
			_ = pool.Close()
			close(entrypointDone)
		}()
		return nil
	}

	executor := newLocalJobExecutor("local_"+jobCtx.Job.Id, entrypoint)
	if err := executor.LaunchRunningJob(ctx, info); err != nil {
		return err
	}
	go func() {
		_ = executor.Close(context.Background())
		close(entrypointDone)
	}()
	return nil
}

func waitForLocalEntrypoint(done <-chan struct{}) {
	timer := time.NewTimer(localEntrypointCloseWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		logger.Logger.Warnw("local job entrypoint did not exit in time", nil)
	}
}

func (s *AgentServer) newJobProcess() *JobProcess {
	return NewJobProcess(JobExecutorTypeThread, s.Options.UserArguments, s.Options.HTTPProxy)
}

func (s *AgentServer) runJobEntrypoint(jobCtx *JobContext) error {
	if jobCtx == nil {
		return fmt.Errorf("job context is nil")
	}
	if jobCtx.process == nil {
		jobCtx.process = s.newJobProcess()
	}
	if s.Options.SetupFunc != nil {
		if err := s.Options.SetupFunc(jobCtx.process); err != nil {
			return fmt.Errorf("worker setup failed: %w", err)
		}
	}
	return runWithJobContext(jobCtx, func() error {
		return s.entrypointFnc(jobCtx)
	})
}

func (s *AgentServer) finishJob(jobCtx *JobContext) bool {
	if jobCtx == nil || jobCtx.Job == nil {
		return false
	}
	finalized := false
	jobCtx.finishOnce.Do(func() {
		finalized = true
	})
	if !finalized {
		return false
	}

	s.mu.Lock()
	delete(s.activeJobs, jobCtx.Job.Id)
	s.mu.Unlock()

	s.runSessionEnd(jobCtx)

	jobCtx.Shutdown("")
	s.uploadJobSessionReport(jobCtx)
	return true
}

func (s *AgentServer) uploadJobSessionReport(jobCtx *JobContext) {
	if !shouldUploadJobSessionReport(jobCtx) {
		return
	}
	go func() {
		err := uploadSessionReport(
			jobCtx.url,
			s.Options.APIKey,
			s.Options.APISecret,
			s.Options.AgentName,
			jobCtx.Report,
		)
		if err != nil {
			logger.Logger.Errorw("failed to upload session report", err, jobLogValues(jobCtx, "jobId", jobCtx.Job.GetId())...)
		}
	}()
}

func shouldUploadJobSessionReport(jobCtx *JobContext) bool {
	if jobCtx == nil || jobCtx.Job == nil || jobCtx.IsFakeJob() || jobCtx.Report == nil {
		return false
	}
	return hasSessionRecordingOption(jobCtx.Report.RecordingOptions) || hasSessionEvaluationReport(jobCtx.Report)
}

func hasSessionRecordingOption(options agent.RecordingOptions) bool {
	return options.Audio || options.Traces || options.Logs || options.Transcript
}

func hasSessionEvaluationReport(report *agent.SessionReport) bool {
	if report == nil || report.Tagger == nil {
		return false
	}
	return report.Tagger.Outcome() != "" || len(report.Tagger.Evaluations()) > 0
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
			logger.Logger.Errorw("Session end callback failed", err, jobLogValues(jobCtx, "jobId", jobCtx.Job.Id)...)
		}
		return
	}

	select {
	case err := <-doneCh:
		if err != nil {
			logger.Logger.Errorw("Session end callback failed", err, jobLogValues(jobCtx, "jobId", jobCtx.Job.Id)...)
		}
	case <-time.After(timeout):
		logger.Logger.Errorw("Session end callback timed out", nil, jobLogValues(jobCtx, "jobId", jobCtx.Job.Id, "timeout", timeout)...)
	}
}

func saveSessionReport(path string, report *agent.SessionReport) error {
	if report == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create session report directory: %w", err)
	}
	reportBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session report: %w", err)
	}
	if err := os.WriteFile(path, reportBytes, 0o644); err != nil {
		return fmt.Errorf("write session report: %w", err)
	}
	return nil
}

func allRecordingOptions() agent.RecordingOptions {
	return agent.RecordingOptions{
		Audio:      true,
		Traces:     true,
		Logs:       true,
		Transcript: true,
	}
}

func newLocalJobContext(roomName string, participantIdentity string, opts WorkerOptions) *JobContext {
	return newLocalJobContextWithOptions(roomName, participantIdentity, opts, LocalJobOptions{FakeJob: true})
}

func newLocalJobContextWithOptions(roomName string, participantIdentity string, opts WorkerOptions, options LocalJobOptions) *JobContext {
	opts = resolveWorkerOptions(opts)
	token := options.Token
	if token != "" {
		if verifier, err := auth.ParseAPIToken(token); err == nil {
			participantIdentity = verifier.Identity()
		}
	}
	jobIDPrefix := "job-"
	if options.FakeJob {
		jobIDPrefix = "mock-job-"
	}
	job := &livekit.Job{
		Id: mathutil.ShortUUID(jobIDPrefix),
		Room: &livekit.Room{
			Name: roomName,
			Sid:  mathutil.ShortUUID("SRM_"),
		},
		Type: livekit.JobType_JT_ROOM,
	}
	if options.RoomInfo != nil {
		job.Room = options.RoomInfo
	}

	if participantIdentity == "" {
		participantIdentity = mathutil.ShortUUID("fake-agent-")
	}
	jobCtx := NewJobContext(job, opts.WSRL, opts.APIKey, opts.APISecret)
	jobCtx.AcceptArguments = JobAcceptArguments{Identity: participantIdentity}
	jobCtx.fakeJob = options.FakeJob
	if hasSessionRecordingOption(options.RecordingOptions) {
		jobCtx.InitRecording(options.RecordingOptions)
	}
	jobCtx.SetSessionDirectory(options.SessionDirectory)
	jobCtx.process = NewJobProcess(JobExecutorTypeThread, opts.UserArguments, opts.HTTPProxy)
	if token != "" {
		jobCtx.token = token
	} else if opts.APIKey != "" && opts.APISecret != "" {
		generatedToken, err := auth.NewAccessToken(opts.APIKey, opts.APISecret).
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
			jobCtx.token = generatedToken
		}
	}
	return jobCtx
}
