package tokenize

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestBufferedTokenStreamClosedReflectsLifecycle(t *testing.T) {
	stream := NewBufferedTokenStream(strings.Fields, 1, 1)
	if stream.Closed() {
		t.Fatal("Closed() = true before close, want false")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !stream.Closed() {
		t.Fatal("Closed() = false after close, want true")
	}
}

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

func TestBufferedSentenceStreamMultilineNumberedListDoesNotDumpGiantToken(t *testing.T) {
	text := "Aku bisa membantu banyak hal, antara lain:\n\n" +
		"1. Menjawab pertanyaan  \n   seputar pengetahuan umum dan teknologi.\n\n" +
		"2. Membantu tugas tulis  \n   seperti rangkuman dan penjelasan materi.\n\n" +
		"3. Belajar bahasa  \n   seperti menerjemahkan dan memperbaiki grammar.\n\n" +
		"Kamu mau aku bantu apa hari ini?"

	stream := NewBasicSentenceTokenizer().Stream("en")
	if err := stream.PushText(text); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}

	var tokens []string
	for {
		tok, err := stream.Next()
		if err != nil {
			break
		}
		tokens = append(tokens, tok.Token)
	}

	if len(tokens) < 4 {
		t.Fatalf("got %d tokens, want at least one per sentence; tokens=%#v", len(tokens), tokens)
	}

	for _, tok := range tokens {
		if strings.Contains(tok, "1.") && strings.Contains(tok, "3.") {
			t.Fatalf("token spans multiple list items (giant-token bug): %q", tok)
		}
		if len([]rune(tok)) > 200 {
			t.Fatalf("token is unexpectedly large (%d runes): %q", len([]rune(tok)), tok)
		}
	}

	joined := strings.Join(tokens, "\x00")
	if got := strings.Count(joined, "Menjawab pertanyaan"); got != 1 {
		t.Fatalf("first list item appears %d times across tokens, want exactly 1; tokens=%#v", got, tokens)
	}
}

func TestBufferedTokenStreamTrimsReferenceWhitespaceContext(t *testing.T) {
	stream := NewBufferedTokenStream(func(text string) []string {
		if strings.HasPrefix(text, "\t") {
			return []string{"\t", "two"}
		}
		return strings.Fields(text)
	}, 1, 1)

	if err := stream.PushText("one\t two"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("Next first returned error: %v", err)
	}
	if first.Token != "one" {
		t.Fatalf("first token = %q, want one", first.Token)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("Next second returned error: %v", err)
	}
	if second.Token != "two" {
		t.Fatalf("second token = %q, want two", second.Token)
	}
	select {
	case token := <-stream.eventCh:
		t.Fatalf("unexpected token after whitespace trim: %q", token.Token)
	default:
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

func TestBufferedTokenStreamEndInputRejectsClosedStream(t *testing.T) {
	stream := NewBufferedTokenStream(strings.Fields, 1, 1)
	if err := stream.EndInput(); err != nil {
		t.Fatalf("first EndInput returned error: %v", err)
	}

	if err := stream.EndInput(); err == nil {
		t.Fatal("second EndInput error = nil, want closed stream error")
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

func TestBufferedTokenStreamPushUnblocksOnClose(t *testing.T) {
	stream := NewBufferedTokenStream(strings.Fields, 1, 1)

	text := strings.TrimSpace(strings.Repeat("word ", 150))

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- stream.PushText(text)
	}()

	time.Sleep(100 * time.Millisecond)
	select {
	case <-pushDone:
		t.Fatal("PushText did not block on the full event channel; test cannot prove the fix")
	default:
	}

	if err := stream.AClose(); err != nil {
		t.Fatalf("AClose returned error: %v", err)
	}

	select {
	case err := <-pushDone:
		if err != nil {
			t.Fatalf("PushText returned error after AClose: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PushText stayed blocked after AClose — signalAbort did not unblock the send")
	}
}
