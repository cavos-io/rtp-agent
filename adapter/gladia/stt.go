package gladia

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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
	defaultGladiaEnergyMinSilence                  = 1.5
	defaultGladiaEnergyThreshold                   = 0.004 * 0.004
)

type GladiaSTT struct {
	mu                                 sync.Mutex
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
	energyFilter                       *gladiaAudioEnergyFilterConfig
	streams                            map[*gladiaSTTStream]struct{}
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

func WithGladiaEnergyFilter(minSilence float64, rmsThreshold float64) GladiaSTTOption {
	return func(s *GladiaSTT) {
		if minSilence <= 0 {
			minSilence = defaultGladiaEnergyMinSilence
		}
		if rmsThreshold <= 0 {
			rmsThreshold = defaultGladiaEnergyThreshold
		}
		s.energyFilter = &gladiaAudioEnergyFilterConfig{
			minSilence:   minSilence,
			rmsThreshold: rmsThreshold,
		}
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

func (s *GladiaSTT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return defaultGladiaSampleRate
	}
	return uint32(s.sampleRate)
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

func (s *GladiaSTT) UpdateOptions(opts ...GladiaSTTOption) {
	if s == nil {
		return
	}
	s.mu.Lock()
	for _, opt := range opts {
		opt(s)
	}
	streams := make([]*gladiaSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()

	for _, stream := range streams {
		stream.updateOptions(s)
	}
}

func (s *GladiaSTT) registerStream(stream *gladiaSTTStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = map[*gladiaSTTStream]struct{}{}
	}
	s.streams[stream] = struct{}{}
	stream.owner = s
}

func (s *GladiaSTT) unregisterStream(stream *gladiaSTTStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
	if stream.owner == s {
		stream.owner = nil
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
	var energyFilter *gladiaAudioEnergyFilter
	if provider.energyFilter != nil {
		energyFilter = provider.energyFilter.newFilter()
	}
	interimResults := provider.interimResults
	stream := &gladiaSTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state: &gladiaSTTStreamState{
			requestID:          session.ID,
			languages:          provider.languages,
			interimResults:     &interimResults,
			translationEnabled: provider.translationEnabled,
		},
		energyFilter: energyFilter,
	}
	s.registerStream(stream)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := &GladiaSTT{
		apiKey:                             s.apiKey,
		baseURL:                            s.baseURL,
		model:                              s.model,
		languages:                          append([]string(nil), s.languages...),
		codeSwitching:                      s.codeSwitching,
		interimResults:                     s.interimResults,
		sampleRate:                         s.sampleRate,
		bitDepth:                           s.bitDepth,
		channels:                           s.channels,
		endpointing:                        s.endpointing,
		maximumDurationWithoutEndpointing:  s.maximumDurationWithoutEndpointing,
		region:                             s.region,
		encoding:                           s.encoding,
		translationEnabled:                 s.translationEnabled,
		translationTargetLanguages:         append([]string(nil), s.translationTargetLanguages...),
		translationModel:                   s.translationModel,
		translationMatchOriginalUtterances: s.translationMatchOriginalUtterances,
		translationLipsync:                 s.translationLipsync,
		translationContextAdaptation:       s.translationContextAdaptation,
		translationContext:                 s.translationContext,
		translationInformal:                s.translationInformal,
		customVocabulary:                   append([]any(nil), s.customVocabulary...),
		customSpelling:                     cloneGladiaCustomSpelling(s.customSpelling),
		preProcessingAudioEnhancer:         s.preProcessingAudioEnhancer,
		preProcessingSpeechThreshold:       s.preProcessingSpeechThreshold,
	}
	if language != "" {
		clone.languages = []string{language}
	}
	if s.energyFilter != nil {
		filter := *s.energyFilter
		clone.energyFilter = &filter
	}
	return clone
}

func cloneGladiaCustomSpelling(spelling map[string][]string) map[string][]string {
	if spelling == nil {
		return nil
	}
	clone := make(map[string][]string, len(spelling))
	for word, variants := range spelling {
		clone[word] = append([]string(nil), variants...)
	}
	return clone
}

type gladiaSTTStream struct {
	owner  *GladiaSTT
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

	energyFilter *gladiaAudioEnergyFilter
	lastFrame    *model.AudioFrame
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
	frames, ended := s.framesForEnergyState(frame)
	if s.audio == nil {
		s.audio = newGladiaAudioByteStream(frame)
	}
	if s.state == nil {
		s.state = &gladiaSTTStreamState{}
	}
	for _, frame := range frames {
		for _, chunk := range s.audio.Push(frame.Data) {
			if err := s.writeTextMessage(buildGladiaAudioChunkMessage(chunk.Data)); err != nil {
				return err
			}
			s.state.audioDuration += audio.CalculateFrameDuration(chunk)
		}
	}
	if ended {
		for _, chunk := range s.audio.Flush() {
			if err := s.writeTextMessage(buildGladiaAudioChunkMessage(chunk.Data)); err != nil {
				return err
			}
			s.state.audioDuration += audio.CalculateFrameDuration(chunk)
		}
		if err := s.writeTextMessage(buildGladiaStopRecordingMessage()); err != nil {
			return err
		}
		s.emitRecognitionUsage()
	}
	return nil
}

func (s *gladiaSTTStream) framesForEnergyState(frame *model.AudioFrame) ([]*model.AudioFrame, bool) {
	if s.energyFilter == nil {
		return []*model.AudioFrame{frame}, false
	}
	switch s.energyFilter.update(frame) {
	case gladiaEnergyStart, gladiaEnergySpeaking:
		frames := make([]*model.AudioFrame, 0, 2)
		if s.lastFrame != nil {
			frames = append(frames, s.lastFrame)
			s.lastFrame = nil
		}
		frames = append(frames, frame)
		return frames, false
	case gladiaEnergyEnd:
		s.lastFrame = nil
		return nil, true
	case gladiaEnergySilence:
		s.lastFrame = frame
	}
	return nil, false
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

type gladiaAudioEnergyFilterConfig struct {
	minSilence   float64
	rmsThreshold float64
}

func (c gladiaAudioEnergyFilterConfig) newFilter() *gladiaAudioEnergyFilter {
	return newGladiaAudioEnergyFilter(c.minSilence, c.rmsThreshold)
}

type gladiaEnergyState int

const (
	gladiaEnergyStart gladiaEnergyState = iota
	gladiaEnergySpeaking
	gladiaEnergySilence
	gladiaEnergyEnd
)

type gladiaAudioEnergyFilter struct {
	cooldownSeconds float64
	cooldown        float64
	state           gladiaEnergyState
	rmsThreshold    float64
}

func newGladiaAudioEnergyFilter(minSilence float64, rmsThreshold float64) *gladiaAudioEnergyFilter {
	return &gladiaAudioEnergyFilter{
		cooldownSeconds: minSilence,
		cooldown:        minSilence,
		state:           gladiaEnergySilence,
		rmsThreshold:    rmsThreshold,
	}
}

func (f *gladiaAudioEnergyFilter) update(frame *model.AudioFrame) gladiaEnergyState {
	if gladiaFrameRMS(frame) > f.rmsThreshold {
		f.cooldown = f.cooldownSeconds
		if f.state == gladiaEnergySilence || f.state == gladiaEnergyEnd {
			f.state = gladiaEnergyStart
		} else {
			f.state = gladiaEnergySpeaking
		}
		return f.state
	}
	if f.cooldown <= 0 {
		if f.state == gladiaEnergySpeaking || f.state == gladiaEnergyStart {
			f.state = gladiaEnergyEnd
		} else if f.state == gladiaEnergyEnd {
			f.state = gladiaEnergySilence
		}
	} else {
		f.cooldown -= audio.CalculateFrameDuration(frame)
		f.state = gladiaEnergySpeaking
	}
	return f.state
}

func gladiaFrameRMS(frame *model.AudioFrame) float64 {
	if frame == nil || len(frame.Data) < 2 {
		return 0
	}
	samples := len(frame.Data) / 2
	var sum float64
	for i := 0; i < samples; i++ {
		v := float64(int16(binary.LittleEndian.Uint16(frame.Data[i*2:]))) / 32768.0
		sum += v * v
	}
	return sum / float64(samples)
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
	err := s.conn.Close()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
	return err
}

func (s *gladiaSTTStream) updateOptions(provider *GladiaSTT) {
	if s == nil || provider == nil {
		return
	}
	provider.mu.Lock()
	languages := append([]string(nil), provider.languages...)
	interimResults := provider.interimResults
	translationEnabled := provider.translationEnabled
	var energyFilter *gladiaAudioEnergyFilter
	if provider.energyFilter != nil {
		energyFilter = provider.energyFilter.newFilter()
	}
	provider.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.ensureStateLocked()
	state.languages = languages
	state.interimResults = &interimResults
	state.translationEnabled = translationEnabled
	s.energyFilter = energyFilter
	s.lastFrame = nil
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

func (s *gladiaSTTStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureStateLocked().startTimeOffset
}

func (s *gladiaSTTStream) SetStartTimeOffset(offset float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureStateLocked().startTimeOffset = offset
}

func (s *gladiaSTTStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureStateLocked().startTime
}

func (s *gladiaSTTStream) SetStartTime(startTime float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureStateLocked().startTime = startTime
}

func (s *gladiaSTTStream) ensureStateLocked() *gladiaSTTStreamState {
	if s.state == nil {
		s.state = &gladiaSTTStreamState{}
	}
	return s.state
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
	requestID          string
	languages          []string
	speaking           bool
	audioDuration      float64
	interimResults     *bool
	translationEnabled bool
	startTimeOffset    float64
	startTime          float64
}

func processGladiaMessage(state *gladiaSTTStreamState, data map[string]any) ([]*stt.SpeechEvent, error) {
	messageType, _ := data["type"].(string)
	if state == nil {
		state = &gladiaSTTStreamState{}
	}
	switch messageType {
	case "transcript":
		return processGladiaTranscriptMessage(state, data)
	case "translation":
		return processGladiaTranslationMessage(state, data), nil
	case "post_final_transcript":
		state.speaking = false
		return nil, nil
	case "error":
		return nil, fmt.Errorf("gladia websocket error: %v", data["data"])
	default:
		return nil, nil
	}
}

func processGladiaTranscriptMessage(state *gladiaSTTStreamState, data map[string]any) ([]*stt.SpeechEvent, error) {
	payload, _ := data["data"].(map[string]any)
	utterance, _ := payload["utterance"].(map[string]any)
	text := strings.TrimSpace(gladiaAnyString(utterance["text"]))
	if text == "" {
		return nil, nil
	}
	utterance["text"] = text
	isFinal, _ := payload["is_final"].(bool)
	speechData := gladiaSpeechData(state, utterance)
	events := []*stt.SpeechEvent{}
	if !state.speaking {
		state.speaking = true
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech, RequestID: state.requestID})
	}
	if isFinal {
		if state.translationEnabled {
			return events, nil
		}
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, RequestID: state.requestID, Alternatives: []stt.SpeechData{speechData}})
		state.speaking = false
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech, RequestID: state.requestID})
		return events, nil
	}
	if gladiaInterimResultsEnabled(state) {
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventInterimTranscript, RequestID: state.requestID, Alternatives: []stt.SpeechData{speechData}})
	}
	return events, nil
}

func gladiaInterimResultsEnabled(state *gladiaSTTStreamState) bool {
	return state.interimResults == nil || *state.interimResults
}

func processGladiaTranslationMessage(state *gladiaSTTStreamState, data map[string]any) []*stt.SpeechEvent {
	if !state.translationEnabled {
		return nil
	}
	payload, _ := data["data"].(map[string]any)
	translatedUtterance, _ := payload["translated_utterance"].(map[string]any)
	text := strings.TrimSpace(gladiaAnyString(translatedUtterance["text"]))
	language := gladiaAnyString(translatedUtterance["language"])
	if language == "" {
		language = gladiaAnyString(payload["target_language"])
	}
	if text == "" || language == "" {
		return nil
	}
	translatedUtterance["text"] = text
	translatedUtterance["language"] = language
	speechData := gladiaSpeechData(state, translatedUtterance)
	originalUtterance, _ := payload["utterance"].(map[string]any)
	if sourceLanguage := gladiaAnyString(originalUtterance["language"]); sourceLanguage != "" {
		speechData.SourceLanguages = []string{sourceLanguage}
		speechData.SourceTexts = []string{gladiaAnyString(originalUtterance["text"])}
	}
	events := []*stt.SpeechEvent{{
		Type:         stt.SpeechEventFinalTranscript,
		RequestID:    state.requestID,
		Alternatives: []stt.SpeechData{speechData},
	}}
	if state.speaking {
		state.speaking = false
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech, RequestID: state.requestID})
	}
	return events
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
		StartTime:  gladiaAnyFloat(utterance["start"]) + state.startTimeOffset,
		EndTime:    gladiaAnyFloat(utterance["end"]) + state.startTimeOffset,
		Confidence: gladiaConfidence(utterance["confidence"]),
		Words:      gladiaWordsFromAny(utterance["words"], state.startTimeOffset),
	}
}

func gladiaWordsFromAny(raw any, startTimeOffset float64) []stt.TimedString {
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
			Text:            gladiaAnyString(wordMap["word"]),
			StartTime:       gladiaAnyFloat(wordMap["start"]) + startTimeOffset,
			EndTime:         gladiaAnyFloat(wordMap["end"]) + startTimeOffset,
			StartTimeOffset: startTimeOffset,
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
