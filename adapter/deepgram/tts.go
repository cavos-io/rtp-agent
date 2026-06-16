package deepgram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const defaultDeepgramTTSBaseURL = "https://api.deepgram.com/v1/speak"

type DeepgramTTS struct {
	apiKey     string
	baseURL    string
	model      string
	encoding   string
	sampleRate int
	mipOptOut  bool
}

type DeepgramTTSOption func(*DeepgramTTS)

func WithDeepgramTTSBaseURL(baseURL string) DeepgramTTSOption {
	return func(t *DeepgramTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithDeepgramTTSMipOptOut(mipOptOut bool) DeepgramTTSOption {
	return func(t *DeepgramTTS) {
		t.mipOptOut = mipOptOut
	}
}

func WithDeepgramTTSAudioFormat(encoding string, sampleRate int) DeepgramTTSOption {
	return func(t *DeepgramTTS) {
		if encoding != "" {
			t.encoding = encoding
		}
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func NewDeepgramTTS(apiKey string, model string, opts ...DeepgramTTSOption) *DeepgramTTS {
	if apiKey == "" {
		apiKey = os.Getenv("DEEPGRAM_API_KEY")
	}
	if model == "" {
		model = "aura-2-andromeda-en"
	}
	provider := &DeepgramTTS{
		apiKey:     apiKey,
		baseURL:    defaultDeepgramTTSBaseURL,
		model:      model,
		encoding:   "linear16",
		sampleRate: 24000,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *DeepgramTTS) Label() string { return "deepgram.TTS" }
func (t *DeepgramTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *DeepgramTTS) SampleRate() int  { return t.sampleRate }
func (t *DeepgramTTS) NumChannels() int { return 1 }
func (t *DeepgramTTS) Model() string    { return t.model }
func (t *DeepgramTTS) Provider() string { return "Deepgram" }

func (t *DeepgramTTS) UpdateOptions(model string) {
	if model != "" {
		t.model = model
	}
}

func (t *DeepgramTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateDeepgramTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	u, jsonBody := buildDeepgramTTSSynthesizeRequest(t, text)

	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token "+t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("deepgram tts error: %s", string(respBody))
	}

	return &deepgramTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildDeepgramTTSSynthesizeRequest(t *DeepgramTTS, text string) (string, []byte) {
	u := deepgramTTSBaseURL(t, false)
	q := u.Query()
	q.Set("model", t.model)
	q.Set("encoding", t.encoding)
	q.Set("sample_rate", fmt.Sprintf("%d", t.sampleRate))
	q.Set("container", "none")
	q.Set("mip_opt_out", fmt.Sprintf("%t", t.mipOptOut))
	u.RawQuery = q.Encode()
	body := map[string]interface{}{"text": text}
	jsonBody, _ := json.Marshal(body)
	return u.String(), jsonBody
}

func (t *DeepgramTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateDeepgramTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	header := make(map[string][]string)
	header["Authorization"] = []string{"Token " + t.apiKey}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildDeepgramTTSStreamURL(t), header)
	if err != nil {
		return nil, err
	}

	stream := &deepgramTTSStream{
		conn:       conn,
		audio:      make(chan *tts.SynthesizedAudio, 10),
		errCh:      make(chan error, 1),
		sampleRate: t.sampleRate,
	}

	go stream.readLoop()

	return stream, nil
}

func validateDeepgramTTSAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("deepgram API key required. Set DEEPGRAM_API_KEY or provide api_key")
	}
	return nil
}

func buildDeepgramTTSStreamURL(t *DeepgramTTS) string {
	u := deepgramTTSBaseURL(t, true)
	q := u.Query()
	q.Set("model", t.model)
	q.Set("encoding", t.encoding)
	q.Set("sample_rate", fmt.Sprintf("%d", t.sampleRate))
	q.Set("mip_opt_out", fmt.Sprintf("%t", t.mipOptOut))
	u.RawQuery = q.Encode()
	return u.String()
}

func deepgramTTSBaseURL(t *DeepgramTTS, websocketURL bool) url.URL {
	baseURL := t.baseURL
	if websocketURL && strings.HasPrefix(baseURL, "http") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	} else if !websocketURL && strings.HasPrefix(baseURL, "ws") {
		baseURL = strings.Replace(baseURL, "ws", "http", 1)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		if websocketURL {
			return url.URL{Scheme: "wss", Host: "api.deepgram.com", Path: "/v1/speak"}
		}
		return url.URL{Scheme: "https", Host: "api.deepgram.com", Path: "/v1/speak"}
	}
	return *parsed
}

type deepgramTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *deepgramTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *deepgramTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

type deepgramTTSStream struct {
	conn   *websocket.Conn
	audio  chan *tts.SynthesizedAudio
	errCh  chan error
	mu     sync.Mutex
	closed bool

	sampleRate int
}

func (s *deepgramTTSStream) readLoop() {
	defer close(s.audio)
	for {
		msgType, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				s.errCh <- err
			}
			return
		}

		if msgType == websocket.BinaryMessage {
			s.audio <- &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              message,
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(len(message) / 2),
				},
			}
		} else {
			// Deepgram sends metadata as text
			var metadata map[string]interface{}
			if err := json.Unmarshal(message, &metadata); err == nil {
				if metadata["type"] == "Flushed" {
					// handle flush if needed
				}
			}
		}
	}
}

func (s *deepgramTTSStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	msg := map[string]interface{}{
		"type": "Speak",
		"text": text,
	}
	return s.conn.WriteJSON(msg)
}

func (s *deepgramTTSStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	msg := map[string]interface{}{
		"type": "Flush",
	}
	return s.conn.WriteJSON(msg)
}

func (s *deepgramTTSStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	// Send close message
	s.conn.WriteMessage(websocket.TextMessage, []byte(`{"type": "Close"}`))
	return s.conn.Close()
}

func (s *deepgramTTSStream) Next() (*tts.SynthesizedAudio, error) {
	select {
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
	case err := <-s.errCh:
		return nil, err
	}
}
