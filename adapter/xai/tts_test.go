package xai

import (
	"context"
	"encoding/base64"
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

func TestXaiTTSDefaultsMatchReference(t *testing.T) {
	provider := NewXaiTTS("test-key", "")

	if provider.websocketURL != "wss://api.x.ai/v1/tts" {
		t.Fatalf("websocket URL = %q, want reference URL", provider.websocketURL)
	}
	if provider.voice != "ara" {
		t.Fatalf("voice = %q, want ara", provider.voice)
	}
	if got := tts.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := tts.Provider(provider); got != "xAI" {
		t.Fatalf("provider metadata = %q, want xAI", got)
	}
	if provider.language != "auto" {
		t.Fatalf("language = %q, want auto", provider.language)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("num channels = %d, want 1", provider.NumChannels())
	}
	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if caps.AlignedTranscript {
		t.Fatal("aligned transcript = true, want false")
	}
}

func TestNewXaiTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "env-key")

	provider := NewXaiTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewXaiTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestXaiTTSOptionsBuildReferenceStreamURLAndHeaders(t *testing.T) {
	provider := NewXaiTTS("test-key", "eve",
		WithXaiTTSWebsocketURL("ws://xai.example/v1/tts"),
		WithXaiTTSLanguage("ja"),
	)

	streamURL, err := url.Parse(buildXaiTTSStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if !strings.HasPrefix(streamURL.String(), "ws://xai.example/v1/tts?") {
		t.Fatalf("stream URL = %q, want websocket endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertXaiQuery(t, query, "voice", "eve")
	assertXaiQuery(t, query, "language", "ja")
	assertXaiQuery(t, query, "codec", "pcm")
	assertXaiQuery(t, query, "sample_rate", "24000")

	headers := buildXaiTTSHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
}

func TestXaiTTSUpdateOptionsMatchesReferenceFutureRequests(t *testing.T) {
	provider := NewXaiTTS("test-key", "ara",
		WithXaiTTSWebsocketURL("ws://xai.example/v1/tts"),
	)

	provider.UpdateOptions(
		WithXaiTTSVoice("eve"),
		WithXaiTTSLanguage("ja"),
	)

	streamURL, err := url.Parse(buildXaiTTSStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	query := streamURL.Query()
	assertXaiQuery(t, query, "voice", "eve")
	assertXaiQuery(t, query, "language", "ja")
	assertXaiQuery(t, query, "codec", "pcm")
	assertXaiQuery(t, query, "sample_rate", "24000")
}

func TestXaiTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	provider := NewXaiTTS("", "", WithXaiTTSWebsocketURL("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "XAI_API_KEY") {
		t.Fatalf("Synthesize error = %q, want XAI_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "XAI_API_KEY") {
		t.Fatalf("Stream error = %q, want XAI_API_KEY guidance", err)
	}
}

func TestXaiTTSSynthesizeReturnsAPIConnectionErrorOnDialTimeout(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, context.DeadlineExceeded
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiTTS("test-key", "", WithXaiTTSWebsocketURL("ws://xai.test/v1/tts"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Synthesize error = %T %v, want APIConnectionError", err, err)
	}
	var timeoutErr *llm.APITimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("Synthesize error = %T %v, want APIConnectionError but not APITimeoutError", err, err)
	}
}

func TestXaiTTSStreamReturnsAPIConnectionErrorOnDialFailure(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial refused")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiTTS("test-key", "", WithXaiTTSWebsocketURL("ws://xai.test/v1/tts"))

	_, err := provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestXaiTTSTextMessagesMatchReference(t *testing.T) {
	delta := buildXaiTTSTextDeltaMessage("hello")
	done := buildXaiTTSTextDoneMessage()

	if delta["type"] != "text.delta" || delta["delta"] != "hello" {
		t.Fatalf("delta message = %#v, want reference text delta", delta)
	}
	if done["type"] != "text.done" {
		t.Fatalf("done message = %#v, want reference text done", done)
	}
}

func TestXaiTTSStreamTokenizesTextBeforeFlush(t *testing.T) {
	var messages []map[string]any
	stream := &xaiTTSSynthesizeStream{
		cancel: func() {},
		writeMessage: func(message map[string]any) error {
			messages = append(messages, message)
			return nil
		},
		closeConn: func() error { return nil },
	}

	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if len(messages) != 3 {
		t.Fatalf("messages = %#v, want two token deltas and done", messages)
	}
	assertXaiTTSMessage(t, messages[0], "text.delta", "hello")
	assertXaiTTSMessage(t, messages[1], "text.delta", "world")
	assertXaiTTSMessage(t, messages[2], "text.done", "")
}

func TestXaiTTSSynthesizeTokenizesTextBeforeDone(t *testing.T) {
	messages := make(chan map[string]any, 3)
	handlerErr := make(chan error, 1)
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = newXaiSTTTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		for i := 0; i < 3; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				handlerErr <- err
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				handlerErr <- err
				return
			}
			messages <- message
		}
	}, handlerErr)
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiTTS("test-key", "ara", WithXaiTTSWebsocketURL("ws://xai.test/v1/tts"))
	stream, err := provider.Synthesize(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	assertXaiTTSMessage(t, readXaiTTSMessage(t, messages, handlerErr), "text.delta", "hello")
	assertXaiTTSMessage(t, readXaiTTSMessage(t, messages, handlerErr), "text.delta", "world")
	assertXaiTTSMessage(t, readXaiTTSMessage(t, messages, handlerErr), "text.done", "")
}

func TestXaiTTSStreamReconnectsBetweenFlushSegments(t *testing.T) {
	requestURLs := make(chan string, 2)
	handlerErr := make(chan error, 2)
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = newXaiSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		requestURLs <- r.URL.String()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				handlerErr <- err
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				handlerErr <- err
				return
			}
			if message["type"] == "text.done" {
				if err := conn.WriteJSON(map[string]any{"type": "audio.done"}); err != nil {
					handlerErr <- err
				}
				return
			}
		}
	}, handlerErr)
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiTTS("test-key", "ara", WithXaiTTSWebsocketURL("ws://xai.test/v1/tts"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	readXaiTTSRequestURL(t, requestURLs, handlerErr)

	if err := stream.PushText("first segment"); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(first) error = %v", err)
	}
	if audio, err := stream.Next(); err != io.EOF {
		t.Fatalf("first Next() = (%#v, %v), want EOF after audio.done", audio, err)
	}

	if err := stream.PushText("second segment"); err != nil {
		t.Fatalf("PushText(second) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(second) error = %v", err)
	}
	readXaiTTSRequestURL(t, requestURLs, handlerErr)
}

func TestXaiTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &xaiTTSSynthesizeStream{
		cancel: func() { cancelled = true },
		writeMessage: func(map[string]any) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("hello world"); !errors.Is(err, writeErr) {
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

func TestXaiTTSAudioFromMessageDecodesAudioDeltaAndDone(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"type":  "audio.delta",
		"delta": base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04}),
	})

	audio, done, err := xaiTTSAudioFromMessage(payload)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if done {
		t.Fatal("done = true, want false for audio delta")
	}
	assertXaiTTSAudio(t, audio, []byte{0x01, 0x02, 0x03, 0x04})

	audio, done, err = xaiTTSAudioFromMessage([]byte(`{"type":"audio.done"}`))
	if err != nil {
		t.Fatalf("done from message: %v", err)
	}
	if !done {
		t.Fatal("done = false, want true for audio.done")
	}
	if audio != nil {
		t.Fatalf("audio = %#v, want nil for audio.done", audio)
	}
}

func TestXaiTTSAudioFromMessageReportsReferenceError(t *testing.T) {
	_, _, err := xaiTTSAudioFromMessage([]byte(`{"type":"error","message":"bad voice"}`))
	if err == nil {
		t.Fatal("error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != -1 {
		t.Fatalf("status code = %d, want -1", statusErr.StatusCode)
	}
	if !strings.Contains(statusErr.Error(), "bad voice") {
		t.Fatalf("error = %v, want provider message", err)
	}
}

func TestXaiTTSAudioFromMessageReturnsAPIConnectionErrorOnInvalidPayload(t *testing.T) {
	_, _, err := xaiTTSAudioFromMessage([]byte(`{`))
	if err == nil {
		t.Fatal("invalid JSON error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("invalid JSON error = %T %v, want APIConnectionError", err, err)
	}

	_, _, err = xaiTTSAudioFromMessage([]byte(`{"type":"audio.delta","delta":"not-base64"}`))
	if err == nil {
		t.Fatal("invalid audio error = nil, want APIConnectionError")
	}
	connectionErr = nil
	if !errors.As(err, &connectionErr) {
		t.Fatalf("invalid audio error = %T %v, want APIConnectionError", err, err)
	}
}

func assertXaiTTSAudio(t *testing.T, audio *tts.SynthesizedAudio, want []byte) {
	t.Helper()
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want frame", audio)
	}
	if string(audio.Frame.Data) != string(want) {
		t.Fatalf("frame data = %#v, want %#v", audio.Frame.Data, want)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want 1", audio.Frame.NumChannels)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("samples = %d, want 2", audio.Frame.SamplesPerChannel)
	}
}

func assertXaiTTSMessage(t *testing.T, message map[string]any, messageType string, delta string) {
	t.Helper()
	if message["type"] != messageType {
		t.Fatalf("message type = %q, want %q in %#v", message["type"], messageType, message)
	}
	if delta == "" {
		if _, ok := message["delta"]; ok {
			t.Fatalf("message delta = %q, want no delta in %#v", message["delta"], message)
		}
		return
	}
	if message["delta"] != delta {
		t.Fatalf("message delta = %q, want %q in %#v", message["delta"], delta, message)
	}
}

func readXaiTTSMessage(t *testing.T, messages <-chan map[string]any, handlerErr <-chan error) map[string]any {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case err := <-handlerErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for xAI TTS message")
	}
	return nil
}

func readXaiTTSRequestURL(t *testing.T, requestURLs <-chan string, handlerErr <-chan error) string {
	t.Helper()
	select {
	case requestURL := <-requestURLs:
		return requestURL
	case err := <-handlerErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for xAI TTS websocket request")
	}
	return ""
}
