package neuphonic

import (
	"bufio"
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
	defaultNeuphonicBaseURL    = "https://api.neuphonic.com"
	defaultNeuphonicVoice      = "8e9c4bc8-3979-48ab-8626-df53befc2090"
	defaultNeuphonicLangCode   = "en"
	defaultNeuphonicEncoding   = "pcm_linear"
	defaultNeuphonicSampleRate = 22050
)

type NeuphonicTTS struct {
	mu         sync.Mutex
	streams    map[*neuphonicTTSSynthesizeStream]struct{}
	apiKey     string
	baseURL    string
	voice      string
	langCode   string
	encoding   string
	sampleRate int
	speed      *float64
}

type NeuphonicTTSOption func(*NeuphonicTTS)

func WithNeuphonicTTSBaseURL(baseURL string) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithNeuphonicTTSVoice(voice string) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithNeuphonicTTSLangCode(langCode string) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		if langCode != "" {
			t.langCode = langCode
		}
	}
}

func WithNeuphonicTTSEncoding(encoding string) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		if encoding != "" {
			t.encoding = encoding
		}
	}
}

func WithNeuphonicTTSSampleRate(sampleRate int) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithNeuphonicTTSSpeed(speed float64) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		t.speed = &speed
	}
}

func NewNeuphonicTTS(apiKey string, voice string, opts ...NeuphonicTTSOption) *NeuphonicTTS {
	if apiKey == "" {
		apiKey = os.Getenv("NEUPHONIC_API_KEY")
	}
	defaultSpeed := 1.0
	provider := &NeuphonicTTS{
		streams:    make(map[*neuphonicTTSSynthesizeStream]struct{}),
		apiKey:     apiKey,
		baseURL:    defaultNeuphonicBaseURL,
		voice:      voice,
		langCode:   defaultNeuphonicLangCode,
		encoding:   defaultNeuphonicEncoding,
		sampleRate: defaultNeuphonicSampleRate,
		speed:      &defaultSpeed,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultNeuphonicVoice
	}
	return provider
}

func (t *NeuphonicTTS) Label() string { return "neuphonic.TTS" }
func (t *NeuphonicTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *NeuphonicTTS) SampleRate() int  { return t.sampleRate }
func (t *NeuphonicTTS) NumChannels() int { return 1 }
func (t *NeuphonicTTS) Model() string    { return "Octave" }
func (t *NeuphonicTTS) Provider() string { return "Neuphonic" }

func (t *NeuphonicTTS) UpdateOptions(opts ...NeuphonicTTSOption) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, opt := range opts {
		opt(t)
	}
}

func (t *NeuphonicTTS) Close() error {
	t.mu.Lock()
	streams := make([]*neuphonicTTSSynthesizeStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*neuphonicTTSSynthesizeStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *NeuphonicTTS) registerStream(stream *neuphonicTTSSynthesizeStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	if t.streams == nil {
		t.streams = make(map[*neuphonicTTSSynthesizeStream]struct{})
	}
	t.streams[stream] = struct{}{}
	stream.owner = t
	t.mu.Unlock()
}

func (t *NeuphonicTTS) unregisterStream(stream *neuphonicTTSSynthesizeStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	delete(t.streams, stream)
	t.mu.Unlock()
}

func (t *NeuphonicTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateNeuphonicAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	req, err := buildNeuphonicTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("neuphonic tts error: %s", string(respBody))
	}

	return &neuphonicTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildNeuphonicTTSRequest(ctx context.Context, t *NeuphonicTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":          text,
		"voice_id":      t.voice,
		"lang_code":     t.langCode,
		"encoding":      t.encoding,
		"sampling_rate": t.sampleRate,
		"speed":         optionalFloat(t.speed),
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/sse/speak/"+t.langCode, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", t.apiKey)
	return req, nil
}

func (t *NeuphonicTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateNeuphonicAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildNeuphonicTTSWebsocketURL(t).String(), buildNeuphonicTTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial neuphonic tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &neuphonicTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
		segmentID:  neuphonicTTSSegmentID(),
		events:     make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
	}
	stream.writeMessage = stream.writeWebsocketMessage
	stream.closeConn = stream.closeWebsocketConn
	t.registerStream(stream)
	go stream.readLoop()
	return stream, nil
}

func validateNeuphonicAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("neuphonic API key or JWT token is required, either as argument or set NEUPHONIC_API_KEY environment variable")
	}
	return nil
}

func buildNeuphonicTTSWebsocketURL(t *NeuphonicTTS) *url.URL {
	baseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	}
	wsURL, err := url.Parse(baseURL + "/speak/en")
	if err != nil {
		return &url.URL{Scheme: "wss", Host: strings.TrimPrefix(baseURL, "wss://"), Path: "/speak/en"}
	}
	query := wsURL.Query()
	if t.speed != nil {
		query.Set("speed", strconv.FormatFloat(*t.speed, 'f', -1, 64))
	}
	query.Set("lang_code", t.langCode)
	query.Set("sampling_rate", strconv.Itoa(t.sampleRate))
	query.Set("voice_id", t.voice)
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func buildNeuphonicTTSWebsocketHeaders(t *NeuphonicTTS) http.Header {
	headers := make(http.Header)
	headers.Set("x-api-key", t.apiKey)
	return headers
}

func buildNeuphonicTTSTextMessage(text string, contextID string) ([]byte, error) {
	return json.Marshal(map[string]string{
		"text":       text + "<STOP>",
		"context_id": contextID,
	})
}

type neuphonicTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	scanner    *bufio.Scanner
}

func (s *neuphonicTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp.Body)
	}
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		audio, err := neuphonicAudioFromSSEData(strings.TrimPrefix(line, "data: "))
		if err != nil {
			return nil, err
		}
		if len(audio) == 0 {
			continue
		}
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              audio,
				SampleRate:        uint32(s.sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(len(audio) / 2),
			},
		}, nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *neuphonicTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func neuphonicAudioFromSSEData(data string) ([]byte, error) {
	var parsed struct {
		StatusCode int `json:"status_code"`
		Data       struct {
			Audio string `json:"audio"`
		} `json:"data"`
		Errors interface{} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, err
	}
	if parsed.Errors != nil {
		return nil, fmt.Errorf("neuphonic tts error: %v", parsed.Errors)
	}
	if parsed.Data.Audio == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(parsed.Data.Audio)
}

func optionalFloat(value *float64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

type neuphonicTTSSynthesizeStream struct {
	owner      *NeuphonicTTS
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	sampleRate int
	segmentID  string
	events     chan *tts.SynthesizedAudio
	errCh      chan error
	mu         sync.Mutex
	closed     bool

	writeMessage func(int, []byte) error
	closeConn    func() error
}

func (s *neuphonicTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("neuphonic tts stream is closed")
	}
	message, err := buildNeuphonicTTSTextMessage(text, s.segmentID)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(websocket.TextMessage, message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *neuphonicTTSSynthesizeStream) Flush() error {
	return nil
}

func (s *neuphonicTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}
	err := s.closeConnection()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
	return err
}

func (s *neuphonicTTSSynthesizeStream) writeMessageData(messageType int, data []byte) error {
	if s.writeMessage != nil {
		return s.writeMessage(messageType, data)
	}
	return s.writeWebsocketMessage(messageType, data)
}

func (s *neuphonicTTSSynthesizeStream) writeWebsocketMessage(messageType int, data []byte) error {
	return s.conn.WriteMessage(messageType, data)
}

func (s *neuphonicTTSSynthesizeStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *neuphonicTTSSynthesizeStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *neuphonicTTSSynthesizeStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	_ = s.closeConnection()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
}

func (s *neuphonicTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *neuphonicTTSSynthesizeStream) readLoop() {
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
		audio, done, err := neuphonicAudioFromStreamMessage(payload, s.segmentID, s.sampleRate)
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

func neuphonicAudioFromStreamMessage(payload []byte, contextID string, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Type string `json:"type"`
		Data struct {
			Audio     string `json:"audio"`
			ContextID string `json:"context_id"`
			Stop      bool   `json:"stop"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.Type == "error" {
		return nil, false, fmt.Errorf("neuphonic tts stream error: %s", string(payload))
	}
	if message.Data.ContextID != "" && message.Data.ContextID != contextID {
		return nil, false, nil
	}
	if message.Data.Audio != "" {
		audio, err := base64.StdEncoding.DecodeString(message.Data.Audio)
		if err != nil {
			return nil, false, err
		}
		if len(audio) > 0 {
			return neuphonicTTSAudioFrame(audio, sampleRate), false, nil
		}
	}
	return nil, message.Data.Stop, nil
}

func neuphonicTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}

func neuphonicTTSSegmentID() string {
	return "segment-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
