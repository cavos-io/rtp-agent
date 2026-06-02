package tokenize

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestBufferedTokenStreamCloseFlushesWithoutDeadlock(t *testing.T) {
	stream := NewBufferedTokenStream(func(text string) []string {
		return []string{text}
	}, 1, 1)

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- stream.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Close did not return")
	}

	token, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if token.Token != "hello" {
		t.Fatalf("token = %q, want hello", token.Token)
	}
}

func TestBufferedTokenStreamNextReturnsIOEOFWhenClosed(t *testing.T) {
	stream := NewBufferedTokenStream(func(text string) []string {
		return nil
	}, 1, 1)
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	_, err := stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}

func TestBufferedTokenStreamKeepsLastTokenAsContext(t *testing.T) {
	stream := NewBufferedTokenStream(strings.Fields, 1, 1)

	if err := stream.PushText("one two three"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error for first token: %v", err)
	}
	if first.Token != "one" {
		t.Fatalf("first token = %q, want one", first.Token)
	}

	second, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error for second token: %v", err)
	}
	if second.Token != "two" {
		t.Fatalf("second token = %q, want two", second.Token)
	}

	select {
	case token := <-stream.eventCh:
		t.Fatalf("unexpected buffered token before flush: %q", token.Token)
	default:
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	third, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error for flushed token: %v", err)
	}
	if third.Token != "three" {
		t.Fatalf("third token = %q, want three", third.Token)
	}
}

func TestBufferedTokenStreamEndInputFlushesAndCloses(t *testing.T) {
	stream := NewBufferedTokenStream(func(text string) []string {
		return []string{text}
	}, 1, 10)

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}

	token, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if token.Token != "hello" {
		t.Fatalf("token = %q, want hello", token.Token)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}

func TestBufferedTokenStreamACloseDoesNotFlush(t *testing.T) {
	stream := NewBufferedTokenStream(func(text string) []string {
		return []string{text}
	}, 1, 10)

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.AClose(); err != nil {
		t.Fatalf("AClose returned error: %v", err)
	}

	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}
