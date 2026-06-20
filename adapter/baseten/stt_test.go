package baseten

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestBasetenSTTDefaultsMatchReferenceOptions(t *testing.T) {
	provider := mustNewBasetenSTT(t, "test-key", "model-id")

	if provider.modelEndpoint != "wss://model-model-id.api.baseten.co/environments/production/websocket" {
		t.Fatalf("endpoint = %q, want generated truss websocket endpoint", provider.modelEndpoint)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.bufferSizeSeconds != 0.032 {
		t.Fatalf("buffer size = %.3f, want 0.032", provider.bufferSizeSeconds)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if !provider.enablePartialTranscripts || !provider.showWordTimestamps {
		t.Fatalf("partial=%v word timestamps=%v, want both true", provider.enablePartialTranscripts, provider.showWordTimestamps)
	}
	if provider.Label() != "baseten.STT" {
		t.Fatalf("Label = %q, want baseten.STT", provider.Label())
	}
	if provider.Provider() != "Baseten" || provider.Model() != "unknown" {
		t.Fatalf("metadata = %q/%q, want Baseten/unknown", provider.Provider(), provider.Model())
	}

	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize || caps.AlignedTranscript != "word" {
		t.Fatalf("capabilities = %+v, want streaming interim word-aligned only", caps)
	}
}

func TestBasetenSTTExposesReferenceInputSampleRate(t *testing.T) {
	provider := mustNewBasetenSTT(t, "test-key", "model-id")

	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate = %d, want 16000", got)
	}
}

func TestBasetenSTTExposesConfiguredInputSampleRate(t *testing.T) {
	provider := mustNewBasetenSTT(t, "test-key", "model-id", WithBasetenSTTSampleRate(8000))

	if got := provider.InputSampleRate(); got != 8000 {
		t.Fatalf("InputSampleRate = %d, want 8000", got)
	}
}

func TestBasetenSTTEndpointOptionsMatchReferencePriority(t *testing.T) {
	explicit := mustNewBasetenSTT(t, "test-key", "ignored",
		WithBasetenSTTModelEndpoint("wss://explicit.example/websocket"),
		WithBasetenSTTChainID("chain-1"),
	)
	if explicit.modelEndpoint != "wss://explicit.example/websocket" {
		t.Fatalf("explicit endpoint = %q, want highest priority endpoint", explicit.modelEndpoint)
	}

	model := mustNewBasetenSTT(t, "test-key", "model-1",
		WithBasetenSTTChainID("chain-1"),
	)
	if model.modelEndpoint != "wss://model-model-1.api.baseten.co/environments/production/websocket" {
		t.Fatalf("model endpoint = %q, want model to outrank chain", model.modelEndpoint)
	}

	chain := mustNewBasetenSTT(t, "test-key", "",
		WithBasetenSTTChainID("chain-1"),
	)
	if chain.modelEndpoint != "wss://chain-chain-1.api.baseten.co/environments/production/websocket" {
		t.Fatalf("chain endpoint = %q, want generated chain endpoint", chain.modelEndpoint)
	}
}

func TestNewBasetenSTTFallsBackToEnvironment(t *testing.T) {
	t.Setenv(basetenAPIKeyEnv, "env-key")
	t.Setenv(basetenModelEndpointEnv, "wss://env.example/websocket")

	provider, err := NewBasetenSTT("", "")
	if err != nil {
		t.Fatalf("NewBasetenSTT error = %v, want env fallback", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	if provider.modelEndpoint != "wss://env.example/websocket" {
		t.Fatalf("endpoint = %q, want env endpoint", provider.modelEndpoint)
	}
}

func TestNewBasetenSTTRequiresAPIKeyAndEndpoint(t *testing.T) {
	t.Setenv(basetenAPIKeyEnv, "")
	t.Setenv(basetenModelEndpointEnv, "")

	_, err := NewBasetenSTT("", "model-id")
	if err == nil || !strings.Contains(err.Error(), "BASETEN_API_KEY") {
		t.Fatalf("missing key error = %v, want API key error", err)
	}

	_, err = NewBasetenSTT("test-key", "")
	if err == nil || !strings.Contains(err.Error(), "BASETEN_MODEL_ENDPOINT") {
		t.Fatalf("missing endpoint error = %v, want endpoint error", err)
	}
}

func TestBuildBasetenSTTMetadataMatchesReferenceSchema(t *testing.T) {
	provider := mustNewBasetenSTT(t, "test-key", "model-id",
		WithBasetenSTTLanguage("auto"),
		WithBasetenSTTEncoding("pcm_mulaw"),
		WithBasetenSTTSampleRate(8000),
		WithBasetenSTTBufferSizeSeconds(0.064),
		WithBasetenSTTVADThreshold(0.7),
	)

	metadata := buildBasetenSTTMetadata(provider)
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}

	whisper := decoded["whisper_params"].(map[string]any)
	if whisper["audio_language"] != "auto" || whisper["show_word_timestamps"] != true {
		t.Fatalf("whisper params = %+v, want language and timestamps", whisper)
	}
	streaming := decoded["streaming_params"].(map[string]any)
	if streaming["encoding"] != "pcm_mulaw" || streaming["sample_rate"] != float64(8000) {
		t.Fatalf("streaming params = %+v, want encoding and sample rate", streaming)
	}
	if streaming["partial_transcript_interval_s"] != float64(1.0) {
		t.Fatalf("streaming params = %+v, want partial transcript interval", streaming)
	}
	if streaming["enable_partial_transcripts"] != true || streaming["final_transcript_max_duration_s"] != float64(30) {
		t.Fatalf("streaming params = %+v, want partial and final duration defaults", streaming)
	}
	vad := decoded["streaming_vad_config"].(map[string]any)
	if vad["threshold"] != float64(0.7) || vad["min_silence_duration_ms"] != float64(300) || vad["speech_pad_ms"] != float64(30) {
		t.Fatalf("vad config = %+v, want reference vad values", vad)
	}
}

func TestBasetenSTTTranscriptEventsMapReferenceMessages(t *testing.T) {
	state := &basetenSTTStreamState{language: "en", startTimeOffset: 1.5}
	events, err := processBasetenSTTMessage(state, []byte(`{
		"type":"transcription",
		"is_final":false,
		"transcript":"hello",
		"confidence":0.75,
		"segments":[{"text":"hello","start_time":0.1,"end_time":0.4,
			"word_timestamps":[{"word":"hello","start_time":0.1,"end_time":0.4}]}]
	}`))
	if err != nil {
		t.Fatalf("process interim: %v", err)
	}
	assertBasetenSTTEvent(t, events, 0, stt.SpeechEventInterimTranscript, "hello")
	if events[0].Alternatives[0].Words[0].StartTime != 1.6 {
		t.Fatalf("word start = %.1f, want offset applied", events[0].Alternatives[0].Words[0].StartTime)
	}

	events, err = processBasetenSTTMessage(state, []byte(`{
		"type":"transcription",
		"is_final":true,
		"language_code":"es",
		"segments":[{"text":"hola","start_time":0.2,"end_time":0.5}]
	}`))
	if err != nil {
		t.Fatalf("process final: %v", err)
	}
	assertBasetenSTTEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hola")
	if events[0].Alternatives[0].Language != "es" {
		t.Fatalf("language = %q, want es", events[0].Alternatives[0].Language)
	}
}

func TestBasetenSTTRecognizeIsUnsupportedLikeReference(t *testing.T) {
	provider := mustNewBasetenSTT(t, "test-key", "model-id")

	_, err := provider.Recognize(context.Background(), nil, "")

	if err == nil || !strings.Contains(err.Error(), "does not support offline recognize") {
		t.Fatalf("error = %v, want offline recognize unsupported", err)
	}
}

func TestBasetenSTTStreamSendsReferenceMetadataAndAudio(t *testing.T) {
	metadataCh := make(chan map[string]any, 1)
	audioCh := make(chan []byte, 1)
	terminateCh := make(chan string, 1)
	errCh := make(chan error, 1)
	dialer := newBasetenSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Api-Key test-key" {
			t.Errorf("Authorization = %q, want Api-Key header", got)
		}
		_, metadataPayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		var metadata map[string]any
		if err := json.Unmarshal(metadataPayload, &metadata); err != nil {
			errCh <- err
			return
		}
		metadataCh <- metadata
		msgType, audioPayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		if msgType != websocket.BinaryMessage {
			t.Errorf("audio message type = %d, want binary", msgType)
		}
		audioCh <- append([]byte(nil), audioPayload...)
		_, terminatePayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		terminateCh <- string(terminatePayload)
	})

	provider := mustNewBasetenSTT(t, "test-key", "",
		WithBasetenSTTModelEndpoint("ws://baseten.test/websocket"),
		dialer,
		WithBasetenSTTLanguage("auto"),
		WithBasetenSTTSampleRate(8000),
	)
	stream, err := provider.Stream(context.Background(), "es")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("pcm")}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	metadata := readBasetenTestChan(t, metadataCh, errCh)
	whisper := metadata["whisper_params"].(map[string]any)
	assertBasetenPayload(t, whisper, "audio_language", "es")
	streaming := metadata["streaming_params"].(map[string]any)
	assertBasetenPayload(t, streaming, "sample_rate", float64(8000))
	if got := readBasetenTestChan(t, audioCh, errCh); string(got) != "pcm" {
		t.Fatalf("audio payload = %q, want pcm", string(got))
	}
	if got := readBasetenTestChan(t, terminateCh, errCh); got != `{"terminate_session":true}` {
		t.Fatalf("terminate payload = %q, want terminate_session", got)
	}
}

func TestBasetenSTTPushFrameBuffersReferenceAudioChunks(t *testing.T) {
	audioCh := make(chan []byte, 2)
	terminateCh := make(chan string, 1)
	errCh := make(chan error, 1)
	dialer := newBasetenSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			errCh <- err
			return
		}
		for i := 0; i < 2; i++ {
			msgType, audioPayload, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if msgType != websocket.BinaryMessage {
				t.Errorf("audio message type = %d, want binary", msgType)
			}
			audioCh <- append([]byte(nil), audioPayload...)
		}
		_, terminatePayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		terminateCh <- string(terminatePayload)
	})

	provider := mustNewBasetenSTT(t, "test-key", "",
		WithBasetenSTTModelEndpoint("ws://baseten.test/websocket"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: bytes.Repeat([]byte{1}, 1536)}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	if got := readBasetenTestChan(t, audioCh, errCh); len(got) != 1024 {
		t.Fatalf("first audio chunk len = %d, want 1024", len(got))
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if got := readBasetenTestChan(t, audioCh, errCh); len(got) != 512 {
		t.Fatalf("flush audio chunk len = %d, want 512", len(got))
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if got := readBasetenTestChan(t, terminateCh, errCh); got != `{"terminate_session":true}` {
		t.Fatalf("terminate payload = %q, want terminate_session", got)
	}
}

func TestBasetenSTTStreamMapsWebsocketTranscripts(t *testing.T) {
	dialer := newBasetenSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read metadata: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{
			"type":"transcription",
			"is_final":true,
			"language_code":"en",
			"transcript":"hello"
		}`)); err != nil {
			t.Errorf("write transcript: %v", err)
			return
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	})

	provider := mustNewBasetenSTT(t, "test-key", "",
		WithBasetenSTTModelEndpoint("ws://baseten.test/websocket"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want transcript", err)
	}
	assertBasetenSTTEvent(t, []*stt.SpeechEvent{event}, 0, stt.SpeechEventFinalTranscript, "hello")

	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("second Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != websocket.CloseNormalClosure {
		t.Fatalf("status code = %d, want normal close", statusErr.StatusCode)
	}
}

func TestBasetenSTTProviderCloseClosesActiveStreams(t *testing.T) {
	terminateCh := make(chan string, 1)
	errCh := make(chan error, 1)
	dialer := newBasetenSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			errCh <- err
			return
		}
		_, terminatePayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		terminateCh <- string(terminatePayload)
	})

	provider := mustNewBasetenSTT(t, "test-key", "",
		WithBasetenSTTModelEndpoint("ws://baseten.test/websocket"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close error = %v", err)
	}

	if got := readBasetenTestChan(t, terminateCh, errCh); got != `{"terminate_session":true}` {
		t.Fatalf("terminate payload = %q, want terminate_session", got)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("pcm")}); err == nil {
		t.Fatal("PushFrame error = nil, want closed stream error")
	}
}

func TestBasetenSTTUnexpectedNormalCloseReturnsAPIStatusError(t *testing.T) {
	dialer := newBasetenSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read metadata: %v", err)
			return
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second))
	})

	provider := mustNewBasetenSTT(t, "test-key", "",
		WithBasetenSTTModelEndpoint("ws://baseten.test/websocket"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != websocket.CloseNormalClosure {
		t.Fatalf("status code = %d, want normal close", statusErr.StatusCode)
	}
	if !strings.Contains(statusErr.Message, "Baseten connection closed unexpectedly") {
		t.Fatalf("message = %q, want unexpected close context", statusErr.Message)
	}
}

func TestBasetenSTTStreamDialErrorReturnsFailure(t *testing.T) {
	provider := mustNewBasetenSTT(t, "test-key", "",
		WithBasetenSTTModelEndpoint("ws://baseten.test/websocket"),
		withBasetenSTTWebsocketDialer(func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
			return nil, nil, errors.New("dial failed")
		}),
	)
	if _, err := provider.Stream(context.Background(), ""); err == nil {
		t.Fatal("Stream error = nil, want dial failure")
	}
}

func mustNewBasetenSTT(t *testing.T, apiKey string, model string, opts ...BasetenSTTOption) *BasetenSTT {
	t.Helper()
	provider, err := NewBasetenSTT(apiKey, model, opts...)
	if err != nil {
		t.Fatalf("NewBasetenSTT error = %v", err)
	}
	return provider
}

func newBasetenSTTTestWebsocketDialer(t *testing.T, handler func(*websocket.Conn, *http.Request)) BasetenSTTOption {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return withBasetenSTTWebsocketDialer(func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
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

type basetenSingleConnListener struct {
	mu     sync.Mutex
	once   sync.Once
	conn   net.Conn
	closed chan struct{}
}

func newBasetenSingleConnListener(conn net.Conn) *basetenSingleConnListener {
	return &basetenSingleConnListener{
		conn:   conn,
		closed: make(chan struct{}),
	}
}

func (l *basetenSingleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.conn != nil {
		conn := l.conn
		l.conn = nil
		l.mu.Unlock()
		return conn, nil
	}
	l.mu.Unlock()

	<-l.closed
	return nil, net.ErrClosed
}

func (l *basetenSingleConnListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		l.mu.Lock()
		if l.conn != nil {
			_ = l.conn.Close()
			l.conn = nil
		}
		l.mu.Unlock()
	})
	return nil
}

func (l *basetenSingleConnListener) Addr() net.Addr {
	return basetenDummyAddr("pipe")
}

type basetenDummyAddr string

func (a basetenDummyAddr) Network() string { return string(a) }
func (a basetenDummyAddr) String() string  { return string(a) }

func assertBasetenSTTEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
	t.Helper()
	if len(events) <= index {
		t.Fatalf("events length = %d, missing index %d", len(events), index)
	}
	if events[index].Type != eventType {
		t.Fatalf("event type = %v, want %v", events[index].Type, eventType)
	}
	if len(events[index].Alternatives) != 1 || events[index].Alternatives[0].Text != text {
		t.Fatalf("alternatives = %+v, want text %q", events[index].Alternatives, text)
	}
}
