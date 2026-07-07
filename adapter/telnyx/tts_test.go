package telnyx

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestTelnyxTTSDefaultsMatchReference(t *testing.T) {
	provider := NewTelnyxTTS("test-key", "")

	if provider.voice != "Telnyx.NaturalHD.astra" {
		t.Fatalf("voice = %q, want reference default", provider.voice)
	}
	if provider.baseURL != "wss://api.telnyx.com/v2/text-to-speech/speech" {
		t.Fatalf("base URL = %q, want reference websocket endpoint", provider.baseURL)
	}
	if provider.SampleRate() != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.SampleRate())
	}
	if got := tts.Model(provider); got != "Telnyx.NaturalHD.astra" {
		t.Fatalf("model metadata = %q, want reference voice", got)
	}
	if got := tts.Provider(provider); got != "telnyx" {
		t.Fatalf("provider metadata = %q, want telnyx", got)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming = false, want true")
	}
}

func TestNewTelnyxTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "env-key")

	provider := NewTelnyxTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewTelnyxTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestTelnyxTTSStreamRequiresAPIKeyBeforeDial(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "")
	provider := NewTelnyxTTS("", "", WithTelnyxTTSBaseURL("://bad-url"))

	_, err := provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "TELNYX_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", err)
	}
}

func TestTelnyxTTSStreamFlushDialFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("telnyx websocket dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewTelnyxTTS("test-key", "")
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v, want lazy stream construction", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	err = stream.Flush()
	if err == nil {
		t.Fatal("Flush error = nil, want APIConnectionError")
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Flush error = %T %v, want APIConnectionError", err, err)
	}
}

func TestTelnyxTTSStreamDialHTTPStatusReturnsAPIStatusError(t *testing.T) {
	err := telnyxTTSDialError(errors.New("websocket: bad handshake"), &http.Response{StatusCode: http.StatusTooManyRequests})

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("dial error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, http.StatusTooManyRequests)
	}
}

func TestTelnyxTTSSynthesizeDefersReferenceConnectUntilNext(t *testing.T) {
	dials := 0
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("telnyx websocket dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewTelnyxTTS("test-key", "", WithTelnyxTTSBaseURL("wss://telnyx.test/v2/tts"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	if dials != 0 {
		t.Fatalf("dials before Next = %d, want 0", dials)
	}

	_, err = stream.Next()
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if dials != 1 {
		t.Fatalf("dials after Next = %d, want 1", dials)
	}

	closedStream, err := provider.Synthesize(context.Background(), "cancelled")
	if err != nil {
		t.Fatalf("second Synthesize error = %v", err)
	}
	if err := closedStream.Close(); err != nil {
		t.Fatalf("Close before Next error = %v", err)
	}
	if audio, err := closedStream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after close = (%#v, %v), want nil EOF", audio, err)
	}
	if dials != 1 {
		t.Fatalf("dials after close-before-Next = %d, want 1", dials)
	}
}

func TestTelnyxTTSSynthesizeUsesReferenceEndInput(t *testing.T) {
	fakeStream := &fakeTelnyxEndInputTTSStream{}
	stream := &telnyxTTSChunkedStream{
		provider: &fakeTelnyxChunkedTTSProvider{stream: fakeStream},
		ctx:      context.Background(),
		text:     "hello",
	}
	if err := stream.ensureStream(); err != nil {
		t.Fatalf("ensureStream error = %v", err)
	}

	want := []string{"PushText:hello", "EndInput"}
	if !reflect.DeepEqual(fakeStream.calls, want) {
		t.Fatalf("stream calls = %#v, want %#v", fakeStream.calls, want)
	}
}

func TestTelnyxTTSSynthesizeNoAudioReturnsReferenceError(t *testing.T) {
	stream := &telnyxTTSChunkedStream{
		provider: &fakeTelnyxChunkedTTSProvider{stream: &fakeTelnyxEndInputTTSStream{}},
		ctx:      context.Background(),
		text:     "hello",
	}

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("audio = %+v, want nil", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIError", err, err)
	}
	if !strings.Contains(err.Error(), "no audio frames were pushed for text: hello") {
		t.Fatalf("Next error = %q, want reference no-audio error", err)
	}
}

func TestTelnyxTTSStreamURLAndHeadersMatchReference(t *testing.T) {
	provider := NewTelnyxTTS("test-key", "voice-1", WithTelnyxTTSBaseURL("wss://telnyx.example/speech"))

	streamURL, err := url.Parse(buildTelnyxTTSStreamURL(provider))
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if streamURL.Scheme != "wss" || streamURL.Host != "telnyx.example" || streamURL.Path != "/speech" {
		t.Fatalf("stream URL = %q, want configured websocket URL", streamURL.String())
	}
	if streamURL.Query().Get("voice") != "voice-1" {
		t.Fatalf("voice query = %q, want voice-1", streamURL.Query().Get("voice"))
	}

	headers := buildTelnyxTTSHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
}

func TestTelnyxTTSTextMessagesMatchReference(t *testing.T) {
	warmup := buildTelnyxTTSTextMessage(" ")
	text := buildTelnyxTTSTextMessage("hello")
	flush := buildTelnyxTTSTextMessage("")

	assertTelnyxTextPayload(t, warmup, " ")
	assertTelnyxTextPayload(t, text, "hello")
	assertTelnyxTextPayload(t, flush, "")
}

func TestTelnyxTTSStreamBuffersTextUntilFlushLikeReference(t *testing.T) {
	var writes []string
	stream := &telnyxTTSStream{
		writeMessage: func(message map[string]string) error {
			writes = append(writes, message["text"])
			return nil
		},
	}

	if err := stream.PushText("hello "); err != nil {
		t.Fatalf("PushText first error = %v", err)
	}
	if err := stream.PushText("world"); err != nil {
		t.Fatalf("PushText second error = %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes after PushText = %#v, want buffered text with no websocket writes", writes)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	want := []string{"hello world", ""}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes after Flush = %#v, want %#v", writes, want)
	}
}

func TestTelnyxTTSStreamFlushStartsReferenceSegmentWebsockets(t *testing.T) {
	var segments []*fakeTelnyxEndInputTTSStream
	provider := NewTelnyxTTS("test-key", "")
	provider.openSegment = func(context.Context) (tts.SynthesizeStream, error) {
		segment := &fakeTelnyxEndInputTTSStream{
			events: []*tts.SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{0x01, 0x02}}}},
		}
		segments = append(segments, segment)
		return segment, nil
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("first"); err != nil {
		t.Fatalf("PushText first error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush first error = %v", err)
	}
	if err := stream.PushText("second"); err != nil {
		t.Fatalf("PushText second error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush second error = %v", err)
	}

	if len(segments) != 1 {
		t.Fatalf("segment streams after second Flush = %d, want second websocket deferred until first drains", len(segments))
	}
	if want := []string{"PushText:first", "EndInput"}; !reflect.DeepEqual(segments[0].calls, want) {
		t.Fatalf("first segment calls = %#v, want %#v", segments[0].calls, want)
	}
	if audio, err := stream.Next(); err != nil || audio == nil || audio.Frame == nil {
		t.Fatalf("first segment audio = (%+v, %v), want audio", audio, err)
	}
	if final, err := stream.Next(); err != nil || final == nil || !final.IsFinal {
		t.Fatalf("first segment final = (%+v, %v), want final marker", final, err)
	}
	if audio, err := stream.Next(); err != nil || audio == nil || audio.Frame == nil {
		t.Fatalf("second segment audio = (%+v, %v), want audio after first drains", audio, err)
	}
	if len(segments) != 2 {
		t.Fatalf("segment streams after first drain = %d, want second provider websocket", len(segments))
	}
	if want := []string{"PushText:second", "EndInput"}; !reflect.DeepEqual(segments[1].calls, want) {
		t.Fatalf("second segment calls = %#v, want %#v", segments[1].calls, want)
	}
}

func TestTelnyxTTSStreamSegmentWriteFailureClosesStream(t *testing.T) {
	writeErr := errors.New("segment write failed")
	segment := &fakeTelnyxEndInputTTSStream{endErr: writeErr}
	provider := NewTelnyxTTS("test-key", "")
	provider.openSegment = func(context.Context) (tts.SynthesizeStream, error) {
		return segment, nil
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	err = stream.Flush()
	if !errors.Is(err, writeErr) {
		t.Fatalf("Flush error = %v, want segment write failure", err)
	}
	if !segment.closed {
		t.Fatal("failed segment was not closed")
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after segment failure error = %v, want io.ErrClosedPipe", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after segment failure error = %v, want EOF", err)
	}
}

func TestTelnyxTTSStreamSegmentNextFailureClosesStream(t *testing.T) {
	nextErr := errors.New("segment next failed")
	segment := &fakeTelnyxEndInputTTSStream{nextErr: nextErr}
	provider := NewTelnyxTTS("test-key", "")
	provider.openSegment = func(context.Context) (tts.SynthesizeStream, error) {
		return segment, nil
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	if _, err := stream.Next(); !errors.Is(err, nextErr) {
		t.Fatalf("Next error = %v, want segment next failure", err)
	}
	if !segment.closed {
		t.Fatal("failed segment was not closed")
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after segment next failure error = %v, want io.ErrClosedPipe", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after segment failure error = %v, want EOF", err)
	}
}

func TestTelnyxTTSStreamNoAudioSegmentReturnsReferenceError(t *testing.T) {
	provider := NewTelnyxTTS("test-key", "")
	provider.openSegment = func(context.Context) (tts.SynthesizeStream, error) {
		return &fakeTelnyxEndInputTTSStream{}, nil
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if err := tts.EndSynthesizeStreamInput(stream); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("audio = %+v, want nil", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIError", err, err)
	}
	if !strings.Contains(err.Error(), "no audio frames were pushed for text: hello") {
		t.Fatalf("Next error = %q, want reference no-audio error", err)
	}
}

func TestTelnyxTTSStreamSynthesizesReferenceFinalMarker(t *testing.T) {
	frame := &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	provider := NewTelnyxTTS("test-key", "")
	provider.openSegment = func(context.Context) (tts.SynthesizeStream, error) {
		return &fakeTelnyxEndInputTTSStream{
			events: []*tts.SynthesizedAudio{{Frame: frame}},
		}, nil
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if err := tts.EndSynthesizeStreamInput(stream); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if audio == nil || audio.Frame != frame {
		t.Fatalf("first audio = %+v, want segment frame", audio)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want reference final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second audio = %+v, want boundary-only final marker", final)
	}
}

func TestTelnyxTTSStreamEndInputFlushesReferenceSegment(t *testing.T) {
	var writes []string
	stream := &telnyxTTSStream{
		writeMessage: func(message map[string]string) error {
			writes = append(writes, message["text"])
			return nil
		},
		closeConn: func() error {
			t.Fatal("EndInput closed websocket; want output side open for audio")
			return nil
		},
	}

	if err := stream.PushText("hello "); err != nil {
		t.Fatalf("PushText first error = %v", err)
	}
	if err := stream.PushText("world"); err != nil {
		t.Fatalf("PushText second error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	want := []string{"hello world", ""}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes after EndInput = %#v, want %#v", writes, want)
	}
	if err := stream.PushText("ignored"); err != nil {
		t.Fatalf("PushText after EndInput error = %v, want nil like reference closed input", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after EndInput error = %v, want nil like reference closed input", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("second EndInput error = %v, want nil like reference closed input", err)
	}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes after closed input = %#v, want unchanged %#v", writes, want)
	}
}

func TestTelnyxTTSStreamWriteFailureReturnsAPIConnectionError(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &telnyxTTSStream{
		cancel: func() { cancelled = true },
		writeMessage: func(map[string]string) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText error = %v, want buffered text accepted", err)
	}
	err := stream.Flush()
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Flush error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "Telnyx TTS websocket write failed") || !strings.Contains(err.Error(), writeErr.Error()) {
		t.Fatalf("Flush error = %q, want Telnyx write context", err)
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

func TestTelnyxTTSWarmupWriteFailureReturnsAPIConnectionError(t *testing.T) {
	writeErr := errors.New("write failed")
	err := telnyxTTSWarmupWriteError(writeErr)

	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("warmup error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "Telnyx TTS websocket write failed") || !strings.Contains(err.Error(), writeErr.Error()) {
		t.Fatalf("warmup error = %q, want write failure context", err)
	}
}

func TestTelnyxTTSProviderCloseClosesActiveStreams(t *testing.T) {
	cancelled := false
	closeCalls := 0
	provider := NewTelnyxTTS("test-key", "")
	stream := &telnyxTTSStream{
		cancel: func() { cancelled = true },
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

func TestTelnyxTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &telnyxTTSStream{
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

func TestTelnyxTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &telnyxTTSStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error),
		closed: true,
	}
	stream.events <- &tts.SynthesizedAudio{RequestID: "stale"}

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestTelnyxTTSNextReturnsQueuedAudioBeforeStreamError(t *testing.T) {
	providerErr := errors.New("provider failed after audio")
	for i := range 200 {
		want := &tts.SynthesizedAudio{RequestID: "req-audio"}
		stream := &telnyxTTSStream{
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

func TestTelnyxTTSRegisterStreamAfterCloseClosesStream(t *testing.T) {
	cancelled := false
	closeCalls := 0
	provider := NewTelnyxTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	stream := &telnyxTTSStream{
		cancel: func() { cancelled = true },
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if provider.registerStream(stream) {
		t.Fatal("registerStream after provider Close = true, want false")
	}
	if !cancelled {
		t.Fatal("cancel not called for stream registered after provider Close")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after rejected registration error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after rejected registration error = %v, want io.ErrClosedPipe", err)
	}
	if len(provider.streams) != 0 {
		t.Fatalf("provider streams = %d, want 0", len(provider.streams))
	}
}

func TestTelnyxTTSStreamAfterCloseIsRejected(t *testing.T) {
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

	provider := NewTelnyxTTS("test-key", "", WithTelnyxTTSBaseURL("wss://telnyx.test/v2/tts"))
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream after Close stream = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
	if dialCalls != 0 {
		t.Fatalf("Stream after Close dial calls = %d, want 0", dialCalls)
	}
}

func TestTelnyxTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
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

	provider := NewTelnyxTTS("test-key", "", WithTelnyxTTSBaseURL("wss://telnyx.test/v2/tts"))
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize after Close stream = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if dialCalls != 0 {
		t.Fatalf("Synthesize after Close dial calls = %d, want 0", dialCalls)
	}
}

func TestTelnyxTTSAudioFromMessageDecodesBase64Audio(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{
		"audio": base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}),
	})

	audio, done, err := telnyxTTSAudioFromMessage(payload, 16000)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if done {
		t.Fatal("done = true, want false for audio message")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}
	if audio.Frame.SampleRate != 16000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 16 kHz mono", audio.Frame)
	}

	empty, done, err := telnyxTTSAudioFromMessage([]byte(`{}`), 16000)
	if err != nil {
		t.Fatalf("empty message: %v", err)
	}
	if empty != nil || done {
		t.Fatalf("empty=%+v done=%v, want ignored no-audio message", empty, done)
	}

	audioBytes, done, err := telnyxTTSAudioBytesFromMessage([]byte(`not json`))
	if err != nil || audioBytes != nil || done {
		t.Fatalf("invalid JSON = audio=%v done=%v err=%v, want ignored message", audioBytes, done, err)
	}

	audioBytes, done, err = telnyxTTSAudioBytesFromMessage([]byte(`{}`))
	if err != nil || audioBytes != nil || done {
		t.Fatalf("no-audio JSON = audio=%v done=%v err=%v, want ignored message", audioBytes, done, err)
	}
}

func TestTelnyxTTSAudioFromMessageReturnsAPIConnectionErrorOnMalformedAudio(t *testing.T) {
	audioBytes, done, err := telnyxTTSAudioBytesFromMessage([]byte(`{"audio":"not-base64"}`))
	if audioBytes != nil || done {
		t.Fatalf("malformed audio = audio=%v done=%v, want no audio and not done", audioBytes, done)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("malformed audio error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "Telnyx TTS audio decode failed") {
		t.Fatalf("malformed audio error = %q, want decode context", err)
	}
}

func TestTelnyxTTSAudioFromMessageIgnoresReferenceEmptyBase64Noise(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`{"audio":"!!!!"}`),
		[]byte(`{"audio":"==="}`),
	} {
		audioBytes, done, err := telnyxTTSAudioBytesFromMessage(payload)
		if err != nil {
			t.Fatalf("audio bytes from noise %s: %v", payload, err)
		}
		if audioBytes != nil || done {
			t.Fatalf("noise %s = audio=%v done=%v, want ignored empty chunk", payload, audioBytes, done)
		}

		audio, done, err := telnyxTTSAudioFromMessage(payload, 16000)
		if err != nil {
			t.Fatalf("audio frame from noise %s: %v", payload, err)
		}
		if audio != nil || done {
			t.Fatalf("noise %s = audio=%+v done=%v, want ignored empty frame", payload, audio, done)
		}
	}
}

func TestTelnyxTTSStreamDecodesReferenceMP3Audio(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	stream := &telnyxTTSStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio, 10),
		errCh:  make(chan error, 1),
	}
	stream.startDecoder()
	defer stream.Close()

	go func() {
		stream.pushAudioData(mp3Data)
		stream.endAudioInput()
	}()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want decoded audio", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatal("audio frame = nil, want decoded PCM frame")
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want reference Telnyx PCM sample rate 16000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want reference Telnyx mono PCM", audio.Frame.NumChannels)
	}
	if audio.Frame.SamplesPerChannel == 0 || int(audio.Frame.SamplesPerChannel)*2 != len(audio.Frame.Data) {
		t.Fatalf("frame samples = %d data bytes = %d, want complete 16-bit mono PCM frame", audio.Frame.SamplesPerChannel, len(audio.Frame.Data))
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	if bytes.HasPrefix(mp3Data, audio.Frame.Data) {
		t.Fatal("frame data still contains raw mp3 bytes")
	}
}

func TestTelnyxTTSStreamEmitsReferenceFinalMarkerAfterMP3Decode(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream := &telnyxTTSStream{
		ctx:    ctx,
		events: make(chan *tts.SynthesizedAudio, 10),
		errCh:  make(chan error, 1),
	}
	stream.startDecoder()
	defer stream.Close()

	go func() {
		stream.pushAudioData(mp3Data)
		stream.endAudioInput()
	}()

	frames := 0
	for {
		audio, err := stream.Next()
		if errors.Is(err, io.EOF) {
			t.Fatalf("stream ended after %d frames without final marker", frames)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timed out after %d frames waiting for final marker", frames)
		}
		if err != nil {
			t.Fatalf("Next error = %v, want decoded audio or final marker", err)
		}
		if audio == nil {
			t.Fatal("audio = nil, want decoded audio or final marker")
		}
		if audio.IsFinal {
			if audio.Frame != nil {
				t.Fatal("final marker included frame, want boundary-only marker")
			}
			if frames == 0 {
				t.Fatal("final marker arrived before decoded MP3 frames")
			}
			return
		}
		if audio.Frame == nil {
			t.Fatal("non-final event missing decoded frame")
		}
		frames++
	}
}

func TestTelnyxTTSStreamDecodeFailureReturnsAPIConnectionError(t *testing.T) {
	decodeErr := errors.New("decode failed")
	stream := &telnyxTTSStream{
		ctx:     context.Background(),
		events:  make(chan *tts.SynthesizedAudio, 1),
		errCh:   make(chan error, 1),
		decoder: &fakeTelnyxAudioDecoder{err: decodeErr},
	}
	go stream.decodeLoop()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("audio = %+v, want nil on decode failure", audio)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("decode error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "Telnyx TTS audio decode failed") {
		t.Fatalf("decode error = %q, want decode context", err)
	}
}

func TestTelnyxTTSStreamNoAudioCloseEndsWithoutDecoderError(t *testing.T) {
	stream := &telnyxTTSStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio),
		errCh:  make(chan error, 1),
	}

	stream.endAudioInput()

	audio, err := stream.Next()
	if audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after no-audio close = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestTelnyxTTSStreamUnexpectedCloseReturnsAPIConnectionError(t *testing.T) {
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

	stream := &telnyxTTSStream{
		conn:   conn,
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var connectionErr *llm.APIConnectionError
		if !errors.As(err, &connectionErr) {
			t.Fatalf("readLoop error = %T %v, want APIConnectionError", err, err)
		}
		if !strings.Contains(err.Error(), "Telnyx TTS WebSocket closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Telnyx close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close error")
	}
}

func assertTelnyxTextPayload(t *testing.T, message map[string]string, want string) {
	t.Helper()
	if got := message["text"]; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestTelnyxTTSStillImplementsInterface(t *testing.T) {
	var _ tts.TTS = NewTelnyxTTS("test-key", "")
}

type fakeTelnyxChunkedTTSProvider struct {
	stream tts.SynthesizeStream
}

func (f *fakeTelnyxChunkedTTSProvider) Stream(context.Context) (tts.SynthesizeStream, error) {
	return f.stream, nil
}

type fakeTelnyxEndInputTTSStream struct {
	calls   []string
	pushErr error
	endErr  error
	nextErr error
	events  []*tts.SynthesizedAudio
	closed  bool
}

func (f *fakeTelnyxEndInputTTSStream) PushText(text string) error {
	f.calls = append(f.calls, "PushText:"+text)
	return f.pushErr
}

func (f *fakeTelnyxEndInputTTSStream) Flush() error {
	f.calls = append(f.calls, "Flush")
	return nil
}

func (f *fakeTelnyxEndInputTTSStream) EndInput() error {
	f.calls = append(f.calls, "EndInput")
	return f.endErr
}

func (f *fakeTelnyxEndInputTTSStream) Close() error {
	f.calls = append(f.calls, "Close")
	f.closed = true
	return nil
}

func (f *fakeTelnyxEndInputTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if f.nextErr != nil {
		return nil, f.nextErr
	}
	if len(f.events) > 0 {
		event := f.events[0]
		f.events = f.events[1:]
		return event, nil
	}
	return nil, io.EOF
}

type fakeTelnyxAudioDecoder struct {
	err error
}

func (f *fakeTelnyxAudioDecoder) Push([]byte) {}

func (f *fakeTelnyxAudioDecoder) EndInput() {}

func (f *fakeTelnyxAudioDecoder) Next() (*model.AudioFrame, error) {
	return nil, f.err
}

func (f *fakeTelnyxAudioDecoder) Close() error {
	return nil
}
