package cavos

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestCavosTTSDefaultsMatchCacatuaEndpoint(t *testing.T) {
	provider := NewTTS()

	if provider.baseURL != "http://localhost:8080/v1" {
		t.Fatalf("baseURL = %q, want Cacatua local v1 endpoint", provider.baseURL)
	}
	if provider.model != "supertonic-3" {
		t.Fatalf("model = %q, want supertonic-3", provider.model)
	}
	if provider.voice != "F1" {
		t.Fatalf("voice = %q, want F1", provider.voice)
	}
	if provider.responseFormat != "pcm" {
		t.Fatalf("responseFormat = %q, want pcm", provider.responseFormat)
	}
	if provider.SampleRate() != 44100 {
		t.Fatalf("sample rate = %d, want Cacatua native rate", provider.SampleRate())
	}
	if provider.Label() != "cavos.TTS" {
		t.Fatalf("label = %q, want cavos.TTS", provider.Label())
	}
	if tts.Provider(provider) != "cavos" {
		t.Fatalf("provider = %q, want cavos", tts.Provider(provider))
	}
	if tts.Model(provider) != "supertonic-3" {
		t.Fatalf("model metadata = %q, want supertonic-3", tts.Model(provider))
	}
}

func TestCavosTTSOptionsBuildCacatuaRequest(t *testing.T) {
	provider := NewTTS(
		WithTTSBaseURL("https://cacatua.example/v1/"),
		WithTTSModel("tts-1"),
		WithTTSVoice("gisa_300521"),
		WithTTSLanguage("id"),
		WithTTSResponseFormat("wav"),
		WithTTSSampleRate(22050),
		withTTSHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("method = %q, want POST", req.Method)
			}
			if req.URL.String() != "https://cacatua.example/v1/audio/speech" {
				t.Fatalf("url = %q, want Cacatua speech endpoint", req.URL.String())
			}
			if got := req.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("content-type = %q, want application/json", got)
			}
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			assertPayloadString(t, payload, "model", "tts-1")
			assertPayloadString(t, payload, "voice", "gisa_300521")
			assertPayloadString(t, payload, "input", "hello")
			assertPayloadString(t, payload, "lang", "id")
			assertPayloadString(t, payload, "response_format", "wav")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"audio/L16"}, "X-Sample-Rate": []string{"44100"}},
				Body:       io.NopCloser(stringsNewReader("\x01\x00\x02\x00")),
			}, nil
		})),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if got := audio.Frame.SampleRate; got != 44100 {
		t.Fatalf("sample rate = %d, want response header sample rate", got)
	}
	if got := provider.SampleRate(); got != 22050 {
		t.Fatalf("provider sample rate = %d, want configured sample rate metadata", got)
	}
}

func assertPayloadString(t *testing.T, payload map[string]any, key, want string) {
	t.Helper()
	if got, _ := payload[key].(string); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
