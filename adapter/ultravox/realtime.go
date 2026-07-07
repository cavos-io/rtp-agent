package ultravox

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

const (
	defaultRealtimeBaseURL          = "https://api.ultravox.ai/api"
	defaultRealtimeModel            = "fixie-ai/ultravox"
	defaultRealtimeVoice            = "Mark"
	defaultRealtimeSystemPrompt     = "You are a helpful assistant."
	defaultRealtimeInputSampleRate  = 16000
	defaultRealtimeOutputSampleRate = 24000
	defaultRealtimeOutputMedium     = "voice"
	defaultRealtimeFirstSpeaker     = "FIRST_SPEAKER_USER"
	ultravoxRealtimeInputChannels   = 1
)

type RealtimeModel struct {
	apiKey              string
	model               string
	voice               string
	baseURL             string
	systemPrompt        string
	outputMedium        string
	inputSampleRate     int
	outputSampleRate    int
	temperature         float64
	temperatureSet      bool
	languageHint        string
	languageHintSet     bool
	maxDuration         string
	maxDurationSet      bool
	timeExceededMessage string
	timeExceededSet     bool
	enableGreeting      bool
	enableGreetingSet   bool
	firstSpeaker        string
	firstSpeakerSet     bool
}

type RealtimeOption func(*RealtimeModel)
type RealtimeUpdateOption func(*realtimeUpdateOptions)

type realtimeUpdateOptions struct {
	outputMedium *string
}

func NewRealtimeModel(apiKey string, opts ...RealtimeOption) (*RealtimeModel, error) {
	if apiKey == "" {
		apiKey = os.Getenv("ULTRAVOX_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("ultravox API key is required. Provide it via api_key parameter or ULTRAVOX_API_KEY environment variable")
	}
	model := &RealtimeModel{
		apiKey:           apiKey,
		model:            defaultRealtimeModel,
		voice:            defaultRealtimeVoice,
		baseURL:          defaultRealtimeBaseURL,
		systemPrompt:     defaultRealtimeSystemPrompt,
		outputMedium:     defaultRealtimeOutputMedium,
		inputSampleRate:  defaultRealtimeInputSampleRate,
		outputSampleRate: defaultRealtimeOutputSampleRate,
		firstSpeaker:     defaultRealtimeFirstSpeaker,
		firstSpeakerSet:  true,
	}
	for _, opt := range opts {
		opt(model)
	}
	return model, nil
}

func WithRealtimeModel(model string) RealtimeOption {
	return func(m *RealtimeModel) {
		if model != "" {
			m.model = model
		}
	}
}

func WithRealtimeVoice(voice string) RealtimeOption {
	return func(m *RealtimeModel) {
		if voice != "" {
			m.voice = voice
		}
	}
}

func WithRealtimeBaseURL(baseURL string) RealtimeOption {
	return func(m *RealtimeModel) {
		if baseURL != "" {
			m.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithRealtimeSystemPrompt(prompt string) RealtimeOption {
	return func(m *RealtimeModel) {
		if prompt != "" {
			m.systemPrompt = prompt
		}
	}
}

func WithRealtimeOutputMedium(outputMedium string) RealtimeOption {
	return func(m *RealtimeModel) {
		if outputMedium != "" {
			m.outputMedium = outputMedium
		}
	}
}

func WithRealtimeInputSampleRate(sampleRate int) RealtimeOption {
	return func(m *RealtimeModel) {
		if sampleRate > 0 {
			m.inputSampleRate = sampleRate
		}
	}
}

func WithRealtimeOutputSampleRate(sampleRate int) RealtimeOption {
	return func(m *RealtimeModel) {
		if sampleRate > 0 {
			m.outputSampleRate = sampleRate
		}
	}
}

func WithRealtimeTemperature(temperature float64) RealtimeOption {
	return func(m *RealtimeModel) {
		m.temperature = temperature
		m.temperatureSet = true
	}
}

func WithRealtimeLanguageHint(languageHint string) RealtimeOption {
	return func(m *RealtimeModel) {
		if languageHint != "" {
			m.languageHint = languageHint
			m.languageHintSet = true
		}
	}
}

func WithRealtimeMaxDuration(maxDuration string) RealtimeOption {
	return func(m *RealtimeModel) {
		if maxDuration != "" {
			m.maxDuration = maxDuration
			m.maxDurationSet = true
		}
	}
}

func WithRealtimeTimeExceededMessage(message string) RealtimeOption {
	return func(m *RealtimeModel) {
		if message != "" {
			m.timeExceededMessage = message
			m.timeExceededSet = true
		}
	}
}

func WithRealtimeEnableGreetingPrompt(enable bool) RealtimeOption {
	return func(m *RealtimeModel) {
		m.enableGreeting = enable
		m.enableGreetingSet = true
	}
}

func WithRealtimeFirstSpeaker(firstSpeaker string) RealtimeOption {
	return func(m *RealtimeModel) {
		if firstSpeaker != "" {
			m.firstSpeaker = firstSpeaker
			m.firstSpeakerSet = true
		}
	}
}

func WithRealtimeUpdateOutputMedium(outputMedium string) RealtimeUpdateOption {
	return func(opts *realtimeUpdateOptions) {
		if outputMedium != "" {
			opts.outputMedium = &outputMedium
		}
	}
}

func (m *RealtimeModel) Label() string { return "ultravox-" + m.model }
func (m *RealtimeModel) Model() string { return m.model }
func (m *RealtimeModel) Provider() string {
	return "Ultravox"
}
func (m *RealtimeModel) APIKey() string               { return m.apiKey }
func (m *RealtimeModel) Voice() string                { return m.voice }
func (m *RealtimeModel) BaseURL() string              { return m.baseURL }
func (m *RealtimeModel) SystemPrompt() string         { return m.systemPrompt }
func (m *RealtimeModel) OutputMedium() string         { return m.outputMedium }
func (m *RealtimeModel) InputSampleRate() int         { return m.inputSampleRate }
func (m *RealtimeModel) OutputSampleRate() int        { return m.outputSampleRate }
func (m *RealtimeModel) Temperature() (float64, bool) { return m.temperature, m.temperatureSet }
func (m *RealtimeModel) LanguageHint() (string, bool) { return m.languageHint, m.languageHintSet }
func (m *RealtimeModel) MaxDuration() (string, bool)  { return m.maxDuration, m.maxDurationSet }
func (m *RealtimeModel) TimeExceededMessage() (string, bool) {
	return m.timeExceededMessage, m.timeExceededSet
}
func (m *RealtimeModel) EnableGreetingPrompt() (bool, bool) {
	return m.enableGreeting, m.enableGreetingSet
}
func (m *RealtimeModel) FirstSpeaker() (string, bool) { return m.firstSpeaker, m.firstSpeakerSet }

func (m *RealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{
		MessageTruncation:       true,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             m.outputMedium == "voice",
		ManualFunctionCalls:     false,
		PerResponseToolChoice:   false,
	}
}

func (m *RealtimeModel) UpdateOptions(opts ...RealtimeUpdateOption) {
	var update realtimeUpdateOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&update)
		}
	}
	if update.outputMedium != nil {
		m.outputMedium = *update.outputMedium
	}
}

func (m *RealtimeModel) Session() (llm.RealtimeSession, error) {
	return &realtimeSession{
		eventCh:          make(chan llm.RealtimeEvent, 16),
		audioCh:          make(chan []byte, 256),
		clientEventCh:    make(chan map[string]any, 256),
		inputSampleRate:  uint32(m.inputSampleRate),
		outputSampleRate: uint32(m.outputSampleRate),
		audioStream:      coreaudio.NewAudioByteStream(uint32(m.inputSampleRate), ultravoxRealtimeInputChannels, uint32(m.inputSampleRate)/10),
	}, nil
}

func (m *RealtimeModel) Close() error { return nil }

type ultravoxRealtimeGeneration struct {
	messageCh  chan llm.MessageGeneration
	functionCh chan *llm.FunctionCall
	textCh     chan string
	audioCh    chan *model.AudioFrame
	done       bool
}

type ultravoxRealtimeTranscriptEvent struct {
	Role    string
	Text    string
	Delta   string
	Final   bool
	Ordinal int
}

type realtimeSession struct {
	mu               sync.Mutex
	eventCh          chan llm.RealtimeEvent
	audioCh          chan []byte
	clientEventCh    chan map[string]any
	inputSampleRate  uint32
	outputSampleRate uint32
	audioStream      *coreaudio.AudioByteStream
	generation       *ultravoxRealtimeGeneration
	closed           bool
	closeOnce        sync.Once
}

func (s *realtimeSession) UpdateInstructions(string) error {
	return ultravoxRealtimeSessionUnsupported("update_instructions")
}
func (s *realtimeSession) UpdateChatContext(*llm.ChatContext) error {
	return ultravoxRealtimeSessionUnsupported("update_chat_context")
}
func (s *realtimeSession) UpdateTools([]llm.Tool) error {
	return ultravoxRealtimeSessionUnsupported("update_tools")
}
func (s *realtimeSession) UpdateOptions(llm.RealtimeSessionOptions) error {
	return ultravoxRealtimeSessionUnsupported("update_options")
}
func (s *realtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	text := ""
	if options.InstructionsSet {
		text = "<instruction>" + options.Instructions + "</instruction>"
	}
	return s.sendClientEvent(map[string]any{
		"type":          "user_text_message",
		"text":          text,
		"deferResponse": false,
	})
}
func (s *realtimeSession) Say(string) error {
	return ultravoxRealtimeSessionUnsupported("say")
}
func (s *realtimeSession) Truncate(llm.RealtimeTruncateOptions) error { return nil }
func (s *realtimeSession) Interrupt() error {
	return ultravoxRealtimeSessionUnsupported("interrupt")
}
func (s *realtimeSession) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closed = true
		close(s.eventCh)
		close(s.audioCh)
		close(s.clientEventCh)
	})
	return nil
}
func (s *realtimeSession) EventCh() <-chan llm.RealtimeEvent { return s.eventCh }
func (s *realtimeSession) PushAudio(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}

	audioFrame, err := ultravoxRealtimeInputAudioFrame(frame, s.inputSampleRate)
	if err != nil {
		return err
	}
	if audioFrame == nil || len(audioFrame.Data) == 0 {
		return nil
	}
	for _, chunk := range s.audioStream.Push(audioFrame.Data) {
		audioData := append([]byte(nil), chunk.Data...)
		select {
		case s.audioCh <- audioData:
		default:
			return errors.New("ultravox realtime audio queue is full")
		}
	}
	return nil
}
func (s *realtimeSession) PushVideo(*images.VideoFrame) error {
	return ultravoxRealtimeSessionUnsupported("push_video")
}
func (s *realtimeSession) CommitAudio() error {
	return nil
}
func (s *realtimeSession) ClearAudio() error {
	return nil
}

var _ llm.RealtimeSession = (*realtimeSession)(nil)

func ultravoxRealtimeSessionUnsupported(operation string) error {
	return errors.New(operation + " is not implemented by the Ultravox realtime session")
}

func (s *realtimeSession) sendClientEvent(event map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	select {
	case s.clientEventCh <- event:
		return nil
	default:
		return errors.New("ultravox realtime client event queue is full")
	}
}

func (s *realtimeSession) handleOutputAudio(audioData []byte) {
	if len(audioData) == 0 {
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	generation := s.ensureGenerationLocked()
	frame := &model.AudioFrame{
		Data:              append([]byte(nil), audioData...),
		SampleRate:        s.outputSampleRate,
		NumChannels:       ultravoxRealtimeInputChannels,
		SamplesPerChannel: uint32(len(audioData)) / (2 * ultravoxRealtimeInputChannels),
	}
	s.mu.Unlock()

	select {
	case generation.audioCh <- frame:
	default:
	}
}

func (s *realtimeSession) ensureGenerationLocked() *ultravoxRealtimeGeneration {
	if s.generation != nil && !s.generation.done {
		return s.generation
	}
	generation := &ultravoxRealtimeGeneration{
		messageCh:  make(chan llm.MessageGeneration, 1),
		functionCh: make(chan *llm.FunctionCall, 1),
		textCh:     make(chan string, 16),
		audioCh:    make(chan *model.AudioFrame, 16),
	}
	generation.messageCh <- llm.MessageGeneration{
		MessageID: "ultravox-turn",
		TextCh:    generation.textCh,
		AudioCh:   generation.audioCh,
	}
	s.generation = generation
	s.eventCh <- llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			MessageCh:  generation.messageCh,
			FunctionCh: generation.functionCh,
		},
	}
	return generation
}

func (s *realtimeSession) handleTranscriptEvent(event ultravoxRealtimeTranscriptEvent) {
	if event.Role != "user" || event.Text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	realtimeEvent := llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     fmt.Sprintf("msg_user_%d", event.Ordinal),
			Transcript: event.Text,
			IsFinal:    event.Final,
		},
	}
	select {
	case s.eventCh <- realtimeEvent:
	default:
	}
}

func ultravoxRealtimeInputAudioFrame(frame *model.AudioFrame, sampleRate uint32) (*model.AudioFrame, error) {
	resampled, err := coreaudio.ResampleAudioFrame(frame, sampleRate)
	if err != nil {
		return nil, err
	}
	if resampled == nil || resampled.NumChannels <= ultravoxRealtimeInputChannels {
		return resampled, nil
	}
	if len(resampled.Data)%2 != 0 {
		return nil, errors.New("ultravox realtime audio input must be 16-bit PCM")
	}
	channels := int(resampled.NumChannels)
	samplesPerChannel := int(resampled.SamplesPerChannel)
	if samplesPerChannel == 0 {
		samplesPerChannel = (len(resampled.Data) / 2) / channels
	}
	expectedBytes := samplesPerChannel * channels * 2
	if len(resampled.Data) < expectedBytes {
		return nil, errors.New("ultravox realtime audio input is shorter than declared sample count")
	}

	mono := make([]byte, samplesPerChannel*2)
	for sample := 0; sample < samplesPerChannel; sample++ {
		var sum int32
		for channel := 0; channel < channels; channel++ {
			offset := (sample*channels + channel) * 2
			sum += int32(int16(binary.LittleEndian.Uint16(resampled.Data[offset:])))
		}
		binary.LittleEndian.PutUint16(mono[sample*2:], uint16(int16(sum/int32(channels))))
	}
	return &model.AudioFrame{
		Data:              mono,
		SampleRate:        sampleRate,
		NumChannels:       ultravoxRealtimeInputChannels,
		SamplesPerChannel: uint32(samplesPerChannel),
		ParticipantID:     resampled.ParticipantID,
	}, nil
}
