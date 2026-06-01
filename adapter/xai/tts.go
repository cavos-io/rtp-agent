package xai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/gorilla/websocket"
)

const (
	defaultXaiTTSWebsocketURL = "wss://api.x.ai/v1/tts"
	defaultXaiTTSVoice        = "ara"
	defaultXaiTTSLanguage     = "auto"
	xaiTTSSampleRate          = 24000
	xaiTTSNumChannels         = 1
)

type XaiTTS struct {
	apiKey       string
	websocketURL string
	voice        string
	language     string
}

type XaiTTSOption func(*XaiTTS)

func WithXaiTTSWebsocketURL(websocketURL string) XaiTTSOption {
	return func(t *XaiTTS) {
		if websocketURL != "" {
			t.websocketURL = websocketURL
		}
	}
}

func WithXaiTTSLanguage(language string) XaiTTSOption {
	return func(t *XaiTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func NewXaiTTS(apiKey string, voice string, opts ...XaiTTSOption) *XaiTTS {
	provider := &XaiTTS{
		apiKey:       apiKey,
		websocketURL: defaultXaiTTSWebsocketURL,
		voice:        voice,
		language:     defaultXaiTTSLanguage,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultXaiTTSVoice
	}
	return provider
}

func (t *XaiTTS) Label() string { return "xai.TTS" }
func (t *XaiTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *XaiTTS) SampleRate() int  { return xaiTTSSampleRate }
func (t *XaiTTS) NumChannels() int { return xaiTTSNumChannels }

func (t *XaiTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildXaiTTSStreamURL(t), buildXaiTTSHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial xai tts websocket: %w", err)
	}
	if err := writeXaiTTSMessage(conn, buildXaiTTSTextDeltaMessage(text)); err != nil {
		conn.Close()
		return nil, err
	}
	if err := writeXaiTTSMessage(conn, buildXaiTTSTextDoneMessage()); err != nil {
		conn.Close()
		return nil, err
	}
	return &xaiTTSWebsocketChunkedStream{conn: conn}, nil
}

func (t *XaiTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildXaiTTSStreamURL(t), buildXaiTTSHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial xai tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &xaiTTSSynthesizeStream{
		conn:   conn,
		ctx:    streamCtx,
		cancel: cancel,
	}, nil
}

func buildXaiTTSStreamURL(t *XaiTTS) string {
	u, _ := url.Parse(t.websocketURL)
	q := u.Query()
	q.Set("voice", t.voice)
	q.Set("language", t.language)
	q.Set("codec", "pcm")
	q.Set("sample_rate", strconv.Itoa(xaiTTSSampleRate))
	u.RawQuery = q.Encode()
	return u.String()
}

func buildXaiTTSHeaders(t *XaiTTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+t.apiKey)
	return headers
}

func buildXaiTTSTextDeltaMessage(text string) map[string]any {
	return map[string]any{"type": "text.delta", "delta": text}
}

func buildXaiTTSTextDoneMessage() map[string]any {
	return map[string]any{"type": "text.done"}
}

func writeXaiTTSMessage(conn *websocket.Conn, message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

type xaiTTSWebsocketChunkedStream struct {
	conn *websocket.Conn
}

func (s *xaiTTSWebsocketChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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
		audio, done, err := xaiTTSAudioFromMessage(payload)
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

func (s *xaiTTSWebsocketChunkedStream) Close() error {
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

type xaiTTSSynthesizeStream struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
}

func (s *xaiTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	return writeXaiTTSMessage(s.conn, buildXaiTTSTextDeltaMessage(text))
}

func (s *xaiTTSSynthesizeStream) Flush() error {
	return writeXaiTTSMessage(s.conn, buildXaiTTSTextDoneMessage())
}

func (s *xaiTTSSynthesizeStream) Close() error {
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

func (s *xaiTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	default:
	}
	return (&xaiTTSWebsocketChunkedStream{conn: s.conn}).Next()
}

func xaiTTSAudioFromMessage(payload []byte) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Type    string `json:"type"`
		Delta   string `json:"delta"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	switch message.Type {
	case "audio.delta":
		audio, err := base64.StdEncoding.DecodeString(message.Delta)
		if err != nil {
			return nil, false, err
		}
		return xaiTTSAudioFrame(audio), false, nil
	case "audio.done":
		return nil, true, nil
	case "error":
		if message.Message == "" {
			message.Message = "unknown xai tts error"
		}
		return nil, false, fmt.Errorf("xai tts error: %s", message.Message)
	default:
		return nil, false, nil
	}
}

func xaiTTSAudioFrame(audio []byte) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        xaiTTSSampleRate,
			NumChannels:       xaiTTSNumChannels,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
