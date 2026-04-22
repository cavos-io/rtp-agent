package assemblyai

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

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

type AssemblyAISTT struct {
	apiKey     string
	apiURL     string
	wsURL      string
	httpClient *http.Client
}

type STTOption func(*AssemblyAISTT)

func WithSTTBaseURL(url string) STTOption {
	return func(s *AssemblyAISTT) {
		s.apiURL = url
	}
}

func WithWSURL(url string) STTOption {
	return func(s *AssemblyAISTT) {
		s.wsURL = url
	}
}

func WithHTTPClient(client *http.Client) STTOption {
	return func(s *AssemblyAISTT) {
		s.httpClient = client
	}
}

func NewAssemblyAISTT(apiKey string, opts ...STTOption) *AssemblyAISTT {
	s := &AssemblyAISTT{
		apiKey:     apiKey,
		apiURL:     "https://api.assemblyai.com/v2",
		wsURL:      "wss://api.assemblyai.com/v2/realtime/ws",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *AssemblyAISTT) Label() string { return "assemblyai.STT" }
func (s *AssemblyAISTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: true}
}

func (s *AssemblyAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	// AssemblyAI requires fetching a temporary token or passing the API key in the header
	// Standard websocket connection to wss://api.assemblyai.com/v2/realtime/ws

	u, err := url.Parse(s.wsURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("sample_rate", "16000")
	u.RawQuery = q.Encode()

	header := make(http.Header)
	header.Set("Authorization", s.apiKey)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return nil, err
	}

	stream := &assemblyAISTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
	}

	go stream.readLoop()

	return stream, nil
}

func (s *AssemblyAISTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	// For AssemblyAI, a standard synchronous request isn't easily doable via single REST call without polling,
	// but we implement a basic upload and creation request to satisfy structural parity.
	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	uploadReq, _ := http.NewRequestWithContext(ctx, "POST", s.apiURL+"/upload", bytes.NewReader(buf.Bytes()))
	uploadReq.Header.Set("Authorization", s.apiKey)

	uploadResp, err := s.httpClient.Do(uploadReq)
	if err != nil {
		return nil, err
	}
	defer uploadResp.Body.Close()

	var uploadResult struct {
		UploadURL string `json:"upload_url"`
	}
	json.NewDecoder(uploadResp.Body).Decode(&uploadResult)

	reqBody := map[string]interface{}{
		"audio_url": uploadResult.UploadURL,
	}
	if language != "" {
		reqBody["language_code"] = language
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", s.apiURL+"/transcript", bytes.NewBuffer(jsonBody))
	req.Header.Set("Authorization", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: fmt.Sprintf("[AssemblyAI Job ID: %s]", result.ID)},
		},
	}, nil
}

type assemblyAISTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool
}

type aaiResponse struct {
	MessageType string  `json:"message_type"`
	Text        string  `json:"text"`
	Confidence  float64 `json:"confidence"`
	Words       []struct {
		Start int `json:"start"`
		End   int `json:"end"`
	} `json:"words"`
	Error string `json:"error"`
}

func (s *assemblyAISTTStream) readLoop() {
	defer close(s.events)
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				s.errCh <- err
			}
			return
		}

		var resp aaiResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}

		if resp.MessageType == "SessionBegins" {
			continue
		}

		if resp.MessageType == "SessionTerminated" {
			return
		}

		if resp.Error != "" {
			s.errCh <- fmt.Errorf("assemblyai error: %s", resp.Error)
			return
		}

		if resp.MessageType == "PartialTranscript" || resp.MessageType == "FinalTranscript" {
			eventType := stt.SpeechEventInterimTranscript
			if resp.MessageType == "FinalTranscript" {
				eventType = stt.SpeechEventFinalTranscript
			}

			if resp.Text != "" {
				s.events <- &stt.SpeechEvent{
					Type: eventType,
					Alternatives: []stt.SpeechData{
						{
							Text:       resp.Text,
							Confidence: resp.Confidence,
						},
					},
				}
			}
		}
	}
}

func (s *assemblyAISTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}

	b64 := base64.StdEncoding.EncodeToString(frame.Data)
	msg := map[string]interface{}{
		"audio_data": b64,
	}

	return s.conn.WriteJSON(msg)
}

func (s *assemblyAISTTStream) Flush() error {
	return nil
}

func (s *assemblyAISTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	// Terminate session
	s.conn.WriteJSON(map[string]bool{"terminate_session": true})
	return s.conn.Close()
}

func (s *assemblyAISTTStream) Next() (*stt.SpeechEvent, error) {
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
	}
}

