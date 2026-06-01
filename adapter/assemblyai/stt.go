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
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/gorilla/websocket"
)

var (
	assemblyAIBaseURL      = "https://api.assemblyai.com/v2"
	assemblyAIHTTPClient   = http.DefaultClient
	assemblyAIPollInterval = time.Second
)

type AssemblyAISTT struct {
	apiKey string
}

func NewAssemblyAISTT(apiKey string) *AssemblyAISTT {
	return &AssemblyAISTT{
		apiKey: apiKey,
	}
}

func (s *AssemblyAISTT) Label() string { return "assemblyai.STT" }
func (s *AssemblyAISTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: true}
}

func (s *AssemblyAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	// AssemblyAI requires fetching a temporary token or passing the API key in the header
	// Standard websocket connection to wss://api.assemblyai.com/v2/realtime/ws

	u := url.URL{Scheme: "wss", Host: "api.assemblyai.com", Path: "/v2/realtime/ws"}
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
	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	uploadReq, err := http.NewRequestWithContext(ctx, "POST", assemblyAIEndpoint("/upload"), bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}
	uploadReq.Header.Set("Authorization", s.apiKey)

	uploadResp, err := assemblyAIHTTPClient.Do(uploadReq)
	if err != nil {
		return nil, err
	}
	defer uploadResp.Body.Close()
	if err := assemblyAIStatusError(uploadResp); err != nil {
		return nil, err
	}

	var uploadResult struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.NewDecoder(uploadResp.Body).Decode(&uploadResult); err != nil {
		return nil, err
	}
	if uploadResult.UploadURL == "" {
		return nil, fmt.Errorf("assemblyai upload response missing upload_url")
	}

	reqBody := map[string]interface{}{
		"audio_url": uploadResult.UploadURL,
	}
	if language != "" {
		reqBody["language_code"] = language
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", assemblyAIEndpoint("/transcript"), bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := assemblyAIHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := assemblyAIStatusError(resp); err != nil {
		return nil, err
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.ID == "" {
		return nil, fmt.Errorf("assemblyai transcript response missing id")
	}

	return s.pollTranscript(ctx, result.ID)
}

func (s *AssemblyAISTT) pollTranscript(ctx context.Context, id string) (*stt.SpeechEvent, error) {
	for {
		req, err := http.NewRequestWithContext(ctx, "GET", assemblyAIEndpoint("/transcript/"+id), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", s.apiKey)

		resp, err := assemblyAIHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}

		var result struct {
			Status     string           `json:"status"`
			Text       string           `json:"text"`
			Confidence float64          `json:"confidence"`
			Error      string           `json:"error"`
			Words      []assemblyAIWord `json:"words"`
		}
		statusErr := assemblyAIStatusError(resp)
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		closeErr := resp.Body.Close()
		if statusErr != nil {
			return nil, statusErr
		}
		if decodeErr != nil {
			return nil, decodeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}

		switch result.Status {
		case "completed":
			return assemblyAITranscriptEvent(result.Text, result.Confidence, assemblyAITimedStrings(result.Words)), nil
		case "error":
			if result.Error == "" {
				result.Error = "transcript failed"
			}
			return nil, fmt.Errorf("assemblyai transcript error: %s", result.Error)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(assemblyAIPollInterval):
		}
	}
}

func assemblyAIEndpoint(path string) string {
	return strings.TrimRight(assemblyAIBaseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func assemblyAIStatusError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) > 0 {
		return fmt.Errorf("assemblyai request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("assemblyai request failed: %s", resp.Status)
}

func assemblyAITranscriptEvent(text string, confidence float64, words []stt.TimedString) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{
				Text:       text,
				Confidence: confidence,
				Words:      words,
			},
		},
	}
}

type assemblyAIWord struct {
	Text       string  `json:"text"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float64 `json:"confidence"`
}

func assemblyAITimedStrings(words []assemblyAIWord) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}
	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:       word.Text,
			StartTime:  float64(word.Start) / 1000,
			EndTime:    float64(word.End) / 1000,
			Confidence: word.Confidence,
		})
	}
	return timed
}

type assemblyAISTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool
}

type aaiResponse struct {
	MessageType string           `json:"message_type"`
	Text        string           `json:"text"`
	Confidence  float64          `json:"confidence"`
	Words       []assemblyAIWord `json:"words"`
	Error       string           `json:"error"`
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
			if event := assemblyAIRealtimeTranscriptEvent(resp); event != nil {
				s.events <- event
			}
		}
	}
}

func assemblyAIRealtimeTranscriptEvent(resp aaiResponse) *stt.SpeechEvent {
	if resp.Text == "" {
		return nil
	}

	eventType := stt.SpeechEventInterimTranscript
	if resp.MessageType == "FinalTranscript" {
		eventType = stt.SpeechEventFinalTranscript
	}

	return &stt.SpeechEvent{
		Type: eventType,
		Alternatives: []stt.SpeechData{
			{
				Text:       resp.Text,
				Confidence: resp.Confidence,
				Words:      assemblyAITimedStrings(resp.Words),
			},
		},
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
