package images

import (
	"strings"
	"testing"
)

func TestEncodeRejectsUnknownResizeStrategy(t *testing.T) {
	frame := &VideoFrame{
		Width:  1,
		Height: 1,
		Format: "rgba",
		Data:   []byte{255, 0, 0, 255},
	}

	_, err := Encode(frame, EncodeOptions{
		Format:   "png",
		Width:    2,
		Height:   2,
		Strategy: "unknown",
	})
	if err == nil {
		t.Fatal("Encode returned nil error, want unknown strategy error")
	}
	if !strings.Contains(err.Error(), "unknown resize strategy") {
		t.Fatalf("Encode error = %v, want unknown resize strategy", err)
	}
}

func TestNewEncodeOptionsMatchesLiveKitDefaults(t *testing.T) {
	opts := NewEncodeOptions()

	if opts.Format != "jpeg" {
		t.Fatalf("Format = %q, want jpeg", opts.Format)
	}
	if opts.Quality != 75 {
		t.Fatalf("Quality = %d, want 75", opts.Quality)
	}
	if opts.Strategy != "scale_aspect_fit" {
		t.Fatalf("Strategy = %q, want scale_aspect_fit", opts.Strategy)
	}
}
