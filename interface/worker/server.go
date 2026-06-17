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
	"os"
	"path/filepath"
	"runtime"
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
	"github.com/gorilla/websocket"
)

type WorkerType = workerlivekit.WorkerType

const (
	WorkerTypeRoom      = workerlivekit.WorkerTypeRoom
	WorkerTypePublisher = workerlivekit.WorkerTypePublisher

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

type WorkerPermissions = workerlivekit.WorkerPermissions

type WorkerStartedHandler func()

type WorkerRegisteredInfo struct {
	WorkerID string
}

type WorkerRegisteredInfoHandler func(WorkerRegisteredInfo)

type WorkerRegisteredHandler = workerlivekit.WorkerRegisteredHandler

type WorkerInfo struct {
	HTTPPort    int
	CloudAgents bool
}

type LocalJobOptions = workerlivekit.LocalJobOptions

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

	activeJobs             map[string]*JobContext
	pendingAccepts         map[string]JobAcceptArguments
	pendingTimers          map[string]*time.Timer
	reservedSlots          int
	draining               bool
	running                bool
	mu                     sync.Mutex
	conn                   *websocket.Conn
	httpServer             *http.Server
	httpPort               int
	prometheusServer       *telemetry.HttpServer
	workerMessageSink      func(*workerlivekit.WorkerMessage) error
	workerID               string
	connectionFailed       bool
	startedHandlers        []WorkerStartedHandler
	registeredInfoHandlers []WorkerRegisteredInfoHandler
	registeredHandlers     []WorkerRegisteredHandler

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

func (s *AgentServer) OnWorkerRegisteredInfo(handler WorkerRegisteredInfoHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.registeredInfoHandlers = append(s.registeredInfoHandlers, handler)
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
	return workeripc.FromLiveKitRunningJobInfo(workerlivekit.RunningJobInfoSnapshot(workerlivekit.RunningJobInfoOptions{
		AcceptArguments: workerlivekit.JobAcceptArguments{
			Name:       jobCtx.AcceptArguments.Name,
			Identity:   jobCtx.AcceptArguments.Identity,
			Metadata:   jobCtx.AcceptArguments.Metadata,
			Attributes: jobCtx.AcceptArguments.Attributes,
		},
		Job:      jobCtx.Job,
		URL:      jobCtx.url,
		Token:    jobCtx.token,
		WorkerID: jobCtx.WorkerID(),
		FakeJob:  jobCtx.fakeJob,
	}))
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

func (s *AgentServer) ReloadRunningJobs(ctx context.Context, jobs []workeripc.RunningJobInfo, now time.Time) error {
	refreshed, err := workerlivekit.RefreshRunningJobsForReload(workeripc.ToLiveKitRunningJobInfos(jobs), s.Options.APISecret, now)
	if err != nil {
		return err
	}

	for _, info := range refreshed {
		if info.Job == nil {
			continue
		}

		reloadedJob := workerlivekit.ReloadedJobContextValues(workerlivekit.ReloadedJobContextValueOptions{
			Info:            info,
			OverrideURL:     s.Options.WSRL,
			DefaultWorkerID: s.workerID,
		})
		jobCtx := NewJobContext(reloadedJob.Job, reloadedJob.URL, s.Options.APIKey, s.Options.APISecret)
		jobCtx.process = s.newJobProcess()
		if reloadedJob.EnableRecording {
			jobCtx.InitRecording(workerlivekit.AllRecordingOptions())
		}
		jobCtx.token = reloadedJob.Token
		jobCtx.workerID = reloadedJob.WorkerID
		jobCtx.AcceptArguments = reloadedJob.AcceptArguments
		jobCtx.fakeJob = reloadedJob.FakeJob

		s.mu.Lock()
		s.activeJobs[reloadedJob.JobID] = jobCtx
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

	runningJob := workerlivekit.RunningJobContextValues(workerlivekit.RunningJobContextValueOptions{
		Info:            workeripc.ToLiveKitRunningJobInfo(info),
		OverrideURL:     s.Options.WSRL,
		DefaultWorkerID: s.workerID,
	})
	jobCtx := NewJobContext(runningJob.Job, runningJob.URL, s.Options.APIKey, s.Options.APISecret)
	jobCtx.process = s.newJobProcess()
	if runningJob.EnableRecording {
		jobCtx.InitRecording(workerlivekit.AllRecordingOptions())
	}
	jobCtx.token = runningJob.Token
	jobCtx.workerID = runningJob.WorkerID
	jobCtx.AcceptArguments = runningJob.AcceptArguments
	jobCtx.fakeJob = runningJob.FakeJob

	s.mu.Lock()
	s.activeJobs[runningJob.JobID] = jobCtx
	s.mu.Unlock()

	return workerlivekit.RunRunningJobEntrypointLifecycle(workerlivekit.RunningJobEntrypointLifecycleOptions{
		Context:     ctx,
		MarkStarted: jobCtx.markEntrypointStarted,
		Entrypoint: func() error {
			return s.runJobEntrypoint(jobCtx)
		},
		MarkDone:     jobCtx.markEntrypointDone,
		ShutdownDone: jobCtx.ShutdownDone(),
		Shutdown: func(reason string) {
			jobCtx.Shutdown(reason)
		},
		WaitEntrypointDone: jobCtx.waitForEntrypointDone,
		CloseWait:          localEntrypointCloseWait,
		Finish: func() bool {
			return s.finishJob(jobCtx)
		},
		OnPanic: func(recovered any) {
			logger.Logger.Errorw("Running job entrypoint panicked", fmt.Errorf("%v", recovered), "jobId", runningJob.JobID)
		},
		OnError: func(err error) {
			logger.Logger.Errorw("Running job entrypoint failed", err, "jobId", runningJob.JobID)
		},
		OnCancelTimeout: func() {
			logger.Logger.Warnw("running job entrypoint did not exit before context cancellation finalized", nil, "jobId", runningJob.JobID)
		},
	})
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
		workerlivekit.RunReloadedJobEntrypointLifecycle(workerlivekit.ReloadedJobEntrypointLifecycleOptions{
			Context: ctx,
			Entrypoint: func() error {
				return s.runJobEntrypoint(jobCtx)
			},
			MarkDone: jobCtx.markEntrypointDone,
			OnResult: func(result workerlivekit.EntrypointResult) {
				if result.Recovered != nil {
					logger.Logger.Errorw("Reloaded job entrypoint panicked", fmt.Errorf("%v", result.Recovered), "jobId", jobCtx.JobID())
				}
				if result.Err != nil {
					logger.Logger.Errorw("Reloaded job entrypoint failed", result.Err, "jobId", jobCtx.JobID())
				}
			},
			ShutdownDone: jobCtx.ShutdownDone(),
			Shutdown: func(reason string) {
				jobCtx.Shutdown(reason)
			},
			Finish: func() bool {
				return s.finishJob(jobCtx)
			},
			SendStatus: func(status workerlivekit.JobStatus) error {
				if err := s.sendWorkerMessage(workerlivekit.JobStatusMessage(jobCtx.JobID(), status)); err != nil {
					logger.Logger.Errorw("failed to update reloaded job status", err, "jobId", jobCtx.JobID())
					return err
				}
				return nil
			},
			OnStatusSkipped: func() {
				logger.Logger.Debugw("reload job status skipped after context cancellation", "jobId", jobCtx.JobID())
			},
		})
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
	if !opts.DevMode && opts.Transport == WorkerTransportLiveKit {
		opts.DevMode = utils.IsDevMode()
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
	if opts.LogLevel == "" && opts.Transport == WorkerTransportLiveKit {
		opts.LogLevel = workerlivekit.WorkerLogLevelFromEnv(nil)
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
	if opts.Transport == WorkerTransportLiveKit {
		if opts.WorkerType == "" {
			opts.WorkerType = WorkerTypeRoom
		}
		if opts.Permissions == nil {
			opts.Permissions = workerlivekit.DefaultWorkerPermissions()
		}
		livekitOptions := workerlivekit.ResolveWorkerConnectionOptions(workerlivekit.WorkerConnectionOptions{
			WSURL:          opts.WSURL,
			LegacyWSURL:    opts.WSRL,
			APIKey:         opts.APIKey,
			APISecret:      opts.APISecret,
			WorkerToken:    opts.WorkerToken,
			AgentName:      opts.AgentName,
			AgentNameIsEnv: opts.AgentNameIsEnv,
		})
		opts.WSURL = livekitOptions.WSURL
		opts.WSRL = livekitOptions.WSURL
		opts.APIKey = livekitOptions.APIKey
		opts.APISecret = livekitOptions.APISecret
		opts.WorkerToken = livekitOptions.WorkerToken
		opts.AgentName = livekitOptions.AgentName
		opts.AgentNameIsEnv = livekitOptions.AgentNameIsEnv
	}
	if opts.HTTPProxy == "" && !opts.HTTPProxySet {
		opts.HTTPProxy = os.Getenv("HTTPS_PROXY")
		if opts.HTTPProxy == "" {
			opts.HTTPProxy = os.Getenv("HTTP_PROXY")
		}
	}

	return opts
}

func (s *AgentServer) workerHTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if s.hasConnectionFailed() {
			http.Error(w, workerConnectionFailureMessage(s.Options.Transport), http.StatusServiceUnavailable)
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
		if NormalizeWorkerTransport(string(s.Options.Transport)) != WorkerTransportLiveKit {
			http.NotFound(w, r)
			return
		}
		body := workerlivekit.WorkerRuntimeMetadata(workerlivekit.WorkerRuntimeMetadataOptions{
			AgentName:       s.Options.AgentName,
			AgentNameIsEnv:  s.Options.AgentNameIsEnv,
			WorkerType:      string(s.Options.WorkerType),
			WorkerLoad:      s.currentLoad(),
			ActiveJobs:      s.activeJobCount(),
			SDKVersion:      s.Options.Version,
			ProtocolVersion: WorkerProtocolVersion,
		})
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			logger.Logger.Errorw("failed to encode worker metadata", err)
		}
	})
	return mux
}

func workerConnectionFailureMessage(transport WorkerTransport) string {
	if NormalizeWorkerTransport(string(transport)) == WorkerTransportLiveKit {
		return workerlivekit.WorkerConnectionFailureMessage()
	}
	return "failed to connect"
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

func (s *AgentServer) registerWorkerRequest() *workerlivekit.WorkerMessage {
	return workerlivekit.ServerRegisterWorkerMessage(workerlivekit.ServerRegisterWorkerMessageOptions{
		WorkerType:  s.Options.WorkerType,
		AgentName:   s.Options.AgentName,
		Version:     s.Options.Version,
		Permissions: s.Options.Permissions,
	})
}

func (s *AgentServer) availableWorkerStatusMessage() *workerlivekit.WorkerMessage {
	jobCount := uint32(s.activeJobCount())
	load := s.currentLoad()
	return workerlivekit.ServerAvailableWorkerStatusMessage(workerlivekit.ServerAvailableWorkerStatusMessageOptions{
		Load:         load,
		JobCount:     jobCount,
		CanAcceptJob: s.availableForJobWithLoad(load),
	})
}

func (s *AgentServer) drainingWorkerStatusMessage() *workerlivekit.WorkerMessage {
	return workerlivekit.ServerDrainingWorkerStatusMessage(uint32(s.activeJobCount()))
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
	if NormalizeWorkerTransport(string(s.Options.Transport)) == WorkerTransportLiveKit {
		agentName := workerlivekit.ResolveAgentNameFromEnv(workerlivekit.AgentNameEnvOptions{
			AgentName:      s.Options.AgentName,
			AgentNameIsEnv: s.Options.AgentNameIsEnv,
		})
		s.Options.AgentName = agentName.AgentName
		s.Options.AgentNameIsEnv = agentName.AgentNameIsEnv
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
	return workerlivekit.ValidateServerConnectionOptions(workerlivekit.ServerConnectionOptions{
		WSURL:     s.Options.WSRL,
		APIKey:    s.Options.APIKey,
		APISecret: s.Options.APISecret,
	})
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
		if err := s.validateRunPreconditions(); err != nil {
			return err
		}
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
	workerlivekit.ApplyServerConnectionEnv(workerlivekit.ServerConnectionEnvOptions{
		ServerConnectionOptions: workerlivekit.ServerConnectionOptions{
			WSURL:     s.Options.WSRL,
			APIKey:    s.Options.APIKey,
			APISecret: s.Options.APISecret,
		},
	})

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

	openResult, err := s.openWorkerWebSocket(ctx, workerlivekit.WorkerWebSocketOpenOptions{
		WSURL:       s.Options.WSRL,
		WorkerToken: s.Options.WorkerToken,
		APIKey:      s.Options.APIKey,
		APISecret:   s.Options.APISecret,
		TTL:         time.Hour,
		HTTPProxy:   s.Options.HTTPProxy,
		MaxRetry:    s.Options.MaxRetry,
	})
	if err != nil {
		return err
	}
	conn := openResult.Conn
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

	msg, err := workerlivekit.ExchangeInitialServerRegisterWebSocket(conn, s.registerWorkerRequest())
	if err != nil {
		return err
	}
	s.handleMessage(ctx, msg)

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
	return workerlivekit.RunServerMessageLoop(ctx, workerlivekit.ServerMessageLoopOptions{
		ReadMessage: readMessage,
		Close:       closeConn,
		Handle: func(msg *workerlivekit.ServerMessage) {
			s.handleMessage(ctx, msg)
		},
		OnDecodeError: func(err error) {
			logger.Logger.Errorw("Failed to unmarshal server message", err)
		},
	})
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
		return s.sendWorkerMessage(workerlivekit.ServerDrainingWorkerStatusMessage(uint32(s.activeJobCount())))
	}
	jobCount := uint32(s.activeJobCount())
	load := s.currentLoad()
	return s.sendWorkerMessage(workerlivekit.ServerAvailableWorkerStatusMessage(workerlivekit.ServerAvailableWorkerStatusMessageOptions{
		Load:         load,
		JobCount:     jobCount,
		CanAcceptJob: s.availableForJobWithLoad(load),
	}))
}

func (s *AgentServer) openWorkerWebSocket(ctx context.Context, opts workerlivekit.WorkerWebSocketOpenOptions) (workerlivekit.WorkerWebSocketOpenResult, error) {
	opts.Dial = workerDialContext
	opts.Sleep = workerRetrySleep
	result, err := workerlivekit.OpenServerWorkerWebSocket(ctx, opts)
	if err != nil {
		if result.ConnectFailed {
			s.setConnectionFailed(true)
		}
		return result, err
	}
	s.setConnectionFailed(false)
	return result, nil
}

func (s *AgentServer) handleMessage(ctx context.Context, msg *workerlivekit.ServerMessage) {
	workerlivekit.RouteServerWorkerMessage(workerlivekit.ServerMessageRouteOptions{
		Message: msg,
		OnRegister: func(event workerlivekit.WorkerRegisteredEvent) {
			logger.Logger.Infow("Worker Registered", "workerId", event.WorkerID, "serverInfo", event.ServerInfo)
			s.mu.Lock()
			s.workerID = event.WorkerID
			s.mu.Unlock()
			s.emitWorkerRegistered(event.WorkerID, event.ServerInfo)
			s.reportActiveJobs()
		},
		OnAvailability: func(req *workerlivekit.AvailabilityRequest) {
			s.handleAvailability(ctx, req)
		},
		OnAssignment: func(req *workerlivekit.JobAssignment) {
			s.handleAssignment(ctx, req)
		},
		OnTermination: s.handleTermination,
		OnUnknown: func() {
			logger.Logger.Warnw("Unhandled message type received", nil)
		},
	})
}

func (s *AgentServer) emitWorkerRegistered(workerID string, serverInfo *workerlivekit.ServerInfo) {
	s.mu.Lock()
	infoHandlers := append([]WorkerRegisteredInfoHandler(nil), s.registeredInfoHandlers...)
	handlers := append([]WorkerRegisteredHandler(nil), s.registeredHandlers...)
	s.mu.Unlock()

	info := WorkerRegisteredInfo{WorkerID: workerID}
	for _, handler := range infoHandlers {
		callWorkerRegisteredInfoHandler(handler, info)
	}
	for _, handler := range handlers {
		callWorkerRegisteredHandler(handler, workerID, serverInfo)
	}
}

func callWorkerRegisteredInfoHandler(handler WorkerRegisteredInfoHandler, info WorkerRegisteredInfo) {
	defer func() {
		if r := recover(); r != nil {
			logger.Logger.Errorw("Worker registered handler failed", fmt.Errorf("panic: %v", r), "workerId", info.WorkerID)
		}
	}()
	handler(info)
}

func callWorkerRegisteredHandler(handler WorkerRegisteredHandler, workerID string, serverInfo *workerlivekit.ServerInfo) {
	defer func() {
		if r := recover(); r != nil {
			logger.Logger.Errorw("Worker registered handler failed", fmt.Errorf("panic: %v", r), "workerId", workerID)
		}
	}()
	handler(workerID, serverInfo)
}

func (s *AgentServer) reportActiveJobs() {
	runningJobs := s.ActiveRunningJobs()
	livekitJobs := workeripc.ToLiveKitRunningJobInfos(runningJobs)
	jobIDs := workerlivekit.ServerMigratableRunningJobIDs(livekitJobs)

	if len(jobIDs) == 0 {
		return
	}

	if err := s.sendWorkerMessage(workerlivekit.ServerMigrateRunningJobsMessage(livekitJobs)); err != nil {
		logger.Logger.Errorw("failed to report active jobs", err, "jobIds", jobIDs)
	}
}

func (s *AgentServer) handleAvailability(ctx context.Context, req *workerlivekit.AvailabilityRequest) {
	go s.answerAvailability(ctx, req)
}

func (s *AgentServer) answerAvailability(ctx context.Context, req *workerlivekit.AvailabilityRequest) {
	availability := workerlivekit.AvailabilityInfo(req)
	jobID := availability.JobID
	logger.Logger.Infow("Received availability request", "jobId", jobID)

	workerlivekit.AnswerAvailabilityRequest(workerlivekit.AvailabilityAnswerOptions{
		Request:         req,
		AgentName:       s.Options.AgentName,
		AvailableForJob: s.availableForJob,
		ReserveSlot:     s.reserveAvailabilitySlot,
		ReleaseSlot:     s.releaseAvailabilitySlot,
		StoreAccept:     s.storePendingAccept,
		Send:            s.sendWorkerMessage,
		HandleRequest:   s.requestFnc,
		OnRequestError: func(err error, jobID string) {
			logger.Logger.Errorw("availability request callback failed", err, "jobId", jobID)
		},
		OnUnavailableRejectError: func(err error, jobID string) {
			logger.Logger.Errorw("failed to reject availability while unavailable", err, "jobId", jobID)
		},
	})
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

func (s *AgentServer) sendWorkerMessage(msg *workerlivekit.WorkerMessage) error {
	if s.workerMessageSink != nil {
		return s.workerMessageSink(msg)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return fmt.Errorf("worker websocket is not connected")
	}
	return workerlivekit.WriteWorkerMessageWebSocket(s.conn, msg)
}

func (s *AgentServer) storePendingAccept(jobID string, args JobAcceptArguments) {
	s.mu.Lock()
	workerlivekit.StorePendingAccept(workerlivekit.PendingAcceptStoreOptions{
		Pending: s.pendingAccepts,
		Timers:  s.pendingTimers,
		JobID:   jobID,
		Args:    args,
		Timeout: assignmentTimeout,
		OnTimeout: func(jobID string, timer *time.Timer) {
			s.mu.Lock()
			expired := workerlivekit.ExpirePendingAccept(s.pendingAccepts, s.pendingTimers, jobID, timer)
			s.mu.Unlock()
			if expired {
				logger.Logger.Warnw("assignment timed out after availability accept", nil, "jobId", jobID)
			}
		},
	})
	s.mu.Unlock()
}

func (s *AgentServer) handleAssignment(ctx context.Context, req *workerlivekit.JobAssignment) {
	assignment := workerlivekit.JobAssignmentInfo(req, s.Options.WSRL)
	jobID := assignment.JobID
	logger.Logger.Infow("Received job assignment", "jobId", jobID)
	s.mu.Lock()
	args, accepted := workerlivekit.AcceptPendingAssignment(s.pendingAccepts, s.pendingTimers, jobID)
	if !accepted {
		s.mu.Unlock()
		logger.Logger.Warnw("received assignment for unknown job", nil, "jobId", jobID)
		return
	}

	assignedJob := workerlivekit.AssignmentContextValues(workerlivekit.AssignmentContextValueOptions{
		Assignment:      assignment,
		AcceptArguments: args,
		WorkerID:        s.workerID,
	})
	jobCtx := NewJobContext(assignedJob.Job, assignedJob.URL, s.Options.APIKey, s.Options.APISecret)
	jobCtx.process = s.newJobProcess()
	if assignedJob.EnableRecording {
		jobCtx.InitRecording(workerlivekit.AllRecordingOptions())
	}
	jobCtx.token = assignedJob.Token
	jobCtx.workerID = assignedJob.WorkerID
	jobCtx.AcceptArguments = assignedJob.AcceptArguments
	jobCtx.LogContextFields()["worker_id"] = jobCtx.WorkerID()
	s.activeJobs[assignedJob.JobID] = jobCtx
	s.mu.Unlock()

	if err := s.sendWorkerMessage(workerlivekit.JobRunningMessage(jobID)); err != nil {
		logger.Logger.Errorw("failed to update job status", err, "jobId", jobID)
	}

	if s.entrypointFnc != nil {
		jobCtx.markEntrypointStarted()
		go func() {
			workerlivekit.RunJobEntrypointLifecycle(workerlivekit.JobEntrypointLifecycleOptions{
				Context: ctx,
				Entrypoint: func() error {
					return s.runJobEntrypoint(jobCtx)
				},
				MarkDone: jobCtx.markEntrypointDone,
				OnResult: func(result workerlivekit.EntrypointResult) {
					if result.Recovered != nil {
						logger.Logger.Errorw("Job entrypoint panicked", fmt.Errorf("%v", result.Recovered), jobLogValues(jobCtx, "jobId", jobID)...)
					}
					if result.Err != nil {
						logger.Logger.Errorw("Job entrypoint failed", result.Err, jobLogValues(jobCtx, "jobId", jobID)...)
					}
				},
				Terminated:   jobCtx.Terminated,
				ShutdownDone: jobCtx.ShutdownDone(),
				Shutdown: func(reason string) {
					jobCtx.Shutdown(reason)
				},
				Finish: func() bool {
					return s.finishJob(jobCtx)
				},
				SendStatus: func(status workerlivekit.JobStatus) error {
					err := s.sendWorkerMessage(workerlivekit.JobStatusMessage(jobID, status))
					if err != nil {
						logger.Logger.Errorw("failed to update job status", err, jobLogValues(jobCtx, "jobId", jobID)...)
					}
					return err
				},
			})
		}()
	}
}

func (s *AgentServer) handleTermination(req *workerlivekit.JobTermination) {
	termination := workerlivekit.JobTerminationInfo(req)
	jobID := termination.JobID
	logger.Logger.Infow("Received job termination", "jobId", jobID)

	s.mu.Lock()
	jobCtx, exists := s.activeJobs[jobID]
	if exists {
		delete(s.activeJobs, jobID)
	}
	s.mu.Unlock()

	plan := workerlivekit.JobTerminationPlanForActiveJob(exists)
	if plan.MarkTerminated {
		jobCtx.markTerminated()
	}
	if plan.Shutdown {
		jobCtx.Shutdown("")
	}
	if plan.WaitEntrypoint {
		if !jobCtx.waitForEntrypointDone(localEntrypointCloseWait) {
			logger.Logger.Warnw("job entrypoint did not exit before termination finalized", nil, "jobId", jobID)
		}
	}
	if plan.Finish {
		s.finishJob(jobCtx)
	}
}

// ExecuteLocalJob runs a job locally without connecting to the worker service, useful for the CLI console
func (s *AgentServer) ExecuteLocalJob(ctx context.Context, roomName string, participantIdentity string) error {
	return s.ExecuteLocalJobWithOptions(ctx, roomName, participantIdentity, workerlivekit.DefaultFakeLocalJobOptions())
}

func (s *AgentServer) ExecuteLocalJobWithOptions(ctx context.Context, roomName string, participantIdentity string, options LocalJobOptions) error {
	participantIdentity, err := workerlivekit.PrepareLocalJobRunOptions(participantIdentity, options)
	if err != nil {
		return err
	}
	if s.entrypointFnc == nil {
		return workerReferenceError(rtcSessionRequiredMessage)
	}
	jobCtx := newLocalJobContextWithOptions(roomName, participantIdentity, s.Options, options)
	if options == workerlivekit.DefaultFakeLocalJobOptions() {
		jobCtx = newLocalJobContext(roomName, participantIdentity, s.Options)
	}
	localJob := workerlivekit.LocalJobExecutorPlan(jobCtx.Job)
	jobCtx.workerID = s.workerID
	jobCtx.LogContextFields()["worker_id"] = jobCtx.WorkerID()
	shutdownCh := make(chan struct{})
	_ = jobCtx.AddShutdownCallback(func() {
		close(shutdownCh)
	})
	entrypointDone := make(chan struct{})

	s.mu.Lock()
	s.activeJobs[localJob.JobID] = jobCtx
	s.mu.Unlock()

	entrypoint := func() error {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Logger.Errorw("Local job entrypoint panicked", fmt.Errorf("%v", recovered), jobLogValues(jobCtx, "jobId", localJob.JobID)...)
				jobCtx.Shutdown("job crashed")
				panic(recovered)
			}
		}()
		if err := s.runJobEntrypoint(jobCtx); err != nil {
			logger.Logger.Errorw("Local job entrypoint failed", err, jobLogValues(jobCtx, "jobId", localJob.JobID)...)
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
	if reportPath := workerlivekit.LocalJobSessionReportPath(options, jobCtx.SessionDirectory()); reportPath != "" {
		return saveSessionReport(reportPath, jobCtx.Report)
	}
	return nil
}

func (s *AgentServer) launchLocalJobExecutor(ctx context.Context, jobCtx *JobContext, entrypoint func() error, entrypointDone chan<- struct{}) error {
	info := runningJobInfoFromContext(jobCtx)
	localJob := workerlivekit.LocalJobExecutorPlan(jobCtx.Job)
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
			if executor := pool.GetByJobID(localJob.JobID); executor != nil {
				_ = executor.Close(context.Background())
			}
			_ = pool.Close()
			close(entrypointDone)
		}()
		return nil
	}

	executor := newLocalJobExecutor(localJob.ExecutorID, entrypoint)
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
	if jobCtx == nil {
		return false
	}
	plan := workerlivekit.JobFinishPlan(jobCtx.Job)
	if !plan.Finish {
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
	delete(s.activeJobs, plan.JobID)
	s.mu.Unlock()

	s.runSessionEnd(jobCtx)

	jobCtx.Shutdown("")
	s.uploadJobSessionReport(jobCtx)
	return true
}

func (s *AgentServer) uploadJobSessionReport(jobCtx *JobContext) {
	plan := jobSessionReportUploadPlan(jobCtx, s.Options)
	if !plan.Upload {
		return
	}
	go func() {
		err := uploadSessionReport(
			plan.URL,
			plan.APIKey,
			plan.APISecret,
			plan.AgentName,
			plan.Report,
		)
		if err != nil {
			logger.Logger.Errorw("failed to upload session report", err, jobLogValues(jobCtx, "jobId", plan.JobID)...)
		}
	}()
}

func shouldUploadJobSessionReport(jobCtx *JobContext) bool {
	if jobCtx == nil {
		return false
	}
	return jobSessionReportUploadPlan(jobCtx, WorkerOptions{}).Upload
}

func jobSessionReportUploadPlan(jobCtx *JobContext, opts WorkerOptions) workerlivekit.JobSessionReportUploadPlanResult {
	if jobCtx == nil {
		return workerlivekit.JobSessionReportUploadPlanResult{}
	}
	return workerlivekit.JobSessionReportUploadPlan(workerlivekit.JobSessionReportUploadPlanOptions{
		Job:       jobCtx.Job,
		FakeJob:   jobCtx.IsFakeJob(),
		Report:    jobCtx.Report,
		URL:       jobCtx.url,
		APIKey:    opts.APIKey,
		APISecret: opts.APISecret,
		AgentName: opts.AgentName,
	})
}

func (s *AgentServer) runSessionEnd(jobCtx *JobContext) {
	if s.sessionEndFnc == nil {
		return
	}

	plan := workerlivekit.JobSessionEndPlan(workerlivekit.JobSessionEndPlanOptions{
		Job:            jobCtx.Job,
		TimeoutSeconds: s.Options.SessionEndTimeoutSeconds,
	})
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- s.sessionEndFnc(jobCtx)
	}()

	if plan.Timeout <= 0 {
		if err := <-doneCh; err != nil {
			logger.Logger.Errorw("Session end callback failed", err, jobLogValues(jobCtx, "jobId", plan.JobID)...)
		}
		return
	}

	select {
	case err := <-doneCh:
		if err != nil {
			logger.Logger.Errorw("Session end callback failed", err, jobLogValues(jobCtx, "jobId", plan.JobID)...)
		}
	case <-time.After(plan.Timeout):
		logger.Logger.Errorw("Session end callback timed out", nil, jobLogValues(jobCtx, "jobId", plan.JobID, "timeout", plan.Timeout)...)
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

func newLocalJobContext(roomName string, participantIdentity string, opts WorkerOptions) *JobContext {
	return newLocalJobContextWithOptions(roomName, participantIdentity, opts, workerlivekit.DefaultFakeLocalJobOptions())
}

func newLocalJobContextWithOptions(roomName string, participantIdentity string, opts WorkerOptions, options LocalJobOptions) *JobContext {
	opts = resolveWorkerOptions(opts)
	localPlan := workerlivekit.LocalJobContextSetupPlan(workerlivekit.LocalJobContextSetupPlanOptions{
		RoomName:            roomName,
		ParticipantIdentity: participantIdentity,
		APIKey:              opts.APIKey,
		APISecret:           opts.APISecret,
		TTL:                 time.Hour,
		Options:             options,
		NewIdentity:         mathutil.ShortUUID,
	})

	jobCtx := NewJobContext(localPlan.Job, opts.WSRL, opts.APIKey, opts.APISecret)
	jobCtx.AcceptArguments = JobAcceptArguments{Identity: localPlan.AcceptIdentity}
	jobCtx.fakeJob = localPlan.FakeJob
	if localPlan.InitRecording {
		jobCtx.InitRecording(localPlan.RecordingOptions)
	}
	jobCtx.SetSessionDirectory(localPlan.SessionDirectory)
	jobCtx.process = NewJobProcess(JobExecutorTypeThread, opts.UserArguments, opts.HTTPProxy)
	jobCtx.token = localPlan.Token
	return jobCtx
}
