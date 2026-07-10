package nvidia

import (
	"context"
	"errors"
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
	defaultNvidiaSTTServer        = "grpc.nvcf.nvidia.com:443"
	defaultNvidiaSTTModel         = "parakeet-1.1b-en-US-asr-streaming-silero-vad-sortformer"
	defaultNvidiaSTTFunctionID    = "1598d209-5e27-4d3c-8079-4751568b1081"
	defaultNvidiaSTTLanguage      = "en-US"
	defaultNvidiaSTTSampleRate    = 16000
	nvidiaSTTRecognizeUnsupported = "Not implemented"
	nvidiaSTTMissingAPIKey        = "NVIDIA_API_KEY is not set while using SSL. Either pass api_key parameter, set NVIDIA_API_KEY environment variable or disable SSL and use a locally hosted Riva NIM service."
)

var errNvidiaSTTStreamInputEnded = errors.New("stream input ended")

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
	clientFactory   nvidiaSTTClientFactory
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
		clientFactory:   newNvidiaSTTClient,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.useSSL && provider.apiKey == "" && !provider.apiKeyExplicit {
		return nil, fmt.Errorf("%s", nvidiaSTTMissingAPIKey)
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
	return nil, fmt.Errorf("%s", nvidiaSTTRecognizeUnsupported)
}

func (s *NvidiaSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	streamLanguage := s.language
	if language != "" {
		streamLanguage = language
	}
	transportCtx, transportCancel := context.WithCancel(ctx)
	stream := &nvidiaSTTStream{
		stt:             s,
		ctx:             ctx,
		transportCtx:    transportCtx,
		language:        streamLanguage,
		stateChanged:    make(chan struct{}),
		transportCancel: transportCancel,
		transportDone:   make(chan struct{}),
		transportNotify: make(chan struct{}),
		startTime:       float64(time.Now().UnixNano()) / float64(time.Second),
	}
	go stream.runTransport()
	return stream, nil
}

type nvidiaSTTStream struct {
	mu                 sync.Mutex
	stateChanged       chan struct{}
	stt                *NvidiaSTT
	ctx                context.Context
	transportCtx       context.Context
	language           string
	closed             bool
	inputEnded         bool
	flushed            bool
	streamErr          error
	streamErrReturned  bool
	speaking           bool
	pushedSampleRate   uint32
	inputSampleRate    uint32
	inputResampleIn    uint64
	inputResampleOut   uint64
	inputResampleLast  []byte
	inputResampleFrame *model.AudioFrame
	startTimeOffset    float64
	startTime          float64
	requestSeq         int
	transportCancel    context.CancelFunc
	transportDone      chan struct{}
	transportNotify    chan struct{}
	transportAudio     [][]byte
	transportEOF       bool
	transportFinished  bool
	events             []stt.SpeechEvent
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
		return errNvidiaSTTStreamInputEnded
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	if err := s.checkInputSampleRate(frame); err != nil {
		return err
	}
	frame, normalizeErr := s.normalizeInputFrame(frame)
	if normalizeErr != nil {
		return normalizeErr
	}
	if s.flushed {
		return nil
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.enqueueTransportAudioLocked(frame.Data)
	return nil
}

func (s *nvidiaSTTStream) checkInputSampleRate(frame *model.AudioFrame) error {
	if frame == nil {
		return nil
	}
	if s.pushedSampleRate == 0 {
		if frame.SampleRate == 0 {
			return nil
		}
		s.pushedSampleRate = frame.SampleRate
		return nil
	}
	if s.pushedSampleRate != frame.SampleRate {
		return fmt.Errorf("the sample rate of the input frames must be consistent")
	}
	return nil
}

func (s *nvidiaSTTStream) normalizeInputFrame(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if frame == nil {
		return nil, nil
	}
	if len(frame.Data) == 0 {
		s.inputSampleRate = frame.SampleRate
		return frame, nil
	}
	normalized, err := downmixNvidiaRealtimeInputFrame(frame)
	if err != nil {
		return nil, err
	}
	frame = normalized
	outputRate := uint32(0)
	if s.stt != nil && s.stt.sampleRate > 0 {
		outputRate = uint32(s.stt.sampleRate)
	}
	if outputRate == 0 || frame.SampleRate == 0 || frame.SampleRate == outputRate {
		s.inputSampleRate = frame.SampleRate
		s.inputResampleIn = 0
		s.inputResampleOut = 0
		s.inputResampleLast = nil
		s.inputResampleFrame = nil
		return frame, nil
	}
	s.inputSampleRate = outputRate
	s.inputResampleFrame = appendNvidiaRealtimeInputFrame(s.inputResampleFrame, frame)
	if s.inputResampleFrame == nil || s.inputResampleFrame.SamplesPerChannel < minNvidiaSTTResampleInputSamples(frame.SampleRate, outputRate) {
		return nil, nil
	}
	return s.resamplePendingInputFrame(outputRate, false)
}

func (s *nvidiaSTTStream) resamplePendingInputFrame(outputRate uint32, flush bool) (*model.AudioFrame, error) {
	pending := s.inputResampleFrame
	if pending == nil {
		return nil, nil
	}
	inputStart := s.inputResampleIn
	outputStart := s.inputResampleOut
	inputEnd := inputStart + uint64(pending.SamplesPerChannel)
	outputEnd := inputEnd * uint64(outputRate) / uint64(pending.SampleRate)
	if flush {
		outputEnd = (inputEnd*uint64(outputRate) + uint64(pending.SampleRate) - 1) / uint64(pending.SampleRate)
	}
	if outputEnd <= outputStart {
		return nil, nil
	}
	outSamples := uint32(outputEnd - outputStart)
	normalized, err := resampleNvidiaRealtimeInputFrame(pending, outputRate, outSamples, inputStart, outputStart, s.inputResampleLast)
	if err != nil {
		return nil, err
	}
	s.inputResampleFrame = nil
	s.inputResampleIn = inputEnd
	s.inputResampleOut = outputEnd
	s.inputResampleLast = lastNvidiaRealtimeInputSample(pending)
	if normalized != nil {
		s.inputSampleRate = normalized.SampleRate
	}
	return normalized, nil
}

func minNvidiaSTTResampleInputSamples(inputRate uint32, outputRate uint32) uint32 {
	if inputRate == 0 || outputRate == 0 || inputRate <= outputRate {
		return 1
	}
	return uint32((uint64(inputRate) + uint64(outputRate) - 1) / uint64(outputRate))
}

func (s *nvidiaSTTStream) drainPendingResampleInputLocked() error {
	if s.inputResampleFrame == nil || s.flushed {
		return nil
	}
	outputRate := uint32(0)
	if s.stt != nil && s.stt.sampleRate > 0 {
		outputRate = uint32(s.stt.sampleRate)
	}
	normalized, err := s.resamplePendingInputFrame(outputRate, true)
	if err != nil {
		return err
	}
	if normalized != nil && len(normalized.Data) > 0 {
		s.enqueueTransportAudioLocked(normalized.Data)
	}
	return nil
}

func (s *nvidiaSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded {
		return errNvidiaSTTStreamInputEnded
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	if err := s.drainPendingResampleInputLocked(); err != nil {
		return err
	}
	s.flushed = true
	s.transportEOF = true
	s.notifyTransportLocked()
	s.notifyLocked()
	return nil
}

func (s *nvidiaSTTStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errNvidiaSTTStreamInputEnded
	}
	if s.inputEnded {
		return errNvidiaSTTStreamInputEnded
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	if err := s.drainPendingResampleInputLocked(); err != nil {
		return err
	}
	s.flushed = true
	s.inputEnded = true
	s.transportEOF = true
	s.notifyTransportLocked()
	s.notifyLocked()
	return nil
}

func (s *nvidiaSTTStream) Close() error {
	s.mu.Lock()
	s.closed = true
	cancel := s.transportCancel
	done := s.transportDone
	s.notifyLocked()
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

func (s *nvidiaSTTStream) Next() (*stt.SpeechEvent, error) {
	for {
		s.mu.Lock()
		if len(s.events) > 0 {
			event := s.events[0]
			s.events = s.events[1:]
			s.mu.Unlock()
			return &event, nil
		}
		if s.ctx != nil {
			if err := s.ctx.Err(); err != nil {
				s.mu.Unlock()
				return nil, err
			}
		}
		if s.streamErr != nil {
			if !s.streamErrReturned {
				s.streamErrReturned = true
				err := s.streamErr
				s.mu.Unlock()
				return nil, err
			}
		}
		if s.closed || s.transportFinished {
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
	var requestID string
	for _, result := range response.Results {
		if strings.TrimSpace(result.Alternative.Transcript) == "" {
			continue
		}
		if requestID == "" {
			s.requestSeq++
			requestID = fmt.Sprintf("nvidia-%d", s.requestSeq)
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
