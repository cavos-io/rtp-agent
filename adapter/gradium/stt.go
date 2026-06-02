package gradium

import (
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
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultSTTModelEndpoint = "wss://api.gradium.ai/api/speech/asr"
	defaultSTTModelName     = "default"
	defaultSTTSampleRate    = 24000
	defaultSTTEncoding      = "pcm_s16le"
	defaultSTTBufferSeconds = 0.08
	defaultSTTVADThreshold  = 0.9
	defaultSTTVADBucket     = 2
	defaultSTTLanguage      = "en"
)

type GradiumSTT struct {
	apiKey            string
	modelEndpoint     string
	modelName         string
	sampleRate        int
	encoding          string
	bufferSizeSeconds float64
	vadThreshold      float64
	vadBucket         *int
	vadFlush          bool
	temperature       *float64
	language          string
}

type GradiumSTTOption func(*GradiumSTT)

func WithGradiumSTTModelEndpoint(endpoint string) GradiumSTTOption {
	return func(s *GradiumSTT) {
		if endpoint != "" {
			s.modelEndpoint = strings.TrimRight(endpoint, "/")
		}
	}
}

func WithGradiumSTTModelName(modelName string) GradiumSTTOption {
	return func(s *GradiumSTT) {
		if modelName != "" {
			s.modelName = modelName
		}
	}
}

func WithGradiumSTTLanguage(language string) GradiumSTTOption {
	return func(s *GradiumSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithGradiumSTTTemperature(temperature float64) GradiumSTTOption {
	return func(s *GradiumSTT) {
		s.temperature = &temperature
	}
}

func WithGradiumSTTVADBucket(bucket *int) GradiumSTTOption {
	return func(s *GradiumSTT) {
		s.vadBucket = bucket
	}
}

func WithGradiumSTTVADFlush(enabled bool) GradiumSTTOption {
	return func(s *GradiumSTT) {
		s.vadFlush = enabled
	}
}

func WithGradiumSTTBufferSizeSeconds(seconds float64) GradiumSTTOption {
	return func(s *GradiumSTT) {
		if seconds > 0 {
			s.bufferSizeSeconds = seconds
		}
	}
}

func NewGradiumSTT(apiKey string, opts ...GradiumSTTOption) *GradiumSTT {
	bucket := defaultSTTVADBucket
	provider := &GradiumSTT{
		apiKey:            apiKey,
		modelEndpoint:     defaultSTTModelEndpoint,
		modelName:         defaultSTTModelName,
		sampleRate:        defaultSTTSampleRate,
		encoding:          defaultSTTEncoding,
		bufferSizeSeconds: defaultSTTBufferSeconds,
		vadThreshold:      defaultSTTVADThreshold,
		vadBucket:         &bucket,
		vadFlush:          true,
		language:          defaultSTTLanguage,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *GradiumSTT) Label() string { return "gradium.STT" }
func (s *GradiumSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: false}
}

func (s *GradiumSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if language != "" {
		s.language = language
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, s.modelEndpoint, buildGradiumSTTHeaders(s))
	if err != nil {
		return nil, fmt.Errorf("failed to dial gradium stt websocket: %w", err)
	}
	if err := writeGradiumSTTMessage(conn, buildGradiumSTTSetup(s)); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &gradiumSTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state: &gradiumSTTMessageState{
			language:       s.language,
			vadBucket:      s.vadBucket,
			vadThreshold:   s.vadThreshold,
			delayInTokens:  6,
			frameSize:      1920,
			bufferedText:   nil,
			remainingSteps: nil,
		},
	}
	go stream.readLoop()
	return stream, nil
}

func (s *GradiumSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("not implemented")
}

func buildGradiumSTTHeaders(s *GradiumSTT) http.Header {
	headers := make(http.Header)
	headers.Set("x-api-key", s.apiKey)
	headers.Set("x-api-source", "livekit")
	return headers
}

func buildGradiumSTTSetup(s *GradiumSTT) map[string]any {
	jsonConfig := map[string]any{"language": s.language}
	if s.temperature != nil {
		jsonConfig["temp"] = *s.temperature
	}
	return map[string]any{
		"type":         "setup",
		"model_name":   s.modelName,
		"input_format": "pcm",
		"json_config":  jsonConfig,
	}
}

func buildGradiumSTTAudioMessage(audio []byte) map[string]any {
	return map[string]any{
		"type":  "audio",
		"audio": base64.StdEncoding.EncodeToString(audio),
	}
}

func buildGradiumSTTCloseMessage() map[string]any {
	return map[string]any{"terminate_session": true}
}

func writeGradiumSTTMessage(conn *websocket.Conn, message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

type gradiumSTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
	state  *gradiumSTTMessageState
}

func (s *gradiumSTTStream) readLoop() {
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
		events, err := processGradiumSTTMessage(s.state, message, 0)
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

func (s *gradiumSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return writeGradiumSTTMessage(s.conn, buildGradiumSTTAudioMessage(frame.Data))
}

func (s *gradiumSTTStream) Flush() error {
	return nil
}

func (s *gradiumSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = writeGradiumSTTMessage(s.conn, buildGradiumSTTCloseMessage())
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *gradiumSTTStream) Next() (*stt.SpeechEvent, error) {
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

type gradiumSTTMessageState struct {
	language       string
	speaking       bool
	bufferedText   []string
	vadBucket      *int
	vadThreshold   float64
	delayInTokens  int
	frameSize      int
	remainingSteps *int
}

func processGradiumSTTMessage(state *gradiumSTTMessageState, payload []byte, startTimeOffset float64) ([]*stt.SpeechEvent, error) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	msgType, _ := raw["type"].(string)
	switch msgType {
	case "text":
		text, _ := raw["text"].(string)
		if text == "" {
			return nil, nil
		}
		startS := float64Value(raw["start_s"])
		events := make([]*stt.SpeechEvent, 0, 2)
		if !state.speaking {
			state.speaking = true
			events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
		}
		state.bufferedText = append(state.bufferedText, text)
		events = append(events, &stt.SpeechEvent{
			Type: stt.SpeechEventInterimTranscript,
			Alternatives: []stt.SpeechData{
				{
					Text:      text,
					Language:  state.language,
					StartTime: startS + startTimeOffset,
				},
			},
		})
		return events, nil
	case "step":
		return processGradiumSTTStep(state, raw), nil
	case "ready":
		if delay, ok := intValue(raw["delay_in_tokens"]); ok {
			state.delayInTokens = delay
		}
		if frameSize, ok := intValue(raw["frame_size"]); ok {
			state.frameSize = frameSize
		}
		return nil, nil
	case "end_text":
		return nil, nil
	default:
		return nil, nil
	}
}

func processGradiumSTTStep(state *gradiumSTTMessageState, raw map[string]any) []*stt.SpeechEvent {
	if !state.speaking || state.vadBucket == nil {
		return nil
	}
	vad, ok := raw["vad"].([]any)
	if !ok || *state.vadBucket < 0 || *state.vadBucket >= len(vad) {
		return nil
	}
	bucket, ok := vad[*state.vadBucket].(map[string]any)
	if !ok {
		return nil
	}
	if float64Value(bucket["inactivity_prob"]) <= state.vadThreshold {
		state.remainingSteps = nil
		return nil
	}
	if state.remainingSteps == nil {
		delay := state.delayInTokens
		if delay == 0 {
			delay = 6
		}
		state.remainingSteps = &delay
		return nil
	}
	*state.remainingSteps--
	if *state.remainingSteps > 0 {
		return nil
	}
	state.speaking = false
	state.remainingSteps = nil
	text := strings.Join(state.bufferedText, " ")
	state.bufferedText = nil
	return []*stt.SpeechEvent{
		{
			Type: stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{
				{Text: text, Language: state.language},
			},
		},
		{Type: stt.SpeechEventEndOfSpeech},
	}
}

func float64Value(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

func intValue(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case json.Number:
		i, err := v.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}
