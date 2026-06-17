package mistralai

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
	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestMistralAITTSDefaultsMatchReference(t *testing.T) {
	provider, err := NewMistralAITTS("test-key", "")
	if err != nil {
		t.Fatalf("new tts: %v", err)
	}

	if provider.baseURL != "https://api.mistral.ai/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "voxtral-mini-tts-latest" {
		t.Fatalf("model = %q, want reference default model", provider.model)
	}
	if provider.voice != "en_paul_neutral" {
		t.Fatalf("voice = %q, want reference default voice", provider.voice)
	}
	if provider.responseFormat != "mp3" {
		t.Fatalf("response format = %q, want mp3", provider.responseFormat)
	}
	if provider.refAudio != "" {
		t.Fatalf("ref audio = %q, want empty", provider.refAudio)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("channels = %d, want 1", provider.NumChannels())
	}
	if got := tts.Model(provider); got != "voxtral-mini-tts-latest" {
		t.Fatalf("model metadata = %q, want voxtral-mini-tts-latest", got)
	}
	if got := tts.Provider(provider); got != "MistralAI" {
		t.Fatalf("provider metadata = %q, want MistralAI", got)
	}
	caps := provider.Capabilities()
	if caps.Streaming {
		t.Fatal("streaming = true, want false for chunked TTS")
	}
}

func TestNewMistralAITTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "env-key")

	provider, err := NewMistralAITTS("", "")
	if err != nil {
		t.Fatalf("new tts with env key: %v", err)
	}
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit, err := NewMistralAITTS("explicit-key", "")
	if err != nil {
		t.Fatalf("new tts with explicit key: %v", err)
	}
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}

	t.Setenv("MISTRAL_API_KEY", "")
	if err := os.Unsetenv("MISTRAL_API_KEY"); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	if _, err := NewMistralAITTS("", ""); err == nil || !strings.Contains(err.Error(), "mistral AI API key is required") {
		t.Fatalf("error = %v, want missing API key error", err)
	}
}

func TestMistralAITTSRejectsVoiceAndReferenceAudioTogether(t *testing.T) {
	_, err := NewMistralAITTS("test-key", "voice", WithMistralAITTSRefAudio("audio"))
	if err == nil || !strings.Contains(err.Error(), "voice") {
		t.Fatalf("error = %v, want voice/ref_audio conflict", err)
	}
}

func TestMistralAITTSRecognizeRequestUsesReferenceBody(t *testing.T) {
	provider, err := NewMistralAITTS("test-key", "",
		WithMistralAITTSBaseURL("https://mistral.example/v1"),
		WithMistralAITTSModel("voxtral-mini-tts-2603"),
		WithMistralAITTSVoice("voice-1"),
		WithMistralAITTSResponseFormat("opus"),
	)
	if err != nil {
		t.Fatalf("new tts: %v", err)
	}

	req, err := buildMistralAITTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://mistral.example/v1/audio/speech" {
		t.Fatalf("url = %q, want speech endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("accept = %q, want text/event-stream", got)
	}

	body := decodeMistralTTSBody(t, req)
	assertMistralTTSBody(t, body, "model", "voxtral-mini-tts-2603")
	assertMistralTTSBody(t, body, "input", "hello")
	assertMistralTTSBody(t, body, "voice_id", "voice-1")
	assertMistralTTSBody(t, body, "response_format", "opus")
	assertMistralTTSBody(t, body, "stream", true)
	if _, ok := body["ref_audio"]; ok {
		t.Fatalf("ref_audio present with voice request: %#v", body)
	}
}

func TestMistralAITTSRequestUsesReferenceAudioInsteadOfVoice(t *testing.T) {
	provider, err := NewMistralAITTS("test-key", "", WithMistralAITTSRefAudio("base64-audio"))
	if err != nil {
		t.Fatalf("new tts: %v", err)
	}

	req, err := buildMistralAITTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	body := decodeMistralTTSBody(t, req)
	assertMistralTTSBody(t, body, "ref_audio", "base64-audio")
	if _, ok := body["voice_id"]; ok {
		t.Fatalf("voice_id present with ref_audio request: %#v", body)
	}
}

func TestMistralAITTSStreamDecodesAudioDeltaDoneAndPCM(t *testing.T) {
	pcmF32 := []byte{0x00, 0x00, 0x00, 0x3f, 0x00, 0x00, 0x00, 0xbf}
	stream := &mistralAITTSChunkedStream{
		reader: strings.NewReader(strings.Join([]string{
			`data: {"event":"speech.audio.delta","data":{"audio_data":"` + base64.StdEncoding.EncodeToString(pcmF32) + `"}}`,
			`data: {"event":"speech.audio.done","data":{"usage":{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6}}}`,
			"",
		}, "\n")),
		responseFormat: "pcm",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("next audio: %v", err)
	}
	assertMistralTTSAudio(t, audio, []byte{0xff, 0x3f, 0x01, 0xc0})

	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("next after done error = %v, want EOF", err)
	}
}

func TestMistralAITTSStreamDecodesJSONAudioResponse(t *testing.T) {
	stream := &mistralAITTSChunkedStream{
		reader:         strings.NewReader(`{"audio_data":"` + base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}) + `"}`),
		responseFormat: "flac",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("next audio: %v", err)
	}
	assertMistralTTSAudio(t, audio, []byte{0x01, 0x02})
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("next after json chunk error = %v, want EOF", err)
	}
}

func TestMistralAITTSStreamDecodesReferenceWAVResponse(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0x03, 0x04}
	stream := &mistralAITTSChunkedStream{
		reader:         strings.NewReader(`{"audio_data":"` + base64.StdEncoding.EncodeToString(mistralAITTSTestWAV(pcm, 16000, 1)) + `"}`),
		responseFormat: "wav",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("next audio: %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want frame", audio)
	}
	if !bytes.Equal(audio.Frame.Data, pcm) {
		t.Fatalf("frame data = %#v, want decoded PCM %#v", audio.Frame.Data, pcm)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want WAV metadata 16000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want WAV metadata mono", audio.Frame.NumChannels)
	}
	if bytes.HasPrefix(audio.Frame.Data, []byte("RIFF")) {
		t.Fatal("frame data still contains WAV container")
	}
}

func TestMistralAITTSStreamDecodesReferenceMP3Response(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	stream := &mistralAITTSChunkedStream{
		reader:         strings.NewReader(`{"audio_data":"` + base64.StdEncoding.EncodeToString(mp3Data) + `"}`),
		responseFormat: "mp3",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("next audio: %v", err)
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
	if string(audio.Frame.Data) == string(mp3Data[:len(audio.Frame.Data)]) {
		t.Fatal("frame data still contains compressed mp3 bytes")
	}
}

func TestMistralAITTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: mistralAITTSRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider, err := NewMistralAITTS("test-key", "", WithMistralAITTSBaseURL("https://mistral.example/v1"))
	if err != nil {
		t.Fatalf("new tts: %v", err)
	}

	_, err = provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if statusErr.Body != `{"error":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
	if !statusErr.Retryable {
		t.Fatal("retryable = false, want true for 429")
	}
}

func TestMistralAITTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: mistralAITTSRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial refused")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider, err := NewMistralAITTS("test-key", "", WithMistralAITTSBaseURL("https://mistral.example/v1"))
	if err != nil {
		t.Fatalf("new tts: %v", err)
	}

	_, err = provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Synthesize error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != `Post "https://mistral.example/v1/audio/speech": dial refused` {
		t.Fatalf("connection message = %q, want transport error", connectionErr.Message)
	}
	if !connectionErr.Retryable {
		t.Fatal("retryable = false, want true")
	}
}

func TestMistralAITTSSynthesizeReturnsAPITimeoutError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: mistralAITTSRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider, err := NewMistralAITTS("test-key", "", WithMistralAITTSBaseURL("https://mistral.example/v1"))
	if err != nil {
		t.Fatalf("new tts: %v", err)
	}

	_, err = provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Synthesize error = %T %v, want APITimeoutError", err, err)
	}
	if !timeoutErr.Retryable {
		t.Fatal("retryable = false, want true")
	}
}

func TestMistralAITTSStreamDecodeFailureReturnsAPIConnectionError(t *testing.T) {
	stream := &mistralAITTSChunkedStream{
		reader:         strings.NewReader(`data: {"event":"speech.audio.delta","data":{"audio_data":"not-base64"}}`),
		responseFormat: "wav",
	}

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message == "" {
		t.Fatal("connection error message empty, want decode failure")
	}
}

type mistralAITTSRoundTripFunc func(*http.Request) (*http.Response, error)

func (f mistralAITTSRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func decodeMistralTTSBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func assertMistralTTSBody(t *testing.T, body map[string]any, key string, want any) {
	t.Helper()
	if got := body[key]; got != want {
		t.Fatalf("%s = %#v, want %#v in body %#v", key, got, want, body)
	}
}

func assertMistralTTSAudio(t *testing.T, audio *tts.SynthesizedAudio, want []byte) {
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
}

func mistralAITTSTestWAV(pcm []byte, sampleRate uint32, channels uint16) []byte {
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
