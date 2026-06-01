package sarvam

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestSarvamSTTDefaultsMatchReference(t *testing.T) {
	provider := NewSarvamSTT("test-key")

	if provider.baseURL != "https://api.sarvam.ai/speech-to-text" {
		t.Fatalf("base URL = %q, want reference STT endpoint", provider.baseURL)
	}
	if provider.streamingURL != "wss://api.sarvam.ai/speech-to-text/ws" {
		t.Fatalf("streaming URL = %q, want reference STT websocket endpoint", provider.streamingURL)
	}
	if provider.model != "saarika:v2.5" {
		t.Fatalf("model = %q, want saarika:v2.5", provider.model)
	}
	if provider.language != "en-IN" {
		t.Fatalf("language = %q, want en-IN", provider.language)
	}
	if provider.mode != "transcribe" {
		t.Fatalf("mode = %q, want transcribe", provider.mode)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}

	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("capabilities = %+v, want streaming, interim, and offline recognize", caps)
	}
}

func TestSarvamSTTModelURLAndValidationReference(t *testing.T) {
	provider := NewSarvamSTT("test-key", WithSarvamSTTModel("saaras:v2.5"))
	if provider.baseURL != "https://api.sarvam.ai/speech-to-text-translate" {
		t.Fatalf("base URL = %q, want translate endpoint for saaras:v2.5", provider.baseURL)
	}
	if provider.streamingURL != "wss://api.sarvam.ai/speech-to-text-translate/ws" {
		t.Fatalf("streaming URL = %q, want translate websocket for saaras:v2.5", provider.streamingURL)
	}

	_, err := NewSarvamSTTWithError("test-key", WithSarvamSTTModel("saarika:v2.5"), WithSarvamSTTMode("translate"))
	if err == nil || !strings.Contains(err.Error(), "mode is not supported") {
		t.Fatalf("error = %v, want unsupported mode error", err)
	}

	_, err = NewSarvamSTTWithError("test-key", WithSarvamSTTModel("saarika:v2.5"), WithSarvamSTTLanguage("as-IN"))
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error = %v, want unsupported language error", err)
	}
}

func TestBuildSarvamSTTRecognizeRequestMatchesReference(t *testing.T) {
	provider := NewSarvamSTT("test-key",
		WithSarvamSTTBaseURL("https://sarvam.example/stt"),
		WithSarvamSTTModel("saaras:v3"),
		WithSarvamSTTLanguage("ta-IN"),
		WithSarvamSTTMode("translate"),
	)

	req, err := buildSarvamSTTRecognizeRequest(context.Background(), provider, []byte("pcm"), "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if req.URL.String() != "https://sarvam.example/stt" {
		t.Fatalf("URL = %q, want configured base URL", req.URL.String())
	}
	if req.Header.Get("api-subscription-key") != "test-key" {
		t.Fatalf("api-subscription-key = %q, want test-key", req.Header.Get("api-subscription-key"))
	}
	if req.Header.Get("User-Agent") == "" {
		t.Fatal("User-Agent missing")
	}

	fields := readMultipartFields(t, req)
	if fields["language_code"] != "ta-IN" {
		t.Fatalf("language_code = %q, want ta-IN", fields["language_code"])
	}
	if fields["model"] != "saaras:v3" {
		t.Fatalf("model = %q, want saaras:v3", fields["model"])
	}
	if fields["mode"] != "translate" {
		t.Fatalf("mode = %q, want translate for saaras:v3", fields["mode"])
	}
	if fields["file"] != "pcm" {
		t.Fatalf("file = %q, want audio payload", fields["file"])
	}
}

func TestSarvamSTTSpeechEventMapsReferenceMetadata(t *testing.T) {
	event := sarvamSTTSpeechEvent("en-IN", sarvamSTTResponse{
		Transcript:          "hello",
		RequestID:           "req-1",
		LanguageCode:        "ta-IN",
		LanguageProbability: 0.82,
		Timestamps: sarvamSTTTimestamps{
			StartTimeSeconds: []float64{0.1},
			EndTimeSeconds:   []float64{0.4, 0.9},
		},
	})

	if event.Type != stt.SpeechEventFinalTranscript || event.RequestID != "req-1" {
		t.Fatalf("event = %+v, want final transcript with request id", event)
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello" || alt.Language != "ta-IN" || alt.Confidence != 0.82 {
		t.Fatalf("alternative = %+v, want transcript language confidence", alt)
	}
	if alt.StartTime != 0.1 || alt.EndTime != 0.9 {
		t.Fatalf("times = %.1f..%.1f, want 0.1..0.9", alt.StartTime, alt.EndTime)
	}
}

func TestSarvamSTTWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewSarvamSTT("test-key",
		WithSarvamSTTStreamingURL("wss://sarvam.example/stt/ws"),
		WithSarvamSTTModel("saaras:v3"),
		WithSarvamSTTLanguage("ta-IN"),
		WithSarvamSTTMode("translate"),
		WithSarvamSTTSampleRate(8000),
	)

	wsURL := buildSarvamSTTWebsocketURL(provider, "")
	if wsURL.Scheme != "wss" || wsURL.Host != "sarvam.example" || wsURL.Path != "/stt/ws" {
		t.Fatalf("websocket URL = %q, want configured websocket endpoint", wsURL.String())
	}
	query := wsURL.Query()
	assertSarvamQuery(t, query, "language-code", "ta-IN")
	assertSarvamQuery(t, query, "model", "saaras:v3")
	assertSarvamQuery(t, query, "vad_signals", "true")
	assertSarvamQuery(t, query, "sample_rate", "8000")
	assertSarvamQuery(t, query, "mode", "translate")

	overrideURL := buildSarvamSTTWebsocketURL(provider, "hi-IN")
	assertSarvamQuery(t, overrideURL.Query(), "language-code", "hi-IN")

	headers := buildSarvamSTTWebsocketHeaders(provider)
	if headers.Get("api-subscription-key") != "test-key" {
		t.Fatalf("api-subscription-key = %q, want test-key", headers.Get("api-subscription-key"))
	}
	if headers.Get("User-Agent") == "" {
		t.Fatalf("headers = %+v, want User-Agent", headers)
	}
}

func TestSarvamSTTStreamMessagesMatchReference(t *testing.T) {
	provider := NewSarvamSTT("test-key",
		WithSarvamSTTModel("saaras:v3"),
		WithSarvamSTTPrompt("names: Kavya"),
		WithSarvamSTTSampleRate(8000),
	)

	configPayload, err := buildSarvamSTTConfigMessage(provider)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(configPayload, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	assertSarvamJSONField(t, config, "type", "config")
	assertSarvamJSONField(t, config, "prompt", "names: Kavya")

	audioPayload, err := buildSarvamSTTAudioMessage(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        8000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}, "audio/wav")
	if err != nil {
		t.Fatalf("build audio: %v", err)
	}
	var audio map[string]any
	if err := json.Unmarshal(audioPayload, &audio); err != nil {
		t.Fatalf("decode audio: %v", err)
	}
	audioData := audio["audio"].(map[string]any)
	assertSarvamJSONField(t, audioData, "data", base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}))
	assertSarvamJSONField(t, audioData, "encoding", "audio/wav")
	assertSarvamJSONField(t, audioData, "sample_rate", float64(8000))

	endPayload, err := buildSarvamSTTEndOfStreamMessage("audio/wav", 8000)
	if err != nil {
		t.Fatalf("build end of stream: %v", err)
	}
	var end map[string]any
	if err := json.Unmarshal(endPayload, &end); err != nil {
		t.Fatalf("decode end of stream: %v", err)
	}
	assertSarvamJSONField(t, end, "type", "end_of_stream")
	endAudio := end["audio"].(map[string]any)
	assertSarvamJSONField(t, endAudio, "data", "")
	assertSarvamJSONField(t, endAudio, "encoding", "audio/wav")
	assertSarvamJSONField(t, endAudio, "sample_rate", float64(8000))
}

func TestSarvamSTTStreamEventsMapReferenceMessages(t *testing.T) {
	events, err := sarvamSTTEventsFromStreamMessage([]byte(`{"type":"data","data":{"transcript":"hello","language_code":"hi-IN","request_id":"req-1","speech_start":0.1,"speech_end":0.7,"language_probability":0.91,"metrics":{"audio_duration":1.2}}}`), "en-IN")
	if err != nil {
		t.Fatalf("stream event: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want usage and final transcript", len(events))
	}
	if events[0].Type != stt.SpeechEventRecognitionUsage || events[0].RecognitionUsage.AudioDuration != 1.2 {
		t.Fatalf("usage event = %+v, want audio duration", events[0])
	}
	if events[1].Type != stt.SpeechEventFinalTranscript || events[1].RequestID != "req-1" {
		t.Fatalf("transcript event = %+v, want final transcript", events[1])
	}
	alt := events[1].Alternatives[0]
	if alt.Text != "hello" || alt.Language != "hi-IN" || alt.StartTime != 0.1 || alt.EndTime != 0.7 || alt.Confidence != 0.91 {
		t.Fatalf("alternative = %+v, want reference transcript data", alt)
	}

	fallbackEvents, err := sarvamSTTEventsFromStreamMessage([]byte(`{"type":"data","data":{"transcript":"fallback","request_id":"req-2"}}`), "en-IN")
	if err != nil {
		t.Fatalf("fallback language event: %v", err)
	}
	if len(fallbackEvents) != 1 || fallbackEvents[0].Alternatives[0].Language != "en-IN" {
		t.Fatalf("fallback events = %+v, want default language", fallbackEvents)
	}

	start, err := sarvamSTTEventsFromStreamMessage([]byte(`{"type":"events","data":{"signal_type":"START_SPEECH"}}`), "en-IN")
	if err != nil {
		t.Fatalf("start event: %v", err)
	}
	if len(start) != 1 || start[0].Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("start events = %+v, want start of speech", start)
	}

	end, err := sarvamSTTEventsFromStreamMessage([]byte(`{"type":"event","data":{"signal_type":"END_SPEECH"}}`), "en-IN")
	if err != nil {
		t.Fatalf("end event: %v", err)
	}
	if len(end) != 1 || end[0].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("end events = %+v, want end of speech", end)
	}

	_, err = sarvamSTTEventsFromStreamMessage([]byte(`{"type":"error","data":{"message":"bad request","code":"400"}}`), "en-IN")
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("error = %v, want provider error", err)
	}
}

func TestSarvamSTTImplementsStreamingInterface(t *testing.T) {
	var _ stt.STT = NewSarvamSTT("test-key")
}

func TestSarvamTTSDefaultsMatchReference(t *testing.T) {
	provider := NewSarvamTTS("test-key", "")

	if provider.baseURL != "https://api.sarvam.ai/text-to-speech" {
		t.Fatalf("base URL = %q, want reference TTS endpoint", provider.baseURL)
	}
	if provider.wsURL != "wss://api.sarvam.ai/text-to-speech/ws" {
		t.Fatalf("ws URL = %q, want reference TTS websocket endpoint", provider.wsURL)
	}
	if provider.model != "bulbul:v3" {
		t.Fatalf("model = %q, want bulbul:v3", provider.model)
	}
	if provider.voice != "shubh" {
		t.Fatalf("voice = %q, want shubh for v3", provider.voice)
	}
	if provider.language != "en-IN" {
		t.Fatalf("language = %q, want en-IN", provider.language)
	}
	if provider.sampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", provider.sampleRate)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("capabilities = %+v, want streaming true", provider.Capabilities())
	}
}

func TestBuildSarvamTTSRequestMatchesReferencePayload(t *testing.T) {
	provider := NewSarvamTTS("test-key", "",
		WithSarvamTTSBaseURL("https://sarvam.example/tts"),
		WithSarvamTTSModel("bulbul:v3"),
		WithSarvamTTSVoice("ritu"),
		WithSarvamTTSLanguage("hi-IN"),
		WithSarvamTTSSampleRate(24000),
		WithSarvamTTSTemperature(0.7),
		WithSarvamTTSOutputAudioCodec("wav"),
	)

	req, err := buildSarvamTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://sarvam.example/tts" {
		t.Fatalf("URL = %q, want configured base URL", req.URL.String())
	}
	if req.Header.Get("api-subscription-key") != "test-key" {
		t.Fatalf("api-subscription-key = %q, want test-key", req.Header.Get("api-subscription-key"))
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	assertSarvamJSONField(t, payload, "text", "hello")
	assertSarvamJSONField(t, payload, "target_language_code", "hi-IN")
	assertSarvamJSONField(t, payload, "speaker", "ritu")
	assertSarvamJSONField(t, payload, "model", "bulbul:v3")
	assertSarvamJSONField(t, payload, "output_audio_codec", "wav")
	assertSarvamJSONField(t, payload, "temperature", float64(0.7))
	if _, ok := payload["pitch"]; ok {
		t.Fatalf("pitch included for v3 payload: %+v", payload)
	}
}

func TestSarvamTTSAdvancedOptionsBuildReferencePayloads(t *testing.T) {
	cacheEnabled := true
	v2Provider := NewSarvamTTS("test-key", "",
		WithSarvamTTSModel("bulbul:v2"),
		WithSarvamTTSPitch(0.5),
		WithSarvamTTSPace(1.4),
		WithSarvamTTSLoudness(1.3),
		WithSarvamTTSEnablePreprocessing(true),
		WithSarvamTTSEnableCachedResponses(cacheEnabled),
	)

	req, err := buildSarvamTTSRequest(context.Background(), v2Provider, "hello")
	if err != nil {
		t.Fatalf("build v2 request: %v", err)
	}
	var v2Payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&v2Payload); err != nil {
		t.Fatalf("decode v2 payload: %v", err)
	}
	assertSarvamJSONField(t, v2Payload, "pitch", float64(0.5))
	assertSarvamJSONField(t, v2Payload, "pace", float64(1.4))
	assertSarvamJSONField(t, v2Payload, "loudness", float64(1.3))
	assertSarvamJSONField(t, v2Payload, "enable_preprocessing", true)
	assertSarvamJSONField(t, v2Payload, "enable_cached_responses", true)

	v3Provider := NewSarvamTTS("test-key", "",
		WithSarvamTTSModel("bulbul:v3"),
		WithSarvamTTSOutputAudioBitrate("96k"),
		WithSarvamTTSMinBufferSize(80),
		WithSarvamTTSMaxChunkLength(240),
		WithSarvamTTSDictID("dict-123"),
	)

	req, err = buildSarvamTTSRequest(context.Background(), v3Provider, "hello")
	if err != nil {
		t.Fatalf("build v3 request: %v", err)
	}
	var v3Payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&v3Payload); err != nil {
		t.Fatalf("decode v3 payload: %v", err)
	}
	assertSarvamJSONField(t, v3Payload, "output_audio_bitrate", "96k")
	assertSarvamJSONField(t, v3Payload, "min_buffer_size", float64(80))
	assertSarvamJSONField(t, v3Payload, "max_chunk_length", float64(240))
	assertSarvamJSONField(t, v3Payload, "dict_id", "dict-123")

	configPayload, err := buildSarvamTTSConfigMessage(v3Provider)
	if err != nil {
		t.Fatalf("build v3 websocket config: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(configPayload, &config); err != nil {
		t.Fatalf("decode v3 websocket config: %v", err)
	}
	data := config["data"].(map[string]any)
	assertSarvamJSONField(t, data, "output_audio_bitrate", "96k")
	assertSarvamJSONField(t, data, "min_buffer_size", float64(80))
	assertSarvamJSONField(t, data, "max_chunk_length", float64(240))
	assertSarvamJSONField(t, data, "dict_id", "dict-123")
}

func TestSarvamTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewSarvamTTS("test-key", "",
		WithSarvamTTSWSURL("wss://sarvam.example/tts/ws"),
		WithSarvamTTSModel("bulbul:v3-beta"),
	)

	wsURL := buildSarvamTTSWebsocketURL(provider)
	if wsURL.Scheme != "wss" || wsURL.Host != "sarvam.example" || wsURL.Path != "/tts/ws" {
		t.Fatalf("websocket URL = %q, want configured websocket endpoint", wsURL.String())
	}
	query := wsURL.Query()
	if query.Get("model") != "bulbul:v3-beta" {
		t.Fatalf("model query = %q, want bulbul:v3-beta", query.Get("model"))
	}
	if query.Get("send_completion_event") != "true" {
		t.Fatalf("send_completion_event query = %q, want true", query.Get("send_completion_event"))
	}

	headers := buildSarvamTTSWebsocketHeaders(provider)
	if headers.Get("api-subscription-key") != "test-key" {
		t.Fatalf("api-subscription-key = %q, want test-key", headers.Get("api-subscription-key"))
	}
	if headers.Get("User-Agent") == "" || headers.Get("Accept") != "*/*" {
		t.Fatalf("headers = %+v, want reference websocket headers", headers)
	}
}

func TestSarvamTTSStreamMessagesMatchReference(t *testing.T) {
	provider := NewSarvamTTS("test-key", "",
		WithSarvamTTSModel("bulbul:v3"),
		WithSarvamTTSVoice("ritu"),
		WithSarvamTTSLanguage("hi-IN"),
		WithSarvamTTSSampleRate(24000),
		WithSarvamTTSTemperature(0.7),
	)

	configPayload, err := buildSarvamTTSConfigMessage(provider)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(configPayload, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if config["type"] != "config" {
		t.Fatalf("config type = %#v, want config", config["type"])
	}
	data := config["data"].(map[string]any)
	assertSarvamJSONField(t, data, "target_language_code", "hi-IN")
	assertSarvamJSONField(t, data, "speaker", "ritu")
	assertSarvamJSONField(t, data, "model", "bulbul:v3")
	assertSarvamJSONField(t, data, "output_audio_codec", "mp3")
	assertSarvamJSONField(t, data, "temperature", float64(0.7))
	assertSarvamJSONField(t, data, "output_audio_bitrate", "128k")
	if data["speech_sample_rate"] != float64(24000) {
		t.Fatalf("speech_sample_rate = %#v, want 24000", data["speech_sample_rate"])
	}

	textPayload, err := buildSarvamTTSTextMessage("hello")
	if err != nil {
		t.Fatalf("build text: %v", err)
	}
	var text map[string]any
	if err := json.Unmarshal(textPayload, &text); err != nil {
		t.Fatalf("decode text: %v", err)
	}
	if text["type"] != "text" || text["data"].(map[string]any)["text"] != "hello" {
		t.Fatalf("text message = %+v, want reference text packet", text)
	}

	flushPayload, err := buildSarvamTTSFlushMessage()
	if err != nil {
		t.Fatalf("build flush: %v", err)
	}
	var flush map[string]any
	if err := json.Unmarshal(flushPayload, &flush); err != nil {
		t.Fatalf("decode flush: %v", err)
	}
	if flush["type"] != "flush" {
		t.Fatalf("flush type = %#v, want flush", flush["type"])
	}
}

func TestSarvamTTSAudioFromStreamMessage(t *testing.T) {
	audio, done, err := sarvamTTSAudioFromStreamMessage([]byte(`{"type":"audio","data":{"audio":"AQIDBA==","request_id":"req-1"}}`), 22050, "mp3")
	if err != nil {
		t.Fatalf("audio from stream message: %v", err)
	}
	if done {
		t.Fatal("done = true for audio message")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}
	if audio.RequestID != "req-1" || audio.Frame.SampleRate != 22050 || audio.Frame.NumChannels != 1 {
		t.Fatalf("audio = %+v, want request id and 22050 Hz mono", audio)
	}

	finished, done, err := sarvamTTSAudioFromStreamMessage([]byte(`{"type":"event","data":{"event_type":"final","request_id":"req-2"}}`), 22050, "mp3")
	if err != nil {
		t.Fatalf("final event: %v", err)
	}
	if finished != nil || !done {
		t.Fatalf("finished=%+v done=%v, want final event to finish stream", finished, done)
	}
}

func TestSarvamTTSAudioFromStreamMessageDecodesTelephonyCodecs(t *testing.T) {
	audio, done, err := sarvamTTSAudioFromStreamMessage([]byte(`{"type":"audio","data":{"audio":"AP8=","request_id":"req-mulaw"}}`), 8000, "mulaw")
	if err != nil {
		t.Fatalf("mulaw audio from stream message: %v", err)
	}
	if done {
		t.Fatal("done = true for mulaw audio message")
	}
	if audio == nil {
		t.Fatal("mulaw audio = nil")
	}
	if got, want := audio.Frame.Data, []byte{0x84, 0x82, 0x00, 0x00}; !bytes.Equal(got, want) {
		t.Fatalf("mulaw decoded data = %v, want %v", got, want)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("mulaw samples = %d, want one 16-bit PCM sample per encoded byte", audio.Frame.SamplesPerChannel)
	}

	audio, done, err = sarvamTTSAudioFromStreamMessage([]byte(`{"type":"audio","data":{"audio":"1VU=","request_id":"req-alaw"}}`), 8000, "alaw")
	if err != nil {
		t.Fatalf("alaw audio from stream message: %v", err)
	}
	if done {
		t.Fatal("done = true for alaw audio message")
	}
	if audio == nil {
		t.Fatal("alaw audio = nil")
	}
	if got, want := audio.Frame.Data, []byte{0x08, 0x00, 0xf8, 0xff}; !bytes.Equal(got, want) {
		t.Fatalf("alaw decoded data = %v, want %v", got, want)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("alaw samples = %d, want one 16-bit PCM sample per encoded byte", audio.Frame.SamplesPerChannel)
	}
}

func TestSarvamTTSImplementsStreamingInterface(t *testing.T) {
	var _ tts.TTS = NewSarvamTTS("test-key", "")
}

func readMultipartFields(t *testing.T, req *http.Request) map[string]string {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	mediaType := req.Header.Get("Content-Type")
	boundary := strings.TrimPrefix(mediaType, "multipart/form-data; boundary=")
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	fields := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		fields[part.FormName()] = string(data)
	}
	return fields
}

func assertSarvamJSONField(t *testing.T, payload map[string]any, key string, want any) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
}

func assertSarvamQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q in query %s", key, got, want, query.Encode())
	}
}
