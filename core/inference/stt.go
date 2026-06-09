package inference

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/library/logger"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/gorilla/websocket"
)

type STT struct {
	mu             sync.Mutex
	model          string
	language       string
	encoding       string
	sampleRate     int
	extraKwargs    map[string]any
	fallbackModels []FallbackModel
	connectOptions *APIConnectOptions
	streams        map[*inferenceSTTStream]struct{}
	apiKey         string
	apiSecret      string
	baseURL        string
	dialWebsocket  inferenceSTTDialer
}

type STTOption func(*STT)

type inferenceSTTDialer func(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error)

func WithSTTModel(model string) STTOption {
	return func(s *STT) {
		modelName, language := sttModelAndLanguage(model, "")
		s.model = modelName
		if language != "" {
			s.language = language
		}
	}
}

func WithSTTLanguage(language string) STTOption {
	return func(s *STT) {
		s.language = language
	}
}

func WithSTTFallbackModels(models ...FallbackModel) STTOption {
	return func(s *STT) {
		s.fallbackModels = cloneSTTFallbackModels(models)
	}
}

func WithSTTExtraKwargs(extra map[string]any) STTOption {
	return func(s *STT) {
		if len(extra) == 0 {
			return
		}
		if s.extraKwargs == nil {
			s.extraKwargs = make(map[string]any, len(extra))
		}
		for key, value := range extra {
			s.extraKwargs[key] = value
		}
	}
}

func WithSTTEncoding(encoding string) STTOption {
	return func(s *STT) {
		s.encoding = encoding
	}
}

func WithSTTSampleRate(sampleRate int) STTOption {
	return func(s *STT) {
		s.sampleRate = sampleRate
	}
}

func WithSTTConnectOptions(options APIConnectOptions) STTOption {
	return func(s *STT) {
		s.connectOptions = &options
	}
}

func NewSTT(model string, apiKey, apiSecret string, opts ...STTOption) *STT {
	if model == "" {
		model = "deepgram/nova-3"
	}
	model, language := sttModelAndLanguage(model, "")
	apiKey, apiSecret = resolveInferenceCredentials(apiKey, apiSecret)
	s := &STT{
		model:         model,
		language:      language,
		encoding:      "pcm_s16le",
		sampleRate:    16000,
		apiKey:        apiKey,
		apiSecret:     apiSecret,
		baseURL:       defaultInferenceWebsocketURL(),
		dialWebsocket: defaultInferenceSTTDialer,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *STT) UpdateOptions(opts ...STTOption) {
	s.mu.Lock()
	oldModel := s.model
	oldLanguage := s.language
	oldExtra := cloneSTTExtra(s.extraKwargs)
	for _, opt := range opts {
		opt(s)
	}
	updateSettings := sttUpdateSettings(oldModel, oldLanguage, oldExtra, s.model, s.language, s.extraKwargs)
	streams := make([]*inferenceSTTStream, 0, len(s.streams))
	if len(updateSettings) > 0 {
		for stream := range s.streams {
			streams = append(streams, stream)
		}
	}
	s.mu.Unlock()

	for _, stream := range streams {
		stream.updateOptions(updateSettings)
	}
}

func (s *STT) Label() string {
	return "livekit.STT"
}

func (s *STT) Model() string {
	return s.model
}

func (s *STT) Provider() string {
	return "livekit"
}

func (s *STT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    true,
		Diarization:       sttDiarizationEnabled(s.extraKwargs),
		AlignedTranscript: "word",
		OfflineRecognize:  false,
	}
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("LiveKit Inference STT does not support batch recognition, use stream() instead")
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	token, err := CreateAccessToken(s.apiKey, s.apiSecret, InferenceAccessTokenTTL)
	if err != nil {
		return nil, err
	}
	if language == "" {
		language = s.language
	}

	modelName, createParams := sttSessionCreateParams(s.model, language, s.encoding, s.sampleRate, s.extraKwargs, s.fallbackModels, s.connectOptions)

	wsURL, err := url.Parse(s.baseURL + "/stt")
	if err != nil {
		return nil, err
	}

	q := wsURL.Query()
	q.Set("model", modelName)
	wsURL.RawQuery = q.Encode()

	header := InferenceHeaders()
	header.Add("Authorization", "Bearer "+token)

	conn, err := s.dialWebsocket(ctx, wsURL.String(), header)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to LiveKit Inference STT: %w", err)
	}

	if err := conn.WriteJSON(createParams); err != nil {
		conn.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	stream := &inferenceSTTStream{
		stt:       s,
		conn:      conn,
		ctx:       ctx,
		cancel:    cancel,
		language:  language,
		requestID: cavosmath.ShortUUID("stt_request_"),
		audioCh:   make(chan *model.AudioFrame, 100),
		eventCh:   make(chan *stt.SpeechEvent, 100),
	}

	s.registerStream(stream)
	go stream.run()

	return stream, nil
}

func (s *STT) registerStream(stream *inferenceSTTStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = make(map[*inferenceSTTStream]struct{})
	}
	s.streams[stream] = struct{}{}
}

func (s *STT) unregisterStream(stream *inferenceSTTStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func defaultInferenceSTTDialer(ctx context.Context, endpoint string, header http.Header) (inferenceWebsocketConn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, header)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func sttSessionCreateParams(model string, language string, encoding string, sampleRate int, extra map[string]any, fallback []FallbackModel, connectOptions *APIConnectOptions) (string, map[string]interface{}) {
	modelName, language := sttModelAndLanguage(model, language)
	if encoding == "" {
		encoding = "pcm_s16le"
	}
	if sampleRate == 0 {
		sampleRate = 16000
	}
	settings := map[string]interface{}{
		"sample_rate": strconv.Itoa(sampleRate),
		"encoding":    encoding,
		"extra":       sttExtraPayload(extra),
	}
	if language != "" {
		settings["language"] = language
	}

	createParams := map[string]interface{}{
		"type":     "session.create",
		"settings": settings,
	}
	if modelName != "auto" {
		createParams["model"] = modelName
	}
	if len(fallback) > 0 {
		createParams["fallback"] = map[string]interface{}{
			"models": sttFallbackModelsPayload(fallback),
		}
	}
	if connectOptions != nil {
		createParams["connection"] = map[string]interface{}{
			"timeout": connectOptions.Timeout.Seconds(),
			"retries": connectOptions.MaxRetry,
		}
	}
	return modelName, createParams
}

func sttFallbackModelsPayload(models []FallbackModel) []map[string]interface{} {
	payload := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		payload = append(payload, map[string]interface{}{
			"model": model.Model,
			"extra": sttExtraPayload(model.ExtraKwargs),
		})
	}
	return payload
}

func sttExtraPayload(extra map[string]any) map[string]interface{} {
	if len(extra) == 0 {
		return map[string]interface{}{}
	}
	payload := make(map[string]interface{}, len(extra))
	for key, value := range extra {
		payload[key] = value
	}
	return payload
}

func cloneSTTFallbackModels(models []FallbackModel) []FallbackModel {
	if len(models) == 0 {
		return nil
	}
	cloned := make([]FallbackModel, 0, len(models))
	for _, model := range models {
		model.ExtraKwargs = cloneSTTExtra(model.ExtraKwargs)
		cloned = append(cloned, model)
	}
	return cloned
}

func cloneSTTExtra(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(extra))
	for key, value := range extra {
		cloned[key] = value
	}
	return cloned
}

func sttUpdateSettings(oldModel string, oldLanguage string, oldExtra map[string]any, model string, language string, extra map[string]any) map[string]interface{} {
	settings := map[string]interface{}{}
	if model != oldModel {
		settings["model"] = model
	}
	if language != oldLanguage {
		settings["language"] = language
	}
	extraUpdate := sttChangedExtra(oldExtra, extra)
	if len(extraUpdate) > 0 {
		settings["extra"] = extraUpdate
	}
	return settings
}

func sttChangedExtra(oldExtra map[string]any, extra map[string]any) map[string]any {
	changed := map[string]any{}
	for key, value := range extra {
		oldValue, ok := oldExtra[key]
		if !ok || !reflect.DeepEqual(oldValue, value) {
			changed[key] = value
		}
	}
	return changed
}

func sttDiarizationEnabled(extra map[string]any) bool {
	for _, key := range []string{"diarize", "speaker_labels", "diarization"} {
		value, ok := extra[key]
		if !ok || !sttExtraTruthy(value) {
			continue
		}
		if text, ok := value.(string); ok && strings.EqualFold(text, "none") {
			continue
		}
		return true
	}
	return false
}

func sttExtraTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case int:
		return typed != 0
	case int8:
		return typed != 0
	case int16:
		return typed != 0
	case int32:
		return typed != 0
	case int64:
		return typed != 0
	case uint:
		return typed != 0
	case uint8:
		return typed != 0
	case uint16:
		return typed != 0
	case uint32:
		return typed != 0
	case uint64:
		return typed != 0
	case float32:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return true
	}
}

func sttModelAndLanguage(model string, language string) (string, string) {
	modelName := model
	if idx := strings.LastIndex(model, ":"); idx != -1 {
		if language == "" {
			language = model[idx+1:]
		}
		modelName = model[:idx]
	}
	return modelName, language
}

type inferenceSTTStream struct {
	stt             *STT
	conn            inferenceWebsocketConn
	ctx             context.Context
	cancel          context.CancelFunc
	audioCh         chan *model.AudioFrame
	eventCh         chan *stt.SpeechEvent
	requestID       string
	language        string
	mu              sync.Mutex
	closed          bool
	inputEnded      bool
	rateGuard       stt.SampleRateGuard
	speaking        bool
	audioDuration   float64
	startTimeOffset float64
	startTime       float64
}

type inferenceWebsocketConn interface {
	WriteJSON(v interface{}) error
	ReadMessage() (messageType int, p []byte, err error)
	Close() error
}

func (s *inferenceSTTStream) StartTimeOffset() float64 {
	return s.startTimeOffset
}

func (s *inferenceSTTStream) SetStartTimeOffset(offset float64) {
	s.startTimeOffset = offset
}

func (s *inferenceSTTStream) StartTime() float64 {
	return s.startTime
}

func (s *inferenceSTTStream) SetStartTime(startTime float64) {
	s.startTime = startTime
}

func (s *inferenceSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if err := s.rateGuard.Check(frame); err != nil {
		return err
	}
	s.audioDuration += audio.CalculateFrameDuration(frame)
	s.audioCh <- frame
	return nil
}

func (s *inferenceSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}

	return s.flushLocked()
}

func (s *inferenceSTTStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	s.inputEnded = true

	return s.flushLocked()
}

func (s *inferenceSTTStream) flushLocked() error {
	endPkt := map[string]interface{}{
		"type": "session.finalize",
	}
	return s.conn.WriteJSON(endPkt)
}

func (s *inferenceSTTStream) updateOptions(settings map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(settings) == 0 {
		return
	}
	_ = s.conn.WriteJSON(map[string]interface{}{
		"type":     "session.update",
		"settings": settings,
	})
}

func (s *inferenceSTTStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel := s.cancel
	conn := s.conn
	audioCh := s.audioCh
	eventCh := s.eventCh
	parent := s.stt
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		conn.Close()
	}
	if audioCh != nil {
		close(audioCh)
	}
	if eventCh != nil {
		close(eventCh)
	}
	if parent != nil {
		parent.unregisterStream(s)
	}
	return nil
}

func (s *inferenceSTTStream) Next() (*stt.SpeechEvent, error) {
	ev, ok := <-s.eventCh
	if !ok {
		return nil, context.Canceled
	}
	return ev, nil
}

func (s *inferenceSTTStream) buildSpeechData(data map[string]interface{}) stt.SpeechData {
	speechData := stt.SpeechData{
		Text:       stringFromMap(data, "transcript"),
		Language:   s.transcriptLanguage(data),
		SpeakerID:  stringFromMap(data, "speaker_id"),
		Confidence: floatFromMapDefault(data, "confidence", 1.0),
	}

	start := floatFromMap(data, "start")
	speechData.StartTime = s.startTimeOffset + start
	speechData.EndTime = s.startTimeOffset + start + floatFromMap(data, "duration")

	if extra, ok := data["extra"].(map[string]interface{}); ok && len(extra) > 0 {
		speechData.Metadata = extra
	}

	if words, ok := data["words"].([]interface{}); ok && len(words) > 0 {
		speechData.Words = make([]stt.TimedString, 0, len(words))
		for _, rawWord := range words {
			word, ok := rawWord.(map[string]interface{})
			if !ok {
				continue
			}
			speechData.Words = append(speechData.Words, stt.TimedString{
				Text:            stringFromMap(word, "word"),
				StartTime:       s.startTimeOffset + floatFromMap(word, "start"),
				EndTime:         s.startTimeOffset + floatFromMap(word, "end"),
				StartTimeOffset: s.startTimeOffset,
				Confidence:      floatFromMap(word, "confidence"),
				SpeakerID:       stringFromMap(word, "speaker_id"),
			})
		}
	}

	return speechData
}

func (s *inferenceSTTStream) transcriptLanguage(data map[string]interface{}) string {
	if language := stringFromMap(data, "language"); language != "" {
		return language
	}
	if s.language != "" {
		return s.language
	}
	return "en"
}

func (s *inferenceSTTStream) transcriptRequestID(data map[string]interface{}) string {
	if requestID := stringFromMap(data, "request_id"); requestID != "" {
		return requestID
	}
	return s.requestID
}

func stringFromMap(data map[string]interface{}, key string) string {
	value, _ := data[key].(string)
	return value
}

func floatFromMap(data map[string]interface{}, key string) float64 {
	value, _ := data[key].(float64)
	return value
}

func floatFromMapDefault(data map[string]interface{}, key string, fallback float64) float64 {
	value, ok := data[key].(float64)
	if !ok {
		return fallback
	}
	return value
}

func (s *inferenceSTTStream) run() {
	defer s.Close()

	// Send loop
	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			case frame, ok := <-s.audioCh:
				if !ok {
					return
				}

				base64Audio := base64.StdEncoding.EncodeToString(frame.Data)
				audioMsg := map[string]interface{}{
					"type":  "input_audio",
					"audio": base64Audio,
				}

				s.mu.Lock()
				if s.closed {
					s.mu.Unlock()
					return
				}
				err := s.conn.WriteJSON(audioMsg)
				s.mu.Unlock()

				if err != nil {
					return
				}
			}
		}
	}()

	// Read loop
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			_, msg, err := s.conn.ReadMessage()
			if err != nil {
				logger.Logger.Errorw("LiveKit Inference STT disconnected", err)
				return
			}

			var ev map[string]interface{}
			if err := json.Unmarshal(msg, &ev); err != nil {
				continue
			}

			evType, _ := ev["type"].(string)
			switch evType {
			case "session.created", "session.finalized", "session.closed":
				// ignore
			case "interim_transcript", "final_transcript":
				s.processTranscript(ev, evType == "final_transcript")
			case "preflight_transcript":
				s.processPreflightTranscript(ev)
			case "error":
				logger.Logger.Errorw("LiveKit Inference STT error", nil, "msg", string(msg))
			}
		}
	}
}

func (s *inferenceSTTStream) processPreflightTranscript(data map[string]interface{}) {
	text, _ := data["transcript"].(string)
	if text == "" || !s.speaking {
		return
	}

	requestID := s.transcriptRequestID(data)
	speechData := s.buildSpeechData(data)
	s.eventCh <- &stt.SpeechEvent{
		Type:         stt.SpeechEventPreflightTranscript,
		RequestID:    requestID,
		Alternatives: []stt.SpeechData{speechData},
	}
}

func (s *inferenceSTTStream) processTranscript(data map[string]interface{}, isFinal bool) {
	text, _ := data["transcript"].(string)
	requestID := s.transcriptRequestID(data)

	if text == "" && !isFinal {
		return
	}

	if !s.speaking {
		s.speaking = true
		s.eventCh <- &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}
	}

	speechData := s.buildSpeechData(data)

	if isFinal {
		s.mu.Lock()
		duration := s.audioDuration
		if duration > 0 {
			s.audioDuration = 0
		}
		s.mu.Unlock()

		if duration > 0 {
			s.eventCh <- &stt.SpeechEvent{
				Type:      stt.SpeechEventRecognitionUsage,
				RequestID: requestID,
				RecognitionUsage: &stt.RecognitionUsage{
					AudioDuration: duration,
				},
			}
		}

		s.eventCh <- &stt.SpeechEvent{
			Type:         stt.SpeechEventFinalTranscript,
			RequestID:    requestID,
			Alternatives: []stt.SpeechData{speechData},
		}

		if s.speaking {
			s.speaking = false
			s.eventCh <- &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}
		}
	} else {
		s.eventCh <- &stt.SpeechEvent{
			Type:         stt.SpeechEventInterimTranscript,
			RequestID:    requestID,
			Alternatives: []stt.SpeechData{speechData},
		}
	}
}
