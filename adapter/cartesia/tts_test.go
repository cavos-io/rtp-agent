package cartesia

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

type cartesiaRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f cartesiaRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCartesiaTTSDefaultsMatchReference(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "", "")

	if provider.voiceID != "f786b574-daa5-4673-aa0c-cbe3e8534c02" {
		t.Fatalf("voiceID = %q, want reference default", provider.voiceID)
	}
	if provider.model != "sonic-3" {
		t.Fatalf("model = %q, want sonic-3", provider.model)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.apiVersion != "2025-04-16" {
		t.Fatalf("api version = %q, want 2025-04-16", provider.apiVersion)
	}
	if !provider.Capabilities().AlignedTranscript {
		t.Fatalf("AlignedTranscript = false, want true when word timestamps are enabled")
	}
	if got := tts.Model(provider); got != "sonic-3" {
		t.Fatalf("model metadata = %q, want sonic-3", got)
	}
	if got := tts.Provider(provider); got != "Cartesia" {
		t.Fatalf("provider metadata = %q, want Cartesia", got)
	}
}

func TestCartesiaTTSConstructorOptionsMatchReference(t *testing.T) {
	t.Setenv("CARTESIA_API_KEY", "env-key")

	provider := NewCartesiaTTS("", "voice-1", "sonic-custom",
		WithCartesiaLanguage("es"),
		WithCartesiaAudioFormat("pcm_mulaw", 8000),
		WithCartesiaAPIVersion("2025-01-01"),
		WithCartesiaWordTimestamps(false),
	)
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}
	if provider.language != "es" {
		t.Fatalf("language = %q, want es", provider.language)
	}
	if provider.encoding != "pcm_mulaw" || provider.sampleRate != 8000 {
		t.Fatalf("audio format = %s/%d, want pcm_mulaw/8000", provider.encoding, provider.sampleRate)
	}
	if provider.apiVersion != "2025-01-01" {
		t.Fatalf("apiVersion = %q, want configured version", provider.apiVersion)
	}
	if provider.Capabilities().AlignedTranscript {
		t.Fatalf("AlignedTranscript = true, want false when word timestamps are disabled")
	}

	msg := buildCartesiaStreamInitMessage(provider)
	if msg["language"] != "es" {
		t.Fatalf("language = %#v, want es", msg["language"])
	}
	if msg["add_timestamps"] != false {
		t.Fatalf("add_timestamps = %#v, want false", msg["add_timestamps"])
	}
	outputFormat := msg["output_format"].(map[string]interface{})
	if outputFormat["encoding"] != "pcm_mulaw" || outputFormat["sample_rate"] != 8000 {
		t.Fatalf("output_format = %+v, want configured encoding/sample rate", outputFormat)
	}

	provider = NewCartesiaTTS("explicit-key", "", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestCartesiaSynthesizeRequestUsesReferenceOptions(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "", "",
		WithCartesiaSpeed(1.2),
		WithCartesiaEmotion("Happy"),
		WithCartesiaVolume(1.1),
		WithCartesiaPronunciationDictID("dict-1"),
	)

	requestURL, body, err := buildCartesiaSynthesizeRequest(provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Path != "/tts/bytes" {
		t.Fatalf("path = %q, want /tts/bytes", parsed.Path)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["model_id"] != "sonic-3" {
		t.Fatalf("model_id = %#v, want sonic-3", payload["model_id"])
	}
	if payload["transcript"] != "hello" {
		t.Fatalf("transcript = %#v, want hello", payload["transcript"])
	}
	if payload["language"] != "en" {
		t.Fatalf("language = %#v, want en", payload["language"])
	}
	if payload["pronunciation_dict_id"] != "dict-1" {
		t.Fatalf("pronunciation_dict_id = %#v, want dict-1", payload["pronunciation_dict_id"])
	}

	generationConfig, ok := payload["generation_config"].(map[string]any)
	if !ok {
		t.Fatalf("generation_config = %#v, want map", payload["generation_config"])
	}
	if generationConfig["speed"] != 1.2 {
		t.Fatalf("speed = %#v, want 1.2", generationConfig["speed"])
	}
	if generationConfig["emotion"] != "Happy" {
		t.Fatalf("emotion = %#v, want Happy", generationConfig["emotion"])
	}
	if generationConfig["volume"] != 1.1 {
		t.Fatalf("volume = %#v, want 1.1", generationConfig["volume"])
	}
}

func TestCartesiaSynthesizeRequestSupportsVoiceEmbedding(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "voice-id", "",
		WithCartesiaVoiceEmbedding([]float64{0.1, 0.2, 0.3}),
	)

	_, body, err := buildCartesiaSynthesizeRequest(provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	voice := payload["voice"].(map[string]any)
	if voice["mode"] != "embedding" {
		t.Fatalf("voice mode = %#v, want embedding", voice["mode"])
	}
	if _, ok := voice["id"]; ok {
		t.Fatalf("voice id = %#v, want omitted for embedding voice", voice["id"])
	}
	embedding := voice["embedding"].([]any)
	if len(embedding) != 3 || embedding[0] != 0.1 || embedding[1] != 0.2 || embedding[2] != 0.3 {
		t.Fatalf("embedding = %+v, want configured vector", embedding)
	}
}

func TestCartesiaTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("CARTESIA_API_KEY", "")
	provider := NewCartesiaTTS("", "", "", WithCartesiaBaseURL("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "CARTESIA_API_KEY") {
		t.Fatalf("Synthesize error = %q, want CARTESIA_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "CARTESIA_API_KEY") {
		t.Fatalf("Stream error = %q, want CARTESIA_API_KEY guidance", err)
	}
}

func TestCartesiaTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: cartesiaRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/tts/bytes" {
			t.Fatalf("path = %q, want /tts/bytes", req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	defer func() {
		http.DefaultClient = oldClient
	}()

	provider := NewCartesiaTTS("test-key", "", "", WithCartesiaBaseURL("https://cartesia.test"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	body, ok := statusErr.Body.(string)
	if !ok || !strings.Contains(body, "rate limited") {
		t.Fatalf("body = %#v, want provider error body", statusErr.Body)
	}
}

func TestCartesiaTTSSynthesizeReturnsAPITimeoutError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: cartesiaRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	defer func() {
		http.DefaultClient = oldClient
	}()

	provider := NewCartesiaTTS("test-key", "", "", WithCartesiaBaseURL("https://cartesia.test"))

	_, err := provider.Synthesize(context.Background(), "hello")
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Synthesize error = %T %v, want APITimeoutError", err, err)
	}
}

func TestCartesiaTTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	transportErr := errors.New("dial failed")
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: cartesiaRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, transportErr
	})}
	defer func() {
		http.DefaultClient = oldClient
	}()

	provider := NewCartesiaTTS("test-key", "", "", WithCartesiaBaseURL("https://cartesia.test"))

	_, err := provider.Synthesize(context.Background(), "hello")
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Synthesize error = %T %v, want APIConnectionError", err, err)
	}
}

func TestCartesiaSynthesizeRequestUsesConfiguredBaseURL(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "", "",
		WithCartesiaBaseURL("https://cartesia.example"),
	)

	requestURL, _, err := buildCartesiaSynthesizeRequest(provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "cartesia.example" {
		t.Fatalf("url = %q, want configured base URL", requestURL)
	}
	if parsed.Path != "/tts/bytes" {
		t.Fatalf("path = %q, want /tts/bytes", parsed.Path)
	}
}

func TestCartesiaStreamInitMessageUsesReferenceOptions(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "", "")

	msg := buildCartesiaStreamInitMessage(provider)

	if msg["model_id"] != "sonic-3" {
		t.Fatalf("model_id = %#v, want sonic-3", msg["model_id"])
	}
	if msg["language"] != "en" {
		t.Fatalf("language = %#v, want en", msg["language"])
	}
	if msg["add_timestamps"] != true {
		t.Fatalf("add_timestamps = %#v, want true", msg["add_timestamps"])
	}
}

func TestCartesiaTTSStreamFlushUsesReferenceEndPacket(t *testing.T) {
	var writes []map[string]any
	stream := &cartesiaTTSStream{
		writeJSON: func(msg any) error {
			payload, ok := msg.(map[string]interface{})
			if !ok {
				t.Fatalf("message = %T, want map[string]interface{}", msg)
			}
			writes = append(writes, payload)
			return nil
		},
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	if len(writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(writes))
	}
	if writes[0]["context_id"] != "default" {
		t.Fatalf("context_id = %#v, want default", writes[0]["context_id"])
	}
	if writes[0]["transcript"] != " " {
		t.Fatalf("transcript = %#v, want single space reference end packet", writes[0]["transcript"])
	}
	if writes[0]["continue"] != false {
		t.Fatalf("continue = %#v, want false", writes[0]["continue"])
	}
}

func TestCartesiaTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	stream := &cartesiaTTSStream{
		writeJSON: func(any) error {
			return writeErr
		},
	}

	err := stream.PushText("hello")
	if !errors.Is(err, writeErr) {
		t.Fatalf("PushText error = %v, want write failure", err)
	}
	if !stream.closed {
		t.Fatal("closed = false after write failure, want true")
	}

	err = stream.PushText("again")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second PushText error = %v, want io.ErrClosedPipe", err)
	}
}

func TestCartesiaTTSUnexpectedNormalCloseReturnsAPIConnectionError(t *testing.T) {
	closed := make(chan struct{})
	closeAfterHandshake := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runCartesiaReadThenNormalCloseWebsocketServer(serverConn, closeAfterHandshake, closed, serverErr)

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

	provider := NewCartesiaTTS("test-key", "", "", WithCartesiaBaseURL("http://cartesia.test"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	close(closeAfterHandshake)

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
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestCartesiaTTSStreamProviderErrorReturnsAPIConnectionError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runCartesiaReadThenErrorWebsocketServer(serverConn, serverErr)

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

	provider := NewCartesiaTTS("test-key", "", "", WithCartesiaBaseURL("http://cartesia.test"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider error server")
	}
}

func runCartesiaReadThenNormalCloseWebsocketServer(conn net.Conn, closeAfterRead <-chan struct{}, closed chan<- struct{}, errCh chan<- error) {
	upgrader := websocket.Upgrader{}
	listener := &singleCartesiaConnListener{conn: conn}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				errCh <- err
				return
			}
			defer ws.Close()
			if _, _, err := ws.ReadMessage(); err != nil {
				errCh <- err
				return
			}
			<-closeAfterRead
			err = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			close(closed)
			errCh <- err
		}),
	}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		errCh <- err
	}
}

func runCartesiaReadThenErrorWebsocketServer(conn net.Conn, errCh chan<- error) {
	upgrader := websocket.Upgrader{}
	listener := &singleCartesiaConnListener{conn: conn}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				errCh <- err
				return
			}
			defer ws.Close()
			if _, _, err := ws.ReadMessage(); err != nil {
				errCh <- err
				return
			}
			errCh <- ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","error":"bad stream"}`))
		}),
	}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		errCh <- err
	}
}

func TestCartesiaWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "", "",
		WithCartesiaBaseURL("https://cartesia.example"),
	)

	streamURL := buildCartesiaStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "cartesia.example" {
		t.Fatalf("stream URL = %q, want configured websocket host", streamURL)
	}
	if parsed.Path != "/tts/websocket" {
		t.Fatalf("path = %q, want /tts/websocket", parsed.Path)
	}
	if parsed.Query().Get("cartesia_version") != "2025-04-16" {
		t.Fatalf("cartesia_version = %q, want 2025-04-16", parsed.Query().Get("cartesia_version"))
	}
	if parsed.Query().Get("api_key") != "" {
		t.Fatalf("api_key query = %q, want API key in header only", parsed.Query().Get("api_key"))
	}

	headers := buildCartesiaStreamHeaders(provider)
	if headers.Get("X-API-Key") != "test-key" {
		t.Fatalf("X-API-Key = %q, want test-key", headers.Get("X-API-Key"))
	}
	if headers.Get("Cartesia-Version") != "2025-04-16" {
		t.Fatalf("Cartesia-Version = %q, want 2025-04-16", headers.Get("Cartesia-Version"))
	}
	if headers.Get("User-Agent") == "" {
		t.Fatal("User-Agent header missing")
	}
}
