package deepgram

import (
	"net/url"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestDeepgramSpeechEventPreservesAlternativeWords(t *testing.T) {
	speaker := 2
	resp := dgResponse{
		Type:     "Results",
		IsFinal:  true,
		Start:    1.5,
		Duration: 0.9,
	}
	resp.Metadata.RequestID = "request-1"
	resp.Channel.Alternatives = []dgAlternative{
		{
			Transcript: "hello world",
			Confidence: 0.98,
			Words: []dgWord{
				{Word: "hello", Start: 1.5, End: 1.8, Confidence: 0.99, Speaker: &speaker},
				{Word: "world", Start: 1.9, End: 2.4, Confidence: 0.97, Speaker: &speaker},
			},
		},
	}

	event := deepgramSpeechEvent(resp)
	if event == nil {
		t.Fatal("expected speech event")
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want %v", event.Type, stt.SpeechEventFinalTranscript)
	}
	if event.RequestID != "request-1" {
		t.Fatalf("request id = %q, want request-1", event.RequestID)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}

	alt := event.Alternatives[0]
	if alt.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", alt.Text)
	}
	if alt.StartTime != 1.5 || alt.EndTime != 2.4 {
		t.Fatalf("time range = %v-%v, want 1.5-2.4", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(alt.Words))
	}
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 1.5 || got.EndTime != 1.8 || got.Confidence != 0.99 || got.SpeakerID != "2" {
		t.Fatalf("first word = %+v, want hello timing with speaker 2", got)
	}
	if got := alt.Words[1]; got.Text != "world" || got.StartTime != 1.9 || got.EndTime != 2.4 || got.Confidence != 0.97 || got.SpeakerID != "2" {
		t.Fatalf("second word = %+v, want world timing with speaker 2", got)
	}
}

func TestDeepgramRecognizeSpeechEventPreservesAlternativeWords(t *testing.T) {
	speaker := 0
	resp := dgRecognitionResponse{}
	resp.Results.Channels = []dgRecognitionChannel{
		{
			Alternatives: []dgAlternative{
				{
					Transcript: "hello offline",
					Confidence: 0.91,
					Words: []dgWord{
						{Word: "hello", Start: 0.1, End: 0.3, Confidence: 0.93, Speaker: &speaker},
						{Word: "offline", Start: 0.4, End: 0.8, Confidence: 0.9, Speaker: &speaker},
					},
				},
			},
		},
	}

	event := deepgramRecognizeSpeechEvent(resp)
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want %v", event.Type, stt.SpeechEventFinalTranscript)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}

	alt := event.Alternatives[0]
	if alt.Text != "hello offline" {
		t.Fatalf("text = %q, want hello offline", alt.Text)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(alt.Words))
	}
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || got.Confidence != 0.93 || got.SpeakerID != "0" {
		t.Fatalf("first word = %+v, want hello timing with speaker 0", got)
	}
	if got := alt.Words[1]; got.Text != "offline" || got.StartTime != 0.4 || got.EndTime != 0.8 || got.Confidence != 0.9 || got.SpeakerID != "0" {
		t.Fatalf("second word = %+v, want offline timing with speaker 0", got)
	}
}

func TestDeepgramSTTCapabilitiesAdvertiseWordAlignment(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "nova-2")

	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
	}
}

func TestDeepgramSTTDefaultsMatchReference(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "")

	if provider.model != "nova-3" {
		t.Fatalf("model = %q, want nova-3", provider.model)
	}
	if !provider.punctuate {
		t.Fatalf("punctuate = false, want true")
	}
	if provider.smartFormat {
		t.Fatalf("smartFormat = true, want false")
	}
	if !provider.noDelay {
		t.Fatalf("noDelay = false, want true")
	}
	if provider.endpointingMS != 25 {
		t.Fatalf("endpointingMS = %d, want 25", provider.endpointingMS)
	}
	if !provider.fillerWords {
		t.Fatalf("fillerWords = false, want true")
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sampleRate = %d, want 16000", provider.sampleRate)
	}
}

func TestDeepgramSTTUsesEnvAPIKeyWhenOmitted(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "env-key")

	provider := NewDeepgramSTT("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewDeepgramSTT("explicit-key", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestDeepgramStreamURLUsesReferenceOptions(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "")

	got, err := url.Parse(buildDeepgramStreamURL(provider, "en-US"))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}

	if got.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", got.Scheme)
	}
	query := got.Query()
	assertDeepgramQuery(t, query, "model", "nova-3")
	assertDeepgramQuery(t, query, "language", "en-US")
	assertDeepgramQuery(t, query, "punctuate", "true")
	assertDeepgramQuery(t, query, "smart_format", "false")
	assertDeepgramQuery(t, query, "no_delay", "true")
	assertDeepgramQuery(t, query, "interim_results", "true")
	assertDeepgramQuery(t, query, "encoding", "linear16")
	assertDeepgramQuery(t, query, "sample_rate", "16000")
	assertDeepgramQuery(t, query, "channels", "1")
	assertDeepgramQuery(t, query, "endpointing", "25")
	assertDeepgramQuery(t, query, "vad_events", "true")
	assertDeepgramQuery(t, query, "filler_words", "true")
}

func TestDeepgramRecognizeURLUsesReferenceOptions(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "")

	got, err := url.Parse(buildDeepgramRecognizeURL(provider, "id-ID"))
	if err != nil {
		t.Fatalf("parse recognize url: %v", err)
	}

	if got.Scheme != "https" {
		t.Fatalf("scheme = %q, want https", got.Scheme)
	}
	query := got.Query()
	assertDeepgramQuery(t, query, "model", "nova-3")
	assertDeepgramQuery(t, query, "language", "id-ID")
	assertDeepgramQuery(t, query, "punctuate", "true")
	assertDeepgramQuery(t, query, "smart_format", "false")
}

func TestDeepgramSTTAdvancedOptionsUseReferenceQueryParams(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "nova-2",
		WithDeepgramSTTBaseURL("https://deepgram.example/v1/listen"),
		WithDeepgramSTTInterimResults(false),
		WithDeepgramSTTPunctuate(false),
		WithDeepgramSTTSmartFormat(true),
		WithDeepgramSTTNoDelay(false),
		WithDeepgramSTTEndpointing(0),
		WithDeepgramSTTDiarization(true),
		WithDeepgramSTTFillerWords(false),
		WithDeepgramSTTSampleRate(48000),
		WithDeepgramSTTNumChannels(2),
		WithDeepgramSTTVADEvents(false),
		WithDeepgramSTTProfanityFilter(true),
		WithDeepgramSTTNumerals(true),
		WithDeepgramSTTMipOptOut(true),
		WithDeepgramSTTKeywords([]DeepgramKeyword{{Keyword: "cavos", Boost: 2.5}}),
		WithDeepgramSTTKeyterms([]string{"LiveKit", "rtp-agent"}),
		WithDeepgramSTTRedact([]string{"pci", "ssn"}),
		WithDeepgramSTTTags([]string{"agent", "test"}),
	)

	caps := provider.Capabilities()
	if caps.InterimResults || !caps.Diarization {
		t.Fatalf("capabilities = %+v, want interim false and diarization true", caps)
	}

	got, err := url.Parse(buildDeepgramStreamURL(provider, "en-US"))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if got.Scheme != "wss" || got.Host != "deepgram.example" || got.Path != "/v1/listen" {
		t.Fatalf("stream url = %q, want configured websocket URL", got.String())
	}
	query := got.Query()
	assertDeepgramQuery(t, query, "model", "nova-2")
	assertDeepgramQuery(t, query, "punctuate", "false")
	assertDeepgramQuery(t, query, "smart_format", "true")
	assertDeepgramQuery(t, query, "no_delay", "false")
	assertDeepgramQuery(t, query, "interim_results", "false")
	assertDeepgramQuery(t, query, "sample_rate", "48000")
	assertDeepgramQuery(t, query, "channels", "2")
	assertDeepgramQuery(t, query, "endpointing", "false")
	assertDeepgramQuery(t, query, "vad_events", "false")
	assertDeepgramQuery(t, query, "filler_words", "false")
	assertDeepgramQuery(t, query, "diarize", "true")
	assertDeepgramQuery(t, query, "profanity_filter", "true")
	assertDeepgramQuery(t, query, "numerals", "true")
	assertDeepgramQuery(t, query, "mip_opt_out", "true")
	assertDeepgramQueryValues(t, query, "keywords", []string{"cavos:2.5"})
	assertDeepgramQueryValues(t, query, "keyterm", []string{"LiveKit", "rtp-agent"})
	assertDeepgramQueryValues(t, query, "redact", []string{"pci", "ssn"})
	assertDeepgramQueryValues(t, query, "tag", []string{"agent", "test"})
}

func assertDeepgramQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertDeepgramQueryValues(t *testing.T, query url.Values, key string, want []string) {
	t.Helper()
	got := query[key]
	if len(got) != len(want) {
		t.Fatalf("%s = %+v, want %+v", key, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %+v, want %+v", key, got, want)
		}
	}
}
