package hume

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

func TestHumeTTSDefaultsMatchReference(t *testing.T) {
	provider := NewHumeTTS("test-key", "")

	if provider.baseURL != "https://api.hume.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.modelVersion != "1" {
		t.Fatalf("model version = %q, want 1", provider.modelVersion)
	}
	if provider.voiceName != "Male English Actor" {
		t.Fatalf("voice name = %q, want reference default voice", provider.voiceName)
	}
	if provider.voiceProvider != "HUME_AI" {
		t.Fatalf("voice provider = %q, want HUME_AI", provider.voiceProvider)
	}
	if provider.audioFormat != "mp3" {
		t.Fatalf("audio format = %q, want mp3", provider.audioFormat)
	}
	if !provider.instantMode {
		t.Fatalf("instant mode = false, want true when voice is configured")
	}
	if provider.SampleRate() != 48000 {
		t.Fatalf("sample rate = %d, want reference supported sample rate", provider.SampleRate())
	}
	if got := coretts.Model(provider); got != "Octave" {
		t.Fatalf("model metadata = %q, want Octave", got)
	}
	if got := coretts.Provider(provider); got != "Hume" {
		t.Fatalf("provider metadata = %q, want Hume", got)
	}
}

func TestNewHumeTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("HUME_API_KEY", "env-key")

	provider := NewHumeTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildHumeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("X-Hume-Api-Key"); got != "env-key" {
		t.Fatalf("X-Hume-Api-Key = %q, want env key", got)
	}

	explicit := NewHumeTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestHumeTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("HUME_API_KEY", "")
	provider := NewHumeTTS("", "")

	_, err := buildHumeTTSRequest(context.Background(), provider, "hello")
	if err == nil {
		t.Fatal("build request returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "HUME_API_KEY") {
		t.Fatalf("build request error = %q, want HUME_API_KEY guidance", err)
	}
}

func TestHumeTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewHumeTTS("test-key", "")

	req, err := buildHumeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.hume.ai/v0/tts/stream/json" {
		t.Fatalf("url = %q, want stream json endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-Hume-Api-Key"); got != "test-key" {
		t.Fatalf("api key = %q, want test key", got)
	}
	if got := req.Header.Get("X-Hume-Client-Name"); got != "livekit" {
		t.Fatalf("client name = %q, want livekit", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["version"] != "1" {
		t.Fatalf("version = %#v, want 1", payload["version"])
	}
	if payload["strip_headers"] != true {
		t.Fatalf("strip_headers = %#v, want true", payload["strip_headers"])
	}
	if payload["instant_mode"] != true {
		t.Fatalf("instant_mode = %#v, want true", payload["instant_mode"])
	}
	format := payload["format"].(map[string]any)
	if format["type"] != "mp3" {
		t.Fatalf("format type = %#v, want mp3", format["type"])
	}
	utterances := payload["utterances"].([]any)
	utterance := utterances[0].(map[string]any)
	if utterance["text"] != "hello" {
		t.Fatalf("utterance text = %#v, want hello", utterance["text"])
	}
	voice := utterance["voice"].(map[string]any)
	if voice["name"] != "Male English Actor" {
		t.Fatalf("voice name = %#v, want reference default", voice["name"])
	}
	if voice["provider"] != "HUME_AI" {
		t.Fatalf("voice provider = %#v, want HUME_AI", voice["provider"])
	}
}

func TestHumeTTSOptionsMatchReference(t *testing.T) {
	provider := NewHumeTTS("test-key", "",
		WithHumeTTSBaseURL("https://hume.example/"),
		WithHumeTTSModelVersion("2"),
		WithHumeTTSVoiceName("Narrator", "CUSTOM_VOICE"),
		WithHumeTTSAudioFormat("wav"),
		WithHumeTTSDescription("calm"),
		WithHumeTTSSpeed(1.2),
		WithHumeTTSTrailingSilence(0.4),
		WithHumeTTSInstantMode(false),
	)

	req, err := buildHumeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://hume.example/v0/tts/stream/json" {
		t.Fatalf("url = %q, want custom stream json endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["version"] != "2" {
		t.Fatalf("version = %#v, want 2", payload["version"])
	}
	if payload["instant_mode"] != false {
		t.Fatalf("instant_mode = %#v, want false", payload["instant_mode"])
	}
	format := payload["format"].(map[string]any)
	if format["type"] != "wav" {
		t.Fatalf("format type = %#v, want wav", format["type"])
	}
	utterance := payload["utterances"].([]any)[0].(map[string]any)
	if utterance["description"] != "calm" {
		t.Fatalf("description = %#v, want calm", utterance["description"])
	}
	if utterance["speed"] != float64(1.2) {
		t.Fatalf("speed = %#v, want 1.2", utterance["speed"])
	}
	if utterance["trailing_silence"] != float64(0.4) {
		t.Fatalf("trailing_silence = %#v, want 0.4", utterance["trailing_silence"])
	}
	voice := utterance["voice"].(map[string]any)
	if voice["name"] != "Narrator" {
		t.Fatalf("voice name = %#v, want Narrator", voice["name"])
	}
	if voice["provider"] != "CUSTOM_VOICE" {
		t.Fatalf("voice provider = %#v, want CUSTOM_VOICE", voice["provider"])
	}
}

func TestHumeTTSRejectsInstantModeWithoutVoice(t *testing.T) {
	provider := NewHumeTTS("test-key", "",
		WithHumeTTSVoiceName("", ""),
		WithHumeTTSInstantMode(true),
	)

	_, err := buildHumeTTSRequest(context.Background(), provider, "hello")
	if err == nil {
		t.Fatal("build request returned nil error, want instant mode without voice error")
	}
	if !strings.Contains(err.Error(), "instant_mode cannot be enabled without specifying a voice") {
		t.Fatalf("build request error = %q, want instant_mode voice guidance", err)
	}
}

func TestHumeTTSVoiceIDBuildsReferencePayload(t *testing.T) {
	provider := NewHumeTTS("test-key", "",
		WithHumeTTSVoiceID("voice-123", "CUSTOM_VOICE"),
	)

	req, err := buildHumeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	utterance := payload["utterances"].([]any)[0].(map[string]any)
	voice := utterance["voice"].(map[string]any)
	if voice["id"] != "voice-123" {
		t.Fatalf("voice id = %#v, want voice-123", voice["id"])
	}
	if _, ok := voice["name"]; ok {
		t.Fatalf("voice name included for id voice: %#v", voice)
	}
	if voice["provider"] != "CUSTOM_VOICE" {
		t.Fatalf("voice provider = %#v, want CUSTOM_VOICE", voice["provider"])
	}
}

func TestHumeTTSContextBuildsReferencePayload(t *testing.T) {
	provider := NewHumeTTS("test-key", "",
		WithHumeTTSContextGenerationID("generation-1"),
	)

	req, err := buildHumeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build generation context request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode generation context body: %v", err)
	}
	contextPayload := payload["context"].(map[string]any)
	if contextPayload["generation_id"] != "generation-1" {
		t.Fatalf("generation_id = %#v, want generation-1", contextPayload["generation_id"])
	}

	speed := 1.1
	trailingSilence := 0.2
	provider = NewHumeTTS("test-key", "",
		WithHumeTTSContextUtterances([]HumeTTSUtterance{
			{
				Text:            "previous line",
				Description:     "warm",
				Speed:           &speed,
				TrailingSilence: &trailingSilence,
				Voice:           &HumeTTSVoice{Name: "Narrator", Provider: "CUSTOM_VOICE"},
			},
		}),
	)

	req, err = buildHumeTTSRequest(context.Background(), provider, "next line")
	if err != nil {
		t.Fatalf("build utterance context request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode utterance context body: %v", err)
	}
	contextPayload = payload["context"].(map[string]any)
	utterance := contextPayload["utterances"].([]any)[0].(map[string]any)
	if utterance["text"] != "previous line" || utterance["description"] != "warm" {
		t.Fatalf("context utterance = %#v, want previous warm line", utterance)
	}
	if utterance["speed"] != float64(1.1) || utterance["trailing_silence"] != float64(0.2) {
		t.Fatalf("context utterance timing = %#v, want speed and trailing silence", utterance)
	}
	voice := utterance["voice"].(map[string]any)
	if voice["name"] != "Narrator" || voice["provider"] != "CUSTOM_VOICE" {
		t.Fatalf("context voice = %#v, want Narrator custom voice", voice)
	}
}

func TestHumeTTSChunkedStreamDecodesReferenceJSONLines(t *testing.T) {
	stream := &humeTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte("{\"audio\":\"AQI=\"}\n\n")))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02}) {
		t.Fatalf("audio data = %#v, want decoded base64 audio", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want 48000", audio.Frame.SampleRate)
	}
}

func TestHumeTTSChunkedStreamDecodesReferenceMP3JSONLines(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	line := `{"audio":"` + base64.StdEncoding.EncodeToString(mp3Data) + `"}` + "\n"
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: humeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(line)),
			Request:    r,
		}, nil
	})}

	provider := NewHumeTTS("test-key", "", WithHumeTTSBaseURL("https://hume.example"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
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

func TestHumeTTSChunkedStreamDecodesReferenceWAVJSONLines(t *testing.T) {
	wav := humeTestWAVPCM16(16000, 1, []byte{0x01, 0x02, 0x03, 0x04})
	line := `{"audio":"` + base64.StdEncoding.EncodeToString(wav) + `"}` + "\n"
	stream := &humeTTSChunkedStream{
		resp:        &http.Response{Body: io.NopCloser(strings.NewReader(line))},
		audioFormat: "wav",
		sampleRate:  48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("audio data = %#v, want WAV PCM payload without RIFF header", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want WAV sample rate 16000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want WAV mono", audio.Frame.NumChannels)
	}
}

func TestHumeTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &humeCloseCountBody{Reader: strings.NewReader("audio")}
	stream := &humeTTSChunkedStream{resp: &http.Response{Body: body}, audioFormat: "pcm", sampleRate: 48000}

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

func TestHumeTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	stream := &humeTTSChunkedStream{
		resp:        &http.Response{Body: io.NopCloser(strings.NewReader(`{"audio":"AQI="}` + "\n"))},
		audioFormat: "pcm",
		sampleRate:  48000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %T %v, want EOF", err, err)
	}
}

func TestHumeTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: humeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limit"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewHumeTTS("test-key", "", WithHumeTTSBaseURL("https://hume.example"))
	_, err := provider.Synthesize(context.Background(), "hello")
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if statusErr.Body != `{"error":"rate limit"}` {
		t.Fatalf("body = %#v, want provider body", statusErr.Body)
	}
}

func TestHumeTTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: humeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})}

	provider := NewHumeTTS("test-key", "", WithHumeTTSBaseURL("https://hume.example"))
	_, err := provider.Synthesize(context.Background(), "hello")
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Synthesize error = %T %v, want APIConnectionError", err, err)
	}
}

type humeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f humeRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type humeCloseCountBody struct {
	*strings.Reader
	closeCount int
}

func (b *humeCloseCountBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("closed twice")
	}
	return nil
}

func humeTestWAVPCM16(sampleRate uint32, channels uint16, pcm []byte) []byte {
	buf := bytes.NewBuffer(nil)
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+len(pcm)))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, channels)
	_ = binary.Write(buf, binary.LittleEndian, sampleRate)
	byteRate := sampleRate * uint32(channels) * 2
	_ = binary.Write(buf, binary.LittleEndian, byteRate)
	blockAlign := channels * 2
	_ = binary.Write(buf, binary.LittleEndian, blockAlign)
	_ = binary.Write(buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)
	return buf.Bytes()
}
