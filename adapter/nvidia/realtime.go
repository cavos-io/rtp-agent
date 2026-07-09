package nvidia

import (
	"encoding/binary"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/hraban/opus"
)

const (
	defaultNvidiaRealtimeBaseURL            = "localhost:8998"
	defaultNvidiaRealtimeVoice              = "NATF2"
	defaultNvidiaRealtimeTextPrompt         = "You are a helpful assistant."
	defaultNvidiaRealtimeModel              = "personaplex-7b"
	defaultNvidiaRealtimeSilenceThresholdMS = 500
	defaultNvidiaRealtimeSampleRate         = 24000
	defaultNvidiaRealtimeNumChannels        = 1
	defaultNvidiaRealtimeInputChunkSamples  = 1920
	nvidiaPersonaplexURLEnv                 = "PERSONAPLEX_URL"
	nvidiaRealtimeMsgHandshake              = 0x00
	nvidiaRealtimeMsgAudio                  = 0x01
	nvidiaRealtimeMsgText                   = 0x02
)

type NvidiaRealtimeModel struct {
	baseURL            string
	voice              string
	textPrompt         string
	seed               *int
	silenceThresholdMS int
	useSSL             bool
}

type nvidiaRealtimeSession struct {
	mu                 sync.Mutex
	baseURL            string
	voice              string
	textPrompt         string
	seed               *int
	silenceThresholdMS int
	useSSL             bool
	label              string
	modelName          string
	provider           string
	chatCtx            *llm.ChatContext
	outboundAudio      []*model.AudioFrame
	outboundMessages   [][]byte
	events             chan llm.RealtimeEvent
	opusEncoder        *opus.Encoder
	opusDecoder        *opus.Decoder
	inputAudioBuffer   []byte
	currentGeneration  *nvidiaRealtimeGeneration
	generationSeq      int
	silenceTimer       *time.Timer
	closed             bool
}

type nvidiaRealtimeGeneration struct {
	responseID   string
	messageCh    chan llm.MessageGeneration
	functionCh   chan *llm.FunctionCall
	textCh       chan string
	timedTextCh  chan llm.RealtimeTimedText
	audioCh      chan *model.AudioFrame
	modalitiesCh chan []string
	outputText   string
	createdAt    time.Time
	firstTokenAt *time.Time
	done         bool
}

type NvidiaRealtimeOption func(*NvidiaRealtimeModel)

func WithNvidiaRealtimeBaseURL(baseURL string) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		if baseURL == "" {
			return
		}
		m.baseURL, m.useSSL = normalizeNvidiaRealtimeBaseURL(baseURL)
	}
}

func WithNvidiaRealtimeVoice(voice string) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		m.voice = voice
	}
}

func WithNvidiaRealtimeTextPrompt(prompt string) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		m.textPrompt = prompt
	}
}

func WithNvidiaRealtimeSeed(seed int) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		m.seed = &seed
	}
}

func WithNvidiaRealtimeSilenceThresholdMS(threshold int) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		m.silenceThresholdMS = threshold
	}
}

func NewNvidiaRealtimeModel(opts ...NvidiaRealtimeOption) *NvidiaRealtimeModel {
	baseURL := os.Getenv(nvidiaPersonaplexURLEnv)
	if baseURL == "" {
		baseURL = defaultNvidiaRealtimeBaseURL
	}
	normalizedBaseURL, useSSL := normalizeNvidiaRealtimeBaseURL(baseURL)
	model := &NvidiaRealtimeModel{
		baseURL:            normalizedBaseURL,
		voice:              defaultNvidiaRealtimeVoice,
		textPrompt:         defaultNvidiaRealtimeTextPrompt,
		silenceThresholdMS: defaultNvidiaRealtimeSilenceThresholdMS,
		useSSL:             useSSL,
	}
	for _, opt := range opts {
		opt(model)
	}
	return model
}

func normalizeNvidiaRealtimeBaseURL(baseURL string) (string, bool) {
	useSSL := strings.HasPrefix(baseURL, "wss://") || strings.HasPrefix(baseURL, "https://")
	for _, prefix := range []string{"ws://", "wss://", "http://", "https://"} {
		if strings.HasPrefix(baseURL, prefix) {
			baseURL = strings.TrimPrefix(baseURL, prefix)
			break
		}
	}
	return baseURL, useSSL
}

func (m *NvidiaRealtimeModel) Label() string {
	return "personaplex-" + m.voice
}

func (m *NvidiaRealtimeModel) Model() string {
	return defaultNvidiaRealtimeModel
}

func (m *NvidiaRealtimeModel) Provider() string {
	return "nvidia"
}

func (m *NvidiaRealtimeModel) websocketURL() string {
	return buildNvidiaRealtimeWebsocketURL(m.useSSL, m.baseURL, m.voice, m.textPrompt, m.seed)
}

func buildNvidiaRealtimeWebsocketURL(useSSL bool, baseURL string, voice string, textPrompt string, seed *int) string {
	scheme := "ws"
	if useSSL {
		scheme = "wss"
	}
	parts := []string{
		"voice_prompt=" + url.QueryEscape(voice+".pt"),
		"text_prompt=" + url.QueryEscape(textPrompt),
	}
	if seed != nil {
		parts = append(parts, "seed="+url.QueryEscape(fmt.Sprintf("%d", *seed)))
	}
	query := strings.ReplaceAll(strings.Join(parts, "&"), "+", "%20")
	return fmt.Sprintf("%s://%s/api/chat?%s", scheme, baseURL, query)
}

func (m *NvidiaRealtimeModel) InputSampleRate() int {
	return defaultNvidiaRealtimeSampleRate
}

func (m *NvidiaRealtimeModel) OutputSampleRate() int {
	return defaultNvidiaRealtimeSampleRate
}

func (m *NvidiaRealtimeModel) NumChannels() int {
	return defaultNvidiaRealtimeNumChannels
}

func (m *NvidiaRealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{
		MessageTruncation:       false,
		TurnDetection:           false,
		UserTranscription:       false,
		AutoToolReplyGeneration: false,
		AudioOutput:             true,
		ManualFunctionCalls:     false,
		PerResponseToolChoice:   false,
	}
}

func (m *NvidiaRealtimeModel) Session() (llm.RealtimeSession, error) {
	return &nvidiaRealtimeSession{
		baseURL:            m.baseURL,
		voice:              m.voice,
		textPrompt:         m.textPrompt,
		seed:               cloneNvidiaRealtimeSeed(m.seed),
		silenceThresholdMS: m.silenceThresholdMS,
		useSSL:             m.useSSL,
		label:              m.Label(),
		modelName:          m.Model(),
		provider:           m.Provider(),
		chatCtx:            llm.EmptyChatContext(),
		events:             make(chan llm.RealtimeEvent, 16),
	}, nil
}

func (m *NvidiaRealtimeModel) Close() error {
	return nil
}

func cloneNvidiaRealtimeSeed(seed *int) *int {
	if seed == nil {
		return nil
	}
	seedValue := *seed
	return &seedValue
}

func cloneNvidiaRealtimeAudioFrame(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil {
		return nil
	}
	cloned := *frame
	if frame.Data != nil {
		cloned.Data = append([]byte(nil), frame.Data...)
	}
	return &cloned
}

func normalizeNvidiaRealtimeInputFrame(frame *model.AudioFrame) (*model.AudioFrame, error) {
	normalized, err := downmixNvidiaRealtimeInputFrame(frame)
	if err != nil {
		return nil, err
	}
	return audio.ResampleAudioFrame(normalized, defaultNvidiaRealtimeSampleRate)
}

func downmixNvidiaRealtimeInputFrame(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if frame == nil || frame.NumChannels == defaultNvidiaRealtimeNumChannels {
		return cloneNvidiaRealtimeAudioFrame(frame), nil
	}
	if frame.NumChannels == 0 {
		return nil, fmt.Errorf("cannot downmix audio with zero channels")
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("cannot downmix non-16-bit PCM audio")
	}
	expectedBytes := int(frame.SamplesPerChannel * frame.NumChannels * 2)
	if len(frame.Data) < expectedBytes {
		return nil, fmt.Errorf("audio frame data is shorter than declared sample count")
	}
	if frame.SamplesPerChannel == 0 {
		return &model.AudioFrame{
			SampleRate:        frame.SampleRate,
			NumChannels:       defaultNvidiaRealtimeNumChannels,
			SamplesPerChannel: 0,
			ParticipantID:     frame.ParticipantID,
		}, nil
	}

	channels := int(frame.NumChannels)
	samplesPerChannel := int(frame.SamplesPerChannel)
	data := make([]byte, samplesPerChannel*2)
	for sampleIdx := 0; sampleIdx < samplesPerChannel; sampleIdx++ {
		sum := int32(0)
		for ch := 0; ch < channels; ch++ {
			offset := (sampleIdx*channels + ch) * 2
			sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset:])))
		}
		mono := int16(sum / int32(channels))
		binary.LittleEndian.PutUint16(data[sampleIdx*2:], uint16(mono))
	}
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        frame.SampleRate,
		NumChannels:       defaultNvidiaRealtimeNumChannels,
		SamplesPerChannel: frame.SamplesPerChannel,
		ParticipantID:     frame.ParticipantID,
	}, nil
}

func (s *nvidiaRealtimeSession) UpdateInstructions(instructions string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.textPrompt == instructions {
		return nil
	}
	s.textPrompt = instructions
	s.resetRealtimeTransportLocked()
	s.finalizeGenerationLocked(true)
	return nil
}

func (s *nvidiaRealtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || chatCtx == nil {
		return nil
	}
	s.chatCtx = chatCtx.Copy()
	return nil
}

func (s *nvidiaRealtimeSession) UpdateTools(_ []llm.Tool) error {
	return nil
}

func (s *nvidiaRealtimeSession) UpdateOptions(_ llm.RealtimeSessionOptions) error {
	return nil
}

func (s *nvidiaRealtimeSession) GenerateReply(_ llm.RealtimeGenerateReplyOptions) error {
	return fmt.Errorf("generate_reply is not yet supported by the PersonaPlex realtime model")
}

func (s *nvidiaRealtimeSession) Say(_ string) error {
	return fmt.Errorf("say is not yet supported by the PersonaPlex realtime model")
}

func (s *nvidiaRealtimeSession) Truncate(_ llm.RealtimeTruncateOptions) error {
	return nil
}

func (s *nvidiaRealtimeSession) Interrupt() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeGenerationLocked(true)
	return nil
}

func (s *nvidiaRealtimeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.finalizeGenerationLocked(true)
	s.resetRealtimeTransportLocked()
	s.closed = true
	close(s.events)
	return nil
}

func (s *nvidiaRealtimeSession) EventCh() <-chan llm.RealtimeEvent {
	return s.events
}

func (s *nvidiaRealtimeSession) PushAudio(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || frame == nil || len(frame.Data) == 0 {
		return nil
	}
	normalized, err := normalizeNvidiaRealtimeInputFrame(frame)
	if err != nil {
		return err
	}
	if normalized == nil || len(normalized.Data) == 0 {
		return nil
	}
	s.outboundAudio = append(s.outboundAudio, cloneNvidiaRealtimeAudioFrame(normalized))
	return s.queueInputAudioMessagesLocked(normalized)
}

func (s *nvidiaRealtimeSession) PushVideo(_ *images.VideoFrame) error {
	return nil
}

func (s *nvidiaRealtimeSession) CommitAudio() error {
	return nil
}

func (s *nvidiaRealtimeSession) ClearAudio() error {
	return nil
}

func (s *nvidiaRealtimeSession) websocketURL() string {
	return buildNvidiaRealtimeWebsocketURL(s.useSSL, s.baseURL, s.voice, s.textPrompt, s.seed)
}

func (s *nvidiaRealtimeSession) resetRealtimeTransportLocked() {
	s.outboundMessages = nil
	s.inputAudioBuffer = nil
	s.opusEncoder = nil
	s.opusDecoder = nil
}

func (s *nvidiaRealtimeSession) queueInputAudioMessagesLocked(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	if s.opusEncoder == nil {
		encoder, err := opus.NewEncoder(defaultNvidiaRealtimeSampleRate, defaultNvidiaRealtimeNumChannels, opus.AppVoIP)
		if err != nil {
			return err
		}
		s.opusEncoder = encoder
	}
	s.inputAudioBuffer = append(s.inputAudioBuffer, frame.Data...)
	chunkBytes := defaultNvidiaRealtimeInputChunkSamples * defaultNvidiaRealtimeNumChannels * 2
	for len(s.inputAudioBuffer) >= chunkBytes {
		chunk := s.inputAudioBuffer[:chunkBytes]
		pcm := littleEndianBytesToInt16Slice(chunk)
		encoded := make([]byte, 4096)
		n, err := s.opusEncoder.Encode(pcm, encoded)
		if err != nil {
			return err
		}
		if n > 0 {
			message := make([]byte, 1+n)
			message[0] = nvidiaRealtimeMsgAudio
			copy(message[1:], encoded[:n])
			s.outboundMessages = append(s.outboundMessages, message)
		}
		s.inputAudioBuffer = s.inputAudioBuffer[chunkBytes:]
	}
	return nil
}

func (s *nvidiaRealtimeSession) handleTextToken(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || text == "" || isNvidiaRealtimeSpecialToken(text) {
		return
	}
	generation := s.ensureGenerationLocked()
	generation.outputText += text
	generation.textCh <- text
}

func (s *nvidiaRealtimeSession) handleTextPayload(payload []byte) {
	if len(payload) == 0 || isNvidiaRealtimeSpecialPayload(payload) || !utf8.Valid(payload) {
		return
	}
	s.handleTextToken(string(payload))
}

func (s *nvidiaRealtimeSession) handleBinaryMessage(data []byte) {
	if len(data) == 0 {
		return
	}
	msgType := data[0]
	payload := data[1:]
	switch msgType {
	case nvidiaRealtimeMsgHandshake:
		return
	case nvidiaRealtimeMsgText:
		s.handleTextPayload(payload)
	case nvidiaRealtimeMsgAudio:
		s.handleAudioPayload(payload)
	}
}

func (s *nvidiaRealtimeSession) handleAudioPayload(payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(payload) == 0 {
		return
	}
	if s.opusDecoder == nil {
		decoder, err := opus.NewDecoder(defaultNvidiaRealtimeSampleRate, defaultNvidiaRealtimeNumChannels)
		if err != nil {
			return
		}
		s.opusDecoder = decoder
	}
	pcm := make([]int16, 5760*defaultNvidiaRealtimeNumChannels)
	n, err := s.opusDecoder.Decode(payload, pcm)
	if err != nil || n == 0 {
		return
	}
	data := int16SliceToLittleEndianBytes(pcm[:n])
	generation := s.ensureGenerationLocked()
	generation.markFirstToken()
	generation.audioCh <- &model.AudioFrame{
		Data:              data,
		SampleRate:        defaultNvidiaRealtimeSampleRate,
		NumChannels:       defaultNvidiaRealtimeNumChannels,
		SamplesPerChannel: uint32(n / defaultNvidiaRealtimeNumChannels),
	}
	s.resetSilenceTimerLocked()
}

func (s *nvidiaRealtimeSession) handleAudioFrame(frame *model.AudioFrame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || frame == nil || len(frame.Data) == 0 {
		return
	}
	generation := s.ensureGenerationLocked()
	generation.markFirstToken()
	generation.audioCh <- frame
	s.resetSilenceTimerLocked()
}

func (s *nvidiaRealtimeSession) ensureGenerationLocked() *nvidiaRealtimeGeneration {
	if s.currentGeneration != nil && !s.currentGeneration.done {
		return s.currentGeneration
	}
	s.generationSeq++
	responseID := fmt.Sprintf("personaplex-turn-%d", s.generationSeq)
	generation := &nvidiaRealtimeGeneration{
		responseID:   responseID,
		messageCh:    make(chan llm.MessageGeneration, 1),
		functionCh:   make(chan *llm.FunctionCall, 1),
		textCh:       make(chan string, 16),
		timedTextCh:  make(chan llm.RealtimeTimedText, 16),
		audioCh:      make(chan *model.AudioFrame, 16),
		modalitiesCh: make(chan []string, 1),
		createdAt:    time.Now(),
	}
	generation.modalitiesCh <- []string{"audio", "text"}
	generation.messageCh <- llm.MessageGeneration{
		MessageID:    responseID,
		TextCh:       generation.textCh,
		TimedTextCh:  generation.timedTextCh,
		AudioCh:      generation.audioCh,
		ModalitiesCh: generation.modalitiesCh,
	}
	s.currentGeneration = generation
	s.events <- llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:  generation.messageCh,
			FunctionCh: generation.functionCh,
			ResponseID: responseID,
		},
	}
	return generation
}

func (s *nvidiaRealtimeSession) finalizeGenerationLocked(interrupted bool) {
	generation := s.currentGeneration
	if generation == nil || generation.done {
		return
	}
	s.cancelSilenceTimerLocked()
	generation.done = true
	close(generation.textCh)
	close(generation.timedTextCh)
	close(generation.audioCh)
	close(generation.functionCh)
	close(generation.messageCh)
	close(generation.modalitiesCh)
	if generation.outputText != "" {
		s.chatCtx.AddMessage(llm.ChatMessageArgs{
			ID:   generation.responseID,
			Role: llm.ChatRoleAssistant,
			Text: generation.outputText,
		})
	}
	if generation.firstTokenAt != nil || generation.outputText != "" {
		ttft := -1.0
		if generation.firstTokenAt != nil {
			ttft = generation.firstTokenAt.Sub(generation.createdAt).Seconds()
		}
		s.events <- llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeMetricsCollected,
			Metrics: &telemetry.RealtimeModelMetrics{
				Label:     s.label,
				RequestID: generation.responseID,
				Timestamp: generation.createdAt,
				Duration:  time.Since(generation.createdAt).Seconds(),
				TTFT:      ttft,
				Cancelled: interrupted,
				Metadata: &telemetry.Metadata{
					ModelName:     s.modelName,
					ModelProvider: s.provider,
				},
			},
		}
	}
	s.currentGeneration = nil
}

func (s *nvidiaRealtimeSession) resetSilenceTimerLocked() {
	s.cancelSilenceTimerLocked()
	threshold := time.Duration(s.silenceThresholdMS) * time.Millisecond
	if threshold < 0 {
		threshold = 0
	}
	s.silenceTimer = time.AfterFunc(threshold, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed || s.currentGeneration == nil || s.currentGeneration.done {
			return
		}
		s.finalizeGenerationLocked(false)
	})
}

func (s *nvidiaRealtimeSession) cancelSilenceTimerLocked() {
	if s.silenceTimer == nil {
		return
	}
	s.silenceTimer.Stop()
	s.silenceTimer = nil
}

func isNvidiaRealtimeSpecialToken(text string) bool {
	return len(text) == 1 && (text[0] == 0 || text[0] == 3)
}

func isNvidiaRealtimeSpecialPayload(payload []byte) bool {
	return len(payload) == 1 && (payload[0] == 0 || payload[0] == 3)
}

func int16SliceToLittleEndianBytes(samples []int16) []byte {
	data := make([]byte, len(samples)*2)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
	}
	return data
}

func littleEndianBytesToInt16Slice(data []byte) []int16 {
	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return samples
}

func (g *nvidiaRealtimeGeneration) markFirstToken() {
	if g.firstTokenAt != nil {
		return
	}
	now := time.Now()
	g.firstTokenAt = &now
}
