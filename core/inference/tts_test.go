package inference

import (
	"context"
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
	onWriteJSON func(map[string]any)
}

func (c *recordingTTSConn) WriteJSON(v any) error {
	if msg, ok := v.(map[string]any); ok && c.onWriteJSON != nil {
		c.onWriteJSON(msg)
	}
	return nil
}

func (c *recordingTTSConn) ReadMessage() (int, []byte, error) {
	for !c.closed.Load() {
		time.Sleep(time.Millisecond)
	}
	return 0, nil, context.Canceled
}

func (c *recordingTTSConn) Close() error {
	c.closed.Store(true)
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

func TestInferenceTTSSessionCreateParamsMatchReferenceShape(t *testing.T) {
	modelName, params := ttsSessionCreateParams("cartesia/sonic-3:voice-id", "")

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
