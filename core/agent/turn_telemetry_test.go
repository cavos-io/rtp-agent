package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collecttrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func turnSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	old := telemetry.Tracer
	telemetry.Tracer = p.Tracer("test")
	t.Cleanup(func() { telemetry.Tracer = old; _ = p.Shutdown(context.Background()) })
	return r
}

type tracedTurnAgent struct {
	*Agent
	err error
}

func (a *tracedTurnAgent) OnUserTurnCompleted(ctx context.Context, _ *llm.ChatContext, _ *llm.ChatMessage) error {
	_, span := telemetry.StartSpan(ctx, "hook_child")
	span.End()
	return a.err
}

func TestTurnTelemetryHookParent(t *testing.T) {
	r := turnSpanRecorder(t)
	a := &tracedTurnAgent{Agent: NewAgent("test")}
	s := NewAgentSession(a, nil, AgentSessionOptions{})
	activity := NewAgentActivity(a, s)
	defer activity.Stop()
	if _, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{NewTranscript: "hello", TranscriptConfidence: 1, ReplyAlreadyGenerated: true}); err != nil {
		t.Fatal(err)
	}
	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range r.Ended() {
		spans[span.Name()] = span
	}
	for _, name := range []string{"user_turn", "on_user_turn_completed", "hook_child"} {
		if spans[name] == nil {
			t.Fatalf("missing %s", name)
		}
	}
	if spans["on_user_turn_completed"].Parent().SpanID() != spans["user_turn"].SpanContext().SpanID() || spans["hook_child"].Parent().SpanID() != spans["on_user_turn_completed"].SpanContext().SpanID() {
		t.Fatal("hook spans have wrong parents")
	}
}

func assertTurnTraceBounds(t *testing.T, spans []sdktrace.ReadOnlySpan) {
	t.Helper()
	byID := map[trace.SpanID]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		if byID[s.SpanContext().SpanID()] != nil {
			t.Fatal("span ended twice")
		}
		byID[s.SpanContext().SpanID()] = s
		if s.EndTime().Before(s.StartTime()) {
			t.Fatalf("negative duration: %s", s.Name())
		}
	}
	for _, s := range spans {
		if p := byID[s.Parent().SpanID()]; p != nil && (s.StartTime().Before(p.StartTime()) || s.EndTime().After(p.EndTime())) {
			t.Fatalf("%s lies outside %s", s.Name(), p.Name())
		}
		if s.Name() == "eou_wait" {
			attrs := spanAttributeValues(s.Attributes())
			if attrs[telemetry.AttrEOUWaitDuration].AsFloat64() != float64(s.EndTime().UnixNano()-s.StartTime().UnixNano())/float64(time.Second) {
				t.Fatal("wait duration differs from span bounds")
			}
		}
	}
}

func TestTurnTelemetryRearmResumeAndStaleDetection(t *testing.T) {
	r := turnSpanRecorder(t)
	ctx, user := telemetry.StartSpan(context.Background(), "user_turn")
	var state turnTelemetry
	first := state.begin(ctx, user, time.Now(), "vad", .3)
	_, oldDetection := state.startDetection(ctx, first)
	second := state.begin(ctx, user, time.Now(), "stt", .3)
	state.notCommitted(second)
	_, detection := state.startDetection(ctx, second)
	state.detectionDone(first, 99)
	state.finish(first, "committed", time.Time{})
	state.finishCurrent("user_resumed", time.Now().Add(-time.Second))
	// Both stale completions must leave the next pause's wait untouched.
	third := state.begin(ctx, user, time.Now(), "vad", .3)
	oldDetection.SetAttributes(attribute.String("late", "old"))
	detection.SetAttributes(attribute.String("late", "old"))
	state.detectionDone(second, 99)
	state.finish(second, "committed", time.Time{})
	state.finish(third, "committed", time.Time{})
	user.End()
	assertTurnTraceBounds(t, r.Ended())
	var waits []sdktrace.ReadOnlySpan
	for _, s := range r.Ended() {
		if s.Name() == "eou_wait" {
			waits = append(waits, s)
		}
	}
	if len(waits) != 2 {
		t.Fatalf("wait count %d", len(waits))
	}
	attrs := spanAttributeValues(waits[0].Attributes())
	if attrs[telemetry.AttrEOUOutcome].AsString() != "user_resumed" || attrs[telemetry.AttrEOURearmCount].AsInt64() != 1 || attrs[telemetry.AttrEOUNotCommittedCount].AsInt64() != 1 || attrs[telemetry.AttrEOUDelay].AsFloat64() != .3 {
		t.Fatalf("wait attrs %v", attrs)
	}
	if spanAttributeValues(waits[1].Attributes())[telemetry.AttrEOUOutcome].AsString() != "committed" {
		t.Fatal("new wait was altered")
	}
	if spanAttributeValues(spanByName(t, r.Ended(), "user_turn").Attributes())[telemetry.AttrEOUResumeCount].AsInt64() != 1 {
		t.Fatal("resume count missing")
	}
}

func TestTurnTelemetryStreamingOutcomes(t *testing.T) {
	for _, name := range []string{"cached", "pending", "incomplete", "missing", "error", "timeout"} {
		t.Run(name, func(t *testing.T) {
			r := turnSpanRecorder(t)
			a, _, stream := setupTurnStream(t)
			minDelay, maxDelay := .001, .002
			if err := a.Session.UpdateOptions(AgentSessionUpdateOptions{MinEndpointingDelay: &minDelay, MaxEndpointingDelay: &maxDelay}); err != nil {
				t.Fatal(err)
			}
			if name != "missing" {
				turnVAD(a, vad.VADEventStartOfSpeech, 1, .1, 0)
				turnVAD(a, vad.VADEventInferenceDone, 1.2, 0, .2)
				call := nextTurnCall(t, stream)
				_, request, _ := a.Session.turnStream.snapshot(a)
				switch name {
				case "error":
					call.fail <- errors.New("prediction failed")
				case "timeout":
				case "pending":
					go func() {
						// Wait for the actual detector-consumption span, not a sleep.
						for {
							for _, s := range r.Started() {
								if s.Name() == "eou_detection" {
									call.reply <- AudioTurnResult{Probability: .9, IsComplete: true}
									return
								}
							}
							select {
							case <-call.ctx.Done():
								return
							default:
								runtime.Gosched()
							}
						}
					}()
				default:
					call.reply <- AudioTurnResult{Probability: .9, IsComplete: name != "incomplete"}
					<-request.done
				}
				turnVAD(a, vad.VADEventEndOfSpeech, 1.3, 0, .3)
			}
			stopped := timeToUnixSeconds(time.Now().Add(-10 * time.Millisecond))
			a.runEOUDetection(EndOfTurnInfo{SkipReply: true, NewTranscript: "hello", TranscriptConfidence: 1, StoppedSpeakingAt: &stopped, Language: "en"})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := a.WaitForInactive(ctx); err != nil {
				t.Fatal(err)
			}
			spans := r.Ended()
			wait, detection, user := spanByName(t, spans, "eou_wait"), spanByName(t, spans, "eou_detection"), spanByName(t, spans, "user_turn")
			if detection.Parent().SpanID() != wait.SpanContext().SpanID() || wait.Parent().SpanID() != user.SpanContext().SpanID() {
				t.Fatal("wrong parent tree")
			}
			attrs := spanAttributeValues(detection.Attributes())
			if _, old := attrs["eou.from_cache"]; old {
				t.Fatal("legacy cache attribute")
			}
			if attrs[telemetry.AttrEOUFromCache].AsBool() != (name == "cached" || name == "incomplete") && name != "error" {
				t.Fatalf("cache attr %v", attrs)
			}
			fallback := name == "missing" || name == "error" || name == "timeout"
			if attrs[telemetry.AttrEOUFallback].AsBool() != fallback {
				t.Fatalf("fallback attr %v", attrs)
			}
			want := .001
			if name == "incomplete" {
				want = .002
			}
			if attrs[telemetry.AttrEOUDelay].AsFloat64() != want {
				t.Fatalf("delay attrs %v", attrs)
			}
			assertTurnTraceBounds(t, spans)
		})
	}
}

func TestTurnTelemetryLifecycleDrops(t *testing.T) {
	for _, action := range []string{"clear", "mode", "stop", "resume"} {
		t.Run(action, func(t *testing.T) {
			r := turnSpanRecorder(t)
			base := NewAgent("test")
			a := NewAgentActivity(base, NewAgentSession(base, nil, AgentSessionOptions{}))
			defer a.Stop()
			info := EndOfTurnInfo{NewTranscript: "hello"}
			attempt := a.beginEOUTrace(a.ctx, info, "vad")
			a.turnTelemetry.startDetection(a.ctx, attempt)
			switch action {
			case "clear":
				a.ClearUserTurn()
			case "mode":
				mode := TurnDetectionModeManual
				_ = a.UpdateOptions(AgentSessionUpdateOptions{TurnDetection: &mode})
			case "stop":
				a.Stop()
			case "resume":
				a.OnStartOfSpeech(nil)
			}
			a.turnTelemetry.detectionDone(attempt, 99)
			a.Session.finishUserTurnSpan(attempt.wait.user)
			wait := spanByName(t, r.Ended(), "eou_wait")
			want := "dropped"
			if action == "resume" {
				want = "user_resumed"
			}
			if spanAttributeValues(wait.Attributes())[telemetry.AttrEOUOutcome].AsString() != want {
				t.Fatal("wrong outcome")
			}
			assertTurnTraceBounds(t, r.Ended())
		})
	}
}

func TestTurnTelemetryHookOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		skip bool
	}{
		{name: "stop", err: llm.StopResponse{}}, {name: "error", err: errors.New("hook failure")}, {name: "skip", skip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := turnSpanRecorder(t)
			a := &tracedTurnAgent{Agent: NewAgent("test"), err: tc.err}
			s := NewAgentSession(a, nil, AgentSessionOptions{})
			activity := NewAgentActivity(a, s)
			defer activity.Stop()
			_, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{NewTranscript: "hello", TranscriptConfidence: 1, ReplyAlreadyGenerated: true, SkipReply: tc.skip})
			if err != nil {
				t.Fatal(err)
			}
			if tc.skip {
				for _, s := range r.Ended() {
					if s.Name() == "on_user_turn_completed" {
						t.Fatal("span for skipped hook")
					}
				}
				return
			}
			hook := spanByName(t, r.Ended(), "on_user_turn_completed")
			want := "exception"
			if tc.name == "stop" {
				want = "stop_response"
			}
			if len(hook.Events()) != 1 || hook.Events()[0].Name != want {
				t.Fatalf("hook events %v", hook.Events())
			}
			assertTurnTraceBounds(t, r.Ended())
		})
	}
}

func TestTurnTelemetryReferenceScenario(t *testing.T) {
	path := os.Getenv("TURN_TELEMETRY_SCENARIO")
	if path == "" {
		path = "../../scripts/parity-fixtures/turn-telemetry.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var scenario struct {
		Contract string                                   `json:"contract"`
		Events   []struct{ Type, Source, Outcome string } `json:"events"`
	}
	if err := json.Unmarshal(data, &scenario); err != nil {
		t.Fatal(err)
	}
	r := turnSpanRecorder(t)
	ctx, user := telemetry.StartSpan(context.Background(), "user_turn", trace.WithTimestamp(time.Now().Add(-2*time.Second)))
	var state turnTelemetry
	var attempt *eouTraceAttempt
	for _, event := range scenario.Events {
		switch event.Type {
		case "begin":
			attempt = state.begin(ctx, user, time.Now().Add(-time.Second), event.Source, .3)
		case "detect":
			state.startDetection(ctx, attempt)
		case "detected":
			state.detectionDone(attempt, .3)
		case "not_committed":
			state.notCommitted(attempt)
		case "finish":
			at := time.Time{}
			if event.Outcome == "user_resumed" {
				at = time.Now().Add(-100 * time.Millisecond)
			}
			state.finish(attempt, event.Outcome, at)
		default:
			t.Fatalf("unknown event %s", event.Type)
		}
	}
	user.End()
	spans := r.Ended()
	assertTurnTraceBounds(t, spans)
	byID := map[trace.SpanID]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		byID[s.SpanContext().SpanID()] = s
	}
	var normalized []map[string]any
	for _, s := range spans {
		parent := ""
		if p := byID[s.Parent().SpanID()]; p != nil {
			parent = p.Name()
		}
		attrs := map[string]any{}
		for _, a := range s.Attributes() {
			switch string(a.Key) {
			case telemetry.AttrEOUSource, telemetry.AttrEOUOutcome, telemetry.AttrEOUDelay, telemetry.AttrEOURearmCount, telemetry.AttrEOUNotCommittedCount, telemetry.AttrEOUResumeCount:
				attrs[string(a.Key)] = a.Value.AsInterface()
			}
		}
		events := []string{}
		for _, e := range s.Events() {
			events = append(events, e.Name)
		}
		normalized = append(normalized, map[string]any{"name": s.Name(), "parent": parent, "attributes": attrs, "events": events, "contained": true, "duration_matches": true})
	}
	if len(normalized) != 7 {
		t.Fatalf("span count %d", len(normalized))
	}
	encoded, err := json.Marshal(map[string]any{"contract": scenario.Contract, "events": normalized})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TURN_TRACE %s", encoded)
}

func TestTurnTelemetryOTLPExport(t *testing.T) {
	for _, redacted := range []bool{false, true} {
		t.Run(fmt.Sprint(redacted), func(t *testing.T) {
			var mu sync.Mutex
			var exported []*tracepb.Span
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/observability/traces/otlp/v0" {
					w.WriteHeader(http.StatusOK)
					return
				}
				var body io.Reader = r.Body
				if r.Header.Get("Content-Encoding") == "gzip" {
					reader, err := gzip.NewReader(r.Body)
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					defer reader.Close()
					body = reader
				}
				data, err := io.ReadAll(body)
				if err != nil {
					t.Error(err)
					return
				}
				var request collecttrace.ExportTraceServiceRequest
				if err := proto.Unmarshal(data, &request); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				for _, resource := range request.ResourceSpans {
					for _, scope := range resource.ScopeSpans {
						exported = append(exported, scope.Spans...)
					}
				}
				w.Header().Set("Content-Type", "application/x-protobuf")
			}))
			defer server.Close()
			observability, err := telemetry.NewJobObservability(context.Background(), telemetry.JobObservabilityConfig{EndpointURL: server.URL, HTTPClient: server.Client(), JobID: "telemetry-test", RoomID: "test-room", TraceMetadata: []attribute.KeyValue{attribute.Bool(telemetry.AttrRedactionEnabled, redacted)}})
			if err != nil {
				t.Fatal(err)
			}
			defer observability.Shutdown(context.Background())
			ctx, sessionSpan := telemetry.StartSpan(observability.Context(context.Background()), "agent_session")
			base := &tracedTurnAgent{Agent: NewAgent("test")}
			base.TurnDetector = WrapTextTurnDetector(legacyTurnDetector{})
			if redacted {
				base.err = errors.New("private hook input")
			}
			s := NewAgentSession(base, nil, AgentSessionOptions{MinEndpointingDelay: .001})
			a := newAgentActivity(base, s, ctx)
			a.runEOUDetection(EndOfTurnInfo{NewTranscript: "hello", TranscriptConfidence: 1, ReplyAlreadyGenerated: true})
			waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := a.WaitForInactive(waitCtx); err != nil {
				t.Fatal(err)
			}
			a.Stop()
			sessionSpan.End()
			if err := observability.ForceFlush(waitCtx); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			byName := map[string]*tracepb.Span{}
			for _, span := range exported {
				byName[span.Name] = span
			}
			for child, parent := range map[string]string{"user_turn": "agent_session", "eou_wait": "user_turn", "eou_detection": "eou_wait", "on_user_turn_completed": "user_turn", "hook_child": "on_user_turn_completed"} {
				c, p := byName[child], byName[parent]
				if c == nil || p == nil {
					t.Fatalf("missing %s/%s", child, parent)
				}
				if !bytes.Equal(c.ParentSpanId, p.SpanId) || !bytes.Equal(c.TraceId, p.TraceId) {
					t.Fatalf("wrong OTLP parent for %s", child)
				}
				if c.StartTimeUnixNano < p.StartTimeUnixNano || c.EndTimeUnixNano > p.EndTimeUnixNano {
					t.Fatalf("OTLP bounds %s", child)
				}
			}
			wait := byName["eou_wait"]
			for _, attr := range wait.Attributes {
				if attr.Key == telemetry.AttrEOUWaitDuration && attr.Value.GetDoubleValue() != float64(wait.EndTimeUnixNano-wait.StartTimeUnixNano)/1e9 {
					t.Fatal("exported wait duration differs from OTLP bounds")
				}
			}
			if redacted {
				hook := byName["on_user_turn_completed"]
				if hook.Status.GetCode() != tracepb.Status_STATUS_CODE_ERROR {
					t.Fatal("hook error status missing")
				}
				if strings.Contains(hook.String(), "private hook input") || len(hook.Events) != 0 {
					t.Fatal("redacted hook error leaked")
				}
			}
		})
	}
}

func TestTurnTelemetrySessionHandoffAndShutdown(t *testing.T) {
	r := turnSpanRecorder(t)
	s := NewAgentSession(NewAgent("first"), nil, AgentSessionOptions{DisableUserAwayTimeout: true})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background())
	old := s.currentActivity()
	first := old.beginEOUTrace(old.ctx, EndOfTurnInfo{NewTranscript: "pending"}, "vad")
	old.turnTelemetry.startDetection(old.ctx, first)
	s.UpdateAgent(NewAgent("next"))
	next := s.currentActivity()
	second := next.beginEOUTrace(next.ctx, EndOfTurnInfo{NewTranscript: "new"}, "stt")
	next.turnTelemetry.startDetection(next.ctx, second)
	old.turnTelemetry.finish(first, "committed", time.Time{})
	old.turnTelemetry.detectionDone(first, 99)
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	waits := 0
	for _, span := range r.Ended() {
		if span.Name() == "eou_wait" {
			waits++
			if spanAttributeValues(span.Attributes())[telemetry.AttrEOUOutcome].AsString() != "dropped" {
				t.Fatal("stale handoff changed outcome")
			}
		}
	}
	if waits != 2 {
		t.Fatalf("wait count %d", waits)
	}
	assertTurnTraceBounds(t, r.Ended())
}

func TestTurnTelemetryBackchannelPreservesSpanEnd(t *testing.T) {
	a, r := newBackchannelTurnActivity(t)
	defer a.Stop()
	a.OnStartOfSpeech(nil)
	attempt := a.beginEOUTrace(a.Session.runCtx, EndOfTurnInfo{}, "vad")
	a.turnTelemetry.startDetection(a.Session.runCtx, attempt)
	a.turnTelemetry.detectionDone(attempt, .3)
	started := a.userSpeechStartedAt
	a.OnOverlapSpeechEnded(OverlappingSpeechEvent{OverlapStartedAt: &started, DetectedAt: time.Now()})
	a.OnEndOfSpeech(nil)
	wait := spanByName(t, r.Ended(), "eou_wait")
	user := spanByName(t, r.Ended(), "user_turn")
	if !user.EndTime().Equal(a.userSpeechStoppedAt) || !wait.EndTime().Equal(a.userSpeechStoppedAt) {
		t.Fatal("backchannel end timing changed")
	}
	if spanAttributeValues(wait.Attributes())[telemetry.AttrEOUOutcome].AsString() != "dropped" {
		t.Fatal("backchannel wait not dropped")
	}
	assertTurnTraceBounds(t, r.Ended())
}

func TestTurnTelemetryWithoutDetectorAndManualCommit(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprint(manual), func(t *testing.T) {
			r := turnSpanRecorder(t)
			base := NewAgent("test")
			base.TurnDetection = TurnDetectionModeManual
			s := NewAgentSession(base, nil, AgentSessionOptions{MinEndpointingDelay: .001})
			a := NewAgentActivity(base, s)
			defer a.Stop()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if manual {
				a.OnFinalTranscript(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "hello", Language: "en", Confidence: 1}}})
				if _, err := a.CommitUserTurn(ctx, CommitUserTurnOptions{SkipReply: true}); err != nil {
					t.Fatal(err)
				}
			} else {
				base.TurnDetection = TurnDetectionModeVAD
				a.runEOUDetection(EndOfTurnInfo{NewTranscript: "hello", TranscriptConfidence: 1, SkipReply: true})
				if err := a.WaitForInactive(ctx); err != nil {
					t.Fatal(err)
				}
			}
			wait := spanByName(t, r.Ended(), "eou_wait")
			attrs := spanAttributeValues(wait.Attributes())
			want := "vad"
			if manual {
				want = "manual"
			}
			if attrs[telemetry.AttrEOUSource].AsString() != want || attrs[telemetry.AttrEOUOutcome].AsString() != "committed" {
				t.Fatalf("wait attrs %v", attrs)
			}
			for _, span := range r.Ended() {
				if span.Name() == "eou_detection" {
					t.Fatal("detection without detector")
				}
			}
			assertTurnTraceBounds(t, r.Ended())
		})
	}
}
