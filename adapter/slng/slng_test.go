package slng

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const expectedPluginNamespace = "rtp-agent.plugins."
const expectedSLNGPluginVersion = "1.5.15"

func TestSLNGPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.slng" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.slng", PluginTitle)
	}
	if PluginVersion != expectedSLNGPluginVersion {
		t.Fatalf("plugin version = %q, want rtp-agent plugin version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.slng" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.slng", PluginPackage)
	}
	if !strings.HasPrefix(PluginTitle, expectedPluginNamespace) {
		t.Fatalf("plugin title = %q, want rtp-agent namespace", PluginTitle)
	}
	if !strings.HasPrefix(PluginPackage, expectedPluginNamespace) {
		t.Fatalf("plugin package = %q, want rtp-agent namespace", PluginPackage)
	}
}

func TestSLNGDefaultEndpointsMatchReference(t *testing.T) {
	sttProvider := NewSTT("test-key")
	if sttProvider.endpoint != "wss://api.slng.ai/v1/stt/deepgram/nova:3" {
		t.Fatalf("STT endpoint = %q, want reference default", sttProvider.endpoint)
	}
	if !sttProvider.Capabilities().Streaming || !sttProvider.Capabilities().InterimResults || sttProvider.Capabilities().OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming websocket capabilities", sttProvider.Capabilities())
	}
	if got := stt.Model(sttProvider); got != "slng" {
		t.Fatalf("STT model metadata = %q, want slng", got)
	}
	if got := stt.Provider(sttProvider); got != "SLNG" {
		t.Fatalf("STT provider metadata = %q, want SLNG", got)
	}

	ttsProvider := NewTTS("test-key")
	if ttsProvider.endpoint != "wss://api.slng.ai/v1/tts/deepgram/aura:2" {
		t.Fatalf("TTS endpoint = %q, want reference default", ttsProvider.endpoint)
	}
	if ttsProvider.voice != "aura-2-thalia-en" {
		t.Fatalf("TTS voice = %q, want Aura default voice", ttsProvider.voice)
	}
	if !ttsProvider.Capabilities().Streaming {
		t.Fatalf("TTS capabilities = %+v, want streaming", ttsProvider.Capabilities())
	}
	if got := tts.Model(ttsProvider); got != "slng" {
		t.Fatalf("TTS model metadata = %q, want slng", got)
	}
	if got := tts.Provider(ttsProvider); got != "SLNG" {
		t.Fatalf("TTS provider metadata = %q, want SLNG", got)
	}
}

func TestNewSLNGSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SLNG_API_KEY", "env-key")

	provider := NewSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewSLNGTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SLNG_API_KEY", "env-key")

	provider := NewTTS("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewTTS("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestSLNGLocalEndpointsUsePlainWebsocket(t *testing.T) {
	provider := NewSTT("test-key", WithSTTBaseURL("localhost:9000"))
	if provider.endpoint != "ws://localhost:9000/v1/stt/deepgram/nova:3" {
		t.Fatalf("endpoint = %q, want ws localhost endpoint", provider.endpoint)
	}
}

func TestSLNGRegionOverrideNormalizesLikeReference(t *testing.T) {
	got := normalizeRegionOverride([]string{" US-East ", "EU-WEST"})
	if got != "us-east, eu-west" {
		t.Fatalf("region override = %q, want normalized comma list", got)
	}

	provider := NewTTS("test-key", WithTTSRegionOverride(" US-East,EU-WEST "))
	headers := buildTTSWebsocketHeaders(provider)
	if headers.Get("X-Region-Override") != "us-east, eu-west" {
		t.Fatalf("region header = %q, want normalized header", headers.Get("X-Region-Override"))
	}
}

func TestSLNGGatewayPayloadsMatchReference(t *testing.T) {
	sttProvider := NewSTT("test-key",
		WithSTTModel("slng/deepgram/nova:3"),
		WithSTTEncoding("pcm_mulaw"),
		WithSTTLanguage("es"),
		WithSTTPartialTranscripts(false),
		WithSTTDiarization(true, 2, 4),
	)
	sttPayload := buildSTTInitPayload(sttProvider)
	assertSLNGField(t, sttPayload, "type", "init")
	assertSLNGField(t, sttPayload, "model", "nova-3")
	assertSLNGNestedField(t, sttPayload, "config", "encoding", "pcm_mulaw")
	assertSLNGNestedField(t, sttPayload, "config", "sample_rate", float64(16000))
	assertSLNGNestedField(t, sttPayload, "config", "language", "es")
	assertSLNGNestedField(t, sttPayload, "config", "enable_partials", false)
	assertSLNGNestedField(t, sttPayload, "config", "enable_partial_transcripts", false)
	assertSLNGNestedField(t, sttPayload, "config", "enable_diarization", true)
	assertSLNGNestedField(t, sttPayload, "config", "min_speakers", float64(2))
	assertSLNGNestedField(t, sttPayload, "config", "max_speakers", float64(4))

	ttsProvider := NewTTS("test-key",
		WithTTSModel("elevenlabs/eleven-flash:2.5"),
		WithTTSVoice("ebSkW3c0ScIDKR30TbE2"),
		WithTTSLanguage("id-ID"),
		WithTTSSampleRate(24000),
		WithTTSSpeed(1.1),
	)
	ttsPayload := buildTTSInitPayload(ttsProvider)
	assertSLNGField(t, ttsPayload, "type", "init")
	assertSLNGField(t, ttsPayload, "model", "elevenlabs/eleven-flash:2.5")
	assertSLNGField(t, ttsPayload, "voice", "ebSkW3c0ScIDKR30TbE2")
	assertSLNGField(t, ttsPayload, "language", "id-ID")
	assertSLNGNestedField(t, ttsPayload, "config", "language", "id-ID")
	assertSLNGNestedField(t, ttsPayload, "config", "sample_rate", float64(24000))
	assertSLNGNestedField(t, ttsPayload, "config", "encoding", "linear16")
	assertSLNGNestedField(t, ttsPayload, "config", "speed", float64(1.1))
}

func TestSLNGTTSReceivedEventParsesReferenceShapes(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	audio, done, err := ttsAudioFromMessage([]byte(`{"type":"audio_chunk","data":"`+encoded+`"}`), 24000)
	if err != nil {
		t.Fatalf("audio chunk: %v", err)
	}
	if done || string(audio.Frame.Data) != "\x01\x02\x03" {
		t.Fatalf("audio=%+v done=%v, want decoded chunk", audio, done)
	}

	audio, done, err = ttsAudioFromMessage([]byte(`{"type":"event","data":{"event_type":"final"}}`), 24000)
	if err != nil {
		t.Fatalf("final event: %v", err)
	}
	if audio != nil || !done {
		t.Fatalf("audio=%+v done=%v, want end event", audio, done)
	}

	audio, done, err = ttsAudioFromMessage([]byte(`{"isFinal":true}`), 24000)
	if err != nil {
		t.Fatalf("isFinal message: %v", err)
	}
	if audio != nil || !done {
		t.Fatalf("audio=%+v done=%v, want no-audio isFinal to end segment", audio, done)
	}
	if got := slngTTSMessageKind([]byte(`{"isFinal":true}`)); got != "isFinal" {
		t.Fatalf("message kind = %q, want isFinal", got)
	}

	_, _, err = ttsAudioFromMessage([]byte(`{"type":"Error","message":"bad voice"}`), 24000)
	if err == nil {
		t.Fatal("error message returned nil error")
	}
}

func TestSLNGTTSReceivedEventParsesReferenceTopLevelCompletionTypes(t *testing.T) {
	for _, payload := range []string{
		`{"type":"complete"}`,
		`{"type":"completed"}`,
		`{"type":"done"}`,
		`{"type":"final"}`,
	} {
		audio, done, err := ttsAudioFromMessage([]byte(payload), 24000)
		if err != nil {
			t.Fatalf("ttsAudioFromMessage(%s) error = %v", payload, err)
		}
		if audio != nil || !done {
			t.Fatalf("ttsAudioFromMessage(%s) audio=%+v done=%v, want no-audio end event", payload, audio, done)
		}
	}
}

func TestSLNGTTSStreamUnexpectedCloseReportsAudioStats(t *testing.T) {
	stream := &ttsStream{
		model:           "elevenlabs/eleven-flash:2.5",
		audioFrames:     0,
		audioBytes:      0,
		textMessages:    2,
		lastMessageType: "audio_chunk",
	}

	err := stream.readError(&websocket.CloseError{Code: websocket.CloseNormalClosure, Text: ""})
	got := err.Error()
	for _, want := range []string{
		"slng tts websocket closed before completion",
		"websocket: close 1000 (normal)",
		"model=elevenlabs/eleven-flash:2.5",
		"audio_frames=0",
		"audio_bytes=0",
		"text_messages=2",
		`last_message_type="audio_chunk"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error = %q, want substring %q", got, want)
		}
	}
}

func TestSLNGTTSStreamNormalCloseAfterAudioReturnsEOF(t *testing.T) {
	stream := &ttsStream{
		model:           "elevenlabs/eleven-flash:2.5",
		audioFrames:     3,
		audioBytes:      93622,
		textMessages:    4,
		lastMessageType: "text/unknown",
	}

	err := stream.readError(&websocket.CloseError{Code: websocket.CloseNormalClosure, Text: ""})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("readError() = %v, want io.EOF", err)
	}
}

func TestSLNGSTTStreamEventsMapReferenceMessages(t *testing.T) {
	events, err := sttEventsFromMessage([]byte(`{"type":"Results","is_final":false,"language":"en","channel":{"alternatives":[{"transcript":"hel","confidence":0.5}]}}`), "en", true)
	if err != nil {
		t.Fatalf("results interim: %v", err)
	}
	if len(events) != 2 || events[0].Type != stt.SpeechEventStartOfSpeech || events[1].Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("events = %+v, want start and interim", events)
	}

	events, err = sttEventsFromMessage([]byte(`{"type":"final_transcript","transcript":"hello","confidence":0.9,"language":"en","words":[{"start":0.1,"end":0.4}]}`), "en", true)
	if err != nil {
		t.Fatalf("final transcript: %v", err)
	}
	if len(events) != 2 || events[0].Type != stt.SpeechEventFinalTranscript || events[1].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("events = %+v, want final and end", events)
	}
	if events[0].Alternatives[0].StartTime != 0.1 || events[0].Alternatives[0].EndTime != 0.4 {
		t.Fatalf("alternative = %+v, want word timings", events[0].Alternatives[0])
	}
}

func TestSLNGImplementsCoreInterfaces(t *testing.T) {
	var _ stt.STT = NewSTT("test-key")
	var _ tts.TTS = NewTTS("test-key")
}

func assertSLNGField(t *testing.T, payload []byte, key string, want any) {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got := data[key]; got != want {
		t.Fatalf("%s = %#v, want %#v in %s", key, got, want, string(payload))
	}
}

func assertSLNGNestedField(t *testing.T, payload []byte, parent, key string, want any) {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	parentMap, _ := data[parent].(map[string]any)
	if parentMap == nil {
		t.Fatalf("%s = %#v, want object in %s", parent, data[parent], string(payload))
	}
	if got := parentMap[key]; got != want {
		t.Fatalf("%s.%s = %#v, want %#v in %s", parent, key, got, want, string(payload))
	}
}

func TestBuildSLNGHeaders(t *testing.T) {
	provider := NewSTT("test-key", WithSTTRegionOverride("us-east"))
	headers := buildSTTWebsocketHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" || headers.Get("X-API-Key") != "test-key" {
		t.Fatalf("headers = %+v, want auth headers", headers)
	}
	if headers.Get("X-Region-Override") != "us-east" {
		t.Fatalf("region header = %q, want us-east", headers.Get("X-Region-Override"))
	}

	endpoint, err := url.Parse(provider.endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	if endpoint.Scheme != "wss" {
		t.Fatalf("endpoint scheme = %q, want wss", endpoint.Scheme)
	}
	_ = http.Header(headers)
}
