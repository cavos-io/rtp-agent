package deepgram

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
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
	if provider.streamResponseTimeout != 10*time.Second {
		t.Fatalf("stream response timeout = %s, want 10s", provider.streamResponseTimeout)
	}
	if got := tts.Model(provider); got != "aura-2-andromeda-en" {
		t.Fatalf("model metadata = %q, want aura-2-andromeda-en", got)
	}
	if got := tts.Provider(provider); got != "Deepgram" {
		t.Fatalf("provider metadata = %q, want Deepgram", got)
	}
}

func TestDeepgramTTSPrewarmDialsAndReusesReferenceConnection(t *testing.T) {
	dials := make(chan net.Conn, 2)
	writes := make(chan string, 4)
	serverErr := make(chan error, 1)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			dials <- serverConn
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	tts.Prewarm(provider)

	var serverConn net.Conn
	select {
	case serverConn = <-dials:
	case <-time.After(time.Second):
		t.Fatal("Prewarm did not dial reference websocket connection")
	}
	go runDeepgramTTSPrewarmedWebsocketServer(serverConn, writes, serverErr)
	waitDeepgramTTSPrewarmReady(t, provider)

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	gotWrites := []string{
		receiveDeepgramTTSWrite(t, writes, "Speak on prewarmed websocket"),
		receiveDeepgramTTSWrite(t, writes, "Flush on prewarmed websocket"),
	}
	wantWrites := []string{`{"type": "Speak", "text": "hello "}`, deepgramTTSFlushMessage}
	if !reflect.DeepEqual(gotWrites, wantWrites) {
		t.Fatalf("prewarmed websocket writes = %#v, want %#v", gotWrites, wantWrites)
	}
	select {
	case extra := <-dials:
		_ = extra.Close()
		t.Fatal("Stream opened a second websocket instead of reusing prewarmed connection")
	default:
	}
	if audio, err := stream.Next(); err != nil || audio == nil || !audio.IsFinal {
		t.Fatalf("Next() = (%+v, %v), want Flushed final marker", audio, err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSProviderCloseCancelsReferencePrewarm(t *testing.T) {
	dialStarted := make(chan struct{})
	dialCanceled := make(chan error, 1)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			close(dialStarted)
			<-ctx.Done()
			dialCanceled <- ctx.Err()
			return nil, ctx.Err()
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	tts.Prewarm(provider)
	select {
	case <-dialStarted:
	case <-time.After(time.Second):
		t.Fatal("Prewarm did not start reference websocket dial")
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-dialCanceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("prewarm dial canceled with %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider Close did not cancel in-flight reference prewarm dial")
	}
}

func TestDeepgramTTSPooledCloseSendsReferenceFlushCloseBeforeAck(t *testing.T) {
	dials := make(chan net.Conn, 1)
	serverErr := make(chan error, 1)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			dials <- serverConn
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	tts.Prewarm(provider)
	serverConn := receiveDeepgramTTSDial(t, dials, "prewarm TTS websocket")
	go runDeepgramTTSPooledCloseOrderWebsocketServer(serverConn, serverErr)
	waitDeepgramTTSPrewarmReady(t, provider)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- provider.Close()
	}()

	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("provider Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider Close() did not finish after reference close ack")
	}
}

func TestDeepgramTTSExpiresReferencePooledConnection(t *testing.T) {
	dials := make(chan net.Conn, 3)
	writes := make(chan string, 4)
	expiredErr := make(chan error, 1)
	streamErr := make(chan error, 1)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			dials <- serverConn
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	tts.Prewarm(provider)
	expiredConn := receiveDeepgramTTSDial(t, dials, "expired prewarm TTS websocket")
	go runDeepgramTTSPooledCloseOrderWebsocketServer(expiredConn, expiredErr)
	waitDeepgramTTSPrewarmReady(t, provider)

	provider.mu.Lock()
	provider.prewarmConnectedAt = time.Now().Add(-deepgramTTSPoolMaxSessionDuration - time.Second)
	provider.mu.Unlock()

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("fresh"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	flushDone := make(chan error, 1)
	go func() {
		flushDone <- stream.Flush()
	}()
	freshConn := receiveDeepgramTTSDial(t, dials, "fresh TTS websocket after pool expiry")
	go runDeepgramTTSPrewarmedWebsocketServer(freshConn, writes, streamErr)
	if err := <-flushDone; err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if err := <-expiredErr; err != nil {
		t.Fatalf("expired websocket close error: %v", err)
	}
	gotWrites := []string{
		receiveDeepgramTTSWrite(t, writes, "fresh Speak"),
		receiveDeepgramTTSWrite(t, writes, "fresh Flush"),
	}
	wantWrites := []string{`{"type": "Speak", "text": "fresh "}`, deepgramTTSFlushMessage}
	if !reflect.DeepEqual(gotWrites, wantWrites) {
		t.Fatalf("fresh websocket writes = %#v, want %#v", gotWrites, wantWrites)
	}
	if audio, err := stream.Next(); err != nil || audio == nil || !audio.IsFinal {
		t.Fatalf("Next() = (%+v, %v), want final marker from fresh websocket", audio, err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close() error = %v", err)
	}
	if err := <-streamErr; err != nil {
		t.Fatalf("fresh websocket server error: %v", err)
	}
}

func TestDeepgramTTSStreamsReuseReferencePooledConnection(t *testing.T) {
	dials := make(chan net.Conn, 2)
	writes := make(chan string, 8)
	serverErr := make(chan error, 1)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			dials <- serverConn
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	first, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("first Stream() error = %v", err)
	}

	if err := first.PushText("first"); err != nil {
		t.Fatalf("first PushText() error = %v", err)
	}
	firstEnd := make(chan error, 1)
	go func() {
		firstEnd <- tts.EndSynthesizeStreamInput(first)
	}()
	firstConn := receiveDeepgramTTSDial(t, dials, "first TTS websocket")
	go runDeepgramTTSReusableWebsocketServer(firstConn, writes, serverErr)
	if err := <-firstEnd; err != nil {
		t.Fatalf("first EndInput() error = %v", err)
	}
	if audio, err := first.Next(); err != nil || audio == nil || !audio.IsFinal {
		t.Fatalf("first Next() = (%+v, %v), want final marker", audio, err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("second Stream() error = %v", err)
	}
	defer second.Close()
	if err := second.PushText("second"); err != nil {
		t.Fatalf("second PushText() error = %v", err)
	}
	secondEnd := make(chan error, 1)
	go func() {
		secondEnd <- tts.EndSynthesizeStreamInput(second)
	}()
	select {
	case extra := <-dials:
		_ = extra.Close()
		t.Fatal("second Stream opened a new websocket instead of reusing reference pool connection")
	case err := <-secondEnd:
		if err != nil {
			t.Fatalf("second EndInput() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second EndInput() did not finish on pooled websocket")
	}

	gotWrites := []string{
		receiveDeepgramTTSWrite(t, writes, "first Speak"),
		receiveDeepgramTTSWrite(t, writes, "first Flush"),
		receiveDeepgramTTSWrite(t, writes, "second Speak"),
		receiveDeepgramTTSWrite(t, writes, "second Flush"),
	}
	wantWrites := []string{
		`{"type": "Speak", "text": "first "}`,
		deepgramTTSFlushMessage,
		`{"type": "Speak", "text": "second "}`,
		deepgramTTSFlushMessage,
	}
	if !reflect.DeepEqual(gotWrites, wantWrites) {
		t.Fatalf("pooled websocket writes = %#v, want %#v", gotWrites, wantWrites)
	}
	if audio, err := second.Next(); err != nil || audio == nil || !audio.IsFinal {
		t.Fatalf("second Next() = (%+v, %v), want final marker", audio, err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close() error = %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSUpdateOptionsKeepsReferencePooledConnection(t *testing.T) {
	dials := make(chan net.Conn, 2)
	writes := make(chan string, 8)
	serverErr := make(chan error, 1)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			dials <- serverConn
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "aura-2-andromeda-en", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	first, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("first Stream() error = %v", err)
	}
	if err := first.PushText("first"); err != nil {
		t.Fatalf("first PushText() error = %v", err)
	}
	firstEnd := make(chan error, 1)
	go func() {
		firstEnd <- tts.EndSynthesizeStreamInput(first)
	}()
	firstConn := receiveDeepgramTTSDial(t, dials, "first TTS websocket")
	go runDeepgramTTSReusableWebsocketServer(firstConn, writes, serverErr)
	if err := <-firstEnd; err != nil {
		t.Fatalf("first EndInput() error = %v", err)
	}
	if audio, err := first.Next(); err != nil || audio == nil || !audio.IsFinal {
		t.Fatalf("first Next() = (%+v, %v), want final marker", audio, err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	provider.UpdateOptions("aura-2-asteria-en")
	second, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("second Stream() error = %v", err)
	}
	defer second.Close()
	if err := second.PushText("second"); err != nil {
		t.Fatalf("second PushText() error = %v", err)
	}
	secondEnd := make(chan error, 1)
	go func() {
		secondEnd <- tts.EndSynthesizeStreamInput(second)
	}()
	select {
	case extra := <-dials:
		_ = extra.Close()
		t.Fatal("updated TTS stream opened a new websocket instead of reusing reference pooled connection")
	case err := <-secondEnd:
		if err != nil {
			t.Fatalf("second EndInput() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second EndInput() did not finish on pooled websocket after update_options")
	}

	gotWrites := []string{
		receiveDeepgramTTSWrite(t, writes, "first Speak"),
		receiveDeepgramTTSWrite(t, writes, "first Flush"),
		receiveDeepgramTTSWrite(t, writes, "second Speak"),
		receiveDeepgramTTSWrite(t, writes, "second Flush"),
	}
	wantWrites := []string{
		`{"type": "Speak", "text": "first "}`,
		deepgramTTSFlushMessage,
		`{"type": "Speak", "text": "second "}`,
		deepgramTTSFlushMessage,
	}
	if !reflect.DeepEqual(gotWrites, wantWrites) {
		t.Fatalf("pooled websocket writes after update_options = %#v, want %#v", gotWrites, wantWrites)
	}
	if audio, err := second.Next(); err != nil || audio == nil || !audio.IsFinal {
		t.Fatalf("second Next() = (%+v, %v), want final marker", audio, err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close() error = %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSConstructorOptionsMatchReference(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "env-key")

	provider := NewDeepgramTTS("", "aura-custom",
		WithDeepgramTTSAudioFormat("mulaw", 8000),
	)
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}
	if provider.model != "aura-custom" {
		t.Fatalf("model = %q, want aura-custom", provider.model)
	}
	if provider.encoding != "mulaw" || provider.sampleRate != 8000 {
		t.Fatalf("audio format = %s/%d, want mulaw/8000", provider.encoding, provider.sampleRate)
	}

	requestURL, _ := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	query := parsed.Query()
	assertDeepgramTTSQuery(t, query, "model", "aura-custom")
	assertDeepgramTTSQuery(t, query, "encoding", "mulaw")
	assertDeepgramTTSQuery(t, query, "sample_rate", "8000")

	provider = NewDeepgramTTS("explicit-key", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestDeepgramTTSAudioFormatNormalizesReferenceEncodingAliases(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "generic pcm_s16le", in: "pcm_s16le", want: "linear16"},
		{name: "generic linear pcm", in: "linear_pcm", want: "linear16"},
		{name: "generic pcm linear", in: "pcm_linear", want: "linear16"},
		{name: "generic pcm mulaw", in: "pcm_mulaw", want: "mulaw"},
		{name: "generic pcm alaw", in: "pcm_alaw", want: "alaw"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSAudioFormat(tt.in, 8000))
			if provider.encoding != tt.want {
				t.Fatalf("encoding = %q, want Deepgram reference encoding %q", provider.encoding, tt.want)
			}

			query, err := url.Parse(buildDeepgramTTSStreamURL(provider))
			if err != nil {
				t.Fatalf("parse stream URL: %v", err)
			}
			assertDeepgramTTSQuery(t, query.Query(), "encoding", tt.want)
		})
	}
}

func TestDeepgramTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "")
	provider := NewDeepgramTTS("", "", WithDeepgramTTSBaseURL("://bad-url"))

	_, synthErr := provider.Synthesize(context.Background(), "hello")
	if synthErr == nil || !strings.Contains(synthErr.Error(), "DEEPGRAM_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", synthErr)
	}

	_, streamErr := provider.Stream(context.Background())
	if streamErr == nil || !strings.Contains(streamErr.Error(), "DEEPGRAM_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", streamErr)
	}
}

func TestDeepgramTTSProviderCloseClosesActiveStreams(t *testing.T) {
	sawClose := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSFlushOnCloseWebsocketServer(serverConn, sawClose, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if err := tts.Close(provider); err != nil {
		t.Fatalf("tts.Close(provider) error = %v", err)
	}

	select {
	case <-sawClose:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider close to close active stream")
	}
	if err := stream.PushText("later"); err != nil {
		t.Fatalf("PushText after provider close error = %v, want nil like reference closed input", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/wav"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
			Request:    r,
		}, nil
	})}
	defer func() { http.DefaultClient = oldClient }()

	provider := NewDeepgramTTS("test-key", "")
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

func TestDeepgramTTSStreamAfterCloseIsRejected(t *testing.T) {
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

	provider := NewDeepgramTTS("test-key", "")
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

func TestDeepgramTTSSynthesizeDecodesReferenceTelephonyAudio(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{0x00, 0xff})),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSAudioFormat("mulaw", 8000),
		WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("Next() audio = %#v, want decoded PCM frame", audio)
	}
	if got, want := audio.Frame.Data, []byte{0x84, 0x82, 0x00, 0x00}; !bytes.Equal(got, want) {
		t.Fatalf("decoded audio = %v, want %v", got, want)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("samples per channel = %d, want one PCM16 sample per mu-law byte", audio.Frame.SamplesPerChannel)
	}
}

func TestDeepgramTTSSynthesizeDefersReferenceRequestUntilNext(t *testing.T) {
	httpCalls := 0
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	if httpCalls != 0 {
		t.Fatalf("HTTP calls after Synthesize = %d, want 0 until first Next like reference ChunkedStream", httpCalls)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("Next() audio = %#v, want provider audio after lazy request", audio)
	}
	if httpCalls != 1 {
		t.Fatalf("HTTP calls after first Next = %d, want 1", httpCalls)
	}
}

func TestDeepgramTTSLazySynthesizeCloseBeforeNextSkipsRequest(t *testing.T) {
	httpCalls := 0
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after Close = (%#v, %v), want nil, io.EOF", audio, err)
	}
	if httpCalls != 0 {
		t.Fatalf("HTTP calls after close-before-next = %d, want 0", httpCalls)
	}
}

func TestDeepgramTTSSynthesizeRequestUsesConfiguredBaseURL(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"),
	)

	requestURL, _ := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "deepgram.example" || parsed.Path != "/v1/speak" {
		t.Fatalf("url = %q, want configured HTTP base URL", requestURL)
	}
}

func TestDeepgramTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"err_msg":"rate limited"}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))

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
	if statusErr.Body != `{"err_msg":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestDeepgramTTSSynthesizeAcceptsReferenceSuccessStatusClass(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   []byte
	}{
		{name: "partial-content", status: http.StatusPartialContent, body: []byte{0x01, 0x02}},
		{name: "no-content", status: http.StatusNoContent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			oldClient := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tt.status,
					Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
					Body:       io.NopCloser(bytes.NewReader(tt.body)),
					Request:    r,
				}, nil
			})}
			t.Cleanup(func() { http.DefaultClient = oldClient })

			provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))
			stream, err := provider.Synthesize(context.Background(), "hello")
			if err != nil {
				t.Fatalf("Synthesize() error = %v", err)
			}
			defer stream.Close()

			if len(tt.body) > 0 {
				audio, err := stream.Next()
				if err != nil {
					t.Fatalf("audio Next() error = %v, want successful 2xx audio", err)
				}
				if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, tt.body) {
					t.Fatalf("audio Next() = %+v, want body bytes %v", audio, tt.body)
				}
			}

			final, err := stream.Next()
			if err != nil {
				t.Fatalf("final Next() error = %v, want final marker for 2xx response", err)
			}
			if final == nil || !final.IsFinal || final.Frame != nil {
				t.Fatalf("final Next() = %+v, want boundary-only final marker", final)
			}
		})
	}
}

func TestDeepgramTTSSynthesizeClientClosedStatusReturnsEOF(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 499,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"err_msg":"client closed"}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next = (%#v, %v), want nil, io.EOF for reference client-closed status", audio, err)
	}
}

func TestDeepgramTTSSynthesizeClientClosedStatusSkipsBodyRead(t *testing.T) {
	oldClient := http.DefaultClient
	body := &deepgramTTSReadTrackingCloser{}
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 499,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       body,
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF for reference client-closed status", err)
	}
	if body.reads != 0 {
		t.Fatalf("body reads = %d, want 0 for client-closed status cleanup", body.reads)
	}
	if !body.closed {
		t.Fatal("body was not closed")
	}
}

func TestDeepgramTTSSynthesizeReturnsAPITimeoutError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestDeepgramTTSSynthesizeAppliesReferenceRequestTimeout(t *testing.T) {
	var hasDeadline bool
	var remaining time.Duration
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		hasDeadline = ok
		if ok {
			remaining = time.Until(deadline)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}

	if !hasDeadline {
		t.Fatal("request context has no deadline, want Deepgram reference 30s request timeout")
	}
	if remaining <= 0 || remaining > 30*time.Second {
		t.Fatalf("request context deadline remaining = %v, want bounded by Deepgram reference 30s timeout", remaining)
	}
}

func TestDeepgramTTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial refused")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestDeepgramTTSSynthesizeCallerCancelReturnsContextCanceled(t *testing.T) {
	oldClient := http.DefaultClient
	requests := make(chan *http.Request, 1)
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests <- r
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := provider.Synthesize(ctx, "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()
	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
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
			t.Fatalf("Next canceled error = %T %v, want context.Canceled", err, err)
		}
		var connectionErr *llm.APIConnectionError
		if errors.As(err, &connectionErr) {
			t.Fatalf("Next canceled error = %T, want raw context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next remained blocked after caller cancellation")
	}
}

func TestDeepgramTTSChunkedStreamReturnsAPITimeoutErrorOnReadFailure(t *testing.T) {
	stream := &deepgramTTSChunkedStream{
		resp:       &http.Response{Body: deepgramTTSReadCloser{err: context.DeadlineExceeded}},
		sampleRate: 24000,
	}

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestDeepgramTTSChunkedStreamReturnsAPIConnectionErrorOnReadFailure(t *testing.T) {
	stream := &deepgramTTSChunkedStream{
		resp:       &http.Response{Body: deepgramTTSReadCloser{err: errors.New("read failed")}},
		sampleRate: 24000,
	}

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestDeepgramTTSChunkedStreamReadCancelReturnsContextCanceled(t *testing.T) {
	stream := &deepgramTTSChunkedStream{
		resp:       &http.Response{Body: deepgramTTSReadCloser{err: context.Canceled}},
		sampleRate: 24000,
	}

	_, err := stream.Next()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next canceled error = %T %v, want context.Canceled", err, err)
	}
	var connectionErr *llm.APIConnectionError
	if errors.As(err, &connectionErr) {
		t.Fatalf("Next canceled error = %T, want raw context cancellation", err)
	}
}

func TestDeepgramTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &deepgramTTSCountingReadCloser{}
	stream := &deepgramTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if body.closeCalls != 1 {
		t.Fatalf("body close calls = %d, want 1", body.closeCalls)
	}
}

func TestDeepgramTTSChunkedStreamCloseDuringReadReturnsEOF(t *testing.T) {
	body := newDeepgramTTSBlockingReadCloser()
	stream := &deepgramTTSChunkedStream{
		resp:       &http.Response{Body: body},
		sampleRate: 24000,
	}

	nextDone := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		nextDone <- err
	}()

	select {
	case <-body.readStarted:
	case <-time.After(time.Second):
		t.Fatal("Next did not start body read")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close() blocked behind in-flight body read")
	}
	select {
	case err := <-nextDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after Close error = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next remained blocked after Close")
	}
}

func TestDeepgramTTSChunkedStreamCloseCancelsInFlightRequest(t *testing.T) {
	entered := make(chan struct{})
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}

	nextDone := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		nextDone <- err
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Next did not start Deepgram TTS request")
	}
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close() blocked behind in-flight request")
	}
	select {
	case err := <-nextDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after Close error = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next did not return after Close canceled in-flight request")
	}
}

func TestDeepgramTTSChunkedStreamKeepsFinalReadBytes(t *testing.T) {
	stream := &deepgramTTSChunkedStream{
		resp:       &http.Response{Body: &deepgramTTSFinalReadCloser{data: []byte{0x01, 0x02, 0x03, 0x04}}},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want final audio bytes", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("Next() = %+v, want audio frame", audio)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("audio bytes = %v, want final read bytes", got)
	}
	if got := audio.Frame.SamplesPerChannel; got != 2 {
		t.Fatalf("samples per channel = %d, want 2", got)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next() error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final Next() = %+v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next() error = %v, want EOF", err)
	}
}

func TestDeepgramTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &deepgramTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
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

func TestDeepgramTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &deepgramTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader(nil))},
		sampleRate: 24000,
	}
	defer stream.Close()

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error before final marker: %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("audio = %#v, want final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestDeepgramTTSChunkedStreamAnnotatesReferenceRequestID(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: deepgramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02, 0x03, 0x04})),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"))
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("audio Next() error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %+v, want frame", audio)
	}
	if audio.RequestID == "" {
		t.Fatal("audio RequestID is empty, want reference request id")
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next() error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final = %+v, want final marker", final)
	}
	if final.RequestID != audio.RequestID {
		t.Fatalf("final RequestID = %q, want %q", final.RequestID, audio.RequestID)
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

func TestDeepgramTTSStreamURLUsesConfiguredBaseURL(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"),
	)

	streamURL := buildDeepgramTTSStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "deepgram.example" || parsed.Path != "/v1/speak" {
		t.Fatalf("url = %q, want configured websocket base URL", streamURL)
	}
}

func TestDeepgramTTSStreamDefersReferenceWebsocketDialUntilInput(t *testing.T) {
	dialCalls := 0
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls++
			return nil, errors.New("unexpected dial before input")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v, want no provider dial before input", err)
	}
	if dialCalls != 0 {
		t.Fatalf("dial calls after Stream() = %d, want 0", dialCalls)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() before input error = %v", err)
	}
	if dialCalls != 0 {
		t.Fatalf("dial calls after pre-input Close() = %d, want 0", dialCalls)
	}
}

func TestDeepgramTTSStreamReturnsAPITimeoutErrorOnDialFailure(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, context.DeadlineExceeded
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	err = stream.Flush()
	if err == nil {
		t.Fatal("Flush error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Flush error = %T %v, want APITimeoutError", err, err)
	}
	if len(provider.streams) != 0 {
		t.Fatalf("provider streams after failed dial = %d, want 0", len(provider.streams))
	}
}

func TestDeepgramTTSStreamReturnsAPIConnectionErrorOnDialFailure(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial refused")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	err = stream.Flush()
	if err == nil {
		t.Fatal("Flush error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Flush error = %T %v, want APIConnectionError", err, err)
	}
	if len(provider.streams) != 0 {
		t.Fatalf("provider streams after failed dial = %d, want 0", len(provider.streams))
	}
}

func TestDeepgramTTSStreamReturnsAPIStatusErrorOnHandshakeStatus(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSHandshakeStatusServer(serverConn, http.StatusTooManyRequests, `{"err_msg":"rate limited"}`, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	err = stream.Flush()
	if err == nil {
		t.Fatal("Flush error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Flush error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, http.StatusTooManyRequests)
	}
	if statusErr.Body == nil || !strings.Contains(fmt.Sprint(statusErr.Body), "rate limited") {
		t.Fatalf("status body = %v, want provider body", statusErr.Body)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
	if len(provider.streams) != 0 {
		t.Fatalf("provider streams after failed handshake = %d, want 0", len(provider.streams))
	}
}

func TestDeepgramTTSStreamTimesOutSilentProviderAfterInput(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSSilentAfterFlushWebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"),
		WithDeepgramTTSStreamResponseTimeout(20*time.Millisecond),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello."); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
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
	case result := <-done:
		if result.audio != nil {
			t.Fatalf("Next audio = %#v, want nil", result.audio)
		}
		var timeoutErr *llm.APITimeoutError
		if !errors.As(result.err, &timeoutErr) {
			t.Fatalf("Next error = %T %v, want APITimeoutError", result.err, result.err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next did not return after response timeout")
	}

	closeStart := time.Now()
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if elapsed := time.Since(closeStart); elapsed > 100*time.Millisecond {
		t.Fatalf("Close after timeout took %s, want no close-ack wait", elapsed)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close error = %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestDeepgramTTSStreamCallerCancelReturnsContextCanceled(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := provider.Stream(ctx)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- stream.Flush()
	}()
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Flush canceled error = %T %v, want context.Canceled", err, err)
		}
		var connectionErr *llm.APIConnectionError
		if errors.As(err, &connectionErr) {
			t.Fatalf("Flush canceled error = %T, want raw context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Flush remained blocked after caller cancellation")
	}
}

func TestDeepgramTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "")

	provider.UpdateOptions("aura-2-asteria-en")

	requestURL, _ := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsedRequest, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse synthesize url: %v", err)
	}
	assertDeepgramTTSQuery(t, parsedRequest.Query(), "model", "aura-2-asteria-en")

	streamURL := buildDeepgramTTSStreamURL(provider)
	parsedStream, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	assertDeepgramTTSQuery(t, parsedStream.Query(), "model", "aura-2-asteria-en")
	if got := provider.Model(); got != "aura-2-asteria-en" {
		t.Fatalf("Model() = %q, want aura-2-asteria-en", got)
	}
}

func TestDeepgramTTSStreamCloseSendsReferenceFlushAndClose(t *testing.T) {
	var writes []string
	closed := false
	stream := &deepgramTTSStream{
		writeText: func(payload string) error {
			writes = append(writes, payload)
			return nil
		},
		closeConn: func() error {
			closed = true
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	want := []string{deepgramTTSFlushMessage, deepgramTTSCloseMessage}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes = %#v, want reference Flush then Close %#v", writes, want)
	}
	if !closed {
		t.Fatal("connection not closed")
	}
	if err := stream.PushText("later"); err != nil {
		t.Fatalf("PushText after Close error = %v, want nil like reference closed input", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after Close error = %v, want nil like reference closed input", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput after Close error = %v, want nil like reference closed input", err)
	}
}

func TestDeepgramTTSControlMessagesMatchReferenceJSONDumps(t *testing.T) {
	if deepgramTTSFlushMessage != `{"type": "Flush"}` {
		t.Fatalf("Flush control message = %q, want Python json.dumps payload", deepgramTTSFlushMessage)
	}
	if deepgramTTSCloseMessage != `{"type": "Close"}` {
		t.Fatalf("Close control message = %q, want Python json.dumps payload", deepgramTTSCloseMessage)
	}
}

func TestDeepgramTTSStreamSendsReferenceJSONDumpsTextFrames(t *testing.T) {
	var writes []string
	stream := &deepgramTTSStream{
		writeText: func(payload string) error {
			writes = append(writes, payload)
			return nil
		},
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	want := []string{`{"type": "Speak", "text": "hello "}`, deepgramTTSFlushMessage}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("text frames = %#v, want reference json.dumps frames %#v", writes, want)
	}
}

func TestDeepgramTTSStreamSpeakTextUsesReferenceJSONEscaping(t *testing.T) {
	var writes []string
	stream := &deepgramTTSStream{
		writeText: func(payload string) error {
			writes = append(writes, payload)
			return nil
		},
	}

	if err := stream.PushText("café"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	want := `{"type": "Speak", "text": "caf\u00e9 "}`
	if len(writes) == 0 || writes[0] != want {
		t.Fatalf("Speak text frame = %#v, want Python json.dumps escaping %q", writes, want)
	}
}

func TestDeepgramTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &deepgramTTSStream{
		audio: make(chan *tts.SynthesizedAudio, 1),
		errCh: make(chan error, 1),
		writeJSON: func(any) error {
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	stream.audio <- &tts.SynthesizedAudio{}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestDeepgramTTSFinalCloseDrainsQueuedAudio(t *testing.T) {
	wantAudio := &tts.SynthesizedAudio{RequestID: "req-final"}
	final := &tts.SynthesizedAudio{IsFinal: true, RequestID: "req-final"}
	stream := &deepgramTTSStream{
		audio:      make(chan *tts.SynthesizedAudio, 2),
		errCh:      make(chan error, 1),
		inputEnded: true,
		closeConn: func() error {
			return nil
		},
	}
	stream.audio <- wantAudio
	stream.audio <- final
	stream.closeAfterFinal()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want queued audio before final marker", err)
	}
	if audio != wantAudio {
		t.Fatalf("Next() audio = %#v, want queued audio %#v", audio, wantAudio)
	}
	audio, err = stream.Next()
	if err != nil {
		t.Fatalf("Next() final error = %v", err)
	}
	if audio != final {
		t.Fatalf("Next() final = %#v, want %#v", audio, final)
	}
}

func TestDeepgramTTSStreamReleasesReferencePoolBeforeFinalMarker(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "")
	stream := &deepgramTTSStream{
		provider:    provider,
		audio:       make(chan *tts.SynthesizedAudio),
		errCh:       make(chan error, 1),
		inputEnded:  true,
		inputClosed: true,
	}
	if !provider.registerStream(stream) {
		t.Fatal("registerStream returned false")
	}

	done := make(chan error, 1)
	go func() {
		done <- stream.handleTextMessage([]byte(`{"type":"Flushed"}`))
	}()

	deadline := time.After(100 * time.Millisecond)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	releasedBeforeFinal := false
	for !releasedBeforeFinal {
		stream.mu.Lock()
		releasedBeforeFinal = stream.closed && stream.drainClosed
		stream.mu.Unlock()
		if releasedBeforeFinal {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline:
			select {
			case audio := <-stream.audio:
				if audio == nil || !audio.IsFinal {
					t.Fatalf("released late and audio = %#v, want final marker", audio)
				}
			case <-time.After(time.Second):
				t.Fatal("handleTextMessage neither released pool nor exposed final marker")
			}
			if err := <-done; err != nil {
				t.Fatalf("handleTextMessage error = %v", err)
			}
			t.Fatal("final marker became readable before stream released the reference pooled connection")
		}
	}

	select {
	case audio := <-stream.audio:
		if audio == nil || !audio.IsFinal {
			t.Fatalf("audio = %#v, want final marker", audio)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for final marker")
	}
	if err := <-done; err != nil {
		t.Fatalf("handleTextMessage error = %v", err)
	}
}

func TestDeepgramTTSManualCloseAfterEndInputDropsQueuedAudio(t *testing.T) {
	stream := &deepgramTTSStream{
		audio:      make(chan *tts.SynthesizedAudio, 2),
		errCh:      make(chan error, 1),
		inputEnded: true,
		writeJSON: func(any) error {
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}
	stream.audio <- &tts.SynthesizedAudio{RequestID: "req-cancel"}
	stream.audio <- &tts.SynthesizedAudio{IsFinal: true, RequestID: "req-cancel"}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stream.closeAfterFinal()
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() after manual Close error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal {
		t.Fatalf("Next() after manual Close = %#v, want final marker without queued audio", audio)
	}
	if next, err := stream.Next(); next != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("second Next() after manual Close = (%#v, %v), want nil EOF", next, err)
	}
}

func TestDeepgramTTSNextReturnsQueuedAudioBeforeStreamError(t *testing.T) {
	providerErr := errors.New("provider failed after audio")
	for i := range 200 {
		want := &tts.SynthesizedAudio{RequestID: "req-audio"}
		stream := &deepgramTTSStream{
			audio: make(chan *tts.SynthesizedAudio, 1),
			errCh: make(chan error, 1),
		}
		stream.audio <- want
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

func TestDeepgramTTSSynthesizeReturnsPartialAudioBeforeReadError(t *testing.T) {
	stream := &deepgramTTSChunkedStream{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Body: &deepgramTTSErrorAfterDataReadCloser{
				data: []byte{0x01, 0x00, 0x02, 0x00},
				err:  io.ErrUnexpectedEOF,
			},
		},
		sampleRate: 24000,
		encoding:   "linear16",
		requestID:  "req-audio",
		started:    true,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v, want partial audio before read error", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("first Next() audio = %#v, want partial audio frame", audio)
	}
	if got, want := audio.Frame.Data, []byte{0x01, 0x00, 0x02, 0x00}; !bytes.Equal(got, want) {
		t.Fatalf("audio data = %v, want %v", got, want)
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("second Next() error = nil, want read error")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("second Next() error = %T %v, want APIConnectionError", err, err)
	}
}

func TestDeepgramTTSStreamCloseUnblocksBackpressuredAudioSend(t *testing.T) {
	audioWritten := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSBackpressuredAudioWebsocketServer(serverConn, audioWritten, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	streamIface, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	stream := streamIface.(*deepgramTTSStream)
	for i := 0; i < cap(stream.audio); i++ {
		stream.audio <- &tts.SynthesizedAudio{RequestID: "queued"}
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	select {
	case <-audioWritten:
	case <-time.After(time.Second):
		t.Fatal("server did not write provider audio")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close() blocked behind backpressured TTS audio delivery")
	}

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) && !strings.Contains(err.Error(), "closed pipe") {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}
}

func TestDeepgramTTSStreamCloseIgnoresReferenceFlushWriteFailure(t *testing.T) {
	writeErr := errors.New("flush write failed")
	closed := false
	stream := &deepgramTTSStream{
		writeJSON: func(any) error {
			return writeErr
		},
		closeConn: func() error {
			closed = true
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil like reference close callback", err)
	}
	if !closed {
		t.Fatal("connection not closed after close write failure")
	}
}

func TestDeepgramTTSStreamCloseWaitsForReferenceFlushedAck(t *testing.T) {
	sawClose := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSFlushOnCloseWebsocketServer(serverConn, sawClose, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case <-sawClose:
	case <-time.After(time.Second):
		t.Fatal("server did not receive Close message")
	}

	nextDone := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		nextDone <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()

	select {
	case result := <-nextDone:
		if result.err != nil {
			t.Fatalf("Next() error = %v, want Flushed final marker", result.err)
		}
		if result.audio == nil || !result.audio.IsFinal {
			t.Fatalf("Next() = %+v, want final marker from Flushed ack", result.audio)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Next() did not receive Flushed final marker promptly")
	}

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close() did not return promptly after Flushed ack")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSStreamCloseUsesReferenceAnyMessageAck(t *testing.T) {
	sawClose := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSBinaryAckOnCloseWebsocketServer(serverConn, sawClose, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case <-sawClose:
	case <-time.After(time.Second):
		t.Fatal("server did not receive Close message")
	}

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close() waited for Flushed instead of reference any-message ack")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSStreamCloseIgnoresStaleReferenceAck(t *testing.T) {
	closeAck := make(chan struct{}, 1)
	closeAck <- struct{}{}
	flushWritten := make(chan struct{}, 1)
	stream := &deepgramTTSStream{
		closeAck: closeAck,
		writeText: func(payload string) error {
			if payload == deepgramTTSFlushMessage {
				flushWritten <- struct{}{}
			}
			return nil
		},
		closeConn: func() error {
			return nil
		},
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case <-flushWritten:
	case <-time.After(time.Second):
		t.Fatal("Flush was not written")
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before post-close provider ack: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	stream.signalCloseAck()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close() did not return after post-close provider ack")
	}
}

func TestDeepgramTTSStreamWaitsForReferenceInputBeforeRead(t *testing.T) {
	stream := &deepgramTTSStream{inputSent: make(chan struct{})}
	waited := make(chan struct{})
	go func() {
		stream.waitForInputSent()
		close(waited)
	}()

	select {
	case <-waited:
		t.Fatal("read gate opened before reference Speak or Flush input")
	case <-time.After(20 * time.Millisecond):
	}

	stream.markInputSent()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("read gate did not open after reference input was sent")
	}
}

func TestDeepgramTTSStreamSpeakTextKeepsReferenceTrailingSeparator(t *testing.T) {
	var speakText string
	stream := &deepgramTTSStream{
		writeJSON: func(v any) error {
			msg, ok := v.(map[string]interface{})
			if !ok {
				t.Fatalf("writeJSON payload = %#v, want map", v)
			}
			if msg["type"] != "Speak" {
				return nil
			}
			speakText, _ = msg["text"].(string)
			return nil
		},
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if speakText != "hello " {
		t.Fatalf("Speak text = %q, want reference trailing separator", speakText)
	}
}

func TestDeepgramTTSStreamTokenizesReferenceSpeakMessages(t *testing.T) {
	var writes []string
	stream := &deepgramTTSStream{
		writeJSON: func(v any) error {
			msg, ok := v.(map[string]interface{})
			if !ok {
				t.Fatalf("writeJSON payload = %#v, want map", v)
			}
			msgType, _ := msg["type"].(string)
			if msgType == "Speak" {
				text, _ := msg["text"].(string)
				writes = append(writes, msgType+":"+text)
				return nil
			}
			writes = append(writes, msgType)
			return nil
		},
	}

	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if !reflect.DeepEqual(writes, []string{"Speak:hello "}) {
		t.Fatalf("writes after PushText = %#v, want completed first word only", writes)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	want := []string{"Speak:hello ", "Speak:world ", "Flush"}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes after Flush = %#v, want %#v", writes, want)
	}

	writes = nil
	stream = &deepgramTTSStream{
		writeJSON: func(v any) error {
			msg, ok := v.(map[string]interface{})
			if !ok {
				t.Fatalf("writeJSON payload = %#v, want map", v)
			}
			msgType, _ := msg["type"].(string)
			if msgType == "Speak" {
				text, _ := msg["text"].(string)
				writes = append(writes, msgType+":"+text)
				return nil
			}
			writes = append(writes, msgType)
			return nil
		},
	}
	if err := stream.PushText("hello wor"); err != nil {
		t.Fatalf("PushText(partial) error = %v", err)
	}
	if err := stream.PushText("ld again"); err != nil {
		t.Fatalf("PushText(rest) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(partial) error = %v", err)
	}
	want = []string{"Speak:hello ", "Speak:world ", "Speak:again ", "Flush"}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes after split PushText = %#v, want %#v", writes, want)
	}
}

func TestDeepgramTTSStreamEndInputFlushesReferenceSegment(t *testing.T) {
	var writes []string
	closeCalls := 0
	stream := &deepgramTTSStream{
		audio:     make(chan *tts.SynthesizedAudio, 1),
		errCh:     make(chan error, 1),
		flushed:   make(chan struct{}, 1),
		requestID: "req-end",
		segmentID: "seg-end",
		writeText: func(payload string) error {
			writes = append(writes, payload)
			return nil
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := tts.EndSynthesizeStreamInput(stream); err != nil {
		t.Fatalf("EndSynthesizeStreamInput() error = %v", err)
	}
	wantWrites := []string{`{"type": "Speak", "text": "hello "}`, `{"type": "Speak", "text": "world "}`, deepgramTTSFlushMessage}
	if !reflect.DeepEqual(writes, wantWrites) {
		t.Fatalf("writes = %#v, want %#v", writes, wantWrites)
	}
	if err := stream.PushText("again"); err != nil {
		t.Fatalf("PushText after EndInput error = %v, want nil like reference closed input", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after EndInput error = %v, want nil like reference closed input", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("second EndInput error = %v, want nil like reference closed input", err)
	}
	if err := stream.handleTextMessage([]byte(`{"type":"Flushed"}`)); err != nil {
		t.Fatalf("handleTextMessage Flushed error = %v", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after Flushed = %d, want 1", closeCalls)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want final marker", err)
	}
	if final.RequestID != "req-end" || final.SegmentID != "seg-end" || !final.IsFinal {
		t.Fatalf("final audio = %+v, want request/segment final marker", final)
	}
	if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final = (%+v, %v), want EOF", audio, err)
	}
}

func TestDeepgramTTSStreamEndInputClosesIdleReferenceStream(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*testing.T, *deepgramTTSStream, *[]string)
	}{
		{
			name: "empty",
		},
		{
			name: "after flushed segment",
			setup: func(t *testing.T, stream *deepgramTTSStream, writes *[]string) {
				t.Helper()
				if err := stream.PushText("hello"); err != nil {
					t.Fatalf("PushText() error = %v", err)
				}
				if err := stream.Flush(); err != nil {
					t.Fatalf("Flush() error = %v", err)
				}
				if err := stream.handleTextMessage([]byte(`{"type":"Flushed"}`)); err != nil {
					t.Fatalf("handleTextMessage Flushed error = %v", err)
				}
				if final, err := stream.Next(); err != nil || final == nil || !final.IsFinal {
					t.Fatalf("Next after setup Flushed = (%+v, %v), want final marker", final, err)
				}
				*writes = nil
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var writes []string
			closeCalls := 0
			stream := &deepgramTTSStream{
				audio:     make(chan *tts.SynthesizedAudio, 1),
				errCh:     make(chan error, 1),
				flushed:   make(chan struct{}, 1),
				inputSent: make(chan struct{}),
				writeText: func(payload string) error {
					writes = append(writes, payload)
					return nil
				},
				closeConn: func() error {
					closeCalls++
					return nil
				},
			}
			if tt.setup != nil {
				tt.setup(t, stream, &writes)
			}

			if err := tts.EndSynthesizeStreamInput(stream); err != nil {
				t.Fatalf("EndSynthesizeStreamInput() error = %v", err)
			}
			if len(writes) != 0 {
				t.Fatalf("writes after idle EndInput = %#v, want none", writes)
			}
			if closeCalls != 1 {
				t.Fatalf("close calls after idle EndInput = %d, want 1", closeCalls)
			}
			select {
			case <-stream.inputSent:
			default:
				t.Fatal("idle EndInput did not release reference read gate")
			}
			if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
				t.Fatalf("Next after idle EndInput = (%+v, %v), want EOF", audio, err)
			}
		})
	}
}

func TestDeepgramTTSStreamIgnoresReferenceEmptyText(t *testing.T) {
	writes := 0
	stream := &deepgramTTSStream{
		writeJSON: func(any) error {
			writes++
			return nil
		},
	}

	if err := stream.PushText(""); err != nil {
		t.Fatalf("PushText(empty) error = %v", err)
	}
	if writes != 0 {
		t.Fatalf("writes after empty PushText = %d, want 0", writes)
	}
}

func TestDeepgramTTSStreamEmptyFlushIsReferenceNoop(t *testing.T) {
	writes := 0
	stream := &deepgramTTSStream{
		writeJSON: func(any) error {
			writes++
			return nil
		},
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(empty) error = %v", err)
	}
	if writes != 0 {
		t.Fatalf("writes after empty Flush = %d, want 0", writes)
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(text) error = %v", err)
	}
	if writes != 2 {
		t.Fatalf("writes after text Flush = %d, want Speak and Flush", writes)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(after segment) error = %v", err)
	}
	if writes != 2 {
		t.Fatalf("writes after second empty Flush = %d, want unchanged", writes)
	}

	writes = 0
	stream = &deepgramTTSStream{
		writeJSON: func(any) error {
			writes++
			return nil
		},
	}
	if err := stream.PushText("   "); err != nil {
		t.Fatalf("PushText(whitespace) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(whitespace) error = %v", err)
	}
	if writes != 1 {
		t.Fatalf("writes after whitespace Flush = %d, want one reference Flush", writes)
	}
}

func TestDeepgramTTSStreamIgnoresReferenceSecondSegment(t *testing.T) {
	var writes []string
	stream := &deepgramTTSStream{
		writeJSON: func(v any) error {
			msg, ok := v.(map[string]interface{})
			if !ok {
				t.Fatalf("writeJSON payload = %#v, want map", v)
			}
			msgType, _ := msg["type"].(string)
			if msgType == "Speak" {
				text, _ := msg["text"].(string)
				writes = append(writes, msgType+":"+text)
				return nil
			}
			writes = append(writes, msgType)
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

	want := []string{"Speak:first ", "Flush"}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes = %#v, want reference first segment only %#v", writes, want)
	}
}

func TestDeepgramTTSStreamMarksFinalAudioOnReferenceFlushed(t *testing.T) {
	stream := &deepgramTTSStream{
		audio: make(chan *tts.SynthesizedAudio, 1),
		errCh: make(chan error, 1),
	}

	if err := stream.handleTextMessage([]byte(`{"type":"Flushed"}`)); err != nil {
		t.Fatalf("handleTextMessage Flushed error = %v", err)
	}

	select {
	case audio := <-stream.audio:
		if audio == nil || !audio.IsFinal {
			t.Fatalf("Flushed audio = %+v, want final marker", audio)
		}
	default:
		t.Fatal("Flushed did not emit final audio marker")
	}
}

func TestDeepgramTTSStreamAnnotatesReferenceAudioSegment(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSAudioSegmentWebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() audio error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %+v, want frame", audio)
	}
	if audio.RequestID == "" {
		t.Fatal("audio RequestID is empty, want stable stream request id")
	}
	if audio.SegmentID == "" {
		t.Fatal("audio SegmentID is empty, want current segment id")
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() final error = %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final = %+v, want final marker", final)
	}
	if final.RequestID != audio.RequestID {
		t.Fatalf("final RequestID = %q, want %q", final.RequestID, audio.RequestID)
	}
	if final.SegmentID != audio.SegmentID {
		t.Fatalf("final SegmentID = %q, want %q", final.SegmentID, audio.SegmentID)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSStreamDecodesReferenceTelephonyAudio(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSTelephonyAudioWebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSAudioFormat("mulaw", 8000),
		WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("Next() audio = %#v, want decoded PCM frame", audio)
	}
	if got, want := audio.Frame.Data, []byte{0x84, 0x82, 0x00, 0x00}; !bytes.Equal(got, want) {
		t.Fatalf("decoded audio = %v, want %v", got, want)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("samples per channel = %d, want one PCM16 sample per mu-law byte", audio.Frame.SamplesPerChannel)
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSStreamPropagatesReferenceErrorMessage(t *testing.T) {
	stream := &deepgramTTSStream{
		audio: make(chan *tts.SynthesizedAudio, 1),
		errCh: make(chan error, 1),
	}

	err := stream.handleTextMessage([]byte(`{"type":"Error","message":"bad request"}`))
	if err == nil {
		t.Fatal("handleTextMessage Error returned nil, want error")
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want APIError", err, err)
	}
	body, ok := apiErr.Body.(map[string]interface{})
	if !ok {
		t.Fatalf("APIError body = %T %#v, want provider response map", apiErr.Body, apiErr.Body)
	}
	if body["type"] != "Error" || body["message"] != "bad request" {
		t.Fatalf("APIError body = %#v, want full provider response", apiErr.Body)
	}
}

func TestDeepgramTTSStreamMalformedTextReturnsAPIConnectionError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSMalformedTextWebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next() error = %T %v, want APIConnectionError", err, err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSStreamNullTextReturnsAPIConnectionError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSMalformedTextPayloadWebsocketServer(serverConn, serverErr, []byte(`null`))

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next() error = %T %v, want APIConnectionError", err, err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSStreamErrorDeliveryDoesNotBlockReadLoop(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSMalformedTextWebsocketServer(serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	streamIface, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	stream := streamIface.(*deepgramTTSStream)
	stream.errCh <- errors.New("already queued")
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	select {
	case _, ok := <-stream.audio:
		if ok {
			t.Fatal("audio channel delivered data, want close after malformed text error")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read loop blocked delivering error to full errCh")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	closeCalls := 0
	stream := &deepgramTTSStream{
		writeJSON: func(any) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("hello world"); !errors.Is(err, writeErr) {
		t.Fatalf("PushText error = %v, want write error", err)
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

func TestDeepgramTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	closed := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSReadFlushThenCloseServer(serverConn, closed, false, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next() error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode == 0 {
		t.Fatalf("status code = %d, want close status or -1", statusErr.StatusCode)
	}
}

func TestDeepgramTTSStreamNormalCloseBeforeFlushedReturnsAPIStatusError(t *testing.T) {
	closed := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramTTSReadFlushThenCloseServer(serverConn, closed, true, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramTTS("test-key", "", WithDeepgramTTSBaseURL("ws://deepgram.test/v1/speak"))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next() error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != websocket.CloseNormalClosure {
		t.Fatalf("status code = %d, want normal close status", statusErr.StatusCode)
	}
}

func assertDeepgramTTSQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

type deepgramTTSReadCloser struct {
	err error
}

func (r deepgramTTSReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r deepgramTTSReadCloser) Close() error {
	return nil
}

type deepgramTTSErrorAfterDataReadCloser struct {
	data []byte
	err  error
	read bool
}

func (r *deepgramTTSErrorAfterDataReadCloser) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	return copy(p, r.data), r.err
}

func (r *deepgramTTSErrorAfterDataReadCloser) Close() error {
	return nil
}

type deepgramTTSReadTrackingCloser struct {
	reads  int
	closed bool
}

func (r *deepgramTTSReadTrackingCloser) Read([]byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

func (r *deepgramTTSReadTrackingCloser) Close() error {
	r.closed = true
	return nil
}

type deepgramTTSCountingReadCloser struct {
	closeCalls int
}

func (r *deepgramTTSCountingReadCloser) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (r *deepgramTTSCountingReadCloser) Close() error {
	r.closeCalls++
	return nil
}

type deepgramTTSBlockingReadCloser struct {
	readStarted chan struct{}
	closed      chan struct{}
	closeOnce   sync.Once
}

func newDeepgramTTSBlockingReadCloser() *deepgramTTSBlockingReadCloser {
	return &deepgramTTSBlockingReadCloser{
		readStarted: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (r *deepgramTTSBlockingReadCloser) Read([]byte) (int, error) {
	select {
	case <-r.readStarted:
	default:
		close(r.readStarted)
	}
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *deepgramTTSBlockingReadCloser) Close() error {
	r.closeOnce.Do(func() {
		close(r.closed)
	})
	return nil
}

type deepgramTTSFinalReadCloser struct {
	data []byte
	read bool
}

func (r *deepgramTTSFinalReadCloser) Read(p []byte) (int, error) {
	if r.read {
		return 0, errors.New("read after final eof")
	}
	r.read = true
	return copy(p, r.data), io.EOF
}

func (r *deepgramTTSFinalReadCloser) Close() error {
	return nil
}

func runDeepgramTTSHandshakeStatusServer(conn net.Conn, statusCode int, body string, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if _, err := http.ReadRequest(reader); err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", statusCode, http.StatusText(statusCode), len(body), body); err != nil {
		errCh <- err
		return
	}
	errCh <- nil
}

func runDeepgramTTSPrewarmedWebsocketServer(conn net.Conn, writes chan<- string, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}

	for {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode != websocket.TextMessage {
			continue
		}
		writes <- string(payload)
		switch deepgramTestWebsocketMessageType(payload) {
		case "Flush":
			if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(`{"type":"Flushed"}`)); err != nil {
				errCh <- err
				return
			}
		case "Close":
			errCh <- nil
			return
		}
	}
}

func runDeepgramTTSPooledCloseOrderWebsocketServer(conn net.Conn, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}

	opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.TextMessage || string(payload) != deepgramTTSFlushMessage {
		errCh <- fmt.Errorf("first pooled close frame = opcode %d payload %q, want reference Flush", opcode, payload)
		return
	}

	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		errCh <- err
		return
	}
	opcode, payload, err = readDeepgramTestClientWebsocketFrame(reader)
	if err != nil {
		_ = conn.SetReadDeadline(time.Time{})
		_ = writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(`{"type":"Flushed"}`))
		errCh <- fmt.Errorf("pooled close waited for ack before Close frame: %w", err)
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if opcode != websocket.TextMessage || string(payload) != deepgramTTSCloseMessage {
		errCh <- fmt.Errorf("second pooled close frame = opcode %d payload %q, want reference Close", opcode, payload)
		return
	}
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(`{"type":"Flushed"}`)); err != nil {
		errCh <- err
		return
	}
	errCh <- nil
}

func runDeepgramTTSReusableWebsocketServer(conn net.Conn, writes chan<- string, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}

	flushes := 0
	for {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode != websocket.TextMessage {
			continue
		}
		msgType := deepgramTestWebsocketMessageType(payload)
		if msgType == "Close" {
			errCh <- nil
			return
		}
		writes <- string(payload)
		if msgType == "Flush" {
			flushes++
			if flushes > 2 {
				opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
				if err != nil {
					errCh <- err
					return
				}
				if opcode != websocket.TextMessage || deepgramTestWebsocketMessageType(payload) != "Close" {
					errCh <- fmt.Errorf("pooled cleanup frame = opcode %d payload %q, want Close", opcode, payload)
					return
				}
				if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(`{"type":"Flushed"}`)); err != nil {
					errCh <- err
					return
				}
				errCh <- nil
				return
			}
			if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(`{"type":"Flushed"}`)); err != nil {
				errCh <- err
				return
			}
		}
	}
}

func receiveDeepgramTTSDial(t *testing.T, dials <-chan net.Conn, label string) net.Conn {
	t.Helper()
	select {
	case conn := <-dials:
		return conn
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return nil
	}
}

func receiveDeepgramTTSWrite(t *testing.T, writes <-chan string, label string) string {
	t.Helper()
	select {
	case write := <-writes:
		return write
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return ""
	}
}

func waitDeepgramTTSPrewarmReady(t *testing.T, provider *DeepgramTTS) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		provider.mu.Lock()
		ready := provider.prewarmConn != nil
		provider.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-deadline:
			t.Fatal("Prewarm did not cache reference websocket connection")
		case <-ticker.C:
		}
	}
}

func runDeepgramTTSReadFlushThenCloseServer(conn net.Conn, closed chan<- struct{}, normalClose bool, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode == websocket.TextMessage && deepgramTestWebsocketMessageType(payload) == "Flush" {
			break
		}
	}
	if normalClose {
		payload := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done")
		if err := writeDeepgramTestWebsocketFrame(conn, websocket.CloseMessage, payload); err != nil {
			errCh <- err
			return
		}
	}
	close(closed)
	errCh <- nil
}

func runDeepgramTTSFlushOnCloseWebsocketServer(conn net.Conn, sawClose chan<- struct{}, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode != websocket.TextMessage {
			continue
		}
		if deepgramTestWebsocketMessageType(payload) == "Close" {
			close(sawClose)
			time.Sleep(50 * time.Millisecond)
			if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(`{"type":"Flushed"}`)); err != nil {
				errCh <- err
				return
			}
			errCh <- nil
			return
		}
	}
}

func runDeepgramTTSBinaryAckOnCloseWebsocketServer(conn net.Conn, sawClose chan<- struct{}, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode != websocket.TextMessage {
			continue
		}
		if deepgramTestWebsocketMessageType(payload) == "Close" {
			close(sawClose)
			if err := writeDeepgramTestWebsocketFrame(conn, websocket.BinaryMessage, []byte{0x01, 0x02}); err != nil {
				errCh <- err
				return
			}
			errCh <- nil
			return
		}
	}
}

func runDeepgramTTSAudioSegmentWebsocketServer(conn net.Conn, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	sawFlush := false
	for !sawFlush {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode == websocket.TextMessage && deepgramTestWebsocketMessageType(payload) == "Flush" {
			sawFlush = true
		}
	}
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.BinaryMessage, []byte{0x01, 0x02, 0x03, 0x04}); err != nil {
		errCh <- err
		return
	}
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(`{"type":"Flushed"}`)); err != nil {
		errCh <- err
		return
	}
	errCh <- nil
}

func runDeepgramTTSBackpressuredAudioWebsocketServer(conn net.Conn, audioWritten chan<- struct{}, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode == websocket.TextMessage && deepgramTestWebsocketMessageType(payload) == "Flush" {
			break
		}
	}
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.BinaryMessage, []byte{0x01, 0x02}); err != nil {
		errCh <- err
		return
	}
	close(audioWritten)
	for {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode == websocket.TextMessage && deepgramTestWebsocketMessageType(payload) == "Close" {
			break
		}
	}
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(`{"type":"Metadata"}`)); err != nil {
		errCh <- err
		return
	}
	errCh <- nil
}

func runDeepgramTTSTelephonyAudioWebsocketServer(conn net.Conn, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode == websocket.TextMessage && deepgramTestWebsocketMessageType(payload) == "Flush" {
			break
		}
	}
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.BinaryMessage, []byte{0x00, 0xff}); err != nil {
		errCh <- err
		return
	}
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(`{"type":"Flushed"}`)); err != nil {
		errCh <- err
		return
	}
	errCh <- nil
}

func runDeepgramTTSMalformedTextWebsocketServer(conn net.Conn, errCh chan<- error) {
	runDeepgramTTSMalformedTextPayloadWebsocketServer(conn, errCh, []byte(`{"type":`))
}

func runDeepgramTTSMalformedTextPayloadWebsocketServer(conn net.Conn, errCh chan<- error, payload []byte) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode == websocket.TextMessage && deepgramTestWebsocketMessageType(payload) == "Flush" {
			break
		}
	}
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, payload); err != nil {
		errCh <- err
		return
	}
	errCh <- nil
}

func runDeepgramTTSSilentAfterFlushWebsocketServer(conn net.Conn, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	for {
		opcode, payload, err := readDeepgramTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode == websocket.TextMessage && deepgramTestWebsocketMessageType(payload) == "Flush" {
			break
		}
	}
	for {
		if _, _, err := readDeepgramTestClientWebsocketFrame(reader); err != nil {
			errCh <- nil
			return
		}
	}
}

func deepgramTestWebsocketMessageType(payload []byte) string {
	var msg map[string]any
	if err := json.Unmarshal(payload, &msg); err != nil {
		return ""
	}
	msgType, _ := msg["type"].(string)
	return msgType
}

func readDeepgramTestClientWebsocketFrame(r *bufio.Reader) (int, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	opcode := int(header[0] & 0x0f)
	masked := header[1]&0x80 != 0
	length := int(header[1] & 0x7f)
	if length == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		length = int(ext[0])<<8 | int(ext[1])
	} else if length == 127 {
		return 0, nil, fmt.Errorf("test websocket frame too large")
	}
	mask := make([]byte, 4)
	if masked {
		if _, err := io.ReadFull(r, mask); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}
