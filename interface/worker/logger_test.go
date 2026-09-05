package worker

import (
	"testing"

	logutil "github.com/cavos-io/rtp-agent/library/logger"
	"github.com/livekit/protocol/livekit"
	livekitlogger "github.com/livekit/protocol/logger"
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
func (l *roomIORecordingLogger) WithValues(keysAndValues ...any) livekitlogger.Logger {
	l.withValues = append(l.withValues, append([]any(nil), keysAndValues...))
	return l
}
func (l *roomIORecordingLogger) WithUnlikelyValues(keysAndValues ...any) livekitlogger.UnlikelyLogger {
	return livekitlogger.GetDiscardLogger().WithUnlikelyValues(keysAndValues...)
}
func (l *roomIORecordingLogger) WithName(name string) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithComponent(component string) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithCallDepth(depth int) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithItemSampler() livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithoutSampler() livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithDeferredValues() (livekitlogger.Logger, livekitlogger.DeferredFieldResolver) {
	return livekitlogger.GetDiscardLogger().WithDeferredValues()
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
