package llm

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestFallbackAdapterRetriesNextLLMWhenStreamFailsBeforeChunk(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}}},
		&fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
		}}},
	})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("chunk content = %q, want fallback", got)
	}
}

func TestFallbackAdapterDoesNotRetryAfterChunkSent(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "partial"}}},
			{err: firstErr},
		}}},
		&fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
		}}},
	})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "partial" {
		t.Fatalf("first chunk content = %q, want partial", got)
	}

	_, err = stream.Next()
	if !errors.Is(err, firstErr) {
		t.Fatalf("second Next error = %v, want primary stream error", err)
	}
}

func TestFallbackAdapterRetriesAfterChunkSentWhenEnabled(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	adapter := NewFallbackAdapterWithOptions([]LLM{
		&fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "partial"}}},
			{err: firstErr},
		}}},
		&fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
		}}},
	}, FallbackAdapterOptions{RetryOnChunkSent: true})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "partial" {
		t.Fatalf("first chunk content = %q, want partial", got)
	}

	chunk, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("second chunk content = %q, want fallback", got)
	}
}

func TestFallbackAdapterRetriesAfterUsageOnlyChunk(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Usage: &CompletionUsage{TotalTokens: 1}}},
			{err: firstErr},
		}}},
		&fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
		}}},
	})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if chunk.Usage == nil || chunk.Usage.TotalTokens != 1 {
		t.Fatalf("first chunk usage = %#v, want usage-only chunk", chunk.Usage)
	}

	chunk, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("chunk content = %q, want fallback", got)
	}
}

func TestFallbackAdapterDoesNotRetryCleanEOF(t *testing.T) {
	second := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{stream: &fakeFallbackStream{}},
		second,
	})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF", err)
	}
	if second.calls != 0 {
		t.Fatalf("fallback LLM calls = %d, want 0", second.calls)
	}
}

type fakeFallbackLLM struct {
	stream LLMStream
	err    error
	calls  int
}

func (f *fakeFallbackLLM) Chat(context.Context, *ChatContext, ...ChatOption) (LLMStream, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.stream, nil
}

type fakeFallbackEvent struct {
	chunk *ChatChunk
	err   error
}

type fakeFallbackStream struct {
	events []fakeFallbackEvent
	index  int
	closed bool
}

func (f *fakeFallbackStream) Next() (*ChatChunk, error) {
	if f.index >= len(f.events) {
		return nil, io.EOF
	}
	event := f.events[f.index]
	f.index++
	if event.err != nil {
		return nil, event.err
	}
	return event.chunk, nil
}

func (f *fakeFallbackStream) Close() error {
	f.closed = true
	return nil
}
