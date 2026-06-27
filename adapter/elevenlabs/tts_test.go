package elevenlabs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestElevenLabsTTSDefaultsMatchReference(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	if provider.voiceID != "hpp4J3VqNfWAUOO0d1Us" {
		t.Fatalf("voiceID = %q, want reference default", provider.voiceID)
	}
	if provider.modelID != "eleven_turbo_v2_5" {
		t.Fatalf("modelID = %q, want eleven_turbo_v2_5", provider.modelID)
	}
	if provider.encoding != "mp3_22050_32" {
		t.Fatalf("encoding = %q, want mp3_22050_32", provider.encoding)
	}
	if provider.SampleRate() != 22050 {
		t.Fatalf("sample rate = %d, want 22050", provider.SampleRate())
	}
	if got := tts.Model(provider); got != "eleven_turbo_v2_5" {
		t.Fatalf("model metadata = %q, want eleven_turbo_v2_5", got)
	}
	if got := tts.Provider(provider); got != "ElevenLabs" {
		t.Fatalf("provider metadata = %q, want ElevenLabs", got)
	}
}

func TestNewElevenLabsTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "env-key")
	t.Setenv("ELEVEN_API_KEY", "fallback-env-key")

	provider, err := NewElevenLabsTTS("", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want primary env key", provider.apiKey)
	}

	explicit, err := NewElevenLabsTTS("explicit-key", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewElevenLabsTTSUsesFallbackEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "")
	t.Setenv("ELEVEN_API_KEY", "fallback-env-key")

	provider, err := NewElevenLabsTTS("", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	if provider.apiKey != "fallback-env-key" {
		t.Fatalf("api key = %q, want fallback env key", provider.apiKey)
	}
}

func TestElevenLabsTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "")
	t.Setenv("ELEVEN_API_KEY", "")
	provider, err := NewElevenLabsTTS("", "", "", WithElevenLabsBaseURL("://bad-url"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	_, err = provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "ELEVEN_API_KEY") {
		t.Fatalf("Synthesize error = %q, want ELEVEN_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "ELEVEN_API_KEY") {
		t.Fatalf("Stream error = %q, want ELEVEN_API_KEY guidance", err)
	}
}

func TestElevenLabsSynthesizeRequestUsesReferenceOptions(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "",
		WithElevenLabsLanguage("en"),
		WithElevenLabsEnableSSMLParsing(true),
		WithElevenLabsStreamingLatency(3),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	requestURL, body := buildElevenLabsSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if parsed.Path != "/v1/text-to-speech/hpp4J3VqNfWAUOO0d1Us/stream" {
		t.Fatalf("path = %q, want default voice stream path", parsed.Path)
	}
	if parsed.Query().Get("model_id") != "eleven_turbo_v2_5" {
		t.Fatalf("model_id = %q, want eleven_turbo_v2_5", parsed.Query().Get("model_id"))
	}
	if parsed.Query().Get("output_format") != "mp3_22050_32" {
		t.Fatalf("output_format = %q, want mp3_22050_32", parsed.Query().Get("output_format"))
	}
	if parsed.Query().Get("optimize_streaming_latency") != "3" {
		t.Fatalf("optimize_streaming_latency = %q, want 3", parsed.Query().Get("optimize_streaming_latency"))
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["text"] != "hello" {
		t.Fatalf("text = %#v, want hello", payload["text"])
	}
	if payload["model_id"] != "eleven_turbo_v2_5" {
		t.Fatalf("model_id = %#v, want eleven_turbo_v2_5", payload["model_id"])
	}
	if _, ok := payload["voice_settings"]; !ok {
		t.Fatalf("voice_settings missing from payload %#v, want explicit null/object field", payload)
	}
	if _, ok := payload["language_code"]; ok {
		t.Fatalf("language_code = %#v, want omitted for reference chunked synthesize request", payload["language_code"])
	}
	if _, ok := payload["enable_ssml_parsing"]; ok {
		t.Fatalf("enable_ssml_parsing = %#v, want omitted for reference chunked synthesize request", payload["enable_ssml_parsing"])
	}
	if _, ok := payload["generation_config"]; ok {
		t.Fatalf("generation_config = %#v, want omitted for reference chunked synthesize request", payload["generation_config"])
	}
}

func TestElevenLabsTTSVoiceSettingsMatchReference(t *testing.T) {
	style := 0.35
	speed := 1.05
	boost := true
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsVoiceSettings(ElevenLabsVoiceSettings{
			Stability:       0.7,
			SimilarityBoost: 0.8,
			Style:           &style,
			Speed:           &speed,
			UseSpeakerBoost: &boost,
		}),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	_, body := buildElevenLabsSynthesizeRequest(provider, "hello")
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	assertElevenLabsTTSVoiceSettings(t, payload["voice_settings"])

	init := elevenLabsInitPayload("ctx_test", elevenLabsVoiceSettingsPayload(provider.voiceSettings), nil, nil)
	assertElevenLabsTTSVoiceSettings(t, init["voice_settings"])
}

func TestElevenLabsSynthesizeRequestUsesConfiguredBaseURL(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1/"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	requestURL, _ := buildElevenLabsSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if parsed.Scheme != "https" || parsed.Host != "eleven.example" {
		t.Fatalf("url = %q, want configured host", requestURL)
	}
	if parsed.Path != "/v1/text-to-speech/voice-1/stream" {
		t.Fatalf("path = %q, want configured base URL with stream synthesize path", parsed.Path)
	}
}

func TestElevenLabsTTSListVoicesMatchesReference(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		if r.URL.String() != "https://eleven.example/v1/voices" {
			t.Fatalf("url = %q, want voices endpoint", r.URL.String())
		}
		if r.Header.Get("xi-api-key") != "test-key" {
			t.Fatalf("xi-api-key = %q, want API key", r.Header.Get("xi-api-key"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"voices":[{"voice_id":"voice-1","name":"Rachel","category":"premade"}]}`)),
			Request:    r,
		}, nil
	})}

	provider, err := NewElevenLabsTTS("test-key", "", "", WithElevenLabsBaseURL("https://eleven.example/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	voices, err := provider.ListVoices(context.Background())
	if err != nil {
		t.Fatalf("ListVoices() error = %v", err)
	}
	if len(voices) != 1 || voices[0].ID != "voice-1" || voices[0].Name != "Rachel" || voices[0].Category != "premade" {
		t.Fatalf("voices = %#v, want reference voice fields", voices)
	}
}

func TestElevenLabsTTSRejectsNonAudioResponse(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"not audio"}`)),
			Request:    r,
		}, nil
	})}

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize returned nil error, want non-audio response error")
	}
	if !strings.Contains(err.Error(), "non-audio") {
		t.Fatalf("Synthesize error = %q, want non-audio guidance", err)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Synthesize error = %T %v, want APIConnectionError", err, err)
	}
}

func TestElevenLabsTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"detail":"rate limited"}`)),
			Request:    r,
		}, nil
	})}

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if statusErr.Body != `{"detail":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestElevenLabsTTSSynthesizeReturnsAPITimeoutError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Synthesize error = %T %v, want APITimeoutError", err, err)
	}
}

func TestElevenLabsTTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial refused")
	})}

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Synthesize error = %T %v, want APIConnectionError", err, err)
	}
}

func TestElevenLabsTTSStreamReturnsAPITimeoutErrorOnDialFailure(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, context.DeadlineExceeded
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Stream error = %T %v, want APITimeoutError", err, err)
	}
}

func TestElevenLabsTTSStreamNextReturnsAPITimeoutErrorOnDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	stream := &elevenLabsStream{
		audio:  make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		ctx:    ctx,
		cancel: cancel,
	}

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestElevenLabsTTSStreamReturnsAPIConnectionErrorOnDialFailure(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial refused")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestElevenLabsTTSDecodesReferenceMP3Response(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/mpeg"}},
			Body:       io.NopCloser(bytes.NewReader(mp3Data)),
			Request:    r,
		}, nil
	})}

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 22050 {
		t.Fatalf("sample rate = %d, want configured mp3 rate 22050", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want reference mono output", audio.Frame.NumChannels)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	if got, want := len(audio.Frame.Data), int(audio.Frame.SamplesPerChannel*audio.Frame.NumChannels*2); got != want {
		t.Fatalf("frame byte length = %d, want %d from samples/channels", got, want)
	}
	if bytes.Equal(audio.Frame.Data, mp3Data[:len(audio.Frame.Data)]) {
		t.Fatal("frame data still contains compressed mp3 bytes")
	}
}

func TestElevenLabsTTSChunkedStreamEmitsReferenceMP3FinalMarker(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	stream := &elevenLabsChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(mp3Data))},
		encoding:   "mp3_22050_32",
		sampleRate: 22050,
	}
	defer stream.Close()

	frames := 0
	for range 5000 {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next returned error before final marker after %d frames: %v", frames, err)
		}
		if audio == nil {
			t.Fatalf("Next returned nil audio before final marker after %d frames", frames)
		}
		if audio.IsFinal {
			if frames == 0 {
				t.Fatal("final marker arrived before decoded audio")
			}
			if _, err := stream.Next(); err != io.EOF {
				t.Fatalf("Next after final marker err = %v, want EOF", err)
			}
			return
		}
		if len(audio.Frame.Data) == 0 {
			t.Fatalf("frame %d is empty", frames)
		}
		frames++
	}
	t.Fatalf("stream did not emit final marker after %d frames", frames)
}

func TestElevenLabsTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyMP3Audio(t *testing.T) {
	stream := &elevenLabsChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(nil))},
		encoding:   "mp3_22050_32",
		sampleRate: 22050,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v, want final marker", err)
	}
	if audio == nil {
		t.Fatal("first Next audio = nil, want final marker")
	}
	if !audio.IsFinal {
		t.Fatal("first Next IsFinal = false, want boundary-only final marker")
	}
	if audio.Frame != nil {
		t.Fatalf("final marker frame = %#v, want nil marker", audio.Frame)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestElevenLabsTTSChunkedMP3StreamsBeforeProviderEOF(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	body := newElevenLabsBlockingEOFBody(mp3Data)
	stream := &elevenLabsChunkedStream{
		resp:       &http.Response{Body: body},
		encoding:   "mp3_22050_32",
		sampleRate: 22050,
	}
	defer stream.Close()

	type nextResult struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	resultCh := make(chan nextResult, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- nextResult{audio: audio, err: err}
	}()

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("Next before provider EOF error = %v", result.err)
		}
		if result.audio == nil || result.audio.Frame == nil || len(result.audio.Frame.Data) == 0 {
			t.Fatalf("Next before provider EOF audio = %#v, want decoded frame", result.audio)
		}
		if result.audio.IsFinal {
			t.Fatal("Next before provider EOF IsFinal = true, want streaming audio frame")
		}
	case <-time.After(750 * time.Millisecond):
		body.Close()
		select {
		case result := <-resultCh:
			t.Fatalf("Next waited for provider EOF; after close got (%#v, %v)", result.audio, result.err)
		case <-time.After(2 * time.Second):
			t.Fatal("Next blocked waiting for provider EOF")
		}
	}
}

func TestElevenLabsTTSChunkedStreamEmitsReferencePCMFinalMarker(t *testing.T) {
	stream := &elevenLabsChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{1, 0, 2, 0}))},
		encoding:   "pcm_8000",
		sampleRate: 8000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("first Next audio = %#v, want PCM frame", audio)
	}
	if audio.IsFinal {
		t.Fatal("first PCM frame IsFinal = true, want separate final marker after provider EOF")
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil {
		t.Fatal("second Next audio = nil, want final marker")
	}
	if !final.IsFinal {
		t.Fatal("second Next IsFinal = false, want reference final marker")
	}
	if final.Frame != nil {
		t.Fatalf("final marker frame = %#v, want nil marker", final.Frame)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestElevenLabsTTSChunkedStreamDoesNotDuplicatePCMFinalMarker(t *testing.T) {
	stream := &elevenLabsChunkedStream{
		resp:       &http.Response{Body: &elevenLabsReadOnceEOFBody{data: []byte{1, 0, 2, 0}}},
		encoding:   "pcm_8000",
		sampleRate: 8000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("first Next audio = %#v, want final PCM frame", audio)
	}
	if !audio.IsFinal {
		t.Fatal("first Next IsFinal = false, want final PCM frame")
	}

	if next, err := stream.Next(); next != nil || err != io.EOF {
		t.Fatalf("second Next = (%#v, %v), want nil EOF", next, err)
	}
}

func TestElevenLabsTTSReadErrorIncludesProviderOperationContext(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       elevenLabsErrReader{err: io.ErrClosedPipe},
			Request:    r,
		}, nil
	})}

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_multilingual_v2",
		WithElevenLabsBaseURL("https://eleven.example/v1"),
		WithElevenLabsLanguage("id"),
		WithElevenLabsEncoding("pcm_8000"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "Halo, ada yang bisa saya bantu?")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next returned nil error, want wrapped closed-pipe read error")
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Next error = %v, want errors.Is io.ErrClosedPipe", err)
	}
	for _, want := range []string{"elevenlabs TTS", "chunked pcm response", "before audio bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Next error = %q, want context %q", err, want)
		}
	}
}

func TestElevenLabsTTSPCMReadDeadlineReturnsAPITimeoutError(t *testing.T) {
	stream := &elevenLabsChunkedStream{
		resp:       &http.Response{Body: elevenLabsErrReader{err: context.DeadlineExceeded}},
		encoding:   "pcm_8000",
		sampleRate: 8000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestElevenLabsTTSMP3ReadDeadlineReturnsAPITimeoutError(t *testing.T) {
	stream := &elevenLabsChunkedStream{
		resp:       &http.Response{Body: elevenLabsErrReader{err: context.DeadlineExceeded}},
		encoding:   "mp3_22050_32",
		sampleRate: 22050,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestElevenLabsTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &elevenLabsCloseCountBody{Reader: strings.NewReader("audio")}
	stream := &elevenLabsChunkedStream{
		resp: &http.Response{
			Body: body,
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close() calls = %d, want 1", body.closeCount)
	}
}

func TestElevenLabsStreamURLUsesReferenceOptions(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "",
		WithElevenLabsLanguage("en"),
		WithElevenLabsEnableSSMLParsing(true),
		WithElevenLabsAutoMode(true),
		WithElevenLabsApplyLanguageTextNormalization(true),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	streamURL := buildElevenLabsStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}

	if parsed.Path != "/v1/text-to-speech/hpp4J3VqNfWAUOO0d1Us/multi-stream-input" {
		t.Fatalf("path = %q, want default voice stream path", parsed.Path)
	}
	if parsed.Query().Get("model_id") != "eleven_turbo_v2_5" {
		t.Fatalf("model_id = %q, want eleven_turbo_v2_5", parsed.Query().Get("model_id"))
	}
	if parsed.Query().Get("output_format") != "mp3_22050_32" {
		t.Fatalf("output_format = %q, want mp3_22050_32", parsed.Query().Get("output_format"))
	}
	if parsed.Query().Get("language_code") != "en" {
		t.Fatalf("language_code = %q, want en", parsed.Query().Get("language_code"))
	}
	if parsed.Query().Get("enable_ssml_parsing") != "true" {
		t.Fatalf("enable_ssml_parsing = %q, want true", parsed.Query().Get("enable_ssml_parsing"))
	}
	if parsed.Query().Get("enable_logging") != "true" {
		t.Fatalf("enable_logging = %q, want true", parsed.Query().Get("enable_logging"))
	}
	if defaultElevenLabsInactivityTimeout != 180 {
		t.Fatalf("default inactivity timeout = %d, want reference 180", defaultElevenLabsInactivityTimeout)
	}
	if parsed.Query().Get("inactivity_timeout") != "180" {
		t.Fatalf("inactivity_timeout = %q, want 180", parsed.Query().Get("inactivity_timeout"))
	}
	if parsed.Query().Get("apply_text_normalization") != "auto" {
		t.Fatalf("apply_text_normalization = %q, want auto", parsed.Query().Get("apply_text_normalization"))
	}
	if parsed.Query().Get("sync_alignment") != "true" {
		t.Fatalf("sync_alignment = %q, want true", parsed.Query().Get("sync_alignment"))
	}
	if parsed.Query().Get("auto_mode") != "true" {
		t.Fatalf("auto_mode = %q, want true", parsed.Query().Get("auto_mode"))
	}
	if parsed.Query().Get("apply_language_text_normalization") != "true" {
		t.Fatalf("apply_language_text_normalization = %q, want true", parsed.Query().Get("apply_language_text_normalization"))
	}
}

func TestElevenLabsStreamURLUsesReferenceTextNormalizationOverride(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "", WithElevenLabsApplyTextNormalization("off"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	parsed, err := url.Parse(buildElevenLabsStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if parsed.Query().Get("apply_text_normalization") != "off" {
		t.Fatalf("apply_text_normalization = %q, want off", parsed.Query().Get("apply_text_normalization"))
	}
}

func TestElevenLabsStreamURLForwardsReferenceLanguageForConfiguredModel(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_multilingual_v2",
		WithElevenLabsLanguage("fr"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	parsed, err := url.Parse(buildElevenLabsStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if parsed.Query().Get("language_code") != "fr" {
		t.Fatalf("language_code = %q, want fr", parsed.Query().Get("language_code"))
	}
}

func TestElevenLabsStreamURLUsesReferenceSyncAlignmentOverride(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "", WithElevenLabsSyncAlignment(false))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	if provider.Capabilities().AlignedTranscript {
		t.Fatal("aligned transcript capability = true, want false when sync alignment is disabled")
	}
	parsed, err := url.Parse(buildElevenLabsStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if parsed.Query().Get("sync_alignment") != "" {
		t.Fatalf("sync_alignment = %q, want omitted when disabled", parsed.Query().Get("sync_alignment"))
	}
}

func TestElevenLabsStreamURLUsesReferenceInactivityTimeoutOverride(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "", WithElevenLabsInactivityTimeout(300))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	parsed, err := url.Parse(buildElevenLabsStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if parsed.Query().Get("inactivity_timeout") != "300" {
		t.Fatalf("inactivity_timeout = %q, want 300", parsed.Query().Get("inactivity_timeout"))
	}
}

func TestElevenLabsStreamURLUsesReferenceEnableLoggingOverride(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "", WithElevenLabsEnableLogging(false))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	parsed, err := url.Parse(buildElevenLabsStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if parsed.Query().Get("enable_logging") != "false" {
		t.Fatalf("enable_logging = %q, want false", parsed.Query().Get("enable_logging"))
	}
}

func TestElevenLabsTTSAutoModeDefaultMatchesReference(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	defaultURL := buildElevenLabsStreamURL(provider)
	defaultParsed, err := url.Parse(defaultURL)
	if err != nil {
		t.Fatalf("parse default stream url: %v", err)
	}
	if defaultParsed.Query().Get("auto_mode") != "true" {
		t.Fatalf("default auto_mode = %q, want true when chunk schedule is unset", defaultParsed.Query().Get("auto_mode"))
	}

	scheduled, err := NewElevenLabsTTS("test-key", "", "", WithElevenLabsChunkLengthSchedule([]int{120, 160, 250, 290}))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() with schedule error = %v", err)
	}
	scheduledURL := buildElevenLabsStreamURL(scheduled)
	scheduledParsed, err := url.Parse(scheduledURL)
	if err != nil {
		t.Fatalf("parse scheduled stream url: %v", err)
	}
	if scheduledParsed.Query().Get("auto_mode") != "false" {
		t.Fatalf("scheduled auto_mode = %q, want false when chunk schedule is set", scheduledParsed.Query().Get("auto_mode"))
	}

	explicit, err := NewElevenLabsTTS("test-key", "", "",
		WithElevenLabsChunkLengthSchedule([]int{120, 160}),
		WithElevenLabsAutoMode(true),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() with explicit auto mode error = %v", err)
	}
	explicitURL := buildElevenLabsStreamURL(explicit)
	explicitParsed, err := url.Parse(explicitURL)
	if err != nil {
		t.Fatalf("parse explicit stream url: %v", err)
	}
	if explicitParsed.Query().Get("auto_mode") != "true" {
		t.Fatalf("explicit auto_mode = %q, want explicit true to win", explicitParsed.Query().Get("auto_mode"))
	}
}

func TestElevenLabsStreamURLUsesConfiguredBaseURL(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1/"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	streamURL := buildElevenLabsStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}

	if parsed.Scheme != "wss" || parsed.Host != "eleven.example" {
		t.Fatalf("stream url = %q, want configured websocket host", streamURL)
	}
	if parsed.Path != "/v1/text-to-speech/voice-1/multi-stream-input" {
		t.Fatalf("path = %q, want configured base URL with stream path", parsed.Path)
	}
}

func TestElevenLabsTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	provider.UpdateOptions(
		WithElevenLabsVoiceID("voice-updated"),
		WithElevenLabsModel("eleven_multilingual_v2"),
		WithElevenLabsLanguage("id"),
	)

	requestURL, body := buildElevenLabsSynthesizeRequest(provider, "halo")
	parsedRequest, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse synthesize url: %v", err)
	}
	if parsedRequest.Path != "/v1/text-to-speech/voice-updated/stream" {
		t.Fatalf("synthesize path = %q, want updated voice", parsedRequest.Path)
	}
	if parsedRequest.Query().Get("model_id") != "eleven_multilingual_v2" {
		t.Fatalf("synthesize model_id = %q, want eleven_multilingual_v2", parsedRequest.Query().Get("model_id"))
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["model_id"] != "eleven_multilingual_v2" {
		t.Fatalf("payload = %#v, want updated model", payload)
	}
	if _, hasLang := payload["language_code"]; hasLang {
		t.Fatalf("payload = %#v, eleven_multilingual_v2 must not include language_code", payload)
	}

	streamURL := buildElevenLabsStreamURL(provider)
	parsedStream, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if parsedStream.Path != "/v1/text-to-speech/voice-updated/multi-stream-input" {
		t.Fatalf("stream path = %q, want updated voice", parsedStream.Path)
	}
	if parsedStream.Query().Get("model_id") != "eleven_multilingual_v2" {
		t.Fatalf("stream model_id = %q, want eleven_multilingual_v2", parsedStream.Query().Get("model_id"))
	}
	if parsedStream.Query().Get("language_code") != "id" {
		t.Fatalf("stream language_code = %q, want id", parsedStream.Query().Get("language_code"))
	}
	if got := provider.Model(); got != "eleven_multilingual_v2" {
		t.Fatalf("Model() = %q, want eleven_multilingual_v2", got)
	}
}

func TestElevenLabsStreamPayloadsUseReferenceContextProtocol(t *testing.T) {
	const contextID = "ctx_test"

	init := elevenLabsInitPayload(contextID, nil, nil, nil)
	if init["text"] != " " || init["context_id"] != contextID {
		t.Fatalf("init payload = %#v, want warmup text with context_id", init)
	}
	voiceSettings, ok := init["voice_settings"].(map[string]any)
	if !ok {
		t.Fatalf("init voice_settings = %#v, want empty settings object", init["voice_settings"])
	}
	if len(voiceSettings) != 0 {
		t.Fatalf("init voice_settings = %#v, want empty settings object", voiceSettings)
	}
	if _, ok := init["generation_config"]; ok {
		t.Fatalf("init payload = %#v, want no generation_config without configured schedule", init)
	}

	scheduledInit := elevenLabsInitPayload(contextID, nil, []int{80, 120, 200}, nil)
	generationConfig, ok := scheduledInit["generation_config"].(map[string]any)
	if !ok {
		t.Fatalf("scheduled init generation_config = %#v, want object", scheduledInit["generation_config"])
	}
	chunkSchedule, ok := generationConfig["chunk_length_schedule"].([]int)
	if !ok {
		t.Fatalf("scheduled init chunk_length_schedule = %#v, want []int", generationConfig["chunk_length_schedule"])
	}
	if !equalIntSlices(chunkSchedule, []int{80, 120, 200}) {
		t.Fatalf("scheduled init chunk_length_schedule = %#v, want [80 120 200]", chunkSchedule)
	}

	dictionaryInit := elevenLabsInitPayload(contextID, nil, nil, []ElevenLabsPronunciationDictionaryLocator{
		{PronunciationDictionaryID: "dict-1", VersionID: "version-1"},
	})
	locators, ok := dictionaryInit["pronunciation_dictionary_locators"].([]map[string]interface{})
	if !ok {
		t.Fatalf("dictionary init locators = %#v, want locator list", dictionaryInit["pronunciation_dictionary_locators"])
	}
	if len(locators) != 1 || locators[0]["pronunciation_dictionary_id"] != "dict-1" || locators[0]["version_id"] != "version-1" {
		t.Fatalf("dictionary init locators = %#v, want reference locator payload", locators)
	}

	text := elevenLabsTextPayload(contextID, "hello", false)
	if text["text"] != "hello " || text["context_id"] != contextID {
		t.Fatalf("text payload = %#v, want text with trailing space and context_id", text)
	}
	if _, ok := text["try_trigger_generation"]; ok {
		t.Fatalf("text payload = %#v, want no legacy try_trigger_generation flag", text)
	}

	flush := elevenLabsFlushPayload(contextID)
	if flush["text"] != "" {
		t.Fatalf("flush text = %#v, want empty end-of-input signal", flush["text"])
	}
	if flush["context_id"] != contextID || flush["flush"] != true {
		t.Fatalf("flush payload = %#v, want context_id and flush=true", flush)
	}

	closeContext := elevenLabsCloseContextPayload(contextID)
	if closeContext["context_id"] != contextID || closeContext["close_context"] != true {
		t.Fatalf("close payload = %#v, want context_id and close_context=true", closeContext)
	}
}

func TestElevenLabsTextPayloadAppendsReferenceTrailingSpace(t *testing.T) {
	payload := elevenLabsTextPayload("ctx_test", "hello", false)
	if payload["text"] != "hello " {
		t.Fatalf("text payload = %#v, want reference trailing space", payload)
	}
}

func TestElevenLabsTTSStreamStartsContextOnFirstText(t *testing.T) {
	messages := make(chan map[string]any, 4)
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSWebsocketServer(messages, serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
		WithElevenLabsChunkLengthSchedule([]int{80, 120, 200}),
		WithElevenLabsPronunciationDictionaries([]ElevenLabsPronunciationDictionaryLocator{
			{PronunciationDictionaryID: "dict-1", VersionID: "version-1"},
		}),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case msg := <-messages:
		t.Fatalf("Stream() sent websocket packet before first text: %#v", msg)
	case err := <-serverErr:
		t.Fatalf("test websocket server error: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}

	init := readElevenLabsTTSStreamMessage(t, messages)
	if init["text"] != " " {
		t.Fatalf("init text = %#v, want warmup space in %#v", init["text"], init)
	}
	contextID, _ := init["context_id"].(string)
	if contextID == "" {
		t.Fatalf("init context_id = %#v, want non-empty string", init["context_id"])
	}
	if _, ok := init["voice_settings"].(map[string]any); !ok {
		t.Fatalf("init voice_settings = %#v, want object", init["voice_settings"])
	}
	generationConfig, ok := init["generation_config"].(map[string]any)
	if !ok {
		t.Fatalf("init generation_config = %#v, want object", init["generation_config"])
	}
	chunkSchedule, ok := generationConfig["chunk_length_schedule"].([]any)
	if !ok || len(chunkSchedule) != 3 || chunkSchedule[0] != float64(80) || chunkSchedule[1] != float64(120) || chunkSchedule[2] != float64(200) {
		t.Fatalf("init chunk_length_schedule = %#v, want [80 120 200]", generationConfig["chunk_length_schedule"])
	}
	locators, ok := init["pronunciation_dictionary_locators"].([]any)
	if !ok || len(locators) != 1 {
		t.Fatalf("init pronunciation_dictionary_locators = %#v, want one locator", init["pronunciation_dictionary_locators"])
	}
	locator, ok := locators[0].(map[string]any)
	if !ok || locator["pronunciation_dictionary_id"] != "dict-1" || locator["version_id"] != "version-1" {
		t.Fatalf("init pronunciation_dictionary_locators[0] = %#v, want reference locator", locators[0])
	}

	text := readElevenLabsTTSStreamMessage(t, messages)
	if text["text"] != "hello " || text["context_id"] != contextID {
		t.Fatalf("text packet = %#v, want hello with trailing space and context_id %q", text, contextID)
	}
	if _, ok := text["flush"]; ok {
		t.Fatalf("text packet = %#v, want no flush before Flush()", text)
	}
}

func TestElevenLabsTTSStreamAutoModeSendsSentencesAndFlushesTailLikeReference(t *testing.T) {
	messages := make(chan map[string]any, 4)
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSWebsocketServer(messages, serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("This first sentence is definitely long enough. Tail"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}

	init := readElevenLabsTTSStreamMessage(t, messages)
	contextID, _ := init["context_id"].(string)
	if init["text"] != " " || contextID == "" {
		t.Fatalf("init packet = %#v, want warmup packet with context_id", init)
	}
	first := readElevenLabsTTSStreamMessage(t, messages)
	if first["text"] != "This first sentence is definitely long enough. " || first["context_id"] != contextID {
		t.Fatalf("first text packet = %#v, want completed sentence for context %q", first, contextID)
	}
	if first["flush"] != true {
		t.Fatalf("first text packet flush = %#v, want true for auto_mode sentence chunk", first["flush"])
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	tail := readElevenLabsTTSStreamMessage(t, messages)
	if tail["text"] != "Tail " || tail["context_id"] != contextID || tail["flush"] != true {
		t.Fatalf("tail text packet = %#v, want flushed tail with flush=true", tail)
	}

	select {
	case extra := <-messages:
		t.Fatalf("unexpected provider end packet after Flush: %#v", extra)
	default:
	}
}

func TestElevenLabsTTSStreamWordTokenizerHoldsTailUntilFlushLikeReference(t *testing.T) {
	messages := make(chan map[string]any, 4)
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSWebsocketServer(messages, serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
		WithElevenLabsChunkLengthSchedule([]int{80, 120, 200}),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello wor"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	init := readElevenLabsTTSStreamMessage(t, messages)
	contextID, _ := init["context_id"].(string)
	if init["text"] != " " || contextID == "" {
		t.Fatalf("init packet = %#v, want warmup packet with context_id", init)
	}
	first := readElevenLabsTTSStreamMessage(t, messages)
	if first["text"] != "hello " || first["context_id"] != contextID {
		t.Fatalf("first text packet = %#v, want complete word only for context %q", first, contextID)
	}
	if _, ok := first["flush"]; ok {
		t.Fatalf("first text packet = %#v, want no flush for word tokenizer", first)
	}
	select {
	case msg := <-messages:
		t.Fatalf("partial tail sent before Flush: %#v", msg)
	default:
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	tail := readElevenLabsTTSStreamMessage(t, messages)
	if tail["text"] != "wor " || tail["context_id"] != contextID {
		t.Fatalf("tail text packet = %#v, want flushed incomplete word for context %q", tail, contextID)
	}
	if _, ok := tail["flush"]; ok {
		t.Fatalf("tail text packet = %#v, want no provider flush for word tokenizer", tail)
	}
}

func TestElevenLabsTTSStreamWordTokenizerSendsCompleteWordsIndividuallyLikeReference(t *testing.T) {
	messages := make(chan map[string]any, 5)
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSWebsocketServer(messages, serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
		WithElevenLabsChunkLengthSchedule([]int{80, 120, 200}),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("one two three"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	init := readElevenLabsTTSStreamMessage(t, messages)
	contextID, _ := init["context_id"].(string)
	if init["text"] != " " || contextID == "" {
		t.Fatalf("init packet = %#v, want warmup packet with context_id", init)
	}
	first := readElevenLabsTTSStreamMessage(t, messages)
	if first["text"] != "one " || first["context_id"] != contextID {
		t.Fatalf("first text packet = %#v, want first complete word for context %q", first, contextID)
	}
	second := readElevenLabsTTSStreamMessage(t, messages)
	if second["text"] != "two " || second["context_id"] != contextID {
		t.Fatalf("second text packet = %#v, want second complete word for context %q", second, contextID)
	}
	select {
	case msg := <-messages:
		t.Fatalf("final incomplete word sent before Flush: %#v", msg)
	default:
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	tail := readElevenLabsTTSStreamMessage(t, messages)
	if tail["text"] != "three " || tail["context_id"] != contextID {
		t.Fatalf("tail text packet = %#v, want flushed final word for context %q", tail, contextID)
	}
}

func TestElevenLabsTTSStreamEndInputClosesContextLikeReference(t *testing.T) {
	messages := make(chan map[string]any, 4)
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSWebsocketServer(messages, serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := endElevenLabsTestInput(stream); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	init := readElevenLabsTTSStreamMessage(t, messages)
	contextID, _ := init["context_id"].(string)
	if init["text"] != " " || contextID == "" {
		t.Fatalf("init packet = %#v, want warmup packet with context_id", init)
	}
	end := readElevenLabsTTSStreamMessage(t, messages)
	if end["text"] != "" || end["context_id"] != contextID || end["flush"] != true {
		t.Fatalf("end packet = %#v, want empty flush packet", end)
	}
	closeContext := readElevenLabsTTSStreamMessage(t, messages)
	if closeContext["context_id"] != contextID || closeContext["close_context"] != true {
		t.Fatalf("close_context packet = %#v, want close for context %q", closeContext, contextID)
	}
	if err := stream.PushText("late"); err != nil {
		t.Fatalf("PushText after EndInput() error = %v, want nil", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after EndInput() error = %v, want nil", err)
	}
	if err := endElevenLabsTestInput(stream); err != nil {
		t.Fatalf("second EndInput() error = %v, want nil", err)
	}
	select {
	case msg := <-messages:
		t.Fatalf("input after EndInput() sent websocket packet: %#v", msg)
	case err := <-serverErr:
		t.Fatalf("test websocket server error: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
}

func TestElevenLabsTTSStreamIgnoresReferenceEmptyText(t *testing.T) {
	messages := make(chan map[string]any, 2)
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSWebsocketServer(messages, serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText(""); err != nil {
		t.Fatalf("PushText(empty) error = %v", err)
	}
	select {
	case msg := <-messages:
		t.Fatalf("PushText(empty) sent websocket packet: %#v", msg)
	case err := <-serverErr:
		t.Fatalf("test websocket server error: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
}

func runElevenLabsTTSWebsocketServer(messages chan<- map[string]any, conn net.Conn, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}

	if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		errCh <- err
		return
	}
	msg, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err == nil {
		messages <- msg
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		errCh <- err
		return
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		errCh <- err
		return
	}

	for {
		msg, err := readElevenLabsClientWebsocketJSONFrame(reader)
		if err != nil {
			return
		}
		messages <- msg
	}
}

func TestElevenLabsTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &elevenLabsStream{
		ctx:    context.Background(),
		audio:  make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		closed: true,
	}
	stream.audio <- &tts.SynthesizedAudio{}

	if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestElevenLabsTTSStreamIgnoresInputAfterCloseLikeReference(t *testing.T) {
	stream := &elevenLabsStream{
		ctx:    context.Background(),
		audio:  make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		closed: true,
	}

	if err := stream.PushText("late"); err != nil {
		t.Fatalf("PushText after Close error = %v, want nil", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after Close error = %v, want nil", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput after Close error = %v, want nil", err)
	}
}

func TestElevenLabsTTSNextReturnsQueuedAudioBeforeStreamError(t *testing.T) {
	providerErr := errors.New("provider failed after audio")
	for i := range 200 {
		want := &tts.SynthesizedAudio{RequestID: "req-audio"}
		stream := &elevenLabsStream{
			ctx:   context.Background(),
			audio: make(chan *tts.SynthesizedAudio, 1),
			errCh: make(chan error, 1),
		}
		stream.audio <- want
		stream.errCh <- providerErr

		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("trial %d Next error = %v, want queued audio before stream error", i, err)
		}
		if audio != want {
			t.Fatalf("trial %d Next audio = %#v, want queued audio %#v", i, audio, want)
		}
	}
}

func TestElevenLabsTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	closed := make(chan struct{})
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsClosingWebsocketServerAfterFrame(serverConn, closed, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	writeErr := stream.PushText("hello there dear friend. Tail")
	if writeErr == nil {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for test websocket close")
		}
		select {
		case err := <-serverErr:
			if err != nil {
				t.Fatalf("test websocket server error: %v", err)
			}
		default:
		}
	}

	for range 3 {
		writeErr = stream.PushText("hello there dear friend. Tail")
		if writeErr != nil {
			break
		}
	}
	if writeErr == nil {
		t.Fatal("PushText error = nil after closed websocket, want write failure")
	}
	providerStream, ok := stream.(*elevenLabsStream)
	if !ok {
		t.Fatalf("stream = %T, want *elevenLabsStream", stream)
	}
	if !providerStream.closed {
		t.Fatal("stream closed = false after write failure, want true")
	}

	err = stream.PushText("again")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second PushText error = %v, want io.ErrClosedPipe", err)
	}
}

func TestElevenLabsTTSProviderCloseClosesActiveStreams(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	handlerDone := make(chan struct{})
	serverErr := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer close(handlerDone)
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})}
	go server.Serve(&singleElevenLabsConnListener{conn: serverConn})
	defer server.Close()

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.PushText("again"); err != nil {
		t.Fatalf("PushText after provider Close error = %v, want nil", err)
	}

	select {
	case <-handlerDone:
	case err := <-serverErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("provider Close did not close active websocket stream")
	}
}

func TestElevenLabsTTSRegisterStreamAfterCloseClosesStream(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	handlerDone := make(chan struct{})
	serverErr := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer close(handlerDone)
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})}
	go server.Serve(&singleElevenLabsConnListener{conn: serverConn})
	defer server.Close()

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	conn, _, err := websocket.DefaultDialer.DialContext(context.Background(), "ws://eleven.test/v1/text-to-speech/voice-1/multi-stream-input", nil)
	if err != nil {
		t.Fatalf("DialContext error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stream := &elevenLabsStream{
		provider:   provider,
		conn:       conn,
		audio:      make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
		ctx:        ctx,
		cancel:     cancel,
		contextID:  "ctx-test",
		sampleRate: provider.sampleRate,
		encoding:   provider.encoding,
	}

	if provider.registerStream(stream) {
		t.Fatal("registerStream after provider Close = true, want false")
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after rejected registration error = %v, want io.ErrClosedPipe", err)
	}
	if len(provider.streams) != 0 {
		t.Fatalf("provider streams = %d, want 0", len(provider.streams))
	}

	select {
	case <-handlerDone:
	case err := <-serverErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("registerStream after Close did not close websocket stream")
	}
}

func TestElevenLabsTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/mpeg"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	defer func() { http.DefaultClient = oldClient }()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("https://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize after Close stream = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if httpCalls != 0 {
		t.Fatalf("Synthesize after Close HTTP calls = %d, want 0", httpCalls)
	}
}

func TestElevenLabsTTSStreamAfterCloseIsRejected(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	dialCalls := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls++
			return nil, errors.New("unexpected websocket dial")
		},
		Proxy: nil,
	}
	defer func() { websocket.DefaultDialer = oldDialer }()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream after Close stream = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
	if dialCalls != 0 {
		t.Fatalf("Stream after Close dial calls = %d, want 0", dialCalls)
	}
}

func TestElevenLabsTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	closed := make(chan struct{})
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsClosingWebsocketServerAfterFrames(serverConn, 2, closed, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello there dear friend. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}

	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != websocket.CloseAbnormalClosure {
		t.Fatalf("StatusCode = %d, want websocket close code", statusErr.StatusCode)
	}
}

func TestElevenLabsTTSStreamUnexpectedNormalCloseReturnsAPIStatusError(t *testing.T) {
	closed := make(chan struct{})
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsNormalCloseWebsocketServerAfterFrames(serverConn, 2, closed, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello there dear friend. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for normal websocket close")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for normal close server")
	}

	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != websocket.CloseNormalClosure {
		t.Fatalf("StatusCode = %d, want normal close code", statusErr.StatusCode)
	}
}

func TestElevenLabsTTSStreamProviderErrorReturnsAPIStatusError(t *testing.T) {
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSProviderErrorWebsocketServer(serverConn, "quota exceeded", serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello there dear friend. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}

	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if !strings.Contains(statusErr.Error(), "quota exceeded") {
		t.Fatalf("Next error = %v, want provider error detail", statusErr)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}
}

func TestElevenLabsTTSWebsocketIgnoresWrongContextErrorLikeReference(t *testing.T) {
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSWrongContextThenAudioWebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
		WithElevenLabsEncoding("pcm_16000"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello there dear friend. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want current context audio", err)
	}
	if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, []byte{1, 0, 2, 0}) {
		t.Fatalf("Next audio = %#v, want current context PCM frame", audio)
	}
	if audio.IsFinal {
		t.Fatal("Next IsFinal = true, want audio before final marker")
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final Next audio = %#v, want final marker", final)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}
}

func TestElevenLabsTTSWebsocketIgnoresContextlessErrorLikeReference(t *testing.T) {
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSContextlessErrorThenAudioWebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
		WithElevenLabsEncoding("pcm_16000"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello there dear friend. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want current context audio", err)
	}
	if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, []byte{3, 0, 4, 0}) {
		t.Fatalf("Next audio = %#v, want current context PCM frame", audio)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}
}

func TestElevenLabsTTSWebsocketMalformedAudioReturnsAPIStatusError(t *testing.T) {
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSMalformedAudioWebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
		WithElevenLabsEncoding("pcm_8000"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello there dear friend. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}

	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if !strings.Contains(statusErr.Error(), "Could not synthesize") {
		t.Fatalf("Next error = %v, want reference synthesis failure", statusErr)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}
}

func TestElevenLabsTTSWebsocketMalformedMP3ReturnsAPIStatusError(t *testing.T) {
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSInvalidMP3WebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello there dear friend. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}

	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if !strings.Contains(statusErr.Error(), "Could not synthesize") {
		t.Fatalf("Next error = %v, want reference synthesis failure", statusErr)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}
}

func TestElevenLabsTTSWebsocketPCMFinalAudioEmitsReferenceFinalMarker(t *testing.T) {
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSFinalPCMWebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
		WithElevenLabsEncoding("pcm_8000"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello there. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || len(audio.Frame.Data) == 0 {
		t.Fatalf("first Next audio = %#v, want PCM frame", audio)
	}
	if audio.IsFinal {
		t.Fatal("first PCM websocket audio IsFinal = true, want separate final marker")
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second Next audio = %#v, want boundary-only final marker", final)
	}
	if next, err := stream.Next(); next != nil || err != io.EOF {
		t.Fatalf("third Next = (%#v, %v), want nil EOF", next, err)
	}

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for final PCM websocket server")
	}
}

func TestElevenLabsTTSWebsocketMP3DecodesSplitProviderAudio(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	if len(mp3Data) < 32 {
		t.Fatalf("mp3 fixture length = %d, want enough bytes to split", len(mp3Data))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &elevenLabsStream{
		audio:      make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
		ctx:        ctx,
		cancel:     cancel,
		encoding:   "mp3_22050_32",
		sampleRate: 22050,
	}
	if err := stream.pushMP3Audio(base64.StdEncoding.EncodeToString(mp3Data[:8]), "", nil); err != nil {
		t.Fatalf("push first split MP3 chunk: %v", err)
	}
	if err := stream.pushMP3Audio(base64.StdEncoding.EncodeToString(mp3Data[8:]), "", nil); err != nil {
		t.Fatalf("push second split MP3 chunk: %v", err)
	}
	done := make(chan struct{})
	go func() {
		stream.closeMP3Decoder(true)
		close(stream.audio)
		close(done)
	}()

	decodedFrames := 0
	for range 5000 {
		var audio *tts.SynthesizedAudio
		select {
		case err := <-stream.errCh:
			t.Fatalf("split MP3 decode error = %v", err)
		case audio = <-stream.audio:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for split MP3 decoded audio")
		}
		if audio.IsFinal {
			if decodedFrames == 0 {
				t.Fatal("final marker arrived before decoded split MP3 audio")
			}
			break
		}
		if audio.Frame == nil || len(audio.Frame.Data) == 0 {
			t.Fatalf("decoded audio = %#v, want PCM frame", audio)
		}
		if audio.Frame.SampleRate != 22050 || audio.Frame.NumChannels != 1 {
			t.Fatalf("decoded frame format = %d Hz/%d ch, want 22050 Hz mono", audio.Frame.SampleRate, audio.Frame.NumChannels)
		}
		decodedFrames++
	}
	if decodedFrames == 0 {
		t.Fatal("decoded split MP3 frames = 0")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for split MP3 decoder shutdown")
	}
}

func TestElevenLabsTTSWebsocketMP3CarriesReferenceAlignmentMetadata(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &elevenLabsStream{
		audio:      make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
		ctx:        ctx,
		cancel:     cancel,
		encoding:   "mp3_22050_32",
		sampleRate: 22050,
	}
	timed := []tts.TimedString{
		{Text: "hello ", StartTime: 0, EndTime: 0.06},
		{Text: "world", StartTime: 0.06, EndTime: 0.11},
	}
	if err := stream.pushMP3Audio(base64.StdEncoding.EncodeToString(mp3Data), "hello world", timed); err != nil {
		t.Fatalf("push MP3 with alignment: %v", err)
	}
	done := make(chan struct{})
	go func() {
		stream.closeMP3Decoder(true)
		close(stream.audio)
		close(done)
	}()

	seenMetadata := false
	for range 5000 {
		var audio *tts.SynthesizedAudio
		select {
		case err := <-stream.errCh:
			t.Fatalf("MP3 decode error = %v", err)
		case audio = <-stream.audio:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for MP3 decoded audio")
		}
		if audio == nil {
			t.Fatal("audio = nil, want decoded frame")
		}
		if audio.IsFinal {
			if !seenMetadata {
				t.Fatal("final marker arrived before alignment metadata")
			}
			break
		}
		if audio.DeltaText == "" && len(audio.TimedTranscript) == 0 {
			continue
		}
		if audio.DeltaText != "hello world" {
			t.Fatalf("DeltaText = %q, want hello world", audio.DeltaText)
		}
		if len(audio.TimedTranscript) != 2 {
			t.Fatalf("TimedTranscript = %#v, want two timed words", audio.TimedTranscript)
		}
		seenMetadata = true
	}
	if !seenMetadata {
		t.Fatal("stream did not emit MP3 alignment metadata")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for MP3 decoder shutdown")
	}
}

func TestElevenLabsTTSWebsocketMP3BuffersAlignmentUntilAudioLikeReference(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSAlignmentThenMP3AudioWebsocketServer(serverConn, mp3Data, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello world. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	seenMetadata := false
	for range 5000 {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next error = %v", err)
		}
		if audio == nil {
			t.Fatal("Next audio = nil")
		}
		if audio.Frame == nil && (audio.DeltaText != "" || len(audio.TimedTranscript) > 0) {
			t.Fatalf("metadata-only event = %#v, want transcript buffered until MP3 audio", audio)
		}
		if audio.Frame != nil && (audio.DeltaText != "" || len(audio.TimedTranscript) > 0) {
			if audio.DeltaText != "hello world" {
				t.Fatalf("DeltaText = %q, want hello world", audio.DeltaText)
			}
			if len(audio.TimedTranscript) != 2 {
				t.Fatalf("TimedTranscript = %#v, want two timed words", audio.TimedTranscript)
			}
			seenMetadata = true
		}
		if audio.IsFinal {
			break
		}
	}
	if !seenMetadata {
		t.Fatal("decoded MP3 audio did not carry buffered alignment metadata")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}
}

func TestElevenLabsTTSWebsocketPCMBuffersAlignmentUntilAudioLikeReference(t *testing.T) {
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSAlignmentThenPCMAudioWebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
		WithElevenLabsEncoding("pcm_16000"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello world. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil {
		t.Fatal("Next audio = nil")
	}
	if audio.Frame == nil {
		t.Fatalf("metadata-only event = %#v, want transcript buffered until PCM audio", audio)
	}
	if audio.DeltaText != "hello world" {
		t.Fatalf("DeltaText = %q, want hello world", audio.DeltaText)
	}
	if len(audio.TimedTranscript) != 2 {
		t.Fatalf("TimedTranscript = %#v, want two timed words", audio.TimedTranscript)
	}
	if !bytes.Equal(audio.Frame.Data, []byte{7, 0, 8, 0}) {
		t.Fatalf("PCM frame = %#v, want provider audio", audio.Frame.Data)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket server")
	}
}

func readElevenLabsTTSStreamMessage(t *testing.T, messages <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ElevenLabs TTS websocket message")
	}
	return nil
}

func endElevenLabsTestInput(stream tts.SynthesizeStream) error {
	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		return errors.New("elevenlabs stream does not implement EndInput")
	}
	return ending.EndInput()
}

func equalIntSlices(a []int, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertElevenLabsTTSVoiceSettings(t *testing.T, raw any) {
	t.Helper()
	settings, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("voice_settings = %#v, want object", raw)
	}
	want := map[string]any{
		"stability":         0.7,
		"similarity_boost":  0.8,
		"style":             0.35,
		"speed":             1.05,
		"use_speaker_boost": true,
	}
	for key, wantValue := range want {
		if settings[key] != wantValue {
			t.Fatalf("voice_settings[%s] = %#v, want %#v in %#v", key, settings[key], wantValue, settings)
		}
	}
}

func runElevenLabsClosingWebsocketServerAfterFrame(conn net.Conn, closed chan<- struct{}, errCh chan<- error) {
	runElevenLabsClosingWebsocketServerAfterFrames(conn, 1, closed, errCh)
}

func runElevenLabsClosingWebsocketServerAfterFrames(conn net.Conn, frames int, closed chan<- struct{}, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for range frames {
		if err := readElevenLabsClientWebsocketFrame(reader); err != nil {
			errCh <- err
			return
		}
	}
	close(closed)
	errCh <- nil
}

func runElevenLabsNormalCloseWebsocketServerAfterFrames(conn net.Conn, frames int, closed chan<- struct{}, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for range frames {
		if err := readElevenLabsClientWebsocketFrame(reader); err != nil {
			errCh <- err
			return
		}
	}
	_, err = conn.Write([]byte{0x88, 0x02, 0x03, 0xe8})
	close(closed)
	errCh <- err
}

func runElevenLabsTTSProviderErrorWebsocketServer(conn net.Conn, providerError string, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	init, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	text, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	contextID, _ := init["context_id"].(string)
	if textContextID, _ := text["context_id"].(string); textContextID != "" {
		contextID = textContextID
	}
	if contextID == "" {
		errCh <- errors.New("missing context_id in client packets")
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"context_id": contextID,
		"error":      providerError,
	}); err != nil {
		errCh <- err
		return
	}
	errCh <- nil
}

func runElevenLabsTTSWrongContextThenAudioWebsocketServer(conn net.Conn, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	init, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	text, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	contextID, _ := init["context_id"].(string)
	if textContextID, _ := text["context_id"].(string); textContextID != "" {
		contextID = textContextID
	}
	if contextID == "" {
		errCh <- errors.New("missing context_id in client packets")
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"context_id": "stale_context",
		"error":      "stale error",
	}); err != nil {
		errCh <- err
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"context_id": contextID,
		"audio":      base64.StdEncoding.EncodeToString([]byte{1, 0, 2, 0}),
		"isFinal":    true,
	}); err != nil {
		errCh <- err
		return
	}
	for {
		if err := readElevenLabsClientWebsocketFrame(reader); err != nil {
			break
		}
	}
	errCh <- nil
}

func runElevenLabsTTSContextlessErrorThenAudioWebsocketServer(conn net.Conn, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	init, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	text, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	contextID, _ := init["context_id"].(string)
	if textContextID, _ := text["context_id"].(string); textContextID != "" {
		contextID = textContextID
	}
	if contextID == "" {
		errCh <- errors.New("missing context_id in client packets")
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"error": "broadcast stale error",
	}); err != nil {
		errCh <- err
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"context_id": contextID,
		"audio":      base64.StdEncoding.EncodeToString([]byte{3, 0, 4, 0}),
		"isFinal":    true,
	}); err != nil {
		errCh <- err
		return
	}
	for {
		if err := readElevenLabsClientWebsocketFrame(reader); err != nil {
			break
		}
	}
	errCh <- nil
}

func runElevenLabsTTSAlignmentThenMP3AudioWebsocketServer(conn net.Conn, mp3Data []byte, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	init, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	text, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	contextID, _ := init["context_id"].(string)
	if textContextID, _ := text["context_id"].(string); textContextID != "" {
		contextID = textContextID
	}
	if contextID == "" {
		errCh <- errors.New("missing context_id in client packets")
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"context_id": contextID,
		"normalizedAlignment": map[string]any{
			"chars":             []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"},
			"charsStartTimesMs": []int{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
			"charsDurationsMs":  []int{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10},
			"charStartTimesMs":  []int{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
			"charDurationsMs":   []int{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10},
		},
	}); err != nil {
		errCh <- err
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"context_id": contextID,
		"audio":      base64.StdEncoding.EncodeToString(mp3Data),
		"isFinal":    true,
	}); err != nil {
		errCh <- err
		return
	}
	for {
		if err := readElevenLabsClientWebsocketFrame(reader); err != nil {
			break
		}
	}
	errCh <- nil
}

func runElevenLabsTTSAlignmentThenPCMAudioWebsocketServer(conn net.Conn, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	init, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	text, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	contextID, _ := init["context_id"].(string)
	if textContextID, _ := text["context_id"].(string); textContextID != "" {
		contextID = textContextID
	}
	if contextID == "" {
		errCh <- errors.New("missing context_id in client packets")
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"context_id": contextID,
		"normalizedAlignment": map[string]any{
			"chars":             []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"},
			"charsStartTimesMs": []int{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
			"charsDurationsMs":  []int{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10},
			"charStartTimesMs":  []int{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
			"charDurationsMs":   []int{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10},
		},
	}); err != nil {
		errCh <- err
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"context_id": contextID,
		"audio":      base64.StdEncoding.EncodeToString([]byte{7, 0, 8, 0}),
		"isFinal":    true,
	}); err != nil {
		errCh <- err
		return
	}
	for {
		if err := readElevenLabsClientWebsocketFrame(reader); err != nil {
			break
		}
	}
	errCh <- nil
}

func runElevenLabsTTSMalformedAudioWebsocketServer(conn net.Conn, errCh chan<- error) {
	runElevenLabsTTSAudioPayloadWebsocketServer(conn, "not base64 !!!", false, errCh)
}

func runElevenLabsTTSInvalidMP3WebsocketServer(conn net.Conn, errCh chan<- error) {
	runElevenLabsTTSAudioPayloadWebsocketServer(conn, base64.StdEncoding.EncodeToString([]byte("not an mp3 frame")), true, errCh)
}

func runElevenLabsTTSAudioPayloadWebsocketServer(conn net.Conn, audio string, waitForClientClose bool, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	init, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	text, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	contextID, _ := init["context_id"].(string)
	if textContextID, _ := text["context_id"].(string); textContextID != "" {
		contextID = textContextID
	}
	if contextID == "" {
		errCh <- errors.New("missing context_id in client packets")
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"context_id": contextID,
		"audio":      audio,
		"isFinal":    true,
	}); err != nil {
		errCh <- err
		return
	}
	if waitForClientClose {
		for {
			if err := readElevenLabsClientWebsocketFrame(reader); err != nil {
				break
			}
		}
	}
	errCh <- nil
}

func runElevenLabsTTSFinalPCMWebsocketServer(conn net.Conn, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	init, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	text, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	contextID, _ := init["context_id"].(string)
	if textContextID, _ := text["context_id"].(string); textContextID != "" {
		contextID = textContextID
	}
	if contextID == "" {
		errCh <- errors.New("missing context_id in client packets")
		return
	}
	if err := writeElevenLabsServerWebsocketJSONFrame(conn, map[string]any{
		"context_id": contextID,
		"audio":      base64.StdEncoding.EncodeToString([]byte{1, 0, 2, 0}),
		"isFinal":    true,
	}); err != nil {
		errCh <- err
		return
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		errCh <- err
		return
	}
	_ = readElevenLabsClientWebsocketFrame(reader)
	errCh <- nil
}

func writeElevenLabsServerWebsocketJSONFrame(w io.Writer, msg map[string]any) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := []byte{0x81}
	switch length := len(payload); {
	case length < 126:
		header = append(header, byte(length))
	case length <= 65535:
		header = append(header, 126, byte(length>>8), byte(length))
	default:
		header = append(header, 127, byte(length>>56), byte(length>>48), byte(length>>40), byte(length>>32), byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func readElevenLabsClientWebsocketFrame(reader *bufio.Reader) error {
	_, err := readElevenLabsClientWebsocketFramePayload(reader)
	return err
}

func readElevenLabsClientWebsocketJSONFrame(reader *bufio.Reader) (map[string]any, error) {
	payload, err := readElevenLabsClientWebsocketFramePayload(reader)
	if err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func readElevenLabsClientWebsocketFramePayload(reader *bufio.Reader) ([]byte, error) {
	if _, err := reader.ReadByte(); err != nil {
		return nil, err
	}
	lengthByte, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	masked := lengthByte&0x80 != 0
	length := uint64(lengthByte & 0x7f)
	switch length {
	case 126:
		var buf [2]byte
		if _, err := io.ReadFull(reader, buf[:]); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(buf[:]))
	case 127:
		var buf [8]byte
		if _, err := io.ReadFull(reader, buf[:]); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(buf[:])
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(reader, mask[:]); err != nil {
			return nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return payload, nil
}

func TestElevenLabsSynthesizedAudioUsesConfiguredSampleRate(t *testing.T) {
	resp := elWSResponse{
		Audio: base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
	}

	audio, err := elevenLabsSynthesizedAudio(resp, 22050, "pcm_22050")
	if err != nil {
		t.Fatalf("elevenLabsSynthesizedAudio() error = %v", err)
	}
	if audio.Frame.SampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", audio.Frame.SampleRate)
	}
}

func TestElevenLabsTTSAlignmentMapsTimedTranscript(t *testing.T) {
	resp := elWSResponse{
		Audio:   base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
		IsFinal: true,
		NormalizedAlignment: &elevenLabsAlignment{
			Chars:            []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"},
			CharStartTimesMs: []int{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
			CharDurationsMs:  []int{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10},
		},
	}

	audio, err := elevenLabsSynthesizedAudio(resp, 22050, "pcm_22050")
	if err != nil {
		t.Fatalf("elevenLabsSynthesizedAudio() error = %v", err)
	}
	if audio.DeltaText != "hello world" {
		t.Fatalf("DeltaText = %q, want hello world", audio.DeltaText)
	}
	if len(audio.TimedTranscript) != 2 {
		t.Fatalf("TimedTranscript = %#v, want two timed words", audio.TimedTranscript)
	}
	if got := audio.TimedTranscript[0]; got.Text != "hello " || got.StartTime != 0 || got.EndTime != 0.06 {
		t.Fatalf("TimedTranscript[0] = %#v, want hello from 0 to 0.06", got)
	}
	if got := audio.TimedTranscript[1]; got.Text != "world" || got.StartTime != 0.06 || got.EndTime != 0.11 {
		t.Fatalf("TimedTranscript[1] = %#v, want world from 0.06 to 0.11", got)
	}
}

func TestElevenLabsTTSUsesOriginalAlignmentForCJKReferenceDefault(t *testing.T) {
	if got := elevenLabsDefaultPreferredAlignment("ja"); got != "original" {
		t.Fatalf("default preferred alignment = %q, want original for ja", got)
	}
	if got := elevenLabsDefaultPreferredAlignment("en"); got != "normalized" {
		t.Fatalf("default preferred alignment = %q, want normalized for en", got)
	}

	resp := elWSResponse{
		Audio:   base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
		IsFinal: true,
		NormalizedAlignment: &elevenLabsAlignment{
			Chars:            []string{"1"},
			CharStartTimesMs: []int{0},
			CharDurationsMs:  []int{10},
		},
		Alignment: &elevenLabsAlignment{
			Chars:            []string{"あ"},
			CharStartTimesMs: []int{20},
			CharDurationsMs:  []int{30},
		},
	}

	stream := &elevenLabsStream{preferredAlignment: "original"}
	timed := stream.timedTranscriptFromAlignment(resp)
	if len(timed) != 1 || timed[0].Text != "あ" || timed[0].StartTime != 0.02 || timed[0].EndTime != 0.05 {
		t.Fatalf("timed transcript = %#v, want original alignment for CJK", timed)
	}
	if got := stream.deltaText(resp); got != "あ" {
		t.Fatalf("delta text = %q, want original alignment text", got)
	}
}

func TestElevenLabsTTSPreferredAlignmentOverrideMatchesReference(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsLanguage("en"),
		WithElevenLabsPreferredAlignment("original"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	if got := elevenLabsPreferredAlignment(provider.language, provider.preferredAlignment); got != "original" {
		t.Fatalf("preferred alignment = %q, want explicit original", got)
	}

	resp := elWSResponse{
		Audio:   base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
		IsFinal: true,
		NormalizedAlignment: &elevenLabsAlignment{
			Chars:            []string{"1"},
			CharStartTimesMs: []int{0},
			CharDurationsMs:  []int{10},
		},
		Alignment: &elevenLabsAlignment{
			Chars:            []string{"a"},
			CharStartTimesMs: []int{20},
			CharDurationsMs:  []int{30},
		},
	}
	stream := &elevenLabsStream{preferredAlignment: elevenLabsPreferredAlignment(provider.language, provider.preferredAlignment)}
	if got := stream.deltaText(resp); got != "a" {
		t.Fatalf("delta text = %q, want original alignment text", got)
	}
}

func TestElevenLabsSynthesizedAudioDecodesReferenceMP3WebsocketAudio(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	resp := elWSResponse{
		Audio: base64.StdEncoding.EncodeToString(mp3Data),
	}

	audio, err := elevenLabsSynthesizedAudio(resp, 22050, "mp3_22050_32")
	if err != nil {
		t.Fatalf("elevenLabsSynthesizedAudio() error = %v", err)
	}
	if audio.Frame.SampleRate != 22050 {
		t.Fatalf("sample rate = %d, want configured mp3 rate 22050", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want reference mono output", audio.Frame.NumChannels)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	if got, want := len(audio.Frame.Data), int(audio.Frame.SamplesPerChannel*audio.Frame.NumChannels*2); got != want {
		t.Fatalf("frame byte length = %d, want %d from samples/channels", got, want)
	}
	if bytes.Equal(audio.Frame.Data, mp3Data[:len(audio.Frame.Data)]) {
		t.Fatal("frame data still contains compressed mp3 bytes")
	}
}

func TestElevenLabsSynthesizedAudioRejectsUnsupportedOpusEncoding(t *testing.T) {
	resp := elWSResponse{
		Audio: base64.StdEncoding.EncodeToString([]byte{0x4f, 0x70, 0x75, 0x73}),
	}

	audio, err := elevenLabsSynthesizedAudio(resp, 48000, "opus_48000_64")
	if err == nil {
		t.Fatalf("elevenLabsSynthesizedAudio() audio = %#v, want unsupported opus error", audio)
	}
	if !strings.Contains(err.Error(), "unsupported elevenlabs TTS encoding") || !strings.Contains(err.Error(), "opus_48000_64") {
		t.Fatalf("error = %v, want unsupported opus encoding context", err)
	}
}

type elevenLabsRoundTripFunc func(*http.Request) (*http.Response, error)

func (f elevenLabsRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type elevenLabsErrReader struct {
	err error
}

func (r elevenLabsErrReader) Read([]byte) (int, error) {
	return 0, r.err
}

func (r elevenLabsErrReader) Close() error {
	return nil
}

type elevenLabsReadOnceEOFBody struct {
	data []byte
	read bool
}

func (b *elevenLabsReadOnceEOFBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.EOF
	}
	b.read = true
	return copy(p, b.data), io.EOF
}

func (b *elevenLabsReadOnceEOFBody) Close() error {
	return nil
}

type elevenLabsBlockingEOFBody struct {
	data    []byte
	read    bool
	closeCh chan struct{}
	once    sync.Once
}

func newElevenLabsBlockingEOFBody(data []byte) *elevenLabsBlockingEOFBody {
	return &elevenLabsBlockingEOFBody{
		data:    data,
		closeCh: make(chan struct{}),
	}
}

func (b *elevenLabsBlockingEOFBody) Read(p []byte) (int, error) {
	if !b.read {
		b.read = true
		return copy(p, b.data), nil
	}
	<-b.closeCh
	return 0, io.EOF
}

func (b *elevenLabsBlockingEOFBody) Close() error {
	b.once.Do(func() { close(b.closeCh) })
	return nil
}

type elevenLabsCloseCountBody struct {
	*strings.Reader
	closeCount int
}

func (b *elevenLabsCloseCountBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("closed twice")
	}
	return nil
}
