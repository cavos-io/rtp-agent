package tokenize

import (
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
