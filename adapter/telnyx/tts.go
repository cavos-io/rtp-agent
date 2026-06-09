package telnyx

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
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultTelnyxTTSBaseURL    = "wss://api.telnyx.com/v2/text-to-speech/speech"
	defaultTelnyxTTSVoice      = "Telnyx.NaturalHD.astra"
	defaultTelnyxTTSSampleRate = 16000
	telnyxTTSNumChannels       = 1
)

type TelnyxTTS struct {
	apiKey     string
	baseURL    string
	voice      string
	sampleRate int
}

type TelnyxTTSOption func(*TelnyxTTS)

func WithTelnyxTTSBaseURL(baseURL string) TelnyxTTSOption {
	return func(t *TelnyxTTS) {
		if baseURL != "" {
			t.baseURL = baseURL
		}
	}
}

func NewTelnyxTTS(apiKey string, voice string, opts ...TelnyxTTSOption) *TelnyxTTS {
	if apiKey == "" {
		apiKey = os.Getenv(telnyxAPIKeyEnv)
	}
	if voice == "" {
		voice = defaultTelnyxTTSVoice
	}
	provider := &TelnyxTTS{
		apiKey:     apiKey,
		baseURL:    defaultTelnyxTTSBaseURL,
		voice:      voice,
		sampleRate: defaultTelnyxTTSSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *TelnyxTTS) Label() string { return "telnyx.TTS" }
func (t *TelnyxTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *TelnyxTTS) SampleRate() int  { return t.sampleRate }
func (t *TelnyxTTS) NumChannels() int { return telnyxTTSNumChannels }

func (t *TelnyxTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	stream, err := t.Stream(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.PushText(text); err != nil {
		stream.Close()
		return nil, err
	}
	if err := stream.Flush(); err != nil {
		stream.Close()
		return nil, err
	}
	return &telnyxTTSChunkedStream{stream: stream}, nil
}

func (t *TelnyxTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateTelnyxAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildTelnyxTTSStreamURL(t), buildTelnyxTTSHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial telnyx tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &telnyxTTSStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
		events:     make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
	}
	if err := writeTelnyxTTSMessage(conn, buildTelnyxTTSTextMessage(" ")); err != nil {
		conn.Close()
		cancel()
		return nil, err
	}
	go stream.readLoop()
	return stream, nil
}

func buildTelnyxTTSStreamURL(t *TelnyxTTS) string {
	u, err := url.Parse(t.baseURL)
	if err != nil {
		return t.baseURL
	}
	q := u.Query()
	q.Set("voice", t.voice)
	u.RawQuery = q.Encode()
	return u.String()
}

func buildTelnyxTTSHeaders(t *TelnyxTTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+t.apiKey)
	return headers
}

func buildTelnyxTTSTextMessage(text string) map[string]string {
	return map[string]string{"text": text}
}

func writeTelnyxTTSMessage(conn *websocket.Conn, message map[string]string) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

type telnyxTTSChunkedStream struct {
	stream tts.SynthesizeStream
}

func (s *telnyxTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	return s.stream.Next()
}

func (s *telnyxTTSChunkedStream) Close() error {
	return s.stream.Close()
}

type telnyxTTSStream struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	sampleRate int
	events     chan *tts.SynthesizedAudio
	errCh      chan error
	mu         sync.Mutex
	closed     bool
}

func (s *telnyxTTSStream) PushText(text string) error {
	return writeTelnyxTTSMessage(s.conn, buildTelnyxTTSTextMessage(text))
}

func (s *telnyxTTSStream) Flush() error {
	return writeTelnyxTTSMessage(s.conn, buildTelnyxTTSTextMessage(""))
}

func (s *telnyxTTSStream) Close() error {
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

func (s *telnyxTTSStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *telnyxTTSStream) readLoop() {
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
		audio, done, err := telnyxTTSAudioFromMessage(payload, s.sampleRate)
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

func telnyxTTSAudioFromMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.Audio == "" {
		return nil, true, nil
	}
	data, err := base64.StdEncoding.DecodeString(message.Audio)
	if err != nil {
		return nil, false, err
	}
	return telnyxTTSAudioFrame(data, sampleRate), false, nil
}

func telnyxTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       telnyxTTSNumChannels,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
