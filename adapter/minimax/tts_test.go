package minimax

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestMinimaxTTSDefaultsMatchReference(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "")

	if provider.baseURL != "https://api-uw.minimax.io" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "speech-02-turbo" {
		t.Fatalf("model = %q, want speech-02-turbo", provider.model)
	}
	if got := tts.Model(provider); got != "speech-02-turbo" {
		t.Fatalf("model metadata = %q, want speech-02-turbo", got)
	}
	if got := tts.Provider(provider); got != "MiniMax" {
		t.Fatalf("provider metadata = %q, want MiniMax", got)
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

func TestMinimaxTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: minimaxRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewMinimaxTTS("test-key", "")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize returned nil error, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if body, ok := statusErr.Body.(string); !ok || body != `{"error":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
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
		WithMinimaxTTSLanguageBoost("Spanish"),
		WithMinimaxTTSPronunciationDict(map[string][]string{"LiveKit": {"live kit"}}),
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
	assertMinimaxPayload(t, payload, "language_boost", "Spanish")
	pronunciationDict := payload["pronunciation_dict"].(map[string]any)
	entries := pronunciationDict["LiveKit"].([]any)
	if len(entries) != 1 || entries[0] != "live kit" {
		t.Fatalf("pronunciation_dict.LiveKit = %#v, want [live kit]", pronunciationDict["LiveKit"])
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

func TestMinimaxTTSChunkedStreamStatusPayloadReturnsAPIStatusError(t *testing.T) {
	stream := &minimaxTTSChunkedStream{
		resp: &http.Response{
			Body:   io.NopCloser(bytes.NewReader([]byte("data: {\"trace_id\":\"trace-body\",\"base_resp\":{\"status_code\":1001,\"status_msg\":\"bad text\"}}\n\n"))),
			Header: http.Header{"Trace-Id": []string{"trace-header"}},
		},
		sampleRate: 16000,
	}
	defer stream.Close()

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next returned nil error, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != 1001 {
		t.Fatalf("status code = %d, want 1001", statusErr.StatusCode)
	}
	if statusErr.RequestID != "trace-body" {
		t.Fatalf("request id = %q, want body trace id", statusErr.RequestID)
	}
	body, ok := statusErr.Body.(map[string]any)
	if !ok {
		t.Fatalf("body = %T, want decoded payload map", statusErr.Body)
	}
	if body["trace_id"] != "trace-body" {
		t.Fatalf("body trace_id = %#v, want trace-body", body["trace_id"])
	}
}

func TestMinimaxTTSChunkedStreamEmitsReferenceFinalMarkerAfterSSEAudio(t *testing.T) {
	stream := &minimaxTTSChunkedStream{
		resp: &http.Response{
			Body:   io.NopCloser(bytes.NewReader([]byte("data: {\"data\":{\"audio\":\"0102\"},\"base_resp\":{\"status_code\":0}}\n\n"))),
			Header: http.Header{"Trace-Id": []string{"trace-sse-final"}},
		},
		audioFormat: "pcm",
		sampleRate:  16000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if audio == nil || audio.IsFinal || audio.Frame == nil {
		t.Fatalf("first audio = %#v, want SSE audio frame", audio)
	}
	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error before final marker: %v", err)
	}
	if audio == nil || !audio.IsFinal || audio.RequestID != "trace-sse-final" {
		t.Fatalf("second audio = %#v, want final marker with trace id", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker err = %v, want EOF", err)
	}
}

func TestMinimaxTTSChunkedStreamDecodesReferenceMP3SSEAudio(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	line := `data: {"data":{"audio":"` + hex.EncodeToString(mp3Data) + `"},"base_resp":{"status_code":0}}` + "\n\n"
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: minimaxRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(line)),
			Header:     http.Header{"Trace-Id": []string{"trace-mp3"}},
			Request:    r,
		}, nil
	})}

	provider := NewMinimaxTTS("test-key", "voice-1", WithMinimaxTTSBaseURL("https://minimax.example"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.RequestID != "trace-mp3" {
		t.Fatalf("request id = %q, want trace-mp3", audio.RequestID)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want decoded mp3 rate 48000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 2 {
		t.Fatalf("channels = %d, want decoded mp3 stereo", audio.Frame.NumChannels)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	if bytes.Equal(audio.Frame.Data, mp3Data[:len(audio.Frame.Data)]) {
		t.Fatal("frame data still contains compressed mp3 bytes")
	}
}

func TestMinimaxTTSChunkedStreamEmitsReferenceMP3FinalMarker(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	line := `data: {"data":{"audio":"` + hex.EncodeToString(mp3Data) + `"},"base_resp":{"status_code":0}}` + "\n\n"
	stream := &minimaxTTSChunkedStream{
		resp: &http.Response{
			Body:   io.NopCloser(strings.NewReader(line)),
			Header: http.Header{"Trace-Id": []string{"trace-final"}},
		},
		audioFormat: "mp3",
		sampleRate:  24000,
	}
	defer stream.Close()

	frames := 0
	for range 5000 {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next returned error before final marker after %d frames: %v", frames, err)
		}
		if audio == nil {
			t.Fatalf("Next returned nil audio before final marker after %d frames", frames)
		}
		if audio.IsFinal {
			if frames == 0 {
				t.Fatal("final marker arrived before decoded audio")
			}
			if audio.RequestID != "trace-final" {
				t.Fatalf("final request id = %q, want trace-final", audio.RequestID)
			}
			if _, err := stream.Next(); err != io.EOF {
				t.Fatalf("Next after final marker err = %v, want EOF", err)
			}
			return
		}
		if audio.RequestID != "trace-final" {
			t.Fatalf("frame request id = %q, want trace-final", audio.RequestID)
		}
		if len(audio.Frame.Data) == 0 {
			t.Fatalf("frame %d is empty", frames)
		}
		frames++
	}
	t.Fatalf("stream did not emit final marker after %d frames", frames)
}

func TestMinimaxTTSChunkedStreamEmitsReferenceMP3FinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &minimaxTTSChunkedStream{
		resp: &http.Response{
			Body:   io.NopCloser(strings.NewReader("data: {\"base_resp\":{\"status_code\":0}}\n\n")),
			Header: http.Header{"Trace-Id": []string{"trace-empty"}},
		},
		audioFormat: "mp3",
		sampleRate:  24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal || audio.RequestID != "trace-empty" {
		t.Fatalf("Next = %+v, want final marker with trace id", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestMinimaxTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &minimaxCloseCountBody{Reader: strings.NewReader("data: {\"data\":{\"audio\":\"0102\"},\"base_resp\":{\"status_code\":0}}\n\n")}
	stream := &minimaxTTSChunkedStream{resp: &http.Response{Body: body}, sampleRate: 24000}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close() calls = %d, want 1", body.closeCount)
	}
}

func TestMinimaxTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	stream := &minimaxTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(strings.NewReader("data: {\"data\":{\"audio\":\"0102\"},\"base_resp\":{\"status_code\":0}}\n\n"))},
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %T %v, want EOF", err, err)
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

func TestMinimaxTTSStreamSendsSentencesAndFlushesTailLikeReference(t *testing.T) {
	var writes []string
	stream := &minimaxTTSSynthesizeStream{
		writeMessage: func(payload []byte) error {
			var msg map[string]any
			if err := json.Unmarshal(payload, &msg); err != nil {
				t.Fatalf("decode stream write: %v", err)
			}
			event, _ := msg["event"].(string)
			if event == "task_continue" {
				text, _ := msg["text"].(string)
				writes = append(writes, "text:"+text)
				return nil
			}
			writes = append(writes, event)
			return nil
		},
	}

	if err := stream.PushText("This first sentence is definitely long enough. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	wantAfterPush := []string{"text:This first sentence is definitely long enough."}
	if strings.Join(writes, "|") != strings.Join(wantAfterPush, "|") {
		t.Fatalf("writes after PushText = %#v, want %#v", writes, wantAfterPush)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	wantAfterFlush := []string{
		"text:This first sentence is definitely long enough.",
		"text:Tail",
	}
	if strings.Join(writes, "|") != strings.Join(wantAfterFlush, "|") {
		t.Fatalf("writes after Flush = %#v, want %#v", writes, wantAfterFlush)
	}
}

func TestMinimaxTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &minimaxTTSSynthesizeStream{
		cancel: func() { cancelled = true },
		writeMessage: func([]byte) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("This sentence is definitely long enough. Tail"); !errors.Is(err, writeErr) {
		t.Fatalf("PushText error = %v, want write error", err)
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

func TestMinimaxTTSProviderCloseClosesActiveStreams(t *testing.T) {
	provider := NewMinimaxTTS("test-key", "")
	cancelled := false
	closeCalls := 0
	stream := &minimaxTTSSynthesizeStream{
		cancel: func() { cancelled = true },
		writeMessage: func([]byte) error {
			return nil
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !cancelled {
		t.Fatal("cancel not called")
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
}

func TestMinimaxTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &minimaxTTSSynthesizeStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *tts.SynthesizedAudio),
		errCh:  make(chan error, 1),
		writeMessage: func([]byte) error {
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next() after Close audio = %#v, want nil", audio)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %v, want EOF", err)
	}
}

func TestMinimaxTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &minimaxTTSSynthesizeStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		closed: true,
	}
	stream.events <- &tts.SynthesizedAudio{RequestID: "stale"}

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestMinimaxTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: minimaxRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}

	provider := NewMinimaxTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if httpCalls != 0 {
		t.Fatalf("HTTP calls after Close = %d, want 0", httpCalls)
	}
}

func TestMinimaxTTSStreamAfterCloseIsRejected(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	dialCalls := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls++
			return nil, errors.New("unexpected websocket dial")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewMinimaxTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
	if dialCalls != 0 {
		t.Fatalf("websocket dials after Close = %d, want 0", dialCalls)
	}
}

func TestMinimaxTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	conn := newMinimaxProviderCloseWebsocketConn(t, websocket.CloseUnsupportedData)

	stream := &minimaxTTSSynthesizeStream{
		conn:       conn,
		traceID:    "trace-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("readLoop error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != websocket.CloseUnsupportedData {
			t.Fatalf("StatusCode = %d, want close code", statusErr.StatusCode)
		}
		if statusErr.RequestID != "trace-1" {
			t.Fatalf("RequestID = %q, want trace id", statusErr.RequestID)
		}
		if !strings.Contains(err.Error(), "MiniMax connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want MiniMax close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close error")
	}
}

func TestMinimaxTTSStreamNormalCloseBeforeTaskFinishedReturnsAPIStatusError(t *testing.T) {
	conn := newMinimaxProviderCloseWebsocketConn(t, websocket.CloseNormalClosure)

	stream := &minimaxTTSSynthesizeStream{
		conn:       conn,
		traceID:    "trace-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("readLoop error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != websocket.CloseNormalClosure {
			t.Fatalf("StatusCode = %d, want normal close code", statusErr.StatusCode)
		}
		if statusErr.RequestID != "trace-1" {
			t.Fatalf("RequestID = %q, want trace id", statusErr.RequestID)
		}
		if !strings.Contains(err.Error(), "MiniMax connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want MiniMax close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket normal close error")
	}
}

func newMinimaxProviderCloseWebsocketConn(t *testing.T, closeCode int) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	listener := newMinimaxSingleConnListener(serverConn)
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(closeCode, ""),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	})}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			serverErr <- err
		}
	}()
	dialer := websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	conn, _, err := dialer.Dial("ws://minimax.test/ws/v1/t2a_v2", nil)
	if err != nil {
		clientConn.Close()
		t.Fatalf("dial test websocket: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		_ = conn.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case err := <-serverErr:
			t.Errorf("test websocket server error: %v", err)
		default:
		}
	})
	return conn
}

type minimaxSingleConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newMinimaxSingleConnListener(conn net.Conn) *minimaxSingleConnListener {
	return &minimaxSingleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *minimaxSingleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() {
		conn = l.conn
	})
	if conn != nil {
		return conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *minimaxSingleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *minimaxSingleConnListener) Addr() net.Addr {
	return minimaxTestAddr("minimax.test:443")
}

type minimaxTestAddr string

func (a minimaxTestAddr) Network() string { return "tcp" }

func (a minimaxTestAddr) String() string { return string(a) }

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

	audio, done, traceID, err = minimaxAudioFromWebsocketMessage([]byte(`{"event":"task_continued","trace_id":"trace-final","is_final":true,"data":{"audio":"0506"}}`), "fallback", 24000)
	if err != nil {
		t.Fatalf("final task_continued message: %v", err)
	}
	if done {
		t.Fatal("done = true for final task_continued, want task_finished to own stream finality")
	}
	if traceID != "trace-final" {
		t.Fatalf("final task_continued trace id = %q, want trace-final", traceID)
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{5, 6}) {
		t.Fatalf("final task_continued audio = %+v, want decoded audio frame", audio)
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
	if finished == nil || !finished.IsFinal || !done || traceID != "fallback" {
		t.Fatalf("finished=%+v done=%v trace=%q, want final marker with fallback trace", finished, done, traceID)
	}
	if finished.Frame != nil {
		t.Fatalf("final marker frame = %+v, want boundary-only marker", finished.Frame)
	}
	if finished.RequestID != "fallback" {
		t.Fatalf("final marker request id = %q, want fallback", finished.RequestID)
	}

	if _, _, _, err := minimaxAudioFromWebsocketMessage([]byte(`{"trace_id":"trace-error","base_resp":{"status_code":1001,"status_msg":"bad text"}}`), "fallback", 24000); err == nil {
		t.Fatal("error response returned nil error, want stream error")
	} else {
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("error response = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != 1001 {
			t.Fatalf("status code = %d, want 1001", statusErr.StatusCode)
		}
		if statusErr.RequestID != "trace-error" {
			t.Fatalf("request id = %q, want trace-error", statusErr.RequestID)
		}
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

type minimaxRoundTripFunc func(*http.Request) (*http.Response, error)

func (f minimaxRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type minimaxCloseCountBody struct {
	*strings.Reader
	closeCount int
}

func (b *minimaxCloseCountBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("closed twice")
	}
	return nil
}
