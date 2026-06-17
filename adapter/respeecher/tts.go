package respeecher

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
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/gorilla/websocket"
)

const (
	respeecherAPIVersion      = "1.5.15"
	defaultRespeecherBaseURL  = "https://api.respeecher.com/v1"
	defaultRespeecherModel    = "/public/tts/en-rt"
	defaultRespeecherEncoding = "pcm_s16le"
	defaultRespeecherRate     = 24000
)

var defaultRespeecherVoices = map[string]string{
	"/public/tts/en-rt": "samantha",
	"/public/tts/ua-rt": "olesia-conversation",
}

type RespeecherTTS struct {
	apiKey         string
	baseURL        string
	model          string
	voiceID        string
	encoding       string
	sampleRate     int
	samplingParams map[string]any
}

type RespeecherTTSOption func(*RespeecherTTS)

func WithRespeecherTTSBaseURL(baseURL string) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithRespeecherTTSModel(model string) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if model != "" {
			t.model = model
			if voice := defaultRespeecherVoices[model]; voice != "" {
				t.voiceID = voice
			}
		}
	}
}

func WithRespeecherTTSVoice(voiceID string) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if voiceID != "" {
			t.voiceID = voiceID
		}
	}
}

func WithRespeecherTTSSampleRate(sampleRate int) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithRespeecherTTSSamplingParams(params map[string]any) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		t.samplingParams = params
	}
}

func NewRespeecherTTS(apiKey string, voiceID string, opts ...RespeecherTTSOption) *RespeecherTTS {
	if apiKey == "" {
		apiKey = os.Getenv("RESPEECHER_API_KEY")
	}
	provider := &RespeecherTTS{
		apiKey:     apiKey,
		baseURL:    defaultRespeecherBaseURL,
		model:      defaultRespeecherModel,
		voiceID:    voiceID,
		encoding:   defaultRespeecherEncoding,
		sampleRate: defaultRespeecherRate,
	}
	if provider.voiceID == "" {
		provider.voiceID = defaultRespeecherVoices[provider.model]
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voiceID == "" {
		provider.voiceID = defaultRespeecherVoices[provider.model]
	}
	return provider
}

func (t *RespeecherTTS) Label() string { return "respeecher.TTS" }
func (t *RespeecherTTS) Model() string { return t.model }
func (t *RespeecherTTS) Provider() string {
	return "Respeecher"
}

func (t *RespeecherTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *RespeecherTTS) SampleRate() int  { return t.sampleRate }
func (t *RespeecherTTS) NumChannels() int { return 1 }

func (t *RespeecherTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateRespeecherAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	req, err := buildRespeecherTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("respeecher tts error: %s", string(respBody))
	}
	return &respeecherTTSChunkedStream{resp: resp, sampleRate: t.sampleRate}, nil
}

func buildRespeecherTTSRequest(ctx context.Context, t *RespeecherTTS, text string) (*http.Request, error) {
	voice := map[string]interface{}{"id": t.voiceID}
	if len(t.samplingParams) > 0 {
		voice["sampling_params"] = t.samplingParams
	}
	reqBody := map[string]interface{}{
		"transcript": text,
		"voice":      voice,
		"output_format": map[string]interface{}{
			"sample_rate": t.sampleRate,
			"encoding":    t.encoding,
		},
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+t.model+"/tts/bytes", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", t.apiKey)
	req.Header.Set("LiveKit-Plugin-Respeecher-Version", respeecherAPIVersion)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *RespeecherTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateRespeecherAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildRespeecherTTSWebsocketURL(t).String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to dial respeecher tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &respeecherTTSSynthesizeStream{
		conn:      conn,
		ctx:       streamCtx,
		cancel:    cancel,
		provider:  t,
		contextID: cavosmath.ShortUUID(""),
		events:    make(chan *tts.SynthesizedAudio, 100),
		errCh:     make(chan error, 1),
	}
	stream.writeMessage = stream.writeWebsocketMessage
	stream.closeConn = stream.closeWebsocketConn
	go stream.readLoop()
	return stream, nil
}

func validateRespeecherAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("RESPEECHER_API_KEY must be set")
	}
	return nil
}

type respeecherTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *respeecherTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *respeecherTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func buildRespeecherTTSWebsocketURL(t *RespeecherTTS) *url.URL {
	baseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	}
	wsURL, err := url.Parse(baseURL + t.model + "/tts/websocket")
	if err != nil {
		return &url.URL{Scheme: "wss", Host: strings.TrimPrefix(baseURL, "wss://"), Path: t.model + "/tts/websocket"}
	}
	query := wsURL.Query()
	query.Set("api_key", t.apiKey)
	query.Set("source", "LiveKit-Plugin-Respeecher-Version")
	query.Set("version", respeecherAPIVersion)
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func buildRespeecherTTSTextMessage(t *RespeecherTTS, contextID string, text string, continuation bool) ([]byte, error) {
	voice := map[string]interface{}{"id": t.voiceID}
	if len(t.samplingParams) > 0 {
		voice["sampling_params"] = t.samplingParams
	}
	return json.Marshal(map[string]interface{}{
		"context_id":    contextID,
		"transcript":    text,
		"voice":         voice,
		"continue":      continuation,
		"output_format": respeecherTTSOutputFormat(t),
	})
}

func buildRespeecherTTSEndMessage(t *RespeecherTTS, contextID string) ([]byte, error) {
	return buildRespeecherTTSTextMessage(t, contextID, " ", false)
}

func respeecherTTSOutputFormat(t *RespeecherTTS) map[string]interface{} {
	return map[string]interface{}{
		"sample_rate": t.sampleRate,
		"encoding":    t.encoding,
	}
}

type respeecherTTSSynthesizeStream struct {
	conn      *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	provider  *RespeecherTTS
	contextID string
	events    chan *tts.SynthesizedAudio
	errCh     chan error
	mu        sync.Mutex
	closed    bool

	writeMessage func([]byte) error
	closeConn    func() error
}

func (s *respeecherTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("respeecher tts stream is closed")
	}
	message, err := buildRespeecherTTSTextMessage(s.provider, s.contextID, text, true)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *respeecherTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("respeecher tts stream is closed")
	}
	message, err := buildRespeecherTTSEndMessage(s.provider, s.contextID)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *respeecherTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.closeConnection()
}

func (s *respeecherTTSSynthesizeStream) writeMessageData(message []byte) error {
	if s.writeMessage != nil {
		return s.writeMessage(message)
	}
	return s.writeWebsocketMessage(message)
}

func (s *respeecherTTSSynthesizeStream) writeWebsocketMessage(message []byte) error {
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *respeecherTTSSynthesizeStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *respeecherTTSSynthesizeStream) closeWebsocketConn() error {
	return s.conn.Close()
}

func (s *respeecherTTSSynthesizeStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	s.cancel()
	_ = s.closeConnection()
}

func (s *respeecherTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *respeecherTTSSynthesizeStream) readLoop() {
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
		audio, done, err := respeecherTTSAudioFromStreamMessage(payload, s.contextID, s.provider.sampleRate)
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

func respeecherTTSAudioFromStreamMessage(payload []byte, contextID string, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		ContextID string `json:"context_id"`
		Type      string `json:"type"`
		Data      string `json:"data"`
		Error     any    `json:"error"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.ContextID != "" && message.ContextID != contextID {
		return nil, false, nil
	}
	switch message.Type {
	case "chunk":
		if message.Data == "" {
			return nil, false, nil
		}
		audio, err := base64.StdEncoding.DecodeString(message.Data)
		if err != nil {
			return nil, false, err
		}
		if len(audio) == 0 {
			return nil, false, nil
		}
		return respeecherTTSAudioFrame(audio, sampleRate), false, nil
	case "done":
		return nil, true, nil
	case "error":
		return nil, false, fmt.Errorf("respeecher tts stream error: %v", message.Error)
	default:
		return nil, false, nil
	}
}

func respeecherTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
