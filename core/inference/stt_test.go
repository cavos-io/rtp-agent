package inference

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestNewSTTUsesReferenceCredentialEnvFallback(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "base-key")
	t.Setenv("LIVEKIT_API_SECRET", "base-secret")
	t.Setenv("LIVEKIT_INFERENCE_API_KEY", "inference-key")
	t.Setenv("LIVEKIT_INFERENCE_API_SECRET", "inference-secret")

	provider := NewSTT("deepgram/nova-3", "", "")

	if provider.apiKey != "inference-key" {
		t.Fatalf("apiKey = %q, want inference-key", provider.apiKey)
	}
	if provider.apiSecret != "inference-secret" {
		t.Fatalf("apiSecret = %q, want inference-secret", provider.apiSecret)
	}
}

func TestInferenceSTTCapabilitiesReportReferenceWordAlignment(t *testing.T) {
	provider := NewSTT("deepgram/nova-3", "key", "secret")

	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
	}
}

func TestInferenceSTTCapabilitiesUseReferenceDefaultDiarization(t *testing.T) {
	provider := NewSTT("deepgram/nova-3", "key", "secret")

	if provider.Capabilities().Diarization {
		t.Fatal("Diarization = true, want false by default")
	}
}

func TestInferenceSTTExtraKwargsMatchReferenceSessionCreate(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{}
	provider := NewSTT("deepgram/nova-3", "key", "secret", WithSTTExtraKwargs(map[string]any{
		"diarize":  true,
		"keyterms": []string{"livekit"},
	}))
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	if !provider.Capabilities().Diarization {
		t.Fatal("Diarization = false, want true when reference diarize extra kwarg is truthy")
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if len(conn.writes) != 1 {
		t.Fatalf("writes = %d, want session.create", len(conn.writes))
	}
	settings, ok := conn.writes[0]["settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("settings = %#v, want map", conn.writes[0]["settings"])
	}
	extra, ok := settings["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("settings.extra = %#v, want map", settings["extra"])
	}
	if got := extra["diarize"]; got != true {
		t.Fatalf("settings.extra.diarize = %#v, want true", got)
	}
	keyterms, ok := extra["keyterms"].([]string)
	if !ok || len(keyterms) != 1 || keyterms[0] != "livekit" {
		t.Fatalf("settings.extra.keyterms = %#v, want [livekit]", extra["keyterms"])
	}
}

func TestInferenceSTTSessionCreateUsesReferenceAudioOptions(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{}
	provider := NewSTT("deepgram/nova-3", "key", "secret",
		WithSTTSampleRate(8000),
		WithSTTEncoding("mulaw"),
	)
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if len(conn.writes) != 1 {
		t.Fatalf("writes = %d, want session.create", len(conn.writes))
	}
	settings, ok := conn.writes[0]["settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("settings = %#v, want map", conn.writes[0]["settings"])
	}
	if settings["sample_rate"] != "8000" {
		t.Fatalf("settings.sample_rate = %#v, want string 8000", settings["sample_rate"])
	}
	if settings["encoding"] != "mulaw" {
		t.Fatalf("settings.encoding = %#v, want mulaw", settings["encoding"])
	}
}

func TestInferenceSTTSessionCreateUsesReferenceConnectOptions(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{}
	provider := NewSTT("deepgram/nova-3", "key", "secret",
		WithSTTConnectOptions(APIConnectOptions{
			Timeout:  1500 * time.Millisecond,
			MaxRetry: 2,
		}),
	)
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if len(conn.writes) != 1 {
		t.Fatalf("writes = %d, want session.create", len(conn.writes))
	}
	connection, ok := conn.writes[0]["connection"].(map[string]interface{})
	if !ok {
		t.Fatalf("connection = %#v, want map", conn.writes[0]["connection"])
	}
	if connection["timeout"] != 1.5 {
		t.Fatalf("connection.timeout = %#v, want 1.5", connection["timeout"])
	}
	if connection["retries"] != 2 {
		t.Fatalf("connection.retries = %#v, want 2", connection["retries"])
	}
}

func TestInferenceSTTRecognizeMatchesReferenceUnsupportedBatch(t *testing.T) {
	provider := NewSTT("deepgram/nova-3", "key", "secret")

	_, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatal("Recognize() error = nil, want unsupported batch recognition error")
	}
	if got, want := err.Error(), "LiveKit Inference STT does not support batch recognition, use stream() instead"; got != want {
		t.Fatalf("Recognize() error = %q, want %q", got, want)
	}
}

func TestInferenceSTTReportsReferenceModelProviderMetadata(t *testing.T) {
	provider := NewSTT("deepgram/nova-3", "key", "secret")

	if got := stt.Model(provider); got != "deepgram/nova-3" {
		t.Fatalf("Model = %q, want configured reference model", got)
	}
	if got := stt.Provider(provider); got != "livekit" {
		t.Fatalf("Provider = %q, want livekit", got)
	}
}

func TestNewSTTParsesReferenceModelStringForMetadata(t *testing.T) {
	provider := NewSTT("deepgram/nova-3:en", "key", "secret")

	if got := stt.Model(provider); got != "deepgram/nova-3" {
		t.Fatalf("Model = %q, want model string language suffix stripped", got)
	}
}

func TestNewSTTPreservesReferenceModelStringLanguageForStream(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{}
	provider := NewSTT("deepgram/nova-3:en", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if len(conn.writes) != 1 {
		t.Fatalf("writes = %d, want session.create", len(conn.writes))
	}
	settings, ok := conn.writes[0]["settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("settings = %#v, want map", conn.writes[0]["settings"])
	}
	if settings["language"] != "en" {
		t.Fatalf("settings.language = %#v, want parsed model string language", settings["language"])
	}
}

func TestInferenceSTTUpdateOptionsMatchReferenceFutureStreams(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{}
	provider := NewSTT("deepgram/nova-3", "key", "secret", WithSTTExtraKwargs(map[string]any{
		"keyterms": []string{"existing"},
	}))
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	provider.UpdateOptions(
		WithSTTModel("assemblyai/universal-streaming:id"),
		WithSTTLanguage("fr"),
		WithSTTExtraKwargs(map[string]any{"speaker_labels": true}),
	)

	if got := stt.Model(provider); got != "assemblyai/universal-streaming" {
		t.Fatalf("Model = %q, want updated model without language suffix", got)
	}
	if !provider.Capabilities().Diarization {
		t.Fatal("Diarization = false, want true after update extra enables speaker_labels")
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if len(conn.writes) != 1 {
		t.Fatalf("writes = %d, want session.create", len(conn.writes))
	}
	if conn.writes[0]["model"] != "assemblyai/universal-streaming" {
		t.Fatalf("session.create model = %#v, want assemblyai/universal-streaming", conn.writes[0]["model"])
	}
	settings, ok := conn.writes[0]["settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("settings = %#v, want map", conn.writes[0]["settings"])
	}
	if settings["language"] != "fr" {
		t.Fatalf("settings.language = %#v, want explicit update language", settings["language"])
	}
	extra, ok := settings["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("settings.extra = %#v, want map", settings["extra"])
	}
	if _, ok := extra["keyterms"]; !ok {
		t.Fatalf("settings.extra = %#v, want existing extra retained", extra)
	}
	if extra["speaker_labels"] != true {
		t.Fatalf("settings.extra.speaker_labels = %#v, want true", extra["speaker_labels"])
	}
}

func TestInferenceSTTUpdateOptionsSendsReferenceSessionUpdateToActiveStream(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{readBlock: make(chan struct{})}
	provider := NewSTT("deepgram/nova-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	provider.UpdateOptions(
		WithSTTModel("assemblyai/universal-streaming"),
		WithSTTLanguage("id"),
		WithSTTExtraKwargs(map[string]any{"speaker_labels": true}),
	)

	if len(conn.writes) != 2 {
		t.Fatalf("writes = %d, want session.create and session.update", len(conn.writes))
	}
	update := conn.writes[1]
	if update["type"] != "session.update" {
		t.Fatalf("second write type = %#v, want session.update", update["type"])
	}
	settings, ok := update["settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("session.update settings = %#v, want map", update["settings"])
	}
	if settings["model"] != "assemblyai/universal-streaming" {
		t.Fatalf("settings.model = %#v, want assemblyai/universal-streaming", settings["model"])
	}
	if settings["language"] != "id" {
		t.Fatalf("settings.language = %#v, want id", settings["language"])
	}
	extra, ok := settings["extra"].(map[string]any)
	if !ok {
		t.Fatalf("settings.extra = %#v, want map", settings["extra"])
	}
	if extra["speaker_labels"] != true {
		t.Fatalf("settings.extra.speaker_labels = %#v, want true", extra["speaker_labels"])
	}
}

func TestSTTWebsocketSendsReferenceInferenceHeaders(t *testing.T) {
	var captured http.Header
	provider := NewSTT("deepgram/nova-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		captured = header.Clone()
		return &fakeInferenceWebsocketConn{}, nil
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if !strings.HasPrefix(captured.Get("User-Agent"), "LiveKit Agents/") {
		t.Fatalf("User-Agent = %q, want LiveKit Agents version prefix", captured.Get("User-Agent"))
	}
	if !strings.Contains(captured.Get("User-Agent"), " (go ") {
		t.Fatalf("User-Agent = %q, want Go runtime marker", captured.Get("User-Agent"))
	}
}

func TestSTTWebsocketSendsReferenceContextHeaders(t *testing.T) {
	restore := SetContextHeadersProvider(func() map[string]string {
		return map[string]string{
			HeaderRoomID: "RM_stt",
			HeaderJobID:  "job_stt",
		}
	})
	defer restore()

	var captured http.Header
	provider := NewSTT("deepgram/nova-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		captured = header.Clone()
		return &fakeInferenceWebsocketConn{}, nil
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if got := captured.Get(HeaderRoomID); got != "RM_stt" {
		t.Fatalf("%s = %q, want RM_stt", HeaderRoomID, got)
	}
	if got := captured.Get(HeaderJobID); got != "job_stt" {
		t.Fatalf("%s = %q, want job_stt", HeaderJobID, got)
	}
}

func TestInferenceSTTSessionCreateParamsMatchReferenceShape(t *testing.T) {
	modelName, params := sttSessionCreateParams("auto:en", "", "", 0, nil, nil, nil)

	if modelName != "auto" {
		t.Fatalf("modelName = %q, want auto", modelName)
	}
	if _, ok := params["model"]; ok {
		t.Fatalf("session.create model = %v, want omitted for auto", params["model"])
	}
	settings, ok := params["settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("settings = %#v, want map", params["settings"])
	}
	if settings["language"] != "en" {
		t.Fatalf("settings.language = %v, want en", settings["language"])
	}
	if extra, ok := settings["extra"].(map[string]interface{}); !ok || len(extra) != 0 {
		t.Fatalf("settings.extra = %#v, want empty map", settings["extra"])
	}
}

func TestInferenceSTTFallbackModelsMatchReferenceSessionCreate(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{}
	provider := NewSTT("deepgram/nova-3", "key", "secret", WithSTTFallbackModels(
		FallbackModel{
			Model:       "assemblyai/universal-streaming",
			ExtraKwargs: map[string]any{"format_turns": true},
		},
		FallbackModel{Model: "cartesia/ink-whisper"},
	))
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if len(conn.writes) != 1 {
		t.Fatalf("writes = %d, want session.create", len(conn.writes))
	}
	fallback, ok := conn.writes[0]["fallback"].(map[string]interface{})
	if !ok {
		t.Fatalf("fallback = %#v, want map", conn.writes[0]["fallback"])
	}
	models, ok := fallback["models"].([]map[string]interface{})
	if !ok {
		t.Fatalf("fallback.models = %#v, want model maps", fallback["models"])
	}
	if len(models) != 2 {
		t.Fatalf("fallback models = %#v, want 2 entries", models)
	}
	if models[0]["model"] != "assemblyai/universal-streaming" {
		t.Fatalf("first fallback model = %#v, want assemblyai/universal-streaming", models[0])
	}
	extra, ok := models[0]["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("first fallback extra = %#v, want map", models[0]["extra"])
	}
	if got := extra["format_turns"]; got != true {
		t.Fatalf("first fallback extra format_turns = %#v, want true", got)
	}
	extra, ok = models[1]["extra"].(map[string]interface{})
	if !ok || len(extra) != 0 {
		t.Fatalf("second fallback extra = %#v, want empty map", models[1]["extra"])
	}
}

func TestInferenceSTTFallbackModelStringOmitsReferenceLanguage(t *testing.T) {
	_, params := sttSessionCreateParams("deepgram/nova-3", "", "", 0, nil, []FallbackModel{
		{Model: "cartesia/ink-whisper:es"},
	}, nil)

	fallback, ok := params["fallback"].(map[string]interface{})
	if !ok {
		t.Fatalf("fallback = %#v, want map", params["fallback"])
	}
	models, ok := fallback["models"].([]map[string]interface{})
	if !ok {
		t.Fatalf("fallback.models = %#v, want model maps", fallback["models"])
	}
	if len(models) != 1 {
		t.Fatalf("fallback models = %#v, want 1 entry", models)
	}
	if models[0]["model"] != "cartesia/ink-whisper" {
		t.Fatalf("fallback model = %#v, want cartesia/ink-whisper", models[0])
	}
}

func TestInferenceSTTFinalTranscriptEmitsStructuredRecognitionUsage(t *testing.T) {
	stream := &inferenceSTTStream{
		eventCh:       make(chan *stt.SpeechEvent, 4),
		audioDuration: 1.5,
	}

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-1",
		"transcript": "hello",
		"language":   "en",
		"start":      2.0,
		"duration":   0.7,
	}, true)

	start := <-stream.eventCh
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event type = %s, want start_of_speech", start.Type)
	}

	usage := <-stream.eventCh
	if usage.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("second event type = %s, want recognition_usage", usage.Type)
	}
	if usage.RecognitionUsage == nil {
		t.Fatal("RecognitionUsage = nil, want structured usage data")
	}
	if usage.RecognitionUsage.AudioDuration != 1.5 {
		t.Fatalf("AudioDuration = %v, want 1.5", usage.RecognitionUsage.AudioDuration)
	}
	if len(usage.Alternatives) != 0 {
		t.Fatalf("usage alternatives = %#v, want none", usage.Alternatives)
	}

	final := <-stream.eventCh
	if final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("third event type = %s, want final_transcript", final.Type)
	}
	if final.Alternatives[0].StartTime != 2.0 || final.Alternatives[0].EndTime != 2.7 {
		t.Fatalf("final timing = (%v, %v), want (2.0, 2.7)", final.Alternatives[0].StartTime, final.Alternatives[0].EndTime)
	}
	if stream.audioDuration != 0 {
		t.Fatalf("audioDuration = %v, want reset to 0", stream.audioDuration)
	}
}

func TestInferenceSTTFinalTranscriptOmitsZeroDurationUsage(t *testing.T) {
	stream := &inferenceSTTStream{
		eventCh: make(chan *stt.SpeechEvent, 4),
	}

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-1",
		"transcript": "hello",
		"language":   "en",
		"start":      2.0,
		"duration":   0.7,
	}, true)

	start := <-stream.eventCh
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event type = %s, want start_of_speech", start.Type)
	}

	final := <-stream.eventCh
	if final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("second event type = %s, want final_transcript", final.Type)
	}

	end := <-stream.eventCh
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("third event type = %s, want end_of_speech", end.Type)
	}
}

func TestInferenceSTTLifecycleEventsOmitRequestID(t *testing.T) {
	stream := &inferenceSTTStream{
		eventCh: make(chan *stt.SpeechEvent, 3),
	}

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-1",
		"transcript": "hello",
		"language":   "en",
	}, true)

	start := <-stream.eventCh
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event type = %s, want start_of_speech", start.Type)
	}
	if start.RequestID != "" {
		t.Fatalf("start RequestID = %q, want empty", start.RequestID)
	}

	final := <-stream.eventCh
	if final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("second event type = %s, want final_transcript", final.Type)
	}
	if final.RequestID != "req-1" {
		t.Fatalf("final RequestID = %q, want req-1", final.RequestID)
	}

	end := <-stream.eventCh
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("third event type = %s, want end_of_speech", end.Type)
	}
	if end.RequestID != "" {
		t.Fatalf("end RequestID = %q, want empty", end.RequestID)
	}
}

func TestInferenceSTTTranscriptPreservesWordsAndMetadata(t *testing.T) {
	stream := &inferenceSTTStream{
		eventCh: make(chan *stt.SpeechEvent, 3),
	}

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-1",
		"transcript": "hello world",
		"language":   "en",
		"confidence": 0.9,
		"start":      1.0,
		"duration":   0.8,
		"speaker_id": "speaker-a",
		"extra":      map[string]interface{}{"provider": "livekit"},
		"words": []interface{}{
			map[string]interface{}{
				"word":       "hello",
				"start":      1.0,
				"end":        1.3,
				"confidence": 0.91,
				"speaker_id": "speaker-a",
			},
			map[string]interface{}{
				"word":       "world",
				"start":      1.4,
				"end":        1.8,
				"confidence": 0.92,
				"speaker_id": "speaker-a",
			},
		},
	}, false)

	<-stream.eventCh
	interim := <-stream.eventCh
	if interim.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event type = %s, want interim_transcript", interim.Type)
	}
	if len(interim.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(interim.Alternatives))
	}
	data := interim.Alternatives[0]
	if data.SpeakerID != "speaker-a" {
		t.Fatalf("SpeakerID = %q, want speaker-a", data.SpeakerID)
	}
	if data.Metadata["provider"] != "livekit" {
		t.Fatalf("Metadata[provider] = %v, want livekit", data.Metadata["provider"])
	}
	if len(data.Words) != 2 {
		t.Fatalf("words = %#v, want 2 words", data.Words)
	}
	if data.Words[0].Text != "hello" || data.Words[0].StartTime != 1.0 || data.Words[0].EndTime != 1.3 {
		t.Fatalf("first word = %#v, want hello timing", data.Words[0])
	}
	if data.Words[1].Text != "world" || data.Words[1].Confidence != 0.92 || data.Words[1].SpeakerID != "speaker-a" {
		t.Fatalf("second word = %#v, want world metadata", data.Words[1])
	}
}

func TestInferenceSTTTranscriptUsesReferenceLanguageFallback(t *testing.T) {
	tests := []struct {
		name       string
		streamLang string
		dataLang   string
		want       string
	}{
		{
			name:       "gateway omitted language uses stream language",
			streamLang: "ja",
			want:       "ja",
		},
		{
			name: "gateway omitted language uses english default",
			want: "en",
		},
		{
			name:       "gateway language wins over stream language",
			streamLang: "ja",
			dataLang:   "fr",
			want:       "fr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &inferenceSTTStream{
				eventCh: make(chan *stt.SpeechEvent, 2),
			}
			if tt.streamLang != "" {
				stream.language = tt.streamLang
			}

			data := map[string]interface{}{
				"request_id": "req-1",
				"transcript": "hello",
			}
			if tt.dataLang != "" {
				data["language"] = tt.dataLang
			}

			stream.processTranscript(data, false)

			<-stream.eventCh
			interim := <-stream.eventCh
			if interim.Type != stt.SpeechEventInterimTranscript {
				t.Fatalf("event type = %s, want interim_transcript", interim.Type)
			}
			if got := interim.Alternatives[0].Language; got != tt.want {
				t.Fatalf("Language = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInferenceSTTTranscriptLanguageUsesStreamOptionSnapshot(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{readBlock: make(chan struct{})}
	provider := NewSTT("deepgram/nova-3", "key", "secret")
	provider.baseURL = "wss://inference.test/v1"
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	rawStream, err := provider.Stream(context.Background(), "ja")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	stream := rawStream.(*inferenceSTTStream)
	defer stream.Close()

	provider.UpdateOptions(WithSTTLanguage("id"))

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-1",
		"transcript": "hello",
	}, false)

	<-stream.eventCh
	interim := <-stream.eventCh
	if interim.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event type = %s, want interim_transcript", interim.Type)
	}
	if got := interim.Alternatives[0].Language; got != "ja" {
		t.Fatalf("Language = %q, want stream creation language ja", got)
	}
}

func TestInferenceSTTTranscriptUsesReferenceConfidenceDefault(t *testing.T) {
	stream := &inferenceSTTStream{
		eventCh: make(chan *stt.SpeechEvent, 2),
	}

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-1",
		"transcript": "hello",
	}, false)

	<-stream.eventCh
	interim := <-stream.eventCh
	if interim.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event type = %s, want interim_transcript", interim.Type)
	}
	if got := interim.Alternatives[0].Confidence; got != 1.0 {
		t.Fatalf("Confidence = %v, want 1.0", got)
	}
}

func TestInferenceSTTTranscriptUsesReferenceRequestIDFallback(t *testing.T) {
	stream := &inferenceSTTStream{
		requestID: "stt_request_test",
		eventCh:   make(chan *stt.SpeechEvent, 3),
	}

	stream.processTranscript(map[string]interface{}{
		"transcript": "hello",
	}, true)

	<-stream.eventCh
	final := <-stream.eventCh
	if final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %s, want final_transcript", final.Type)
	}
	if final.RequestID != "stt_request_test" {
		t.Fatalf("RequestID = %q, want fallback stream request id", final.RequestID)
	}
}

func TestInferenceSTTPreflightTranscriptRequiresActiveSpeech(t *testing.T) {
	stream := &inferenceSTTStream{
		eventCh: make(chan *stt.SpeechEvent, 3),
	}

	stream.processPreflightTranscript(map[string]interface{}{
		"request_id": "req-ignored",
		"transcript": "ignored",
	})

	select {
	case ev := <-stream.eventCh:
		t.Fatalf("unexpected preflight event before speech starts: %#v", ev)
	default:
	}

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-start",
		"transcript": "hello",
		"start":      1.0,
		"duration":   0.5,
	}, false)
	<-stream.eventCh
	<-stream.eventCh

	stream.processPreflightTranscript(map[string]interface{}{
		"request_id": "req-preflight",
		"transcript": "hello there",
		"language":   "en",
		"start":      1.2,
		"duration":   0.4,
	})

	ev := <-stream.eventCh
	if ev.Type != stt.SpeechEventPreflightTranscript {
		t.Fatalf("event type = %s, want preflight_transcript", ev.Type)
	}
	if ev.RequestID != "req-preflight" {
		t.Fatalf("RequestID = %q, want req-preflight", ev.RequestID)
	}
	if len(ev.Alternatives) != 1 || ev.Alternatives[0].Text != "hello there" {
		t.Fatalf("Alternatives = %#v, want preflight transcript text", ev.Alternatives)
	}
}

func TestInferenceSTTAppliesStartTimeOffsetToTranscriptAndWords(t *testing.T) {
	stream := &inferenceSTTStream{
		eventCh:         make(chan *stt.SpeechEvent, 3),
		startTimeOffset: 10.0,
	}

	stream.processTranscript(map[string]interface{}{
		"request_id": "req-1",
		"transcript": "hello",
		"start":      1.0,
		"duration":   0.5,
		"words": []interface{}{
			map[string]interface{}{
				"word":  "hello",
				"start": 1.0,
				"end":   1.5,
			},
		},
	}, false)

	<-stream.eventCh
	interim := <-stream.eventCh
	data := interim.Alternatives[0]
	if data.StartTime != 11.0 || data.EndTime != 11.5 {
		t.Fatalf("transcript timing = (%v, %v), want (11.0, 11.5)", data.StartTime, data.EndTime)
	}
	if len(data.Words) != 1 {
		t.Fatalf("words = %#v, want one word", data.Words)
	}
	if data.Words[0].StartTime != 11.0 || data.Words[0].EndTime != 11.5 {
		t.Fatalf("word timing = (%v, %v), want (11.0, 11.5)", data.Words[0].StartTime, data.Words[0].EndTime)
	}
	if data.Words[0].StartTimeOffset != 10.0 {
		t.Fatalf("word StartTimeOffset = %v, want 10.0", data.Words[0].StartTimeOffset)
	}
}

func TestInferenceSTTStreamRejectsMismatchedSampleRates(t *testing.T) {
	stream := &inferenceSTTStream{
		audioCh: make(chan *model.AudioFrame, 2),
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame(first) returned error: %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("second"), SampleRate: 8000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame(second) returned nil, want sample-rate mismatch error")
	}
	if got := len(stream.audioCh); got != 1 {
		t.Fatalf("audio frames forwarded = %d, want 1", got)
	}
}

func TestInferenceSTTStreamFlushDoesNotFinalize(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{}
	stream := &inferenceSTTStream{
		conn:    conn,
		audioCh: make(chan *model.AudioFrame, 2),
		eventCh: make(chan *stt.SpeechEvent, 1),
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if len(conn.writes) != 0 {
		t.Fatalf("writes = %#v, want no websocket finalize on flush", conn.writes)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("after"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame after Flush returned error: %v", err)
	}
}

func TestInferenceSTTStreamEndInputFinalizesAndRejectsMoreInput(t *testing.T) {
	var _ stt.InputEnding = (*inferenceSTTStream)(nil)

	conn := &fakeInferenceWebsocketConn{}

	stream := &inferenceSTTStream{
		conn:    conn,
		audioCh: make(chan *model.AudioFrame, 2),
		eventCh: make(chan *stt.SpeechEvent, 1),
	}

	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}

	if len(conn.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(conn.writes))
	}
	if conn.writes[0]["type"] != "session.finalize" {
		t.Fatalf("finalize message type = %v, want session.finalize", conn.writes[0]["type"])
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("late"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame after EndInput returned nil, want error")
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush after EndInput returned nil, want error")
	}
}

func TestInferenceSTTStreamErrorMessageReturnsNextError(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{
		readCh: make(chan []byte, 1),
	}
	conn.readCh <- []byte(`{"type":"error","message":"provider failed"}`)
	close(conn.readCh)
	provider := NewSTT("livekit/stt", "key", "secret")
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want gateway error")
	}
	if !strings.Contains(err.Error(), "LiveKit Inference STT returned error") {
		t.Fatalf("Next() error = %v, want gateway error", err)
	}
	if !strings.Contains(err.Error(), "provider failed") {
		t.Fatalf("Next() error = %v, want provider message", err)
	}
}

func TestInferenceSTTUnexpectedCloseReturnsNextError(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{
		readCh: make(chan []byte),
	}
	close(conn.readCh)
	provider := NewSTT("livekit/stt", "key", "secret")
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want unexpected close error")
	}
	if !strings.Contains(err.Error(), "LiveKit Inference STT connection closed unexpectedly") {
		t.Fatalf("Next() error = %v, want unexpected close error", err)
	}
}

func TestInferenceSTTMalformedGatewayJSONReturnsNextError(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{
		readCh: make(chan []byte, 1),
	}
	conn.readCh <- []byte(`{`)
	close(conn.readCh)
	provider := NewSTT("livekit/stt", "key", "secret")
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "failed to decode LiveKit Inference STT message") {
		t.Fatalf("Next() error = %v, want malformed JSON error", err)
	}
}

func TestInferenceSTTInputAudioWriteErrorReturnsNextError(t *testing.T) {
	conn := &fakeInferenceWebsocketConn{
		readBlock:  make(chan struct{}),
		writeErr:   errors.New("write failed"),
		writeErrAt: 2,
	}
	provider := NewSTT("livekit/stt", "key", "secret")
	provider.dialWebsocket = func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
		return conn, nil
	}

	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte("audio"),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	select {
	case err = <-errCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for input audio write error")
	}
	if err == nil {
		t.Fatal("Next() error = nil, want input audio write error")
	}
	if !strings.Contains(err.Error(), "failed to send input_audio message to LiveKit Inference STT") {
		t.Fatalf("Next() error = %v, want input audio write error", err)
	}
}

type fakeInferenceWebsocketConn struct {
	writes     []map[string]interface{}
	closed     bool
	readBlock  chan struct{}
	readCh     chan []byte
	writeErr   error
	writeErrAt int
}

func (f *fakeInferenceWebsocketConn) WriteJSON(v interface{}) error {
	if f.writeErr != nil && (f.writeErrAt == 0 || len(f.writes)+1 >= f.writeErrAt) {
		return f.writeErr
	}
	msg, _ := v.(map[string]interface{})
	f.writes = append(f.writes, msg)
	return nil
}

func (f *fakeInferenceWebsocketConn) ReadMessage() (int, []byte, error) {
	if f.readCh != nil {
		msg, ok := <-f.readCh
		if !ok {
			return 0, nil, io.EOF
		}
		return 1, msg, nil
	}
	if f.readBlock != nil {
		<-f.readBlock
	}
	return 0, nil, io.EOF
}

func (f *fakeInferenceWebsocketConn) Close() error {
	f.closed = true
	if f.readBlock != nil {
		close(f.readBlock)
		f.readBlock = nil
	}
	return nil
}
