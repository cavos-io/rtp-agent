package assemblyai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/model"
)

func TestAssemblyAIRecognizePollsTranscriptUntilCompleted(t *testing.T) {
	var transcriptPolls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "test-key" {
			t.Fatalf("Authorization header = %q, want test-key", got)
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload":
			writeJSON(t, w, map[string]any{"upload_url": "https://cdn.example/audio.wav"})
		case r.Method == http.MethodPost && r.URL.Path == "/transcript":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode transcript request: %v", err)
			}
			if got := body["audio_url"]; got != "https://cdn.example/audio.wav" {
				t.Fatalf("audio_url = %v, want upload url", got)
			}
			if got := body["language_code"]; got != "en" {
				t.Fatalf("language_code = %v, want en", got)
			}
			writeJSON(t, w, map[string]any{"id": "tr-123"})
		case r.Method == http.MethodGet && r.URL.Path == "/transcript/tr-123":
			transcriptPolls++
			if transcriptPolls == 1 {
				writeJSON(t, w, map[string]any{"status": "processing"})
				return
			}
			writeJSON(t, w, map[string]any{
				"status":     "completed",
				"text":       "hello from assembly",
				"confidence": 0.91,
				"words": []map[string]any{
					{
						"text":       "hello",
						"start":      100,
						"end":        300,
						"confidence": 0.93,
					},
					{
						"text":       "assembly",
						"start":      350,
						"end":        700,
						"confidence": 0.89,
					},
				},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	restore := configureAssemblyAITestHTTP(server)
	defer restore()

	provider := NewAssemblyAISTT("test-key")
	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "en")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}

	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event.Type = %s, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("len(event.Alternatives) = %d, want 1", len(event.Alternatives))
	}
	if got := event.Alternatives[0].Text; got != "hello from assembly" {
		t.Fatalf("transcript text = %q, want final transcript text", got)
	}
	if got := event.Alternatives[0].Confidence; got != 0.91 {
		t.Fatalf("confidence = %v, want 0.91", got)
	}
	words := event.Alternatives[0].Words
	if len(words) != 2 {
		t.Fatalf("words = %#v, want two timed words", words)
	}
	if words[0].Text != "hello" || words[0].StartTime != 0.1 || words[0].EndTime != 0.3 || words[0].Confidence != 0.93 {
		t.Fatalf("first word = %#v, want converted AssemblyAI word timing", words[0])
	}
	if transcriptPolls != 2 {
		t.Fatalf("transcript polls = %d, want 2", transcriptPolls)
	}
}

func TestAssemblyAIRecognizeReturnsTranscriptError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload":
			writeJSON(t, w, map[string]any{"upload_url": "https://cdn.example/audio.wav"})
		case r.Method == http.MethodPost && r.URL.Path == "/transcript":
			writeJSON(t, w, map[string]any{"id": "tr-bad"})
		case r.Method == http.MethodGet && r.URL.Path == "/transcript/tr-bad":
			writeJSON(t, w, map[string]any{"status": "error", "error": "bad audio"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	restore := configureAssemblyAITestHTTP(server)
	defer restore()

	provider := NewAssemblyAISTT("test-key")
	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err == nil {
		t.Fatal("Recognize returned nil error")
	}
	if got := err.Error(); got != "assemblyai transcript error: bad audio" {
		t.Fatalf("error = %q, want transcript error", got)
	}
}

func TestAssemblyAIRealtimeTranscriptEventPreservesWordTimings(t *testing.T) {
	resp := aaiResponse{
		MessageType: "FinalTranscript",
		Text:        "hello realtime",
		Confidence:  0.92,
		Words: []assemblyAIWord{
			{Text: "hello", Start: 100, End: 300, Confidence: 0.95},
			{Text: "realtime", Start: 350, End: 800, Confidence: 0.9},
		},
	}

	event := assemblyAIRealtimeTranscriptEvent(resp)
	if event == nil {
		t.Fatal("expected realtime transcript event")
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event.Type = %s, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello realtime" {
		t.Fatalf("text = %q, want hello realtime", alt.Text)
	}
	if alt.Confidence != 0.92 {
		t.Fatalf("confidence = %v, want 0.92", alt.Confidence)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %#v, want two timed words", alt.Words)
	}
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || got.Confidence != 0.95 {
		t.Fatalf("first word = %#v, want converted AssemblyAI realtime word timing", got)
	}
	if got := alt.Words[1]; got.Text != "realtime" || got.StartTime != 0.35 || got.EndTime != 0.8 || got.Confidence != 0.9 {
		t.Fatalf("second word = %#v, want converted AssemblyAI realtime word timing", got)
	}
}

func TestAssemblyAISTTCapabilitiesAdvertiseWordAlignment(t *testing.T) {
	provider := NewAssemblyAISTT("test-key")

	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func configureAssemblyAITestHTTP(server *httptest.Server) func() {
	prevBaseURL := assemblyAIBaseURL
	prevClient := assemblyAIHTTPClient
	prevPollInterval := assemblyAIPollInterval
	assemblyAIBaseURL = server.URL
	assemblyAIHTTPClient = server.Client()
	assemblyAIPollInterval = time.Nanosecond
	return func() {
		assemblyAIBaseURL = prevBaseURL
		assemblyAIHTTPClient = prevClient
		assemblyAIPollInterval = prevPollInterval
	}
}
