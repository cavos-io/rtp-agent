package llm

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
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

func TestFallbackAdapterReportsReferenceMetadata(t *testing.T) {
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{label: "primary.LLM"},
	})

	if got := adapter.Label(); got != "FallbackAdapter(primary.LLM)" {
		t.Fatalf("Label() = %q, want FallbackAdapter(primary.LLM)", got)
	}
	if got := adapter.Model(); got != "FallbackAdapter" {
		t.Fatalf("Model() = %q, want FallbackAdapter", got)
	}
	if got := adapter.Provider(); got != "livekit" {
		t.Fatalf("Provider() = %q, want livekit", got)
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

func TestFallbackAdapterRetriesSameLLMBeforeFallback(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &fakeFallbackLLM{streams: []LLMStream{
		&fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}},
		&fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "primary recovered"}}},
		}},
	}}
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapterWithOptions([]LLM{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerLLM: 1,
		RetryInterval:  time.Nanosecond,
	})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "primary recovered" {
		t.Fatalf("chunk content = %q, want primary recovered", got)
	}
	if primary.calls != 2 {
		t.Fatalf("primary calls = %d, want 2", primary.calls)
	}
	if fallback.calls != 0 {
		t.Fatalf("fallback calls = %d, want 0", fallback.calls)
	}
}

func TestFallbackAdapterAppliesAttemptTimeoutToProviderCall(t *testing.T) {
	primaryErr := errors.New("primary failed")
	primary := &fakeFallbackLLM{
		err: primaryErr,
		onChat: func(ctx context.Context) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("provider context has no deadline, want attempt timeout deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > time.Second {
				t.Fatalf("provider context deadline remaining = %v, want bounded attempt timeout", remaining)
			}
		},
	}
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapterWithOptions([]LLM{primary, fallback}, FallbackAdapterOptions{
		AttemptTimeout: 50 * time.Millisecond,
	})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("chunk content = %q, want fallback", got)
	}
}

func TestFallbackAdapterDefaultsAttemptTimeout(t *testing.T) {
	primaryErr := errors.New("primary failed")
	primary := &fakeFallbackLLM{
		err: primaryErr,
		onChat: func(ctx context.Context) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("provider context has no deadline, want default attempt timeout deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > 6*time.Second {
				t.Fatalf("provider context deadline remaining = %v, want default attempt timeout near 5s", remaining)
			}
		},
	}
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{primary, fallback})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("chunk content = %q, want fallback", got)
	}
}

func TestFallbackAdapterWaitsRetryIntervalBeforeSameProviderRetry(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	var callTimes []time.Time
	primary := &fakeFallbackLLM{
		streams: []LLMStream{
			&fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}},
			&fakeFallbackStream{events: []fakeFallbackEvent{
				{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "primary recovered"}}},
			}},
		},
		onChat: func(context.Context) {
			callTimes = append(callTimes, time.Now())
		},
	}
	adapter := NewFallbackAdapterWithOptions([]LLM{primary}, FallbackAdapterOptions{
		MaxRetryPerLLM: 1,
		RetryInterval:  25 * time.Millisecond,
	})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "primary recovered" {
		t.Fatalf("chunk content = %q, want primary recovered", got)
	}
	if len(callTimes) != 2 {
		t.Fatalf("callTimes length = %d, want 2", len(callTimes))
	}
	if elapsed := callTimes[1].Sub(callTimes[0]); elapsed < 25*time.Millisecond {
		t.Fatalf("retry interval = %v, want at least 25ms", elapsed)
	}
}

func TestFallbackAdapterDefaultsRetryInterval(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	var callTimes []time.Time
	primary := &fakeFallbackLLM{
		streams: []LLMStream{
			&fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}},
			&fakeFallbackStream{events: []fakeFallbackEvent{
				{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "primary recovered"}}},
			}},
		},
		onChat: func(context.Context) {
			callTimes = append(callTimes, time.Now())
		},
	}
	adapter := NewFallbackAdapterWithOptions([]LLM{primary}, FallbackAdapterOptions{
		MaxRetryPerLLM: 1,
	})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "primary recovered" {
		t.Fatalf("chunk content = %q, want primary recovered", got)
	}
	if len(callTimes) != 2 {
		t.Fatalf("callTimes length = %d, want 2", len(callTimes))
	}
	if elapsed := callTimes[1].Sub(callTimes[0]); elapsed < 500*time.Millisecond {
		t.Fatalf("retry interval = %v, want at least default 500ms", elapsed)
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

func TestFallbackAdapterReturnsAllFailedErrorWhenProvidersExhausted(t *testing.T) {
	firstErr := errors.New("primary unavailable")
	secondErr := errors.New("fallback unavailable")
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{err: firstErr},
		&fakeFallbackLLM{err: secondErr},
	})

	_, err := adapter.Chat(context.Background(), NewChatContext())
	if err == nil {
		t.Fatal("Chat error = nil, want all LLMs failed error")
	}
	if !errors.Is(err, secondErr) {
		t.Fatalf("Chat error = %v, want to wrap final provider error", err)
	}
	if !strings.Contains(err.Error(), "all LLMs failed") {
		t.Fatalf("Chat error = %q, want all LLMs failed message", err)
	}
}

func TestFallbackAdapterSkipsUnavailableProviderOnNextChat(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &fakeFallbackLLM{streams: []LLMStream{
		&fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}},
		&fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "primary should be skipped"}}},
		}},
	}}
	fallback := &fakeFallbackLLM{streams: []LLMStream{
		&fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback first"}}},
		}},
		&fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback second"}}},
		}},
	}}
	adapter := NewFallbackAdapter([]LLM{primary, fallback})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback first" {
		t.Fatalf("first stream content = %q, want fallback first", got)
	}

	stream, err = adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	chunk, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback second" {
		t.Fatalf("second stream content = %q, want fallback second", got)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want failed provider skipped on second chat", primary.calls)
	}
}

func TestFallbackAdapterEmitsAvailabilityChangedWhenProviderFails(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}}}
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{primary, fallback})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("fallback content = %q, want fallback", got)
	}

	ev := readFallbackAvailabilityEvent(t, adapter)
	if ev.LLM != primary || ev.Index != 0 || ev.Available {
		t.Fatalf("availability event = %#v, want primary unavailable", ev)
	}
}

func TestFallbackAdapterMarksProviderUnavailableAfterChunkFailure(t *testing.T) {
	firstErr := errors.New("primary stream failed after output")
	primary := &fakeFallbackLLM{streams: []LLMStream{
		&fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "partial"}}},
			{err: firstErr},
		}},
		&fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "primary should be skipped"}}},
		}},
	}}
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{primary, fallback})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "partial" {
		t.Fatalf("first stream content = %q, want partial", got)
	}
	_, err = stream.Next()
	if !errors.Is(err, firstErr) {
		t.Fatalf("second Next error = %v, want primary stream error", err)
	}

	stream, err = adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	chunk, err = stream.Next()
	if err != nil {
		t.Fatalf("fallback Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("fallback content = %q, want fallback", got)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want failed provider skipped after chunk failure", primary.calls)
	}
}

func TestFallbackAdapterEmitsAvailabilityChangedWhenProviderRecovers(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &fakeFallbackLLM{streams: []LLMStream{
		&fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}},
		&fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "recovery probe"}}},
		}},
	}}
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{primary, fallback})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("fallback content = %q, want fallback", got)
	}

	unavailable := readFallbackAvailabilityEvent(t, adapter)
	if unavailable.LLM != primary || unavailable.Index != 0 || unavailable.Available {
		t.Fatalf("availability event = %#v, want primary unavailable", unavailable)
	}

	waitForFallbackCalls(t, primary, 2)
	recovered := readFallbackAvailabilityEvent(t, adapter)
	if recovered.LLM != primary || recovered.Index != 0 || !recovered.Available {
		t.Fatalf("availability event = %#v, want primary recovered", recovered)
	}
}

func TestFallbackAdapterRecoversUnavailableProviderInBackground(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &fakeFallbackLLM{streams: []LLMStream{
		&fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}},
		&fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "recovery probe"}}},
		}},
		&fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "primary active"}}},
		}},
	}}
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{primary, fallback})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("fallback content = %q, want fallback", got)
	}

	waitForFallbackCalls(t, primary, 2)

	stream, err = adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	chunk, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "primary active" {
		t.Fatalf("second stream content = %q, want recovered primary active", got)
	}
}

type fakeFallbackLLM struct {
	streams []LLMStream
	stream  LLMStream
	err     error
	label   string
	calls   int
	onChat  func(context.Context)
}

func (f *fakeFallbackLLM) Chat(ctx context.Context, _ *ChatContext, _ ...ChatOption) (LLMStream, error) {
	f.calls++
	if f.onChat != nil {
		f.onChat(ctx)
	}
	if f.err != nil {
		return nil, f.err
	}
	if len(f.streams) > 0 {
		stream := f.streams[0]
		f.streams = f.streams[1:]
		return stream, nil
	}
	return f.stream, nil
}

func (f *fakeFallbackLLM) Label() string {
	return f.label
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

func waitForFallbackCalls(t *testing.T, llm *fakeFallbackLLM, calls int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if llm.calls >= calls {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("llm calls = %d, want at least %d", llm.calls, calls)
}

func readFallbackAvailabilityEvent(t *testing.T, adapter *FallbackAdapter) FallbackAvailabilityChangedEvent {
	t.Helper()
	select {
	case ev := <-adapter.AvailabilityChangedCh():
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fallback availability event")
	}
	return FallbackAvailabilityChangedEvent{}
}
