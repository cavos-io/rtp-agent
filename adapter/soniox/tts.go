package soniox

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

	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/gorilla/websocket"
)

const (
	defaultSonioxTTSWebsocketURL = "wss://tts-rt.soniox.com/tts-websocket"
	defaultSonioxTTSModel        = "tts-rt-v1-preview"
	defaultSonioxTTSLanguage     = "en"
	defaultSonioxTTSVoice        = "Maya"
	defaultSonioxTTSAudioFormat  = "pcm_s16le"
	defaultSonioxTTSSampleRate   = 24000
	sonioxTTSNumChannels         = 1
	sonioxTTSKeepaliveInterval   = 10 * time.Second
)

type SonioxTTS struct {
	apiKey       string
	websocketURL string
	model        string
	language     string
	voice        string
	audioFormat  string
	sampleRate   int
	bitrate      *int
}

type SonioxTTSOption func(*SonioxTTS)

func WithSonioxTTSWebsocketURL(websocketURL string) SonioxTTSOption {
	return func(t *SonioxTTS) {
		if websocketURL != "" {
			t.websocketURL = websocketURL
		}
	}
}

func WithSonioxTTSModel(model string) SonioxTTSOption {
	return func(t *SonioxTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithSonioxTTSLanguage(language string) SonioxTTSOption {
	return func(t *SonioxTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithSonioxTTSVoice(voice string) SonioxTTSOption {
	return func(t *SonioxTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithSonioxTTSAudioFormat(audioFormat string) SonioxTTSOption {
	return func(t *SonioxTTS) {
		if audioFormat != "" {
			t.audioFormat = audioFormat
		}
	}
}

func WithSonioxTTSSampleRate(sampleRate int) SonioxTTSOption {
	return func(t *SonioxTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithSonioxTTSBitrate(bitrate int) SonioxTTSOption {
	return func(t *SonioxTTS) {
		if bitrate > 0 {
			t.bitrate = &bitrate
		}
	}
}

func NewSonioxTTS(apiKey string, opts ...SonioxTTSOption) *SonioxTTS {
	provider := &SonioxTTS{
		apiKey:       apiKey,
		websocketURL: defaultSonioxTTSWebsocketURL,
		model:        defaultSonioxTTSModel,
		language:     defaultSonioxTTSLanguage,
		voice:        defaultSonioxTTSVoice,
		audioFormat:  defaultSonioxTTSAudioFormat,
		sampleRate:   defaultSonioxTTSSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *SonioxTTS) Label() string { return "soniox.TTS" }
func (t *SonioxTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *SonioxTTS) SampleRate() int  { return t.sampleRate }
func (t *SonioxTTS) NumChannels() int { return sonioxTTSNumChannels }

func (t *SonioxTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
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
	return &sonioxTTSChunkedStream{stream: stream}, nil
}

func (t *SonioxTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, t.websocketURL, http.Header{})
	if err != nil {
		return nil, fmt.Errorf("failed to dial soniox tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	streamID := shortSonioxTTSStreamID()
	stream := &sonioxTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		provider:   t,
		streamID:   streamID,
		sampleRate: t.sampleRate,
		events:     make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
	}
	if err := writeSonioxTTSMessage(conn, buildSonioxTTSStartConfig(t, streamID)); err != nil {
		conn.Close()
		cancel()
		return nil, err
	}
	go stream.readLoop()
	go stream.keepAliveLoop()
	return stream, nil
}

type sonioxTTSChunkedStream struct {
	stream tts.SynthesizeStream
}

func (s *sonioxTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	return s.stream.Next()
}

func (s *sonioxTTSChunkedStream) Close() error {
	return s.stream.Close()
}

type sonioxTTSSynthesizeStream struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	provider   *SonioxTTS
	streamID   string
	sampleRate int
	events     chan *tts.SynthesizedAudio
	errCh      chan error
	mu         sync.Mutex
	closed     bool
}

func (s *sonioxTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	return writeSonioxTTSMessage(s.conn, buildSonioxTTSTextMessage(s.streamID, text, false))
}

func (s *sonioxTTSSynthesizeStream) Flush() error {
	return writeSonioxTTSMessage(s.conn, buildSonioxTTSTextMessage(s.streamID, "", true))
}

func (s *sonioxTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = writeSonioxTTSMessage(s.conn, buildSonioxTTSCancelMessage(s.streamID))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *sonioxTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *sonioxTTSSynthesizeStream) readLoop() {
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
		audio, done, err := sonioxTTSAudioFromMessage(payload, s.streamID, s.sampleRate)
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

func (s *sonioxTTSSynthesizeStream) keepAliveLoop() {
	ticker := time.NewTicker(sonioxTTSKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = writeSonioxTTSMessage(s.conn, buildSonioxTTSKeepaliveMessage())
		case <-s.ctx.Done():
			return
		}
	}
}

func buildSonioxTTSStartConfig(t *SonioxTTS, streamID string) map[string]any {
	config := map[string]any{
		"api_key":      t.apiKey,
		"model":        t.model,
		"language":     t.language,
		"voice":        t.voice,
		"audio_format": t.audioFormat,
		"sample_rate":  t.sampleRate,
		"stream_id":    streamID,
	}
	if t.bitrate != nil {
		config["bitrate"] = *t.bitrate
	}
	return config
}

func buildSonioxTTSTextMessage(streamID string, text string, textEnd bool) map[string]any {
	payload := map[string]any{"stream_id": streamID}
	if text != "" {
		payload["text"] = text
	}
	if textEnd {
		payload["text_end"] = true
	}
	return payload
}

func buildSonioxTTSCancelMessage(streamID string) map[string]any {
	return map[string]any{"stream_id": streamID, "cancel": true}
}

func buildSonioxTTSKeepaliveMessage() map[string]any {
	return map[string]any{"keep_alive": true}
}

func writeSonioxTTSMessage(conn *websocket.Conn, message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func sonioxTTSAudioFromMessage(payload []byte, streamID string, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		StreamID     string `json:"stream_id"`
		Audio        string `json:"audio"`
		AudioEnd     bool   `json:"audio_end"`
		Terminated   bool   `json:"terminated"`
		ErrorCode    any    `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.StreamID == "" || message.StreamID != streamID {
		return nil, false, nil
	}
	if message.ErrorCode != nil {
		return nil, false, fmt.Errorf("soniox tts error %v: %s", message.ErrorCode, message.ErrorMessage)
	}
	var audio *tts.SynthesizedAudio
	if message.Audio != "" {
		data, err := base64.StdEncoding.DecodeString(message.Audio)
		if err != nil {
			return nil, false, err
		}
		audio = sonioxTTSAudioFrame(data, sampleRate)
	}
	return audio, message.Terminated, nil
}

func sonioxTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       sonioxTTSNumChannels,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}

func sonioxTTSAudioFormatToMIMEType(audioFormat string) string {
	if strings.HasPrefix(audioFormat, "pcm") {
		return "audio/pcm"
	}
	if audioFormat == "mp3" {
		return "audio/mpeg"
	}
	return "audio/" + audioFormat
}

func shortSonioxTTSStreamID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
