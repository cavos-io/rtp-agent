package respeecher

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

type respeecherFinalEOFReader struct {
	data []byte
	done bool
}

func (r *respeecherFinalEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("read after final eof")
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func (r *respeecherFinalEOFReader) Close() error { return nil }

type respeecherCloseErrorBody struct {
	closed bool
}

func (b *respeecherCloseErrorBody) Read(_ []byte) (int, error) {
	if b.closed {
		return 0, errors.New("read after close")
	}
	return 0, io.EOF
}

func (b *respeecherCloseErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestRespeecherTTSDefaultsMatchReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "")

	if provider.baseURL != "https://api.respeecher.com/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "/public/tts/en-rt" {
		t.Fatalf("model = %q, want English public model", provider.model)
	}
	if got := tts.Model(provider); got != "/public/tts/en-rt" {
		t.Fatalf("model metadata = %q, want English public model", got)
	}
	if got := tts.Provider(provider); got != "Respeecher" {
		t.Fatalf("provider metadata = %q, want Respeecher", got)
	}
	if provider.voiceID != "samantha" {
		t.Fatalf("voice id = %q, want model default voice", provider.voiceID)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming = false, want true")
	}
}

func TestNewRespeecherTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("RESPEECHER_API_KEY", "env-key")

	provider := NewRespeecherTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildRespeecherTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("X-API-Key"); got != "env-key" {
		t.Fatalf("X-API-Key = %q, want env key", got)
	}
	if got := buildRespeecherTTSWebsocketURL(provider).Query().Get("api_key"); got != "env-key" {
		t.Fatalf("websocket api_key = %q, want env key", got)
	}

	explicit := NewRespeecherTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestRespeecherTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("RESPEECHER_API_KEY", "")
	provider := NewRespeecherTTS("", "", WithRespeecherTTSBaseURL("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "RESPEECHER_API_KEY") {
		t.Fatalf("Synthesize error = %q, want RESPEECHER_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "RESPEECHER_API_KEY") {
		t.Fatalf("Stream error = %q, want RESPEECHER_API_KEY guidance", err)
	}
}

func TestRespeecherTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "")

	req, err := buildRespeecherTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.respeecher.com/v1/public/tts/en-rt/tts/bytes" {
		t.Fatalf("url = %q, want bytes endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-API-Key"); got != "test-key" {
		t.Fatalf("X-API-Key = %q, want test key", got)
	}
	wantAPIVersion := respeecherAPIVersion
	if got := req.Header.Get("LiveKit-Plugin-Respeecher-Version"); got != wantAPIVersion {
		t.Fatalf("version header = %q, want reference plugin version", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRespeecherPayload(t, payload, "transcript", "hello")
	voice := payload["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "samantha")
	output := payload["output_format"].(map[string]any)
	assertRespeecherPayload(t, output, "encoding", "pcm_s16le")
	if output["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", output["sample_rate"])
	}
}

func TestRespeecherTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: respeecherRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewRespeecherTTS("test-key", "", WithRespeecherTTSBaseURL("https://respeecher.example/v1"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error before stream consumption: %v", err)
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
}

func TestRespeecherTTSSynthesizeDefersReferenceRequestUntilNext(t *testing.T) {
	var requests int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: respeecherRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(respeecherTestWAV([]byte{0x01, 0x02}, 24000, 1))),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewRespeecherTTS("test-key", "", WithRespeecherTTSBaseURL("https://respeecher.example/v1"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()
	if requests != 0 {
		t.Fatalf("requests before Next = %d, want 0", requests)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("Next audio = %#v, want first audio frame", audio)
	}
	if requests != 1 {
		t.Fatalf("requests after Next = %d, want 1", requests)
	}
}

func TestRespeecherTTSOptionsMatchReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "",
		WithRespeecherTTSBaseURL("https://respeecher.example/v1/"),
		WithRespeecherTTSModel("/public/tts/ua-rt"),
		WithRespeecherTTSVoice("olesia-conversation"),
		WithRespeecherTTSSampleRate(48000),
		WithRespeecherTTSSamplingParams(map[string]any{"temperature": 0.4}),
	)

	req, err := buildRespeecherTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://respeecher.example/v1/public/tts/ua-rt/tts/bytes" {
		t.Fatalf("url = %q, want custom bytes endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	voice := payload["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "olesia-conversation")
	samplingParams := voice["sampling_params"].(map[string]any)
	if samplingParams["temperature"] != float64(0.4) {
		t.Fatalf("temperature = %#v, want 0.4", samplingParams["temperature"])
	}
	output := payload["output_format"].(map[string]any)
	if output["sample_rate"] != float64(48000) {
		t.Fatalf("sample_rate = %#v, want 48000", output["sample_rate"])
	}
}

func TestRespeecherTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &respeecherTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(respeecherTestWAV([]byte{0x01, 0x02}, 48000, 1)))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func TestRespeecherTTSChunkedStreamDecodesReferenceWAVResponse(t *testing.T) {
	pcm := []byte{0x01, 0x00, 0x02, 0x00}
	stream := &respeecherTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(respeecherTestWAV(pcm, 24000, 1)))},
		sampleRate: 24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, pcm) {
		t.Fatalf("frame data = %#v, want decoded PCM %#v", audio.Frame.Data, pcm)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame shape = rate %d channels %d samples %d, want 24000/1/2", audio.Frame.SampleRate, audio.Frame.NumChannels, audio.Frame.SamplesPerChannel)
	}
	if bytes.HasPrefix(audio.Frame.Data, []byte("RIFF")) {
		t.Fatal("frame data still contains WAV header")
	}
}

func TestRespeecherTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &respeecherTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(respeecherTestWAV([]byte{0x01, 0x02}, 48000, 1)))},
		sampleRate: 48000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if audio == nil || audio.IsFinal || audio.Frame == nil {
		t.Fatalf("first audio = %#v, want audio frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error before final marker: %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second audio = %#v, want final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestRespeecherTTSChunkedStreamKeepsAudioReturnedWithEOF(t *testing.T) {
	stream := &respeecherTTSChunkedStream{
		resp:       &http.Response{Body: &respeecherFinalEOFReader{data: respeecherTestWAV([]byte{0x01, 0x02}, 48000, 1)}},
		sampleRate: 48000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if audio == nil || audio.IsFinal || audio.Frame == nil {
		t.Fatalf("first audio = %#v, want audio frame", audio)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{0x01, 0x02}) {
		t.Fatalf("audio data = %v, want final bytes", got)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error before final marker: %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second audio = %#v, want final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestRespeecherTTSWebsocketURLMatchesReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "",
		WithRespeecherTTSBaseURL("https://respeecher.example/v1"),
		WithRespeecherTTSModel("/public/tts/ua-rt"),
	)

	wsURL := buildRespeecherTTSWebsocketURL(provider)
	if wsURL.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", wsURL.Scheme)
	}
	if wsURL.Host != "respeecher.example" || wsURL.Path != "/v1/public/tts/ua-rt/tts/websocket" {
		t.Fatalf("websocket URL = %q, want reference websocket endpoint", wsURL.String())
	}
	query := wsURL.Query()
	if query.Get("api_key") != "test-key" {
		t.Fatalf("api_key query = %q, want test-key", query.Get("api_key"))
	}
	if query.Get("source") != "LiveKit-Plugin-Respeecher-Version" {
		t.Fatalf("source query = %q, want version header name", query.Get("source"))
	}
	wantAPIVersion := respeecherAPIVersion
	if query.Get("version") != wantAPIVersion {
		t.Fatalf("version query = %q, want plugin API version", query.Get("version"))
	}
}

func TestRespeecherTTSWebsocketMessagesMatchReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "",
		WithRespeecherTTSVoice("speaker-1"),
		WithRespeecherTTSSampleRate(48000),
		WithRespeecherTTSSamplingParams(map[string]any{"temperature": 0.4}),
	)

	chunk, err := buildRespeecherTTSTextMessage(provider, "ctx-1", "hello", true)
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(chunk, &payload); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	assertRespeecherPayload(t, payload, "context_id", "ctx-1")
	assertRespeecherPayload(t, payload, "transcript", "hello")
	if payload["continue"] != true {
		t.Fatalf("continue = %#v, want true", payload["continue"])
	}
	voice := payload["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "speaker-1")
	samplingParams := voice["sampling_params"].(map[string]any)
	if samplingParams["temperature"] != float64(0.4) {
		t.Fatalf("temperature = %#v, want 0.4", samplingParams["temperature"])
	}
	output := payload["output_format"].(map[string]any)
	assertRespeecherPayload(t, output, "encoding", "pcm_s16le")
	if output["sample_rate"] != float64(48000) {
		t.Fatalf("sample_rate = %#v, want 48000", output["sample_rate"])
	}

	end, err := buildRespeecherTTSEndMessage(provider, "ctx-1")
	if err != nil {
		t.Fatalf("build end message: %v", err)
	}
	payload = map[string]any{}
	if err := json.Unmarshal(end, &payload); err != nil {
		t.Fatalf("decode end message: %v", err)
	}
	assertRespeecherPayload(t, payload, "transcript", " ")
	if payload["continue"] != false {
		t.Fatalf("continue = %#v, want false", payload["continue"])
	}
}

func TestRespeecherTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &respeecherCloseErrorBody{}
	stream := &respeecherTTSChunkedStream{resp: &http.Response{Body: body}}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("Next after Close = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestRespeecherTTSStreamSendsSentencesAndFlushesTailLikeReference(t *testing.T) {
	var writes []map[string]any
	stream := &respeecherTTSSynthesizeStream{
		provider:  NewRespeecherTTS("test-key", ""),
		contextID: "ctx-1",
		cancel:    func() {},
		writeMessage: func(payload []byte) error {
			var msg map[string]any
			if err := json.Unmarshal(payload, &msg); err != nil {
				t.Fatalf("decode websocket payload: %v", err)
			}
			writes = append(writes, msg)
			return nil
		},
	}

	if err := stream.PushText("This first sentence is definitely long enough. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("writes after PushText = %d, want only completed sentence", len(writes))
	}
	if writes[0]["transcript"] != "This first sentence is definitely long enough." || writes[0]["continue"] != true {
		t.Fatalf("first message = %#v, want completed sentence with continue=true", writes[0])
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if len(writes) != 2 {
		t.Fatalf("writes after Flush = %d, want tail text only", len(writes))
	}
	if writes[1]["transcript"] != "Tail" || writes[1]["continue"] != true {
		t.Fatalf("tail message = %#v, want flushed tail with continue=true", writes[1])
	}
}

func TestRespeecherTTSStreamEndInputSendsReferenceEndOnce(t *testing.T) {
	var writes []map[string]any
	stream := &respeecherTTSSynthesizeStream{
		provider:  NewRespeecherTTS("test-key", ""),
		contextID: "ctx-1",
		cancel:    func() {},
		writeMessage: func(payload []byte) error {
			var msg map[string]any
			if err := json.Unmarshal(payload, &msg); err != nil {
				t.Fatalf("decode websocket payload: %v", err)
			}
			writes = append(writes, msg)
			return nil
		},
		closeConn: func() error { return nil },
	}

	if err := stream.PushText("Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("second EndInput error = %v", err)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after EndInput error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after EndInput error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	if len(writes) != 2 {
		t.Fatalf("writes = %d, want tail text and end packet", len(writes))
	}
	if writes[0]["transcript"] != "Tail" || writes[0]["continue"] != true {
		t.Fatalf("tail message = %#v, want flushed tail with continue=true", writes[0])
	}
	if writes[1]["transcript"] != " " || writes[1]["continue"] != false {
		t.Fatalf("end message = %#v, want reference end packet", writes[1])
	}
}

func TestRespeecherTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &respeecherTTSSynthesizeStream{
		cancel:    func() { cancelled = true },
		provider:  NewRespeecherTTS("test-key", ""),
		contextID: "ctx-1",
		writeMessage: func([]byte) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("hello there dear friend. Tail"); !errors.Is(err, writeErr) {
		t.Fatalf("PushText error = %v, want write error", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after write failure")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
	}
}

func TestRespeecherTTSProviderCloseClosesActiveStreams(t *testing.T) {
	cancelled := false
	closeCalls := 0
	provider := NewRespeecherTTS("test-key", "")
	stream := &respeecherTTSSynthesizeStream{
		cancel:    func() { cancelled = true },
		provider:  provider,
		contextID: "ctx-1",
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after provider Close")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestRespeecherTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &respeecherTTSSynthesizeStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *tts.SynthesizedAudio),
		errCh:  make(chan error),
		closeConn: func() error {
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	_, err := stream.Next()

	if err != io.EOF {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestRespeecherTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &respeecherTTSSynthesizeStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		closed: true,
	}
	stream.events <- &tts.SynthesizedAudio{RequestID: "stale"}

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestRespeecherTTSNextReturnsQueuedAudioBeforeStreamError(t *testing.T) {
	providerErr := errors.New("provider failed after audio")
	for i := range 200 {
		want := &tts.SynthesizedAudio{RequestID: "req-audio"}
		stream := &respeecherTTSSynthesizeStream{
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

func TestRespeecherTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: respeecherRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	defer func() { http.DefaultClient = oldClient }()

	provider := NewRespeecherTTS("test-key", "")
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

func TestRespeecherTTSStreamAfterCloseIsRejected(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	dialCalls := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls++
			return nil, errors.New("unexpected websocket dial")
		},
		Proxy: nil,
	}
	defer func() { websocket.DefaultDialer = oldDialer }()

	provider := NewRespeecherTTS("test-key", "")
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

func TestRespeecherTTSStreamDialFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("respeecher tts dial failed")
		},
		Proxy: nil,
	}
	defer func() { websocket.DefaultDialer = oldDialer }()

	provider := NewRespeecherTTS("test-key", "")

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil on dial failure", stream)
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestRespeecherTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	conn := newRespeecherProviderCloseWebsocketConn(t, websocket.CloseUnsupportedData)

	stream := &respeecherTTSSynthesizeStream{
		conn:      conn,
		contextID: "ctx-1",
		provider:  NewRespeecherTTS("test-key", ""),
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
		if statusErr.StatusCode != websocket.CloseUnsupportedData {
			t.Fatalf("StatusCode = %d, want close code", statusErr.StatusCode)
		}
		if !strings.Contains(err.Error(), "Respeecher connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Respeecher close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close error")
	}
}

func TestRespeecherTTSStreamNormalCloseBeforeDoneReturnsAPIStatusError(t *testing.T) {
	conn := newRespeecherProviderCloseWebsocketConn(t, websocket.CloseNormalClosure)

	stream := &respeecherTTSSynthesizeStream{
		conn:      conn,
		contextID: "ctx-1",
		provider:  NewRespeecherTTS("test-key", ""),
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
		if statusErr.StatusCode != websocket.CloseNormalClosure {
			t.Fatalf("StatusCode = %d, want normal close code", statusErr.StatusCode)
		}
		if !strings.Contains(err.Error(), "Respeecher connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Respeecher close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket normal close error")
	}
}

func newRespeecherProviderCloseWebsocketConn(t *testing.T, closeCode int) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	listener := newRespeecherSingleConnListener(serverConn)
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(closeCode, ""),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	})}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			serverErr <- err
		}
	}()
	dialer := websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	conn, _, err := dialer.Dial("ws://respeecher.test/stream", nil)
	if err != nil {
		clientConn.Close()
		t.Fatalf("dial test websocket: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		_ = conn.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case err := <-serverErr:
			t.Errorf("test websocket server error: %v", err)
		default:
		}
	})
	return conn
}

type respeecherSingleConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newRespeecherSingleConnListener(conn net.Conn) *respeecherSingleConnListener {
	return &respeecherSingleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *respeecherSingleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() {
		conn = l.conn
	})
	if conn != nil {
		return conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *respeecherSingleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *respeecherSingleConnListener) Addr() net.Addr {
	return respeecherTestAddr("respeecher.test:443")
}

type respeecherTestAddr string

func (a respeecherTestAddr) Network() string { return "tcp" }

func (a respeecherTestAddr) String() string { return string(a) }

type respeecherRoundTripFunc func(*http.Request) (*http.Response, error)

func (f respeecherRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func respeecherTestWAV(pcm []byte, sampleRate uint32, channels uint16) []byte {
	var wav bytes.Buffer
	blockAlign := channels * 2
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
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)))
	wav.Write(pcm)
	return wav.Bytes()
}

func TestRespeecherTTSAudioFromStreamMessage(t *testing.T) {
	audio, done, err := respeecherTTSAudioFromStreamMessage([]byte(`{"context_id":"ctx-1","type":"chunk","data":"`+base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})+`"}`), "ctx-1", 24000)
	if err != nil {
		t.Fatalf("audio from stream message: %v", err)
	}
	if done {
		t.Fatal("done = true for chunk message")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded audio frame", audio)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24000 Hz mono", audio.Frame)
	}

	other, done, err := respeecherTTSAudioFromStreamMessage([]byte(`{"context_id":"ctx-2","type":"chunk","data":"AQI="}`), "ctx-1", 24000)
	if err != nil {
		t.Fatalf("other context message: %v", err)
	}
	if other != nil || done {
		t.Fatalf("other=%+v done=%v, want ignored message", other, done)
	}

	finished, done, err := respeecherTTSAudioFromStreamMessage([]byte(`{"context_id":"ctx-1","type":"done"}`), "ctx-1", 24000)
	if err != nil {
		t.Fatalf("done message: %v", err)
	}
	if !done {
		t.Fatalf("done=%v, want true for done message", done)
	}
	if finished == nil || !finished.IsFinal {
		t.Fatalf("finished=%+v, want reference final marker", finished)
	}
	if finished.Frame != nil {
		t.Fatalf("finished frame = %+v, want boundary-only final marker", finished.Frame)
	}

	if _, _, err := respeecherTTSAudioFromStreamMessage([]byte(`{"context_id":"ctx-1","type":"error","error":"bad text"}`), "ctx-1", 24000); err == nil {
		t.Fatal("error message returned nil error, want stream error")
	} else {
		var apiErr *llm.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error message error = %T %v, want APIError", err, err)
		}
		if apiErr.Message != "Respeecher returned error: bad text" {
			t.Fatalf("APIError message = %q, want reference message", apiErr.Message)
		}
	}
}

func TestRespeecherTTSImplementsStreamingInterface(t *testing.T) {
	var _ tts.TTS = NewRespeecherTTS("test-key", "")
}

func assertRespeecherPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}
