package agora

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const defaultAssistantTranscriptStreamID = "100"

type TranscriptForwarderOptions struct {
	UserStreamID      string
	AssistantStreamID string
}

type TranscriptForwarder struct {
	session   *agent.AgentSession
	publisher DataPublisher
	opts      TranscriptForwarderOptions

	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
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
	ctx, cancel := context.WithCancel(normalizeContext(ctx))
	f.cancel = cancel
	userEvents := f.session.UserInputTranscribedEvents()
	agentEvents := f.session.AgentOutputTranscribedEvents()
	f.wg.Add(2)
	go f.forwardUserTranscripts(ctx, userEvents)
	go f.forwardAgentTranscripts(ctx, agentEvents)
}

func (f *TranscriptForwarder) Stop(ctx context.Context) error {
	if f == nil {
		return nil
	}
	if f.cancel != nil {
		f.cancel()
	}
	f.once.Do(func() {
		f.wg.Wait()
	})
	if f.publisher == nil {
		return nil
	}
	return f.publisher.Close(normalizeContext(ctx))
}

func (f *TranscriptForwarder) forwardUserTranscripts(ctx context.Context, events <-chan agent.UserInputTranscribedEvent) {
	defer f.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			f.publishTranscript(ctx, "user", ev.Transcript, ev.IsFinal, f.opts.UserStreamID, ev.CreatedAt)
		}
	}
}

func (f *TranscriptForwarder) forwardAgentTranscripts(ctx context.Context, events <-chan agent.AgentOutputTranscribedEvent) {
	defer f.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			f.publishTranscript(ctx, "assistant", ev.Transcript, ev.IsFinal, f.opts.AssistantStreamID, ev.CreatedAt)
		}
	}
}

func (f *TranscriptForwarder) publishTranscript(ctx context.Context, role string, text string, final bool, streamID string, createdAt time.Time) {
	if text == "" || f == nil || f.publisher == nil {
		return
	}
	_ = PublishTranscript(ctx, f.publisher, role, text, final, streamID, createdAt)
}

func PublishTranscript(ctx context.Context, publisher DataPublisher, role string, text string, final bool, streamID string, createdAt time.Time) error {
	if text == "" || publisher == nil {
		return nil
	}
	payload, err := marshalTENTranscript(role, text, final, streamID, createdAt)
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
		"role":      role,
		"text":      text,
		"text_ts":   createdAt.UnixMilli(),
		"is_final":  final,
		"stream_id": transcriptStreamIDValue(streamID),
	})
}

func transcriptStreamIDValue(streamID string) any {
	if streamID == "" {
		return nil
	}
	if value, err := strconv.ParseInt(streamID, 10, 64); err == nil {
		return value
	}
	return streamID
}
