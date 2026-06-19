package smallestai

import (
	"bytes"
	"context"
	"encoding/base64"
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
	defaultSmallestAIBaseURL       = "https://api.smallest.ai/waves/v1"
	defaultSmallestAIWebsocketURL  = "wss://api.smallest.ai/waves/v1/tts/live"
	smallestAIPluginVersion        = "1.5.15"
	defaultSmallestAIModel         = "lightning_v3.1_pro"
	defaultSmallestAIProVoice      = "meher"
	defaultSmallestAIStandardVoice = "sophia"
	defaultSmallestAISampleRate    = 24000
	defaultSmallestAISpeed         = 1.0
	defaultSmallestAILanguage      = "en"
	defaultSmallestAIOutputFormat  = "pcm"
)

type SmallestAITTS struct {
	apiKey       string
	baseURL      string
	model        string
	voice        string
	sampleRate   int
	speed        float64
	language     string
	outputFormat string
	wsURL        string
}

type SmallestAITTSOption func(*SmallestAITTS)

func WithSmallestAITTSBaseURL(baseURL string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSmallestAITTSWebsocketURL(wsURL string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if wsURL != "" {
			t.wsURL = wsURL
		}
	}
}

func WithSmallestAITTSModel(model string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if model != "" {
			t.model = model
			if t.voice == "" {
				t.voice = defaultSmallestAIVoice(model)
			}
		}
	}
}

func WithSmallestAITTSVoice(voice string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithSmallestAITTSSampleRate(sampleRate int) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithSmallestAITTSSpeed(speed float64) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		t.speed = speed
	}
}

func WithSmallestAITTSLanguage(language string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithSmallestAITTSOutputFormat(outputFormat string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if outputFormat != "" {
			t.outputFormat = outputFormat
		}
	}
}

func NewSmallestAITTS(apiKey string, voice string, opts ...SmallestAITTSOption) *SmallestAITTS {
	if apiKey == "" {
		apiKey = os.Getenv(smallestAIAPIKeyEnv)
	}
	provider := &SmallestAITTS{
		apiKey:       apiKey,
		baseURL:      defaultSmallestAIBaseURL,
		model:        defaultSmallestAIModel,
		voice:        voice,
		sampleRate:   defaultSmallestAISampleRate,
		speed:        defaultSmallestAISpeed,
		language:     defaultSmallestAILanguage,
		outputFormat: defaultSmallestAIOutputFormat,
		wsURL:        defaultSmallestAIWebsocketURL,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultSmallestAIVoice(provider.model)
	}
	return provider
}

func (t *SmallestAITTS) Label() string { return "smallestai.TTS" }
func (t *SmallestAITTS) Model() string { return t.model }
func (t *SmallestAITTS) Provider() string {
	return "SmallestAI"
}

func (t *SmallestAITTS) UpdateOptions(opts ...SmallestAITTSOption) {
	if t == nil {
		return
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	if t.voice == "" {
		t.voice = defaultSmallestAIVoice(t.model)
	}
}

func (t *SmallestAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *SmallestAITTS) SampleRate() int  { return t.sampleRate }
func (t *SmallestAITTS) NumChannels() int { return 1 }

func (t *SmallestAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateSmallestAITTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	req, err := buildSmallestAITTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("smallestai tts error: %s", string(respBody))
	}

	return &smallestaiTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildSmallestAITTSRequest(ctx context.Context, t *SmallestAITTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"model":         t.model,
		"voice_id":      t.voice,
		"text":          text,
		"sample_rate":   t.sampleRate,
		"speed":         t.speed,
		"language":      t.language,
		"output_format": t.outputFormat,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/tts", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Source", "livekit")
	return req, nil
}

func (t *SmallestAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateSmallestAITTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildSmallestAITTSWebsocketURL(t), buildSmallestAITTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial smallestai tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &smallestaiTTSSynthesizeStream{
		provider:   t,
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
	}, nil
}

func validateSmallestAITTSAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("smallestai API key is required, either as argument or set SMALLEST_API_KEY environment variable")
	}
	return nil
}

func buildSmallestAITTSWebsocketURL(t *SmallestAITTS) string {
	return t.wsURL
}

func buildSmallestAITTSWebsocketHeaders(t *SmallestAITTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+t.apiKey)
	headers.Set("X-Source", "livekit")
	headers.Set("X-LiveKit-Version", smallestAIPluginVersion)
	return headers
}

func buildSmallestAITTSStreamMessage(t *SmallestAITTS, text string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"model":       t.model,
		"voice_id":    t.voice,
		"text":        text,
		"sample_rate": t.sampleRate,
		"speed":       t.speed,
		"language":    t.language,
	})
}

type smallestaiTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	mu         sync.Mutex
}

func (s *smallestaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	s.mu.Lock()
	resp := s.resp
	s.mu.Unlock()
	if resp == nil || resp.Body == nil {
		return nil, io.EOF
	}
	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *smallestaiTTSChunkedStream) Close() error {
	s.mu.Lock()
	resp := s.resp
	s.resp = nil
	s.mu.Unlock()
	if resp == nil || resp.Body == nil {
		return nil
	}
	return resp.Body.Close()
}

type smallestaiTTSWebsocketChunkedStream struct {
	conn       *websocket.Conn
	sampleRate int
	segmentID  string
}

func (s *smallestaiTTSWebsocketChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.conn == nil {
		return nil, io.EOF
	}
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || err == io.EOF {
				return nil, fmt.Errorf("smallestai tts websocket closed unexpectedly: %w", err)
			}
			return nil, err
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := smallestAITTSAudioFromWebsocketMessage(payload, s.sampleRate, s.segmentID)
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

type smallestaiTTSSynthesizeStream struct {
	provider    *SmallestAITTS
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	sampleRate  int
	segmentID   string
	pendingText bytes.Buffer
	mu          sync.Mutex
	closed      bool
}

func (s *smallestaiTTSSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if text == "" {
		return nil
	}
	if s.closed {
		return fmt.Errorf("smallestai tts stream is closed")
	}
	_, err := s.pendingText.WriteString(text)
	return err
}

func (s *smallestaiTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("smallestai tts stream is closed")
	}
	text := strings.TrimSpace(s.pendingText.String())
	s.pendingText.Reset()
	if text == "" {
		return nil
	}
	if s.conn == nil || s.provider == nil {
		return nil
	}
	s.segmentID = fmt.Sprintf("seg-%d", time.Now().UnixNano())
	payload, err := buildSmallestAITTSStreamMessage(s.provider, text)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, payload)
}

func (s *smallestaiTTSSynthesizeStream) Close() error {
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
	return closeSmallestAIWebsocket(s.conn)
}

func (s *smallestaiTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	if s.ctx != nil {
		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		default:
		}
	}
	return (&smallestaiTTSWebsocketChunkedStream{conn: s.conn, sampleRate: s.sampleRate, segmentID: s.segmentID}).Next()
}

func closeSmallestAIWebsocket(conn *websocket.Conn) error {
	if conn == nil {
		return nil
	}
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return conn.Close()
}

func smallestAITTSAudioFromWebsocketMessage(payload []byte, sampleRate int, segmentID string) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Audio string `json:"audio"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	switch message.Status {
	case "chunk":
		if message.Data.Audio == "" {
			return nil, false, nil
		}
		audio, err := base64.StdEncoding.DecodeString(message.Data.Audio)
		if err != nil {
			return nil, false, err
		}
		if len(audio) == 0 {
			return nil, false, nil
		}
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              bytes.Clone(audio),
				SampleRate:        uint32(sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(len(audio) / 2),
			},
			SegmentID: segmentID,
		}, false, nil
	case "complete":
		return nil, true, nil
	case "error":
		if message.Message == "" {
			message.Message = "unknown error"
		}
		return nil, false, fmt.Errorf("smallestai tts error: %s", message.Message)
	default:
		return nil, false, nil
	}
}

func defaultSmallestAIVoice(model string) string {
	if model == defaultSmallestAIModel {
		return defaultSmallestAIProVoice
	}
	return defaultSmallestAIStandardVoice
}
