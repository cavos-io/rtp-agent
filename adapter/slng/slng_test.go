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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
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

func TestSLNGSTTInitPayloadPreservesExplicitZeroVADSilence(t *testing.T) {
	provider := NewSTT("test-key", WithSTTVADMinSilenceDurationMS(0))

	payload := buildSTTInitPayload(provider)

	assertSLNGNestedField(t, payload, "config", "vad_min_silence_duration_ms", float64(0))
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
	if audio != nil || !done {
		t.Fatalf("ttsAudioFromMessage(isFinal invalid audio) audio=%+v done=%v, want final marker", audio, done)
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
