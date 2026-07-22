package simplismart

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultSimplismartSTTBaseURL    = "https://api.simplismart.live/predict"
	defaultSimplismartSTTModel      = "openai/whisper-large-v3-turbo"
	defaultSimplismartSTTLanguage   = "en"
	defaultSimplismartSTTTask       = "transcribe"
	defaultSimplismartSTTSampleRate = 16000
	simplismartSTTChunkDurationMS   = 50
	simplismartAPIKeyEnv            = "SIMPLISMART_API_KEY"
)

type STT struct {
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

type STTOption func(*STT)

func WithSimplismartSTTBaseURL(baseURL string) STTOption {
	return func(s *STT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
			if s.streaming {
				s.baseURL = simplismartSTTWebsocketURL(s.baseURL)
			}
		}
	}
}

func WithSimplismartSTTStreaming(streaming bool) STTOption {
	return func(s *STT) {
		s.streaming = streaming
		if streaming {
			s.baseURL = simplismartSTTWebsocketURL(s.baseURL)
		} else if strings.HasPrefix(s.baseURL, "ws://") || strings.HasPrefix(s.baseURL, "wss://") {
			s.baseURL = defaultSimplismartSTTBaseURL
		}
	}
}

func WithSimplismartSTTModel(model string) STTOption {
	return func(s *STT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithSimplismartSTTLanguage(language string) STTOption {
	return func(s *STT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSimplismartSTTTask(task string) STTOption {
	return func(s *STT) {
		if task != "" {
			s.task = task
		}
	}
}

func WithSimplismartSTTWithoutTimestamps(withoutTimestamps bool) STTOption {
	return func(s *STT) {
		s.withoutTimestamps = withoutTimestamps
	}
}

func WithSimplismartSTTHotwords(hotwords string) STTOption {
	return func(s *STT) {
		s.hotwords = hotwords
	}
}

func WithSimplismartSTTNumSpeakers(numSpeakers int) STTOption {
	return func(s *STT) {
		if numSpeakers >= 0 {
			s.numSpeakers = numSpeakers
		}
	}
}

func NewSTT(apiKey string, opts ...STTOption) *STT {
	if apiKey == "" {
		apiKey = os.Getenv(simplismartAPIKeyEnv)
	}
	vadOnset := 0.5
	compressionRatioThreshold := 2.4
	maxTokens := 400.0
	logProbThreshold := -1.0
	provider := &STT{
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

func (s *STT) Label() string { return "simplismart.STT" }
func (s *STT) Model() string { return s.model }
func (s *STT) Provider() string {
	return "Simplismart"
}
func (s *STT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return defaultSimplismartSTTSampleRate
	}
	return uint32(s.sampleRate)
}

func (s *STT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:         s.streaming,
		InterimResults:    false,
		Diarization:       s.numSpeakers > 0,
		AlignedTranscript: "word",
		OfflineRecognize:  true,
	}
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if !s.streaming {
		return nil, fmt.Errorf("simplismart streaming stt requires streaming mode enabled")
	}
	if err := validateSimplismartSTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildSimplismartSTTStreamURL(s), buildSimplismartSTTHeaders(s))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial simplismart stt websocket: %v", err))
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
		conn:         conn,
		events:       make(chan *stt.SpeechEvent, 100),
		errCh:        make(chan error, 1),
		ctx:          streamCtx,
		cancel:       cancel,
		requestID:    fmt.Sprintf("%p", conn),
		language:     resolveSimplismartSTTLanguage(s, language),
		audioBStream: newSimplismartSTTAudioByteStream(),
	}
	go stream.readLoop()
	return stream, nil
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if err := validateSimplismartSTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	req, err := buildSimplismartSTTRecognizeRequest(ctx, s, simplismartSTTWAVBytes(frames), language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, simplismartHTTPTransportError("Simplismart STT request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, llm.NewAPIStatusError("Simplismart STT request failed", resp.StatusCode, "", string(respBody))
	}
	var result simplismartSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return simplismartSTTSpeechEvent(resolveSimplismartSTTLanguage(s, language), result), nil
}

func simplismartHTTPTransportError(prefix string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(err.Error())
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return llm.NewAPITimeoutError(err.Error())
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("%s: %v", prefix, err))
}

func simplismartSTTWAVBytes(frames []*model.AudioFrame) []byte {
	var pcm bytes.Buffer
	sampleRate := uint32(defaultSimplismartSTTSampleRate)
	numChannels := uint32(1)
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate > 0 && pcm.Len() == 0 {
			sampleRate = frame.SampleRate
		}
		if frame.NumChannels > 0 && pcm.Len() == 0 {
			numChannels = frame.NumChannels
		}
		pcm.Write(frame.Data)
	}
	data := pcm.Bytes()
	dataSize := uint32(len(data))
	blockAlign := uint16(numChannels * 2)
	byteRate := sampleRate * numChannels * 2
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36)+dataSize)
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(&wav, binary.LittleEndian, sampleRate)
	_ = binary.Write(&wav, binary.LittleEndian, byteRate)
	_ = binary.Write(&wav, binary.LittleEndian, blockAlign)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, dataSize)
	wav.Write(data)
	return wav.Bytes()
}

func validateSimplismartSTTAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("%s is not set", simplismartAPIKeyEnv)
	}
	return nil
}

func buildSimplismartSTTRecognizeRequest(ctx context.Context, s *STT, audio []byte, language string) (*http.Request, error) {
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

func simplismartSTTRequestPayload(s *STT, audio []byte, language string) map[string]interface{} {
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

func simplismartSTTHTTPURL(s *STT) string {
	if strings.HasPrefix(s.baseURL, "ws://") || strings.HasPrefix(s.baseURL, "wss://") {
		return defaultSimplismartSTTBaseURL
	}
	return s.baseURL
}

func buildSimplismartSTTStreamURL(s *STT) string {
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

func buildSimplismartSTTHeaders(s *STT) http.Header {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+s.apiKey)
	return headers
}

func buildSimplismartSTTInitialConfig(language string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{"language": language})
}

func resolveSimplismartSTTLanguage(s *STT, language string) string {
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
	data.Confidence = stt.DefaultTranscriptConfidence(data.Text)
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

	audioBStream *audio.AudioByteStream
}

func (s *simplismartSTTStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || err == io.EOF {
				if !s.isClosed() {
					s.errCh <- llm.NewAPIConnectionError("Simplismart STT WebSocket closed unexpectedly")
				}
			} else {
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

func (s *simplismartSTTStream) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *simplismartSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.audioBStream == nil {
		s.audioBStream = newSimplismartSTTAudioByteStream()
	}
	return s.writeAudioFramesLocked(s.audioBStream.Write(frame.Data))
}

func (s *simplismartSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.audioBStream == nil {
		return nil
	}
	return s.writeAudioFramesLocked(s.audioBStream.Flush())
}

func (s *simplismartSTTStream) writeAudioFramesLocked(frames []*model.AudioFrame) error {
	for _, frame := range frames {
		if frame == nil || len(frame.Data) == 0 {
			continue
		}
		if err := s.conn.WriteMessage(websocket.BinaryMessage, frame.Data); err != nil {
			_ = s.closeLocked()
			return err
		}
	}
	return nil
}

func newSimplismartSTTAudioByteStream() *audio.AudioByteStream {
	samplesPerChannel := defaultSimplismartSTTSampleRate / (1000 / simplismartSTTChunkDurationMS)
	return audio.NewAudioByteStream(defaultSimplismartSTTSampleRate, 1, uint32(samplesPerChannel))
}

func (s *simplismartSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

func (s *simplismartSTTStream) closeLocked() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *simplismartSTTStream) Next() (*stt.SpeechEvent, error) {
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
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		return nil, err
	case <-s.ctx.Done():
		if s.isClosed() {
			return nil, io.EOF
		}
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

// Deprecated: use STT.
type SimplismartSTT = STT

// Deprecated: use STTOption.
type SimplismartSTTOption = STTOption

// Deprecated: use NewSTT.
func NewSimplismartSTT(apiKey string, opts ...STTOption) *STT {
	return NewSTT(apiKey, opts...)
}
