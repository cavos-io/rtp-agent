package rime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if body, ok := statusErr.Body.(string); !ok || body != `{"error":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
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

func TestRimeTTSUpdateOptionsRejectsReferenceMistV2TimeScaleFactor(t *testing.T) {
	provider := NewRimeTTS("test-key", "", WithRimeTTSModel("coda"), WithRimeTTSTimeScaleFactor(1.1))

	err := provider.UpdateOptions(WithRimeTTSModel("mistv2"))
	if err == nil || !strings.Contains(err.Error(), "time_scale_factor is not supported by the mistv2 model") {
		t.Fatalf("UpdateOptions error = %v, want reference mistv2 time_scale_factor error", err)
	}
	if provider.Model() != "coda" {
		t.Fatalf("model after rejected update = %q, want unchanged coda", provider.Model())
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

func TestRimeTTSNonAudioResponseEndsLikeReference(t *testing.T) {
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
	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %T %v, want EOF", err, err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close() calls = %d, want 1", body.closeCount)
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

func TestRimeTTSStreamEmptyFlushEmitsReferenceFinalMarker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
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
			t.Fatal("empty Flush closed connection, want stream remain open")
			return nil
		},
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("Next = %#v, want boundary-only final marker", audio)
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
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want empty-turn final marker", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("Next audio = %+v, want final marker", audio)
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
		provider: NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true)),
		conn:     conn,
		events:   make(chan *tts.SynthesizedAudio, 1),
		errCh:    make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("readLoop error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != websocket.CloseUnsupportedData {
			t.Fatalf("StatusCode = %d, want close code", statusErr.StatusCode)
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
		provider: NewRimeTTS("test-key", "", WithRimeTTSWebsocket(true)),
		conn:     conn,
		events:   make(chan *tts.SynthesizedAudio, 1),
		errCh:    make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("readLoop error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != websocket.CloseNormalClosure {
			t.Fatalf("StatusCode = %d, want normal close code", statusErr.StatusCode)
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
