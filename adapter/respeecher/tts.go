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
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
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
func (t *RespeecherTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *RespeecherTTS) SampleRate() int  { return t.sampleRate }
func (t *RespeecherTTS) NumChannels() int { return 1 }

func (t *RespeecherTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
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
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildRespeecherTTSStreamURL(t), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to dial respeecher tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &respeecherTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		contextID:  respeecherTTSContextID(),
		sampleRate: t.sampleRate,
		voiceID:    t.voiceID,
		encoding:   t.encoding,
		params:     t.samplingParams,
	}, nil
}

func buildRespeecherTTSStreamURL(t *RespeecherTTS) string {
	baseURL := strings.TrimRight(t.baseURL, "/")
	switch {
	case strings.HasPrefix(baseURL, "https://"):
		baseURL = "wss://" + strings.TrimPrefix(baseURL, "https://")
	case strings.HasPrefix(baseURL, "http://"):
		baseURL = "ws://" + strings.TrimPrefix(baseURL, "http://")
	}
	u, err := url.Parse(baseURL + t.model + "/tts/websocket")
	if err != nil {
		return baseURL + t.model + "/tts/websocket"
	}
	q := u.Query()
	q.Set("api_key", t.apiKey)
	q.Set("source", "LiveKit-Plugin-Respeecher-Version")
	q.Set("version", respeecherAPIVersion)
	u.RawQuery = q.Encode()
	return u.String()
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

type respeecherTTSSynthesizeStream struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	contextID  string
	sampleRate int
	voiceID    string
	encoding   string
	params     map[string]any
	mu         sync.Mutex
	closed     bool
}

func (s *respeecherTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		text = " "
	}
	return writeRespeecherTTSStreamPayload(s.conn, s.buildPayload(text, true))
}

func (s *respeecherTTSSynthesizeStream) Flush() error {
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
	_ = writeRespeecherTTSStreamPayload(s.conn, s.buildPayload(" ", false))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *respeecherTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	default:
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
		audio, done, err := respeecherTTSAudioFromStreamMessage(payload, s.contextID, s.sampleRate)
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

func (s *respeecherTTSSynthesizeStream) buildPayload(text string, continueStream bool) map[string]any {
	voice := map[string]any{"id": s.voiceID}
	if len(s.params) > 0 {
		voice["sampling_params"] = s.params
	}
	return map[string]any{
		"context_id": s.contextID,
		"transcript": text,
		"voice":      voice,
		"continue":   continueStream,
		"output_format": map[string]any{
			"encoding":    s.encoding,
			"sample_rate": s.sampleRate,
		},
	}
}

func writeRespeecherTTSStreamPayload(conn *websocket.Conn, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

func respeecherTTSAudioFromStreamMessage(payload []byte, contextID string, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		ContextID string `json:"context_id"`
		Type      string `json:"type"`
		Data      string `json:"data"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.ContextID != "" && message.ContextID != contextID {
		return nil, false, nil
	}
	switch message.Type {
	case "chunk":
		audio, err := base64.StdEncoding.DecodeString(message.Data)
		if err != nil {
			return nil, false, err
		}
		return respeecherTTSAudioFrame(audio, sampleRate), false, nil
	case "done":
		return nil, true, nil
	case "error":
		if message.Error == "" {
			message.Error = "unknown respeecher tts error"
		}
		return nil, false, fmt.Errorf("respeecher tts error: %s", message.Error)
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

func respeecherTTSContextID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
