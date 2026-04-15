package elevenlabs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/gorilla/websocket"
)

type ElevenLabsTTS struct {
	apiKey  string
	voiceID string
	modelID string
}

func NewElevenLabsTTS(apiKey string, voiceID string, modelID string) (*ElevenLabsTTS, error) {
	if voiceID == "" {
		voiceID = "21m00Tcm4TlvDq8ikWAM" // Rachel
	}
	if modelID == "" {
		modelID = "eleven_monolingual_v1"
	}
	return &ElevenLabsTTS{
		apiKey:  apiKey,
		voiceID: voiceID,
		modelID: modelID,
	}, nil
}

func (t *ElevenLabsTTS) Label() string { return "elevenlabs.TTS" }
func (t *ElevenLabsTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: true}
}
func (t *ElevenLabsTTS) SampleRate() int  { return 24000 }
func (t *ElevenLabsTTS) NumChannels() int { return 1 }

// Synthesize performs a full HTTP POST for non-streaming scenarios.
func (t *ElevenLabsTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	apiURL := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s?output_format=pcm_24000", t.voiceID)
	body := map[string]interface{}{
		"text":     text,
		"model_id": t.modelID,
	}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("elevenlabs error: %s", string(respBody))
	}

	return &elevenLabsChunkedStream{
		resp: resp,
	}, nil
}

type elevenLabsChunkedStream struct {
	resp *http.Response
}

func (s *elevenLabsChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	// Read PCM audio in chunks from the HTTP response
	buf := make([]byte, 8192)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF && n > 0 {
			// Return final chunk
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              buf[:n],
					SampleRate:        24000,
					NumChannels:       1,
					SamplesPerChannel: uint32(n / 2),
				},
				IsFinal: true,
			}, nil
		}
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *elevenLabsChunkedStream) Close() error {
	return s.resp.Body.Close()
}

// Stream establishes a high-performance WebSocket connection to ElevenLabs for low-latency streaming TTS.
// The connection is lazily initialized on the first PushText call to avoid input_timeout_exceeded errors
// when the LLM takes time to produce output.
func (t *ElevenLabsTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	stream := &elevenLabsStream{
		audio:  make(chan *tts.SynthesizedAudio, 100),
		errCh:  make(chan error, 1),
		ctx:    ctx,
		cancel: cancel,
		tts:    t,
	}

	return stream, nil
}

// connect dials the ElevenLabs WebSocket and sends the initial configuration.
// It must be called with s.mu held.
func (s *elevenLabsStream) connect() error {
	u := url.URL{Scheme: "wss", Host: "api.elevenlabs.io", Path: fmt.Sprintf("/v1/text-to-speech/%s/stream-input", s.tts.voiceID)}
	q := u.Query()
	q.Set("model_id", s.tts.modelID)
	q.Set("output_format", "pcm_24000")
	u.RawQuery = q.Encode()

	header := make(http.Header)
	header.Set("xi-api-key", s.tts.apiKey)

	conn, _, err := websocket.DefaultDialer.DialContext(s.ctx, u.String(), header)
	if err != nil {
		return fmt.Errorf("failed to dial elevenlabs websocket: %w", err)
	}

	// Send initial configuration
	initMsg := map[string]interface{}{
		"text": " ", // Start with a space to initialize
		"voice_settings": map[string]interface{}{
			"stability":        0.5,
			"similarity_boost": 0.8,
		},
		"generation_config": map[string]interface{}{
			"chunk_length_schedule": []int{120, 160, 250, 290},
		},
	}
	if err := conn.WriteJSON(initMsg); err != nil {
		conn.Close()
		return fmt.Errorf("failed to write initial config to elevenlabs: %w", err)
	}

	s.conn = conn
	s.connected = true

	go s.readLoop()
	go s.pingLoop()

	return nil
}

type elevenLabsStream struct {
	conn      *websocket.Conn
	audio     chan *tts.SynthesizedAudio
	errCh     chan error
	mu        sync.Mutex
	closed    bool
	connected bool
	tts       *ElevenLabsTTS

	ctx    context.Context
	cancel context.CancelFunc
}

type elWSResponse struct {
	Audio               string `json:"audio"`
	IsFinal             bool   `json:"isFinal"`
	NormalizedAlignment *struct {
		Chars            []string `json:"chars"`
		CharStartTimesMs []int    `json:"charStartTimesMs"`
		CharDurationsMs  []int    `json:"charDurationsMs"`
	} `json:"normalizedAlignment"`
	Alignment *struct {
		Chars            []string `json:"chars"`
		CharStartTimesMs []int    `json:"charStartTimesMs"`
		CharDurationsMs  []int    `json:"charDurationsMs"`
	} `json:"alignment"`
	Error string `json:"error,omitempty"`
}

func (s *elevenLabsStream) readLoop() {
	defer s.Close()
	defer close(s.audio)

	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				logger.Logger.Errorw("ElevenLabs WebSocket read error", err)
				s.sendError(err)
			}
			return
		}

		var resp elWSResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			logger.Logger.Warnw("Failed to unmarshal ElevenLabs response", err, "payload", string(message))
			continue
		}

		if resp.Error != "" {
			logger.Logger.Errorw("ElevenLabs WebSocket returned error", nil, "error", resp.Error)
			s.sendError(fmt.Errorf("elevenlabs error: %s", resp.Error))
			return
		}

		var deltaText strings.Builder
		if resp.NormalizedAlignment != nil {
			for _, char := range resp.NormalizedAlignment.Chars {
				deltaText.WriteString(char)
			}
		} else if resp.Alignment != nil {
			for _, char := range resp.Alignment.Chars {
				deltaText.WriteString(char)
			}
		}

		if resp.Audio != "" {
			data, err := base64.StdEncoding.DecodeString(resp.Audio)
			if err != nil {
				logger.Logger.Errorw("Failed to decode base64 audio", err)
				continue
			}

			// Block slightly if buffer is full, but respect context
			select {
			case <-s.ctx.Done():
				return
			case s.audio <- &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              data,
					SampleRate:        24000,
					NumChannels:       1,
					SamplesPerChannel: uint32(len(data) / 2),
				},
				IsFinal:   resp.IsFinal,
				DeltaText: deltaText.String(),
			}:
			}
		} else if resp.IsFinal || deltaText.Len() > 0 {
			// Even if there's no audio, pass alignment or final flags
			select {
			case <-s.ctx.Done():
				return
			case s.audio <- &tts.SynthesizedAudio{
				IsFinal:   resp.IsFinal,
				DeltaText: deltaText.String(),
			}:
			}
		}

		if resp.IsFinal {
			return
		}
	}
}

func (s *elevenLabsStream) pingLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if !s.closed {
				_ = s.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))
			}
			s.mu.Unlock()
		}
	}
}

func (s *elevenLabsStream) sendError(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *elevenLabsStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	// Lazy connect: only dial WebSocket when first text is ready,
	// avoiding input_timeout_exceeded from idle connections.
	if !s.connected {
		if err := s.connect(); err != nil {
			return err
		}
	}
	msg := map[string]interface{}{
		"text":                   text,
		"try_trigger_generation": true,
	}
	if err := s.conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("failed to write text to elevenlabs: %w", err)
	}
	return nil
}

func (s *elevenLabsStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if !s.connected {
		// No text was ever pushed; cancel context so Next() returns io.EOF cleanly.
		s.closed = true
		s.cancel()
		return nil
	}
	// Empty string is ElevenLabs' end-of-input signal.
	// Do NOT include flush:true — that is a mid-stream directive and causes
	// ElevenLabs to treat the message as a no-op flush rather than closing
	// the stream, which leads to input_timeout_exceeded after 20 seconds.
	return s.conn.WriteJSON(map[string]interface{}{"text": ""})
}

func (s *elevenLabsStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if s.connected {
		// Clean close via empty text
		_ = s.conn.WriteJSON(map[string]interface{}{"text": ""})
		// Wait a moment for final chunks
		time.Sleep(50 * time.Millisecond)
		return s.conn.Close()
	}
	return nil
}

func (s *elevenLabsStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case err := <-s.errCh:
		return nil, err
	case audio, ok := <-s.audio:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return audio, nil
	case <-s.ctx.Done():
		// Drain any buffered audio before returning EOF. Without this, frames
		// that readLoop already pushed to s.audio can be lost when ctx is
		// cancelled concurrently with s.audio being closed (LIFO defer race).
		select {
		case audio, ok := <-s.audio:
			if ok {
				return audio, nil
			}
		default:
		}
		return nil, io.EOF
	}
}
