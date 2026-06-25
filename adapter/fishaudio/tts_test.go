package fishaudio

import (
	"bytes"
	"context"
	"encoding/binary"
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
	"github.com/vmihailenco/msgpack/v5"
)

type fishAudioFinalEOFReader struct {
	data []byte
	read bool
}

func (r *fishAudioFinalEOFReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, errors.New("read after final eof")
	}
	r.read = true
	return copy(p, r.data), io.EOF
}

func (r *fishAudioFinalEOFReader) Close() error { return nil }

func TestFishAudioTTSDefaultsMatchReference(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "")

	if provider.baseURL != "https://api.fish.audio" {
		t.Fatalf("base URL = %q, want reference API base", provider.baseURL)
	}
	if provider.model != "s2-pro" {
		t.Fatalf("model = %q, want s2-pro", provider.model)
	}
	if provider.voice != "933563129e564b19a115bedd57b7406a" {
		t.Fatalf("voice = %q, want default voice id", provider.voice)
	}
	if provider.outputFormat != "wav" {
		t.Fatalf("output format = %q, want wav", provider.outputFormat)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want wav default 24000", provider.sampleRate)
	}
	if provider.latencyMode != "balanced" {
		t.Fatalf("latency mode = %q, want balanced", provider.latencyMode)
	}
	if provider.chunkLength != 100 {
		t.Fatalf("chunk length = %d, want 100", provider.chunkLength)
	}
	if got := tts.Model(provider); got != "s2-pro" {
		t.Fatalf("model metadata = %q, want s2-pro", got)
	}
	if got := tts.Provider(provider); got != "FishAudio" {
		t.Fatalf("provider metadata = %q, want FishAudio", got)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming = false, want true")
	}
}

func TestNewFishAudioTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FISHAUDIO_API_KEY", "env-key")
	t.Setenv("FISH_AUDIO_API_KEY", "fallback-env-key")

	provider := NewFishAudioTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want primary env key", provider.apiKey)
	}

	explicit := NewFishAudioTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewFishAudioTTSUsesReferenceEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FISH_API_KEY", "reference-env-key")
	t.Setenv("FISHAUDIO_API_KEY", "")
	t.Setenv("FISH_AUDIO_API_KEY", "")

	provider := NewFishAudioTTS("", "")

	if provider.apiKey != "reference-env-key" {
		t.Fatalf("api key = %q, want reference env key", provider.apiKey)
	}
}

func TestNewFishAudioTTSUsesFallbackEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FISHAUDIO_API_KEY", "")
	t.Setenv("FISH_AUDIO_API_KEY", "fallback-env-key")

	provider := NewFishAudioTTS("", "")

	if provider.apiKey != "fallback-env-key" {
		t.Fatalf("api key = %q, want fallback env key", provider.apiKey)
	}
}

func TestFishAudioTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("FISH_API_KEY", "")
	t.Setenv("FISHAUDIO_API_KEY", "")
	t.Setenv("FISH_AUDIO_API_KEY", "")
	provider := NewFishAudioTTS("", "", WithFishAudioTTSBaseURL("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "FISH_API_KEY") {
		t.Fatalf("Synthesize error = %q, want FISH_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "FISH_API_KEY") {
		t.Fatalf("Stream error = %q, want FISH_API_KEY guidance", err)
	}
}

func TestFishAudioTTSSynthesizeRequestUsesReferenceMsgpackPayload(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "")

	req, err := buildFishAudioTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.fish.audio/v1/tts" {
		t.Fatalf("url = %q, want /v1/tts endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/msgpack" {
		t.Fatalf("content type = %q, want application/msgpack", got)
	}
	if got := req.Header.Get("model"); got != "s2-pro" {
		t.Fatalf("model header = %q, want s2-pro", got)
	}

	var payload map[string]any
	if err := msgpack.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode msgpack body: %v", err)
	}
	assertFishPayload(t, payload, "text", "hello")
	assertFishPayload(t, payload, "format", "wav")
	assertFishPayload(t, payload, "reference_id", "933563129e564b19a115bedd57b7406a")
	assertFishPayload(t, payload, "latency", "balanced")
	if got := payload["chunk_length"]; got != int8(100) && got != int64(100) && got != 100 {
		t.Fatalf("chunk_length = %#v, want 100", got)
	}
	if got := fishPayloadInt(payload["sample_rate"]); got != 24000 {
		t.Fatalf("sample_rate = %#v, want 24000", got)
	}
	if got := payload["normalize"]; got != true {
		t.Fatalf("normalize = %#v, want true", got)
	}
	if got := fishPayloadInt(payload["mp3_bitrate"]); got != 64 {
		t.Fatalf("mp3_bitrate = %#v, want 64", got)
	}
	if got := fishPayloadInt(payload["opus_bitrate"]); got != 64000 {
		t.Fatalf("opus_bitrate = %#v, want 64000", got)
	}
	if got := payload["top_p"]; got != 0.7 {
		t.Fatalf("top_p = %#v, want 0.7", got)
	}
	if got := payload["temperature"]; got != 0.7 {
		t.Fatalf("temperature = %#v, want 0.7", got)
	}
}

func TestFishAudioTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: fishAudioRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	provider := NewFishAudioTTS("test-key", "")

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
	if body, ok := statusErr.Body.(string); !ok || !strings.Contains(body, "rate limited") {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestFishAudioTTSOptionsMatchReference(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "",
		WithFishAudioTTSBaseURL("https://fish.example"),
		WithFishAudioTTSModel("s1"),
		WithFishAudioTTSVoice("voice-2"),
		WithFishAudioTTSOutputFormat("opus"),
		WithFishAudioTTSSampleRate(48000),
		WithFishAudioTTSLatencyMode("low"),
		WithFishAudioTTSChunkLength(250),
	)

	req, err := buildFishAudioTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://fish.example/v1/tts" {
		t.Fatalf("url = %q, want custom base /v1/tts endpoint", req.URL.String())
	}
	if got := req.Header.Get("model"); got != "s1" {
		t.Fatalf("model header = %q, want s1", got)
	}

	var payload map[string]any
	if err := msgpack.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode msgpack body: %v", err)
	}
	assertFishPayload(t, payload, "reference_id", "voice-2")
	assertFishPayload(t, payload, "format", "opus")
	assertFishPayload(t, payload, "latency", "low")
	if got := fishPayloadInt(payload["sample_rate"]); got != 48000 {
		t.Fatalf("sample_rate = %#v, want 48000", got)
	}
	if got := fishPayloadInt(payload["chunk_length"]); got != 250 {
		t.Fatalf("chunk_length = %#v, want 250", got)
	}
}

func TestFishAudioTTSUpdateOptionsAffectsFutureRequests(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "",
		WithFishAudioTTSBaseURL("https://fish.example"),
		WithFishAudioTTSModel("s1"),
		WithFishAudioTTSVoice("voice-1"),
		WithFishAudioTTSLatencyMode("balanced"),
		WithFishAudioTTSChunkLength(100),
	)

	provider.UpdateOptions(
		WithFishAudioTTSModel("s2-pro"),
		WithFishAudioTTSVoice("voice-2"),
		WithFishAudioTTSLatencyMode("low"),
		WithFishAudioTTSChunkLength(250),
	)

	req, err := buildFishAudioTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("model"); got != "s2-pro" {
		t.Fatalf("model header = %q, want updated model", got)
	}
	var payload map[string]any
	if err := msgpack.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode msgpack body: %v", err)
	}
	assertFishPayload(t, payload, "reference_id", "voice-2")
	assertFishPayload(t, payload, "latency", "low")
	if got := fishPayloadInt(payload["chunk_length"]); got != 250 {
		t.Fatalf("chunk_length = %#v, want 250", got)
	}
}

func TestFishAudioTTSUpdateOptionsRejectsInvalidChunkLength(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "", WithFishAudioTTSChunkLength(150))

	err := provider.UpdateOptions(WithFishAudioTTSChunkLength(99))
	if err == nil || !strings.Contains(err.Error(), "chunk_length") {
		t.Fatalf("UpdateOptions error = %v, want chunk_length validation", err)
	}
	if provider.chunkLength != 150 {
		t.Fatalf("chunk length = %d, want previous valid value", provider.chunkLength)
	}

	err = provider.UpdateOptions(WithFishAudioTTSChunkLength(301))
	if err == nil || !strings.Contains(err.Error(), "chunk_length") {
		t.Fatalf("UpdateOptions high error = %v, want chunk_length validation", err)
	}
	if provider.chunkLength != 150 {
		t.Fatalf("chunk length after high value = %d, want previous valid value", provider.chunkLength)
	}
}

func TestFishAudioTTSChunkedStreamDecodesReferenceWAVResponse(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0x03, 0x04}
	stream := &fishaudioTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(fishAudioTestWAV(pcm, 48000, 1)))},
		sampleRate: 48000,
		format:     "wav",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want 48000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want mono wav metadata", audio.Frame.NumChannels)
	}
	if !bytes.Equal(audio.Frame.Data, pcm) {
		t.Fatalf("audio data = %#v, want decoded wav pcm", audio.Frame.Data)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second Next = %+v, want final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestFishAudioTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &fishaudioTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(fishAudioTestWAV([]byte{0x01, 0x02}, 24000, 1)))},
		sampleRate: 24000,
		format:     "wav",
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
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestFishAudioTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &fishaudioTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(nil))},
		sampleRate: 24000,
		format:     "wav",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("Next = %+v, want final marker", audio)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestFishAudioTTSChunkedStreamKeepsAudioReturnedWithEOF(t *testing.T) {
	stream := &fishaudioTTSChunkedStream{
		resp:       &http.Response{Body: &fishAudioFinalEOFReader{data: []byte{0x01, 0x02}}},
		sampleRate: 24000,
		format:     "pcm",
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next audio error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first Next = %+v, want audio frame before final marker", audio)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{0x01, 0x02}) {
		t.Fatalf("audio data = %v, want final read bytes", got)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next final error = %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final Next = %+v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestFishAudioTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &fishAudioCloseCountBody{Reader: bytes.NewReader(fishAudioTestWAV([]byte{0x01, 0x02}, 24000, 1))}
	stream := &fishaudioTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 24000,
		format:     "wav",
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

func TestFishAudioTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	stream := &fishaudioTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(fishAudioTestWAV([]byte{0x01, 0x02}, 24000, 1)))},
		sampleRate: 24000,
		format:     "wav",
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close error = %T %v, want EOF", err, err)
	}
}

func TestFishAudioTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "", WithFishAudioTTSBaseURL("https://fish.example"))

	if got := buildFishAudioTTSWebsocketURL(provider); got != "wss://fish.example/v1/tts/live" {
		t.Fatalf("websocket URL = %q, want live websocket URL", got)
	}
	headers := buildFishAudioTTSWebsocketHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
	if headers.Get("model") != "s2-pro" {
		t.Fatalf("model = %q, want s2-pro", headers.Get("model"))
	}
	if headers.Get("User-Agent") == "" {
		t.Fatal("User-Agent missing")
	}
}

func TestFishAudioTTSStreamMessagesMatchReference(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "")

	start, err := buildFishAudioTTSStartMessage(provider)
	if err != nil {
		t.Fatalf("start message: %v", err)
	}
	var startPayload map[string]any
	if err := msgpack.Unmarshal(start, &startPayload); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if startPayload["event"] != "start" {
		t.Fatalf("start event = %#v, want start", startPayload["event"])
	}
	request := startPayload["request"].(map[string]any)
	assertFishPayload(t, request, "text", "")
	assertFishPayload(t, request, "reference_id", "933563129e564b19a115bedd57b7406a")

	text, err := buildFishAudioTTSTextMessage("hello")
	if err != nil {
		t.Fatalf("text message: %v", err)
	}
	var textPayload map[string]any
	if err := msgpack.Unmarshal(text, &textPayload); err != nil {
		t.Fatalf("decode text: %v", err)
	}
	if textPayload["event"] != "text" || textPayload["text"] != "hello " {
		t.Fatalf("text payload = %+v, want text event with trailing space", textPayload)
	}

	flush, _ := buildFishAudioTTSSimpleEvent("flush")
	stop, _ := buildFishAudioTTSSimpleEvent("stop")
	assertFishEvent(t, flush, "flush")
	assertFishEvent(t, stop, "stop")
}

func TestFishAudioTTSStreamSendsSentencesAndFlushesTailLikeReference(t *testing.T) {
	var writes []string
	stream := &fishAudioTTSSynthesizeStream{
		writeMessage: func(_ int, payload []byte) error {
			var msg map[string]any
			if err := msgpack.Unmarshal(payload, &msg); err != nil {
				t.Fatalf("decode stream write: %v", err)
			}
			event, _ := msg["event"].(string)
			if event == "text" {
				text, _ := msg["text"].(string)
				writes = append(writes, "text:"+text)
				return nil
			}
			writes = append(writes, event)
			return nil
		},
	}

	if err := stream.PushText("This first sentence is definitely long enough. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	wantAfterPush := []string{"text:This first sentence is definitely long enough. ", "flush"}
	if strings.Join(writes, "|") != strings.Join(wantAfterPush, "|") {
		t.Fatalf("writes after PushText = %#v, want %#v", writes, wantAfterPush)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	wantAfterFlush := []string{
		"text:This first sentence is definitely long enough. ",
		"flush",
		"text:Tail ",
		"flush",
	}
	if strings.Join(writes, "|") != strings.Join(wantAfterFlush, "|") {
		t.Fatalf("writes after Flush = %#v, want %#v", writes, wantAfterFlush)
	}
}

func TestFishAudioTTSStreamEndInputFlushesTailAndStopsOnce(t *testing.T) {
	var writes []string
	stream := &fishAudioTTSSynthesizeStream{
		writeMessage: func(_ int, payload []byte) error {
			var msg map[string]any
			if err := msgpack.Unmarshal(payload, &msg); err != nil {
				t.Fatalf("decode stream write: %v", err)
			}
			event, _ := msg["event"].(string)
			if event == "text" {
				text, _ := msg["text"].(string)
				writes = append(writes, "text:"+text)
				return nil
			}
			writes = append(writes, event)
			return nil
		},
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

	want := []string{"text:Tail ", "flush", "stop"}
	if strings.Join(writes, "|") != strings.Join(want, "|") {
		t.Fatalf("writes = %#v, want %#v", writes, want)
	}
}

func TestFishAudioTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &fishAudioTTSSynthesizeStream{
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

func TestFishAudioTTSProviderCloseClosesActiveStreams(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "")
	ctx, cancel := context.WithCancel(context.Background())
	closeCalls := 0
	var stopSeen bool
	stream := &fishAudioTTSSynthesizeStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		writeMessage: func(_ int, data []byte) error {
			var msg map[string]any
			if err := msgpack.Unmarshal(data, &msg); err != nil {
				t.Fatalf("decode close message: %v", err)
			}
			stopSeen = msg["event"] == "stop"
			return nil
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if !stopSeen {
		t.Fatal("stop event not sent on provider Close")
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	provider.mu.Lock()
	active := len(provider.streams)
	provider.mu.Unlock()
	if active != 0 {
		t.Fatalf("active streams after Close = %d, want 0", active)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("stream context still active after provider Close")
	}
}

func TestFishAudioTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &fishAudioTTSSynthesizeStream{
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

func TestFishAudioTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &fishAudioTTSSynthesizeStream{
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

func TestFishAudioTTSNextReturnsQueuedAudioBeforeStreamError(t *testing.T) {
	providerErr := errors.New("provider failed after audio")
	for i := range 200 {
		want := &tts.SynthesizedAudio{RequestID: "req-audio"}
		stream := &fishAudioTTSSynthesizeStream{
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

func TestFishAudioTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fishAudioRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/mpeg"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	defer func() { http.DefaultClient = oldClient }()

	provider := NewFishAudioTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize error = %v, want io.ErrClosedPipe", err)
	}
	if httpCalls != 0 {
		t.Fatalf("HTTP calls after Close = %d, want 0", httpCalls)
	}
}

func TestFishAudioTTSStreamAfterCloseIsRejected(t *testing.T) {
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

	provider := NewFishAudioTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream error = %v, want io.ErrClosedPipe", err)
	}
	if dialCalls != 0 {
		t.Fatalf("websocket dials after Close = %d, want 0", dialCalls)
	}
}

func TestFishAudioTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	conn := newFishAudioProviderCloseWebsocketConn(t, websocket.CloseUnsupportedData)

	stream := &fishAudioTTSSynthesizeStream{
		conn:       conn,
		sampleRate: 24000,
		format:     "wav",
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
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
		if !strings.Contains(err.Error(), "Fish Audio websocket connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Fish Audio close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close error")
	}
}

func TestFishAudioTTSStreamNormalCloseBeforeFinishReturnsAPIStatusError(t *testing.T) {
	conn := newFishAudioProviderCloseWebsocketConn(t, websocket.CloseNormalClosure)

	stream := &fishAudioTTSSynthesizeStream{
		conn:       conn,
		sampleRate: 24000,
		format:     "wav",
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
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
		if !strings.Contains(err.Error(), "Fish Audio websocket connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Fish Audio close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket normal close error")
	}
}

func newFishAudioProviderCloseWebsocketConn(t *testing.T, closeCode int) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	listener := newFishAudioSingleConnListener(serverConn)
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
	conn, _, err := dialer.Dial("ws://fish.audio.test/stream", nil)
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

type fishAudioSingleConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newFishAudioSingleConnListener(conn net.Conn) *fishAudioSingleConnListener {
	return &fishAudioSingleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *fishAudioSingleConnListener) Accept() (net.Conn, error) {
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

func (l *fishAudioSingleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *fishAudioSingleConnListener) Addr() net.Addr {
	return fishAudioTestAddr("fish.audio.test:443")
}

type fishAudioTestAddr string

func (a fishAudioTestAddr) Network() string { return "tcp" }

func (a fishAudioTestAddr) String() string { return string(a) }

func TestFishAudioTTSAudioFromStreamMessage(t *testing.T) {
	pcm := []byte{1, 2, 3, 4}
	audio, done, err := fishAudioTTSAudioFromStreamMessage(mustFishMessage(t, map[string]any{
		"event": "audio",
		"audio": fishAudioTestWAV(pcm, 24000, 1),
	}), 24000, "wav")
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if done {
		t.Fatal("done = true for audio event")
	}
	if audio == nil || !bytes.Equal(audio.Frame.Data, pcm) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24 kHz mono", audio.Frame)
	}

	finished, done, err := fishAudioTTSAudioFromStreamMessage(mustFishMessage(t, map[string]any{
		"event": "finish",
	}), 24000, "wav")
	if err != nil {
		t.Fatalf("finish event: %v", err)
	}
	if finished == nil || !finished.IsFinal || !done {
		t.Fatalf("finished=%+v done=%v, want final marker and done", finished, done)
	}
	if finished.Frame != nil {
		t.Fatalf("final marker frame = %+v, want no audio frame", finished.Frame)
	}

	finished, done, err = fishAudioTTSAudioFromStreamMessage(mustFishMessage(t, map[string]any{
		"event":  "finish",
		"reason": "error",
	}), 24000, "wav")
	if finished != nil || done {
		t.Fatalf("error finish = finished=%+v done=%v, want no final marker", finished, done)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error finish err = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != -1 {
		t.Fatalf("status code = %d, want -1", statusErr.StatusCode)
	}
}

func TestFishAudioTTSImplementsStreamingInterface(t *testing.T) {
	var _ tts.TTS = NewFishAudioTTS("test-key", "")
}

func assertFishPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func fishPayloadInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case uint:
		return int(v)
	default:
		return 0
	}
}

func assertFishEvent(t *testing.T, encoded []byte, want string) {
	t.Helper()
	var payload map[string]any
	if err := msgpack.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if payload["event"] != want {
		t.Fatalf("event = %#v, want %q", payload["event"], want)
	}
}

type fishAudioCloseCountBody struct {
	*bytes.Reader
	closeCount int
}

func (b *fishAudioCloseCountBody) Close() error {
	b.closeCount++
	if b.closeCount > 1 {
		return errors.New("closed twice")
	}
	return nil
}

type fishAudioRoundTripFunc func(*http.Request) (*http.Response, error)

func (f fishAudioRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func mustFishMessage(t *testing.T, message map[string]any) []byte {
	t.Helper()
	encoded, err := msgpack.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	return encoded
}

func fishAudioTestWAV(pcm []byte, sampleRate uint32, channels uint16) []byte {
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
