package deepgram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

type DeepgramTTS struct {
	apiKey string
	model  string
}

func NewDeepgramTTS(apiKey string, model string) *DeepgramTTS {
	if model == "" {
		model = "aura-asteria-en"
	}
	return &DeepgramTTS{
		apiKey: apiKey,
		model:  model,
	}
}

func (t *DeepgramTTS) Label() string { return "deepgram.TTS" }
func (t *DeepgramTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *DeepgramTTS) SampleRate() int { return 48000 }
func (t *DeepgramTTS) NumChannels() int { return 1 }

func (t *DeepgramTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	u := fmt.Sprintf("https://api.deepgram.com/v1/speak?model=%s&encoding=linear16&sample_rate=48000", t.model)
	body := map[string]interface{}{
		"text": text,
	}
	jsonBody, _ := json.Marshal(body)

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
		resp: resp,
	}, nil
}

func (t *DeepgramTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	u := url.URL{Scheme: "wss", Host: "api.deepgram.com", Path: "/v1/speak"}
	q := u.Query()
	q.Set("model", t.model)
	q.Set("encoding", "linear16")
	q.Set("sample_rate", "48000")
	u.RawQuery = q.Encode()

	header := make(map[string][]string)
	header["Authorization"] = []string{"Token " + t.apiKey}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return nil, err
	}

	stream := &deepgramTTSStream{
		conn:   conn,
		audio:  make(chan *tts.SynthesizedAudio, 10),
		errCh:  make(chan error, 1),
	}

	go stream.readLoop()

	return stream, nil
}

type deepgramTTSChunkedStream struct {
	resp *http.Response
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
			SampleRate:        48000,
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
					SampleRate:        48000,
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
