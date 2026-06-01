package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestMinimaxTTSDefaultsMatchReference(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "")

	if provider.baseURL != "https://api-uw.minimax.io" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "speech-02-turbo" {
		t.Fatalf("model = %q, want speech-02-turbo", provider.model)
	}
	if provider.voice != "socialmedia_female_2_v1" {
		t.Fatalf("voice = %q, want default voice", provider.voice)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.bitrate != 128000 {
		t.Fatalf("bitrate = %d, want 128000", provider.bitrate)
	}
	if provider.audioFormat != "mp3" {
		t.Fatalf("audio format = %q, want mp3", provider.audioFormat)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want reference streaming support")
	}
}

func TestMinimaxTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "")

	req, err := buildMinimaxTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api-uw.minimax.io/v1/t2a_v2" {
		t.Fatalf("url = %q, want v1/t2a_v2 endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertMinimaxPayload(t, payload, "model", "speech-02-turbo")
	assertMinimaxPayload(t, payload, "text", "hello")
	if payload["stream"] != true {
		t.Fatalf("stream = %#v, want true", payload["stream"])
	}
	streamOptions := payload["stream_options"].(map[string]any)
	if streamOptions["exclude_aggregated_audio"] != true {
		t.Fatalf("exclude_aggregated_audio = %#v, want true", streamOptions["exclude_aggregated_audio"])
	}

	voiceSetting := payload["voice_setting"].(map[string]any)
	assertMinimaxPayload(t, voiceSetting, "voice_id", "socialmedia_female_2_v1")
	if voiceSetting["speed"] != 1.0 {
		t.Fatalf("speed = %#v, want 1.0", voiceSetting["speed"])
	}
	if voiceSetting["vol"] != 1.0 {
		t.Fatalf("vol = %#v, want 1.0", voiceSetting["vol"])
	}
	if voiceSetting["pitch"] != float64(0) {
		t.Fatalf("pitch = %#v, want 0", voiceSetting["pitch"])
	}

	audioSetting := payload["audio_setting"].(map[string]any)
	if audioSetting["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", audioSetting["sample_rate"])
	}
	if audioSetting["bitrate"] != float64(128000) {
		t.Fatalf("bitrate = %#v, want 128000", audioSetting["bitrate"])
	}
	assertMinimaxPayload(t, audioSetting, "format", "mp3")
	if audioSetting["channel"] != float64(1) {
		t.Fatalf("channel = %#v, want 1", audioSetting["channel"])
	}
}

func TestMinimaxTTSOptionsMatchReference(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "",
		WithMinimaxTTSBaseURL("https://minimax.example"),
		WithMinimaxTTSModel("speech-2.6-hd"),
		WithMinimaxTTSVoice("voice-2"),
		WithMinimaxTTSSampleRate(44100),
		WithMinimaxTTSBitrate(256000),
		WithMinimaxTTSAudioFormat("wav"),
		WithMinimaxTTSEmotion("fluent"),
		WithMinimaxTTSSpeed(1.4),
		WithMinimaxTTSVolume(2.0),
		WithMinimaxTTSPitch(-2),
		WithMinimaxTTSTextNormalization(true),
	)

	req, err := buildMinimaxTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://minimax.example/v1/t2a_v2" {
		t.Fatalf("url = %q, want custom v1/t2a_v2 endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertMinimaxPayload(t, payload, "model", "speech-2.6-hd")
	if payload["text_normalization"] != true {
		t.Fatalf("text_normalization = %#v, want true", payload["text_normalization"])
	}
	voiceSetting := payload["voice_setting"].(map[string]any)
	assertMinimaxPayload(t, voiceSetting, "voice_id", "voice-2")
	assertMinimaxPayload(t, voiceSetting, "emotion", "fluent")
	audioSetting := payload["audio_setting"].(map[string]any)
	assertMinimaxPayload(t, audioSetting, "format", "wav")
	if audioSetting["sample_rate"] != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", audioSetting["sample_rate"])
	}
	if audioSetting["bitrate"] != float64(256000) {
		t.Fatalf("bitrate = %#v, want 256000", audioSetting["bitrate"])
	}
}

func TestMinimaxTTSChunkedStreamDecodesReferenceSSEAudio(t *testing.T) {
	stream := &minimaxTTSChunkedStream{
		resp: &http.Response{
			Body:   io.NopCloser(bytes.NewReader([]byte("data: {\"data\":{\"audio\":\"0102\"},\"base_resp\":{\"status_code\":0}}\n\n"))),
			Header: http.Header{"Trace-Id": []string{"trace-123"}},
		},
		sampleRate: 16000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02}) {
		t.Fatalf("audio data = %#v, want decoded hex audio", audio.Frame.Data)
	}
	if audio.RequestID != "trace-123" {
		t.Fatalf("request id = %q, want trace header", audio.RequestID)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", audio.Frame.SampleRate)
	}
}

func TestMinimaxTTSWebsocketURLMatchesReference(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "", WithMinimaxTTSBaseURL("https://minimax.example"))

	if got := buildMinimaxTTSWebsocketURL(provider); got != "wss://minimax.example/ws/v1/t2a_v2" {
		t.Fatalf("websocket URL = %q, want reference websocket endpoint", got)
	}
}

func TestMinimaxTTSWebsocketMessagesMatchReference(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "",
		WithMinimaxTTSModel("speech-2.6-hd"),
		WithMinimaxTTSVoice("voice-2"),
		WithMinimaxTTSSampleRate(44100),
		WithMinimaxTTSBitrate(256000),
		WithMinimaxTTSAudioFormat("wav"),
		WithMinimaxTTSEmotion("fluent"),
	)

	startPayload, err := buildMinimaxTTSTaskStartMessage(provider)
	if err != nil {
		t.Fatalf("build start message: %v", err)
	}
	var start map[string]any
	if err := json.Unmarshal(startPayload, &start); err != nil {
		t.Fatalf("decode start message: %v", err)
	}
	assertMinimaxPayload(t, start, "event", "task_start")
	assertMinimaxPayload(t, start, "model", "speech-2.6-hd")
	voiceSetting := start["voice_setting"].(map[string]any)
	assertMinimaxPayload(t, voiceSetting, "voice_id", "voice-2")
	assertMinimaxPayload(t, voiceSetting, "emotion", "fluent")
	audioSetting := start["audio_setting"].(map[string]any)
	assertMinimaxPayload(t, audioSetting, "format", "wav")

	continuePayload, err := buildMinimaxTTSTaskContinueMessage("hello")
	if err != nil {
		t.Fatalf("build continue message: %v", err)
	}
	var cont map[string]any
	if err := json.Unmarshal(continuePayload, &cont); err != nil {
		t.Fatalf("decode continue message: %v", err)
	}
	assertMinimaxPayload(t, cont, "event", "task_continue")
	assertMinimaxPayload(t, cont, "text", "hello")

	finishPayload, err := buildMinimaxTTSTaskFinishMessage()
	if err != nil {
		t.Fatalf("build finish message: %v", err)
	}
	var finish map[string]any
	if err := json.Unmarshal(finishPayload, &finish); err != nil {
		t.Fatalf("decode finish message: %v", err)
	}
	assertMinimaxPayload(t, finish, "event", "task_finish")
}

func TestMinimaxTTSAudioFromWebsocketMessage(t *testing.T) {
	audio, done, err := minimaxAudioFromWebsocketMessage([]byte(`{"event":"task_continued","trace_id":"trace-1","data":{"audio":"0102"}}`), 24000, "seg-1")
	if err != nil {
		t.Fatalf("audio message: %v", err)
	}
	if done || string(audio.Frame.Data) != "\x01\x02" || audio.RequestID != "trace-1" || audio.SegmentID != "seg-1" {
		t.Fatalf("audio=%+v done=%v, want decoded websocket audio", audio, done)
	}

	audio, done, err = minimaxAudioFromWebsocketMessage([]byte(`{"event":"task_finished"}`), 24000, "seg-1")
	if err != nil {
		t.Fatalf("finish message: %v", err)
	}
	if audio != nil || !done {
		t.Fatalf("audio=%+v done=%v, want task finished marker", audio, done)
	}
}

func TestMinimaxTTSStreamBuffersTextUntilFlush(t *testing.T) {
	stream := &minimaxTTSSynthesizeStream{}
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

func TestMinimaxTTSImplementsInterface(t *testing.T) {
	var _ tts.TTS = NewMinimaxTTS("test-key", "")
}

func assertMinimaxPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}
