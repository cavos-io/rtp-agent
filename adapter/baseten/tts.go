package baseten

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultBasetenTTSVoice       = "tara"
	defaultBasetenTTSLanguage    = "en"
	defaultBasetenTTSTemperature = 0.6
	defaultBasetenTTSSampleRate  = 24000
	defaultBasetenTTSMaxTokens   = 2000
	defaultBasetenTTSBufferSize  = 10
	basetenTTSEndSentinel        = "__END__"
)

type BasetenTTS struct {
	apiKey        string
	modelEndpoint string
	voice         string
	language      string
	temperature   float64
	maxTokens     int
	bufferSize    int
	sampleRate    int
	httpClient    basetenTTSHTTPDoer
	dialWebsocket basetenTTSWebsocketDialer
}

type BasetenTTSOption func(*BasetenTTS)

type basetenTTSHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type basetenTTSWebsocketDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

func WithBasetenTTSModelEndpoint(endpoint string) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if endpoint != "" {
			t.modelEndpoint = endpoint
		}
	}
}

func WithBasetenTTSVoice(voice string) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithBasetenTTSLanguage(language string) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithBasetenTTSTemperature(temperature float64) BasetenTTSOption {
	return func(t *BasetenTTS) {
		t.temperature = temperature
	}
}

func WithBasetenTTSMaxTokens(maxTokens int) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if maxTokens > 0 {
			t.maxTokens = maxTokens
		}
	}
}

func WithBasetenTTSBufferSize(bufferSize int) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if bufferSize > 0 {
			t.bufferSize = bufferSize
		}
	}
}

func withBasetenTTSHTTPClient(client basetenTTSHTTPDoer) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if client != nil {
			t.httpClient = client
		}
	}
}

func withBasetenTTSWebsocketDialer(dialer basetenTTSWebsocketDialer) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if dialer != nil {
			t.dialWebsocket = dialer
		}
	}
}

func NewBasetenTTS(apiKey string, model string, opts ...BasetenTTSOption) (*BasetenTTS, error) {
	if apiKey == "" {
		apiKey = os.Getenv(basetenAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("BASETEN_API_KEY is required, either as argument or set BASETEN_API_KEY environment variable")
	}

	endpoint := ""
	if model != "" {
		endpoint = model
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") &&
			!strings.HasPrefix(endpoint, "ws://") && !strings.HasPrefix(endpoint, "wss://") {
			endpoint = fmt.Sprintf("https://model-%s.api.baseten.co/environments/production/predict", endpoint)
		}
	} else if envEndpoint := os.Getenv(basetenModelEndpointEnv); envEndpoint != "" {
		endpoint = envEndpoint
	}
	provider := &BasetenTTS{
		apiKey:        apiKey,
		modelEndpoint: endpoint,
		voice:         defaultBasetenTTSVoice,
		language:      defaultBasetenTTSLanguage,
		temperature:   defaultBasetenTTSTemperature,
		maxTokens:     defaultBasetenTTSMaxTokens,
		bufferSize:    defaultBasetenTTSBufferSize,
		sampleRate:    defaultBasetenTTSSampleRate,
		httpClient:    http.DefaultClient,
		dialWebsocket: defaultBasetenTTSWebsocketDialer,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.modelEndpoint == "" {
		return nil, fmt.Errorf("BASETEN_MODEL_ENDPOINT is required, provide model_endpoint or set BASETEN_MODEL_ENDPOINT environment variable")
	}
	return provider, nil
}

func (t *BasetenTTS) Label() string { return "baseten.TTS" }
func (t *BasetenTTS) Model() string { return "unknown" }
func (t *BasetenTTS) Provider() string {
	return "Baseten"
}
func (t *BasetenTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: strings.HasPrefix(t.modelEndpoint, "ws://") || strings.HasPrefix(t.modelEndpoint, "wss://"), AlignedTranscript: false}
}
func (t *BasetenTTS) SampleRate() int  { return t.sampleRate }
func (t *BasetenTTS) NumChannels() int { return 1 }

func (t *BasetenTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildBasetenTTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("baseten tts error: %s", string(respBody))
	}
	return &basetenTTSChunkedStream{body: resp.Body, sampleRate: t.sampleRate}, nil
}

func buildBasetenTTSRequest(ctx context.Context, t *BasetenTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"prompt":      text,
		"voice":       t.voice,
		"temperature": t.temperature,
		"language":    t.language,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.modelEndpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+t.apiKey)
	return req, nil
}

func (t *BasetenTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if !t.Capabilities().Streaming {
		return nil, fmt.Errorf("baseten websocket tts streaming requires a ws:// or wss:// endpoint")
	}
	conn, _, err := t.dialWebsocket(ctx, t.modelEndpoint, buildBasetenTTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial baseten tts websocket: %w", err)
	}
	startMessage, err := buildBasetenTTSStartMessage(t)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, startMessage); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &basetenTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
		events:     make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
	}
	go stream.readLoop()
	return stream, nil
}

func defaultBasetenTTSWebsocketDialer(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
}

func buildBasetenTTSWebsocketHeaders(t *BasetenTTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Api-Key "+t.apiKey)
	return headers
}

func buildBasetenTTSStartMessage(t *BasetenTTS) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"voice":       t.voice,
		"max_tokens":  t.maxTokens,
		"buffer_size": t.bufferSize,
	})
}

func buildBasetenTTSTextMessage(text string) ([]byte, error) {
	return []byte(text), nil
}

func buildBasetenTTSEndMessage() ([]byte, error) {
	return []byte(basetenTTSEndSentinel), nil
}

type basetenTTSChunkedStream struct {
	body       io.ReadCloser
	sampleRate int
}

func (s *basetenTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.body.Read(buf)
	if n > 0 {
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              buf[:n],
				SampleRate:        uint32(s.sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(n / 2),
			},
		}, nil
	}
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}
	return nil, io.EOF
}

func (s *basetenTTSChunkedStream) Close() error {
	return s.body.Close()
}

type basetenTTSSynthesizeStream struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	sampleRate int
	events     chan *tts.SynthesizedAudio
	errCh      chan error
	mu         sync.Mutex
	closed     bool
}

func (s *basetenTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	message, err := buildBasetenTTSTextMessage(text)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *basetenTTSSynthesizeStream) Flush() error {
	return nil
}

func (s *basetenTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if endMessage, err := buildBasetenTTSEndMessage(); err == nil {
		_ = s.conn.WriteMessage(websocket.TextMessage, endMessage)
	}
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *basetenTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case audio, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return audio, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *basetenTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
			}
			return
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		audio, err := basetenTTSAudioFromStreamMessage(payload, s.sampleRate)
		if err != nil {
			s.errCh <- err
			return
		}
		if audio != nil {
			s.events <- audio
		}
	}
}

func basetenTTSAudioFromStreamMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(payload),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(payload) / 2),
		},
	}, nil
}
