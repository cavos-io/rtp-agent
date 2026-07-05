package rime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestRimeTTSDefaultsMatchReference(t *testing.T) {
	provider := NewRimeTTS("test-key", "")

	if provider.baseURL != "https://users.rime.ai/v1/rime-tts" {
		t.Fatalf("base URL = %q, want reference HTTP endpoint", provider.baseURL)
	}
	if provider.model != "arcana" {
		t.Fatalf("model = %q, want arcana", provider.model)
	}
	if got := tts.Model(provider); got != "arcana" {
		t.Fatalf("model metadata = %q, want arcana", got)
	}
	if got := tts.Provider(provider); got != "Rime" {
		t.Fatalf("provider metadata = %q, want Rime", got)
	}
	if provider.voice != "astra" {
		t.Fatalf("voice = %q, want astra", provider.voice)
	}
	if provider.lang != "eng" {
		t.Fatalf("lang = %q, want eng", provider.lang)
	}
	if provider.sampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", provider.sampleRate)
	}
	if provider.Capabilities().Streaming {
		t.Fatal("streaming = true, want false for default HTTP mode")
	}
}

func TestNewRimeTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("RIME_API_KEY", "env-key")

	provider := NewRimeTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("authorization = %q, want env bearer token", got)
	}

	streaming := NewRimeTTS("", "", WithRimeTTSWebsocket(true))
	if got := buildRimeTTSWebsocketHeaders(streaming).Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("websocket authorization = %q, want env bearer token", got)
	}

	explicit := NewRimeTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestRimeTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("RIME_API_KEY", "")
	provider := NewRimeTTS("", "", WithRimeTTSBaseURL("://bad-url"))

	_, synthErr := provider.Synthesize(context.Background(), "hello")
	if synthErr == nil || !strings.Contains(synthErr.Error(), "RIME_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", synthErr)
	}

	streaming := NewRimeTTS("", "", WithRimeTTSBaseURL("://bad-url"), WithRimeTTSWebsocket(true))
	_, streamErr := streaming.Stream(context.Background())
	if streamErr == nil || !strings.Contains(streamErr.Error(), "RIME_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", streamErr)
	}
}

func TestRimeTTSSynthesizeRequestUsesReferenceDefaults(t *testing.T) {
	provider := NewRimeTTS("test-key", "")

	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://users.rime.ai/v1/rime-tts" {
		t.Fatalf("url = %q, want reference endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Accept"); got != "audio/pcm" {
		t.Fatalf("accept = %q, want audio/pcm", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRimePayload(t, payload, "speaker", "astra")
	assertRimePayload(t, payload, "text", "hello")
	assertRimePayload(t, payload, "modelId", "arcana")
	assertRimePayload(t, payload, "lang", "eng")
	if got := payload["samplingRate"]; got != float64(22050) {
		t.Fatalf("samplingRate = %#v, want 22050", got)
	}
	if _, ok := payload["audioFormat"]; ok {
		t.Fatalf("audioFormat = %#v, want omitted for HTTP reference payload", payload["audioFormat"])
	}
}

func TestRimeTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if statusErr.Message != "Too Many Requests" {
		t.Fatalf("message = %q, want reference reason phrase", statusErr.Message)
	}
	if statusErr.Body != nil {
		t.Fatalf("body = %#v, want nil like reference ClientResponseError", statusErr.Body)
	}
}

func TestRimeTTSSynthesizeStatus499EndsLikeReferenceClientClose(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	body := &rimeCloseCountBody{Reader: bytes.NewReader([]byte(`client closed`))}
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 499,
			Body:       body,
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	audio, err := stream.Next()
	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after 499 = (%#v, %v), want EOF like reference client close", audio, err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close calls = %d, want 1", body.closeCount)
	}
}

func TestRimeTTSSynthesizeAcceptsReferenceSuccessStatusClass(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	body := []byte{0x01, 0x02}
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "",
		WithRimeTTSBaseURL("https://rime.example/v1/rime-tts"),
	)
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next audio error = %v, want successful 2xx audio", err)
	}
	if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, body) {
		t.Fatalf("Next audio = %+v, want body bytes %v", audio, body)
	}
}

func TestRimeTTSOptionsMatchReferenceModels(t *testing.T) {
	provider := NewRimeTTS("test-key", "",
		WithRimeTTSModel("coda"),
		WithRimeTTSSampleRate(24000),
		WithRimeTTSBaseURL("https://rime.example/v1/rime-tts"),
		WithRimeTTSLang("spa"),
		WithRimeTTSTimeScaleFactor(1.1),
	)

	if provider.voice != "lyra" {
		t.Fatalf("voice = %q, want coda default lyra", provider.voice)
	}

	req, err := buildRimeTTSRequest(context.Background(), provider, "hola")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://rime.example/v1/rime-tts" {
		t.Fatalf("url = %q, want custom base URL", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRimePayload(t, payload, "speaker", "lyra")
	assertRimePayload(t, payload, "modelId", "coda")
	assertRimePayload(t, payload, "lang", "spa")
	if got := payload["samplingRate"]; got != float64(24000) {
		t.Fatalf("samplingRate = %#v, want 24000", got)
	}
	if got := payload["timeScaleFactor"]; got != 1.1 {
		t.Fatalf("timeScaleFactor = %#v, want 1.1", got)
	}

	customVoice := NewRimeTTS("test-key", "", WithRimeTTSVoice("ember"))
	req, err = buildRimeTTSRequest(context.Background(), customVoice, "hello")
	if err != nil {
		t.Fatalf("build custom voice request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode custom voice body: %v", err)
	}
	assertRimePayload(t, payload, "speaker", "ember")

	emptyVoice := NewRimeTTS("test-key", "", WithRimeTTSVoice(""))
	req, err = buildRimeTTSRequest(context.Background(), emptyVoice, "hello")
	if err != nil {
		t.Fatalf("build empty voice request: %v", err)
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode empty voice body: %v", err)
	}
	assertRimePayload(t, payload, "speaker", "")
}

func TestRimeTTSUpdateOptionsMatchesReferenceFutureRequests(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))

	if err := provider.UpdateOptions(
		WithRimeTTSBaseURL("wss://rime.example"),
		WithRimeTTSModel("coda"),
		WithRimeTTSVoice("ember"),
		WithRimeTTSLang("spa"),
		WithRimeTTSSampleRate(24000),
		WithRimeTTSTimeScaleFactor(1.2),
		WithRimeTTSSegment("immediate"),
	); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}

	if provider.Model() != "coda" {
		t.Fatalf("model = %q, want coda", provider.Model())
	}
	if provider.SampleRate() != 22050 {
		t.Fatalf("sample rate = %d, want constructor sample rate unchanged like reference", provider.SampleRate())
	}
	u := buildRimeTTSWebsocketURL(provider)
	if got := u.Scheme + "://" + u.Host + u.Path; got != "wss://rime.example/ws3" {
		t.Fatalf("websocket URL base = %q, want updated base URL", got)
	}
	query := u.Query()
	assertRimePayload(t, queryMap(query), "speaker", "ember")
	assertRimePayload(t, queryMap(query), "modelId", "coda")
	assertRimePayload(t, queryMap(query), "lang", "spa")
	assertRimePayload(t, queryMap(query), "samplingRate", "22050")
	assertRimePayload(t, queryMap(query), "timeScaleFactor", "1.2")
	assertRimePayload(t, queryMap(query), "segment", "immediate")

	req, err := buildRimeTTSRequest(context.Background(), provider, "hola")
	if err != nil {
		t.Fatalf("build updated request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode updated request: %v", err)
	}
	if got := payload["samplingRate"]; got != float64(24000) {
		t.Fatalf("HTTP samplingRate = %#v, want updated request sample rate", got)
	}
}

func TestRimeTTSSegmentOptionsAllowReferenceEmptyValue(t *testing.T) {
	provider := NewRimeTTS("test-key", "",
		WithRimeTTSWebsocket(true),
		WithRimeTTSSegment(""),
	)
	query := buildRimeTTSWebsocketURL(provider).Query()
	if got, ok := query["segment"]; !ok || len(got) != 1 || got[0] != "" {
		t.Fatalf("constructor segment query = %#v, want explicit empty value", query["segment"])
	}

	updatable := NewRimeTTS("test-key", "",
		WithRimeTTSWebsocket(true),
		WithRimeTTSSegment("immediate"),
	)
	if err := updatable.UpdateOptions(WithRimeTTSSegment("")); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}
	query = buildRimeTTSWebsocketURL(updatable).Query()
	if got, ok := query["segment"]; !ok || len(got) != 1 || got[0] != "" {
		t.Fatalf("updated segment query = %#v, want explicit empty value", query["segment"])
	}
}

func TestRimeTTSUpdateOptionsAllowsReferenceEmptyVoice(t *testing.T) {
	provider := NewRimeTTS("test-key", "")

	if err := provider.UpdateOptions(WithRimeTTSVoice("")); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}

	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRimePayload(t, payload, "speaker", "")
}

func TestRimeTTSLanguageOptionsAllowReferenceEmptyValue(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSLang(""))

	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build constructor request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode constructor body: %v", err)
	}
	assertRimePayload(t, payload, "lang", "")

	streaming := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true), WithRimeTTSLang(""))
	query := buildRimeTTSWebsocketURL(streaming).Query()
	if got, ok := query["lang"]; !ok || len(got) != 1 || got[0] != "" {
		t.Fatalf("constructor websocket lang query = %#v, want explicit empty value", query["lang"])
	}

	updatable := NewRimeTTS("test-key", "",
		WithRimeTTSWebsocket(true),
		WithRimeTTSLang("spa"),
	)
	if err := updatable.UpdateOptions(WithRimeTTSLang("")); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}
	query = buildRimeTTSWebsocketURL(updatable).Query()
	if got, ok := query["lang"]; !ok || len(got) != 1 || got[0] != "" {
		t.Fatalf("updated websocket lang query = %#v, want explicit empty value", query["lang"])
	}
}

func TestRimeTTSUpdateOptionsPreservesReferenceTransportMode(t *testing.T) {
	provider := NewRimeTTS("test-key", "")
	if provider.Capabilities().Streaming {
		t.Fatal("initial streaming = true, want HTTP mode")
	}

	if err := provider.UpdateOptions(WithRimeTTSBaseURL("wss://rime.example")); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}

	if provider.Capabilities().Streaming {
		t.Fatal("streaming = true after base URL update, want transport unchanged")
	}
	if _, err := provider.Synthesize(context.Background(), "hello"); err != nil {
		t.Fatalf("Synthesize after websocket-looking base URL update error = %v", err)
	}
}

func TestRimeTTSBaseURLOptionsAllowReferenceEmptyValue(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSBaseURL(""))
	if provider.baseURL != "" {
		t.Fatalf("constructor base URL = %q, want explicit empty value", provider.baseURL)
	}
	if provider.Capabilities().Streaming {
		t.Fatal("constructor streaming = true, want false for empty HTTP base URL")
	}

	streaming := NewRimeTTS("test-key", "",
		WithRimeTTSWebsocket(true),
		WithRimeTTSBaseURL(""),
	)
	if streaming.baseURL != "" {
		t.Fatalf("constructor websocket base URL = %q, want explicit empty value", streaming.baseURL)
	}
	u := buildRimeTTSWebsocketURL(streaming)
	if got := u.Scheme + "://" + u.Host + u.Path; got != ":///ws3" {
		t.Fatalf("constructor websocket URL base = %q, want empty reference base plus /ws3", got)
	}

	updatable := NewRimeTTS("test-key", "", WithRimeTTSBaseURL("https://rime.example/old"))
	if err := updatable.UpdateOptions(WithRimeTTSBaseURL("")); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}
	if updatable.baseURL != "" {
		t.Fatalf("updated base URL = %q, want explicit empty value", updatable.baseURL)
	}
	if updatable.Capabilities().Streaming {
		t.Fatal("updated streaming = true, want transport mode unchanged")
	}
}

func TestRimeTTSBaseURLOptionsPreserveReferenceTrailingSlash(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSBaseURL("https://rime.example/"))
	if provider.baseURL != "https://rime.example/" {
		t.Fatalf("constructor base URL = %q, want trailing slash preserved", provider.baseURL)
	}
	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://rime.example/" {
		t.Fatalf("request URL = %q, want trailing slash preserved", req.URL.String())
	}

	streaming := NewRimeTTS("test-key", "", WithRimeTTSBaseURL("wss://rime.example/"))
	if !streaming.Capabilities().Streaming {
		t.Fatal("streaming = false, want websocket inferred from base URL")
	}
	u := buildRimeTTSWebsocketURL(streaming)
	if got := u.Scheme + "://" + u.Host + u.Path; got != "wss://rime.example//ws3" {
		t.Fatalf("websocket URL base = %q, want trailing slash preserved before /ws3", got)
	}

	updatable := NewRimeTTS("test-key", "", WithRimeTTSBaseURL("https://rime.example/old"))
	if err := updatable.UpdateOptions(WithRimeTTSBaseURL("https://rime.example/new/")); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}
	if updatable.baseURL != "https://rime.example/new/" {
		t.Fatalf("updated base URL = %q, want trailing slash preserved", updatable.baseURL)
	}
}

func TestRimeTTSModelOptionsAllowReferenceEmptyValue(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSModel(""))
	if provider.Model() != "" {
		t.Fatalf("constructor model = %q, want explicit empty value", provider.Model())
	}
	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	assertRimePayload(t, payload, "modelId", "")
	assertRimePayload(t, payload, "speaker", "astra")
	if _, ok := payload["lang"]; ok {
		t.Fatalf("empty model lang = %#v, want omitted like reference", payload["lang"])
	}
	if _, ok := payload["samplingRate"]; ok {
		t.Fatalf("empty model samplingRate = %#v, want omitted like reference", payload["samplingRate"])
	}

	updatable := NewRimeTTS("test-key", "", WithRimeTTSModel("coda"))
	if err := updatable.UpdateOptions(WithRimeTTSModel("")); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}
	if updatable.Model() != "" {
		t.Fatalf("updated model = %q, want explicit empty value", updatable.Model())
	}
	u := buildRimeTTSWebsocketURL(updatable)
	query := queryMap(u.Query())
	assertRimePayload(t, query, "modelId", "")
	assertRimePayload(t, query, "speaker", "lyra")
	if _, ok := query["lang"]; ok {
		t.Fatalf("empty model websocket lang = %#v, want omitted like reference", query["lang"])
	}
}

func TestRimeTTSUpdateOptionsIgnoresReferenceTransportChanges(t *testing.T) {
	provider := NewRimeTTS("test-key", "")

	if err := provider.UpdateOptions(WithRimeTTSWebsocket(true)); err != nil {
		t.Fatalf("UpdateOptions websocket true error = %v", err)
	}
	if provider.Capabilities().Streaming {
		t.Fatal("streaming = true after update, want reference transport fixed after construction")
	}
	if _, err := provider.Synthesize(context.Background(), "hello"); err != nil {
		t.Fatalf("Synthesize after websocket true update error = %v, want HTTP mode still usable", err)
	}
	if stream, err := provider.Stream(context.Background()); err == nil || stream != nil {
		t.Fatalf("Stream after websocket true update = (%#v, %v), want streaming still rejected", stream, err)
	}

	streaming := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	if err := streaming.UpdateOptions(WithRimeTTSWebsocket(false)); err != nil {
		t.Fatalf("UpdateOptions websocket false error = %v", err)
	}
	if !streaming.Capabilities().Streaming {
		t.Fatal("streaming = false after update, want reference transport fixed after construction")
	}
	if _, err := streaming.Synthesize(context.Background(), "hello"); err == nil {
		t.Fatal("Synthesize after websocket false update error = nil, want websocket mode still active")
	}
}

func TestRimeTTSUpdateOptionsDropsReferenceTimeScaleOnModelChange(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSModel("coda"), WithRimeTTSTimeScaleFactor(1.1))

	if err := provider.UpdateOptions(WithRimeTTSModel("mistv2")); err != nil {
		t.Fatalf("UpdateOptions model change error = %v, want stale coda timeScaleFactor ignored", err)
	}
	if provider.Model() != "mistv2" {
		t.Fatalf("model after update = %q, want mistv2", provider.Model())
	}
	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build mistv2 request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode mistv2 request: %v", err)
	}
	if _, ok := payload["timeScaleFactor"]; ok {
		t.Fatalf("mistv2 payload timeScaleFactor = %#v, want omitted stale value", payload["timeScaleFactor"])
	}

	err = provider.UpdateOptions(WithRimeTTSTimeScaleFactor(1.2))
	if err == nil || !strings.Contains(err.Error(), "time_scale_factor is not supported by the mistv2 model") {
		t.Fatalf("UpdateOptions explicit timeScaleFactor error = %v, want reference mistv2 rejection", err)
	}

	if err := provider.UpdateOptions(WithRimeTTSModel("coda")); err != nil {
		t.Fatalf("UpdateOptions back to coda error = %v", err)
	}
	req, err = buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build coda request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode coda request: %v", err)
	}
	if got := payload["timeScaleFactor"]; got != 1.1 {
		t.Fatalf("restored coda timeScaleFactor = %#v, want previous reference value 1.1", got)
	}
}

func TestRimeTTSUpdateOptionsKeepsReferenceMaxTokensModelSpecific(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSModel("coda"), WithRimeTTSMaxTokens(64))

	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build initial coda request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode initial coda request: %v", err)
	}
	if got := payload["max_tokens"]; got != float64(64) {
		t.Fatalf("initial coda max_tokens = %#v, want 64", got)
	}

	if err := provider.UpdateOptions(WithRimeTTSModel("arcana")); err != nil {
		t.Fatalf("UpdateOptions arcana error = %v", err)
	}
	req, err = buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build arcana request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode arcana request: %v", err)
	}
	if _, ok := payload["max_tokens"]; ok {
		t.Fatalf("arcana max_tokens = %#v, want omitted stale coda value", payload["max_tokens"])
	}

	if err := provider.UpdateOptions(WithRimeTTSModel("coda")); err != nil {
		t.Fatalf("UpdateOptions back to coda error = %v", err)
	}
	req, err = buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build restored coda request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode restored coda request: %v", err)
	}
	if got := payload["max_tokens"]; got != float64(64) {
		t.Fatalf("restored coda max_tokens = %#v, want previous reference value 64", got)
	}

	if err := provider.UpdateOptions(WithRimeTTSModel("arcana"), WithRimeTTSMaxTokens(32)); err != nil {
		t.Fatalf("UpdateOptions explicit arcana max_tokens error = %v", err)
	}
	req, err = buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build explicit arcana request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode explicit arcana request: %v", err)
	}
	if got := payload["max_tokens"]; got != float64(32) {
		t.Fatalf("explicit arcana max_tokens = %#v, want 32", got)
	}
}

func TestRimeTTSUpdateOptionsKeepsReferenceCommonParamsModelSpecific(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSModel("coda"))
	if err := provider.UpdateOptions(WithRimeTTSLang("spa"), WithRimeTTSSampleRate(24000)); err != nil {
		t.Fatalf("UpdateOptions coda common params error = %v", err)
	}

	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build updated coda request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode updated coda request: %v", err)
	}
	assertRimePayload(t, payload, "lang", "spa")
	if got := payload["samplingRate"]; got != float64(24000) {
		t.Fatalf("updated coda samplingRate = %#v, want 24000", got)
	}

	if err := provider.UpdateOptions(WithRimeTTSModel("arcana")); err != nil {
		t.Fatalf("UpdateOptions arcana error = %v", err)
	}
	req, err = buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build arcana request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode arcana request: %v", err)
	}
	if _, ok := payload["lang"]; ok {
		t.Fatalf("arcana lang = %#v, want omitted stale coda value", payload["lang"])
	}
	if _, ok := payload["samplingRate"]; ok {
		t.Fatalf("arcana samplingRate = %#v, want omitted stale coda value", payload["samplingRate"])
	}

	if err := provider.UpdateOptions(WithRimeTTSModel("coda")); err != nil {
		t.Fatalf("UpdateOptions back to coda error = %v", err)
	}
	req, err = buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build restored coda request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode restored coda request: %v", err)
	}
	assertRimePayload(t, payload, "lang", "spa")
	if got := payload["samplingRate"]; got != float64(24000) {
		t.Fatalf("restored coda samplingRate = %#v, want 24000", got)
	}
}

func TestRimeTTSUpdateOptionsKeepsReferenceArcanaParamsModelSpecific(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSModel("coda"))
	if err := provider.UpdateOptions(
		WithRimeTTSRepetitionPenalty(1.2),
		WithRimeTTSTemperature(0.7),
		WithRimeTTSTopP(0.8),
	); err != nil {
		t.Fatalf("UpdateOptions coda arcana-only params error = %v", err)
	}

	if err := provider.UpdateOptions(WithRimeTTSModel("arcana")); err != nil {
		t.Fatalf("UpdateOptions arcana error = %v", err)
	}
	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build arcana request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode arcana request: %v", err)
	}
	for _, key := range []string{"repetition_penalty", "temperature", "top_p"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("arcana %s = %#v, want omitted value ignored while model was coda", key, payload[key])
		}
	}

	if err := provider.UpdateOptions(
		WithRimeTTSRepetitionPenalty(1.3),
		WithRimeTTSTemperature(0.6),
		WithRimeTTSTopP(0.9),
	); err != nil {
		t.Fatalf("UpdateOptions explicit arcana params error = %v", err)
	}
	if err := provider.UpdateOptions(WithRimeTTSModel("coda")); err != nil {
		t.Fatalf("UpdateOptions coda error = %v", err)
	}
	if err := provider.UpdateOptions(WithRimeTTSModel("arcana")); err != nil {
		t.Fatalf("UpdateOptions back to arcana error = %v", err)
	}
	req, err = buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build restored arcana request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode restored arcana request: %v", err)
	}
	if got := payload["repetition_penalty"]; got != 1.3 {
		t.Fatalf("restored repetition_penalty = %#v, want 1.3", got)
	}
	if got := payload["temperature"]; got != 0.6 {
		t.Fatalf("restored temperature = %#v, want 0.6", got)
	}
	if got := payload["top_p"]; got != 0.9 {
		t.Fatalf("restored top_p = %#v, want 0.9", got)
	}
}

func TestRimeTTSUpdateOptionsKeepsReferenceMistParamsModelSpecific(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSModel("arcana"))
	if err := provider.UpdateOptions(
		WithRimeTTSSpeedAlpha(0.6),
		WithRimeTTSReduceLatency(true),
		WithRimeTTSPauseBetweenBrackets(true),
		WithRimeTTSPhonemizeBetweenBrackets(false),
	); err != nil {
		t.Fatalf("UpdateOptions arcana mist-only params error = %v", err)
	}

	if err := provider.UpdateOptions(WithRimeTTSModel("mistv2")); err != nil {
		t.Fatalf("UpdateOptions mistv2 error = %v", err)
	}
	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build mistv2 request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode mistv2 request: %v", err)
	}
	for _, key := range []string{"speedAlpha", "reduceLatency", "pauseBetweenBrackets", "phonemizeBetweenBrackets"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("mistv2 %s = %#v, want omitted value ignored while model was arcana", key, payload[key])
		}
	}

	if err := provider.UpdateOptions(
		WithRimeTTSSpeedAlpha(0.7),
		WithRimeTTSReduceLatency(true),
		WithRimeTTSPauseBetweenBrackets(true),
		WithRimeTTSPhonemizeBetweenBrackets(false),
	); err != nil {
		t.Fatalf("UpdateOptions explicit mist params error = %v", err)
	}
	if err := provider.UpdateOptions(WithRimeTTSModel("coda")); err != nil {
		t.Fatalf("UpdateOptions coda error = %v", err)
	}
	if err := provider.UpdateOptions(WithRimeTTSModel("mistv2")); err != nil {
		t.Fatalf("UpdateOptions back to mistv2 error = %v", err)
	}
	req, err = buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build restored mistv2 request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode restored mistv2 request: %v", err)
	}
	if got := payload["speedAlpha"]; got != 0.7 {
		t.Fatalf("restored speedAlpha = %#v, want 0.7", got)
	}
	if got := payload["reduceLatency"]; got != true {
		t.Fatalf("restored reduceLatency = %#v, want true", got)
	}
	if got := payload["pauseBetweenBrackets"]; got != true {
		t.Fatalf("restored pauseBetweenBrackets = %#v, want true", got)
	}
	if got := payload["phonemizeBetweenBrackets"]; got != false {
		t.Fatalf("restored phonemizeBetweenBrackets = %#v, want false", got)
	}
}

func TestRimeTTSModelSpecificOptionsMatchReferenceRequests(t *testing.T) {
	arcana := NewRimeTTS("test-key", "",
		WithRimeTTSRepetitionPenalty(1.2),
		WithRimeTTSTemperature(0.7),
		WithRimeTTSTopP(0.8),
		WithRimeTTSMaxTokens(128),
	)
	req, err := buildRimeTTSRequest(context.Background(), arcana, "hello")
	if err != nil {
		t.Fatalf("build arcana request: %v", err)
	}
	var arcanaPayload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&arcanaPayload); err != nil {
		t.Fatalf("decode arcana body: %v", err)
	}
	if got := arcanaPayload["repetition_penalty"]; got != 1.2 {
		t.Fatalf("repetition_penalty = %#v, want 1.2", got)
	}
	if got := arcanaPayload["temperature"]; got != 0.7 {
		t.Fatalf("temperature = %#v, want 0.7", got)
	}
	if got := arcanaPayload["top_p"]; got != 0.8 {
		t.Fatalf("top_p = %#v, want 0.8", got)
	}
	if got := arcanaPayload["max_tokens"]; got != float64(128) {
		t.Fatalf("max_tokens = %#v, want 128", got)
	}

	mist := NewRimeTTS("test-key", "",
		WithRimeTTSModel("mistv2"),
		WithRimeTTSSpeedAlpha(0.6),
		WithRimeTTSReduceLatency(true),
		WithRimeTTSPauseBetweenBrackets(true),
		WithRimeTTSPhonemizeBetweenBrackets(false),
	)
	req, err = buildRimeTTSRequest(context.Background(), mist, "hello")
	if err != nil {
		t.Fatalf("build mist request: %v", err)
	}
	var mistPayload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&mistPayload); err != nil {
		t.Fatalf("decode mist body: %v", err)
	}
	if got := mistPayload["speedAlpha"]; got != 0.6 {
		t.Fatalf("speedAlpha = %#v, want 0.6", got)
	}
	if got := mistPayload["reduceLatency"]; got != true {
		t.Fatalf("reduceLatency = %#v, want true", got)
	}
	if got := mistPayload["pauseBetweenBrackets"]; got != true {
		t.Fatalf("pauseBetweenBrackets = %#v, want true", got)
	}
	if got := mistPayload["phonemizeBetweenBrackets"]; got != false {
		t.Fatalf("phonemizeBetweenBrackets = %#v, want false", got)
	}

	mist.useWebsocket = true
	query := buildRimeTTSWebsocketURL(mist).Query()
	assertRimePayload(t, queryMap(query), "speedAlpha", "0.6")
	assertRimePayload(t, queryMap(query), "pauseBetweenBrackets", "true")
	assertRimePayload(t, queryMap(query), "phonemizeBetweenBrackets", "false")
	if got := query.Get("reduceLatency"); got != "" {
		t.Fatalf("websocket reduceLatency = %q, want omitted like reference", got)
	}
}

func TestRimeTTSRejectsMistV2TimeScaleFactor(t *testing.T) {
	provider := NewRimeTTS("test-key", "",
		WithRimeTTSModel("mistv2"),
		WithRimeTTSTimeScaleFactor(1.1),
	)

	_, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err == nil || !strings.Contains(err.Error(), "time_scale_factor is not supported by the mistv2 model") {
		t.Fatalf("build request error = %v, want reference mistv2 time_scale_factor error", err)
	}

	streaming := NewRimeTTS("test-key", "",
		WithRimeTTSModel("mistv2"),
		WithRimeTTSTimeScaleFactor(1.1),
		WithRimeTTSWebsocket(true),
	)
	_, err = streaming.Stream(context.Background())
	if err == nil || !strings.Contains(err.Error(), "time_scale_factor is not supported by the mistv2 model") {
		t.Fatalf("Stream error = %v, want reference mistv2 time_scale_factor error", err)
	}
}

func TestRimeTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
}

func TestRimeTTSSampleRateOptionsAllowReferenceZeroValue(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSSampleRate(0))

	if provider.SampleRate() != 0 {
		t.Fatalf("sample rate = %d, want explicit zero like reference", provider.SampleRate())
	}
	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build constructor request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode constructor body: %v", err)
	}
	if got := payload["samplingRate"]; got != float64(0) {
		t.Fatalf("constructor samplingRate = %#v, want 0", got)
	}

	streaming := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true), WithRimeTTSSampleRate(0))
	query := buildRimeTTSWebsocketURL(streaming).Query()
	assertRimePayload(t, queryMap(query), "samplingRate", "0")

	updatable := NewRimeTTS("test-key", "")
	if err := updatable.UpdateOptions(WithRimeTTSSampleRate(0)); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}
	req, err = buildRimeTTSRequest(context.Background(), updatable, "hello")
	if err != nil {
		t.Fatalf("build updated request: %v", err)
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode updated body: %v", err)
	}
	if got := payload["samplingRate"]; got != float64(0) {
		t.Fatalf("updated samplingRate = %#v, want 0", got)
	}
	if updatable.SampleRate() != defaultRimeSampleRate {
		t.Fatalf("updated output sample rate = %d, want constructor sample rate unchanged", updatable.SampleRate())
	}
}

func TestRimeTTSChunkedStreamKeepsAudioReturnedWithEOF(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: &rimeFinalEOFReader{data: []byte{0x01, 0x02}}},
		sampleRate: 24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("Next = %#v, want audio frame", audio)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{0x01, 0x02}) {
		t.Fatalf("audio data = %v, want final bytes", got)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next error = %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final Next = %#v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestRimeTTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("rime transport failed")
	})}

	provider := NewRimeTTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestRimeTTSSynthesizeReturnsAPITimeoutError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}

	provider := NewRimeTTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestRimeTTSSynthesizeAppliesReferenceTotalTimeoutOnFirstNext(t *testing.T) {
	cases := []struct {
		name      string
		model     string
		wantLimit time.Duration
	}{
		{name: "arcana", model: "arcana", wantLimit: 240 * time.Second},
		{name: "coda", model: "coda", wantLimit: 240 * time.Second},
		{name: "mist", model: "mistv3", wantLimit: 30 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hasDeadline bool
			var remaining time.Duration
			originalClient := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = originalClient })
			http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				hasDeadline = ok
				if ok {
					remaining = time.Until(deadline)
				}
				return &http.Response{
					StatusCode: http.StatusTeapot,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":"stop"}`)),
					Request:    r,
				}, nil
			})}

			provider := NewRimeTTS("test-key", "", WithRimeTTSModel(tc.model))
			stream, err := provider.Synthesize(context.Background(), "hello")
			if err != nil {
				t.Fatalf("Synthesize() error = %v", err)
			}
			defer stream.Close()
			_, err = stream.Next()
			if err == nil {
				t.Fatal("Next error = nil, want provider error after request capture")
			}
			if !hasDeadline {
				t.Fatal("request context has no deadline, want reference total timeout")
			}
			if remaining <= 0 || remaining > tc.wantLimit {
				t.Fatalf("request context deadline remaining = %v, want bounded by %s total timeout", remaining, tc.wantLimit)
			}
		})
	}
}

func TestRimeTTSSynthesizeLazyRequestUsesUpdatedReferenceTimeout(t *testing.T) {
	var hasDeadline bool
	var remaining time.Duration
	var modelID string
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		hasDeadline = ok
		if ok {
			remaining = time.Until(deadline)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		modelID, _ = payload["modelId"].(string)
		return &http.Response{
			StatusCode: http.StatusTeapot,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"stop"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "", WithRimeTTSModel("arcana"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	if err := provider.UpdateOptions(WithRimeTTSModel("mistv3")); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}
	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want provider error after request capture")
	}
	if modelID != "arcana" {
		t.Fatalf("request modelId = %q, want stream snapshot model arcana", modelID)
	}
	if !hasDeadline {
		t.Fatal("request context has no deadline, want reference total timeout")
	}
	if remaining <= 0 || remaining > 35*time.Second {
		t.Fatalf("request context deadline remaining = %v, want updated mist timeout", remaining)
	}
}

func TestRimeTTSSynthesizeDefersReferenceRequestUntilNext(t *testing.T) {
	requests := 0
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
		}, nil
	})}

	provider := NewRimeTTS("test-key", "", WithRimeTTSBaseURL("https://rime.example/v1/rime-tts"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	if requests != 0 {
		t.Fatalf("requests after Synthesize = %d, want 0 until stream is consumed", requests)
	}
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("Next() audio = %#v, want provider audio", audio)
	}
	if requests != 1 {
		t.Fatalf("requests after Next = %d, want 1", requests)
	}
}

func TestRimeTTSSynthesizeSendsReferenceEmptyText(t *testing.T) {
	requests := 0
	var text string
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		text, _ = payload["text"].(string)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests after empty text Next = %d, want reference request", requests)
	}
	if text != "" {
		t.Fatalf("request text = %q, want empty string", text)
	}
	if audio == nil || audio.Frame == nil || string(audio.Frame.Data) != string([]byte{0x01, 0x02}) {
		t.Fatalf("empty text audio = %#v, want provider audio", audio)
	}
}

func TestRimeTTSSynthesizeLazyRequestUsesUpdatedReferenceBaseURL(t *testing.T) {
	var requestedURL string
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestedURL = r.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "", WithRimeTTSBaseURL("https://rime.example/old"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	if err := provider.UpdateOptions(WithRimeTTSBaseURL("https://rime.example/new")); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}
	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if requestedURL != "https://rime.example/new" {
		t.Fatalf("request URL = %q, want updated reference base URL", requestedURL)
	}
}

func TestRimeTTSSynthesizeLazyRequestUsesUpdatedReferenceModelOptions(t *testing.T) {
	var payload map[string]any
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "",
		WithRimeTTSModel("coda"),
		WithRimeTTSBaseURL("https://rime.example/v1/rime-tts"),
	)
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	if err := provider.UpdateOptions(
		WithRimeTTSLang("spa"),
		WithRimeTTSSampleRate(24000),
		WithRimeTTSTimeScaleFactor(1.2),
		WithRimeTTSMaxTokens(64),
	); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}
	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}

	assertRimePayload(t, payload, "modelId", "coda")
	assertRimePayload(t, payload, "speaker", "lyra")
	assertRimePayload(t, payload, "lang", "spa")
	if got := payload["samplingRate"]; got != float64(24000) {
		t.Fatalf("samplingRate = %#v, want updated reference value 24000", got)
	}
	if got := payload["timeScaleFactor"]; got != 1.2 {
		t.Fatalf("timeScaleFactor = %#v, want updated reference value 1.2", got)
	}
	if got := payload["max_tokens"]; got != float64(64) {
		t.Fatalf("max_tokens = %#v, want updated reference value 64", got)
	}
}

func TestRimeTTSStreamDialFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("rime websocket dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil", stream)
	}
	if err == nil {
		t.Fatal("Stream error = nil, want APIConnectionError")
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestRimeTTSStreamDialTimeoutReturnsAPITimeoutError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, context.DeadlineExceeded
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil", stream)
	}
	if err == nil {
		t.Fatal("Stream error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Stream error = %T %v, want APITimeoutError", err, err)
	}
}

func TestRimeTTSStreamDialUsesReferenceConnectTimeout(t *testing.T) {
	var hasDeadline bool
	var remaining time.Duration
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			deadline, ok := ctx.Deadline()
			hasDeadline = ok
			if ok {
				remaining = time.Until(deadline)
			}
			return nil, context.DeadlineExceeded
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewRimeTTS("test-key", "",
		WithRimeTTSWebsocket(true),
		WithRimeTTSStreamResponseTimeout(25*time.Millisecond),
	)
	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil", stream)
	}
	if err == nil {
		t.Fatal("Stream error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Stream error = %T %v, want APITimeoutError", err, err)
	}
	if !hasDeadline {
		t.Fatal("dial context has no deadline, want reference connect timeout")
	}
	if remaining <= 0 || remaining > 50*time.Millisecond {
		t.Fatalf("dial context deadline remaining = %v, want bounded by connect timeout", remaining)
	}
}

func TestRimeTTSChunkedStreamReadFailureReturnsAPIConnectionError(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: rimeErrorReader{}},
		sampleRate: 22050,
	}
	defer stream.Close()

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestRimeTTSChunkedStreamKeepsAudioBeforeReferenceReadFailure(t *testing.T) {
	readErr := errors.New("rime response broke after audio")
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: &rimeAudioThenErrorReader{data: []byte{0x01, 0x00}, err: readErr}},
		sampleRate: 22050,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v, want audio before read error", err)
	}
	if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x00}) {
		t.Fatalf("first Next audio = %+v, want provider audio bytes", audio)
	}
	audio, err = stream.Next()
	if err == nil {
		t.Fatal("second Next error = nil, want APIConnectionError")
	}
	if audio != nil {
		t.Fatalf("second Next audio = %+v, want nil with read error", audio)
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("second Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestRimeTTSChunkedStreamReadTimeoutReturnsAPITimeoutError(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: rimeTimeoutReader{}},
		sampleRate: 22050,
	}
	defer stream.Close()

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestRimeTTSChunkedStreamNetReadTimeoutReturnsAPITimeoutError(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: rimeNetTimeoutReader{}},
		sampleRate: 22050,
	}
	defer stream.Close()

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestRimeTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next audio error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first Next = %+v, want audio frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next final error = %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final Next = %+v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

func TestRimeTTSChunkedStreamPreservesReferencePCMSampleBoundaries(t *testing.T) {
	want := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	stream := &rimeTTSChunkedStream{
		resp: &http.Response{Body: &rimeChunkedBody{chunks: [][]byte{
			{0x11, 0x22, 0x33},
			{0x44, 0x55, 0x66},
		}}},
		sampleRate: 24000,
	}
	defer stream.Close()

	var got []byte
	for {
		audio, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next error = %v", err)
		}
		if audio == nil || audio.Frame == nil {
			continue
		}
		data := audio.Frame.Data
		if len(data)%2 != 0 {
			t.Fatalf("emitted odd PCM frame length %d bytes: %v", len(data), data)
		}
		if int(audio.Frame.SamplesPerChannel) != len(data)/2 {
			t.Fatalf("SamplesPerChannel = %d, want %d", audio.Frame.SamplesPerChannel, len(data)/2)
		}
		got = append(got, data...)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("reassembled PCM = %v, want %v", got, want)
	}
}

func TestRimeTTSChunkedStreamAnnotatesReferenceRequestID(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next audio error = %v", err)
	}
	if audio.RequestID == "" {
		t.Fatal("audio RequestID is empty, want reference request id")
	}
	if audio.SegmentID != "" {
		t.Fatalf("audio SegmentID = %q, want empty chunked segment id", audio.SegmentID)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next final error = %v", err)
	}
	if final.RequestID != audio.RequestID {
		t.Fatalf("final RequestID = %q, want %q", final.RequestID, audio.RequestID)
	}
	if final.SegmentID != "" {
		t.Fatalf("final SegmentID = %q, want empty chunked segment id", final.SegmentID)
	}
}

func TestRimeTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &rimeCloseCountBody{Reader: bytes.NewReader([]byte{0x01, 0x02})}
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close() calls = %d, want 1", body.closeCount)
	}
}

func TestRimeTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %T %v, want EOF", err, err)
	}
}

func TestRimeTTSNonAudioResponseReportsReferenceNoAudio(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	body := &rimeCloseCountBody{Reader: bytes.NewReader([]byte(`{"error":"not audio"}`))}
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       body,
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "",
		WithRimeTTSBaseURL("https://rime.example/v1/rime-tts"),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil on no-audio error", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(err.Error(), "no audio frames were pushed for text: hello") {
		t.Fatalf("Next error = %T %v, want reference no-audio APIError", err, err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close() calls = %d, want 1", body.closeCount)
	}
	audio, err = stream.Next()
	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after no-audio = (%+v, %v), want nil EOF", audio, err)
	}
}

func TestRimeTTSEmptyAudioResponseReportsReferenceNoAudio(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	body := &rimeCloseCountBody{Reader: bytes.NewReader(nil)}
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       body,
			Request:    r,
		}, nil
	})}

	provider := NewRimeTTS("test-key", "",
		WithRimeTTSBaseURL("https://rime.example/v1/rime-tts"),
	)
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil on no-audio error", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(err.Error(), "no audio frames were pushed for text: hello") {
		t.Fatalf("Next error = %T %v, want reference no-audio APIError", err, err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close() calls = %d, want 1", body.closeCount)
	}
	audio, err = stream.Next()
	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after no-audio = (%+v, %v), want nil EOF", audio, err)
	}
}

func TestRimeTTSWhitespaceNonAudioResponseEndsLikeReference(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("no audio")),
		}, nil
	})}

	provider := NewRimeTTS("test-key", "",
		WithRimeTTSBaseURL("https://rime.example/v1/rime-tts"),
	)
	stream, err := provider.Synthesize(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next = (%#v, %v), want EOF without no-audio error for whitespace input", audio, err)
	}
}

func TestRimeTTSWebsocketModeMatchesReference(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))

	if provider.baseURL != "wss://users-ws.rime.ai" {
		t.Fatalf("base URL = %q, want reference websocket base URL", provider.baseURL)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true for websocket mode")
	}
	if !provider.Capabilities().AlignedTranscript {
		t.Fatal("aligned transcript = false, want true for websocket mode")
	}
}

func TestRimeTTSInfersWebsocketModeFromBaseURL(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSBaseURL("wss://rime.example"))

	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true for ws base URL")
	}

	provider = NewRimeTTS("test-key", "",
		WithRimeTTSBaseURL("wss://rime.example"),
		WithRimeTTSWebsocket(false),
	)

	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false after explicit false option, want ws base URL to match reference")
	}
}

func TestRimeTTSPrewarmWarmsReferenceWebsocketConnection(t *testing.T) {
	accepted := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var connections atomic.Int32
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				if err := rimeTestWebsocketHandshake(server); err != nil {
					t.Errorf("websocket handshake: %v", err)
					return
				}
				connections.Add(1)
				accepted <- struct{}{}
				<-release
			}()
			return client, nil
		},
		Proxy: nil,
	}
	t.Cleanup(func() {
		websocket.DefaultDialer = oldDialer
		releaseOnce.Do(func() { close(release) })
	})

	provider := NewRimeTTS("test-key", "",
		WithRimeTTSWebsocket(true),
		WithRimeTTSBaseURL("ws://rime.example"),
		WithRimeTTSStreamResponseTimeout(time.Second),
	)
	tts.Prewarm(provider)
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("Prewarm did not open reference websocket connection")
	}
	deadline := time.Now().Add(time.Second)
	for {
		provider.mu.Lock()
		warmed := provider.prewarmConn != nil
		provider.mu.Unlock()
		if warmed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Prewarm did not cache reference websocket connection")
		}
		time.Sleep(time.Millisecond)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream after Prewarm error = %v", err)
	}
	select {
	case <-accepted:
		t.Fatal("Stream opened a second websocket, want prewarmed connection reused")
	case <-time.After(25 * time.Millisecond):
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("websocket connections = %d, want one prewarmed connection reused", got)
	}
	releaseOnce.Do(func() { close(release) })
	if err := stream.Close(); err != nil {
		t.Fatalf("Close prewarmed stream error = %v", err)
	}
}

func TestRimeTTSStreamsReuseReferenceWebsocketConnection(t *testing.T) {
	var connections atomic.Int32
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go rimeTestServeReusableWebsocket(t, server, &connections)
			return client, nil
		},
		Proxy: nil,
	}
	t.Cleanup(func() {
		websocket.DefaultDialer = oldDialer
	})

	provider := NewRimeTTS(
		"test-key",
		"",
		WithRimeTTSWebsocket(true),
		WithRimeTTSBaseURL("ws://rime.example"),
	)
	for i := 0; i < 2; i++ {
		stream, err := provider.Stream(context.Background())
		if err != nil {
			t.Fatalf("Stream %d error = %v", i+1, err)
		}
		if err := stream.PushText("Hello there."); err != nil {
			t.Fatalf("PushText %d error = %v", i+1, err)
		}
		ending, ok := any(stream).(interface{ EndInput() error })
		if !ok {
			t.Fatal("Rime stream does not implement EndInput")
		}
		if err := ending.EndInput(); err != nil {
			t.Fatalf("EndInput %d error = %v", i+1, err)
		}
		for {
			audio, err := stream.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("Next %d error = %v", i+1, err)
			}
			if audio != nil && audio.Frame != nil && len(audio.Frame.Data) > 0 {
				continue
			}
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("websocket connections = %d, want one pooled reference connection reused", got)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close after pooled reuse error = %v", err)
	}
}

func TestRimeTTSExpiredPooledWebsocketReconnectsLikeReference(t *testing.T) {
	var connections atomic.Int32
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go rimeTestServeReusableWebsocket(t, server, &connections)
			return client, nil
		},
		Proxy: nil,
	}
	t.Cleanup(func() {
		websocket.DefaultDialer = oldDialer
	})

	provider := NewRimeTTS(
		"test-key",
		"",
		WithRimeTTSWebsocket(true),
		WithRimeTTSBaseURL("ws://rime.example"),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("first Stream error = %v", err)
	}
	if err := stream.PushText("Hello there."); err != nil {
		t.Fatalf("first PushText error = %v", err)
	}
	ending, ok := any(stream).(interface{ EndInput() error })
	if !ok {
		t.Fatal("Rime stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("first EndInput error = %v", err)
	}
	for {
		_, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("first Next error = %v", err)
		}
	}
	provider.mu.Lock()
	provider.prewarmRefreshedAt = time.Now().Add(-301 * time.Second)
	provider.mu.Unlock()

	stream, err = provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("second Stream error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if got := connections.Load(); got != 2 {
		t.Fatalf("websocket connections = %d, want expired pooled connection closed and redialed", got)
	}
	_ = provider.Close()
}

func TestRimeTTSEmptyStreamReturnsWebsocketToPoolLikeReference(t *testing.T) {
	var connections atomic.Int32
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go rimeTestServeReusableWebsocket(t, server, &connections)
			return client, nil
		},
		Proxy: nil,
	}
	t.Cleanup(func() {
		websocket.DefaultDialer = oldDialer
	})

	provider := NewRimeTTS(
		"test-key",
		"",
		WithRimeTTSWebsocket(true),
		WithRimeTTSBaseURL("ws://rime.example"),
	)
	emptyStream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("empty Stream error = %v", err)
	}
	ending, ok := any(emptyStream).(interface{ EndInput() error })
	if !ok {
		t.Fatal("Rime stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("empty EndInput error = %v", err)
	}
	audio, err := emptyStream.Next()
	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("empty Next = (%#v, %v), want nil EOF", audio, err)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("second Stream error = %v", err)
	}
	if err := stream.PushText("Hello there."); err != nil {
		t.Fatalf("second PushText error = %v", err)
	}
	ending, ok = any(stream).(interface{ EndInput() error })
	if !ok {
		t.Fatal("Rime stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("second EndInput error = %v", err)
	}
	for {
		_, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("second Next error = %v", err)
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("websocket connections = %d, want empty stream returned pooled connection", got)
	}
	_ = provider.Close()
}

func rimeTestServeReusableWebsocket(t *testing.T, conn net.Conn, connections *atomic.Int32) {
	t.Helper()
	defer conn.Close()
	if err := rimeTestWebsocketHandshake(conn); err != nil {
		t.Errorf("websocket handshake: %v", err)
		return
	}
	connections.Add(1)
	for {
		payload, err := rimeTestReadClientTextFrame(conn)
		if err != nil {
			return
		}
		var message map[string]any
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Errorf("decode text message: %v", err)
			return
		}
		if message["operation"] == "eos" {
			return
		}
		if _, ok := message["text"]; !ok {
			t.Errorf("first stream message = %v, want text", message)
			return
		}
		payload, err = rimeTestReadClientTextFrame(conn)
		if err != nil {
			t.Errorf("read flush message: %v", err)
			return
		}
		message = map[string]any{}
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Errorf("decode flush message: %v", err)
			return
		}
		if message["operation"] != "flush" {
			t.Errorf("second stream message = %v, want flush", message)
			return
		}
		chunk, err := json.Marshal(map[string]any{
			"type": "chunk",
			"data": base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
		})
		if err != nil {
			t.Errorf("marshal chunk: %v", err)
			return
		}
		if err := rimeTestWriteServerTextFrame(conn, chunk); err != nil {
			t.Errorf("write chunk message: %v", err)
			return
		}
		if err := rimeTestWriteServerTextFrame(conn, []byte(`{"type":"done"}`)); err != nil {
			t.Errorf("write done message: %v", err)
			return
		}
	}
}

func rimeTestReadClientTextFrame(conn net.Conn) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	if header[0]&0x0f == websocket.CloseMessage {
		return nil, io.EOF
	}
	if header[0]&0x0f != websocket.TextMessage {
		return nil, fmt.Errorf("websocket opcode = %d, want text", header[0]&0x0f)
	}
	masked := header[1]&0x80 != 0
	length := int(header[1] & 0x7f)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(conn, extended); err != nil {
			return nil, err
		}
		length = int(extended[0])<<8 | int(extended[1])
	case 127:
		return nil, errors.New("large websocket test frame unsupported")
	}
	if !masked {
		return nil, errors.New("client websocket frame missing mask")
	}
	mask := make([]byte, 4)
	if _, err := io.ReadFull(conn, mask); err != nil {
		return nil, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return payload, nil
}

func rimeTestWriteServerTextFrame(conn net.Conn, payload []byte) error {
	header := []byte{0x80 | byte(websocket.TextMessage)}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		return errors.New("large websocket test frame unsupported")
	}
	if _, err := conn.Write(header); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

func rimeTestWebsocketHandshake(conn net.Conn) error {
	reader := bufio.NewReader(conn)
	var key string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key = strings.TrimSpace(parts[1])
			}
		}
	}
	if key == "" {
		return errors.New("missing websocket key")
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	_, err := conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"))
	return err
}

func TestRimeTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewRimeTTS("test-key", "",
		WithRimeTTSWebsocket(true),
		WithRimeTTSModel("coda"),
		WithRimeTTSSampleRate(24000),
		WithRimeTTSLang("spa"),
		WithRimeTTSTimeScaleFactor(1.2),
		WithRimeTTSSegment("immediate"),
	)

	u := buildRimeTTSWebsocketURL(provider)
	if got := u.Scheme + "://" + u.Host + u.Path; got != "wss://users-ws.rime.ai/ws3" {
		t.Fatalf("websocket URL base = %q, want reference ws3 endpoint", got)
	}
	query := u.Query()
	assertRimePayload(t, queryMap(query), "speaker", "lyra")
	assertRimePayload(t, queryMap(query), "modelId", "coda")
	assertRimePayload(t, queryMap(query), "audioFormat", "pcm")
	assertRimePayload(t, queryMap(query), "samplingRate", "24000")
	assertRimePayload(t, queryMap(query), "segment", "immediate")
	assertRimePayload(t, queryMap(query), "lang", "spa")
	assertRimePayload(t, queryMap(query), "timeScaleFactor", "1.2")

	headers := buildRimeTTSWebsocketHeaders(provider)
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func TestRimeTTSWebsocketMessagesMatchReference(t *testing.T) {
	textMessage, err := buildRimeTTSTextMessage("ctx-1", "hello")
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var textPayload map[string]any
	if err := json.Unmarshal(textMessage, &textPayload); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	assertRimePayload(t, textPayload, "text", "hello ")
	assertRimePayload(t, textPayload, "contextId", "ctx-1")

	flushMessage, err := buildRimeTTSFlushMessage("ctx-1")
	if err != nil {
		t.Fatalf("build flush message: %v", err)
	}
	var flushPayload map[string]any
	if err := json.Unmarshal(flushMessage, &flushPayload); err != nil {
		t.Fatalf("decode flush message: %v", err)
	}
	assertRimePayload(t, flushPayload, "operation", "flush")
	assertRimePayload(t, flushPayload, "contextId", "ctx-1")
}

func TestRimeTTSStreamSendsSentencesAndDrainsTailLikeReference(t *testing.T) {
	var writes []map[string]any
	stream := &rimeTTSSynthesizeStream{
		contextID: "ctx-1",
		writeMessage: func(_ int, payload []byte) error {
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode write payload: %v", err)
			}
			writes = append(writes, message)
			return nil
		},
	}

	if err := stream.PushText("This first sentence is definitely long enough. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("writes after PushText = %d, want one completed sentence", len(writes))
	}
	assertRimePayload(t, writes[0], "text", "This first sentence is definitely long enough. ")

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if len(writes) != 2 {
		t.Fatalf("writes after Flush = %d, want completed sentence and tail text only", len(writes))
	}
	assertRimePayload(t, writes[1], "text", "Tail ")
	if _, ok := writes[1]["operation"]; ok {
		t.Fatalf("tail write operation = %#v, want text packet without provider flush", writes[1])
	}

	ending, ok := any(stream).(interface{ EndInput() error })
	if !ok {
		t.Fatal("Rime stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	if len(writes) != 3 {
		t.Fatalf("writes after EndInput = %d, want provider flush", len(writes))
	}
	assertRimePayload(t, writes[2], "operation", "flush")
	assertRimePayload(t, writes[2], "contextId", "ctx-1")
}

func TestRimeTTSStreamBuffersShortReferenceSentenceBeforeBoundary(t *testing.T) {
	var writes []map[string]any
	stream := &rimeTTSSynthesizeStream{
		contextID: "ctx-1",
		writeMessage: func(_ int, payload []byte) error {
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode write payload: %v", err)
			}
			writes = append(writes, message)
			return nil
		},
	}

	if err := stream.PushText("Dr. Smith is here. Next sentence is long enough."); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes after short first sentence = %d, want buffered like reference min_sentence_len", len(writes))
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("writes after Flush = %d, want buffered text drained", len(writes))
	}
	assertRimePayload(t, writes[0], "text", "Dr. Smith is here. Next sentence is long enough. ")
}

func TestRimeTTSStreamEndInputFlushesReferenceTailAndClosesInput(t *testing.T) {
	var writes []map[string]any
	stream := &rimeTTSSynthesizeStream{
		contextID: "ctx-1",
		writeMessage: func(_ int, payload []byte) error {
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode write payload: %v", err)
			}
			writes = append(writes, message)
			return nil
		},
	}
	ending, ok := any(stream).(interface{ EndInput() error })
	if !ok {
		t.Fatal("Rime stream does not implement EndInput")
	}

	if err := stream.PushText("Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	if len(writes) != 2 {
		t.Fatalf("writes after EndInput = %d, want tail text and flush", len(writes))
	}
	assertRimePayload(t, writes[0], "text", "Tail ")
	assertRimePayload(t, writes[1], "operation", "flush")
	assertRimePayload(t, writes[1], "contextId", "ctx-1")
	if err := stream.PushText("late"); err != nil {
		t.Fatalf("PushText after EndInput error = %v, want nil", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after EndInput error = %v, want nil", err)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("second EndInput error = %v, want nil", err)
	}
	if len(writes) != 2 {
		t.Fatalf("writes after closed input = %d, want no extra provider messages", len(writes))
	}
}

func TestRimeTTSStreamIgnoresReferenceSecondSegment(t *testing.T) {
	var writes []map[string]any
	stream := &rimeTTSSynthesizeStream{
		contextID: "ctx-1",
		writeMessage: func(_ int, payload []byte) error {
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode write payload: %v", err)
			}
			writes = append(writes, message)
			return nil
		},
	}

	if err := stream.PushText("first"); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(first) error = %v", err)
	}
	if err := stream.PushText("second"); err != nil {
		t.Fatalf("PushText(second) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(second) error = %v", err)
	}

	if len(writes) != 1 {
		t.Fatalf("writes = %d (%#v), want first segment text only", len(writes), writes)
	}
	assertRimePayload(t, writes[0], "text", "first ")
}

func TestRimeTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &rimeTTSSynthesizeStream{
		cancel: func() { cancelled = true },
		writeMessage: func(int, []byte) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("This sentence is definitely long enough. Tail"); !errors.Is(err, writeErr) {
		t.Fatalf("PushText error = %v, want write error", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after write failure")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("PushText after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("Flush after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
	}
}

func TestRimeTTSProviderCloseClosesActiveStreams(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	cancelled := false
	closeCalls := 0
	stream := &rimeTTSSynthesizeStream{
		cancel: func() { cancelled = true },
		writeMessage: func(int, []byte) error {
			return nil
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !cancelled {
		t.Fatal("stream cancel not called after provider Close")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("Flush after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestRimeTTSProviderCloseSendsReferenceEOS(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	var writes []map[string]any
	stream := &rimeTTSSynthesizeStream{
		cancel: func() {},
		writeMessage: func(_ int, payload []byte) error {
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode provider close payload: %v", err)
			}
			writes = append(writes, message)
			return nil
		},
		closeConn: func() error { return nil },
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("provider close writes = %d, want reference eos message", len(writes))
	}
	assertRimePayload(t, writes[0], "operation", "eos")
}

func TestRimeTTSProviderCloseWaitsForReferenceEOSAck(t *testing.T) {
	eosReceived := make(chan struct{})
	sendAck := make(chan struct{})
	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	stream := &rimeTTSSynthesizeStream{
		cancel: func() {},
		writeMessage: func(_ int, payload []byte) error {
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode eos: %v", err)
			}
			assertRimePayload(t, message, "operation", "eos")
			close(eosReceived)
			return nil
		},
		readMessage: func() (int, []byte, error) {
			<-sendAck
			return websocket.TextMessage, []byte(`{"type":"done"}`), nil
		},
		closeConn: func() error { return nil },
	}
	provider.registerStream(stream)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- provider.Close()
	}()

	select {
	case <-eosReceived:
	case err := <-closeDone:
		t.Fatalf("Close returned before eos was written: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for eos")
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before provider eos ack: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(sendAck)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Close after eos ack")
	}
}

func TestRimeTTSProviderCloseContinuesAfterReferenceEOSAckTimeout(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	readStarted := make(chan struct{})
	stream := &rimeTTSSynthesizeStream{
		cancel: func() {},
		writeMessage: func(_ int, payload []byte) error {
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode eos: %v", err)
			}
			assertRimePayload(t, message, "operation", "eos")
			return nil
		},
		readMessage: func() (int, []byte, error) {
			close(readStarted)
			return 0, nil, rimeTimeoutError{}
		},
		closeConn: func() error { return nil },
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v, want timeout ignored like reference close sequence", err)
	}
	select {
	case <-readStarted:
	default:
		t.Fatal("Close did not wait for eos ack before closing")
	}
}

type rimeTimeoutError struct{}

func (rimeTimeoutError) Error() string   { return "timeout" }
func (rimeTimeoutError) Timeout() bool   { return true }
func (rimeTimeoutError) Temporary() bool { return true }

func TestRimeTTSProviderCloseClosesAfterReferenceEOSFailure(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	writeErr := errors.New("eos write failed")
	closeCalls := 0
	stream := &rimeTTSSynthesizeStream{
		cancel: func() {},
		writeMessage: func(int, []byte) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	err := provider.Close()
	if !errors.Is(err, writeErr) {
		t.Fatalf("Close error = %v, want eos write error", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after eos failure = %d, want 1", closeCalls)
	}
}

func TestRimeTTSStreamCloseDoesNotSendReferenceEOS(t *testing.T) {
	cancelled := false
	writeCalls := 0
	closeCalls := 0
	stream := &rimeTTSSynthesizeStream{
		cancel: func() { cancelled = true },
		writeMessage: func(int, []byte) error {
			writeCalls++
			return nil
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !cancelled {
		t.Fatal("cancel not called on Close")
	}
	if writeCalls != 0 {
		t.Fatalf("write calls = %d, want 0 reference eos messages", writeCalls)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
}

func TestRimeTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &rimeTTSSynthesizeStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *tts.SynthesizedAudio),
		errCh:  make(chan error, 1),
		writeMessage: func(int, []byte) error {
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next() after Close audio = %#v, want nil", audio)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %v, want EOF", err)
	}
}

func TestRimeTTSStreamEmptyFlushIsReferenceNoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	closeCalls := 0
	stream := &rimeTTSSynthesizeStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		writeMessage: func(int, []byte) error {
			t.Fatal("empty Flush wrote provider message, want local final marker only")
			return nil
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	select {
	case audio := <-stream.events:
		t.Fatalf("empty Flush audio = %#v, want no event", audio)
	default:
	}
	if closeCalls != 0 {
		t.Fatalf("empty Flush close calls = %d, want 0", closeCalls)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	audio, err := stream.Next()
	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after empty EndInput = (%#v, %v), want nil EOF", audio, err)
	}
	if closeCalls != 1 {
		t.Fatalf("empty EndInput close calls = %d, want 1", closeCalls)
	}
}

func TestRimeTTSStreamDoesNotReadProviderBeforeReferenceInput(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
		close(serverClosed)
	}))
	defer server.Close()

	provider := NewRimeTTS(
		"test-key",
		"",
		WithRimeTTSWebsocket(true),
		WithRimeTTSBaseURL("ws"+strings.TrimPrefix(server.URL, "http")),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	rimeStream := stream.(*rimeTTSSynthesizeStream)
	select {
	case <-serverClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for provider close")
	}
	select {
	case err := <-rimeStream.errCh:
		t.Fatalf("read error before input = %v, want receive delayed like reference", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("empty Flush error = %v", err)
	}
	select {
	case audio := <-rimeStream.events:
		t.Fatalf("empty Flush audio = %+v, want no event", audio)
	default:
	}
	ending, ok := any(stream).(interface{ EndInput() error })
	if !ok {
		t.Fatal("Rime stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	audio, err := stream.Next()
	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after empty EndInput = (%+v, %v), want nil EOF", audio, err)
	}
}

func TestRimeTTSStreamDoneWithoutAudioReportsReferenceNoAudio(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read text message: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"done"}`)); err != nil {
			t.Errorf("write done message: %v", err)
		}
	}))
	defer server.Close()

	provider := NewRimeTTS(
		"test-key",
		"",
		WithRimeTTSWebsocket(true),
		WithRimeTTSBaseURL("ws"+strings.TrimPrefix(server.URL, "http")),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("Hello there."); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	ending, ok := any(stream).(interface{ EndInput() error })
	if !ok {
		t.Fatal("Rime stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil on no-audio error", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(err.Error(), "no audio frames were pushed for text: Hello there.") {
		t.Fatalf("Next error = %T %v, want reference no-audio APIError", err, err)
	}
}

func TestRimeTTSStreamPreservesReferencePCMSampleBoundaries(t *testing.T) {
	upgrader := websocket.Upgrader{}
	want := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read text message: %v", err)
			return
		}
		for _, data := range [][]byte{{0x11, 0x22, 0x33}, {0x44, 0x55, 0x66}} {
			payload, err := json.Marshal(map[string]any{
				"type": "chunk",
				"data": base64.StdEncoding.EncodeToString(data),
			})
			if err != nil {
				t.Errorf("marshal chunk: %v", err)
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				t.Errorf("write chunk message: %v", err)
				return
			}
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"done"}`)); err != nil {
			t.Errorf("write done message: %v", err)
		}
	}))
	defer server.Close()

	provider := NewRimeTTS(
		"test-key",
		"",
		WithRimeTTSWebsocket(true),
		WithRimeTTSBaseURL("ws"+strings.TrimPrefix(server.URL, "http")),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("Hello there."); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	ending, ok := any(stream).(interface{ EndInput() error })
	if !ok {
		t.Fatal("Rime stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}

	var got []byte
	for {
		audio, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next error = %v", err)
		}
		if audio == nil || audio.Frame == nil {
			continue
		}
		data := audio.Frame.Data
		if len(data)%2 != 0 {
			t.Fatalf("emitted odd PCM frame length %d bytes: %v", len(data), data)
		}
		if int(audio.Frame.SamplesPerChannel) != len(data)/2 {
			t.Fatalf("SamplesPerChannel = %d, want %d", audio.Frame.SamplesPerChannel, len(data)/2)
		}
		got = append(got, data...)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("reassembled PCM = %v, want %v", got, want)
	}
}

func TestRimeTTSStreamBuffersTimedTranscriptUntilAudioLikeReference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read text message: %v", err)
			return
		}
		timestamps := `{"type":"timestamps","word_timestamps":{"words":["hello","world"],"start":[0.1,0.3],"end":[0.2,0.5]}}`
		if err := conn.WriteMessage(websocket.TextMessage, []byte(timestamps)); err != nil {
			t.Errorf("write timestamps message: %v", err)
			return
		}
		chunk, err := json.Marshal(map[string]any{
			"type": "chunk",
			"data": base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
		})
		if err != nil {
			t.Errorf("marshal chunk: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, chunk); err != nil {
			t.Errorf("write chunk message: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"done"}`)); err != nil {
			t.Errorf("write done message: %v", err)
		}
	}))
	defer server.Close()

	provider := NewRimeTTS(
		"test-key",
		"",
		WithRimeTTSWebsocket(true),
		WithRimeTTSBaseURL("ws"+strings.TrimPrefix(server.URL, "http")),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("Hello world."); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	ending, ok := any(stream).(interface{ EndInput() error })
	if !ok {
		t.Fatal("Rime stream does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil {
		t.Fatal("Next audio = nil")
	}
	if audio.Frame == nil {
		t.Fatalf("metadata-only event = %#v, want transcript buffered until PCM audio", audio)
	}
	if audio.DeltaText != "hello world " {
		t.Fatalf("DeltaText = %q, want hello world", audio.DeltaText)
	}
	if len(audio.TimedTranscript) != 2 {
		t.Fatalf("TimedTranscript = %#v, want two timed words", audio.TimedTranscript)
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02}) {
		t.Fatalf("PCM frame = %#v, want provider audio", audio.Frame.Data)
	}
}

func TestRimeTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &rimeTTSSynthesizeStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		closed: true,
	}
	stream.events <- &tts.SynthesizedAudio{RequestID: "stale"}

	audio, err := stream.Next()

	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestRimeTTSNextReturnsQueuedAudioBeforeStreamError(t *testing.T) {
	providerErr := errors.New("provider failed after audio")
	for i := range 200 {
		want := &tts.SynthesizedAudio{RequestID: "req-audio"}
		stream := &rimeTTSSynthesizeStream{
			ctx:    context.Background(),
			events: make(chan *tts.SynthesizedAudio, 1),
			errCh:  make(chan error, 1),
		}
		stream.events <- want
		stream.errCh <- providerErr

		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("trial %d Next error = %v, want queued audio before stream error", i, err)
		}
		if audio != want {
			t.Fatalf("trial %d Next audio = %#v, want queued audio %#v", i, audio, want)
		}
	}
}

func TestRimeTTSStreamAnnotatesReferenceRequestAndSegmentIDs(t *testing.T) {
	stream := &rimeTTSSynthesizeStream{
		requestID: "req-1",
		contextID: "ctx-1",
	}

	events := []*tts.SynthesizedAudio{
		{Frame: &model.AudioFrame{Data: []byte{1}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
		{DeltaText: "hello ", TimedTranscript: []tts.TimedString{{Text: "hello ", StartTime: 0.1, EndTime: 0.2}}},
		{IsFinal: true},
	}

	for i, audio := range events {
		stream.annotateAudio(audio)
		if audio.RequestID != "req-1" {
			t.Fatalf("event %d RequestID = %q, want req-1", i, audio.RequestID)
		}
		if audio.SegmentID != "ctx-1" {
			t.Fatalf("event %d SegmentID = %q, want ctx-1", i, audio.SegmentID)
		}
	}
}

func TestRimeTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: rimeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}

	provider := NewRimeTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if httpCalls != 0 {
		t.Fatalf("HTTP calls after Close = %d, want 0", httpCalls)
	}
}

func TestRimeTTSStreamAfterCloseIsRejected(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	dialCalls := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls++
			return nil, errors.New("unexpected websocket dial")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true))
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
	if dialCalls != 0 {
		t.Fatalf("websocket dials after Close = %d, want 0", dialCalls)
	}
}

func TestRimeTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "bad audio stream"),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	stream := &rimeTTSSynthesizeStream{
		provider:  NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true)),
		conn:      conn,
		requestID: "req-close",
		events:    make(chan *tts.SynthesizedAudio, 1),
		errCh:     make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("readLoop error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != 0 {
			t.Fatalf("StatusCode = %d, want unset like reference", statusErr.StatusCode)
		}
		if statusErr.Body != nil {
			t.Fatalf("Body = %#v, want nil like reference", statusErr.Body)
		}
		if statusErr.RequestID != "req-close" {
			t.Fatalf("RequestID = %q, want stream request ID", statusErr.RequestID)
		}
		if !strings.Contains(err.Error(), "Rime ws closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Rime close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close error")
	}
}

func TestRimeTTSStreamNormalCloseBeforeDoneReturnsAPIStatusError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	stream := &rimeTTSSynthesizeStream{
		provider:  NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true)),
		conn:      conn,
		requestID: "req-normal-close",
		events:    make(chan *tts.SynthesizedAudio, 1),
		errCh:     make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("readLoop error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != 0 {
			t.Fatalf("StatusCode = %d, want unset like reference", statusErr.StatusCode)
		}
		if statusErr.Body != nil {
			t.Fatalf("Body = %#v, want nil like reference", statusErr.Body)
		}
		if statusErr.RequestID != "req-normal-close" {
			t.Fatalf("RequestID = %q, want stream request ID", statusErr.RequestID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for normal websocket close error")
	}
}

func TestRimeTTSReadTimeoutReturnsAPITimeoutError(t *testing.T) {
	err := rimeTTSReadError(context.DeadlineExceeded)
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("read error = %T %v, want APITimeoutError", err, err)
	}
}

func TestRimeTTSStreamReadDeadlineReturnsAPITimeoutError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	releaseServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		<-releaseServer
	}))
	defer server.Close()
	defer close(releaseServer)

	provider := NewRimeTTS(
		"test-key",
		"",
		WithRimeTTSWebsocket(true),
		WithRimeTTSBaseURL("ws"+strings.TrimPrefix(server.URL, "http")),
		WithRimeTTSStreamResponseTimeout(20*time.Millisecond),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("This sentence is definitely long enough."); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil on read timeout", audio)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestRimeTTSAudioFromWebsocketMessage(t *testing.T) {
	audio, done, transcript, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"chunk","data":"AQIDBA=="}`), 24000)
	if err != nil {
		t.Fatalf("audio from websocket message: %v", err)
	}
	if done {
		t.Fatal("done = true for chunk message")
	}
	if transcript != "" {
		t.Fatalf("transcript = %q, want empty for audio chunk", transcript)
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded audio frame", audio)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24000 Hz mono", audio.Frame)
	}

	timedAudio, done, transcript, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"timestamps","word_timestamps":{"words":["hi","there"],"start":[0.1,0.3],"end":[0.2,0.5]}}`), 24000)
	if err != nil {
		t.Fatalf("timestamps message: %v", err)
	}
	if done || transcript != "" {
		t.Fatalf("done=%v transcript=%q, want timed transcript audio event", done, transcript)
	}
	if timedAudio == nil || timedAudio.DeltaText != "hi there " || len(timedAudio.TimedTranscript) != 2 {
		t.Fatalf("timed audio = %+v, want two aligned transcript words", timedAudio)
	}
	if timedAudio.TimedTranscript[0].Text != "hi " || timedAudio.TimedTranscript[0].StartTime != 0.1 || timedAudio.TimedTranscript[0].EndTime != 0.2 {
		t.Fatalf("first timed word = %+v, want hi timing", timedAudio.TimedTranscript[0])
	}
	if timedAudio.TimedTranscript[1].Text != "there " || timedAudio.TimedTranscript[1].StartTime != 0.3 || timedAudio.TimedTranscript[1].EndTime != 0.5 {
		t.Fatalf("second timed word = %+v, want there timing", timedAudio.TimedTranscript[1])
	}

	truncated, done, transcript, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"timestamps","word_timestamps":{"words":["keep","drop"],"start":[0.4],"end":[0.6]}}`), 24000)
	if err != nil {
		t.Fatalf("mismatched timestamps message: %v", err)
	}
	if done || transcript != "" || truncated == nil || truncated.DeltaText != "keep " || len(truncated.TimedTranscript) != 1 {
		t.Fatalf("truncated timestamps = audio:%+v done:%v transcript:%q, want shortest zip", truncated, done, transcript)
	}

	finished, done, transcript, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"done"}`), 24000)
	if err != nil {
		t.Fatalf("done message: %v", err)
	}
	if finished == nil || !finished.IsFinal || !done || transcript != "" {
		t.Fatalf("finished=%+v done=%v transcript=%q, want final marker", finished, done, transcript)
	}
	if finished.Frame != nil {
		t.Fatalf("final marker frame = %+v, want boundary-only marker", finished.Frame)
	}

	if _, _, _, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"error","message":"bad text"}`), 24000); err == nil {
		t.Fatal("error message returned nil error, want stream error")
	} else {
		var apiErr *llm.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error message error = %T %v, want APIError", err, err)
		}
		if apiErr.Message != "Rime ws error: bad text" {
			t.Fatalf("APIError message = %q, want reference message", apiErr.Message)
		}
	}

	if _, _, _, err := rimeTTSAudioFromWebsocketMessage([]byte(`{"type":"error"}`), 24000); err == nil {
		t.Fatal("empty error message returned nil error, want stream error")
	} else if err.Error() != "Rime ws error: (no message)" {
		t.Fatalf("empty error message = %q, want reference fallback", err)
	}
}

func TestRimeTTSAudioFromWebsocketMalformedPayloadReturnsAPIConnectionError(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{
			name:    "malformed json",
			payload: []byte(`{`),
		},
		{
			name:    "malformed audio",
			payload: []byte(`{"type":"chunk","data":"not-base64"}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := rimeTTSAudioFromWebsocketMessage(tc.payload, 22050)
			if err == nil {
				t.Fatal("error = nil, want APIConnectionError")
			}
			var connErr *llm.APIConnectionError
			if !errors.As(err, &connErr) {
				t.Fatalf("error = %T %v, want APIConnectionError", err, err)
			}
		})
	}
}

func queryMap(values map[string][]string) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		if len(value) > 0 {
			out[key] = value[0]
		}
	}
	return out
}

func assertRimePayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

type rimeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f rimeRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type rimeCloseCountBody struct {
	*bytes.Reader
	closeCount int
}

func (b *rimeCloseCountBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("closed twice")
	}
	return nil
}

type rimeChunkedBody struct {
	chunks [][]byte
	index  int
}

func (b *rimeChunkedBody) Read(p []byte) (int, error) {
	if b.index >= len(b.chunks) {
		return 0, io.EOF
	}
	chunk := b.chunks[b.index]
	b.index++
	return copy(p, chunk), nil
}

func (b *rimeChunkedBody) Close() error { return nil }

type rimeFinalEOFReader struct {
	data []byte
	done bool
}

func (r *rimeFinalEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("read after final eof")
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func (r *rimeFinalEOFReader) Close() error { return nil }

type rimeAudioThenErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *rimeAudioThenErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), r.err
}

func (r *rimeAudioThenErrorReader) Close() error { return nil }

type rimeErrorReader struct{}

func (rimeErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("rime read failed")
}

func (rimeErrorReader) Close() error { return nil }

type rimeTimeoutReader struct{}

func (rimeTimeoutReader) Read([]byte) (int, error) {
	return 0, context.DeadlineExceeded
}

func (rimeTimeoutReader) Close() error { return nil }

type rimeNetTimeoutReader struct{}

func (rimeNetTimeoutReader) Read([]byte) (int, error) {
	return 0, rimeNetTimeoutError{}
}

func (rimeNetTimeoutReader) Close() error { return nil }

type rimeNetTimeoutError struct{}

func (rimeNetTimeoutError) Error() string   { return "i/o timeout" }
func (rimeNetTimeoutError) Timeout() bool   { return true }
func (rimeNetTimeoutError) Temporary() bool { return true }
