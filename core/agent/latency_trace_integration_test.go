package agent

import (
	"context"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func latencyMeterReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(previous); _ = provider.Shutdown(context.Background()) })
	return reader
}

func assertLatencyHistograms(t *testing.T, reader *sdkmetric.ManualReader, want map[string]float64, absent ...string) {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	measurements := make(map[string]metricdata.HistogramDataPoint[float64])
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if histogram, ok := metric.Data.(metricdata.Histogram[float64]); ok {
				if len(histogram.DataPoints) != 1 || metric.Unit != "s" {
					t.Fatalf("histogram %s: points=%d, unit=%q", metric.Name, len(histogram.DataPoints), metric.Unit)
				}
				measurements[metric.Name] = histogram.DataPoints[0]
			}
		}
	}
	for name, expected := range want {
		point, ok := measurements[name]
		if !ok || point.Count != 1 || math.Abs(point.Sum-expected) > 1e-6 {
			t.Errorf("histogram %s: present=%v count=%d sum=%g, want one sample of %g", name, ok, point.Count, point.Sum, expected)
		}
	}
	for _, name := range absent {
		if _, ok := measurements[name]; ok {
			t.Errorf("unavailable measurement %s was exported", name)
		}
	}
}

func TestLatencyImmediateEmission(t *testing.T) {
	reader := latencyMeterReader(t)
	session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
	message := &llm.ChatMessage{
		ID:      "assistant-1",
		Role:    llm.ChatRoleAssistant,
		Metrics: map[string]any{"e2e_latency": 0.5},
	}

	session.EmitConversationItemAdded(message)
	assertLatencyHistograms(t, reader, map[string]float64{"lk.agents.turn.e2e_latency": 0.5})

	session.flushOTelTurnMetrics(context.Background())
	assertLatencyHistograms(t, reader, map[string]float64{"lk.agents.turn.e2e_latency": 0.5})
}

func TestLatencyCloseFlushIdempotent(t *testing.T) {
	t.Run("duplicate assistant ID", func(t *testing.T) {
		reader := latencyMeterReader(t)
		session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
		message := &llm.ChatMessage{ID: "assistant-1", Role: llm.ChatRoleAssistant, Metrics: map[string]any{"e2e_latency": 0.5}}

		session.EmitConversationItemAdded(message)
		session.EmitConversationItemAdded(message)
		session.flushOTelTurnMetrics(context.Background())

		assertLatencyHistograms(t, reader, map[string]float64{"lk.agents.turn.e2e_latency": 0.5})
	})

	t.Run("user metric waits for close flush", func(t *testing.T) {
		reader := latencyMeterReader(t)
		session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
		message := &llm.ChatMessage{ID: "user-1", Role: llm.ChatRoleUser, Metrics: map[string]any{"end_of_turn_delay": 0.25}}

		session.EmitConversationItemAdded(message)
		assertLatencyHistograms(t, reader, nil, "lk.agents.turn.end_of_turn_delay")

		session.flushOTelTurnMetrics(context.Background())
		assertLatencyHistograms(t, reader, map[string]float64{"lk.agents.turn.end_of_turn_delay": 0.25})
	})
}

func TestLatencyRealtimeSpeechEndSharedByTraceAndMeter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := traceParityRecorder(t)
		reader := latencyMeterReader(t)
		session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
		activity := NewAgentActivity(session.Agent, session)
		session.activity = activity
		defer activity.Stop()
		ma := NewMultimodalAgent(nil, llm.NewChatContext())
		ma.session, ma.ctx = session, context.Background()
		start := timeToUnixSeconds(time.Now().Add(-time.Second))
		stop := timeToUnixSeconds(time.Now().Add(-400 * time.Millisecond))
		ma.handleRealtimeEvent(llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted, SpeechStartTime: &start})
		ma.handleRealtimeEvent(llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStopped, SpeechStopped: &llm.InputSpeechStoppedEvent{ItemID: "user-timing", SpeechEndTime: &stop}})
		for _, span := range recorder.Ended() {
			if span.Name() == "user_turn" {
				t.Error("speech-stop metrics prematurely ended the user turn")
			}
		}
		activity.eouMu.Lock()
		waitStart := activity.eouTrace.started
		activity.eouMu.Unlock()
		if !waitStart.Equal(unixSecondsToTime(stop)) {
			t.Error("EOU wait used event receipt instead of the metric's speech-end anchor")
		}
		activity.OnInputAudioTranscriptionCompleted(llm.InputTranscriptionCompleted{ItemID: "user-timing", Transcript: "hello", IsFinal: true})
		time.Sleep(100 * time.Millisecond)
		messages := make(chan llm.MessageGeneration)
		close(messages)
		if _, err := activity.OnGenerationCreated(llm.GenerationCreatedEvent{ResponseID: "reply", MessageCh: messages}); err != nil {
			t.Fatal(err)
		}
		var turn, wait sdktrace.ReadOnlySpan
		for _, span := range recorder.Ended() {
			switch span.Name() {
			case "user_turn":
				turn = span
			case "eou_wait":
				wait = span
			}
		}
		if turn == nil || wait == nil || wait.Parent().SpanID() != turn.SpanContext().SpanID() || wait.StartTime().Before(turn.StartTime()) || wait.EndTime().After(turn.EndTime()) {
			t.Fatal("EOU wait escaped its user-turn lifetime")
		}
		if got := spanAttributeValues(turn.Attributes())[telemetry.AttrEndOfTurnDelay].AsFloat64(); got != 0.4 {
			t.Fatalf("turn delay=%g, want 0.4s from actual speech end", got)
		}
		session.flushOTelTurnMetrics(context.Background())
		assertLatencyHistograms(t, reader, map[string]float64{"lk.agents.turn.end_of_turn_delay": 0.4}, "lk.agents.turn.transcription_delay")
	})
}

func TestLatencyPipelineSpanAttributesMatchMeter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := traceParityRecorder(t)
		reader := latencyMeterReader(t)
		session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{UseTTSAlignedTranscript: true})
		provider := &latencyLLM{setup: 300 * time.Millisecond}
		provider.stream = &latencyLLMStream{chunks: []*llm.ChatChunk{{Delta: &llm.ChoiceDelta{Content: "Hello."}}}, delays: []time.Duration{200 * time.Millisecond}}
		va := NewPipelineAgent(nil, nil, provider, &latencyChunkedTTS{}, llm.NewChatContext())
		va.session, va.ctx = session, context.Background()
		playback := &latencyPlaybackController{}
		session.SetAudioPlaybackController(playback)
		va.PublishAudio = func(context.Context, *model.AudioFrame) error {
			playback.notify(time.Now())
			time.Sleep(300 * time.Millisecond)
			return nil
		}
		speech := NewSpeechHandle(true, DefaultInputDetails())
		speech.Generation.UserMessage = &llm.ChatMessage{Metrics: map[string]any{"stopped_speaking_at": timeToUnixSeconds(time.Now().Add(-time.Second))}}
		va.OnSpeechScheduled(context.Background(), speech)
		seen := make(map[string]float64)
		for _, span := range recorder.Ended() {
			attrs := spanAttributeValues(span.Attributes())
			for _, key := range []string{telemetry.AttrResponseTTFT, telemetry.AttrResponseTTFB, telemetry.AttrE2ELatency} {
				if value, ok := attrs[key]; ok {
					seen[key] = value.AsFloat64()
				}
			}
		}
		for key, expected := range map[string]float64{telemetry.AttrResponseTTFT: 0.5, telemetry.AttrResponseTTFB: 0.1, telemetry.AttrE2ELatency: 1.9} {
			if value, ok := seen[key]; !ok || math.Abs(value-expected) > 1e-6 {
				t.Errorf("span attribute %s=%g present=%v, want %g", key, value, ok, expected)
			}
		}
		session.flushOTelTurnMetrics(context.Background())
		assertLatencyHistograms(t, reader, map[string]float64{"lk.agents.turn.llm_ttft": 0.5, "lk.agents.turn.tts_ttfb": 0.1, "lk.agents.turn.e2e_latency": 1.9})
	})
}

func TestLatencyUnknownAndZeroMatchTraceAndMeter(t *testing.T) {
	for _, known := range []bool{false, true} {
		name := "unknown"
		if known {
			name = "zero"
		}
		t.Run(name, func(t *testing.T) {
			recorder := traceParityRecorder(t)
			reader := latencyMeterReader(t)
			session := NewAgentSession(NewAgent("timing"), nil, AgentSessionOptions{})
			activity := NewAgentActivity(session.Agent, session)
			defer activity.Stop()
			_, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{NewTranscript: "hello", TranscriptConfidence: 1, SkipReply: true, EndOfTurnDelaySet: known, TranscriptionDelaySet: known})
			if err != nil {
				t.Fatal(err)
			}
			foundTurn := false
			for _, span := range recorder.Ended() {
				if span.Name() != "user_turn" {
					continue
				}
				foundTurn = true
				attrs := spanAttributeValues(span.Attributes())
				for _, key := range []string{telemetry.AttrEndOfTurnDelay, telemetry.AttrTranscriptionDelay} {
					value, present := attrs[key]
					if present != known || (present && value.AsFloat64() != 0) {
						t.Errorf("%s present=%v value=%v, want known=%v", key, present, value, known)
					}
				}
			}
			if !foundTurn {
				t.Fatal("missing user-turn span for the committed input")
			}
			session.flushOTelTurnMetrics(context.Background())
			if known {
				assertLatencyHistograms(t, reader, map[string]float64{"lk.agents.turn.end_of_turn_delay": 0, "lk.agents.turn.transcription_delay": 0})
			} else {
				assertLatencyHistograms(t, reader, nil, "lk.agents.turn.end_of_turn_delay", "lk.agents.turn.transcription_delay")
			}
		})
	}
}

func TestLatencyRealtimeNonfiniteTTFTIsOmitted(t *testing.T) {
	for _, ttft := range []float64{math.Inf(1), math.NaN()} {
		recorder := traceParityRecorder(t)
		ma := NewMultimodalAgent(nil, llm.NewChatContext())
		ma.ctx = context.Background()
		messages := make(chan llm.MessageGeneration)
		close(messages)
		ma.consumeRealtimeGeneration(context.Background(), nil, &llm.GenerationCreatedEvent{ResponseID: "invalid", MessageCh: messages})
		ma.recordRealtimeMetrics(&telemetry.RealtimeModelMetrics{RequestID: "invalid", TTFT: ttft, Timestamp: time.Now()})
		for _, span := range recorder.Ended() {
			attrs := spanAttributeValues(span.Attributes())
			for _, key := range []string{telemetry.AttrGenAIResponseTTFC, "langfuse.completion_start_time"} {
				if _, ok := attrs[key]; ok {
					t.Errorf("invalid TTFT exported as %s", key)
				}
			}
		}
	}
}
