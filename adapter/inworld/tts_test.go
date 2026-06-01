package inworld

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestInworldTTSDefaultsMatchReference(t *testing.T) {
	provider := NewInworldTTS("test-key", "")

	if provider.baseURL != "https://api.inworld.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.wsURL != "wss://api.inworld.ai" {
		t.Fatalf("websocket URL = %q, want reference websocket URL", provider.wsURL)
	}
	if provider.model != "inworld-tts-1.5-max" {
		t.Fatalf("model = %q, want reference model", provider.model)
	}
	if provider.voice != "Ashley" {
		t.Fatalf("voice = %q, want reference voice", provider.voice)
	}
	if provider.encoding != "PCM" {
		t.Fatalf("encoding = %q, want PCM", provider.encoding)
	}
	if provider.bitRate != 64000 {
		t.Fatalf("bit rate = %d, want 64000", provider.bitRate)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want reference streaming support")
	}
}

func TestInworldTTSSynthesizeRequestMatchesReference(t *testing.T) {
	provider := NewInworldTTS("test-key", "",
		WithInworldTTSBaseURL("https://inworld.example/"),
		WithInworldTTSModel("inworld-tts-2"),
		WithInworldTTSEncoding("MP3"),
		WithInworldTTSBitRate(96000),
		WithInworldTTSSampleRate(44100),
		WithInworldTTSSpeakingRate(1.2),
		WithInworldTTSTemperature(0.7),
	)

	req, err := buildInworldTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://inworld.example/tts/v1/voice:stream" {
		t.Fatalf("url = %q, want reference endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Basic test-key" {
		t.Fatalf("authorization = %q, want basic token", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertInworldPayload(t, payload, "text", "hello")
	assertInworldPayload(t, payload, "voiceId", "Ashley")
	assertInworldPayload(t, payload, "modelId", "inworld-tts-2")
	audioConfig := payload["audioConfig"].(map[string]any)
	assertInworldPayload(t, audioConfig, "audioEncoding", "MP3")
	if audioConfig["sampleRateHertz"] != float64(44100) || audioConfig["bitrate"] != float64(96000) {
		t.Fatalf("audio config = %+v, want sample rate and bitrate", audioConfig)
	}
}

func TestInworldTTSWebsocketMessagesMatchReference(t *testing.T) {
	provider := NewInworldTTS("test-key", "", WithInworldTTSWebsocketURL("wss://inworld.example/"))

	if got := buildInworldTTSWebsocketURL(provider); got != "wss://inworld.example/tts/v1/voice:streamBidirectional" {
		t.Fatalf("websocket URL = %q, want reference endpoint", got)
	}

	createPayload, err := buildInworldTTSCreateContextMessage(provider, "ctx-1")
	if err != nil {
		t.Fatalf("build create message: %v", err)
	}
	var create map[string]any
	if err := json.Unmarshal(createPayload, &create); err != nil {
		t.Fatalf("decode create message: %v", err)
	}
	if create["contextId"] != "ctx-1" {
		t.Fatalf("contextId = %q, want ctx-1", create["contextId"])
	}
	createBody := create["create"].(map[string]any)
	assertInworldPayload(t, createBody, "voiceId", "Ashley")
	assertInworldPayload(t, createBody, "modelId", "inworld-tts-1.5-max")
	if createBody["autoMode"] != true {
		t.Fatalf("autoMode = %#v, want true", createBody["autoMode"])
	}

	textPayload, err := buildInworldTTSSendTextMessage("ctx-1", "hello")
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var text map[string]any
	if err := json.Unmarshal(textPayload, &text); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	assertInworldPayload(t, text["send_text"].(map[string]any), "text", "hello")

	flushPayload, err := buildInworldTTSFlushContextMessage("ctx-1")
	if err != nil {
		t.Fatalf("build flush message: %v", err)
	}
	var flush map[string]any
	if err := json.Unmarshal(flushPayload, &flush); err != nil {
		t.Fatalf("decode flush message: %v", err)
	}
	if _, ok := flush["flush_context"]; !ok {
		t.Fatalf("flush message = %+v, want flush_context", flush)
	}
}

func TestInworldTTSAudioFromWebsocketMessage(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})
	audio, done, err := inworldAudioFromWebsocketMessage([]byte(`{"result":{"contextId":"ctx-1","audioChunk":{"audioContent":"`+encoded+`"}}}`), 24000)
	if err != nil {
		t.Fatalf("audio message: %v", err)
	}
	if done || string(audio.Frame.Data) != "\x01\x02" || audio.SegmentID != "ctx-1" {
		t.Fatalf("audio=%+v done=%v, want decoded segment audio", audio, done)
	}

	audio, done, err = inworldAudioFromWebsocketMessage([]byte(`{"result":{"contextId":"ctx-1","contextClosed":{}}}`), 24000)
	if err != nil {
		t.Fatalf("context closed message: %v", err)
	}
	if audio != nil || !done {
		t.Fatalf("audio=%+v done=%v, want context closed marker", audio, done)
	}
}

func TestInworldTTSStreamBuffersTextUntilFlush(t *testing.T) {
	stream := &inworldTTSSynthesizeStream{}
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

func TestInworldTTSEmptyStreamNextEOF(t *testing.T) {
	stream := &inworldTTSWebsocketChunkedStream{sampleRate: 24000}
	_, err := stream.Next()
	if err != io.EOF {
		t.Fatalf("Next err = %v, want EOF without websocket", err)
	}
}

func TestInworldTTSImplementsInterface(t *testing.T) {
	var _ tts.TTS = NewInworldTTS("test-key", "")
}

func assertInworldPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}
