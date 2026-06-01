package baseten

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

const (
	defaultBasetenSTTSampleRate              = 16000
	defaultBasetenSTTBufferSizeSeconds       = 0.032
	defaultBasetenSTTEncoding                = "pcm_s16le"
	defaultBasetenSTTLanguage                = "en"
	defaultBasetenSTTPartialIntervalSeconds  = 1.0
	defaultBasetenSTTFinalMaxDurationSeconds = 30
	defaultBasetenSTTVADThreshold            = 0.5
	defaultBasetenSTTVADMinSilenceMS         = 300
	defaultBasetenSTTVADSpeechPadMS          = 30
)

type BasetenSTT struct {
	apiKey                     string
	modelEndpoint              string
	endpointPriority           int
	sampleRate                 int
	bufferSizeSeconds          float64
	encoding                   string
	language                   string
	enablePartialTranscripts   bool
	partialTranscriptInterval  float64
	finalTranscriptMaxDuration int
	showWordTimestamps         bool
	vadThreshold               float64
	vadMinSilenceDurationMS    int
	vadSpeechPadMS             int
}

type BasetenSTTOption func(*BasetenSTT)

func WithBasetenSTTModelEndpoint(endpoint string) BasetenSTTOption {
	return func(s *BasetenSTT) {
		if endpoint != "" {
			s.modelEndpoint = endpoint
			s.endpointPriority = 3
		}
	}
}

func WithBasetenSTTChainID(chainID string) BasetenSTTOption {
	return func(s *BasetenSTT) {
		if chainID != "" && s.endpointPriority < 2 {
			s.modelEndpoint = fmt.Sprintf("wss://chain-%s.api.baseten.co/environments/production/websocket", chainID)
			s.endpointPriority = 2
		}
	}
}

func WithBasetenSTTLanguage(language string) BasetenSTTOption {
	return func(s *BasetenSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithBasetenSTTEncoding(encoding string) BasetenSTTOption {
	return func(s *BasetenSTT) {
		if encoding != "" {
			s.encoding = encoding
		}
	}
}

func WithBasetenSTTSampleRate(sampleRate int) BasetenSTTOption {
	return func(s *BasetenSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithBasetenSTTBufferSizeSeconds(seconds float64) BasetenSTTOption {
	return func(s *BasetenSTT) {
		if seconds > 0 {
			s.bufferSizeSeconds = seconds
		}
	}
}

func WithBasetenSTTVADThreshold(threshold float64) BasetenSTTOption {
	return func(s *BasetenSTT) {
		s.vadThreshold = threshold
	}
}

func NewBasetenSTT(apiKey string, model string, opts ...BasetenSTTOption) *BasetenSTT {
	endpoint := model
	if endpoint == "" {
		endpoint = "whisper-v3"
	}
	if !strings.HasPrefix(endpoint, "ws://") && !strings.HasPrefix(endpoint, "wss://") {
		endpoint = fmt.Sprintf("wss://model-%s.api.baseten.co/environments/production/websocket", endpoint)
	}
	provider := &BasetenSTT{
		apiKey:                     apiKey,
		modelEndpoint:              endpoint,
		endpointPriority:           1,
		sampleRate:                 defaultBasetenSTTSampleRate,
		bufferSizeSeconds:          defaultBasetenSTTBufferSizeSeconds,
		encoding:                   defaultBasetenSTTEncoding,
		language:                   defaultBasetenSTTLanguage,
		enablePartialTranscripts:   true,
		partialTranscriptInterval:  defaultBasetenSTTPartialIntervalSeconds,
		finalTranscriptMaxDuration: defaultBasetenSTTFinalMaxDurationSeconds,
		showWordTimestamps:         true,
		vadThreshold:               defaultBasetenSTTVADThreshold,
		vadMinSilenceDurationMS:    defaultBasetenSTTVADMinSilenceMS,
		vadSpeechPadMS:             defaultBasetenSTTVADSpeechPadMS,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *BasetenSTT) Label() string { return "baseten.STT" }
func (s *BasetenSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    true,
		Diarization:       false,
		AlignedTranscript: "word",
		OfflineRecognize:  false,
	}
}

func (s *BasetenSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if language != "" {
		s.language = language
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, s.modelEndpoint, buildBasetenSTTHeaders(s))
	if err != nil {
		return nil, fmt.Errorf("failed to dial baseten stt websocket: %w", err)
	}
	metadata, err := json.Marshal(buildBasetenSTTMetadata(s))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, metadata); err != nil {
		_ = conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &basetenSTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state: &basetenSTTStreamState{
			language: s.language,
		},
	}
	go stream.readLoop()
	return stream, nil
}

func (s *BasetenSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("baseten stt does not support offline recognize")
}

func buildBasetenSTTHeaders(s *BasetenSTT) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Api-Key "+s.apiKey)
	return headers
}

func buildBasetenSTTMetadata(s *BasetenSTT) map[string]interface{} {
	return map[string]interface{}{
		"whisper_params": map[string]interface{}{
			"audio_language":       s.language,
			"show_word_timestamps": s.showWordTimestamps,
		},
		"streaming_params": map[string]interface{}{
			"encoding":                        s.encoding,
			"sample_rate":                     s.sampleRate,
			"enable_partial_transcripts":      s.enablePartialTranscripts,
			"partial_transcript_interval_s":   s.partialTranscriptInterval,
			"final_transcript_max_duration_s": s.finalTranscriptMaxDuration,
		},
		"streaming_vad_config": map[string]interface{}{
			"threshold":               s.vadThreshold,
			"min_silence_duration_ms": s.vadMinSilenceDurationMS,
			"speech_pad_ms":           s.vadSpeechPadMS,
		},
	}
}

type basetenSTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	state  *basetenSTTStreamState
}

func (s *basetenSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, frame.Data)
}

func (s *basetenSTTStream) Flush() error {
	return nil
}

func (s *basetenSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteMessage(websocket.TextMessage, []byte(`{"terminate_session":true}`))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *basetenSTTStream) Next() (*stt.SpeechEvent, error) {
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
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *basetenSTTStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		events, err := processBasetenSTTMessage(s.state, payload)
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

type basetenSTTStreamState struct {
	language        string
	startTimeOffset float64
}

func processBasetenSTTMessage(state *basetenSTTStreamState, payload []byte) ([]*stt.SpeechEvent, error) {
	var data map[string]interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	if msgType, _ := data["type"].(string); msgType != "" && msgType != "transcription" {
		return nil, nil
	}
	text, _ := data["transcript"].(string)
	segments := basetenSTTSegments(data["segments"])
	if text == "" {
		text = basetenSTTTextFromSegments(segments)
	}
	if text == "" {
		return nil, nil
	}
	confidence := basetenAnyFloat(data["confidence"])
	words := basetenSTTTimedWords(segments, state.startTimeOffset)
	language := state.language
	if value, _ := data["language_code"].(string); value != "" {
		language = value
	}
	eventType := stt.SpeechEventFinalTranscript
	if isFinal, ok := data["is_final"].(bool); ok && !isFinal {
		eventType = stt.SpeechEventInterimTranscript
		language = ""
	}
	startTime, endTime := basetenSTTStartEnd(segments, state.startTimeOffset)
	return []*stt.SpeechEvent{{
		Type: eventType,
		Alternatives: []stt.SpeechData{{
			Language:   language,
			Text:       text,
			Confidence: confidence,
			StartTime:  startTime,
			EndTime:    endTime,
			Words:      words,
		}},
	}}, nil
}

func basetenSTTSegments(raw interface{}) []map[string]interface{} {
	rawSegments, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	segments := make([]map[string]interface{}, 0, len(rawSegments))
	for _, rawSegment := range rawSegments {
		if segment, ok := rawSegment.(map[string]interface{}); ok {
			segments = append(segments, segment)
		}
	}
	return segments
}

func basetenSTTTextFromSegments(segments []map[string]interface{}) string {
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		if text, _ := segment["text"].(string); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func basetenSTTTimedWords(segments []map[string]interface{}, offset float64) []stt.TimedString {
	words := []stt.TimedString{}
	for _, segment := range segments {
		rawWords, ok := segment["word_timestamps"].([]interface{})
		if ok && len(rawWords) > 0 {
			for _, rawWord := range rawWords {
				wordMap, ok := rawWord.(map[string]interface{})
				if !ok {
					continue
				}
				words = append(words, stt.TimedString{
					Text:            basetenAnyString(wordMap["word"]),
					StartTime:       basetenAnyFloat(wordMap["start_time"]) + offset,
					EndTime:         basetenAnyFloat(wordMap["end_time"]) + offset,
					StartTimeOffset: offset,
				})
			}
			continue
		}
		words = append(words, stt.TimedString{
			Text:            basetenAnyString(segment["text"]),
			StartTime:       basetenAnyFloat(segment["start_time"]) + offset,
			EndTime:         basetenAnyFloat(segment["end_time"]) + offset,
			StartTimeOffset: offset,
		})
	}
	return words
}

func basetenSTTStartEnd(segments []map[string]interface{}, offset float64) (float64, float64) {
	if len(segments) == 0 {
		return 0, 0
	}
	start := basetenAnyFloat(segments[0]["start_time"]) + offset
	end := basetenAnyFloat(segments[len(segments)-1]["end_time"]) + offset
	return start, end
}

func basetenAnyString(value interface{}) string {
	str, _ := value.(string)
	return str
}

func basetenAnyFloat(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0
	}
}
