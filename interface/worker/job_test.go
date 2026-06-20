package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	adapterlivekit "github.com/cavos-io/rtp-agent/adapter/livekit"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	workeripc "github.com/cavos-io/rtp-agent/interface/worker/ipc"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/twitchtv/twirp"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestGetJobContextReturnsActiveEntrypointContext(t *testing.T) {
	jobCtx := NewJobContext(&livekit.Job{Id: "job_current"}, "", "", "")

	if got, ok := GetJobContext(); ok || got != nil {
		t.Fatalf("GetJobContext() before entrypoint = %#v, %v; want nil, false", got, ok)
	}

	if err := runWithJobContext(jobCtx, func() error {
		got, ok := GetJobContext()
		if !ok || got != jobCtx {
			t.Fatalf("GetJobContext() inside entrypoint = %#v, %v; want job context, true", got, ok)
		}
		if got, ok := GetCurrentJobContext(); !ok || got != jobCtx {
			t.Fatalf("GetCurrentJobContext() inside entrypoint = %#v, %v; want job context, true", got, ok)
		}
		return nil
	}); err != nil {
		t.Fatalf("runWithJobContext() error = %v", err)
	}

	if got, ok := GetJobContext(); ok || got != nil {
		t.Fatalf("GetJobContext() after entrypoint = %#v, %v; want nil, false", got, ok)
	}
}

func TestRequireJobContextMatchesReferenceRequiredDefault(t *testing.T) {
	const wantMessage = "no job context found, are you running this code inside a job entrypoint?"
	if got, err := RequireJobContext(); err == nil || got != nil || err.Error() != wantMessage {
		t.Fatalf("RequireJobContext() = %#v, %v; want nil and reference error", got, err)
	}

	jobCtx := NewJobContext(&livekit.Job{Id: "job_required"}, "", "", "")
	if err := runWithJobContext(jobCtx, func() error {
		got, err := RequireJobContext()
		if err != nil || got != jobCtx {
			t.Fatalf("RequireJobContext() inside entrypoint = %#v, %v; want job context, nil", got, err)
		}
		got, err = RequireCurrentJobContext()
		if err != nil || got != jobCtx {
			t.Fatalf("RequireCurrentJobContext() inside entrypoint = %#v, %v; want job context, nil", got, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("runWithJobContext() error = %v", err)
	}
}

func TestJobContextProvidesReferenceInferenceHeaders(t *testing.T) {
	jobCtx := NewJobContext(&livekit.Job{
		Id:   "job_inference",
		Room: &livekit.Room{Sid: "RM_inference", Name: "room-a"},
	}, "", "", "")

	if got := adapterlivekit.InferenceHeaders().Get("X-LiveKit-Job-ID"); got != "" {
		t.Fatalf("X-LiveKit-Job-ID outside job context = %q, want empty", got)
	}
	if got := adapterlivekit.InferenceHeaders().Get("User-Agent"); !strings.HasPrefix(got, "LiveKit Agents/") {
		t.Fatalf("User-Agent outside job context = %q, want LiveKit Agents prefix", got)
	}

	if err := runWithJobContext(jobCtx, func() error {
		headers := adapterlivekit.InferenceHeaders()
		if got := headers.Get("X-LiveKit-Job-ID"); got != "job_inference" {
			t.Fatalf("X-LiveKit-Job-ID = %q, want job_inference", got)
		}
		if got := headers.Get("X-LiveKit-Room-ID"); got != "RM_inference" {
			t.Fatalf("X-LiveKit-Room-ID = %q, want RM_inference", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("runWithJobContext() error = %v", err)
	}
}

func TestRunWithJobContextRestoresPreviousContextAfterPanic(t *testing.T) {
	outer := NewJobContext(&livekit.Job{Id: "job_outer"}, "", "", "")
	inner := NewJobContext(&livekit.Job{Id: "job_inner"}, "", "", "")

	_ = runWithJobContext(outer, func() error {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("inner runWithJobContext did not panic")
			}
			if got, ok := GetJobContext(); !ok || got != outer {
				t.Fatalf("GetJobContext() after nested panic = %#v, %v; want outer context", got, ok)
			}
		}()
		_ = runWithJobContext(inner, func() error {
			panic("boom")
		})
		return nil
	})

	if got, ok := GetJobContext(); ok || got != nil {
		t.Fatalf("GetJobContext() after outer entrypoint = %#v, %v; want nil, false", got, ok)
	}
}

func TestJobContextShutdownRunsCallbacks(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_shutdown"}, "", "", "")
	var calls []string

	if err := ctx.AddShutdownCallback(func(reason string) {
		calls = append(calls, "reason:"+reason)
	}); err != nil {
		t.Fatalf("AddShutdownCallback(reason) error = %v", err)
	}
	if err := ctx.AddShutdownCallback(func() {
		calls = append(calls, "no-reason")
	}); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	ctx.Shutdown("user_initiated")

	want := []string{"reason:user_initiated", "no-reason"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("shutdown callbacks = %#v, want %#v", calls, want)
	}
}

func TestJobContextShutdownDefaultsEmptyReason(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_shutdown_default_reason"}, "", "", "")
	gotReason := "unset"

	if err := ctx.AddShutdownCallback(func(reason string) {
		gotReason = reason
	}); err != nil {
		t.Fatalf("AddShutdownCallback(reason) error = %v", err)
	}

	ctx.Shutdown()

	if gotReason != "" {
		t.Fatalf("shutdown callback reason = %q, want empty string", gotReason)
	}
}

func TestJobContextShutdownRunsCallbacksOnce(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_shutdown_once"}, "", "", "")
	callCount := 0

	if err := ctx.AddShutdownCallback(func(string) {
		callCount++
	}); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	ctx.Shutdown("first")
	ctx.Shutdown("second")

	if callCount != 1 {
		t.Fatalf("shutdown callback call count = %d, want 1", callCount)
	}
}

func TestJobContextShutdownContinuesAfterCallbackPanic(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_shutdown_callback_panic"}, "", "", "")
	laterCalled := false

	if err := ctx.AddShutdownCallback(func(string) {
		panic("shutdown callback panic")
	}); err != nil {
		t.Fatalf("AddShutdownCallback(panic) error = %v", err)
	}
	if err := ctx.AddShutdownCallback(func() {
		laterCalled = true
	}); err != nil {
		t.Fatalf("AddShutdownCallback(later) error = %v", err)
	}

	ctx.Shutdown("job done")

	if !laterCalled {
		t.Fatal("shutdown callback after panic was not called")
	}
}

func TestNewJobContextInitializesSessionReportMetadata(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{
			Id: "job_report",
			Room: &livekit.Room{
				Sid:  "RM_report",
				Name: "room-report",
			},
		},
		"wss://livekit.example",
		"key",
		"secret",
	)

	if ctx.Report.JobID != "job_report" {
		t.Fatalf("Report.JobID = %q, want job_report", ctx.Report.JobID)
	}
	if ctx.Report.RoomID != "RM_report" {
		t.Fatalf("Report.RoomID = %q, want RM_report", ctx.Report.RoomID)
	}
	if ctx.Report.Room != "room-report" {
		t.Fatalf("Report.Room = %q, want room-report", ctx.Report.Room)
	}
}

func TestNewJobContextAttachesTaggerToSessionReport(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_tagger"},
		"wss://livekit.example",
		"key",
		"secret",
	)

	tagger := ctx.Tagger()
	if tagger == nil {
		t.Fatal("Tagger() = nil, want job tagger")
	}
	if ctx.Report == nil || ctx.Report.Tagger != tagger {
		t.Fatal("Report Tagger does not reference JobContext Tagger()")
	}

	tagger.Fail("caller hung up")
	data := ctx.Report.ToDict()
	outcome, ok := data["outcome"].(map[string]any)
	if !ok {
		t.Fatalf("outcome = %T, want map", data["outcome"])
	}
	if outcome["outcome"] != "fail" || outcome["reason"] != "caller hung up" {
		t.Fatalf("outcome = %#v, want fail reason", outcome)
	}
}

func TestJobContextWorkerIDReturnsAssignedWorkerID(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_worker_id"}, "", "", "")

	if got := ctx.WorkerID(); got != "" {
		t.Fatalf("WorkerID() before assignment = %q, want empty", got)
	}

	ctx.workerID = "worker-a"

	if got := ctx.WorkerID(); got != "worker-a" {
		t.Fatalf("WorkerID() = %q, want worker-a", got)
	}
}

func TestJobContextInitRecordingStoresOptionsOnce(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_recording"}, "", "", "")

	ctx.InitRecording(agent.RecordingOptions{Audio: true, Logs: true})
	if got, want := ctx.Report.RecordingOptions, (agent.RecordingOptions{Audio: true, Logs: true}); got != want {
		t.Fatalf("RecordingOptions = %#v, want %#v", got, want)
	}

	ctx.InitRecording(agent.RecordingOptions{Transcript: true})
	if got, want := ctx.Report.RecordingOptions, (agent.RecordingOptions{Audio: true, Logs: true}); got != want {
		t.Fatalf("RecordingOptions after second InitRecording = %#v, want first options %#v", got, want)
	}
}

func TestJobContextPrimarySessionRequiresRegisteredSession(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_no_session"}, "", "", "")

	const wantMessage = "No AgentSession was started for this job"
	if _, err := ctx.PrimarySession(); err == nil || err.Error() != wantMessage {
		t.Fatalf("PrimarySession() error = %v, want %q", err, wantMessage)
	}
}

func TestJobContextMakeSessionReportUsesPrimarySession(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{
			Id: "job_session_report",
			Room: &livekit.Room{
				Sid:  "RM_session",
				Name: "room-session",
			},
		},
		"wss://livekit.example",
		"key",
		"secret",
	)
	baseAgent := agent.NewAgent("test")
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{AllowInterruptions: false})
	session.ChatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "hello"}},
	})

	ctx.SetPrimarySession(session)
	report, err := ctx.MakeSessionReport()
	if err != nil {
		t.Fatalf("MakeSessionReport() error = %v", err)
	}

	if report.JobID != "job_session_report" {
		t.Fatalf("report JobID = %q, want job_session_report", report.JobID)
	}
	if report.RoomID != "RM_session" || report.Room != "room-session" {
		t.Fatalf("report room = %q/%q, want RM_session/room-session", report.RoomID, report.Room)
	}
	if report.ChatHistory == session.ChatCtx {
		t.Fatal("report ChatHistory aliases session ChatCtx, want copy")
	}
	if got := len(report.ChatHistory.Items); got != 1 {
		t.Fatalf("report chat history items = %d, want 1", got)
	}
	if report.Tagger != ctx.Tagger() {
		t.Fatal("report Tagger does not preserve job tagger")
	}
	if ctx.Report != report {
		t.Fatal("JobContext Report was not updated to generated session report")
	}
}

func TestJobContextMakeSessionReportRequiresSession(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_report_no_session"}, "", "", "")

	const wantMessage = "Cannot prepare report, no AgentSession was found"
	if report, err := ctx.MakeSessionReport(); err == nil || report != nil || err.Error() != wantMessage {
		t.Fatalf("MakeSessionReport() = %#v, %v; want nil and %q", report, err, wantMessage)
	}
}

func TestJobContextSessionDirectoryCanBeConfigured(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_session_dir"}, "", "", "")
	dir := t.TempDir()

	ctx.SetSessionDirectory(dir)

	if got := ctx.SessionDirectory(); got != dir {
		t.Fatalf("SessionDirectory() = %q, want configured directory", got)
	}
}

func TestJobContextLogContextFieldsAreMutableAndReplaceable(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_log_fields"}, "", "", "")

	ctx.LogContextFields()["trace_id"] = "trace-a"
	if got := ctx.LogContextFields()["trace_id"]; got != "trace-a" {
		t.Fatalf("LogContextFields()[trace_id] = %#v, want trace-a", got)
	}

	replacement := map[string]any{"request_id": "req-a"}
	ctx.SetLogContextFields(replacement)
	if got := ctx.LogContextFields()["request_id"]; got != "req-a" {
		t.Fatalf("LogContextFields()[request_id] = %#v, want req-a", got)
	}
	if _, ok := ctx.LogContextFields()["trace_id"]; ok {
		t.Fatal("SetLogContextFields did not replace previous fields")
	}
}

func TestJobContextAvatarStartInfoExposesLiveKitConnection(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_avatar"}, "wss://livekit.example", "key", "secret")
	ctx.token = "room-token"

	info := ctx.AvatarStartInfo()

	if info.LiveKitURL != "wss://livekit.example" {
		t.Fatalf("LiveKitURL = %q, want job URL", info.LiveKitURL)
	}
	if info.LiveKitToken != "room-token" {
		t.Fatalf("LiveKitToken = %q, want job token", info.LiveKitToken)
	}
	if info.RoomName != "" {
		t.Fatalf("RoomName = %q, want empty without room info", info.RoomName)
	}
	if info.AgentIdentity != "agent-job_avatar" {
		t.Fatalf("AgentIdentity = %q, want default local participant identity", info.AgentIdentity)
	}
}

func TestJobContextAvatarStartInfoExposesRoomName(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{
		Id:   "job_avatar",
		Room: &livekit.Room{Name: "support-room"},
	}, "wss://livekit.example", "key", "secret")
	ctx.token = "room-token"

	info := ctx.AvatarStartInfo()

	if info.RoomName != "support-room" {
		t.Fatalf("RoomName = %q, want job room name", info.RoomName)
	}
}

func TestJobContextAPIReturnsCachedLiveKitClients(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_api"}, "wss://livekit.example", "key", "secret")

	api := ctx.API()
	if api == nil {
		t.Fatal("API() = nil, want LiveKit API clients")
	}
	if api.RoomService == nil {
		t.Fatal("API().RoomService = nil, want room service client")
	}
	if api.SIP == nil {
		t.Fatal("API().SIP = nil, want SIP client")
	}
	if again := ctx.API(); again != api {
		t.Fatal("API() did not return cached API clients")
	}
}

func TestJobContextConnectInfoUsesAcceptedParticipantFields(t *testing.T) {
	info := workerlivekit.ConnectInfo(workerlivekit.ConnectInfoOptions{
		APIKey:              "key",
		APISecret:           "secret",
		RoomName:            "room-a",
		ParticipantName:     "Agent Name",
		ParticipantIdentity: "custom-agent",
		ParticipantMetadata: "custom-metadata",
		ParticipantAttributes: map[string]string{
			"tier": "gold",
		},
	})

	if info.APIKey != "key" {
		t.Fatalf("ConnectInfo.APIKey = %q, want key", info.APIKey)
	}
	if info.APISecret != "secret" {
		t.Fatalf("ConnectInfo.APISecret = %q, want secret", info.APISecret)
	}
	if info.RoomName != "room-a" {
		t.Fatalf("ConnectInfo.RoomName = %q, want room-a", info.RoomName)
	}
	if info.ParticipantIdentity != "custom-agent" {
		t.Fatalf("ConnectInfo.ParticipantIdentity = %q, want custom-agent", info.ParticipantIdentity)
	}
	if info.ParticipantName != "Agent Name" {
		t.Fatalf("ConnectInfo.ParticipantName = %q, want Agent Name", info.ParticipantName)
	}
	if info.ParticipantMetadata != "custom-metadata" {
		t.Fatalf("ConnectInfo.ParticipantMetadata = %q, want custom-metadata", info.ParticipantMetadata)
	}
	if info.ParticipantAttributes["tier"] != "gold" {
		t.Fatalf("ConnectInfo.ParticipantAttributes[tier] = %q, want gold", info.ParticipantAttributes["tier"])
	}
	if info.ParticipantKind != lksdk.ParticipantAgent {
		t.Fatalf("ConnectInfo.ParticipantKind = %v, want ParticipantAgent", info.ParticipantKind)
	}

	defaultInfo := workerlivekit.ConnectInfo(workerlivekit.ConnectInfoOptions{})
	if defaultInfo.ParticipantKind != lksdk.ParticipantAgent {
		t.Fatalf("default ConnectInfo.ParticipantKind = %v, want ParticipantAgent", defaultInfo.ParticipantKind)
	}
	if defaultInfo.ParticipantAttributes != nil {
		t.Fatalf("default ConnectInfo.ParticipantAttributes = %#v, want nil", defaultInfo.ParticipantAttributes)
	}
}

func TestJobContextProcExposesReferenceProcessState(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_proc"}, "", "", "")
	ctx.process = NewJobProcess(JobExecutorTypeThread, "args", "https://proxy.example")

	proc := ctx.Proc()

	if proc.ExecutorType() != workeripc.ExecutorTypeThread {
		t.Fatalf("ExecutorType() = %q, want thread", proc.ExecutorType())
	}
	if proc.PID() != os.Getpid() {
		t.Fatalf("PID() = %d, want current pid %d", proc.PID(), os.Getpid())
	}
	if proc.UserArguments() != "args" {
		t.Fatalf("UserArguments() = %#v, want args", proc.UserArguments())
	}
	if proc.HTTPProxy() != "https://proxy.example" {
		t.Fatalf("HTTPProxy() = %q, want proxy URL", proc.HTTPProxy())
	}

	proc.Userdata()["attempt"] = 1
	if ctx.Proc().Userdata()["attempt"] != 1 {
		t.Fatal("Userdata() did not preserve mutable process state")
	}
}

func TestNewJobContextInitializesDefaultProc(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_default_proc"}, "", "", "")

	if ctx.Proc() == nil {
		t.Fatal("Proc() = nil, want default job process")
	}
	if ctx.Proc().ExecutorType() != JobExecutorTypeThread {
		t.Fatalf("default ExecutorType() = %q, want thread", ctx.Proc().ExecutorType())
	}
}

func TestJobContextConnectIsNoopWhenRoomAlreadyConnected(t *testing.T) {
	room := &lksdk.Room{}
	ctx := NewJobContext(
		&livekit.Job{Id: "job_connect_once", Room: &livekit.Room{Name: "room-a"}},
		"://invalid-url",
		"key",
		"secret",
	)
	ctx.Room = room

	if err := ctx.Connect(context.Background(), nil); err != nil {
		t.Fatalf("Connect() error = %v, want nil when room is already connected", err)
	}
	if ctx.Room != room {
		t.Fatal("Connect() replaced existing room, want existing room preserved")
	}
}

func TestAutoSubscribeSDKEnabledMatchesReferenceModes(t *testing.T) {
	tests := []struct {
		mode AutoSubscribe
		want bool
	}{
		{AutoSubscribeSubscribeAll, true},
		{AutoSubscribeSubscribeNone, false},
		{AutoSubscribeAudioOnly, false},
		{AutoSubscribeVideoOnly, false},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			if got := workerlivekit.AutoSubscribeSDKEnabled(string(tt.mode)); got != tt.want {
				t.Fatalf("AutoSubscribeSDKEnabled(%q) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

func TestShouldAutoSubscribeTrackMatchesReferenceModes(t *testing.T) {
	tests := []struct {
		mode AutoSubscribe
		kind lksdk.TrackKind
		want bool
	}{
		{AutoSubscribeSubscribeAll, lksdk.TrackKindAudio, false},
		{AutoSubscribeSubscribeNone, lksdk.TrackKindAudio, false},
		{AutoSubscribeAudioOnly, lksdk.TrackKindAudio, true},
		{AutoSubscribeAudioOnly, lksdk.TrackKindVideo, false},
		{AutoSubscribeVideoOnly, lksdk.TrackKindAudio, false},
		{AutoSubscribeVideoOnly, lksdk.TrackKindVideo, true},
		{"", lksdk.TrackKindAudio, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode)+"_"+string(tt.kind), func(t *testing.T) {
			if got := workerlivekit.ShouldAutoSubscribeTrack(string(tt.mode), tt.kind); got != tt.want {
				t.Fatalf("ShouldAutoSubscribeTrack(%q, %q) = %v, want %v", tt.mode, tt.kind, got, tt.want)
			}
		})
	}
}

func TestJobContextConnectAcceptsAutoSubscribeOptions(t *testing.T) {
	room := &lksdk.Room{}
	ctx := NewJobContext(
		&livekit.Job{Id: "job_connect_options", Room: &livekit.Room{Name: "room-a"}},
		"://invalid-url",
		"key",
		"secret",
	)
	ctx.Room = room

	if err := ctx.Connect(context.Background(), nil, ConnectOptions{AutoSubscribe: AutoSubscribeAudioOnly}); err != nil {
		t.Fatalf("Connect() with AutoSubscribe option error = %v", err)
	}
}

func TestJobContextConnectPreparedRoomJoinsExistingRoom(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_prepared_room", Room: &livekit.Room{Name: "room-a"}},
		"wss://livekit.example",
		"key",
		"secret",
	)
	room := lksdk.NewRoom(nil)
	joined := false

	oldConnector := jobContextRoomConnector
	jobContextRoomConnector = workerlivekit.RoomConnector{
		Join: func(joinCtx context.Context, gotRoom *lksdk.Room, url string, info lksdk.ConnectInfo, opts ...lksdk.ConnectOption) error {
			if joinCtx == nil {
				t.Fatal("join context = nil")
			}
			if gotRoom != room {
				t.Fatal("joined room did not match prepared room")
			}
			if url != "wss://livekit.example" {
				t.Fatalf("join url = %q, want configured URL", url)
			}
			if info.RoomName != "room-a" {
				t.Fatalf("join room name = %q, want room-a", info.RoomName)
			}
			joined = true
			return nil
		},
	}
	t.Cleanup(func() {
		jobContextRoomConnector = oldConnector
	})

	if err := ctx.ConnectPreparedRoom(context.Background(), room); err != nil {
		t.Fatalf("ConnectPreparedRoom() error = %v", err)
	}
	if !joined {
		t.Fatal("prepared room was not joined")
	}
	if ctx.Room != room {
		t.Fatal("ConnectPreparedRoom did not install prepared room on context")
	}
}

func TestJobContextConnectPreparedRoomRegistersRoomIOPreConnectBeforeJoin(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_preconnect_roomio", Room: &livekit.Room{Name: "room-a"}},
		"wss://livekit.example",
		"key",
		"secret",
	)
	rio := workerlivekit.NewRoomIO(nil, &agent.AgentSession{}, workerlivekit.RoomOptions{DisableTextInput: true})
	room := ctx.NewRoom(rio.GetCallback())
	rio.AttachRoom(room)

	oldConnector := jobContextRoomConnector
	jobContextRoomConnector = workerlivekit.RoomConnector{
		Join: func(_ context.Context, gotRoom *lksdk.Room, _ string, _ lksdk.ConnectInfo, _ ...lksdk.ConnectOption) error {
			if gotRoom != room {
				t.Fatal("joined room did not match prepared room")
			}
			err := room.RegisterByteStreamHandler(workerlivekit.PreConnectAudioBufferStream, func(*lksdk.ByteStreamReader, string) {})
			if err == nil {
				t.Fatal("pre-connect byte-stream handler was not registered before join")
			}
			return nil
		},
	}
	t.Cleanup(func() {
		jobContextRoomConnector = oldConnector
		_ = rio.Close()
	})

	if err := ctx.ConnectPreparedRoom(context.Background(), room); err != nil {
		t.Fatalf("ConnectPreparedRoom() error = %v", err)
	}
}

func TestJobContextAddParticipantEntrypointRejectsDuplicates(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_entrypoint"}, "", "", "")
	entrypoint := func(*JobContext, *livekit.ParticipantInfo) {}

	if err := ctx.AddParticipantEntrypoint(entrypoint); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	err := ctx.AddParticipantEntrypoint(entrypoint)
	const wantMessage = "entrypoints cannot be added more than once"
	if err == nil || err.Error() != wantMessage {
		t.Fatalf("AddParticipantEntrypoint() duplicate error = %v, want %q", err, wantMessage)
	}
}

func TestJobContextAddParticipantEntrypointStoresKinds(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_entrypoint_kinds"}, "", "", "")
	entrypoint := func(*JobContext, *livekit.ParticipantInfo) {}

	err := ctx.AddParticipantEntrypoint(
		entrypoint,
		livekit.ParticipantInfo_AGENT,
		livekit.ParticipantInfo_SIP,
	)
	if err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	if len(ctx.participantEntrypoints) != 1 {
		t.Fatalf("participant entrypoints = %d, want 1", len(ctx.participantEntrypoints))
	}
	gotKinds := ctx.participantEntrypoints[0].kinds
	wantKinds := []livekit.ParticipantInfo_Kind{
		livekit.ParticipantInfo_AGENT,
		livekit.ParticipantInfo_SIP,
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("participant entrypoint kinds = %#v, want %#v", gotKinds, wantKinds)
	}
}

func TestJobContextRunParticipantEntrypointsFiltersKinds(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_run"}, "", "", "")
	calls := make(chan string, 2)

	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- "standard:" + p.Identity
	}, livekit.ParticipantInfo_STANDARD); err != nil {
		t.Fatalf("AddParticipantEntrypoint(standard) error = %v", err)
	}
	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- "sip:" + p.Identity
	}, livekit.ParticipantInfo_SIP); err != nil {
		t.Fatalf("AddParticipantEntrypoint(sip) error = %v", err)
	}

	ctx.scheduleParticipantEntrypoints(&livekit.ParticipantInfo{
		Identity: "caller",
		Kind:     livekit.ParticipantInfo_SIP,
	})

	select {
	case got := <-calls:
		if got != "sip:caller" {
			t.Fatalf("participant entrypoint call = %q, want sip:caller", got)
		}
	case <-time.After(time.Second):
		t.Fatal("participant entrypoint was not called")
	}
	select {
	case got := <-calls:
		t.Fatalf("unexpected participant entrypoint call %q", got)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestJobContextAddParticipantEntrypointDefaultsReferenceKinds(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_run_all"}, "", "", "")
	entrypoint := func(*JobContext, *livekit.ParticipantInfo) {}

	if err := ctx.AddParticipantEntrypoint(entrypoint); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	gotKinds := ctx.participantEntrypoints[0].kinds
	wantKinds := []livekit.ParticipantInfo_Kind{
		livekit.ParticipantInfo_CONNECTOR,
		livekit.ParticipantInfo_SIP,
		livekit.ParticipantInfo_STANDARD,
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("default participant entrypoint kinds = %#v, want %#v", gotKinds, wantKinds)
	}
}

func TestJobContextRunDefaultParticipantEntrypointsSkipsAgentParticipants(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_run_default"}, "", "", "")
	calls := make(chan string, 2)

	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- p.Identity
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}
	ctx.scheduleParticipantEntrypoints(&livekit.ParticipantInfo{
		Identity: "agent-a",
		Kind:     livekit.ParticipantInfo_AGENT,
	})
	ctx.scheduleParticipantEntrypoints(&livekit.ParticipantInfo{
		Identity: "caller",
		Kind:     livekit.ParticipantInfo_SIP,
	})

	select {
	case got := <-calls:
		if got != "caller" {
			t.Fatalf("participant entrypoint call = %q, want caller", got)
		}
	case <-time.After(time.Second):
		t.Fatal("participant entrypoint was not called")
	}
	select {
	case got := <-calls:
		t.Fatalf("unexpected participant entrypoint call %q", got)
	case <-time.After(20 * time.Millisecond):
	}
}

type fakeParticipantView struct {
	sid        string
	identity   string
	name       string
	kind       lksdk.ParticipantKind
	metadata   string
	attributes map[string]string
}

var _ workerlivekit.RemoteParticipantView = fakeParticipantView{}

func (p fakeParticipantView) SID() string                   { return p.sid }
func (p fakeParticipantView) Identity() string              { return p.identity }
func (p fakeParticipantView) Name() string                  { return p.name }
func (p fakeParticipantView) Kind() lksdk.ParticipantKind   { return p.kind }
func (p fakeParticipantView) Metadata() string              { return p.metadata }
func (p fakeParticipantView) Attributes() map[string]string { return p.attributes }

func TestParticipantInfoFromRemoteParticipantCopiesJoinFields(t *testing.T) {
	info := workerlivekit.ParticipantInfoFromRemoteParticipant(fakeParticipantView{
		sid:      "PA_sip",
		identity: "caller",
		name:     "SIP Caller",
		kind:     lksdk.ParticipantSIP,
		metadata: "metadata",
		attributes: map[string]string{
			"phone": "+15551234567",
		},
	})

	if info.Sid != "PA_sip" {
		t.Fatalf("ParticipantInfo.Sid = %q, want PA_sip", info.Sid)
	}
	if info.Identity != "caller" {
		t.Fatalf("ParticipantInfo.Identity = %q, want caller", info.Identity)
	}
	if info.Name != "SIP Caller" {
		t.Fatalf("ParticipantInfo.Name = %q, want SIP Caller", info.Name)
	}
	if info.Kind != livekit.ParticipantInfo_SIP {
		t.Fatalf("ParticipantInfo.Kind = %v, want SIP", info.Kind)
	}
	if info.Metadata != "metadata" {
		t.Fatalf("ParticipantInfo.Metadata = %q, want metadata", info.Metadata)
	}
	if info.Attributes["phone"] != "+15551234567" {
		t.Fatalf("ParticipantInfo.Attributes[phone] = %q, want +15551234567", info.Attributes["phone"])
	}
}

func TestParticipantInfoFromRemoteParticipantCopiesAttributes(t *testing.T) {
	attrs := map[string]string{"tier": "gold"}
	info := workerlivekit.ParticipantInfoFromRemoteParticipant(fakeParticipantView{attributes: attrs})
	attrs["tier"] = "platinum"

	if info.Attributes["tier"] != "gold" {
		t.Fatalf("ParticipantInfo attributes were not copied, got %q", info.Attributes["tier"])
	}
}

func TestParticipantInfoFromRemoteParticipantNil(t *testing.T) {
	if info := workerlivekit.ParticipantInfoFromRemoteParticipant(nil); info != nil {
		t.Fatalf("ParticipantInfoFromRemoteParticipant(nil) = %#v, want nil", info)
	}
}

func TestJobContextRoomCallbackWithEntrypointsPreservesExistingParticipantCallback(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_callback"}, "", "", "")
	called := false
	cb := ctx.roomCallbackWithEntrypoints(&lksdk.RoomCallback{
		OnParticipantConnected: func(*lksdk.RemoteParticipant) {
			called = true
		},
	}, AutoSubscribeSubscribeAll)

	cb.OnParticipantConnected(nil)

	if !called {
		t.Fatal("OnParticipantConnected callback was not preserved")
	}
}

func TestJobContextParticipantAvailableRunsMatchingEntrypoints(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_available"}, "", "", "")
	calls := make(chan string, 1)
	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- p.Identity
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	ctx.participantAvailable(fakeParticipantView{
		identity: "caller",
		kind:     lksdk.ParticipantSIP,
	})

	select {
	case got := <-calls:
		if got != "caller" {
			t.Fatalf("participant entrypoint call = %q, want caller", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint was not called")
	}
}

func TestJobContextParticipantEntrypointHasCurrentJobContext(t *testing.T) {
	jobCtx := NewJobContext(&livekit.Job{Id: "job_participant_context"}, "", "", "")
	observed := make(chan *JobContext, 1)
	if err := jobCtx.AddParticipantEntrypoint(func(_ *JobContext, _ *livekit.ParticipantInfo) {
		got, _ := GetJobContext()
		observed <- got
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	if err := runWithJobContext(jobCtx, func() error {
		jobCtx.participantAvailable(fakeParticipantView{
			identity: "caller",
			kind:     lksdk.ParticipantStandard,
		})
		return nil
	}); err != nil {
		t.Fatalf("runWithJobContext() error = %v", err)
	}

	select {
	case got := <-observed:
		if got != jobCtx {
			t.Fatalf("GetJobContext() inside participant entrypoint = %#v, want scheduled job context %#v", got, jobCtx)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint was not called")
	}
}

func TestJobContextAddParticipantEntrypointRunsForExistingParticipants(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_entrypoint_existing"}, "", "", "")
	ctx.participantAvailable(fakeParticipantView{
		identity: "caller",
		kind:     lksdk.ParticipantSIP,
	})

	calls := make(chan string, 1)
	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- p.Identity
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	select {
	case got := <-calls:
		if got != "caller" {
			t.Fatalf("participant entrypoint call = %q, want caller", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint was not called for existing participant")
	}
}

func TestJobContextParticipantAvailableDoesNotBlockOnEntrypoints(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_available_async"}, "", "", "")
	block := make(chan struct{})
	defer close(block)
	secondCalled := make(chan struct{}, 1)
	if err := ctx.AddParticipantEntrypoint(func(*JobContext, *livekit.ParticipantInfo) {
		<-block
	}, livekit.ParticipantInfo_SIP); err != nil {
		t.Fatalf("AddParticipantEntrypoint(blocking) error = %v", err)
	}
	if err := ctx.AddParticipantEntrypoint(func(*JobContext, *livekit.ParticipantInfo) {
		secondCalled <- struct{}{}
	}, livekit.ParticipantInfo_SIP); err != nil {
		t.Fatalf("AddParticipantEntrypoint(second) error = %v", err)
	}

	ctx.participantAvailable(fakeParticipantView{
		identity: "caller",
		kind:     lksdk.ParticipantSIP,
	})

	select {
	case <-secondCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second participant entrypoint was blocked by the first")
	}
}

func TestJobContextParticipantEntrypointPanicDoesNotCrashProcess(t *testing.T) {
	if os.Getenv("RTP_AGENT_PARTICIPANT_ENTRYPOINT_PANIC_HELPER") == "1" {
		ctx := NewJobContext(&livekit.Job{Id: "job_participant_entrypoint_panic"}, "", "", "")
		if err := ctx.AddParticipantEntrypoint(func(*JobContext, *livekit.ParticipantInfo) {
			panic("participant entrypoint panic")
		}); err != nil {
			t.Fatalf("AddParticipantEntrypoint() error = %v", err)
		}
		ctx.participantAvailable(fakeParticipantView{
			identity: "caller",
			kind:     lksdk.ParticipantStandard,
		})
		time.Sleep(50 * time.Millisecond)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestJobContextParticipantEntrypointPanicDoesNotCrashProcess$")
	cmd.Env = append(os.Environ(), "RTP_AGENT_PARTICIPANT_ENTRYPOINT_PANIC_HELPER=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("participant entrypoint panic helper exited with %v\n%s", err, output)
	}
}

func TestJobContextParticipantAvailableStartsDuplicateEntrypointWhileRunning(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_available_duplicate"}, "", "", "")
	release := make(chan struct{})
	started := make(chan string, 2)
	entrypoint := func(_ *JobContext, p *livekit.ParticipantInfo) {
		started <- p.Identity
		<-release
	}
	if err := ctx.AddParticipantEntrypoint(entrypoint); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	participant := fakeParticipantView{
		identity: "caller",
		kind:     lksdk.ParticipantStandard,
	}
	ctx.participantAvailable(participant)

	select {
	case got := <-started:
		if got != "caller" {
			t.Fatalf("participant entrypoint call = %q, want caller", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint was not called")
	}
	ctx.participantAvailable(participant)
	select {
	case got := <-started:
		if got != "caller" {
			t.Fatalf("duplicate participant entrypoint call = %q, want caller", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("duplicate participant entrypoint was not called")
	}

	close(release)
}

func TestJobContextParticipantAvailableKeepsNewestDuplicateEntrypointTracked(t *testing.T) {
	recorder := &roomIORecordingLogger{}
	oldLogger := logutil.Logger
	logutil.SetLogger(recorder)
	t.Cleanup(func() { logutil.SetLogger(oldLogger) })

	ctx := NewJobContext(&livekit.Job{Id: "job_participant_available_duplicate_tracking"}, "", "", "")
	started := make(chan struct{}, 3)
	finished := make(chan struct{}, 3)
	release := make(chan struct{}, 3)
	entrypoint := func(*JobContext, *livekit.ParticipantInfo) {
		started <- struct{}{}
		<-release
		finished <- struct{}{}
	}
	if err := ctx.AddParticipantEntrypoint(entrypoint); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	participant := fakeParticipantView{
		identity: "caller",
		kind:     lksdk.ParticipantStandard,
	}
	ctx.participantAvailable(participant)
	waitForParticipantEntrypointStart(t, started)
	ctx.participantAvailable(participant)
	waitForParticipantEntrypointStart(t, started)

	release <- struct{}{}
	waitForParticipantEntrypointFinish(t, finished)
	time.Sleep(20 * time.Millisecond)

	ctx.participantAvailable(participant)
	waitForParticipantEntrypointStart(t, started)

	got := countWarnMessage(recorder.warnMessages, "participant entrypoint already running for participant")
	if got != 2 {
		t.Fatalf("duplicate entrypoint warnings = %d (%#v), want two reference warnings", got, recorder.warnMessages)
	}

	release <- struct{}{}
	release <- struct{}{}
}

func waitForParticipantEntrypointStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint did not start")
	}
}

func waitForParticipantEntrypointFinish(t *testing.T, finished <-chan struct{}) {
	t.Helper()
	select {
	case <-finished:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint did not finish")
	}
}

func countWarnMessage(messages []string, want string) int {
	count := 0
	for _, msg := range messages {
		if msg == want {
			count++
		}
	}
	return count
}

func TestJobContextAddParticipantEntrypointReplaysAvailableParticipantOncePerIdentity(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_participant_available_replay_unique"}, "", "", "")
	participant := fakeParticipantView{
		sid:      "PA_first",
		identity: "caller",
		kind:     lksdk.ParticipantStandard,
	}
	ctx.participantAvailable(participant)
	participant.sid = "PA_second"
	ctx.participantAvailable(participant)

	calls := make(chan string, 2)
	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- p.Sid
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	select {
	case got := <-calls:
		if got != "PA_second" {
			t.Fatalf("replayed participant SID = %q, want latest PA_second", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("participant entrypoint was not called for available participant")
	}
	select {
	case got := <-calls:
		t.Fatalf("duplicate replayed participant SID = %q, want one replay per identity", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestJobContextParticipantsAvailableReplaysExistingParticipants(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_existing_participants"}, "", "", "")
	calls := make(chan string, 2)
	if err := ctx.AddParticipantEntrypoint(func(_ *JobContext, p *livekit.ParticipantInfo) {
		calls <- p.Identity
	}); err != nil {
		t.Fatalf("AddParticipantEntrypoint() error = %v", err)
	}

	ctx.participantsAvailable([]workerlivekit.RemoteParticipantView{
		fakeParticipantView{identity: "agent-a", kind: lksdk.ParticipantAgent},
		fakeParticipantView{identity: "caller-a", kind: lksdk.ParticipantSIP},
		fakeParticipantView{identity: "caller-b", kind: lksdk.ParticipantStandard},
	})

	got := map[string]bool{}
	for range 2 {
		select {
		case identity := <-calls:
			got[identity] = true
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("participant entrypoint calls = %#v, want caller-a and caller-b", got)
		}
	}
	if !got["caller-a"] || !got["caller-b"] {
		t.Fatalf("participant entrypoint calls = %#v, want caller-a and caller-b", got)
	}
}

func TestJobContextWaitForParticipantConnectsBeforeWaiting(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_wait_connect", Room: &livekit.Room{Name: "room-a"}},
		"://invalid-url",
		"key",
		"secret",
	)

	_, err := ctx.WaitForParticipant(context.Background(), "")
	if err == nil {
		t.Fatal("WaitForParticipant() error = nil, want connection error")
	}
	if strings.Contains(err.Error(), "room is nil") {
		t.Fatalf("WaitForParticipant() error = %q, want Connect error before utility wait", err)
	}
}

func TestJobContextWaitForAgentConnectsBeforeWaiting(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_wait_agent_connect", Room: &livekit.Room{Name: "room-a"}},
		"://invalid-url",
		"key",
		"secret",
	)

	_, err := ctx.WaitForAgent(context.Background(), "agent-a")
	if err == nil {
		t.Fatal("WaitForAgent() error = nil, want connection error")
	}
	if strings.Contains(err.Error(), "room is nil") {
		t.Fatalf("WaitForAgent() error = %q, want Connect error before utility wait", err)
	}
}

func TestJobContextWaitForTrackPublicationConnectsBeforeWaiting(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_wait_track_connect", Room: &livekit.Room{Name: "room-a"}},
		"://invalid-url",
		"key",
		"secret",
	)

	_, err := ctx.WaitForTrackPublication(context.Background(), "caller-a", livekit.TrackType_AUDIO)
	if err == nil {
		t.Fatal("WaitForTrackPublication() error = nil, want connection error")
	}
	if strings.Contains(err.Error(), "room is nil") {
		t.Fatalf("WaitForTrackPublication() error = %q, want Connect error before utility wait", err)
	}
}

func TestJobContextWaitForParticipantAttributeConnectsBeforeWaiting(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_wait_attribute_connect", Room: &livekit.Room{Name: "room-a"}},
		"://invalid-url",
		"key",
		"secret",
	)

	err := ctx.WaitForParticipantAttribute(context.Background(), "caller-a", "status", "ready")
	if err == nil {
		t.Fatal("WaitForParticipantAttribute() error = nil, want connection error")
	}
	if strings.Contains(err.Error(), "room is nil") {
		t.Fatalf("WaitForParticipantAttribute() error = %q, want Connect error before utility wait", err)
	}
}

func TestJobContextDefaultParticipantWaitKindsMatchReference(t *testing.T) {
	got := workerlivekit.DefaultParticipantKindsWhenUnset(nil)
	want := []livekit.ParticipantInfo_Kind{
		livekit.ParticipantInfo_CONNECTOR,
		livekit.ParticipantInfo_SIP,
		livekit.ParticipantInfo_STANDARD,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default participant wait kinds = %#v, want %#v", got, want)
	}

	configured := []livekit.ParticipantInfo_Kind{livekit.ParticipantInfo_AGENT}
	if got := workerlivekit.DefaultParticipantKindsWhenUnset(configured); !reflect.DeepEqual(got, configured) {
		t.Fatalf("configured participant wait kinds = %#v, want %#v", got, configured)
	}
}

func TestJobContextRoomInfoReturnsJobRoom(t *testing.T) {
	room := &livekit.Room{Name: "room-a", Sid: "RM_a"}
	ctx := NewJobContext(&livekit.Job{Id: "job_room", Room: room}, "", "", "")

	if got := ctx.RoomInfo(); got != room {
		t.Fatal("RoomInfo() did not return the job room")
	}

	ctx.Job = nil
	if got := ctx.RoomInfo(); got != nil {
		t.Fatalf("RoomInfo() with nil job = %#v, want nil", got)
	}
}

func TestJobContextAgentReturnsRoomLocalParticipant(t *testing.T) {
	room := lksdk.NewRoom(nil)
	ctx := NewJobContext(&livekit.Job{Id: "job_agent"}, "", "", "")
	ctx.Room = room

	if got := ctx.Agent(); got != room.LocalParticipant {
		t.Fatal("Agent() did not return room local participant")
	}

	ctx.Room = nil
	if got := ctx.Agent(); got != nil {
		t.Fatalf("Agent() with nil room = %#v, want nil", got)
	}
}

func TestJobContextJobIDReturnsCurrentJobID(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job-a"}, "", "", "")
	if got := ctx.JobID(); got != "job-a" {
		t.Fatalf("JobID() = %q, want job-a", got)
	}

	ctx.Job = nil
	if got := ctx.JobID(); got != "" {
		t.Fatalf("JobID() with nil job = %q, want empty", got)
	}
}

func TestJobContextLocalParticipantIdentity(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job-a"}, "", "", "")
	if got := ctx.LocalParticipantIdentity(); got != "agent-job-a" {
		t.Fatalf("LocalParticipantIdentity() = %q, want agent-job-a", got)
	}

	ctx.AcceptArguments.Identity = "custom-agent"
	if got := ctx.LocalParticipantIdentity(); got != "custom-agent" {
		t.Fatalf("LocalParticipantIdentity() with accept identity = %q, want custom-agent", got)
	}

	ctx.AcceptArguments.Identity = ""
	ctx.Job = nil
	if got := ctx.LocalParticipantIdentity(); got != "" {
		t.Fatalf("LocalParticipantIdentity() with nil job = %q, want empty", got)
	}
}

func TestJobContextLocalParticipantIdentityPrefersTokenIdentity(t *testing.T) {
	token, err := auth.NewAccessToken("key", "secret").
		SetIdentity("token-agent").
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	ctx := NewJobContext(&livekit.Job{Id: "job-a"}, "", "", "")
	ctx.AcceptArguments.Identity = "accepted-agent"
	ctx.token = token

	if got := ctx.LocalParticipantIdentity(); got != "token-agent" {
		t.Fatalf("LocalParticipantIdentity() = %q, want token-agent", got)
	}
}

func TestJobContextTokenClaimsReturnsUnverifiedTokenClaims(t *testing.T) {
	token, err := auth.NewAccessToken("key", "secret").
		SetIdentity("token-agent").
		SetName("Token Agent").
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     "room-a",
			Agent:    true,
		}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	ctx := NewJobContext(&livekit.Job{Id: "job-a"}, "", "", "")
	ctx.token = token

	claims, err := ctx.TokenClaims()
	if err != nil {
		t.Fatalf("TokenClaims() error = %v", err)
	}
	if claims.Identity != "token-agent" {
		t.Fatalf("TokenClaims().Identity = %q, want token-agent", claims.Identity)
	}
	if claims.Name != "Token Agent" {
		t.Fatalf("TokenClaims().Name = %q, want Token Agent", claims.Name)
	}
	if claims.Video == nil {
		t.Fatal("TokenClaims().Video = nil, want video grant")
	}
	if !claims.Video.RoomJoin {
		t.Fatal("TokenClaims().Video.RoomJoin = false, want true")
	}
	if !claims.Video.Agent {
		t.Fatal("TokenClaims().Video.Agent = false, want true")
	}
	if claims.Video.Room != "room-a" {
		t.Fatalf("TokenClaims().Video.Room = %q, want room-a", claims.Video.Room)
	}
}

func TestJobContextPublisherInfoReturnsJobParticipant(t *testing.T) {
	publisher := &livekit.ParticipantInfo{Identity: "publisher-a"}
	ctx := NewJobContext(&livekit.Job{Id: "job-a", Participant: publisher}, "", "", "")

	if got := ctx.PublisherInfo(); got != publisher {
		t.Fatal("PublisherInfo() did not return the job participant")
	}

	ctx.Job = nil
	if got := ctx.PublisherInfo(); got != nil {
		t.Fatalf("PublisherInfo() with nil job = %#v, want nil", got)
	}
}

func TestJobRequestAccessorsExposeJobFields(t *testing.T) {
	room := &livekit.Room{Name: "room-a"}
	publisher := &livekit.ParticipantInfo{Identity: "publisher-a"}
	req := workerlivekit.NewJobRequest(&livekit.Job{
		Id:          "job_request",
		Room:        room,
		Participant: publisher,
		AgentName:   "agent-a",
	}, nil, nil)

	if got := req.ID(); got != "job_request" {
		t.Fatalf("ID() = %q, want job_request", got)
	}
	if got := req.Room(); got != room {
		t.Fatal("Room() did not return the job room")
	}
	if got := req.Publisher(); got != publisher {
		t.Fatal("Publisher() did not return the job participant")
	}
	if got := req.AgentName(); got != "agent-a" {
		t.Fatalf("AgentName() = %q, want agent-a", got)
	}
}

func TestJobRequestRootAliasUsesLiveKitImplementation(t *testing.T) {
	var req *JobRequest = workerlivekit.NewJobRequest(&livekit.Job{Id: "job_alias"}, nil, nil)

	if got := req.ID(); got != "job_alias" {
		t.Fatalf("ID() = %q, want job_alias", got)
	}
}

func TestJobContextCreateSIPParticipantRequestUsesReferenceDefaultName(t *testing.T) {
	req := workerlivekit.CreateSIPParticipantRequest("room-a", "+15551234567", "trunk-a", "caller-a", "")

	if req.RoomName != "room-a" {
		t.Fatalf("CreateSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantIdentity != "caller-a" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantIdentity = %q, want caller-a", req.ParticipantIdentity)
	}
	if req.SipTrunkId != "trunk-a" {
		t.Fatalf("CreateSIPParticipantRequest.SipTrunkId = %q, want trunk-a", req.SipTrunkId)
	}
	if req.SipCallTo != "+15551234567" {
		t.Fatalf("CreateSIPParticipantRequest.SipCallTo = %q, want +15551234567", req.SipCallTo)
	}
	if req.ParticipantName != "SIP-participant" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantName = %q, want SIP-participant", req.ParticipantName)
	}
}

func TestJobContextCreateSIPParticipantRequestPreservesExplicitName(t *testing.T) {
	req := workerlivekit.CreateSIPParticipantRequest("room-a", "+15551234567", "trunk-a", "caller-a", "SIP Caller")

	if req.ParticipantName != "SIP Caller" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantName = %q, want SIP Caller", req.ParticipantName)
	}
	if workerlivekit.DefaultSIPParticipantName != "SIP-participant" {
		t.Fatalf("DefaultSIPParticipantName = %q, want SIP-participant", workerlivekit.DefaultSIPParticipantName)
	}
}

func TestJobContextCreateSIPParticipantUsesProvidedRequest(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_sip", Room: &livekit.Room{Name: "caller-room"}},
		"",
		"",
		"",
	)
	sip := &fakeJobSIPAPI{}
	ctx.api = &JobAPI{SIP: sip}
	req := &livekit.CreateSIPParticipantRequest{
		RoomName:            "caller-room-human-agent",
		ParticipantIdentity: "human-agent-sip",
		SipTrunkId:          "trunk_123",
		SipCallTo:           "+15550100",
		WaitUntilAnswered:   true,
		SipNumber:           "+15550999",
		Headers:             map[string]string{"X-Trace": "trace-a"},
		Dtmf:                "123#",
	}

	if _, err := ctx.CreateSIPParticipant(context.Background(), req); err != nil {
		t.Fatalf("CreateSIPParticipant() error = %v", err)
	}
	if sip.createRequest != req {
		t.Fatalf("CreateSIPParticipant() request = %#v, want provided request", sip.createRequest)
	}
}

func TestJobContextAddSIPParticipantBuildsReferenceRequest(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_sip_add", Room: &livekit.Room{Name: "room-a"}},
		"",
		"",
		"",
	)
	sip := &fakeJobSIPAPI{}
	ctx.api = &JobAPI{SIP: sip}

	if _, err := ctx.AddSIPParticipant(context.Background(), "+15551234567", "trunk-a", "caller-a"); err != nil {
		t.Fatalf("AddSIPParticipant() error = %v", err)
	}
	if sip.createRequest == nil {
		t.Fatal("AddSIPParticipant() did not call SIP create API")
	}
	if sip.createRequest.RoomName != "room-a" {
		t.Fatalf("AddSIPParticipant() RoomName = %q, want room-a", sip.createRequest.RoomName)
	}
	if sip.createRequest.ParticipantName != workerlivekit.DefaultSIPParticipantName {
		t.Fatalf("AddSIPParticipant() ParticipantName = %q, want %q", sip.createRequest.ParticipantName, workerlivekit.DefaultSIPParticipantName)
	}
}

func TestJobContextTransferSIPParticipantRequestMatchesReferenceFields(t *testing.T) {
	req := workerlivekit.TransferSIPParticipantRequest("room-a", "caller-a", "+15557654321", true)

	if req.RoomName != "room-a" {
		t.Fatalf("TransferSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantIdentity != "caller-a" {
		t.Fatalf("TransferSIPParticipantRequest.ParticipantIdentity = %q, want caller-a", req.ParticipantIdentity)
	}
	if req.TransferTo != "+15557654321" {
		t.Fatalf("TransferSIPParticipantRequest.TransferTo = %q, want +15557654321", req.TransferTo)
	}
	if !req.PlayDialtone {
		t.Fatal("TransferSIPParticipantRequest.PlayDialtone = false, want true")
	}
}

func TestJobContextTransferSIPParticipantDefaultsWithoutPlayDialtoneAndAllowsOverride(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_sip_transfer", Room: &livekit.Room{Name: "room-a"}},
		"",
		"",
		"",
	)
	sip := &fakeJobSIPAPI{}
	ctx.api = &JobAPI{SIP: sip}

	if err := ctx.TransferSIPParticipant(context.Background(), "caller-a", "+15557654321"); err != nil {
		t.Fatalf("TransferSIPParticipant() error = %v", err)
	}
	if sip.transferRequest == nil {
		t.Fatal("TransferSIPParticipant() did not call SIP transfer API")
	}
	if sip.transferRequest.PlayDialtone {
		t.Fatal("TransferSIPParticipant() default PlayDialtone = true, want false")
	}

	if err := ctx.TransferSIPParticipant(context.Background(), "caller-a", "+15557654321", true); err != nil {
		t.Fatalf("TransferSIPParticipant(true) error = %v", err)
	}
	if !sip.transferRequest.PlayDialtone {
		t.Fatal("TransferSIPParticipant(true) PlayDialtone = false, want true")
	}
}

type fakeJobSIPAPI struct {
	createRequest   *livekit.CreateSIPParticipantRequest
	transferRequest *livekit.TransferSIPParticipantRequest
}

func (f *fakeJobSIPAPI) CreateSIPParticipant(_ context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error) {
	f.createRequest = req
	return &livekit.SIPParticipantInfo{}, nil
}

func (f *fakeJobSIPAPI) TransferSIPParticipant(_ context.Context, req *livekit.TransferSIPParticipantRequest) (*emptypb.Empty, error) {
	f.transferRequest = req
	return &emptypb.Empty{}, nil
}

func TestJobContextDeleteRoomIgnoresAPIError(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_delete_room", Room: &livekit.Room{Name: "room-a"}},
		"",
		"",
		"",
	)
	roomAPI := &fakeJobRoomServiceAPI{err: errors.New("server disconnected")}
	ctx.api = &JobAPI{RoomService: roomAPI}

	resp, err := ctx.DeleteRoom(context.Background(), "")
	if err != nil {
		t.Fatalf("DeleteRoom() error = %v, want nil for best-effort reference behavior", err)
	}
	if resp == nil {
		t.Fatal("DeleteRoom() response = nil, want empty response")
	}
	if roomAPI.request == nil {
		t.Fatal("DeleteRoom() did not call room service API")
	}
	if roomAPI.request.Room != "room-a" {
		t.Fatalf("DeleteRoom() room = %q, want room-a", roomAPI.request.Room)
	}
}

func TestJobContextDeleteRoomIgnoresNotFoundWithoutWarning(t *testing.T) {
	recorder := &roomIORecordingLogger{}
	oldLogger := logutil.Logger
	logutil.SetLogger(recorder)
	t.Cleanup(func() { logutil.SetLogger(oldLogger) })

	ctx := NewJobContext(
		&livekit.Job{Id: "job_delete_room_not_found", Room: &livekit.Room{Name: "room-a"}},
		"",
		"",
		"",
	)
	roomAPI := &fakeJobRoomServiceAPI{err: twirp.NewError(twirp.NotFound, "requested room does not exist")}
	ctx.api = &JobAPI{RoomService: roomAPI}

	resp, err := ctx.DeleteRoom(context.Background(), "")
	if err != nil {
		t.Fatalf("DeleteRoom() error = %v, want nil for reference not_found cleanup", err)
	}
	if resp == nil {
		t.Fatal("DeleteRoom() response = nil, want empty response")
	}
	if len(recorder.warnMessages) != 0 {
		t.Fatalf("warn messages = %#v, want none for reference not_found cleanup", recorder.warnMessages)
	}
}

func TestJobContextMoveParticipantBuildsReferenceRequest(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_move_participant", Room: &livekit.Room{Name: "caller-room"}},
		"",
		"",
		"",
	)
	roomAPI := &fakeJobRoomServiceAPI{}
	ctx.api = &JobAPI{RoomService: roomAPI}

	if err := ctx.MoveParticipant(context.Background(), "human-room", "human-agent-sip", "caller-room"); err != nil {
		t.Fatalf("MoveParticipant() error = %v", err)
	}
	if roomAPI.moveRequest == nil {
		t.Fatal("MoveParticipant() did not call room service API")
	}
	if roomAPI.moveRequest.Room != "human-room" {
		t.Fatalf("MoveParticipantRequest.Room = %q, want human-room", roomAPI.moveRequest.Room)
	}
	if roomAPI.moveRequest.Identity != "human-agent-sip" {
		t.Fatalf("MoveParticipantRequest.Identity = %q, want human-agent-sip", roomAPI.moveRequest.Identity)
	}
	if roomAPI.moveRequest.DestinationRoom != "caller-room" {
		t.Fatalf("MoveParticipantRequest.DestinationRoom = %q, want caller-room", roomAPI.moveRequest.DestinationRoom)
	}
}

type fakeJobRoomServiceAPI struct {
	err         error
	request     *livekit.DeleteRoomRequest
	moveRequest *livekit.MoveParticipantRequest
}

func (f *fakeJobRoomServiceAPI) DeleteRoom(_ context.Context, req *livekit.DeleteRoomRequest) (*livekit.DeleteRoomResponse, error) {
	f.request = req
	if f.err != nil {
		return nil, f.err
	}
	return &livekit.DeleteRoomResponse{}, nil
}

func (f *fakeJobRoomServiceAPI) MoveParticipant(_ context.Context, req *livekit.MoveParticipantRequest) (*livekit.MoveParticipantResponse, error) {
	f.moveRequest = req
	if f.err != nil {
		return nil, f.err
	}
	return &livekit.MoveParticipantResponse{}, nil
}

func TestTransferSIPParticipantIdentityAcceptsString(t *testing.T) {
	identity, err := workerlivekit.TransferSIPParticipantIdentity("caller-a")
	if err != nil {
		t.Fatalf("TransferSIPParticipantIdentity(string) error = %v", err)
	}
	if identity != "caller-a" {
		t.Fatalf("TransferSIPParticipantIdentity(string) = %q, want caller-a", identity)
	}
}

func TestTransferSIPParticipantIdentityAcceptsSIPParticipant(t *testing.T) {
	identity, err := workerlivekit.TransferSIPParticipantIdentity(fakeParticipantView{
		identity: "caller-a",
		kind:     lksdk.ParticipantSIP,
	})
	if err != nil {
		t.Fatalf("TransferSIPParticipantIdentity(SIP participant) error = %v", err)
	}
	if identity != "caller-a" {
		t.Fatalf("TransferSIPParticipantIdentity(SIP participant) = %q, want caller-a", identity)
	}
}

func TestTransferSIPParticipantIdentityRejectsNonSIPParticipant(t *testing.T) {
	_, err := workerlivekit.TransferSIPParticipantIdentity(fakeParticipantView{
		identity: "agent-a",
		kind:     lksdk.ParticipantAgent,
	})
	if err == nil {
		t.Fatal("TransferSIPParticipantIdentity(agent participant) error = nil, want error")
	}
	if got, want := err.Error(), "Participant must be a SIP participant"; got != want {
		t.Fatalf("TransferSIPParticipantIdentity(agent participant) error = %q, want %q", got, want)
	}
}

func TestTransferSIPParticipantIdentityRejectsUnsupportedValue(t *testing.T) {
	_, err := workerlivekit.TransferSIPParticipantIdentity(42)
	if err == nil {
		t.Fatal("TransferSIPParticipantIdentity(int) error = nil, want error")
	}
	if got, want := err.Error(), "participant must be a SIP participant or identity string"; got != want {
		t.Fatalf("TransferSIPParticipantIdentity(int) error = %q, want %q", got, want)
	}
}

func TestLocalJobContextSkipsDestructiveLiveKitAPIs(t *testing.T) {
	ctx := newLocalJobContext("room-a", "agent-local", WorkerOptions{})
	if !ctx.IsFakeJob() {
		t.Fatal("local job context IsFakeJob() = false, want true")
	}

	if resp, err := ctx.DeleteRoom(context.Background(), ""); err != nil {
		t.Fatalf("DeleteRoom() error = %v", err)
	} else if resp == nil {
		t.Fatal("DeleteRoom() response = nil, want empty response")
	}

	if info, err := ctx.AddSIPParticipant(context.Background(), "+15551234567", "trunk", "sip-user", "SIP User"); err != nil {
		t.Fatalf("AddSIPParticipant() error = %v", err)
	} else if info == nil {
		t.Fatal("AddSIPParticipant() info = nil, want empty info")
	}

	if info, err := ctx.AddSIPParticipant(context.Background(), "+15551234567", "trunk", "sip-user"); err != nil {
		t.Fatalf("AddSIPParticipant() with default name error = %v", err)
	} else if info == nil {
		t.Fatal("AddSIPParticipant() with default name info = nil, want empty info")
	}

	if err := ctx.TransferSIPParticipant(context.Background(), "sip-user", "+15557654321", false); err != nil {
		t.Fatalf("TransferSIPParticipant() error = %v", err)
	}

	if err := ctx.TransferSIPParticipant(context.Background(), "sip-user", "+15557654321"); err != nil {
		t.Fatalf("TransferSIPParticipant() with default dialtone error = %v", err)
	}
}
