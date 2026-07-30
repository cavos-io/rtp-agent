package slng

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestGatewayHeadersMatchReference(t *testing.T) {
	headers, err := (gatewayHeaders{
		APIKey:            "slng-key",
		ProviderAPIKey:    "provider-key",
		RegionOverride:    " eu, US ",
		WorldPartOverride: " ap ",
		ExternalAgentID:   " agent-1 ",
		ExternalSessionID: " session-1 ",
	}).build(http.Header{"X-Candidate": {"yes"}})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"Authorization":         "Bearer slng-key",
		"X-Slng-Provider-Key":   "provider-key",
		"X-Region-Override":     "eu, us",
		"X-World-Part-Override": "ap",
		"X-SLNG-Agent-Id":       "agent-1",
		"X-SLNG-Session-Id":     "session-1",
		"X-Candidate":           "yes",
	} {
		if got := headers.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestGatewayHeadersRejectInvalidTrackingID(t *testing.T) {
	_, err := (gatewayHeaders{ExternalAgentID: "bad,id"}).build(nil)
	if err == nil {
		t.Fatal("build() error = nil")
	}
}

func TestGatewayHeadersCountTrackingIDCharacters(t *testing.T) {
	headers, err := (gatewayHeaders{ExternalAgentID: strings.Repeat("界", 128)}).build(nil)
	if err != nil {
		t.Fatalf("128-character tracking ID error = %v", err)
	}
	if got := utf8.RuneCountInString(headers.Get("X-SLNG-Agent-Id")); got != 128 {
		t.Fatalf("tracking ID length = %d, want 128", got)
	}
	if _, err := (gatewayHeaders{ExternalAgentID: strings.Repeat("界", 129)}).build(nil); err == nil {
		t.Fatal("129-character tracking ID error = nil")
	}
}

func TestSLNGStatusErrorMapsBridgeCode(t *testing.T) {
	err := slngStatusError(map[string]any{
		"type": "error",
		"data": map[string]any{"code": "rate_limit", "message": "slow down"},
	})
	if err.StatusCode != 429 || err.Message != "slow down" {
		t.Fatalf("slngStatusError() = %#v", err)
	}
}

func TestSLNGStatusErrorTruncatesAtRuneBoundary(t *testing.T) {
	err := slngStatusError(map[string]any{
		"data": map[string]any{"message": strings.Repeat("界", 501)},
	})
	if !utf8.ValidString(err.Message) || utf8.RuneCountInString(err.Message) != slngErrorMessageMaxLen {
		t.Fatalf("message valid=%v length=%d, want valid length %d", utf8.ValidString(err.Message), utf8.RuneCountInString(err.Message), slngErrorMessageMaxLen)
	}
}

func TestSTTGatewayOptionsBuildHeaders(t *testing.T) {
	provider := NewSTT("slng-key",
		WithSTTProviderAPIKey("provider-key"),
		WithSTTWorldPartOverride(" AP "),
		WithSTTExternalTracking(" agent-1 ", " session-1 "),
		WithSTTExtraHeaders(http.Header{"X-Extra": {"yes"}}),
	)
	headers, err := buildSTTWebsocketHeaders(provider)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"X-Slng-Provider-Key":   "provider-key",
		"X-World-Part-Override": "ap",
		"X-SLNG-Agent-Id":       "agent-1",
		"X-SLNG-Session-Id":     "session-1",
		"X-Extra":               "yes",
	} {
		if got := headers.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestTTSGatewayOptionsBuildHeaders(t *testing.T) {
	provider := NewTTS("slng-key",
		WithTTSProviderAPIKey("provider-key"),
		WithTTSWorldPartOverride(" AP "),
		WithTTSExternalTracking(" agent-1 ", " session-1 "),
		WithTTSExtraHeaders(http.Header{"X-Extra": {"yes"}}),
	)
	headers, err := buildTTSWebsocketHeaders(provider)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"X-Slng-Provider-Key":   "provider-key",
		"X-World-Part-Override": "ap",
		"X-SLNG-Agent-Id":       "agent-1",
		"X-SLNG-Session-Id":     "session-1",
		"X-Extra":               "yes",
	} {
		if got := headers.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestSLNGProviderFramesReturnTypedStatusError(t *testing.T) {
	_, err := sttEventsFromMessage([]byte(`{"type":"Error","data":{"code":"rate_limit","message":"slow down"}}`), "en", true)
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("sttEventsFromMessage() error = %#v", err)
	}

	_, _, err = ttsAudioFromMessage([]byte(`{"type":"error","data":{"code":"rate_limit","message":"slow down"}}`), 24000)
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("ttsAudioFromMessage() error = %#v", err)
	}
}

func TestSLNGSTTLowercaseProviderFrameReturnsTypedStatusError(t *testing.T) {
	_, err := sttEventsFromMessage([]byte(`{"type":"error","data":{"code":"rate_limit","message":"slow down"}}`), "en", true)
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("sttEventsFromMessage() error = %#v", err)
	}
}

func TestSLNGFallbackEligibilityMatchesReference(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "canceled", err: context.Canceled, want: false},
		{name: "bad request", err: llm.NewAPIStatusError("bad", http.StatusBadRequest, "", nil), want: false},
		{name: "payload too large", err: llm.NewAPIStatusError("large", http.StatusRequestEntityTooLarge, "", nil), want: false},
		{name: "rate limit", err: llm.NewAPIStatusError("slow", http.StatusTooManyRequests, "", nil), want: true},
		{name: "server", err: llm.NewAPIStatusError("down", http.StatusServiceUnavailable, "", nil), want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isSLNGFallbackEligible(test.err); got != test.want {
				t.Fatalf("isSLNGFallbackEligible() = %v, want %v", got, test.want)
			}
		})
	}
}
