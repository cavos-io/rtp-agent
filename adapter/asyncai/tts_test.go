package asyncai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
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
	if audio != nil || !done {
		t.Fatalf("audio=%+v done=%v, want final marker", audio, done)
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
	if err := stream.PushText("again"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PushText after write failure error = %v, want closed stream error", err)
	}
	if err := stream.Flush(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Flush after write failure error = %v, want closed stream error", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
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
