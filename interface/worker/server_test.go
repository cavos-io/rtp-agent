package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/interface/worker/ipc"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/gorilla/websocket"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
)

type fakeWorkerHTTPListener struct {
	closed chan struct{}
	once   sync.Once
	port   int
}

func newFakeWorkerHTTPListener() *fakeWorkerHTTPListener {
	return newFakeWorkerHTTPListenerWithPort(43881)
}

func newFakeWorkerHTTPListenerWithPort(port int) *fakeWorkerHTTPListener {
	return &fakeWorkerHTTPListener{closed: make(chan struct{}), port: port}
}

func (l *fakeWorkerHTTPListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *fakeWorkerHTTPListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *fakeWorkerHTTPListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: l.port}
}

func stubWorkerHTTPListener(t *testing.T) {
	t.Helper()
	oldListen := workerListen
	workerListen = func(network, address string) (net.Listener, error) {
		if network != "tcp" {
			t.Fatalf("workerListen network = %q, want tcp", network)
		}
		if address == "" {
			t.Fatal("workerListen address is empty")
		}
		return newFakeWorkerHTTPListener(), nil
	}
	t.Cleanup(func() {
		workerListen = oldListen
	})
}

func stubWorkerPrometheusListener(t *testing.T, assignedPort int) {
	t.Helper()
	oldListen := workerPrometheusListen
	workerPrometheusListen = func(network, address string) (net.Listener, error) {
		if network != "tcp" {
			t.Fatalf("workerPrometheusListen network = %q, want tcp", network)
		}
		if !strings.HasPrefix(address, "127.0.0.1:") {
			t.Fatalf("workerPrometheusListen address = %q, want 127.0.0.1:<port>", address)
		}
		return newFakeWorkerHTTPListenerWithPort(assignedPort), nil
	}
	t.Cleanup(func() {
		workerPrometheusListen = oldListen
	})
}

func TestNewAgentServerLoadsLiveKitOptionsFromEnvironment(t *testing.T) {
	t.Setenv("LIVEKIT_URL", "wss://livekit.example")
	t.Setenv("LIVEKIT_API_KEY", "env-key")
	t.Setenv("LIVEKIT_API_SECRET", "env-secret")
	t.Setenv("LIVEKIT_AGENT_NAME", "env-agent")
	t.Setenv("HTTPS_PROXY", "https://proxy.example")
	t.Setenv("HTTP_PROXY", "http://proxy.example")

	server := NewAgentServer(WorkerOptions{})

	if server.Options.WSRL != "wss://livekit.example" {
		t.Fatalf("WSRL = %q, want env LIVEKIT_URL", server.Options.WSRL)
	}
	if server.Options.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want env LIVEKIT_API_KEY", server.Options.APIKey)
	}
	if server.Options.APISecret != "env-secret" {
		t.Fatalf("APISecret = %q, want env LIVEKIT_API_SECRET", server.Options.APISecret)
	}
	if server.Options.AgentName != "env-agent" {
		t.Fatalf("AgentName = %q, want env LIVEKIT_AGENT_NAME", server.Options.AgentName)
	}
	if !server.Options.AgentNameIsEnv {
		t.Fatal("AgentNameIsEnv = false, want true when loaded from LIVEKIT_AGENT_NAME")
	}
	if server.Options.HTTPProxy != "https://proxy.example" {
		t.Fatalf("HTTPProxy = %q, want env HTTPS_PROXY", server.Options.HTTPProxy)
	}
}

func TestNewAgentServerExplicitOptionsOverrideEnvironment(t *testing.T) {
	t.Setenv("LIVEKIT_URL", "wss://env.example")
	t.Setenv("LIVEKIT_API_KEY", "env-key")
	t.Setenv("LIVEKIT_API_SECRET", "env-secret")
	t.Setenv("LIVEKIT_AGENT_NAME", "env-agent")
	t.Setenv("HTTPS_PROXY", "https://env-proxy.example")

	server := NewAgentServer(WorkerOptions{
		AgentName: "explicit-agent",
		WSRL:      "wss://explicit.example",
		APIKey:    "explicit-key",
		APISecret: "explicit-secret",
		HTTPProxy: "https://explicit-proxy.example",
	})

	if server.Options.WSRL != "wss://explicit.example" {
		t.Fatalf("WSRL = %q, want explicit value", server.Options.WSRL)
	}
	if server.Options.APIKey != "explicit-key" {
		t.Fatalf("APIKey = %q, want explicit value", server.Options.APIKey)
	}
	if server.Options.APISecret != "explicit-secret" {
		t.Fatalf("APISecret = %q, want explicit value", server.Options.APISecret)
	}
	if server.Options.AgentName != "explicit-agent" {
		t.Fatalf("AgentName = %q, want explicit value", server.Options.AgentName)
	}
	if server.Options.AgentNameIsEnv {
		t.Fatal("AgentNameIsEnv = true, want false for explicit agent name")
	}
	if server.Options.HTTPProxy != "https://explicit-proxy.example" {
		t.Fatalf("HTTPProxy = %q, want explicit value", server.Options.HTTPProxy)
	}
}

func TestNewAgentServerPreservesExplicitEmptyHTTPProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "https://env-proxy.example")
	t.Setenv("HTTP_PROXY", "http://env-proxy.example")

	server := NewAgentServer(WorkerOptions{HTTPProxySet: true})

	if server.Options.HTTPProxy != "" {
		t.Fatalf("HTTPProxy = %q, want explicit empty proxy", server.Options.HTTPProxy)
	}
}

func TestNewAgentServerPrefersWSURLAliasOverDeprecatedWSRL(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		WSURL: "wss://canonical.example",
		WSRL:  "wss://legacy.example",
	})

	if server.Options.WSRL != "wss://canonical.example" {
		t.Fatalf("WSRL = %q, want canonical WSURL value", server.Options.WSRL)
	}
	if server.Options.WSURL != "wss://canonical.example" {
		t.Fatalf("WSURL = %q, want canonical WSURL value", server.Options.WSURL)
	}
}

func TestNewAgentServerUsesReferenceWorkerDefaults(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	if server.Options.MaxRetry != 16 {
		t.Fatalf("MaxRetry = %d, want reference default 16", server.Options.MaxRetry)
	}
	if server.Options.JobMemoryWarnMB != 500 {
		t.Fatalf("JobMemoryWarnMB = %v, want reference default 500", server.Options.JobMemoryWarnMB)
	}
	if server.Options.DrainTimeoutSeconds != 1800 {
		t.Fatalf("DrainTimeoutSeconds = %d, want reference default 1800", server.Options.DrainTimeoutSeconds)
	}
	if server.Options.SessionEndTimeoutSeconds != 300 {
		t.Fatalf("SessionEndTimeoutSeconds = %v, want reference default 300", server.Options.SessionEndTimeoutSeconds)
	}
	if server.Options.ShutdownProcessTimeoutSeconds != 10 {
		t.Fatalf("ShutdownProcessTimeoutSeconds = %v, want reference default 10", server.Options.ShutdownProcessTimeoutSeconds)
	}
	if server.Options.InitializeProcessTimeoutSeconds != 10 {
		t.Fatalf("InitializeProcessTimeoutSeconds = %v, want reference default 10", server.Options.InitializeProcessTimeoutSeconds)
	}
	if server.Options.LoadThreshold != 0.7 {
		t.Fatalf("LoadThreshold = %v, want reference production default 0.7", server.Options.LoadThreshold)
	}
	wantIdle := runtime.NumCPU()
	if wantIdle > 4 {
		wantIdle = 4
	}
	if server.Options.NumIdleProcesses != wantIdle {
		t.Fatalf("NumIdleProcesses = %d, want reference production default %d", server.Options.NumIdleProcesses, wantIdle)
	}
	if server.Options.LogLevel != "INFO" {
		t.Fatalf("LogLevel = %q, want reference production default INFO", server.Options.LogLevel)
	}
	if server.Options.Port != 8081 {
		t.Fatalf("Port = %d, want reference production default 8081", server.Options.Port)
	}
	if server.Options.LoadFunc == nil {
		t.Fatal("LoadFunc = nil, want reference default CPU load function")
	}
}

func TestDefaultWorkerLoadFunctionUsesMovingAverage(t *testing.T) {
	oldSampler := defaultWorkerLoadSample
	oldCalc := defaultWorkerLoadCalc
	samples := []float64{0.2, 0.4, 0.6}
	defaultWorkerLoadSample = func() float64 {
		sample := samples[0]
		samples = samples[1:]
		return sample
	}
	defaultWorkerLoadCalc = nil
	t.Cleanup(func() {
		defaultWorkerLoadSample = oldSampler
		defaultWorkerLoadCalc = oldCalc
	})

	server := NewAgentServer(WorkerOptions{})
	if got := server.currentLoad(); math.Abs(got-0.2) > 1e-9 {
		t.Fatalf("first load = %v, want first sample 0.2", got)
	}
	if got := server.currentLoad(); math.Abs(got-0.3) > 1e-9 {
		t.Fatalf("second load = %v, want average 0.3", got)
	}
	if got := server.currentLoad(); math.Abs(got-0.4) > 1e-9 {
		t.Fatalf("third load = %v, want average 0.4", got)
	}
}

func TestDefaultWorkerLoadFunctionResetsAverageOnInvalidSample(t *testing.T) {
	oldSampler := defaultWorkerLoadSample
	oldCalc := defaultWorkerLoadCalc
	samples := []float64{0.8, math.NaN(), 0.2}
	defaultWorkerLoadSample = func() float64 {
		sample := samples[0]
		samples = samples[1:]
		return sample
	}
	defaultWorkerLoadCalc = nil
	t.Cleanup(func() {
		defaultWorkerLoadSample = oldSampler
		defaultWorkerLoadCalc = oldCalc
	})

	server := NewAgentServer(WorkerOptions{})
	if got := server.currentLoad(); math.Abs(got-0.8) > 1e-9 {
		t.Fatalf("first load = %v, want first sample 0.8", got)
	}
	if got := server.currentLoad(); got != 0 {
		t.Fatalf("invalid load = %v, want reset zero", got)
	}
	if got := server.currentLoad(); math.Abs(got-0.2) > 1e-9 {
		t.Fatalf("load after reset = %v, want fresh sample 0.2", got)
	}
}

func TestNewAgentServerPreservesExplicitZeroInitializeProcessTimeout(t *testing.T) {
	server := NewAgentServer(WorkerOptions{InitializeProcessTimeoutSecondsSet: true})

	if server.Options.InitializeProcessTimeoutSeconds != 0 {
		t.Fatalf("InitializeProcessTimeoutSeconds = %v, want explicit zero", server.Options.InitializeProcessTimeoutSeconds)
	}
}

func TestNewAgentServerUsesReferenceDevModeDefaultsFromEnvironment(t *testing.T) {
	t.Setenv("LIVEKIT_DEV_MODE", "1")

	server := NewAgentServer(WorkerOptions{})

	if !server.Options.DevMode {
		t.Fatal("DevMode = false, want true from LIVEKIT_DEV_MODE")
	}
	if !math.IsInf(server.Options.LoadThreshold, 1) {
		t.Fatalf("LoadThreshold = %v, want reference development default +Inf", server.Options.LoadThreshold)
	}
	if server.Options.NumIdleProcesses != 0 {
		t.Fatalf("NumIdleProcesses = %d, want reference development default 0", server.Options.NumIdleProcesses)
	}
	if server.Options.LogLevel != "DEBUG" {
		t.Fatalf("LogLevel = %q, want reference development default DEBUG", server.Options.LogLevel)
	}
	if server.Options.Port != 0 {
		t.Fatalf("Port = %d, want reference development default 0", server.Options.Port)
	}
	if !server.availableForJob() {
		t.Fatal("availableForJob() = false, want true with development infinite load threshold")
	}
}

func TestNewAgentServerUsesReferenceDevModeDefaultsFromOptions(t *testing.T) {
	server := NewAgentServer(WorkerOptions{DevMode: true})

	if !math.IsInf(server.Options.LoadThreshold, 1) {
		t.Fatalf("LoadThreshold = %v, want reference development default +Inf", server.Options.LoadThreshold)
	}
	if server.Options.NumIdleProcesses != 0 {
		t.Fatalf("NumIdleProcesses = %d, want reference development default 0", server.Options.NumIdleProcesses)
	}
}

func TestNewAgentServerKeepsExplicitDevModeCapacityOptions(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		DevMode:          true,
		LoadThreshold:    0.5,
		NumIdleProcesses: 2,
	})

	if server.Options.LoadThreshold != 0.5 {
		t.Fatalf("LoadThreshold = %v, want explicit development value 0.5", server.Options.LoadThreshold)
	}
	if server.Options.NumIdleProcesses != 2 {
		t.Fatalf("NumIdleProcesses = %d, want explicit development value 2", server.Options.NumIdleProcesses)
	}
}

func TestNewAgentServerNormalizesExplicitLogLevel(t *testing.T) {
	server := NewAgentServer(WorkerOptions{LogLevel: "trace"})

	if server.Options.LogLevel != "TRACE" {
		t.Fatalf("LogLevel = %q, want normalized TRACE", server.Options.LogLevel)
	}
}

func TestNewAgentServerLoadsLogLevelFromEnvironment(t *testing.T) {
	t.Setenv("LIVEKIT_LOG_LEVEL", "warn")

	server := NewAgentServer(WorkerOptions{})

	if server.Options.LogLevel != "WARN" {
		t.Fatalf("LogLevel = %q, want env LIVEKIT_LOG_LEVEL normalized to WARN", server.Options.LogLevel)
	}
}

func TestNewAgentServerLoadsWorkerTokenFromEnvironment(t *testing.T) {
	t.Setenv("LIVEKIT_WORKER_TOKEN", "env-worker-token")

	server := NewAgentServer(WorkerOptions{})

	if server.Options.WorkerToken != "env-worker-token" {
		t.Fatalf("WorkerToken = %q, want env LIVEKIT_WORKER_TOKEN", server.Options.WorkerToken)
	}
}

func TestAgentServerWorkerInfoReportsCloudAgentsMode(t *testing.T) {
	server := NewAgentServer(WorkerOptions{WorkerToken: "worker-token"})

	info := server.WorkerInfo()
	if !info.CloudAgents {
		t.Fatal("WorkerInfo().CloudAgents = false, want true with worker token")
	}
	if info.HTTPPort != 0 {
		t.Fatalf("WorkerInfo().HTTPPort = %d, want 0 before HTTP server starts", info.HTTPPort)
	}
}

func TestWorkerHTTPHandlerReportsHealthOK(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	server.workerHTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "OK" {
		t.Fatalf("health body = %q, want OK", rec.Body.String())
	}
}

func TestWorkerHTTPHandlerReportsConnectionFailure(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.setConnectionFailed(true)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	server.workerHTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d, want 503", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "failed to connect to livekit" {
		t.Fatalf("health body = %q, want failed connection message", rec.Body.String())
	}
}

func TestWorkerHTTPHandlerReportsGenericConnectionFailureForAgora(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Transport: WorkerTransportAgora,
		Agora: AgoraOptions{
			AppID:   "agora-app",
			Channel: "support",
		},
	})
	server.setConnectionFailed(true)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	server.workerHTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d, want 503", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "failed to connect" {
		t.Fatalf("health body = %q, want generic failed connection message", rec.Body.String())
	}
}

func TestWorkerHTTPHandlerUsesCustomHealthCheck(t *testing.T) {
	called := false
	server := NewAgentServer(WorkerOptions{
		HealthCheck: func(got *AgentServer) error {
			called = true
			if got != nil && got.ID() != "unregistered" {
				t.Fatalf("health check server ID = %q, want unregistered", got.ID())
			}
			return nil
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	server.workerHTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", rec.Code)
	}
	if !called {
		t.Fatal("custom health check was not called")
	}
	if strings.TrimSpace(rec.Body.String()) != "OK" {
		t.Fatalf("health body = %q, want OK", rec.Body.String())
	}
}

func TestWorkerHTTPHandlerReportsCustomHealthCheckFailure(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		HealthCheck: func(*AgentServer) error {
			return errors.New("dependency unavailable")
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	server.workerHTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d, want 503", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "dependency unavailable" {
		t.Fatalf("health body = %q, want dependency unavailable", rec.Body.String())
	}
}

func TestWorkerHTTPHandlerSkipsCustomHealthCheckAfterConnectionFailure(t *testing.T) {
	called := false
	server := NewAgentServer(WorkerOptions{
		HealthCheck: func(*AgentServer) error {
			called = true
			return nil
		},
	})
	server.setConnectionFailed(true)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	server.workerHTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d, want 503", rec.Code)
	}
	if called {
		t.Fatal("custom health check was called after connection failure")
	}
}

func TestWorkerHTTPHandlerReportsWorkerMetadata(t *testing.T) {
	t.Setenv("LIVEKIT_REMOTE_EOT_URL", "https://hosted.example")
	server := NewAgentServer(WorkerOptions{
		AgentName: "sales-agent",
		LoadFunc:  func(*AgentServer) float64 { return 0.42 },
	})
	server.activeJobs["job-a"] = NewJobContext(&livekit.Job{Id: "job-a"}, "", "", "")

	req := httptest.NewRequest(http.MethodGet, "/worker", nil)
	rec := httptest.NewRecorder()

	server.workerHTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("worker status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"agent_name":"sales-agent"`,
		`"agent_name_is_env":false`,
		`"worker_type":"JT_ROOM"`,
		`"worker_load":0.42`,
		`"active_jobs":1`,
		`"protocol_version":1`,
		`"project_type":"go"`,
		`"node_name":"`,
		`"hosted":true`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/worker response missing %s in %s", want, body)
		}
	}
}

func TestWorkerHTTPHandlerReportsEnvAgentNameProvenance(t *testing.T) {
	t.Setenv("LIVEKIT_AGENT_NAME", "env-agent")
	server := NewAgentServer(WorkerOptions{})

	req := httptest.NewRequest(http.MethodGet, "/worker", nil)
	rec := httptest.NewRecorder()

	server.workerHTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("worker status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"agent_name":"env-agent"`,
		`"agent_name_is_env":true`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/worker response missing %s in %s", want, body)
		}
	}
}

func TestWorkerHTTPHandlerDoesNotExposeLiveKitMetadataForAgora(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Transport: WorkerTransportAgora,
		Agora: AgoraOptions{
			AppID:   "agora-app",
			Channel: "support",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/worker", nil)
	rec := httptest.NewRecorder()

	server.workerHTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("worker status = %d, want 404 for Agora transport", rec.Code)
	}
}

func TestWorkerInfoReportsStartedHTTPPort(t *testing.T) {
	stubWorkerHTTPListener(t)
	server := NewAgentServer(WorkerOptions{DevMode: true, Host: "127.0.0.1"})
	httpServer, err := server.startWorkerHTTPServer()
	if err != nil {
		t.Fatalf("startWorkerHTTPServer() error = %v", err)
	}
	defer httpServer.Close()

	info := server.WorkerInfo()
	if info.HTTPPort == 0 {
		t.Fatal("WorkerInfo().HTTPPort = 0, want started HTTP port")
	}
}

func TestStartPrometheusServerUsesConfiguredPort(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Host:           "127.0.0.1",
		PrometheusPort: 0,
	})

	prometheusServer, err := server.startPrometheusServer()
	if err != nil {
		t.Fatalf("startPrometheusServer() error = %v", err)
	}
	if prometheusServer != nil {
		t.Fatal("startPrometheusServer() = server, want nil when PrometheusPort is unset")
	}

	port := 43882
	stubWorkerPrometheusListener(t, port)
	server = NewAgentServer(WorkerOptions{
		Host:           "127.0.0.1",
		PrometheusPort: port,
	})
	prometheusServer, err = server.startPrometheusServer()
	if err != nil {
		t.Fatalf("startPrometheusServer() error = %v", err)
	}
	if prometheusServer == nil {
		t.Fatal("startPrometheusServer() = nil, want server when PrometheusPort is configured")
	}
	defer prometheusServer.Stop(context.Background())
	if prometheusServer.Port != port {
		t.Fatalf("Prometheus server Port = %d, want %d", prometheusServer.Port, port)
	}
}

func TestStartPrometheusServerEnablesExplicitZeroPort(t *testing.T) {
	stubWorkerPrometheusListener(t, 43883)
	server := NewAgentServer(WorkerOptions{
		Host:              "127.0.0.1",
		PrometheusPortSet: true,
	})

	prometheusServer, err := server.startPrometheusServer()
	if err != nil {
		t.Fatalf("startPrometheusServer() error = %v", err)
	}
	if prometheusServer == nil {
		t.Fatal("startPrometheusServer() = nil, want server for explicit zero Prometheus port")
	}
	defer prometheusServer.Stop(context.Background())

	if prometheusServer.Port == 0 {
		t.Fatal("Prometheus server Port = 0, want assigned listener port")
	}
}

func TestConfigurePrometheusMultiprocDirSetsEnvironment(t *testing.T) {
	t.Setenv("PROMETHEUS_MULTIPROC_DIR", "")
	dir := t.TempDir()
	server := NewAgentServer(WorkerOptions{PrometheusMultiprocDir: dir})

	if err := server.configurePrometheusMultiprocDir(); err != nil {
		t.Fatalf("configurePrometheusMultiprocDir() error = %v", err)
	}
	if got := os.Getenv("PROMETHEUS_MULTIPROC_DIR"); got != dir {
		t.Fatalf("PROMETHEUS_MULTIPROC_DIR = %q, want %q", got, dir)
	}
}

func TestConfigurePrometheusMultiprocDirCleansExistingMetricFiles(t *testing.T) {
	dir := t.TempDir()
	staleFile := dir + "/stale.db"
	if err := os.WriteFile(staleFile, []byte("old metrics"), 0o644); err != nil {
		t.Fatalf("write stale metric file: %v", err)
	}
	nestedDir := dir + "/nested"
	if err := os.Mkdir(nestedDir, 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	t.Setenv("PROMETHEUS_MULTIPROC_DIR", dir)
	server := NewAgentServer(WorkerOptions{})

	if err := server.configurePrometheusMultiprocDir(); err != nil {
		t.Fatalf("configurePrometheusMultiprocDir() error = %v", err)
	}
	if server.Options.PrometheusMultiprocDir != dir {
		t.Fatalf("PrometheusMultiprocDir = %q, want env dir %q", server.Options.PrometheusMultiprocDir, dir)
	}
	if _, err := os.Stat(staleFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale metric file stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(nestedDir); err != nil {
		t.Fatalf("nested directory stat error = %v, want preserved directory", err)
	}
}

func TestUpdateOptionsMergesConfiguredValuesBeforeRun(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		WSRL:          "wss://old.example",
		APIKey:        "old-key",
		APISecret:     "old-secret",
		MaxRetry:      3,
		LoadThreshold: 0.5,
	})

	permissions := &WorkerPermissions{
		CanPublish:     false,
		CanSubscribe:   true,
		CanPublishData: false,
		Hidden:         true,
	}
	healthCheck := func(*AgentServer) error { return nil }
	setupFunc := func(*JobProcess) error { return nil }
	err := server.UpdateOptions(WorkerOptions{
		WSURL:            "wss://new.example",
		APIKey:           "new-key",
		MaxRetry:         9,
		LoadThreshold:    0.8,
		NumIdleProcesses: 2,
		HealthCheck:      healthCheck,
		SetupFunc:        setupFunc,
		Permissions:      permissions,
	})
	if err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}

	if server.Options.WSURL != "wss://new.example" {
		t.Fatalf("WSURL = %q, want updated value", server.Options.WSURL)
	}
	if server.Options.WSRL != "wss://new.example" {
		t.Fatalf("WSRL = %q, want canonical updated WSURL value", server.Options.WSRL)
	}
	if server.Options.APIKey != "new-key" {
		t.Fatalf("APIKey = %q, want updated value", server.Options.APIKey)
	}
	if server.Options.APISecret != "old-secret" {
		t.Fatalf("APISecret = %q, want unchanged value", server.Options.APISecret)
	}
	if server.Options.MaxRetry != 9 {
		t.Fatalf("MaxRetry = %d, want updated value", server.Options.MaxRetry)
	}
	if server.Options.LoadThreshold != 0.8 {
		t.Fatalf("LoadThreshold = %v, want updated value", server.Options.LoadThreshold)
	}
	if server.Options.NumIdleProcesses != 2 {
		t.Fatalf("NumIdleProcesses = %d, want updated value", server.Options.NumIdleProcesses)
	}
	if server.Options.HealthCheck == nil {
		t.Fatal("HealthCheck was not updated")
	}
	if server.Options.SetupFunc == nil {
		t.Fatal("SetupFunc was not updated")
	}
	if server.Options.Permissions != permissions {
		t.Fatal("Permissions was not replaced with updated pointer")
	}
}

func TestUpdateOptionsPreservesAgoraTransportConfig(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Transport: WorkerTransportAgora,
		Agora: AgoraOptions{
			AppID:          "app",
			AppCertificate: "cert",
			Channel:        "support",
			UID:            "agent",
			Token:          "token",
		},
	})

	err := server.UpdateOptions(WorkerOptions{
		LogLevel: "DEBUG",
		DevMode:  true,
	})
	if err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}

	if server.Options.Transport != WorkerTransportAgora {
		t.Fatalf("Transport = %q, want %q", server.Options.Transport, WorkerTransportAgora)
	}
	if server.Options.Agora.AppID != "app" {
		t.Fatalf("Agora.AppID = %q, want app", server.Options.Agora.AppID)
	}
	if server.Options.Agora.AppCertificate != "cert" {
		t.Fatalf("Agora.AppCertificate = %q, want cert", server.Options.Agora.AppCertificate)
	}
	if server.Options.Agora.Channel != "support" {
		t.Fatalf("Agora.Channel = %q, want support", server.Options.Agora.Channel)
	}
	if server.Options.Agora.UID != "agent" {
		t.Fatalf("Agora.UID = %q, want agent", server.Options.Agora.UID)
	}
	if server.Options.Agora.Token != "token" {
		t.Fatalf("Agora.Token = %q, want token", server.Options.Agora.Token)
	}
}

func TestUpdateOptionsPreservesExplicitZeroPorts(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Port:           8081,
		PrometheusPort: 9090,
	})

	err := server.UpdateOptions(WorkerOptions{
		PortSet:           true,
		PrometheusPortSet: true,
	})
	if err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}

	if server.Options.Port != 0 {
		t.Fatalf("Port = %d, want explicit zero", server.Options.Port)
	}
	if server.Options.PrometheusPort != 0 {
		t.Fatalf("PrometheusPort = %d, want explicit zero", server.Options.PrometheusPort)
	}
	if !server.Options.PrometheusPortSet {
		t.Fatal("PrometheusPortSet = false, want true")
	}
}

func TestUpdateOptionsRejectsInvalidLogLevel(t *testing.T) {
	server := NewAgentServer(WorkerOptions{LogLevel: "info"})

	err := server.UpdateOptions(WorkerOptions{LogLevel: "verbose"})
	if err == nil {
		t.Fatal("UpdateOptions() error = nil, want invalid log level error")
	}
	if got, want := err.Error(), "Invalid log level 'verbose'. Valid levels: CRITICAL, DEBUG, ERROR, INFO, TRACE, WARN"; got != want {
		t.Fatalf("UpdateOptions() error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "invalid log_level") {
		t.Fatalf("UpdateOptions() error = %q, want reference log level wording", err.Error())
	}
	if server.Options.LogLevel != "INFO" {
		t.Fatalf("LogLevel = %q, want previous normalized INFO after rejected update", server.Options.LogLevel)
	}
}

func TestUpdateOptionsPreservesExplicitZeroResourceValues(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadThreshold:    0.5,
		JobMemoryWarnMB:  250,
		JobMemoryLimitMB: 512,
		NumIdleProcesses: 2,
	})

	err := server.UpdateOptions(WorkerOptions{
		LoadThresholdSet:    true,
		JobMemoryWarnMBSet:  true,
		JobMemoryLimitMBSet: true,
		NumIdleProcessesSet: true,
	})
	if err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}

	if server.Options.LoadThreshold != 0 {
		t.Fatalf("LoadThreshold = %v, want explicit zero", server.Options.LoadThreshold)
	}
	if server.Options.JobMemoryWarnMB != 0 {
		t.Fatalf("JobMemoryWarnMB = %v, want explicit zero", server.Options.JobMemoryWarnMB)
	}
	if server.Options.JobMemoryLimitMB != 0 {
		t.Fatalf("JobMemoryLimitMB = %v, want explicit zero", server.Options.JobMemoryLimitMB)
	}
	if server.Options.NumIdleProcesses != 0 {
		t.Fatalf("NumIdleProcesses = %d, want explicit zero", server.Options.NumIdleProcesses)
	}
}

func TestUpdateOptionsPreservesExplicitZeroMaxRetry(t *testing.T) {
	server := NewAgentServer(WorkerOptions{MaxRetry: 5})

	err := server.UpdateOptions(WorkerOptions{MaxRetrySet: true})
	if err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}

	if server.Options.MaxRetry != 0 {
		t.Fatalf("MaxRetry = %d, want explicit zero", server.Options.MaxRetry)
	}
}

func TestUpdateOptionsPreservesExplicitZeroTimeoutValues(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		DrainTimeoutSeconds:             1800,
		SessionEndTimeoutSeconds:        120,
		ShutdownProcessTimeoutSeconds:   30,
		InitializeProcessTimeoutSeconds: 20,
	})

	err := server.UpdateOptions(WorkerOptions{
		DrainTimeoutSecondsSet:             true,
		SessionEndTimeoutSecondsSet:        true,
		ShutdownProcessTimeoutSecondsSet:   true,
		InitializeProcessTimeoutSecondsSet: true,
	})
	if err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}

	if server.Options.DrainTimeoutSeconds != 0 {
		t.Fatalf("DrainTimeoutSeconds = %d, want explicit zero", server.Options.DrainTimeoutSeconds)
	}
	if server.Options.SessionEndTimeoutSeconds != 0 {
		t.Fatalf("SessionEndTimeoutSeconds = %v, want explicit zero", server.Options.SessionEndTimeoutSeconds)
	}
	if server.Options.ShutdownProcessTimeoutSeconds != 0 {
		t.Fatalf("ShutdownProcessTimeoutSeconds = %v, want explicit zero", server.Options.ShutdownProcessTimeoutSeconds)
	}
	if server.Options.InitializeProcessTimeoutSeconds != 0 {
		t.Fatalf("InitializeProcessTimeoutSeconds = %v, want explicit zero", server.Options.InitializeProcessTimeoutSeconds)
	}
}

func TestUpdateOptionsMarksNonZeroDrainTimeoutAsSet(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	err := server.UpdateOptions(WorkerOptions{DrainTimeoutSeconds: 60})
	if err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}

	if server.Options.DrainTimeoutSeconds != 60 {
		t.Fatalf("DrainTimeoutSeconds = %d, want updated value", server.Options.DrainTimeoutSeconds)
	}
	if !server.Options.DrainTimeoutSecondsSet {
		t.Fatal("DrainTimeoutSecondsSet = false, want true for non-zero update")
	}
}

func TestUpdateOptionsMarksExplicitAgentNameAsNotEnvironment(t *testing.T) {
	t.Setenv("LIVEKIT_AGENT_NAME", "env-agent")
	server := NewAgentServer(WorkerOptions{})
	if !server.Options.AgentNameIsEnv {
		t.Fatal("AgentNameIsEnv = false, want true before explicit update")
	}

	if err := server.UpdateOptions(WorkerOptions{AgentName: "explicit-agent"}); err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}

	if server.Options.AgentName != "explicit-agent" {
		t.Fatalf("AgentName = %q, want explicit-agent", server.Options.AgentName)
	}
	if server.Options.AgentNameIsEnv {
		t.Fatal("AgentNameIsEnv = true, want false after explicit update")
	}
}

func TestUpdateOptionsRejectsAfterWorkerStarted(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.conn = &websocket.Conn{}

	err := server.UpdateOptions(WorkerOptions{APIKey: "new-key"})
	if err == nil {
		t.Fatal("UpdateOptions() error = nil, want started worker error")
	}
	if got, want := err.Error(), "cannot update options after starting the server"; got != want {
		t.Fatalf("UpdateOptions() error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "worker already started") {
		t.Fatalf("UpdateOptions() error = %q, want reference started-server wording", err.Error())
	}
}

func TestUpdateOptionsRejectsAfterHTTPServerStarted(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.httpServer = &http.Server{}

	err := server.UpdateOptions(WorkerOptions{APIKey: "new-key"})
	if err == nil {
		t.Fatal("UpdateOptions() error = nil, want started worker error")
	}
	if got, want := err.Error(), "cannot update options after starting the server"; got != want {
		t.Fatalf("UpdateOptions() error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "http server already started") {
		t.Fatalf("UpdateOptions() error = %q, want reference started-server wording", err.Error())
	}
}

func TestAgentWebSocketURLPreservesBasePath(t *testing.T) {
	got, err := workerlivekit.AgentWebSocketURL("https://livekit.example/project-a", "")
	if err != nil {
		t.Fatalf("AgentWebSocketURL() error = %v", err)
	}

	want := "wss://livekit.example/project-a/agent"
	if got != want {
		t.Fatalf("AgentWebSocketURL() = %q, want %q", got, want)
	}

	got, err = workerlivekit.AgentWebSocketURL("http://livekit.example/project-a", "")
	if err != nil {
		t.Fatalf("AgentWebSocketURL(http) error = %v", err)
	}
	if want := "ws://livekit.example/project-a/agent"; got != want {
		t.Fatalf("AgentWebSocketURL(http) = %q, want %q", got, want)
	}
}

func TestAgentWebSocketURLAddsWorkerToken(t *testing.T) {
	got, err := workerlivekit.AgentWebSocketURL("wss://livekit.example/project-a/", "cloud token")
	if err != nil {
		t.Fatalf("AgentWebSocketURL() error = %v", err)
	}

	want := "wss://livekit.example/project-a/agent?worker_token=cloud+token"
	if got != want {
		t.Fatalf("AgentWebSocketURL() = %q, want %q", got, want)
	}
}

func TestWorkerTypeMapsToLiveKitJobType(t *testing.T) {
	tests := []struct {
		name       string
		workerType string
		want       livekit.JobType
	}{
		{
			name:       "default",
			workerType: "",
			want:       livekit.JobType_JT_ROOM,
		},
		{
			name:       "room",
			workerType: string(WorkerTypeRoom),
			want:       livekit.JobType_JT_ROOM,
		},
		{
			name:       "publisher",
			workerType: string(WorkerTypePublisher),
			want:       livekit.JobType_JT_PUBLISHER,
		},
		{
			name:       "unknown defaults to room",
			workerType: "background",
			want:       livekit.JobType_JT_ROOM,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workerlivekit.JobTypeForWorkerType(tt.workerType); got != tt.want {
				t.Fatalf("JobTypeForWorkerType(%q) = %v, want %v", tt.workerType, got, tt.want)
			}
		})
	}
}

func TestRegisterWorkerRequestUsesConfiguredWorkerType(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		AgentName:  "publisher-agent",
		WorkerType: WorkerTypePublisher,
		Version:    "2.3.4",
	})

	req := server.registerWorkerRequest()
	register := req.GetRegister()
	if register == nil {
		t.Fatal("register worker message is nil")
	}
	if register.Type != livekit.JobType_JT_PUBLISHER {
		t.Fatalf("register.Type = %v, want %v", register.Type, livekit.JobType_JT_PUBLISHER)
	}
	if register.AgentName != "publisher-agent" {
		t.Fatalf("register.AgentName = %q, want %q", register.AgentName, "publisher-agent")
	}
	if register.Version != "2.3.4" {
		t.Fatalf("register.Version = %q, want configured version", register.Version)
	}
}

func TestRegisterWorkerRequestIncludesDefaultPermissions(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	register := server.registerWorkerRequest().GetRegister()
	if register == nil {
		t.Fatal("register worker message is nil")
	}

	permissions := register.GetAllowedPermissions()
	if permissions == nil {
		t.Fatal("register.AllowedPermissions = nil, want default permissions")
	}
	if !permissions.CanPublish {
		t.Fatal("permissions.CanPublish = false, want true")
	}
	if !permissions.CanSubscribe {
		t.Fatal("permissions.CanSubscribe = false, want true")
	}
	if !permissions.CanPublishData {
		t.Fatal("permissions.CanPublishData = false, want true")
	}
	if !permissions.CanUpdateMetadata {
		t.Fatal("permissions.CanUpdateMetadata = false, want true")
	}
	if permissions.Hidden {
		t.Fatal("permissions.Hidden = true, want false")
	}
	//lint:ignore SA1019 keep verifying the deprecated protobuf field while LiveKit still sends it
	if !permissions.Agent {
		t.Fatal("permissions.Agent = false, want true")
	}
}

func TestRegisterWorkerRequestIncludesConfiguredPermissions(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Permissions: &WorkerPermissions{
			CanPublish:        false,
			CanSubscribe:      true,
			CanPublishData:    false,
			CanUpdateMetadata: false,
			CanPublishSources: []livekit.TrackSource{
				livekit.TrackSource_MICROPHONE,
				livekit.TrackSource_SCREEN_SHARE,
			},
			Hidden: true,
		},
	})

	register := server.registerWorkerRequest().GetRegister()
	if register == nil {
		t.Fatal("register worker message is nil")
	}

	permissions := register.GetAllowedPermissions()
	if permissions == nil {
		t.Fatal("register.AllowedPermissions = nil, want configured permissions")
	}
	if permissions.CanPublish {
		t.Fatal("permissions.CanPublish = true, want false")
	}
	if !permissions.CanSubscribe {
		t.Fatal("permissions.CanSubscribe = false, want true")
	}
	if permissions.CanPublishData {
		t.Fatal("permissions.CanPublishData = true, want false")
	}
	if permissions.CanUpdateMetadata {
		t.Fatal("permissions.CanUpdateMetadata = true, want false")
	}
	if !permissions.Hidden {
		t.Fatal("permissions.Hidden = false, want true")
	}
	//lint:ignore SA1019 keep verifying the deprecated protobuf field while LiveKit still sends it
	if !permissions.Agent {
		t.Fatal("permissions.Agent = false, want true")
	}
	if len(permissions.CanPublishSources) != 2 {
		t.Fatalf("permissions.CanPublishSources len = %d, want 2", len(permissions.CanPublishSources))
	}
	if permissions.CanPublishSources[0] != livekit.TrackSource_MICROPHONE {
		t.Fatalf("permissions.CanPublishSources[0] = %v, want MICROPHONE", permissions.CanPublishSources[0])
	}
	if permissions.CanPublishSources[1] != livekit.TrackSource_SCREEN_SHARE {
		t.Fatalf("permissions.CanPublishSources[1] = %v, want SCREEN_SHARE", permissions.CanPublishSources[1])
	}
}

func TestHandleRegisterNotifiesWorkerRegisteredHandlers(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	serverInfo := &livekit.ServerInfo{}

	var gotWorkerID string
	var gotServerInfo *livekit.ServerInfo
	server.OnWorkerRegistered(func(workerID string, info *livekit.ServerInfo) {
		gotWorkerID = workerID
		gotServerInfo = info
	})

	server.handleMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Register{
			Register: &livekit.RegisterWorkerResponse{
				WorkerId:   "worker-a",
				ServerInfo: serverInfo,
			},
		},
	})

	if gotWorkerID != "worker-a" {
		t.Fatalf("registered workerID = %q, want worker-a", gotWorkerID)
	}
	if gotServerInfo != serverInfo {
		t.Fatalf("registered serverInfo = %p, want %p", gotServerInfo, serverInfo)
	}
}

func TestHandleRegisterNotifiesSharedWorkerRegisteredHandlers(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	var gotInfo WorkerRegisteredInfo
	server.OnWorkerRegisteredInfo(func(info WorkerRegisteredInfo) {
		gotInfo = info
	})

	server.handleMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Register{
			Register: &livekit.RegisterWorkerResponse{WorkerId: "worker-shared"},
		},
	})

	if gotInfo.WorkerID != "worker-shared" {
		t.Fatalf("registered info WorkerID = %q, want worker-shared", gotInfo.WorkerID)
	}
}

func TestWorkerRegisteredHandlerPanicDoesNotBlockOtherHandlers(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	var calls int
	server.OnWorkerRegistered(func(string, *livekit.ServerInfo) {
		panic("boom")
	})
	server.OnWorkerRegistered(func(workerID string, info *livekit.ServerInfo) {
		if workerID != "worker-a" {
			t.Fatalf("workerID = %q, want worker-a", workerID)
		}
		if info == nil {
			t.Fatal("serverInfo is nil")
		}
		calls++
	})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("worker registered handler panic propagated: %v", recovered)
		}
	}()

	server.handleMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Register{
			Register: &livekit.RegisterWorkerResponse{
				WorkerId:   "worker-a",
				ServerInfo: &livekit.ServerInfo{},
			},
		},
	})

	if calls != 1 {
		t.Fatalf("worker registered handler calls = %d, want 1", calls)
	}
}

func TestAgentServerIDReturnsRegisteredWorkerID(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	if server.ID() != "unregistered" {
		t.Fatalf("ID() before registration = %q, want unregistered", server.ID())
	}
	if server.WorkerInfo().CloudAgents {
		t.Fatal("WorkerInfo().CloudAgents = true, want false for unregistered worker without token")
	}

	server.handleMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Register{
			Register: &livekit.RegisterWorkerResponse{WorkerId: "worker-a"},
		},
	})

	if server.ID() != "worker-a" {
		t.Fatalf("ID() after registration = %q, want worker-a", server.ID())
	}
}

func TestAgentServerActiveJobsReturnsSnapshot(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	jobA := NewJobContext(&livekit.Job{Id: "job-a"}, "", "", "")
	jobB := NewJobContext(&livekit.Job{Id: "job-b"}, "", "", "")
	server.mu.Lock()
	server.activeJobs[jobA.Job.Id] = jobA
	server.activeJobs[jobB.Job.Id] = jobB
	server.mu.Unlock()

	activeJobs := server.ActiveJobs()
	if len(activeJobs) != 2 {
		t.Fatalf("ActiveJobs() len = %d, want 2", len(activeJobs))
	}

	got := map[string]*JobContext{}
	for _, jobCtx := range activeJobs {
		got[jobCtx.Job.Id] = jobCtx
	}
	if got["job-a"] != jobA {
		t.Fatal("ActiveJobs() missing job-a context")
	}
	if got["job-b"] != jobB {
		t.Fatal("ActiveJobs() missing job-b context")
	}

	activeJobs[0] = nil
	if len(server.ActiveJobs()) != 2 {
		t.Fatal("mutating ActiveJobs() result changed server active job count")
	}
}

func TestAgentServerActiveRunningJobsReturnsReferenceAssignmentSnapshots(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.workerID = "worker-a"
	jobCtx := NewJobContext(&livekit.Job{Id: "job-a", Room: &livekit.Room{Name: "room-a"}}, "wss://livekit.example", "key", "secret")
	jobCtx.AcceptArguments = JobAcceptArguments{
		Name:     "Agent A",
		Identity: "agent-a",
		Metadata: "metadata-a",
		Attributes: map[string]string{
			"tier": "gold",
		},
	}
	jobCtx.token = "assignment-token"
	jobCtx.fakeJob = true
	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	runningJobs := server.ActiveRunningJobs()
	if len(runningJobs) != 1 {
		t.Fatalf("ActiveRunningJobs() len = %d, want 1", len(runningJobs))
	}

	running := runningJobs[0]
	if running.Job != jobCtx.Job {
		t.Fatal("ActiveRunningJobs()[0].Job did not preserve job pointer")
	}
	if running.URL != "wss://livekit.example" {
		t.Fatalf("ActiveRunningJobs()[0].URL = %q, want wss://livekit.example", running.URL)
	}
	if running.Token != "assignment-token" {
		t.Fatalf("ActiveRunningJobs()[0].Token = %q, want assignment-token", running.Token)
	}
	if running.WorkerID != "worker-a" {
		t.Fatalf("ActiveRunningJobs()[0].WorkerID = %q, want worker-a", running.WorkerID)
	}
	if !running.FakeJob {
		t.Fatal("ActiveRunningJobs()[0].FakeJob = false, want true")
	}
	if running.AcceptArguments.Name != "Agent A" {
		t.Fatalf("ActiveRunningJobs()[0].AcceptArguments.Name = %q, want Agent A", running.AcceptArguments.Name)
	}
	if running.AcceptArguments.Identity != "agent-a" {
		t.Fatalf("ActiveRunningJobs()[0].AcceptArguments.Identity = %q, want agent-a", running.AcceptArguments.Identity)
	}
	if running.AcceptArguments.Metadata != "metadata-a" {
		t.Fatalf("ActiveRunningJobs()[0].AcceptArguments.Metadata = %q, want metadata-a", running.AcceptArguments.Metadata)
	}
	if running.AcceptArguments.Attributes["tier"] != "gold" {
		t.Fatalf("ActiveRunningJobs()[0].AcceptArguments.Attributes[tier] = %q, want gold", running.AcceptArguments.Attributes["tier"])
	}

	runningJobs[0].AcceptArguments.Attributes["tier"] = "platinum"
	if got := server.ActiveRunningJobs()[0].AcceptArguments.Attributes["tier"]; got != "gold" {
		t.Fatalf("mutating ActiveRunningJobs() attributes changed stored context to %q, want gold", got)
	}
}

func TestRefreshRunningJobTokenForReloadPreservesAssignmentAndExtendsToken(t *testing.T) {
	originalToken, err := auth.NewAccessToken("api-key", "api-secret").
		SetIdentity("agent-a").
		SetName("Agent A").
		SetMetadata("metadata-a").
		SetAttributes(map[string]string{"tier": "gold"}).
		SetKind(livekit.ParticipantInfo_AGENT).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     "room-a",
			Agent:    true,
		}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}
	info := ipc.RunningJobInfo{
		AcceptArguments: ipc.JobAcceptArguments{
			Name:     "Agent A",
			Identity: "agent-a",
			Metadata: "metadata-a",
			Attributes: map[string]string{
				"tier": "gold",
			},
		},
		Job:      &livekit.Job{Id: "job-a", Room: &livekit.Room{Name: "room-a"}},
		URL:      "wss://livekit.example",
		Token:    originalToken,
		WorkerID: "worker-a",
		FakeJob:  true,
	}
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	livekitInfo := ipc.ToLiveKitRunningJobInfo(info)
	refreshed, err := workerlivekit.RefreshRunningJobTokenForReload(livekitInfo, "api-secret", now)
	if err != nil {
		t.Fatalf("refreshRunningJobTokenForReload() error = %v", err)
	}

	if refreshed.AcceptArguments.Identity != "agent-a" {
		t.Fatalf("AcceptArguments.Identity = %q, want agent-a", refreshed.AcceptArguments.Identity)
	}
	if refreshed.Job != info.Job {
		t.Fatal("Job pointer was not preserved")
	}
	if refreshed.URL != "wss://livekit.example" {
		t.Fatalf("URL = %q, want wss://livekit.example", refreshed.URL)
	}
	if refreshed.WorkerID != "worker-a" {
		t.Fatalf("WorkerID = %q, want worker-a", refreshed.WorkerID)
	}
	if !refreshed.FakeJob {
		t.Fatal("FakeJob = false, want true")
	}
	if refreshed.Token == "" || refreshed.Token == originalToken {
		t.Fatal("Token was not refreshed")
	}
	if _, err := workerlivekit.TokenClaims(refreshed.Token); err != nil {
		t.Fatalf("TokenClaims(refreshed.Token) error = %v", err)
	}

	tok, err := jwt.ParseSigned(refreshed.Token)
	if err != nil {
		t.Fatalf("ParseSigned() error = %v", err)
	}
	standardClaims := jwt.Claims{}
	grants := auth.ClaimGrants{}
	if err := tok.Claims([]byte("api-secret"), &standardClaims, &grants); err != nil {
		t.Fatalf("refreshed token Claims() error = %v", err)
	}
	if standardClaims.Expiry == nil {
		t.Fatal("refreshed token expiry = nil, want one-hour expiry")
	}
	if got := standardClaims.Expiry.Time(); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("refreshed token expiry = %v, want %v", got, now.Add(time.Hour))
	}
	if grants.Identity != "agent-a" {
		t.Fatalf("refreshed token identity = %q, want agent-a", grants.Identity)
	}
	if grants.Name != "Agent A" {
		t.Fatalf("refreshed token name = %q, want Agent A", grants.Name)
	}
	if grants.Metadata != "metadata-a" {
		t.Fatalf("refreshed token metadata = %q, want metadata-a", grants.Metadata)
	}
	if grants.Attributes["tier"] != "gold" {
		t.Fatalf("refreshed token attribute tier = %q, want gold", grants.Attributes["tier"])
	}
	if grants.GetParticipantKind() != livekit.ParticipantInfo_AGENT {
		t.Fatalf("refreshed token kind = %v, want AGENT", grants.GetParticipantKind())
	}
	if grants.Video == nil || !grants.Video.RoomJoin || !grants.Video.Agent || grants.Video.Room != "room-a" {
		t.Fatalf("refreshed token video grant = %#v, want room-a agent join grant", grants.Video)
	}
}

func TestRefreshRunningJobsForReloadRefreshesEveryJob(t *testing.T) {
	tokenA, err := auth.NewAccessToken("api-key", "api-secret").
		SetIdentity("agent-a").
		SetName("Agent A").
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     "room-a",
			Agent:    true,
		}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() tokenA error = %v", err)
	}
	tokenB, err := auth.NewAccessToken("api-key", "api-secret").
		SetIdentity("agent-b").
		SetName("Agent B").
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     "room-b",
			Agent:    true,
		}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() tokenB error = %v", err)
	}

	jobA := &livekit.Job{Id: "job-a", Room: &livekit.Room{Name: "room-a"}}
	jobB := &livekit.Job{Id: "job-b", Room: &livekit.Room{Name: "room-b"}}
	jobs := []ipc.RunningJobInfo{
		{
			AcceptArguments: ipc.JobAcceptArguments{Name: "Agent A", Identity: "agent-a"},
			Job:             jobA,
			URL:             "wss://livekit.example",
			Token:           tokenA,
			WorkerID:        "worker-a",
		},
		{
			AcceptArguments: ipc.JobAcceptArguments{Name: "Agent B", Identity: "agent-b"},
			Job:             jobB,
			URL:             "wss://livekit.example",
			Token:           tokenB,
			WorkerID:        "worker-a",
			FakeJob:         true,
		},
	}
	now := time.Date(2026, 5, 31, 13, 0, 0, 0, time.UTC)

	livekitJobs := ipc.ToLiveKitRunningJobInfos(jobs)
	refreshed, err := workerlivekit.RefreshRunningJobsForReload(livekitJobs, "api-secret", now)
	if err != nil {
		t.Fatalf("refreshRunningJobsForReload() error = %v", err)
	}

	if len(refreshed) != 2 {
		t.Fatalf("refreshRunningJobsForReload() len = %d, want 2", len(refreshed))
	}
	if jobs[0].Token != tokenA || jobs[1].Token != tokenB {
		t.Fatal("refreshRunningJobsForReload mutated input jobs")
	}
	for i, info := range refreshed {
		if info.Job != jobs[i].Job {
			t.Fatalf("refreshed[%d].Job was not preserved", i)
		}
		if info.AcceptArguments.Identity != jobs[i].AcceptArguments.Identity {
			t.Fatalf("refreshed[%d].AcceptArguments.Identity = %q, want %q", i, info.AcceptArguments.Identity, jobs[i].AcceptArguments.Identity)
		}
		if info.URL != jobs[i].URL {
			t.Fatalf("refreshed[%d].URL = %q, want %q", i, info.URL, jobs[i].URL)
		}
		if info.WorkerID != jobs[i].WorkerID {
			t.Fatalf("refreshed[%d].WorkerID = %q, want %q", i, info.WorkerID, jobs[i].WorkerID)
		}
		if info.FakeJob != jobs[i].FakeJob {
			t.Fatalf("refreshed[%d].FakeJob = %v, want %v", i, info.FakeJob, jobs[i].FakeJob)
		}
		if info.Token == "" || info.Token == jobs[i].Token {
			t.Fatalf("refreshed[%d].Token was not refreshed", i)
		}

		tok, err := jwt.ParseSigned(info.Token)
		if err != nil {
			t.Fatalf("ParseSigned(refreshed[%d].Token) error = %v", i, err)
		}
		standardClaims := jwt.Claims{}
		grants := auth.ClaimGrants{}
		if err := tok.Claims([]byte("api-secret"), &standardClaims, &grants); err != nil {
			t.Fatalf("refreshed[%d] token Claims() error = %v", i, err)
		}
		if standardClaims.Expiry == nil {
			t.Fatalf("refreshed[%d] token expiry = nil, want one-hour expiry", i)
		}
		if got := standardClaims.Expiry.Time(); !got.Equal(now.Add(time.Hour)) {
			t.Fatalf("refreshed[%d] token expiry = %v, want %v", i, got, now.Add(time.Hour))
		}
		if grants.Identity != jobs[i].AcceptArguments.Identity {
			t.Fatalf("refreshed[%d] token identity = %q, want %q", i, grants.Identity, jobs[i].AcceptArguments.Identity)
		}
	}
}

func TestAgentServerReloadRunningJobsRequiresAPISecretEvenWithoutJobs(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	err := server.ReloadRunningJobs(context.Background(), nil, time.Now())
	if err == nil {
		t.Fatal("ReloadRunningJobs() error = nil, want missing api secret error")
	}
	if !strings.Contains(err.Error(), "api_secret is required to reload jobs") {
		t.Fatalf("ReloadRunningJobs() error = %v, want missing api secret error", err)
	}
}

func TestAgentServerReloadRunningJobsLaunchesRefreshedJobs(t *testing.T) {
	originalToken, err := auth.NewAccessToken("api-key", "api-secret").
		SetIdentity("agent-a").
		SetName("Agent A").
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     "room-a",
			Agent:    true,
		}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	server := NewAgentServer(WorkerOptions{
		WSRL:      "wss://new-livekit.example",
		APIKey:    "api-key",
		APISecret: "api-secret",
	})
	server.workerID = "worker-new"

	entrypointCh := make(chan *JobContext, 1)
	releaseEntrypoint := make(chan struct{})
	if err := server.RTCSession(func(ctx *JobContext) error {
		entrypointCh <- ctx
		<-releaseEntrypoint
		return nil
	}, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}
	t.Cleanup(func() {
		close(releaseEntrypoint)
	})

	job := &livekit.Job{Id: "job-a", Room: &livekit.Room{Name: "room-a"}}
	now := time.Date(2026, 5, 31, 14, 0, 0, 0, time.UTC)
	err = server.ReloadRunningJobs(context.Background(), []ipc.RunningJobInfo{
		{
			AcceptArguments: ipc.JobAcceptArguments{
				Name:       "Agent A",
				Identity:   "agent-a",
				Metadata:   "metadata-a",
				Attributes: map[string]string{"tier": "gold"},
			},
			Job:      job,
			URL:      "wss://old-livekit.example",
			Token:    originalToken,
			WorkerID: "worker-old",
			FakeJob:  true,
		},
	}, now)
	if err != nil {
		t.Fatalf("ReloadRunningJobs() error = %v", err)
	}

	var launched *JobContext
	select {
	case launched = <-entrypointCh:
	case <-time.After(time.Second):
		t.Fatal("reloaded job entrypoint was not invoked")
	}

	if launched.Job != job {
		t.Fatal("reloaded Job pointer was not preserved")
	}
	if launched.url != "wss://new-livekit.example" {
		t.Fatalf("reloaded job url = %q, want current worker URL", launched.url)
	}
	if launched.apiKey != "api-key" {
		t.Fatalf("reloaded job apiKey = %q, want api-key", launched.apiKey)
	}
	if launched.apiSecret != "api-secret" {
		t.Fatalf("reloaded job apiSecret = %q, want api-secret", launched.apiSecret)
	}
	if launched.WorkerID() != "worker-old" {
		t.Fatalf("reloaded job WorkerID() = %q, want original worker id", launched.WorkerID())
	}
	if !launched.fakeJob {
		t.Fatal("reloaded job fakeJob = false, want true")
	}
	if launched.AcceptArguments.Identity != "agent-a" {
		t.Fatalf("reloaded job identity = %q, want agent-a", launched.AcceptArguments.Identity)
	}
	if launched.AcceptArguments.Attributes["tier"] != "gold" {
		t.Fatalf("reloaded job tier = %q, want gold", launched.AcceptArguments.Attributes["tier"])
	}
	if launched.token == "" || launched.token == originalToken {
		t.Fatal("reloaded job token was not refreshed")
	}

	tok, err := jwt.ParseSigned(launched.token)
	if err != nil {
		t.Fatalf("ParseSigned(reloaded token) error = %v", err)
	}
	standardClaims := jwt.Claims{}
	grants := auth.ClaimGrants{}
	if err := tok.Claims([]byte("api-secret"), &standardClaims, &grants); err != nil {
		t.Fatalf("reloaded token Claims() error = %v", err)
	}
	if standardClaims.Expiry == nil {
		t.Fatal("reloaded token expiry = nil, want one-hour expiry")
	}
	if got := standardClaims.Expiry.Time(); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("reloaded token expiry = %v, want %v", got, now.Add(time.Hour))
	}
	if grants.Identity != "agent-a" {
		t.Fatalf("reloaded token identity = %q, want agent-a", grants.Identity)
	}

	activeJobs := server.ActiveRunningJobs()
	if len(activeJobs) != 1 {
		t.Fatalf("ActiveRunningJobs() len after reload = %d, want 1", len(activeJobs))
	}
	if activeJobs[0].Token != launched.token {
		t.Fatal("ActiveRunningJobs()[0].Token does not match refreshed launched token")
	}
}

func TestAgentServerReloadRunningJobsPreservesRecordingOptions(t *testing.T) {
	originalToken, err := auth.NewAccessToken("api-key", "api-secret").
		SetIdentity("agent-a").
		SetVideoGrant(&auth.VideoGrant{RoomJoin: true, Room: "room-a", Agent: true}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	server := NewAgentServer(WorkerOptions{
		WSRL:      "wss://new-livekit.example",
		APIKey:    "api-key",
		APISecret: "api-secret",
	})
	entrypointCh := make(chan *JobContext, 1)
	releaseEntrypoint := make(chan struct{})
	if err := server.RTCSession(func(ctx *JobContext) error {
		entrypointCh <- ctx
		<-releaseEntrypoint
		return nil
	}, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}
	t.Cleanup(func() {
		close(releaseEntrypoint)
	})

	job := &livekit.Job{
		Id:              "job-recording-reload",
		Room:            &livekit.Room{Name: "room-a"},
		EnableRecording: true,
	}
	err = server.ReloadRunningJobs(context.Background(), []ipc.RunningJobInfo{
		{
			AcceptArguments: ipc.JobAcceptArguments{Identity: "agent-a"},
			Job:             job,
			URL:             "wss://old-livekit.example",
			Token:           originalToken,
		},
	}, time.Date(2026, 5, 31, 14, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ReloadRunningJobs() error = %v", err)
	}

	select {
	case launched := <-entrypointCh:
		if launched.Report.RecordingOptions != (agent.RecordingOptions{Audio: true, Traces: true, Logs: true, Transcript: true}) {
			t.Fatalf("RecordingOptions = %#v, want all enabled", launched.Report.RecordingOptions)
		}
	case <-time.After(time.Second):
		t.Fatal("reloaded job entrypoint was not invoked")
	}
}

func TestAgentServerExecuteRunningJobRunsSetupBeforeEntrypoint(t *testing.T) {
	setupCh := make(chan *JobProcess, 1)
	server := NewAgentServer(WorkerOptions{
		APIKey:        "api-key",
		APISecret:     "api-secret",
		HTTPProxy:     "https://proxy.example",
		HTTPProxySet:  true,
		UserArguments: "user-args",
		SetupFunc: func(proc *JobProcess) error {
			proc.Userdata()["setup"] = true
			setupCh <- proc
			return nil
		},
	})
	startedCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		if ctx.Proc().Userdata()["setup"] != true {
			return errors.New("setup did not run before entrypoint")
		}
		startedCh <- ctx
		ctx.Shutdown("session ended")
		return nil
	}

	info := ipc.RunningJobInfo{
		AcceptArguments: ipc.JobAcceptArguments{
			Name:       "support",
			Identity:   "agent-job-process",
			Metadata:   `{"tier":"gold"}`,
			Attributes: map[string]string{"tier": "gold"},
		},
		Job:      &livekit.Job{Id: "job-process", Room: &livekit.Room{Name: "room-process"}},
		URL:      "wss://process.example",
		Token:    "room-token",
		WorkerID: "worker-process",
		FakeJob:  true,
	}

	if err := server.ExecuteRunningJob(context.Background(), info); err != nil {
		t.Fatalf("ExecuteRunningJob() error = %v", err)
	}

	var setupProc *JobProcess
	select {
	case setupProc = <-setupCh:
	case <-time.After(time.Second):
		t.Fatal("setup function was not invoked")
	}
	if setupProc.UserArguments() != "user-args" {
		t.Fatalf("setup UserArguments() = %#v, want user-args", setupProc.UserArguments())
	}
	if setupProc.HTTPProxy() != "https://proxy.example" {
		t.Fatalf("setup HTTPProxy() = %q, want configured proxy", setupProc.HTTPProxy())
	}

	var jobCtx *JobContext
	select {
	case jobCtx = <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("running job entrypoint was not invoked")
	}
	if jobCtx.Job != info.Job {
		t.Fatal("running job did not preserve Job pointer")
	}
	if jobCtx.AcceptArguments.Identity != "agent-job-process" {
		t.Fatalf("identity = %q, want agent-job-process", jobCtx.AcceptArguments.Identity)
	}
	if jobCtx.AcceptArguments.Attributes["tier"] != "gold" {
		t.Fatalf("tier = %q, want gold", jobCtx.AcceptArguments.Attributes["tier"])
	}
	if jobCtx.WorkerID() != "worker-process" {
		t.Fatalf("WorkerID() = %q, want worker-process", jobCtx.WorkerID())
	}
	if jobCtx.url != "wss://process.example" {
		t.Fatalf("url = %q, want process URL", jobCtx.url)
	}
	if jobCtx.token != "room-token" {
		t.Fatalf("token = %q, want room-token", jobCtx.token)
	}
	if !jobCtx.fakeJob {
		t.Fatal("fakeJob = false, want true")
	}
	if got := server.ActiveRunningJobs(); len(got) != 0 {
		t.Fatalf("ActiveRunningJobs() len after completion = %d, want 0", len(got))
	}
}

func TestAgentServerExecuteRunningJobWaitsForShutdownAfterEntrypointCompletes(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	entrypointDone := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		entrypointDone <- ctx
		return nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ExecuteRunningJob(context.Background(), ipc.RunningJobInfo{
			Job:      &livekit.Job{Id: "job-running-wait", Room: &livekit.Room{Name: "room-a"}},
			WorkerID: "worker-running",
		})
	}()

	var jobCtx *JobContext
	select {
	case jobCtx = <-entrypointDone:
	case <-time.After(time.Second):
		t.Fatal("running job entrypoint did not return")
	}

	select {
	case err := <-errCh:
		t.Fatalf("ExecuteRunningJob returned before shutdown: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := server.ActiveRunningJobs(); len(got) != 1 || got[0].Job.GetId() != "job-running-wait" {
		t.Fatalf("ActiveRunningJobs() = %#v, want running job before shutdown", got)
	}

	jobCtx.Shutdown("session ended")

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ExecuteRunningJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteRunningJob did not return after shutdown")
	}
	if got := server.ActiveRunningJobs(); len(got) != 0 {
		t.Fatalf("ActiveRunningJobs() len after shutdown = %d, want 0", len(got))
	}
}

func TestAgentServerHandleReloadMessageReportsAndReloadsJobs(t *testing.T) {
	originalToken, err := auth.NewAccessToken("api-key", "api-secret").
		SetIdentity("agent-a").
		SetName("Agent A").
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     "room-a",
			Agent:    true,
		}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	server := NewAgentServer(WorkerOptions{
		WSRL:      "wss://livekit.example",
		APIKey:    "api-key",
		APISecret: "api-secret",
	})
	server.workerID = "worker-a"
	activeCtx := NewJobContext(&livekit.Job{Id: "job-active", Room: &livekit.Room{Name: "room-active"}}, "wss://livekit.example", "api-key", "api-secret")
	activeCtx.AcceptArguments = JobAcceptArguments{Identity: "agent-active"}
	activeCtx.token = "active-token"
	server.mu.Lock()
	server.activeJobs[activeCtx.Job.Id] = activeCtx
	server.mu.Unlock()

	resp, ok, err := server.handleReloadMessage(context.Background(), &ipc.ActiveJobsRequest{}, 7, time.Now())
	if err != nil {
		t.Fatalf("handleReloadMessage(ActiveJobsRequest) error = %v", err)
	}
	if !ok {
		t.Fatal("handleReloadMessage(ActiveJobsRequest) = false, want true")
	}
	activeResp, ok := resp.(*ipc.ActiveJobsResponse)
	if !ok {
		t.Fatalf("handleReloadMessage(ActiveJobsRequest) response = %T, want *ActiveJobsResponse", resp)
	}
	if activeResp.ReloadCount != 7 {
		t.Fatalf("ActiveJobsResponse.ReloadCount = %d, want 7", activeResp.ReloadCount)
	}
	if len(activeResp.Jobs) != 1 || activeResp.Jobs[0].Job.GetId() != "job-active" {
		t.Fatalf("ActiveJobsResponse.Jobs = %#v, want active job", activeResp.Jobs)
	}

	now := time.Date(2026, 5, 31, 15, 0, 0, 0, time.UTC)
	reloadJob := ipc.RunningJobInfo{
		AcceptArguments: ipc.JobAcceptArguments{Name: "Agent A", Identity: "agent-a"},
		Job:             &livekit.Job{Id: "job-reloaded", Room: &livekit.Room{Name: "room-a"}},
		URL:             "wss://old-livekit.example",
		Token:           originalToken,
		WorkerID:        "worker-a",
	}
	resp, ok, err = server.handleReloadMessage(context.Background(), &ipc.ReloadJobsResponse{
		Jobs:        []ipc.RunningJobInfo{reloadJob},
		ReloadCount: 7,
	}, 7, now)
	if err != nil {
		t.Fatalf("handleReloadMessage(ReloadJobsResponse) error = %v", err)
	}
	if !ok {
		t.Fatal("handleReloadMessage(ReloadJobsResponse) = false, want true")
	}
	if _, ok := resp.(*ipc.Reloaded); !ok {
		t.Fatalf("handleReloadMessage(ReloadJobsResponse) response = %T, want *Reloaded", resp)
	}

	activeJobs := server.ActiveRunningJobs()
	got := map[string]ipc.RunningJobInfo{}
	for _, job := range activeJobs {
		got[job.Job.GetId()] = job
	}
	reloaded, ok := got["job-reloaded"]
	if !ok {
		t.Fatalf("ActiveRunningJobs() missing reloaded job, got %#v", activeJobs)
	}
	if reloaded.Token == "" || reloaded.Token == originalToken {
		t.Fatal("reloaded token was not refreshed")
	}
	tok, err := jwt.ParseSigned(reloaded.Token)
	if err != nil {
		t.Fatalf("ParseSigned(reloaded token) error = %v", err)
	}
	standardClaims := jwt.Claims{}
	grants := auth.ClaimGrants{}
	if err := tok.Claims([]byte("api-secret"), &standardClaims, &grants); err != nil {
		t.Fatalf("reloaded token Claims() error = %v", err)
	}
	if standardClaims.Expiry == nil {
		t.Fatal("reloaded token expiry = nil, want one-hour expiry")
	}
	if got := standardClaims.Expiry.Time(); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("reloaded token expiry = %v, want %v", got, now.Add(time.Hour))
	}
	if grants.Identity != "agent-a" {
		t.Fatalf("reloaded token identity = %q, want agent-a", grants.Identity)
	}

	if resp, ok, err := server.handleReloadMessage(context.Background(), &ipc.PingRequest{}, 7, now); err != nil || ok || resp != nil {
		t.Fatalf("handleReloadMessage(PingRequest) = (%#v, %v, %v), want (nil, false, nil)", resp, ok, err)
	}
}

func TestAgentServerHandleReloadIPCMessageWritesResponse(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.workerID = "worker-a"
	jobCtx := NewJobContext(&livekit.Job{Id: "job-active", Room: &livekit.Room{Name: "room-active"}}, "wss://livekit.example", "api-key", "api-secret")
	jobCtx.AcceptArguments = JobAcceptArguments{Identity: "agent-active"}
	jobCtx.token = "active-token"
	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	req, err := ipc.NewMessage(&ipc.ActiveJobsRequest{})
	if err != nil {
		t.Fatalf("NewMessage(ActiveJobsRequest): %v", err)
	}
	var input bytes.Buffer
	if err := ipc.WriteMessage(&input, req); err != nil {
		t.Fatalf("WriteMessage request: %v", err)
	}
	var output bytes.Buffer

	handled, err := server.handleReloadIPCMessage(context.Background(), &input, &output, 9, time.Now())
	if err != nil {
		t.Fatalf("handleReloadIPCMessage() error = %v", err)
	}
	if !handled {
		t.Fatal("handleReloadIPCMessage() handled = false, want true")
	}

	msg, err := ipc.ReadMessage(&output)
	if err != nil {
		t.Fatalf("ReadMessage response: %v", err)
	}
	if msg.Type != ipc.MessageTypeActiveJobsResponse {
		t.Fatalf("response Type = %q, want %q", msg.Type, ipc.MessageTypeActiveJobsResponse)
	}
	payload, err := ipc.DecodePayload(msg)
	if err != nil {
		t.Fatalf("DecodePayload response: %v", err)
	}
	resp, ok := payload.(*ipc.ActiveJobsResponse)
	if !ok {
		t.Fatalf("response payload = %T, want *ActiveJobsResponse", payload)
	}
	if resp.ReloadCount != 9 {
		t.Fatalf("ReloadCount = %d, want 9", resp.ReloadCount)
	}
	if len(resp.Jobs) != 1 || resp.Jobs[0].Job.GetId() != "job-active" {
		t.Fatalf("Jobs = %#v, want active job", resp.Jobs)
	}
	if resp.Jobs[0].Token != "active-token" {
		t.Fatalf("Jobs[0].Token = %q, want active-token", resp.Jobs[0].Token)
	}
}

func TestAgentServerExecuteRunningJobSetupFailureSkipsEntrypoint(t *testing.T) {
	wantErr := errors.New("setup failed")
	server := NewAgentServer(WorkerOptions{
		SetupFunc: func(*JobProcess) error { return wantErr },
	})
	entrypointCalled := false
	server.entrypointFnc = func(*JobContext) error {
		entrypointCalled = true
		return nil
	}

	err := server.ExecuteRunningJob(context.Background(), ipc.RunningJobInfo{
		Job: &livekit.Job{Id: "job-setup-fails", Room: &livekit.Room{Name: "room-a"}},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExecuteRunningJob() error = %v, want setup failure", err)
	}
	if entrypointCalled {
		t.Fatal("entrypoint was called after setup failure")
	}
	server.mu.Lock()
	_, exists := server.activeJobs["job-setup-fails"]
	server.mu.Unlock()
	if exists {
		t.Fatal("job remained active after setup failure")
	}
}

func TestReloadedJobEntrypointPanicDoesNotCrashProcess(t *testing.T) {
	if os.Getenv("RTP_AGENT_RELOADED_PANIC_HELPER") == "1" {
		server := NewAgentServer(WorkerOptions{})
		server.entrypointFnc = func(*JobContext) error {
			panic("reloaded entrypoint panic")
		}
		jobCtx := NewJobContext(&livekit.Job{Id: "job-reloaded-panic", Room: &livekit.Room{Name: "room-a"}}, "", "", "")
		server.mu.Lock()
		server.activeJobs[jobCtx.Job.Id] = jobCtx
		server.mu.Unlock()

		server.launchReloadedJob(context.Background(), jobCtx)
		time.Sleep(50 * time.Millisecond)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestReloadedJobEntrypointPanicDoesNotCrashProcess$")
	cmd.Env = append(os.Environ(), "RTP_AGENT_RELOADED_PANIC_HELPER=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reloaded job panic helper exited with %v\n%s", err, output)
	}
}

func TestReloadedJobWaitsForShutdownAfterEntrypointCompletes(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	entrypointDone := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		entrypointDone <- ctx
		return nil
	}
	jobCtx := NewJobContext(&livekit.Job{Id: "job-reloaded-wait", Room: &livekit.Room{Name: "room-a"}}, "", "", "")
	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	server.launchReloadedJob(context.Background(), jobCtx)

	select {
	case <-entrypointDone:
	case <-time.After(time.Second):
		t.Fatal("reloaded job entrypoint did not return")
	}

	select {
	case msg := <-sentCh:
		t.Fatalf("received reloaded job status before shutdown: %#v", msg.GetUpdateJob())
	case <-time.After(50 * time.Millisecond):
	}

	jobCtx.Shutdown("session ended")

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job-reloaded-wait", livekit.JobStatus_JS_SUCCESS)
	if got := server.ActiveRunningJobs(); len(got) != 0 {
		t.Fatalf("ActiveRunningJobs() len after shutdown = %d, want 0", len(got))
	}
}

func TestAgentServerProcessReloadIPCMessagesUntilEOF(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.workerID = "worker-a"
	jobCtx := NewJobContext(&livekit.Job{Id: "job-active", Room: &livekit.Room{Name: "room-active"}}, "wss://livekit.example", "api-key", "api-secret")
	jobCtx.token = "active-token"
	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	req, err := ipc.NewMessage(&ipc.ActiveJobsRequest{})
	if err != nil {
		t.Fatalf("NewMessage(ActiveJobsRequest): %v", err)
	}
	var input bytes.Buffer
	if err := ipc.WriteMessage(&input, req); err != nil {
		t.Fatalf("WriteMessage first request: %v", err)
	}
	if err := ipc.WriteMessage(&input, req); err != nil {
		t.Fatalf("WriteMessage second request: %v", err)
	}
	var output bytes.Buffer

	if err := server.processReloadIPCMessages(context.Background(), &input, &output, 10, time.Now()); err != nil {
		t.Fatalf("processReloadIPCMessages() error = %v", err)
	}

	for i := 0; i < 2; i++ {
		msg, err := ipc.ReadMessage(&output)
		if err != nil {
			t.Fatalf("ReadMessage response %d: %v", i, err)
		}
		if msg.Type != ipc.MessageTypeActiveJobsResponse {
			t.Fatalf("response %d Type = %q, want %q", i, msg.Type, ipc.MessageTypeActiveJobsResponse)
		}
		payload, err := ipc.DecodePayload(msg)
		if err != nil {
			t.Fatalf("DecodePayload response %d: %v", i, err)
		}
		resp, ok := payload.(*ipc.ActiveJobsResponse)
		if !ok {
			t.Fatalf("response %d payload = %T, want *ActiveJobsResponse", i, payload)
		}
		if resp.ReloadCount != 10 {
			t.Fatalf("response %d ReloadCount = %d, want 10", i, resp.ReloadCount)
		}
		if len(resp.Jobs) != 1 || resp.Jobs[0].Job.GetId() != "job-active" {
			t.Fatalf("response %d Jobs = %#v, want active job", i, resp.Jobs)
		}
	}
	if _, err := ipc.ReadMessage(&output); err == nil {
		t.Fatal("third ReadMessage response error = nil, want EOF")
	}
}

func TestAgentServerRunReloadIPCSessionRequestsThenProcessesMessages(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	peerReader, workerWriter := io.Pipe()
	workerReader, peerWriter := io.Pipe()
	sessionErr := make(chan error, 1)
	go func() {
		sessionErr <- server.runReloadIPCSession(context.Background(), struct {
			io.Reader
			io.Writer
		}{
			Reader: workerReader,
			Writer: workerWriter,
		}, 11, time.Now())
	}()

	msg, err := ipc.ReadMessage(peerReader)
	if err != nil {
		t.Fatalf("ReadMessage initial request: %v", err)
	}
	if msg.Type != ipc.MessageTypeReloadJobsRequest {
		t.Fatalf("initial request Type = %q, want %q", msg.Type, ipc.MessageTypeReloadJobsRequest)
	}
	if err := ipc.WriteMessage(peerWriter, mustWorkerIPCMessage(t, &ipc.Reloaded{})); err != nil {
		t.Fatalf("WriteMessage Reloaded: %v", err)
	}
	if err := peerWriter.Close(); err != nil {
		t.Fatalf("close peer writer: %v", err)
	}

	select {
	case err := <-sessionErr:
		if err != nil {
			t.Fatalf("runReloadIPCSession() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runReloadIPCSession() did not return after peer close")
	}
}

func TestAgentServerStartReloadIPCSessionFromEnvDialsUnixSocket(t *testing.T) {
	socketPath := tempWorkerUnixSocketPath(t)
	workerConn, watcherConn := net.Pipe()
	t.Cleanup(func() {
		workerConn.Close()
		watcherConn.Close()
	})
	oldDial := workerReloadIPCDial
	workerReloadIPCDial = func(network, address string) (net.Conn, error) {
		if network != "unix" {
			t.Fatalf("reload IPC network = %q, want unix", network)
		}
		if address != socketPath {
			t.Fatalf("reload IPC address = %q, want %q", address, socketPath)
		}
		return workerConn, nil
	}
	t.Cleanup(func() {
		workerReloadIPCDial = oldDial
	})
	t.Setenv("RTP_AGENT_RELOAD_IPC", socketPath)

	server := NewAgentServer(WorkerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.startReloadIPCSessionFromEnv(ctx)

	if err := watcherConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	msg, err := ipc.ReadMessage(watcherConn)
	if err != nil {
		t.Fatalf("ReadMessage initial reload request: %v", err)
	}
	if msg.Type != ipc.MessageTypeReloadJobsRequest {
		t.Fatalf("initial request Type = %q, want %q", msg.Type, ipc.MessageTypeReloadJobsRequest)
	}
}

func TestAgentServerStartReloadIPCSessionFromEnvSkipsEmptySocketPath(t *testing.T) {
	oldDial := workerReloadIPCDial
	workerReloadIPCDial = func(network, address string) (net.Conn, error) {
		t.Fatalf("workerReloadIPCDial called with network=%q address=%q, want no dial without RTP_AGENT_RELOAD_IPC", network, address)
		return nil, nil
	}
	t.Cleanup(func() {
		workerReloadIPCDial = oldDial
	})

	server := NewAgentServer(WorkerOptions{})
	server.startReloadIPCSessionFromEnv(context.Background())
}

func tempWorkerUnixSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rtp-agent-reload-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp /tmp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return filepath.Join(dir, "reload.sock")
}

func mustWorkerIPCMessage(t *testing.T, payload any) ipc.Message {
	t.Helper()
	msg, err := ipc.NewMessage(payload)
	if err != nil {
		t.Fatalf("NewMessage(%T): %v", payload, err)
	}
	return msg
}

func TestEmitWorkerStartedNotifiesHandlers(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	var calls int
	server.OnWorkerStarted(func() {
		calls++
	})

	server.emitWorkerStarted()

	if calls != 1 {
		t.Fatalf("worker started handler calls = %d, want 1", calls)
	}
}

func TestWorkerStartedHandlerPanicDoesNotBlockOtherHandlers(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	var calls int
	server.OnWorkerStarted(func() {
		panic("boom")
	})
	server.OnWorkerStarted(func() {
		calls++
	})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("worker started handler panic propagated: %v", recovered)
		}
	}()

	server.emitWorkerStarted()

	if calls != 1 {
		t.Fatalf("worker started handler calls = %d, want 1", calls)
	}
}

func TestAvailableWorkerStatusMessageIncludesCurrentLoad(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadFunc: func(*AgentServer) float64 {
			return 0.42
		},
	})

	msg := server.availableWorkerStatusMessage()
	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("update worker message is nil")
	}
	if update.GetStatus() != livekit.WorkerStatus_WS_AVAILABLE {
		t.Fatalf("UpdateWorker.Status = %v, want WS_AVAILABLE", update.GetStatus())
	}
	if update.Load != 0.42 {
		t.Fatalf("UpdateWorker.Load = %v, want 0.42", update.Load)
	}
}

func TestWorkerStatusMessagePreservesReferenceNegativeLoad(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadFunc: func(*AgentServer) float64 {
			return -0.25
		},
	})

	msg := server.availableWorkerStatusMessage()
	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("update worker message is nil")
	}
	if update.Load != -0.25 {
		t.Fatalf("UpdateWorker.Load = %v, want reference load -0.25", update.Load)
	}
}

func TestWorkerStatusMessageMarksOverloadedWorkerFull(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadThreshold: 0.5,
		LoadFunc: func(*AgentServer) float64 {
			return 0.8
		},
	})

	msg := server.availableWorkerStatusMessage()
	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("update worker message is nil")
	}
	if update.GetStatus() != livekit.WorkerStatus_WS_FULL {
		t.Fatalf("UpdateWorker.Status = %v, want WS_FULL", update.GetStatus())
	}
}

func TestWorkerStatusMessageUsesSingleLoadSample(t *testing.T) {
	loads := []float64{0.8, 0.1}
	server := NewAgentServer(WorkerOptions{
		LoadThreshold: 0.5,
		LoadFunc: func(*AgentServer) float64 {
			load := loads[0]
			loads = loads[1:]
			return load
		},
	})

	msg := server.availableWorkerStatusMessage()
	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("update worker message is nil")
	}
	if update.GetStatus() != livekit.WorkerStatus_WS_FULL {
		t.Fatalf("UpdateWorker.Status = %v, want WS_FULL", update.GetStatus())
	}
	if update.Load != 0.8 {
		t.Fatalf("UpdateWorker.Load = %v, want the same load sample used for status", update.Load)
	}
	if len(loads) != 1 {
		t.Fatalf("LoadFunc calls consumed %d samples, want 1", 2-len(loads))
	}
}

func TestWorkerStatusMessageSkipsFakeJobsInJobCount(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	realCtx := NewJobContext(&livekit.Job{Id: "job-real"}, "", "", "")
	fakeCtx := NewJobContext(&livekit.Job{Id: "job-fake"}, "", "", "")
	fakeCtx.fakeJob = true
	server.mu.Lock()
	server.activeJobs[realCtx.Job.Id] = realCtx
	server.activeJobs[fakeCtx.Job.Id] = fakeCtx
	server.mu.Unlock()

	msg := server.availableWorkerStatusMessage()
	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("update worker message is nil")
	}
	if update.JobCount != 1 {
		t.Fatalf("UpdateWorker.JobCount = %d, want only non-fake jobs", update.JobCount)
	}
}

func TestDrainingWorkerStatusMessageReportsFullWithoutLoad(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadFunc: func(*AgentServer) float64 {
			return 0.9
		},
	})

	msg := server.drainingWorkerStatusMessage()
	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("update worker message is nil")
	}
	if update.GetStatus() != livekit.WorkerStatus_WS_FULL {
		t.Fatalf("UpdateWorker.Status = %v, want WS_FULL", update.GetStatus())
	}
	if update.Load != 0 {
		t.Fatalf("UpdateWorker.Load = %v, want 0 while draining", update.Load)
	}
}

func TestWorkerStatusUpdatesPeriodically(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadFunc: func(*AgentServer) float64 {
			return 0.25
		},
	})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.runWorkerStatusUpdates(ctx, time.Millisecond)

	msg := receiveWorkerMessage(t, sentCh)
	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("update worker message is nil")
	}
	if update.GetStatus() != livekit.WorkerStatus_WS_AVAILABLE {
		t.Fatalf("UpdateWorker.Status = %v, want WS_AVAILABLE", update.GetStatus())
	}
	if update.Load != 0.25 {
		t.Fatalf("UpdateWorker.Load = %v, want 0.25", update.Load)
	}
}

func TestAgentIdentityForJobIDUsesFullJobID(t *testing.T) {
	jobID := "job_123456789"
	want := "agent-" + jobID

	if got := workerlivekit.AgentIdentityForJobID(jobID); got != want {
		t.Fatalf("AgentIdentityForJobID(%q) = %q, want %q", jobID, got, want)
	}
}

func TestAgentIdentityForJobIDHandlesShortJobID(t *testing.T) {
	jobID := "abc"
	want := "agent-abc"

	if got := workerlivekit.AgentIdentityForJobID(jobID); got != want {
		t.Fatalf("AgentIdentityForJobID(%q) = %q, want %q", jobID, got, want)
	}
}

func TestAgentIdentityForJobIDHandlesEmptyJobID(t *testing.T) {
	if got, want := workerlivekit.AgentIdentityForJobID(""), "agent-"; got != want {
		t.Fatalf("AgentIdentityForJobID(empty) = %q, want %q", got, want)
	}
}

func TestAvailabilityResponseAcceptsWithDefaultIdentity(t *testing.T) {
	req := &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_abc123"},
	}

	resp := workerlivekit.AvailabilityResponseForAccept(req, workerlivekit.AvailabilityAcceptOptions{}, "")
	availability := resp.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if !availability.Available {
		t.Fatal("availability.Available = false, want true")
	}
	if availability.JobId != "job_abc123" {
		t.Fatalf("availability.JobId = %q, want %q", availability.JobId, "job_abc123")
	}
	if availability.ParticipantIdentity != "agent-job_abc123" {
		t.Fatalf("availability.ParticipantIdentity = %q, want default identity", availability.ParticipantIdentity)
	}
	agentName, ok := availability.ParticipantAttributes["lk.agent.name"]
	if !ok {
		t.Fatal("availability.ParticipantAttributes missing lk.agent.name")
	}
	if agentName != "" {
		t.Fatalf("availability.ParticipantAttributes[lk.agent.name] = %q, want empty string", agentName)
	}
}

func TestAvailabilityResponseAcceptUsesCustomArguments(t *testing.T) {
	req := &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_custom"},
	}

	resp := workerlivekit.AvailabilityResponseForAccept(req, workerlivekit.AvailabilityAcceptOptions{
		Name:     "Agent Name",
		Identity: "custom-agent",
		Metadata: "custom-metadata",
		Attributes: map[string]string{
			"tier": "gold",
		},
	}, "sales-agent")

	availability := resp.GetAvailability()
	if !availability.Available {
		t.Fatal("availability.Available = false, want true")
	}
	if availability.JobId != "job_custom" {
		t.Fatalf("availability.JobId = %q, want job_custom", availability.JobId)
	}
	if availability.ParticipantIdentity != "custom-agent" {
		t.Fatalf("availability.ParticipantIdentity = %q, want custom identity", availability.ParticipantIdentity)
	}
	if availability.ParticipantName != "Agent Name" {
		t.Fatalf("availability.ParticipantName = %q, want custom name", availability.ParticipantName)
	}
	if availability.ParticipantMetadata != "custom-metadata" {
		t.Fatalf("availability.ParticipantMetadata = %q, want custom metadata", availability.ParticipantMetadata)
	}
	if availability.ParticipantAttributes["tier"] != "gold" {
		t.Fatalf("availability.ParticipantAttributes[tier] = %q, want gold", availability.ParticipantAttributes["tier"])
	}
	if availability.ParticipantAttributes["lk.agent.name"] != "sales-agent" {
		t.Fatalf("availability.ParticipantAttributes[lk.agent.name] = %q, want sales-agent", availability.ParticipantAttributes["lk.agent.name"])
	}
}

func TestAvailabilityResponseRejectsJob(t *testing.T) {
	req := &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_reject"},
	}

	resp := workerlivekit.AvailabilityResponseForReject(req, workerlivekit.AvailabilityRejectOptions{Terminate: true})
	availability := resp.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if availability.Available {
		t.Fatal("availability.Available = true, want false")
	}
	if availability.JobId != "job_reject" {
		t.Fatalf("availability.JobId = %q, want %q", availability.JobId, "job_reject")
	}
	if !availability.Terminate {
		t.Fatal("availability.Terminate = false, want true")
	}
}

func TestAvailabilityResponseRejectCanAvoidTermination(t *testing.T) {
	req := &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_requeue"},
	}

	resp := workerlivekit.AvailabilityResponseForReject(req, workerlivekit.AvailabilityRejectOptions{Terminate: false})
	availability := resp.GetAvailability()
	if availability.Terminate {
		t.Fatal("availability.Terminate = true, want false")
	}
}

func TestHandleAvailabilityRejectsWhenDraining(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}

	server.draining = true
	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_drain_reject"},
	})

	msg := receiveWorkerMessage(t, sentCh)
	availability := msg.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if availability.Available {
		t.Fatal("availability.Available = true, want false")
	}
	if availability.JobId != "job_drain_reject" {
		t.Fatalf("availability.JobId = %q, want job_drain_reject", availability.JobId)
	}
	if availability.Terminate {
		t.Fatal("availability.Terminate = true, want false")
	}
}

func TestHandleAvailabilityRejectsWhenRequestCallbackDoesNotAnswer(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.requestFnc = func(req *JobRequest) error {
		return nil
	}

	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_no_answer"},
	})

	msg := receiveWorkerMessage(t, sentCh)
	availability := msg.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if availability.Available {
		t.Fatal("availability.Available = true, want false")
	}
	if availability.JobId != "job_no_answer" {
		t.Fatalf("availability.JobId = %q, want job_no_answer", availability.JobId)
	}
	if availability.Terminate {
		t.Fatal("availability.Terminate = true, want false")
	}
}

func TestHandleAvailabilityDefaultAcceptLeavesParticipantNameEmpty(t *testing.T) {
	server := NewAgentServer(WorkerOptions{AgentName: "sales-agent"})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}

	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_default_name"},
	})

	availability := receiveWorkerMessage(t, sentCh).GetAvailability()
	if availability == nil || !availability.Available {
		t.Fatal("availability response was not accepted")
	}
	if availability.ParticipantName != "" {
		t.Fatalf("ParticipantName = %q, want empty default name", availability.ParticipantName)
	}
	if availability.ParticipantAttributes["lk.agent.name"] != "sales-agent" {
		t.Fatalf("ParticipantAttributes[lk.agent.name] = %q, want sales-agent", availability.ParticipantAttributes["lk.agent.name"])
	}
}

func TestHandleAvailabilityRejectsWhenLoadExceedsThreshold(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadThreshold: 0.5,
		LoadFunc: func(*AgentServer) float64 {
			return 0.8
		},
	})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	requestCalled := false
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.requestFnc = func(req *JobRequest) error {
		requestCalled = true
		return nil
	}

	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_full_load"},
	})

	msg := receiveWorkerMessage(t, sentCh)
	availability := msg.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if availability.Available {
		t.Fatal("availability.Available = true, want false")
	}
	if availability.JobId != "job_full_load" {
		t.Fatalf("availability.JobId = %q, want job_full_load", availability.JobId)
	}
	if availability.Terminate {
		t.Fatal("availability.Terminate = true, want false")
	}
	if requestCalled {
		t.Fatal("request callback was called while worker was over load threshold")
	}
}

func TestAvailabilityAllowsReservedSlotsWithInfiniteLoadThreshold(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadThreshold: math.Inf(1),
		LoadFunc: func(*AgentServer) float64 {
			return 0
		},
	})
	server.reserveAvailabilitySlot()

	if !server.availableForJob() {
		t.Fatal("availableForJob() = false, want true for infinite load threshold")
	}
}

func TestHandleAvailabilityCountsPendingAcceptsAsReservedLoad(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadThreshold:    0.5,
		NumIdleProcesses: 1,
		LoadFunc: func(*AgentServer) float64 {
			return 0
		},
	})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}

	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_reserved_one"},
	})
	first := receiveWorkerMessage(t, sentCh).GetAvailability()
	if first == nil || !first.Available {
		t.Fatal("first availability response was not accepted")
	}

	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_reserved_two"},
	})
	second := receiveWorkerMessage(t, sentCh).GetAvailability()
	if second == nil {
		t.Fatal("second availability response is nil")
	}
	if second.Available {
		t.Fatal("second availability response was accepted despite reserved load")
	}
	if second.JobId != "job_reserved_two" {
		t.Fatalf("second availability JobId = %q, want job_reserved_two", second.JobId)
	}
}

func TestAvailabilityReservesLoadWhileRequestCallbackRuns(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadThreshold:    0.5,
		NumIdleProcesses: 1,
		LoadFunc: func(*AgentServer) float64 {
			return 0
		},
	})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.requestFnc = func(req *JobRequest) error {
		if req.ID() == "job_reserving_one" {
			close(requestStarted)
			<-releaseRequest
			return req.Accept(JobAcceptArguments{})
		}
		return req.Accept(JobAcceptArguments{})
	}

	doneCh := make(chan struct{})
	go func() {
		server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
			Job: &livekit.Job{Id: "job_reserving_one"},
		})
		close(doneCh)
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("first request callback did not start")
	}

	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_reserving_two"},
	})

	second := receiveWorkerMessage(t, sentCh).GetAvailability()
	if second == nil {
		t.Fatal("second availability response is nil")
	}
	if second.Available {
		t.Fatal("second availability response was accepted despite in-flight request reservation")
	}
	if second.JobId != "job_reserving_two" {
		t.Fatalf("second availability JobId = %q, want job_reserving_two", second.JobId)
	}

	close(releaseRequest)
	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
}

func TestHandleAvailabilityReturnsWhileRequestCallbackRuns(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		return nil
	}
	server.requestFnc = func(req *JobRequest) error {
		close(requestStarted)
		<-releaseRequest
		return req.Accept(JobAcceptArguments{})
	}

	doneCh := make(chan struct{})
	go func() {
		server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
			Job: &livekit.Job{Id: "job_async_request"},
		})
		close(doneCh)
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request callback did not start")
	}

	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Fatal("handleAvailability blocked on request callback")
	}

	close(releaseRequest)
}

func TestHandleRegisterReportsActiveJobs(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	jobCtx := NewJobContext(&livekit.Job{Id: "job_active"}, "", "", "")
	fakeCtx := NewJobContext(&livekit.Job{Id: "mock-job-local"}, "", "", "")
	fakeCtx.fakeJob = true
	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.activeJobs[fakeCtx.Job.Id] = fakeCtx
	server.mu.Unlock()

	server.handleMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Register{
			Register: &livekit.RegisterWorkerResponse{WorkerId: "worker-a"},
		},
	})

	msg := receiveWorkerMessage(t, sentCh)
	migrate := msg.GetMigrateJob()
	if migrate == nil {
		t.Fatal("migrate job message is nil")
	}
	if len(migrate.JobIds) != 1 || migrate.JobIds[0] != "job_active" {
		t.Fatalf("MigrateJob.JobIds = %v, want only non-fake job [job_active]", migrate.JobIds)
	}
}

func TestInitialRegisterMessageRejectsNonRegisterMessage(t *testing.T) {
	register := &livekit.RegisterWorkerResponse{WorkerId: "worker-ok"}
	gotRegister, err := workerlivekit.InitialRegisterResponse(&livekit.ServerMessage{
		Message: &livekit.ServerMessage_Register{
			Register: register,
		},
	})
	if err != nil {
		t.Fatalf("InitialRegisterResponse(register) error = %v", err)
	}
	if gotRegister != register {
		t.Fatalf("InitialRegisterResponse(register) = %p, want %p", gotRegister, register)
	}

	_, err = workerlivekit.InitialRegisterResponse(&livekit.ServerMessage{
		Message: &livekit.ServerMessage_Availability{
			Availability: &livekit.AvailabilityRequest{Job: &livekit.Job{Id: "job_early"}},
		},
	})
	if err == nil {
		t.Fatal("InitialRegisterResponse() error = nil, want expected register response error")
	}
	if got, want := err.Error(), "expected register response as first message"; got != want {
		t.Fatalf("InitialRegisterResponse() error = %q, want %q", got, want)
	}
}

func TestAcceptedAvailabilityExpiresWithoutAssignment(t *testing.T) {
	oldTimeout := assignmentTimeout
	assignmentTimeout = 10 * time.Millisecond
	t.Cleanup(func() {
		assignmentTimeout = oldTimeout
	})

	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}

	job := &livekit.Job{Id: "job_assignment_timeout", Room: &livekit.Room{Name: "room-a"}}
	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{Job: job})
	availability := receiveWorkerMessage(t, sentCh).GetAvailability()
	if availability == nil || !availability.Available {
		t.Fatal("availability response was not accepted")
	}

	deadline := time.After(time.Second)
	for {
		server.mu.Lock()
		_, pending := server.pendingAccepts[job.Id]
		server.mu.Unlock()
		if !pending {
			return
		}

		select {
		case <-deadline:
			t.Fatal("accepted arguments remained pending after assignment timeout")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestAssignmentPreservesAcceptedParticipantIdentity(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.requestFnc = func(req *JobRequest) error {
		return req.Accept(JobAcceptArguments{Identity: "custom-agent"})
	}
	startedCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	job := &livekit.Job{Id: "job_custom_identity", Room: &livekit.Room{Name: "room-a"}}
	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{Job: job})
	availability := receiveWorkerMessage(t, sentCh).GetAvailability()
	if availability == nil || !availability.Available {
		t.Fatal("availability response was not accepted")
	}
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	select {
	case jobCtx := <-startedCh:
		if jobCtx.AcceptArguments.Identity != "custom-agent" {
			t.Fatalf("AcceptArguments.Identity = %q, want custom-agent", jobCtx.AcceptArguments.Identity)
		}
		if got := jobCtx.ParticipantIdentity(); got != "custom-agent" {
			t.Fatalf("ParticipantIdentity() = %q, want custom-agent", got)
		}
	case <-time.After(time.Second):
		t.Fatal("assignment entrypoint did not run")
	}

	server.mu.Lock()
	_, pending := server.pendingAccepts[job.Id]
	server.mu.Unlock()
	if pending {
		t.Fatal("accepted arguments remained pending after assignment")
	}
}

func TestAssignmentIgnoresUnknownJob(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	startedCh := make(chan *JobContext, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	job := &livekit.Job{Id: "job_unknown_assignment", Room: &livekit.Room{Name: "room-a"}}
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	select {
	case <-startedCh:
		t.Fatal("unknown assignment started entrypoint")
	case <-sentCh:
		t.Fatal("unknown assignment sent worker message")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAssignmentUsesAssignmentURLWhenProvided(t *testing.T) {
	server := NewAgentServer(WorkerOptions{WSRL: "wss://worker.example"})
	startedCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	assignmentURL := "wss://assignment.example"
	job := &livekit.Job{Id: "job_assignment_url", Room: &livekit.Room{Name: "room-a"}}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{
		Job: job,
		Url: &assignmentURL,
	})

	select {
	case jobCtx := <-startedCh:
		if jobCtx.url != assignmentURL {
			t.Fatalf("jobCtx.url = %q, want assignment URL", jobCtx.url)
		}
	case <-time.After(time.Second):
		t.Fatal("assignment entrypoint did not run")
	}
}

func TestAssignmentRecordsRegisteredWorkerID(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	server.handleMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Register{
			Register: &livekit.RegisterWorkerResponse{WorkerId: "worker-a"},
		},
	})

	job := &livekit.Job{Id: "job_worker_id", Room: &livekit.Room{Name: "room-a"}}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	select {
	case jobCtx := <-startedCh:
		if jobCtx.WorkerID() != "worker-a" {
			t.Fatalf("jobCtx.WorkerID() = %q, want worker-a", jobCtx.WorkerID())
		}
	case <-time.After(time.Second):
		t.Fatal("assignment entrypoint did not run")
	}
}

func TestAssignmentInitializesJobLogContextFields(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	server.handleMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Register{
			Register: &livekit.RegisterWorkerResponse{WorkerId: "worker-log"},
		},
	})

	job := &livekit.Job{
		Id: "job_log_fields",
		Room: &livekit.Room{
			Sid:  "RM_log",
			Name: "room-log",
		},
	}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	select {
	case jobCtx := <-startedCh:
		fields := jobCtx.LogContextFields()
		if jobCtx.WorkerID() != "worker-log" {
			t.Fatalf("WorkerID() = %q, want worker-log", jobCtx.WorkerID())
		}
		if fields["job_id"] != "job_log_fields" {
			t.Fatalf("log job_id = %#v, want job_log_fields", fields["job_id"])
		}
		if fields["worker_id"] != "worker-log" {
			t.Fatalf("log worker_id = %#v, want worker-log", fields["worker_id"])
		}
		if fields["room"] != "room-log" {
			t.Fatalf("log room = %#v, want room-log", fields["room"])
		}
	case <-time.After(time.Second):
		t.Fatal("assignment entrypoint did not run")
	}
}

func TestAssignmentEnablesRecordingOptionsWhenRequested(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	job := &livekit.Job{
		Id:              "job_recording",
		Room:            &livekit.Room{Name: "room-a"},
		EnableRecording: true,
	}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	select {
	case jobCtx := <-startedCh:
		if jobCtx.Report.RecordingOptions != (agent.RecordingOptions{Audio: true, Traces: true, Logs: true, Transcript: true}) {
			t.Fatalf("RecordingOptions = %#v, want all enabled", jobCtx.Report.RecordingOptions)
		}
	case <-time.After(time.Second):
		t.Fatal("assignment entrypoint did not run")
	}
}

func TestAssignmentSendsRunningJobStatus(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}

	job := &livekit.Job{Id: "job_running_status", Room: &livekit.Room{Name: "room-a"}}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	msg := receiveWorkerMessage(t, sentCh)
	update := msg.GetUpdateJob()
	if update == nil {
		t.Fatal("update job message is nil")
	}
	if update.JobId != "job_running_status" {
		t.Fatalf("UpdateJob.JobId = %q, want job_running_status", update.JobId)
	}
	if update.Status != livekit.JobStatus_JS_RUNNING {
		t.Fatalf("UpdateJob.Status = %v, want JS_RUNNING", update.Status)
	}
}

func TestAssignmentReportsSuccessWhenJobContextShutsDown(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
		ctx.Shutdown("session ended")
		return nil
	}

	job := &livekit.Job{Id: "job_success_status", Room: &livekit.Room{Name: "room-a"}}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_success_status", livekit.JobStatus_JS_RUNNING)
	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_success_status", livekit.JobStatus_JS_SUCCESS)

	server.mu.Lock()
	_, exists := server.activeJobs[job.Id]
	server.mu.Unlock()
	if exists {
		t.Fatal("assigned job remained in activeJobs after job context shutdown")
	}
}

func TestAssignmentCompletionUploadsRecordedSessionReport(t *testing.T) {
	oldUpload := uploadSessionReport
	uploadCh := make(chan struct {
		cloudURL string
		apiKey   string
		secret   string
		agent    string
		report   *agent.SessionReport
	}, 1)
	uploadSessionReport = func(cloudURL string, apiKey string, apiSecret string, agentName string, report *agent.SessionReport) error {
		uploadCh <- struct {
			cloudURL string
			apiKey   string
			secret   string
			agent    string
			report   *agent.SessionReport
		}{
			cloudURL: cloudURL,
			apiKey:   apiKey,
			secret:   apiSecret,
			agent:    agentName,
			report:   report,
		}
		return nil
	}
	defer func() { uploadSessionReport = oldUpload }()

	server := NewAgentServer(WorkerOptions{
		APIKey:    "api-key",
		APISecret: "api-secret",
		AgentName: "support-agent",
	})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
		ctx.Report.Room = ctx.Job.GetRoom().GetName()
		ctx.Shutdown("session ended")
		return nil
	}

	assignmentURL := "wss://tenant.livekit.cloud"
	job := &livekit.Job{
		Id:              "job_upload_report",
		Room:            &livekit.Room{Name: "room-a"},
		EnableRecording: true,
	}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{
		Job: job,
		Url: &assignmentURL,
	})

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_upload_report", livekit.JobStatus_JS_RUNNING)
	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_upload_report", livekit.JobStatus_JS_SUCCESS)

	select {
	case upload := <-uploadCh:
		if upload.cloudURL != assignmentURL {
			t.Fatalf("upload cloudURL = %q, want assignment URL", upload.cloudURL)
		}
		if upload.apiKey != "api-key" || upload.secret != "api-secret" || upload.agent != "support-agent" {
			t.Fatalf("upload credentials = (%q, %q, %q), want server credentials", upload.apiKey, upload.secret, upload.agent)
		}
		if upload.report.Room != "room-a" {
			t.Fatalf("uploaded report Room = %q, want room-a", upload.report.Room)
		}
	case <-time.After(time.Second):
		t.Fatal("recorded assignment did not upload session report")
	}
}

func TestAssignmentWaitsForShutdownAfterEntrypointCompletes(t *testing.T) {
	oldUpload := uploadSessionReport
	uploadCh := make(chan struct{}, 1)
	uploadSessionReport = func(string, string, string, string, *agent.SessionReport) error {
		uploadCh <- struct{}{}
		return nil
	}
	defer func() { uploadSessionReport = oldUpload }()

	server := NewAgentServer(WorkerOptions{
		APIKey:    "api-key",
		APISecret: "api-secret",
		AgentName: "support-agent",
	})
	sentCh := make(chan *livekit.WorkerMessage, 3)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	entrypointDone := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		ctx.Report.Room = ctx.Job.GetRoom().GetName()
		entrypointDone <- ctx
		return nil
	}

	assignmentURL := "wss://tenant.livekit.cloud"
	job := &livekit.Job{
		Id:              "job_wait_shutdown",
		Room:            &livekit.Room{Name: "room-a"},
		EnableRecording: true,
	}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{
		Job: job,
		Url: &assignmentURL,
	})

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_wait_shutdown", livekit.JobStatus_JS_RUNNING)

	var jobCtx *JobContext
	select {
	case jobCtx = <-entrypointDone:
	case <-time.After(time.Second):
		t.Fatal("entrypoint did not return")
	}

	select {
	case msg := <-sentCh:
		t.Fatalf("received job status before shutdown: %#v", msg.GetUpdateJob())
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-uploadCh:
		t.Fatal("uploaded session report before shutdown")
	case <-time.After(50 * time.Millisecond):
	}

	server.mu.Lock()
	_, exists := server.activeJobs[job.Id]
	server.mu.Unlock()
	if !exists {
		t.Fatal("assigned job left activeJobs before shutdown")
	}

	jobCtx.Shutdown("session ended")

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_wait_shutdown", livekit.JobStatus_JS_SUCCESS)
	select {
	case <-uploadCh:
	case <-time.After(time.Second):
		t.Fatal("session report did not upload after shutdown")
	}
}

func TestShouldUploadJobSessionReportUsesAnyRecordingOption(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_logs_only"}, "", "", "")
	ctx.Report.RecordingOptions = agent.RecordingOptions{Logs: true}

	if !shouldUploadJobSessionReport(ctx) {
		t.Fatal("shouldUploadJobSessionReport(logs-only) = false, want true")
	}
}

func TestShouldUploadJobSessionReportUsesEvaluationOrOutcome(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_eval_only"}, "", "", "")
	ctx.Report.RecordingOptions = agent.RecordingOptions{}
	ctx.Tagger().Evaluation(&agent.EvaluationResult{Judgments: map[string]string{"helpfulness": "pass"}})

	if !shouldUploadJobSessionReport(ctx) {
		t.Fatal("shouldUploadJobSessionReport(evaluation-only) = false, want true")
	}

	ctx = NewJobContext(&livekit.Job{Id: "job_outcome_only"}, "", "", "")
	ctx.Report.RecordingOptions = agent.RecordingOptions{}
	ctx.Tagger().Success("completed")

	if !shouldUploadJobSessionReport(ctx) {
		t.Fatal("shouldUploadJobSessionReport(outcome-only) = false, want true")
	}
}

func TestAssignmentReportsFailureWhenEntrypointFails(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
		return errors.New("entrypoint failed")
	}

	job := &livekit.Job{Id: "job_failed_status", Room: &livekit.Room{Name: "room-a"}}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_failed_status", livekit.JobStatus_JS_RUNNING)
	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_failed_status", livekit.JobStatus_JS_FAILED)

	server.mu.Lock()
	_, exists := server.activeJobs[job.Id]
	server.mu.Unlock()
	if exists {
		t.Fatal("assigned job remained in activeJobs after failed entrypoint completion")
	}
}

func TestAssignmentReportsFailureWhenEntrypointPanics(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
		panic("entrypoint panic")
	}

	job := &livekit.Job{Id: "job_panic_status", Room: &livekit.Room{Name: "room-a"}}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_panic_status", livekit.JobStatus_JS_RUNNING)
	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_panic_status", livekit.JobStatus_JS_FAILED)

	server.mu.Lock()
	_, exists := server.activeJobs[job.Id]
	server.mu.Unlock()
	if exists {
		t.Fatal("assigned job remained in activeJobs after panicked entrypoint")
	}
}

func TestAssignmentPreservesAssignmentToken(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	job := &livekit.Job{Id: "job_assignment_token", Room: &livekit.Room{Name: "room-a"}}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{
		Job:   job,
		Token: "assignment-token",
	})

	select {
	case jobCtx := <-startedCh:
		if jobCtx.token != "assignment-token" {
			t.Fatalf("jobCtx.token = %q, want assignment-token", jobCtx.token)
		}
	case <-time.After(time.Second):
		t.Fatal("assignment entrypoint did not run")
	}
}

func TestJobRequestRejectDefaultsToTerminate(t *testing.T) {
	var got JobRejectArguments
	req := workerlivekit.NewJobRequest(nil, nil, func(args JobRejectArguments) error {
		got = args
		return nil
	})

	if err := req.Reject(); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if !got.Terminate {
		t.Fatal("Reject() Terminate = false, want true")
	}
}

func TestJobRequestAcceptDefaultsIdentityBeforeCallback(t *testing.T) {
	var got JobAcceptArguments
	req := workerlivekit.NewJobRequest(&livekit.Job{Id: "job_identity"}, func(args JobAcceptArguments) error {
		got = args
		return nil
	}, nil)

	if err := req.Accept(JobAcceptArguments{}); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if got.Identity != "agent-job_identity" {
		t.Fatalf("Accept() Identity = %q, want default identity", got.Identity)
	}
}

func TestJobRequestAcceptCanUseDefaultArguments(t *testing.T) {
	var got JobAcceptArguments
	req := workerlivekit.NewJobRequest(&livekit.Job{Id: "job_default_accept"}, func(args JobAcceptArguments) error {
		got = args
		return nil
	}, nil)

	if err := req.Accept(); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if got.Identity != "agent-job_default_accept" {
		t.Fatalf("Accept() Identity = %q, want default identity", got.Identity)
	}
	if got.Name != "" {
		t.Fatalf("Accept() Name = %q, want empty", got.Name)
	}
	if got.Metadata != "" {
		t.Fatalf("Accept() Metadata = %q, want empty", got.Metadata)
	}
	if got.Attributes != nil {
		t.Fatalf("Accept() Attributes = %#v, want nil", got.Attributes)
	}
}

func TestJobRequestConstructorAcceptsRootCallbackTypes(t *testing.T) {
	var accepted JobAcceptArguments
	var rejected JobRejectArguments
	req := workerlivekit.NewJobRequest(
		&livekit.Job{Id: "job_root_callbacks"},
		func(args JobAcceptArguments) error {
			accepted = args
			return nil
		},
		func(args JobRejectArguments) error {
			rejected = args
			return nil
		},
	)

	if err := req.Accept(JobAcceptArguments{Name: "Agent Root"}); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if accepted.Name != "Agent Root" {
		t.Fatalf("accepted name = %q, want Agent Root", accepted.Name)
	}
	if err := req.Reject(JobRejectArguments{Terminate: false}); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if rejected.Terminate {
		t.Fatal("rejected terminate = true, want false")
	}
}

func TestValidateRunPreconditionsRequiresRTCSession(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		WSRL:      "wss://livekit.example",
		APIKey:    "key",
		APISecret: "secret",
	})

	err := server.validateRunPreconditions()
	if err == nil {
		t.Fatal("validateRunPreconditions() error = nil, want missing RTC session error")
	}
	if got, want := err.Error(), rtcSessionRequiredMessage; got != want {
		t.Fatalf("validateRunPreconditions() error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "no RTC session entrypoint") {
		t.Fatalf("validateRunPreconditions() error = %q, want reference capitalization", err.Error())
	}
}

func TestValidateRunPreconditionsRequiresCredentialsAfterRTCSession(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil)

	err := server.validateRunPreconditions()
	if err == nil {
		t.Fatal("validateRunPreconditions() error = nil, want missing credentials error")
	}
	if !strings.Contains(err.Error(), "ws_url is required") {
		t.Fatalf("validateRunPreconditions() error = %q, want ws_url credentials message", err.Error())
	}
}

func TestValidateRunPreconditionsRejectsUnknownWorkerTransport(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Transport: WorkerTransport("matrix"),
		WSRL:      "wss://livekit.example",
		APIKey:    "key",
		APISecret: "secret",
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	err := server.validateRunPreconditions()
	if err == nil {
		t.Fatal("validateRunPreconditions() error = nil, want unknown transport error")
	}
	if !strings.Contains(err.Error(), "unknown worker transport") {
		t.Fatalf("validateRunPreconditions() error = %q, want unknown worker transport", err.Error())
	}
}

func TestValidateRunPreconditionsAcceptsAgoraWithoutProviderOptions(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Transport: WorkerTransportAgora,
		WSRL:      "",
		APIKey:    "",
		APISecret: "",
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	if err := server.validateRunPreconditions(); err != nil {
		t.Fatalf("validateRunPreconditions() error = %v, want provider-neutral preconditions", err)
	}
	if server.Options.Transport != WorkerTransportAgora {
		t.Fatalf("Transport = %q, want %q", server.Options.Transport, WorkerTransportAgora)
	}
	if server.Options.WSRL != "" || server.Options.APIKey != "" || server.Options.APISecret != "" {
		t.Fatalf("LiveKit credentials mutated for Agora transport: url=%q key=%q secret=%q", server.Options.WSRL, server.Options.APIKey, server.Options.APISecret)
	}
}

func TestValidateRunPreconditionsAcceptsAgoraTransportConfig(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Transport: WorkerTransportAgora,
		Agora: AgoraOptions{
			AppID:   "app",
			Channel: "support",
			UID:     "agent",
		},
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	if err := server.validateRunPreconditions(); err != nil {
		t.Fatalf("validateRunPreconditions() error = %v", err)
	}
}

func TestRunRequiresAgoraTransportRunFunc(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Transport: WorkerTransportAgora,
		Agora: AgoraOptions{
			AppID:   "app",
			Channel: "support",
			UID:     "agent",
		},
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Run() error = nil, want missing Agora transport runner")
		}
		if !strings.Contains(err.Error(), "agora transport run function") {
			t.Fatalf("Run() error = %q, want agora transport run function", err.Error())
		}
	case <-ctx.Done():
		t.Fatal("Run() did not return missing Agora transport runner error")
	}
}

func TestRunRequiresAgoraTransportRunnerWithoutProviderValidation(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		Transport: WorkerTransportAgora,
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	err := server.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want missing transport runner")
	}
	if !strings.Contains(err.Error(), "agora transport run function") {
		t.Fatalf("Run() error = %q, want missing transport runner", err.Error())
	}
	if strings.Contains(err.Error(), "AGORA_") {
		t.Fatalf("Run() error = %q, want no provider-specific validation", err.Error())
	}
}

func TestValidateRunPreconditionsReportsSpecificMissingCredential(t *testing.T) {
	tests := []struct {
		name    string
		options WorkerOptions
		want    string
	}{
		{
			name:    "ws url",
			options: WorkerOptions{APIKey: "key", APISecret: "secret"},
			want:    "ws_url is required, or set LIVEKIT_URL environment variable",
		},
		{
			name:    "api key",
			options: WorkerOptions{WSRL: "wss://livekit.example", APISecret: "secret"},
			want:    "api_key is required, or set LIVEKIT_API_KEY environment variable",
		},
		{
			name:    "api secret",
			options: WorkerOptions{WSRL: "wss://livekit.example", APIKey: "key"},
			want:    "api_secret is required, or set LIVEKIT_API_SECRET environment variable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewAgentServer(tt.options)
			if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
				t.Fatalf("RTCSession() error = %v", err)
			}

			err := server.validateRunPreconditions()
			if err == nil {
				t.Fatal("validateRunPreconditions() error = nil, want missing credential error")
			}
			if err.Error() != tt.want {
				t.Fatalf("validateRunPreconditions() error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidateRunPreconditionsNormalizesCloudLoadOptions(t *testing.T) {
	oldSampler := defaultWorkerLoadSample
	oldCalc := defaultWorkerLoadCalc
	defaultWorkerLoadSample = func() float64 { return 0.64 }
	defaultWorkerLoadCalc = nil
	t.Cleanup(func() {
		defaultWorkerLoadSample = oldSampler
		defaultWorkerLoadCalc = oldCalc
	})

	server := NewAgentServer(WorkerOptions{
		WSRL:          "wss://livekit.example",
		APIKey:        "key",
		APISecret:     "secret",
		WorkerToken:   "worker-token",
		LoadThreshold: 0.2,
		LoadFunc: func(*AgentServer) float64 {
			return 0.9
		},
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	if err := server.validateRunPreconditions(); err != nil {
		t.Fatalf("validateRunPreconditions() error = %v", err)
	}

	if got := server.currentLoad(); got != 0.64 {
		t.Fatalf("currentLoad() = %v, want default load sample 0.64", got)
	}
	if server.Options.LoadThreshold != defaultLoadThreshold {
		t.Fatalf("LoadThreshold = %v, want default %v for cloud worker token", server.Options.LoadThreshold, defaultLoadThreshold)
	}
}

func TestValidateRunPreconditionsAllowsFiniteLoadThresholdAboveOne(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		WSRL:          "wss://livekit.example",
		APIKey:        "key",
		APISecret:     "secret",
		LoadThreshold: 1.2,
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	err := server.validateRunPreconditions()
	if err != nil {
		t.Fatalf("validateRunPreconditions() error = %v, want nil for reference warning-only behavior", err)
	}
	if server.Options.LoadThreshold != 1.2 {
		t.Fatalf("LoadThreshold = %v, want explicit threshold preserved after validation", server.Options.LoadThreshold)
	}
	if server.Options.DevMode {
		t.Fatal("DevMode = true, want production validation case")
	}
}

func TestValidateRunPreconditionsAllowsDevLoadThresholdAboveOne(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		WSRL:          "wss://livekit.example",
		APIKey:        "key",
		APISecret:     "secret",
		DevMode:       true,
		LoadThreshold: 1.2,
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	if err := server.validateRunPreconditions(); err != nil {
		t.Fatalf("validateRunPreconditions() error = %v, want nil in dev mode", err)
	}
}

func TestValidateRunPreconditionsRejectsInvalidLogLevel(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		WSRL:      "wss://livekit.example",
		APIKey:    "key",
		APISecret: "secret",
		LogLevel:  "verbose",
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	err := server.validateRunPreconditions()
	if err == nil {
		t.Fatal("validateRunPreconditions() error = nil, want invalid log level error")
	}
	if got, want := err.Error(), "Invalid log level 'verbose'. Valid levels: CRITICAL, DEBUG, ERROR, INFO, TRACE, WARN"; got != want {
		t.Fatalf("validateRunPreconditions() error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "invalid log_level") {
		t.Fatalf("validateRunPreconditions() error = %q, want reference log level wording", err.Error())
	}
}

func TestRunExportsLiveKitCredentialsBeforeDial(t *testing.T) {
	stubWorkerHTTPListener(t)
	t.Setenv("LIVEKIT_URL", "wss://old.example")
	t.Setenv("LIVEKIT_API_KEY", "old-key")
	t.Setenv("LIVEKIT_API_SECRET", "old-secret")

	oldDial := workerDialContext
	oldSleep := workerRetrySleep
	t.Cleanup(func() {
		workerDialContext = oldDial
		workerRetrySleep = oldSleep
	})

	dialed := false
	var server *AgentServer
	workerDialContext = func(context.Context, *websocket.Dialer, string, http.Header) (*websocket.Conn, *http.Response, error) {
		dialed = true
		if serverHTTPPort := server.WorkerInfo().HTTPPort; serverHTTPPort == 0 {
			t.Fatal("WorkerInfo().HTTPPort = 0 before dial, want started HTTP server port")
		}
		if os.Getenv("LIVEKIT_URL") != "wss://run.example" {
			t.Fatalf("LIVEKIT_URL = %q, want run option", os.Getenv("LIVEKIT_URL"))
		}
		if os.Getenv("LIVEKIT_API_KEY") != "run-key" {
			t.Fatalf("LIVEKIT_API_KEY = %q, want run-key", os.Getenv("LIVEKIT_API_KEY"))
		}
		if os.Getenv("LIVEKIT_API_SECRET") != "run-secret" {
			t.Fatalf("LIVEKIT_API_SECRET = %q, want run-secret", os.Getenv("LIVEKIT_API_SECRET"))
		}
		return nil, nil, errors.New("stop after env check")
	}
	workerRetrySleep = func(context.Context, time.Duration) error {
		return context.Canceled
	}

	server = NewAgentServer(WorkerOptions{
		WSRL:      "wss://run.example",
		APIKey:    "run-key",
		APISecret: "run-secret",
		MaxRetry:  1,
		DevMode:   true,
		Host:      "127.0.0.1",
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	_ = server.Run(context.Background())
	if !dialed {
		t.Fatal("worker dial was not attempted")
	}
}

func TestRunStartsConfiguredPrometheusServerBeforeDial(t *testing.T) {
	stubWorkerPrometheusListener(t, 43884)
	oldDial := workerDialContext
	oldSleep := workerRetrySleep
	t.Cleanup(func() {
		workerDialContext = oldDial
		workerRetrySleep = oldSleep
	})

	var server *AgentServer
	workerDialContext = func(context.Context, *websocket.Dialer, string, http.Header) (*websocket.Conn, *http.Response, error) {
		server.mu.Lock()
		prometheusStarted := server.prometheusServer != nil
		server.mu.Unlock()
		if !prometheusStarted {
			t.Fatal("prometheusServer = nil before dial, want started Prometheus server")
		}
		return nil, nil, errors.New("stop after prometheus check")
	}
	workerRetrySleep = func(context.Context, time.Duration) error {
		return context.Canceled
	}

	server = NewAgentServer(WorkerOptions{
		WSRL:           "wss://run.example",
		APIKey:         "run-key",
		APISecret:      "run-secret",
		MaxRetry:       1,
		DevMode:        true,
		Host:           "127.0.0.1",
		PrometheusPort: 43884,
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	_ = server.Run(context.Background())
}

func TestRunUnregisteredStartsHTTPAndSkipsLiveKitCredentials(t *testing.T) {
	stubWorkerHTTPListener(t)
	server := NewAgentServer(WorkerOptions{DevMode: true, Host: "127.0.0.1"})
	if err := server.RTCSession(func(*JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}
	startedCh := make(chan struct{}, 1)
	server.OnWorkerStarted(func() {
		startedCh <- struct{}{}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.RunUnregistered(ctx)
	}()

	select {
	case <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("RunUnregistered() did not emit worker started")
	}
	info := server.WorkerInfo()
	if info.HTTPPort == 0 {
		t.Fatal("WorkerInfo().HTTPPort = 0, want unregistered HTTP server port")
	}
	if server.conn != nil {
		t.Fatal("RunUnregistered() established worker websocket, want unregistered local run")
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("RunUnregistered() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunUnregistered() did not return after context cancellation")
	}
}

func TestRunRejectsAlreadyStartedServer(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		WSRL:      "wss://run.example",
		APIKey:    "run-key",
		APISecret: "run-secret",
		DevMode:   true,
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}
	server.httpServer = &http.Server{}

	err := server.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want already running error")
	}
	if got, want := err.Error(), "worker is already running"; got != want {
		t.Fatalf("Run() error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "server is already running") {
		t.Fatalf("Run() error = %q, want reference worker wording", err.Error())
	}
}

func TestRunWorkerMessageLoopReturnsPromptlyWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := make(chan struct{})
	readStarted := make(chan struct{})
	var closeOnce sync.Once
	server := NewAgentServer(WorkerOptions{})

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.runWorkerMessageLoop(
			ctx,
			func() (int, []byte, error) {
				close(readStarted)
				<-closed
				return 0, nil, errors.New("closed")
			},
			func() error {
				closeOnce.Do(func() { close(closed) })
				return nil
			},
		)
	}()

	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("worker read loop did not start reading")
	}
	cancel()

	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runWorkerMessageLoop() error = %v, want context canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("runWorkerMessageLoop() did not return promptly after context cancellation")
	}
}

func TestConnectWorkerWebSocketRetriesDialFailures(t *testing.T) {
	oldDial := workerDialContext
	oldSleep := workerRetrySleep
	t.Cleanup(func() {
		workerDialContext = oldDial
		workerRetrySleep = oldSleep
	})

	attempts := 0
	workerDialContext = func(context.Context, *websocket.Dialer, string, http.Header) (*websocket.Conn, *http.Response, error) {
		attempts++
		if attempts < 3 {
			return nil, nil, errors.New("dial failed")
		}
		return &websocket.Conn{}, nil, nil
	}

	var sleeps []time.Duration
	workerRetrySleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}

	server := NewAgentServer(WorkerOptions{MaxRetry: 3})
	_, err := server.openWorkerWebSocket(context.Background(), workerlivekit.WorkerWebSocketOpenOptions{
		WSURL:     "wss://livekit.example",
		APIKey:    "api-key",
		APISecret: "api-secret",
		TTL:       time.Hour,
		MaxRetry:  3,
	})
	if err != nil {
		t.Fatalf("openWorkerWebSocket() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("dial attempts = %d, want 3", attempts)
	}
	wantSleeps := []time.Duration{0, 2 * time.Second}
	if len(sleeps) != len(wantSleeps) {
		t.Fatalf("retry sleeps = %v, want %v", sleeps, wantSleeps)
	}
	for i := range wantSleeps {
		if sleeps[i] != wantSleeps[i] {
			t.Fatalf("retry sleeps = %v, want %v", sleeps, wantSleeps)
		}
	}
}

func TestRTCSessionRejectsSecondRegistration(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("first RTCSession() error = %v", err)
	}

	err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil)
	if err == nil {
		t.Fatal("second RTCSession() error = nil, want duplicate registration error")
	}
	if got, want := err.Error(), duplicateRTCSessionMessage; got != want {
		t.Fatalf("second RTCSession() error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "supports registering one rtc_session") {
		t.Fatalf("second RTCSession() error = %q, want reference duplicate-registration wording", err.Error())
	}
}

func TestRTCSessionLoadsAgentNameFromEnvironmentAtRegistration(t *testing.T) {
	t.Setenv("LIVEKIT_AGENT_NAME", "")
	server := NewAgentServer(WorkerOptions{})
	t.Setenv("LIVEKIT_AGENT_NAME", "late-env-agent")

	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	if server.Options.AgentName != "late-env-agent" {
		t.Fatalf("AgentName = %q, want late env agent", server.Options.AgentName)
	}
	if !server.Options.AgentNameIsEnv {
		t.Fatal("AgentNameIsEnv = false, want true when env is loaded at registration")
	}
	register := server.registerWorkerRequest().GetRegister()
	if register.GetAgentName() != "late-env-agent" {
		t.Fatalf("register.AgentName = %q, want late env agent", register.GetAgentName())
	}
}

func TestRTCSessionDoesNotLoadLiveKitAgentNameForAgora(t *testing.T) {
	t.Setenv("LIVEKIT_AGENT_NAME", "livekit-agent")
	server := NewAgentServer(WorkerOptions{
		Transport: WorkerTransportAgora,
		Agora: AgoraOptions{
			AppID:   "agora-app",
			Channel: "support",
		},
	})

	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	if server.Options.AgentName != "" {
		t.Fatalf("AgentName = %q, want empty for Agora", server.Options.AgentName)
	}
	if server.Options.AgentNameIsEnv {
		t.Fatal("AgentNameIsEnv = true, want false for Agora")
	}
}

func TestRTCSessionAgentNameOverrideTakesPrecedence(t *testing.T) {
	t.Setenv("LIVEKIT_AGENT_NAME", "env-agent")
	t.Setenv("LIVEKIT_AGENT_NAME_OVERRIDE", "override-agent")
	server := NewAgentServer(WorkerOptions{AgentName: "explicit-agent"})

	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	if server.Options.AgentName != "override-agent" {
		t.Fatalf("AgentName = %q, want override-agent", server.Options.AgentName)
	}
	if !server.Options.AgentNameIsEnv {
		t.Fatal("AgentNameIsEnv = false, want true for environment override")
	}
	register := server.registerWorkerRequest().GetRegister()
	if register.GetAgentName() != "override-agent" {
		t.Fatalf("register.AgentName = %q, want override-agent", register.GetAgentName())
	}
}

func TestExecuteRunningJobSetsCurrentJobContext(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.workerID = "worker-current"
	entrypointCtx := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		got, ok := GetJobContext()
		if !ok {
			return errors.New("current job context missing")
		}
		if got != ctx {
			return errors.New("current job context does not match entrypoint context")
		}
		entrypointCtx <- got
		ctx.Shutdown("session ended")
		return nil
	}

	err := server.ExecuteRunningJob(context.Background(), ipc.RunningJobInfo{
		Job:      &livekit.Job{Id: "job_current", Room: &livekit.Room{Name: "room-current"}},
		WorkerID: "worker-current",
	})
	if err != nil {
		t.Fatalf("ExecuteRunningJob() error = %v", err)
	}

	select {
	case got := <-entrypointCtx:
		if got.JobID() != "job_current" {
			t.Fatalf("current context JobID = %q, want job_current", got.JobID())
		}
	default:
		t.Fatal("entrypoint did not observe current job context")
	}
	if got, ok := GetJobContext(); ok || got != nil {
		t.Fatalf("GetJobContext() after ExecuteRunningJob = %#v, %v; want nil, false", got, ok)
	}
}

func TestNewJobContextDefaultsParticipantIdentity(t *testing.T) {
	job := &livekit.Job{Id: "job_default"}
	ctx := NewJobContext(job, "wss://livekit.example", "key", "secret")

	if got := ctx.ParticipantIdentity(); got != "agent-job_default" {
		t.Fatalf("ParticipantIdentity() = %q, want default job identity", got)
	}
}

func TestLocalJobContextUsesProvidedParticipantIdentity(t *testing.T) {
	ctx := newLocalJobContext("room-a", "agent-custom", WorkerOptions{})

	if got := ctx.ParticipantIdentity(); got != "agent-custom" {
		t.Fatalf("ParticipantIdentity() = %q, want provided local identity", got)
	}
	if ctx.Job.Room.Name != "room-a" {
		t.Fatalf("Job.Room.Name = %q, want room-a", ctx.Job.Room.Name)
	}
}

func TestLocalJobContextDefaultsReferenceFakeAgentIdentity(t *testing.T) {
	ctx := newLocalJobContext("room-a", "", WorkerOptions{})

	if !strings.HasPrefix(ctx.ParticipantIdentity(), "fake-agent-") {
		t.Fatalf("ParticipantIdentity() = %q, want fake-agent- prefix", ctx.ParticipantIdentity())
	}
}

func TestLocalJobContextUsesReferenceMockJobIDPrefix(t *testing.T) {
	ctx := newLocalJobContext("room-a", "agent-local", WorkerOptions{})

	if !strings.HasPrefix(ctx.Job.Id, "mock-job-") {
		t.Fatalf("local job ID = %q, want mock-job- prefix", ctx.Job.Id)
	}
}

func TestLocalJobContextUsesReferenceFakeRoomSIDPrefix(t *testing.T) {
	ctx := newLocalJobContext("room-a", "agent-local", WorkerOptions{})

	if !strings.HasPrefix(ctx.Job.Room.Sid, "SRM_") {
		t.Fatalf("local room SID = %q, want SRM_ prefix", ctx.Job.Room.Sid)
	}
}

func TestLocalJobContextCreatesReferenceAgentJoinToken(t *testing.T) {
	ctx := newLocalJobContext("room-a", "agent-local", WorkerOptions{
		APIKey:    "api-key",
		APISecret: "api-secret",
	})

	if ctx.token == "" {
		t.Fatal("local job token is empty, want generated agent join token")
	}
	verifier, err := auth.ParseAPIToken(ctx.token)
	if err != nil {
		t.Fatalf("ParseAPIToken() error = %v", err)
	}
	if got := verifier.Identity(); got != "agent-local" {
		t.Fatalf("token identity = %q, want agent-local", got)
	}
	if got := verifier.APIKey(); got != "api-key" {
		t.Fatalf("token api key = %q, want api-key", got)
	}
	_, grants, err := verifier.Verify("api-secret")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got := grants.GetParticipantKind(); got != livekit.ParticipantInfo_AGENT {
		t.Fatalf("token participant kind = %v, want AGENT", got)
	}
	if grants.Video == nil {
		t.Fatal("token video grant = nil, want room join agent grant")
	}
	if !grants.Video.RoomJoin {
		t.Fatal("token video grant RoomJoin = false, want true")
	}
	if !grants.Video.Agent {
		t.Fatal("token video grant Agent = false, want true")
	}
	if grants.Video.Room != "room-a" {
		t.Fatalf("token video grant Room = %q, want room-a", grants.Video.Room)
	}
}

func TestLocalJobContextGeneratesUniqueReferenceIDs(t *testing.T) {
	jobIDs := map[string]struct{}{}
	roomSIDs := map[string]struct{}{}
	participantIdentities := map[string]struct{}{}

	for range 3 {
		ctx := newLocalJobContext("room-a", "", WorkerOptions{})

		if _, exists := jobIDs[ctx.Job.Id]; exists {
			t.Fatalf("duplicate local job ID generated: %q", ctx.Job.Id)
		}
		jobIDs[ctx.Job.Id] = struct{}{}

		if _, exists := roomSIDs[ctx.Job.Room.Sid]; exists {
			t.Fatalf("duplicate local room SID generated: %q", ctx.Job.Room.Sid)
		}
		roomSIDs[ctx.Job.Room.Sid] = struct{}{}

		if _, exists := participantIdentities[ctx.ParticipantIdentity()]; exists {
			t.Fatalf("duplicate local participant identity generated: %q", ctx.ParticipantIdentity())
		}
		participantIdentities[ctx.ParticipantIdentity()] = struct{}{}
	}
}

func TestExecuteLocalJobRecordsRegisteredWorkerID(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.workerID = "worker-local"
	startedCh := make(chan *JobContext, 1)

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			return nil
		},
		nil,
		nil,
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	}()

	select {
	case jobCtx := <-startedCh:
		if jobCtx.WorkerID() != "worker-local" {
			t.Fatalf("local job WorkerID() = %q, want worker-local", jobCtx.WorkerID())
		}
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return after context cancellation")
	}
}

func TestExecuteLocalJobLaunchesThroughThreadExecutor(t *testing.T) {
	oldFactory := newLocalJobExecutor
	launchedCh := make(chan ipc.RunningJobInfo, 1)
	releaseEntrypoint := make(chan struct{})
	releaseOnce := make(chan struct{})
	newLocalJobExecutor = func(id string, entrypoint func() error) ipc.JobExecutor {
		return &localJobExecutorStub{
			id: id,
			launch: func(info ipc.RunningJobInfo) error {
				launchedCh <- info
				go func() {
					<-releaseEntrypoint
					_ = entrypoint()
				}()
				return nil
			},
		}
	}
	t.Cleanup(func() {
		newLocalJobExecutor = oldFactory
		select {
		case <-releaseOnce:
		default:
			close(releaseOnce)
			close(releaseEntrypoint)
		}
	})

	server := NewAgentServer(WorkerOptions{WSRL: "wss://local.example"})
	server.workerID = "worker-a"
	entrypointCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		entrypointCh <- ctx
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	}()

	var launched ipc.RunningJobInfo
	select {
	case launched = <-launchedCh:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("local job was not launched through executor")
	}
	if launched.WorkerID != "worker-a" {
		cancel()
		t.Fatalf("RunningJobInfo.WorkerID = %q, want worker-a", launched.WorkerID)
	}
	if launched.URL != "wss://local.example" {
		cancel()
		t.Fatalf("RunningJobInfo.URL = %q, want local worker URL", launched.URL)
	}
	if launched.AcceptArguments.Identity != "agent-local" {
		cancel()
		t.Fatalf("RunningJobInfo.AcceptArguments.Identity = %q, want agent-local", launched.AcceptArguments.Identity)
	}

	close(releaseOnce)
	close(releaseEntrypoint)
	select {
	case <-entrypointCh:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("local executor did not run entrypoint")
	}
	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return")
	}
}

func TestExecuteLocalJobExposesProcessContextToEntrypoint(t *testing.T) {
	setupCh := make(chan *JobProcess, 1)
	server := NewAgentServer(WorkerOptions{
		WSRL:          "wss://local.example",
		HTTPProxy:     "https://proxy.example",
		HTTPProxySet:  true,
		UserArguments: "user-args",
		SetupFunc: func(proc *JobProcess) error {
			proc.Userdata()["setup"] = true
			setupCh <- proc
			return nil
		},
	})
	entrypointCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		if ctx.Proc().Userdata()["setup"] != true {
			return errors.New("setup did not run before local entrypoint")
		}
		ctx.Proc().Userdata()["seen"] = true
		entrypointCh <- ctx
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	}()

	var setupProc *JobProcess
	select {
	case setupProc = <-setupCh:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("local job setup did not run")
	}
	if setupProc.UserArguments() != "user-args" {
		cancel()
		t.Fatalf("setup UserArguments() = %#v, want user-args", setupProc.UserArguments())
	}

	var jobCtx *JobContext
	select {
	case jobCtx = <-entrypointCh:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("local job entrypoint did not run")
	}

	if jobCtx.Proc().ExecutorType() != JobExecutorTypeThread {
		cancel()
		t.Fatalf("ExecutorType() = %q, want thread", jobCtx.Proc().ExecutorType())
	}
	if jobCtx.Proc().HTTPProxy() != "https://proxy.example" {
		cancel()
		t.Fatalf("HTTPProxy() = %q, want configured proxy", jobCtx.Proc().HTTPProxy())
	}
	if jobCtx.Proc().UserArguments() != "user-args" {
		cancel()
		t.Fatalf("UserArguments() = %#v, want configured user arguments", jobCtx.Proc().UserArguments())
	}
	if jobCtx.Proc().Userdata()["seen"] != true {
		cancel()
		t.Fatal("Userdata() did not keep entrypoint process state")
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return after context cancellation")
	}
}

func TestExecuteLocalJobWithExplicitIdleProcessesLaunchesThroughProcPool(t *testing.T) {
	oldPoolFactory := newLocalProcPool
	launchedCh := make(chan ipc.RunningJobInfo, 1)
	releaseEntrypoint := make(chan struct{})
	entrypointReturned := make(chan struct{})
	releaseOnce := make(chan struct{})
	var capturedMaxProcesses int
	var capturedExecutorType ipc.ExecutorType
	var capturedTargetIdle int
	var capturedCloseTimeout time.Duration
	var poolStarted bool
	var launchSawStarted bool
	newLocalProcPool = func(maxProcesses int, executorType ipc.ExecutorType, entrypoint func() error) localJobPool {
		capturedMaxProcesses = maxProcesses
		capturedExecutorType = executorType
		return &localJobPoolStub{
			start: func() error {
				poolStarted = true
				return nil
			},
			launch: func(info ipc.RunningJobInfo) error {
				launchSawStarted = poolStarted
				launchedCh <- info
				go func() {
					<-releaseEntrypoint
					_ = entrypoint()
					close(entrypointReturned)
				}()
				return nil
			},
			getByJobID: func(jobID string) ipc.JobExecutor {
				return &localJobExecutorStub{
					id:      "pool-executor",
					info:    &ipc.RunningJobInfo{Job: &livekit.Job{Id: jobID}},
					started: true,
					close: func(ctx context.Context) error {
						select {
						case <-entrypointReturned:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					},
				}
			},
			setCloseTimeout: func(timeout time.Duration) {
				capturedCloseTimeout = timeout
			},
			setTargetIdle: func(numIdleProcesses int) {
				capturedTargetIdle = numIdleProcesses
			},
		}
	}
	t.Cleanup(func() {
		newLocalProcPool = oldPoolFactory
		select {
		case <-releaseOnce:
		default:
			close(releaseOnce)
			close(releaseEntrypoint)
		}
	})

	server := NewAgentServer(WorkerOptions{
		WSRL:                             "wss://local.example",
		NumIdleProcesses:                 2,
		NumIdleProcessesSet:              true,
		ShutdownProcessTimeoutSeconds:    3,
		ShutdownProcessTimeoutSecondsSet: true,
	})
	server.workerID = "worker-pool"
	entrypointCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		entrypointCh <- ctx
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-pool", "agent-pool")
	}()

	var launched ipc.RunningJobInfo
	select {
	case launched = <-launchedCh:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("local job was not launched through proc pool")
	}
	if capturedMaxProcesses != 2 {
		cancel()
		t.Fatalf("proc pool max processes = %d, want 2", capturedMaxProcesses)
	}
	if capturedExecutorType != ipc.ExecutorTypeThread {
		cancel()
		t.Fatalf("proc pool executor type = %q, want thread", capturedExecutorType)
	}
	if capturedCloseTimeout != 3*time.Second {
		cancel()
		t.Fatalf("proc pool close timeout = %v, want 3s", capturedCloseTimeout)
	}
	if capturedTargetIdle != 2 {
		cancel()
		t.Fatalf("proc pool target idle = %d, want 2", capturedTargetIdle)
	}
	if !launchSawStarted {
		cancel()
		t.Fatal("proc pool launch ran before Start")
	}
	if launched.WorkerID != "worker-pool" || launched.URL != "wss://local.example" {
		cancel()
		t.Fatalf("RunningJobInfo worker/url = %q/%q, want worker-pool/wss://local.example", launched.WorkerID, launched.URL)
	}

	select {
	case <-entrypointCh:
		t.Fatal("entrypoint ran before proc pool release")
	default:
	}
	close(releaseOnce)
	close(releaseEntrypoint)
	select {
	case jobCtx := <-entrypointCh:
		if jobCtx.Job.Id != launched.Job.Id {
			cancel()
			t.Fatalf("entrypoint job ID = %q, want launched job %q", jobCtx.Job.Id, launched.Job.Id)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("local job entrypoint did not run")
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return after context cancellation")
	}
}

func TestExecuteLocalJobRejectsMissingRTCSession(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	if err == nil {
		t.Fatal("ExecuteLocalJob() error = nil, want missing RTC session error")
	}
	if got, want := err.Error(), rtcSessionRequiredMessage; got != want {
		t.Fatalf("ExecuteLocalJob() error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "no RTC session entrypoint") {
		t.Fatalf("ExecuteLocalJob() error = %q, want reference capitalization", err.Error())
	}
}

func TestExecuteLocalJobWithOptionsCanRunReferenceConnectJob(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	roomInfo := &livekit.Room{
		Sid:  "RM_existing",
		Name: "room-a",
	}

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			return nil
		},
		nil,
		nil,
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJobWithOptions(ctx, "room-a", "agent-connect", LocalJobOptions{
			FakeJob:  false,
			RoomInfo: roomInfo,
		})
	}()

	var jobCtx *JobContext
	select {
	case jobCtx = <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local connect job entrypoint did not run")
	}

	if jobCtx.IsFakeJob() {
		t.Fatal("local connect job IsFakeJob() = true, want false")
	}
	if !strings.HasPrefix(jobCtx.Job.Id, "job-") {
		t.Fatalf("local connect job ID = %q, want job- prefix", jobCtx.Job.Id)
	}
	if jobCtx.Job.Room != roomInfo {
		t.Fatalf("local connect job room = %#v, want provided LiveKit room info", jobCtx.Job.Room)
	}
	if jobCtx.ParticipantIdentity() != "agent-connect" {
		t.Fatalf("ParticipantIdentity() = %q, want agent-connect", jobCtx.ParticipantIdentity())
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJobWithOptions() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJobWithOptions() did not return after context cancellation")
	}
}

func TestExecuteLocalJobWithOptionsUsesTokenIdentity(t *testing.T) {
	server := NewAgentServer(WorkerOptions{APIKey: "api-key", APISecret: "api-secret"})
	startedCh := make(chan *JobContext, 1)
	roomInfo := &livekit.Room{Sid: "RM_existing", Name: "room-a"}
	token, err := auth.NewAccessToken("api-key", "api-secret").
		SetIdentity("agent-token").
		SetVideoGrant(&auth.VideoGrant{RoomJoin: true, Room: "room-a", Agent: true}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			return nil
		},
		nil,
		nil,
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJobWithOptions(ctx, "room-a", "", LocalJobOptions{
			FakeJob:  false,
			RoomInfo: roomInfo,
			Token:    token,
		})
	}()

	var jobCtx *JobContext
	select {
	case jobCtx = <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local token job entrypoint did not run")
	}

	if jobCtx.ParticipantIdentity() != "agent-token" {
		t.Fatalf("ParticipantIdentity() = %q, want token identity", jobCtx.ParticipantIdentity())
	}
	if jobCtx.token != token {
		t.Fatal("local token job did not preserve provided token")
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJobWithOptions() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJobWithOptions() did not return after context cancellation")
	}
}

func TestExecuteLocalJobWithOptionsAppliesRecordingOptions(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			return nil
		},
		nil,
		nil,
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJobWithOptions(ctx, "room-a", "agent-local", LocalJobOptions{
			FakeJob: true,
			RecordingOptions: agent.RecordingOptions{
				Audio:      true,
				Traces:     true,
				Logs:       true,
				Transcript: true,
			},
		})
	}()

	var jobCtx *JobContext
	select {
	case jobCtx = <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}

	if jobCtx.Report.RecordingOptions != (agent.RecordingOptions{Audio: true, Traces: true, Logs: true, Transcript: true}) {
		t.Fatalf("RecordingOptions = %#v, want all enabled", jobCtx.Report.RecordingOptions)
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJobWithOptions() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJobWithOptions() did not return after context cancellation")
	}
}

func TestExecuteLocalJobWithOptionsSavesSessionReport(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	reportPath := filepath.Join(t.TempDir(), "recordings", "session_report.json")

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			return nil
		},
		nil,
		func(ctx *JobContext) error {
			ctx.Report.JobID = ctx.Job.GetId()
			ctx.Report.Room = ctx.Job.GetRoom().GetName()
			return nil
		},
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJobWithOptions(ctx, "room-a", "agent-local", LocalJobOptions{
			FakeJob:           true,
			SessionReportPath: reportPath,
		})
	}()

	select {
	case <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJobWithOptions() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJobWithOptions() did not return after context cancellation")
	}

	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", reportPath, err)
	}
	var report agent.SessionReport
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("Unmarshal report: %v", err)
	}
	if report.JobID == "" {
		t.Fatal("saved report JobID is empty")
	}
	if report.Room != "room-a" {
		t.Fatalf("saved report Room = %q, want room-a", report.Room)
	}
}

func TestExecuteLocalJobWithOptionsSavesSessionReportInSessionDirectory(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	sessionDir := t.TempDir()

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			return nil
		},
		nil,
		func(ctx *JobContext) error {
			ctx.Report.JobID = ctx.Job.GetId()
			ctx.Report.Room = ctx.Job.GetRoom().GetName()
			return nil
		},
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJobWithOptions(ctx, "room-a", "agent-local", LocalJobOptions{
			FakeJob:          true,
			SessionDirectory: sessionDir,
		})
	}()

	var jobCtx *JobContext
	select {
	case jobCtx = <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}
	if jobCtx.SessionDirectory() != sessionDir {
		cancel()
		t.Fatalf("SessionDirectory() = %q, want configured directory", jobCtx.SessionDirectory())
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJobWithOptions() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJobWithOptions() did not return after context cancellation")
	}

	reportPath := filepath.Join(sessionDir, "session_report.json")
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", reportPath, err)
	}
	var report agent.SessionReport
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("Unmarshal report: %v", err)
	}
	if report.Room != "room-a" {
		t.Fatalf("saved report Room = %q, want room-a", report.Room)
	}
}

func TestExecuteLocalJobWithOptionsRejectsInvalidToken(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := server.ExecuteLocalJobWithOptions(ctx, "room-a", "", LocalJobOptions{
		FakeJob:  false,
		RoomInfo: &livekit.Room{Sid: "RM_existing", Name: "room-a"},
		Token:    "not-a-jwt",
	})
	if err == nil {
		t.Fatal("ExecuteLocalJobWithOptions() error = nil, want invalid token error")
	}
	if !strings.Contains(err.Error(), "invalid local job token") {
		t.Fatalf("ExecuteLocalJobWithOptions() error = %q, want invalid token message", err.Error())
	}
}

func TestExecuteLocalJobWithOptionsRejectsNonFakeWithoutRoomInfo(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := server.ExecuteLocalJobWithOptions(ctx, "room-a", "agent-connect", LocalJobOptions{FakeJob: false})
	if err == nil {
		t.Fatal("ExecuteLocalJobWithOptions() error = nil, want missing room info error")
	}
	if got, want := err.Error(), "room_info is None but fake_job is False"; got != want {
		t.Fatalf("ExecuteLocalJobWithOptions() error = %q, want %q", got, want)
	}
}

func TestExecuteLocalJobWithOptionsRejectsNonFakeWithoutAgentIdentity(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := server.ExecuteLocalJobWithOptions(ctx, "room-a", "", LocalJobOptions{
		FakeJob:  false,
		RoomInfo: &livekit.Room{Sid: "RM_existing", Name: "room-a"},
	})
	if err == nil {
		t.Fatal("ExecuteLocalJobWithOptions() error = nil, want missing agent identity error")
	}
	if got, want := err.Error(), "agent_identity is None but fake_job is False"; got != want {
		t.Fatalf("ExecuteLocalJobWithOptions() error = %q, want %q", got, want)
	}
}

func TestExecuteLocalJobWithOptionsChecksReferenceIdentityBeforeRoomInfo(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := server.ExecuteLocalJobWithOptions(ctx, "room-a", "", LocalJobOptions{FakeJob: false})
	if err == nil {
		t.Fatal("ExecuteLocalJobWithOptions() error = nil, want missing agent identity error")
	}
	if got, want := err.Error(), "agent_identity is None but fake_job is False"; got != want {
		t.Fatalf("ExecuteLocalJobWithOptions() error = %q, want %q", got, want)
	}
}

func TestExecuteLocalJobCleansUpAndRunsSessionEnd(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	sessionEndCh := make(chan *JobContext, 1)

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			return nil
		},
		nil,
		func(ctx *JobContext) error {
			sessionEndCh <- ctx
			return nil
		},
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	}()

	var jobCtx *JobContext
	select {
	case jobCtx = <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}

	cancel()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return after context cancellation")
	}

	select {
	case endedCtx := <-sessionEndCh:
		if endedCtx != jobCtx {
			t.Fatal("session end callback received a different job context")
		}
	case <-time.After(time.Second):
		t.Fatal("session end callback did not run")
	}

	server.mu.Lock()
	_, exists := server.activeJobs[jobCtx.Job.Id]
	server.mu.Unlock()
	if exists {
		t.Fatal("local job remained in activeJobs after completion")
	}
}

func TestExecuteLocalJobContextCancelLetsEntrypointFinishBeforeSessionEnd(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	entrypointDone := make(chan struct{})
	sessionEndCh := make(chan struct{}, 1)

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			release := make(chan struct{})
			if err := ctx.AddShutdownCallback(func() {
				close(release)
			}); err != nil {
				return err
			}
			<-release
			close(entrypointDone)
			return nil
		},
		nil,
		func(*JobContext) error {
			select {
			case <-entrypointDone:
			default:
				return errors.New("session end ran before entrypoint finished")
			}
			sessionEndCh <- struct{}{}
			return nil
		},
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	}()

	select {
	case <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}

	cancel()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return after context cancellation")
	}

	select {
	case <-sessionEndCh:
	case <-time.After(time.Second):
		t.Fatal("session end callback did not run after entrypoint finished")
	}
}

func TestExecuteLocalJobContextCancelProceedsWhenEntrypointDoesNotExit(t *testing.T) {
	oldCloseWait := localEntrypointCloseWait
	localEntrypointCloseWait = 10 * time.Millisecond
	defer func() { localEntrypointCloseWait = oldCloseWait }()

	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	block := make(chan struct{})

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			<-block
			return nil
		},
		nil,
		nil,
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	}()

	select {
	case <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}

	cancel()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return after bounded entrypoint close wait")
	}

	close(block)
}

func TestExecuteLocalJobCleansUpWhenEntrypointPanics(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	sessionEndCh := make(chan *JobContext, 1)

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			panic("entrypoint panic")
		},
		nil,
		func(ctx *JobContext) error {
			sessionEndCh <- ctx
			return nil
		},
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	}()

	var jobCtx *JobContext
	select {
	case jobCtx = <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}

	cancel()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return after entrypoint panic")
	}

	select {
	case endedCtx := <-sessionEndCh:
		if endedCtx != jobCtx {
			t.Fatal("session end callback received a different job context")
		}
	case <-time.After(time.Second):
		t.Fatal("session end callback did not run after entrypoint panic")
	}
}

func TestExecuteLocalJobReturnsWhenEntrypointFails(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	sessionEndCh := make(chan *JobContext, 1)

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			return errors.New("entrypoint failed")
		},
		nil,
		func(ctx *JobContext) error {
			sessionEndCh <- ctx
			return nil
		},
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	}()

	var jobCtx *JobContext
	select {
	case jobCtx = <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return after entrypoint failure")
	}

	select {
	case endedCtx := <-sessionEndCh:
		if endedCtx != jobCtx {
			t.Fatal("session end callback received a different job context")
		}
	case <-time.After(time.Second):
		t.Fatal("session end callback did not run after entrypoint failure")
	}
}

func TestExecuteLocalJobReturnsWhenJobContextShutsDown(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	sessionEndCh := make(chan *JobContext, 1)

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			ctx.Shutdown("entrypoint_done")
			return nil
		},
		nil,
		func(ctx *JobContext) error {
			sessionEndCh <- ctx
			return nil
		},
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	}()

	var jobCtx *JobContext
	select {
	case jobCtx = <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return after job context shutdown")
	}

	select {
	case endedCtx := <-sessionEndCh:
		if endedCtx != jobCtx {
			t.Fatal("session end callback received a different job context")
		}
	case <-time.After(time.Second):
		t.Fatal("session end callback did not run")
	}

	server.mu.Lock()
	_, exists := server.activeJobs[jobCtx.Job.Id]
	server.mu.Unlock()
	if exists {
		t.Fatal("local job remained in activeJobs after shutdown")
	}
}

func TestFinishJobTimesOutSessionEndCallback(t *testing.T) {
	server := NewAgentServer(WorkerOptions{SessionEndTimeoutSeconds: 0.01})
	blockCh := make(chan struct{})
	jobCtx := NewJobContext(&livekit.Job{Id: "job_session_end_timeout"}, "", "", "")
	server.sessionEndFnc = func(*JobContext) error {
		<-blockCh
		return nil
	}
	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	doneCh := make(chan struct{})
	go func() {
		server.finishJob(jobCtx)
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Fatal("finishJob() blocked on session end callback beyond timeout")
	}

	server.mu.Lock()
	_, exists := server.activeJobs[jobCtx.Job.Id]
	server.mu.Unlock()
	if exists {
		t.Fatal("job remained in activeJobs after session end timeout")
	}

	close(blockCh)
}

func TestDrainWaitsForActiveJobs(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	jobCtx := NewJobContext(&livekit.Job{Id: "job_drain"}, "", "", "")

	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.Drain(context.Background())
	}()

	drainingDeadline := time.After(time.Second)
	for !server.Draining() {
		select {
		case <-drainingDeadline:
			t.Fatal("server.Draining() = false, want true")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	select {
	case err := <-doneCh:
		t.Fatalf("Drain() returned before active job finished: %v", err)
	default:
	}

	server.finishJob(jobCtx)

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Drain() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Drain() did not return after active job finished")
	}
}

func TestDrainWaitsForInFlightAvailabilityRequest(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		return nil
	}
	server.requestFnc = func(req *JobRequest) error {
		close(requestStarted)
		<-releaseRequest
		return nil
	}

	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_drain_request"},
	})

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request callback did not start")
	}

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.Drain(context.Background())
	}()

	drainingDeadline := time.After(time.Second)
	for !server.Draining() {
		select {
		case <-drainingDeadline:
			t.Fatal("server.Draining() = false, want true")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	select {
	case err := <-doneCh:
		t.Fatalf("Drain() returned before in-flight request finished: %v", err)
	default:
	}

	close(releaseRequest)
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Drain() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Drain() did not return after in-flight request finished")
	}
}

func TestDrainWithTimeoutReturnsContextDeadline(t *testing.T) {
	server := NewAgentServer(WorkerOptions{DrainTimeoutSeconds: 1800})
	jobCtx := NewJobContext(&livekit.Job{Id: "job_drain_timeout"}, "", "", "")
	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	started := time.Now()
	err := server.DrainWithTimeout(context.Background(), 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DrainWithTimeout() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("DrainWithTimeout() elapsed = %v, want per-call timeout instead of configured timeout", elapsed)
	}
}

func TestDrainWaitsForPendingAcceptedJobs(t *testing.T) {
	oldTimeout := assignmentTimeout
	assignmentTimeout = time.Second
	t.Cleanup(func() {
		assignmentTimeout = oldTimeout
	})

	server := NewAgentServer(WorkerOptions{})
	jobID := "job_drain_pending"
	server.storePendingAccept(jobID, JobAcceptArguments{})

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.Drain(context.Background())
	}()

	drainingDeadline := time.After(time.Second)
	for !server.Draining() {
		select {
		case <-drainingDeadline:
			t.Fatal("server.Draining() = false, want true")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	select {
	case err := <-doneCh:
		t.Fatalf("Drain() returned before pending accepted job settled: %v", err)
	default:
	}

	server.mu.Lock()
	if timer, ok := server.pendingTimers[jobID]; ok {
		timer.Stop()
		delete(server.pendingTimers, jobID)
	}
	delete(server.pendingAccepts, jobID)
	server.mu.Unlock()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Drain() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Drain() did not return after pending accepted job settled")
	}
}

func TestHandleTerminationRunsJobShutdownCallbacks(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	jobCtx := NewJobContext(&livekit.Job{Id: "job_shutdown"}, "", "", "")
	shutdownCh := make(chan string, 1)
	if err := jobCtx.AddShutdownCallback(func(reason string) {
		shutdownCh <- reason
	}); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	server.handleTermination(&livekit.JobTermination{JobId: jobCtx.Job.Id})

	select {
	case reason := <-shutdownCh:
		if reason != "" {
			t.Fatalf("shutdown reason = %q, want empty reason", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown callback did not run")
	}
}

func TestHandleTerminationFinalizesAssignedJobOnce(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	sessionEndCh := make(chan struct{}, 2)
	server.sessionEndFnc = func(*JobContext) error {
		sessionEndCh <- struct{}{}
		return nil
	}
	entrypointStarted := make(chan struct{})
	releaseEntrypoint := make(chan struct{})
	server.entrypointFnc = func(ctx *JobContext) error {
		if err := ctx.AddShutdownCallback(func() {
			close(releaseEntrypoint)
		}); err != nil {
			return err
		}
		close(entrypointStarted)
		<-releaseEntrypoint
		return nil
	}

	job := &livekit.Job{Id: "job_termination_once", Room: &livekit.Room{Name: "room-a"}}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_termination_once", livekit.JobStatus_JS_RUNNING)
	select {
	case <-entrypointStarted:
	case <-time.After(time.Second):
		t.Fatal("entrypoint did not start")
	}
	server.handleTermination(&livekit.JobTermination{JobId: job.Id})

	select {
	case <-sessionEndCh:
	case <-time.After(time.Second):
		t.Fatal("session end callback did not run on termination")
	}

	select {
	case msg := <-sentCh:
		t.Fatalf("received job status after termination finalized job: %#v", msg.GetUpdateJob())
	case <-time.After(20 * time.Millisecond):
	}

	select {
	case <-sessionEndCh:
		t.Fatal("session end callback ran more than once")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestHandleTerminationLetsEntrypointFinishBeforeSessionEnd(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	entrypointStarted := make(chan struct{})
	releaseEntrypoint := make(chan struct{})
	entrypointDone := make(chan struct{})
	server.entrypointFnc = func(*JobContext) error {
		close(entrypointStarted)
		<-releaseEntrypoint
		close(entrypointDone)
		return nil
	}
	sessionEndCh := make(chan struct{}, 1)
	server.sessionEndFnc = func(*JobContext) error {
		select {
		case <-entrypointDone:
		default:
			return errors.New("session end ran before entrypoint finished")
		}
		sessionEndCh <- struct{}{}
		return nil
	}

	job := &livekit.Job{Id: "job_termination_wait_entrypoint", Room: &livekit.Room{Name: "room-a"}}
	markJobAccepted(t, server, job)
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_termination_wait_entrypoint", livekit.JobStatus_JS_RUNNING)
	select {
	case <-entrypointStarted:
	case <-time.After(time.Second):
		t.Fatal("entrypoint did not start")
	}

	terminationDone := make(chan struct{})
	go func() {
		server.handleTermination(&livekit.JobTermination{JobId: job.Id})
		close(terminationDone)
	}()

	select {
	case <-sessionEndCh:
		t.Fatal("session end ran before entrypoint was released")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseEntrypoint)

	select {
	case <-terminationDone:
	case <-time.After(time.Second):
		t.Fatal("termination did not finish after entrypoint returned")
	}
	select {
	case <-sessionEndCh:
	case <-time.After(time.Second):
		t.Fatal("session end did not run after entrypoint finished")
	}
	select {
	case msg := <-sentCh:
		t.Fatalf("received job status after termination finalized job: %#v", msg.GetUpdateJob())
	case <-time.After(20 * time.Millisecond):
	}
}

func receiveWorkerMessage(t *testing.T, receivedCh <-chan *livekit.WorkerMessage) *livekit.WorkerMessage {
	t.Helper()

	select {
	case msg := <-receivedCh:
		return msg
	case <-time.After(time.Second):
		t.Fatal("worker message was not sent")
		return nil
	}
}

func assertJobStatusMessage(t *testing.T, msg *livekit.WorkerMessage, jobID string, status livekit.JobStatus) {
	t.Helper()

	update := msg.GetUpdateJob()
	if update == nil {
		t.Fatal("update job message is nil")
	}
	if update.JobId != jobID {
		t.Fatalf("UpdateJob.JobId = %q, want %s", update.JobId, jobID)
	}
	if update.Status != status {
		t.Fatalf("UpdateJob.Status = %v, want %v", update.Status, status)
	}
}

func markJobAccepted(t *testing.T, server *AgentServer, job *livekit.Job) {
	t.Helper()

	server.storePendingAccept(job.Id, JobAcceptArguments{})
}

type localJobExecutorStub struct {
	id      string
	info    *ipc.RunningJobInfo
	launch  func(ipc.RunningJobInfo) error
	close   func(context.Context) error
	started bool
	status  ipc.JobStatus
}

func (e *localJobExecutorStub) ID() string { return e.id }

func (e *localJobExecutorStub) Status() ipc.JobStatus {
	if e.status == "" {
		return ipc.JobStatusRunning
	}
	return e.status
}

func (e *localJobExecutorStub) Started() bool { return e.started }

func (e *localJobExecutorStub) Job() *livekit.Job {
	if e.info == nil {
		return nil
	}
	return e.info.Job
}

func (e *localJobExecutorStub) RunningJob() *ipc.RunningJobInfo { return e.info }

func (e *localJobExecutorStub) LaunchJob(ctx context.Context, job *livekit.Job) error {
	return e.LaunchRunningJob(ctx, ipc.RunningJobInfo{Job: job})
}

func (e *localJobExecutorStub) LaunchRunningJob(_ context.Context, info ipc.RunningJobInfo) error {
	e.started = true
	e.info = &info
	if e.launch != nil {
		return e.launch(info)
	}
	return nil
}

type localJobPoolStub struct {
	start           func() error
	launch          func(ipc.RunningJobInfo) error
	getByJobID      func(string) ipc.JobExecutor
	setTargetIdle   func(int)
	setCloseTimeout func(time.Duration)
	close           func() error
}

func (p *localJobPoolStub) Start(_ context.Context) error {
	if p.start != nil {
		return p.start()
	}
	return nil
}

func (p *localJobPoolStub) LaunchRunningJob(_ context.Context, info ipc.RunningJobInfo) error {
	if p.launch != nil {
		return p.launch(info)
	}
	return nil
}

func (p *localJobPoolStub) GetByJobID(jobID string) ipc.JobExecutor {
	if p.getByJobID != nil {
		return p.getByJobID(jobID)
	}
	return nil
}

func (p *localJobPoolStub) SetTargetIdleProcesses(numIdleProcesses int) {
	if p.setTargetIdle != nil {
		p.setTargetIdle(numIdleProcesses)
	}
}

func (p *localJobPoolStub) SetCloseTimeout(timeout time.Duration) {
	if p.setCloseTimeout != nil {
		p.setCloseTimeout(timeout)
	}
}

func (p *localJobPoolStub) Close() error {
	if p.close != nil {
		return p.close()
	}
	return nil
}

func (e *localJobExecutorStub) Close(ctx context.Context) error {
	if e.close != nil {
		return e.close(ctx)
	}
	return nil
}
