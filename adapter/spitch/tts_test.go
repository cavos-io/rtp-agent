package spitch

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	corestt "github.com/cavos-io/rtp-agent/core/stt"
	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

func TestSpitchTTSDefaultsMatchReference(t *testing.T) {
	provider := NewSpitchTTS("test-key", "")

	if provider.voice != "lina" {
		t.Fatalf("voice = %q, want reference default voice", provider.voice)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.outputFormat != "mp3" {
		t.Fatalf("format = %q, want mp3", provider.outputFormat)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if got := coretts.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := coretts.Provider(provider); got != "Spitch" {
		t.Fatalf("provider metadata = %q, want Spitch", got)
	}
}

func TestNewSpitchSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SPITCH_API_KEY", "env-key")

	provider := NewSpitchSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	if got := corestt.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := corestt.Provider(provider); got != "Spitch" {
		t.Fatalf("provider metadata = %q, want Spitch", got)
	}

	explicit := NewSpitchSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewSpitchTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SPITCH_API_KEY", "env-key")

	provider := NewSpitchTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewSpitchTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestSpitchTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewSpitchTTS("test-key", "")

	req, err := buildSpitchTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.spitch.ai/tts/v1/synthesize" {
		t.Fatalf("url = %q, want synthesize endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSpitchPayload(t, payload, "text", "hello")
	assertSpitchPayload(t, payload, "voice", "lina")
	assertSpitchPayload(t, payload, "language", "en")
	assertSpitchPayload(t, payload, "format", "mp3")
}

func TestSpitchTTSOptionsMatchReference(t *testing.T) {
	provider := NewSpitchTTS("test-key", "",
		WithSpitchTTSBaseURL("https://spitch.example/"),
		WithSpitchTTSVoice("amina"),
		WithSpitchTTSLanguage("fr"),
		WithSpitchTTSOutputFormat("wav"),
	)

	req, err := buildSpitchTTSRequest(context.Background(), provider, "bonjour")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://spitch.example/tts/v1/synthesize" {
		t.Fatalf("url = %q, want custom synthesize endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSpitchPayload(t, payload, "voice", "amina")
	assertSpitchPayload(t, payload, "language", "fr")
	assertSpitchPayload(t, payload, "format", "wav")
}

func TestSpitchTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &spitchTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(spitchTestWAV([]byte{0x01, 0x02}, 16000, 1)))},
		sampleRate: 16000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func TestSpitchTTSChunkedStreamDecodesWAVResponse(t *testing.T) {
	pcm := []byte{0x01, 0x00, 0x02, 0x00}
	stream := &spitchTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(spitchTestWAV(pcm, 24000, 1)))},
		sampleRate: 24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, pcm) {
		t.Fatalf("frame data = %#v, want decoded PCM %#v", audio.Frame.Data, pcm)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame shape = rate %d channels %d samples %d, want 24000/1/2", audio.Frame.SampleRate, audio.Frame.NumChannels, audio.Frame.SamplesPerChannel)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestSpitchTTSChunkedStreamDecodesReferenceMP3Response(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: spitchRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(mp3Data)),
			Request:    r,
		}, nil
	})}

	provider := NewSpitchTTS("test-key", "", WithSpitchTTSBaseURL("https://spitch.example"))
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

type spitchRoundTripFunc func(*http.Request) (*http.Response, error)

func (f spitchRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func assertSpitchPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func spitchTestWAV(pcm []byte, sampleRate uint32, channels uint16) []byte {
	var wav bytes.Buffer
	byteRate := sampleRate * uint32(channels) * 2
	blockAlign := channels * 2
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36+len(pcm)))
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, channels)
	_ = binary.Write(&wav, binary.LittleEndian, sampleRate)
	_ = binary.Write(&wav, binary.LittleEndian, byteRate)
	_ = binary.Write(&wav, binary.LittleEndian, blockAlign)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)))
	wav.Write(pcm)
	return wav.Bytes()
}
