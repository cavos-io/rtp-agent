package agent

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestLLMTurnDetectorPropagatesStreamError(t *testing.T) {
	cause := errors.New("turn detector stream failed")
	detector := NewLLMTurnDetector(&turnDetectorLLM{
		stream: &turnDetectorStream{
			events: []turnDetectorStreamEvent{{err: cause}},
		},
	})
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "I was thinking"}},
	})

	_, err := detector.PredictEndOfTurn(context.Background(), chatCtx)

	if !errors.Is(err, cause) {
		t.Fatalf("PredictEndOfTurn() error = %v, want stream error %v", err, cause)
	}
}

type turnDetectorLLM struct {
	stream llm.LLMStream
}

func (f *turnDetectorLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	return f.stream, nil
}

type turnDetectorStreamEvent struct {
	chunk *llm.ChatChunk
	err   error
}

type turnDetectorStream struct {
	events []turnDetectorStreamEvent
	closed bool
}

func (s *turnDetectorStream) Next() (*llm.ChatChunk, error) {
	if len(s.events) == 0 {
		return nil, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	if event.err != nil {
		return nil, event.err
	}
	return event.chunk, nil
}

func (s *turnDetectorStream) Close() error {
	s.closed = true
	return nil
}
