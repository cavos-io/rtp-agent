package murf

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultMurfBaseURL    = "https://global.api.murf.ai"
	defaultMurfModel      = "FALCON"
	defaultMurfVoice      = "en-US-matthew"
	defaultMurfStyle      = "Conversation"
	defaultMurfEncoding   = "pcm"
	defaultMurfSampleRate = 24000
	defaultMurfMinBuffer  = 3
	defaultMurfMaxDelayMS = 0
)

type MurfTTS struct {
	apiKey     string
	baseURL    string
	model      string
	locale     string
	voice      string
	style      string
	speed      *int
	pitch      *int
	sampleRate int
	encoding   string
}

type MurfTTSOption func(*MurfTTS)

func WithMurfTTSBaseURL(baseURL string) MurfTTSOption {
	return func(t *MurfTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithMurfTTSModel(model string) MurfTTSOption {
	return func(t *MurfTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithMurfTTSLocale(locale string) MurfTTSOption {
	return func(t *MurfTTS) {
		t.locale = locale
	}
}

func WithMurfTTSVoice(voice string) MurfTTSOption {
	return func(t *MurfTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithMurfTTSStyle(style string) MurfTTSOption {
	return func(t *MurfTTS) {
		t.style = style
	}
}

func WithMurfTTSSpeed(speed int) MurfTTSOption {
	return func(t *MurfTTS) {
		t.speed = &speed
	}
}

func WithMurfTTSPitch(pitch int) MurfTTSOption {
	return func(t *MurfTTS) {
		t.pitch = &pitch
	}
}

func WithMurfTTSSampleRate(sampleRate int) MurfTTSOption {
	return func(t *MurfTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithMurfTTSEncoding(encoding string) MurfTTSOption {
	return func(t *MurfTTS) {
		if encoding != "" {
			t.encoding = encoding
		}
	}
}

func NewMurfTTS(apiKey string, voice string, opts ...MurfTTSOption) *MurfTTS {
	if apiKey == "" {
		apiKey = os.Getenv("MURF_API_KEY")
	}
	provider := &MurfTTS{
		apiKey:     apiKey,
		baseURL:    defaultMurfBaseURL,
		model:      defaultMurfModel,
		voice:      voice,
		style:      defaultMurfStyle,
		sampleRate: defaultMurfSampleRate,
		encoding:   defaultMurfEncoding,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultMurfVoice
	}
	return provider
}

func (t *MurfTTS) Label() string { return "murf.TTS" }
func (t *MurfTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *MurfTTS) SampleRate() int  { return t.sampleRate }
func (t *MurfTTS) NumChannels() int { return 1 }
func (t *MurfTTS) Model() string    { return t.model }
func (t *MurfTTS) Provider() string { return "Murf" }

func (t *MurfTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateMurfAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	req, err := buildMurfTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("murf tts error: %s", string(respBody))
	}
	return &murfTTSChunkedStream{resp: resp, sampleRate: t.sampleRate}, nil
}

func buildMurfTTSRequest(ctx context.Context, t *MurfTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":              text,
		"model":             t.model,
		"multiNativeLocale": nil,
		"voice_id":          t.voice,
		"style":             t.style,
		"rate":              nil,
		"pitch":             nil,
		"format":            t.encoding,
		"sample_rate":       t.sampleRate,
	}
	if t.locale != "" {
		reqBody["multiNativeLocale"] = t.locale
	}
	if t.speed != nil {
		reqBody["rate"] = *t.speed
	}
	if t.pitch != nil {
		reqBody["pitch"] = *t.pitch
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/v1/speech/stream", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("api-key", t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *MurfTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateMurfAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildMurfTTSWebsocketURL(t).String(), buildMurfTTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial murf tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &murfTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		provider:   t,
		contextID:  murfTTSContextID(),
		sampleRate: t.sampleRate,
		events:     make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
	}
	go stream.readLoop()
	return stream, nil
}

func validateMurfAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("murf api key required: MURF_API_KEY must be set")
	}
	return nil
}

func buildMurfTTSWebsocketURL(t *MurfTTS) *url.URL {
	baseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	}
	wsURL, err := url.Parse(baseURL + "/v1/speech/stream-input")
	if err != nil {
		return &url.URL{Scheme: "wss", Host: strings.TrimPrefix(baseURL, "wss://"), Path: "/v1/speech/stream-input"}
	}
	query := wsURL.Query()
	query.Set("sample_rate", strconv.Itoa(t.sampleRate))
	query.Set("format", t.encoding)
	query.Set("model", t.model)
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func buildMurfTTSWebsocketHeaders(t *MurfTTS) http.Header {
	headers := make(http.Header)
	headers.Set("api-key", t.apiKey)
	return headers
}

func buildMurfTTSTextMessage(t *MurfTTS, text string, contextID string) ([]byte, error) {
	packet := murfTTSWebsocketPacket(t)
	packet["context_id"] = contextID
	packet["text"] = text + " "
	return json.Marshal(packet)
}

func buildMurfTTSEndMessage(t *MurfTTS, contextID string) ([]byte, error) {
	packet := murfTTSWebsocketPacket(t)
	packet["context_id"] = contextID
	packet["end"] = true
	return json.Marshal(packet)
}

func murfTTSWebsocketPacket(t *MurfTTS) map[string]interface{} {
	voiceConfig := map[string]interface{}{}
	if t.voice != "" {
		voiceConfig["voice_id"] = t.voice
	}
	if t.style != "" {
		voiceConfig["style"] = t.style
	}
	if t.speed != nil {
		voiceConfig["rate"] = *t.speed
	}
	if t.pitch != nil {
		voiceConfig["pitch"] = *t.pitch
	}
	if t.locale != "" {
		voiceConfig["multi_native_locale"] = t.locale
	}
	return map[string]interface{}{
		"voice_config":           voiceConfig,
		"min_buffer_size":        defaultMurfMinBuffer,
		"max_buffer_delay_in_ms": defaultMurfMaxDelayMS,
	}
}

type murfTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *murfTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
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

func (s *murfTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

type murfTTSSynthesizeStream struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	provider   *MurfTTS
	contextID  string
	sampleRate int
	events     chan *tts.SynthesizedAudio
	errCh      chan error
	mu         sync.Mutex
	closed     bool
}

func (s *murfTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	message, err := buildMurfTTSTextMessage(s.provider, text, s.contextID)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *murfTTSSynthesizeStream) Flush() error {
	message, err := buildMurfTTSEndMessage(s.provider, s.contextID)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *murfTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *murfTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *murfTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := murfAudioFromStreamMessage(payload, s.sampleRate)
		if err != nil {
			s.errCh <- err
			return
		}
		if audio != nil {
			s.events <- audio
		}
		if done {
			return
		}
	}
}

func murfAudioFromStreamMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Audio string `json:"audio"`
		Final bool   `json:"final"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.Audio != "" {
		audio, err := base64.StdEncoding.DecodeString(message.Audio)
		if err != nil {
			return nil, false, err
		}
		if len(audio) > 0 {
			return murfTTSAudioFrame(audio, sampleRate), false, nil
		}
	}
	return nil, message.Final, nil
}

func murfTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}

func murfTTSContextID() string {
	return "context-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
