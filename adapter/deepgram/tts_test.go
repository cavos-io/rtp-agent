package deepgram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestDeepgramTTSDefaultsMatchReference(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "")

	if provider.model != "aura-2-andromeda-en" {
		t.Fatalf("model = %q, want aura-2-andromeda-en", provider.model)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.encoding != "linear16" {
		t.Fatalf("encoding = %q, want linear16", provider.encoding)
	}
	if got := tts.Model(provider); got != "aura-2-andromeda-en" {
		t.Fatalf("model metadata = %q, want aura-2-andromeda-en", got)
	}
	if got := tts.Provider(provider); got != "Deepgram" {
		t.Fatalf("provider metadata = %q, want Deepgram", got)
	}
}

func TestDeepgramTTSConstructorOptionsMatchReference(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "env-key")

	provider := NewDeepgramTTS("", "aura-custom",
		WithDeepgramTTSAudioFormat("mulaw", 8000),
	)
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}
	if provider.model != "aura-custom" {
		t.Fatalf("model = %q, want aura-custom", provider.model)
	}
	if provider.encoding != "mulaw" || provider.sampleRate != 8000 {
		t.Fatalf("audio format = %s/%d, want mulaw/8000", provider.encoding, provider.sampleRate)
	}

	requestURL, _ := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	query := parsed.Query()
	assertDeepgramTTSQuery(t, query, "model", "aura-custom")
	assertDeepgramTTSQuery(t, query, "encoding", "mulaw")
	assertDeepgramTTSQuery(t, query, "sample_rate", "8000")

	provider = NewDeepgramTTS("explicit-key", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestDeepgramTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "")
	provider := NewDeepgramTTS("", "", WithDeepgramTTSBaseURL("://bad-url"))

	_, synthErr := provider.Synthesize(context.Background(), "hello")
	if synthErr == nil || !strings.Contains(synthErr.Error(), "DEEPGRAM_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", synthErr)
	}

	_, streamErr := provider.Stream(context.Background())
	if streamErr == nil || !strings.Contains(streamErr.Error(), "DEEPGRAM_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", streamErr)
	}
}

func TestDeepgramTTSSynthesizeRequestUsesReferenceOptions(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSMipOptOut(true),
	)

	requestURL, body := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	query := parsed.Query()
	assertDeepgramTTSQuery(t, query, "model", "aura-2-andromeda-en")
	assertDeepgramTTSQuery(t, query, "encoding", "linear16")
	assertDeepgramTTSQuery(t, query, "sample_rate", "24000")
	assertDeepgramTTSQuery(t, query, "container", "none")
	assertDeepgramTTSQuery(t, query, "mip_opt_out", "true")

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["text"] != "hello" {
		t.Fatalf("text = %#v, want hello", payload["text"])
	}
}

func TestDeepgramTTSSynthesizeRequestUsesConfiguredBaseURL(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"),
	)

	requestURL, _ := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "deepgram.example" || parsed.Path != "/v1/speak" {
		t.Fatalf("url = %q, want configured HTTP base URL", requestURL)
	}
}

func TestDeepgramTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"err_msg":"rate limited"}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))

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
	if statusErr.Body != `{"err_msg":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestDeepgramTTSSynthesizeReturnsAPITimeoutError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))

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

func TestDeepgramTTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial refused")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))

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

func TestDeepgramTTSChunkedStreamReturnsAPITimeoutErrorOnReadFailure(t *testing.T) {
	stream := &deepgramTTSChunkedStream{
		resp:       &http.Response{Body: deepgramTTSReadCloser{err: context.DeadlineExceeded}},
		sampleRate: 24000,
	}

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestDeepgramTTSChunkedStreamReturnsAPIConnectionErrorOnReadFailure(t *testing.T) {
	stream := &deepgramTTSChunkedStream{
		resp:       &http.Response{Body: deepgramTTSReadCloser{err: errors.New("read failed")}},
		sampleRate: 24000,
	}

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestDeepgramTTSStreamURLUsesReferenceOptions(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "")

	streamURL := buildDeepgramTTSStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", parsed.Scheme)
	}
	query := parsed.Query()
	assertDeepgramTTSQuery(t, query, "model", "aura-2-andromeda-en")
	assertDeepgramTTSQuery(t, query, "encoding", "linear16")
	assertDeepgramTTSQuery(t, query, "sample_rate", "24000")
	assertDeepgramTTSQuery(t, query, "mip_opt_out", "false")
}

func TestDeepgramTTSStreamURLUsesConfiguredBaseURL(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"),
	)

	streamURL := buildDeepgramTTSStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "deepgram.example" || parsed.Path != "/v1/speak" {
		t.Fatalf("url = %q, want configured websocket base URL", streamURL)
	}
}

func TestDeepgramTTSStreamReturnsAPITimeoutErrorOnDialFailure(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, context.DeadlineExceeded
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))

	_, err := provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Stream error = %T %v, want APITimeoutError", err, err)
	}
}

func TestDeepgramTTSStreamReturnsAPIConnectionErrorOnDialFailure(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial refused")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))

	_, err := provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestDeepgramTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "")

	provider.UpdateOptions("aura-2-asteria-en")

	requestURL, _ := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsedRequest, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse synthesize url: %v", err)
	}
	assertDeepgramTTSQuery(t, parsedRequest.Query(), "model", "aura-2-asteria-en")

	streamURL := buildDeepgramTTSStreamURL(provider)
	parsedStream, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	assertDeepgramTTSQuery(t, parsedStream.Query(), "model", "aura-2-asteria-en")
	if got := provider.Model(); got != "aura-2-asteria-en" {
		t.Fatalf("Model() = %q, want aura-2-asteria-en", got)
	}
}

func TestDeepgramTTSStreamCloseSendsReferenceFlushAndClose(t *testing.T) {
	var writes []string
	closed := false
	stream := &deepgramTTSStream{
		writeJSON: func(v any) error {
			msg, ok := v.(map[string]interface{})
			if !ok {
				t.Fatalf("writeJSON payload = %#v, want map", v)
			}
			msgType, _ := msg["type"].(string)
			writes = append(writes, msgType)
			return nil
		},
		closeConn: func() error {
			closed = true
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(writes, []string{"Flush", "Close"}) {
		t.Fatalf("writes = %#v, want Flush then Close", writes)
	}
	if !closed {
		t.Fatal("connection not closed")
	}
	if err := stream.PushText("later"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestDeepgramTTSStreamCloseIgnoresReferenceFlushWriteFailure(t *testing.T) {
	writeErr := errors.New("flush write failed")
	closed := false
	stream := &deepgramTTSStream{
		writeJSON: func(any) error {
			return writeErr
		},
		closeConn: func() error {
			closed = true
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil like reference close callback", err)
	}
	if !closed {
		t.Fatal("connection not closed after close write failure")
	}
}

func TestDeepgramTTSStreamSpeakTextKeepsReferenceTrailingSeparator(t *testing.T) {
	var speakText string
	stream := &deepgramTTSStream{
		writeJSON: func(v any) error {
			msg, ok := v.(map[string]interface{})
			if !ok {
				t.Fatalf("writeJSON payload = %#v, want map", v)
			}
			if msg["type"] != "Speak" {
				t.Fatalf("message type = %#v, want Speak", msg["type"])
			}
			speakText, _ = msg["text"].(string)
			return nil
		},
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if speakText != "hello " {
		t.Fatalf("Speak text = %q, want reference trailing separator", speakText)
	}
}

func TestDeepgramTTSStreamMarksFinalAudioOnReferenceFlushed(t *testing.T) {
	stream := &deepgramTTSStream{
		audio: make(chan *tts.SynthesizedAudio, 1),
		errCh: make(chan error, 1),
	}

	if err := stream.handleTextMessage([]byte(`{"type":"Flushed"}`)); err != nil {
		t.Fatalf("handleTextMessage Flushed error = %v", err)
	}

	select {
	case audio := <-stream.audio:
		if audio == nil || !audio.IsFinal {
			t.Fatalf("Flushed audio = %+v, want final marker", audio)
		}
	default:
		t.Fatal("Flushed did not emit final audio marker")
	}
}

func TestDeepgramTTSStreamPropagatesReferenceErrorMessage(t *testing.T) {
	stream := &deepgramTTSStream{
		audio: make(chan *tts.SynthesizedAudio, 1),
		errCh: make(chan error, 1),
	}

	err := stream.handleTextMessage([]byte(`{"type":"Error","message":"bad request"}`))
	if err == nil {
		t.Fatal("handleTextMessage Error returned nil, want error")
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want APIError", err, err)
	}
	if apiErr.Body != "bad request" {
		t.Fatalf("APIError body = %#v, want provider detail", apiErr.Body)
	}
}

func TestDeepgramTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	closeCalls := 0
	stream := &deepgramTTSStream{
		writeJSON: func(any) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("hello"); !errors.Is(err, writeErr) {
		t.Fatalf("PushText error = %v, want write error", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
	}
}

func TestDeepgramTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	closed := make(chan struct{})
	closeAfterHandshake := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramClosingWebsocketServer(serverConn, closeAfterHandshake, closed, serverErr)

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

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	close(closeAfterHandshake)

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

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next() error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode == 0 {
		t.Fatalf("status code = %d, want close status or -1", statusErr.StatusCode)
	}
}

func assertDeepgramTTSQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

type deepgramTTSReadCloser struct {
	err error
}

func (r deepgramTTSReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r deepgramTTSReadCloser) Close() error {
	return nil
}
