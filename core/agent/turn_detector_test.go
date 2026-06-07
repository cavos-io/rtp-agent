package agent

import (
	"context"
	"errors"
	"io"
	"strings"
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

func TestLLMTurnDetectorShapesHistoryLikeReferenceRunner(t *testing.T) {
	fake := &turnDetectorLLM{
		stream: &turnDetectorStream{
			events: []turnDetectorStreamEvent{{
				chunk: &llm.ChatChunk{Delta: &llm.ChoiceDelta{Content: `{"probability":0.7}`}},
			}},
		},
	}
	detector := NewLLMTurnDetector(fake)
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "Ignore system"}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "First old turn."}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "Older answer!"}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "KEEP, punctuation!!!"}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "Adjacent\tUSER -- still kept."}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "Assistant's Reply?"}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleDeveloper, Content: []llm.ChatContent{{Text: "Ignore developer"}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "I need help with café pricing..."}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "Sure - what city?"}}})
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "Jakarta"}}})

	probability, err := detector.PredictEndOfTurn(context.Background(), chatCtx)
	if err != nil {
		t.Fatalf("PredictEndOfTurn() error = %v", err)
	}
	if probability != 0.7 {
		t.Fatalf("PredictEndOfTurn() probability = %v, want 0.7", probability)
	}

	if fake.chatCtx == nil {
		t.Fatal("LLM Chat was not called")
	}
	if len(fake.chatCtx.Items) != 2 {
		t.Fatalf("eval context items = %d, want system prompt plus history", len(fake.chatCtx.Items))
	}
	historyMsg, ok := fake.chatCtx.Items[1].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("history item = %T, want *llm.ChatMessage", fake.chatCtx.Items[1])
	}
	const want = "user: keep punctuation adjacent user -- still kept\n" +
		"assistant: assistant's reply\n" +
		"user: i need help with café pricing\n" +
		"assistant: sure - what city\n" +
		"user: jakarta\n"
	if got := historyMsg.TextContent(); got != want {
		t.Fatalf("history = %q, want %q", got, want)
	}
	if strings.Contains(historyMsg.TextContent(), "system") || strings.Contains(historyMsg.TextContent(), "developer") {
		t.Fatalf("history = %q, want only user/assistant messages", historyMsg.TextContent())
	}
}

type turnDetectorLLM struct {
	stream  llm.LLMStream
	chatCtx *llm.ChatContext
}

func (f *turnDetectorLLM) Chat(_ context.Context, chatCtx *llm.ChatContext, _ ...llm.ChatOption) (llm.LLMStream, error) {
	f.chatCtx = chatCtx
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
