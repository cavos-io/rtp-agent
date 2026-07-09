package nvidia

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/gorilla/websocket"
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
	defaultNvidiaRealtimeResampleMinSamples = 960
	defaultNvidiaRealtimeInitialRetryDelay  = time.Second
	defaultNvidiaRealtimeMaxRetryDelay      = 30 * time.Second
	nvidiaRealtimeEventBuffer               = 1024
	nvidiaRealtimeGenerationStreamBuffer    = 1024
	nvidiaPersonaplexURLEnv                 = "PERSONAPLEX_URL"
	nvidiaRealtimeMsgHandshake              = 0x00
	nvidiaRealtimeMsgAudio                  = 0x01
	nvidiaRealtimeMsgText                   = 0x02
	nvidiaRealtimeGenerateReplyUnsupported  = "generate_reply is not yet supported by the PersonaPlex realtime model."
)

type NvidiaRealtimeModel struct {
	baseURL            string
	voice              string
	textPrompt         string
	seed               *int
	silenceThresholdMS int
	useSSL             bool
	preconnect         bool
}

type nvidiaRealtimeSession struct {
	mu                 sync.Mutex
	baseURL            string
	voice              string
	textPrompt         string
	seed               *int
	silenceThresholdMS int
	useSSL             bool
	preconnect         bool
	label              string
	modelName          string
	provider           string
	chatCtx            *llm.ChatContext
	outboundAudio      []*model.AudioFrame
	outboundMessages   [][]byte
	events             *nvidiaRealtimeUnboundedStream[llm.RealtimeEvent]
	opusEncoder        *opus.Encoder
	opusDecoder        *opus.Decoder
	inputAudioBuffer   []byte
	inputResampleRate  uint32
	inputResampleIn    uint64
	inputResampleOut   uint64
	inputResampleLast  []byte
	inputResampleFrame *model.AudioFrame
	transportStarted   bool
	transportCtx       context.Context
	transportCancel    context.CancelFunc
	transportDone      chan struct{}
	transportNotify    chan struct{}
	transportSent      int
	retryDelay         time.Duration
	retryTimer         *time.Timer
	restartPending     bool
	currentGeneration  *nvidiaRealtimeGeneration
	generationSeq      int
	silenceTimer       *time.Timer
	closed             bool
}

type nvidiaRealtimeGeneration struct {
	responseID   string
	messageCh    chan llm.MessageGeneration
	functionCh   chan *llm.FunctionCall
	textStream   *nvidiaRealtimeUnboundedStream[string]
	timedTextCh  chan llm.RealtimeTimedText
	audioStream  *nvidiaRealtimeUnboundedStream[*model.AudioFrame]
	modalitiesCh chan []string
	outputText   string
	createdAt    time.Time
	firstTokenAt *time.Time
	done         bool
}

type nvidiaRealtimeUnboundedStream[T any] struct {
	in        chan T
	out       chan T
	once      sync.Once
	onDeliver func(T)
}

func newNvidiaRealtimeUnboundedStream[T any]() *nvidiaRealtimeUnboundedStream[T] {
	return newNvidiaRealtimeUnboundedStreamWithBuffer[T](nvidiaRealtimeGenerationStreamBuffer)
}

func newNvidiaRealtimeEventStream(onDeliver func(llm.RealtimeEvent)) *nvidiaRealtimeUnboundedStream[llm.RealtimeEvent] {
	return newNvidiaRealtimeUnboundedStreamWithBufferCallback[llm.RealtimeEvent](nvidiaRealtimeEventBuffer, onDeliver)
}

func newNvidiaRealtimeUnboundedStreamWithBuffer[T any](buffer int) *nvidiaRealtimeUnboundedStream[T] {
	return newNvidiaRealtimeUnboundedStreamWithBufferCallback[T](buffer, nil)
}

func newNvidiaRealtimeUnboundedStreamWithBufferCallback[T any](buffer int, onDeliver func(T)) *nvidiaRealtimeUnboundedStream[T] {
	stream := &nvidiaRealtimeUnboundedStream[T]{
		in:        make(chan T, buffer),
		out:       make(chan T, buffer),
		onDeliver: onDeliver,
	}
	go stream.run()
	return stream
}

func (s *nvidiaRealtimeUnboundedStream[T]) run() {
	var pending []T
	in := s.in
	for in != nil || len(pending) > 0 {
		var out chan T
		var next T
		if len(pending) > 0 {
			out = s.out
			next = pending[0]
		}
		select {
		case value, ok := <-in:
			if !ok {
				in = nil
				continue
			}
			pending = append(pending, value)
		case out <- next:
			if s.onDeliver != nil {
				s.onDeliver(next)
			}
			var zero T
			pending[0] = zero
			pending = pending[1:]
		}
	}
	close(s.out)
}

func (s *nvidiaRealtimeUnboundedStream[T]) send(value T) {
	s.in <- value
}

func (s *nvidiaRealtimeUnboundedStream[T]) close() {
	s.once.Do(func() {
		close(s.in)
	})
}

func (s *nvidiaRealtimeUnboundedStream[T]) channel() <-chan T {
	return s.out
}

type NvidiaRealtimeOption func(*NvidiaRealtimeModel)

func WithNvidiaRealtimeBaseURL(baseURL string) NvidiaRealtimeOption {
	return func(m *NvidiaRealtimeModel) {
		if baseURL == "" {
			return
		}
		m.baseURL, m.useSSL = normalizeNvidiaRealtimeBaseURL(baseURL)
		m.preconnect = true
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
		preconnect:         os.Getenv(nvidiaPersonaplexURLEnv) != "",
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
	session := &nvidiaRealtimeSession{
		baseURL:            m.baseURL,
		voice:              m.voice,
		textPrompt:         m.textPrompt,
		seed:               cloneNvidiaRealtimeSeed(m.seed),
		silenceThresholdMS: m.silenceThresholdMS,
		useSSL:             m.useSSL,
		preconnect:         m.preconnect,
		label:              m.Label(),
		modelName:          m.Model(),
		provider:           m.Provider(),
		chatCtx:            llm.EmptyChatContext(),
		transportNotify:    make(chan struct{}),
		retryDelay:         defaultNvidiaRealtimeInitialRetryDelay,
	}
	session.events = newNvidiaRealtimeEventStream(func(ev llm.RealtimeEvent) {
		if ev.Type != llm.RealtimeEventTypeSessionReconnected {
			return
		}
		session.mu.Lock()
		defer session.mu.Unlock()
		session.restartPending = false
	})
	if m.preconnect {
		session.startRealtimeTransportLocked()
	}
	return session, nil
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

func resampleNvidiaRealtimeInputFrame(frame *model.AudioFrame, outputRate uint32, outSamples uint32, inputStart uint64, outputStart uint64, previousSample []byte) (*model.AudioFrame, error) {
	if frame == nil || outputRate == 0 || frame.SampleRate == outputRate {
		return frame, nil
	}
	if frame.SampleRate == 0 {
		return nil, fmt.Errorf("cannot resample audio with zero sample rate")
	}
	if frame.NumChannels == 0 {
		return nil, fmt.Errorf("cannot resample audio with zero channels")
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("cannot resample non-16-bit PCM audio")
	}
	expectedBytes := int(frame.SamplesPerChannel * frame.NumChannels * 2)
	if len(frame.Data) < expectedBytes {
		return nil, fmt.Errorf("audio frame data is shorter than declared sample count")
	}
	if frame.SamplesPerChannel == 0 {
		return &model.AudioFrame{
			SampleRate:        outputRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: 0,
			ParticipantID:     frame.ParticipantID,
		}, nil
	}
	if outSamples == 0 {
		outSamples = uint32((uint64(frame.SamplesPerChannel)*uint64(outputRate) + uint64(frame.SampleRate) - 1) / uint64(frame.SampleRate))
	}
	out := make([]byte, int(outSamples*frame.NumChannels*2))
	inputSamples := int(frame.SamplesPerChannel)
	channelCount := int(frame.NumChannels)
	sampleBytes := channelCount * 2
	for outIdx := 0; outIdx < int(outSamples); outIdx++ {
		srcGlobal := (outputStart + uint64(outIdx)) * uint64(frame.SampleRate) / uint64(outputRate)
		if srcGlobal < inputStart {
			if len(previousSample) == sampleBytes {
				copy(out[outIdx*sampleBytes:(outIdx+1)*sampleBytes], previousSample)
				continue
			}
			srcGlobal = inputStart
		}
		srcIdx := int(srcGlobal - inputStart)
		if srcIdx < 0 {
			srcIdx = 0
		} else if srcIdx >= inputSamples {
			srcIdx = inputSamples - 1
		}
		for ch := 0; ch < channelCount; ch++ {
			inOffset := (srcIdx*channelCount + ch) * 2
			outOffset := (outIdx*channelCount + ch) * 2
			copy(out[outOffset:outOffset+2], frame.Data[inOffset:inOffset+2])
		}
	}
	return &model.AudioFrame{
		Data:              out,
		SampleRate:        outputRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: outSamples,
		ParticipantID:     frame.ParticipantID,
	}, nil
}

func lastNvidiaRealtimeInputSample(frame *model.AudioFrame) []byte {
	if frame == nil || frame.NumChannels == 0 || frame.SamplesPerChannel == 0 {
		return nil
	}
	sampleBytes := int(frame.NumChannels * 2)
	offset := int((frame.SamplesPerChannel - 1) * frame.NumChannels * 2)
	if offset < 0 || offset+sampleBytes > len(frame.Data) {
		return nil
	}
	return append([]byte(nil), frame.Data[offset:offset+sampleBytes]...)
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
	if s.restartPending {
		s.textPrompt = instructions
		return nil
	}
	wasStarted := s.transportStarted
	transportDone := s.transportDone
	wasRetrying := s.retryTimer != nil
	s.textPrompt = instructions
	s.restartPending = true
	s.resetRealtimeTransportLocked()
	s.finalizeGenerationLocked(true)
	if wasStarted && transportDone != nil {
		go s.emitSessionReconnectedAfterTransportDone(transportDone)
		s.startRealtimeTransportLocked()
	} else if s.preconnect {
		if wasRetrying {
			s.restartPending = false
		}
		s.startRealtimeTransportLocked()
		if !wasRetrying {
			s.events.send(llm.RealtimeEvent{
				Type:      llm.RealtimeEventTypeSessionReconnected,
				Reconnect: &llm.RealtimeSessionReconnectedEvent{},
			})
		}
	} else {
		s.events.send(llm.RealtimeEvent{
			Type:      llm.RealtimeEventTypeSessionReconnected,
			Reconnect: &llm.RealtimeSessionReconnectedEvent{},
		})
	}
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
	return fmt.Errorf("%s", nvidiaRealtimeGenerateReplyUnsupported)
}

func (s *nvidiaRealtimeSession) Say(_ string) error {
	return fmt.Errorf("RealtimeSession does not implement say(). use a TTS model instead")
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
	s.restartPending = false
	s.closed = true
	s.events.close()
	return nil
}

func (s *nvidiaRealtimeSession) EventCh() <-chan llm.RealtimeEvent {
	return s.events.channel()
}

func (s *nvidiaRealtimeSession) PushAudio(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || frame == nil || len(frame.Data) == 0 || frame.SampleRate == 0 {
		return nil
	}
	if len(frame.Data)%2 != 0 {
		return nil
	}
	normalized, err := s.normalizeInputFrameLocked(frame)
	if err != nil {
		return nil
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
	s.stopRealtimeTransportLocked()
	s.outboundMessages = nil
	s.transportSent = 0
	s.inputAudioBuffer = nil
	s.inputResampleRate = 0
	s.inputResampleIn = 0
	s.inputResampleOut = 0
	s.inputResampleLast = nil
	s.inputResampleFrame = nil
	s.opusEncoder = nil
	s.opusDecoder = nil
}

func (s *nvidiaRealtimeSession) stopRealtimeTransportLocked() {
	if s.transportCancel != nil {
		s.transportCancel()
		s.transportCancel = nil
	}
	if s.retryTimer != nil {
		s.retryTimer.Stop()
		s.retryTimer = nil
	}
	s.transportStarted = false
	s.transportCtx = nil
	s.transportDone = nil
	s.notifyRealtimeTransportLocked()
}

func (s *nvidiaRealtimeSession) notifyRealtimeTransportLocked() {
	if s.transportNotify == nil {
		s.transportNotify = make(chan struct{})
		return
	}
	close(s.transportNotify)
	s.transportNotify = make(chan struct{})
}

func (s *nvidiaRealtimeSession) normalizeInputFrameLocked(frame *model.AudioFrame) (*model.AudioFrame, error) {
	normalized, err := downmixNvidiaRealtimeInputFrame(frame)
	if err != nil {
		return nil, err
	}
	if normalized == nil || normalized.SampleRate == 0 || normalized.SampleRate == defaultNvidiaRealtimeSampleRate {
		s.inputResampleRate = 0
		s.inputResampleIn = 0
		s.inputResampleOut = 0
		s.inputResampleLast = nil
		s.inputResampleFrame = nil
		return normalized, nil
	}
	if s.inputResampleRate != normalized.SampleRate {
		s.inputResampleRate = normalized.SampleRate
		s.inputResampleIn = 0
		s.inputResampleOut = 0
		s.inputResampleLast = nil
		s.inputResampleFrame = nil
	}
	s.inputResampleFrame = appendNvidiaRealtimeInputFrame(s.inputResampleFrame, normalized)
	minSamples := minNvidiaRealtimeResampleInputSamples(normalized.SampleRate)
	if s.inputResampleFrame == nil || s.inputResampleFrame.SamplesPerChannel < minSamples {
		return nil, nil
	}
	pending := s.inputResampleFrame
	s.inputResampleFrame = nil
	inputStart := s.inputResampleIn
	outputStart := s.inputResampleOut
	inputEnd := inputStart + uint64(pending.SamplesPerChannel)
	outputEnd := inputEnd * uint64(defaultNvidiaRealtimeSampleRate) / uint64(pending.SampleRate)
	outSamples := uint32(outputEnd - outputStart)
	resampled, err := resampleNvidiaRealtimeInputFrame(pending, defaultNvidiaRealtimeSampleRate, outSamples, inputStart, outputStart, s.inputResampleLast)
	if err != nil {
		return nil, err
	}
	s.inputResampleIn = inputEnd
	s.inputResampleOut = outputEnd
	s.inputResampleLast = lastNvidiaRealtimeInputSample(pending)
	return resampled, nil
}

func appendNvidiaRealtimeInputFrame(pending *model.AudioFrame, frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil || len(frame.Data) == 0 {
		return pending
	}
	if pending == nil || pending.SampleRate != frame.SampleRate || pending.NumChannels != frame.NumChannels {
		return cloneNvidiaRealtimeAudioFrame(frame)
	}
	pending.Data = append(pending.Data, frame.Data...)
	pending.SamplesPerChannel += frame.SamplesPerChannel
	if pending.ParticipantID == "" {
		pending.ParticipantID = frame.ParticipantID
	}
	return pending
}

func minNvidiaRealtimeResampleInputSamples(sampleRate uint32) uint32 {
	if sampleRate > defaultNvidiaRealtimeSampleRate {
		return defaultNvidiaRealtimeResampleMinSamples * 2
	}
	return defaultNvidiaRealtimeResampleMinSamples
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
			s.notifyRealtimeTransportLocked()
		}
		s.inputAudioBuffer = s.inputAudioBuffer[chunkBytes:]
	}
	s.ensureRealtimeTransportLocked()
	return nil
}

func (s *nvidiaRealtimeSession) ensureRealtimeTransportLocked() {
	if s.closed || s.transportStarted || len(s.outboundMessages) == 0 {
		return
	}
	s.startRealtimeTransportLocked()
}

func (s *nvidiaRealtimeSession) startRealtimeTransportLocked() {
	if s.closed || s.transportStarted {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.transportStarted = true
	s.transportCtx = ctx
	s.transportCancel = cancel
	s.transportDone = done
	go s.runRealtimeTransport(ctx, done)
}

func (s *nvidiaRealtimeSession) runRealtimeTransport(ctx context.Context, done chan struct{}) {
	defer func() {
		close(done)
		s.mu.Lock()
		if s.transportCtx == ctx {
			s.transportStarted = false
			s.transportCtx = nil
			s.transportCancel = nil
			s.transportDone = nil
		}
		s.mu.Unlock()
	}()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, s.websocketURL(), nil)
	if err != nil {
		s.failRealtimeTransport(ctx, llm.NewAPIConnectionError(fmt.Sprintf("Connection failed: %v", err)))
		return
	}
	s.mu.Lock()
	if s.transportCtx == ctx && !s.closed {
		s.retryDelay = defaultNvidiaRealtimeInitialRetryDelay
	}
	s.mu.Unlock()
	defer conn.Close()
	connDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-connDone:
		}
	}()
	defer close(connDone)

	if !s.waitRealtimeHandshake(ctx, conn) {
		return
	}
	go s.receiveRealtimeTransport(ctx, conn)
	s.sendRealtimeTransport(ctx, conn)
}

func (s *nvidiaRealtimeSession) emitSessionReconnectedAfterTransportDone(done <-chan struct{}) {
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.events.send(llm.RealtimeEvent{
		Type:      llm.RealtimeEventTypeSessionReconnected,
		Reconnect: &llm.RealtimeSessionReconnectedEvent{},
	})
}

func (s *nvidiaRealtimeSession) waitRealtimeHandshake(ctx context.Context, conn *websocket.Conn) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				s.mu.Lock()
				if s.transportCtx == ctx && !s.closed {
					s.resetRealtimeTransportLocked()
					s.startRealtimeTransportLocked()
				}
				s.mu.Unlock()
			} else {
				s.failRealtimeTransport(ctx, llm.NewAPIConnectionError("PersonaPlex connection closed unexpectedly"))
			}
			return false
		}
		if msgType != websocket.BinaryMessage || len(data) == 0 {
			continue
		}
		if data[0] == nvidiaRealtimeMsgHandshake {
			return true
		}
		s.handleBinaryMessage(data)
	}
}

func (s *nvidiaRealtimeSession) receiveRealtimeTransport(ctx context.Context, conn *websocket.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			s.finishRealtimeTransportReceive(ctx, websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived))
			return
		}
		if msgType == websocket.BinaryMessage {
			s.handleBinaryMessage(data)
		}
	}
}

func (s *nvidiaRealtimeSession) finishRealtimeTransportReceive(ctx context.Context, normalClose bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transportCtx != ctx || s.closed {
		return
	}
	if normalClose {
		s.finalizeGenerationLocked(false)
	} else {
		s.finalizeGenerationLocked(true)
		s.events.send(llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: llm.NewRealtimeModelError(s.label, llm.NewAPIConnectionError("PersonaPlex connection closed unexpectedly"), true),
		})
	}
	s.resetRealtimeTransportLocked()
	if normalClose {
		s.startRealtimeTransportLocked()
	} else {
		s.scheduleRealtimeRetryLocked()
	}
}

func (s *nvidiaRealtimeSession) failRealtimeTransport(ctx context.Context, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transportCtx != ctx || s.closed {
		return
	}
	s.finalizeGenerationLocked(true)
	s.events.send(llm.RealtimeEvent{
		Type:  llm.RealtimeEventTypeError,
		Error: llm.NewRealtimeModelError(s.label, err, true),
	})
	s.resetRealtimeTransportLocked()
	s.scheduleRealtimeRetryLocked()
}

func (s *nvidiaRealtimeSession) scheduleRealtimeRetryLocked() {
	if s.closed || s.retryTimer != nil {
		return
	}
	delay := s.retryDelay
	if delay <= 0 {
		delay = defaultNvidiaRealtimeInitialRetryDelay
	}
	nextDelay := delay * 2
	if nextDelay > defaultNvidiaRealtimeMaxRetryDelay {
		nextDelay = defaultNvidiaRealtimeMaxRetryDelay
	}
	s.retryDelay = nextDelay
	s.retryTimer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.retryTimer = nil
		s.startRealtimeTransportLocked()
	})
}

func (s *nvidiaRealtimeSession) sendRealtimeTransport(ctx context.Context, conn *websocket.Conn) {
	for {
		for {
			msg := s.nextRealtimeTransportMessage()
			if msg == nil {
				break
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				s.failRealtimeTransport(ctx, llm.NewAPIConnectionError(fmt.Sprintf("Connection failed: %v", err)))
				return
			}
		}
		ch := s.realtimeTransportNotifyCh()
		select {
		case <-ctx.Done():
			return
		case <-ch:
		}
	}
}

func (s *nvidiaRealtimeSession) nextRealtimeTransportMessage() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transportSent >= len(s.outboundMessages) {
		return nil
	}
	msg := append([]byte(nil), s.outboundMessages[s.transportSent]...)
	s.transportSent++
	return msg
}

func (s *nvidiaRealtimeSession) realtimeTransportNotifyCh() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transportNotify
}

func (s *nvidiaRealtimeSession) handleTextToken(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || text == "" || isNvidiaRealtimeSpecialToken(text) {
		return
	}
	generation := s.ensureGenerationLocked()
	generation.outputText += text
	generation.textStream.send(text)
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
	generation.audioStream.send(&model.AudioFrame{
		Data:              data,
		SampleRate:        defaultNvidiaRealtimeSampleRate,
		NumChannels:       defaultNvidiaRealtimeNumChannels,
		SamplesPerChannel: uint32(n / defaultNvidiaRealtimeNumChannels),
	})
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
	generation.audioStream.send(frame)
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
		textStream:   newNvidiaRealtimeUnboundedStream[string](),
		timedTextCh:  make(chan llm.RealtimeTimedText, nvidiaRealtimeGenerationStreamBuffer),
		audioStream:  newNvidiaRealtimeUnboundedStream[*model.AudioFrame](),
		modalitiesCh: make(chan []string, 1),
		createdAt:    time.Now(),
	}
	generation.modalitiesCh <- []string{"audio", "text"}
	generation.messageCh <- llm.MessageGeneration{
		MessageID:    responseID,
		TextCh:       generation.textStream.channel(),
		TimedTextCh:  generation.timedTextCh,
		AudioCh:      generation.audioStream.channel(),
		ModalitiesCh: generation.modalitiesCh,
	}
	s.currentGeneration = generation
	s.events.send(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:  generation.messageCh,
			FunctionCh: generation.functionCh,
			ResponseID: responseID,
		},
	})
	return generation
}

func (s *nvidiaRealtimeSession) finalizeGenerationLocked(interrupted bool) {
	generation := s.currentGeneration
	if generation == nil || generation.done {
		return
	}
	s.cancelSilenceTimerLocked()
	generation.done = true
	generation.textStream.close()
	close(generation.timedTextCh)
	generation.audioStream.close()
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
		s.events.send(llm.RealtimeEvent{
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
		})
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
