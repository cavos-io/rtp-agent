package worker

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
	"github.com/livekit/protocol/livekit"
	protoLogger "github.com/livekit/protocol/logger"
)

type roomIORecordingLogger struct {
	warnMessages  []string
	errorMessages []string
	withValues    [][]any
}

func (l *roomIORecordingLogger) Debugw(string, ...any) {}
func (l *roomIORecordingLogger) Infow(string, ...any)  {}
func (l *roomIORecordingLogger) Warnw(msg string, err error, keysAndValues ...any) {
	l.warnMessages = append(l.warnMessages, msg)
}
func (l *roomIORecordingLogger) Errorw(msg string, err error, keysAndValues ...any) {
	l.errorMessages = append(l.errorMessages, msg)
}
func (l *roomIORecordingLogger) WithValues(keysAndValues ...any) protoLogger.Logger {
	l.withValues = append(l.withValues, append([]any(nil), keysAndValues...))
	return l
}
func (l *roomIORecordingLogger) WithUnlikelyValues(keysAndValues ...any) protoLogger.UnlikelyLogger {
	return protoLogger.GetDiscardLogger().WithUnlikelyValues(keysAndValues...)
}
func (l *roomIORecordingLogger) WithName(name string) protoLogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithComponent(component string) protoLogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithCallDepth(depth int) protoLogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithItemSampler() protoLogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithoutSampler() protoLogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithDeferredValues() (protoLogger.Logger, protoLogger.DeferredFieldResolver) {
	return protoLogger.GetDiscardLogger().WithDeferredValues()
}

func TestJobContextNewRoomBindsJobLogContext(t *testing.T) {
	recorder := &roomIORecordingLogger{}
	oldLogger := logutil.Logger
	logutil.SetLogger(recorder)
	t.Cleanup(func() { logutil.SetLogger(oldLogger) })

	ctx := NewJobContext(
		&livekit.Job{Id: "job-room-logger", Room: &livekit.Room{Name: "room-logger"}},
		"",
		"",
		"",
	)
	t.Cleanup(func() { _ = ctx.onCleanUp() })
	ctx.LogContextFields()["worker_id"] = "worker-room-logger"
	ctx.LogContextFields()["custom"] = "custom-value"

	if room := ctx.NewRoom(nil); room == nil {
		t.Fatal("NewRoom() returned nil")
	}

	got := make(map[string]any)
	for _, values := range recorder.withValues {
		for i := 0; i+1 < len(values); i += 2 {
			key, ok := values[i].(string)
			if ok {
				got[key] = values[i+1]
			}
		}
	}
	for key, want := range map[string]any{
		"job_id":    "job-room-logger",
		"room":      "room-logger",
		"worker_id": "worker-room-logger",
		"custom":    "custom-value",
	} {
		if got[key] != want {
			t.Errorf("room logger %s = %#v, want %#v", key, got[key], want)
		}
	}
}

func TestJobContextStartSessionBindsJobLogContext(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	session.Assistant = workerTestSessionAssistant{}

	recorder := &roomIORecordingLogger{}
	oldLogger := logutil.Logger
	logutil.SetLogger(recorder)
	t.Cleanup(func() { logutil.SetLogger(oldLogger) })

	ctx := NewJobContext(
		&livekit.Job{Id: "job-session-logger", Room: &livekit.Room{Name: "room-session-logger"}},
		"",
		"",
		"",
	)
	t.Cleanup(func() { _ = ctx.onCleanUp() })
	ctx.fakeJob = true
	ctx.roomConnected.Store(true)
	ctx.LogContextFields()["worker_id"] = "worker-session-logger"

	if err := ctx.StartSession(context.Background(), session); err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Stop(context.Background()) })

	if session.Logger() != recorder {
		t.Fatal("StartSession() did not bind the job logger to AgentSession")
	}
	got := make(map[string]any)
	for _, values := range recorder.withValues {
		for i := 0; i+1 < len(values); i += 2 {
			key, ok := values[i].(string)
			if ok {
				got[key] = values[i+1]
			}
		}
	}
	for key, want := range map[string]any{
		"job_id":    "job-session-logger",
		"room":      "room-session-logger",
		"worker_id": "worker-session-logger",
	} {
		if got[key] != want {
			t.Errorf("session logger %s = %#v, want %#v", key, got[key], want)
		}
	}
}
