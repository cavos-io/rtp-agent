package rtzr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultAPIBase         = "https://openapi.vito.ai"
	defaultWSBase          = "wss://openapi.vito.ai"
	defaultModelName       = "sommers_ko"
	defaultLanguage        = "ko"
	defaultSampleRate      = 8000
	defaultEncoding        = "LINEAR16"
	defaultDomain          = "CALL"
	defaultEPDTime         = 0.8
	defaultNoiseThreshold  = 0.60
	defaultActiveThreshold = 0.80
)

type RtzrSTT struct {
	clientID        string
	clientSecret    string
	accessToken     string
	apiBase         string
	wsBase          string
	modelName       string
	language        string
	sampleRate      int
	encoding        string
	domain          string
	epdTime         float64
	noiseThreshold  float64
	activeThreshold float64
	usePunctuation  bool
	keywords        []string
}

type RtzrSTTOption func(*RtzrSTT)

func WithRtzrClientSecret(secret string) RtzrSTTOption {
	return func(s *RtzrSTT) {
		s.clientSecret = secret
	}
}

func WithRtzrAccessToken(token string) RtzrSTTOption {
	return func(s *RtzrSTT) {
		s.accessToken = token
	}
}

func WithRtzrAPIBase(apiBase string) RtzrSTTOption {
	return func(s *RtzrSTT) {
		if apiBase != "" {
			s.apiBase = strings.TrimRight(apiBase, "/")
		}
	}
}

func WithRtzrWSBase(wsBase string) RtzrSTTOption {
	return func(s *RtzrSTT) {
		if wsBase != "" {
			s.wsBase = strings.TrimRight(wsBase, "/")
		}
	}
}

func WithRtzrModel(model string) RtzrSTTOption {
	return func(s *RtzrSTT) {
		if model != "" {
			s.modelName = model
		}
	}
}

func WithRtzrLanguage(language string) RtzrSTTOption {
	return func(s *RtzrSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithRtzrSampleRate(sampleRate int) RtzrSTTOption {
	return func(s *RtzrSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithRtzrDomain(domain string) RtzrSTTOption {
	return func(s *RtzrSTT) {
		if domain != "" {
			s.domain = domain
		}
	}
}

func WithRtzrEPDTime(epdTime float64) RtzrSTTOption {
	return func(s *RtzrSTT) {
		if epdTime > 0 {
			s.epdTime = epdTime
		}
	}
}

func WithRtzrNoiseThreshold(noiseThreshold float64) RtzrSTTOption {
	return func(s *RtzrSTT) {
		if noiseThreshold > 0 {
			s.noiseThreshold = noiseThreshold
		}
	}
}

func WithRtzrActiveThreshold(activeThreshold float64) RtzrSTTOption {
	return func(s *RtzrSTT) {
		if activeThreshold > 0 {
			s.activeThreshold = activeThreshold
		}
	}
}

func WithRtzrUsePunctuation(usePunctuation bool) RtzrSTTOption {
	return func(s *RtzrSTT) {
		s.usePunctuation = usePunctuation
	}
}

func WithRtzrKeywords(keywords []string) RtzrSTTOption {
	return func(s *RtzrSTT) {
		s.keywords = keywords
	}
}

func NewRtzrSTT(clientID string, opts ...RtzrSTTOption) *RtzrSTT {
	provider := &RtzrSTT{
		clientID:        clientID,
		apiBase:         defaultAPIBase,
		wsBase:          defaultWSBase,
		modelName:       defaultModelName,
		language:        defaultLanguage,
		sampleRate:      defaultSampleRate,
		encoding:        defaultEncoding,
		domain:          defaultDomain,
		epdTime:         defaultEPDTime,
		noiseThreshold:  defaultNoiseThreshold,
		activeThreshold: defaultActiveThreshold,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *RtzrSTT) Label() string { return "rtzr.STT" }
func (s *RtzrSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, AlignedTranscript: "chunk", OfflineRecognize: false}
}

func (s *RtzrSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if language != "" {
		s.language = language
	}
	token, err := s.token(ctx)
	if err != nil {
		return nil, err
	}
	header := make(http.Header)
	header.Set("Authorization", "bearer "+token)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildRtzrStreamURL(s, buildRtzrConfig(s)), header)
	if err != nil {
		return nil, fmt.Errorf("failed to dial rtzr websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &rtzrStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state:  &rtzrTranscriptState{language: s.language},
	}
	go stream.readLoop()
	return stream, nil
}

func (s *RtzrSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("single-shot recognition is not supported; use stream")
}

func (s *RtzrSTT) token(ctx context.Context) (string, error) {
	if s.accessToken != "" {
		return s.accessToken, nil
	}
	if s.clientID == "" || s.clientSecret == "" {
		return "", fmt.Errorf("rtzr client id and client secret are required for streaming auth")
	}
	req, err := buildRtzrAuthRequest(ctx, s)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("rtzr authenticate error: %s", string(respBody))
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("rtzr authenticate returned empty access token")
	}
	s.accessToken = result.AccessToken
	return s.accessToken, nil
}

func buildRtzrAuthRequest(ctx context.Context, s *RtzrSTT) (*http.Request, error) {
	values := url.Values{}
	values.Set("client_id", s.clientID)
	values.Set("client_secret", s.clientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.apiBase, "/")+"/v1/authenticate", strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}

func buildRtzrConfig(s *RtzrSTT) map[string]string {
	config := map[string]string{
		"model_name":       s.modelName,
		"domain":           s.domain,
		"sample_rate":      strconv.Itoa(s.sampleRate),
		"encoding":         s.encoding,
		"epd_time":         strconv.FormatFloat(s.epdTime, 'f', -1, 64),
		"noise_threshold":  strconv.FormatFloat(s.noiseThreshold, 'f', -1, 64),
		"active_threshold": strconv.FormatFloat(s.activeThreshold, 'f', -1, 64),
		"use_punctuation":  strconv.FormatBool(s.usePunctuation),
	}
	if len(s.keywords) > 0 {
		config["keywords"] = strings.Join(s.keywords, ",")
	}
	return config
}

func buildRtzrStreamURL(s *RtzrSTT, config map[string]string) string {
	u, err := url.Parse(strings.TrimRight(s.wsBase, "/") + "/v1/transcribe:streaming")
	if err != nil {
		return ""
	}
	query := u.Query()
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		query.Set(key, config[key])
	}
	u.RawQuery = query.Encode()
	return u.String()
}

type rtzrStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
	state  *rtzrTranscriptState
}

func (s *rtzrStream) readLoop() {
	defer close(s.events)
	for {
		msgType, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var payload rtzrTranscriptPayload
		if err := json.Unmarshal(message, &payload); err != nil {
			continue
		}
		events, err := processRtzrTranscriptEvent(s.state, payload, 0)
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

func (s *rtzrStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, frame.Data)
}

func (s *rtzrStream) Flush() error {
	return s.conn.WriteMessage(websocket.TextMessage, []byte("EOS"))
}

func (s *rtzrStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteMessage(websocket.TextMessage, []byte("EOS"))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *rtzrStream) Next() (*stt.SpeechEvent, error) {
	select {
	case event, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return event, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

type rtzrTranscriptState struct {
	language string
	inSpeech bool
}

type rtzrTranscriptPayload struct {
	Type         string            `json:"type"`
	Message      string            `json:"message"`
	Error        string            `json:"error"`
	StartAt      float64           `json:"start_at"`
	Duration     float64           `json:"duration"`
	Final        bool              `json:"final"`
	Alternatives []rtzrAlternative `json:"alternatives"`
	Words        []rtzrWord        `json:"words"`
}

type rtzrAlternative struct {
	Text string `json:"text"`
}

type rtzrWord struct {
	Text     string  `json:"text"`
	StartAt  float64 `json:"start_at"`
	Duration float64 `json:"duration"`
}

func processRtzrTranscriptEvent(state *rtzrTranscriptState, payload rtzrTranscriptPayload, startTimeOffset float64) ([]*stt.SpeechEvent, error) {
	if payload.Error != "" {
		return nil, fmt.Errorf("rtzr server error: %s", payload.Error)
	}
	if payload.Type == "error" && payload.Message != "" {
		return nil, fmt.Errorf("rtzr server error: %s", payload.Message)
	}
	if len(payload.Alternatives) == 0 || payload.Alternatives[0].Text == "" {
		return nil, nil
	}

	text := payload.Alternatives[0].Text
	var events []*stt.SpeechEvent
	if !state.inSpeech {
		state.inSpeech = true
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
	}

	eventType := stt.SpeechEventInterimTranscript
	if payload.Final {
		eventType = stt.SpeechEventFinalTranscript
	}
	events = append(events, &stt.SpeechEvent{
		Type: eventType,
		Alternatives: []stt.SpeechData{
			{
				Text:      text,
				Language:  state.language,
				StartTime: payload.StartAt/1000 + startTimeOffset,
				EndTime:   (payload.StartAt+payload.Duration)/1000 + startTimeOffset,
				Words:     rtzrTimedStrings(payload.Words, startTimeOffset),
			},
		},
	})
	if payload.Final {
		state.inSpeech = false
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
	}
	return events, nil
}

func rtzrTimedStrings(words []rtzrWord, startTimeOffset float64) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}
	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:      word.Text,
			StartTime: word.StartAt/1000 + startTimeOffset,
			EndTime:   (word.StartAt+word.Duration)/1000 + startTimeOffset,
		})
	}
	return timed
}
