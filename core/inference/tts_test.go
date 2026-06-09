package inference

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/go-jose/go-jose/v3/jwt"
)

func TestNewTTSUsesConfiguredSentenceTokenizer(t *testing.T) {
	tokenizer := &recordingSentenceTokenizer{}

	provider := NewTTS("cartesia/sonic-3", "key", "secret", WithSentenceTokenizer(tokenizer))

	if got := provider.sentenceTokenizer; got != tokenizer {
		t.Fatalf("sentenceTokenizer = %T, want configured tokenizer", got)
	}
}

func TestNewTTSUsesReferenceCredentialEnvFallback(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "base-key")
	t.Setenv("LIVEKIT_API_SECRET", "base-secret")
	t.Setenv("LIVEKIT_INFERENCE_API_KEY", "inference-key")
	t.Setenv("LIVEKIT_INFERENCE_API_SECRET", "inference-secret")

	provider := NewTTS("cartesia/sonic-3", "", "")

	if provider.apiKey != "inference-key" {
		t.Fatalf("apiKey = %q, want inference-key", provider.apiKey)
	}
	if provider.apiSecret != "inference-secret" {
		t.Fatalf("apiSecret = %q, want inference-secret", provider.apiSecret)
	}
}

func TestNewTTSFallsBackToLiveKitCredentials(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "base-key")
	t.Setenv("LIVEKIT_API_SECRET", "base-secret")

	provider := NewTTS("cartesia/sonic-3", "", "")

	if provider.apiKey != "base-key" {
		t.Fatalf("apiKey = %q, want base-key", provider.apiKey)
	}
	if provider.apiSecret != "base-secret" {
		t.Fatalf("apiSecret = %q, want base-secret", provider.apiSecret)
	}
}

func TestInferenceTTSReportsReferenceModelProviderMetadata(t *testing.T) {
	provider := NewTTS("cartesia/sonic-3:voice-id", "key", "secret")

	if got := coretts.Model(provider); got != "cartesia/sonic-3" {
		t.Fatalf("Model = %q, want parsed reference model", got)
	}
	if got := coretts.Provider(provider); got != "livekit" {
		t.Fatalf("Provider = %q, want livekit", got)
	}
}

func TestInferenceTTSDefaultCapabilitiesMatchReferenceAlignment(t *testing.T) {
	provider := NewTTS("cartesia/sonic-3", "key", "secret")

	capabilities := provider.Capabilities()
	if !capabilities.Streaming {
		t.Fatal("Streaming = false, want true")
	}
	if capabilities.AlignedTranscript {
		t.Fatal("AlignedTranscript = true, want false without timestamp options")
	}
}

func TestInferenceTTSAlignedTranscriptMatchesReferenceOptions(t *testing.T) {
	tests := []struct {
		name  string
		model string
		extra map[string]any
	}{
		{
			name:  "cartesia add timestamps",
			model: "cartesia/sonic-3",
			extra: map[string]any{"add_timestamps": true},
		},
		{
			name:  "elevenlabs sync alignment",
			model: "elevenlabs/eleven_flash_v2_5",
			extra: map[string]any{"sync_alignment": true},
		},
		{
			name:  "inworld word timestamps",
			model: "inworld/tts-1",
			extra: map[string]any{"timestamp_type": "WORD"},
		},
		{
			name:  "inworld character timestamps",
			model: "inworld/tts-1",
			extra: map[string]any{"timestamp_type": "CHARACTER"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewTTS(tt.model, "key", "secret", WithTTSExtraKwargs(tt.extra))

			if !provider.Capabilities().AlignedTranscript {
				t.Fatal("AlignedTranscript = false, want true for reference timestamp option")
			}
		})
	}
}

func TestTTSPrewarmReusesConnectionForNextStream(t *testing.T) {
	var connCount atomic.Int32
	sessionCreated := make(chan struct{}, 1)
	provider := NewTTS("cartesia/sonic-3:voice-id", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		if !strings.HasPrefix(endpoint, "wss://inference.test/v1/tts?") {
			t.Errorf("endpoint = %q, want inference TTS websocket endpoint", endpoint)
		}
		if !strings.Contains(endpoint, "model=cartesia%2Fsonic-3") {
			t.Errorf("endpoint = %q, want encoded model query", endpoint)
		}
		if got := header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		connCount.Add(1)
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				if msg["type"] == "session.create" {
					if got := msg["model"]; got != "cartesia/sonic-3" {
						t.Errorf("model = %v, want cartesia/sonic-3", got)
					}
					if got := msg["voice"]; got != "voice-id" {
						t.Errorf("voice = %v, want voice-id", got)
					}
					select {
					case sessionCreated <- struct{}{}:
					default:
					}
				}
			},
		}, nil
	}

	provider.Prewarm()
	select {
	case <-sessionCreated:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prewarmed session.create")
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if got := connCount.Load(); got != 1 {
		t.Fatalf("connections = %d, want 1 prewarmed connection reused by Stream", got)
	}
}

func TestTTSWebsocketSendsReferenceInferenceHeaders(t *testing.T) {
	var captured http.Header
	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		captured = header.Clone()
		return &recordingTTSConn{}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if !strings.HasPrefix(captured.Get("User-Agent"), "LiveKit Agents/") {
		t.Fatalf("User-Agent = %q, want LiveKit Agents version prefix", captured.Get("User-Agent"))
	}
	if !strings.Contains(captured.Get("User-Agent"), " (go ") {
		t.Fatalf("User-Agent = %q, want Go runtime marker", captured.Get("User-Agent"))
	}
}

func TestTTSWebsocketSendsReferenceContextHeaders(t *testing.T) {
	restore := SetContextHeadersProvider(func() map[string]string {
		return map[string]string{
			HeaderRoomID: "RM_tts",
			HeaderJobID:  "job_tts",
		}
	})
	defer restore()

	var captured http.Header
	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		captured = header.Clone()
		return &recordingTTSConn{}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if got := captured.Get(HeaderRoomID); got != "RM_tts" {
		t.Fatalf("%s = %q, want RM_tts", HeaderRoomID, got)
	}
	if got := captured.Get(HeaderJobID); got != "job_tts" {
		t.Fatalf("%s = %q, want job_tts", HeaderJobID, got)
	}
}

type recordingTTSConn struct {
	closed      atomic.Bool
	writeCount  atomic.Int64
	readCh      chan []byte
	writeErr    error
	writeErrAt  int64
	onWriteJSON func(map[string]any)
}

func (c *recordingTTSConn) WriteJSON(v any) error {
	writeCount := c.writeCount.Add(1)
	if c.writeErr != nil && (c.writeErrAt == 0 || writeCount >= c.writeErrAt) {
		return c.writeErr
	}
	if msg, ok := v.(map[string]any); ok && c.onWriteJSON != nil {
		c.onWriteJSON(msg)
	}
	return nil
}

func (c *recordingTTSConn) ReadMessage() (int, []byte, error) {
	if c.readCh != nil {
		msg, ok := <-c.readCh
		if !ok {
			c.closed.Store(true)
			return 0, nil, context.Canceled
		}
		return 1, msg, nil
	}
	for !c.closed.Load() {
		time.Sleep(time.Millisecond)
	}
	return 0, nil, context.Canceled
}

func (c *recordingTTSConn) Close() error {
	c.closed.Store(true)
	if c.readCh != nil {
		select {
		case <-c.readCh:
		default:
		}
	}
	return nil
}

func TestTTSConnectionPoolRefreshesSessionAgeOnGet(t *testing.T) {
	provider := NewTTS("cartesia/sonic-3", "key", "secret")

	pool := reflect.ValueOf(provider.connectionPool()).Elem()
	markRefreshedOnGet := pool.FieldByName("opts").FieldByName("MarkRefreshedOnGet").Bool()
	if !markRefreshedOnGet {
		t.Fatal("connection pool MarkRefreshedOnGet = false, want true")
	}
}

func TestTTSConnectionPoolUsesReferenceMaxSessionDuration(t *testing.T) {
	provider := NewTTS("cartesia/sonic-3", "key", "secret")

	pool := reflect.ValueOf(provider.connectionPool()).Elem()
	got := time.Duration(pool.FieldByName("opts").FieldByName("MaxSessionDuration").Int())
	if got != 5*time.Minute {
		t.Fatalf("MaxSessionDuration = %v, want 5m reference duration", got)
	}
}

func TestInferenceTTSSessionCreateWriteErrorMatchesReference(t *testing.T) {
	conn := &recordingTTSConn{writeErr: errors.New("write failed")}
	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		stream.Close()
	}
	if err == nil {
		t.Fatal("Stream() error = nil, want session.create write error")
	}
	if !strings.Contains(err.Error(), "failed to send session.create message to LiveKit Inference TTS") {
		t.Fatalf("Stream() error = %v, want reference session.create write error", err)
	}
	if !conn.closed.Load() {
		t.Fatal("connection closed = false, want true after session.create write error")
	}
}

func TestInferenceTTSInputTranscriptWriteErrorReturnsNextError(t *testing.T) {
	conn := &recordingTTSConn{
		writeErr:   errors.New("write failed"),
		writeErrAt: 2,
	}
	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	select {
	case err = <-errCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for input write error")
	}
	if err == nil {
		t.Fatal("Next() error = nil, want input write error")
	}
	if !strings.Contains(err.Error(), "failed to send input_transcript message to LiveKit Inference TTS") {
		t.Fatalf("Next() error = %v, want input write error", err)
	}
}

func TestInferenceTTSInputTranscriptIncludesReferenceVoice(t *testing.T) {
	writes := make(chan map[string]any, 4)
	provider := NewTTS("cartesia/sonic-3:voice-id", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				writes <- msg
			},
		}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	var input map[string]any
	deadline := time.After(time.Second)
	for input == nil {
		select {
		case msg := <-writes:
			if msg["type"] == "input_transcript" {
				input = msg
			}
		case <-deadline:
			t.Fatal("timed out waiting for input_transcript")
		}
	}

	config, ok := input["generation_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("generation_config = %#v, want map", input["generation_config"])
	}
	if got := config["voice"]; got != "voice-id" {
		t.Fatalf("generation_config.voice = %#v, want voice-id", got)
	}
}

func TestInferenceTTSInputTranscriptIncludesReferenceExtra(t *testing.T) {
	writes := make(chan map[string]any, 4)
	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				writes <- msg
			},
		}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	var input map[string]any
	deadline := time.After(time.Second)
	for input == nil {
		select {
		case msg := <-writes:
			if msg["type"] == "input_transcript" {
				input = msg
			}
		case <-deadline:
			t.Fatal("timed out waiting for input_transcript")
		}
	}

	extra, ok := input["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("extra = %#v, want map", input["extra"])
	}
	if len(extra) != 0 {
		t.Fatalf("extra = %#v, want empty map", extra)
	}
}

func TestInferenceTTSFlushOnlyFlushesTokenizerUntilEndInput(t *testing.T) {
	writes := make(chan map[string]any, 8)
	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				writes <- msg
			},
		}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	deadline := time.After(time.Second)
	seenInput := false
	for !seenInput {
		select {
		case msg := <-writes:
			if msg["type"] == "session.flush" {
				t.Fatal("Flush() wrote session.flush before input end")
			}
			if msg["type"] == "input_transcript" {
				seenInput = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for input_transcript")
		}
	}

	select {
	case msg := <-writes:
		if msg["type"] == "session.flush" {
			t.Fatal("Flush() wrote session.flush before input end")
		}
	case <-time.After(25 * time.Millisecond):
	}

	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		t.Fatal("stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	deadline = time.After(time.Second)
	for {
		select {
		case msg := <-writes:
			if msg["type"] == "session.flush" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for final session.flush")
		}
	}
}

func TestInferenceTTSStreamErrorMessageReturnsNextError(t *testing.T) {
	readCh := make(chan []byte, 2)
	readCh <- []byte(`{"type":"error","message":"provider failed"}`)
	close(readCh)

	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{readCh: readCh}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		t.Fatal("stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want gateway error")
	}
	if !strings.Contains(err.Error(), "LiveKit Inference TTS returned error") {
		t.Fatalf("Next() error = %v, want gateway error", err)
	}
}

func TestInferenceTTSUnexpectedCloseReturnsNextError(t *testing.T) {
	readCh := make(chan []byte)
	close(readCh)

	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{readCh: readCh}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		t.Fatal("stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want unexpected close error")
	}
	if !strings.Contains(err.Error(), "Gateway connection closed unexpectedly") {
		t.Fatalf("Next() error = %v, want unexpected close error", err)
	}
}

func TestInferenceTTSMalformedGatewayJSONReturnsNextError(t *testing.T) {
	readCh := make(chan []byte, 1)
	readCh <- []byte(`{`)
	close(readCh)

	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{readCh: readCh}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		t.Fatal("stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "failed to decode LiveKit Inference TTS message") {
		t.Fatalf("Next() error = %v, want malformed JSON error", err)
	}
}

func TestInferenceTTSOutputAudioUsesConfiguredSampleRate(t *testing.T) {
	readCh := make(chan []byte, 2)
	readCh <- []byte(`{"type":"output_audio","audio":"AQIDBA=="}`)
	close(readCh)

	provider := NewTTS("cartesia/sonic-3", "key", "secret", WithTTSSampleRate(16000))
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{readCh: readCh}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		t.Fatal("stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("SampleRate = %d, want configured sample rate 16000", audio.Frame.SampleRate)
	}
}

func TestInferenceTTSInvalidOutputAudioReturnsNextError(t *testing.T) {
	readCh := make(chan []byte, 2)
	readCh <- []byte(`{"type":"output_audio","audio":"%%%"}`)
	close(readCh)

	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{readCh: readCh}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		t.Fatal("stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want invalid audio error")
	}
	if !strings.Contains(err.Error(), "invalid output_audio payload") {
		t.Fatalf("Next() error = %v, want invalid output_audio payload", err)
	}
}

func TestInferenceTTSDoneMessageReturnsEOF(t *testing.T) {
	readCh := make(chan []byte, 2)
	readCh <- []byte(`{"type":"done"}`)
	close(readCh)

	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{readCh: readCh}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		t.Fatal("stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() error = %v, want io.EOF", err)
	}
}

func TestInferenceTTSOutputAlignmentMapsTimedTranscript(t *testing.T) {
	readCh := make(chan []byte, 2)
	readCh <- []byte(`{"type":"output_alignment","words":[{"word":"hello","start":0.25,"end":0.5}]}`)
	close(readCh)

	provider := NewTTS("cartesia/sonic-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{readCh: readCh}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		t.Fatal("stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if audio.Frame != nil {
		t.Fatalf("Frame = %#v, want nil for alignment-only event", audio.Frame)
	}
	if len(audio.TimedTranscript) != 1 {
		t.Fatalf("TimedTranscript = %#v, want one word", audio.TimedTranscript)
	}
	got := audio.TimedTranscript[0]
	if got.Text != "hello" || got.StartTime != 0.25 || got.EndTime != 0.5 {
		t.Fatalf("TimedTranscript[0] = %#v, want hello 0.25-0.5", got)
	}
}

func TestInferenceTTSLanguageOptionMatchesReferencePackets(t *testing.T) {
	writes := make(chan map[string]any, 4)
	provider := NewTTS("cartesia/sonic-3:voice-id", "key", "secret", WithTTSLanguage("fr"))
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				writes <- msg
			},
		}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	session := <-writes
	if session["type"] != "session.create" {
		t.Fatalf("first write type = %v, want session.create", session["type"])
	}
	if got := session["language"]; got != "fr" {
		t.Fatalf("session.create language = %#v, want fr", got)
	}

	if err := stream.PushText("bonjour"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	var input map[string]any
	deadline := time.After(time.Second)
	for input == nil {
		select {
		case msg := <-writes:
			if msg["type"] == "input_transcript" {
				input = msg
			}
		case <-deadline:
			t.Fatal("timed out waiting for input_transcript")
		}
	}

	config, ok := input["generation_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("generation_config = %#v, want map", input["generation_config"])
	}
	if got := config["language"]; got != "fr" {
		t.Fatalf("generation_config.language = %#v, want fr", got)
	}
}

func TestInferenceTTSExtraKwargsMatchReferencePackets(t *testing.T) {
	writes := make(chan map[string]any, 4)
	provider := NewTTS("cartesia/sonic-3", "key", "secret", WithTTSExtraKwargs(map[string]any{
		"temperature": 0.7,
	}))
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				writes <- msg
			},
		}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	session := <-writes
	if session["type"] != "session.create" {
		t.Fatalf("first write type = %v, want session.create", session["type"])
	}
	sessionExtra, ok := session["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("session.create extra = %#v, want map", session["extra"])
	}
	if got := sessionExtra["temperature"]; got != 0.7 {
		t.Fatalf("session.create extra temperature = %#v, want 0.7", got)
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	var input map[string]any
	deadline := time.After(time.Second)
	for input == nil {
		select {
		case msg := <-writes:
			if msg["type"] == "input_transcript" {
				input = msg
			}
		case <-deadline:
			t.Fatal("timed out waiting for input_transcript")
		}
	}

	inputExtra, ok := input["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("input extra = %#v, want map", input["extra"])
	}
	if got := inputExtra["temperature"]; got != 0.7 {
		t.Fatalf("input extra temperature = %#v, want 0.7", got)
	}
}

func TestInferenceTTSUpdateOptionsMatchReferenceFutureStreams(t *testing.T) {
	writes := make(chan map[string]any, 8)
	provider := NewTTS("cartesia/sonic-3", "key", "secret", WithTTSExtraKwargs(map[string]any{
		"temperature": 0.2,
	}))
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				writes <- msg
			},
		}, nil
	}

	provider.UpdateOptions(
		WithTTSModel("elevenlabs/turbo"),
		WithTTSVoice("voice-updated"),
		WithTTSLanguage("id"),
		WithTTSExtraKwargs(map[string]any{"sync_alignment": true}),
	)

	if got := provider.Model(); got != "elevenlabs/turbo" {
		t.Fatalf("Model = %q, want elevenlabs/turbo", got)
	}
	if !provider.Capabilities().AlignedTranscript {
		t.Fatal("AlignedTranscript = false, want true after sync_alignment update")
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	session := <-writes
	if session["type"] != "session.create" {
		t.Fatalf("first write type = %v, want session.create", session["type"])
	}
	if session["model"] != "elevenlabs/turbo" {
		t.Fatalf("session.create model = %#v, want elevenlabs/turbo", session["model"])
	}
	if session["voice"] != "voice-updated" {
		t.Fatalf("session.create voice = %#v, want voice-updated", session["voice"])
	}
	sessionExtra, ok := session["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("session.create extra = %#v, want map", session["extra"])
	}
	if sessionExtra["temperature"] != 0.2 || sessionExtra["sync_alignment"] != true {
		t.Fatalf("session.create extra = %#v, want merged extra kwargs", sessionExtra)
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	var input map[string]any
	deadline := time.After(time.Second)
	for input == nil {
		select {
		case msg := <-writes:
			if msg["type"] == "input_transcript" {
				input = msg
			}
		case <-deadline:
			t.Fatal("timed out waiting for input_transcript")
		}
	}

	config, ok := input["generation_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("generation_config = %#v, want map", input["generation_config"])
	}
	if config["model"] != "elevenlabs/turbo" || config["voice"] != "voice-updated" || config["language"] != "id" {
		t.Fatalf("generation_config = %#v, want updated model, voice, and language", config)
	}
	extra, ok := input["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("input extra = %#v, want map", input["extra"])
	}
	if extra["temperature"] != 0.2 || extra["sync_alignment"] != true {
		t.Fatalf("input extra = %#v, want merged extra kwargs", extra)
	}
}

func TestInferenceTTSStreamKeepsReferenceOptionSnapshot(t *testing.T) {
	writes := make(chan map[string]any, 8)
	provider := NewTTS("cartesia/sonic-3:voice-original", "key", "secret", WithTTSExtraKwargs(map[string]any{
		"temperature": 0.2,
	}))
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				writes <- msg
			},
		}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	session := <-writes
	if session["type"] != "session.create" {
		t.Fatalf("first write type = %v, want session.create", session["type"])
	}

	provider.UpdateOptions(
		WithTTSModel("elevenlabs/turbo"),
		WithTTSVoice("voice-updated"),
		WithTTSLanguage("id"),
		WithTTSExtraKwargs(map[string]any{"sync_alignment": true}),
	)

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	var input map[string]any
	deadline := time.After(time.Second)
	for input == nil {
		select {
		case msg := <-writes:
			if msg["type"] == "input_transcript" {
				input = msg
			}
		case <-deadline:
			t.Fatal("timed out waiting for input_transcript")
		}
	}

	config, ok := input["generation_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("generation_config = %#v, want map", input["generation_config"])
	}
	if config["model"] != "cartesia/sonic-3" || config["voice"] != "voice-original" {
		t.Fatalf("generation_config = %#v, want stream creation options", config)
	}
	if _, ok := config["language"]; ok {
		t.Fatalf("generation_config.language = %#v, want omitted from original stream options", config["language"])
	}
	extra, ok := input["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("input extra = %#v, want map", input["extra"])
	}
	if extra["temperature"] != 0.2 {
		t.Fatalf("input extra temperature = %#v, want original value 0.2", extra["temperature"])
	}
	if _, ok := extra["sync_alignment"]; ok {
		t.Fatalf("input extra sync_alignment = %#v, want omitted from original stream options", extra["sync_alignment"])
	}
}

func TestInferenceTTSSessionCreateUsesReferenceAudioOptions(t *testing.T) {
	writes := make(chan map[string]any, 2)
	provider := NewTTS("cartesia/sonic-3", "key", "secret",
		WithTTSSampleRate(16000),
		WithTTSEncoding("mulaw"),
	)
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				writes <- msg
			},
		}, nil
	}

	if provider.SampleRate() != 16000 {
		t.Fatalf("SampleRate = %d, want configured 16000", provider.SampleRate())
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	session := <-writes
	if session["type"] != "session.create" {
		t.Fatalf("first write type = %v, want session.create", session["type"])
	}
	if session["sample_rate"] != "16000" {
		t.Fatalf("session.create sample_rate = %#v, want string 16000", session["sample_rate"])
	}
	if session["encoding"] != "mulaw" {
		t.Fatalf("session.create encoding = %#v, want mulaw", session["encoding"])
	}
}

func TestInferenceTTSSessionCreateUsesReferenceConnectOptions(t *testing.T) {
	writes := make(chan map[string]any, 2)
	provider := NewTTS("cartesia/sonic-3", "key", "secret",
		WithTTSConnectOptions(APIConnectOptions{
			Timeout:  1500 * time.Millisecond,
			MaxRetry: 2,
		}),
	)
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				writes <- msg
			},
		}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	session := <-writes
	if session["type"] != "session.create" {
		t.Fatalf("first write type = %v, want session.create", session["type"])
	}
	connection, ok := session["connection"].(map[string]interface{})
	if !ok {
		t.Fatalf("connection = %#v, want map", session["connection"])
	}
	if connection["timeout"] != 1.5 {
		t.Fatalf("connection.timeout = %#v, want 1.5", connection["timeout"])
	}
	if connection["retries"] != 2 {
		t.Fatalf("connection.retries = %#v, want 2", connection["retries"])
	}
}

func TestInferenceTTSFallbackModelsMatchReferenceSessionCreate(t *testing.T) {
	writes := make(chan map[string]any, 4)
	provider := NewTTS("cartesia/sonic-3", "key", "secret", WithTTSFallbackModels(
		FallbackModel{
			Model:       "deepgram/aura-2",
			Voice:       "luna",
			ExtraKwargs: map[string]any{"stability": 0.4},
		},
		FallbackModel{
			Model: "rime/mist",
			Voice: "river",
		},
	))
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
		return &recordingTTSConn{
			onWriteJSON: func(msg map[string]any) {
				writes <- msg
			},
		}, nil
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	session := <-writes
	if session["type"] != "session.create" {
		t.Fatalf("first write type = %v, want session.create", session["type"])
	}
	fallback, ok := session["fallback"].(map[string]interface{})
	if !ok {
		t.Fatalf("fallback = %#v, want map", session["fallback"])
	}
	models, ok := fallback["models"].([]map[string]interface{})
	if !ok {
		t.Fatalf("fallback.models = %#v, want model maps", fallback["models"])
	}
	if len(models) != 2 {
		t.Fatalf("fallback models = %#v, want 2 entries", models)
	}
	if models[0]["model"] != "deepgram/aura-2" || models[0]["voice"] != "luna" {
		t.Fatalf("first fallback model = %#v, want deepgram/aura-2/luna", models[0])
	}
	extra, ok := models[0]["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("first fallback extra = %#v, want map", models[0]["extra"])
	}
	if got := extra["stability"]; got != 0.4 {
		t.Fatalf("first fallback extra stability = %#v, want 0.4", got)
	}
	extra, ok = models[1]["extra"].(map[string]interface{})
	if !ok || len(extra) != 0 {
		t.Fatalf("second fallback extra = %#v, want empty map", models[1]["extra"])
	}
}

func TestInferenceTTSFallbackModelStringParsesReferenceVoice(t *testing.T) {
	_, params := ttsSessionCreateParams("cartesia/sonic-3", "", "", "", 0, nil, []FallbackModel{
		{Model: "rime/mist:river"},
	}, nil)

	fallback, ok := params["fallback"].(map[string]interface{})
	if !ok {
		t.Fatalf("fallback = %#v, want map", params["fallback"])
	}
	models, ok := fallback["models"].([]map[string]interface{})
	if !ok {
		t.Fatalf("fallback.models = %#v, want model maps", fallback["models"])
	}
	if len(models) != 1 {
		t.Fatalf("fallback models = %#v, want 1 entry", models)
	}
	if models[0]["model"] != "rime/mist" || models[0]["voice"] != "river" {
		t.Fatalf("fallback model = %#v, want rime/mist/river", models[0])
	}
}

func TestInferenceTTSSessionCreateParamsMatchReferenceShape(t *testing.T) {
	modelName, params := ttsSessionCreateParams("cartesia/sonic-3:voice-id", "", "", "", 0, nil, nil, nil)

	if modelName != "cartesia/sonic-3" {
		t.Fatalf("modelName = %q, want cartesia/sonic-3", modelName)
	}
	if params["voice"] != "voice-id" {
		t.Fatalf("voice = %v, want voice-id", params["voice"])
	}
	if params["model"] != "cartesia/sonic-3" {
		t.Fatalf("model = %v, want cartesia/sonic-3", params["model"])
	}
	if extra, ok := params["extra"].(map[string]interface{}); !ok || len(extra) != 0 {
		t.Fatalf("extra = %#v, want empty map", params["extra"])
	}
}

func TestInferenceAccessTokenTTLMatchesReferenceDefault(t *testing.T) {
	token, err := CreateAccessToken("key", "secret", InferenceAccessTokenTTL)
	if err != nil {
		t.Fatalf("CreateAccessToken() error = %v", err)
	}
	parsed, err := jwt.ParseSigned(token)
	if err != nil {
		t.Fatalf("ParseSigned() error = %v", err)
	}
	claims := jwt.Claims{}
	if err := parsed.UnsafeClaimsWithoutVerification(&claims); err != nil {
		t.Fatalf("UnsafeClaimsWithoutVerification() error = %v", err)
	}
	if claims.NotBefore == nil || claims.Expiry == nil {
		t.Fatalf("claims missing not-before or expiry: %#v", claims)
	}
	if got := claims.Expiry.Time().Sub(claims.NotBefore.Time()); got != 10*time.Minute {
		t.Fatalf("access token TTL = %v, want 10m", got)
	}
}

type recordingSentenceTokenizer struct{}

func (r *recordingSentenceTokenizer) Tokenize(text string, language string) []string {
	return []string{"custom"}
}

func (r *recordingSentenceTokenizer) Stream(language string) tokenize.SentenceStream {
	return tokenize.NewBufferedTokenStream(func(string) []string {
		return []string{"custom"}
	}, 1, 1)
}
