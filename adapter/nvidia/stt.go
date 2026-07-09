package nvidia

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

const (
	defaultNvidiaSTTServer     = "grpc.nvcf.nvidia.com:443"
	defaultNvidiaSTTModel      = "parakeet-1.1b-en-US-asr-streaming-silero-vad-sortformer"
	defaultNvidiaSTTFunctionID = "1598d209-5e27-4d3c-8079-4751568b1081"
	defaultNvidiaSTTLanguage   = "en-US"
	defaultNvidiaSTTSampleRate = 16000
)

type NvidiaSTT struct {
	apiKey          string
	apiKeyExplicit  bool
	model           string
	functionID      string
	server          string
	language        string
	sampleRate      int
	punctuate       bool
	useSSL          bool
	diarization     bool
	maxSpeakerCount int
}

type NvidiaSTTOption func(*NvidiaSTT)

func WithNvidiaSTTAPIKey(apiKey string) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.apiKey = apiKey
		s.apiKeyExplicit = true
	}
}

func WithNvidiaSTTServer(server string) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.server = server
	}
}

func WithNvidiaSTTFunctionID(functionID string) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.functionID = functionID
	}
}

func WithNvidiaSTTModel(model string) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.model = model
	}
}

func WithNvidiaSTTLanguage(language string) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.language = language
	}
}

func WithNvidiaSTTSampleRate(sampleRate int) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.sampleRate = sampleRate
	}
}

func WithNvidiaSTTPunctuate(enabled bool) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.punctuate = enabled
	}
}

func WithNvidiaSTTUseSSL(useSSL bool) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.useSSL = useSSL
	}
}

func WithNvidiaSTTDiarization(enabled bool) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.diarization = enabled
	}
}

func WithNvidiaSTTMaxSpeakerCount(count int) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.maxSpeakerCount = count
	}
}

func NewNvidiaSTT(apiKey string, model string, opts ...NvidiaSTTOption) (*NvidiaSTT, error) {
	if apiKey == "" {
		apiKey = os.Getenv(nvidiaAPIKeyEnv)
	}
	if model == "" {
		model = defaultNvidiaSTTModel
	}

	provider := &NvidiaSTT{
		apiKey:          apiKey,
		model:           model,
		functionID:      defaultNvidiaSTTFunctionID,
		server:          defaultNvidiaSTTServer,
		language:        defaultNvidiaSTTLanguage,
		sampleRate:      defaultNvidiaSTTSampleRate,
		punctuate:       true,
		useSSL:          true,
		diarization:     false,
		maxSpeakerCount: 0,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.useSSL && provider.apiKey == "" && !provider.apiKeyExplicit {
		return nil, fmt.Errorf("nvidia api key is required while using SSL")
	}
	return provider, nil
}

func (s *NvidiaSTT) Label() string { return "nvidia.STT" }
func (s *NvidiaSTT) Model() string { return s.model }
func (s *NvidiaSTT) Provider() string {
	return "nvidia"
}
func (s *NvidiaSTT) InputSampleRate() uint32 {
	if s == nil {
		return defaultNvidiaSTTSampleRate
	}
	if s.sampleRate < 0 {
		return 0
	}
	return uint32(s.sampleRate)
}
func (s *NvidiaSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    true,
		Diarization:       s.diarization,
		AlignedTranscript: "word",
		OfflineRecognize:  false,
	}
}

func (s *NvidiaSTT) Recognize(ctx context.Context, _ []*model.AudioFrame, _ string) (*stt.SpeechEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("nvidia riva stt recognition is not implemented")
}

func (s *NvidiaSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	streamLanguage := s.language
	if language != "" {
		streamLanguage = language
	}
	return &nvidiaSTTStream{
		stt:          s,
		ctx:          ctx,
		language:     streamLanguage,
		stateChanged: make(chan struct{}),
		startTime:    float64(time.Now().UnixNano()) / float64(time.Second),
	}, nil
}

type nvidiaSTTStream struct {
	mu              sync.Mutex
	stateChanged    chan struct{}
	stt             *NvidiaSTT
	ctx             context.Context
	language        string
	closed          bool
	inputEnded      bool
	flushed         bool
	streamErr       error
	speaking        bool
	inputSampleRate uint32
	startTimeOffset float64
	startTime       float64
	requestSeq      int
}

type nvidiaSTTWord struct {
	Word       string
	StartTime  float64
	EndTime    float64
	SpeakerTag int
}

type nvidiaSTTAlternative struct {
	Transcript string
	Confidence float64
	Words      []nvidiaSTTWord
}

type nvidiaSTTResult struct {
	RequestID   string
	IsFinal     bool
	Alternative nvidiaSTTAlternative
}

type nvidiaSTTResponse struct {
	RequestID string
	Results   []nvidiaSTTResult
}

func (s *nvidiaSTTStream) notifyLocked() {
	close(s.stateChanged)
	s.stateChanged = make(chan struct{})
}

func (s *nvidiaSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded {
		return io.ErrClosedPipe
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	if err := s.checkInputSampleRate(frame); err != nil {
		return err
	}
	if s.flushed {
		return nil
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	err := fmt.Errorf("nvidia riva stt streaming is not implemented")
	s.streamErr = err
	s.notifyLocked()
	return nil
}

func (s *nvidiaSTTStream) checkInputSampleRate(frame *model.AudioFrame) error {
	if frame == nil {
		return nil
	}
	if s.inputSampleRate == 0 {
		if frame.SampleRate == 0 {
			return nil
		}
		s.inputSampleRate = frame.SampleRate
		return nil
	}
	if s.inputSampleRate != frame.SampleRate {
		return fmt.Errorf("the sample rate of the input frames must be consistent")
	}
	return nil
}

func (s *nvidiaSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded {
		return io.ErrClosedPipe
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	s.flushed = true
	s.notifyLocked()
	return nil
}

func (s *nvidiaSTTStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return io.ErrClosedPipe
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	s.flushed = true
	s.inputEnded = true
	s.notifyLocked()
	return nil
}

func (s *nvidiaSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.notifyLocked()
	return nil
}

func (s *nvidiaSTTStream) Next() (*stt.SpeechEvent, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.ctx != nil {
			if err := s.ctx.Err(); err != nil {
				s.mu.Unlock()
				return nil, err
			}
		}
		if s.streamErr != nil {
			err := s.streamErr
			s.mu.Unlock()
			return nil, err
		}
		if s.inputEnded {
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.flushed {
			s.mu.Unlock()
			return nil, io.EOF
		}
		changed := s.stateChanged
		ctx := s.ctx
		s.mu.Unlock()
		if ctx == nil {
			<-changed
			continue
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (s *nvidiaSTTStream) eventsFromResult(result nvidiaSTTResult) []stt.SpeechEvent {
	transcript := result.Alternative.Transcript
	if strings.TrimSpace(transcript) == "" {
		return nil
	}

	events := make([]stt.SpeechEvent, 0, 3)
	if !s.speaking {
		s.speaking = true
		events = append(events, stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
	}

	eventType := stt.SpeechEventInterimTranscript
	if result.IsFinal {
		eventType = stt.SpeechEventFinalTranscript
	}
	events = append(events, stt.SpeechEvent{
		Type:         eventType,
		RequestID:    result.RequestID,
		Alternatives: []stt.SpeechData{s.speechDataFromAlternative(result.Alternative, result.IsFinal)},
	})
	if result.IsFinal && s.speaking {
		events = append(events, stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
	}
	return events
}

func (s *nvidiaSTTStream) eventsFromResponse(response nvidiaSTTResponse) []stt.SpeechEvent {
	events := make([]stt.SpeechEvent, 0, len(response.Results)+2)
	requestID := response.RequestID
	for _, result := range response.Results {
		if strings.TrimSpace(result.Alternative.Transcript) == "" {
			continue
		}
		if requestID == "" {
			s.requestSeq++
			requestID = fmt.Sprintf("nvidia-response-%d", s.requestSeq)
		}
		result.RequestID = requestID
		events = append(events, s.eventsFromResult(result)...)
	}
	return events
}

func (s *nvidiaSTTStream) speechDataFromAlternative(alternative nvidiaSTTAlternative, isFinal bool) stt.SpeechData {
	data := stt.SpeechData{
		Language:   s.language,
		Text:       alternative.Transcript,
		Confidence: alternative.Confidence,
	}
	if len(alternative.Words) == 0 {
		return data
	}

	first := alternative.Words[0]
	last := alternative.Words[len(alternative.Words)-1]
	data.StartTime = first.StartTime/1000.0 + s.startTimeOffset
	data.EndTime = last.EndTime/1000.0 + s.startTimeOffset
	data.Words = make([]stt.TimedString, 0, len(alternative.Words))
	for _, word := range alternative.Words {
		data.Words = append(data.Words, stt.TimedString{
			Text:      word.Word,
			StartTime: word.StartTime + s.startTimeOffset,
			EndTime:   word.EndTime + s.startTimeOffset,
		})
	}
	if s.stt != nil && s.stt.diarization && isFinal {
		if speakerTag, ok := majoritySpeakerTag(alternative.Words); ok {
			data.SpeakerID = fmt.Sprintf("S%d", speakerTag)
		}
	}
	return data
}

func majoritySpeakerTag(words []nvidiaSTTWord) (int, bool) {
	counts := make(map[int]int)
	bestCount := 0
	for _, word := range words {
		counts[word.SpeakerTag]++
		if counts[word.SpeakerTag] > bestCount {
			bestCount = counts[word.SpeakerTag]
		}
	}
	if bestCount == 0 {
		return 0, false
	}
	for _, word := range words {
		if counts[word.SpeakerTag] == bestCount {
			return word.SpeakerTag, true
		}
	}
	return 0, false
}

func (s *nvidiaSTTStream) StartTimeOffset() float64 {
	return s.startTimeOffset
}

func (s *nvidiaSTTStream) SetStartTimeOffset(offset float64) {
	if offset < 0 {
		panic("start_time_offset must be non-negative")
	}
	s.startTimeOffset = offset
}

func (s *nvidiaSTTStream) StartTime() float64 {
	return s.startTime
}

func (s *nvidiaSTTStream) SetStartTime(startTime float64) {
	if startTime < 0 {
		panic("start_time must be non-negative")
	}
	s.startTime = startTime
}
