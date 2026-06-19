package xai

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestXaiSTTDefaultsMatchReference(t *testing.T) {
	provider := NewXaiSTT("test-key")

	if provider.restURL != "https://api.x.ai/v1/stt" {
		t.Fatalf("REST URL = %q, want reference REST URL", provider.restURL)
	}
	if provider.websocketURL != "wss://api.x.ai/v1/stt" {
		t.Fatalf("websocket URL = %q, want reference websocket URL", provider.websocketURL)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if !provider.enableInterimResults {
		t.Fatal("interim results = false, want true")
	}
	if provider.enableDiarization {
		t.Fatal("diarization = true, want false")
	}
	if provider.endpointing != 100 {
		t.Fatalf("endpointing = %d, want 100", provider.endpointing)
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.Diarization {
		t.Fatal("diarization = true, want false")
	}
	if caps.AlignedTranscript != "word" {
		t.Fatalf("aligned transcript = %q, want word", caps.AlignedTranscript)
	}
	if !caps.OfflineRecognize {
		t.Fatal("offline recognize = false, want true")
	}
}

func TestNewXaiSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "env-key")

	provider := NewXaiSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewXaiSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestXaiSTTRecognizeRequestUsesReferenceMultipartFields(t *testing.T) {
	provider := NewXaiSTT("test-key")

	req, err := buildXaiSTTRecognizeRequest(context.Background(), provider, []byte{0x01, 0x02}, "fr")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.x.ai/v1/stt" {
		t.Fatalf("url = %q, want reference REST endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("accept = %q, want application/json", got)
	}
	if got := req.Header.Get("Content-Type"); !strings.HasPrefix(got, "multipart/form-data; boundary=") {
		t.Fatalf("content type = %q, want multipart form", got)
	}

	fields, files := readMultipartRequest(t, req)
	if fields["language"] != "fr" {
		t.Fatalf("language field = %q, want override", fields["language"])
	}
	if fields["format"] != "true" {
		t.Fatalf("format field = %q, want true", fields["format"])
	}
	file := files["file"]
	if file.filename != "audio.wav" {
		t.Fatalf("filename = %q, want audio.wav", file.filename)
	}
	if file.contentType != "audio/wav" {
		t.Fatalf("file content type = %q, want audio/wav", file.contentType)
	}
	if !bytes.Equal(file.data, []byte{0x01, 0x02}) {
		t.Fatalf("file data = %#v, want audio bytes", file.data)
	}
}

func TestXaiSTTRecognizeUploadsReferenceWAV(t *testing.T) {
	var uploaded []byte
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		_, files := readMultipartRequest(t, r)
		uploaded = files["file"].data
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"language":"en","segments":[{"text":"ok","start":0,"end":0.1}]}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiSTT("test-key", WithXaiSTTRestURL("https://xai.example/v1/stt"))
	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{
		{
			Data:              []byte{0x01, 0x02, 0x03, 0x04},
			SampleRate:        8000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		},
	}, "en")
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}

	if len(uploaded) < 48 {
		t.Fatalf("uploaded bytes = %d, want wav header plus pcm", len(uploaded))
	}
	if string(uploaded[0:4]) != "RIFF" || string(uploaded[8:12]) != "WAVE" || string(uploaded[36:40]) != "data" {
		t.Fatalf("uploaded prefix = %q/%q/%q, want RIFF/WAVE/data", uploaded[0:4], uploaded[8:12], uploaded[36:40])
	}
	if got := binary.LittleEndian.Uint32(uploaded[24:28]); got != 8000 {
		t.Fatalf("wav sample rate = %d, want 8000", got)
	}
	if got := binary.LittleEndian.Uint16(uploaded[22:24]); got != 1 {
		t.Fatalf("wav channels = %d, want 1", got)
	}
	if got := uploaded[len(uploaded)-4:]; !bytes.Equal(got, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("wav payload tail = %#v, want original pcm", got)
	}
}

func TestXaiSTTOptionsBuildReferenceStreamURLAndHeaders(t *testing.T) {
	provider := NewXaiSTT("test-key",
		WithXaiSTTWebsocketURL("ws://xai.example/v1/stt"),
		WithXaiSTTSampleRate(48000),
		WithXaiSTTLanguage("auto"),
		WithXaiSTTInterimResults(false),
		WithXaiSTTDiarization(true),
		WithXaiSTTEndpointing(250),
	)

	streamURL, err := url.Parse(buildXaiSTTStreamURL(provider, "hi"))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if !strings.HasPrefix(streamURL.String(), "ws://xai.example/v1/stt?") {
		t.Fatalf("stream URL = %q, want websocket endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertXaiQuery(t, query, "encoding", "pcm")
	assertXaiQuery(t, query, "sample_rate", "48000")
	assertXaiQuery(t, query, "interim_results", "false")
	assertXaiQuery(t, query, "diarize", "true")
	assertXaiQuery(t, query, "language", "hi")
	assertXaiQuery(t, query, "endpointing", "250")

	headers := buildXaiSTTHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
}

func TestXaiSTTUpdateOptionsMatchesReferenceFutureRequests(t *testing.T) {
	provider := NewXaiSTT("test-key",
		WithXaiSTTWebsocketURL("ws://xai.example/v1/stt"),
	)

	provider.UpdateOptions(
		WithXaiSTTSampleRate(48000),
		WithXaiSTTLanguage("id"),
		WithXaiSTTInterimResults(false),
		WithXaiSTTDiarization(true),
		WithXaiSTTEndpointing(250),
	)

	if got := provider.InputSampleRate(); got != 48000 {
		t.Fatalf("InputSampleRate() = %d, want 48000", got)
	}
	caps := provider.Capabilities()
	if caps.InterimResults {
		t.Fatal("interim results = true, want updated false")
	}
	if !caps.Diarization {
		t.Fatal("diarization = false, want updated true")
	}

	streamURL, err := url.Parse(buildXaiSTTStreamURL(provider, ""))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	query := streamURL.Query()
	assertXaiQuery(t, query, "sample_rate", "48000")
	assertXaiQuery(t, query, "interim_results", "false")
	assertXaiQuery(t, query, "diarize", "true")
	assertXaiQuery(t, query, "language", "id")
	assertXaiQuery(t, query, "endpointing", "250")

	req, err := buildXaiSTTRecognizeRequest(context.Background(), provider, []byte{0x01}, "")
	if err != nil {
		t.Fatalf("build recognize request: %v", err)
	}
	fields, _ := readMultipartRequest(t, req)
	if fields["language"] != "id" {
		t.Fatalf("language field = %q, want updated default", fields["language"])
	}
}

func TestXaiSTTUpdateOptionsPropagatesToActiveStreams(t *testing.T) {
	releaseServer := make(chan struct{})
	handlerDone := make(chan struct{})
	handlerErr := make(chan error, 1)
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = newXaiSTTTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		defer close(handlerDone)
		<-releaseServer
		_ = conn.Close()
	}, handlerErr)
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiSTT("test-key", WithXaiSTTWebsocketURL("ws://xai.test/v1/stt"))
	streamIface, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	t.Cleanup(func() {
		close(releaseServer)
		_ = streamIface.Close()
		select {
		case <-handlerDone:
		case err := <-handlerErr:
			t.Fatal(err)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for websocket handler")
		}
	})
	stream, ok := streamIface.(*xaiSTTStream)
	if !ok {
		t.Fatalf("stream = %T, want *xaiSTTStream", streamIface)
	}

	provider.UpdateOptions(
		WithXaiSTTInterimResults(false),
		WithXaiSTTDiarization(true),
	)

	stream.mu.Lock()
	interimResults := stream.state.interimResults
	diarization := stream.state.diarization
	stream.mu.Unlock()
	if interimResults {
		t.Fatal("active stream interim results = true, want updated false")
	}
	if !diarization {
		t.Fatal("active stream diarization = false, want updated true")
	}
}

func TestXaiSTTUpdateOptionsReconnectsActiveStreams(t *testing.T) {
	requestURLs := make(chan string, 2)
	handlerErr := make(chan error, 2)
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = newXaiSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		select {
		case requestURLs <- r.URL.String():
		case <-time.After(time.Second):
			handlerErr <- errors.New("timed out recording websocket request URL")
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}, handlerErr)
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiSTT("test-key", WithXaiSTTWebsocketURL("ws://xai.test/v1/stt"))
	streamIface, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	t.Cleanup(func() { _ = streamIface.Close() })

	var firstURL string
	select {
	case firstURL = <-requestURLs:
	case err := <-handlerErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial websocket request")
	}
	first, err := url.Parse(firstURL)
	if err != nil {
		t.Fatalf("parse initial URL: %v", err)
	}
	assertXaiQuery(t, first.Query(), "sample_rate", "16000")
	assertXaiQuery(t, first.Query(), "language", "en")
	assertXaiQuery(t, first.Query(), "endpointing", "100")

	provider.UpdateOptions(
		WithXaiSTTSampleRate(48000),
		WithXaiSTTLanguage("id"),
		WithXaiSTTInterimResults(false),
		WithXaiSTTDiarization(true),
		WithXaiSTTEndpointing(250),
	)

	var secondURL string
	select {
	case secondURL = <-requestURLs:
	case err := <-handlerErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active stream reconnect")
	}
	second, err := url.Parse(secondURL)
	if err != nil {
		t.Fatalf("parse reconnect URL: %v", err)
	}
	query := second.Query()
	assertXaiQuery(t, query, "sample_rate", "48000")
	assertXaiQuery(t, query, "language", "id")
	assertXaiQuery(t, query, "interim_results", "false")
	assertXaiQuery(t, query, "diarize", "true")
	assertXaiQuery(t, query, "endpointing", "250")
}

func TestXaiSTTUpdateOptionsResetsActiveAudioChunker(t *testing.T) {
	var writes [][]byte
	stream := &xaiSTTStream{
		sampleRate: 16000,
		state:      &xaiSTTStreamState{interimResults: true},
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 1600),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	}); err != nil {
		t.Fatalf("PushFrame(16k) error = %v", err)
	}
	if len(writes) != 1 || len(writes[0]) != 1600 {
		t.Fatalf("initial writes = %v, want one 1600-byte chunk", chunkLengths(writes))
	}

	stream.updateOptions(48000, "en", true, false, 100)
	writes = nil
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 4800),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 2400,
	}); err != nil {
		t.Fatalf("PushFrame(48k) error = %v", err)
	}
	if len(writes) != 1 || len(writes[0]) != 4800 {
		t.Fatalf("post-update writes = %v, want one 4800-byte chunk", chunkLengths(writes))
	}
}

func TestXaiSTTStreamWaitsForTranscriptCreatedBeforeSendingAudio(t *testing.T) {
	binaryWrites := make(chan []byte, 1)
	readyToSend := make(chan struct{})
	releaseServer := make(chan struct{})
	handlerErr := make(chan error, 2)
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = newXaiSTTTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		go func() {
			for {
				msgType, payload, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if msgType == websocket.BinaryMessage {
					binaryWrites <- append([]byte(nil), payload...)
				}
			}
		}()
		<-readyToSend
		if err := conn.WriteJSON(map[string]any{"type": "transcript.created"}); err != nil {
			handlerErr <- err
		}
		<-releaseServer
	}, handlerErr)
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiSTT("test-key", WithXaiSTTWebsocketURL("ws://xai.test/v1/stt"))
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	frameData := make([]byte, 1600)
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              frameData,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	select {
	case payload := <-binaryWrites:
		t.Fatalf("audio sent before transcript.created: %d bytes", len(payload))
	case err := <-handlerErr:
		t.Fatal(err)
	case <-time.After(50 * time.Millisecond):
	}

	close(readyToSend)
	select {
	case payload := <-binaryWrites:
		close(releaseServer)
		if len(payload) != len(frameData) {
			t.Fatalf("audio payload length = %d, want %d", len(payload), len(frameData))
		}
	case err := <-handlerErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for buffered audio after transcript.created")
	}
}

func TestXaiSTTStreamCloseFlushesBufferedAudioBeforeDone(t *testing.T) {
	messages := make(chan string, 3)
	handlerErr := make(chan error, 2)
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = newXaiSTTTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		if err := conn.WriteJSON(map[string]any{"type": "transcript.created"}); err != nil {
			handlerErr <- err
			return
		}
		for {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			switch msgType {
			case websocket.BinaryMessage:
				messages <- "binary:" + strconv.Itoa(len(payload))
			case websocket.TextMessage:
				messages <- string(payload)
			}
		}
	}, handlerErr)
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiSTT("test-key", WithXaiSTTWebsocketURL("ws://xai.test/v1/stt"))
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 1600),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	}); err != nil {
		t.Fatalf("PushFrame(full chunk) error = %v", err)
	}
	assertXaiSTTMessage(t, messages, handlerErr, "binary:1600")

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 400),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 200,
	}); err != nil {
		t.Fatalf("PushFrame(partial chunk) error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertXaiSTTMessage(t, messages, handlerErr, "binary:400")
	assertXaiSTTMessage(t, messages, handlerErr, `{"type":"audio.done"}`)
}

func TestXaiSTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	provider := NewXaiSTT("",
		WithXaiSTTRestURL("://bad-url"),
		WithXaiSTTWebsocketURL("://bad-url"),
	)

	_, err := provider.Recognize(context.Background(), nil, "en")
	if err == nil {
		t.Fatal("Recognize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "XAI_API_KEY") {
		t.Fatalf("Recognize error = %q, want XAI_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background(), "en")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "XAI_API_KEY") {
		t.Fatalf("Stream error = %q, want XAI_API_KEY guidance", err)
	}
}

func TestXaiSTTRecognizeReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiSTT("test-key", WithXaiSTTRestURL("https://xai.example/v1/stt"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en")
	if err == nil {
		t.Fatal("Recognize error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Recognize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if statusErr.Body != `{"error":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestXaiSTTRecognizeReturnsAPITimeoutError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiSTT("test-key", WithXaiSTTRestURL("https://xai.example/v1/stt"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en")
	if err == nil {
		t.Fatal("Recognize error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Recognize error = %T %v, want APITimeoutError", err, err)
	}
}

func TestXaiSTTRecognizeReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial refused")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiSTT("test-key", WithXaiSTTRestURL("https://xai.example/v1/stt"))

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en")
	if err == nil {
		t.Fatal("Recognize error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Recognize error = %T %v, want APIConnectionError", err, err)
	}
}

func TestXaiSTTStreamReturnsAPIConnectionErrorOnDialTimeout(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, context.DeadlineExceeded
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiSTT("test-key", WithXaiSTTWebsocketURL("ws://xai.test/v1/stt"))

	_, err := provider.Stream(context.Background(), "en")
	if err == nil {
		t.Fatal("Stream error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
	var timeoutErr *llm.APITimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError but not APITimeoutError", err, err)
	}
}

func TestXaiSTTStreamReturnsAPIConnectionErrorOnDialFailure(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial refused")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiSTT("test-key", WithXaiSTTWebsocketURL("ws://xai.test/v1/stt"))

	_, err := provider.Stream(context.Background(), "en")
	if err == nil {
		t.Fatal("Stream error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestXaiSTTStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	handlerErr := make(chan error, 1)
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = newXaiSTTTestWebsocketDialer(t, func(*websocket.Conn, *http.Request) {}, handlerErr)
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewXaiSTT("test-key", WithXaiSTTWebsocketURL("ws://xai.test/v1/stt"))
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
}

func TestXaiSTTBatchResponseMapsSpeechEvent(t *testing.T) {
	event := xaiSTTBatchSpeechEvent(true, xaiSTTResponse{
		Text:     "hello world",
		Language: "en",
		Words: []xaiSTTWord{
			{Text: "hello", Start: 0.1, End: 0.4, Speaker: intPtr(1)},
			{Text: "world", Start: 0.5, End: 0.9, Speaker: intPtr(1)},
		},
	})

	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("type = %v, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello world" || alt.Language != "en" {
		t.Fatalf("alt = %+v, want English transcript", alt)
	}
	if alt.StartTime != 0.1 || alt.EndTime != 0.9 {
		t.Fatalf("time range = %v-%v, want word span", alt.StartTime, alt.EndTime)
	}
	if alt.SpeakerID != "S1" {
		t.Fatalf("speaker id = %q, want first word speaker", alt.SpeakerID)
	}
	if len(alt.Words) != 2 || alt.Words[0].Text != "hello" {
		t.Fatalf("words = %+v, want word timings", alt.Words)
	}
}

func TestXaiSTTStreamEventsMapReferenceLifecycle(t *testing.T) {
	state := &xaiSTTStreamState{interimResults: true, diarization: true}

	events := processXaiSTTStreamEvent(state, map[string]any{
		"type":     "transcript.partial",
		"text":     "hel",
		"language": "en",
		"is_final": false,
	})
	assertXaiEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertXaiEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hel")

	events = processXaiSTTStreamEvent(state, map[string]any{
		"type":         "transcript.partial",
		"text":         "hello",
		"language":     "en",
		"is_final":     true,
		"speech_final": false,
		"words": []any{
			map[string]any{"text": "hello", "start": 0.1, "end": 0.4, "speaker": float64(2)},
		},
	})
	assertXaiEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello")
	if state.emittedChunkFinal != true {
		t.Fatal("emitted chunk final = false, want true after non-speech-final final")
	}

	events = processXaiSTTStreamEvent(state, map[string]any{
		"type":         "transcript.partial",
		"text":         "hello world",
		"language":     "en",
		"is_final":     true,
		"speech_final": true,
		"words": []any{
			map[string]any{"text": "hello", "start": 0.1, "end": 0.4, "speaker": float64(2)},
			map[string]any{"text": "world", "start": 0.5, "end": 0.9, "speaker": float64(2)},
		},
	})
	assertXaiEvent(t, events, 0, stt.SpeechEventEndOfSpeech, "")
	if state.speaking {
		t.Fatal("speaking = true, want false after speech-final event")
	}
	if state.emittedChunkFinal {
		t.Fatal("emitted chunk final = true, want reset after speech-final event")
	}
}

func TestXaiSTTStreamChunksAndFlushesReferenceAudio(t *testing.T) {
	var writes [][]byte
	stream := &xaiSTTStream{
		sampleRate: 16000,
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
	}
	audioData := make([]byte, 2000)
	for i := range audioData {
		audioData[i] = byte(i)
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              audioData,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1000,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("binary writes after PushFrame = %d, want one 50ms chunk", len(writes))
	}
	if got := len(writes[0]); got != 1600 {
		t.Fatalf("first chunk length = %d, want 1600", got)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(writes) != 2 {
		t.Fatalf("binary writes after Flush = %d, want remainder chunk", len(writes))
	}
	if got := len(writes[1]); got != 400 {
		t.Fatalf("flush chunk length = %d, want 400", got)
	}
}

type multipartFile struct {
	filename    string
	contentType string
	data        []byte
}

func newXaiSTTTestWebsocketDialer(t *testing.T, handler func(*websocket.Conn, *http.Request), serverErrCh chan<- error) *websocket.Dialer {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			listener := newXaiSingleConnListener(serverConn)
			server := &http.Server{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						serverErrCh <- err
						return
					}
					defer conn.Close()
					handler(conn, r)
				}),
			}
			go func() {
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
					serverErrCh <- err
				}
			}()
			t.Cleanup(func() {
				_ = server.Close()
				_ = listener.Close()
				_ = clientConn.Close()
				_ = serverConn.Close()
			})
			return clientConn, nil
		},
		Proxy: nil,
	}
}

func readMultipartRequest(t *testing.T, req *http.Request) (map[string]string, map[string]multipartFile) {
	t.Helper()
	_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	reader := multipart.NewReader(req.Body, params["boundary"])
	fields := map[string]string{}
	files := map[string]multipartFile{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FileName() == "" {
			fields[part.FormName()] = string(data)
			continue
		}
		files[part.FormName()] = multipartFile{
			filename:    part.FileName(),
			contentType: part.Header.Get("Content-Type"),
			data:        data,
		}
	}
	return fields, files
}

func assertXaiSTTMessage(t *testing.T, messages <-chan string, handlerErr <-chan error, want string) {
	t.Helper()
	select {
	case got := <-messages:
		if got != want {
			t.Fatalf("websocket message = %q, want %q", got, want)
		}
	case err := <-handlerErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for websocket message %q", want)
	}
}

func chunkLengths(chunks [][]byte) []int {
	lengths := make([]int, 0, len(chunks))
	for _, chunk := range chunks {
		lengths = append(lengths, len(chunk))
	}
	return lengths
}

func assertXaiQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertXaiEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
	t.Helper()
	if len(events) <= index {
		t.Fatalf("events length = %d, missing index %d", len(events), index)
	}
	if events[index].Type != eventType {
		t.Fatalf("event %d type = %v, want %v", index, events[index].Type, eventType)
	}
	if text == "" {
		return
	}
	if len(events[index].Alternatives) != 1 {
		t.Fatalf("event %d alternatives = %d, want 1", index, len(events[index].Alternatives))
	}
	if events[index].Alternatives[0].Text != text {
		t.Fatalf("event %d text = %q, want %q", index, events[index].Alternatives[0].Text, text)
	}
}

func intPtr(v int) *int {
	return &v
}

type xaiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f xaiRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
