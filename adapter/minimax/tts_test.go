package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
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
		t.Fatal("streaming = false, want true for websocket streaming")
	}
}

func TestNewMinimaxTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "env-key")

	provider := NewMinimaxTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	if got := buildMinimaxTTSWebsocketHeaders(provider).Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("authorization = %q, want env bearer key", got)
	}

	explicit := NewMinimaxTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
	if got := buildMinimaxTTSWebsocketHeaders(explicit).Get("Authorization"); got != "Bearer explicit-key" {
		t.Fatalf("authorization = %q, want explicit bearer key", got)
	}
}

func TestNewMinimaxTTSUsesEnvironmentBaseURL(t *testing.T) {
	t.Setenv("MINIMAX_BASE_URL", "https://minimax.env")

	provider := NewMinimaxTTS("test-key", "")

	if provider.baseURL != "https://minimax.env" {
		t.Fatalf("base URL = %q, want env base URL", provider.baseURL)
	}
	req, err := buildMinimaxTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://minimax.env/v1/t2a_v2" {
		t.Fatalf("url = %q, want env base URL endpoint", req.URL.String())
	}

	explicit := NewMinimaxTTS("test-key", "", WithMinimaxTTSBaseURL("https://minimax.explicit"))
	if explicit.baseURL != "https://minimax.explicit" {
		t.Fatalf("base URL = %q, want explicit base URL", explicit.baseURL)
	}
}

func TestMinimaxTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "")
	provider := NewMinimaxTTS("", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, synthErr := provider.Synthesize(ctx, "hello")
	if synthErr == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(synthErr.Error(), "MINIMAX_API_KEY") {
		t.Fatalf("Synthesize error = %q, want MINIMAX_API_KEY guidance", synthErr)
	}

	_, streamErr := provider.Stream(ctx)
	if streamErr == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(streamErr.Error(), "MINIMAX_API_KEY") {
		t.Fatalf("Stream error = %q, want MINIMAX_API_KEY guidance", streamErr)
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
		WithMinimaxTTSIntensity(75),
		WithMinimaxTTSTimbre(-40),
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
	voiceModify := payload["voice_modify"].(map[string]any)
	if voiceModify["intensity"] != float64(75) {
		t.Fatalf("voice_modify.intensity = %#v, want 75", voiceModify["intensity"])
	}
	if voiceModify["timbre"] != float64(-40) {
		t.Fatalf("voice_modify.timbre = %#v, want -40", voiceModify["timbre"])
	}
}

func TestMinimaxTTSRejectsInvalidSpeedBeforeRequest(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "", WithMinimaxTTSSpeed(0.4))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, synthErr := provider.Synthesize(ctx, "hello")
	if synthErr == nil {
		t.Fatal("Synthesize returned nil error, want speed validation error")
	}
	if !strings.Contains(synthErr.Error(), "speed must be between 0.5 and 2.0") {
		t.Fatalf("Synthesize error = %q, want speed range guidance", synthErr)
	}

	_, streamErr := provider.Stream(ctx)
	if streamErr == nil {
		t.Fatal("Stream returned nil error, want speed validation error")
	}
	if !strings.Contains(streamErr.Error(), "speed must be between 0.5 and 2.0") {
		t.Fatalf("Stream error = %q, want speed range guidance", streamErr)
	}
}

func TestMinimaxTTSRejectsInvalidVoiceModifyBeforeRequest(t *testing.T) {
	tests := []struct {
		name    string
		option  MinimaxTTSOption
		message string
	}{
		{
			name:    "intensity",
			option:  WithMinimaxTTSIntensity(101),
			message: "intensity must be between -100 and 100",
		},
		{
			name:    "timbre",
			option:  WithMinimaxTTSTimbre(-101),
			message: "timbre must be between -100 and 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewMinimaxTTS("test-key", "", tt.option)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			_, synthErr := provider.Synthesize(ctx, "hello")
			if synthErr == nil {
				t.Fatal("Synthesize returned nil error, want voice modify validation error")
			}
			if !strings.Contains(synthErr.Error(), tt.message) {
				t.Fatalf("Synthesize error = %q, want %q", synthErr, tt.message)
			}

			_, streamErr := provider.Stream(ctx)
			if streamErr == nil {
				t.Fatal("Stream returned nil error, want voice modify validation error")
			}
			if !strings.Contains(streamErr.Error(), tt.message) {
				t.Fatalf("Stream error = %q, want %q", streamErr, tt.message)
			}
		})
	}
}

func TestMinimaxTTSRejectsFluentEmotionForUnsupportedModel(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "",
		WithMinimaxTTSModel("speech-02-turbo"),
		WithMinimaxTTSEmotion("fluent"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, synthErr := provider.Synthesize(ctx, "hello")
	if synthErr == nil {
		t.Fatal("Synthesize returned nil error, want fluent emotion validation error")
	}
	if !strings.Contains(synthErr.Error(), `"fluent" emotion is only supported by speech-2.6-* models`) {
		t.Fatalf("Synthesize error = %q, want fluent model guidance", synthErr)
	}

	_, streamErr := provider.Stream(ctx)
	if streamErr == nil {
		t.Fatal("Stream returned nil error, want fluent emotion validation error")
	}
	if !strings.Contains(streamErr.Error(), `"fluent" emotion is only supported by speech-2.6-* models`) {
		t.Fatalf("Stream error = %q, want fluent model guidance", streamErr)
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

func TestMinimaxTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "", WithMinimaxTTSBaseURL("https://minimax.example"))

	wsURL := buildMinimaxTTSWebsocketURL(provider)
	if wsURL.String() != "wss://minimax.example/ws/v1/t2a_v2" {
		t.Fatalf("websocket URL = %q, want reference websocket endpoint", wsURL.String())
	}

	httpProvider := NewMinimaxTTS("test-key", "", WithMinimaxTTSBaseURL("http://minimax.example"))
	httpURL := buildMinimaxTTSWebsocketURL(httpProvider)
	if httpURL.String() != "ws://minimax.example/ws/v1/t2a_v2" {
		t.Fatalf("http websocket URL = %q, want ws endpoint", httpURL.String())
	}

	headers := buildMinimaxTTSWebsocketHeaders(provider)
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func TestMinimaxTTSWebsocketMessagesMatchReference(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "voice-1",
		WithMinimaxTTSModel("speech-2.6-hd"),
		WithMinimaxTTSSampleRate(44100),
		WithMinimaxTTSBitrate(256000),
		WithMinimaxTTSAudioFormat("wav"),
		WithMinimaxTTSEmotion("fluent"),
	)

	start, err := buildMinimaxTTSTaskStartMessage(provider)
	if err != nil {
		t.Fatalf("build start message: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(start, &payload); err != nil {
		t.Fatalf("decode start message: %v", err)
	}
	assertMinimaxPayload(t, payload, "event", "task_start")
	assertMinimaxPayload(t, payload, "model", "speech-2.6-hd")
	voiceSetting := payload["voice_setting"].(map[string]any)
	assertMinimaxPayload(t, voiceSetting, "voice_id", "voice-1")
	assertMinimaxPayload(t, voiceSetting, "emotion", "fluent")
	audioSetting := payload["audio_setting"].(map[string]any)
	assertMinimaxPayload(t, audioSetting, "format", "wav")
	if audioSetting["sample_rate"] != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", audioSetting["sample_rate"])
	}
	if audioSetting["bitrate"] != float64(256000) {
		t.Fatalf("bitrate = %#v, want 256000", audioSetting["bitrate"])
	}

	continued, err := buildMinimaxTTSTaskContinueMessage("hello")
	if err != nil {
		t.Fatalf("build continue message: %v", err)
	}
	payload = map[string]any{}
	if err := json.Unmarshal(continued, &payload); err != nil {
		t.Fatalf("decode continue message: %v", err)
	}
	assertMinimaxPayload(t, payload, "event", "task_continue")
	assertMinimaxPayload(t, payload, "text", "hello")

	finished, err := buildMinimaxTTSTaskFinishMessage()
	if err != nil {
		t.Fatalf("build finish message: %v", err)
	}
	payload = map[string]any{}
	if err := json.Unmarshal(finished, &payload); err != nil {
		t.Fatalf("decode finish message: %v", err)
	}
	assertMinimaxPayload(t, payload, "event", "task_finish")
}

func TestMinimaxTTSAudioFromWebsocketMessage(t *testing.T) {
	audio, done, traceID, err := minimaxAudioFromWebsocketMessage([]byte(`{"event":"task_continued","trace_id":"trace-1","data":{"audio":"01020304"}}`), "fallback", 24000)
	if err != nil {
		t.Fatalf("audio from websocket message: %v", err)
	}
	if done {
		t.Fatal("done = true for task_continued")
	}
	if traceID != "trace-1" {
		t.Fatalf("trace id = %q, want trace-1", traceID)
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded audio frame", audio)
	}
	if audio.RequestID != "trace-1" {
		t.Fatalf("request id = %q, want trace id", audio.RequestID)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24000 Hz mono", audio.Frame)
	}

	started, done, traceID, err := minimaxAudioFromWebsocketMessage([]byte(`{"event":"task_started","session_id":"session-1","base_resp":{"trace_id":"trace-2","status_code":0}}`), "fallback", 24000)
	if err != nil {
		t.Fatalf("task_started message: %v", err)
	}
	if started != nil || done || traceID != "trace-2" {
		t.Fatalf("started=%+v done=%v trace=%q, want no audio and trace-2", started, done, traceID)
	}

	finished, done, traceID, err := minimaxAudioFromWebsocketMessage([]byte(`{"event":"task_finished"}`), "fallback", 24000)
	if err != nil {
		t.Fatalf("task_finished message: %v", err)
	}
	if finished != nil || !done || traceID != "fallback" {
		t.Fatalf("finished=%+v done=%v trace=%q, want done with fallback trace", finished, done, traceID)
	}

	if _, _, _, err := minimaxAudioFromWebsocketMessage([]byte(`{"base_resp":{"status_code":1001,"status_msg":"bad text"}}`), "fallback", 24000); err == nil {
		t.Fatal("error response returned nil error, want stream error")
	}
	if _, _, _, err := minimaxAudioFromWebsocketMessage([]byte(`{"event":"task_failed","trace_id":"trace-3"}`), "fallback", 24000); err == nil {
		t.Fatal("task_failed returned nil error, want stream error")
	}
}

func assertMinimaxPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}
