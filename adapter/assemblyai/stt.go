package assemblyai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultAssemblyAIBaseURL        = "wss://streaming.assemblyai.com"
	defaultAssemblyAIEncoding       = "pcm_s16le"
	defaultAssemblyAISpeechModel    = "universal-streaming-english"
	defaultAssemblyAISampleRate     = 16000
	defaultAssemblyAIMinTurnSilence = 100
)

type AssemblyAISTT struct {
	apiKey             string
	baseURL            string
	sampleRate         int
	encoding           string
	speechModel        string
	languageDetection  *bool
	minTurnSilence     *int
	maxTurnSilence     *int
	continuousPartials *bool
	speakerLabels      *bool
}

type AssemblyAISTTOption func(*AssemblyAISTT)

func WithAssemblyAISTTBaseURL(baseURL string) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithAssemblyAISTTSampleRate(sampleRate int) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithAssemblyAISTTModel(model string) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if model == "u3-pro" {
			model = "u3-rt-pro"
		}
		if model != "" {
			s.speechModel = model
		}
	}
}

func WithAssemblyAISTTMinTurnSilence(ms int) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if ms > 0 {
			s.minTurnSilence = intPtr(ms)
		}
	}
}

func WithAssemblyAISTTMaxTurnSilence(ms int) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if ms > 0 {
			s.maxTurnSilence = intPtr(ms)
		}
	}
}

func WithAssemblyAISTTLanguageDetection(enabled bool) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.languageDetection = boolPtr(enabled)
	}
}

func WithAssemblyAISTTContinuousPartials(enabled bool) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.continuousPartials = boolPtr(enabled)
	}
}

func WithAssemblyAISTTSpeakerLabels(enabled bool) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.speakerLabels = boolPtr(enabled)
	}
}

func NewAssemblyAISTT(apiKey string, opts ...AssemblyAISTTOption) *AssemblyAISTT {
	provider := &AssemblyAISTT{
		apiKey:         apiKey,
		baseURL:        defaultAssemblyAIBaseURL,
		sampleRate:     defaultAssemblyAISampleRate,
		encoding:       defaultAssemblyAIEncoding,
		speechModel:    defaultAssemblyAISpeechModel,
		minTurnSilence: intPtr(defaultAssemblyAIMinTurnSilence),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.speechModel == "u3-rt-pro" && provider.continuousPartials == nil {
		provider.continuousPartials = boolPtr(true)
	}
	return provider
}

func (s *AssemblyAISTT) Label() string { return "assemblyai.STT" }
func (s *AssemblyAISTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: s.speakerLabels != nil && *s.speakerLabels, AlignedTranscript: "word", OfflineRecognize: false}
}

func (s *AssemblyAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	header := make(http.Header)
	header.Set("Authorization", s.apiKey)
	header.Set("Content-Type", "application/json")
	header.Set("User-Agent", "AssemblyAI/1.0 (integration=Livekit)")

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildAssemblyAIStreamURL(s), header)
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
	return nil, fmt.Errorf("assemblyai offline recognize is not implemented")
}

func buildAssemblyAIStreamURL(s *AssemblyAISTT) string {
	u, err := url.Parse(strings.TrimRight(s.baseURL, "/") + "/v3/ws")
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("sample_rate", strconv.Itoa(s.sampleRate))
	q.Set("encoding", s.encoding)
	q.Set("speech_model", s.speechModel)
	if s.minTurnSilence != nil {
		q.Set("min_turn_silence", strconv.Itoa(*s.minTurnSilence))
	}
	if s.maxTurnSilence != nil {
		q.Set("max_turn_silence", strconv.Itoa(*s.maxTurnSilence))
	} else if s.speechModel == "u3-rt-pro" && s.minTurnSilence != nil {
		q.Set("max_turn_silence", strconv.Itoa(*s.minTurnSilence))
	}
	if s.continuousPartials != nil {
		q.Set("continuous_partials", strconv.FormatBool(*s.continuousPartials))
	}
	if s.languageDetection != nil {
		q.Set("language_detection", strconv.FormatBool(*s.languageDetection))
	} else {
		q.Set("language_detection", strconv.FormatBool(strings.Contains(s.speechModel, "multilingual") || s.speechModel == "u3-rt-pro"))
	}
	if s.speakerLabels != nil {
		q.Set("speaker_labels", strconv.FormatBool(*s.speakerLabels))
	}
	u.RawQuery = q.Encode()
	return u.String()
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
	Type        string           `json:"type"`
	MessageType string           `json:"message_type"`
	Text        string           `json:"text"`
	Transcript  string           `json:"transcript"`
	Confidence  float64          `json:"confidence"`
	EndOfTurn   bool             `json:"end_of_turn"`
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

		if resp.Type == "Begin" || resp.MessageType == "SessionBegins" {
			continue
		}

		if resp.Type == "Termination" || resp.MessageType == "SessionTerminated" {
			return
		}

		if resp.Error != "" {
			s.errCh <- fmt.Errorf("assemblyai error: %s", resp.Error)
			return
		}

		if resp.Type == "Turn" || resp.MessageType == "PartialTranscript" || resp.MessageType == "FinalTranscript" {
			if event := assemblyAIRealtimeTranscriptEvent(resp); event != nil {
				s.events <- event
			}
		}
	}
}

func assemblyAIRealtimeTranscriptEvent(resp aaiResponse) *stt.SpeechEvent {
	text := resp.Text
	if text == "" {
		text = resp.Transcript
	}
	if text == "" {
		return nil
	}

	eventType := stt.SpeechEventInterimTranscript
	if resp.EndOfTurn || resp.MessageType == "FinalTranscript" {
		eventType = stt.SpeechEventFinalTranscript
	}
	words := assemblyAITimedStrings(resp.Words)
	confidence := resp.Confidence
	if confidence == 0 && len(words) > 0 {
		for _, word := range words {
			confidence += word.Confidence
		}
		confidence /= float64(len(words))
	}

	return &stt.SpeechEvent{
		Type: eventType,
		Alternatives: []stt.SpeechData{
			{
				Text:       text,
				Confidence: confidence,
				Words:      words,
			},
		},
	}
}

func intPtr(value int) *int {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
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
