package images

import (
	"bytes"
	"image/png"
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

	if opts.Format != "JPEG" {
		t.Fatalf("Format = %q, want JPEG", opts.Format)
	}
	if opts.Quality != 75 {
		t.Fatalf("Quality = %d, want 75", opts.Quality)
	}
	if opts.Strategy != "scale_aspect_fit" {
		t.Fatalf("Strategy = %q, want scale_aspect_fit", opts.Strategy)
	}
}

func TestEncodeAcceptsReferenceFormatNames(t *testing.T) {
	frame := &VideoFrame{
		Width:  1,
		Height: 1,
		Format: "rgba",
		Data:   []byte{255, 0, 0, 255},
	}

	for _, format := range []string{"JPEG", "PNG"} {
		t.Run(format, func(t *testing.T) {
			encoded, err := Encode(frame, EncodeOptions{
				Format:  format,
				Quality: 75,
			})
			if err != nil {
				t.Fatalf("Encode returned error for reference format %q: %v", format, err)
			}
			if len(encoded) == 0 {
				t.Fatalf("Encode returned empty bytes for reference format %q", format)
			}
		})
	}
}

func TestEncodePNGDiscardsAlphaLikeReferenceRGBConversion(t *testing.T) {
	frame := &VideoFrame{
		Width:  1,
		Height: 1,
		Format: "rgba",
		Data:   []byte{255, 0, 0, 0},
	}

	encoded, err := Encode(frame, EncodeOptions{Format: "PNG"})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	img, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("Decode PNG error = %v", err)
	}
	r, g, b, a := img.At(0, 0).RGBA()
	if r != 0xffff || g != 0 || b != 0 || a != 0xffff {
		t.Fatalf("decoded RGBA = (%#x, %#x, %#x, %#x), want opaque red", r, g, b, a)
	}
}
