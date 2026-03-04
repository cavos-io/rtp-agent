package cartesia

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/gorilla/websocket"
)

type CartesiaTTS struct {
	apiKey  string
	voiceID string
	model   string
}

func NewCartesiaTTS(apiKey string, voiceID string, model string) *CartesiaTTS {
	if voiceID == "" {
		voiceID = "79a125e8-cd45-4c13-8a67-188112f4dd22" // A default voice
	}
	if model == "" {
		model = "sonic-english"
	}
	return &CartesiaTTS{
		apiKey:  apiKey,
		voiceID: voiceID,
		model:   model,
	}
}

func (t *CartesiaTTS) Label() string { return "cartesia.TTS" }
func (t *CartesiaTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *CartesiaTTS) SampleRate() int { return 24000 }
func (t *CartesiaTTS) NumChannels() int { return 1 }

func (t *CartesiaTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	apiURL := "https://api.cartesia.ai/tts/bytes"

	reqBody := map[string]interface{}{
		"model_id": t.model,
		"transcript": text,
		"voice": map[string]interface{}{
			"mode": "id",
			"id":   t.voiceID,
		},
		"output_format": map[string]interface{}{
			"container": "raw",
			"encoding":  "pcm_s16le",
			"sample_rate": 24000,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", t.apiKey)
	req.Header.Set("Cartesia-Version", "2024-06-10")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("cartesia tts error: %s", string(respBody))
	}

	return &cartesiaTTSChunkedStream{
		resp: resp,
	}, nil
}

type cartesiaTTSChunkedStream struct {
	resp *http.Response
}

func (s *cartesiaTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *cartesiaTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func (t *CartesiaTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	u := url.URL{Scheme: "wss", Host: "api.cartesia.ai", Path: "/tts/websocket"}
	q := u.Query()
	q.Set("api_key", t.apiKey)
	q.Set("cartesia_version", "2024-06-10")
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return nil, err
	}

	// Send context initialization
	initMsg := map[string]interface{}{
		"context_id": "default",
		"model_id":   t.model,
		"transcript": " ",
		"voice": map[string]interface{}{
			"mode": "id",
			"id":   t.voiceID,
		},
		"output_format": map[string]interface{}{
			"container":   "raw",
			"encoding":    "pcm_s16le",
			"sample_rate": 24000,
		},
	}
	if err := conn.WriteJSON(initMsg); err != nil {
		conn.Close()
		return nil, err
	}

	stream := &cartesiaTTSStream{
		conn:   conn,
		audio:  make(chan *tts.SynthesizedAudio, 10),
		errCh:  make(chan error, 1),
	}

	go stream.readLoop()

	return stream, nil
}

type cartesiaTTSStream struct {
	conn   *websocket.Conn
	audio  chan *tts.SynthesizedAudio
	errCh  chan error
	mu     sync.Mutex
	closed bool
}

type cartesiaWSResponse struct {
	Type     string `json:"type"`
	Error    string `json:"error"`
	Data     string `json:"data"` // base64 encoded audio
	Done     bool   `json:"done"`
}

func (s *cartesiaTTSStream) readLoop() {
	defer close(s.audio)
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				s.errCh <- err
			}
			return
		}

		var resp cartesiaWSResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}

		if resp.Type == "error" {
			s.errCh <- fmt.Errorf("cartesia error: %s", resp.Error)
			return
		}

		if resp.Type == "chunk" && resp.Data != "" {
			data, err := base64.StdEncoding.DecodeString(resp.Data)
			if err == nil {
				s.audio <- &tts.SynthesizedAudio{
					Frame: &model.AudioFrame{
						Data:              data,
						SampleRate:        24000,
						NumChannels:       1,
						SamplesPerChannel: uint32(len(data) / 2),
					},
					IsFinal: resp.Done,
				}
			}
		}

		if resp.Type == "done" || resp.Done {
			// Context finished, but we might keep connection open for more text
		}
	}
}

func (s *cartesiaTTSStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	msg := map[string]interface{}{
		"context_id": "default",
		"transcript": text,
		"continue":   true,
	}
	return s.conn.WriteJSON(msg)
}

func (s *cartesiaTTSStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	msg := map[string]interface{}{
		"context_id": "default",
		"transcript": "",
		"continue":   false,
	}
	return s.conn.WriteJSON(msg)
}

func (s *cartesiaTTSStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.conn.Close()
}

func (s *cartesiaTTSStream) Next() (*tts.SynthesizedAudio, error) {
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
