package inference

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

type TTS struct {
	model     string
	voice     string
	apiKey    string
	apiSecret string
	baseURL   string
}

type TTSOption func(*TTS)

func WithTTSBaseURL(url string) TTSOption {
	return func(t *TTS) {
		t.baseURL = url
	}
}

func NewTTS(model string, apiKey, apiSecret string, opts ...TTSOption) *TTS {
	if model == "" {
		model = "cartesia/sonic-3"
	}
	t := &TTS{
		model:     model,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   "wss://agent-gateway.livekit.cloud/v1",
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

type FallbackModel struct {
	Model       string         `json:"model"`
	Voice       string         `json:"voice"`
	ExtraKwargs map[string]any `json:"extra,omitempty"`
}

func (t *TTS) Label() string {
	return "livekit.TTS"
}

func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: true}
}

func (t *TTS) SampleRate() int {
	return 24000
}

func (t *TTS) NumChannels() int {
	return 1
}

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	// For parity, implement synthesize using stream helper
	return nil, fmt.Errorf("synthesize is unsupported natively by LiveKit Inference TTS, use stream instead")
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	token, err := CreateAccessToken(t.apiKey, t.apiSecret, time.Hour)
	if err != nil {
		return nil, err
	}

	modelName := t.model
	voice := t.voice
	if idx := strings.LastIndex(t.model, ":"); idx != -1 {
		voice = t.model[idx+1:]
		modelName = t.model[:idx]
	}

	wsURL, err := url.Parse(t.baseURL + "/tts")
	if err != nil {
		return nil, err
	}

	q := wsURL.Query()
	q.Set("model", modelName)
	wsURL.RawQuery = q.Encode()

	header := http.Header{}
	header.Add("Authorization", "Bearer "+token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), header)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to LiveKit Inference TTS: %w", err)
	}

	// Send session.create
	createParams := map[string]interface{}{
		"type":        "session.create",
		"sample_rate": "24000",
		"encoding":    "pcm_s16le",
		"model":       modelName,
	}
	if voice != "" {
		createParams["voice"] = voice
	}

	if err := conn.WriteJSON(createParams); err != nil {
		conn.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	stream := &inferenceTTSStream{
		tts:       t,
		model:     modelName,
		conn:      conn,
		ctx:       ctx,
		cancel:    cancel,
		tokenizer: tokenize.NewBasicSentenceTokenizer().Stream("en"),
		eventCh:   make(chan *tts.SynthesizedAudio, 100),
	}

	go stream.run()

	return stream, nil
}

type inferenceTTSStream struct {
	tts       *TTS
	model     string
	conn      *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	tokenizer tokenize.SentenceStream
	eventCh   chan *tts.SynthesizedAudio
	mu        sync.Mutex
	closeOnce sync.Once
	closed    bool
}

func (s *inferenceTTSStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	return s.tokenizer.PushText(text)
}

func (s *inferenceTTSStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	s.tokenizer.Flush()

	endPkt := map[string]interface{}{
		"type": "session.flush",
	}
	return s.conn.WriteJSON(endPkt)
}

func (s *inferenceTTSStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.closeOnce.Do(func() {
		s.cancel()
		s.tokenizer.Close()
		_ = s.conn.Close()
	})

	return nil
}

func (s *inferenceTTSStream) Next() (*tts.SynthesizedAudio, error) {
	ev, ok := <-s.eventCh
	if !ok {
		return nil, context.Canceled
	}
	return ev, nil
}

func (s *inferenceTTSStream) run() {
	defer func() {
		_ = s.Close()
		close(s.eventCh)
	}()

	// Tokenizer loop
	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}

			tok, err := s.tokenizer.Next()
			if err != nil {
				return
			}

			tokenPkt := map[string]interface{}{
				"type":       "input_transcript",
				"transcript": tok.Token + " ",
				"generation_config": map[string]interface{}{
					"model": s.model,
				},
			}

			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			err = s.conn.WriteJSON(tokenPkt)
			s.mu.Unlock()

			if err != nil {
				return
			}
		}
	}()

	// Read loop
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			_, msg, err := s.conn.ReadMessage()
			if err != nil {
				logger.Logger.Errorw("LiveKit Inference TTS disconnected", err)
				return
			}

			var ev map[string]interface{}
			if err := json.Unmarshal(msg, &ev); err != nil {
				continue
			}

			if evType, ok := ev["type"].(string); ok {
				if evType == "output_audio" {
					if audioB64, ok := ev["audio"].(string); ok {
						data, _ := base64.StdEncoding.DecodeString(audioB64)
						s.emitEvent(&tts.SynthesizedAudio{
							Frame: &model.AudioFrame{
								Data:              data,
								SampleRate:        24000,
								NumChannels:       1,
								SamplesPerChannel: uint32(len(data) / 2),
							},
						})
					}
				} else if evType == "error" {
					logger.Logger.Errorw("LiveKit Inference TTS error", nil, "msg", string(msg))
				}
			}
		}
	}
}

func (s *inferenceTTSStream) emitEvent(ev *tts.SynthesizedAudio) {
	if ev == nil {
		return
	}

	select {
	case <-s.ctx.Done():
		return
	case s.eventCh <- ev:
	}
}

