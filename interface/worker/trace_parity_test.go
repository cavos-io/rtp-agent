package worker

import (
	"context"
	"encoding/json"
	"testing"

	workeripc "github.com/cavos-io/rtp-agent/interface/worker/ipc"
	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/livekit/protocol/livekit"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestJobLifecycleTraceParity(t *testing.T) {
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	j := NewJobContext(&livekit.Job{Id: "job-trace", Room: &livekit.Room{Sid: "room"}}, "", "", "")
	j.fakeJob = true
	j.roomConnected.Store(true)
	t.Cleanup(func() { _ = j.onCleanUp() })
	if err := j.SetTracerProvider(p); err != nil {
		t.Fatal(err)
	}
	s := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	s.Assistant = workerTestSessionAssistant{}
	if err := j.StartSession(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := j.AddShutdownCallback(func() { called = true }); err != nil {
		t.Fatal(err)
	}
	j.Room = lksdk.NewRoom(nil)
	j.Shutdown("finished")
	if !called {
		t.Fatal("callback missing")
	}
	server := NewAgentServer(WorkerOptions{})
	server.sessionEndFnc = func(*JobContext) error { return nil }
	done := make(chan struct{})
	server.finishJobLifecycle(j, j.JobID(), done)
	<-done
	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range r.Ended() {
		spans[span.Name()] = span
	}
	for _, name := range []string{"job_entrypoint", "agent_session", "session_start", "session_close", "job_shutdown", "shutdown_callback", "on_session_end", "session_end_upload", "room_disconnect"} {
		if spans[name] == nil {
			t.Fatalf("missing %s", name)
		}
	}
	if spans["agent_session"].Parent().SpanID() != spans["job_entrypoint"].SpanContext().SpanID() {
		t.Fatal("session not nested under job")
	}
	if spans["shutdown_callback"].Parent().SpanID() != spans["job_shutdown"].SpanContext().SpanID() {
		t.Fatal("callback not nested under shutdown")
	}
	for _, name := range []string{"on_session_end", "session_end_upload", "room_disconnect"} {
		if spans[name].Parent().SpanID() != spans["job_shutdown"].SpanContext().SpanID() || spans[name].EndTime().After(spans["job_shutdown"].EndTime()) {
			t.Fatalf("%s escaped shutdown", name)
		}
	}
	if spans["job_shutdown"].EndTime().After(spans["job_entrypoint"].EndTime()) {
		t.Fatal("shutdown outlived job")
	}
}

func TestJobDispatchTimelineSurvivesIPCRoundTrip(t *testing.T) {
	raw := []byte(`{"received_at":1,"accepted_at":2,"assigned_at":3,"launched_at":4,"job":null}`)
	var info workeripc.RunningJobInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	converted, err := livekitRunningJobInfoFromIPC(info)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(livekitRunningJobInfoToIPC(converted))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(encoded, &values); err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"received_at", "accepted_at", "assigned_at", "launched_at"} {
		if values[key] != float64(i+1) {
			t.Fatalf("%s lost: %s", key, encoded)
		}
	}
}

func TestShutdownTracingDoesNotEnableUnusedCloudReporting(t *testing.T) {
	j := NewJobContext(&livekit.Job{Id: "unused-cloud", Room: &livekit.Room{}}, "wss://tenant.livekit.cloud", "key", "secret")
	j.InitRecording(agent.RecordingOptions{Traces: true})
	t.Cleanup(func() { _ = j.onCleanUp() })
	j.beginShutdownTrace("entrypoint returned without session")
	if j.Observability() != nil {
		t.Fatal("shutdown enabled Cloud reporting without a session or connection")
	}
}
