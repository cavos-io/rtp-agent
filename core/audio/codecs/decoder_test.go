package codecs

import (
	"encoding/base64"
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

func TestPCMAudioStreamDecoderDropsIncompleteFinalSample(t *testing.T) {
	decoder := NewPCMAudioStreamDecoder(48000, 1)
	decoder.Push([]byte{1, 2, 3})
	decoder.EndInput()

	errCh := make(chan error, 1)
	go func() {
		frame, err := decoder.Next()
		if frame != nil {
			errCh <- nil
			return
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "decoder closed") {
			t.Fatalf("Next() error = %v, want decoder closed without malformed partial-sample frame", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() blocked, want decoder closed after dropping incomplete final sample")
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

func TestOpusAudioStreamDecoderDecodesOggOpus(t *testing.T) {
	opusData, err := base64.StdEncoding.DecodeString(opusOggFixtureBase64)
	if err != nil {
		t.Fatalf("DecodeString fixture error = %v", err)
	}
	decoder := NewOpusAudioStreamDecoder(48000, 1)
	decoder.Push(opusData)
	decoder.EndInput()

	var decodedFrames int
	for {
		frame, err := decoder.Next()
		if err != nil {
			if strings.Contains(err.Error(), "decoder closed") {
				break
			}
			t.Fatalf("Next() error = %v", err)
		}
		if frame == nil {
			t.Fatal("Next() frame = nil")
		}
		if frame.SampleRate != 48000 || frame.NumChannels != 1 {
			t.Fatalf("decoded frame format = %d Hz/%d channels, want 48000 Hz mono", frame.SampleRate, frame.NumChannels)
		}
		if len(frame.Data) == 0 {
			t.Fatal("decoded Opus frame data is empty")
		}
		if got, want := len(frame.Data), int(frame.SamplesPerChannel*frame.NumChannels*2); got != want {
			t.Fatalf("frame byte length = %d, want %d from samples/channels", got, want)
		}
		decodedFrames++
	}
	if decodedFrames == 0 {
		t.Fatal("decoded frame count = 0, want Opus audio")
	}
}

func TestOpusAudioStreamDecoderEmitsBeforeEndInput(t *testing.T) {
	opusData, err := base64.StdEncoding.DecodeString(opusOggFixtureBase64)
	if err != nil {
		t.Fatalf("DecodeString fixture error = %v", err)
	}
	decoder := NewOpusAudioStreamDecoder(48000, 1)
	decoder.Push(opusData)
	defer decoder.Close()

	frameCh := make(chan struct {
		frame any
		err   error
	}, 1)
	go func() {
		frame, err := decoder.Next()
		frameCh <- struct {
			frame any
			err   error
		}{frame: frame, err: err}
	}()

	select {
	case result := <-frameCh:
		if result.err != nil {
			t.Fatalf("Next() error = %v", result.err)
		}
		if result.frame == nil {
			t.Fatal("Next() frame = nil")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() blocked before EndInput, want streaming Opus frame")
	}
}

const opusOggFixtureBase64 = "T2dnUwACAAAAAAAAAACXynBsAAAAAMy/Wi4BE09wdXNIZWFkAQE4AYC7AAAAAABPZ2dTAAAAAAAAAAAAAJfKcGwBAAAAYQP1NwE+T3B1c1RhZ3MNAAAATGF2ZjU5LjI3LjEwMAEAAAAdAAAAZW5jb2Rlcj1MYXZjNTkuMzcuMTAwIGxpYm9wdXNPZ2dTAAT4BAAAAAAAAJfKcGwCAAAAdYmr1AIDA/j//vj//g=="
