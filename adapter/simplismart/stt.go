package simplismart

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

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultSimplismartSTTBaseURL    = "https://api.simplismart.live/predict"
	defaultSimplismartSTTModel      = "openai/whisper-large-v3-turbo"
	defaultSimplismartSTTLanguage   = "en"
	defaultSimplismartSTTTask       = "transcribe"
	defaultSimplismartSTTSampleRate = 16000
)

type SimplismartSTT struct {
	apiKey                       string
	baseURL                      string
	model                        string
	language                     string
	task                         string
	streaming                    bool
	withoutTimestamps            bool
	vadModel                     string
	vadFilter                    bool
	vadOnset                     *float64
	vadOffset                    *float64
	minSpeechDurationMS          int
	maxSpeechDurationS           float64
	minSilenceDurationMS         int
	speechPadMS                  int
	initialPrompt                string
	hotwords                     string
	numSpeakers                  int
	compressionRatioThreshold    *float64
	beamSize                     int
	temperature                  float64
	multilingual                 bool
	maxTokens                    *float64
	logProbThreshold             *float64
	lengthPenalty                int
	repetitionPenalty            float64
	strictHallucinationReduction bool
	sampleRate                   int
}

type SimplismartSTTOption func(*SimplismartSTT)

func WithSimplismartSTTBaseURL(baseURL string) SimplismartSTTOption {
	return func(s *SimplismartSTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
			if s.streaming {
				s.baseURL = simplismartSTTWebsocketURL(s.baseURL)
			}
		}
	}
}

func WithSimplismartSTTStreaming(streaming bool) SimplismartSTTOption {
	return func(s *SimplismartSTT) {
		s.streaming = streaming
		if streaming {
			s.baseURL = simplismartSTTWebsocketURL(s.baseURL)
		} else if strings.HasPrefix(s.baseURL, "ws://") || strings.HasPrefix(s.baseURL, "wss://") {
			s.baseURL = defaultSimplismartSTTBaseURL
		}
	}
}

func WithSimplismartSTTModel(model string) SimplismartSTTOption {
	return func(s *SimplismartSTT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithSimplismartSTTLanguage(language string) SimplismartSTTOption {
	return func(s *SimplismartSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSimplismartSTTTask(task string) SimplismartSTTOption {
	return func(s *SimplismartSTT) {
		if task != "" {
			s.task = task
		}
	}
}

func WithSimplismartSTTWithoutTimestamps(withoutTimestamps bool) SimplismartSTTOption {
	return func(s *SimplismartSTT) {
		s.withoutTimestamps = withoutTimestamps
	}
}

func WithSimplismartSTTHotwords(hotwords string) SimplismartSTTOption {
	return func(s *SimplismartSTT) {
		s.hotwords = hotwords
	}
}

func WithSimplismartSTTNumSpeakers(numSpeakers int) SimplismartSTTOption {
	return func(s *SimplismartSTT) {
		if numSpeakers >= 0 {
			s.numSpeakers = numSpeakers
		}
	}
}

func NewSimplismartSTT(apiKey string, opts ...SimplismartSTTOption) *SimplismartSTT {
	vadOnset := 0.5
	compressionRatioThreshold := 2.4
	maxTokens := 400.0
	logProbThreshold := -1.0
	provider := &SimplismartSTT{
		apiKey:                       apiKey,
		baseURL:                      defaultSimplismartSTTBaseURL,
		model:                        defaultSimplismartSTTModel,
		language:                     defaultSimplismartSTTLanguage,
		task:                         defaultSimplismartSTTTask,
		withoutTimestamps:            true,
		vadModel:                     "frame",
		vadFilter:                    true,
		vadOnset:                     &vadOnset,
		minSpeechDurationMS:          0,
		maxSpeechDurationS:           30,
		minSilenceDurationMS:         2000,
		speechPadMS:                  400,
		numSpeakers:                  0,
		compressionRatioThreshold:    &compressionRatioThreshold,
		beamSize:                     4,
		temperature:                  0,
		maxTokens:                    &maxTokens,
		logProbThreshold:             &logProbThreshold,
		lengthPenalty:                1,
		repetitionPenalty:            1.01,
		strictHallucinationReduction: false,
		sampleRate:                   defaultSimplismartSTTSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *SimplismartSTT) Label() string { return "simplismart.STT" }

func (s *SimplismartSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:         s.streaming,
		InterimResults:    false,
		Diarization:       s.numSpeakers > 0,
		AlignedTranscript: "word",
		OfflineRecognize:  true,
	}
}

func (s *SimplismartSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if !s.streaming {
		return nil, fmt.Errorf("simplismart streaming stt requires streaming mode enabled")
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildSimplismartSTTStreamURL(s), buildSimplismartSTTHeaders(s))
	if err != nil {
		return nil, fmt.Errorf("failed to dial simplismart stt websocket: %w", err)
	}
	config, err := buildSimplismartSTTInitialConfig(resolveSimplismartSTTLanguage(s, language))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, config); err != nil {
		_ = conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &simplismartSTTStream{
		conn:      conn,
		events:    make(chan *stt.SpeechEvent, 100),
		errCh:     make(chan error, 1),
		ctx:       streamCtx,
		cancel:    cancel,
		requestID: fmt.Sprintf("%p", conn),
		language:  resolveSimplismartSTTLanguage(s, language),
	}
	go stream.readLoop()
	return stream, nil
}

func (s *SimplismartSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	var audio bytes.Buffer
	for _, frame := range frames {
		audio.Write(frame.Data)
	}
	req, err := buildSimplismartSTTRecognizeRequest(ctx, s, audio.Bytes(), language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("simplismart stt error: %s", string(respBody))
	}
	var result simplismartSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return simplismartSTTSpeechEvent(resolveSimplismartSTTLanguage(s, language), result), nil
}

func buildSimplismartSTTRecognizeRequest(ctx context.Context, s *SimplismartSTT, audio []byte, language string) (*http.Request, error) {
	body, err := json.Marshal(simplismartSTTRequestPayload(s, audio, language))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, simplismartSTTHTTPURL(s), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	return req, nil
}

func simplismartSTTRequestPayload(s *SimplismartSTT, audio []byte, language string) map[string]interface{} {
	payload := map[string]interface{}{
		"audio_data":                     base64.StdEncoding.EncodeToString(audio),
		"language":                       resolveSimplismartSTTLanguage(s, language),
		"model":                          s.model,
		"task":                           s.task,
		"without_timestamps":             s.withoutTimestamps,
		"vad_model":                      s.vadModel,
		"vad_filter":                     s.vadFilter,
		"min_speech_duration_ms":         s.minSpeechDurationMS,
		"max_speech_duration_s":          s.maxSpeechDurationS,
		"min_silence_duration_ms":        s.minSilenceDurationMS,
		"speech_pad_ms":                  s.speechPadMS,
		"num_speakers":                   s.numSpeakers,
		"beam_size":                      s.beamSize,
		"temperature":                    s.temperature,
		"multilingual":                   s.multilingual,
		"length_penalty":                 s.lengthPenalty,
		"repetition_penalty":             s.repetitionPenalty,
		"strict_hallucination_reduction": s.strictHallucinationReduction,
	}
	setOptionalFloat(payload, "vad_onset", s.vadOnset)
	setOptionalFloat(payload, "vad_offset", s.vadOffset)
	setOptionalFloat(payload, "compression_ratio_threshold", s.compressionRatioThreshold)
	setOptionalFloat(payload, "max_tokens", s.maxTokens)
	setOptionalFloat(payload, "log_prob_threshold", s.logProbThreshold)
	if s.initialPrompt != "" {
		payload["initial_prompt"] = s.initialPrompt
	}
	if s.hotwords != "" {
		payload["hotwords"] = s.hotwords
	}
	return payload
}

func setOptionalFloat(payload map[string]interface{}, key string, value *float64) {
	if value != nil {
		payload[key] = *value
	}
}

func simplismartSTTHTTPURL(s *SimplismartSTT) string {
	if strings.HasPrefix(s.baseURL, "ws://") || strings.HasPrefix(s.baseURL, "wss://") {
		return defaultSimplismartSTTBaseURL
	}
	return s.baseURL
}

func buildSimplismartSTTStreamURL(s *SimplismartSTT) string {
	return simplismartSTTWebsocketURL(s.baseURL)
}

func simplismartSTTWebsocketURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "wss://api.simplismart.live/ws/audio"
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	}
	parsed.Path = "/ws/audio"
	parsed.RawQuery = ""
	return parsed.String()
}

func buildSimplismartSTTHeaders(s *SimplismartSTT) http.Header {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+s.apiKey)
	return headers
}

func buildSimplismartSTTInitialConfig(language string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{"language": language})
}

func resolveSimplismartSTTLanguage(s *SimplismartSTT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

type simplismartSTTResponse struct {
	RequestID     string             `json:"request_id"`
	Transcription []string           `json:"transcription"`
	Timestamps    [][2]float64       `json:"timestamps"`
	Info          simplismartSTTInfo `json:"info"`
}

type simplismartSTTInfo struct {
	Language string `json:"language"`
}

func simplismartSTTSpeechEvent(defaultLanguage string, resp simplismartSTTResponse) *stt.SpeechEvent {
	language := resp.Info.Language
	if language == "" {
		language = defaultLanguage
	}
	data := stt.SpeechData{
		Language: language,
		Text:     strings.Join(resp.Transcription, ""),
	}
	if len(resp.Timestamps) > 0 {
		data.StartTime = resp.Timestamps[0][0]
		data.EndTime = resp.Timestamps[len(resp.Timestamps)-1][1]
	}
	return &stt.SpeechEvent{
		Type:         stt.SpeechEventFinalTranscript,
		RequestID:    resp.RequestID,
		Alternatives: []stt.SpeechData{data},
	}
}

type simplismartSTTStream struct {
	conn      *websocket.Conn
	events    chan *stt.SpeechEvent
	errCh     chan error
	mu        sync.Mutex
	closed    bool
	ctx       context.Context
	cancel    context.CancelFunc
	requestID string
	language  string
}

func (s *simplismartSTTStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
			}
			return
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		for _, event := range simplismartSTTStreamEvents(s.requestID, s.language, payload) {
			s.events <- event
		}
	}
}

func (s *simplismartSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, frame.Data)
}

func (s *simplismartSTTStream) Flush() error {
	return nil
}

func (s *simplismartSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *simplismartSTTStream) Next() (*stt.SpeechEvent, error) {
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

func simplismartSTTStreamEvents(requestID string, language string, payload []byte) []*stt.SpeechEvent {
	text := string(payload)
	if text == "" {
		return nil
	}
	requestData, _ := json.Marshal(map[string]interface{}{
		"original_id":        requestID,
		"processing_latency": 0.0,
	})
	return []*stt.SpeechEvent{
		{
			Type:      stt.SpeechEventRecognitionUsage,
			RequestID: string(requestData),
			RecognitionUsage: &stt.RecognitionUsage{
				AudioDuration: 0,
			},
		},
		{
			Type:      stt.SpeechEventFinalTranscript,
			RequestID: requestID,
			Alternatives: []stt.SpeechData{
				{Language: language, Text: text},
			},
		},
	}
}
