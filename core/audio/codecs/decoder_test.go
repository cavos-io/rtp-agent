package codecs

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
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

func TestFFmpegAudioStreamDecoderDecodesAAC(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(aacADTSFixtureBase64)
	if err != nil {
		t.Fatalf("DecodeString fixture error = %v", err)
	}
	decoder := NewAACAudioStreamDecoder()
	decoder.Push(data[:len(data)/2])
	decoder.Push(data[len(data)/2:])
	decoder.EndInput()

	frame := nextDecodedAudioFrame(t, decoder)
	if frame.SampleRate != 24000 || frame.NumChannels != 1 {
		t.Fatalf("decoded AAC format = %d Hz/%d channels, want 24000 Hz mono", frame.SampleRate, frame.NumChannels)
	}
	if len(frame.Data) == 0 {
		t.Fatal("decoded AAC frame data is empty")
	}
	if strings.HasPrefix(string(frame.Data), string(data[:min(len(data), len(frame.Data))])) {
		t.Fatal("decoded AAC frame still contains compressed bytes")
	}
}

func TestFFmpegAudioStreamDecoderDecodesFLAC(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(flacFixtureBase64)
	if err != nil {
		t.Fatalf("DecodeString fixture error = %v", err)
	}
	decoder := NewFLACAudioStreamDecoder()
	decoder.Push(data[:len(data)/2])
	decoder.Push(data[len(data)/2:])
	decoder.EndInput()

	frame := nextDecodedAudioFrame(t, decoder)
	if frame.SampleRate != 24000 || frame.NumChannels != 1 {
		t.Fatalf("decoded FLAC format = %d Hz/%d channels, want 24000 Hz mono", frame.SampleRate, frame.NumChannels)
	}
	if len(frame.Data) == 0 {
		t.Fatal("decoded FLAC frame data is empty")
	}
	if strings.HasPrefix(string(frame.Data), string(data[:min(len(data), len(frame.Data))])) {
		t.Fatal("decoded FLAC frame still contains compressed bytes")
	}
}

func nextDecodedAudioFrame(t *testing.T, decoder AudioStreamDecoder) *model.AudioFrame {
	t.Helper()
	for {
		frame, err := decoder.Next()
		if err != nil {
			if strings.Contains(err.Error(), "decoder closed") {
				t.Fatal("decoder closed before producing audio")
			}
			t.Fatalf("Next() error = %v", err)
		}
		if frame != nil {
			return frame
		}
	}
}

const opusOggFixtureBase64 = "T2dnUwACAAAAAAAAAACXynBsAAAAAMy/Wi4BE09wdXNIZWFkAQE4AYC7AAAAAABPZ2dTAAAAAAAAAAAAAJfKcGwBAAAAYQP1NwE+T3B1c1RhZ3MNAAAATGF2ZjU5LjI3LjEwMAEAAAAdAAAAZW5jb2Rlcj1MYXZjNTkuMzcuMTAwIGxpYm9wdXNPZ2dTAAT4BAAAAAAAAJfKcGwCAAAAdYmr1AIDA/j//vj//g=="

const aacADTSFixtureBase64 = "//FYQCW//N4CAExhdmM1OS4zNy4xMDAAAkivW6qEHV2Era+88Zx+Lmqu6laZJJuSSdvOREkl//+xxdxr2VxbpLZtPUzbWI83eI1VlnJXPMf/z/t8jbtgVAi7i5pzVxrsrPuOsRwqmaa771bhdqxuKsNiynHYnHbTt2U5VYbFWZ7G2Kw1qNjo1+sOOsNirNirMdWY6NjlKpSqUqlKpSqUqlKpSqUqlKpSqUqlKpSqUqlKpSqUriSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSiSnBRJRJRLklyS5JKcFRRRRRRRRZ2hVDGydCyDBt6FdMLIULJr87Nyau3n5NeSIoiFRIosqnJDFdorqF/4/fvbfuXtv3LrXW2XaSo3JcM4P/xWEAv//wBTJ7Zu9tyGlF3I4RCrddMhL+P/4fGS7vTS1evPz8/v1AXq76/t/T/cXq7ulX+f9f9y7u7uKACEPCTwwZ2dnZ2dnYWdn79ra1ShMEvRxEo87yHMSqOqu7MBm3hsjdUTH5leOGyNhDr2FVOxysSf22tuaTBdtNotAHOZ+jamA1fKflIMDO4SEgwM7hISDAwM7uEhISDAwM7uEhIT/P3b94fy/lst7vcB/L1fyQ9pfVxGvDOIiNxJ3CuOGJu39ZDoCoTgRoMSkxIvLOB94REf6sQGPzuWoMsh3Dc1yRPaV68HbXFPnZuTVIJ20WpqkE50XJyqmqQTgTtps6bU1VTSghgXOmziamqo1UC4FzprTNTVUaVS0palqhnTZzJTP/G/q3ya7du6ZCQnUwMDA1269oSEhIT6PPpuNezge2oTGWYIvlErXOCc2FMRFBf133vVsTPYmess9Oz062nZ5q2nWzVtOyTVs1VNVTU0xNYY4YyUuzs7Ozhg1QO//FYQCV//AEin7bUJIPF/kPy/p/3tq5F3d3/29/v99LVABSncK/EKYrRESC9SpDC4T6d7Tx4FvvUy7s3w+7N8PuzfD7t2ab4eHzRf4/wHx0gf406QP8adIB8fl/jSAf4/x/j4gB/j/HxiuLOwQAAcc8gAB/V+ogAARhvItaAABGRFIw4YAAEYLyLnAAARixCMOEAABGDAIveRc0i1gAAAAARQgic5ExiJigAAAABFByJzkTGImMAAAAAESlIlKRIQiQhEYyIxgAAAAAAAERjIiEREIiIREQSIQgAAAAAAAEQhIhCRCAiABEACIAEQAIgAAAAAAAAAAABEAP/3/9//f/3/9//f/0AAAAAAAAAAP/3/9//f/3/9//f/3/9//f/0AAAAAAAAAAAAA4="

const flacFixtureBase64 = "ZkxhQwAAACIJAAkAAAFxAAFxBdwA8AAAA8DrQTcn425MUFIsj3pbRk7HhAAALg0AAABMYXZmNTkuMjcuMTAwAQAAABUAAABlbmNvZGVyPUxhdmY1OS4yNy4xMDD/+HcIAAO/OEIASwHH5r+TwAAqAAFAmabaVU3y9oypCEoUjaNIUjKQJCIFT08KaFJKEIgGGTMkyZkyUOcMpLCphEnDIhkzAKGTkNDCmQIJOBTh+SZwiQLAiHDhQhJQmcLDJz0KZQNJSckoFkKQlDmEJJhSUJSkp5cshJKSgZJyFDmThQhKEQ8OFKFJTKUMhmEySGQiBkycKTQMyJDykpgRmTJQgYYcOBlCFDkmc4GETJScoSyTCoZIEkk0JECk5KZNDkEmhyZoFQMoFCTMIGSUChTKGgXOUJYFClIaSFJhkkwkyQCzJQqTwsmcLJBJmYczMMoQ4cwIXKaeU5zCwlAIRAzMmQ5mHDSUgaUlCJlCISIQsyUDDDJmYUKHChECwKcPQwoU8JEOEpJwIJkw55DOU5TKBShzIgEQMocMkpIUCFDJKTzKcvlISZwyScKGGhSScCaSgXIWZlC8mTqYrIqoyeioyKjKtCSixgeAQAAkK1k="
