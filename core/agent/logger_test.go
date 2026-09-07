package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	protoLogger "github.com/livekit/protocol/logger"
)

type runtimeLogEntry struct {
	message string
	fields  map[string]any
}

type runtimeLogSink struct {
	mu      sync.Mutex
	entries []runtimeLogEntry
}

type runtimeRecordingLogger struct {
	sink   *runtimeLogSink
	values []any
}

func newRuntimeRecordingLogger() *runtimeRecordingLogger {
	return &runtimeRecordingLogger{sink: &runtimeLogSink{}}
}

func (l *runtimeRecordingLogger) record(message string, values ...any) {
	fields := make(map[string]any)
	all := append(append([]any(nil), l.values...), values...)
	for i := 0; i+1 < len(all); i += 2 {
		key, ok := all[i].(string)
		if ok {
			fields[key] = all[i+1]
		}
	}
	l.sink.mu.Lock()
	l.sink.entries = append(l.sink.entries, runtimeLogEntry{message: message, fields: fields})
	l.sink.mu.Unlock()
}

func (l *runtimeRecordingLogger) Debugw(message string, values ...any) {
	l.record(message, values...)
}

func (l *runtimeRecordingLogger) Infow(message string, values ...any) {
	l.record(message, values...)
}

func (l *runtimeRecordingLogger) Warnw(message string, _ error, values ...any) {
	l.record(message, values...)
}

func (l *runtimeRecordingLogger) Errorw(message string, _ error, values ...any) {
	l.record(message, values...)
}

func (l *runtimeRecordingLogger) WithValues(values ...any) protoLogger.Logger {
	return &runtimeRecordingLogger{
		sink:   l.sink,
		values: append(append([]any(nil), l.values...), values...),
	}
}

func (l *runtimeRecordingLogger) WithUnlikelyValues(values ...any) protoLogger.UnlikelyLogger {
	return protoLogger.NewUnlikelyLogger(l, values...)
}

func (l *runtimeRecordingLogger) WithName(string) protoLogger.Logger      { return l }
func (l *runtimeRecordingLogger) WithComponent(string) protoLogger.Logger { return l }
func (l *runtimeRecordingLogger) WithCallDepth(int) protoLogger.Logger    { return l }
func (l *runtimeRecordingLogger) WithItemSampler() protoLogger.Logger     { return l }
func (l *runtimeRecordingLogger) WithoutSampler() protoLogger.Logger      { return l }
func (l *runtimeRecordingLogger) WithDeferredValues() (protoLogger.Logger, protoLogger.DeferredFieldResolver) {
	return protoLogger.GetDiscardLogger().WithDeferredValues()
}

func (l *runtimeRecordingLogger) waitForMessages(t *testing.T, messages ...string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		missing := make(map[string]struct{}, len(messages))
		for _, message := range messages {
			missing[message] = struct{}{}
		}
		l.sink.mu.Lock()
		for _, entry := range l.sink.entries {
			if _, ok := missing[entry.message]; ok && entry.fields["job_id"] == "job-runtime-logger" {
				delete(missing, entry.message)
			}
		}
		l.sink.mu.Unlock()
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("messages missing job logger fields: %#v", missing)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAgentSessionLoggerScopesOwnedRuntimeLogs(t *testing.T) {
	recorder := newRuntimeRecordingLogger()
	sessionAgent := NewAgent("test")
	session := NewAgentSession(sessionAgent, nil, AgentSessionOptions{})
	if err := session.SetLogger(recorder.WithValues("job_id", "job-runtime-logger")); err != nil {
		t.Fatalf("SetLogger() error = %v", err)
	}

	session.UpdateAgentState(AgentStateThinking)

	pipeline := NewPipelineAgent(nil, nil, nil, nil, nil)
	pipelineCtx, cancelPipeline := context.WithCancel(t.Context())
	if err := pipeline.Start(pipelineCtx, session); err != nil {
		t.Fatalf("PipelineAgent.Start() error = %v", err)
	}
	cancelPipeline()

	activity := NewAgentActivity(sessionAgent, session)
	activity.OnSTTStartOfSpeech(&stt.SpeechEvent{})

	multimodal := NewMultimodalAgent(nil, nil)
	multimodal.session = session
	multimodal.handleRealtimeEvent(llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted})

	ivr := NewIVRActivity(session)
	ivr.onSilenceDetected()
	ivr.Stop()

	recorder.waitForMessages(t,
		"Agent state changed",
		"PipelineAgent started",
		"Start of speech detected",
		"User started speaking (multimodal)",
		"IVRActivity: silence detected; sending notification",
	)
}

func TestAgentSessionSetLoggerRejectsRunningSession(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.started = true

	err := session.SetLogger(newRuntimeRecordingLogger())
	if !errors.Is(err, ErrAgentSessionRunning) {
		t.Fatalf("SetLogger() error = %v, want %v", err, ErrAgentSessionRunning)
	}
}

func TestSpeechHandleUsesSessionLoggerForCallbackPanics(t *testing.T) {
	recorder := newRuntimeRecordingLogger()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	if err := session.SetLogger(recorder.WithValues("job_id", "job-runtime-logger")); err != nil {
		t.Fatalf("SetLogger() error = %v", err)
	}

	speech := newSpeechHandleWithLogger(true, DefaultInputDetails(), session.Logger())
	speech.AddDoneCallback(func(*SpeechHandle) { panic("boom") })
	speech.MarkDone()

	recorder.waitForMessages(t, "error in done_callback")
}

func TestAgentSessionLoggersDoNotLeakAcrossJobs(t *testing.T) {
	recorder := newRuntimeRecordingLogger()
	sessions := []*AgentSession{
		NewAgentSession(NewAgent("first"), nil, AgentSessionOptions{}),
		NewAgentSession(NewAgent("second"), nil, AgentSessionOptions{}),
	}
	jobIDs := []string{"job-a", "job-b"}
	for i, session := range sessions {
		if err := session.SetLogger(recorder.WithValues("job_id", jobIDs[i])); err != nil {
			t.Fatalf("SetLogger(%q) error = %v", jobIDs[i], err)
		}
	}

	var wg sync.WaitGroup
	for _, session := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session.UpdateAgentState(AgentStateThinking)
		}()
	}
	wg.Wait()

	seen := make(map[string]int)
	recorder.sink.mu.Lock()
	for _, entry := range recorder.sink.entries {
		if entry.message == "Agent state changed" {
			jobID, _ := entry.fields["job_id"].(string)
			seen[jobID]++
		}
	}
	recorder.sink.mu.Unlock()
	for _, jobID := range jobIDs {
		if seen[jobID] != 1 {
			t.Errorf("Agent state changed logs for %q = %d, want 1", jobID, seen[jobID])
		}
	}
}
