package slng

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

var slngElevenLabsTTSModelOptionKeys = []string{
	"inactivity_timeout",
	"apply_text_normalization",
	"auto_mode",
	"enable_logging",
	"enable_ssml_parsing",
	"sync_alignment",
	"language_code",
	"stability",
	"similarity_boost",
	"style",
	"speed",
	"use_speaker_boost",
	"chunk_length_schedule",
	"preferred_alignment",
}

type TTS struct {
	mu             sync.Mutex
	apiKey         string
	model          string
	endpoint       string
	regionOverride string
	voice          string
	language       string
	sampleRate     int
	speed          float64
	encoding       string
	modelOptions   map[string]any
	streams        map[*ttsStream]struct{}
	closed         bool
}

type TTSOption func(*TTS)

func WithTTSBaseURL(baseURL string) TTSOption {
	return func(t *TTS) {
		if baseURL != "" {
			t.endpoint = defaultTTSEndpoint(strings.TrimRight(baseURL, "/"), t.model)
		}
	}
}

func WithTTSModel(modelName string) TTSOption {
	return func(t *TTS) {
		if modelName != "" {
			t.model = modelName
			t.endpoint = defaultTTSEndpoint(defaultSLNGBaseURL, modelName)
			t.voice = normalizeTTSVoice(modelName, t.voice)
			t.language = normalizeLanguageForModel(modelName, t.language, t.modelOptions)
		}
	}
}

func WithTTSEndpoint(endpoint string) TTSOption {
	return func(t *TTS) {
		if endpoint != "" {
			t.endpoint = endpoint
		}
	}
}

func WithTTSRegionOverride(region any) TTSOption {
	return func(t *TTS) {
		t.regionOverride = normalizeRegionOverride(region)
	}
}

func WithTTSVoice(voice string) TTSOption {
	return func(t *TTS) {
		t.voice = normalizeTTSVoice(t.model, voice)
	}
}

func WithTTSLanguage(language string) TTSOption {
	return func(t *TTS) {
		t.language = normalizeLanguageForModel(t.model, language, t.modelOptions)
	}
}

func WithTTSSampleRate(sampleRate int) TTSOption {
	return func(t *TTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithTTSSpeed(speed float64) TTSOption {
	return func(t *TTS) {
		t.speed = speed
	}
}

func WithTTSModelOptions(options map[string]any) TTSOption {
	return func(t *TTS) {
		t.modelOptions = cloneSLNGMap(options)
		t.language = normalizeLanguageForModel(t.model, t.language, t.modelOptions)
	}
}

func NewTTS(apiKey string, opts ...TTSOption) *TTS {
	if apiKey == "" {
		apiKey = os.Getenv(slngAPIKeyEnv)
	}
	provider := &TTS{
		apiKey:     apiKey,
		model:      defaultSLNGTTSModel,
		endpoint:   defaultTTSEndpoint(defaultSLNGBaseURL, defaultSLNGTTSModel),
		voice:      normalizeTTSVoice(defaultSLNGTTSModel, defaultSLNGTTSVoice),
		language:   defaultSLNGLanguage,
		sampleRate: defaultSLNGTTSSampleRate,
		speed:      defaultSLNGSpeed,
		encoding:   defaultSLNGTTSEncoding,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *TTS) Label() string { return "slng.TTS" }
func (t *TTS) Model() string { return "slng" }
func (t *TTS) Provider() string {
	return "SLNG"
}
func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *TTS) SampleRate() int  { return t.sampleRate }
func (t *TTS) NumChannels() int { return slngNumChannels }

func (t *TTS) UpdateOptions(opts ...TTSOption) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
}

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	stream, err := t.stream(ctx, false)
	if err != nil {
		return nil, err
	}
	if text != "" {
		if err := stream.PushText(text); err != nil {
			stream.Close()
			return nil, err
		}
	}
	if err := stream.Flush(); err != nil {
		stream.Close()
		return nil, err
	}
	return &ttsChunkedStream{stream: stream}, nil
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return t.stream(ctx, true)
}

func (t *TTS) stream(ctx context.Context, appendTextSpace bool) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := t.requireAPIKey(); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, t.endpoint, buildTTSWebsocketHeaders(t))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial slng tts websocket: %v", err))
	}
	if err := conn.WriteMessage(websocket.TextMessage, buildTTSInitPayload(t)); err != nil {
		conn.Close()
		return nil, err
	}
	stream := &ttsStream{provider: t, conn: conn, sampleRate: t.sampleRate, model: t.model, appendTextSpace: appendTextSpace}
	if !t.registerStream(stream) {
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *TTS) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	streams := make([]*ttsStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.mu.Unlock()

	var firstErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *TTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *TTS) registerStream(stream *ttsStream) bool {
	if stream == nil {
		return false
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = stream.Close()
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*ttsStream]struct{})
	}
	stream.provider = t
	t.streams[stream] = struct{}{}
	t.mu.Unlock()
	return true
}

func (t *TTS) unregisterStream(stream *ttsStream) {
	if stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func (t *TTS) requireAPIKey() error {
	if t.apiKey == "" {
		return fmt.Errorf("api key is required, or set %s environment variable", slngAPIKeyEnv)
	}
	return nil
}
func buildTTSWebsocketHeaders(t *TTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+t.apiKey)
	headers.Set("X-API-Key", t.apiKey)
	if t.regionOverride != "" {
		headers.Set("X-Region-Override", t.regionOverride)
	}
	return headers
}
func buildTTSInitPayload(t *TTS) []byte {
	language := normalizeLanguageForModel(t.model, t.language, t.modelOptions)
	config := map[string]any{
		"language":    language,
		"encoding":    t.encoding,
		"sample_rate": t.sampleRate,
		"speed":       t.speed,
	}
	payload := map[string]any{
		"type":     "init",
		"model":    t.model,
		"voice":    t.voice,
		"language": language,
		"config":   config,
	}
	ref, err := parseModelRef(t.model)
	if err == nil {
		switch {
		case ref.routeProvider == "deepgram" && ref.routeModel == "aura":
			payload["model"] = t.voice
		case ref.routeProvider == "rime" && ref.routeModel == "arcana":
			config["modelId"] = slngOptionDefault(t.modelOptions, "modelId", "arcana")
			config["segment"] = slngOptionDefault(t.modelOptions, "segment", "bySentence")
			for _, key := range []string{"speakingStyle", "addBreathing", "addDisfluencies", "phonemizeBetweenBrackets", "translateTo"} {
				if value, ok := t.modelOptions[key]; ok {
					config[key] = value
				}
			}
			payload["speaker"] = t.voice
		case ref.routeProvider == "rime" && ref.routeModel == "coda":
			config["modelId"] = slngOptionDefault(t.modelOptions, "modelId", "coda")
			if value, ok := t.modelOptions["segment"]; ok {
				config["segment"] = value
			}
			payload["speaker"] = t.voice
		case ref.routeProvider == "elevenlabs":
			for _, key := range slngElevenLabsTTSModelOptionKeys {
				if value, ok := t.modelOptions[key]; ok {
					config[key] = value
				}
			}
		case ref.routeProvider == "sarvam" && ref.routeModel == "bulbul":
			config["speech_sample_rate"] = fmt.Sprint(t.sampleRate)
			config["pace"] = slngOptionDefault(t.modelOptions, "pace", t.speed)
			for _, key := range []string{"temperature", "output_audio_bitrate", "min_buffer_size", "max_chunk_length"} {
				if value, ok := t.modelOptions[key]; ok {
					config[key] = value
				}
			}
		}
	}
	data, _ := json.Marshal(payload)
	return data
}
func isRimeArcanaModel(modelName string) bool {
	ref, err := parseModelRef(modelName)
	return err == nil && ref.routeProvider == "rime" && ref.routeModel == "arcana"
}

func isRimeCodaModel(modelName string) bool {
	ref, err := parseModelRef(modelName)
	return err == nil && ref.routeProvider == "rime" && ref.routeModel == "coda"
}
func normalizeTTSVoice(modelName, voice string) string {
	cleaned := strings.TrimSpace(voice)
	ref, err := parseModelRef(modelName)
	if err != nil {
		return cleaned
	}
	if ref.routeProvider == "deepgram" && ref.routeModel == "aura" {
		if cleaned != "" && cleaned != "default" {
			return cleaned
		}
		if mapped := auraDefaultVoiceByVariant[ref.variant]; mapped != "" {
			return mapped
		}
		return auraDefaultVoiceByVariant["2"]
	}
	if ref.routeProvider == "rime" && ref.routeModel == "arcana" {
		if cleaned != "" && cleaned != "default" {
			return cleaned
		}
		lang := rimeLangFromVariant(ref.variant)
		if lang == "" {
			lang = "en"
		}
		return rimeDefaultSpeakerByLang[lang]
	}
	return cleaned
}

func rimeLangFromVariant(variant string) string {
	if variant == "" {
		return ""
	}
	if _, ok := rimeDefaultSpeakerByLang[variant]; ok {
		return variant
	}
	if _, lang, ok := strings.Cut(variant, "-"); ok {
		if _, exists := rimeDefaultSpeakerByLang[lang]; exists {
			return lang
		}
	}
	return ""
}

var auraDefaultVoiceByVariant = map[string]string{
	"":     "aura-2-thalia-en",
	"2":    "aura-2-thalia-en",
	"2-en": "aura-2-thalia-en",
	"2-es": "aura-2-celeste-es",
}

var rimeDefaultSpeakerByLang = map[string]string{
	"ar": "sakina",
	"de": "lorelei",
	"en": "astra",
	"es": "seraphina",
	"fr": "destin",
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
	if closeErr.Code == websocket.CloseNormalClosure && (s.audioFrames > 0 || isRimeArcanaModel(s.model) || isRimeCodaModel(s.model)) {
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
