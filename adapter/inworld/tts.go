package inworld

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultInworldBaseURL                  = "https://api.inworld.ai"
	defaultInworldWebsocketURL             = "wss://api.inworld.ai"
	defaultInworldModel                    = "inworld-tts-1.5-max"
	defaultInworldVoice                    = "Ashley"
	defaultInworldEncoding                 = "PCM"
	defaultInworldBitRate                  = 64000
	defaultInworldSampleRate               = 24000
	defaultInworldSpeakingRate             = 1.0
	defaultInworldTemperature              = 1.0
	defaultInworldBufferCharThreshold      = 120
	defaultInworldMaxBufferDelayMillis     = 3000
	defaultInworldTimestampTransportPolicy = "ASYNC"
)

type InworldTTS struct {
	apiKey              string
	baseURL             string
	wsURL               string
	model               string
	voice               string
	encoding            string
	bitRate             int
	sampleRate          int
	speakingRate        float64
	temperature         float64
	bufferCharThreshold int
	maxBufferDelayMS    int
}

type InworldTTSOption func(*InworldTTS)

func WithInworldTTSBaseURL(baseURL string) InworldTTSOption {
	return func(t *InworldTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithInworldTTSWebsocketURL(wsURL string) InworldTTSOption {
	return func(t *InworldTTS) {
		if wsURL != "" {
			t.wsURL = strings.TrimRight(wsURL, "/")
		}
	}
}

func WithInworldTTSModel(model string) InworldTTSOption {
	return func(t *InworldTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithInworldTTSEncoding(encoding string) InworldTTSOption {
	return func(t *InworldTTS) {
		if encoding != "" {
			t.encoding = encoding
		}
	}
}

func WithInworldTTSBitRate(bitRate int) InworldTTSOption {
	return func(t *InworldTTS) {
		if bitRate > 0 {
			t.bitRate = bitRate
		}
	}
}

func WithInworldTTSSampleRate(sampleRate int) InworldTTSOption {
	return func(t *InworldTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithInworldTTSSpeakingRate(speakingRate float64) InworldTTSOption {
	return func(t *InworldTTS) {
		t.speakingRate = speakingRate
	}
}

func WithInworldTTSTemperature(temperature float64) InworldTTSOption {
	return func(t *InworldTTS) {
		t.temperature = temperature
	}
}

func NewInworldTTS(apiKey string, voice string, opts ...InworldTTSOption) *InworldTTS {
	provider := &InworldTTS{
		apiKey:              apiKey,
		baseURL:             defaultInworldBaseURL,
		wsURL:               defaultInworldWebsocketURL,
		model:               defaultInworldModel,
		voice:               voice,
		encoding:            defaultInworldEncoding,
		bitRate:             defaultInworldBitRate,
		sampleRate:          defaultInworldSampleRate,
		speakingRate:        defaultInworldSpeakingRate,
		temperature:         defaultInworldTemperature,
		bufferCharThreshold: defaultInworldBufferCharThreshold,
		maxBufferDelayMS:    defaultInworldMaxBufferDelayMillis,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultInworldVoice
	}
	return provider
}

func (t *InworldTTS) Label() string { return "inworld.TTS" }
func (t *InworldTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *InworldTTS) SampleRate() int  { return t.sampleRate }
func (t *InworldTTS) NumChannels() int { return 1 }

func (t *InworldTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildInworldTTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("inworld tts error: %s", string(respBody))
	}
	return &inworldTTSChunkedStream{resp: resp, sampleRate: t.sampleRate}, nil
}

func buildInworldTTSRequest(ctx context.Context, t *InworldTTS, text string) (*http.Request, error) {
	audioConfig := map[string]any{
		"audioEncoding":   t.encoding,
		"bitrate":         t.bitRate,
		"sampleRateHertz": t.sampleRate,
		"temperature":     t.temperature,
		"speakingRate":    t.speakingRate,
	}
	body := map[string]any{
		"text":                       text,
		"voiceId":                    t.voice,
		"modelId":                    t.model,
		"audioConfig":                audioConfig,
		"timestampTransportStrategy": defaultInworldTimestampTransportPolicy,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/tts/v1/voice:stream", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+t.apiKey)
	req.Header.Set("X-User-Agent", "livekit-agents-go")
	return req, nil
}

func (t *InworldTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildInworldTTSWebsocketURL(t), buildInworldTTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial inworld tts websocket: %w", err)
	}
	contextID := fmt.Sprintf("ctx-%d", time.Now().UnixNano())
	createPayload, err := buildInworldTTSCreateContextMessage(t, contextID)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, createPayload); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &inworldTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		contextID:  contextID,
		sampleRate: t.sampleRate,
	}, nil
}

func buildInworldTTSWebsocketURL(t *InworldTTS) string {
	return strings.TrimRight(t.wsURL, "/") + "/tts/v1/voice:streamBidirectional"
}

func buildInworldTTSWebsocketHeaders(t *InworldTTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Basic "+t.apiKey)
	headers.Set("X-User-Agent", "livekit-agents-go")
	headers.Set("X-Request-Id", fmt.Sprintf("req-%d", time.Now().UnixNano()))
	return headers
}

func buildInworldTTSCreateContextMessage(t *InworldTTS, contextID string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"contextId": contextID,
		"create": map[string]any{
			"voiceId": t.voice,
			"modelId": t.model,
			"audioConfig": map[string]any{
				"audioEncoding":   t.encoding,
				"sampleRateHertz": t.sampleRate,
				"bitrate":         t.bitRate,
				"speakingRate":    t.speakingRate,
			},
			"temperature":                t.temperature,
			"bufferCharThreshold":        t.bufferCharThreshold,
			"maxBufferDelayMs":           t.maxBufferDelayMS,
			"timestampTransportStrategy": defaultInworldTimestampTransportPolicy,
			"autoMode":                   true,
		},
	})
}

func buildInworldTTSSendTextMessage(contextID, text string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"contextId": contextID,
		"send_text": map[string]any{
			"text": text,
		},
	})
}

func buildInworldTTSFlushContextMessage(contextID string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"contextId":     contextID,
		"flush_context": map[string]any{},
	})
}

func buildInworldTTSCloseContextMessage(contextID string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"contextId":     contextID,
		"close_context": map[string]any{},
	})
}

type inworldTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	scanner    *bufio.Scanner
}

func (s *inworldTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp.Body)
	}
	for s.scanner.Scan() {
		audio, done, err := inworldAudioFromWebsocketMessage(s.scanner.Bytes(), s.sampleRate)
		if err != nil {
			return nil, err
		}
		if done {
			return nil, io.EOF
		}
		if audio != nil {
			return audio, nil
		}
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *inworldTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

type inworldTTSWebsocketChunkedStream struct {
	conn       *websocket.Conn
	sampleRate int
}

func (s *inworldTTSWebsocketChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.conn == nil {
		return nil, io.EOF
	}
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || err == io.EOF {
				return nil, io.EOF
			}
			return nil, err
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := inworldAudioFromWebsocketMessage(payload, s.sampleRate)
		if err != nil {
			return nil, err
		}
		if done {
			return nil, io.EOF
		}
		if audio != nil {
			return audio, nil
		}
	}
}

func (s *inworldTTSWebsocketChunkedStream) Close() error {
	if s.conn == nil {
		return nil
	}
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

type inworldTTSSynthesizeStream struct {
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	contextID   string
	sampleRate  int
	pendingText bytes.Buffer
	mu          sync.Mutex
	closed      bool
}

func (s *inworldTTSSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if text == "" {
		return nil
	}
	_, err := s.pendingText.WriteString(text)
	return err
}

func (s *inworldTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := strings.TrimSpace(s.pendingText.String())
	s.pendingText.Reset()
	if s.conn == nil {
		return nil
	}
	if text != "" {
		payload, err := buildInworldTTSSendTextMessage(s.contextID, text)
		if err != nil {
			return err
		}
		if err := s.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			return err
		}
	}
	flushPayload, err := buildInworldTTSFlushContextMessage(s.contextID)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, flushPayload)
}

func (s *inworldTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn == nil {
		return nil
	}
	if s.contextID != "" {
		if closePayload, err := buildInworldTTSCloseContextMessage(s.contextID); err == nil {
			_ = s.conn.WriteMessage(websocket.TextMessage, closePayload)
		}
	}
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *inworldTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	if s.ctx != nil {
		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		default:
		}
	}
	return (&inworldTTSWebsocketChunkedStream{conn: s.conn, sampleRate: s.sampleRate}).Next()
}

func inworldAudioFromWebsocketMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			ContextID     string         `json:"contextId"`
			ContextClosed map[string]any `json:"contextClosed"`
			Status        struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"status"`
			AudioChunk struct {
				AudioContent string `json:"audioContent"`
			} `json:"audioChunk"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.Error.Code != 0 || message.Error.Message != "" {
		if message.Error.Message == "" {
			message.Error.Message = "unknown error"
		}
		return nil, false, fmt.Errorf("inworld tts error: %s", message.Error.Message)
	}
	if message.Result.Status.Code != 0 {
		if message.Result.Status.Message == "" {
			message.Result.Status.Message = "unknown error"
		}
		return nil, false, fmt.Errorf("inworld tts error: %s", message.Result.Status.Message)
	}
	if message.Result.ContextClosed != nil {
		return nil, true, nil
	}
	if message.Result.AudioChunk.AudioContent == "" {
		return nil, false, nil
	}
	audio, err := base64.StdEncoding.DecodeString(message.Result.AudioChunk.AudioContent)
	if err != nil {
		return nil, false, err
	}
	return &tts.SynthesizedAudio{
		SegmentID: message.Result.ContextID,
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}, false, nil
}
