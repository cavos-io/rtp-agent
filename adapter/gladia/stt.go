package gladia

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultGladiaBaseURL                           = "https://api.gladia.io/v2/live"
	defaultGladiaModel                             = "solaria-1"
	defaultGladiaSampleRate                        = 16000
	defaultGladiaBitDepth                          = 16
	defaultGladiaChannels                          = 1
	defaultGladiaEndpointing                       = 0.05
	defaultGladiaMaximumDurationWithoutEndpointing = 5.0
	defaultGladiaRegion                            = "eu-west"
	defaultGladiaEncoding                          = "wav/pcm"
	defaultGladiaTranslationModel                  = "base"
	defaultGladiaPreProcessingSpeechThreshold      = 0.6
)

type GladiaSTT struct {
	apiKey                             string
	baseURL                            string
	model                              string
	languages                          []string
	codeSwitching                      bool
	interimResults                     bool
	sampleRate                         int
	bitDepth                           int
	channels                           int
	endpointing                        float64
	maximumDurationWithoutEndpointing  float64
	region                             string
	encoding                           string
	translationEnabled                 bool
	translationTargetLanguages         []string
	translationModel                   string
	translationMatchOriginalUtterances bool
	translationLipsync                 bool
	translationContextAdaptation       bool
	translationContext                 string
	translationInformal                bool
	customVocabulary                   []any
	customSpelling                     map[string][]string
	preProcessingAudioEnhancer         bool
	preProcessingSpeechThreshold       float64
}

type GladiaSTTOption func(*GladiaSTT)

func WithGladiaBaseURL(baseURL string) GladiaSTTOption {
	return func(s *GladiaSTT) {
		if baseURL != "" {
			s.baseURL = baseURL
		}
	}
}

func WithGladiaModel(model string) GladiaSTTOption {
	return func(s *GladiaSTT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithGladiaInterimResults(interimResults bool) GladiaSTTOption {
	return func(s *GladiaSTT) {
		s.interimResults = interimResults
	}
}

func WithGladiaLanguages(languages []string) GladiaSTTOption {
	return func(s *GladiaSTT) {
		s.languages = append([]string(nil), languages...)
	}
}

func WithGladiaCodeSwitching(codeSwitching bool) GladiaSTTOption {
	return func(s *GladiaSTT) {
		s.codeSwitching = codeSwitching
	}
}

func WithGladiaAudioFormat(sampleRate int, bitDepth int, channels int, encoding string) GladiaSTTOption {
	return func(s *GladiaSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
		if bitDepth > 0 {
			s.bitDepth = bitDepth
		}
		if channels > 0 {
			s.channels = channels
		}
		if encoding != "" {
			s.encoding = encoding
		}
	}
}

func WithGladiaEndpointing(endpointing float64, maximumDurationWithoutEndpointing float64) GladiaSTTOption {
	return func(s *GladiaSTT) {
		if endpointing >= 0 {
			s.endpointing = endpointing
		}
		if maximumDurationWithoutEndpointing > 0 {
			s.maximumDurationWithoutEndpointing = maximumDurationWithoutEndpointing
		}
	}
}

func WithGladiaRegion(region string) GladiaSTTOption {
	return func(s *GladiaSTT) {
		if region != "" {
			s.region = region
		}
	}
}

func WithGladiaCustomVocabulary(vocabulary []any) GladiaSTTOption {
	return func(s *GladiaSTT) {
		s.customVocabulary = append([]any(nil), vocabulary...)
	}
}

func WithGladiaCustomSpelling(spelling map[string][]string) GladiaSTTOption {
	return func(s *GladiaSTT) {
		if spelling == nil {
			s.customSpelling = nil
			return
		}
		copied := make(map[string][]string, len(spelling))
		for word, variants := range spelling {
			copied[word] = append([]string(nil), variants...)
		}
		s.customSpelling = copied
	}
}

func WithGladiaTranslation(targetLanguages []string) GladiaSTTOption {
	return func(s *GladiaSTT) {
		s.translationEnabled = true
		s.translationTargetLanguages = append([]string(nil), targetLanguages...)
	}
}

func WithGladiaTranslationConfig(targetLanguages []string, model string, matchOriginalUtterances bool, lipsync bool, contextAdaptation bool, context string, informal bool) GladiaSTTOption {
	return func(s *GladiaSTT) {
		s.translationEnabled = true
		s.translationTargetLanguages = append([]string(nil), targetLanguages...)
		if model != "" {
			s.translationModel = model
		}
		s.translationMatchOriginalUtterances = matchOriginalUtterances
		s.translationLipsync = lipsync
		s.translationContextAdaptation = contextAdaptation
		s.translationContext = context
		s.translationInformal = informal
	}
}

func WithGladiaPreProcessing(audioEnhancer bool, speechThreshold float64) GladiaSTTOption {
	return func(s *GladiaSTT) {
		s.preProcessingAudioEnhancer = audioEnhancer
		s.preProcessingSpeechThreshold = speechThreshold
	}
}

func NewGladiaSTT(apiKey string, opts ...GladiaSTTOption) *GladiaSTT {
	if apiKey == "" {
		apiKey = os.Getenv("GLADIA_API_KEY")
	}
	provider := &GladiaSTT{
		apiKey:                             apiKey,
		baseURL:                            defaultGladiaBaseURL,
		model:                              defaultGladiaModel,
		codeSwitching:                      true,
		interimResults:                     true,
		sampleRate:                         defaultGladiaSampleRate,
		bitDepth:                           defaultGladiaBitDepth,
		channels:                           defaultGladiaChannels,
		endpointing:                        defaultGladiaEndpointing,
		maximumDurationWithoutEndpointing:  defaultGladiaMaximumDurationWithoutEndpointing,
		region:                             defaultGladiaRegion,
		encoding:                           defaultGladiaEncoding,
		translationModel:                   defaultGladiaTranslationModel,
		translationMatchOriginalUtterances: true,
		translationLipsync:                 true,
		preProcessingSpeechThreshold:       defaultGladiaPreProcessingSpeechThreshold,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *GladiaSTT) Label() string { return "gladia.STT" }
func (s *GladiaSTT) Model() string { return s.model }
func (s *GladiaSTT) Provider() string {
	return "Gladia"
}

func (s *GladiaSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    s.interimResults,
		Diarization:       false,
		AlignedTranscript: "word",
		OfflineRecognize:  false,
	}
}

func (s *GladiaSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := validateGladiaAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	provider := s.cloneWithLanguage(language)
	req, err := buildGladiaInitRequest(ctx, provider)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gladia session init error: %s", string(respBody))
	}
	var session struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, session.URL, nil)
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &gladiaSTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state: &gladiaSTTStreamState{
			requestID: session.ID,
			languages: provider.languages,
		},
	}
	go stream.readLoop()
	return stream, nil
}

func validateGladiaAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("gladia API key is required; set GLADIA_API_KEY or pass api_key")
	}
	return nil
}

func (s *GladiaSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	stream, err := s.Stream(ctx, language)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	for _, frame := range frames {
		if err := stream.PushFrame(frame); err != nil {
			return nil, err
		}
	}
	if err := stream.Flush(); err != nil {
		return nil, err
	}
	for {
		event, err := stream.Next()
		if err != nil {
			return nil, err
		}
		if event.Type == stt.SpeechEventFinalTranscript {
			return event, nil
		}
	}
}

func buildGladiaStreamingConfig(s *GladiaSTT) map[string]any {
	realtime := map[string]any{
		"words_accurate_timestamps": false,
	}
	if len(s.customVocabulary) > 0 {
		realtime["custom_vocabulary"] = true
		realtime["custom_vocabulary_config"] = map[string]any{"vocabulary": s.customVocabulary}
	}
	if len(s.customSpelling) > 0 {
		realtime["custom_spelling"] = true
		realtime["custom_spelling_config"] = map[string]any{"spelling_dictionary": s.customSpelling}
	}
	if s.translationEnabled {
		realtime["translation"] = true
		translationConfig := map[string]any{
			"target_languages":          s.translationTargetLanguages,
			"model":                     s.translationModel,
			"match_original_utterances": s.translationMatchOriginalUtterances,
			"lipsync":                   s.translationLipsync,
			"context_adaptation":        s.translationContextAdaptation,
			"informal":                  s.translationInformal,
		}
		if s.translationContext != "" {
			translationConfig["context"] = s.translationContext
		}
		realtime["translation_config"] = translationConfig
	}
	return map[string]any{
		"region":                               s.region,
		"encoding":                             s.encoding,
		"sample_rate":                          s.sampleRate,
		"model":                                s.model,
		"endpointing":                          s.endpointing,
		"maximum_duration_without_endpointing": s.maximumDurationWithoutEndpointing,
		"bit_depth":                            s.bitDepth,
		"channels":                             s.channels,
		"language_config": map[string]any{
			"languages":      append([]string(nil), s.languages...),
			"code_switching": s.codeSwitching,
		},
		"realtime_processing": realtime,
		"messages_config": map[string]any{
			"receive_partial_transcripts": s.interimResults,
			"receive_final_transcripts":   true,
		},
		"custom_metadata": map[string]any{
			"livekit": "go",
		},
		"pre_processing": map[string]any{
			"audio_enhancer":   s.preProcessingAudioEnhancer,
			"speech_threshold": s.preProcessingSpeechThreshold,
		},
	}
}

func buildGladiaInitRequest(ctx context.Context, s *GladiaSTT) (*http.Request, error) {
	config := buildGladiaStreamingConfig(s)
	region, _ := config["region"].(string)
	delete(config, "region")
	u, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("region", region)
	u.RawQuery = q.Encode()
	body, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gladia-Key", s.apiKey)
	return req, nil
}

func buildGladiaAudioChunkMessage(audio []byte) map[string]any {
	return map[string]any{
		"type": "audio_chunk",
		"data": map[string]any{
			"chunk": base64.StdEncoding.EncodeToString(audio),
		},
	}
}

func buildGladiaStopRecordingMessage() map[string]any {
	return map[string]any{"type": "stop_recording"}
}

func writeGladiaMessage(conn *websocket.Conn, message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func (s *GladiaSTT) cloneWithLanguage(language string) *GladiaSTT {
	clone := *s
	clone.languages = append([]string(nil), s.languages...)
	if language != "" {
		clone.languages = []string{language}
	}
	return &clone
}

type gladiaSTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	state  *gladiaSTTStreamState
	audio  *audio.AudioByteStream

	writeText func(map[string]any) error
}

func (s *gladiaSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	if s.audio == nil {
		s.audio = newGladiaAudioByteStream(frame)
	}
	if s.state == nil {
		s.state = &gladiaSTTStreamState{}
	}
	for _, chunk := range s.audio.Push(frame.Data) {
		if err := s.writeTextMessage(buildGladiaAudioChunkMessage(chunk.Data)); err != nil {
			return err
		}
		s.state.audioDuration += audio.CalculateFrameDuration(chunk)
	}
	return nil
}

func (s *gladiaSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.state == nil {
		s.state = &gladiaSTTStreamState{}
	}
	if s.audio != nil {
		for _, chunk := range s.audio.Flush() {
			if err := s.writeTextMessage(buildGladiaAudioChunkMessage(chunk.Data)); err != nil {
				return err
			}
			s.state.audioDuration += audio.CalculateFrameDuration(chunk)
		}
	}
	if err := s.writeTextMessage(buildGladiaStopRecordingMessage()); err != nil {
		return err
	}
	s.emitRecognitionUsage()
	return nil
}

func (s *gladiaSTTStream) writeTextMessage(message map[string]any) error {
	if s.writeText != nil {
		return s.writeText(message)
	}
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	return writeGladiaMessage(s.conn, message)
}

func (s *gladiaSTTStream) emitRecognitionUsage() {
	if s.events == nil || s.state == nil || s.state.audioDuration <= 0 {
		return
	}
	duration := s.state.audioDuration
	s.state.audioDuration = 0
	s.events <- &stt.SpeechEvent{
		Type:      stt.SpeechEventRecognitionUsage,
		RequestID: s.state.requestID,
		RecognitionUsage: &stt.RecognitionUsage{
			AudioDuration: duration,
		},
	}
}

func newGladiaAudioByteStream(frame *model.AudioFrame) *audio.AudioByteStream {
	sampleRate := frame.SampleRate
	if sampleRate == 0 {
		sampleRate = defaultGladiaSampleRate
	}
	numChannels := frame.NumChannels
	if numChannels == 0 {
		numChannels = defaultGladiaChannels
	}
	return audio.NewAudioByteStream(sampleRate, numChannels, 0)
}

func (s *gladiaSTTStream) Close() error {
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

func (s *gladiaSTTStream) Next() (*stt.SpeechEvent, error) {
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

func (s *gladiaSTTStream) readLoop() {
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
		var data map[string]any
		if err := json.Unmarshal(payload, &data); err != nil {
			s.errCh <- err
			return
		}
		events, err := processGladiaMessage(s.state, data)
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

type gladiaSTTStreamState struct {
	requestID     string
	languages     []string
	speaking      bool
	audioDuration float64
}

func processGladiaMessage(state *gladiaSTTStreamState, data map[string]any) ([]*stt.SpeechEvent, error) {
	messageType, _ := data["type"].(string)
	if messageType != "transcript" {
		if messageType == "error" {
			return nil, fmt.Errorf("gladia websocket error: %v", data["data"])
		}
		return nil, nil
	}
	payload, _ := data["data"].(map[string]any)
	utterance, _ := payload["utterance"].(map[string]any)
	text := gladiaAnyString(utterance["text"])
	if text == "" {
		return nil, nil
	}
	isFinal, _ := payload["is_final"].(bool)
	speechData := gladiaSpeechData(state, utterance)
	events := []*stt.SpeechEvent{}
	if !state.speaking {
		state.speaking = true
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech, RequestID: state.requestID})
	}
	if isFinal {
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, RequestID: state.requestID, Alternatives: []stt.SpeechData{speechData}})
		state.speaking = false
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech, RequestID: state.requestID})
		return events, nil
	}
	events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventInterimTranscript, RequestID: state.requestID, Alternatives: []stt.SpeechData{speechData}})
	return events, nil
}

func gladiaSpeechData(state *gladiaSTTStreamState, utterance map[string]any) stt.SpeechData {
	language := gladiaAnyString(utterance["language"])
	if language == "" && len(state.languages) > 0 {
		language = state.languages[0]
	}
	if language == "" {
		language = "en"
	}
	return stt.SpeechData{
		Language:   language,
		Text:       gladiaAnyString(utterance["text"]),
		StartTime:  gladiaAnyFloat(utterance["start"]),
		EndTime:    gladiaAnyFloat(utterance["end"]),
		Confidence: gladiaConfidence(utterance["confidence"]),
		Words:      gladiaWordsFromAny(utterance["words"]),
	}
}

func gladiaWordsFromAny(raw any) []stt.TimedString {
	rawWords, ok := raw.([]any)
	if !ok {
		return nil
	}
	words := make([]stt.TimedString, 0, len(rawWords))
	for _, rawWord := range rawWords {
		wordMap, ok := rawWord.(map[string]any)
		if !ok {
			continue
		}
		words = append(words, stt.TimedString{
			Text:      gladiaAnyString(wordMap["word"]),
			StartTime: gladiaAnyFloat(wordMap["start"]),
			EndTime:   gladiaAnyFloat(wordMap["end"]),
		})
	}
	return words
}

func gladiaAnyString(value any) string {
	str, _ := value.(string)
	return str
}

func gladiaAnyFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0
	}
}

func gladiaConfidence(value any) float64 {
	if value == nil {
		return 1.0
	}
	return gladiaAnyFloat(value)
}
