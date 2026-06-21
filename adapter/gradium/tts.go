package gradium

import (
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
	defaultTTSModelEndpoint = "wss://api.gradium.ai/api/speech/tts"
	defaultTTSModelName     = "default"
	defaultTTSVoiceID       = "YTpq7expH9539ERJ"
	gradiumTTSSampleRate    = 48000
)

type GradiumTTS struct {
	apiKey          string
	modelEndpoint   string
	modelName       string
	voice           string
	voiceID         string
	pronunciationID string
	jsonConfig      map[string]any
}

type GradiumTTSOption func(*GradiumTTS)

func WithGradiumTTSModelEndpoint(endpoint string) GradiumTTSOption {
	return func(t *GradiumTTS) {
		if endpoint != "" {
			t.modelEndpoint = strings.TrimRight(endpoint, "/")
		}
	}
}

func WithGradiumTTSModelName(modelName string) GradiumTTSOption {
	return func(t *GradiumTTS) {
		if modelName != "" {
			t.modelName = modelName
		}
	}
}

func WithGradiumTTSVoiceID(voiceID string) GradiumTTSOption {
	return func(t *GradiumTTS) {
		t.voiceID = voiceID
	}
}

func WithGradiumTTSPronunciationID(pronunciationID string) GradiumTTSOption {
	return func(t *GradiumTTS) {
		t.pronunciationID = pronunciationID
	}
}

func WithGradiumTTSJSONConfig(jsonConfig map[string]any) GradiumTTSOption {
	return func(t *GradiumTTS) {
		t.jsonConfig = jsonConfig
	}
}

func NewGradiumTTS(apiKey string, voice string, opts ...GradiumTTSOption) *GradiumTTS {
	provider := &GradiumTTS{
		apiKey:        resolveGradiumAPIKey(apiKey),
		modelEndpoint: defaultTTSModelEndpoint,
		modelName:     defaultTTSModelName,
		voice:         voice,
		voiceID:       defaultTTSVoiceID,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *GradiumTTS) Label() string { return "gradium.TTS" }
func (t *GradiumTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *GradiumTTS) SampleRate() int  { return gradiumTTSSampleRate }
func (t *GradiumTTS) NumChannels() int { return 1 }
func (t *GradiumTTS) Model() string    { return "unknown" }
func (t *GradiumTTS) Provider() string { return "Gradium" }

func (t *GradiumTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateGradiumAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, t.modelEndpoint, buildGradiumTTSHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial gradium tts websocket: %w", err)
	}
	setup, err := json.Marshal(mustBuildGradiumTTSSetup(t))
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, setup); err != nil {
		conn.Close()
		return nil, err
	}
	if err := writeGradiumTTSMessage(conn, buildGradiumTTSTextMessage(text)); err != nil {
		conn.Close()
		return nil, err
	}
	if err := writeGradiumTTSMessage(conn, buildGradiumTTSEndMessage()); err != nil {
		conn.Close()
		return nil, err
	}
	return &gradiumTTSWebsocketChunkedStream{conn: conn, sampleRate: t.SampleRate()}, nil
}

func buildGradiumTTSHeaders(t *GradiumTTS) http.Header {
	headers := make(http.Header)
	headers.Set("x-api-key", t.apiKey)
	headers.Set("x-api-source", "livekit")
	return headers
}

func buildGradiumTTSSetup(t *GradiumTTS) (map[string]any, error) {
	setup := map[string]any{
		"type":          "setup",
		"model_name":    t.modelName,
		"output_format": "pcm",
	}
	if t.voice != "" {
		setup["voice"] = t.voice
	}
	if t.voiceID != "" {
		setup["voice_id"] = t.voiceID
	}
	if t.pronunciationID != "" {
		setup["pronunciation_id"] = t.pronunciationID
	}
	if t.jsonConfig != nil {
		payload, err := json.Marshal(t.jsonConfig)
		if err != nil {
			return nil, err
		}
		setup["json_config"] = string(payload)
	}
	return setup, nil
}

func mustBuildGradiumTTSSetup(t *GradiumTTS) map[string]any {
	setup, err := buildGradiumTTSSetup(t)
	if err != nil {
		return map[string]any{}
	}
	return setup
}

func buildGradiumTTSTextMessage(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func buildGradiumTTSEndMessage() map[string]any {
	return map[string]any{"type": "end_of_stream"}
}

func writeGradiumTTSMessage(conn *websocket.Conn, message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func (t *GradiumTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateGradiumAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, t.modelEndpoint, buildGradiumTTSHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial gradium tts websocket: %w", err)
	}
	if err := writeGradiumTTSMessage(conn, mustBuildGradiumTTSSetup(t)); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &gradiumTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.SampleRate(),
	}, nil
}

func validateGradiumAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("gradium API key is required; pass api_key or set GRADIUM_API_KEY environment variable")
	}
	return nil
}

type gradiumTTSWebsocketChunkedStream struct {
	conn       *websocket.Conn
	sampleRate int
	completed  bool
}

func (s *gradiumTTSWebsocketChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.completed {
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
		audio, done, err := gradiumTTSAudioFromMessage(payload, s.sampleRate)
		if err != nil {
			return nil, err
		}
		if audio != nil {
			if done {
				s.completed = true
			}
			return audio, nil
		}
		if done {
			s.completed = true
			return nil, io.EOF
		}
	}
}

func (s *gradiumTTSWebsocketChunkedStream) Close() error {
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

type gradiumTTSSynthesizeStream struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	closed     bool
	sampleRate int
	finalDone  bool
}

func (s *gradiumTTSSynthesizeStream) PushText(text string) error {
	return writeGradiumTTSMessage(s.conn, buildGradiumTTSTextMessage(text))
}

func (s *gradiumTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	s.finalDone = false
	s.mu.Unlock()
	return writeGradiumTTSMessage(s.conn, buildGradiumTTSEndMessage())
}

func (s *gradiumTTSSynthesizeStream) Close() error {
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

func (s *gradiumTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	default:
	}
	s.mu.Lock()
	if s.finalDone {
		s.mu.Unlock()
		return nil, io.EOF
	}
	conn := s.conn
	sampleRate := s.sampleRate
	s.mu.Unlock()

	audio, err := (&gradiumTTSWebsocketChunkedStream{conn: conn, sampleRate: sampleRate}).Next()
	if err != nil {
		return nil, err
	}
	if audio != nil && audio.IsFinal {
		s.mu.Lock()
		s.finalDone = true
		s.mu.Unlock()
	}
	return audio, nil
}

func gradiumTTSAudioFromMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Type  string `json:"type"`
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	switch message.Type {
	case "audio":
		audio, err := base64.StdEncoding.DecodeString(message.Audio)
		if err != nil {
			return nil, false, err
		}
		return gradiumTTSAudioFrame(audio, sampleRate), false, nil
	case "end_of_stream":
		return &tts.SynthesizedAudio{IsFinal: true}, true, nil
	default:
		return nil, false, nil
	}
}

func gradiumTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
