package codecs

import (
	"strings"
	"testing"
	"time"
)

func TestPCMAudioStreamDecoderReturnsClosedAfterDrain(t *testing.T) {
	decoder := NewPCMAudioStreamDecoder(48000, 1)
	decoder.Push(make([]byte, 960*2))
	decoder.EndInput()

	frame, err := decoder.Next()
	if err != nil {
		t.Fatalf("Next() first error = %v", err)
	}
	if frame.SamplesPerChannel != 960 {
		t.Fatalf("SamplesPerChannel = %d, want 960", frame.SamplesPerChannel)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := decoder.Next()
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "decoder closed") {
			t.Fatalf("Next() after drain error = %v, want decoder closed", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() after drain blocked, want decoder closed error")
	}
}

func TestPCMAudioStreamDecoderCloseIsIdempotent(t *testing.T) {
	decoder := NewPCMAudioStreamDecoder(48000, 1)

	if err := decoder.Close(); err != nil {
		t.Fatalf("Close() first error = %v", err)
	}
	if err := decoder.Close(); err != nil {
		t.Fatalf("Close() second error = %v", err)
	}
}
