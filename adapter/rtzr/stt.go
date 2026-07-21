package rtzr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
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
	defaultChunkMS         = 100
	recvCompletionTimeout  = 5 * time.Second
	rtzrClientIDEnv        = "RTZR_CLIENT_ID"
	rtzrClientSecretEnv    = "RTZR_CLIENT_SECRET"
)

type STT struct {
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
	httpClient      rtzrHTTPDoer
	dialWebsocket   rtzrWebsocketDialer
}

type STTOption func(*STT)

type rtzrHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type rtzrWebsocketDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

func WithRtzrClientSecret(secret string) STTOption {
	return func(s *STT) {
		s.clientSecret = secret
	}
}

func WithRtzrAccessToken(token string) STTOption {
	return func(s *STT) {
		s.accessToken = token
	}
}

func WithRtzrAPIBase(apiBase string) STTOption {
	return func(s *STT) {
		if apiBase != "" {
			s.apiBase = strings.TrimRight(apiBase, "/")
		}
	}
}

func WithRtzrWSBase(wsBase string) STTOption {
	return func(s *STT) {
		if wsBase != "" {
			s.wsBase = strings.TrimRight(wsBase, "/")
		}
	}
}

func WithRtzrModel(model string) STTOption {
	return func(s *STT) {
		if model != "" {
			s.modelName = model
		}
	}
}

func WithRtzrLanguage(language string) STTOption {
	return func(s *STT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithRtzrSampleRate(sampleRate int) STTOption {
	return func(s *STT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithRtzrDomain(domain string) STTOption {
	return func(s *STT) {
		if domain != "" {
			s.domain = domain
		}
	}
}

func WithRtzrEPDTime(epdTime float64) STTOption {
	return func(s *STT) {
		if epdTime > 0 {
			s.epdTime = epdTime
		}
	}
}

func WithRtzrNoiseThreshold(noiseThreshold float64) STTOption {
	return func(s *STT) {
		if noiseThreshold > 0 {
			s.noiseThreshold = noiseThreshold
		}
	}
}

func WithRtzrActiveThreshold(activeThreshold float64) STTOption {
	return func(s *STT) {
		if activeThreshold > 0 {
			s.activeThreshold = activeThreshold
		}
	}
}

func WithRtzrUsePunctuation(usePunctuation bool) STTOption {
	return func(s *STT) {
		s.usePunctuation = usePunctuation
	}
}

func WithRtzrKeywords(keywords []string) STTOption {
	return func(s *STT) {
		s.keywords = keywords
	}
}

func withRtzrHTTPClient(client rtzrHTTPDoer) STTOption {
	return func(s *STT) {
		if client != nil {
			s.httpClient = client
		}
	}
}

func withRtzrWebsocketDialer(dialer rtzrWebsocketDialer) STTOption {
	return func(s *STT) {
		if dialer != nil {
			s.dialWebsocket = dialer
		}
	}
}

func NewSTT(clientID string, opts ...STTOption) *STT {
	if clientID == "" {
		clientID = os.Getenv(rtzrClientIDEnv)
	}
	provider := &STT{
		clientID:        clientID,
		clientSecret:    os.Getenv(rtzrClientSecretEnv),
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
		httpClient:      http.DefaultClient,
		dialWebsocket:   defaultRtzrWebsocketDialer,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *STT) Label() string { return "rtzr.STT" }
func (s *STT) Model() string { return s.modelName }
func (s *STT) Provider() string {
	return "RTZR"
}

func (s *STT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return defaultSampleRate
	}
	return uint32(s.sampleRate)
}

func (s *STT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, AlignedTranscript: "chunk", OfflineRecognize: false}
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &rtzrStream{
		events:       make(chan *stt.SpeechEvent, 100),
		errCh:        make(chan error, 1),
		ctx:          streamCtx,
		cancel:       cancel,
		provider:     s,
		state:        &rtzrTranscriptState{language: s.language},
		audioBStream: newRtzrAudioByteStream(s.sampleRate),
	}
	return stream, nil
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("single-shot recognition is not supported; use stream")
}

func (s *STT) token(ctx context.Context) (string, error) {
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
	resp, err := s.httpClient.Do(req)
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

func defaultRtzrWebsocketDialer(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
}

func buildRtzrAuthRequest(ctx context.Context, s *STT) (*http.Request, error) {
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

func buildRtzrConfig(s *STT) map[string]string {
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

func buildRtzrStreamURL(s *STT, config map[string]string) string {
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
	conn            *websocket.Conn
	readDone        map[*websocket.Conn]chan struct{}
	endingSegments  map[*websocket.Conn]bool
	events          chan *stt.SpeechEvent
	errCh           chan error
	mu              sync.Mutex
	closed          bool
	startTimeOffset float64
	startTime       float64

	ctx      context.Context
	cancel   context.CancelFunc
	provider *STT
	state    *rtzrTranscriptState

	audioBStream *audio.AudioByteStream
}

func (s *rtzrStream) readLoop(conn *websocket.Conn) {
	defer func() {
		s.mu.Lock()
		segmentEnding := s.endingSegments != nil && s.endingSegments[conn]
		if segmentEnding {
			delete(s.endingSegments, conn)
		}
		var done chan struct{}
		if s.readDone != nil {
			done = s.readDone[conn]
			delete(s.readDone, conn)
		}
		if s.conn == conn {
			s.conn = nil
		}
		closed := s.closed
		s.mu.Unlock()
		if done != nil {
			close(done)
		}
		if closed || !segmentEnding {
			close(s.events)
		}
	}()
	for {
		msgType, message, err := conn.ReadMessage()
		if err != nil {
			var closeErr *websocket.CloseError
			if !errors.As(err, &closeErr) && err != io.EOF {
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
		events, err := processRtzrTranscriptEvent(s.state, payload, s.currentStartTimeOffset())
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.audioBStream == nil {
		s.audioBStream = newRtzrAudioByteStream(defaultSampleRate)
	}
	return s.writeAudioFramesLocked(s.audioBStream.Write(frame.Data))
}

func (s *rtzrStream) Flush() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	if s.audioBStream != nil {
		frames := s.audioBStream.Flush()
		if err := s.writeAudioFramesLocked(frames); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	if s.conn == nil {
		s.mu.Unlock()
		return nil
	}
	conn := s.conn
	var done <-chan struct{}
	if s.readDone != nil {
		done = s.readDone[conn]
	}
	if s.endingSegments == nil {
		s.endingSegments = make(map[*websocket.Conn]bool)
	}
	s.endingSegments[conn] = true
	err := conn.WriteMessage(websocket.TextMessage, []byte("EOS"))
	s.mu.Unlock()
	if err != nil {
		_ = s.Close()
		return err
	}
	s.awaitSegmentCompletion(conn, done)
	return nil
}

func (s *rtzrStream) writeAudioFramesLocked(frames []*model.AudioFrame) error {
	if len(frames) == 0 {
		return nil
	}
	if err := s.ensureConnectedLocked(); err != nil {
		return err
	}
	for _, frame := range frames {
		if frame == nil || len(frame.Data) == 0 {
			continue
		}
		if err := s.conn.WriteMessage(websocket.BinaryMessage, frame.Data); err != nil {
			_ = s.closeLocked()
			return err
		}
	}
	return nil
}

func (s *rtzrStream) ensureConnectedLocked() error {
	if s.conn != nil {
		return nil
	}
	token, err := s.provider.token(s.ctx)
	if err != nil {
		return err
	}
	header := make(http.Header)
	header.Set("Authorization", "bearer "+token)
	conn, _, err := s.provider.dialWebsocket(s.ctx, buildRtzrStreamURL(s.provider, buildRtzrConfig(s.provider)), header)
	if err != nil {
		return fmt.Errorf("failed to dial rtzr websocket: %w", err)
	}
	done := make(chan struct{})
	if s.readDone == nil {
		s.readDone = make(map[*websocket.Conn]chan struct{})
	}
	s.conn = conn
	s.readDone[conn] = done
	go s.readLoop(conn)
	return nil
}

func (s *rtzrStream) awaitSegmentCompletion(conn *websocket.Conn, done <-chan struct{}) {
	if done != nil {
		select {
		case <-done:
		case <-time.After(recvCompletionTimeout):
			_ = conn.Close()
			<-done
		}
	}
	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
	}
	s.mu.Unlock()
}

func (s *rtzrStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTimeOffset
}

func (s *rtzrStream) SetStartTimeOffset(offset float64) {
	if offset < 0 {
		panic("start_time_offset must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startTimeOffset = offset
}

func (s *rtzrStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTime
}

func (s *rtzrStream) SetStartTime(startTime float64) {
	if startTime < 0 {
		panic("start_time must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startTime = startTime
}

func (s *rtzrStream) currentStartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTimeOffset
}

func newRtzrAudioByteStream(sampleRate int) *audio.AudioByteStream {
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}
	samplesPerChannel := sampleRate / (1000 / defaultChunkMS)
	return audio.NewAudioByteStream(uint32(sampleRate), 1, uint32(samplesPerChannel))
}

func (s *rtzrStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

func (s *rtzrStream) closeLocked() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if s.conn == nil {
		return nil
	}
	_ = s.conn.WriteMessage(websocket.TextMessage, []byte("EOS"))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *rtzrStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *rtzrStream) Next() (*stt.SpeechEvent, error) {
	if s.isClosed() {
		return nil, io.EOF
	}
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
		if s.isClosed() {
			return nil, io.EOF
		}
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
				Text:       text,
				Language:   state.language,
				Confidence: stt.DefaultTranscriptConfidence(text),
				StartTime:  payload.StartAt/1000 + startTimeOffset,
				EndTime:    (payload.StartAt+payload.Duration)/1000 + startTimeOffset,
				Words:      rtzrTimedStrings(payload.Words, startTimeOffset),
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
			Text:            word.Text,
			StartTime:       word.StartAt/1000 + startTimeOffset,
			EndTime:         (word.StartAt+word.Duration)/1000 + startTimeOffset,
			StartTimeOffset: startTimeOffset,
		})
	}
	return timed
}

// Deprecated: use STT.
type RtzrSTT = STT

// Deprecated: use STTOption.
type RtzrSTTOption = STTOption

// Deprecated: use NewSTT.
func NewRtzrSTT(clientID string, opts ...STTOption) *STT {
	return NewSTT(clientID, opts...)
}
