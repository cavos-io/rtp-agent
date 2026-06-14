package rime

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
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/gorilla/websocket"
)

const (
	defaultRimeHTTPBaseURL = "https://users.rime.ai/v1/rime-tts"
	defaultRimeWSBaseURL   = "wss://users-ws.rime.ai"
	defaultRimeModel       = "arcana"
	defaultRimeArcanaVoice = "astra"
	defaultRimeMistVoice   = "cove"
	defaultRimeCodaVoice   = "lyra"
	defaultRimeLang        = "eng"
	defaultRimeSampleRate  = 22050
	defaultRimeSegment     = "bySentence"
)

type RimeTTS struct {
	apiKey          string
	baseURL         string
	model           string
	voice           string
	lang            string
	sampleRate      int
	timeScaleFactor *float64
	useWebsocket    bool
	segment         string
}

type RimeTTSOption func(*RimeTTS)

func WithRimeTTSBaseURL(baseURL string) RimeTTSOption {
	return func(t *RimeTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
			if strings.HasPrefix(baseURL, "ws://") || strings.HasPrefix(baseURL, "wss://") {
				t.useWebsocket = true
			}
		}
	}
}

func WithRimeTTSModel(model string) RimeTTSOption {
	return func(t *RimeTTS) {
		if model != "" {
			t.model = model
			if t.voice == "" {
				t.voice = defaultRimeVoice(model)
			}
		}
	}
}

func WithRimeTTSSampleRate(sampleRate int) RimeTTSOption {
	return func(t *RimeTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithRimeTTSLang(lang string) RimeTTSOption {
	return func(t *RimeTTS) {
		if lang != "" {
			t.lang = lang
		}
	}
}

func WithRimeTTSTimeScaleFactor(timeScaleFactor float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.timeScaleFactor = &timeScaleFactor
	}
}

func WithRimeTTSWebsocket(useWebsocket bool) RimeTTSOption {
	return func(t *RimeTTS) {
		t.useWebsocket = useWebsocket
	}
}

func WithRimeTTSSegment(segment string) RimeTTSOption {
	return func(t *RimeTTS) {
		if segment != "" {
			t.segment = segment
		}
	}
}

func NewRimeTTS(apiKey string, voice string, opts ...RimeTTSOption) *RimeTTS {
	if apiKey == "" {
		apiKey = os.Getenv("RIME_API_KEY")
	}
	provider := &RimeTTS{
		apiKey:     apiKey,
		baseURL:    defaultRimeHTTPBaseURL,
		model:      defaultRimeModel,
		lang:       defaultRimeLang,
		sampleRate: defaultRimeSampleRate,
		segment:    defaultRimeSegment,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.useWebsocket && provider.baseURL == defaultRimeHTTPBaseURL {
		provider.baseURL = defaultRimeWSBaseURL
	}
	if voice == "" {
		voice = defaultRimeVoice(provider.model)
	}
	provider.voice = voice
	return provider
}

func defaultRimeVoice(model string) string {
	switch {
	case model == "coda":
		return defaultRimeCodaVoice
	case strings.Contains(model, "mist"):
		return defaultRimeMistVoice
	default:
		return defaultRimeArcanaVoice
	}
}

func (t *RimeTTS) Label() string { return "rime.TTS" }
func (t *RimeTTS) Model() string { return t.model }
func (t *RimeTTS) Provider() string {
	return "Rime"
}

func (t *RimeTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: t.useWebsocket, AlignedTranscript: t.useWebsocket}
}
func (t *RimeTTS) SampleRate() int  { return t.sampleRate }
func (t *RimeTTS) NumChannels() int { return 1 }

func (t *RimeTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.useWebsocket {
		return nil, fmt.Errorf("rime tts one-shot synthesize requires websocket mode disabled")
	}
	if err := validateRimeAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	req, err := buildRimeTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("rime tts error: %s", string(respBody))
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "audio") {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("rime tts returned non-audio data: %s", string(respBody))
	}

	return &rimeTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildRimeTTSRequest(ctx context.Context, t *RimeTTS, text string) (*http.Request, error) {
	if err := validateRimeTimeScaleFactor(t); err != nil {
		return nil, err
	}
	reqBody := map[string]interface{}{
		"speaker":      t.voice,
		"text":         text,
		"modelId":      t.model,
		"lang":         t.lang,
		"samplingRate": t.sampleRate,
	}
	if t.timeScaleFactor != nil {
		reqBody["timeScaleFactor"] = *t.timeScaleFactor
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "audio/pcm")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

func (t *RimeTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if !t.useWebsocket {
		return nil, fmt.Errorf("rime tts streaming requires websocket mode enabled")
	}
	if err := validateRimeAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	if err := validateRimeTimeScaleFactor(t); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildRimeTTSWebsocketURL(t).String(), buildRimeTTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial rime tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &rimeTTSSynthesizeStream{
		conn:      conn,
		ctx:       streamCtx,
		cancel:    cancel,
		provider:  t,
		contextID: cavosmath.ShortUUID(""),
		events:    make(chan *tts.SynthesizedAudio, 100),
		errCh:     make(chan error, 1),
	}
	go stream.readLoop()
	return stream, nil
}

func validateRimeAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("rime API key is required, either as argument or set RIME_API_KEY environmental variable")
	}
	return nil
}

func validateRimeTimeScaleFactor(t *RimeTTS) error {
	if t.model == "mistv2" && t.timeScaleFactor != nil {
		return fmt.Errorf("time_scale_factor is not supported by the mistv2 model; use arcana, mistv3, or coda")
	}
	return nil
}

func buildRimeTTSWebsocketURL(t *RimeTTS) *url.URL {
	wsURL, err := url.Parse(strings.TrimRight(t.baseURL, "/") + "/ws3")
	if err != nil {
		wsURL = &url.URL{Scheme: "wss", Host: strings.TrimPrefix(t.baseURL, "wss://"), Path: "/ws3"}
	}
	query := wsURL.Query()
	query.Set("speaker", t.voice)
	query.Set("modelId", t.model)
	query.Set("audioFormat", "pcm")
	query.Set("samplingRate", strconv.Itoa(t.sampleRate))
	query.Set("segment", t.segment)
	if t.lang != "" {
		query.Set("lang", t.lang)
	}
	if t.timeScaleFactor != nil {
		query.Set("timeScaleFactor", strconv.FormatFloat(*t.timeScaleFactor, 'f', -1, 64))
	}
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func buildRimeTTSWebsocketHeaders(t *RimeTTS) http.Header {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+t.apiKey)
	return header
}

func buildRimeTTSTextMessage(contextID string, text string) ([]byte, error) {
	if !strings.HasSuffix(text, " ") {
		text += " "
	}
	return json.Marshal(map[string]interface{}{
		"text":      text,
		"contextId": contextID,
	})
}

func buildRimeTTSFlushMessage(contextID string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"operation": "flush",
		"contextId": contextID,
	})
}

type rimeTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *rimeTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *rimeTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

type rimeTTSSynthesizeStream struct {
	conn      *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	provider  *RimeTTS
	contextID string
	events    chan *tts.SynthesizedAudio
	errCh     chan error
	mu        sync.Mutex
	closed    bool
	started   bool
}

func (s *rimeTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("rime tts stream is closed")
	}
	message, err := buildRimeTTSTextMessage(s.contextID, text)
	if err != nil {
		return err
	}
	s.started = true
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *rimeTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("rime tts stream is closed")
	}
	if !s.started {
		return nil
	}
	message, err := buildRimeTTSFlushMessage(s.contextID)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *rimeTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteMessage(websocket.TextMessage, []byte(`{"operation":"eos"}`))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *rimeTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *rimeTTSSynthesizeStream) readLoop() {
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
		audio, done, transcript, err := rimeTTSAudioFromWebsocketMessage(payload, s.provider.sampleRate)
		if err != nil {
			s.errCh <- err
			return
		}
		if audio != nil {
			s.events <- audio
		}
		if transcript != "" {
			s.events <- &tts.SynthesizedAudio{DeltaText: transcript}
		}
		if done {
			return
		}
	}
}

func rimeTTSAudioFromWebsocketMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, string, error) {
	var message struct {
		Type           string `json:"type"`
		Data           string `json:"data"`
		Message        string `json:"message"`
		WordTimestamps struct {
			Words []string  `json:"words"`
			Start []float64 `json:"start"`
			End   []float64 `json:"end"`
		} `json:"word_timestamps"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, "", err
	}
	switch message.Type {
	case "chunk":
		if message.Data == "" {
			return nil, false, "", nil
		}
		audio, err := base64.StdEncoding.DecodeString(message.Data)
		if err != nil {
			return nil, false, "", err
		}
		if len(audio) == 0 {
			return nil, false, "", nil
		}
		return rimeTTSAudioFrame(audio, sampleRate), false, "", nil
	case "timestamps":
		if len(message.WordTimestamps.Words) == 0 {
			return nil, false, "", nil
		}
		return nil, false, strings.Join(message.WordTimestamps.Words, " ") + " ", nil
	case "done":
		return nil, true, "", nil
	case "error":
		if message.Message == "" {
			message.Message = string(payload)
		}
		return nil, false, "", fmt.Errorf("rime tts stream error: %s", message.Message)
	default:
		return nil, false, "", nil
	}
}

func rimeTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
