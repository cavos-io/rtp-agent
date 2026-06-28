package groq

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestGroqTTSDefaultsMatchReference(t *testing.T) {
	provider := NewGroqTTS("test-key", "")

	if provider.baseURL != "https://api.groq.com/openai/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "canopylabs/orpheus-v1-english" {
		t.Fatalf("model = %q, want reference default model", provider.model)
	}
	if provider.voice != "autumn" {
		t.Fatalf("voice = %q, want reference default voice", provider.voice)
	}
	if provider.responseFormat != "wav" {
		t.Fatalf("response format = %q, want wav", provider.responseFormat)
	}
	if provider.SampleRate() != 48000 {
		t.Fatalf("sample rate = %d, want 48000", provider.SampleRate())
	}
	if got := tts.Model(provider); got != "canopylabs/orpheus-v1-english" {
		t.Fatalf("model metadata = %q, want reference model", got)
	}
	if got := tts.Provider(provider); got != "Groq" {
		t.Fatalf("provider metadata = %q, want Groq", got)
	}
}

func TestNewGroqTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "env-key")

	provider := NewGroqTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildGroqTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("authorization = %q, want env bearer token", got)
	}

	explicit := NewGroqTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestGroqTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewGroqTTS("test-key", "")

	req, err := buildGroqTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.groq.com/openai/v1/audio/speech" {
		t.Fatalf("url = %q, want audio speech endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertGroqTTSPayload(t, payload, "model", "canopylabs/orpheus-v1-english")
	assertGroqTTSPayload(t, payload, "voice", "autumn")
	assertGroqTTSPayload(t, payload, "input", "hello")
	assertGroqTTSPayload(t, payload, "response_format", "wav")
}

func TestGroqTTSOptionsMatchReference(t *testing.T) {
	provider := NewGroqTTS("test-key", "",
		WithGroqTTSBaseURL("https://groq.example/openai/v1/"),
		WithGroqTTSModel("canopylabs/orpheus-arabic-saudi"),
		WithGroqTTSVoice("noura"),
	)

	req, err := buildGroqTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://groq.example/openai/v1/audio/speech" {
		t.Fatalf("url = %q, want custom audio speech endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertGroqTTSPayload(t, payload, "model", "canopylabs/orpheus-arabic-saudi")
	assertGroqTTSPayload(t, payload, "voice", "noura")
}

func TestGroqTTSUpdateOptionsMatchReference(t *testing.T) {
	provider := NewGroqTTS("test-key", "",
		WithGroqTTSModel("canopylabs/orpheus-v1-english"),
		WithGroqTTSVoice("autumn"),
	)

	provider.UpdateOptions("canopylabs/orpheus-arabic-saudi", "fahad")

	req, err := buildGroqTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertGroqTTSPayload(t, payload, "model", "canopylabs/orpheus-arabic-saudi")
	assertGroqTTSPayload(t, payload, "voice", "fahad")
}

func TestGroqTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "")
	provider := NewGroqTTS("", "", WithGroqTTSBaseURL("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GROQ_API_KEY") {
		t.Fatalf("error = %q, want GROQ_API_KEY guidance", err)
	}
}

func TestGroqTTSRejectsNonAudioResponse(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"not audio"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewGroqTTS("test-key", "", WithGroqTTSBaseURL("https://groq.example/openai/v1"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize returned nil error, want non-audio response error")
	}
	if !strings.Contains(err.Error(), "non-audio") {
		t.Fatalf("error = %q, want non-audio guidance", err)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Synthesize error = %T %v, want APIError", err, err)
	}
	if apiErr.Body != `{"error":"not audio"}` {
		t.Fatalf("APIError body = %#v, want provider body", apiErr.Body)
	}
	if !apiErr.Retryable {
		t.Fatal("APIError retryable = false, want true")
	}
}

func TestGroqTTSAcceptsReferenceAudioPrefixContentType(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audioevil"}},
			Body:       io.NopCloser(bytes.NewReader(groqTestWAV([]byte{0x01, 0x00}, 48000, 1))),
			Request:    r,
		}, nil
	})}

	provider := NewGroqTTS("test-key", "", WithGroqTTSBaseURL("https://groq.example/openai/v1"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error = %v, want reference audio prefix accepted", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error = %v, want decoded WAV audio", err)
	}
	if audio == nil || audio.IsFinal || len(audio.Frame.Data) == 0 {
		t.Fatalf("audio = %#v, want decoded non-final frame", audio)
	}
}

func TestGroqTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewGroqTTS("test-key", "", WithGroqTTSBaseURL("https://groq.example/openai/v1"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize returned nil error, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
}

func TestGroqTTSSynthesizeClientClosedStatusReturnsEOF(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	body := &groqReadTrackingCloser{}
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 499,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       body,
			Request:    r,
		}, nil
	})}

	provider := NewGroqTTS("test-key", "", WithGroqTTSBaseURL("https://groq.example/openai/v1"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	if audio, err := stream.Next(); audio != nil || err != io.EOF {
		t.Fatalf("Next = (%#v, %v), want nil, io.EOF for reference client-closed status", audio, err)
	}
	if body.reads != 0 {
		t.Fatalf("body reads = %d, want 0 for client-closed status cleanup", body.reads)
	}
	if body.closed != 1 {
		t.Fatalf("body closes = %d, want 1", body.closed)
	}
}

func TestGroqTTSSynthesizeTransportErrorsMatchReference(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr any
	}{
		{
			name:    "connection",
			err:     errors.New("dial failed"),
			wantErr: &llm.APIConnectionError{},
		},
		{
			name:    "timeout",
			err:     context.DeadlineExceeded,
			wantErr: &llm.APITimeoutError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalClient := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = originalClient })
			http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return nil, tt.err
			})}

			provider := NewGroqTTS("test-key", "", WithGroqTTSBaseURL("https://groq.example/openai/v1"))

			stream, err := provider.Synthesize(context.Background(), "hello")
			if err == nil {
				defer stream.Close()
				t.Fatal("Synthesize returned nil error, want transport error")
			}
			switch tt.wantErr.(type) {
			case *llm.APITimeoutError:
				var timeoutErr *llm.APITimeoutError
				if !errors.As(err, &timeoutErr) {
					t.Fatalf("Synthesize error = %T %v, want APITimeoutError", err, err)
				}
			case *llm.APIConnectionError:
				var connectionErr *llm.APIConnectionError
				if !errors.As(err, &connectionErr) {
					t.Fatalf("Synthesize error = %T %v, want APIConnectionError", err, err)
				}
			}
		})
	}
}

func TestGroqTTSProviderCloseClosesActiveStreams(t *testing.T) {
	body := &groqCloseCountBody{Reader: bytes.NewReader(groqTestWAV([]byte{0x01, 0x00}, 48000, 1))}
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/wav"}},
			Body:       body,
			Request:    r,
		}, nil
	})}

	provider := NewGroqTTS("test-key", "", WithGroqTTSBaseURL("https://groq.example/openai/v1"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	if err := tts.Close(provider); err != nil {
		t.Fatalf("tts.Close error = %v", err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close calls = %d, want 1", body.closeCount)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after provider Close error = %T %v, want EOF", err, err)
	}
	if err := tts.Close(provider); err != nil {
		t.Fatalf("second tts.Close error = %v", err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close calls after second Close = %d, want 1", body.closeCount)
	}
}

func TestGroqTTSProviderCloseCancelsPendingSynthesize(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	requests := make(chan *http.Request, 1)
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests <- r
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}

	provider := NewGroqTTS("test-key", "", WithGroqTTSBaseURL("https://groq.example/openai/v1"))
	errCh := make(chan error, 1)
	go func() {
		stream, err := provider.Synthesize(context.Background(), "hello")
		if stream != nil {
			_ = stream.Close()
		}
		errCh <- err
	}()

	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("Synthesize did not start provider request")
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Synthesize after provider Close error = %T %v, want io.ErrClosedPipe", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("Synthesize remained blocked after provider Close")
	}
}

func TestGroqTTSSynthesizeCallerCancelReturnsContextCanceled(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	requests := make(chan *http.Request, 1)
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests <- r
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}

	provider := NewGroqTTS("test-key", "", WithGroqTTSBaseURL("https://groq.example/openai/v1"))
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		stream, err := provider.Synthesize(ctx, "hello")
		if stream != nil {
			_ = stream.Close()
		}
		errCh <- err
	}()

	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("Synthesize did not start provider request")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Synthesize canceled error = %T %v, want context.Canceled", err, err)
		}
		var connectionErr *llm.APIConnectionError
		if errors.As(err, &connectionErr) {
			t.Fatalf("Synthesize canceled error = %T, want raw context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Synthesize remained blocked after caller cancellation")
	}
}

func TestGroqTTSChunkedStreamFailuresReturnAPIConnectionError(t *testing.T) {
	truncated := groqTestWAV([]byte{0x01, 0x00, 0x02, 0x00}, 48000, 1)
	truncated = truncated[:len(truncated)-1]
	tests := []struct {
		name string
		body []byte
	}{
		{name: "invalid header", body: []byte("not wav")},
		{name: "truncated data", body: truncated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &groqTTSChunkedStream{
				resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(tt.body))},
				sampleRate: 48000,
			}

			audio, err := stream.Next()
			if audio != nil {
				t.Fatalf("Next audio = %#v, want nil on malformed provider stream", audio)
			}
			var connectionErr *llm.APIConnectionError
			if !errors.As(err, &connectionErr) {
				t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
			}
		})
	}
}

func TestGroqTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(groqTestWAV([]byte{0x01, 0x00, 0x02, 0x00}, 24000, 1)))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
	if audio.Frame.SamplesPerChannel != 4 {
		t.Fatalf("samples per channel = %d, want resampled 48 kHz duration", audio.Frame.SamplesPerChannel)
	}
}

func TestGroqTTSChunkedStreamNormalizesReferenceMonoOutput(t *testing.T) {
	stereoPCM := []byte{
		0x02, 0x00, 0x04, 0x00,
		0x06, 0x00, 0x08, 0x00,
	}
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(groqTestWAV(stereoPCM, 48000, 2)))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want mono output", audio.Frame.NumChannels)
	}
	want := []byte{0x03, 0x00, 0x07, 0x00}
	if !bytes.Equal(audio.Frame.Data, want) {
		t.Fatalf("frame data = %#v, want downmixed mono %#v", audio.Frame.Data, want)
	}
}

func TestGroqTTSChunkedStreamDecodesReferenceWAVResponse(t *testing.T) {
	pcm := []byte{0x01, 0x00, 0x02, 0x00}
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(groqTestWAV(pcm, 48000, 1)))},
		sampleRate: 48000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, pcm) {
		t.Fatalf("frame data = %#v, want decoded PCM %#v", audio.Frame.Data, pcm)
	}
	if audio.Frame.SampleRate != 48000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame shape = rate %d channels %d samples %d, want 48000/1/2", audio.Frame.SampleRate, audio.Frame.NumChannels, audio.Frame.SamplesPerChannel)
	}

	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestGroqTTSChunkedStreamEmitsIncrementalWAVChunks(t *testing.T) {
	firstPCM := []byte{0x01, 0x00, 0x02, 0x00}
	secondPCM := []byte{0x03, 0x00, 0x04, 0x00}
	body := &groqChunkedReadCloser{
		reader: bytes.NewReader(groqTestWAV(append(append([]byte(nil), firstPCM...), secondPCM...), 48000, 1)),
		limit:  len(firstPCM),
	}
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 48000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, firstPCM) {
		t.Fatalf("first frame data = %#v, want first provider chunk %#v", audio.Frame.Data, firstPCM)
	}

	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, secondPCM) {
		t.Fatalf("second frame data = %#v, want second provider chunk %#v", audio.Frame.Data, secondPCM)
	}
}

func TestGroqTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(groqTestWAV([]byte{0x01, 0x00}, 48000, 1)))},
		sampleRate: 48000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if audio == nil || audio.IsFinal {
		t.Fatalf("first audio = %#v, want non-final decoded audio", audio)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}

	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error before final marker: %v", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker err = %v, want EOF", err)
	}
}

func TestGroqTTSSynthesizeSetsStableRequestID(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/wav"}},
			Body:       io.NopCloser(bytes.NewReader(groqTestWAV([]byte{0x01, 0x00}, 48000, 1))),
			Request:    r,
		}, nil
	})}

	provider := NewGroqTTS("test-key", "", WithGroqTTSBaseURL("https://groq.example/openai/v1"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error = %v", err)
	}
	if audio.RequestID == "" {
		t.Fatal("audio RequestID is empty, want reference stable request id")
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", final)
	}
	if final.RequestID != audio.RequestID {
		t.Fatalf("final RequestID = %q, want stable request id %q", final.RequestID, audio.RequestID)
	}
}

func TestGroqTTSChunkedStreamClosesBodyAfterFinal(t *testing.T) {
	body := &groqCloseCountBody{Reader: bytes.NewReader(groqTestWAV([]byte{0x01, 0x00}, 48000, 1))}
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 48000,
		requestID:  "req-1",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error = %v", err)
	}
	if audio == nil || audio.IsFinal {
		t.Fatalf("first audio = %#v, want decoded audio", audio)
	}
	if body.closeCount != 0 {
		t.Fatalf("body Close() calls after audio = %d, want 0", body.closeCount)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", final)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close() calls after final = %d, want 1", body.closeCount)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after final returned error = %v", err)
	}
	if body.closeCount != 1 {
		t.Fatalf("body Close() calls after idempotent Close = %d, want 1", body.closeCount)
	}
}

func TestGroqTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(nil))},
		sampleRate: 48000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error before final marker: %v", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("first audio = %#v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker err = %v, want EOF", err)
	}
}

func TestGroqTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &groqCloseCountBody{Reader: bytes.NewReader(groqTestWAV([]byte{0x01, 0x02}, 48000, 1))}
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 48000,
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

func TestGroqTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	stream := &groqTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(groqTestWAV([]byte{0x01, 0x02}, 48000, 1)))},
		sampleRate: 48000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %T %v, want EOF", err, err)
	}
}

func assertGroqTTSPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func groqTestWAV(pcm []byte, sampleRate uint32, channels uint16) []byte {
	var wav bytes.Buffer
	byteRate := sampleRate * uint32(channels) * 2
	blockAlign := channels * 2
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36+len(pcm)))
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, channels)
	_ = binary.Write(&wav, binary.LittleEndian, sampleRate)
	_ = binary.Write(&wav, binary.LittleEndian, byteRate)
	_ = binary.Write(&wav, binary.LittleEndian, blockAlign)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)))
	wav.Write(pcm)
	return wav.Bytes()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type groqCloseCountBody struct {
	*bytes.Reader
	closeCount int
}

func (b *groqCloseCountBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("closed twice")
	}
	return nil
}

type groqReadTrackingCloser struct {
	reads  int
	closed int
}

func (r *groqReadTrackingCloser) Read([]byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

func (r *groqReadTrackingCloser) Close() error {
	r.closed++
	return nil
}

type groqChunkedReadCloser struct {
	reader *bytes.Reader
	limit  int
	reads  int
}

func (r *groqChunkedReadCloser) Read(p []byte) (int, error) {
	if len(p) > r.limit {
		p = p[:r.limit]
	}
	r.reads++
	return r.reader.Read(p)
}

func (r *groqChunkedReadCloser) Close() error {
	return nil
}
