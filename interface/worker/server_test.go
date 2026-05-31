package worker

import (
	"context"
	"errors"
	"math"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/interface/worker/ipc"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/gorilla/websocket"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
)

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
	if server.Options.HTTPProxy != "https://explicit-proxy.example" {
		t.Fatalf("HTTPProxy = %q, want explicit value", server.Options.HTTPProxy)
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
	err := server.UpdateOptions(WorkerOptions{
		WSURL:            "wss://new.example",
		APIKey:           "new-key",
		MaxRetry:         9,
		LoadThreshold:    0.8,
		NumIdleProcesses: 2,
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
	if server.Options.Permissions != permissions {
		t.Fatal("Permissions was not replaced with updated pointer")
	}
}

func TestUpdateOptionsRejectsAfterWorkerStarted(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.conn = &websocket.Conn{}

	err := server.UpdateOptions(WorkerOptions{APIKey: "new-key"})
	if err == nil {
		t.Fatal("UpdateOptions() error = nil, want started worker error")
	}
	if !strings.Contains(err.Error(), "cannot update options after starting the server") {
		t.Fatalf("UpdateOptions() error = %q, want started worker message", err.Error())
	}
}

func TestAgentWebSocketURLPreservesBasePath(t *testing.T) {
	got, err := agentWebSocketURL("https://livekit.example/project-a", "")
	if err != nil {
		t.Fatalf("agentWebSocketURL() error = %v", err)
	}

	want := "wss://livekit.example/project-a/agent"
	if got != want {
		t.Fatalf("agentWebSocketURL() = %q, want %q", got, want)
	}
}

func TestAgentWebSocketURLAddsWorkerToken(t *testing.T) {
	got, err := agentWebSocketURL("wss://livekit.example/project-a/", "cloud token")
	if err != nil {
		t.Fatalf("agentWebSocketURL() error = %v", err)
	}

	want := "wss://livekit.example/project-a/agent?worker_token=cloud+token"
	if got != want {
		t.Fatalf("agentWebSocketURL() = %q, want %q", got, want)
	}
}

func TestWorkerTypeMapsToLiveKitJobType(t *testing.T) {
	tests := []struct {
		name       string
		workerType WorkerType
		want       livekit.JobType
	}{
		{
			name:       "default",
			workerType: "",
			want:       livekit.JobType_JT_ROOM,
		},
		{
			name:       "room",
			workerType: WorkerTypeRoom,
			want:       livekit.JobType_JT_ROOM,
		},
		{
			name:       "publisher",
			workerType: WorkerTypePublisher,
			want:       livekit.JobType_JT_PUBLISHER,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workerTypeToJobType(tt.workerType); got != tt.want {
				t.Fatalf("workerTypeToJobType(%q) = %v, want %v", tt.workerType, got, tt.want)
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

func TestAgentServerIDReturnsRegisteredWorkerID(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	if server.ID() != "" {
		t.Fatalf("ID() before registration = %q, want empty", server.ID())
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

	refreshed, err := refreshRunningJobTokenForReload(info, "api-secret", now)
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

	refreshed, err := refreshRunningJobsForReload(jobs, "api-secret", now)
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
	if launched.WorkerID != "worker-old" {
		t.Fatalf("reloaded job WorkerID = %q, want original worker id", launched.WorkerID)
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

func TestWorkerStatusMessageIncludesCurrentLoad(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadFunc: func(*AgentServer) float64 {
			return 0.42
		},
	})

	msg := server.workerStatusMessage(livekit.WorkerStatus_WS_AVAILABLE)
	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("update worker message is nil")
	}
	if update.Load != 0.42 {
		t.Fatalf("UpdateWorker.Load = %v, want 0.42", update.Load)
	}
}

func TestWorkerStatusMessageMarksOverloadedWorkerFull(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadThreshold: 0.5,
		LoadFunc: func(*AgentServer) float64 {
			return 0.8
		},
	})

	msg := server.workerStatusMessage(livekit.WorkerStatus_WS_AVAILABLE)
	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("update worker message is nil")
	}
	if update.GetStatus() != livekit.WorkerStatus_WS_FULL {
		t.Fatalf("UpdateWorker.Status = %v, want WS_FULL", update.GetStatus())
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

	if got := agentIdentityForJobID(jobID); got != want {
		t.Fatalf("agentIdentityForJobID(%q) = %q, want %q", jobID, got, want)
	}
}

func TestAgentIdentityForJobIDHandlesShortJobID(t *testing.T) {
	jobID := "abc"
	want := "agent-abc"

	if got := agentIdentityForJobID(jobID); got != want {
		t.Fatalf("agentIdentityForJobID(%q) = %q, want %q", jobID, got, want)
	}
}

func TestAvailabilityResponseAcceptsWithDefaultIdentity(t *testing.T) {
	req := &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_abc123"},
	}

	resp := availabilityResponseForAccept(req, JobAcceptArguments{}, "")
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
}

func TestAvailabilityResponseAcceptUsesCustomArguments(t *testing.T) {
	req := &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_custom"},
	}

	resp := availabilityResponseForAccept(req, JobAcceptArguments{
		Name:     "Agent Name",
		Identity: "custom-agent",
		Metadata: "custom-metadata",
		Attributes: map[string]string{
			"tier": "gold",
		},
	}, "sales-agent")

	availability := resp.GetAvailability()
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

	resp := availabilityResponseForReject(req, JobRejectArguments{Terminate: true})
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

	resp := availabilityResponseForReject(req, JobRejectArguments{Terminate: false})
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
	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
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
		t.Fatalf("MigrateJob.JobIds = %v, want [job_active]", migrate.JobIds)
	}
}

func TestInitialRegisterMessageRejectsNonRegisterMessage(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	err := server.handleInitialRegisterMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Availability{
			Availability: &livekit.AvailabilityRequest{Job: &livekit.Job{Id: "job_early"}},
		},
	})
	if err == nil {
		t.Fatal("handleInitialRegisterMessage() error = nil, want expected register response error")
	}
	if !strings.Contains(err.Error(), "expected register response as first message") {
		t.Fatalf("handleInitialRegisterMessage() error = %q, want expected register response message", err.Error())
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
		if jobCtx.WorkerID != "worker-a" {
			t.Fatalf("jobCtx.WorkerID = %q, want worker-a", jobCtx.WorkerID)
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

func TestAssignmentReportsSuccessWhenEntrypointCompletes(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
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
		t.Fatal("assigned job remained in activeJobs after successful entrypoint completion")
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
	req := &JobRequest{
		rejectFnc: func(args JobRejectArguments) error {
			got = args
			return nil
		},
	}

	if err := req.Reject(); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if !got.Terminate {
		t.Fatal("Reject() Terminate = false, want true")
	}
}

func TestJobRequestAcceptDefaultsIdentityBeforeCallback(t *testing.T) {
	var got JobAcceptArguments
	req := &JobRequest{
		Job: &livekit.Job{Id: "job_identity"},
		acceptFnc: func(args JobAcceptArguments) error {
			got = args
			return nil
		},
	}

	if err := req.Accept(JobAcceptArguments{}); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if got.Identity != "agent-job_identity" {
		t.Fatalf("Accept() Identity = %q, want default identity", got.Identity)
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
	if !strings.Contains(err.Error(), "No RTC session entrypoint") {
		t.Fatalf("validateRunPreconditions() error = %q, want RTC session message", err.Error())
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

	if server.Options.LoadFunc != nil {
		t.Fatal("LoadFunc was not reset for cloud worker token")
	}
	if server.Options.LoadThreshold != defaultLoadThreshold {
		t.Fatalf("LoadThreshold = %v, want default %v for cloud worker token", server.Options.LoadThreshold, defaultLoadThreshold)
	}
}

func TestValidateRunPreconditionsRejectsFiniteLoadThresholdAboveOne(t *testing.T) {
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
	if err == nil {
		t.Fatal("validateRunPreconditions() error = nil, want invalid load threshold error")
	}
	if !strings.Contains(err.Error(), "load_threshold in prod env must be less than 1") {
		t.Fatalf("validateRunPreconditions() error = %q, want load threshold message", err.Error())
	}
}

func TestRunExportsLiveKitCredentialsBeforeDial(t *testing.T) {
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
	workerDialContext = func(context.Context, *websocket.Dialer, string, http.Header) (*websocket.Conn, *http.Response, error) {
		dialed = true
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

	server := NewAgentServer(WorkerOptions{
		WSRL:      "wss://run.example",
		APIKey:    "run-key",
		APISecret: "run-secret",
		MaxRetry:  1,
	})
	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	_ = server.Run(context.Background())
	if !dialed {
		t.Fatal("worker dial was not attempted")
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
	_, _, err := server.connectWorkerWebSocket(context.Background(), &websocket.Dialer{}, "wss://livekit.example/agent", nil)
	if err != nil {
		t.Fatalf("connectWorkerWebSocket() error = %v", err)
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
	if !strings.Contains(err.Error(), "only supports registering one rtc_session") {
		t.Fatalf("second RTCSession() error = %q, want duplicate registration message", err.Error())
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
		if jobCtx.WorkerID != "worker-local" {
			t.Fatalf("local job WorkerID = %q, want worker-local", jobCtx.WorkerID)
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
