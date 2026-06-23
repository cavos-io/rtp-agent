package asyncai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestAsyncAITTSDefaultsMatchReference(t *testing.T) {
	provider := NewAsyncAITTS("test-key", "")

	if provider.baseURL != "https://api.async.com" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "async_flash_v1.0" {
		t.Fatalf("model = %q, want async_flash_v1.0", provider.model)
	}
	if provider.voice != "e0f39dc4-f691-4e78-bba5-5c636692cc04" {
		t.Fatalf("voice = %q, want reference default voice", provider.voice)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.sampleRate != 32000 {
		t.Fatalf("sample rate = %d, want 32000", provider.sampleRate)
	}
	if provider.Label() != "asyncai.TTS" {
		t.Fatalf("label = %q, want asyncai.TTS", provider.Label())
	}
	if provider.SampleRate() != 32000 {
		t.Fatalf("SampleRate = %d, want 32000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("NumChannels = %d, want 1", provider.NumChannels())
	}
	if got := tts.Model(provider); got != "async_flash_v1.0" {
		t.Fatalf("model metadata = %q, want async_flash_v1.0", got)
	}
	if got := tts.Provider(provider); got != "AsyncAI" {
		t.Fatalf("provider metadata = %q, want AsyncAI", got)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want reference streaming support")
	}
}

func TestAsyncAITTSFallsBackToEnvironmentAPIKey(t *testing.T) {
	t.Setenv(asyncAIAPIKeyEnv, "env-key")

	provider := NewAsyncAITTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env-key", provider.apiKey)
	}
}

func TestAsyncAITTSStreamRequiresAPIKeyBeforeDial(t *testing.T) {
	t.Setenv(asyncAIAPIKeyEnv, "")
	provider := NewAsyncAITTS("", "")

	_, err := provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "ASYNCAI_API_KEY") {
		t.Fatalf("Stream error = %v, want API key error", err)
	}
}

func TestAsyncAITTSWebsocketURLUsesReferenceQuery(t *testing.T) {
	provider := NewAsyncAITTS("test-key", "", WithAsyncAITTSBaseURL("https://async.example"))

	wsURL := buildAsyncAITTSWebsocketURL(provider)
	parsed, err := url.Parse(wsURL)
	if err != nil {
		t.Fatalf("parse websocket URL: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "async.example" || parsed.Path != "/text_to_speech/websocket/ws" {
		t.Fatalf("websocket URL = %q, want reference websocket endpoint", wsURL)
	}
	query := parsed.Query()
	if query.Get("api_key") != "test-key" {
		t.Fatalf("api_key = %q, want test-key", query.Get("api_key"))
	}
	if query.Get("version") != "v1" {
		t.Fatalf("version = %q, want v1", query.Get("version"))
	}

	provider = NewAsyncAITTS("test-key", "", WithAsyncAITTSBaseURL("http://async.example"))
	parsed, err = url.Parse(buildAsyncAITTSWebsocketURL(provider))
	if err != nil {
		t.Fatalf("parse websocket URL: %v", err)
	}
	if parsed.Scheme != "ws" {
		t.Fatalf("websocket scheme = %q, want ws for http base URL", parsed.Scheme)
	}
}

func TestAsyncAITTSOptionsMatchReference(t *testing.T) {
	provider := NewAsyncAITTS("test-key", "",
		WithAsyncAITTSBaseURL("https://async.example"),
		WithAsyncAITTSModel("async_flash_v1.0"),
		WithAsyncAITTSVoice("voice-2"),
		WithAsyncAITTSLanguage("en"),
		WithAsyncAITTSEncoding("pcm_mulaw"),
		WithAsyncAITTSSampleRate(24000),
	)

	if provider.baseURL != "https://async.example" {
		t.Fatalf("base URL = %q, want custom base URL", provider.baseURL)
	}
	if provider.voice != "voice-2" || provider.language != "en" {
		t.Fatalf("provider = %+v, want custom voice/language", provider)
	}
	if provider.encoding != "pcm_mulaw" || provider.sampleRate != 24000 {
		t.Fatalf("provider = %+v, want custom encoding/sample rate", provider)
	}
}

func TestAsyncAITTSInitPayloadMatchesReference(t *testing.T) {
	provider := NewAsyncAITTS("test-key", "voice-1",
		WithAsyncAITTSModel("async_flash_v1.0"),
		WithAsyncAITTSLanguage("hi"),
		WithAsyncAITTSSampleRate(24000),
	)

	payload, err := buildAsyncAITTSInitMessage(provider)
	if err != nil {
		t.Fatalf("build init message: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode init message: %v", err)
	}
	if message["model_id"] != "async_flash_v1.0" || message["language"] != "hi" {
		t.Fatalf("message = %+v, want model and language", message)
	}
	voice := message["voice"].(map[string]any)
	if voice["mode"] != "id" || voice["id"] != "voice-1" {
		t.Fatalf("voice = %+v, want id voice", voice)
	}
	output := message["output_format"].(map[string]any)
	if output["container"] != "raw" || output["encoding"] != "pcm_s16le" || output["sample_rate"] != float64(24000) {
		t.Fatalf("output = %+v, want raw pcm config", output)
	}
}

func TestAsyncAITTSUpdateOptionsAppliesToFutureInitPayload(t *testing.T) {
	provider := NewAsyncAITTS("test-key", "voice-1",
		WithAsyncAITTSModel("async_flash_v1.0"),
		WithAsyncAITTSLanguage("en"),
		WithAsyncAITTSEncoding("pcm_mulaw"),
		WithAsyncAITTSSampleRate(24000),
	)

	provider.UpdateOptions(
		WithAsyncAITTSModel("async_v2"),
		WithAsyncAITTSLanguage("hi"),
		WithAsyncAITTSVoice("voice-2"),
	)

	payload, err := buildAsyncAITTSInitMessage(provider)
	if err != nil {
		t.Fatalf("build init message: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode init message: %v", err)
	}
	if message["model_id"] != "async_v2" || message["language"] != "hi" {
		t.Fatalf("message = %+v, want updated model and language", message)
	}
	voice := message["voice"].(map[string]any)
	if voice["id"] != "voice-2" {
		t.Fatalf("voice = %+v, want updated voice", voice)
	}
	output := message["output_format"].(map[string]any)
	if output["encoding"] != "pcm_mulaw" || output["sample_rate"] != float64(24000) {
		t.Fatalf("output = %+v, want preserved audio format", output)
	}
}

func TestAsyncAITTSTextAndEndMessagesMatchReference(t *testing.T) {
	textPayload, err := buildAsyncAITTSTextMessage("ctx-1", "hello")
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var text map[string]any
	if err := json.Unmarshal(textPayload, &text); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	if text["context_id"] != "ctx-1" || text["transcript"] != "hello " || text["force"] != true {
		t.Fatalf("text message = %+v, want reference packet", text)
	}

	endPayload, err := buildAsyncAITTSEndMessage("ctx-1")
	if err != nil {
		t.Fatalf("build end message: %v", err)
	}
	var end map[string]any
	if err := json.Unmarshal(endPayload, &end); err != nil {
		t.Fatalf("decode end message: %v", err)
	}
	if end["context_id"] != "ctx-1" || end["transcript"] != "" {
		t.Fatalf("end message = %+v, want empty transcript end packet", end)
	}
}

func TestAsyncAITTSTextMessageKeepsTrailingWhitespace(t *testing.T) {
	payload, err := buildAsyncAITTSTextMessage("ctx-1", "hello ")
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	if message["transcript"] != "hello " {
		t.Fatalf("transcript = %q, want original trailing whitespace", message["transcript"])
	}
}

func asyncAITTSTestTranscript(t *testing.T, payload []byte) string {
	t.Helper()
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode AsyncAI message: %v", err)
	}
	transcript, ok := message["transcript"].(string)
	if !ok {
		t.Fatalf("message transcript = %#v, want string", message["transcript"])
	}
	return transcript
}

func TestAsyncAITTSAudioFromWebsocketMessage(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})
	audio, done, err := asyncAITTSAudioFromWebsocketMessage([]byte(`{"context_id":"ctx-1","audio":"`+encoded+`"}`), 32000)
	if err != nil {
		t.Fatalf("audio message: %v", err)
	}
	if done || string(audio.Frame.Data) != "\x01\x02" || audio.SegmentID != "ctx-1" {
		t.Fatalf("audio=%+v done=%v, want decoded audio with segment", audio, done)
	}

	audio, done, err = asyncAITTSAudioFromWebsocketMessage([]byte(`{"context_id":"ctx-1","final":true}`), 32000)
	if err != nil {
		t.Fatalf("final message: %v", err)
	}
	if !done {
		t.Fatalf("done=%v, want true for final message", done)
	}
	if audio == nil || !audio.IsFinal || audio.SegmentID != "ctx-1" {
		t.Fatalf("audio=%+v, want reference final marker with segment", audio)
	}
	if audio.Frame != nil {
		t.Fatalf("final frame = %+v, want boundary-only final marker", audio.Frame)
	}
}

func TestAsyncAITTSWebsocketNextReturnsFinalMarker(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"context_id":"ctx-1","final":true}`)); err != nil {
			t.Errorf("write final message: %v", err)
		}
		if err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
			t.Errorf("write close message: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	stream := &asyncAITTSWebsocketChunkedStream{conn: conn, sampleRate: 32000}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next final error = %v", err)
	}
	if final == nil || !final.IsFinal || final.SegmentID != "ctx-1" {
		t.Fatalf("final = %#v, want reference final marker with segment", final)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("Next after final = %v, want io.EOF", err)
	}
}

func TestAsyncAITTSWebsocketNextUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "bad audio stream"),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	stream := &asyncAITTSWebsocketChunkedStream{conn: conn, sampleRate: 32000}
	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != websocket.CloseUnsupportedData {
		t.Fatalf("StatusCode = %d, want close code", statusErr.StatusCode)
	}
	if !strings.Contains(err.Error(), "Async connection closed unexpectedly") {
		t.Fatalf("Next error = %q, want Async close context", err)
	}
}

func TestAsyncAITTSWebsocketNextNormalCloseBeforeFinalReturnsAPIStatusError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	stream := &asyncAITTSWebsocketChunkedStream{conn: conn, sampleRate: 32000}
	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != websocket.CloseNormalClosure {
		t.Fatalf("StatusCode = %d, want normal close code", statusErr.StatusCode)
	}
}

func TestAsyncAITTSStreamNextReturnsEOFAfterReferenceFinalMarker(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"context_id":"ctx-1","final":true}`)); err != nil {
			t.Errorf("write final message: %v", err)
		}
		if err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
			t.Errorf("write close message: %v", err)
		}
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	stream := &asyncAITTSStream{conn: conn, sampleRate: 32000}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next final error = %v", err)
	}
	if final == nil || !final.IsFinal || final.SegmentID != "ctx-1" {
		t.Fatalf("final = %#v, want reference final marker with segment", final)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("Next after final = %v, want io.EOF", err)
	}
}

func TestAsyncAITTSStreamBuffersTextUntilFlush(t *testing.T) {
	stream := &asyncAITTSStream{}
	if err := stream.PushText("hello "); err != nil {
		t.Fatalf("push first: %v", err)
	}
	if err := stream.PushText("world"); err != nil {
		t.Fatalf("push second: %v", err)
	}
	if got := stream.pendingText.String(); got != "hello world" {
		t.Fatalf("pending text = %q, want concatenated text", got)
	}
}

func TestAsyncAITTSStreamSendsSentencesAndFlushesTailLikeReference(t *testing.T) {
	var sent [][]byte
	stream := &asyncAITTSStream{
		contextID: "ctx-1",
		writeMessage: func(payload []byte) error {
			sent = append(sent, bytes.Clone(payload))
			return nil
		},
	}

	if err := stream.PushText("This is a complete sen"); err != nil {
		t.Fatalf("PushText(partial) error = %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("sent after partial = %d, want no provider text before complete sentence", len(sent))
	}

	if err := stream.PushText("tence. Tail"); err != nil {
		t.Fatalf("PushText(sentence) error = %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent after sentence = %d, want one text message", len(sent))
	}
	if got := asyncAITTSTestTranscript(t, sent[0]); got != "This is a complete sentence. " {
		t.Fatalf("transcript = %q, want completed sentence with reference spacing", got)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if len(sent) != 3 {
		t.Fatalf("sent after flush = %d, want tail text and end message", len(sent))
	}
	if got := asyncAITTSTestTranscript(t, sent[1]); got != "Tail " {
		t.Fatalf("tail transcript = %q, want Tail with reference spacing", got)
	}
	if got := asyncAITTSTestTranscript(t, sent[2]); got != "" {
		t.Fatalf("end transcript = %q, want empty end packet", got)
	}
}

func TestAsyncAITTSStreamClosesAfterFlushWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &asyncAITTSStream{
		cancel:    func() { cancelled = true },
		contextID: "ctx-1",
		writeMessage: func([]byte) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText error = %v, want nil", err)
	}
	if err := stream.Flush(); !errors.Is(err, writeErr) {
		t.Fatalf("Flush error = %v, want write error", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after write failure")
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

func TestAsyncAITTSProviderCloseClosesActiveStreams(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	closeCalls := 0
	stream := &asyncAITTSStream{
		ctx:    ctx,
		cancel: cancel,
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider := NewAsyncAITTS("test-key", "")
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	provider.mu.Lock()
	active := len(provider.streams)
	provider.mu.Unlock()
	if active != 0 {
		t.Fatalf("active streams after Close = %d, want 0", active)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("stream context still active after provider Close")
	}
}

func TestAsyncAITTSStreamAfterCloseIsRejected(t *testing.T) {
	originalDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("unexpected asyncai tts dial")
		},
	}
	t.Cleanup(func() {
		websocket.DefaultDialer = originalDialer
	})

	provider := NewAsyncAITTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := provider.Stream(context.Background())

	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestAsyncAITTSSynthesizeReportsStreamingOnly(t *testing.T) {
	provider := NewAsyncAITTS("test-key", "")
	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "supports streaming only") {
		t.Fatalf("Synthesize error = %v, want streaming-only error", err)
	}
}

func TestAsyncAITTSImplementsInterface(t *testing.T) {
	var _ tts.TTS = NewAsyncAITTS("test-key", "")
}

func TestAsyncAITTSEmptyStreamNextEOF(t *testing.T) {
	stream := &asyncAITTSWebsocketChunkedStream{sampleRate: 32000}
	_, err := stream.Next()
	if err != io.EOF {
		t.Fatalf("Next err = %v, want EOF without websocket", err)
	}
}

func TestAsyncAITTSStreamCloseWithoutWebsocket(t *testing.T) {
	stream := &asyncAITTSStream{}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close err = %v, want nil without websocket", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close err = %v, want nil without websocket", err)
	}
}

func TestAsyncAITTSStreamNextReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := &asyncAITTSStream{ctx: ctx}

	_, err := stream.Next()

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next err = %v, want context canceled", err)
	}
}
