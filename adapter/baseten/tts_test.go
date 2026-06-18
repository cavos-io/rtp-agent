package baseten

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestBasetenTTSDefaultsMatchReferenceOptions(t *testing.T) {
	provider := mustNewBasetenTTS(t, "test-key", "model-id")

	if provider.modelEndpoint != "https://model-model-id.api.baseten.co/environments/production/predict" {
		t.Fatalf("endpoint = %q, want generated model predict endpoint", provider.modelEndpoint)
	}
	if provider.voice != "tara" {
		t.Fatalf("voice = %q, want tara", provider.voice)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.temperature != 0.6 {
		t.Fatalf("temperature = %.1f, want 0.6", provider.temperature)
	}
	if provider.maxTokens != 2000 {
		t.Fatalf("max tokens = %d, want 2000", provider.maxTokens)
	}
	if provider.bufferSize != 10 {
		t.Fatalf("buffer size = %d, want 10", provider.bufferSize)
	}
	if provider.Capabilities().Streaming {
		t.Fatalf("streaming = true for https endpoint, want false")
	}
	if provider.Label() != "baseten.TTS" {
		t.Fatalf("Label = %q, want baseten.TTS", provider.Label())
	}
	if provider.Provider() != "Baseten" || provider.Model() != "unknown" {
		t.Fatalf("metadata = %q/%q, want Baseten/unknown", provider.Provider(), provider.Model())
	}
	if provider.SampleRate() != 24000 || provider.NumChannels() != 1 {
		t.Fatalf("audio format = %d/%d, want 24000/1", provider.SampleRate(), provider.NumChannels())
	}
}

func TestBasetenTTSWebSocketEndpointReportsStreamingCapability(t *testing.T) {
	provider := mustNewBasetenTTS(t, "test-key", "",
		WithBasetenTTSModelEndpoint("wss://model.example/websocket"),
	)

	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false for websocket endpoint, want true")
	}
}

func TestNewBasetenTTSFallsBackToEnvironment(t *testing.T) {
	t.Setenv(basetenAPIKeyEnv, "env-key")
	t.Setenv(basetenModelEndpointEnv, "https://env.example/predict")

	provider, err := NewBasetenTTS("", "")
	if err != nil {
		t.Fatalf("NewBasetenTTS error = %v, want env fallback", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	if provider.modelEndpoint != "https://env.example/predict" {
		t.Fatalf("endpoint = %q, want env endpoint", provider.modelEndpoint)
	}
}

func TestNewBasetenTTSRequiresAPIKeyAndEndpoint(t *testing.T) {
	t.Setenv(basetenAPIKeyEnv, "")
	t.Setenv(basetenModelEndpointEnv, "")

	_, err := NewBasetenTTS("", "model-id")
	if err == nil || !strings.Contains(err.Error(), "BASETEN_API_KEY") {
		t.Fatalf("missing key error = %v, want API key error", err)
	}

	_, err = NewBasetenTTS("test-key", "")
	if err == nil || !strings.Contains(err.Error(), "BASETEN_MODEL_ENDPOINT") {
		t.Fatalf("missing endpoint error = %v, want endpoint error", err)
	}
}

func TestBuildBasetenTTSRequestMatchesReferencePayload(t *testing.T) {
	provider := mustNewBasetenTTS(t, "test-key", "",
		WithBasetenTTSModelEndpoint("https://model.example/predict"),
		WithBasetenTTSVoice("emma"),
		WithBasetenTTSLanguage("es"),
		WithBasetenTTSTemperature(0.8),
	)

	req, err := buildBasetenTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if req.URL.String() != "https://model.example/predict" {
		t.Fatalf("URL = %q, want configured endpoint", req.URL.String())
	}
	if req.Header.Get("Authorization") != "Api-Key test-key" {
		t.Fatalf("Authorization = %q, want Api-Key header", req.Header.Get("Authorization"))
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	assertBasetenPayload(t, payload, "prompt", "hello")
	assertBasetenPayload(t, payload, "voice", "emma")
	assertBasetenPayload(t, payload, "language", "es")
	assertBasetenPayload(t, payload, "temperature", float64(0.8))
	if _, ok := payload["text"]; ok {
		t.Fatalf("payload still uses legacy text field: %+v", payload)
	}
}

func TestBasetenTTSSynthesizePostsReferencePayloadAndReturnsChunks(t *testing.T) {
	var gotAuth string
	var payload map[string]any
	client := basetenTTSRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("pcm")),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})

	provider := mustNewBasetenTTS(t, "test-key", "",
		WithBasetenTTSModelEndpoint("https://baseten.test/predict"),
		WithBasetenTTSVoice("emma"),
		WithBasetenTTSLanguage("es"),
		WithBasetenTTSTemperature(0.8),
		withBasetenTTSHTTPClient(client),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	if gotAuth != "Api-Key test-key" {
		t.Fatalf("Authorization = %q, want Api-Key header", gotAuth)
	}
	assertBasetenPayload(t, payload, "prompt", "hello")
	assertBasetenPayload(t, payload, "voice", "emma")
	assertBasetenPayload(t, payload, "language", "es")
	assertBasetenPayload(t, payload, "temperature", float64(0.8))

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want audio", err)
	}
	if string(audio.Frame.Data) != "pcm" {
		t.Fatalf("audio = %q, want pcm", string(audio.Frame.Data))
	}
}

func TestBasetenTTSSynthesizeReturnsHTTPErrorBody(t *testing.T) {
	client := basetenTTSRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader("bad request\n")),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})

	provider := mustNewBasetenTTS(t, "test-key", "",
		WithBasetenTTSModelEndpoint("https://baseten.test/predict"),
		withBasetenTTSHTTPClient(client),
	)

	_, err := provider.Synthesize(context.Background(), "hello")

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", statusErr.StatusCode)
	}
	if statusErr.Body != "bad request\n" {
		t.Fatalf("body = %#v, want response body", statusErr.Body)
	}
}

func TestBasetenTTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	client := basetenTTSRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial refused")
	})

	provider := mustNewBasetenTTS(t, "test-key", "",
		WithBasetenTTSModelEndpoint("https://baseten.test/predict"),
		withBasetenTTSHTTPClient(client),
	)

	_, err := provider.Synthesize(context.Background(), "hello")

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Synthesize error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Message, "dial refused") {
		t.Fatalf("message = %q, want transport context", connectionErr.Message)
	}
}

func TestBasetenTTSChunkedStreamReturnsRawAudioChunks(t *testing.T) {
	body := &recordingReadCloser{Reader: strings.NewReader("abcdef")}
	stream := &basetenTTSChunkedStream{
		body:       body,
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if string(audio.Frame.Data) != "abcdef" {
		t.Fatalf("audio data = %q, want raw chunk", string(audio.Frame.Data))
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24 kHz mono", audio.Frame)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second chunk err = %v, want EOF", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !body.closed {
		t.Fatal("body closed = false, want true")
	}
}

func TestBasetenTTSStreamSendsReferenceSetupTextAndEnd(t *testing.T) {
	setupCh := make(chan map[string]any, 1)
	textCh := make(chan string, 1)
	endCh := make(chan string, 1)
	errCh := make(chan error, 1)
	dialer := newBasetenTTSTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Api-Key test-key" {
			t.Errorf("Authorization = %q, want Api-Key header", got)
		}
		_, setupPayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		var setup map[string]any
		if err := json.Unmarshal(setupPayload, &setup); err != nil {
			errCh <- err
			return
		}
		setupCh <- setup
		_, textPayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		textCh <- string(textPayload)
		_, endPayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		endCh <- string(endPayload)
	})

	provider := mustNewBasetenTTS(t, "test-key", "",
		WithBasetenTTSModelEndpoint("ws://baseten.test/websocket"),
		WithBasetenTTSVoice("emma"),
		WithBasetenTTSMaxTokens(512),
		WithBasetenTTSBufferSize(4),
		dialer,
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := stream.PushText("hello "); err != nil {
		t.Fatalf("push text: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	setup := readBasetenTestChan(t, setupCh, errCh)
	assertBasetenPayload(t, setup, "voice", "emma")
	assertBasetenPayload(t, setup, "max_tokens", float64(512))
	assertBasetenPayload(t, setup, "buffer_size", float64(4))
	if got := readBasetenTestChan(t, textCh, errCh); got != "hello " {
		t.Fatalf("text message = %q, want raw text", got)
	}
	if got := readBasetenTestChan(t, endCh, errCh); got != "__END__" {
		t.Fatalf("end message = %q, want sentinel", got)
	}
}

func TestBasetenTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &basetenTTSSynthesizeStream{
		cancel: func() { cancelled = true },
		writeMessage: func(int, []byte) error {
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
	if !cancelled {
		t.Fatal("cancel not called after write failure")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PushText after write failure error = %v, want closed stream error", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
	}
}

func TestBasetenTTSStreamReturnsBinaryAudioFrames(t *testing.T) {
	dialer := newBasetenTTSTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read setup: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03, 0x04}); err != nil {
			t.Errorf("write audio: %v", err)
			return
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	})

	provider := mustNewBasetenTTS(t, "test-key", "",
		WithBasetenTTSModelEndpoint("ws://baseten.test/websocket"),
		dialer,
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if string(audio.Frame.Data) != "\x01\x02\x03\x04" {
		t.Fatalf("audio data = %#v, want binary websocket payload", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame = %+v, want 24 kHz mono PCM", audio.Frame)
	}
	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second next err = %v, want EOF", err)
	}
}

func TestBasetenTTSStreamingOptionsMatchReference(t *testing.T) {
	provider := mustNewBasetenTTS(t, "test-key", "",
		WithBasetenTTSModelEndpoint("wss://model.example/websocket"),
		WithBasetenTTSVoice("emma"),
		WithBasetenTTSMaxTokens(123),
		WithBasetenTTSBufferSize(4),
	)

	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false for websocket endpoint")
	}

	headers := buildBasetenTTSWebsocketHeaders(provider)
	if headers.Get("Authorization") != "Api-Key test-key" {
		t.Fatalf("Authorization = %q, want Api-Key header", headers.Get("Authorization"))
	}

	payload, err := buildBasetenTTSStartMessage(provider)
	if err != nil {
		t.Fatalf("build start message: %v", err)
	}
	var start map[string]any
	if err := json.Unmarshal(payload, &start); err != nil {
		t.Fatalf("decode start message: %v", err)
	}
	assertBasetenPayload(t, start, "voice", "emma")
	if start["max_tokens"] != float64(123) || start["buffer_size"] != float64(4) {
		t.Fatalf("start message = %+v, want max_tokens and buffer_size", start)
	}
}

func TestBasetenTTSStreamMessagesMatchReference(t *testing.T) {
	text, err := buildBasetenTTSTextMessage("hello")
	if err != nil {
		t.Fatalf("text message: %v", err)
	}
	if string(text) != "hello" {
		t.Fatalf("text message = %q, want raw text", string(text))
	}

	end, err := buildBasetenTTSEndMessage()
	if err != nil {
		t.Fatalf("end message: %v", err)
	}
	if string(end) != "__END__" {
		t.Fatalf("end message = %q, want sentinel", string(end))
	}
}

func TestBasetenTTSAudioFromStreamMessage(t *testing.T) {
	audio, err := basetenTTSAudioFromStreamMessage([]byte{1, 2, 3, 4}, 24000)
	if err != nil {
		t.Fatalf("audio from stream message: %v", err)
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want raw binary audio frame", audio)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24 kHz mono", audio.Frame)
	}
}

func TestBasetenTTSImplementsStreamingInterface(t *testing.T) {
	provider := mustNewBasetenTTS(t, "test-key", "", WithBasetenTTSModelEndpoint("wss://model.example/websocket"))
	var _ tts.TTS = provider
}

func assertBasetenPayload(t *testing.T, payload map[string]any, key string, want any) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
}

func newBasetenTTSTestWebsocketDialer(t *testing.T, handler func(*websocket.Conn, *http.Request)) BasetenTTSOption {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return withBasetenTTSWebsocketDialer(func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		listener := newBasetenSingleConnListener(serverConn)
		server := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer conn.Close()
				handler(conn, r)
			}),
		}
		serverErrCh := make(chan error, 1)
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				serverErrCh <- err
			}
		}()
		t.Cleanup(func() {
			_ = server.Close()
			_ = listener.Close()
			_ = clientConn.Close()
			_ = serverConn.Close()
		})

		dialer := websocket.Dialer{
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
		}
		conn, response, err := dialer.DialContext(ctx, endpoint, headers)
		select {
		case serverErr := <-serverErrCh:
			if err == nil {
				err = serverErr
			}
		default:
		}
		return conn, response, err
	})
}

func readBasetenTestChan[T any](t *testing.T, ch <-chan T, errCh <-chan error) T {
	t.Helper()
	var zero T
	select {
	case got := <-ch:
		return got
	case err := <-errCh:
		t.Fatalf("websocket server: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket server")
	}
	return zero
}

func TestBasetenTTSStreamDialErrorReturnsFailure(t *testing.T) {
	provider := mustNewBasetenTTS(t, "test-key", "",
		WithBasetenTTSModelEndpoint("ws://baseten.test/websocket"),
		withBasetenTTSWebsocketDialer(func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
			return nil, nil, errors.New("dial failed")
		}),
	)
	if _, err := provider.Stream(context.Background()); err == nil {
		t.Fatal("stream error = nil, want dial failure")
	}
}

func mustNewBasetenTTS(t *testing.T, apiKey string, model string, opts ...BasetenTTSOption) *BasetenTTS {
	t.Helper()
	provider, err := NewBasetenTTS(apiKey, model, opts...)
	if err != nil {
		t.Fatalf("NewBasetenTTS error = %v", err)
	}
	return provider
}

type recordingReadCloser struct {
	io.Reader
	closed bool
}

func (r *recordingReadCloser) Close() error {
	r.closed = true
	return nil
}

type basetenTTSRoundTripFunc func(*http.Request) (*http.Response, error)

func (f basetenTTSRoundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}
