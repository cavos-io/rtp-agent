package livekit

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	lksdk "github.com/livekit/server-sdk-go/v2"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRoomIOWaitSpansEndOnParticipantAndFirstFrame(t *testing.T) {
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	old := telemetry.Tracer
	telemetry.Tracer = p.Tracer("test")
	t.Cleanup(func() { telemetry.Tracer = old; _ = p.Shutdown(context.Background()) })
	ctx, parent := telemetry.StartSpan(context.Background(), "agent_session")
	s := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	rio := NewRoomIO(nil, s, RoomOptions{DisableAudioOutput: true})
	defer rio.Close()
	if err := rio.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !rio.handleParticipantConnected("user", lksdk.ParticipantStandard, nil, "") {
		t.Fatal("participant not selected")
	}
	rio.forwardRoomInputFrame(ctx, &model.AudioFrame{Data: []byte{0, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	parent.End()
	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range r.Ended() {
		spans[s.Name()] = s
	}
	for _, name := range []string{"wait_for_participant", "wait_for_audio_track"} {
		if spans[name] == nil {
			t.Fatalf("missing %s", name)
		}
		if spans[name].Parent().SpanID() != parent.SpanContext().SpanID() {
			t.Fatal("wait ancestry")
		}
	}
	if len(spans["wait_for_audio_track"].Events()) != 1 {
		t.Fatal("missing first_frame event")
	}
}

func TestRoomIOWaitSpansEndOnTeardown(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "participant", true: "audio"}[selected], func(t *testing.T) {
			r := tracetest.NewSpanRecorder()
			p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
			obs, err := telemetry.NewJobObservability(context.Background(), telemetry.JobObservabilityConfig{TracerProvider: p})
			if err != nil {
				t.Fatal(err)
			}
			defer obs.Shutdown(context.Background())
			ctx, parent := telemetry.StartSpan(obs.Context(context.Background()), "agent_session")
			rio := NewRoomIO(nil, nil, RoomOptions{DisableAudioOutput: true})
			rio.SetTraceContext(ctx)
			if selected {
				rio.setParticipant("user", true)
			}
			rio.forwardRoomInputFrame(ctx, &model.AudioFrame{})
			if err := rio.Close(); err != nil {
				t.Fatal(err)
			}
			parent.End()
			if len(r.Started()) != len(r.Ended()) {
				t.Fatal("input wait remained open after teardown")
			}
			for _, span := range r.Ended() {
				if span.Name() == "wait_for_audio_track" && len(span.Events()) != 0 {
					t.Fatal("an empty frame ended the audio wait")
				}
			}
		})
	}
}
