package cambai

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

	"github.com/cavos-io/rtp-agent/core/llm"
)

type cambaiRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f cambaiRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type cambaiFinalEOFReader struct {
	data []byte
	done bool
}

func (r *cambaiFinalEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("read after final eof")
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func (r *cambaiFinalEOFReader) Close() error { return nil }

type cambaiCloseErrorBody struct {
	closed bool
}

func (b *cambaiCloseErrorBody) Read(_ []byte) (int, error) {
	if b.closed {
		return 0, errors.New("read after close")
	}
	return 0, io.EOF
}

func (b *cambaiCloseErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestCambaiTTSDefaultsMatchReference(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "")
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}

	if provider.baseURL != "https://client.camb.ai/apis" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.voiceID != 147320 {
		t.Fatalf("voice id = %d, want default voice", provider.voiceID)
	}
	if provider.language != "en-us" {
		t.Fatalf("language = %q, want en-us", provider.language)
	}
	if provider.model != "mars-flash" {
		t.Fatalf("model = %q, want mars-flash", provider.model)
	}
	if provider.outputFormat != "pcm_s16le" {
		t.Fatalf("output format = %q, want pcm_s16le", provider.outputFormat)
	}
	if provider.SampleRate() != 22050 {
		t.Fatalf("sample rate = %d, want mars-flash sample rate", provider.SampleRate())
	}
	if provider.Label() != "cambai.TTS" {
		t.Fatalf("Label = %q, want cambai.TTS", provider.Label())
	}
	if provider.Model() != "mars-flash" {
		t.Fatalf("Model = %q, want mars-flash", provider.Model())
	}
	if provider.Provider() != "Camb.ai" {
		t.Fatalf("Provider = %q, want Camb.ai", provider.Provider())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("NumChannels = %d, want 1", provider.NumChannels())
	}
	if provider.Capabilities().Streaming {
		t.Fatal("Streaming = true, want false")
	}
}

func TestCambaiTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "")
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}

	req, err := buildCambaiTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://client.camb.ai/apis/tts-stream" {
		t.Fatalf("url = %q, want tts-stream endpoint", req.URL.String())
	}
	if got := req.Header.Get("x-api-key"); got != "test-key" {
		t.Fatalf("x-api-key = %q, want API key", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertCambaiPayload(t, payload, "text", "hello")
	if payload["voice_id"] != float64(147320) {
		t.Fatalf("voice_id = %#v, want default voice", payload["voice_id"])
	}
	assertCambaiPayload(t, payload, "language", "en-us")
	assertCambaiPayload(t, payload, "speech_model", "mars-flash")
	if payload["enhance_named_entities_pronunciation"] != false {
		t.Fatalf("enhance_named_entities_pronunciation = %#v, want false", payload["enhance_named_entities_pronunciation"])
	}
	outputConfig := payload["output_configuration"].(map[string]any)
	assertCambaiPayload(t, outputConfig, "format", "pcm_s16le")
	if _, ok := payload["user_instructions"]; ok {
		t.Fatalf("user_instructions present, want omitted by default")
	}
}

func TestCambaiTTSFallsBackToEnvironmentAPIKey(t *testing.T) {
	t.Setenv(cambaiAPIKeyEnv, "env-key")

	provider, err := NewCambaiTTS("", "")
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v, want nil from env key", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", provider.apiKey)
	}
}

func TestCambaiTTSRequiresAPIKey(t *testing.T) {
	t.Setenv(cambaiAPIKeyEnv, "")

	_, err := NewCambaiTTS("", "")

	if err == nil || !strings.Contains(err.Error(), "CAMB_API_KEY") {
		t.Fatalf("NewCambaiTTS error = %v, want API key error", err)
	}
}

func TestCambaiTTSOptionsMatchReference(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "",
		WithCambaiTTSBaseURL("https://cambai.example/apis/"),
		WithCambaiTTSVoiceID(42),
		WithCambaiTTSLanguage("fr-fr"),
		WithCambaiTTSModel("mars-pro"),
		WithCambaiTTSOutputFormat("wav"),
		WithCambaiTTSUserInstructions("warm and concise"),
		WithCambaiTTSEnhanceNamedEntities(true),
	)
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}

	req, err := buildCambaiTTSRequest(context.Background(), provider, "bonjour")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://cambai.example/apis/tts-stream" {
		t.Fatalf("url = %q, want custom tts-stream endpoint", req.URL.String())
	}
	if provider.SampleRate() != 48000 {
		t.Fatalf("sample rate = %d, want mars-pro sample rate", provider.SampleRate())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["voice_id"] != float64(42) {
		t.Fatalf("voice_id = %#v, want custom voice", payload["voice_id"])
	}
	assertCambaiPayload(t, payload, "language", "fr-fr")
	assertCambaiPayload(t, payload, "speech_model", "mars-pro")
	assertCambaiPayload(t, payload, "user_instructions", "warm and concise")
	if payload["enhance_named_entities_pronunciation"] != true {
		t.Fatalf("enhance_named_entities_pronunciation = %#v, want true", payload["enhance_named_entities_pronunciation"])
	}
	outputConfig := payload["output_configuration"].(map[string]any)
	assertCambaiPayload(t, outputConfig, "format", "wav")
}

func TestCambaiTTSUpdateOptionsAffectsFutureRequestsLikeReference(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "",
		WithCambaiTTSBaseURL("https://cambai.example/apis/"),
		WithCambaiTTSOutputFormat("wav"),
	)
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}

	beforeUpdate, err := provider.Synthesize(context.Background(), "old")
	if err != nil {
		t.Fatalf("Synthesize before update error = %v", err)
	}
	oldStream, ok := beforeUpdate.(*cambaiTTSChunkedStream)
	if !ok {
		t.Fatalf("stream type = %T, want *cambaiTTSChunkedStream", beforeUpdate)
	}
	defer oldStream.Close()

	provider.UpdateOptions(
		WithCambaiTTSUpdateVoiceID(42),
		WithCambaiTTSUpdateLanguage("fr-fr"),
		WithCambaiTTSUpdateModel("mars-pro"),
		WithCambaiTTSUpdateUserInstructions("warm and concise"),
	)

	if oldStream.opts.voiceID != defaultCambaiVoiceID || oldStream.opts.language != defaultCambaiLanguage || oldStream.opts.model != defaultCambaiModel || oldStream.opts.userInstructions != "" {
		t.Fatalf("pre-update stream options = voice=%d language=%q model=%q instructions=%q, want original snapshot", oldStream.opts.voiceID, oldStream.opts.language, oldStream.opts.model, oldStream.opts.userInstructions)
	}
	if oldStream.sampleRate != cambaiSampleRateForModel(defaultCambaiModel) || oldStream.outputFormat != "wav" {
		t.Fatalf("pre-update stream audio route = rate=%d format=%q, want original route snapshot", oldStream.sampleRate, oldStream.outputFormat)
	}

	req, err := buildCambaiTTSRequest(context.Background(), provider, "new")
	if err != nil {
		t.Fatalf("build request after update: %v", err)
	}
	if req.URL.String() != "https://cambai.example/apis/tts-stream" {
		t.Fatalf("updated request URL = %q, want base URL unchanged", req.URL.String())
	}
	if provider.SampleRate() != 48000 {
		t.Fatalf("sample rate = %d, want mars-pro sample rate", provider.SampleRate())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode updated body: %v", err)
	}
	if payload["voice_id"] != float64(42) {
		t.Fatalf("voice_id = %#v, want updated voice", payload["voice_id"])
	}
	assertCambaiPayload(t, payload, "language", "fr-fr")
	assertCambaiPayload(t, payload, "speech_model", "mars-pro")
	assertCambaiPayload(t, payload, "user_instructions", "warm and concise")
	outputConfig := payload["output_configuration"].(map[string]any)
	assertCambaiPayload(t, outputConfig, "format", "wav")
}

func TestCambaiTTSSynthesizeUsesConfiguredClient(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "", WithCambaiTTSModel("mars-pro"))
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: cambaiRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("x-api-key") != "test-key" {
				t.Fatalf("x-api-key = %q, want test-key", req.Header.Get("x-api-key"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want mars-pro sample rate", audio.Frame.SampleRate)
	}
}

func TestCambaiTTSSynthesizeDefersReferenceRequestUntilNext(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "")
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}
	requests := 0
	provider.httpClient = &http.Client{
		Transport: cambaiRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	if requests != 0 {
		t.Fatalf("requests after Synthesize = %d, want 0 before Next", requests)
	}

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests after Next = %d, want 1", requests)
	}
}

func TestCambaiTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "")
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: cambaiRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			header := make(http.Header)
			header.Set("x-request-id", "req_429")
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
				Request:    req,
			}, nil
		}),
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v, want deferred stream", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if statusErr.RequestID != "req_429" {
		t.Fatalf("request id = %q, want req_429", statusErr.RequestID)
	}
	if body, ok := statusErr.Body.(string); !ok || !strings.Contains(body, "rate limited") {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestCambaiTTSStreamReportsUnsupported(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "")
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}

	_, err = provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not natively supported") {
		t.Fatalf("Stream error = %v, want unsupported streaming error", err)
	}
}

func TestCambaiTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &cambaiTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestCambaiTTSChunkedStreamDecodesReferenceWAVResponse(t *testing.T) {
	pcm := []byte{0x01, 0x00, 0x02, 0x00}
	stream := &cambaiTTSChunkedStream{
		resp:         &http.Response{Body: io.NopCloser(bytes.NewReader(cambaiTestWAV(pcm, 48000, 1)))},
		sampleRate:   22050,
		outputFormat: "wav",
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("audio = %#v, want decoded WAV frame", audio)
	}
	if !bytes.Equal(audio.Frame.Data, pcm) {
		t.Fatalf("audio data = %#v, want decoded PCM without WAV header", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 48000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame = %+v, want WAV metadata 48 kHz mono", audio.Frame)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final = %#v, want final marker", final)
	}
}

func TestCambaiTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &cambaiTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 48000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if audio == nil || audio.IsFinal {
		t.Fatalf("first audio = %#v, want non-final audio", audio)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("audio frame is empty")
	}

	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error before final marker: %v", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker err = %v, want EOF", err)
	}
}

func TestCambaiTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &cambaiCloseErrorBody{}
	stream := &cambaiTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 48000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	_, err := stream.Next()

	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestCambaiTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &cambaiTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(nil))},
		sampleRate: 48000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("Next = %+v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestCambaiTTSChunkedStreamKeepsAudioReturnedWithEOF(t *testing.T) {
	stream := &cambaiTTSChunkedStream{
		resp:       &http.Response{Body: &cambaiFinalEOFReader{data: []byte{0x01, 0x02}}},
		sampleRate: 48000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if audio == nil || audio.IsFinal || audio.Frame == nil {
		t.Fatalf("first audio = %#v, want audio frame", audio)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{0x01, 0x02}) {
		t.Fatalf("audio data = %v, want final bytes", got)
	}

	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error before final marker: %v", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", audio)
	}
	if audio.Frame != nil {
		t.Fatalf("final marker frame = %+v, want boundary-only final marker", audio.Frame)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func assertCambaiPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func cambaiTestWAV(pcm []byte, sampleRate uint32, channels uint16) []byte {
	var wav bytes.Buffer
	blockAlign := channels * 2
	byteRate := sampleRate * uint32(blockAlign)
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
