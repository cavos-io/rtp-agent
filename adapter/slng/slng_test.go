package slng

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const expectedPluginNamespace = "rtp-agent.plugins."

func TestSLNGPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.slng" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.slng", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("plugin version = %q, want reference version", PluginVersion)
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
	assertSLNGField(t, sttPayload, "encoding", "pcm_mulaw")
	assertSLNGField(t, sttPayload, "sample_rate", float64(16000))
	assertSLNGField(t, sttPayload, "language", "es")
	assertSLNGField(t, sttPayload, "enable_partial_transcripts", false)
	assertSLNGField(t, sttPayload, "enable_diarization", true)
	assertSLNGField(t, sttPayload, "min_speakers", float64(2))
	assertSLNGField(t, sttPayload, "max_speakers", float64(4))

	ttsProvider := NewTTS("test-key",
		WithTTSModel("sarvam/bulbul:v3"),
		WithTTSVoice("default"),
		WithTTSLanguage("hi"),
		WithTTSSampleRate(24000),
		WithTTSSpeed(1.2),
		WithTTSModelOptions(map[string]any{"temperature": 0.7}),
	)
	ttsPayload := buildTTSInitPayload(ttsProvider)
	assertSLNGField(t, ttsPayload, "voice", "default")
	assertSLNGField(t, ttsPayload, "language", "hi-IN")
	assertSLNGField(t, ttsPayload, "sample_rate", float64(24000))
	assertSLNGField(t, ttsPayload, "encoding", "linear16")
	assertSLNGField(t, ttsPayload, "speed", float64(1.2))
	assertSLNGField(t, ttsPayload, "temperature", float64(0.7))
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

	_, _, err = ttsAudioFromMessage([]byte(`{"type":"Error","message":"bad voice"}`), 24000)
	if err == nil {
		t.Fatal("error message returned nil error")
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
