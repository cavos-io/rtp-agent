package rtzr

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestRtzrSTTDefaultsMatchReference(t *testing.T) {
	provider := NewRtzrSTT("client-id")

	if provider.apiBase != "https://openapi.vito.ai" {
		t.Fatalf("api base = %q, want reference api base", provider.apiBase)
	}
	if provider.wsBase != "wss://openapi.vito.ai" {
		t.Fatalf("ws base = %q, want reference ws base", provider.wsBase)
	}
	if provider.modelName != "sommers_ko" {
		t.Fatalf("model = %q, want sommers_ko", provider.modelName)
	}
	if got := stt.Model(provider); got != "sommers_ko" {
		t.Fatalf("model metadata = %q, want sommers_ko", got)
	}
	if got := stt.Provider(provider); got != "RTZR" {
		t.Fatalf("provider metadata = %q, want RTZR", got)
	}
	if provider.language != "ko" {
		t.Fatalf("language = %q, want ko", provider.language)
	}
	if provider.sampleRate != 8000 {
		t.Fatalf("sample rate = %d, want 8000", provider.sampleRate)
	}
	if provider.encoding != "LINEAR16" {
		t.Fatalf("encoding = %q, want LINEAR16", provider.encoding)
	}
	if provider.domain != "CALL" {
		t.Fatalf("domain = %q, want CALL", provider.domain)
	}
	if provider.epdTime != 0.8 {
		t.Fatalf("epd time = %f, want 0.8", provider.epdTime)
	}
	if provider.noiseThreshold != 0.60 {
		t.Fatalf("noise threshold = %f, want 0.60", provider.noiseThreshold)
	}
	if provider.activeThreshold != 0.80 {
		t.Fatalf("active threshold = %f, want 0.80", provider.activeThreshold)
	}
	if provider.usePunctuation {
		t.Fatal("use punctuation = true, want false")
	}
	if provider.Label() != "rtzr.STT" {
		t.Fatalf("label = %q, want rtzr.STT", provider.Label())
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.AlignedTranscript != "chunk" {
		t.Fatalf("aligned transcript = %q, want chunk", caps.AlignedTranscript)
	}
	if caps.OfflineRecognize {
		t.Fatal("offline recognize = true, want false")
	}
}

func TestNewRtzrSTTUsesEnvironmentCredentials(t *testing.T) {
	t.Setenv("RTZR_CLIENT_ID", "env-client-id")
	t.Setenv("RTZR_CLIENT_SECRET", "env-client-secret")

	provider := NewRtzrSTT("")

	if provider.clientID != "env-client-id" {
		t.Fatalf("client id = %q, want env client id", provider.clientID)
	}
	if provider.clientSecret != "env-client-secret" {
		t.Fatalf("client secret = %q, want env client secret", provider.clientSecret)
	}

	explicit := NewRtzrSTT("explicit-client-id", WithRtzrClientSecret("explicit-client-secret"))
	if explicit.clientID != "explicit-client-id" {
		t.Fatalf("client id = %q, want explicit client id", explicit.clientID)
	}
	if explicit.clientSecret != "explicit-client-secret" {
		t.Fatalf("client secret = %q, want explicit client secret", explicit.clientSecret)
	}
}

func TestRtzrBuildAuthRequestMatchesReference(t *testing.T) {
	provider := NewRtzrSTT("client-id", WithRtzrClientSecret("client-secret"))

	req, err := buildRtzrAuthRequest(context.Background(), provider)
	if err != nil {
		t.Fatalf("build auth request: %v", err)
	}
	if req.URL.String() != "https://openapi.vito.ai/v1/authenticate" {
		t.Fatalf("auth url = %q, want authenticate endpoint", req.URL.String())
	}
	if got := req.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q, want form encoding", got)
	}
	body := readRequestBody(t, req)
	values, err := url.ParseQuery(body)
	if err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if values.Get("client_id") != "client-id" {
		t.Fatalf("client_id = %q, want client-id", values.Get("client_id"))
	}
	if values.Get("client_secret") != "client-secret" {
		t.Fatalf("client_secret = %q, want client-secret", values.Get("client_secret"))
	}
}

func TestRtzrTokenUsesCustomAPIBaseAndCachesResult(t *testing.T) {
	requests := 0
	client := newRtzrTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/authenticate" {
			t.Fatalf("auth path = %q, want /v1/authenticate", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatalf("content type = %q, want form encoding", r.Header.Get("Content-Type"))
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm returned error: %v", err)
		}
		if r.Form.Get("client_id") != "client-id" || r.Form.Get("client_secret") != "client-secret" {
			t.Fatalf("form = %v, want client credentials", r.Form)
		}
		_, _ = w.Write([]byte(`{"access_token":"token-1"}`))
	}))

	provider := NewRtzrSTT("client-id",
		WithRtzrClientSecret("client-secret"),
		WithRtzrAPIBase("https://rtzr.test"),
		withRtzrHTTPClient(client),
	)
	token, err := provider.token(context.Background())
	if err != nil {
		t.Fatalf("token returned error: %v", err)
	}
	if token != "token-1" {
		t.Fatalf("token = %q, want token-1", token)
	}
	cached, err := provider.token(context.Background())
	if err != nil {
		t.Fatalf("cached token returned error: %v", err)
	}
	if cached != "token-1" || requests != 1 {
		t.Fatalf("cached token = %q requests=%d, want token-1 with one request", cached, requests)
	}
}

func TestRtzrBuildConfigAndStreamURLMatchReference(t *testing.T) {
	provider := NewRtzrSTT("client-id",
		WithRtzrModel("sommers_ja"),
		WithRtzrLanguage("ja"),
		WithRtzrSampleRate(16000),
		WithRtzrDomain("MEETING"),
		WithRtzrEPDTime(1.2),
		WithRtzrNoiseThreshold(0.4),
		WithRtzrActiveThreshold(0.7),
		WithRtzrUsePunctuation(true),
		WithRtzrKeywords([]string{"alpha", "beta:1.5"}),
	)

	config := buildRtzrConfig(provider)
	assertRtzrConfig(t, config, "model_name", "sommers_ja")
	assertRtzrConfig(t, config, "domain", "MEETING")
	assertRtzrConfig(t, config, "sample_rate", "16000")
	assertRtzrConfig(t, config, "encoding", "LINEAR16")
	assertRtzrConfig(t, config, "epd_time", "1.2")
	assertRtzrConfig(t, config, "noise_threshold", "0.4")
	assertRtzrConfig(t, config, "active_threshold", "0.7")
	assertRtzrConfig(t, config, "use_punctuation", "true")
	assertRtzrConfig(t, config, "keywords", "alpha,beta:1.5")

	streamURL, err := url.Parse(buildRtzrStreamURL(provider, config))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if !strings.HasPrefix(streamURL.String(), "wss://openapi.vito.ai/v1/transcribe:streaming?") {
		t.Fatalf("stream url = %q, want streaming endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertRtzrQuery(t, query, "model_name", "sommers_ja")
	assertRtzrQuery(t, query, "keywords", "alpha,beta:1.5")
}

func TestRtzrSTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewRtzrSTT("client-id")

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "single-shot recognition is not supported") {
		t.Fatalf("Recognize error = %q, want reference unsupported error", err.Error())
	}
}

func TestRtzrSTTStreamSendsAudioFlushAndCloseMessages(t *testing.T) {
	queryCh := make(chan url.Values, 1)
	authCh := make(chan string, 1)
	audioCh := make(chan []byte, 1)
	flushCh := make(chan string, 1)
	closeCh := make(chan string, 1)
	dialer := newRtzrTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		queryCh <- r.URL.Query()
		authCh <- r.Header.Get("Authorization")

		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"start_at":0,"duration":100,"final":false,"alternatives":[{"text":"hello"}]}`)); err != nil {
			t.Errorf("write transcript event: %v", err)
			return
		}

		msgType, audioPayload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read audio: %v", err)
			return
		}
		if msgType != websocket.BinaryMessage {
			t.Errorf("audio message type = %d, want binary", msgType)
			return
		}
		audioCh <- audioPayload

		msgType, flushPayload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read flush: %v", err)
			return
		}
		if msgType != websocket.TextMessage {
			t.Errorf("flush message type = %d, want text", msgType)
			return
		}
		flushCh <- string(flushPayload)

		msgType, closePayload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read close: %v", err)
			return
		}
		if msgType != websocket.TextMessage {
			t.Errorf("close message type = %d, want text", msgType)
			return
		}
		closeCh <- string(closePayload)
	})

	provider := NewRtzrSTT("client-id",
		WithRtzrAccessToken("access-token"),
		WithRtzrWSBase("ws://rtzr.test"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	query := receiveRtzrQuery(t, queryCh)
	if query.Get("model_name") != defaultModelName || query.Get("sample_rate") != "8000" {
		t.Fatalf("stream query = %v, want default model/sample rate", query)
	}
	if got := receiveRtzrString(t, authCh, "auth header"); got != "bearer access-token" {
		t.Fatalf("Authorization = %q, want bearer access-token", got)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event = %v, want start of speech", event.Type)
	}
	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventInterimTranscript || event.Alternatives[0].Text != "hello" {
		t.Fatalf("second event = %#v, want interim hello", event)
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01, 0x02}}); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	if got := receiveRtzrBytes(t, audioCh); string(got) != "\x01\x02" {
		t.Fatalf("audio = %v, want pcm bytes", got)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if got := receiveRtzrString(t, flushCh, "flush"); got != "EOS" {
		t.Fatalf("flush = %q, want EOS", got)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if got := receiveRtzrString(t, closeCh, "close"); got != "EOS" {
		t.Fatalf("close = %q, want EOS", got)
	}
}

func TestRtzrProcessTranscriptEventMapsInterimFinalAndWords(t *testing.T) {
	state := &rtzrTranscriptState{language: "ko"}
	payload := rtzrTranscriptPayload{
		StartAt:  100,
		Duration: 300,
		Final:    false,
		Alternatives: []rtzrAlternative{
			{Text: "hello"},
		},
		Words: []rtzrWord{
			{Text: "hello", StartAt: 100, Duration: 300},
		},
	}

	events, err := processRtzrTranscriptEvent(state, payload, 1.5)
	if err != nil {
		t.Fatalf("process event: %v", err)
	}
	assertRtzrEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertRtzrEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello")
	alt := events[1].Alternatives[0]
	if alt.Language != "ko" {
		t.Fatalf("language = %q, want ko", alt.Language)
	}
	if alt.StartTime != 1.6 || alt.EndTime != 1.9 {
		t.Fatalf("time range = %v-%v, want 1.6-1.9", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 1 || alt.Words[0].StartTime != 1.6 || alt.Words[0].EndTime != 1.9 {
		t.Fatalf("words = %+v, want adjusted word timing", alt.Words)
	}

	payload.Final = true
	payload.Alternatives[0].Text = "done"
	events, err = processRtzrTranscriptEvent(state, payload, 0)
	if err != nil {
		t.Fatalf("process final: %v", err)
	}
	assertRtzrEvent(t, events, 0, stt.SpeechEventFinalTranscript, "done")
	assertRtzrEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
}

func TestRtzrProcessTranscriptEventReturnsServerErrors(t *testing.T) {
	_, err := processRtzrTranscriptEvent(&rtzrTranscriptState{}, rtzrTranscriptPayload{Error: "bad request"}, 0)
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("error = %v, want server error", err)
	}
	_, err = processRtzrTranscriptEvent(&rtzrTranscriptState{}, rtzrTranscriptPayload{Type: "error", Message: "denied"}, 0)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("error = %v, want type error", err)
	}
}

func assertRtzrConfig(t *testing.T, config map[string]string, key string, want string) {
	t.Helper()
	if got := config[key]; got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertRtzrQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func receiveRtzrQuery(t *testing.T, ch <-chan url.Values) url.Values {
	t.Helper()
	select {
	case query := <-ch:
		return query
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream query")
		return nil
	}
}

func receiveRtzrBytes(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()
	select {
	case payload := <-ch:
		return payload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio payload")
		return nil
	}
}

func receiveRtzrString(t *testing.T, ch <-chan string, label string) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return ""
	}
}

func assertRtzrEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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

func readRequestBody(t *testing.T, req *http.Request) string {
	t.Helper()
	if req.GetBody == nil {
		t.Fatal("request GetBody is nil")
	}
	body, err := req.GetBody()
	if err != nil {
		t.Fatalf("get body: %v", err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

func newRtzrTestHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{
		Transport: rtzrRoundTripper(func(req *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			resp := recorder.Result()
			if resp.Body == nil {
				resp.Body = io.NopCloser(strings.NewReader(""))
			}
			return resp, nil
		}),
	}
}

type rtzrRoundTripper func(*http.Request) (*http.Response, error)

func (f rtzrRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newRtzrTestWebsocketDialer(t *testing.T, handler func(*websocket.Conn, *http.Request)) RtzrSTTOption {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return withRtzrWebsocketDialer(func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		listener := newRtzrSingleConnListener(serverConn)
		server := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("Upgrade returned error: %v", err)
					return
				}
				defer conn.Close()
				handler(conn, r)
			}),
		}
		serverErrCh := make(chan error, 1)
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

		dialer := websocket.Dialer{
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
		}
		conn, response, err := dialer.DialContext(ctx, endpoint, headers)
		select {
		case serverErr := <-serverErrCh:
			if err == nil {
				err = serverErr
			}
		default:
		}
		return conn, response, err
	})
}

type rtzrSingleConnListener struct {
	mu     sync.Mutex
	once   sync.Once
	conn   net.Conn
	closed chan struct{}
}

func newRtzrSingleConnListener(conn net.Conn) *rtzrSingleConnListener {
	return &rtzrSingleConnListener{
		conn:   conn,
		closed: make(chan struct{}),
	}
}

func (l *rtzrSingleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.conn != nil {
		conn := l.conn
		l.conn = nil
		l.mu.Unlock()
		return conn, nil
	}
	l.mu.Unlock()

	<-l.closed
	return nil, net.ErrClosed
}

func (l *rtzrSingleConnListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		l.mu.Lock()
		if l.conn != nil {
			_ = l.conn.Close()
			l.conn = nil
		}
		l.mu.Unlock()
	})
	return nil
}

func (l *rtzrSingleConnListener) Addr() net.Addr {
	return rtzrDummyAddr("pipe")
}

type rtzrDummyAddr string

func (a rtzrDummyAddr) Network() string { return string(a) }
func (a rtzrDummyAddr) String() string  { return string(a) }
