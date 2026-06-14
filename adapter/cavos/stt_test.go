package cavos

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestCavosSTTDefaultsMatchStenoEndpoint(t *testing.T) {
	provider := NewSTT()

	if provider.baseURL != "http://steno.dev.cavos.internal/v1" {
		t.Fatalf("baseURL = %q, want Steno dev internal v1 endpoint", provider.baseURL)
	}
	if provider.model != "whisper-1" {
		t.Fatalf("model = %q, want OpenAI-compatible whisper-1 default", provider.model)
	}
	if provider.language != "id" {
		t.Fatalf("language = %q, want Steno default language", provider.language)
	}
	if provider.Label() != "cavos.STT" {
		t.Fatalf("label = %q, want cavos.STT", provider.Label())
	}
	if stt.Provider(provider) != "cavos" {
		t.Fatalf("provider = %q, want cavos", stt.Provider(provider))
	}
	if stt.Model(provider) != "whisper-1" {
		t.Fatalf("model metadata = %q, want whisper-1", stt.Model(provider))
	}
	caps := provider.Capabilities()
	if !caps.OfflineRecognize || caps.Streaming || caps.InterimResults {
		t.Fatalf("capabilities = %+v, want offline-only STT", caps)
	}
}

func TestCavosSTTOptionsBuildStenoRequest(t *testing.T) {
	provider := NewSTT(
		WithSTTBaseURL("https://steno.example/v1/"),
		WithSTTModel("small"),
		WithSTTLanguage("en"),
		WithSTTPrompt("domain words"),
		withSTTHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("method = %q, want POST", req.Method)
			}
			if req.URL.String() != "https://steno.example/v1/audio/transcriptions" {
				t.Fatalf("url = %q, want Steno transcription endpoint", req.URL.String())
			}
			contentType := req.Header.Get("Content-Type")
			if !strings.HasPrefix(contentType, "multipart/form-data; boundary=") {
				t.Fatalf("content-type = %q, want multipart form", contentType)
			}
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			_ = req.Body.Close()
			fields := parseMultipartFields(t, contentType, body)
			if fields["model"] != "small" {
				t.Fatalf("model field = %q, want small", fields["model"])
			}
			if fields["language"] != "en" {
				t.Fatalf("language field = %q, want en", fields["language"])
			}
			if fields["prompt"] != "domain words" {
				t.Fatalf("prompt field = %q, want domain words", fields["prompt"])
			}
			if !bytes.Contains(body, []byte("RIFF")) || !bytes.Contains(body, []byte("WAVE")) {
				t.Fatalf("multipart body does not contain WAV payload")
			}
			return jsonResponse(http.StatusOK, `{"text":"halo dunia","language":"id","duration":1.25}`), nil
		})),
	)

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{
		Data:              []byte{1, 0, 2, 0},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}}, "")
	if err != nil {
		t.Fatalf("Recognize error = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %q, want final transcript", event.Type)
	}
	if got := event.Alternatives[0].Text; got != "halo dunia" {
		t.Fatalf("text = %q, want halo dunia", got)
	}
	if got := event.Alternatives[0].Language; got != "id" {
		t.Fatalf("language = %q, want id", got)
	}
}

func parseMultipartFields(t *testing.T, contentType string, body []byte) map[string]string {
	t.Helper()
	_, params, err := mimeParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	form, err := reader.ReadForm(32 << 20)
	if err != nil {
		t.Fatalf("read multipart form: %v", err)
	}
	fields := map[string]string{}
	for key, values := range form.Value {
		if len(values) > 0 {
			fields[key] = values[0]
		}
	}
	return fields
}
