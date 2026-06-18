package agora

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
)

type recordingDataPublisher struct {
	payloads [][]byte
	closes   int
}

func (r *recordingDataPublisher) PublishData(_ context.Context, payload []byte) error {
	r.payloads = append(r.payloads, append([]byte(nil), payload...))
	return nil
}

func (r *recordingDataPublisher) Close(context.Context) error {
	r.closes++
	return nil
}

func TestTranscriptForwarderPublishesTENAssistantTranscript(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	publisher := &recordingDataPublisher{}
	forwarder := NewTranscriptForwarder(session, publisher, TranscriptForwarderOptions{
		AssistantStreamID: "100",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forwarder.Start(ctx)
	defer forwarder.Stop(context.Background())

	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "hello there",
		IsFinal:    true,
		CreatedAt:  time.UnixMilli(1710000000123),
	})

	got := waitForPublishedTranscript(t, publisher)
	if got["data_type"] != "transcribe" {
		t.Fatalf("data_type = %#v, want transcribe", got["data_type"])
	}
	if got["role"] != "assistant" {
		t.Fatalf("role = %#v, want assistant", got["role"])
	}
	if got["text"] != "hello there" {
		t.Fatalf("text = %#v, want transcript", got["text"])
	}
	if got["is_final"] != true {
		t.Fatalf("is_final = %#v, want true", got["is_final"])
	}
	if got["text_ts"] != float64(1710000000123) {
		t.Fatalf("text_ts = %#v, want event millis", got["text_ts"])
	}
	if got["stream_id"] != float64(100) {
		t.Fatalf("stream_id = %#v, want numeric assistant stream", got["stream_id"])
	}
}

func TestTranscriptForwarderPublishesTENUserTranscriptWithRemoteStreamID(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	publisher := &recordingDataPublisher{}
	forwarder := NewTranscriptForwarder(session, publisher, TranscriptForwarderOptions{
		UserStreamID: "caller-7",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forwarder.Start(ctx)
	defer forwarder.Stop(context.Background())

	session.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "need help",
		IsFinal:    false,
		CreatedAt:  time.UnixMilli(1710000000456),
	})

	got := waitForPublishedTranscript(t, publisher)
	if got["role"] != "user" {
		t.Fatalf("role = %#v, want user", got["role"])
	}
	if got["text"] != "need help" {
		t.Fatalf("text = %#v, want transcript", got["text"])
	}
	if got["is_final"] != false {
		t.Fatalf("is_final = %#v, want false", got["is_final"])
	}
	if got["stream_id"] != "caller-7" {
		t.Fatalf("stream_id = %#v, want remote stream id string", got["stream_id"])
	}
}

func TestTranscriptForwarderPublishesTENUserTranscriptWithSpeakerID(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	publisher := &recordingDataPublisher{}
	forwarder := NewTranscriptForwarder(session, publisher, TranscriptForwarderOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forwarder.Start(ctx)
	defer forwarder.Stop(context.Background())

	session.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "typed hello",
		IsFinal:    true,
		SpeakerID:  "caller-9",
		CreatedAt:  time.UnixMilli(1710000000555),
	})

	got := waitForPublishedTranscript(t, publisher)
	if got["role"] != "user" {
		t.Fatalf("role = %#v, want user", got["role"])
	}
	if got["text"] != "typed hello" {
		t.Fatalf("text = %#v, want transcript", got["text"])
	}
	if got["stream_id"] != "caller-9" {
		t.Fatalf("stream_id = %#v, want event speaker id", got["stream_id"])
	}
}

func TestTranscriptForwarderPublishesTENUserTranscriptWithDefaultStreamID(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	publisher := &recordingDataPublisher{}
	forwarder := NewTranscriptForwarder(session, publisher, TranscriptForwarderOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forwarder.Start(ctx)
	defer forwarder.Stop(context.Background())

	session.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "typed hello",
		IsFinal:    true,
		CreatedAt:  time.UnixMilli(1710000000666),
	})

	got := waitForPublishedTranscript(t, publisher)
	if got["role"] != "user" {
		t.Fatalf("role = %#v, want user", got["role"])
	}
	if got["text"] != "typed hello" {
		t.Fatalf("text = %#v, want transcript", got["text"])
	}
	if got["stream_id"] != float64(0) {
		t.Fatalf("stream_id = %#v, want TEN default user stream id 0", got["stream_id"])
	}
}

func TestPublishReasoningSendsTENRawPayload(t *testing.T) {
	publisher := &recordingDataPublisher{}

	err := PublishReasoning(context.Background(), publisher, "assistant", "thinking step", false, "100", time.UnixMilli(1710000000789))
	if err != nil {
		t.Fatalf("PublishReasoning() error = %v, want nil", err)
	}

	got := publishedJSON(t, publisher, 0)
	if got["data_type"] != "raw" {
		t.Fatalf("data_type = %#v, want raw", got["data_type"])
	}
	if got["role"] != "assistant" {
		t.Fatalf("role = %#v, want assistant", got["role"])
	}
	if got["is_final"] != false {
		t.Fatalf("is_final = %#v, want false", got["is_final"])
	}
	if got["text_ts"] != float64(1710000000789) {
		t.Fatalf("text_ts = %#v, want event millis", got["text_ts"])
	}
	if got["stream_id"] != float64(100) {
		t.Fatalf("stream_id = %#v, want numeric assistant stream", got["stream_id"])
	}
	rawText, ok := got["text"].(string)
	if !ok {
		t.Fatalf("text = %#v, want raw JSON string", got["text"])
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(rawText), &raw); err != nil {
		t.Fatalf("raw text is not JSON: %v", err)
	}
	if raw["type"] != "reasoning" {
		t.Fatalf("raw type = %#v, want reasoning", raw["type"])
	}
	data, ok := raw["data"].(map[string]any)
	if !ok {
		t.Fatalf("raw data = %#v, want object", raw["data"])
	}
	if data["text"] != "thinking step" {
		t.Fatalf("raw data text = %#v, want reasoning text", data["text"])
	}
}

func TestTranscriptForwarderPublishesTENReasoningTranscript(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	publisher := &recordingDataPublisher{}
	forwarder := NewTranscriptForwarder(session, publisher, TranscriptForwarderOptions{
		AssistantStreamID: "100",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forwarder.Start(ctx)
	defer forwarder.Stop(context.Background())

	session.EmitAgentReasoningTranscribed(agent.AgentReasoningTranscribedEvent{
		Text:      "thinking step",
		IsFinal:   true,
		CreatedAt: time.UnixMilli(1710000000999),
	})

	got := waitForPublishedTranscript(t, publisher)
	if got["data_type"] != "raw" {
		t.Fatalf("data_type = %#v, want raw", got["data_type"])
	}
	if got["role"] != "assistant" {
		t.Fatalf("role = %#v, want assistant", got["role"])
	}
	if got["is_final"] != true {
		t.Fatalf("is_final = %#v, want true", got["is_final"])
	}
	if got["text_ts"] != float64(1710000000999) {
		t.Fatalf("text_ts = %#v, want event millis", got["text_ts"])
	}
	if got["stream_id"] != float64(100) {
		t.Fatalf("stream_id = %#v, want numeric assistant stream", got["stream_id"])
	}
	rawText, ok := got["text"].(string)
	if !ok {
		t.Fatalf("text = %#v, want raw JSON string", got["text"])
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(rawText), &raw); err != nil {
		t.Fatalf("raw text is not JSON: %v", err)
	}
	if raw["type"] != "reasoning" {
		t.Fatalf("raw type = %#v, want reasoning", raw["type"])
	}
	data, ok := raw["data"].(map[string]any)
	if !ok {
		t.Fatalf("raw data = %#v, want object", raw["data"])
	}
	if data["text"] != "thinking step" {
		t.Fatalf("raw data text = %#v, want reasoning text", data["text"])
	}
}

func TestTranscriptForwarderStartIsIdempotent(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	publisher := &recordingDataPublisher{}
	forwarder := NewTranscriptForwarder(session, publisher, TranscriptForwarderOptions{
		AssistantStreamID: "100",
	})
	ctx, cancel := context.WithCancel(context.Background())
	forwarder.Start(ctx)
	forwarder.Start(ctx)
	defer func() {
		cancel()
		_ = forwarder.Stop(context.Background())
	}()

	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "hello once",
		IsFinal:    true,
		CreatedAt:  time.UnixMilli(1710000001111),
	})

	got := waitForPublishedTranscript(t, publisher)
	if got["text"] != "hello once" {
		t.Fatalf("text = %#v, want transcript", got["text"])
	}
	assertPublishedPayloadCount(t, publisher, 1)
}

func TestTranscriptForwarderStopIsIdempotent(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	publisher := &recordingDataPublisher{}
	forwarder := NewTranscriptForwarder(session, publisher, TranscriptForwarderOptions{})
	forwarder.Start(context.Background())

	if err := forwarder.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop() error = %v, want nil", err)
	}
	if err := forwarder.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v, want nil", err)
	}
	if publisher.closes != 1 {
		t.Fatalf("publisher closes = %d, want 1", publisher.closes)
	}
}

func TestTranscriptForwarderStopBeforeStartPreventsLaterPublish(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	publisher := &recordingDataPublisher{}
	forwarder := NewTranscriptForwarder(session, publisher, TranscriptForwarderOptions{
		AssistantStreamID: "100",
	})

	if err := forwarder.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	forwarder.Start(context.Background())
	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "late hello",
		IsFinal:    true,
		CreatedAt:  time.UnixMilli(1710000001222),
	})

	assertPublishedPayloadCount(t, publisher, 0)
	if publisher.closes != 1 {
		t.Fatalf("publisher closes = %d, want 1", publisher.closes)
	}
}

func waitForPublishedTranscript(t *testing.T, publisher *recordingDataPublisher) map[string]any {
	t.Helper()
	deadline := time.After(time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if len(publisher.payloads) > 0 {
			return publishedJSON(t, publisher, 0)
		}
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatal("timed out waiting for published transcript")
		}
	}
}

func publishedJSON(t *testing.T, publisher *recordingDataPublisher, index int) map[string]any {
	t.Helper()
	if index >= len(publisher.payloads) {
		t.Fatalf("published payload index %d missing, have %d", index, len(publisher.payloads))
	}
	var got map[string]any
	if err := json.Unmarshal(publisher.payloads[index], &got); err != nil {
		t.Fatalf("published payload is not JSON: %v", err)
	}
	return got
}

func assertPublishedPayloadCount(t *testing.T, publisher *recordingDataPublisher, want int) {
	t.Helper()
	deadline := time.After(50 * time.Millisecond)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if len(publisher.payloads) > want {
			t.Fatalf("published payload count = %d, want %d", len(publisher.payloads), want)
		}
		select {
		case <-tick.C:
		case <-deadline:
			if len(publisher.payloads) != want {
				t.Fatalf("published payload count = %d, want %d", len(publisher.payloads), want)
			}
			return
		}
	}
}
