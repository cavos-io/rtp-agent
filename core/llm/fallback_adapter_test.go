package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func TestFallbackAdapterForwardsProviderMetrics(t *testing.T) {
	primary := &fakeFallbackLLM{label: "primary.LLM"}
	fallback := &fakeFallbackLLM{label: "fallback.LLM"}
	adapter := NewFallbackAdapter([]LLM{primary, fallback})
	metricsCh := make(chan string, 2)

	unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.LLMMetrics) {
		metricsCh <- metrics.RequestID
	})
	defer unsubscribe()

	primary.EmitMetricsCollected(&telemetry.LLMMetrics{RequestID: "primary-req"})
	fallback.EmitMetricsCollected(&telemetry.LLMMetrics{RequestID: "fallback-req"})

	got := []string{<-metricsCh, <-metricsCh}
	if strings.Join(got, ",") != "primary-req,fallback-req" {
		t.Fatalf("forwarded metrics = %#v, want primary and fallback request IDs", got)
	}
}

func TestFallbackAdapterRequiresAtLeastOneLLM(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("NewFallbackAdapter() did not panic, want empty LLM list rejection")
		}
		if got, want := fmt.Sprint(r), "at least one LLM instance must be provided."; got != want {
			t.Fatalf("NewFallbackAdapter() panic = %q, want %q", got, want)
		}
	}()

	_ = NewFallbackAdapter(nil)
}

func TestFallbackAdapterDoesNotForwardProviderErrors(t *testing.T) {
	primary := &fakeFallbackLLM{label: "primary.LLM"}
	fallback := &fakeFallbackLLM{label: "fallback.LLM"}
	adapter := NewFallbackAdapter([]LLM{primary, fallback})
	labelsCh := make(chan string, 3)

	unsubscribe := adapter.OnError(func(err *LLMError) {
		labelsCh <- err.Label
	})
	defer unsubscribe()

	primary.EmitError(NewLLMError("primary", errors.New("primary failed"), true))
	fallback.EmitError(NewLLMError("fallback", errors.New("fallback failed"), true))
	adapter.EmitError(NewLLMError("adapter", errors.New("adapter failed"), true))

	select {
	case label := <-labelsCh:
		if label != "adapter" {
			t.Fatalf("error label = %q, want adapter-local error only", label)
		}
	default:
		t.Fatal("error handler was not called")
	}
	select {
	case label := <-labelsCh:
		t.Fatalf("unexpected forwarded provider error label %q", label)
	default:
	}
}

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

func TestFallbackAdapterStreamExposesChatContext(t *testing.T) {
	providerChatCtx := NewChatContext()
	providerChatCtx.AddMessage(ChatMessageArgs{Role: ChatRoleAssistant, Text: "provider"})
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{stream: &fakeFallbackStream{
			chatCtx: providerChatCtx,
			events: []fakeFallbackEvent{
				{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "ok"}}},
			},
		}},
	})
	chatCtx := NewChatContext()
	chatCtx.AddMessage(ChatMessageArgs{Role: ChatRoleUser, Text: "hello"})

	stream, err := adapter.Chat(context.Background(), chatCtx)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	defer stream.Close()

	chatCtxStream, ok := stream.(interface{ ChatCtx() *ChatContext })
	if !ok {
		t.Fatal("fallback stream does not expose ChatCtx()")
	}
	if got := chatCtxStream.ChatCtx(); got != chatCtx {
		t.Fatalf("ChatCtx() = %p, want original context %p", got, chatCtx)
	}

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chatCtxStream.ChatCtx(); got != providerChatCtx {
		t.Fatalf("ChatCtx() after first chunk = %p, want provider context %p", got, providerChatCtx)
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

func TestFallbackAdapterDoesNotRetrySameLLMBeforeFallback(t *testing.T) {
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
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("chunk content = %q, want fallback", got)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}
	if fallback.calls != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallback.calls)
	}
	if len(primary.options) != 1 {
		t.Fatalf("primary options = %d, want 1", len(primary.options))
	}
	connectOptions := primary.options[0].ConnectOptions
	if connectOptions == nil {
		t.Fatal("primary ConnectOptions = nil, want fallback attempt options")
	}
	if connectOptions.MaxRetry != 1 {
		t.Fatalf("primary MaxRetry = %d, want provider-level retry count", connectOptions.MaxRetry)
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

func TestFallbackAdapterPassesRetryIntervalToProviderWithoutAdapterDelay(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &fakeFallbackLLM{
		streams: []LLMStream{
			&fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}},
			&fakeFallbackStream{events: []fakeFallbackEvent{
				{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "primary recovered"}}},
			}},
		},
	}
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapterWithOptions([]LLM{primary, fallback}, FallbackAdapterOptions{
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
	if got := chunk.Delta.Content; got != "fallback" {
		t.Fatalf("chunk content = %q, want fallback", got)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}
	if len(primary.options) != 1 {
		t.Fatalf("primary options = %d, want 1", len(primary.options))
	}
	connectOptions := primary.options[0].ConnectOptions
	if connectOptions == nil {
		t.Fatal("provider ConnectOptions = nil, want fallback attempt options")
	}
	if connectOptions.RetryInterval != 25*time.Millisecond {
		t.Fatalf("RetryInterval = %v, want fallback retry interval", connectOptions.RetryInterval)
	}
}

func TestFallbackAdapterDefaultsRetryInterval(t *testing.T) {
	primary := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "ok"}}},
	}}}
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
	if got := chunk.Delta.Content; got != "ok" {
		t.Fatalf("chunk content = %q, want ok", got)
	}
	if len(primary.options) != 1 {
		t.Fatalf("primary options = %d, want 1", len(primary.options))
	}
	connectOptions := primary.options[0].ConnectOptions
	if connectOptions == nil {
		t.Fatal("provider ConnectOptions = nil, want fallback attempt options")
	}
	if connectOptions.RetryInterval != 500*time.Millisecond {
		t.Fatalf("RetryInterval = %v, want default 500ms", connectOptions.RetryInterval)
	}
}

func TestFallbackAdapterPassesAttemptConnectOptionsToProvider(t *testing.T) {
	primary := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "ok"}}},
	}}}
	adapter := NewFallbackAdapterWithOptions([]LLM{primary}, FallbackAdapterOptions{
		AttemptTimeout: 75 * time.Millisecond,
		MaxRetryPerLLM: 2,
		RetryInterval:  25 * time.Millisecond,
	})

	stream, err := adapter.Chat(
		context.Background(),
		NewChatContext(),
		WithConnectOptions(APIConnectOptions{
			MaxRetry:      7,
			RetryInterval: time.Second,
			Timeout:       3 * time.Second,
		}),
	)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	defer stream.Close()

	if len(primary.options) != 1 {
		t.Fatalf("captured options = %d, want 1", len(primary.options))
	}
	connectOptions := primary.options[0].ConnectOptions
	if connectOptions == nil {
		t.Fatal("provider ConnectOptions = nil, want fallback attempt options")
	}
	if connectOptions.MaxRetry != 2 {
		t.Fatalf("MaxRetry = %d, want fallback max retry per LLM", connectOptions.MaxRetry)
	}
	if connectOptions.RetryInterval != 25*time.Millisecond {
		t.Fatalf("RetryInterval = %v, want fallback retry interval", connectOptions.RetryInterval)
	}
	if connectOptions.Timeout != 75*time.Millisecond {
		t.Fatalf("Timeout = %v, want fallback attempt timeout", connectOptions.Timeout)
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

func TestFallbackAdapterTreatsClientClosedStatusAsCleanEOF(t *testing.T) {
	second := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
			{err: NewAPIStatusError("client closed", 499, "req_123", nil)},
		}}},
		second,
	})

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF for 499 client closed", err)
	}
	if second.calls != 0 {
		t.Fatalf("fallback LLM calls = %d, want 0 for client closed", second.calls)
	}
}

func TestFallbackAdapterReturnsAllFailedErrorWhenProvidersExhausted(t *testing.T) {
	firstErr := errors.New("primary unavailable")
	secondErr := errors.New("fallback unavailable")
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{label: "primary.LLM", err: firstErr},
		&fakeFallbackLLM{label: "fallback.LLM", err: secondErr},
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
	var allFailed *FallbackAllFailedError
	if !errors.As(err, &allFailed) {
		t.Fatalf("Chat error type = %T, want FallbackAllFailedError", err)
	}
	var connectionErr *APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Chat error type = %T, want APIConnectionError", err)
	}
	if !connectionErr.Retryable {
		t.Fatal("APIConnectionError retryable = false, want true")
	}
	if got, want := strings.Join(allFailed.Labels, ","), "primary.LLM,fallback.LLM"; got != want {
		t.Fatalf("FallbackAllFailedError.Labels = %q, want %q", got, want)
	}
	if !strings.Contains(err.Error(), "primary.LLM") || !strings.Contains(err.Error(), "fallback.LLM") {
		t.Fatalf("Chat error = %q, want exhausted provider labels", err)
	}
}

func TestFallbackAdapterFallsBackOnNonRetryableAPIErrorBeforeChunk(t *testing.T) {
	primaryErr := NewAPIStatusError("bad request", 400, "req_123", nil)
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{err: primaryErr},
		fallback,
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
		t.Fatalf("chunk content = %q, want fallback after non-retryable API error", got)
	}
	if fallback.calls != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallback.calls)
	}
}

func TestFallbackAdapterTreatsNilProviderStreamAsFailure(t *testing.T) {
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{},
		fallback,
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

func TestFallbackAdapterTreatsTypedNilProviderStreamAsFailure(t *testing.T) {
	var typedNil *fakeFallbackStream
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{
		&fakeFallbackLLM{stream: typedNil},
		fallback,
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

func TestFallbackAdapterCallsAvailabilityChangedHandlers(t *testing.T) {
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
	changes := make(chan FallbackAvailabilityChangedEvent, 2)
	adapter.OnAvailabilityChanged(func(event FallbackAvailabilityChangedEvent) {
		changes <- event
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
		t.Fatalf("fallback content = %q, want fallback", got)
	}

	unavailable := receiveFallbackAvailabilityChange(t, changes)
	if unavailable.LLM != primary || unavailable.Index != 0 || unavailable.Available {
		t.Fatalf("handler event = %#v, want primary unavailable", unavailable)
	}

	waitForFallbackCalls(t, primary, 2)
	recovered := receiveFallbackAvailabilityChange(t, changes)
	if recovered.LLM != primary || recovered.Index != 0 || !recovered.Available {
		t.Fatalf("handler event = %#v, want primary recovered", recovered)
	}
}

func TestFallbackAdapterAvailabilityHandlerPanicDoesNotBlockOtherHandlers(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}}}
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{primary, fallback})
	changes := make(chan FallbackAvailabilityChangedEvent, 1)
	adapter.OnAvailabilityChanged(func(FallbackAvailabilityChangedEvent) {
		panic("availability handler failed")
	})
	adapter.OnAvailabilityChanged(func(event FallbackAvailabilityChangedEvent) {
		changes <- event
	})

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("availability handler panic escaped: %v", recovered)
			}
		}()

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
	}()

	unavailable := receiveFallbackAvailabilityChange(t, changes)
	if unavailable.LLM != primary || unavailable.Index != 0 || unavailable.Available {
		t.Fatalf("handler event = %#v, want primary unavailable", unavailable)
	}
}

func TestFallbackAdapterCanUnsubscribeAvailabilityChangedHandler(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}}}
	fallback := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "fallback"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{primary, fallback})
	changes := make(chan FallbackAvailabilityChangedEvent, 1)
	unsubscribe := adapter.OnAvailabilityChanged(func(event FallbackAvailabilityChangedEvent) {
		changes <- event
	})
	unsubscribe()
	unsubscribe()

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	assertNoFallbackAvailabilityChange(t, changes)
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

func TestFallbackAdapterAllUnavailableMainSuccessDoesNotEmitRecovered(t *testing.T) {
	primary := &fakeFallbackLLM{stream: &fakeFallbackStream{events: []fakeFallbackEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "primary active"}}},
	}}}
	adapter := NewFallbackAdapter([]LLM{primary})
	adapter.mu.Lock()
	adapter.available[0] = false
	adapter.mu.Unlock()

	stream, err := adapter.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Delta.Content; got != "primary active" {
		t.Fatalf("chunk content = %q, want primary active", got)
	}
	assertNoFallbackAvailabilityEvent(t, adapter)
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

func TestFallbackAdapterRetriesRecoveryAfterFailedProbeOnLaterChat(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	recoveryErr := errors.New("recovery probe failed")
	primary := &fakeFallbackLLM{streams: []LLMStream{
		&fakeFallbackStream{events: []fakeFallbackEvent{{err: firstErr}}},
		&fakeFallbackStream{events: []fakeFallbackEvent{{err: recoveryErr}}},
		&fakeFallbackStream{events: []fakeFallbackEvent{
			{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "second recovery probe"}}},
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
		t.Fatalf("fallback content = %q, want fallback first", got)
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
	if got := chunk.Delta.Content; got != "fallback second" {
		t.Fatalf("second fallback content = %q, want fallback second", got)
	}
	waitForFallbackCalls(t, primary, 3)
}

type fakeFallbackLLM struct {
	MetricsEmitter
	ErrorEmitter

	streams []LLMStream
	stream  LLMStream
	err     error
	label   string
	calls   int
	onChat  func(context.Context)
	options []ChatOptions
}

func (f *fakeFallbackLLM) Chat(ctx context.Context, _ *ChatContext, opts ...ChatOption) (LLMStream, error) {
	f.calls++
	var options ChatOptions
	for _, opt := range opts {
		opt(&options)
	}
	f.options = append(f.options, options)
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
	events  []fakeFallbackEvent
	chatCtx *ChatContext
	index   int
	closed  bool
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

func (f *fakeFallbackStream) ChatCtx() *ChatContext {
	return f.chatCtx
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

func receiveFallbackAvailabilityChange(t *testing.T, changes <-chan FallbackAvailabilityChangedEvent) FallbackAvailabilityChangedEvent {
	t.Helper()
	select {
	case event := <-changes:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fallback availability handler")
	}
	return FallbackAvailabilityChangedEvent{}
}

func assertNoFallbackAvailabilityChange(t *testing.T, changes <-chan FallbackAvailabilityChangedEvent) {
	t.Helper()
	select {
	case event := <-changes:
		t.Fatalf("received unexpected fallback availability handler event: %#v", event)
	case <-time.After(25 * time.Millisecond):
	}
}

func assertNoFallbackAvailabilityEvent(t *testing.T, adapter *FallbackAdapter) {
	t.Helper()
	select {
	case event := <-adapter.AvailabilityChangedCh():
		t.Fatalf("received unexpected fallback availability event: %#v", event)
	case <-time.After(25 * time.Millisecond):
	}
}
