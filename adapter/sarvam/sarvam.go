package sarvam

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultSarvamSTTBaseURL            = "https://api.sarvam.ai/speech-to-text"
	defaultSarvamSTTStreamingURL       = "wss://api.sarvam.ai/speech-to-text/ws"
	defaultSarvamSTTTranslateBaseURL   = "https://api.sarvam.ai/speech-to-text-translate"
	defaultSarvamSTTTranslateWSURL     = "wss://api.sarvam.ai/speech-to-text-translate/ws"
	defaultSarvamSTTModel              = "saarika:v2.5"
	defaultSarvamSTTLanguage           = "en-IN"
	defaultSarvamSTTMode               = "transcribe"
	defaultSarvamSTTSampleRate         = 16000
	defaultSarvamTTSBaseURL            = "https://api.sarvam.ai/text-to-speech"
	defaultSarvamTTSWSURL              = "wss://api.sarvam.ai/text-to-speech/ws"
	defaultSarvamTTSModel              = "bulbul:v3"
	defaultSarvamTTSLanguage           = "en-IN"
	defaultSarvamTTSSampleRate         = 22050
	defaultSarvamTTSPace               = 1.0
	defaultSarvamTTSPitch              = 0.0
	defaultSarvamTTSLoudness           = 1.0
	defaultSarvamTTSTemperature        = 0.6
	defaultSarvamTTSOutputAudioBitrate = "128k"
	defaultSarvamTTSMinBufferSize      = 50
	defaultSarvamTTSMaxChunkLength     = 150
	defaultSarvamTTSOutputAudioCodec   = "mp3"
	sarvamUserAgent                    = "LiveKit Agents Sarvam Plugin/Go"
)

var (
	sarvamAllowedModes = map[string]struct{}{
		"transcribe": {},
		"translate":  {},
		"verbatim":   {},
		"translit":   {},
		"codemix":    {},
	}
	sarvamSaarikaV25Languages = map[string]struct{}{
		"unknown": {}, "hi-IN": {}, "bn-IN": {}, "kn-IN": {}, "ml-IN": {}, "mr-IN": {},
		"od-IN": {}, "pa-IN": {}, "ta-IN": {}, "te-IN": {}, "en-IN": {}, "gu-IN": {},
	}
	sarvamSaarasV3Languages = map[string]struct{}{
		"unknown": {}, "hi-IN": {}, "bn-IN": {}, "kn-IN": {}, "ml-IN": {}, "mr-IN": {},
		"od-IN": {}, "pa-IN": {}, "ta-IN": {}, "te-IN": {}, "en-IN": {}, "gu-IN": {},
		"as-IN": {}, "ur-IN": {}, "ne-IN": {}, "kok-IN": {}, "ks-IN": {}, "sd-IN": {},
		"sa-IN": {}, "sat-IN": {}, "mni-IN": {}, "brx-IN": {}, "mai-IN": {}, "doi-IN": {},
	}
)

type SarvamSTT struct {
	apiKey                     string
	baseURL                    string
	streamingURL               string
	baseURLSet                 bool
	streamingSet               bool
	model                      string
	language                   string
	mode                       string
	prompt                     string
	sampleRate                 int
	highVADSensitivity         *bool
	flushSignal                *bool
	inputAudioCodec            string
	positiveSpeechThreshold    *float64
	negativeSpeechThreshold    *float64
	minSpeechFrames            *int
	firstTurnMinSpeechFrames   *int
	negativeFramesCount        *int
	negativeFramesWindow       *int
	startSpeechVolumeThreshold *float64
	interruptMinSpeechFrames   *int
	preSpeechPadFrames         *int
	numInitialIgnoredFrames    *int
}

type SarvamSTTOption func(*SarvamSTT)

func WithSarvamSTTBaseURL(baseURL string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
			s.baseURLSet = true
		}
	}
}

func WithSarvamSTTStreamingURL(streamingURL string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if streamingURL != "" {
			s.streamingURL = strings.TrimRight(streamingURL, "/")
			s.streamingSet = true
		}
	}
}

func WithSarvamSTTModel(model string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if model != "" {
			s.model = model
			if sarvamSTTUsesTranslateEndpoint(model) {
				if !s.baseURLSet {
					s.baseURL = defaultSarvamSTTTranslateBaseURL
				}
				if !s.streamingSet {
					s.streamingURL = defaultSarvamSTTTranslateWSURL
				}
			} else {
				if !s.baseURLSet {
					s.baseURL = defaultSarvamSTTBaseURL
				}
				if !s.streamingSet {
					s.streamingURL = defaultSarvamSTTStreamingURL
				}
			}
		}
	}
}

func WithSarvamSTTLanguage(language string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSarvamSTTMode(mode string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if mode != "" {
			s.mode = mode
		}
	}
}

func WithSarvamSTTPrompt(prompt string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.prompt = prompt
	}
}

func WithSarvamSTTSampleRate(sampleRate int) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithSarvamSTTHighVADSensitivity(enabled bool) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.highVADSensitivity = &enabled
	}
}

func WithSarvamSTTFlushSignal(enabled bool) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.flushSignal = &enabled
	}
}

func WithSarvamSTTInputAudioCodec(codec string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if codec != "" {
			s.inputAudioCodec = codec
		}
	}
}

func WithSarvamSTTPositiveSpeechThreshold(threshold float64) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.positiveSpeechThreshold = &threshold
	}
}

func WithSarvamSTTNegativeSpeechThreshold(threshold float64) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.negativeSpeechThreshold = &threshold
	}
}

func WithSarvamSTTMinSpeechFrames(frames int) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.minSpeechFrames = &frames
	}
}

func WithSarvamSTTFirstTurnMinSpeechFrames(frames int) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.firstTurnMinSpeechFrames = &frames
	}
}

func WithSarvamSTTNegativeFramesCount(frames int) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.negativeFramesCount = &frames
	}
}

func WithSarvamSTTNegativeFramesWindow(frames int) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.negativeFramesWindow = &frames
	}
}

func WithSarvamSTTStartSpeechVolumeThreshold(threshold float64) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.startSpeechVolumeThreshold = &threshold
	}
}

func WithSarvamSTTInterruptMinSpeechFrames(frames int) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.interruptMinSpeechFrames = &frames
	}
}

func WithSarvamSTTPreSpeechPadFrames(frames int) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.preSpeechPadFrames = &frames
	}
}

func WithSarvamSTTNumInitialIgnoredFrames(frames int) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.numInitialIgnoredFrames = &frames
	}
}

func NewSarvamSTT(apiKey string, opts ...SarvamSTTOption) *SarvamSTT {
	provider, _ := NewSarvamSTTWithError(apiKey, opts...)
	return provider
}

func NewSarvamSTTWithError(apiKey string, opts ...SarvamSTTOption) (*SarvamSTT, error) {
	provider := &SarvamSTT{
		apiKey:       resolveSarvamAPIKey(apiKey),
		baseURL:      defaultSarvamSTTBaseURL,
		streamingURL: defaultSarvamSTTStreamingURL,
		model:        defaultSarvamSTTModel,
		language:     defaultSarvamSTTLanguage,
		mode:         defaultSarvamSTTMode,
		sampleRate:   defaultSarvamSTTSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if err := validateSarvamSTTOptions(provider.model, provider.language, provider.mode); err != nil {
		return nil, err
	}
	return provider, nil
}

func (s *SarvamSTT) Label() string { return "sarvam.STT" }
func (s *SarvamSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: true}
}

func (s *SarvamSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	requestLanguage := resolveSarvamSTTLanguage(s, language)
	if err := validateSarvamSTTOptions(s.model, requestLanguage, s.mode); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildSarvamSTTWebsocketURL(s, language).String(), buildSarvamSTTWebsocketHeaders(s))
	if err != nil {
		return nil, fmt.Errorf("failed to dial sarvam stt websocket: %w", err)
	}
	if sarvamSTTSupportsPrompt(s.model) && s.prompt != "" {
		configMessage, err := buildSarvamSTTConfigMessage(s)
		if err != nil {
			conn.Close()
			return nil, err
		}
		if err := conn.WriteMessage(websocket.TextMessage, configMessage); err != nil {
			conn.Close()
			return nil, err
		}
	}
	stream := newSarvamSTTRecognizeStream(ctx, conn, s, requestLanguage)
	go stream.readLoop()
	return stream, nil
}

func buildSarvamSTTWebsocketURL(s *SarvamSTT, language string) *url.URL {
	wsURL, err := url.Parse(strings.TrimRight(s.streamingURL, "/"))
	if err != nil {
		return &url.URL{Scheme: "wss", Host: strings.TrimPrefix(s.streamingURL, "wss://")}
	}
	query := wsURL.Query()
	query.Set("language-code", resolveSarvamSTTLanguage(s, language))
	query.Set("model", s.model)
	query.Set("vad_signals", "true")
	if s.sampleRate > 0 {
		query.Set("sample_rate", strconv.Itoa(s.sampleRate))
	}
	if sarvamSTTSupportsMode(s.model) {
		query.Set("mode", s.mode)
	}
	if s.highVADSensitivity != nil {
		query.Set("high_vad_sensitivity", strconv.FormatBool(*s.highVADSensitivity))
	}
	if s.flushSignal != nil {
		query.Set("flush_signal", strconv.FormatBool(*s.flushSignal))
	}
	if s.inputAudioCodec != "" {
		query.Set("input_audio_codec", s.inputAudioCodec)
	}
	if sarvamSTTSupportsVADParams(s.model) {
		setSarvamFloatQuery(query, "positive_speech_threshold", s.positiveSpeechThreshold)
		setSarvamFloatQuery(query, "negative_speech_threshold", s.negativeSpeechThreshold)
		setSarvamIntQuery(query, "min_speech_frames", s.minSpeechFrames)
		setSarvamIntQuery(query, "first_turn_min_speech_frames", s.firstTurnMinSpeechFrames)
		setSarvamIntQuery(query, "negative_frames_count", s.negativeFramesCount)
		setSarvamIntQuery(query, "negative_frames_window", s.negativeFramesWindow)
		setSarvamFloatQuery(query, "start_speech_volume_threshold", s.startSpeechVolumeThreshold)
		setSarvamIntQuery(query, "interrupt_min_speech_frames", s.interruptMinSpeechFrames)
		setSarvamIntQuery(query, "pre_speech_pad_frames", s.preSpeechPadFrames)
		setSarvamIntQuery(query, "num_initial_ignored_frames", s.numInitialIgnoredFrames)
	}
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func setSarvamFloatQuery(query url.Values, key string, value *float64) {
	if value != nil {
		query.Set(key, strconv.FormatFloat(*value, 'f', -1, 64))
	}
}

func setSarvamIntQuery(query url.Values, key string, value *int) {
	if value != nil {
		query.Set(key, strconv.Itoa(*value))
	}
}

func buildSarvamSTTWebsocketHeaders(s *SarvamSTT) http.Header {
	headers := make(http.Header)
	headers.Set("api-subscription-key", s.apiKey)
	headers.Set("User-Agent", sarvamUserAgent)
	return headers
}

func buildSarvamSTTConfigMessage(s *SarvamSTT) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type":   "config",
		"prompt": s.prompt,
	})
}

func buildSarvamSTTAudioMessage(frame *model.AudioFrame, encoding string) ([]byte, error) {
	if frame == nil {
		return json.Marshal(map[string]any{
			"audio": map[string]any{
				"data":        "",
				"encoding":    encoding,
				"sample_rate": 0,
			},
		})
	}
	return json.Marshal(map[string]any{
		"audio": map[string]any{
			"data":        base64.StdEncoding.EncodeToString(frame.Data),
			"encoding":    encoding,
			"sample_rate": int(frame.SampleRate),
		},
	})
}

func buildSarvamSTTEndOfStreamMessage(encoding string, sampleRate int) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type": "end_of_stream",
		"audio": map[string]any{
			"data":        "",
			"encoding":    encoding,
			"sample_rate": sampleRate,
		},
	})
}

func (s *SarvamSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	var audio bytes.Buffer
	for _, f := range frames {
		audio.Write(f.Data)
	}
	req, err := buildSarvamSTTRecognizeRequest(ctx, s, audio.Bytes(), language)
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
		return nil, fmt.Errorf("sarvam stt error: %s", string(respBody))
	}
	var result sarvamSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return sarvamSTTSpeechEvent(resolveSarvamSTTLanguage(s, language), result), nil
}

func buildSarvamSTTRecognizeRequest(ctx context.Context, s *SarvamSTT, audio []byte, language string) (*http.Request, error) {
	requestLanguage := resolveSarvamSTTLanguage(s, language)
	if err := validateSarvamSTTOptions(s.model, requestLanguage, s.mode); err != nil {
		return nil, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="audio.wav"`)
	header.Set("Content-Type", "audio/wav")
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	if requestLanguage != "" {
		if err := writer.WriteField("language_code", requestLanguage); err != nil {
			return nil, err
		}
	}
	if s.model != "" {
		if err := writer.WriteField("model", s.model); err != nil {
			return nil, err
		}
	}
	if sarvamSTTSupportsMode(s.model) {
		if err := writer.WriteField("mode", s.mode); err != nil {
			return nil, err
		}
	}
	if s.prompt != "" && strings.HasPrefix(s.model, "saaras") {
		if err := writer.WriteField("prompt", s.prompt); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("api-subscription-key", s.apiKey)
	req.Header.Set("User-Agent", "LiveKit Agents Sarvam Plugin/Go")
	return req, nil
}

type sarvamSTTResponse struct {
	Transcript          string              `json:"transcript"`
	RequestID           string              `json:"request_id"`
	LanguageCode        string              `json:"language_code"`
	LanguageProbability float64             `json:"language_probability"`
	Timestamps          sarvamSTTTimestamps `json:"timestamps"`
}

type sarvamSTTTimestamps struct {
	StartTimeSeconds []float64 `json:"start_time_seconds"`
	EndTimeSeconds   []float64 `json:"end_time_seconds"`
}

func sarvamSTTSpeechEvent(defaultLanguage string, resp sarvamSTTResponse) *stt.SpeechEvent {
	language := resp.LanguageCode
	if language == "" {
		language = defaultLanguage
	}
	return &stt.SpeechEvent{
		Type:      stt.SpeechEventFinalTranscript,
		RequestID: resp.RequestID,
		Alternatives: []stt.SpeechData{
			{
				Text:       resp.Transcript,
				Language:   language,
				StartTime:  sarvamSTTStartTime(resp.Timestamps),
				EndTime:    sarvamSTTEndTime(resp.Timestamps),
				Confidence: sarvamSTTConfidence(resp.LanguageProbability),
			},
		},
	}
}

func resolveSarvamSTTLanguage(s *SarvamSTT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

func validateSarvamSTTOptions(model, language, mode string) error {
	if err := validateSarvamSTTLanguage(model, language); err != nil {
		return err
	}
	if _, ok := sarvamAllowedModes[mode]; mode != "" && !ok {
		return fmt.Errorf("mode must be one of codemix, transcribe, translate, translit, verbatim")
	}
	if !sarvamSTTSupportsMode(model) && mode != "" && mode != defaultSarvamSTTMode {
		return fmt.Errorf("mode is not supported for model %s", model)
	}
	return nil
}

func validateSarvamSTTLanguage(model, language string) error {
	if language == "" {
		return nil
	}
	allowed := sarvamSaarasV3Languages
	if model == "saarika:v2.5" || model == "saaras:v2.5" {
		allowed = sarvamSaarikaV25Languages
	}
	if _, ok := allowed[language]; !ok {
		return fmt.Errorf("language %s is not supported for model %s", language, model)
	}
	return nil
}

func sarvamSTTSupportsMode(model string) bool {
	return model == "saaras:v3"
}

func sarvamSTTSupportsVADParams(model string) bool {
	return model == "saaras:v3"
}

func sarvamSTTSupportsPrompt(model string) bool {
	return strings.HasPrefix(model, "saaras")
}

func sarvamSTTUsesTranslateEndpoint(model string) bool {
	return model == "saaras:v2.5"
}

func sarvamSTTStartTime(timestamps sarvamSTTTimestamps) float64 {
	if len(timestamps.StartTimeSeconds) == 0 {
		return 0
	}
	return timestamps.StartTimeSeconds[0]
}

func sarvamSTTEndTime(timestamps sarvamSTTTimestamps) float64 {
	if len(timestamps.EndTimeSeconds) == 0 {
		return 0
	}
	return timestamps.EndTimeSeconds[len(timestamps.EndTimeSeconds)-1]
}

func sarvamSTTConfidence(probability float64) float64 {
	if probability == 0 {
		return 1.0
	}
	return probability
}

type sarvamSTTRecognizeStream struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	language   string
	encoding   string
	sampleRate int
	events     chan *stt.SpeechEvent
	errCh      chan error
	mu         sync.Mutex
	closed     bool
}

func newSarvamSTTRecognizeStream(ctx context.Context, conn *websocket.Conn, provider *SarvamSTT, language string) *sarvamSTTRecognizeStream {
	streamCtx, cancel := context.WithCancel(ctx)
	encoding := "audio/wav"
	if provider.inputAudioCodec != "" {
		encoding = provider.inputAudioCodec
	}
	return &sarvamSTTRecognizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		language:   language,
		encoding:   encoding,
		sampleRate: provider.sampleRate,
		events:     make(chan *stt.SpeechEvent, 100),
		errCh:      make(chan error, 1),
	}
}

func (s *sarvamSTTRecognizeStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	sampleRate := s.sampleRate
	if frame.SampleRate > 0 {
		sampleRate = int(frame.SampleRate)
	}
	chunkBytes := sarvamSTTChunkBytes(sampleRate)
	for start := 0; start < len(frame.Data); start += chunkBytes {
		end := start + chunkBytes
		if end > len(frame.Data) {
			end = len(frame.Data)
		}
		chunk := *frame
		chunk.Data = frame.Data[start:end]
		chunk.SampleRate = uint32(sampleRate)
		chunk.SamplesPerChannel = uint32(len(chunk.Data) / 2)
		message, err := buildSarvamSTTAudioMessage(&chunk, s.encoding)
		if err != nil {
			return err
		}
		if err := s.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			return err
		}
	}
	return nil
}

func (s *sarvamSTTRecognizeStream) Flush() error {
	message, err := buildSarvamSTTEndOfStreamMessage(s.encoding, s.sampleRate)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *sarvamSTTRecognizeStream) Close() error {
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

func (s *sarvamSTTRecognizeStream) Next() (*stt.SpeechEvent, error) {
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

func (s *sarvamSTTRecognizeStream) readLoop() {
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
		events, err := sarvamSTTEventsFromStreamMessage(payload, s.language)
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
			if event.Type == stt.SpeechEventEndOfSpeech {
				_ = s.Flush()
			}
		}
	}
}

func sarvamSTTChunkBytes(sampleRate int) int {
	if sampleRate <= 0 {
		sampleRate = defaultSarvamSTTSampleRate
	}
	samplesPerChunk := sampleRate * 50 / 1000
	if samplesPerChunk < 1 {
		samplesPerChunk = 1
	}
	return samplesPerChunk * 2
}

func sarvamSTTEventsFromStreamMessage(payload []byte, defaultLanguage string) ([]*stt.SpeechEvent, error) {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, err
	}
	messageType, _ := message["type"].(string)
	switch messageType {
	case "data":
		data := sarvamMap(message["data"])
		transcript, _ := data["transcript"].(string)
		if transcript == "" {
			return nil, nil
		}
		requestID := sarvamString(data["request_id"])
		events := []*stt.SpeechEvent{}
		if metrics := sarvamMap(data["metrics"]); len(metrics) > 0 {
			events = append(events, &stt.SpeechEvent{
				Type:      stt.SpeechEventRecognitionUsage,
				RequestID: requestID,
				RecognitionUsage: &stt.RecognitionUsage{
					AudioDuration: sarvamFloat64(metrics["audio_duration"]),
				},
			})
		}
		events = append(events, &stt.SpeechEvent{
			Type:      stt.SpeechEventFinalTranscript,
			RequestID: requestID,
			Alternatives: []stt.SpeechData{{
				Text:       transcript,
				Language:   sarvamSTTStreamLanguage(data, defaultLanguage),
				StartTime:  sarvamFloat64(data["speech_start"]),
				EndTime:    sarvamFloat64(data["speech_end"]),
				Confidence: sarvamSTTConfidence(sarvamFloat64(data["language_probability"])),
			}},
		})
		return events, nil
	case "events", "event":
		data := sarvamMap(message["data"])
		if sarvamStreamMessageHasError(message) {
			return nil, sarvamSTTStreamError(message)
		}
		switch sarvamString(data["signal_type"]) {
		case "START_SPEECH":
			return []*stt.SpeechEvent{{Type: stt.SpeechEventStartOfSpeech}}, nil
		case "END_SPEECH":
			return []*stt.SpeechEvent{{Type: stt.SpeechEventEndOfSpeech}}, nil
		default:
			return nil, nil
		}
	case "error", "errors":
		return nil, sarvamSTTStreamError(message)
	default:
		if sarvamStreamMessageHasError(message) {
			return nil, sarvamSTTStreamError(message)
		}
		return nil, nil
	}
}

func sarvamStreamMessageHasError(message map[string]any) bool {
	if message["error"] != nil {
		return true
	}
	data := sarvamMap(message["data"])
	return data["error"] != nil || data["event_type"] == "error" || data["event"] == "error"
}

func sarvamSTTStreamError(message map[string]any) error {
	data := sarvamMap(message["data"])
	errorText := sarvamString(message["error"])
	if errorText == "" {
		errorText = sarvamString(data["error"])
	}
	if errorText == "" {
		errorText = sarvamString(data["message"])
	}
	if errorText == "" {
		errorText = "unknown sarvam stt stream error"
	}
	return fmt.Errorf("sarvam stt stream error: %s", errorText)
}

func sarvamMap(value any) map[string]any {
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return nil
}

func sarvamString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func sarvamSTTStreamLanguage(data map[string]any, defaultLanguage string) string {
	if language := sarvamString(data["language_code"]); language != "" {
		return language
	}
	return defaultLanguage
}

func sarvamFloat64(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

type SarvamTTS struct {
	apiKey                string
	baseURL               string
	wsURL                 string
	voice                 string
	language              string
	model                 string
	sampleRate            int
	pitch                 float64
	pace                  float64
	loudness              float64
	temperature           float64
	outputAudioBitrate    string
	minBufferSize         int
	maxChunkLength        int
	enablePreprocessing   bool
	enableCachedResponses *bool
	dictID                string
	outputAudioCodec      string
}

type SarvamTTSOption func(*SarvamTTS)

func WithSarvamTTSBaseURL(baseURL string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSarvamTTSWSURL(wsURL string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if wsURL != "" {
			t.wsURL = strings.TrimRight(wsURL, "/")
		}
	}
}

func WithSarvamTTSModel(model string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if model != "" {
			t.model = model
			if t.voice == "" {
				t.voice = defaultSarvamTTSVoice(model)
			}
		}
	}
}

func WithSarvamTTSVoice(voice string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithSarvamTTSLanguage(language string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithSarvamTTSSampleRate(sampleRate int) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithSarvamTTSTemperature(temperature float64) SarvamTTSOption {
	return func(t *SarvamTTS) {
		t.temperature = temperature
	}
}

func WithSarvamTTSPitch(pitch float64) SarvamTTSOption {
	return func(t *SarvamTTS) {
		t.pitch = pitch
	}
}

func WithSarvamTTSPace(pace float64) SarvamTTSOption {
	return func(t *SarvamTTS) {
		t.pace = pace
	}
}

func WithSarvamTTSLoudness(loudness float64) SarvamTTSOption {
	return func(t *SarvamTTS) {
		t.loudness = loudness
	}
}

func WithSarvamTTSOutputAudioBitrate(bitrate string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if bitrate != "" {
			t.outputAudioBitrate = bitrate
		}
	}
}

func WithSarvamTTSMinBufferSize(size int) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if size > 0 {
			t.minBufferSize = size
		}
	}
}

func WithSarvamTTSMaxChunkLength(length int) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if length > 0 {
			t.maxChunkLength = length
		}
	}
}

func WithSarvamTTSEnablePreprocessing(enabled bool) SarvamTTSOption {
	return func(t *SarvamTTS) {
		t.enablePreprocessing = enabled
	}
}

func WithSarvamTTSEnableCachedResponses(enabled bool) SarvamTTSOption {
	return func(t *SarvamTTS) {
		t.enableCachedResponses = &enabled
	}
}

func WithSarvamTTSDictID(dictID string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		t.dictID = dictID
	}
}

func WithSarvamTTSOutputAudioCodec(codec string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if codec != "" {
			t.outputAudioCodec = codec
		}
	}
}

func NewSarvamTTS(apiKey string, voice string, opts ...SarvamTTSOption) *SarvamTTS {
	provider := &SarvamTTS{
		apiKey:             resolveSarvamAPIKey(apiKey),
		baseURL:            defaultSarvamTTSBaseURL,
		wsURL:              defaultSarvamTTSWSURL,
		voice:              voice,
		language:           defaultSarvamTTSLanguage,
		model:              defaultSarvamTTSModel,
		sampleRate:         defaultSarvamTTSSampleRate,
		pitch:              defaultSarvamTTSPitch,
		pace:               defaultSarvamTTSPace,
		loudness:           defaultSarvamTTSLoudness,
		temperature:        defaultSarvamTTSTemperature,
		outputAudioBitrate: defaultSarvamTTSOutputAudioBitrate,
		minBufferSize:      defaultSarvamTTSMinBufferSize,
		maxChunkLength:     defaultSarvamTTSMaxChunkLength,
		outputAudioCodec:   defaultSarvamTTSOutputAudioCodec,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultSarvamTTSVoice(provider.model)
	}
	return provider
}

func (t *SarvamTTS) Label() string { return "sarvam.TTS" }
func (t *SarvamTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *SarvamTTS) SampleRate() int  { return t.sampleRate }
func (t *SarvamTTS) NumChannels() int { return 1 }

func (t *SarvamTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildSarvamTTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("sarvam tts error: %s", string(respBody))
	}
	return &sarvamTTSChunkedStream{resp: resp, sampleRate: t.sampleRate, outputAudioCodec: t.outputAudioCodec}, nil
}

func buildSarvamTTSRequest(ctx context.Context, t *SarvamTTS, text string) (*http.Request, error) {
	payload := map[string]interface{}{
		"target_language_code": t.language,
		"text":                 text,
		"speaker":              t.voice,
		"pace":                 t.pace,
		"speech_sample_rate":   t.sampleRate,
		"model":                t.model,
		"output_audio_bitrate": t.outputAudioBitrate,
		"min_buffer_size":      t.minBufferSize,
		"max_chunk_length":     t.maxChunkLength,
		"output_audio_codec":   t.outputAudioCodec,
	}
	if t.model == "bulbul:v2" {
		payload["pitch"] = t.pitch
		payload["loudness"] = t.loudness
		payload["enable_preprocessing"] = t.enablePreprocessing
		if t.enableCachedResponses != nil {
			payload["enable_cached_responses"] = *t.enableCachedResponses
		}
	}
	if t.model == "bulbul:v3" || t.model == "bulbul:v3-beta" {
		payload["temperature"] = t.temperature
	}
	if t.model == "bulbul:v3" && t.dictID != "" {
		payload["dict_id"] = t.dictID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-subscription-key", t.apiKey)
	req.Header.Set("User-Agent", "LiveKit Agents Sarvam Plugin/Go")
	return req, nil
}

func (t *SarvamTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildSarvamTTSWebsocketURL(t).String(), buildSarvamTTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial sarvam tts websocket: %w", err)
	}
	configMessage, err := buildSarvamTTSConfigMessage(t)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, configMessage); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &sarvamTTSSynthesizeStream{
		conn:             conn,
		ctx:              streamCtx,
		cancel:           cancel,
		sampleRate:       t.sampleRate,
		outputAudioCodec: t.outputAudioCodec,
		events:           make(chan *tts.SynthesizedAudio, 100),
		errCh:            make(chan error, 1),
	}
	go stream.readLoop()
	return stream, nil
}

func buildSarvamTTSWebsocketURL(t *SarvamTTS) *url.URL {
	wsURL, err := url.Parse(strings.TrimRight(t.wsURL, "/"))
	if err != nil {
		return &url.URL{Scheme: "wss", Host: strings.TrimPrefix(t.wsURL, "wss://")}
	}
	query := wsURL.Query()
	query.Set("model", t.model)
	query.Set("send_completion_event", "true")
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func buildSarvamTTSWebsocketHeaders(t *SarvamTTS) http.Header {
	headers := make(http.Header)
	headers.Set("api-subscription-key", t.apiKey)
	headers.Set("User-Agent", sarvamUserAgent)
	headers.Set("Accept", "*/*")
	headers.Set("Accept-Encoding", "gzip, deflate, br")
	return headers
}

func buildSarvamTTSConfigMessage(t *SarvamTTS) ([]byte, error) {
	data := sarvamTTSConfigPayload(t)
	return json.Marshal(map[string]interface{}{
		"type": "config",
		"data": data,
	})
}

func sarvamTTSConfigPayload(t *SarvamTTS) map[string]interface{} {
	data := map[string]interface{}{
		"target_language_code": t.language,
		"speaker":              t.voice,
		"pace":                 t.pace,
		"model":                t.model,
		"speech_sample_rate":   t.sampleRate,
		"output_audio_codec":   t.outputAudioCodec,
	}
	if t.model == "bulbul:v2" {
		data["pitch"] = t.pitch
		data["loudness"] = t.loudness
		data["enable_preprocessing"] = t.enablePreprocessing
		if t.enableCachedResponses != nil {
			data["enable_cached_responses"] = *t.enableCachedResponses
		}
	}
	if t.model == "bulbul:v3" || t.model == "bulbul:v3-beta" {
		data["temperature"] = t.temperature
		data["output_audio_bitrate"] = t.outputAudioBitrate
		data["min_buffer_size"] = t.minBufferSize
		data["max_chunk_length"] = t.maxChunkLength
	}
	if t.model == "bulbul:v3" && t.dictID != "" {
		data["dict_id"] = t.dictID
	}
	return data
}

func buildSarvamTTSTextMessage(text string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"type": "text",
		"data": map[string]interface{}{"text": text},
	})
}

func buildSarvamTTSFlushMessage() ([]byte, error) {
	return json.Marshal(map[string]interface{}{"type": "flush"})
}

type sarvamTTSChunkedStream struct {
	resp             *http.Response
	sampleRate       int
	outputAudioCodec string
	read             bool
}

func (s *sarvamTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.read {
		return nil, io.EOF
	}
	s.read = true
	var result struct {
		RequestID string   `json:"request_id"`
		Audios    []string `json:"audios"`
	}
	if err := json.NewDecoder(s.resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Audios) == 0 {
		return nil, io.EOF
	}
	data, err := base64.StdEncoding.DecodeString(result.Audios[0])
	if err != nil {
		return nil, err
	}
	return sarvamTTSAudioFrame(data, s.sampleRate, result.RequestID, s.outputAudioCodec), nil
}

func (s *sarvamTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func defaultSarvamTTSVoice(model string) string {
	if model == "bulbul:v3" || model == "bulbul:v3-beta" {
		return "shubh"
	}
	return "anushka"
}

type sarvamTTSSynthesizeStream struct {
	conn             *websocket.Conn
	ctx              context.Context
	cancel           context.CancelFunc
	sampleRate       int
	outputAudioCodec string
	events           chan *tts.SynthesizedAudio
	errCh            chan error
	mu               sync.Mutex
	closed           bool
}

func (s *sarvamTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	message, err := buildSarvamTTSTextMessage(text)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *sarvamTTSSynthesizeStream) Flush() error {
	message, err := buildSarvamTTSFlushMessage()
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *sarvamTTSSynthesizeStream) Close() error {
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

func (s *sarvamTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case audio, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return audio, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *sarvamTTSSynthesizeStream) readLoop() {
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
		audio, done, err := sarvamTTSAudioFromStreamMessage(payload, s.sampleRate, s.outputAudioCodec)
		if err != nil {
			s.errCh <- err
			return
		}
		if audio != nil {
			s.events <- audio
		}
		if done {
			return
		}
	}
}

func sarvamTTSAudioFromStreamMessage(payload []byte, sampleRate int, outputAudioCodec string) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Type string `json:"type"`
		Data struct {
			Audio     string `json:"audio"`
			EventType string `json:"event_type"`
			RequestID string `json:"request_id"`
			Message   string `json:"message"`
			Code      string `json:"code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	switch message.Type {
	case "audio":
		if message.Data.Audio == "" {
			return nil, false, nil
		}
		data, err := base64.StdEncoding.DecodeString(message.Data.Audio)
		if err != nil {
			return nil, false, err
		}
		if len(data) == 0 {
			return nil, false, nil
		}
		return sarvamTTSAudioFrame(data, sampleRate, message.Data.RequestID, outputAudioCodec), false, nil
	case "event":
		return nil, message.Data.EventType == "final", nil
	case "error":
		return nil, false, fmt.Errorf("sarvam tts stream error: %s", string(payload))
	default:
		return nil, false, nil
	}
}

func sarvamTTSAudioFrame(data []byte, sampleRate int, requestID string, outputAudioCodec string) *tts.SynthesizedAudio {
	frameData := sarvamTTSDecodeTelephony(outputAudioCodec, data)
	return &tts.SynthesizedAudio{
		RequestID: requestID,
		Frame: &model.AudioFrame{
			Data:              frameData,
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(frameData) / 2),
		},
	}
}

func sarvamTTSDecodeTelephony(codec string, data []byte) []byte {
	switch codec {
	case "mulaw":
		return sarvamTTSDecodeMuLaw(data)
	case "alaw":
		return sarvamTTSDecodeALaw(data)
	default:
		return bytes.Clone(data)
	}
}

func sarvamTTSDecodeMuLaw(data []byte) []byte {
	pcm := make([]byte, len(data)*2)
	for i, encoded := range data {
		u := ^encoded
		sign := 1
		if u&0x80 != 0 {
			sign = -1
		}
		exponent := int((u >> 4) & 0x07)
		mantissa := int(u & 0x0f)
		sample := ((mantissa << 3) + 0x84) << exponent
		value := int16(sign * (sample - 0x84))
		pcm[i*2] = byte(value)
		pcm[i*2+1] = byte(value >> 8)
	}
	return pcm
}

func sarvamTTSDecodeALaw(data []byte) []byte {
	pcm := make([]byte, len(data)*2)
	for i, encoded := range data {
		a := encoded ^ 0x55
		sign := -1
		if a&0x80 != 0 {
			sign = 1
		}
		exponent := int((a >> 4) & 0x07)
		mantissa := int(a & 0x0f)
		sample := 0
		if exponent == 0 {
			sample = (mantissa << 4) + 8
		} else {
			sample = ((mantissa << 4) + 0x108) << (exponent - 1)
		}
		value := int16(sign * sample)
		pcm[i*2] = byte(value)
		pcm[i*2+1] = byte(value >> 8)
	}
	return pcm
}
