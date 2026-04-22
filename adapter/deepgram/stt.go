package deepgram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/utils/language"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

// Keyword represents a keyword with a boost weight for Deepgram STT recognition.
// This is the canonical definition shared between rtp-agent and agent-worker layer.
type Keyword struct {
	Keyword string  `json:"keyword"`
	Boost   float64 `json:"boost"`
}

// DeepgramOption is a functional option for configuring DeepgramSTT.
type DeepgramOption func(*DeepgramSTT)

// WithKeywords sets the keywords with boost weights (for nova-2 and older models).
// Keywords are passed as "keyword:boostInt" format in the Deepgram API.
func WithKeywords(keywords []Keyword) DeepgramOption {
	return func(s *DeepgramSTT) {
		s.keywords = keywords
	}
}

// WithKeyterms sets the keyterms (for nova-3 and newer models).
// Keyterms are passed as keyword strings only (no boost weight) in the Deepgram API.
func WithKeyterms(keyterms []string) DeepgramOption {
	return func(s *DeepgramSTT) {
		s.keyterms = keyterms
	}
}

// IsNova3Model returns true if the model string indicates a nova-3 or newer model.
// This is exported for use by application-layer logic to determine keyword parameters.
func IsNova3Model(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "nova-3") || strings.Contains(lower, "nova3")
}

type DeepgramSTT struct {
	apiKey  string
	model   string
	baseURL string
	wsURL   string
}

type Option func(*DeepgramSTT)

func WithBaseURL(url string) Option {
	return func(s *DeepgramSTT) {
		s.baseURL = url
	}
}

func WithWSURL(url string) Option {
	return func(s *DeepgramSTT) {
		s.wsURL = url
	}
}

func NewDeepgramSTT(apiKey string, model string, opts ...Option) *DeepgramSTT {
	if model == "" {
		model = "nova-2"
	}
	s := &DeepgramSTT{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.deepgram.com",
		wsURL:   "wss://api.deepgram.com",
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *DeepgramSTT) Label() string { return "deepgram.STT" }
func (s *DeepgramSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: true}
}

func (s *DeepgramSTT) Stream(ctx context.Context, languageStr string) (stt.RecognizeStream, error) {
	languageStr = language.NormalizeLanguage(languageStr)

	u, err := url.Parse(s.wsURL + "/v1/listen")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("model", s.model)
	if languageStr != "" {
		q.Set("language", languageStr)
	}
	q.Set("smart_format", "true")
	q.Set("interim_results", "true")
	q.Set("encoding", "linear16")
	q.Set("sample_rate", "24000")
	// Enable Deepgram's native Voice Activity Detection / Endpointing
	q.Set("endpointing", "300")
	q.Set("vad_events", "true")

	// Add keyword/keyterm parameters
	// The application layer (adapter/provider) is responsible for choosing
	// which parameters to use based on model version.
	// rtp-agent just passes them through unconditionally.
	for _, kt := range s.keyterms {
		q.Add("keyterms", kt)
	}
	for _, kw := range s.keywords {
		// Boost weight must be an integer; round float to nearest int
		boost := int(math.Round(kw.Boost))
		if boost < 1 {
			boost = 1
		}
		q.Add("keywords", fmt.Sprintf("%s:%d", kw.Keyword, boost))
	}

	u.RawQuery = q.Encode()

	header := make(http.Header)
	header.Set("Authorization", "Token "+s.apiKey)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return nil, fmt.Errorf("failed to dial deepgram websocket: %w", err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &deepgramStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
	}

	go stream.readLoop()
	go stream.keepAliveLoop()

	return stream, nil
}

func (s *DeepgramSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, languageStr string) (*stt.SpeechEvent, error) {
	languageStr = language.NormalizeLanguage(languageStr)

	apiURL := fmt.Sprintf("%s/v1/listen?model=%s&smart_format=true", strings.TrimSuffix(s.baseURL, "/"), s.model)
	if languageStr != "" {
		q.Set("language", languageStr)
	}

	// Add keyword/keyterm parameters
	// The application layer is responsible for choosing which parameters to use.
	for _, kt := range s.keyterms {
		q.Add("keyterms", kt)
	}
	for _, kw := range s.keywords {
		boost := int(math.Round(kw.Boost))
		if boost < 1 {
			boost = 1
		}
		q.Add("keywords", fmt.Sprintf("%s:%d", kw.Keyword, boost))
	}

	u.RawQuery = q.Encode()
	apiURL := u.String()

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "audio/wav")
	req.Header.Set("Authorization", "Token "+s.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("deepgram recognize error: %s", string(respBody))
	}

	var result struct {
		Results struct {
			Channels []struct {
				Alternatives []struct {
					Transcript string  `json:"transcript"`
					Confidence float64 `json:"confidence"`
				} `json:"alternatives"`
			} `json:"channels"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var transcript string
	var confidence float64
	if len(result.Results.Channels) > 0 && len(result.Results.Channels[0].Alternatives) > 0 {
		alt := result.Results.Channels[0].Alternatives[0]
		transcript = alt.Transcript
		confidence = alt.Confidence
	}

	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: transcript, Confidence: confidence},
		},
	}, nil
}

type deepgramStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
}

type dgResponse struct {
	Type        string `json:"type"`
	IsFinal     bool   `json:"is_final"`
	SpeechFinal bool   `json:"speech_final"`
	Channel     struct {
		Alternatives []struct {
			Transcript string  `json:"transcript"`
			Confidence float64 `json:"confidence"`
		} `json:"alternatives"`
	} `json:"channel"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
	Metadata struct {
		RequestID string `json:"request_id"`
	} `json:"metadata"`
}

func (s *deepgramStream) readLoop() {
	defer s.Close()
	defer close(s.events)

	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				logger.Logger.Errorw("Deepgram WebSocket read error", err)
				s.sendError(err)
			}
			return
		}

		var resp dgResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}

		switch resp.Type {
		case "SpeechStarted":
			s.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})

		case "UtteranceEnd":
			s.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})

		case "Results":
			if len(resp.Channel.Alternatives) > 0 {
				event := &stt.SpeechEvent{
					Type:      stt.SpeechEventInterimTranscript,
					RequestID: resp.Metadata.RequestID,
				}

				if resp.IsFinal {
					event.Type = stt.SpeechEventFinalTranscript
				}

				var transcriptBuilder string
				for _, alt := range resp.Channel.Alternatives {
					transcriptBuilder += alt.Transcript
					event.Alternatives = append(event.Alternatives, stt.SpeechData{
						Text:       alt.Transcript,
						Confidence: alt.Confidence,
						StartTime:  resp.Start,
						EndTime:    resp.Start + resp.Duration,
					})
				}

				// Only send if there is actual text or if it's explicitly marked final
				if transcriptBuilder != "" || resp.IsFinal {
					s.sendEvent(event)
				}

				if resp.SpeechFinal {
					s.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
				}
			}
		}
	}
}

// keepAliveLoop sends a native KeepAlive payload every 10 seconds to prevent Deepgram from dropping idle streams.
func (s *deepgramStream) keepAliveLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if !s.closed {
				_ = s.conn.WriteJSON(map[string]string{"type": "KeepAlive"})
			}
			s.mu.Unlock()
		}
	}
}

func (s *deepgramStream) sendEvent(ev *stt.SpeechEvent) {
	select {
	case <-s.ctx.Done():
	case s.events <- ev:
	}
}

func (s *deepgramStream) sendError(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *deepgramStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, frame.Data)
}

func (s *deepgramStream) Flush() error {
	// Deepgram forces a flush by sending a CloseStream payload (but we want to stay alive)
	// We can send an empty audio frame or rely on Endpointing
	return nil
}

func (s *deepgramStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteJSON(map[string]string{"type": "CloseStream"})
	// Wait a tiny bit for the final transcript
	time.Sleep(50 * time.Millisecond)
	return s.conn.Close()
}

func (s *deepgramStream) Next() (*stt.SpeechEvent, error) {
	select {
	case <-s.ctx.Done():
		return nil, io.EOF
	case err := <-s.errCh:
		return nil, err
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
	}
}
