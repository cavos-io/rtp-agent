package deepgram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/library/utils/language"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/gorilla/websocket"
)

type DeepgramSTT struct {
	apiKey         string
	model          string
	punctuate      bool
	smartFormat    bool
	noDelay        bool
	endpointingMS  int
	fillerWords    bool
	sampleRate     int
	numChannels    int
	interimResults bool
	vadEvents      bool
	baseURL        string
}

func NewDeepgramSTT(apiKey string, model string) *DeepgramSTT {
	if model == "" {
		model = "nova-3"
	}
	return &DeepgramSTT{
		apiKey:         apiKey,
		model:          model,
		punctuate:      true,
		smartFormat:    false,
		noDelay:        true,
		endpointingMS:  25,
		fillerWords:    true,
		sampleRate:     16000,
		numChannels:    1,
		interimResults: true,
		vadEvents:      true,
		baseURL:        "https://api.deepgram.com/v1/listen",
	}
}

func (s *DeepgramSTT) Label() string { return "deepgram.STT" }
func (s *DeepgramSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, AlignedTranscript: "word", OfflineRecognize: true}
}

func (s *DeepgramSTT) Stream(ctx context.Context, languageStr string) (stt.RecognizeStream, error) {
	languageStr = language.NormalizeLanguage(languageStr)

	header := make(http.Header)
	header.Set("Authorization", "Token "+s.apiKey)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildDeepgramStreamURL(s, languageStr), header)
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

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", buildDeepgramRecognizeURL(s, languageStr), bytes.NewReader(buf.Bytes()))
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

	var result dgRecognitionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return deepgramRecognizeSpeechEvent(result), nil
}

func buildDeepgramStreamURL(s *DeepgramSTT, languageStr string) string {
	u, q := deepgramBaseURL(s, true)
	q.Set("model", s.model)
	if languageStr != "" {
		q.Set("language", languageStr)
	}
	q.Set("punctuate", strconv.FormatBool(s.punctuate))
	q.Set("smart_format", strconv.FormatBool(s.smartFormat))
	q.Set("no_delay", strconv.FormatBool(s.noDelay))
	q.Set("interim_results", strconv.FormatBool(s.interimResults))
	q.Set("encoding", "linear16")
	q.Set("sample_rate", strconv.Itoa(s.sampleRate))
	q.Set("channels", strconv.Itoa(s.numChannels))
	if s.endpointingMS == 0 {
		q.Set("endpointing", "false")
	} else {
		q.Set("endpointing", strconv.Itoa(s.endpointingMS))
	}
	q.Set("vad_events", strconv.FormatBool(s.vadEvents))
	q.Set("filler_words", strconv.FormatBool(s.fillerWords))
	u.RawQuery = q.Encode()
	return u.String()
}

func buildDeepgramRecognizeURL(s *DeepgramSTT, languageStr string) string {
	u, q := deepgramBaseURL(s, false)
	q.Set("model", s.model)
	q.Set("punctuate", strconv.FormatBool(s.punctuate))
	q.Set("smart_format", strconv.FormatBool(s.smartFormat))
	if languageStr != "" {
		q.Set("language", languageStr)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func deepgramBaseURL(s *DeepgramSTT, websocketURL bool) (*url.URL, url.Values) {
	parsed, err := url.Parse(s.baseURL)
	if err != nil {
		parsed = &url.URL{Scheme: "https", Host: "api.deepgram.com", Path: "/v1/listen"}
	}
	if websocketURL && parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else if websocketURL && parsed.Scheme == "http" {
		parsed.Scheme = "ws"
	} else if !websocketURL && parsed.Scheme == "wss" {
		parsed.Scheme = "https"
	} else if !websocketURL && parsed.Scheme == "ws" {
		parsed.Scheme = "http"
	}
	return parsed, parsed.Query()
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

type dgWord struct {
	Word       string  `json:"word"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
	Speaker    *int    `json:"speaker,omitempty"`
}

type dgAlternative struct {
	Transcript string   `json:"transcript"`
	Confidence float64  `json:"confidence"`
	Words      []dgWord `json:"words"`
}

type dgRecognitionChannel struct {
	Alternatives []dgAlternative `json:"alternatives"`
}

type dgRecognitionResponse struct {
	Results struct {
		Channels []dgRecognitionChannel `json:"channels"`
	} `json:"results"`
}

type dgResponse struct {
	Type        string `json:"type"`
	IsFinal     bool   `json:"is_final"`
	SpeechFinal bool   `json:"speech_final"`
	Channel     struct {
		Alternatives []dgAlternative `json:"alternatives"`
	} `json:"channel"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
	Metadata struct {
		RequestID string `json:"request_id"`
	} `json:"metadata"`
}

func deepgramRecognizeSpeechEvent(resp dgRecognitionResponse) *stt.SpeechEvent {
	event := &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{},
		},
	}

	if len(resp.Results.Channels) == 0 || len(resp.Results.Channels[0].Alternatives) == 0 {
		return event
	}

	alt := resp.Results.Channels[0].Alternatives[0]
	event.Alternatives[0] = stt.SpeechData{
		Text:       alt.Transcript,
		Confidence: alt.Confidence,
		Words:      deepgramTimedStrings(alt.Words),
	}
	return event
}

func deepgramSpeechEvent(resp dgResponse) *stt.SpeechEvent {
	if resp.Type != "Results" || len(resp.Channel.Alternatives) == 0 {
		return nil
	}

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
			Words:      deepgramTimedStrings(alt.Words),
		})
	}

	if transcriptBuilder == "" && !resp.IsFinal {
		return nil
	}

	return event
}

func deepgramTimedStrings(words []dgWord) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}

	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:       word.Word,
			StartTime:  word.Start,
			EndTime:    word.End,
			Confidence: word.Confidence,
			SpeakerID:  deepgramSpeakerID(word.Speaker),
		})
	}
	return timed
}

func deepgramSpeakerID(speaker *int) string {
	if speaker == nil {
		return ""
	}
	return strconv.Itoa(*speaker)
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
			if event := deepgramSpeechEvent(resp); event != nil {
				s.sendEvent(event)

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
