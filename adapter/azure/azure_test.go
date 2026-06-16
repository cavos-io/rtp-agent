package azure

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

type azureRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f azureRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAzureSTTFallsBackToSpeechEnvironment(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "env-key")
	t.Setenv(azureSpeechRegionEnv, "eastus")

	provider, err := NewAzureSTT("", "")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v, want nil from env config", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", provider.apiKey)
	}
	if provider.region != "eastus" {
		t.Fatalf("region = %q, want eastus", provider.region)
	}
	if provider.Label() != "azure.STT" {
		t.Fatalf("Label = %q, want azure.STT", provider.Label())
	}
	if provider.Provider() != "Azure STT" {
		t.Fatalf("Provider = %q, want Azure STT", provider.Provider())
	}
	if provider.Model() != "unknown" {
		t.Fatalf("Model = %q, want unknown", provider.Model())
	}
	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || caps.AlignedTranscript != "chunk" || caps.OfflineRecognize {
		t.Fatalf("Capabilities = %+v, want reference streaming/interim/chunk without offline", caps)
	}
}

func TestAzureSTTRequiresSpeechConfig(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "")
	t.Setenv(azureSpeechRegionEnv, "")

	_, err := NewAzureSTT("", "")

	if err == nil || !strings.Contains(err.Error(), "AZURE_SPEECH_KEY") {
		t.Fatalf("NewAzureSTT error = %v, want speech config error", err)
	}
}

func TestAzureSTTRecognizeReportsUnsupportedOffline(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	_, err = provider.Recognize(context.Background(), nil, "en-US")

	if err == nil || !strings.Contains(err.Error(), "does not support single frame recognition") {
		t.Fatalf("Recognize error = %v, want unsupported offline error", err)
	}
}

func TestAzureSTTBuildsReferenceStreamURL(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	streamURL := buildAzureSTTStreamURL(provider, "id-ID")
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}

	if parsed.Scheme != "wss" {
		t.Fatalf("stream URL scheme = %q, want wss", parsed.Scheme)
	}
	if parsed.Host != "eastus.stt.speech.microsoft.com" {
		t.Fatalf("stream URL host = %q, want eastus.stt.speech.microsoft.com", parsed.Host)
	}
	if parsed.Path != "/speech/recognition/conversation/cognitiveservices/v1" {
		t.Fatalf("stream URL path = %q, want Azure conversation endpoint", parsed.Path)
	}
	query := parsed.Query()
	if query.Get("language") != "id-ID" {
		t.Fatalf("language query = %q, want id-ID", query.Get("language"))
	}
	if query.Get("format") != "detailed" {
		t.Fatalf("format query = %q, want detailed", query.Get("format"))
	}
}

func TestAzureSTTStreamUsesWebsocketProtocol(t *testing.T) {
	requests := make(chan *http.Request, 1)
	configMessages := make(chan string, 1)
	audioMessages := make(chan []byte, 1)

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.dialWebsocket = azureTestDialer(t, requests, configMessages, audioMessages)

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	req := receiveAzureTestValue(t, requests, "request")
	if req.URL.Path != "/speech/recognition/conversation/cognitiveservices/v1" {
		t.Fatalf("path = %q, want Azure conversation endpoint", req.URL.Path)
	}
	if req.URL.Query().Get("language") != "id-ID" {
		t.Fatalf("language query = %q, want id-ID", req.URL.Query().Get("language"))
	}
	if req.URL.Query().Get("format") != "detailed" {
		t.Fatalf("format query = %q, want detailed", req.URL.Query().Get("format"))
	}
	if got := req.Header.Get("Ocp-Apim-Subscription-Key"); got != "key" {
		t.Fatalf("subscription header = %q, want key", got)
	}
	if got := req.Header.Get("X-ConnectionId"); got == "" {
		t.Fatal("X-ConnectionId header empty")
	}

	configMessage := receiveAzureTestValue(t, configMessages, "speech config")
	configHeaders, configBody := splitAzureTestMessage(t, []byte(configMessage))
	if configHeaders["Path"] != "speech.config" {
		t.Fatalf("speech config Path = %q, want speech.config", configHeaders["Path"])
	}
	var configPayload map[string]any
	if err := json.Unmarshal(configBody, &configPayload); err != nil {
		t.Fatalf("speech config JSON: %v", err)
	}
	if _, ok := configPayload["context"].(map[string]any); !ok {
		t.Fatalf("speech config = %s, want context object", string(configBody))
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02, 0x03, 0x04},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	audioMessage := receiveAzureTestValue(t, audioMessages, "audio")
	audioHeaders, audioPayload := splitAzureTestMessage(t, audioMessage)
	if audioHeaders["Path"] != "audio" {
		t.Fatalf("audio Path = %q, want audio", audioHeaders["Path"])
	}
	if audioHeaders["Content-Type"] != "audio/x-wav" {
		t.Fatalf("audio Content-Type = %q, want audio/x-wav", audioHeaders["Content-Type"])
	}
	if !bytes.Equal(audioPayload, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("audio payload = %v, want pushed PCM", audioPayload)
	}

	interim := nextAzureTestEvent(t, stream)
	if interim.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("interim Type = %s, want interim_transcript", interim.Type)
	}
	if got := interim.Alternatives[0].Text; got != "halo sementara" {
		t.Fatalf("interim text = %q, want halo sementara", got)
	}
	if got := interim.Alternatives[0].Language; got != "id-ID" {
		t.Fatalf("interim language = %q, want id-ID", got)
	}

	final := nextAzureTestEvent(t, stream)
	if final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("final Type = %s, want final_transcript", final.Type)
	}
	if got := final.Alternatives[0].Text; got != "halo final" {
		t.Fatalf("final text = %q, want halo final", got)
	}
	if got := final.Alternatives[0].Confidence; got != 0.87 {
		t.Fatalf("final confidence = %v, want 0.87", got)
	}
	if got := final.Alternatives[0].Language; got != "id-ID" {
		t.Fatalf("final language = %q, want id-ID", got)
	}

	end := nextAzureTestEvent(t, stream)
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("end Type = %s, want end_of_speech", end.Type)
	}
}

func TestAzureTTSDefaultsAndEnvironmentMatchReference(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "env-key")
	t.Setenv(azureSpeechRegionEnv, "westus")

	provider, err := NewAzureTTS("", "", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v, want nil from env config", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", provider.apiKey)
	}
	if provider.region != "westus" {
		t.Fatalf("region = %q, want westus", provider.region)
	}
	if provider.voice != "en-US-JennyNeural" {
		t.Fatalf("voice = %q, want reference default", provider.voice)
	}
	if provider.language != "en-US" {
		t.Fatalf("language = %q, want reference default", provider.language)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sampleRate = %d, want 24000", provider.sampleRate)
	}
	if provider.Label() != "azure.TTS" {
		t.Fatalf("Label = %q, want azure.TTS", provider.Label())
	}
	if provider.Provider() != "Azure TTS" {
		t.Fatalf("Provider = %q, want Azure TTS", provider.Provider())
	}
	if provider.Model() != "unknown" {
		t.Fatalf("Model = %q, want unknown", provider.Model())
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("SampleRate = %d, want 24000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("NumChannels = %d, want 1", provider.NumChannels())
	}
	if provider.Language() != "en-US" {
		t.Fatalf("Language = %q, want en-US", provider.Language())
	}
	if provider.Capabilities().Streaming {
		t.Fatal("Streaming = true, want false for Azure REST TTS")
	}
}

func TestAzureTTSRequiresSpeechConfig(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "")
	t.Setenv(azureSpeechRegionEnv, "")

	_, err := NewAzureTTS("", "", "")

	if err == nil || !strings.Contains(err.Error(), "AZURE_SPEECH_KEY") {
		t.Fatalf("NewAzureTTS error = %v, want speech config error", err)
	}
}

func TestAzureTTSBuildsReferenceRequest(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "en-US-AvaNeural")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.URL.String() != "https://eastus.tts.speech.microsoft.com/cognitiveservices/v1" {
		t.Fatalf("URL = %q, want reference endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-Microsoft-OutputFormat"); got != "raw-24khz-16bit-mono-pcm" {
		t.Fatalf("output format = %q, want raw-24khz-16bit-mono-pcm", got)
	}
	if got := req.Header.Get("Ocp-Apim-Subscription-Key"); got != "key" {
		t.Fatalf("subscription header = %q, want key", got)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), `voice name="en-US-AvaNeural"`) {
		t.Fatalf("SSML = %q, want voice name", string(body))
	}
}

func TestAzureTTSBuildsRequestWithConfiguredLanguage(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "id-ID-GadisNeural", "id-ID")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "halo")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	ssml := string(body)
	if !strings.Contains(ssml, `xml:lang="id-ID"`) {
		t.Fatalf("SSML = %q, want configured language", ssml)
	}
	if !strings.Contains(ssml, `voice name="id-ID-GadisNeural"`) {
		t.Fatalf("SSML = %q, want configured voice", ssml)
	}
}

func TestAzureTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	provider.UpdateOptions("id-ID-GadisNeural", "id-ID")

	req, err := buildAzureTTSRequest(context.Background(), provider, "halo")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	ssml := string(body)
	if !strings.Contains(ssml, `xml:lang="id-ID"`) {
		t.Fatalf("SSML = %q, want updated language", ssml)
	}
	if !strings.Contains(ssml, `voice name="id-ID-GadisNeural"`) {
		t.Fatalf("SSML = %q, want updated voice", ssml)
	}
	if provider.Language() != "id-ID" {
		t.Fatalf("Language() = %q, want id-ID", provider.Language())
	}
}

func TestAzureTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &azureTTSChunkedStream{
		body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sampleRate = %d, want 24000", audio.Frame.SampleRate)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestAzureTTSChunkedStreamKeepsFinalReadBytes(t *testing.T) {
	stream := &azureTTSChunkedStream{
		body:       &finalReadBytesCloser{data: []byte{0x01, 0x02}},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02}) {
		t.Fatalf("data = %v, want final read bytes", audio.Frame.Data)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
}

func TestAzureTTSSynthesizeUsesConfiguredClient(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Ocp-Apim-Subscription-Key") != "key" {
				t.Fatalf("subscription key header = %q, want key", req.Header.Get("Ocp-Apim-Subscription-Key"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sampleRate = %d, want 24000", audio.Frame.SampleRate)
	}
}

type finalReadBytesCloser struct {
	data []byte
	done bool
}

func (r *finalReadBytesCloser) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func (r *finalReadBytesCloser) Close() error {
	return nil
}

func receiveAzureTestValue[T any](t *testing.T, ch <-chan T, name string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		var zero T
		t.Fatalf("timed out waiting for %s", name)
		return zero
	}
}

func splitAzureTestMessage(t *testing.T, payload []byte) (map[string]string, []byte) {
	t.Helper()
	parts := bytes.SplitN(payload, []byte("\r\n\r\n"), 2)
	if len(parts) != 2 {
		t.Fatalf("azure message %q missing header separator", string(payload))
	}
	headers := map[string]string{}
	for _, line := range strings.Split(string(parts[0]), "\r\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			headers[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return headers, parts[1]
}

func nextAzureTestEvent(t *testing.T, stream stt.RecognizeStream) *stt.SpeechEvent {
	t.Helper()
	type result struct {
		event *stt.SpeechEvent
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		event, err := stream.Next()
		ch <- result{event: event, err: err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("Next error = %v", res.err)
		}
		return res.event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream event")
		return nil
	}
}

func azureTestDialer(
	t *testing.T,
	requests chan<- *http.Request,
	configMessages chan<- string,
	audioMessages chan<- []byte,
) azureSTTWebsocketDialer {
	t.Helper()
	return func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		errCh := make(chan error, 1)
		go runAzureTestWebsocketServer(t, serverConn, requests, configMessages, audioMessages, errCh)

		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, nil, err
		}
		dialer := websocket.Dialer{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
			Proxy: nil,
		}
		conn, resp, err := dialer.DialContext(ctx, parsed.String(), headers)
		if err != nil {
			clientConn.Close()
			select {
			case serverErr := <-errCh:
				return nil, resp, fmt.Errorf("%w; server: %v", err, serverErr)
			default:
				return nil, resp, err
			}
		}
		go func() {
			if serverErr := <-errCh; serverErr != nil {
				t.Errorf("test websocket server: %v", serverErr)
			}
		}()
		return conn, resp, nil
	}
}

func runAzureTestWebsocketServer(
	t *testing.T,
	conn net.Conn,
	requests chan<- *http.Request,
	configMessages chan<- string,
	audioMessages chan<- []byte,
	errCh chan<- error,
) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	requests <- req
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", azureTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}

	opcode, payload, err := readAzureTestWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.TextMessage {
		errCh <- fmt.Errorf("speech config opcode = %d, want text", opcode)
		return
	}
	configMessages <- string(payload)

	opcode, payload, err = readAzureTestWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.BinaryMessage {
		errCh <- fmt.Errorf("audio opcode = %d, want binary", opcode)
		return
	}
	audioMessages <- payload

	for _, message := range []string{
		"Path: speech.hypothesis\r\nContent-Type: application/json\r\n\r\n{\"Text\":\"halo sementara\"}",
		"Path: speech.phrase\r\nContent-Type: application/json\r\n\r\n{\"RecognitionStatus\":\"Success\",\"DisplayText\":\"halo final\",\"NBest\":[{\"Display\":\"halo final\",\"Confidence\":0.87}]}",
		"Path: turn.end\r\nContent-Type: application/json\r\n\r\n{}",
	} {
		if err := writeAzureTestWebsocketFrame(conn, websocket.TextMessage, []byte(message)); err != nil {
			errCh <- err
			return
		}
	}
	_, _, _ = readAzureTestWebsocketFrame(reader)
	errCh <- nil
}

func azureTestAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func readAzureTestWebsocketFrame(r io.Reader) (int, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	opcode := int(header[0] & 0x0f)
	masked := header[1]&0x80 != 0
	payloadLen := uint64(header[1] & 0x7f)
	switch payloadLen {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(r, extended[:]); err != nil {
			return 0, nil, err
		}
		payloadLen = uint64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(r, extended[:]); err != nil {
			return 0, nil, err
		}
		payloadLen = binary.BigEndian.Uint64(extended[:])
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, payloadLen)
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

func writeAzureTestWebsocketFrame(w io.Writer, opcode int, payload []byte) error {
	header := []byte{0x80 | byte(opcode)}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(payload)))
		header = append(header, 127)
		header = append(header, length[:]...)
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func TestAzureTTSStreamReportsUnsupported(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	_, err = provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Stream error = %v, want unsupported error", err)
	}
}

func TestAzureTTSImplementsInterface(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}
	var _ tts.TTS = provider
}

func TestAzureSTTImplementsInterface(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	var _ stt.STT = provider
}
