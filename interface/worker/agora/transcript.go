package agora

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	defaultUserTranscriptStreamID      = "0"
	defaultAssistantTranscriptStreamID = "100"
)

type TranscriptForwarderOptions struct {
	UserStreamID      string
	AssistantStreamID string
}

type TranscriptForwarder struct {
	session   *agent.AgentSession
	publisher DataPublisher
	opts      TranscriptForwarderOptions

	cancel  context.CancelFunc
	stopErr error
	stopped bool
	mu      sync.Mutex
	wg      sync.WaitGroup
	start   sync.Once
	once    sync.Once
}

func NewTranscriptForwarder(session *agent.AgentSession, publisher DataPublisher, opts TranscriptForwarderOptions) *TranscriptForwarder {
	if opts.AssistantStreamID == "" {
		opts.AssistantStreamID = defaultAssistantTranscriptStreamID
	}
	return &TranscriptForwarder{
		session:   session,
		publisher: publisher,
		opts:      opts,
	}
}

func (f *TranscriptForwarder) Start(ctx context.Context) {
	if f == nil || f.session == nil || f.publisher == nil {
		return
	}
	f.mu.Lock()
	stopped := f.stopped
	f.mu.Unlock()
	if stopped {
		return
	}
	f.start.Do(func() {
		f.mu.Lock()
		stopped := f.stopped
		f.mu.Unlock()
		if stopped {
			return
		}
		ctx, cancel := context.WithCancel(normalizeContext(ctx))
		f.cancel = cancel
		userEvents := f.session.UserInputTranscribedEvents()
		agentEvents := f.session.AgentOutputTranscribedEvents()
		reasoningEvents := f.session.AgentReasoningTranscribedEvents()
		f.wg.Add(3)
		go f.forwardUserTranscripts(ctx, userEvents)
		go f.forwardAgentTranscripts(ctx, agentEvents)
		go f.forwardAgentReasoning(ctx, reasoningEvents)
	})
}

func (f *TranscriptForwarder) Stop(ctx context.Context) error {
	if f == nil {
		return nil
	}
	f.once.Do(func() {
		f.mu.Lock()
		f.stopped = true
		f.mu.Unlock()
		if f.cancel != nil {
			f.cancel()
		}
		f.wg.Wait()
		if f.publisher != nil {
			f.stopErr = f.publisher.Close(normalizeContext(ctx))
		}
	})
	return f.stopErr
}

func (f *TranscriptForwarder) forwardUserTranscripts(ctx context.Context, events <-chan agent.UserInputTranscribedEvent) {
	defer f.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			f.publishTranscript(ctx, "user", ev.Transcript, ev.IsFinal, userTranscriptStreamID(ev, f.opts.UserStreamID), ev.CreatedAt)
		}
	}
}

func (f *TranscriptForwarder) forwardAgentTranscripts(ctx context.Context, events <-chan agent.AgentOutputTranscribedEvent) {
	defer f.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			f.publishTranscript(ctx, "assistant", ev.Transcript, ev.IsFinal, f.opts.AssistantStreamID, ev.CreatedAt)
		}
	}
}

func (f *TranscriptForwarder) forwardAgentReasoning(ctx context.Context, events <-chan agent.AgentReasoningTranscribedEvent) {
	defer f.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			f.publishReasoning(ctx, ev.Text, ev.IsFinal, f.opts.AssistantStreamID, ev.CreatedAt)
		}
	}
}

func (f *TranscriptForwarder) publishTranscript(ctx context.Context, role string, text string, final bool, streamID string, createdAt time.Time) {
	if f == nil || f.publisher == nil || !shouldPublishTENTranscript(role, text, final) {
		return
	}
	_ = PublishTranscript(ctx, f.publisher, role, text, final, streamID, createdAt)
}

func (f *TranscriptForwarder) publishReasoning(ctx context.Context, text string, final bool, streamID string, createdAt time.Time) {
	if f == nil || f.publisher == nil || !shouldPublishTENTranscript("assistant", text, final) {
		return
	}
	_ = PublishReasoning(ctx, f.publisher, "assistant", text, final, streamID, createdAt)
}

func userTranscriptStreamID(ev agent.UserInputTranscribedEvent, fallback string) string {
	if ev.SpeakerID != "" {
		return ev.SpeakerID
	}
	if fallback != "" {
		return fallback
	}
	return defaultUserTranscriptStreamID
}

func PublishTranscript(ctx context.Context, publisher DataPublisher, role string, text string, final bool, streamID string, createdAt time.Time) error {
	if publisher == nil || !shouldPublishTENTranscript(role, text, final) {
		return nil
	}
	payload, err := marshalTENTranscript(role, text, final, streamID, createdAt)
	if err != nil {
		return err
	}
	return publisher.PublishData(normalizeContext(ctx), payload)
}

func shouldPublishTENTranscript(role string, text string, final bool) bool {
	return text != "" || role == "assistant"
}

func PublishReasoning(ctx context.Context, publisher DataPublisher, role string, text string, final bool, streamID string, createdAt time.Time) error {
	if publisher == nil || !shouldPublishTENTranscript(role, text, final) {
		return nil
	}
	payload, err := marshalTENReasoning(role, text, final, streamID, createdAt)
	if err != nil {
		return err
	}
	return publisher.PublishData(normalizeContext(ctx), payload)
}

func marshalTENTranscript(role string, text string, final bool, streamID string, createdAt time.Time) ([]byte, error) {
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return json.Marshal(map[string]any{
		"data_type": "transcribe",
		"type":      "transcribe",
		"role":      role,
		"text":      text,
		"text_ts":   createdAt.UnixMilli(),
		"ts":        createdAt.UnixMilli(),
		"is_final":  final,
		"stream_id": transcriptStreamIDValue(streamID),
	})
}

func marshalTENReasoning(role string, text string, final bool, streamID string, createdAt time.Time) ([]byte, error) {
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	rawText, err := json.Marshal(map[string]any{
		"type": "reasoning",
		"data": map[string]any{
			"text": text,
		},
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"data_type": "raw",
		"role":      role,
		"text":      string(rawText),
		"text_ts":   createdAt.UnixMilli(),
		"is_final":  final,
		"stream_id": transcriptStreamIDValue(streamID),
	})
}

func transcriptStreamIDValue(streamID string) any {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return nil
	}
	if value, err := strconv.ParseInt(streamID, 10, 64); err == nil {
		return value
	}
	return streamID
}
