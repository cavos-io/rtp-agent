package groq

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestGroqTTSDefaultsMatchReference(t *testing.T) {
	provider := NewGroqTTS("test-key", "")

	if provider.baseURL != "https://api.groq.com/openai/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "canopylabs/orpheus-v1-english" {
		t.Fatalf("model = %q, want reference default model", provider.model)
	}
	if provider.voice != "autumn" {
		t.Fatalf("voice = %q, want reference default voice", provider.voice)
	}
	if provider.responseFormat != "wav" {
		t.Fatalf("response format = %q, want wav", provider.responseFormat)
	}
	if provider.SampleRate() != 48000 {
		t.Fatalf("sample rate = %d, want 48000", provider.SampleRate())
	}
	if got := tts.Model(provider); got != "canopylabs/orpheus-v1-english" {
		t.Fatalf("model metadata = %q, want reference model", got)
	}
	if got := tts.Provider(provider); got != "Groq" {
		t.Fatalf("provider metadata = %q, want Groq", got)
	}
}

func TestNewGroqTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "env-key")

	provider := NewGroqTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildGroqTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("authorization = %q, want env bearer token", got)
	}

	explicit := NewGroqTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestGroqTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewGroqTTS("test-key", "")

	req, err := buildGroqTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.groq.com/openai/v1/audio/speech" {
		t.Fatalf("url = %q, want audio speech endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertGroqTTSPayload(t, payload, "model", "canopylabs/orpheus-v1-english")
	assertGroqTTSPayload(t, payload, "voice", "autumn")
	assertGroqTTSPayload(t, payload, "input", "hello")
	assertGroqTTSPayload(t, payload, "response_format", "wav")
}

func TestGroqTTSOptionsMatchReference(t *testing.T) {
	provider := NewGroqTTS("test-key", "",
		WithGroqTTSBaseURL("https://groq.example/openai/v1/"),
		WithGroqTTSModel("canopylabs/orpheus-arabic-saudi"),
		WithGroqTTSVoice("noura"),
	)

	req, err := buildGroqTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://groq.example/openai/v1/audio/speech" {
		t.Fatalf("url = %q, want custom audio speech endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertGroqTTSPayload(t, payload, "model", "canopylabs/orpheus-arabic-saudi")
	assertGroqTTSPayload(t, payload, "voice", "noura")
}

func TestGroqTTSUpdateOptionsMatchReference(t *testing.T) {
	provider := NewGroqTTS("test-key", "",
		WithGroqTTSModel("canopylabs/orpheus-v1-english"),
		WithGroqTTSVoice("autumn"),
	)

	provider.UpdateOptions("canopylabs/orpheus-arabic-saudi", "fahad")

	req, err := buildGroqTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertGroqTTSPayload(t, payload, "model", "canopylabs/orpheus-arabic-saudi")
	assertGroqTTSPayload(t, payload, "voice", "fahad")
}

func TestGroqTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "")
	provider := NewGroqTTS("", "", WithGroqTTSBaseURL("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GROQ_API_KEY") {
		t.Fatalf("error = %q, want GROQ_API_KEY guidance", err)
	}
}

func TestGroqTTSRejectsNonAudioResponse(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"not audio"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewGroqTTS("test-key", "", WithGroqTTSBaseURL("https://groq.example/openai/v1"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize returned nil error, want non-audio response error")
	}
	if !strings.Contains(err.Error(), "non-audio") {
		t.Fatalf("error = %q, want non-audio guidance", err)
	}
}

func TestGroqTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(groqTestWAV([]byte{0x01, 0x02}, 48000, 1)))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func TestGroqTTSChunkedStreamDecodesReferenceWAVResponse(t *testing.T) {
	pcm := []byte{0x01, 0x00, 0x02, 0x00}
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(groqTestWAV(pcm, 48000, 1)))},
		sampleRate: 48000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, pcm) {
		t.Fatalf("frame data = %#v, want decoded PCM %#v", audio.Frame.Data, pcm)
	}
	if audio.Frame.SampleRate != 48000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame shape = rate %d channels %d samples %d, want 48000/1/2", audio.Frame.SampleRate, audio.Frame.NumChannels, audio.Frame.SamplesPerChannel)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestGroqTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &groqCloseCountBody{Reader: bytes.NewReader(groqTestWAV([]byte{0x01, 0x02}, 48000, 1))}
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 48000,
	}

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

func TestGroqTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(groqTestWAV([]byte{0x01, 0x02}, 48000, 1)))},
		sampleRate: 48000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %T %v, want EOF", err, err)
	}
}

func assertGroqTTSPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func groqTestWAV(pcm []byte, sampleRate uint32, channels uint16) []byte {
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type groqCloseCountBody struct {
	*bytes.Reader
	closeCount int
}

func (b *groqCloseCountBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("closed twice")
	}
	return nil
}
