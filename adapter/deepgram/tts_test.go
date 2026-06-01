package deepgram

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestDeepgramTTSDefaultsMatchReference(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "")

	if provider.model != "aura-2-andromeda-en" {
		t.Fatalf("model = %q, want aura-2-andromeda-en", provider.model)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.encoding != "linear16" {
		t.Fatalf("encoding = %q, want linear16", provider.encoding)
	}
}

func TestDeepgramTTSSynthesizeRequestUsesReferenceOptions(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSMipOptOut(true),
	)

	requestURL, body := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	query := parsed.Query()
	assertDeepgramTTSQuery(t, query, "model", "aura-2-andromeda-en")
	assertDeepgramTTSQuery(t, query, "encoding", "linear16")
	assertDeepgramTTSQuery(t, query, "sample_rate", "24000")
	assertDeepgramTTSQuery(t, query, "container", "none")
	assertDeepgramTTSQuery(t, query, "mip_opt_out", "true")

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["text"] != "hello" {
		t.Fatalf("text = %#v, want hello", payload["text"])
	}
}

func TestDeepgramTTSStreamURLUsesReferenceOptions(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "")

	streamURL := buildDeepgramTTSStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", parsed.Scheme)
	}
	query := parsed.Query()
	assertDeepgramTTSQuery(t, query, "model", "aura-2-andromeda-en")
	assertDeepgramTTSQuery(t, query, "encoding", "linear16")
	assertDeepgramTTSQuery(t, query, "sample_rate", "24000")
	assertDeepgramTTSQuery(t, query, "mip_opt_out", "false")
}

func assertDeepgramTTSQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
