package inworld

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultInworldSTTModel                            = "inworld/inworld-stt-1"
	defaultInworldSTTLanguage                         = "en-US"
	defaultInworldSTTSampleRate                       = 16000
	defaultInworldSTTNumChannels                      = 1
	defaultInworldSTTBaseURL                          = "https://api.inworld.ai/"
	defaultInworldSTTMinEndOfTurnSilenceWhenConfident = 200
	defaultInworldSTTEndOfTurnConfidenceThreshold     = 0.3
	inworldSTTEndpoint                                = "stt/v1/transcribe:streamBidirectional"
)

type InworldSTT struct {
	mu                               sync.Mutex
	apiKey                           string
	authorization                    string
	baseURL                          string
	model                            string
	language                         string
	sampleRate                       int
	numChannels                      int
	enableVoiceProfile               bool
	voiceProfileTopN                 int
	vadThreshold                     *float64
	minEndOfTurnSilenceWhenConfident int
	endOfTurnConfidenceThreshold     float64
	streams                          map[*inworldSTTStream]struct{}
}

type InworldSTTOption func(*InworldSTT)

func WithInworldSTTBaseURL(baseURL string) InworldSTTOption {
	return func(s *InworldSTT) {
		if baseURL != "" {
			s.baseURL = baseURL
		}
	}
}

func WithInworldSTTModel(model string) InworldSTTOption {
	return func(s *InworldSTT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithInworldSTTLanguage(language string) InworldSTTOption {
	return func(s *InworldSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithInworldSTTSampleRate(sampleRate int) InworldSTTOption {
	return func(s *InworldSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithInworldSTTNumChannels(numChannels int) InworldSTTOption {
	return func(s *InworldSTT) {
		if numChannels > 0 {
			s.numChannels = numChannels
		}
	}
}

func WithInworldSTTVoiceProfile(enabled bool) InworldSTTOption {
	return func(s *InworldSTT) {
		s.enableVoiceProfile = enabled
	}
}

func WithInworldSTTVoiceProfileTopN(topN int) InworldSTTOption {
	return func(s *InworldSTT) {
		if topN > 0 {
			s.voiceProfileTopN = topN
		}
	}
}

func WithInworldSTTVADThreshold(threshold float64) InworldSTTOption {
	return func(s *InworldSTT) {
		s.vadThreshold = &threshold
	}
}

func WithInworldSTTMinEndOfTurnSilenceWhenConfident(ms int) InworldSTTOption {
	return func(s *InworldSTT) {
		if ms >= 0 {
			s.minEndOfTurnSilenceWhenConfident = ms
		}
	}
}

func WithInworldSTTEndOfTurnConfidenceThreshold(threshold float64) InworldSTTOption {
	return func(s *InworldSTT) {
		s.endOfTurnConfidenceThreshold = threshold
	}
}

func NewInworldSTT(apiKey string, opts ...InworldSTTOption) *InworldSTT {
	resolvedAPIKey := resolveInworldAPIKey(apiKey)
	provider := &InworldSTT{
		apiKey:                           resolvedAPIKey,
		authorization:                    "Basic " + resolvedAPIKey,
		baseURL:                          defaultInworldSTTBaseURL,
		model:                            defaultInworldSTTModel,
		language:                         defaultInworldSTTLanguage,
		sampleRate:                       defaultInworldSTTSampleRate,
		numChannels:                      defaultInworldSTTNumChannels,
		enableVoiceProfile:               true,
		voiceProfileTopN:                 1,
		minEndOfTurnSilenceWhenConfident: defaultInworldSTTMinEndOfTurnSilenceWhenConfident,
		endOfTurnConfidenceThreshold:     defaultInworldSTTEndOfTurnConfidenceThreshold,
		streams:                          make(map[*inworldSTTStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *InworldSTT) Label() string { return "inworld.STT" }
func (s *InworldSTT) Model() string { return s.model }
func (s *InworldSTT) Provider() string {
	return "Inworld"
}

func (s *InworldSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, OfflineRecognize: false}
}

func (s *InworldSTT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return defaultInworldSTTSampleRate
	}
	return uint32(s.sampleRate)
}

func (s *InworldSTT) UpdateOptions(opts ...InworldSTTOption) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, opt := range opts {
		opt(s)
	}
}

func (s *InworldSTT) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	streams := make([]*inworldSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streams = make(map[*inworldSTTStream]struct{})
	s.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (s *InworldSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildInworldSTTStreamURL(s), buildInworldSTTHeaders(s))
	if err != nil {
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial inworld stt websocket: %v", err))
	}
	requestLanguage := s.language
	if language != "" {
		requestLanguage = language
	}
	if err := writeInworldSTTMessage(conn, buildInworldSTTConfigMessage(s, requestLanguage)); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &inworldSTTStream{
		conn:        conn,
		events:      make(chan *stt.SpeechEvent, 100),
		errCh:       make(chan error, 1),
		ctx:         streamCtx,
		cancel:      cancel,
		sendMessage: func(message map[string]any) error { return writeInworldSTTMessage(conn, message) },
		state: &inworldSTTStreamState{
			language:  requestLanguage,
			requestID: shortInworldSTTRequestID(),
		},
		provider: s,
	}
	s.registerStream(stream)
	go stream.readLoop()
	return stream, nil
}

func (s *InworldSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("inworld stt does not support batch recognition")
}

type inworldSTTStream struct {
	conn          *websocket.Conn
	events        chan *stt.SpeechEvent
	errCh         chan error
	mu            sync.Mutex
	closed        bool
	audioDuration float64
	sendMessage   func(map[string]any) error

	ctx    context.Context
	cancel context.CancelFunc
	state  *inworldSTTStreamState

	provider *InworldSTT
}

func (s *InworldSTT) registerStream(stream *inworldSTTStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = make(map[*inworldSTTStream]struct{})
	}
	s.streams[stream] = struct{}{}
}

func (s *InworldSTT) unregisterStream(stream *inworldSTTStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func (s *inworldSTTStream) unregisterFromProvider() {
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
}

func (s *inworldSTTStream) PushFrame(frame *model.AudioFrame) error {
	if s.isClosed() {
		return io.ErrClosedPipe
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.addAudioDuration(frame)
	return s.writeMessage(buildInworldSTTAudioChunkMessage(frame.Data))
}

func (s *inworldSTTStream) Flush() error {
	if s.isClosed() {
		return io.ErrClosedPipe
	}
	s.flushRecognitionUsage()
	return s.writeMessage(buildInworldSTTEndTurnMessage())
}

func (s *inworldSTTStream) writeMessage(message map[string]any) error {
	if s.sendMessage != nil {
		return s.sendMessage(message)
	}
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	return writeInworldSTTMessage(s.conn, message)
}

func (s *inworldSTTStream) addAudioDuration(frame *model.AudioFrame) {
	if frame == nil || frame.SampleRate == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audioDuration += float64(frame.SamplesPerChannel) / float64(frame.SampleRate)
}

func (s *inworldSTTStream) flushRecognitionUsage() {
	s.mu.Lock()
	duration := s.audioDuration
	s.audioDuration = 0
	s.mu.Unlock()
	if duration == 0 {
		return
	}
	s.events <- &stt.SpeechEvent{
		Type:      stt.SpeechEventRecognitionUsage,
		RequestID: s.state.requestID,
		RecognitionUsage: &stt.RecognitionUsage{
			AudioDuration: duration,
		},
	}
}

func (s *inworldSTTStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel := s.cancel
	conn := s.conn
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	_ = s.writeMessage(buildInworldSTTCloseStreamMessage())
	var err error
	if conn != nil {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		err = conn.Close()
	}
	s.unregisterFromProvider()
	return err
}

func (s *inworldSTTStream) Next() (*stt.SpeechEvent, error) {
	if s.isClosed() {
		return nil, io.EOF
	}
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
		if s.isClosed() {
			return nil, io.EOF
		}
		return nil, s.ctx.Err()
	}
}

func (s *inworldSTTStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *inworldSTTStream) readLoop() {
	defer func() {
		s.unregisterFromProvider()
		close(s.events)
	}()
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
		var data map[string]any
		if err := json.Unmarshal(payload, &data); err != nil {
			continue
		}
		for _, event := range processInworldSTTStreamEvent(s.state, data) {
			s.events <- event
		}
	}
}

type inworldSTTStreamState struct {
	language  string
	requestID string
	speaking  bool
}

func buildInworldSTTTranscribeConfig(s *InworldSTT, language string) map[string]any {
	if language == "" {
		language = s.language
	}
	config := map[string]any{
		"modelId":                      s.model,
		"audioEncoding":                "LINEAR16",
		"sampleRateHertz":              s.sampleRate,
		"numberOfChannels":             s.numChannels,
		"language":                     language,
		"endOfTurnConfidenceThreshold": s.endOfTurnConfidenceThreshold,
		"inworldSttV1Config": map[string]any{
			"minEndOfTurnSilenceWhenConfident": s.minEndOfTurnSilenceWhenConfident,
		},
	}
	if s.enableVoiceProfile {
		config["voiceProfileConfig"] = map[string]any{
			"enableVoiceProfile": true,
			"topN":               s.voiceProfileTopN,
		}
	}
	if s.vadThreshold != nil {
		config["inworldSttV1Config"].(map[string]any)["vadThreshold"] = *s.vadThreshold
	}
	return config
}

func buildInworldSTTConfigMessage(s *InworldSTT, language string) map[string]any {
	return map[string]any{"transcribeConfig": buildInworldSTTTranscribeConfig(s, language)}
}

func buildInworldSTTAudioChunkMessage(audio []byte) map[string]any {
	return map[string]any{"audioChunk": map[string]any{"content": base64.StdEncoding.EncodeToString(audio)}}
}

func buildInworldSTTEndTurnMessage() map[string]any {
	return map[string]any{"endTurn": map[string]any{}}
}

func buildInworldSTTCloseStreamMessage() map[string]any {
	return map[string]any{"closeStream": map[string]any{}}
}

func buildInworldSTTStreamURL(s *InworldSTT) string {
	base := strings.TrimRight(s.baseURL, "/")
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)
	return base + "/" + inworldSTTEndpoint
}

func buildInworldSTTHeaders(s *InworldSTT) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", s.authorization)
	return headers
}

func writeInworldSTTMessage(conn *websocket.Conn, message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func processInworldSTTStreamEvent(state *inworldSTTStreamState, data map[string]any) []*stt.SpeechEvent {
	result, _ := data["result"].(map[string]any)
	if result == nil {
		return nil
	}
	if _, ok := result["speechStarted"]; ok && !state.speaking {
		state.speaking = true
		return []*stt.SpeechEvent{{Type: stt.SpeechEventStartOfSpeech, RequestID: state.requestID}}
	}
	transcription, _ := result["transcription"].(map[string]any)
	if transcription == nil {
		return nil
	}
	text, _ := transcription["transcript"].(string)
	isFinal, _ := transcription["isFinal"].(bool)
	if text == "" && !isFinal {
		return nil
	}

	var events []*stt.SpeechEvent
	if text != "" {
		eventType := stt.SpeechEventInterimTranscript
		if isFinal {
			eventType = stt.SpeechEventFinalTranscript
		}
		metadata := map[string]any(nil)
		if voiceProfile, ok := transcription["voiceProfile"]; ok {
			metadata = map[string]any{"voice_profile": voiceProfile}
		}
		events = append(events, &stt.SpeechEvent{
			Type:      eventType,
			RequestID: state.requestID,
			Alternatives: []stt.SpeechData{{
				Text:     text,
				Language: state.language,
				Metadata: metadata,
			}},
		})
	}
	if isFinal && state.speaking {
		state.speaking = false
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech, RequestID: state.requestID})
	}
	return events
}

func shortInworldSTTRequestID() string {
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}
