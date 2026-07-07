package speechmatics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestSpeechmaticsTTSDefaultsMatchReference(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key")

	if provider.voice != "sarah" {
		t.Fatalf("voice = %q, want sarah", provider.voice)
	}
	if got := tts.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := tts.Provider(provider); got != "Speechmatics" {
		t.Fatalf("provider metadata = %q, want Speechmatics", got)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.baseURL != "https://preview.tts.speechmatics.com" {
		t.Fatalf("base URL = %q, want preview endpoint", provider.baseURL)
	}
}

func TestNewSpeechmaticsTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SPEECHMATICS_API_KEY", "env-key")

	provider := NewSpeechmaticsTTS("")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSpeechmaticsTTS("explicit-key")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSpeechmaticsTTSSynthesizeRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SPEECHMATICS_API_KEY", "")
	originalClient := http.DefaultClient
	requests := 0
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Request:    r,
		}, nil
	})}

	provider := NewSpeechmaticsTTS("")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize stream = %#v, want nil without API key", stream)
	}
	if err == nil || !strings.Contains(err.Error(), "SPEECHMATICS_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want no provider request before API key validation", requests)
	}
}

func TestSpeechmaticsTTSProviderCloseClosesLazyStreamsBeforeRequest(t *testing.T) {
	originalClient := http.DefaultClient
	requests := 0
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
			Request:    r,
		}, nil
	})}

	provider := NewSpeechmaticsTTS("test-key")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := tts.Close(provider); err != nil {
		t.Fatalf("provider Close error = %v", err)
	}
	if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after provider Close = (%+v, %v), want EOF", audio, err)
	}
	if requests != 0 {
		t.Fatalf("requests after provider Close before Next = %d, want none", requests)
	}
}

func TestSpeechmaticsTTSSynthesizeRequestUsesReferenceOptions(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key",
		WithSpeechmaticsTTSVoice("theo"),
		WithSpeechmaticsTTSSampleRate(24000),
		WithSpeechmaticsTTSBaseURL("https://tts.example.com"),
	)

	req, err := buildSpeechmaticsTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	if req.URL.Scheme != "https" || req.URL.Host != "tts.example.com" || req.URL.Path != "/generate/theo" {
		t.Fatalf("url = %s, want https://tts.example.com/generate/theo", req.URL.String())
	}
	query := req.URL.Query()
	assertSpeechmaticsTTSQuery(t, query, "output_format", "pcm_24000")
	if query.Get("sm-sdk") == "" {
		t.Fatal("sm-sdk query parameter is empty")
	}
	if query.Get("sm-app") == "" {
		t.Fatal("sm-app query parameter is empty")
	}
	assertSpeechmaticsTTSQuery(t, query, "sm-sdk", "livekit-plugins-1.5.19.rc1")
	assertSpeechmaticsTTSQuery(t, query, "sm-app", "livekit/0.2.8")

	var payload map[string]string
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if payload["text"] != "hello" {
		t.Fatalf("text = %q, want hello", payload["text"])
	}
}

func TestSpeechmaticsTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key")

	provider.UpdateOptions(
		WithSpeechmaticsTTSVoice("theo"),
	)

	if provider.voice != "theo" {
		t.Fatalf("voice = %q, want theo", provider.voice)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want unchanged 16000", provider.sampleRate)
	}
}

func TestSpeechmaticsTTSAllowsReferenceEmptyVoice(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key", WithSpeechmaticsTTSVoice(""))
	if provider.voice != "" {
		t.Fatalf("constructor voice = %q, want explicit empty reference voice", provider.voice)
	}

	provider = NewSpeechmaticsTTS("test-key", WithSpeechmaticsTTSVoice("theo"))
	provider.UpdateOptions(WithSpeechmaticsTTSVoice(""))
	if provider.voice != "" {
		t.Fatalf("updated voice = %q, want explicit empty reference voice", provider.voice)
	}

	req, err := buildSpeechmaticsTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.Path != "/generate/" {
		t.Fatalf("request path = %q, want reference empty voice path", req.URL.Path)
	}
}

func TestSpeechmaticsTTSUpdateOptionsPreservesReferenceSampleRate(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key")

	provider.UpdateOptions(WithSpeechmaticsTTSSampleRate(24000))

	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want unchanged 16000", provider.sampleRate)
	}
}

func TestSpeechmaticsTTSUpdateOptionsPreservesReferenceBaseURL(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key", WithSpeechmaticsTTSBaseURL("https://tts.example.com"))

	provider.UpdateOptions(WithSpeechmaticsTTSBaseURL("https://changed.example.com"))

	if provider.baseURL != "https://tts.example.com" {
		t.Fatalf("base URL = %q, want constructor value like reference", provider.baseURL)
	}
	req, err := buildSpeechmaticsTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.Host != "tts.example.com" {
		t.Fatalf("request host = %q, want constructor route", req.URL.Host)
	}
}

func TestSpeechmaticsTTSRequestPreservesReferenceBaseURLPath(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key",
		WithSpeechmaticsTTSVoice("theo"),
		WithSpeechmaticsTTSBaseURL("https://tts.example.com/"),
	)

	req, err := buildSpeechmaticsTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.Path != "//generate/theo" {
		t.Fatalf("request path = %q, want reference base_url concatenation path", req.URL.Path)
	}
}

func TestSpeechmaticsTTSRequestPreservesReferenceVoicePath(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key",
		WithSpeechmaticsTTSVoice("custom/voice"),
		WithSpeechmaticsTTSBaseURL("https://tts.example.com"),
	)

	req, err := buildSpeechmaticsTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.Path != "/generate/custom/voice" {
		t.Fatalf("request path = %q, want reference voice path concatenation", req.URL.Path)
	}
	if req.URL.EscapedPath() != "/generate/custom/voice" {
		t.Fatalf("escaped request path = %q, want unescaped reference voice path", req.URL.EscapedPath())
	}
}

func TestSpeechmaticsTTSSynthesizePostsAndStreamsPCM(t *testing.T) {
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "tts.example.com" || r.URL.Path != "/generate/theo" {
			t.Fatalf("url = %s, want https://tts.example.com/generate/theo", r.URL.String())
		}
		if got := r.URL.Query().Get("output_format"); got != "pcm_24000" {
			t.Fatalf("output_format = %q, want pcm_24000", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if payload["text"] != "hello" {
			t.Fatalf("text = %q, want hello", payload["text"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02, 0x03, 0x04})),
		}, nil
	})}
	defer func() {
		http.DefaultClient = originalClient
	}()

	provider := NewSpeechmaticsTTS("test-key",
		WithSpeechmaticsTTSVoice("theo"),
		WithSpeechmaticsTTSSampleRate(24000),
		WithSpeechmaticsTTSBaseURL("https://tts.example.com"),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("samples per channel = %d, want 2", audio.Frame.SamplesPerChannel)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("frame data = %#v, want complete PCM bytes", audio.Frame.Data)
	}
	if audio.RequestID == "" {
		t.Fatal("audio RequestID is empty, want reference request id")
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next final returned error: %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final audio = %+v, want final marker", final)
	}
	if final.RequestID != audio.RequestID {
		t.Fatalf("final RequestID = %q, want stable request id %q", final.RequestID, audio.RequestID)
	}
}

func TestSpeechmaticsTTSSynthesizeDefersReferenceRequestUntilNext(t *testing.T) {
	originalClient := http.DefaultClient
	requests := 0
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider := NewSpeechmaticsTTS("test-key")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	if requests != 0 {
		t.Fatalf("requests after Synthesize = %d, want 0 before Next", requests)
	}

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests after Next = %d, want 1", requests)
	}
}

func TestSpeechmaticsTTSSynthesizeEmptyTextStillFlushesReferenceFinal(t *testing.T) {
	originalClient := http.DefaultClient
	requests := 0
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if payload["text"] != "" {
			t.Fatalf("text = %q, want empty text payload", payload["text"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider := NewSpeechmaticsTTS("test-key")
	stream, err := provider.Synthesize(context.Background(), "")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("Next() = %+v, want final marker", audio)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want provider request for empty text", requests)
	}
}

func TestSpeechmaticsTTSSynthesizeNonEmptyTextErrorsWithoutAudio(t *testing.T) {
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider := NewSpeechmaticsTTS("test-key")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next() audio = %+v, want nil", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next() error = %T %v, want APIError", err, err)
	}
	if !strings.Contains(err.Error(), "no audio frames were pushed for text: hello") {
		t.Fatalf("Next() error = %v, want no-audio text", err)
	}
}

func TestSpeechmaticsTTSSynthesizeAcceptsReferenceNoContentStatus(t *testing.T) {
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider := NewSpeechmaticsTTS("test-key")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next() audio = %+v, want nil", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next() error = %T %v, want no-audio APIError after accepted 2xx status", err, err)
	}
	var statusErr *llm.APIStatusError
	if errors.As(err, &statusErr) {
		t.Fatalf("Next() error = %T %v, want accepted 2xx response to reach no-audio handling", err, err)
	}
	if !strings.Contains(err.Error(), "no audio frames were pushed for text: hello") {
		t.Fatalf("Next() error = %v, want no-audio text", err)
	}
}

func TestSpeechmaticsTTSSynthesizeAppliesReferenceRequestTimeout(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("request context has no deadline, want reference 30s synth timeout")
		}
		remaining := time.Until(deadline)
		if remaining < 25*time.Second || remaining > 31*time.Second {
			t.Fatalf("request timeout = %s, want about 30s", remaining)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
			Request:    r,
		}, nil
	})}

	provider := NewSpeechmaticsTTS("test-key")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
}

func TestSpeechmaticsTTSSynthesizeStartsReferenceTimeoutAtRequest(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	chunked, ok := stream.(*speechmaticsTTSChunkedStream)
	if !ok {
		t.Fatalf("stream type = %T, want Speechmatics chunked stream", stream)
	}
	if deadline, ok := chunked.ctx.Deadline(); ok {
		t.Fatalf("stream context deadline = %v before request, want timeout applied when Next starts provider request", deadline)
	}
}

func TestSpeechmaticsTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"rate limited"}`))),
			Request:    r,
		}, nil
	})}

	provider := NewSpeechmaticsTTS("test-key")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v, want deferred stream", err)
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

func TestSpeechmaticsTTSSynthesizeClientClosedStatusReturnsEOF(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 499,
			Body:       io.NopCloser(bytes.NewReader([]byte(`client closed`))),
			Request:    r,
		}, nil
	})}

	provider := NewSpeechmaticsTTS("test-key")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil", audio)
	}
	if err != io.EOF {
		t.Fatalf("Next error = %v, want EOF for reference client-closed status", err)
	}
}

func TestSpeechmaticsTTSSynthesizeTimeoutReturnsAPITimeoutError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}

	provider := NewSpeechmaticsTTS("test-key")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v, want deferred stream", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestSpeechmaticsTTSChunkedStreamReadErrorReturnsAPIConnectionError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       speechmaticsReadErrorBody{},
			Request:    r,
		}, nil
	})}

	provider := NewSpeechmaticsTTS("test-key")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v, want deferred stream", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestSpeechmaticsTTSChunkedStreamSurfacesReadErrorAfterAudio(t *testing.T) {
	stream := &speechmaticsTTSChunkedStream{
		stream:     &speechmaticsDataThenErrorBody{data: []byte{0x01, 0x02}},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v, want audio before provider read error", err)
	}
	if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02}) {
		t.Fatalf("first Next = %+v, want provider audio bytes", audio)
	}

	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("second Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestSpeechmaticsTTSChunkedStreamBuffersPartialSamples(t *testing.T) {
	stream := &speechmaticsTTSChunkedStream{
		stream:     io.NopCloser(&chunkedReader{chunks: [][]byte{{0x01}, {0x02, 0x03}}}),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02}) {
		t.Fatalf("first frame data = %#v, want buffered complete sample", audio.Frame.Data)
	}
	if audio.Frame.SamplesPerChannel != 1 {
		t.Fatalf("first samples per channel = %d, want 1", audio.Frame.SamplesPerChannel)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker with trailing partial byte discarded", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("second Next = %+v, want final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestSpeechmaticsTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	body := &speechmaticsCloseCountBody{reader: bytes.NewReader([]byte{0x01, 0x02})}
	stream := &speechmaticsTTSChunkedStream{
		stream:     body,
		sampleRate: 24000,
	}

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
		t.Fatalf("final Next = %+v, want final marker", final)
	}
	if got, want := body.closeCount, 1; got != want {
		t.Fatalf("response body close count after final = %d, want %d", got, want)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestSpeechmaticsTTSChunkedStreamDiscardsPartialEOFRead(t *testing.T) {
	stream := &speechmaticsTTSChunkedStream{
		stream:     io.NopCloser(partialEOFReader{}),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final marker with trailing partial sample discarded", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("Next = %+v, want final marker without partial sample frame", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestSpeechmaticsTTSChunkedStreamKeepsAudioReturnedWithEOF(t *testing.T) {
	stream := &speechmaticsTTSChunkedStream{
		stream:     io.NopCloser(&finalEOFReader{data: []byte{0x01, 0x02}}),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v, want final audio bytes", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first Next = %+v, want non-final audio", audio)
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02}) {
		t.Fatalf("audio data = %v, want final EOF bytes", audio.Frame.Data)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second Next = %+v, want boundary-only final marker", final)
	}
	if audio, err := stream.Next(); audio != nil || err != io.EOF {
		t.Fatalf("third Next = (%+v, %v), want EOF", audio, err)
	}
}

func TestSpeechmaticsTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &speechmaticsTTSChunkedStream{
		stream:     io.NopCloser(bytes.NewReader(nil)),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("Next = %+v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestSpeechmaticsTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &speechmaticsCloseCountBody{reader: bytes.NewReader([]byte{0x01, 0x02})}
	stream := &speechmaticsTTSChunkedStream{
		stream:     body,
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if got, want := body.closeCount, 1; got != want {
		t.Fatalf("close count = %d, want %d", got, want)
	}

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next after Close audio = %+v, want nil", audio)
	}
	if err != io.EOF {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestSpeechmaticsTTSChunkedStreamCloseCancelsPendingRequest(t *testing.T) {
	originalClient := http.DefaultClient
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		http.DefaultClient = originalClient
	})
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(entered)
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-release:
			return nil, errors.New("released without request cancellation")
		}
	})}

	provider := NewSpeechmaticsTTS("test-key")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}

	type nextResult struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan nextResult, 1)
	go func() {
		audio, err := stream.Next()
		done <- nextResult{audio: audio, err: err}
	}()

	select {
	case <-entered:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Next did not start Speechmatics request")
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	select {
	case result := <-done:
		if result.audio != nil {
			t.Fatalf("Next after Close audio = %+v, want nil", result.audio)
		}
		if result.err != io.EOF {
			t.Fatalf("Next after Close error = %v, want EOF", result.err)
		}
	case <-time.After(500 * time.Millisecond):
		releaseOnce.Do(func() { close(release) })
		t.Fatal("Close did not cancel pending Speechmatics request")
	}
}

func TestSpeechmaticsTTSChunkedStreamCloseCancelsActiveResponse(t *testing.T) {
	body := newSpeechmaticsBlockingBody()
	stream := &speechmaticsTTSChunkedStream{
		stream:     body,
		cancel:     func() {},
		sampleRate: 24000,
	}

	type nextResult struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	done := make(chan nextResult, 1)
	go func() {
		audio, err := stream.Next()
		done <- nextResult{audio: audio, err: err}
	}()

	select {
	case <-body.readStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Next did not start reading active Speechmatics response")
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	select {
	case result := <-done:
		if result.audio != nil {
			t.Fatalf("Next after active response Close audio = %+v, want nil", result.audio)
		}
		if result.err != io.EOF {
			t.Fatalf("Next after active response Close error = %v, want EOF", result.err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close did not cancel active Speechmatics response read")
	}
}

func assertSpeechmaticsTTSQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

type chunkedReader struct {
	chunks [][]byte
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	copy(p, chunk)
	r.chunks = r.chunks[1:]
	return len(chunk), nil
}

type partialEOFReader struct{}

func (partialEOFReader) Read(p []byte) (int, error) {
	p[0] = 0x01
	return 1, io.EOF
}

type finalEOFReader struct {
	data []byte
	done bool
}

func (r *finalEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("read after final eof")
	}
	r.done = true
	copy(p, r.data)
	return len(r.data), io.EOF
}

type speechmaticsCloseCountBody struct {
	reader     *bytes.Reader
	closeCount int
}

func (b *speechmaticsCloseCountBody) Read(p []byte) (int, error) {
	if b.closeCount > 0 {
		return 0, errors.New("read after close")
	}
	return b.reader.Read(p)
}

func (b *speechmaticsCloseCountBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("already closed")
	}
	return nil
}

type speechmaticsReadErrorBody struct{}

func (speechmaticsReadErrorBody) Read([]byte) (int, error) {
	return 0, errors.New("provider read failed")
}

func (speechmaticsReadErrorBody) Close() error {
	return nil
}

type speechmaticsDataThenErrorBody struct {
	data []byte
	done bool
}

func (b *speechmaticsDataThenErrorBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	b.done = true
	copy(p, b.data)
	return len(b.data), errors.New("provider read failed")
}

func (b *speechmaticsDataThenErrorBody) Close() error {
	return nil
}

type speechmaticsBlockingBody struct {
	readStarted chan struct{}
	closeDone   chan struct{}
	closeOnce   sync.Once
}

func newSpeechmaticsBlockingBody() *speechmaticsBlockingBody {
	return &speechmaticsBlockingBody{
		readStarted: make(chan struct{}),
		closeDone:   make(chan struct{}),
	}
}

func (b *speechmaticsBlockingBody) Read([]byte) (int, error) {
	b.closeOnce.Do(func() { close(b.readStarted) })
	<-b.closeDone
	return 0, errors.New("response closed")
}

func (b *speechmaticsBlockingBody) Close() error {
	close(b.closeDone)
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
