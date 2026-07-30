package slng

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const expectedPluginNamespace = "rtp-agent.plugins."
const expectedSLNGPluginVersion = PluginVersion

func TestSLNGPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.slng" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.slng", PluginTitle)
	}
	if PluginVersion != expectedSLNGPluginVersion {
		t.Fatalf("plugin version = %q, want rtp-agent plugin version", PluginVersion)
	}
	if PluginVersion == "" {
		t.Fatalf("plugin version = %q, want non-empty project release version", PluginVersion)
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
	if sttProvider.endpoint != "wss://api.slng.ai/v1/bridges/unmute/stt/deepgram/nova:3" {
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
	if ttsProvider.endpoint != "wss://api.slng.ai/v1/bridges/unmute/tts/deepgram/aura:2" {
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

func TestSLNGSTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SLNG_API_KEY", "")
	provider := NewSTT("", WithSTTEndpoint("ws://127.0.0.1:1/v1/stt/deepgram/nova:3"))

	if _, err := provider.Stream(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "SLNG_API_KEY") {
		t.Fatalf("Stream() error = %v, want SLNG_API_KEY guidance before dialing", err)
	}
	if _, err := provider.Recognize(context.Background(), nil, ""); err == nil || !strings.Contains(err.Error(), "SLNG_API_KEY") {
		t.Fatalf("Recognize() error = %v, want SLNG_API_KEY guidance before request", err)
	}
}

func TestSLNGSTTStreamDialFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("slng stt dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewSTT("test-key")
	stream, err := provider.Stream(context.Background(), "")

	if stream != nil {
		t.Fatalf("Stream = %#v, want nil", stream)
	}
	var apiErr *llm.APIConnectionError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
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

func TestSLNGTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SLNG_API_KEY", "")
	provider := NewTTS("", WithTTSEndpoint("ws://127.0.0.1:1/v1/tts/deepgram/aura:2"))

	if _, err := provider.Stream(context.Background()); err == nil || !strings.Contains(err.Error(), "SLNG_API_KEY") {
		t.Fatalf("Stream() error = %v, want SLNG_API_KEY guidance before dialing", err)
	}
	if _, err := provider.Synthesize(context.Background(), "hello"); err == nil || !strings.Contains(err.Error(), "SLNG_API_KEY") {
		t.Fatalf("Synthesize() error = %v, want SLNG_API_KEY guidance before request", err)
	}
}

func TestSLNGTTSStreamDialFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("slng tts dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewTTS("test-key")
	stream, err := provider.Stream(context.Background())

	if stream != nil {
		t.Fatalf("Stream = %#v, want nil", stream)
	}
	var apiErr *llm.APIConnectionError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestSLNGLocalEndpointsUsePlainWebsocket(t *testing.T) {
	provider := NewSTT("test-key", WithSTTBaseURL("localhost:9000"))
	if provider.endpoint != "ws://localhost:9000/v1/bridges/unmute/stt/deepgram/nova:3" {
		t.Fatalf("endpoint = %q, want ws localhost endpoint", provider.endpoint)
	}
}

func TestSLNGSTTExplicitBridgeEndpointDerivesModel(t *testing.T) {
	endpoint := "wss://api.slng.ai/v1/bridges/unmute/stt/deepgram/nova:2"
	provider := NewSTT("test-key", WithSTTEndpoint(endpoint))

	if provider.endpoint != endpoint {
		t.Fatalf("endpoint = %q, want caller-provided endpoint", provider.endpoint)
	}
	if provider.model != "deepgram/nova:2" {
		t.Fatalf("model = %q, want bridge model", provider.model)
	}
	assertSLNGField(t, buildSTTInitPayload(provider), "model", "nova-2")
}

func TestSLNGSTTExplicitBridgeModelEndpointsDeriveModel(t *testing.T) {
	endpoint := "wss://api.slng.ai/v1/bridges/unmute/stt/deepgram/nova:2"
	provider := NewSTT("test-key", WithSTTModelEndpoints(endpoint, "wss://backup.slng.ai/v1/bridges/unmute/stt/deepgram/nova:3"))

	if provider.endpoint != endpoint {
		t.Fatalf("endpoint = %q, want caller-provided endpoint", provider.endpoint)
	}
	if provider.model != "deepgram/nova:2" {
		t.Fatalf("model = %q, want bridge model", provider.model)
	}
	assertSLNGField(t, buildSTTInitPayload(provider), "model", "nova-2")
}

func TestSLNGSTTExplicitLegacyEndpointIsPreserved(t *testing.T) {
	endpoint := "wss://api.slng.ai/v1/stt/deepgram/nova:2"
	provider := NewSTT("test-key", WithSTTEndpoint(endpoint))

	if provider.endpoint != endpoint {
		t.Fatalf("endpoint = %q, want caller-provided endpoint", provider.endpoint)
	}
	if provider.model != "deepgram/nova:2" {
		t.Fatalf("model = %q, want legacy model", provider.model)
	}
}

func TestSLNGSTTFallsBackOnRetryableServerError(t *testing.T) {
	order := make(chan string, 2)
	upgrader := websocket.Upgrader{}
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			order <- "primary"
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order <- "fallback"
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade fallback websocket: %v", err)
				return
			}
			defer conn.Close()
			_, _, _ = conn.ReadMessage()
		}),
	)
	provider := NewSTT("test-key", WithSTTConnections(
		STTConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"},
		STTConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/stt/deepgram/nova:2"},
	))

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if got := []string{<-order, <-order}; !reflect.DeepEqual(got, []string{"primary", "fallback"}) {
		t.Fatalf("connection order = %v, want primary then fallback", got)
	}
}

func TestSLNGSTTDoesNotFallbackOnInvalidRequest(t *testing.T) {
	fallbackHit := make(chan struct{}, 1)
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "invalid request", http.StatusBadRequest)
		}),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fallbackHit <- struct{}{}
			http.Error(w, "unexpected fallback", http.StatusInternalServerError)
		}),
	)
	provider := NewSTT("test-key", WithSTTConnections(
		STTConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"},
		STTConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/stt/deepgram/nova:2"},
	))

	stream, err := provider.Stream(context.Background(), "")
	if stream != nil {
		stream.Close()
		t.Fatalf("Stream() = %#v, want nil", stream)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("Stream() error = %T %v, want APIStatusError 400", err, err)
	}
	select {
	case <-fallbackHit:
		t.Fatal("invalid request tried fallback candidate")
	default:
	}
}

func TestSLNGSTTStopsFallbackOnPayloadTooLarge(t *testing.T) {
	fallbackHit := make(chan struct{}, 1)
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		}),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fallbackHit <- struct{}{}
			http.Error(w, "unexpected fallback", http.StatusInternalServerError)
		}),
	)
	provider := NewSTT("test-key", WithSTTConnections(
		STTConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"},
		STTConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/stt/deepgram/nova:2"},
	))

	stream, err := provider.Stream(context.Background(), "")
	if stream != nil {
		stream.Close()
		t.Fatalf("Stream() = %#v, want nil", stream)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("Stream() error = %T %v, want APIStatusError 413", err, err)
	}
	select {
	case <-fallbackHit:
		t.Fatal("payload-too-large error tried fallback candidate")
	default:
	}
}

func TestSLNGSTTRetriesPrimaryAfterCooldown(t *testing.T) {
	oldNow := slngSTTNow
	now := time.Unix(100, 0)
	slngSTTNow = func() time.Time { return now }
	t.Cleanup(func() { slngSTTNow = oldNow })

	var primaryHits atomic.Int32
	order := make(chan string, 4)
	upgrader := websocket.Upgrader{}
	accept := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			order <- name
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade %s websocket: %v", name, err)
				return
			}
			defer conn.Close()
			_, _, _ = conn.ReadMessage()
		}
	}
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if primaryHits.Add(1) == 1 {
				order <- "primary-failed"
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			accept("primary")(w, r)
		}),
		accept("fallback"),
	)
	const cooldown = time.Minute
	provider := NewSTT("test-key",
		WithSTTConnections(
			STTConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"},
			STTConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/stt/deepgram/nova:2"},
		),
		WithSTTFallbackRecoveryCooldown(cooldown),
	)

	first, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("first Stream() error = %v", err)
	}
	first.Close()
	second, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("second Stream() error = %v", err)
	}
	second.Close()
	now = now.Add(cooldown)
	third, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("third Stream() error = %v", err)
	}
	third.Close()

	got := []string{<-order, <-order, <-order, <-order}
	want := []string{"primary-failed", "fallback", "fallback", "primary"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connection order = %v, want %v", got, want)
	}
}

func TestSLNGRegionOverrideNormalizesLikeReference(t *testing.T) {
	got := normalizeRegionOverride([]string{" US-East ", "EU-WEST"})
	if got != "us-east, eu-west" {
		t.Fatalf("region override = %q, want normalized comma list", got)
	}

	provider := NewTTS("test-key", WithTTSRegionOverride(" US-East,EU-WEST "))
	headers, err := buildTTSWebsocketHeaders(provider)
	if err != nil {
		t.Fatal(err)
	}
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

func TestSLNGTTSInitPayloadIncludesRimeCodaOptions(t *testing.T) {
	provider := NewTTS("test-key",
		WithTTSModel("rime/coda:0-id"),
		WithTTSVoice("speaker-123"),
		WithTTSModelOptions(map[string]any{
			"modelId": "coda-custom",
			"segment": "bySentence",
		}),
	)

	payload := buildTTSInitPayload(provider)

	assertSLNGField(t, payload, "type", "init")
	assertSLNGField(t, payload, "model", "rime/coda:0-id")
	assertSLNGField(t, payload, "voice", "speaker-123")
	assertSLNGField(t, payload, "speaker", "speaker-123")
	assertSLNGNestedField(t, payload, "config", "modelId", "coda-custom")
	assertSLNGNestedField(t, payload, "config", "segment", "bySentence")
}

func TestSLNGTTSInitPayloadDefaultsRimeCodaModelID(t *testing.T) {
	provider := NewTTS("test-key",
		WithTTSModel("slng/rime/coda:0-id"),
		WithTTSVoice("speaker-456"),
	)

	payload := buildTTSInitPayload(provider)

	assertSLNGField(t, payload, "speaker", "speaker-456")
	assertSLNGNestedField(t, payload, "config", "modelId", "coda")
	assertSLNGNestedFieldAbsent(t, payload, "config", "segment")
}

func TestSLNGTTSInitPayloadForwardsElevenLabsModelOptions(t *testing.T) {
	provider := NewTTS("test-key",
		WithTTSModel("elevenlabs/eleven-flash:2.5"),
		WithTTSVoice("voice-123"),
		WithTTSModelOptions(map[string]any{
			"inactivity_timeout":       30,
			"apply_text_normalization": "auto",
			"auto_mode":                true,
			"enable_logging":           false,
			"enable_ssml_parsing":      true,
			"sync_alignment":           true,
			"language_code":            "id",
			"stability":                0.44,
			"similarity_boost":         0.81,
			"style":                    0.2,
			"speed":                    1.05,
			"use_speaker_boost":        true,
			"chunk_length_schedule":    []any{50, 90, 160},
			"preferred_alignment":      "normalized",
			"unsupported_option":       "must-not-leak",
		}),
	)

	payload := buildTTSInitPayload(provider)

	assertSLNGNestedField(t, payload, "config", "inactivity_timeout", float64(30))
	assertSLNGNestedField(t, payload, "config", "apply_text_normalization", "auto")
	assertSLNGNestedField(t, payload, "config", "auto_mode", true)
	assertSLNGNestedField(t, payload, "config", "enable_logging", false)
	assertSLNGNestedField(t, payload, "config", "enable_ssml_parsing", true)
	assertSLNGNestedField(t, payload, "config", "sync_alignment", true)
	assertSLNGNestedField(t, payload, "config", "language_code", "id")
	assertSLNGNestedField(t, payload, "config", "stability", 0.44)
	assertSLNGNestedField(t, payload, "config", "similarity_boost", 0.81)
	assertSLNGNestedField(t, payload, "config", "style", 0.2)
	assertSLNGNestedField(t, payload, "config", "speed", 1.05)
	assertSLNGNestedField(t, payload, "config", "use_speaker_boost", true)
	assertSLNGNestedArrayField(t, payload, "config", "chunk_length_schedule", []any{float64(50), float64(90), float64(160)})
	assertSLNGNestedField(t, payload, "config", "preferred_alignment", "normalized")
	assertSLNGNestedFieldAbsent(t, payload, "config", "unsupported_option")
}

func TestSLNGTTSInitPayloadPreservesExplicitZeroSpeed(t *testing.T) {
	provider := NewTTS("test-key", WithTTSSpeed(0))

	payload := buildTTSInitPayload(provider)

	assertSLNGNestedField(t, payload, "config", "speed", float64(0))
}

func TestSLNGTTSInitPayloadUsesTargetLanguageWithoutLeakingOption(t *testing.T) {
	provider := NewTTS("test-key",
		WithTTSModel("sarvam/bulbul:v3"),
		WithTTSLanguage("en"),
		WithTTSModelOptions(map[string]any{"target_language_code": "hi"}),
	)

	payload := buildTTSInitPayload(provider)

	assertSLNGField(t, payload, "language", "hi-IN")
	assertSLNGNestedField(t, payload, "config", "language", "hi-IN")
	assertSLNGNestedFieldAbsent(t, payload, "config", "target_language_code")
}

func TestSLNGTTSUpdateOptionsAffectsFutureInitPayload(t *testing.T) {
	provider := NewTTS("test-key",
		WithTTSModel("elevenlabs/eleven-flash:2.5"),
		WithTTSVoice("voice-before"),
		WithTTSLanguage("en-US"),
	)

	provider.UpdateOptions(
		WithTTSVoice("voice-after"),
		WithTTSLanguage("id-ID"),
	)

	payload := buildTTSInitPayload(provider)
	assertSLNGField(t, payload, "voice", "voice-after")
	assertSLNGField(t, payload, "language", "id-ID")
	assertSLNGNestedField(t, payload, "config", "language", "id-ID")
	assertSLNGNestedField(t, payload, "config", "sample_rate", float64(24000))
	assertSLNGNestedField(t, payload, "config", "encoding", "linear16")
}

func TestSLNGSTTInitPayloadPreservesExplicitZeroVADSilence(t *testing.T) {
	provider := NewSTT("test-key", WithSTTVADMinSilenceDurationMS(0))

	payload := buildSTTInitPayload(provider)

	assertSLNGNestedField(t, payload, "config", "vad_min_silence_duration_ms", float64(0))
}

func TestSLNGSTTUpdateOptionsAffectsFutureInitAndActiveStream(t *testing.T) {
	provider := NewSTT("test-key",
		WithSTTLanguage("en"),
		WithSTTPartialTranscripts(true),
		WithSTTBufferSizeSeconds(0.064),
	)
	stream := &sttStream{
		language:          "en",
		partials:          true,
		bufferSizeSeconds: 0.064,
		sampleRate:        defaultSLNGSTTSampleRate,
		encoding:          defaultSLNGSTTEncoding,
	}
	provider.registerStream(stream)

	provider.UpdateOptions(
		WithSTTLanguage("id"),
		WithSTTPartialTranscripts(false),
		WithSTTBufferSizeSeconds(0.02),
		WithSTTVADThreshold(0.7),
		WithSTTVADMinSilenceDurationMS(450),
		WithSTTVADSpeechPadMS(80),
		WithSTTDiarization(true, 2, 4),
	)

	payload := buildSTTInitPayload(provider)
	assertSLNGNestedField(t, payload, "config", "language", "id")
	assertSLNGNestedField(t, payload, "config", "enable_partials", false)
	assertSLNGNestedField(t, payload, "config", "enable_partial_transcripts", false)
	assertSLNGNestedField(t, payload, "config", "vad_threshold", float64(0.7))
	assertSLNGNestedField(t, payload, "config", "vad_min_silence_duration_ms", float64(450))
	assertSLNGNestedField(t, payload, "config", "vad_speech_pad_ms", float64(80))
	assertSLNGNestedField(t, payload, "config", "enable_diarization", true)

	if stream.language != "id" {
		t.Fatalf("active stream language = %q, want id", stream.language)
	}
	if stream.partials {
		t.Fatal("active stream partials = true, want false")
	}
	if stream.bufferSizeSeconds != 0.02 {
		t.Fatalf("active stream buffer size = %v, want 0.02", stream.bufferSizeSeconds)
	}
	if stream.vadThreshold != 0.7 {
		t.Fatalf("active stream vad threshold = %v, want 0.7", stream.vadThreshold)
	}
	if stream.vadMinSilenceDurationMS != 450 {
		t.Fatalf("active stream vad min silence = %v, want 450", stream.vadMinSilenceDurationMS)
	}
	if stream.vadSpeechPadMS != 80 {
		t.Fatalf("active stream vad speech pad = %v, want 80", stream.vadSpeechPadMS)
	}
	if !stream.diarization {
		t.Fatal("active stream diarization = false, want true")
	}
}

func TestSLNGSTTUpdateOptionsReconnectsActiveStreamBeforeAudio(t *testing.T) {
	initPayloads := make(chan map[string]any, 2)
	audioWrites := make(chan int, 2)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode init payload: %v", err)
			return
		}
		initPayloads <- init

		msgType, audio, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if msgType == websocket.BinaryMessage {
			audioWrites <- len(audio)
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]

	provider := NewSTT("test-key",
		WithSTTEndpoint(endpoint+"/v1/stt/deepgram/nova:3"),
		WithSTTLanguage("en"),
		WithSTTBufferSizeSeconds(0.01),
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	readInit := func(label string) map[string]any {
		t.Helper()
		select {
		case init := <-initPayloads:
			return init
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s init payload", label)
			return nil
		}
	}
	readInit("initial")

	provider.UpdateOptions(
		WithSTTLanguage("id"),
		WithSTTVADThreshold(0.7),
		WithSTTVADMinSilenceDurationMS(450),
		WithSTTVADSpeechPadMS(80),
		WithSTTDiarization(true, 2, 4),
	)
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, defaultSLNGSTTSampleRate/100*2),
		SampleRate:        defaultSLNGSTTSampleRate,
		NumChannels:       1,
		SamplesPerChannel: uint32(defaultSLNGSTTSampleRate / 100),
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}

	reconnected := readInit("reconnect")
	reconnectedPayload, err := json.Marshal(reconnected)
	if err != nil {
		t.Fatalf("marshal reconnected init: %v", err)
	}
	assertSLNGNestedField(t, reconnectedPayload, "config", "language", "id")
	assertSLNGNestedField(t, reconnectedPayload, "config", "vad_threshold", float64(0.7))
	assertSLNGNestedField(t, reconnectedPayload, "config", "vad_min_silence_duration_ms", float64(450))
	assertSLNGNestedField(t, reconnectedPayload, "config", "vad_speech_pad_ms", float64(80))
	assertSLNGNestedField(t, reconnectedPayload, "config", "enable_diarization", true)
	select {
	case got := <-audioWrites:
		if got == 0 {
			t.Fatal("audio write length = 0, want reconnected audio")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio on reconnected websocket")
	}
}

func TestSLNGSTTConnectionInitOverrideWinsAfterUpdateReconnect(t *testing.T) {
	initPayloads := make(chan map[string]any, 2)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode init payload: %v", err)
			return
		}
		initPayloads <- init
		_, _, _ = conn.ReadMessage()
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"
	override := map[string]any{
		"type": "init",
		"config": map[string]any{
			"language": "candidate",
		},
	}
	provider := NewSTT("test-key",
		WithSTTConnections(STTConnectionConfig{Endpoint: endpoint, Init: override}),
		WithSTTLanguage("provider"),
		WithSTTBufferSizeSeconds(0.001),
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if got := <-initPayloads; !reflect.DeepEqual(got, override) {
		t.Fatalf("initial init = %#v, want candidate override %#v", got, override)
	}

	provider.UpdateOptions(WithSTTLanguage("updated"))
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 32),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 16,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if got := <-initPayloads; !reflect.DeepEqual(got, override) {
		t.Fatalf("reconnect init = %#v, want candidate override %#v", got, override)
	}
}

func TestSLNGSTTReconnectFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("slng stt redial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	stream := &sttStream{
		ctx:                context.Background(),
		provider:           NewSTT("test-key", WithSTTEndpoint("wss://slng.test/v1/stt/deepgram/nova:3")),
		language:           "en",
		sampleRate:         defaultSLNGSTTSampleRate,
		bufferSizeSeconds:  0.01,
		encoding:           "linear16",
		reconnectRequested: true,
	}

	err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, defaultSLNGSTTSampleRate/100*2),
		SampleRate:        defaultSLNGSTTSampleRate,
		NumChannels:       1,
		SamplesPerChannel: uint32(defaultSLNGSTTSampleRate / 100),
	})
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("reconnect error = %T %v, want APIConnectionError", err, err)
	}
}

func TestSLNGSTTReconnectFallsBackAndRecoversPrimaryAfterCooldown(t *testing.T) {
	oldNow := slngSTTNow
	now := time.Unix(200, 0)
	slngSTTNow = func() time.Time { return now }
	t.Cleanup(func() { slngSTTNow = oldNow })

	order := make(chan string, 4)
	var primaryHits atomic.Int32
	upgrader := websocket.Upgrader{}
	accept := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			order <- name
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade %s websocket: %v", name, err)
				return
			}
			defer conn.Close()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}
	}
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if primaryHits.Add(1) == 2 {
				order <- "primary-failed"
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			accept("primary")(w, r)
		}),
		accept("fallback"),
	)
	const cooldown = time.Minute
	provider := NewSTT("test-key",
		WithSTTConnections(
			STTConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"},
			STTConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/stt/deepgram/nova:2"},
		),
		WithSTTFallbackRecoveryCooldown(cooldown),
		WithSTTBufferSizeSeconds(0.001),
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	provider.UpdateOptions(WithSTTLanguage("id"))
	if err := stream.PushFrame(&model.AudioFrame{Data: make([]byte, 32)}); err != nil {
		t.Fatalf("fallback PushFrame() error = %v", err)
	}
	now = now.Add(cooldown)
	provider.UpdateOptions(WithSTTLanguage("en"))
	if err := stream.PushFrame(&model.AudioFrame{Data: make([]byte, 32)}); err != nil {
		t.Fatalf("recovery PushFrame() error = %v", err)
	}

	got := []string{<-order, <-order, <-order, <-order}
	want := []string{"primary", "primary-failed", "fallback", "primary"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connection order = %v, want %v", got, want)
	}
}

func TestSLNGSTTInitialDialPreservesCallerDeadline(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return nil, ctx.Err()
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := NewSTT("test-key", WithSTTEndpoint("ws://slng.test/v1/stt/deepgram/nova:3")).Stream(ctx, "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stream() error = %T %v, want context deadline", err, err)
	}
}

func TestSLNGSTTReconnectPreservesCallerDeadline(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return nil, ctx.Err()
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	stream := &sttStream{
		ctx:                ctx,
		provider:           NewSTT("test-key", WithSTTEndpoint("ws://slng.test/v1/stt/deepgram/nova:3")),
		reconnectRequested: true,
		sampleRate:         defaultSLNGSTTSampleRate,
		encoding:           defaultSLNGSTTEncoding,
	}

	err := stream.PushFrame(&model.AudioFrame{Data: []byte{0, 0}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("PushFrame() error = %T %v, want context deadline", err, err)
	}
}

func TestSLNGSTTNextDiscardsStaleSocketErrorAfterReconnect(t *testing.T) {
	var connections atomic.Int32
	firstReady := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		connection := connections.Add(1)
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if connection == 1 {
			firstReady <- struct{}{}
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}
		for {
			messageType, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType == websocket.BinaryMessage {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(
					`{"type":"final_transcript","transcript":"replacement","language":"en"}`,
				))
				return
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"
	stream, err := NewSTT(
		"test-key",
		WithSTTEndpoint(endpoint),
		WithSTTBufferSizeSeconds(0.001),
	).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	<-firstReady

	type nextResult struct {
		event *stt.SpeechEvent
		err   error
	}
	next := make(chan nextResult, 1)
	go func() {
		event, err := stream.Next()
		next <- nextResult{event: event, err: err}
	}()
	time.Sleep(10 * time.Millisecond)
	concrete := stream.(*sttStream)
	concrete.mu.Lock()
	concrete.reconnectRequested = true
	err = concrete.reconnectLocked()
	concrete.mu.Unlock()
	if err != nil {
		t.Fatalf("reconnectLocked() error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: make([]byte, 32)}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}

	select {
	case result := <-next:
		if result.err != nil {
			t.Fatalf("Next() error = %v, want replacement transcript", result.err)
		}
		if result.event == nil || result.event.Type != stt.SpeechEventStartOfSpeech {
			t.Fatalf("Next() event = %#v, want start_of_speech", result.event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replacement transcript")
	}
}

func TestSLNGSTTInitPayloadUsesVADSpeechPadOption(t *testing.T) {
	provider := NewSTT("test-key", WithSTTVADSpeechPadMS(75))

	payload := buildSTTInitPayload(provider)

	assertSLNGNestedField(t, payload, "config", "vad_speech_pad_ms", float64(75))
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
	if audio == nil || !audio.IsFinal || !done {
		t.Fatalf("audio=%+v done=%v, want final marker", audio, done)
	}
	if audio.Frame != nil {
		t.Fatalf("final marker frame = %+v, want boundary-only marker", audio.Frame)
	}

	audio, done, err = ttsAudioFromMessage([]byte(`{"isFinal":true}`), 24000)
	if err != nil {
		t.Fatalf("isFinal message: %v", err)
	}
	if audio == nil || !audio.IsFinal || !done {
		t.Fatalf("audio=%+v done=%v, want final marker for no-audio isFinal", audio, done)
	}
	if audio.Frame != nil {
		t.Fatalf("isFinal marker frame = %+v, want boundary-only marker", audio.Frame)
	}
	if got := slngTTSMessageKind([]byte(`{"isFinal":true}`)); got != "isFinal" {
		t.Fatalf("message kind = %q, want isFinal", got)
	}

	_, _, err = ttsAudioFromMessage([]byte(`{"type":"Error","message":"bad voice"}`), 24000)
	if err == nil {
		t.Fatal("error message returned nil error")
	}
}

func TestSLNGTTSProviderErrorFramesReturnAPIStatusError(t *testing.T) {
	for _, payload := range []string{
		`{"type":"Error","message":"bad voice"}`,
		`{"error":"rate limited"}`,
	} {
		audio, done, err := ttsAudioFromMessage([]byte(payload), 24000)
		if audio != nil || done {
			t.Fatalf("ttsAudioFromMessage(%s) = (%#v, %v, %v), want nil false error", payload, audio, done, err)
		}
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("ttsAudioFromMessage(%s) error = %T %v, want APIStatusError", payload, err, err)
		}
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
		if audio == nil || !audio.IsFinal || !done {
			t.Fatalf("ttsAudioFromMessage(%s) audio=%+v done=%v, want final marker", payload, audio, done)
		}
		if audio.Frame != nil {
			t.Fatalf("ttsAudioFromMessage(%s) final frame = %+v, want boundary-only marker", payload, audio.Frame)
		}
	}
}

func TestSLNGTTSReceivedEventIgnoresInvalidBase64LikeReference(t *testing.T) {
	for _, payload := range []string{
		`{"type":"audio_chunk","data":"not-base64"}`,
		`{"audio":"not-base64"}`,
	} {
		audio, done, err := ttsAudioFromMessage([]byte(payload), 24000)
		if err != nil {
			t.Fatalf("ttsAudioFromMessage(%s) error = %v, want nil", payload, err)
		}
		if audio != nil || done {
			t.Fatalf("ttsAudioFromMessage(%s) audio=%+v done=%v, want ignored frame", payload, audio, done)
		}
	}

	audio, done, err := ttsAudioFromMessage([]byte(`{"audio":"not-base64","isFinal":true}`), 24000)
	if err != nil {
		t.Fatalf("ttsAudioFromMessage(isFinal invalid audio) error = %v, want nil", err)
	}
	if audio == nil || !audio.IsFinal || !done {
		t.Fatalf("ttsAudioFromMessage(isFinal invalid audio) audio=%+v done=%v, want final marker", audio, done)
	}
	if audio.Frame != nil {
		t.Fatalf("ttsAudioFromMessage(isFinal invalid audio) frame = %+v, want boundary-only marker", audio.Frame)
	}
}

func TestSLNGTTSReceivedEventDecodesReferenceNoisyBase64(t *testing.T) {
	for _, payload := range []string{
		`{"type":"audio_chunk","data":"AQI=!!!!"}`,
		`{"audio":"AQI=!!!!"}`,
	} {
		audio, done, err := ttsAudioFromMessage([]byte(payload), 24000)
		if err != nil {
			t.Fatalf("ttsAudioFromMessage(%s) error = %v", payload, err)
		}
		if audio == nil || audio.Frame == nil || done {
			t.Fatalf("ttsAudioFromMessage(%s) audio=%+v done=%v, want decoded audio", payload, audio, done)
		}
		if got := string(audio.Frame.Data); got != "\x01\x02" {
			t.Fatalf("ttsAudioFromMessage(%s) audio data = %q, want decoded noisy base64 bytes", payload, got)
		}
	}
}

func TestSLNGTTSReceivedEventIgnoresNonJSONTextLikeReference(t *testing.T) {
	audio, done, err := ttsAudioFromMessage([]byte(`not-json`), 24000)
	if err != nil {
		t.Fatalf("ttsAudioFromMessage(non-json) error = %v, want nil", err)
	}
	if audio != nil || done {
		t.Fatalf("ttsAudioFromMessage(non-json) audio=%+v done=%v, want ignored frame", audio, done)
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
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("readError() = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != websocket.CloseNormalClosure {
		t.Fatalf("StatusCode = %d, want normal close code", statusErr.StatusCode)
	}
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

func TestSLNGTTSRimeArcanaNormalCloseReturnsEOF(t *testing.T) {
	stream := &ttsStream{
		model:           "rime/arcana:en",
		audioFrames:     0,
		audioBytes:      0,
		textMessages:    1,
		lastMessageType: "text",
	}

	err := stream.readError(&websocket.CloseError{Code: websocket.CloseNormalClosure, Text: ""})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("readError() = %v, want io.EOF", err)
	}
}

func TestSLNGTTSRimeCodaNormalCloseReturnsEOF(t *testing.T) {
	stream := &ttsStream{
		model:           "rime/coda:0-id",
		audioFrames:     0,
		audioBytes:      0,
		textMessages:    1,
		lastMessageType: "text",
	}

	err := stream.readError(&websocket.CloseError{Code: websocket.CloseNormalClosure, Text: ""})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("readError() = %v, want io.EOF", err)
	}
}

func TestSLNGTTSRimeArcanaFlushSendsCancel(t *testing.T) {
	messages := make(chan map[string]any, 3)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		for i := 0; i < 3; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read message %d: %v", i, err)
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Errorf("decode message %d: %v", i, err)
				return
			}
			messages <- message
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]

	provider := NewTTS("test-key",
		WithTTSModel("rime/arcana:en"),
		WithTTSEndpoint(endpoint),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	for _, want := range []string{"init", "text", "cancel"} {
		select {
		case message := <-messages:
			if got := message["type"]; got != want {
				t.Fatalf("message type = %#v, want %#v in %#v", got, want, message)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s message", want)
		}
	}
}

func TestSLNGTTSStreamTextMessageUsesReferenceSpacing(t *testing.T) {
	messages := make(chan map[string]any, 3)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		for i := 0; i < 3; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read message %d: %v", i, err)
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Errorf("decode message %d: %v", i, err)
				return
			}
			messages <- message
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]

	provider := NewTTS("test-key", WithTTSEndpoint(endpoint))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	for _, wantType := range []string{"init", "text", "flush"} {
		select {
		case message := <-messages:
			if got := message["type"]; got != wantType {
				t.Fatalf("message type = %#v, want %#v in %#v", got, wantType, message)
			}
			if wantType == "text" && message["text"] != "hello " {
				t.Fatalf("text message = %#v, want %#v", message["text"], "hello ")
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s message", wantType)
		}
	}
}

func TestSLNGTTSStreamTokenizesWordsAndFlushesTailLikeReference(t *testing.T) {
	messages := make(chan map[string]any, 4)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		for i := 0; i < 4; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read message %d: %v", i, err)
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Errorf("decode message %d: %v", i, err)
				return
			}
			messages <- message
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]

	provider := NewTTS("test-key", WithTTSEndpoint(endpoint))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello wor"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	readMessage := func(want string) map[string]any {
		t.Helper()
		select {
		case message := <-messages:
			if got := message["type"]; got != want {
				t.Fatalf("message type = %#v, want %#v in %#v", got, want, message)
			}
			return message
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s message", want)
		}
		return nil
	}

	readMessage("init")
	first := readMessage("text")
	if first["text"] != "hello " {
		t.Fatalf("first text message = %#v, want completed word only", first)
	}
	tail := readMessage("text")
	if tail["text"] != "wor " {
		t.Fatalf("tail text message = %#v, want flushed tail word", tail)
	}
	readMessage("flush")
}

func TestSLNGTTSWordChunkingBuffersLetterlessTokens(t *testing.T) {
	messages := make(chan map[string]any, 8)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil {
				messages <- message
				if message["type"] == "flush" {
					return
				}
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]
	provider := NewTTS("test-key", WithTTSEndpoint(endpoint))
	defer provider.Close()
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("— hello 4.5 world !"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	var got []string
	for {
		select {
		case message := <-messages:
			if message["type"] == "text" {
				got = append(got, slngString(message["text"]))
			}
			if message["type"] == "flush" {
				goto complete
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for provider-safe word messages")
		}
	}
complete:
	want := []string{"— hello 4.5 ", "world ! "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("text frames = %#v, want letter-aware frames %#v", got, want)
	}
}

func TestSLNGTTSRejectsInvalidChunking(t *testing.T) {
	provider := NewTTS("key", WithTTSTextChunking("sentence", 60))
	if provider.optionError == nil {
		t.Fatal("optionError = nil")
	}
}

func TestSLNGTTSPhraseChunkingKeepsUnicodeWords(t *testing.T) {
	got := slngPhraseChunks("नमस्ते — दुनिया 4.5", 20)
	want := []string{"नमस्ते — दुनिया 4.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slngPhraseChunks() = %#v, want %#v", got, want)
	}
}

func TestSLNGTTSPhraseChunkingBatchesTextFrames(t *testing.T) {
	messages := make(chan map[string]any, 4)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		for i := 0; i < 4; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message map[string]any
			_ = json.Unmarshal(payload, &message)
			messages <- message
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]
	provider := NewTTS("test-key",
		WithTTSEndpoint(endpoint),
		WithTTSTextChunking(TTSChunkingPhrase, 60),
	)
	defer provider.Close()
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("Hello world. More text"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	var got []string
	for i := 0; i < 4; i++ {
		select {
		case message := <-messages:
			if message["type"] == "text" {
				got = append(got, slngString(message["text"]))
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for phrase messages")
		}
	}
	if want := []string{"Hello world. ", "More text "}; !reflect.DeepEqual(got, want) {
		t.Fatalf("text frames = %#v, want phrase batches %#v", got, want)
	}
}

func TestSLNGTTSFirstAudioTimeout(t *testing.T) {
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		for i := 0; i < 2; i++ {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
		<-r.Context().Done()
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]
	provider := NewTTS("test-key",
		WithTTSEndpoint(endpoint),
		WithTTSFirstAudioTimeout(20*time.Millisecond),
	)
	defer provider.Close()
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}

	_, err = stream.Next()
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next() error = %T %v, want APITimeoutError", err, err)
	}
}

func TestSLNGTTSFirstAudioTimeoutArmsWhileNextIsBlocked(t *testing.T) {
	initReceived := make(chan struct{}, 1)
	textReceived := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		initReceived <- struct{}{}
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil && message["type"] == "text" {
				select {
				case textReceived <- struct{}{}:
				default:
				}
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]
	provider := NewTTS("test-key",
		WithTTSEndpoint(endpoint),
		WithTTSFirstAudioTimeout(20*time.Millisecond),
	)
	defer provider.Close()
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	<-initReceived

	result := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("Next() returned before text: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	<-textReceived
	select {
	case err := <-result:
		var timeoutErr *llm.APITimeoutError
		if !errors.As(err, &timeoutErr) {
			t.Fatalf("Next() error = %T %v, want APITimeoutError", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next() did not observe timeout armed after its read began")
	}
}

func TestSLNGTTSExhaustedFirstAudioTimeoutClosesStream(t *testing.T) {
	connectionClosed := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				connectionClosed <- struct{}{}
				return
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]
	provider := NewTTS("test-key",
		WithTTSEndpoint(endpoint),
		WithTTSFirstAudioTimeout(20*time.Millisecond),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	_, err = stream.Next()
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next() error = %T %v, want APITimeoutError", err, err)
	}
	if err := stream.PushText("late text"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText() after timeout = %v, want io.ErrClosedPipe", err)
	}
	select {
	case <-connectionClosed:
	case <-time.After(time.Second):
		t.Fatal("exhausted timeout did not close websocket")
	}
	provider.mu.Lock()
	registered := len(provider.streams)
	provider.mu.Unlock()
	if registered != 0 {
		t.Fatalf("registered streams = %d, want 0 after terminal timeout", registered)
	}
	raw := stream.(*ttsStream)
	raw.mu.Lock()
	deadline := raw.firstAudioDeadline
	raw.mu.Unlock()
	if !deadline.IsZero() {
		t.Fatalf("first audio deadline = %v, want cleared", deadline)
	}
}

func TestSLNGTTSFirstAudioTimeoutStopsOnCancellation(t *testing.T) {
	textReceived := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
		_, _, _ = conn.ReadMessage()
		textReceived <- struct{}{}
		<-r.Context().Done()
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]
	ctx, cancel := context.WithCancel(context.Background())
	provider := NewTTS("test-key",
		WithTTSEndpoint(endpoint),
		WithTTSFirstAudioTimeout(200*time.Millisecond),
	)
	defer provider.Close()
	stream, err := provider.Stream(ctx)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		result <- err
	}()
	<-textReceived
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Next() error = %T %v, want context.Canceled", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next() did not stop after cancellation")
	}
}

func TestSLNGTTSFallsBackBeforeFirstAudio(t *testing.T) {
	texts := make(chan string, 2)
	upgrader := websocket.Upgrader{}
	handler := func(status int, audio []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade websocket: %v", err)
				return
			}
			defer conn.Close()
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Errorf("decode text message: %v", err)
				return
			}
			texts <- slngString(message["text"])
			if status != 0 {
				_ = conn.WriteJSON(map[string]any{"type": "Error", "message": "unavailable", "status_code": status})
				return
			}
			_ = conn.WriteMessage(websocket.BinaryMessage, audio)
		}
	}
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		handler(http.StatusServiceUnavailable, nil),
		handler(0, []byte{1, 2}),
	)
	provider := NewTTS("test-key", WithTTSConnections(
		TTSConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
		TTSConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
	))
	defer provider.Close()
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if audio == nil || audio.Frame == nil || !reflect.DeepEqual(audio.Frame.Data, []byte{1, 2}) {
		t.Fatalf("Next() audio = %#v, want fallback audio", audio)
	}
	if got := []string{<-texts, <-texts}; !reflect.DeepEqual(got, []string{"hello ", "hello "}) {
		t.Fatalf("replayed texts = %#v, want primary then fallback copy", got)
	}
}

func TestSLNGTTSCloseDuringFallbackDialDiscardsNewConnection(t *testing.T) {
	oldDial := slngTTSDialContext
	t.Cleanup(func() { slngTTSDialContext = oldDial })

	fallbackDialReady := make(chan struct{}, 1)
	releaseFallbackDial := make(chan struct{})
	fallbackDone := make(chan struct{})
	var fallbackReplayed atomic.Bool
	upgrader := websocket.Upgrader{}
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade primary websocket: %v", err)
				return
			}
			defer conn.Close()
			_, _, _ = conn.ReadMessage()
			_, _, _ = conn.ReadMessage()
			_ = conn.WriteJSON(map[string]any{
				"type":        "Error",
				"message":     "unavailable",
				"status_code": http.StatusServiceUnavailable,
			})
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(fallbackDone)
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			for {
				_, payload, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var message map[string]any
				if json.Unmarshal(payload, &message) == nil && message["type"] == "text" {
					fallbackReplayed.Store(true)
					return
				}
			}
		}),
	)
	fallbackEndpoint := endpoints[1] + "/v1/bridges/unmute/tts/deepgram/aura:2"
	slngTTSDialContext = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		conn, response, err := oldDial(ctx, endpoint, headers)
		if endpoint == fallbackEndpoint && err == nil {
			fallbackDialReady <- struct{}{}
			<-releaseFallbackDial
		}
		return conn, response, err
	}

	provider := NewTTS("test-key", WithTTSConnections(
		TTSConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
		TTSConnectionConfig{Endpoint: fallbackEndpoint},
	))
	defer provider.Close()
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		result <- err
	}()
	select {
	case <-fallbackDialReady:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fallback dial")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	close(releaseFallbackDial)
	select {
	case <-fallbackDone:
	case <-time.After(time.Second):
		t.Fatal("fallback connection remained open after stream close")
	}
	if fallbackReplayed.Load() {
		t.Fatal("closed stream replayed text to newly dialed fallback")
	}
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("Next() remained blocked after closing during fallback")
	}
}

func TestSLNGTTSDoesNotFallbackAfterAudio(t *testing.T) {
	fallbackHit := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade primary websocket: %v", err)
				return
			}
			defer conn.Close()
			_, _, _ = conn.ReadMessage()
			_, _, _ = conn.ReadMessage()
			_ = conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2})
			_ = conn.WriteJSON(map[string]any{"type": "Error", "message": "late failure", "status_code": http.StatusServiceUnavailable})
		}),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fallbackHit <- struct{}{}
			http.Error(w, "unexpected fallback", http.StatusInternalServerError)
		}),
	)
	provider := NewTTS("test-key", WithTTSConnections(
		TTSConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
		TTSConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
	))
	defer provider.Close()
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if _, err := stream.Next(); err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second Next() error = %T %v, want APIStatusError 503", err, err)
	}
	select {
	case <-fallbackHit:
		t.Fatal("post-audio failure replayed text to fallback")
	default:
	}
}

func TestSLNGTTSDoesNotFallbackOnInvalidRequest(t *testing.T) {
	testSLNGTTSNonFallbackStatus(t, http.StatusBadRequest)
}

func TestSLNGTTSStopsFallbackOnPayloadTooLarge(t *testing.T) {
	testSLNGTTSNonFallbackStatus(t, http.StatusRequestEntityTooLarge)
}

func TestSLNGTTSRetriesPrimaryAfterCooldown(t *testing.T) {
	oldNow := slngTTSNow
	now := time.Unix(100, 0)
	slngTTSNow = func() time.Time { return now }
	t.Cleanup(func() { slngTTSNow = oldNow })

	var primaryHits atomic.Int32
	order := make(chan string, 4)
	upgrader := websocket.Upgrader{}
	handler := func(name string, failFirst bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade %s websocket: %v", name, err)
				return
			}
			defer conn.Close()
			_, _, _ = conn.ReadMessage()
			_, _, _ = conn.ReadMessage()
			if failFirst && primaryHits.Add(1) == 1 {
				order <- "primary-failed"
				_ = conn.WriteJSON(map[string]any{"type": "Error", "message": "unavailable", "status_code": http.StatusServiceUnavailable})
				return
			}
			order <- name
			_ = conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2})
		}
	}
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		handler("primary", true),
		handler("fallback", false),
	)
	const cooldown = time.Minute
	provider := NewTTS("test-key",
		WithTTSConnections(
			TTSConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
			TTSConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
		),
		WithTTSFallbackRecoveryCooldown(cooldown),
	)
	defer provider.Close()

	nextAudio := func(text string) {
		t.Helper()
		stream, err := provider.Stream(context.Background())
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		if err := stream.PushText(text + " tail"); err != nil {
			t.Fatalf("PushText() error = %v", err)
		}
		if _, err := stream.Next(); err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		_ = stream.Close()
	}
	nextAudio("first")
	nextAudio("second")
	now = now.Add(cooldown)
	nextAudio("third")

	got := []string{<-order, <-order, <-order, <-order}
	want := []string{"primary-failed", "fallback", "fallback", "primary"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connection order = %v, want %v", got, want)
	}
}

func TestSLNGTTSCandidateVoiceAndInitOverride(t *testing.T) {
	fallbackInit := make(chan map[string]any, 2)
	upgrader := websocket.Upgrader{}
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade primary websocket: %v", err)
				return
			}
			defer conn.Close()
			_, _, _ = conn.ReadMessage()
			_, _, _ = conn.ReadMessage()
			_ = conn.WriteJSON(map[string]any{"type": "Error", "message": "unavailable", "status_code": http.StatusServiceUnavailable})
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Candidate"); got != "fallback" {
				t.Errorf("X-Candidate = %q, want fallback", got)
			}
			if got := r.Header.Get("X-Shared"); got != "candidate" {
				t.Errorf("X-Shared = %q, want candidate override", got)
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade fallback websocket: %v", err)
				return
			}
			defer conn.Close()
			_, payload, _ := conn.ReadMessage()
			var init map[string]any
			_ = json.Unmarshal(payload, &init)
			fallbackInit <- init
			_, _, _ = conn.ReadMessage()
			_ = conn.WriteJSON(map[string]any{"type": "Error", "message": "unavailable", "status_code": http.StatusServiceUnavailable})
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Candidate"); got != "runtime" {
				t.Errorf("X-Candidate = %q, want runtime", got)
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade runtime websocket: %v", err)
				return
			}
			defer conn.Close()
			_, payload, _ := conn.ReadMessage()
			var init map[string]any
			_ = json.Unmarshal(payload, &init)
			fallbackInit <- init
			_, _, _ = conn.ReadMessage()
			_ = conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2})
		}),
	)
	runtimeInit := map[string]any{"type": "init", "voice": "runtime-voice", "custom": true}
	provider := NewTTS("test-key",
		WithTTSVoice("default-voice"),
		WithTTSExtraHeaders(http.Header{"X-Shared": {"default"}}),
		WithTTSConnections(
			TTSConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
			TTSConnectionConfig{
				Endpoint: endpoints[1] + "/v1/bridges/unmute/tts/deepgram/aura:2",
				Voice:    "fallback-voice",
				Headers:  http.Header{"X-Candidate": {"fallback"}, "X-Shared": {"candidate"}},
			},
			TTSConnectionConfig{
				Endpoint: endpoints[2] + "/v1/bridges/unmute/tts/deepgram/aura:2",
				Headers:  http.Header{"X-Candidate": {"runtime"}},
				Init:     runtimeInit,
			},
		),
	)
	defer provider.Close()
	runtimeInit["custom"] = false
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	voiceInit := <-fallbackInit
	if voiceInit["voice"] != "fallback-voice" {
		t.Fatalf("candidate voice init = %#v, want fallback voice", voiceInit)
	}
	init := <-fallbackInit
	if !reflect.DeepEqual(init, map[string]any{"type": "init", "voice": "runtime-voice", "custom": true}) {
		t.Fatalf("fallback init = %#v, want cloned runtime override", init)
	}
}

func testSLNGTTSNonFallbackStatus(t *testing.T, status int) {
	t.Helper()
	fallbackHit := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade primary websocket: %v", err)
				return
			}
			defer conn.Close()
			_, _, _ = conn.ReadMessage()
			_, _, _ = conn.ReadMessage()
			_ = conn.WriteJSON(map[string]any{"type": "Error", "message": http.StatusText(status), "status_code": status})
		}),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fallbackHit <- struct{}{}
			http.Error(w, "unexpected fallback", http.StatusInternalServerError)
		}),
	)
	provider := NewTTS("test-key", WithTTSConnections(
		TTSConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
		TTSConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
	))
	defer provider.Close()
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != status {
		t.Fatalf("Next() error = %T %v, want APIStatusError %d", err, err, status)
	}
	select {
	case <-fallbackHit:
		t.Fatalf("status %d tried fallback candidate", status)
	default:
	}
}

func TestSLNGTTSWarmStandbyReusesReadyConnection(t *testing.T) {
	var connections atomic.Int32
	ready := make(chan int32, 2)
	textConnection := make(chan int32, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		connection := connections.Add(1)
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		ready <- connection
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil && message["type"] == "text" {
				textConnection <- connection
				_ = conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2})
				return
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]
	provider := NewTTS("test-key",
		WithTTSEndpoint(endpoint),
		WithTTSWarmStandby(true),
	)
	defer provider.Close()
	provider.Prewarm()
	select {
	case connection := <-ready:
		if connection != 1 {
			t.Fatalf("prewarmed connection = %d, want 1", connection)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for warm standby")
	}
	waitSLNGTTSStandby(t, provider)

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	select {
	case connection := <-textConnection:
		if connection != 1 {
			t.Fatalf("text used connection %d, want prewarmed connection 1", connection)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for text on warm standby")
	}
}

func TestSLNGTTSUpdateOptionsDiscardsStaleStandby(t *testing.T) {
	initPayloads := make(chan map[string]any, 2)
	standbyClosed := make(chan struct{}, 1)
	var connections atomic.Int32
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		connection := connections.Add(1)
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var init map[string]any
		_ = json.Unmarshal(payload, &init)
		initPayloads <- init
		if _, _, err := conn.ReadMessage(); err != nil && connection == 1 {
			standbyClosed <- struct{}{}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]
	provider := NewTTS("test-key",
		WithTTSEndpoint(endpoint),
		WithTTSVoice("voice-before"),
		WithTTSWarmStandby(true),
	)
	defer provider.Close()
	provider.Prewarm()
	select {
	case init := <-initPayloads:
		if init["voice"] != "voice-before" {
			t.Fatalf("standby voice = %#v, want voice-before", init["voice"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for standby init")
	}
	waitSLNGTTSStandby(t, provider)

	provider.UpdateOptions(WithTTSVoice("voice-after"))
	select {
	case <-standbyClosed:
	case <-time.After(time.Second):
		t.Fatal("stale standby connection was not closed")
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	select {
	case init := <-initPayloads:
		if init["voice"] != "voice-after" {
			t.Fatalf("new connection voice = %#v, want voice-after", init["voice"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fresh init")
	}
	if connections.Load() != 2 {
		t.Fatalf("connections = %d, want stale standby plus fresh dial", connections.Load())
	}
}

func TestSLNGTTSUpdateOptionsInvalidatesStandbyForAttemptSettings(t *testing.T) {
	tests := []struct {
		name string
		opt  TTSOption
	}{
		{name: "endpoint", opt: WithTTSEndpoint("ws://updated.local/v1/bridges/unmute/tts/deepgram/aura:2")},
		{name: "connections", opt: WithTTSConnections(TTSConnectionConfig{Endpoint: "ws://candidate.local/v1/bridges/unmute/tts/deepgram/aura:2"})},
		{name: "provider key", opt: WithTTSProviderAPIKey("provider-key")},
		{name: "region", opt: WithTTSRegionOverride("eu")},
		{name: "world part", opt: WithTTSWorldPartOverride("ap")},
		{name: "tracking", opt: WithTTSExternalTracking("agent", "session")},
		{name: "extra headers", opt: WithTTSExtraHeaders(http.Header{"X-Extra": {"updated"}})},
		{name: "voice", opt: WithTTSVoice("updated-voice")},
		{name: "language", opt: WithTTSLanguage("id")},
		{name: "sample rate", opt: WithTTSSampleRate(16000)},
		{name: "speed", opt: WithTTSSpeed(1.2)},
		{name: "model options", opt: WithTTSModelOptions(map[string]any{"temperature": 0.3})},
		{name: "runtime init", opt: WithTTSRuntimeInit(map[string]any{"type": "init", "custom": true})},
		{name: "chunking", opt: WithTTSTextChunking(TTSChunkingPhrase, 32)},
		{name: "first audio timeout", opt: WithTTSFirstAudioTimeout(time.Second)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := NewTTS("test-key", WithTTSWarmStandby(true))
			before := provider.standbyEpoch
			provider.UpdateOptions(test.opt)
			if provider.standbyEpoch != before+1 {
				t.Fatalf("standby epoch = %d, want %d", provider.standbyEpoch, before+1)
			}
		})
	}
}

func TestSLNGTTSCloseClosesStandbyConnection(t *testing.T) {
	standbyReady := make(chan struct{}, 1)
	standbyClosed := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		standbyReady <- struct{}{}
		if _, _, err := conn.ReadMessage(); err != nil {
			standbyClosed <- struct{}{}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]
	provider := NewTTS("test-key",
		WithTTSEndpoint(endpoint),
		WithTTSWarmStandby(true),
	)
	provider.Prewarm()
	select {
	case <-standbyReady:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for standby init")
	}
	waitSLNGTTSStandby(t, provider)
	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-standbyClosed:
	case <-time.After(time.Second):
		t.Fatal("provider Close did not close standby websocket")
	}
}

func TestSLNGTTSFallbackDiscardsPrimaryStandby(t *testing.T) {
	var primaryConnections atomic.Int32
	var fallbackConnections atomic.Int32
	standbyConnected := make(chan struct{}, 1)
	releaseStandbyInit := make(chan struct{})
	primaryStandbyUsed := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	primary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade primary websocket: %v", err)
			return
		}
		defer conn.Close()
		connection := primaryConnections.Add(1)
		if connection == 2 {
			standbyConnected <- struct{}{}
			<-releaseStandbyInit
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if connection == 1 {
			<-standbyConnected
			_ = conn.WriteJSON(map[string]any{"type": "Error", "message": "unavailable", "status_code": http.StatusServiceUnavailable})
			return
		}
		primaryStandbyUsed <- struct{}{}
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{9, 9})
	})
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade fallback websocket: %v", err)
			return
		}
		defer conn.Close()
		fallbackConnections.Add(1)
		_, _, _ = conn.ReadMessage()
		_, _, _ = conn.ReadMessage()
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2})
	})
	endpoints := newSLNGInMemoryWebsocketEndpoints(t, primary, fallback)
	provider := NewTTS("test-key",
		WithTTSConnections(
			TTSConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
			TTSConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
		),
		WithTTSWarmStandby(true),
	)
	defer provider.Close()
	first, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("first Stream() error = %v", err)
	}
	if err := first.PushText("first tail"); err != nil {
		t.Fatalf("first PushText() error = %v", err)
	}
	if _, err := first.Next(); err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	_ = first.Close()
	close(releaseStandbyInit)
	waitSLNGTTSStandbyTask(t, provider)

	second, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("second Stream() error = %v", err)
	}
	if err := second.PushText("second tail"); err != nil {
		t.Fatalf("second PushText() error = %v", err)
	}
	if _, err := second.Next(); err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	_ = second.Close()
	select {
	case <-primaryStandbyUsed:
		t.Fatal("primary standby was reused after fallback became active")
	default:
	}
	if got := fallbackConnections.Load(); got != 2 {
		t.Fatalf("fallback connections = %d, want 2 active streams", got)
	}
}

func waitSLNGTTSStandby(t *testing.T, provider *TTS) {
	t.Helper()
	timeout := time.After(time.Second)
	for {
		provider.mu.Lock()
		ready := provider.standby != nil
		provider.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-timeout:
			t.Fatal("timed out waiting for standby installation")
		default:
			runtime.Gosched()
		}
	}
}

func waitSLNGTTSStandbyTask(t *testing.T, provider *TTS) {
	t.Helper()
	timeout := time.After(time.Second)
	for {
		provider.mu.Lock()
		done := provider.standbyCancel == nil
		provider.mu.Unlock()
		if done {
			return
		}
		select {
		case <-timeout:
			t.Fatal("timed out waiting for standby task")
		default:
			runtime.Gosched()
		}
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
	if len(events) != 3 || events[0].Type != stt.SpeechEventStartOfSpeech || events[1].Type != stt.SpeechEventFinalTranscript || events[2].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("events = %+v, want start, final, and end", events)
	}
	if events[1].Alternatives[0].StartTime != 0.1 || events[1].Alternatives[0].EndTime != 0.4 {
		t.Fatalf("alternative = %+v, want word timings", events[1].Alternatives[0])
	}
}

func TestSLNGSTTStreamIgnoresReferenceNonJSONTextFrame(t *testing.T) {
	events, err := sttEventsFromMessage([]byte(`not-json`), "en", true)
	if err != nil {
		t.Fatalf("non-json text frame error = %v, want nil", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want ignored frame", events)
	}
}

func TestSLNGSTTStreamNextPreservesReferenceEventSequence(t *testing.T) {
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		for _, message := range []string{
			`{"type":"Results","is_final":false,"language":"en","channel":{"alternatives":[{"transcript":"hel","confidence":0.5}]}}`,
			`{"type":"Results","is_final":false,"language":"en","channel":{"alternatives":[{"transcript":"hell","confidence":0.6}]}}`,
			`{"type":"final_transcript","transcript":"hello","confidence":0.9,"language":"en","words":[{"start":0.1,"end":0.4}]}`,
		} {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
				t.Errorf("write transcript message: %v", err)
				return
			}
		}
		<-r.Context().Done()
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]

	provider := NewSTT("test-key", WithSTTEndpoint(endpoint))
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	wantTypes := []stt.SpeechEventType{
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
	}
	for _, wantType := range wantTypes {
		event := nextSLNGTestSpeechEvent(t, stream)
		if event.Type != wantType {
			t.Fatalf("event type = %s, want %s", event.Type, wantType)
		}
	}
}

func TestSLNGSTTOrdersPartialFinalSpeechAndUsage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for {
			messageType, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType != websocket.BinaryMessage {
				continue
			}
			for _, message := range []string{
				`{"type":"partial_transcript","transcript":"hel","confidence":0.5,"language":"en"}`,
				`{"type":"final_transcript","transcript":"hello","confidence":0.9,"language":"en","words":[{"start":0.1,"end":0.4}]}`,
			} {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
					return
				}
			}
			return
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"
	stream, err := NewSTT(
		"test-key",
		WithSTTEndpoint(endpoint),
		WithSTTBufferSizeSeconds(0.001),
	).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 32),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 16,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}

	want := []stt.SpeechEventType{
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
		stt.SpeechEventRecognitionUsage,
	}
	for _, wantType := range want {
		event := nextSLNGTestSpeechEvent(t, stream)
		if event.Type != wantType {
			t.Fatalf("event type = %s, want %s", event.Type, wantType)
		}
		if wantType == stt.SpeechEventRecognitionUsage &&
			(event.RecognitionUsage == nil || event.RecognitionUsage.AudioDuration != 0.001) {
			t.Fatalf("recognition usage = %#v, want 0.001s", event.RecognitionUsage)
		}
	}
}

func TestSLNGSTTUnexpectedNormalCloseReturnsReferenceError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
			t.Errorf("write close: %v", err)
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]

	provider := NewSTT("test-key", WithSTTEndpoint(endpoint))
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("Next() event = %#v, want nil on provider close", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next() error = %v, want reference provider status error", err)
	}
	if statusErr.StatusCode != websocket.CloseNormalClosure {
		t.Fatalf("StatusCode = %d, want normal close code", statusErr.StatusCode)
	}
}

func TestSLNGSTTStreamEmptyFinalEmitsReferenceUsage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	binaryReceived := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		for {
			msgType, _, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read audio payload: %v", err)
				return
			}
			if msgType != websocket.BinaryMessage {
				continue
			}
			binaryReceived <- struct{}{}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"final_transcript","transcript":"","confidence":0,"language":"en"}`)); err != nil {
				t.Errorf("write empty final: %v", err)
			}
			<-r.Context().Done()
			return
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]

	provider := NewSTT(
		"test-key",
		WithSTTEndpoint(endpoint),
		WithSTTBufferSizeSeconds(0.001),
	)
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 32),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 16,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	select {
	case <-binaryReceived:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SLNG STT binary audio")
	}

	event := nextSLNGTestSpeechEvent(t, stream)
	if event.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("event type = %s, want recognition_usage", event.Type)
	}
	if event.RecognitionUsage == nil {
		t.Fatal("RecognitionUsage = nil")
	}
	if event.RecognitionUsage.AudioDuration != 0.001 {
		t.Fatalf("AudioDuration = %v, want 0.001", event.RecognitionUsage.AudioDuration)
	}
}

func nextSLNGTestSpeechEvent(t *testing.T, stream stt.RecognizeStream) *stt.SpeechEvent {
	t.Helper()
	type result struct {
		event *stt.SpeechEvent
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		event, err := stream.Next()
		ch <- result{event: event, err: err}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("Next() error = %v", got.err)
		}
		if got.event == nil {
			t.Fatal("Next() event = nil")
		}
		return got.event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SLNG STT event")
		return nil
	}
}

type slngSingleConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newSLNGSingleConnListener(conn net.Conn) *slngSingleConnListener {
	return &slngSingleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *slngSingleConnListener) Accept() (net.Conn, error) {
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

func (l *slngSingleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *slngSingleConnListener) Addr() net.Addr {
	return slngTestAddr("slng.test:443")
}

type slngTestAddr string

func (a slngTestAddr) Network() string { return "tcp" }

func (a slngTestAddr) String() string { return string(a) }

func newSLNGInMemoryWebsocketEndpoints(t *testing.T, handlers ...http.Handler) []string {
	t.Helper()
	oldDialer := websocket.DefaultDialer
	handlerByHost := make(map[string]http.Handler, len(handlers))
	endpoints := make([]string, 0, len(handlers))
	for i, handler := range handlers {
		host := fmt.Sprintf("slng-test-%d.local", i)
		handlerByHost[host] = handler
		endpoints = append(endpoints, "ws://"+host)
	}

	var mu sync.Mutex
	var cleanup []func()
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_ = ctx
			_ = network
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				host = address
			}
			handler, ok := handlerByHost[host]
			if !ok {
				return nil, fmt.Errorf("no in-memory SLNG websocket endpoint for %s", address)
			}
			clientConn, serverConn := net.Pipe()
			listener := newSLNGSingleConnListener(serverConn)
			server := &http.Server{Handler: handler}
			serverErr := make(chan error, 1)
			go func() {
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
					serverErr <- err
				}
			}()
			mu.Lock()
			cleanup = append(cleanup, func() {
				_ = server.Close()
				_ = listener.Close()
				_ = clientConn.Close()
				_ = serverConn.Close()
				select {
				case err := <-serverErr:
					t.Errorf("in-memory SLNG websocket server error: %v", err)
				default:
				}
			})
			mu.Unlock()
			return clientConn, nil
		},
		Proxy: nil,
	}

	t.Cleanup(func() {
		websocket.DefaultDialer = oldDialer
		mu.Lock()
		defer mu.Unlock()
		for _, cleanupFn := range cleanup {
			cleanupFn()
		}
	})
	return endpoints
}

func TestSLNGSTTStreamFlushSkipsMisalignedAudio(t *testing.T) {
	binaryLengths := make(chan int, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		for {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if msgType == websocket.BinaryMessage {
				binaryLengths <- len(payload)
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]

	provider := NewSTT("test-key", WithSTTEndpoint(endpoint))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01},
		SampleRate:        defaultSLNGSTTSampleRate,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-binaryLengths:
		t.Fatalf("sent misaligned %d-byte audio chunk", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSLNGSTTEndInputSendsReferenceFinalize(t *testing.T) {
	textFrames := make(chan string, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType == websocket.TextMessage {
				textFrames <- string(payload)
				return
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"

	provider := NewSTT(
		"test-key",
		WithSTTEndpoint(endpoint),
		WithSTTBufferSizeSeconds(0.001),
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 32),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 16,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	select {
	case got := <-textFrames:
		if got != `{"type":"finalize"}` {
			t.Fatalf("EndInput frame = %s, want reference finalize", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for STT finalize frame")
	}
}

func TestSLNGSTTEndInputReconnectsAfterUpdateOptions(t *testing.T) {
	var connections atomic.Int32
	secondFrames := make(chan string, 2)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		connection := connections.Add(1)
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if connection != 2 {
				continue
			}
			if messageType == websocket.BinaryMessage {
				secondFrames <- "binary"
			} else if messageType == websocket.TextMessage {
				secondFrames <- string(payload)
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"
	provider := NewSTT("test-key", WithSTTEndpoint(endpoint), WithSTTBufferSizeSeconds(0.01))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushFrame(&model.AudioFrame{Data: make([]byte, 32)}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	provider.UpdateOptions(WithSTTLanguage("id"))
	if err := stream.(stt.InputEnding).EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if got := <-secondFrames; got != "binary" {
		t.Fatalf("first replacement frame = %q, want buffered binary audio", got)
	}
	if got := <-secondFrames; got != `{"type":"finalize"}` {
		t.Fatalf("second replacement frame = %q, want finalize", got)
	}
}

func TestSLNGSTTFlushReconnectsAfterUpdateOptions(t *testing.T) {
	var connections atomic.Int32
	replacementAudio := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		connection := connections.Add(1)
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for {
			messageType, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if connection == 2 && messageType == websocket.BinaryMessage {
				replacementAudio <- struct{}{}
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"
	provider := NewSTT("test-key", WithSTTEndpoint(endpoint), WithSTTBufferSizeSeconds(0.01))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushFrame(&model.AudioFrame{Data: make([]byte, 32)}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	provider.UpdateOptions(WithSTTLanguage("id"))
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case <-replacementAudio:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replacement audio")
	}
}

func TestSLNGSTTUpdateOptionsSkipsEndedInput(t *testing.T) {
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"
	provider := NewSTT("test-key", WithSTTEndpoint(endpoint))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.(stt.InputEnding).EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	provider.UpdateOptions(WithSTTLanguage("id"))
	concrete := stream.(*sttStream)
	concrete.mu.Lock()
	reconnectRequested := concrete.reconnectRequested
	concrete.mu.Unlock()
	if reconnectRequested {
		t.Fatal("UpdateOptions requested reconnect after input ended")
	}
}

func TestSLNGSTTLegacyEndInputSendsFlush(t *testing.T) {
	textFrames := make(chan string, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType == websocket.TextMessage {
				textFrames <- string(payload)
				return
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/stt/deepgram/nova:3"
	stream, err := NewSTT(
		"test-key",
		WithSTTEndpoint(endpoint),
		WithSTTBufferSizeSeconds(0.001),
	).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushFrame(&model.AudioFrame{Data: make([]byte, 32)}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if err := stream.(stt.InputEnding).EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	select {
	case got := <-textFrames:
		if got != `{"type":"flush"}` {
			t.Fatalf("legacy EndInput frame = %s, want flush", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for legacy flush")
	}
}

func TestSLNGSTTFinalTimeoutReturnsTypedTimeoutError(t *testing.T) {
	finalizeSeen := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType == websocket.TextMessage && string(payload) == `{"type":"finalize"}` {
				finalizeSeen <- struct{}{}
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"
	stream, err := NewSTT(
		"test-key",
		WithSTTEndpoint(endpoint),
		WithSTTBufferSizeSeconds(0.001),
		WithSTTFinalTimeout(20*time.Millisecond),
	).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 32),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 16,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if err := stream.(stt.InputEnding).EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	select {
	case <-finalizeSeen:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for finalize frame")
	}

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("Next() event = %#v, want nil", event)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next() error = %T %v, want APITimeoutError", err, err)
	}
	concrete := stream.(*sttStream)
	if !concrete.isClosed() {
		t.Fatal("stream remains open after final timeout")
	}
	provider := concrete.provider
	provider.mu.Lock()
	activeStreams := len(provider.streams)
	provider.mu.Unlock()
	if activeStreams != 0 {
		t.Fatalf("active streams after final timeout = %d, want 0", activeStreams)
	}
	select {
	case <-concrete.lifecycleDone:
	default:
		t.Fatal("lifecycle goroutine remains active after final timeout")
	}
}

func TestSLNGSTTFinalAtTimeoutBoundaryKeepsPendingFinal(t *testing.T) {
	oldAfterFunc := slngSTTAfterFunc
	callbacks := make(chan func(), 1)
	var timer *time.Timer
	slngSTTAfterFunc = func(_ time.Duration, callback func()) *time.Timer {
		callbacks <- callback
		timer = time.AfterFunc(time.Hour, func() {})
		return timer
	}
	t.Cleanup(func() {
		slngSTTAfterFunc = oldAfterFunc
		if timer != nil {
			timer.Stop()
		}
	})

	provider := NewSTT("test-key")
	stream := &sttStream{
		provider:      provider,
		inputEnded:    true,
		finalTimeout:  time.Second,
		lifecycleDone: make(chan struct{}),
	}
	provider.registerStream(stream)
	defer stream.Close()

	stream.mu.Lock()
	stream.startFinalTimerLocked()
	callback := <-callbacks
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		callback()
		close(done)
	}()
	<-started
	stream.stopFinalTimerLocked()
	stream.pendingEvents = append(stream.pendingEvents, &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Language: "en",
			Text:     "boundary final",
		}},
	})
	stream.mu.Unlock()
	<-done

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want pending final", err)
	}
	if event == nil || event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("Next() event = %#v, want final transcript", event)
	}
	if stream.isClosed() {
		t.Fatal("timeout callback closed stream after final was accepted")
	}
	provider.mu.Lock()
	activeStreams := len(provider.streams)
	provider.mu.Unlock()
	if activeStreams != 1 {
		t.Fatalf("active streams after boundary final = %d, want 1", activeStreams)
	}
}

func TestSLNGSTTSendsKeepaliveWhileInputOpen(t *testing.T) {
	oldInterval := slngSTTKeepaliveInterval
	slngSTTKeepaliveInterval = 10 * time.Millisecond
	t.Cleanup(func() { slngSTTKeepaliveInterval = oldInterval })

	textFrames := make(chan string, 2)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType == websocket.TextMessage {
				textFrames <- string(payload)
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"

	stream, err := NewSTT("test-key", WithSTTEndpoint(endpoint)).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	select {
	case got := <-textFrames:
		if got != `{"type":"keepalive"}` {
			t.Fatalf("idle frame = %s, want keepalive", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for STT keepalive")
	}
}

func TestSLNGSTTStopsKeepaliveAfterEndInput(t *testing.T) {
	oldInterval := slngSTTKeepaliveInterval
	slngSTTKeepaliveInterval = 10 * time.Millisecond
	t.Cleanup(func() { slngSTTKeepaliveInterval = oldInterval })

	textFrames := make(chan string, 8)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType == websocket.TextMessage {
				textFrames <- string(payload)
			}
		}
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"
	stream, err := NewSTT(
		"test-key",
		WithSTTEndpoint(endpoint),
		WithSTTBufferSizeSeconds(0.001),
	).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	select {
	case got := <-textFrames:
		if got != `{"type":"keepalive"}` {
			t.Fatalf("idle frame = %s, want keepalive", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for first STT keepalive")
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 32),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 16,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if err := stream.(stt.InputEnding).EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	for {
		select {
		case frame := <-textFrames:
			if frame == `{"type":"finalize"}` {
				goto finalized
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timed out waiting for finalize frame")
		}
	}

finalized:
	select {
	case got := <-textFrames:
		t.Fatalf("frame after EndInput = %s, want keepalive stopped", got)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestSLNGSTTBoundsSilentReconnects(t *testing.T) {
	var connections atomic.Int32
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		connections.Add(1)
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"
	stream, err := NewSTT("test-key", WithSTTEndpoint(endpoint)).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("Next() event = %#v, want nil", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next() error = %T %v, want APIStatusError", err, err)
	}
	if got := connections.Load(); got != 3 {
		t.Fatalf("websocket connections = %d, want bounded 3", got)
	}
}

func TestSLNGSTTCancellationStopsReconnect(t *testing.T) {
	var connections atomic.Int32
	connected := make(chan struct{}, 1)
	release := make(chan struct{})
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		connections.Add(1)
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		connected <- struct{}{}
		<-release
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0] + "/v1/bridges/unmute/stt/deepgram/nova:3"
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := NewSTT("test-key", WithSTTEndpoint(endpoint)).Stream(ctx, "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	<-connected
	cancel()
	close(release)

	if _, err := stream.Next(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		t.Fatalf("Next() error = %T %v, want EOF or cancellation", err, err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := connections.Load(); got != 1 {
		t.Fatalf("websocket connections after cancellation = %d, want 1", got)
	}
}

func TestSLNGSTTStreamClosesAfterAudioWriteFailure(t *testing.T) {
	closed := make(chan struct{})
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		close(closed)
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]

	provider := NewSTT(
		"test-key",
		WithSTTEndpoint(endpoint+"/v1/stt/deepgram/nova:3"),
		WithSTTBufferSizeSeconds(0.001),
	)
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}

	var writeErr error
	for range 3 {
		writeErr = stream.PushFrame(&model.AudioFrame{
			Data:              make([]byte, 32),
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 16,
		})
		if writeErr != nil {
			break
		}
	}
	if writeErr == nil {
		t.Fatal("PushFrame error = nil after closed websocket, want write failure")
	}

	err = stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 32),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 16,
	})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second PushFrame error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close after write failure error = %v", err)
	}
}

func TestSLNGSTTProviderCloseClosesActiveStreams(t *testing.T) {
	provider := NewSTT("test-key")
	stream := &sttStream{}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01, 0x02}}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestSLNGSTTStreamAfterCloseIsRejected(t *testing.T) {
	provider := NewSTT("test-key", WithSTTEndpoint("ws://slng.test/v1/stt/deepgram/nova:3"))
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	oldDialer := websocket.DefaultDialer
	dials := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("unexpected slng stt dial")
		},
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	stream, err := provider.Stream(context.Background(), "en")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
	if stream != nil {
		t.Fatalf("Stream after Close stream = %#v, want nil", stream)
	}
	if dials != 0 {
		t.Fatalf("Stream after Close dialed %d times, want none", dials)
	}
}

func TestSLNGSTTClosedStreamNextReturnsEOF(t *testing.T) {
	provider := NewSTT("test-key")
	stream := &sttStream{}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after provider Close error = %T %v, want io.EOF", err, err)
	}
}

func TestSLNGTTSProviderCloseClosesActiveStreams(t *testing.T) {
	provider := NewTTS("test-key")
	stream := &ttsStream{}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestSLNGTTSStreamAfterCloseIsRejected(t *testing.T) {
	provider := NewTTS("test-key", WithTTSEndpoint("ws://slng.test/v1/tts/elevenlabs"))
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	oldDialer := websocket.DefaultDialer
	dials := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("unexpected slng tts dial")
		},
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	stream, err := provider.Stream(context.Background())
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
	if stream != nil {
		t.Fatalf("Stream after Close stream = %#v, want nil", stream)
	}
	if dials != 0 {
		t.Fatalf("Stream after Close dialed %d times, want none", dials)
	}
}

func TestSLNGTTSClosedStreamNextReturnsEOF(t *testing.T) {
	provider := NewTTS("test-key")
	stream := &ttsStream{}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after provider Close error = %T %v, want io.EOF", err, err)
	}
}

func TestSLNGSTTStreamFallsBackToNextModelEndpoint(t *testing.T) {
	initPayloads := make(chan map[string]any, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode init payload: %v", err)
			return
		}
		initPayloads <- init
	})
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, handler)[0]

	provider := NewSTT("test-key", WithSTTModelEndpoints(
		"ws://127.0.0.1:1/v1/stt/deepgram/failing",
		endpoint+"/v1/stt/deepgram/nova:3",
	))
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case init := <-initPayloads:
		if got, want := init["model"], "nova-3"; got != want {
			t.Fatalf("init.model = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fallback SLNG STT init payload")
	}
}

func TestSLNGSTTStreamStartsAtRememberedFallbackEndpoint(t *testing.T) {
	failedEndpointHits := make(chan struct{}, 2)
	failedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failedEndpointHits <- struct{}{}
		http.Error(w, "first endpoint unavailable", http.StatusServiceUnavailable)
	})
	var successHits atomic.Int32
	initPayloads := make(chan map[string]any, 2)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		successHits.Add(1)

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode init payload: %v", err)
			return
		}
		initPayloads <- init
	})
	endpoints := newSLNGInMemoryWebsocketEndpoints(t, failedHandler, handler)

	provider := NewSTT("test-key", WithSTTModelEndpoints(
		endpoints[0]+"/v1/stt/deepgram/failing",
		endpoints[1]+"/v1/stt/deepgram/nova:3",
	))
	first, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("first Stream() error = %v", err)
	}
	defer first.Close()
	select {
	case <-failedEndpointHits:
	case <-time.After(time.Second):
		t.Fatal("first stream did not try the first endpoint")
	}
	select {
	case init := <-initPayloads:
		if got, want := init["model"], "nova-3"; got != want {
			t.Fatalf("first init.model = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first fallback init payload")
	}

	second, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("second Stream() error = %v", err)
	}
	defer second.Close()
	select {
	case <-failedEndpointHits:
		t.Fatal("second stream retried failed first endpoint after successful failover")
	default:
	}
	select {
	case init := <-initPayloads:
		if got, want := init["model"], "nova-3"; got != want {
			t.Fatalf("second init.model = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second fallback init payload")
	}
	if got, want := successHits.Load(), int32(2); got != want {
		t.Fatalf("success endpoint hits = %d, want %d", got, want)
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

func assertSLNGNestedFieldAbsent(t *testing.T, payload []byte, parent, key string) {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	parentMap, _ := data[parent].(map[string]any)
	if parentMap == nil {
		t.Fatalf("%s = %#v, want object in %s", parent, data[parent], string(payload))
	}
	if _, ok := parentMap[key]; ok {
		t.Fatalf("%s.%s present in %s", parent, key, string(payload))
	}
}

func assertSLNGNestedArrayField(t *testing.T, payload []byte, parent, key string, want []any) {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	parentMap, _ := data[parent].(map[string]any)
	if parentMap == nil {
		t.Fatalf("%s = %#v, want object in %s", parent, data[parent], string(payload))
	}
	got, ok := parentMap[key].([]any)
	if !ok {
		t.Fatalf("%s.%s = %#v, want array in %s", parent, key, parentMap[key], string(payload))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s.%s = %#v, want %#v in %s", parent, key, got, want, string(payload))
	}
}

func TestBuildSLNGHeaders(t *testing.T) {
	provider := NewSTT("test-key", WithSTTRegionOverride("us-east"))
	headers, err := buildSTTWebsocketHeaders(provider)
	if err != nil {
		t.Fatal(err)
	}
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
