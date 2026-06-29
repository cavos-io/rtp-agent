package slng

import (
	"fmt"
	"strings"
)

const (
	defaultSLNGBaseURL         = "api.slng.ai"
	defaultSLNGSTTModel        = "deepgram/nova:3"
	defaultSLNGTTSModel        = "deepgram/aura:2"
	defaultSLNGSTTSampleRate   = 16000
	defaultSLNGTTSSampleRate   = 24000
	defaultSLNGBufferSeconds   = 0.064
	defaultSLNGSTTEncoding     = "pcm_s16le"
	defaultSLNGTTSEncoding     = "linear16"
	defaultSLNGTTSVoice        = "default"
	defaultSLNGLanguage        = "en"
	defaultSLNGVADThreshold    = 0.5
	defaultSLNGVADMinSilenceMS = 300
	defaultSLNGVADSpeechPadMS  = 30
	defaultSLNGSpeed           = 1.0
	slngAPIKeyEnv              = "SLNG_API_KEY"
	slngNumChannels            = 1
	slngFlushMessage           = `{"type":"flush"}`
	slngCancelMessage          = `{"type":"cancel"}`
)

func defaultSTTEndpoint(baseURL, model string) string {
	return defaultSLNGEndpoint(baseURL, "stt", model)
}

func defaultTTSEndpoint(baseURL, model string) string {
	return defaultSLNGEndpoint(baseURL, "tts", model)
}

func defaultSLNGEndpoint(baseURL, kind, modelName string) string {
	host := strings.Split(baseURL, ":")[0]
	scheme := "wss"
	if host == "localhost" || host == "127.0.0.1" {
		scheme = "ws"
	}
	return fmt.Sprintf("%s://%s/v1/%s/%s", scheme, strings.TrimRight(baseURL, "/"), kind, modelName)
}

func normalizeRegionOverride(region any) string {
	var raw []string
	switch v := region.(type) {
	case nil:
		return ""
	case string:
		raw = strings.Split(v, ",")
	case []string:
		raw = v
	default:
		raw = []string{fmt.Sprint(v)}
	}
	values := make([]string, 0, len(raw))
	for _, value := range raw {
		cleaned := strings.ToLower(strings.TrimSpace(value))
		if cleaned != "" {
			values = append(values, cleaned)
		}
	}
	return strings.Join(values, ", ")
}

func slngOptionDefault(options map[string]any, key string, fallback any) any {
	if value, ok := options[key]; ok {
		return value
	}
	return fallback
}

type modelRef struct {
	raw           string
	routeProvider string
	routeModel    string
	variant       string
}

func parseModelRef(modelName string) (modelRef, error) {
	raw := strings.TrimSpace(modelName)
	if raw == "" {
		return modelRef{}, fmt.Errorf("model must not be empty")
	}
	modelPath, variant, _ := strings.Cut(raw, ":")
	if strings.Contains(raw, ":") {
		before, after, _ := strings.Cut(raw, ":")
		modelPath, variant = before, after
		if variant == "" {
			return modelRef{}, fmt.Errorf("model variant must not be empty")
		}
	}
	parts := strings.Split(modelPath, "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) < 2 {
		return modelRef{}, fmt.Errorf("invalid model %q", raw)
	}
	if cleaned[0] == "slng" {
		if len(cleaned) < 3 {
			return modelRef{}, fmt.Errorf("invalid model %q", raw)
		}
		return modelRef{raw: raw, routeProvider: cleaned[1], routeModel: strings.Join(cleaned[2:], "/"), variant: variant}, nil
	}
	return modelRef{raw: raw, routeProvider: cleaned[0], routeModel: strings.Join(cleaned[1:], "/"), variant: variant}, nil
}

func normalizeLanguageForModel(modelName, language string, options map[string]any) string {
	cleaned := strings.TrimSpace(language)
	if candidate, ok := options["target_language_code"].(string); ok && strings.TrimSpace(candidate) != "" {
		cleaned = strings.TrimSpace(candidate)
	}
	ref, err := parseModelRef(modelName)
	if err != nil || ref.routeProvider != "sarvam" {
		return cleaned
	}
	if mapped := sarvamLanguageMap[strings.ToLower(cleaned)]; mapped != "" {
		return mapped
	}
	return cleaned
}

var sarvamLanguageMap = map[string]string{
	"bn": "bn-IN", "bn-in": "bn-IN",
	"en": "en-IN", "en-in": "en-IN",
	"gu": "gu-IN", "gu-in": "gu-IN",
	"hi": "hi-IN", "hi-in": "hi-IN",
	"kn": "kn-IN", "kn-in": "kn-IN",
	"ml": "ml-IN", "ml-in": "ml-IN",
	"mr": "mr-IN", "mr-in": "mr-IN",
	"od": "od-IN", "od-in": "od-IN",
	"pa": "pa-IN", "pa-in": "pa-IN",
	"ta": "ta-IN", "ta-in": "ta-IN",
	"te": "te-IN", "te-in": "te-IN",
}

func ttsAudioFromMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, nil
	}
	if isSLNGTTSEndEvent(message) {
		return slngTTSFinalMarker(), true, nil
	}
	messageType := slngString(message["type"])
	switch messageType {
	case "Audio", "audio", "audio_chunk", "chunk":
		encoded := extractSLNGAudio(message)
		if encoded == "" {
			return nil, false, nil
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, false, nil
		}
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              data,
				SampleRate:        uint32(sampleRate),
				NumChannels:       slngNumChannels,
				SamplesPerChannel: uint32(len(data) / 2),
			},
		}, false, nil
	case "Flushed", "audio_end", "end", "flushed", "complete", "completed", "done", "final":
		return slngTTSFinalMarker(), true, nil
	case "Error", "error":
		return nil, false, llm.NewAPIStatusError("SLNG TTS error: "+extractSLNGError(message), -1, "", message)
	case "":
		if encoded := slngString(message["audio"]); encoded != "" {
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				if slngBool(message["isFinal"]) {
					return slngTTSFinalMarker(), true, nil
				}
				return nil, false, nil
			}
			audio := &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              data,
					SampleRate:        uint32(sampleRate),
					NumChannels:       slngNumChannels,
					SamplesPerChannel: uint32(len(data) / 2),
				},
			}
			return audio, slngBool(message["isFinal"]), nil
		}
		if slngBool(message["isFinal"]) {
			return slngTTSFinalMarker(), true, nil
		}
		if message["error"] != nil {
			return nil, false, llm.NewAPIStatusError("SLNG TTS error: "+extractSLNGError(message), -1, "", message)
		}
	}
	return nil, false, nil
}

func slngTTSFinalMarker() *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{IsFinal: true}
}

func isSLNGTTSEndEvent(message map[string]any) bool {
	if slngString(message["type"]) != "event" {
		return false
	}
	data, _ := message["data"].(map[string]any)
	raw := strings.ToLower(slngString(data["event_type"]))
	if raw == "" {
		raw = strings.ToLower(slngString(data["event"]))
	}
	switch raw {
	case "complete", "completed", "done", "end", "final":
		return true
	default:
		return false
	}
}

func sttEventsFromMessage(payload []byte, defaultLanguage string, partials bool) ([]*stt.SpeechEvent, error) {
	events, _, _, err := sttEventsFromMessageWithSpeechState(payload, defaultLanguage, partials, false, 0)
	return events, err
}

func sttEventsFromMessageWithSpeechState(payload []byte, defaultLanguage string, partials bool, speechStarted bool, speechDuration float64) ([]*stt.SpeechEvent, bool, float64, error) {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, speechStarted, speechDuration, err
	}
	messageType := slngString(message["type"])
	if messageType == "Results" {
		message = normalizeSLNGResults(message)
		messageType = slngString(message["type"])
	}
	if messageType == "Error" {
		return nil, speechStarted, speechDuration, fmt.Errorf("slng stt error: %s", extractSLNGError(message))
	}
	if messageType == "partial_transcript" && !partials {
		return nil, speechStarted, speechDuration, nil
	}
	if messageType != "partial_transcript" && messageType != "final_transcript" {
		return nil, speechStarted, speechDuration, nil
	}
	isFinal := messageType == "final_transcript"
	text := slngString(message["transcript"])
	if text == "" {
		if isFinal && speechDuration > 0 {
			return []*stt.SpeechEvent{slngSTTRecognitionUsageEvent(speechDuration)}, speechStarted, 0, nil
		}
		return nil, speechStarted, speechDuration, nil
	}
	eventType := stt.SpeechEventInterimTranscript
	if isFinal {
		eventType = stt.SpeechEventFinalTranscript
	}
	alternative := stt.SpeechData{
		Language:   slngStringDefault(message["language"], defaultLanguage),
		Text:       text,
		Confidence: slngFloat(message["confidence"]),
	}
	if isFinal {
		words := slngSlice(message["words"])
		if len(words) > 0 {
			alternative.StartTime = slngFloat(slngMap(words[0])["start"])
			alternative.EndTime = slngFloat(slngMap(words[len(words)-1])["end"])
		}
	}
	events := []*stt.SpeechEvent{}
	if !speechStarted {
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
		speechStarted = true
	}
	events = append(events, &stt.SpeechEvent{
		Type:         eventType,
		Alternatives: []stt.SpeechData{alternative},
	})
	if isFinal {
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
		speechStarted = false
		if speechDuration > 0 {
			events = append(events, slngSTTRecognitionUsageEvent(speechDuration))
			speechDuration = 0
		}
	}
	return events, speechStarted, speechDuration, nil
}

func slngSTTRecognitionUsageEvent(audioDuration float64) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type:             stt.SpeechEventRecognitionUsage,
		RecognitionUsage: &stt.RecognitionUsage{AudioDuration: audioDuration},
	}
}

func normalizeSLNGResults(message map[string]any) map[string]any {
	channel := slngMap(message["channel"])
	alternatives := slngSlice(channel["alternatives"])
	alt := map[string]any{}
	if len(alternatives) > 0 {
		alt = slngMap(alternatives[0])
	}
	messageType := "partial_transcript"
	if slngBool(message["is_final"]) {
		messageType = "final_transcript"
	}
	return map[string]any{
		"type":       messageType,
		"transcript": alt["transcript"],
		"confidence": alt["confidence"],
		"words":      alt["words"],
		"language":   slngStringDefault(message["language"], slngString(alt["language"])),
	}
}

type sttStream struct {
	mu                      sync.Mutex
	ctx                     context.Context
	provider                *STT
	conn                    *websocket.Conn
	language                string
	partials                bool
	sampleRate              int
	bufferSizeSeconds       float64
	encoding                string
	diarization             bool
	vadThreshold            float64
	vadMinSilenceDurationMS int
	vadSpeechPadMS          int
	audioBuffer             []byte
	pendingEvents           []*stt.SpeechEvent
	speechStarted           bool
	speechDuration          float64
	reconnectRequested      bool
	closed                  bool
}

func (s *sttStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	if s.reconnectRequested {
		if err := s.reconnectLocked(); err != nil {
			return err
		}
	}
	s.audioBuffer = append(s.audioBuffer, frame.Data...)
	chunkSize := s.audioChunkBytes()
	for len(s.audioBuffer) >= chunkSize {
		chunk := append([]byte(nil), s.audioBuffer[:chunkSize]...)
		if err := s.writeAlignedAudio(chunk); err != nil {
			return err
		}
		s.audioBuffer = s.audioBuffer[chunkSize:]
	}
	return nil
}

func (s *sttStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if len(s.audioBuffer) == 0 {
		return nil
	}
	chunk := append([]byte(nil), s.audioBuffer...)
	s.audioBuffer = nil
	return s.writeAlignedAudio(chunk)
}

func (s *sttStream) writeAlignedAudio(chunk []byte) error {
	if len(chunk)%slngSTTBytesPerSample(s.encoding) != 0 {
		return nil
	}
	if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
		s.closed = true
		_ = s.conn.Close()
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return err
	}
	s.speechDuration += s.audioDuration(chunk)
	return nil
}

func (s *sttStream) audioDuration(chunk []byte) float64 {
	sampleRate := s.sampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSLNGSTTSampleRate
	}
	bytesPerSample := slngSTTBytesPerSample(s.encoding)
	if bytesPerSample <= 0 || len(chunk) == 0 {
		return 0
	}
	return float64(len(chunk)/bytesPerSample) / float64(sampleRate)
}

func (s *sttStream) audioChunkBytes() int {
	sampleRate := s.sampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSLNGSTTSampleRate
	}
	bufferSizeSeconds := s.bufferSizeSeconds
	if bufferSizeSeconds <= 0 {
		bufferSizeSeconds = defaultSLNGBufferSeconds
	}
	samplesPerBuffer := int(math.Round(float64(sampleRate) * bufferSizeSeconds))
	if samplesPerBuffer < 1 {
		samplesPerBuffer = 1
	}
	return samplesPerBuffer * slngSTTBytesPerSample(s.encoding)
}

func (s *sttStream) updateOptions(opts slngSTTActiveOptions) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if opts.language != "" {
		s.language = opts.language
	}
	s.partials = opts.partials
	if opts.bufferSizeSeconds > 0 {
		s.bufferSizeSeconds = opts.bufferSizeSeconds
	}
	s.diarization = opts.diarization
	s.vadThreshold = opts.vadThreshold
	s.vadMinSilenceDurationMS = opts.vadMinSilenceDurationMS
	s.vadSpeechPadMS = opts.vadSpeechPadMS
	s.reconnectRequested = true
	conn := s.conn
	s.mu.Unlock()

	if conn != nil {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.Close()
	}
}

func (s *sttStream) reconnectLocked() error {
	if s.closed {
		return io.ErrClosedPipe
	}
	provider := s.provider
	if provider == nil {
		return io.ErrClosedPipe
	}

	provider.mu.Lock()
	attempt := STT{
		apiKey:                  provider.apiKey,
		endpoint:                provider.endpoint,
		model:                   provider.model,
		regionOverride:          provider.regionOverride,
		sampleRate:              s.sampleRate,
		bufferSizeSeconds:       s.bufferSizeSeconds,
		encoding:                s.encoding,
		enablePartialTranscript: s.partials,
		vadThreshold:            s.vadThreshold,
		vadMinSilenceDurationMS: s.vadMinSilenceDurationMS,
		vadSpeechPadMS:          s.vadSpeechPadMS,
		enableDiarization:       s.diarization,
		language:                s.language,
		modelOptions:            cloneSLNGMap(provider.modelOptions),
	}
	if provider.minSpeakers != nil {
		minSpeakers := *provider.minSpeakers
		attempt.minSpeakers = &minSpeakers
	}
	if provider.maxSpeakers != nil {
		maxSpeakers := *provider.maxSpeakers
		attempt.maxSpeakers = &maxSpeakers
	}
	provider.mu.Unlock()
	endpoint := attempt.endpoint
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, buildSTTWebsocketHeaders(&attempt))
	if err != nil {
		return fmt.Errorf("failed to reconnect slng stt websocket: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, buildSTTInitPayload(&attempt)); err != nil {
		_ = conn.Close()
		return err
	}
	if s.closed {
		_ = conn.Close()
		return io.ErrClosedPipe
	}
	s.conn = conn
	s.reconnectRequested = false
	s.audioBuffer = nil
	s.pendingEvents = nil
	s.speechStarted = false
	s.speechDuration = 0
	return nil
}

func slngSTTBytesPerSample(encoding string) int {
	if encoding == "pcm_mulaw" {
		return 1
	}
	return 2
}

func (s *sttStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.provider != nil {
		defer s.provider.unregisterStream(s)
	}
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *sttStream) Next() (*stt.SpeechEvent, error) {
	for {
		s.mu.Lock()
		closed := s.closed
		conn := s.conn
		if closed || conn == nil {
			s.mu.Unlock()
			return nil, io.EOF
		}
		if len(s.pendingEvents) > 0 {
			event := s.pendingEvents[0]
			s.pendingEvents = s.pendingEvents[1:]
			s.mu.Unlock()
			return event, nil
		}
		if s.reconnectRequested {
			if err := s.reconnectLocked(); err != nil {
				s.mu.Unlock()
				return nil, err
			}
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			if s.shouldReconnect() {
				s.mu.Lock()
				err := s.reconnectLocked()
				s.mu.Unlock()
				if err != nil {
					return nil, err
				}
				continue
			}
			if s.isClosed() {
				return nil, io.EOF
			}
			return nil, slngSTTReadError(err)
		}
		if msgType != websocket.TextMessage {
			continue
		}
		events, speechStarted, speechDuration, err := sttEventsFromMessageWithSpeechState(payload, s.language, s.partials, s.speechStarted, s.speechDuration)
		if err != nil {
			return nil, err
		}
		s.speechStarted = speechStarted
		s.speechDuration = speechDuration
		if len(events) > 0 {
			event := events[0]
			s.pendingEvents = append(s.pendingEvents, events[1:]...)
			return event, nil
		}
	}
}

func (s *sttStream) shouldReconnect() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.reconnectRequested
}

func (s *sttStream) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func slngSTTReadError(err error) error {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return llm.NewAPIStatusError("SLNG connection closed unexpectedly", closeErr.Code, "", err.Error())
	}
	return err
}

type ttsStream struct {
	mu              sync.Mutex
	provider        *TTS
	conn            *websocket.Conn
	sampleRate      int
	model           string
	audioFrames     int
	audioBytes      int
	textMessages    int
	pendingText     string
	lastMessageType string
	appendTextSpace bool
	closed          bool
}

func (s *ttsStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if text == "" {
		return nil
	}
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	s.pendingText += text
	return s.sendCompleteWordsLocked()
}

func (s *ttsStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	if s.pendingText != "" {
		text := strings.Join(tokenize.NewBasicWordTokenizer().Tokenize(s.pendingText, ""), " ")
		s.pendingText = ""
		if err := s.sendTextLocked(text); err != nil {
			return err
		}
	}
	if isRimeArcanaModel(s.model) {
		if err := s.conn.WriteMessage(websocket.TextMessage, []byte(slngCancelMessage)); err != nil {
			s.closed = true
			_ = s.conn.Close()
			if s.provider != nil {
				s.provider.unregisterStream(s)
			}
			return err
		}
		return nil
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, []byte(slngFlushMessage)); err != nil {
		s.closed = true
		_ = s.conn.Close()
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return err
	}
	return nil
}

func (s *ttsStream) sendCompleteWordsLocked() error {
	for {
		tokens := tokenize.NewBasicWordTokenizer().Tokenize(s.pendingText, "")
		if len(tokens) <= 1 {
			return nil
		}
		word := tokens[0]
		if err := s.sendTextLocked(word); err != nil {
			return err
		}
		wordIdx := strings.Index(s.pendingText, word)
		if wordIdx < 0 {
			return nil
		}
		s.pendingText = strings.TrimLeftFunc(s.pendingText[wordIdx+len(word):], unicode.IsSpace)
	}
}

func (s *ttsStream) sendTextLocked(text string) error {
	if s.appendTextSpace && !strings.HasSuffix(text, " ") {
		text += " "
	}
	data, err := json.Marshal(map[string]any{"type": "text", "text": text})
	if err != nil {
		return err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		s.closed = true
		_ = s.conn.Close()
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return err
	}
	return nil
}

func (s *ttsStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.provider != nil {
		defer s.provider.unregisterStream(s)
	}
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *ttsStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		s.mu.Lock()
		closed := s.closed
		conn := s.conn
		s.mu.Unlock()
		if closed || conn == nil {
			return nil, io.EOF
		}
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return nil, s.readError(err)
		}
		if msgType == websocket.BinaryMessage {
			s.audioFrames++
			s.audioBytes += len(payload)
			s.lastMessageType = "binary"
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              payload,
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       slngNumChannels,
					SamplesPerChannel: uint32(len(payload) / 2),
				},
			}, nil
		}
		if msgType != websocket.TextMessage {
			continue
		}
		s.textMessages++
		s.lastMessageType = slngTTSMessageKind(payload)
		audio, done, err := ttsAudioFromMessage(payload, s.sampleRate)
		if err != nil {
			return nil, err
		}
		if audio != nil && audio.Frame != nil {
			s.audioFrames++
			s.audioBytes += len(audio.Frame.Data)
		}
		if done {
			return nil, io.EOF
		}
		if audio != nil {
			return audio, nil
		}
	}
}

func (s *ttsStream) readError(err error) error {
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		return err
	}
	if closeErr.Code == websocket.CloseNormalClosure && (s.audioFrames > 0 || isRimeArcanaModel(s.model)) {
		return io.EOF
	}
	message := fmt.Sprintf(
		"slng tts websocket closed before completion: %v (model=%s audio_frames=%d audio_bytes=%d text_messages=%d last_message_type=%q)",
		err,
		s.model,
		s.audioFrames,
		s.audioBytes,
		s.textMessages,
		s.lastMessageType,
	)
	return llm.NewAPIStatusError(message, closeErr.Code, "", err.Error())
}

func slngTTSMessageKind(payload []byte) string {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return "text/non-json"
	}
	if messageType := slngString(message["type"]); messageType != "" {
		return messageType
	}
	if slngString(message["audio"]) != "" {
		return "audio"
	}
	if slngBool(message["isFinal"]) {
		return "isFinal"
	}
	if message["error"] != nil {
		return "error"
	}
	return "text/unknown"
}

type ttsChunkedStream struct {
	stream tts.SynthesizeStream
}

func (s *ttsChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	return s.stream.Next()
}

func (s *ttsChunkedStream) Close() error {
	return s.stream.Close()
}

func extractSLNGAudio(message map[string]any) string {
	if data := slngString(message["data"]); data != "" {
		return data
	}
	if data := slngMap(message["data"]); len(data) > 0 {
		return slngString(data["audio"])
	}
	return slngString(message["audio"])
}

func extractSLNGError(message map[string]any) string {
	for _, key := range []string{"message", "description", "error"} {
		if value := slngString(message[key]); value != "" {
			return value
		}
	}
	return "Unknown error"
}

func slngString(value any) string {
	text, _ := value.(string)
	return text
}

func slngStringDefault(value any, fallback string) string {
	if text := slngString(value); text != "" {
		return text
	}
	return fallback
}

func slngMap(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}

func slngSlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func slngFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func slngBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func cloneSLNGMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
