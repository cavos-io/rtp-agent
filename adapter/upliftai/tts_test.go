package upliftai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
)

type upliftAIFinalEOFReader struct {
	data []byte
	done bool
}

func (r *upliftAIFinalEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("read after final eof")
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func (r *upliftAIFinalEOFReader) Close() error { return nil }

type upliftAIFinalErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *upliftAIFinalErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(p, r.data), r.err
}

func (r *upliftAIFinalErrorReader) Close() error { return nil }

type upliftAIErrorReader struct{}

func (upliftAIErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("upliftai read failed")
}

func (upliftAIErrorReader) Close() error { return nil }

type upliftAIReadErrorBody struct {
	err error
}

func (b upliftAIReadErrorBody) Read([]byte) (int, error) { return 0, b.err }
func (b upliftAIReadErrorBody) Close() error             { return nil }

type upliftAICloseUnblocksReadBody struct {
	readEntered chan struct{}
	closed      chan struct{}
	data        []byte
	err         error
	once        sync.Once
	offset      int
}

func newUpliftAICloseUnblocksReadBody(err error) *upliftAICloseUnblocksReadBody {
	return &upliftAICloseUnblocksReadBody{
		readEntered: make(chan struct{}),
		closed:      make(chan struct{}),
		err:         err,
	}
}

func newUpliftAICloseUnblocksAudioReadBody(data []byte) *upliftAICloseUnblocksReadBody {
	body := newUpliftAICloseUnblocksReadBody(io.EOF)
	body.data = data
	return body
}

func (b *upliftAICloseUnblocksReadBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.readEntered) })
	<-b.closed
	if b.offset < len(b.data) {
		n := copy(p, b.data[b.offset:])
		b.offset += n
		return n, nil
	}
	return 0, b.err
}

func (b *upliftAICloseUnblocksReadBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

type upliftAICloseErrorBody struct {
	err error
}

func (b upliftAICloseErrorBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b upliftAICloseErrorBody) Close() error             { return b.err }

type upliftAIChunkReader struct {
	chunks [][]byte
}

func (r *upliftAIChunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return copy(p, chunk), nil
}

func (r *upliftAIChunkReader) Close() error { return nil }

type upliftAIBlockingEOFBody struct {
	data   []byte
	offset int
	closed chan struct{}
	once   sync.Once
}

func newUpliftAIBlockingEOFBody(data []byte) *upliftAIBlockingEOFBody {
	return &upliftAIBlockingEOFBody{data: data, closed: make(chan struct{})}
}

func (b *upliftAIBlockingEOFBody) Read(p []byte) (int, error) {
	if b.offset < len(b.data) {
		n := copy(p, b.data[b.offset:])
		b.offset += n
		return n, nil
	}
	<-b.closed
	return 0, io.EOF
}

func (b *upliftAIBlockingEOFBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func newUpliftAITestHTTPProvider(apiKey string, voice string, opts ...UpliftAITTSOption) *UpliftAITTS {
	baseOpts := []UpliftAITTSOption{WithUpliftAIBaseURL("https://upliftai.example/v1/tts")}
	baseOpts = append(baseOpts, opts...)
	return NewUpliftAITTS(apiKey, voice, baseOpts...)
}

func TestUpliftAIPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.upliftai" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.upliftai", PluginTitle)
	}
	if PluginVersion == "" {
		t.Fatalf("PluginVersion = %q, want non-empty project release version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.upliftai" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.upliftai", PluginPackage)
	}
}

func TestUpliftAITTSReferenceDefaultsAndCapabilities(t *testing.T) {
	tts := NewUpliftAITTS("secret", "")
	if tts.apiKey != "secret" {
		t.Fatalf("apiKey = %q, want secret", tts.apiKey)
	}
	if got, want := tts.voice, "v_meklc281"; got != want {
		t.Fatalf("voice = %q, want reference default voice %q", got, want)
	}
	if got, want := tts.outputFormat, "MP3_22050_32"; got != want {
		t.Fatalf("outputFormat = %q, want reference default output format %q", got, want)
	}
	if got, want := tts.baseURL, "wss://api.upliftai.org"; got != want {
		t.Fatalf("baseURL = %q, want reference websocket base URL %q", got, want)
	}
	if got, want := tts.Label(), "upliftai.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if caps := tts.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
	if got, want := tts.SampleRate(), 22050; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := tts.NumChannels(), 1; got != want {
		t.Fatalf("NumChannels() = %d, want %d", got, want)
	}

	stream, err := tts.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v, want stream adapter", err)
	}
	if stream == nil {
		t.Fatal("Stream() = nil, want stream adapter")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Stream Close error = %v", err)
	}
}

func TestUpliftAITTSConfiguredNumChannelsControlsWAVOutput(t *testing.T) {
	stereoPCM := []byte{
		0x01, 0x00, 0x02, 0x00,
		0x03, 0x00, 0x04, 0x00,
	}
	provider := newUpliftAITestHTTPProvider(
		"test-key",
		"",
		WithUpliftAIOutputFormat("WAV_22050_16"),
		WithUpliftAINumChannels(2),
	)
	if got, want := provider.NumChannels(), 2; got != want {
		t.Fatalf("NumChannels() = %d, want configured reference channel count %d", got, want)
	}
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: io.NopCloser(bytes.NewReader(upliftAITestWAV(stereoPCM, 22050, 2)))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want decoded stereo WAV frame", audio)
	}
	if got, want := audio.Frame.NumChannels, uint32(2); got != want {
		t.Fatalf("frame channels = %d, want configured reference channel count %d", got, want)
	}
	if got, want := audio.Frame.SamplesPerChannel, uint32(2); got != want {
		t.Fatalf("samples per channel = %d, want %d", got, want)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, stereoPCM) {
		t.Fatalf("audio data = %#v, want stereo PCM unchanged %#v", got, stereoPCM)
	}
}

func TestUpliftAITTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("UPLIFTAI_API_KEY", "env-secret")

	tts := NewUpliftAITTS("", "")

	if got, want := tts.apiKey, "env-secret"; got != want {
		t.Fatalf("apiKey = %q, want environment key %q", got, want)
	}
}

func TestUpliftAITTSUpdateOptionsChangesReferenceVoice(t *testing.T) {
	oldClient := http.DefaultClient
	var gotVoice string
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var payload map[string]string
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		gotVoice = payload["voiceId"]
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	tts := NewUpliftAITTS("test-key", "")
	stream, err := tts.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	tts.UpdateOptions(WithUpliftAIUpdateVoiceID("voice-updated"))

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if gotVoice != "voice-updated" {
		t.Fatalf("request voice = %q, want updated voice", gotVoice)
	}
}

func TestUpliftAITTSUsesEnvironmentBaseURL(t *testing.T) {
	t.Setenv("UPLIFTAI_BASE_URL", "https://upliftai.example/tts")

	tts := NewUpliftAITTS("secret", "")

	if got, want := tts.baseURL, "https://upliftai.example/tts"; got != want {
		t.Fatalf("baseURL = %q, want environment base URL %q", got, want)
	}
}

func TestUpliftAITTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("UPLIFTAI_API_KEY", "")
	tts := NewUpliftAITTS("", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tts.Synthesize(ctx, "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "UPLIFTAI_API_KEY") {
		t.Fatalf("Synthesize error = %q, want UPLIFTAI_API_KEY guidance", err)
	}
}

func TestUpliftAITTSStreamRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("UPLIFTAI_API_KEY", "")
	provider := NewUpliftAITTS("", "", WithUpliftAIBaseURL("https://upliftai.example/v1/tts"))

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil without API key", stream)
	}
	if err == nil {
		t.Fatal("Stream error = nil, want missing API key error")
	}
	if !strings.Contains(err.Error(), "UPLIFTAI_API_KEY") {
		t.Fatalf("Stream error = %q, want UPLIFTAI_API_KEY guidance", err)
	}
}

func TestUpliftAITTSProviderCloseClosesActiveStreams(t *testing.T) {
	oldClient := http.DefaultClient
	body := &upliftAICloseCountBody{reader: strings.NewReader("audio")}
	var httpCalls int
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       body,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	if stream == nil {
		t.Fatal("Synthesize stream = nil, want active stream")
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if httpCalls != 0 {
		t.Fatalf("HTTP calls before stream consumption = %d, want 0", httpCalls)
	}
	if got, want := body.closeCount, 0; got != want {
		t.Fatalf("active unconsumed stream close count = %d, want %d", got, want)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if got, want := body.closeCount, 0; got != want {
		t.Fatalf("second provider Close close count = %d, want %d", got, want)
	}
}

func TestUpliftAITTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
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

func TestUpliftAITTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("upliftai transport failed")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error before stream consumption = %v", err)
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

func TestUpliftAITTSSynthesizeRequestDeadlineReturnsAPITimeoutError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error before stream consumption = %v", err)
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

func TestUpliftAITTSSynthesizeRequestCancelReturnsContextCanceled(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream, err := provider.Synthesize(ctx, "hello")
	if err != nil {
		t.Fatalf("Synthesize error before stream consumption = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next error = %T(%v), want context.Canceled for caller cancellation", err, err)
	}
}

func TestUpliftAITTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error before stream consumption = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusTooManyRequests)
	}
	if statusErr.Body != `{"error":"rate limited"}` {
		t.Fatalf("Body = %#v, want provider body", statusErr.Body)
	}
}

func TestUpliftAITTSSynthesizeDefersReferenceRequestUntilNext(t *testing.T) {
	var httpCalls int
	var requestBody map[string]string
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		httpCalls++
		if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("\x01\x02\x03\x04")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "")
	provider.outputFormat = "PCM_22050_16"

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	if httpCalls != 0 {
		t.Fatalf("HTTP calls before Next = %d, want 0", httpCalls)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("Next audio = %#v, want first audio frame", audio)
	}
	if httpCalls != 1 {
		t.Fatalf("HTTP calls after Next = %d, want 1", httpCalls)
	}
	if got, want := requestBody["text"], "hello"; got != want {
		t.Fatalf("request text = %q, want %q", got, want)
	}
	if got, want := requestBody["voiceId"], defaultUpliftAIVoiceID; got != want {
		t.Fatalf("request voiceId = %q, want %q", got, want)
	}
	if got, want := requestBody["outputFormat"], "PCM_22050_16"; got != want {
		t.Fatalf("request outputFormat = %q, want %q", got, want)
	}
}

func TestUpliftAITTSSynthesizeUsesReferenceRouteAndOptions(t *testing.T) {
	var requestBody map[string]string
	var requestPath string
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestPath = req.URL.Path
		if auth := req.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("Authorization = %q, want bearer test-key", auth)
		}
		if contentType := req.Header.Get("Content-Type"); contentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", contentType)
		}
		if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request body: %v", err)
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("https://upliftai.example/custom-tts"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
		WithUpliftAIPhraseReplacementConfigID("phrases-1"),
	)
	stream, err := provider.Synthesize(context.Background(), "hello route")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want frame", audio)
	}
	if requestPath != "/custom-tts" {
		t.Fatalf("request path = %q, want /custom-tts", requestPath)
	}
	if got, want := requestBody["text"], "hello route"; got != want {
		t.Fatalf("request text = %q, want %q", got, want)
	}
	if got, want := requestBody["voiceId"], "voice-1"; got != want {
		t.Fatalf("request voiceId = %q, want %q", got, want)
	}
	if got, want := requestBody["outputFormat"], "PCM_22050_16"; got != want {
		t.Fatalf("request outputFormat = %q, want %q", got, want)
	}
	if got, want := requestBody["phraseReplacementConfigId"], "phrases-1"; got != want {
		t.Fatalf("request phraseReplacementConfigId = %q, want %q", got, want)
	}
}

func TestUpliftAITTSUpdateOptionsAffectsFutureRequests(t *testing.T) {
	var requestBody map[string]string
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("https://upliftai.example/custom-tts"),
		WithUpliftAIOutputFormat("MP3_22050_32"),
	)
	provider.UpdateOptions(
		WithUpliftAIUpdateVoiceID(""),
		WithUpliftAIUpdateOutputFormat("PCM_22050_16"),
	)
	stream, err := provider.Synthesize(context.Background(), "updated")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want frame", audio)
	}
	if got, want := requestBody["text"], "updated"; got != want {
		t.Fatalf("request text = %q, want %q", got, want)
	}
	if got, want := requestBody["voiceId"], ""; got != want {
		t.Fatalf("request voiceId = %q, want explicit empty voice", got)
	}
	if got, want := requestBody["outputFormat"], "PCM_22050_16"; got != want {
		t.Fatalf("request outputFormat = %q, want %q", got, want)
	}
}

func TestUpliftAITTSRejectsUnsupportedReferenceOutputFormatBeforeRequest(t *testing.T) {
	for _, outputFormat := range []string{"BAD_FORMAT", "MP3_BAD", "OGG_BAD"} {
		t.Run(outputFormat, func(t *testing.T) {
			var httpCalls int
			oldClient := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
				httpCalls++
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("\x01\x02")),
				}, nil
			})}
			t.Cleanup(func() { http.DefaultClient = oldClient })

			provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat(outputFormat))
			stream, err := provider.Synthesize(context.Background(), "bad format")
			if err != nil {
				t.Fatalf("Synthesize error = %v", err)
			}
			defer stream.Close()

			audio, err := stream.Next()
			if audio != nil {
				t.Fatalf("Next audio = %#v, want nil for unsupported output format", audio)
			}
			if err == nil {
				t.Fatal("Next error = nil, want unsupported output format error")
			}
			var connErr *llm.APIConnectionError
			if !errors.As(err, &connErr) {
				t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
			}
			if !strings.Contains(err.Error(), "unsupported output format: "+outputFormat) {
				t.Fatalf("Next error = %q, want reference unsupported output format message", err.Error())
			}
			if httpCalls != 0 {
				t.Fatalf("HTTP calls = %d, want 0 before rejecting unsupported output format", httpCalls)
			}
		})
	}
}

func TestUpliftAITTSStreamAfterCloseIsRejected(t *testing.T) {
	provider := newUpliftAITestHTTPProvider("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestUpliftAITTSProviderCloseClosesActiveSynthesizeStreams(t *testing.T) {
	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText before provider Close error = %v", err)
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close error = %v", err)
	}
	if err := stream.PushText("again"); err != nil {
		t.Fatalf("PushText after provider Close error = %v, want reference no-op", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after provider Close error = %v, want reference no-op", err)
	}
	if audio, err := stream.Next(); audio != nil || err != io.EOF {
		t.Fatalf("Next after provider Close = (%#v, %v), want EOF", audio, err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream Close after provider Close error = %v", err)
	}
}

func TestUpliftAITTSStreamPushTextAfterEndInputIsReferenceNoop(t *testing.T) {
	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	ending, ok := stream.(interface{ EndInput() error })
	if !ok {
		t.Fatal("UpliftAI stream does not implement EndInput")
	}

	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	if err := stream.PushText("ignored after end"); err != nil {
		t.Fatalf("PushText after EndInput error = %v, want reference no-op", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after EndInput error = %v, want reference no-op", err)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("second EndInput error = %v, want idempotent no-op", err)
	}
	if audio, err := stream.Next(); audio != nil || err != io.EOF {
		t.Fatalf("Next after empty EndInput = (%#v, %v), want EOF", audio, err)
	}
}

func TestUpliftAITTSStreamFlushSynthesizesReferenceSegment(t *testing.T) {
	var httpCalls int
	var requestBody map[string]string
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		httpCalls++
		if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("\x01\x02\x03\x04")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	provider.outputFormat = "PCM_22050_16"

	if err := stream.PushText("hello "); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.PushText("world"); err != nil {
		t.Fatalf("PushText(second) error = %v", err)
	}
	if httpCalls != 0 {
		t.Fatalf("HTTP calls before Flush = %d, want 0", httpCalls)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	upliftAIWaitForCondition(t, func() bool { return httpCalls == 1 }, "provider request after Flush")

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first audio = %#v, want non-final frame", audio)
	}
	if got, want := audio.Frame.SampleRate, uint32(22050); got != want {
		t.Fatalf("SampleRate = %d, want %d", got, want)
	}
	if got, want := audio.Frame.NumChannels, uint32(1); got != want {
		t.Fatalf("NumChannels = %d, want %d", got, want)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", final)
	}
	if got, want := httpCalls, 1; got != want {
		t.Fatalf("HTTP calls after stream Flush = %d, want %d", got, want)
	}
	if got, want := requestBody["text"], "hello world"; got != want {
		t.Fatalf("request text = %q, want %q", got, want)
	}
	if got, want := requestBody["voiceId"], defaultUpliftAIVoiceID; got != want {
		t.Fatalf("request voiceId = %q, want %q", got, want)
	}
	if got, want := requestBody["outputFormat"], "PCM_22050_16"; got != want {
		t.Fatalf("request outputFormat = %q, want %q", got, want)
	}
}

func TestUpliftAITTSStreamFormatsPushedWordsLikeReference(t *testing.T) {
	var requestBody map[string]string
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("\x01\x02")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("PCM_22050_16"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello,\n"); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.PushText("  world"); err != nil {
		t.Fatalf("PushText(second) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if audio, err := stream.Next(); err != nil || audio == nil || audio.Frame == nil {
		t.Fatalf("Next = (%#v, %v), want audio frame", audio, err)
	}
	if got, want := requestBody["text"], "hello, world"; got != want {
		t.Fatalf("request text = %q, want reference formatted text %q", got, want)
	}
}

func TestUpliftAITTSStreamUsesReferenceSentenceTokenizer(t *testing.T) {
	var requestBody map[string]string
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("\x01\x02")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider(
		"test-key",
		"",
		WithUpliftAIOutputFormat("PCM_22050_16"),
		WithUpliftAISentenceTokenizer(upliftAIFixedSentenceTokenizer{tokens: []string{"custom sentence"}}),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("raw text ignored by tokenizer"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if audio, err := stream.Next(); err != nil || audio == nil || audio.Frame == nil {
		t.Fatalf("Next = (%#v, %v), want audio frame", audio, err)
	}
	if got, want := requestBody["text"], "custom sentence"; got != want {
		t.Fatalf("request text = %q, want custom reference tokenizer output %q", got, want)
	}
}

func TestUpliftAITTSStreamUsesReferenceWordTokenizerFormat(t *testing.T) {
	var requestBody map[string]string
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("\x01\x02")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider(
		"test-key",
		"",
		WithUpliftAIOutputFormat("PCM_22050_16"),
		WithUpliftAIWordTokenizer(upliftAIFixedWordTokenizer{
			tokens:    []string{"alpha", "beta"},
			formatted: "alpha|beta",
		}),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("raw text ignored by word tokenizer"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if audio, err := stream.Next(); err != nil || audio == nil || audio.Frame == nil {
		t.Fatalf("Next = (%#v, %v), want audio frame", audio, err)
	}
	if got, want := requestBody["text"], "alpha|beta"; got != want {
		t.Fatalf("request text = %q, want custom reference word formatter output %q", got, want)
	}
}

func TestUpliftAITTSStreamTokenizerOptionsUseReferenceLastValue(t *testing.T) {
	var requestBody map[string]string
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("\x01\x02")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider(
		"test-key",
		"",
		WithUpliftAIOutputFormat("PCM_22050_16"),
		WithUpliftAIWordTokenizer(upliftAIFixedWordTokenizer{
			tokens:    []string{"word"},
			formatted: "word formatter",
		}),
		WithUpliftAISentenceTokenizer(upliftAIFixedSentenceTokenizer{tokens: []string{"later sentence tokenizer"}}),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("raw text"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if audio, err := stream.Next(); err != nil || audio == nil || audio.Frame == nil {
		t.Fatalf("Next = (%#v, %v), want audio frame", audio, err)
	}
	if got, want := requestBody["text"], "later sentence tokenizer"; got != want {
		t.Fatalf("request text = %q, want last configured tokenizer output %q", got, want)
	}
}

func TestUpliftAITTSStreamStopsAfterReferenceSegmentError(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		if httpCalls == 1 {
			return nil, errors.New("first segment failed")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("\x01\x02\x03\x04")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("PCM_22050_16"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("broken"); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(first) error = %v", err)
	}

	nextErrCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		nextErrCh <- err
	}()
	select {
	case err = <-nextErrCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for reference segment failure to terminate stream")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T(%v), want reference APIConnectionError after segment failure", err, err)
	}
	if got, want := httpCalls, 1; got != want {
		t.Fatalf("HTTP calls = %d, want one failed segment request", got)
	}
	if audio, err := stream.Next(); audio != nil || err != io.EOF {
		t.Fatalf("Next after stream failure = (%#v, %v), want terminal EOF", audio, err)
	}
	if got, want := httpCalls, 1; got != want {
		t.Fatalf("HTTP calls after terminal failure = %d, want no second segment request", got)
	}
}

func TestUpliftAITTSStreamNoAudioKeepsReferenceAPIError(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("PCM_22050_16"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("silent segment"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil for no-audio segment", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T(%v), want reference APIError for no-audio segment", err, err)
	}
	var connectionErr *llm.APIConnectionError
	if errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T(%v), want base APIError not APIConnectionError for no-audio segment", err, err)
	}
	if !strings.Contains(err.Error(), "no audio frames were pushed for text: silent segment") {
		t.Fatalf("Next error = %q, want reference no-audio message", err.Error())
	}
	if got, want := httpCalls, 1; got != want {
		t.Fatalf("HTTP calls = %d, want one silent segment request", got)
	}
}

func TestUpliftAITTSStreamSegmentCancelReturnsContextCanceled(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: upliftAIRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       upliftAIReadErrorBody{err: context.Canceled},
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("PCM_22050_16"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("interrupted segment"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next error = %T(%v), want context.Canceled for reference segment cancellation", err, err)
	}
}

func TestUpliftAITTSStreamIgnoresReferenceSecondSegment(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	oldDial := upliftAISocketIODialContext
	var dials int
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		dials++
		if dials > 1 {
			return nil, fmt.Errorf("socket.io dial count = %d, want one connection for one accepted stream segment", dials)
		}
		return conn, nil
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	defer provider.Close()

	if err := stream.PushText("first segment"); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(first) error = %v", err)
	}
	_ = receiveUpliftAITestString(t, conn.writes, "namespace connect packet")
	firstSynthesize := receiveUpliftAITestString(t, conn.writes, "first synthesize packet")
	firstRequestID := upliftAITestSocketIORequestID(t, firstSynthesize)
	sendUpliftAITestSocketIOAudio(conn, firstRequestID, []byte{0x01, 0x02, 0x03, 0x04})
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + firstRequestID + `"}]`
	audio, err := stream.Next()
	if err != nil || audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first segment Next = (%#v, %v), want audio frame", audio, err)
	}
	requireUpliftAITestFinal(t, stream, "first segment")

	if err := stream.PushText("second segment"); err != nil {
		t.Fatalf("PushText(second) error = %v, want reference no-op", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(second) error = %v, want reference no-op", err)
	}
	select {
	case packet := <-conn.writes:
		t.Fatalf("second segment wrote synthesize packet %q, want reference second segment ignored", packet)
	case <-time.After(100 * time.Millisecond):
	}
	if err := stream.PushText("third segment"); err != nil {
		t.Fatalf("PushText(third) error = %v, want reference no-op", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(third) error = %v, want reference no-op", err)
	}
	select {
	case packet := <-conn.writes:
		t.Fatalf("third segment wrote synthesize packet %q, want reference later segments ignored", packet)
	case <-time.After(100 * time.Millisecond):
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after second segment error = %v", err)
	}
	if got, want := dials, 1; got != want {
		t.Fatalf("socket.io dial count = %d, want one connection for first accepted segment", got)
	}
	if audio, err := stream.Next(); audio != nil || err != io.EOF {
		t.Fatalf("Next after ignored second segment close = (%#v, %v), want EOF", audio, err)
	}
}

func TestUpliftAITTSChunkedStreamUsesReferenceSocketIOTransport(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(ctx context.Context, endpoint string) (upliftAISocketIOConn, error) {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		if parsed.Scheme != "ws" || parsed.Host != "upliftai.example" || parsed.Path != "/base/socket.io/" {
			return nil, fmt.Errorf("endpoint = %q, want socket.io endpoint under configured base path", endpoint)
		}
		if parsed.Query().Get("EIO") != "4" || parsed.Query().Get("transport") != "websocket" || parsed.Query().Get("keep") != "1" {
			return nil, fmt.Errorf("endpoint query = %s, want EIO=4&transport=websocket plus existing query", parsed.RawQuery)
		}
		return conn, nil
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example/base?keep=1"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
		WithUpliftAIPhraseReplacementConfigID("phrases-1"),
	)
	stream, err := provider.Synthesize(context.Background(), "hello socket")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	resultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()

	connect := receiveUpliftAITestString(t, conn.writes, "namespace connect packet")
	if connect != `40/text-to-speech/multi-stream,{"token":"test-key"}` {
		t.Fatalf("connect packet = %q, want reference namespace auth packet", connect)
	}
	synthesize := receiveUpliftAITestString(t, conn.writes, "synthesize packet")
	if !strings.HasPrefix(synthesize, `42/text-to-speech/multi-stream,`) {
		t.Fatalf("synthesize packet = %q, want reference namespace event packet", synthesize)
	}
	requestID := upliftAITestSocketIORequestID(t, synthesize)
	audioPayload := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04})
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio","requestId":"` + requestID + `","audio":"` + audioPayload + `"}]`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + requestID + `"}]`

	result := receiveUpliftAITestSocketIOResult(t, resultCh)
	audio, err := result.audio, result.err
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want socket.io audio frame", audio)
	}
	if got, want := audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}; !bytes.Equal(got, want) {
		t.Fatalf("audio data = %#v, want socket.io audio %#v", got, want)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", final)
	}

	var event []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(synthesize, `42/text-to-speech/multi-stream,`)), &event); err != nil {
		t.Fatalf("decode synthesize event: %v", err)
	}
	if len(event) != 2 || string(event[0]) != `"synthesize"` {
		t.Fatalf("synthesize event = %s, want synthesize event name and payload", synthesize)
	}
	var payload map[string]string
	if err := json.Unmarshal(event[1], &payload); err != nil {
		t.Fatalf("decode synthesize payload: %v", err)
	}
	if payload["type"] != "synthesize" || payload["text"] != "hello socket" ||
		payload["voiceId"] != "voice-1" || payload["outputFormat"] != "PCM_22050_16" ||
		payload["phraseReplacementConfigId"] != "phrases-1" || payload["requestId"] == "" {
		t.Fatalf("synthesize payload = %#v, want reference fields", payload)
	}
}

func TestUpliftAITTSChunkedStreamDecodesReferenceNoisySocketIOAudio(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	stream, err := provider.Synthesize(context.Background(), "hello socket")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	resultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()

	_ = receiveUpliftAITestString(t, conn.writes, "namespace connect packet")
	synthesize := receiveUpliftAITestString(t, conn.writes, "synthesize packet")
	requestID := upliftAITestSocketIORequestID(t, synthesize)
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio","requestId":"` + requestID + `","audio":"AQID !!\nBA=="}]`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + requestID + `"}]`

	result := receiveUpliftAITestSocketIOResult(t, resultCh)
	if result.err != nil {
		t.Fatalf("Next error = %v", result.err)
	}
	if result.audio == nil || result.audio.Frame == nil {
		t.Fatalf("audio = %#v, want socket.io audio frame", result.audio)
	}
	if got, want := result.audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}; !bytes.Equal(got, want) {
		t.Fatalf("audio data = %#v, want decoded noisy socket.io audio %#v", got, want)
	}
}

func TestUpliftAITTSChunkedStreamIgnoresReferenceMalformedSocketIOAudio(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	stream, err := provider.Synthesize(context.Background(), "hello socket")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	resultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()

	_ = receiveUpliftAITestString(t, conn.writes, "namespace connect packet")
	synthesize := receiveUpliftAITestString(t, conn.writes, "synthesize packet")
	requestID := upliftAITestSocketIORequestID(t, synthesize)
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio","requestId":"` + requestID + `","audio":"abc"}]`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio","requestId":"` + requestID + `","audio":"AQIDBA=="}]`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + requestID + `"}]`

	result := receiveUpliftAITestSocketIOResult(t, resultCh)
	if result.err != nil {
		t.Fatalf("Next error = %v, want reference to keep request alive after malformed socket.io audio", result.err)
	}
	if result.audio == nil || result.audio.Frame == nil {
		t.Fatalf("audio = %#v, want valid audio after malformed socket.io audio", result.audio)
	}
	if got, want := result.audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}; !bytes.Equal(got, want) {
		t.Fatalf("audio data = %#v, want later valid socket.io audio %#v", got, want)
	}
}

func TestUpliftAITTSChunkedStreamSocketIOErrorEventWithoutAudioReturnsNoAudioError(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	stream, err := provider.Synthesize(context.Background(), "provider error")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	resultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()

	_ = receiveUpliftAITestString(t, conn.writes, "namespace connect packet")
	synthesize := receiveUpliftAITestString(t, conn.writes, "synthesize packet")
	requestID := upliftAITestSocketIORequestID(t, synthesize)
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"error","requestId":"` + requestID + `","message":"provider diagnostic"}]`

	result := receiveUpliftAITestSocketIOResult(t, resultCh)
	requireUpliftAITestNoAudioError(t, result, "provider error event")
}

func TestUpliftAITTSChunkedStreamWaitsForSocketIOReadyBeforeSynthesize(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	stream, err := provider.Synthesize(context.Background(), "ready first")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	resultCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		resultCh <- err
	}()

	if connect := receiveUpliftAITestString(t, conn.writes, "namespace connect packet"); connect != `40/text-to-speech/multi-stream,{"token":"test-key"}` {
		t.Fatalf("connect packet = %q, want reference namespace auth packet", connect)
	}
	select {
	case packet := <-conn.writes:
		t.Fatalf("socket.io wrote %q before ready message", packet)
	case <-time.After(50 * time.Millisecond):
	}
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	synthesize := receiveUpliftAITestString(t, conn.writes, "synthesize packet after ready")
	if !strings.HasPrefix(synthesize, `42/text-to-speech/multi-stream,`) {
		t.Fatalf("synthesize packet = %q, want namespace event after ready", synthesize)
	}
	requestID := upliftAITestSocketIORequestID(t, synthesize)
	sendUpliftAITestSocketIOAudio(conn, requestID, []byte{0x01, 0x02, 0x03, 0x04})
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + requestID + `"}]`
	if err := receiveUpliftAITestError(t, resultCh, "stream Next completion"); err != nil {
		t.Fatalf("Next error = %v", err)
	}
}

func TestUpliftAITTSChunkedStreamFallsBackAfterSocketIONamespaceConnect(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	conn.readTimeout = 6 * time.Second
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	stream, err := provider.Synthesize(context.Background(), "fallback")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	resultCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		resultCh <- err
	}()
	if connect := receiveUpliftAITestString(t, conn.writes, "namespace connect packet"); connect != `40/text-to-speech/multi-stream,{"token":"test-key"}` {
		t.Fatalf("connect packet = %q, want reference namespace auth packet", connect)
	}
	synthesize := receiveUpliftAITestStringWithin(t, conn.writes, "synthesize packet after namespace fallback", 7*time.Second)
	if !strings.HasPrefix(synthesize, `42/text-to-speech/multi-stream,`) {
		t.Fatalf("synthesize packet = %q, want namespace event after fallback", synthesize)
	}
	requestID := upliftAITestSocketIORequestID(t, synthesize)
	sendUpliftAITestSocketIOAudio(conn, requestID, []byte{0x01, 0x02, 0x03, 0x04})
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + requestID + `"}]`
	if err := receiveUpliftAITestError(t, resultCh, "stream Next completion"); err != nil {
		t.Fatalf("Next error = %v", err)
	}
}

func TestUpliftAITTSChunkedStreamSocketIODialUsesReferenceTimeout(t *testing.T) {
	oldDial := upliftAISocketIODialContext
	var sawDeadline bool
	upliftAISocketIODialContext = func(ctx context.Context, endpoint string) (upliftAISocketIOConn, error) {
		deadline, ok := ctx.Deadline()
		sawDeadline = ok
		if !ok {
			return nil, errors.New("missing dial deadline")
		}
		remaining := time.Until(deadline)
		if remaining < 9*time.Second || remaining > 10*time.Second {
			return nil, fmt.Errorf("deadline remaining = %s, want about 10s", remaining)
		}
		return nil, errors.New("stop after deadline check")
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS("test-key", "", WithUpliftAIBaseURL("ws://upliftai.example"))
	stream, err := provider.Synthesize(context.Background(), "timeout")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want dial error after deadline check")
	}
	if !sawDeadline {
		t.Fatal("socket.io dial context had no reference timeout deadline")
	}
}

func TestUpliftAITTSChunkedStreamSocketIOConnectReadUsesReferenceTimeout(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.readTimeout = time.Second
	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()
	stream, err := provider.Synthesize(ctx, "connect read timeout")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	resultCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		resultCh <- err
	}()
	select {
	case err := <-resultCh:
		var connectionErr *llm.APIConnectionError
		if !errors.As(err, &connectionErr) {
			t.Fatalf("Next error = %T(%v), want reference APIConnectionError for connect read timeout", err, err)
		}
	case <-time.After(100 * time.Millisecond):
		_ = conn.Close()
		t.Fatal("socket.io connect read ignored caller/reference timeout")
	}
}

func TestUpliftAITTSChunkedStreamSocketIODisconnectEmitsFinalMarker(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	stream, err := provider.Synthesize(context.Background(), "disconnect final")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	resultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()
	_ = receiveUpliftAITestString(t, conn.writes, "namespace connect packet")
	synthesize := receiveUpliftAITestString(t, conn.writes, "synthesize packet")
	requestID := upliftAITestSocketIORequestID(t, synthesize)
	audioPayload := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio","requestId":"` + requestID + `","audio":"` + audioPayload + `"}]`
	conn.readErrs <- io.EOF

	result := receiveUpliftAITestSocketIOResult(t, resultCh)
	if result.err != nil {
		t.Fatalf("first Next error = %v", result.err)
	}
	if result.audio == nil || result.audio.Frame == nil {
		t.Fatalf("first audio = %#v, want flushed audio after disconnect", result.audio)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker after disconnect", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("second audio = %#v, want final marker after disconnect", final)
	}
}

func TestUpliftAITTSChunkedStreamSocketIOAudioWaitWithoutAudioReturnsNoAudioError(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	conn.readTimeout = time.Second
	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	oldAudioWait := upliftAISocketIOAudioWait
	upliftAISocketIOAudioWait = 20 * time.Millisecond
	t.Cleanup(func() {
		upliftAISocketIODialContext = oldDial
		upliftAISocketIOAudioWait = oldAudioWait
	})

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()
	stream, err := provider.Synthesize(context.Background(), "audio timeout")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	resultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()
	_ = receiveUpliftAITestString(t, conn.writes, "namespace connect packet")
	_ = receiveUpliftAITestString(t, conn.writes, "synthesize packet")

	result := receiveUpliftAITestSocketIOResult(t, resultCh)
	requireUpliftAITestNoAudioError(t, result, "audio wait timeout")
}

func TestUpliftAITTSChunkedStreamSocketIOWriteFailureReconnectsNextRequest(t *testing.T) {
	firstConn := newUpliftAITestSocketIOConn()
	firstConn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	firstConn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	firstConn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	firstConn.writeErrAfter = 1
	firstConn.writeErr = io.ErrClosedPipe

	secondConn := newUpliftAITestSocketIOConn()
	secondConn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	secondConn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	secondConn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-2"}]`

	oldDial := upliftAISocketIODialContext
	dials := 0
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		dials++
		switch dials {
		case 1:
			return firstConn, nil
		case 2:
			return secondConn, nil
		default:
			return nil, fmt.Errorf("socket.io dial count = %d, want reconnect once", dials)
		}
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()

	firstStream, err := provider.Synthesize(context.Background(), "first")
	if err != nil {
		t.Fatalf("first Synthesize error = %v", err)
	}
	defer firstStream.Close()
	if _, err := firstStream.Next(); err == nil {
		t.Fatal("first Next error = nil, want synthesize write failure")
	}

	secondStream, err := provider.Synthesize(context.Background(), "second")
	if err != nil {
		t.Fatalf("second Synthesize error = %v", err)
	}
	defer secondStream.Close()
	secondResultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := secondStream.Next()
		secondResultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()
	_ = receiveUpliftAITestString(t, secondConn.writes, "second namespace connect packet")
	secondSynthesize := receiveUpliftAITestString(t, secondConn.writes, "second synthesize packet after reconnect")
	secondRequestID := upliftAITestSocketIORequestID(t, secondSynthesize)
	sendUpliftAITestSocketIOAudio(secondConn, secondRequestID, []byte{0x01, 0x02, 0x03, 0x04})
	secondConn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + secondRequestID + `"}]`
	result := receiveUpliftAITestSocketIOResult(t, secondResultCh)
	requireUpliftAITestSocketIOFrame(t, result, "second reconnect")
	requireUpliftAITestFinal(t, secondStream, "second reconnect")
	if got, want := dials, 2; got != want {
		t.Fatalf("socket.io dial count = %d, want reconnect after write failure", got)
	}
}

func TestUpliftAITTSChunkedStreamSocketIOSynthesizeWriteCancelReturnsContextCanceled(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	conn.writeErrAfter = 1
	conn.writeErr = context.Canceled

	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()
	stream, err := provider.Synthesize(context.Background(), "interrupted")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next error = %T(%v), want context.Canceled for caller cancellation", err, err)
	}
}

func TestUpliftAITTSChunkedStreamSocketIOPingWriteFailureReconnectsNextRequest(t *testing.T) {
	firstConn := newUpliftAITestSocketIOConn()
	firstConn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	firstConn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	firstConn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	firstConn.writeErrAfter = 2
	firstConn.writeErr = io.ErrClosedPipe

	secondConn := newUpliftAITestSocketIOConn()
	secondConn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	secondConn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	secondConn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-2"}]`

	oldDial := upliftAISocketIODialContext
	dials := 0
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		dials++
		switch dials {
		case 1:
			return firstConn, nil
		case 2:
			return secondConn, nil
		default:
			return nil, fmt.Errorf("socket.io dial count = %d, want reconnect once", dials)
		}
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()

	firstStream, err := provider.Synthesize(context.Background(), "first")
	if err != nil {
		t.Fatalf("first Synthesize error = %v", err)
	}
	defer firstStream.Close()
	firstResultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := firstStream.Next()
		firstResultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()
	_ = receiveUpliftAITestString(t, firstConn.writes, "first namespace connect packet")
	firstSynthesize := receiveUpliftAITestString(t, firstConn.writes, "first synthesize packet")
	firstRequestID := upliftAITestSocketIORequestID(t, firstSynthesize)
	sendUpliftAITestSocketIOAudio(firstConn, firstRequestID, []byte{0x01, 0x02, 0x03, 0x04})
	firstConn.reads <- "2"

	select {
	case <-firstConn.closed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("socket.io ping pong write failure left poisoned connection open")
	}
	firstResult := receiveUpliftAITestSocketIOResult(t, firstResultCh)
	requireUpliftAITestSocketIOFrame(t, firstResult, "first ping disconnect")
	requireUpliftAITestFinal(t, firstStream, "first ping disconnect")

	secondStream, err := provider.Synthesize(context.Background(), "second")
	if err != nil {
		t.Fatalf("second Synthesize error = %v", err)
	}
	defer secondStream.Close()
	secondResultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := secondStream.Next()
		secondResultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()
	_ = receiveUpliftAITestString(t, secondConn.writes, "second namespace connect packet")
	secondSynthesize := receiveUpliftAITestString(t, secondConn.writes, "second synthesize packet")
	secondRequestID := upliftAITestSocketIORequestID(t, secondSynthesize)
	sendUpliftAITestSocketIOAudio(secondConn, secondRequestID, []byte{0x05, 0x06, 0x07, 0x08})
	secondConn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + secondRequestID + `"}]`
	secondResult := receiveUpliftAITestSocketIOResult(t, secondResultCh)
	requireUpliftAITestSocketIOFrame(t, secondResult, "second ping reconnect")
	requireUpliftAITestFinal(t, secondStream, "second ping reconnect")
	if got, want := dials, 2; got != want {
		t.Fatalf("socket.io dial count = %d, want reconnect after ping write failure", got)
	}
}

func TestUpliftAITTSChunkedStreamSocketIODialRetriesReferenceReconnect(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`

	oldDial := upliftAISocketIODialContext
	oldDelay := upliftAISocketIOReconnectDelay
	dials := 0
	upliftAISocketIOReconnectDelay = time.Millisecond
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		dials++
		switch dials {
		case 1:
			return nil, errors.New("temporary socket.io dial failure")
		case 2:
			return conn, nil
		default:
			return nil, fmt.Errorf("socket.io dial count = %d, want one reference reconnect", dials)
		}
	}
	t.Cleanup(func() {
		upliftAISocketIODialContext = oldDial
		upliftAISocketIOReconnectDelay = oldDelay
	})

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()

	stream, err := provider.Synthesize(context.Background(), "retry")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	resultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()

	_ = receiveUpliftAITestString(t, conn.writes, "namespace connect packet after dial retry")
	synthesize := receiveUpliftAITestString(t, conn.writes, "synthesize packet after dial retry")
	requestID := upliftAITestSocketIORequestID(t, synthesize)
	sendUpliftAITestSocketIOAudio(conn, requestID, []byte{0x01, 0x02, 0x03, 0x04})
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + requestID + `"}]`
	result := receiveUpliftAITestSocketIOResult(t, resultCh)
	requireUpliftAITestSocketIOFrame(t, result, "dial retry")
	requireUpliftAITestFinal(t, stream, "dial retry")
	if got, want := dials, 2; got != want {
		t.Fatalf("socket.io dial count = %d, want reference retry count %d", got, want)
	}
}

func TestUpliftAITTSChunkedStreamSocketIOConnectPingWriteFailureIsAPIConnectionError(t *testing.T) {
	conns := make([]*upliftAITestSocketIOConn, upliftAISocketIOAttempts)
	for i := range conns {
		conn := newUpliftAITestSocketIOConn()
		conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
		conn.reads <- "2"
		conn.writeErrAfter = 1
		conn.writeErr = io.ErrClosedPipe
		conns[i] = conn
	}

	oldDial := upliftAISocketIODialContext
	oldDelay := upliftAISocketIOReconnectDelay
	dials := 0
	upliftAISocketIOReconnectDelay = time.Millisecond
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		dials++
		if dials > len(conns) {
			return nil, fmt.Errorf("socket.io dial count = %d, want %d attempts", dials, len(conns))
		}
		return conns[dials-1], nil
	}
	t.Cleanup(func() {
		upliftAISocketIODialContext = oldDial
		upliftAISocketIOReconnectDelay = oldDelay
	})

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()

	stream, err := provider.Synthesize(context.Background(), "connect fail")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T(%v), want reference APIConnectionError for connect ping write failure", err, err)
	}
	if got, want := dials, upliftAISocketIOAttempts; got != want {
		t.Fatalf("socket.io dial count = %d, want reference reconnect attempts %d", got, want)
	}
}

func TestUpliftAITTSChunkedStreamSocketIOAuthWriteCancelReturnsContextCanceled(t *testing.T) {
	oldDial := upliftAISocketIODialContext
	oldDelay := upliftAISocketIOReconnectDelay
	upliftAISocketIOReconnectDelay = 0
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		conn := newUpliftAITestSocketIOConn()
		conn.reads <- `2`
		conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
		conn.writeErrAfter = 1
		conn.writeErr = context.Canceled
		return conn, nil
	}
	t.Cleanup(func() {
		upliftAISocketIODialContext = oldDial
		upliftAISocketIOReconnectDelay = oldDelay
	})

	provider := NewUpliftAITTS(
		"test-key",
		"",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()
	stream, err := provider.Synthesize(context.Background(), "interrupted")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next error = %T(%v), want context.Canceled for caller cancellation", err, err)
	}
}

func TestUpliftAITTSChunkedStreamSocketIOReconnectDeadlineIsAPIConnectionError(t *testing.T) {
	oldDial := upliftAISocketIODialContext
	oldDelay := upliftAISocketIOReconnectDelay
	var dials int
	upliftAISocketIOReconnectDelay = time.Second
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		dials++
		return nil, errors.New("temporary socket.io dial failure")
	}
	t.Cleanup(func() {
		upliftAISocketIODialContext = oldDial
		upliftAISocketIOReconnectDelay = oldDelay
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()

	stream, err := provider.Synthesize(ctx, "connect timeout")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T(%v), want reference APIConnectionError for reconnect deadline", err, err)
	}
	if got, want := dials, 1; got != want {
		t.Fatalf("socket.io dial count = %d, want one attempt before caller deadline", got)
	}
}

func TestUpliftAITTSChunkedStreamCloseCancelsReferenceSocketIOConnect(t *testing.T) {
	oldDial := upliftAISocketIODialContext
	dialStarted := make(chan struct{})
	dialReleased := make(chan struct{})
	upliftAISocketIODialContext = func(ctx context.Context, _ string) (upliftAISocketIOConn, error) {
		close(dialStarted)
		<-ctx.Done()
		close(dialReleased)
		return nil, ctx.Err()
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()

	stream, err := provider.Synthesize(context.Background(), "cancel connect")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	resultCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		resultCh <- err
	}()
	select {
	case <-dialStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for socket.io dial to start")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close during socket.io connect error = %v", err)
	}
	select {
	case <-dialReleased:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close did not cancel reference socket.io connect")
	}
	select {
	case err := <-resultCh:
		if err != io.EOF {
			t.Fatalf("Next after Close during connect error = %v, want EOF", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next did not finish after Close canceled socket.io connect")
	}
}

func TestUpliftAITTSChunkedStreamSocketIODialCancelReturnsContextCanceled(t *testing.T) {
	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(ctx context.Context, _ string) (upliftAISocketIOConn, error) {
		return nil, ctx.Err()
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"",
		WithUpliftAIBaseURL("ws://upliftai.example"),
	)
	defer provider.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream, err := provider.Synthesize(ctx, "interrupted")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next error = %T(%v), want context.Canceled for caller cancellation", err, err)
	}
}

func TestUpliftAITTSChunkedStreamConnectedSocketIOCancelSkipsSynthesize(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	oldDial := upliftAISocketIODialContext
	oldAudioWait := upliftAISocketIOAudioWait
	upliftAISocketIOAudioWait = 20 * time.Millisecond
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	t.Cleanup(func() {
		upliftAISocketIODialContext = oldDial
		upliftAISocketIOAudioWait = oldAudioWait
	})

	provider := NewUpliftAITTS(
		"test-key",
		"",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()
	firstStream, err := provider.Synthesize(context.Background(), "warm")
	if err != nil {
		t.Fatalf("first Synthesize error = %v", err)
	}
	defer firstStream.Close()
	firstResultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := firstStream.Next()
		firstResultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()
	_ = receiveUpliftAITestString(t, conn.writes, "namespace connect packet")
	firstSynthesize := receiveUpliftAITestString(t, conn.writes, "first synthesize packet")
	firstRequestID := upliftAITestSocketIORequestID(t, firstSynthesize)
	sendUpliftAITestSocketIOAudio(conn, firstRequestID, []byte{0x01, 0x02, 0x03, 0x04})
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + firstRequestID + `"}]`
	firstResult := receiveUpliftAITestSocketIOResult(t, firstResultCh)
	requireUpliftAITestSocketIOFrame(t, firstResult, "warm connected socket")
	requireUpliftAITestFinal(t, firstStream, "warm connected socket")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	secondStream, err := provider.Synthesize(ctx, "interrupted")
	if err != nil {
		t.Fatalf("second Synthesize error = %v", err)
	}
	defer secondStream.Close()
	_, err = secondStream.Next()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("second Next error = %T(%v), want context.Canceled before synthesize emit", err, err)
	}
	select {
	case got := <-conn.writes:
		t.Fatalf("canceled synthesize packet = %q, want no provider write after caller cancellation", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUpliftAITTSChunkedStreamSocketIOReadErrorEndsRequestAndReconnects(t *testing.T) {
	firstConn := newUpliftAITestSocketIOConn()
	firstConn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	firstConn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	firstConn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`

	secondConn := newUpliftAITestSocketIOConn()
	secondConn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	secondConn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	secondConn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-2"}]`

	oldDial := upliftAISocketIODialContext
	dials := 0
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		dials++
		switch dials {
		case 1:
			return firstConn, nil
		case 2:
			return secondConn, nil
		default:
			return nil, fmt.Errorf("socket.io dial count = %d, want reconnect once", dials)
		}
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()

	firstStream, err := provider.Synthesize(context.Background(), "first")
	if err != nil {
		t.Fatalf("first Synthesize error = %v", err)
	}
	defer firstStream.Close()
	firstResultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := firstStream.Next()
		firstResultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()
	_ = receiveUpliftAITestString(t, firstConn.writes, "first namespace connect packet")
	firstSynthesize := receiveUpliftAITestString(t, firstConn.writes, "first synthesize packet")
	firstRequestID := upliftAITestSocketIORequestID(t, firstSynthesize)
	sendUpliftAITestSocketIOAudio(firstConn, firstRequestID, []byte{0x01, 0x02, 0x03, 0x04})
	firstConn.readErrs <- errors.New("temporary socket read failure")
	firstResult := receiveUpliftAITestSocketIOResult(t, firstResultCh)
	requireUpliftAITestSocketIOFrame(t, firstResult, "first read disconnect")
	requireUpliftAITestFinal(t, firstStream, "first read disconnect")

	secondStream, err := provider.Synthesize(context.Background(), "second")
	if err != nil {
		t.Fatalf("second Synthesize error = %v", err)
	}
	defer secondStream.Close()
	secondResultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := secondStream.Next()
		secondResultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()
	_ = receiveUpliftAITestString(t, secondConn.writes, "second namespace connect packet")
	secondSynthesize := receiveUpliftAITestString(t, secondConn.writes, "second synthesize packet")
	secondRequestID := upliftAITestSocketIORequestID(t, secondSynthesize)
	sendUpliftAITestSocketIOAudio(secondConn, secondRequestID, []byte{0x05, 0x06, 0x07, 0x08})
	secondConn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + secondRequestID + `"}]`
	secondResult := receiveUpliftAITestSocketIOResult(t, secondResultCh)
	requireUpliftAITestSocketIOFrame(t, secondResult, "second read reconnect")
	requireUpliftAITestFinal(t, secondStream, "second read reconnect")
	if got, want := dials, 2; got != want {
		t.Fatalf("socket.io dial count = %d, want reconnect after read error", got)
	}
}

func TestUpliftAITTSChunkedStreamSerializesReferenceSocketIOWrites(t *testing.T) {
	conn := newUpliftAITestSocketIOConn()
	conn.reads <- `0{"sid":"engine","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`
	conn.reads <- `40/text-to-speech/multi-stream,{"sid":"namespace"}`
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"ready","sessionId":"session-1"}]`
	conn.writeBlockAfter = 1
	conn.releaseWrites = make(chan struct{})

	oldDial := upliftAISocketIODialContext
	upliftAISocketIODialContext = func(context.Context, string) (upliftAISocketIOConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { upliftAISocketIODialContext = oldDial })

	provider := NewUpliftAITTS(
		"test-key",
		"voice-1",
		WithUpliftAIBaseURL("ws://upliftai.example"),
		WithUpliftAIOutputFormat("PCM_22050_16"),
	)
	defer provider.Close()

	firstStream, err := provider.Synthesize(context.Background(), "first")
	if err != nil {
		t.Fatalf("first Synthesize error = %v", err)
	}
	defer firstStream.Close()
	secondStream, err := provider.Synthesize(context.Background(), "second")
	if err != nil {
		t.Fatalf("second Synthesize error = %v", err)
	}
	defer secondStream.Close()

	firstResultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := firstStream.Next()
		firstResultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()
	<-conn.writeEntered
	if connect := receiveUpliftAITestString(t, conn.writes, "namespace connect packet"); connect != `40/text-to-speech/multi-stream,{"token":"test-key"}` {
		t.Fatalf("connect packet = %q, want reference namespace auth packet", connect)
	}
	<-conn.writeEntered

	secondResultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := secondStream.Next()
		secondResultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()
	select {
	case <-conn.writeEntered:
		t.Fatal("second socket.io writer entered before first synthesize write finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(conn.releaseWrites)
	firstSynthesize := receiveUpliftAITestString(t, conn.writes, "first synthesize packet")
	firstRequestID := upliftAITestSocketIORequestID(t, firstSynthesize)
	sendUpliftAITestSocketIOAudio(conn, firstRequestID, []byte{0x01, 0x02, 0x03, 0x04})
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + firstRequestID + `"}]`
	firstResult := receiveUpliftAITestSocketIOResult(t, firstResultCh)
	requireUpliftAITestSocketIOFrame(t, firstResult, "first serialized write")
	requireUpliftAITestFinal(t, firstStream, "first serialized write")

	<-conn.writeEntered
	secondSynthesize := receiveUpliftAITestString(t, conn.writes, "second synthesize packet")
	secondRequestID := upliftAITestSocketIORequestID(t, secondSynthesize)
	sendUpliftAITestSocketIOAudio(conn, secondRequestID, []byte{0x05, 0x06, 0x07, 0x08})
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio_end","requestId":"` + secondRequestID + `"}]`
	secondResult := receiveUpliftAITestSocketIOResult(t, secondResultCh)
	requireUpliftAITestSocketIOFrame(t, secondResult, "second serialized write")
	requireUpliftAITestFinal(t, secondStream, "second serialized write")
	if got, want := conn.maxConcurrentWrites(), 1; got != want {
		t.Fatalf("max concurrent socket.io writes = %d, want %d", got, want)
	}
}

func upliftAIWaitForCondition(t *testing.T, condition func() bool, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", name)
}

func receiveUpliftAITestString(t *testing.T, ch <-chan string, name string) string {
	t.Helper()
	return receiveUpliftAITestStringWithin(t, ch, name, 2*time.Second)
}

func receiveUpliftAITestStringWithin(t *testing.T, ch <-chan string, name string, timeout time.Duration) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %s", name)
		return ""
	}
}

func receiveUpliftAITestError(t *testing.T, ch <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}

func receiveUpliftAITestSocketIOResult(t *testing.T, ch <-chan struct {
	audio *tts.SynthesizedAudio
	err   error
}) struct {
	audio *tts.SynthesizedAudio
	err   error
} {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for socket.io audio result")
		return struct {
			audio *tts.SynthesizedAudio
			err   error
		}{}
	}
}

func sendUpliftAITestSocketIOAudio(conn *upliftAITestSocketIOConn, requestID string, data []byte) {
	audioPayload := base64.StdEncoding.EncodeToString(data)
	conn.reads <- `42/text-to-speech/multi-stream,["message",{"type":"audio","requestId":"` + requestID + `","audio":"` + audioPayload + `"}]`
}

func requireUpliftAITestSocketIOFrame(t *testing.T, result struct {
	audio *tts.SynthesizedAudio
	err   error
}, name string) {
	t.Helper()
	if result.err != nil {
		t.Fatalf("%s Next error = %v, want audio frame", name, result.err)
	}
	if result.audio == nil || result.audio.Frame == nil || result.audio.IsFinal {
		t.Fatalf("%s audio = %#v, want non-final audio frame", name, result.audio)
	}
}

func requireUpliftAITestFinal(t *testing.T, stream tts.ChunkedStream, name string) {
	t.Helper()
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("%s final Next error = %v", name, err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("%s final audio = %#v, want final marker", name, final)
	}
}

func requireUpliftAITestNoAudioError(t *testing.T, result struct {
	audio *tts.SynthesizedAudio
	err   error
}, name string) {
	t.Helper()
	if result.audio != nil {
		t.Fatalf("%s audio = %#v, want nil for no-audio provider response", name, result.audio)
	}
	var apiErr *llm.APIError
	if !errors.As(result.err, &apiErr) {
		t.Fatalf("%s error = %T(%v), want reference no-audio APIError", name, result.err, result.err)
	}
	if !strings.Contains(result.err.Error(), "no audio frames were pushed") {
		t.Fatalf("%s error = %q, want no-audio reference message", name, result.err.Error())
	}
}

func upliftAITestSocketIORequestID(t *testing.T, packet string) string {
	t.Helper()
	var event []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(packet, `42/text-to-speech/multi-stream,`)), &event); err != nil {
		t.Fatalf("decode socket.io event: %v", err)
	}
	if len(event) != 2 {
		t.Fatalf("socket.io event = %s, want event name and payload", packet)
	}
	var payload map[string]string
	if err := json.Unmarshal(event[1], &payload); err != nil {
		t.Fatalf("decode socket.io payload: %v", err)
	}
	if payload["requestId"] == "" {
		t.Fatalf("socket.io payload = %#v, want requestId", payload)
	}
	return payload["requestId"]
}

type upliftAITestSocketIOConn struct {
	reads           chan string
	readErrs        chan error
	writes          chan string
	closed          chan struct{}
	deadlineCh      chan time.Time
	deadlineMu      sync.Mutex
	readDeadline    time.Time
	readTimeout     time.Duration
	writeErr        error
	writeErrAfter   int
	writeCount      int
	writeBlockAfter int
	releaseWrites   chan struct{}
	writeEntered    chan struct{}
	activeWrites    int
	maxWrites       int
	once            sync.Once
}

func newUpliftAITestSocketIOConn() *upliftAITestSocketIOConn {
	return &upliftAITestSocketIOConn{
		reads:        make(chan string, 10),
		readErrs:     make(chan error, 10),
		writes:       make(chan string, 10),
		closed:       make(chan struct{}),
		deadlineCh:   make(chan time.Time, 10),
		writeEntered: make(chan struct{}, 10),
	}
}

func (c *upliftAITestSocketIOConn) ReadMessage() (int, []byte, error) {
	timer := time.NewTimer(c.currentReadTimeout())
	defer timer.Stop()
	select {
	case msg := <-c.reads:
		return 1, []byte(msg), nil
	default:
	}
	select {
	case msg := <-c.reads:
		return 1, []byte(msg), nil
	case err := <-c.readErrs:
		return 0, nil, err
	case <-c.closed:
		return 0, nil, io.ErrClosedPipe
	case <-c.deadlineCh:
		return c.ReadMessage()
	case <-timer.C:
		if c.hasReadDeadline() {
			return 0, nil, upliftAITestTimeoutError{}
		}
		return 0, nil, errors.New("timed out waiting for fake socket.io read")
	}
}

func (c *upliftAITestSocketIOConn) SetReadDeadline(deadline time.Time) error {
	c.deadlineMu.Lock()
	c.readDeadline = deadline
	c.deadlineMu.Unlock()
	select {
	case c.deadlineCh <- deadline:
	default:
	}
	return nil
}

func (c *upliftAITestSocketIOConn) currentReadTimeout() time.Duration {
	c.deadlineMu.Lock()
	deadline := c.readDeadline
	c.deadlineMu.Unlock()
	if !deadline.IsZero() {
		wait := time.Until(deadline)
		if wait <= 0 {
			return time.Nanosecond
		}
		return wait
	}
	if c.readTimeout > 0 {
		return c.readTimeout
	}
	return 2 * time.Second
}

func (c *upliftAITestSocketIOConn) hasReadDeadline() bool {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return !c.readDeadline.IsZero()
}

func (c *upliftAITestSocketIOConn) maxConcurrentWrites() int {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return c.maxWrites
}

type upliftAITestTimeoutError struct{}

func (upliftAITestTimeoutError) Error() string   { return "fake socket.io read timeout" }
func (upliftAITestTimeoutError) Timeout() bool   { return true }
func (upliftAITestTimeoutError) Temporary() bool { return true }

type upliftAIFixedSentenceTokenizer struct {
	tokens []string
}

func (t upliftAIFixedSentenceTokenizer) Tokenize(string, string) []string {
	return append([]string(nil), t.tokens...)
}

func (t upliftAIFixedSentenceTokenizer) Stream(string) tokenize.SentenceStream {
	return tokenize.NewBufferedTokenStream(func(string) []string {
		return append([]string(nil), t.tokens...)
	}, 1, 1)
}

type upliftAIFixedWordTokenizer struct {
	tokens    []string
	formatted string
}

func (t upliftAIFixedWordTokenizer) Tokenize(string, string) []string {
	return append([]string(nil), t.tokens...)
}

func (t upliftAIFixedWordTokenizer) Stream(string) tokenize.WordStream {
	return tokenize.NewBufferedTokenStream(func(string) []string {
		return append([]string(nil), t.tokens...)
	}, 1, 1)
}

func (t upliftAIFixedWordTokenizer) FormatWords(words []string) string {
	if t.formatted != "" {
		return t.formatted
	}
	return strings.Join(words, " ")
}

func (c *upliftAITestSocketIOConn) WriteMessage(_ int, data []byte) error {
	c.deadlineMu.Lock()
	c.writeCount++
	writeCount := c.writeCount
	writeErrAfter := c.writeErrAfter
	writeErr := c.writeErr
	writeBlockAfter := c.writeBlockAfter
	releaseWrites := c.releaseWrites
	c.activeWrites++
	if c.activeWrites > c.maxWrites {
		c.maxWrites = c.activeWrites
	}
	c.deadlineMu.Unlock()
	defer func() {
		c.deadlineMu.Lock()
		c.activeWrites--
		c.deadlineMu.Unlock()
	}()
	select {
	case c.writeEntered <- struct{}{}:
	default:
	}
	if writeBlockAfter > 0 && writeCount > writeBlockAfter && releaseWrites != nil {
		select {
		case <-releaseWrites:
		case <-c.closed:
			return io.ErrClosedPipe
		}
	}
	if writeErrAfter > 0 && writeCount > writeErrAfter {
		if writeErr != nil {
			return writeErr
		}
		return errors.New("fake socket.io write failed")
	}
	select {
	case c.writes <- string(data):
		return nil
	case <-c.closed:
		return io.ErrClosedPipe
	case <-time.After(2 * time.Second):
		return errors.New("timed out writing fake socket.io packet")
	}
}

func (c *upliftAITestSocketIOConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func TestUpliftAITTSChunkedStreamDecodesReferenceMP3Response(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: io.NopCloser(bytes.NewReader(mp3Data))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want decoded MP3 frame", audio)
	}
	if audio.Frame.SampleRate != defaultUpliftAISampleRate {
		t.Fatalf("sample rate = %d, want %d", audio.Frame.SampleRate, defaultUpliftAISampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want mono output", audio.Frame.NumChannels)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	prefixLen := min(len(audio.Frame.Data), len(mp3Data))
	if bytes.Equal(audio.Frame.Data[:prefixLen], mp3Data[:prefixLen]) {
		t.Fatal("frame data still contains compressed MP3 bytes")
	}
}

func TestUpliftAITTSChunkedStreamStreamsReferenceMP3BeforeEOF(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	body := newUpliftAIBlockingEOFBody(mp3Data)
	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: body},
	}
	defer stream.Close()

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- result{audio: audio, err: err}
	}()

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("Next error = %v", got.err)
		}
		if got.audio == nil || got.audio.Frame == nil {
			t.Fatalf("audio = %#v, want decoded MP3 frame before response EOF", got.audio)
		}
	case <-time.After(500 * time.Millisecond):
		_ = stream.Close()
		select {
		case <-resultCh:
		case <-time.After(time.Second):
		}
		t.Fatal("timed out waiting for decoded MP3 frame before response EOF")
	}
}

func TestUpliftAITTSChunkedStreamEmitsReferenceMP3FinalMarker(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	provider := newUpliftAITestHTTPProvider("test-key", "")
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: io.NopCloser(bytes.NewReader(mp3Data))},
	}
	defer stream.Close()

	frames := 0
	for i := 0; i < 5000; i++ {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next error after %d decoded frames = %v", frames, err)
		}
		if audio == nil {
			t.Fatalf("audio after %d decoded frames = nil", frames)
		}
		if audio.IsFinal {
			if frames == 0 {
				t.Fatal("final marker arrived before decoded MP3 frames")
			}
			if audio, err := stream.Next(); audio != nil || err != io.EOF {
				t.Fatalf("Next after final = (%#v, %v), want EOF", audio, err)
			}
			return
		}
		if audio.Frame != nil {
			frames++
		}
	}
	t.Fatalf("read %d decoded MP3 frames without final marker", frames)
}

func TestUpliftAITTSChunkedStreamDecodesReferenceWAVResponse(t *testing.T) {
	pcm := []byte{0x01, 0x00, 0x03, 0x00, 0x05, 0x00, 0x07, 0x00}
	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("WAV_22050_16"))
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: io.NopCloser(bytes.NewReader(upliftAITestWAV(pcm, 22050, 1)))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want decoded WAV frame", audio)
	}
	if audio.Frame.SampleRate != defaultUpliftAISampleRate {
		t.Fatalf("sample rate = %d, want %d", audio.Frame.SampleRate, defaultUpliftAISampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want mono output", audio.Frame.NumChannels)
	}
	if bytes.HasPrefix(audio.Frame.Data, []byte("RIFF")) {
		t.Fatal("frame data still contains WAV header")
	}
	if !bytes.Equal(audio.Frame.Data, pcm) {
		t.Fatalf("audio data = %#v, want decoded wav pcm %#v", audio.Frame.Data, pcm)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", final)
	}
	if audio, err := stream.Next(); audio != nil || err != io.EOF {
		t.Fatalf("Next after final = (%#v, %v), want EOF", audio, err)
	}
}

func TestUpliftAITTSChunkedStreamStreamsReferenceWAVBeforeEOF(t *testing.T) {
	pcm := []byte{0x01, 0x00, 0x03, 0x00, 0x05, 0x00, 0x07, 0x00}
	body := newUpliftAIBlockingEOFBody(upliftAITestWAV(pcm, 22050, 1))
	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("WAV_22050_16"))
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: body},
	}
	defer stream.Close()

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- result{audio: audio, err: err}
	}()

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("Next error = %v", got.err)
		}
		if got.audio == nil || got.audio.Frame == nil {
			t.Fatalf("audio = %#v, want decoded WAV frame before response EOF", got.audio)
		}
		if !bytes.Equal(got.audio.Frame.Data, pcm) {
			t.Fatalf("audio data = %#v, want decoded wav pcm %#v", got.audio.Frame.Data, pcm)
		}
	case <-time.After(500 * time.Millisecond):
		_ = stream.Close()
		select {
		case <-resultCh:
		case <-time.After(time.Second):
		}
		t.Fatal("timed out waiting for decoded WAV frame before response EOF")
	}
}

func TestUpliftAITTSChunkedStreamDecodesReferenceConcatenatedWAVSegments(t *testing.T) {
	firstPCM := []byte{0x01, 0x00, 0x03, 0x00}
	secondPCM := []byte{0x05, 0x00, 0x07, 0x00}
	body := append(upliftAITestWAV(firstPCM, 22050, 1), upliftAITestWAV(secondPCM, 22050, 1)...)
	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("WAV_22050_16"))
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: io.NopCloser(bytes.NewReader(body))},
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if first == nil || first.Frame == nil {
		t.Fatalf("first audio = %#v, want decoded first WAV segment", first)
	}
	if !bytes.Equal(first.Frame.Data, firstPCM) {
		t.Fatalf("first audio data = %#v, want %#v", first.Frame.Data, firstPCM)
	}

	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v", err)
	}
	if second == nil || second.Frame == nil {
		t.Fatalf("second audio = %#v, want decoded second WAV segment before final", second)
	}
	if !bytes.Equal(second.Frame.Data, secondPCM) {
		t.Fatalf("second audio data = %#v, want %#v", second.Frame.Data, secondPCM)
	}
}

func TestUpliftAITTSChunkedStreamDecodesReferenceWAV32Response(t *testing.T) {
	pcm32 := []byte{
		0x00, 0x00, 0x00, 0x40,
		0x00, 0x00, 0x00, 0xc0,
	}
	wantPCM16 := []byte{0x00, 0x40, 0x00, 0xc0}
	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("WAV_22050_32"))
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: io.NopCloser(bytes.NewReader(upliftAITestWAVBits(pcm32, 22050, 1, 32)))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want decoded WAV_22050_32 frame", audio)
	}
	if audio.Frame.SampleRate != defaultUpliftAISampleRate {
		t.Fatalf("sample rate = %d, want %d", audio.Frame.SampleRate, defaultUpliftAISampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want mono output", audio.Frame.NumChannels)
	}
	if bytes.HasPrefix(audio.Frame.Data, []byte("RIFF")) {
		t.Fatal("frame data still contains WAV header")
	}
	if !bytes.Equal(audio.Frame.Data, wantPCM16) {
		t.Fatalf("audio data = %#v, want decoded wav32 pcm16 %#v", audio.Frame.Data, wantPCM16)
	}
}

func TestUpliftAITTSChunkedStreamDecodesReferenceULawResponse(t *testing.T) {
	encoded := []byte{0x00, 0xff}
	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("ULAW_8000_8"))
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: io.NopCloser(bytes.NewReader(encoded))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want decoded mu-law frame", audio)
	}
	if got, want := audio.Frame.SampleRate, uint32(8000); got != want {
		t.Fatalf("sample rate = %d, want ULAW_8000_8 rate %d", got, want)
	}
	if got, want := audio.Frame.NumChannels, uint32(1); got != want {
		t.Fatalf("channels = %d, want mono output %d", got, want)
	}
	if got, want := audio.Frame.SamplesPerChannel, uint32(2); got != want {
		t.Fatalf("samples per channel = %d, want one PCM16 sample per mu-law byte", got)
	}
	wantPCM := []byte{0x84, 0x82, 0x00, 0x00}
	if !bytes.Equal(audio.Frame.Data, wantPCM) {
		t.Fatalf("audio data = %#v, want decoded mu-law PCM %#v", audio.Frame.Data, wantPCM)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", final)
	}
}

func TestUpliftAITTSChunkedStreamBuffersReferenceULawChunksBeforeFinal(t *testing.T) {
	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("ULAW_8000_8"))
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp: &http.Response{Body: &upliftAIChunkReader{chunks: [][]byte{
			{0x00, 0xff},
			{0x7f, 0x80},
		}}},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want decoded buffered mu-law frame", audio)
	}
	if got, want := audio.Frame.SampleRate, uint32(8000); got != want {
		t.Fatalf("sample rate = %d, want ULAW_8000_8 rate %d", got, want)
	}
	if got, want := audio.Frame.SamplesPerChannel, uint32(4); got != want {
		t.Fatalf("samples per channel = %d, want buffered samples across provider chunks %d", got, want)
	}
	wantPCM := append(decodeUpliftAIMuLaw([]byte{0x00, 0xff}), decodeUpliftAIMuLaw([]byte{0x7f, 0x80})...)
	if !bytes.Equal(audio.Frame.Data, wantPCM) {
		t.Fatalf("audio data = %#v, want buffered decoded mu-law PCM %#v", audio.Frame.Data, wantPCM)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("second audio = %#v, want final marker after buffered mu-law tail", final)
	}
}

func TestUpliftAITTSChunkedStreamHonorsConfiguredChannelsForULaw(t *testing.T) {
	provider := newUpliftAITestHTTPProvider(
		"test-key",
		"",
		WithUpliftAIOutputFormat("ULAW_8000_8"),
		WithUpliftAINumChannels(2),
	)
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp: &http.Response{Body: &upliftAIChunkReader{chunks: [][]byte{
			{0x00, 0xff},
			{0x7f, 0x80},
		}}},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want decoded stereo mu-law frame", audio)
	}
	if got, want := audio.Frame.NumChannels, uint32(2); got != want {
		t.Fatalf("channels = %d, want configured reference channel count %d", got, want)
	}
	if got, want := audio.Frame.SamplesPerChannel, uint32(4); got != want {
		t.Fatalf("samples per channel = %d, want decoded sample count %d", got, want)
	}
	if got, want := len(audio.Frame.Data), int(audio.Frame.SamplesPerChannel*audio.Frame.NumChannels*2); got != want {
		t.Fatalf("frame data length = %d, want %d for stereo PCM16 frame", got, want)
	}
}

func TestUpliftAITTSChunkedStreamDecodesReferenceOGGResponse(t *testing.T) {
	oggData, err := base64.StdEncoding.DecodeString(upliftAITestOpusOggFixtureBase64)
	if err != nil {
		t.Fatalf("decode ogg fixture: %v", err)
	}
	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("OGG_22050_16"))
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: io.NopCloser(bytes.NewReader(oggData))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want decoded OGG frame", audio)
	}
	if got, want := audio.Frame.SampleRate, uint32(defaultUpliftAISampleRate); got != want {
		t.Fatalf("sample rate = %d, want %d", got, want)
	}
	if got, want := audio.Frame.NumChannels, uint32(1); got != want {
		t.Fatalf("channels = %d, want mono output %d", got, want)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded OGG frame is empty")
	}
	if bytes.HasPrefix(audio.Frame.Data, []byte("OggS")) {
		t.Fatal("frame data still contains OGG container bytes")
	}

	frames := 1
	for i := 0; i < 500; i++ {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next after %d frames error = %v", frames, err)
		}
		if audio == nil {
			t.Fatalf("audio after %d frames = nil", frames)
		}
		if audio.IsFinal {
			if frames == 0 {
				t.Fatal("final marker arrived before decoded OGG frames")
			}
			return
		}
		if audio.Frame != nil {
			frames++
		}
	}
	t.Fatalf("read %d decoded OGG frames without final marker", frames)
}

func TestUpliftAITTSChunkedStreamHonorsConfiguredChannelsForOGG(t *testing.T) {
	oggData, err := base64.StdEncoding.DecodeString(upliftAITestOpusOggFixtureBase64)
	if err != nil {
		t.Fatalf("decode ogg fixture: %v", err)
	}
	provider := newUpliftAITestHTTPProvider(
		"test-key",
		"",
		WithUpliftAIOutputFormat("OGG_22050_16"),
		WithUpliftAINumChannels(2),
	)
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: io.NopCloser(bytes.NewReader(oggData))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want decoded OGG frame", audio)
	}
	if got, want := audio.Frame.NumChannels, uint32(2); got != want {
		t.Fatalf("channels = %d, want configured reference channel count %d", got, want)
	}
	if got, want := audio.Frame.SampleRate, uint32(defaultUpliftAISampleRate); got != want {
		t.Fatalf("sample rate = %d, want %d", got, want)
	}
	if got, want := len(audio.Frame.Data), int(audio.Frame.SamplesPerChannel*audio.Frame.NumChannels*2); got != want {
		t.Fatalf("frame data length = %d, want %d for stereo PCM16 frame", got, want)
	}
}

func TestUpliftAITTSChunkedStreamStreamsReferenceOGGBeforeEOF(t *testing.T) {
	oggData, err := base64.StdEncoding.DecodeString(upliftAITestOpusOggFixtureBase64)
	if err != nil {
		t.Fatalf("decode ogg fixture: %v", err)
	}

	body := newUpliftAIBlockingEOFBody(oggData)
	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("OGG_22050_16"))
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: body},
	}
	defer stream.Close()

	type result struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- result{audio: audio, err: err}
	}()

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("Next error = %v", got.err)
		}
		if got.audio == nil || got.audio.Frame == nil {
			t.Fatalf("audio = %#v, want decoded OGG frame before response EOF", got.audio)
		}
		if bytes.HasPrefix(got.audio.Frame.Data, []byte("OggS")) {
			t.Fatal("frame data still contains OGG container bytes")
		}
	case <-time.After(500 * time.Millisecond):
		_ = stream.Close()
		select {
		case <-resultCh:
		case <-time.After(time.Second):
		}
		t.Fatal("timed out waiting for decoded OGG frame before response EOF")
	}
}

func TestUpliftAITTSChunkedStreamFramesAudio(t *testing.T) {
	body := io.NopCloser(strings.NewReader("\x01\x02\x03\x04"))
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: body}}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}
	if got, want := audio.Frame.Data, []byte{1, 2, 3, 4}; string(got) != string(want) {
		t.Fatalf("Frame.Data = %v, want %v", got, want)
	}
	if got, want := audio.Frame.SampleRate, uint32(22050); got != want {
		t.Fatalf("SampleRate = %d, want %d", got, want)
	}
	if got, want := audio.Frame.NumChannels, uint32(1); got != want {
		t.Fatalf("NumChannels = %d, want %d", got, want)
	}
	if got, want := audio.Frame.SamplesPerChannel, uint32(2); got != want {
		t.Fatalf("SamplesPerChannel = %d, want %d", got, want)
	}
	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next() error = %v, want EOF", err)
	}
}

func TestUpliftAITTSChunkedStreamBuffersReferencePCMChunksBeforeFinal(t *testing.T) {
	body := &upliftAIChunkReader{chunks: [][]byte{{0x01, 0x02}, {0x03, 0x04}}}
	provider := newUpliftAITestHTTPProvider("test-key", "", WithUpliftAIOutputFormat("PCM_22050_16"))
	stream := &upliftAITTSChunkedStream{
		owner: provider,
		resp:  &http.Response{Body: body},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want buffered PCM frame", audio)
	}
	if got, want := audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}; !bytes.Equal(got, want) {
		t.Fatalf("Frame.Data = %#v, want buffered PCM chunks %#v", got, want)
	}
	if got, want := audio.Frame.SamplesPerChannel, uint32(2); got != want {
		t.Fatalf("SamplesPerChannel = %d, want %d", got, want)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", final)
	}
}

func TestUpliftAITTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	body := io.NopCloser(strings.NewReader("\x01\x02\x03\x04"))
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: body}}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if audio == nil || audio.IsFinal {
		t.Fatalf("first audio = %#v, want non-final audio", audio)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("audio frame is empty")
	}

	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next() error before final marker = %v", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("second audio = %#v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

func TestUpliftAITTSChunkedStreamErrorsAfterReferenceEmptyAudio(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outputFormat string
	}{
		{name: "raw pcm", outputFormat: "PCM_22050_16"},
		{name: "mp3", outputFormat: "MP3_22050_32"},
		{name: "ogg", outputFormat: "OGG_22050_16"},
		{name: "wav", outputFormat: "WAV_22050_16"},
		{name: "ulaw", outputFormat: "ULAW_8000_8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := io.NopCloser(strings.NewReader(""))
			stream := &upliftAITTSChunkedStream{text: "hello", outputFormat: tc.outputFormat, resp: &http.Response{Body: body}}
			defer stream.Close()

			audio, err := stream.Next()
			if audio != nil {
				t.Fatalf("Next audio = %#v, want nil for no-audio provider response", audio)
			}
			var apiErr *llm.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("Next error = %T(%v), want reference APIError for non-empty text with no audio frames", err, err)
			}
			if !strings.Contains(err.Error(), "no audio frames were pushed") {
				t.Fatalf("Next error = %q, want no-audio reference message", err.Error())
			}
		})
	}
}

func TestUpliftAITTSChunkedStreamKeepsAudioReturnedWithEOF(t *testing.T) {
	body := &upliftAIFinalEOFReader{data: []byte{0x01, 0x02}}
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: body}}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v, want final audio bytes", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first audio = %#v, want non-final audio", audio)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{0x01, 0x02}) {
		t.Fatalf("audio data = %v, want final EOF bytes", got)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second audio = %#v, want boundary-only final marker", final)
	}
	if audio, err := stream.Next(); audio != nil || err != io.EOF {
		t.Fatalf("third Next() = (%#v, %v), want EOF", audio, err)
	}
}

func TestUpliftAITTSChunkedStreamKeepsAudioReturnedWithReadError(t *testing.T) {
	errRead := errors.New("upliftai read failed after audio")
	pcm := bytes.Repeat([]byte{0x01, 0x00}, defaultUpliftAISampleRate*20/1000)
	stream := &upliftAITTSChunkedStream{
		resp: &http.Response{Body: &upliftAIFinalErrorReader{data: pcm, err: errRead}},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v, want audio returned before read error", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("first audio = %#v, want frame returned before read error", audio)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, pcm) {
		t.Fatalf("audio data = %#v, want PCM bytes returned with read error %#v", got, pcm)
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("second Next error = nil, want read error after returned audio")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("second Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestUpliftAITTSChunkedStreamReadFailureReturnsAPIConnectionError(t *testing.T) {
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: upliftAIErrorReader{}}}
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

func TestUpliftAITTSChunkedStreamReadDeadlineReturnsAPITimeoutError(t *testing.T) {
	stream := &upliftAITTSChunkedStream{
		resp: &http.Response{Body: upliftAIReadErrorBody{err: context.DeadlineExceeded}},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestUpliftAITTSChunkedStreamReadCancelReturnsContextCanceled(t *testing.T) {
	stream := &upliftAITTSChunkedStream{
		resp: &http.Response{Body: upliftAIReadErrorBody{err: context.Canceled}},
	}
	defer stream.Close()

	_, err := stream.Next()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next error = %T(%v), want context.Canceled for caller cancellation", err, err)
	}
}

func TestUpliftAITTSChunkedStreamCloseDuringReadReturnsEOF(t *testing.T) {
	body := newUpliftAICloseUnblocksReadBody(io.ErrClosedPipe)
	stream := &upliftAITTSChunkedStream{
		text: "interrupted",
		resp: &http.Response{Body: body},
	}
	resultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()

	select {
	case <-body.readEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocked UpliftAI response-body read")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close during read error = %v", err)
	}
	result := receiveUpliftAITestSocketIOResult(t, resultCh)
	if result.audio != nil || result.err != io.EOF {
		t.Fatalf("Next after Close during read = (%#v, %v), want EOF", result.audio, result.err)
	}
}

func TestUpliftAITTSChunkedStreamCloseDuringAudioReadDropsLateFrame(t *testing.T) {
	pcm := bytes.Repeat([]byte{0x01, 0x00}, defaultUpliftAISampleRate*20/1000)
	body := newUpliftAICloseUnblocksAudioReadBody(pcm)
	stream := &upliftAITTSChunkedStream{
		text: "interrupted",
		resp: &http.Response{Body: body},
	}
	resultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()

	select {
	case <-body.readEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocked UpliftAI response-body audio read")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close during audio read error = %v", err)
	}
	result := receiveUpliftAITestSocketIOResult(t, resultCh)
	if result.audio != nil || result.err != io.EOF {
		t.Fatalf("Next after Close during late audio read = (%#v, %v), want EOF without stale audio", result.audio, result.err)
	}
}

func TestUpliftAITTSChunkedStreamCloseSuppressesReferenceBodyCloseError(t *testing.T) {
	stream := &upliftAITTSChunkedStream{
		resp: &http.Response{Body: upliftAICloseErrorBody{err: errors.New("body close failed")}},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil for caller-owned cleanup", err)
	}
	if audio, err := stream.Next(); audio != nil || err != io.EOF {
		t.Fatalf("Next after Close = (%#v, %v), want EOF", audio, err)
	}
}

func TestUpliftAITTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &upliftAICloseCountBody{reader: strings.NewReader("audio")}
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: body}}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if got, want := body.closeCount, 1; got != want {
		t.Fatalf("close count = %d, want %d", got, want)
	}

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next() after Close audio = %#v, want nil", audio)
	}
	if err != io.EOF {
		t.Fatalf("Next() after Close error = %v, want EOF", err)
	}
}

type upliftAIRoundTripFunc func(*http.Request) (*http.Response, error)

func (f upliftAIRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type upliftAICloseCountBody struct {
	reader     *strings.Reader
	closeCount int
}

func (b *upliftAICloseCountBody) Read(p []byte) (int, error) {
	if b.closeCount > 0 {
		return 0, errors.New("read after close")
	}
	return b.reader.Read(p)
}

func (b *upliftAICloseCountBody) Close() error {
	b.closeCount++
	return nil
}

func upliftAITestWAV(pcm []byte, sampleRate uint32, channels uint16) []byte {
	return upliftAITestWAVBits(pcm, sampleRate, channels, 16)
}

func upliftAITestWAVBits(pcm []byte, sampleRate uint32, channels uint16, bitsPerSample uint16) []byte {
	var wav bytes.Buffer
	blockAlign := uint16(channels * bitsPerSample / 8)
	byteRate := sampleRate * uint32(blockAlign)
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
	_ = binary.Write(&wav, binary.LittleEndian, bitsPerSample)
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)))
	wav.Write(pcm)
	return wav.Bytes()
}

const upliftAITestOpusOggFixtureBase64 = "T2dnUwACAAAAAAAAAACXynBsAAAAAMy/Wi4BE09wdXNIZWFkAQE4AYC7AAAAAABPZ2dTAAAAAAAAAAAAAJfKcGwBAAAAYQP1NwE+T3B1c1RhZ3MNAAAATGF2ZjU5LjI3LjEwMAEAAAAdAAAAZW5jb2Rlcj1MYXZjNTkuMzcuMTAwIGxpYm9wdXNPZ2dTAAT4BAAAAAAAAJfKcGwCAAAAdYmr1AIDA/j//vj//g=="
