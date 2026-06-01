package vad

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	lkmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

type SimpleVAD struct {
	options  SimpleVADOptions
	mu       sync.RWMutex
	handlers []VADMetricsHandler
	streams  map[*simpleVADStream]struct{}
}

type SimpleVADOptions struct {
	Threshold                 float64
	MinSpeechDuration         float64
	MinSilenceDuration        float64
	PrefixPaddingDuration     float64
	MaxBufferedSpeechDuration float64
	DeactivationThreshold     float64
	UpdateInterval            float64
	SampleRate                uint32
	WindowDuration            float64
	ProbabilitySmoothingAlpha float64

	maxBufferedSpeechDurationSet bool
	deactivationThresholdSet     bool
}

type SimpleVADOption func(*SimpleVADOptions)

func WithThreshold(threshold float64) SimpleVADOption {
	return func(o *SimpleVADOptions) {
		o.Threshold = threshold
	}
}

func WithMinSpeechDuration(duration float64) SimpleVADOption {
	return func(o *SimpleVADOptions) {
		o.MinSpeechDuration = duration
	}
}

func WithMinSilenceDuration(duration float64) SimpleVADOption {
	return func(o *SimpleVADOptions) {
		o.MinSilenceDuration = duration
	}
}

func WithPrefixPaddingDuration(duration float64) SimpleVADOption {
	return func(o *SimpleVADOptions) {
		o.PrefixPaddingDuration = duration
	}
}

func WithMaxBufferedSpeechDuration(duration float64) SimpleVADOption {
	return func(o *SimpleVADOptions) {
		o.MaxBufferedSpeechDuration = duration
		o.maxBufferedSpeechDurationSet = true
	}
}

func WithDeactivationThreshold(threshold float64) SimpleVADOption {
	return func(o *SimpleVADOptions) {
		o.DeactivationThreshold = threshold
		o.deactivationThresholdSet = true
	}
}

func WithUpdateInterval(interval float64) SimpleVADOption {
	return func(o *SimpleVADOptions) {
		o.UpdateInterval = interval
	}
}

func WithSampleRate(sampleRate uint32) SimpleVADOption {
	return func(o *SimpleVADOptions) {
		o.SampleRate = sampleRate
	}
}

func WithWindowDuration(duration float64) SimpleVADOption {
	return func(o *SimpleVADOptions) {
		o.WindowDuration = duration
	}
}

func WithProbabilitySmoothingAlpha(alpha float64) SimpleVADOption {
	return func(o *SimpleVADOptions) {
		o.ProbabilitySmoothingAlpha = alpha
	}
}

func NewSimpleVAD(threshold float64) *SimpleVAD {
	if threshold == 0 {
		threshold = 0.05
	}
	return NewSimpleVADWithOptions(SimpleVADOptions{Threshold: threshold})
}

func NewSimpleVADWith(opts ...SimpleVADOption) *SimpleVAD {
	options := SimpleVADOptions{
		Threshold:             0.05,
		DeactivationThreshold: 0.05,
		UpdateInterval:        1,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if !options.deactivationThresholdSet {
		options.DeactivationThreshold = options.Threshold
	}
	return newSimpleVADWithResolvedOptions(options)
}

func NewSimpleVADWithOptions(options SimpleVADOptions) *SimpleVAD {
	if options.Threshold == 0 {
		options.Threshold = 0.05
	}
	if options.DeactivationThreshold == 0 {
		options.DeactivationThreshold = options.Threshold
	}
	if options.UpdateInterval == 0 {
		options.UpdateInterval = 1
	}
	return newSimpleVADWithResolvedOptions(options)
}

func newSimpleVADWithResolvedOptions(options SimpleVADOptions) *SimpleVAD {
	return &SimpleVAD{
		options: options,
		streams: make(map[*simpleVADStream]struct{}),
	}
}

func (v *SimpleVAD) Label() string {
	return "vad.SimpleVAD"
}

func (v *SimpleVAD) Model() string {
	return "simple"
}

func (v *SimpleVAD) Provider() string {
	return "builtin"
}

func (v *SimpleVAD) Capabilities() VADCapabilities {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return VADCapabilities{UpdateInterval: v.options.UpdateInterval}
}

func (v *SimpleVAD) OnMetricsCollected(handler VADMetricsHandler) {
	if handler == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.handlers = append(v.handlers, handler)
}

func (v *SimpleVAD) UpdateOptions(options SimpleVADOptions) {
	v.mu.Lock()
	merged := mergeSimpleVADOptions(v.options, options)
	if err := validateSimpleVADOptions(merged); err != nil {
		v.mu.Unlock()
		return
	}
	v.options = merged
	streams := make([]*simpleVADStream, 0, len(v.streams))
	for stream := range v.streams {
		streams = append(streams, stream)
	}
	v.mu.Unlock()

	for _, stream := range streams {
		stream.updateOptions(options)
	}
}

func (v *SimpleVAD) UpdateOptionsWith(opts ...SimpleVADOption) {
	v.mu.Lock()
	options := v.options
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if err := validateSimpleVADOptions(options); err != nil {
		v.mu.Unlock()
		return
	}
	v.options = options
	streams := make([]*simpleVADStream, 0, len(v.streams))
	for stream := range v.streams {
		streams = append(streams, stream)
	}
	v.mu.Unlock()

	for _, stream := range streams {
		stream.setOptions(options)
	}
}

func (v *SimpleVAD) Stream(ctx context.Context) (VADStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v.mu.RLock()
	options := v.options
	v.mu.RUnlock()
	if err := validateSimpleVADOptions(options); err != nil {
		return nil, err
	}
	stream := &simpleVADStream{
		vad:          v,
		ctx:          ctx,
		options:      options,
		done:         make(chan struct{}),
		eventNotify:  make(chan struct{}, 1),
		lastActivity: time.Now(),
	}
	v.mu.Lock()
	v.streams[stream] = struct{}{}
	v.mu.Unlock()
	if ctx.Done() != nil {
		go stream.unregisterOnContextCancel()
	}
	return stream, nil
}

func validateSimpleVADOptions(options SimpleVADOptions) error {
	if math.IsNaN(options.UpdateInterval) || math.IsInf(options.UpdateInterval, 0) || options.UpdateInterval <= 0 {
		return errors.New("update interval must be greater than 0")
	}
	if math.IsNaN(options.WindowDuration) || math.IsInf(options.WindowDuration, 0) || options.WindowDuration < 0 {
		return errors.New("window duration must be greater than or equal to 0")
	}
	if math.IsNaN(options.Threshold) || math.IsInf(options.Threshold, 0) || options.Threshold < 0 {
		return errors.New("threshold must be greater than or equal to 0")
	}
	if math.IsNaN(options.MinSpeechDuration) || math.IsInf(options.MinSpeechDuration, 0) || options.MinSpeechDuration < 0 {
		return errors.New("min speech duration must be greater than or equal to 0")
	}
	if math.IsNaN(options.MinSilenceDuration) || math.IsInf(options.MinSilenceDuration, 0) || options.MinSilenceDuration < 0 {
		return errors.New("min silence duration must be greater than or equal to 0")
	}
	if math.IsNaN(options.PrefixPaddingDuration) || math.IsInf(options.PrefixPaddingDuration, 0) || options.PrefixPaddingDuration < 0 {
		return errors.New("prefix padding duration must be greater than or equal to 0")
	}
	if math.IsNaN(options.MaxBufferedSpeechDuration) || math.IsInf(options.MaxBufferedSpeechDuration, 0) || options.MaxBufferedSpeechDuration < 0 {
		return errors.New("max buffered speech duration must be greater than or equal to 0")
	}
	if math.IsNaN(options.DeactivationThreshold) || math.IsInf(options.DeactivationThreshold, 0) || options.DeactivationThreshold < 0 {
		return errors.New("deactivation threshold must be greater than or equal to 0")
	}
	if math.IsNaN(options.ProbabilitySmoothingAlpha) || math.IsInf(options.ProbabilitySmoothingAlpha, 0) || options.ProbabilitySmoothingAlpha < 0 || options.ProbabilitySmoothingAlpha > 1 {
		return errors.New("probability smoothing alpha must be in [0, 1]")
	}
	return nil
}

type simpleVADStream struct {
	vad                        *SimpleVAD
	ctx                        context.Context
	options                    SimpleVADOptions
	speaking                   bool
	closed                     bool
	inputEnded                 bool
	inputSampleRate            uint32
	inputNumChannels           uint32
	samplesIndex               int
	sampleIndexRemainder       float64
	timestamp                  float64
	speechDuration             float64
	silenceDuration            float64
	accumulatedSpeechDuration  float64
	accumulatedSilenceDuration float64
	prefixFrames               []*model.AudioFrame
	prefixDuration             float64
	pendingSpeechFrames        []*model.AudioFrame
	speechFrames               []*model.AudioFrame
	bufferedSpeechDuration     float64
	windowFrames               []*model.AudioFrame
	windowBufferedSamples      uint32
	windowSampleRemainder      float64
	probabilityFilter          *lkmath.ExpFilter
	lastActivity               time.Time
	metricsInferenceDuration   float64
	metricsInferenceCount      int
	eventQueue                 []*VADEvent
	eventClosed                bool
	eventNotify                chan struct{}
	eventMu                    sync.Mutex
	mu                         sync.Mutex
	done                       chan struct{}
	doneOnce                   sync.Once
}

func (s *simpleVADStream) updateOptions(options SimpleVADOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	merged := mergeSimpleVADOptions(s.options, options)
	if merged.ProbabilitySmoothingAlpha != s.options.ProbabilitySmoothingAlpha {
		s.probabilityFilter = nil
	}
	s.options = merged
	s.trimSpeechFrames()
	s.trimPrefixFrames()
	s.trimPendingSpeechFrames()
}

func (s *simpleVADStream) setOptions(options SimpleVADOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if options.ProbabilitySmoothingAlpha != s.options.ProbabilitySmoothingAlpha {
		s.probabilityFilter = nil
	}
	s.options = options
	s.trimSpeechFrames()
	s.trimPrefixFrames()
	s.trimPendingSpeechFrames()
}

func (s *simpleVADStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	if s.inputEnded {
		s.mu.Unlock()
		return errors.New("vad stream input ended")
	}
	if s.closed {
		s.mu.Unlock()
		return errors.New("vad stream closed")
	}
	if err := s.contextErr(); err != nil {
		s.mu.Unlock()
		return err
	}
	if frame == nil {
		s.mu.Unlock()
		return errors.New("vad frame nil")
	}
	if frame.SampleRate == 0 {
		s.mu.Unlock()
		return errors.New("vad frame sample rate zero")
	}
	if frame.NumChannels == 0 {
		s.mu.Unlock()
		return errors.New("vad frame channel count zero")
	}
	if frame.SamplesPerChannel == 0 {
		s.mu.Unlock()
		return errors.New("vad frame samples per channel zero")
	}
	expectedDataLength := uint64(frame.SamplesPerChannel) * uint64(frame.NumChannels) * 2
	if uint64(len(frame.Data)) != expectedDataLength {
		s.mu.Unlock()
		return errors.New("vad frame data length mismatch")
	}
	if s.inputSampleRate == 0 {
		s.inputSampleRate = frame.SampleRate
		s.inputNumChannels = frame.NumChannels
	} else if frame.SampleRate != s.inputSampleRate {
		s.mu.Unlock()
		return nil
	} else if frame.NumChannels != s.inputNumChannels {
		s.mu.Unlock()
		return nil
	}
	frame = cloneFrame(frame)
	var metrics []*telemetry.VADMetrics

	if s.windowSamples() == 0 {
		metric, err := s.processFrame(frame, frameDuration(frame))
		if err != nil {
			s.mu.Unlock()
			return err
		}
		if metric != nil {
			metrics = append(metrics, metric)
		}
		s.mu.Unlock()
		for _, metric := range metrics {
			s.vad.emitMetrics(metric)
		}
		return nil
	}

	s.windowFrames = append(s.windowFrames, frame)
	s.windowBufferedSamples += frame.SamplesPerChannel
	for windowSamples := s.windowSamples(); windowSamples > 0 && s.windowBufferedSamples >= windowSamples; windowSamples = s.windowSamples() {
		s.advanceWindowSampleRemainder(windowSamples)
		metric, err := s.processFrame(s.takeWindowFrame(windowSamples), s.options.WindowDuration)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		if metric != nil {
			metrics = append(metrics, metric)
		}
	}
	s.mu.Unlock()
	for _, metric := range metrics {
		s.vad.emitMetrics(metric)
	}
	return nil
}

func (s *simpleVADStream) processFrame(frame *model.AudioFrame, duration float64) (*telemetry.VADMetrics, error) {
	probability, err := s.smoothProbability(frameRMS(frame))
	if err != nil {
		return nil, err
	}
	if s.lastActivity.IsZero() {
		s.lastActivity = time.Now()
	}
	s.timestamp += duration
	s.samplesIndex += s.sampleIndexAdvance(duration)

	inferenceStart := time.Now()
	inferenceDuration := time.Since(inferenceStart).Seconds()
	inferenceSpeechDuration := s.speechDuration
	inferenceSilenceDuration := s.silenceDuration
	if s.speaking {
		inferenceSpeechDuration += duration
	} else {
		inferenceSilenceDuration += duration
	}
	s.enqueueEvent(&VADEvent{
		Type:                  VADEventInferenceDone,
		SamplesIndex:          s.samplesIndex,
		Timestamp:             s.timestamp,
		SpeechDuration:        inferenceSpeechDuration,
		SilenceDuration:       inferenceSilenceDuration,
		Frames:                []*model.AudioFrame{frame},
		Probability:           probability,
		InferenceDuration:     inferenceDuration,
		Speaking:              s.speaking,
		RawAccumulatedSpeech:  s.accumulatedSpeechDuration,
		RawAccumulatedSilence: s.accumulatedSilenceDuration,
	})
	metrics := s.collectInferenceMetrics(inferenceDuration)

	if probability >= s.options.Threshold || (s.speaking && probability > s.options.DeactivationThreshold) {
		s.accumulatedSpeechDuration += duration
		s.accumulatedSilenceDuration = 0

		if !s.speaking {
			s.silenceDuration += duration
			s.appendPendingSpeechFrame(frame, duration)
			if s.accumulatedSpeechDuration >= s.options.MinSpeechDuration {
				s.speaking = true
				s.speechDuration = s.accumulatedSpeechDuration
				s.silenceDuration = 0
				startFrames := s.startSpeechFrames()
				s.speechFrames = append([]*model.AudioFrame(nil), startFrames...)
				s.bufferedSpeechDuration = framesDuration(startFrames)
				s.enqueueEvent(&VADEvent{
					Type:           VADEventStartOfSpeech,
					SamplesIndex:   s.samplesIndex,
					Timestamp:      s.timestamp,
					SpeechDuration: s.speechDuration,
					Frames:         combineFrames(startFrames),
					Speaking:       true,
				})
				s.lastActivity = time.Now()
			}
		} else {
			s.speechDuration += duration
			s.silenceDuration = 0
			s.appendSpeechFrame(frame, duration)
		}
	} else {
		s.accumulatedSilenceDuration += duration
		s.accumulatedSpeechDuration = 0

		if s.speaking {
			s.speechDuration += duration
			s.silenceDuration = s.accumulatedSilenceDuration
			s.appendSpeechFrame(frame, duration)
			if s.accumulatedSilenceDuration >= s.options.MinSilenceDuration {
				s.speaking = false
				frames := append([]*model.AudioFrame(nil), s.speechFrames...)
				s.enqueueEvent(&VADEvent{
					Type:            VADEventEndOfSpeech,
					SamplesIndex:    s.samplesIndex,
					Timestamp:       s.timestamp,
					SpeechDuration:  normalizeDuration(max(s.speechDuration-s.silenceDuration, 0)),
					SilenceDuration: s.silenceDuration,
					Frames:          combineFrames(frames),
					Speaking:        false,
				})
				s.lastActivity = time.Now()
				s.resetSegmentWithPrefixTail(frames)
			}
		} else {
			s.silenceDuration += duration
			for _, pending := range s.pendingSpeechFrames {
				s.appendPrefixFrame(pending, frameDuration(pending))
			}
			s.appendPrefixFrame(frame, duration)
			s.pendingSpeechFrames = nil
		}
	}
	return metrics, nil
}

func (s *simpleVADStream) collectInferenceMetrics(inferenceDuration float64) *telemetry.VADMetrics {
	s.metricsInferenceDuration += inferenceDuration
	s.metricsInferenceCount++
	updateInterval := s.options.UpdateInterval
	if updateInterval <= 0 {
		updateInterval = 1
	}
	if float64(s.metricsInferenceCount) < 1/updateInterval {
		return nil
	}

	idleTime := 0.0
	if !s.lastActivity.IsZero() {
		idleTime = time.Since(s.lastActivity).Seconds()
	}
	metrics := &telemetry.VADMetrics{
		Label:                  s.vad.Label(),
		Timestamp:              time.Now(),
		IdleTime:               idleTime,
		InferenceDurationTotal: s.metricsInferenceDuration,
		InferenceCount:         s.metricsInferenceCount,
		Metadata: &telemetry.Metadata{
			ModelName:     s.vad.Model(),
			ModelProvider: s.vad.Provider(),
		},
	}

	s.metricsInferenceDuration = 0
	s.metricsInferenceCount = 0
	return metrics
}

func (s *simpleVADStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return errors.New("vad stream input ended")
	}
	if s.closed {
		return errors.New("vad stream closed")
	}
	if err := s.contextErr(); err != nil {
		return err
	}
	s.resetState()
	return nil
}

func (s *simpleVADStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return errors.New("vad stream input ended")
	}
	if s.closed {
		return errors.New("vad stream closed")
	}
	if err := s.contextErr(); err != nil {
		return err
	}
	s.resetState()
	s.inputEnded = true
	s.closed = true
	s.closeEvents(false)
	s.markDone()
	s.vad.unregisterStream(s)
	return nil
}

func (s *simpleVADStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.closeEvents(true)
		return nil
	}
	s.inputEnded = true
	s.closed = true
	s.closeEvents(true)
	s.markDone()
	s.vad.unregisterStream(s)
	return nil
}

func (s *simpleVADStream) unregisterOnContextCancel() {
	select {
	case <-s.ctx.Done():
		s.vad.unregisterStream(s)
	case <-s.done:
	}
}

func (s *simpleVADStream) markDone() {
	s.doneOnce.Do(func() {
		close(s.done)
	})
}

func (s *simpleVADStream) contextErr() error {
	if s.ctx == nil {
		return nil
	}
	return s.ctx.Err()
}

func (s *simpleVADStream) Next() (*VADEvent, error) {
	for {
		s.eventMu.Lock()
		if len(s.eventQueue) > 0 {
			if !s.eventClosed {
				if err := s.contextErr(); err != nil {
					s.eventMu.Unlock()
					return nil, err
				}
			}
			ev := s.eventQueue[0]
			copy(s.eventQueue, s.eventQueue[1:])
			s.eventQueue[len(s.eventQueue)-1] = nil
			s.eventQueue = s.eventQueue[:len(s.eventQueue)-1]
			s.eventMu.Unlock()
			return ev, nil
		}
		if s.eventClosed {
			s.eventMu.Unlock()
			return nil, io.EOF
		}
		if err := s.contextErr(); err != nil {
			s.eventMu.Unlock()
			return nil, err
		}
		notify := s.eventNotify
		s.eventMu.Unlock()

		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		case <-notify:
		}
	}
}

func (s *simpleVADStream) enqueueEvent(event *VADEvent) {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	if s.eventClosed {
		return
	}
	s.eventQueue = append(s.eventQueue, event)
	s.notifyEventsLocked()
}

func (s *simpleVADStream) closeEvents(dropQueued bool) {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	if s.eventClosed {
		if dropQueued {
			for i := range s.eventQueue {
				s.eventQueue[i] = nil
			}
			s.eventQueue = nil
		}
		return
	}
	if dropQueued {
		for i := range s.eventQueue {
			s.eventQueue[i] = nil
		}
		s.eventQueue = nil
	}
	s.eventClosed = true
	s.notifyEventsLocked()
}

func (s *simpleVADStream) notifyEventsLocked() {
	select {
	case s.eventNotify <- struct{}{}:
	default:
	}
}

func (s *simpleVADStream) resetSegment() {
	s.speaking = false
	s.speechDuration = 0
	s.silenceDuration = 0
	s.accumulatedSpeechDuration = 0
	s.accumulatedSilenceDuration = 0
	s.prefixDuration = 0
	s.prefixFrames = nil
	s.pendingSpeechFrames = nil
	s.speechFrames = nil
	s.bufferedSpeechDuration = 0
}

func (s *simpleVADStream) resetSegmentWithPrefixTail(frames []*model.AudioFrame) {
	prefixFrames := s.prefixTailFrames(frames)
	silenceDuration := s.silenceDuration
	accumulatedSilenceDuration := s.accumulatedSilenceDuration
	s.resetSegment()
	s.silenceDuration = silenceDuration
	s.accumulatedSilenceDuration = accumulatedSilenceDuration
	for _, frame := range prefixFrames {
		s.appendPrefixFrame(frame, frameDuration(frame))
	}
}

func (s *simpleVADStream) resetState() {
	s.resetSegment()
	s.windowFrames = nil
	s.windowBufferedSamples = 0
	s.windowSampleRemainder = 0
	s.samplesIndex = 0
	s.sampleIndexRemainder = 0
	s.timestamp = 0
	s.probabilityFilter = nil
}

func (s *simpleVADStream) smoothProbability(probability float64) (float64, error) {
	if s.options.ProbabilitySmoothingAlpha <= 0 {
		s.probabilityFilter = nil
		return probability, nil
	}
	if s.probabilityFilter == nil {
		filter, err := lkmath.NewExpFilterWithOptions(s.options.ProbabilitySmoothingAlpha, lkmath.ExpFilterOptions{})
		if err != nil {
			return 0, err
		}
		s.probabilityFilter = filter
	}
	return s.probabilityFilter.Apply(1, probability), nil
}

func (s *simpleVADStream) windowSamples() uint32 {
	if s.options.WindowDuration <= 0 || s.inputSampleRate == 0 {
		return 0
	}
	samples := uint32(s.options.WindowDuration*float64(s.inputSampleRate) + s.windowSampleRemainder)
	if samples == 0 {
		return 0
	}
	return samples
}

func (s *simpleVADStream) advanceWindowSampleRemainder(samples uint32) {
	exactSamples := s.options.WindowDuration*float64(s.inputSampleRate) + s.windowSampleRemainder
	s.windowSampleRemainder = exactSamples - float64(samples)
}

func (s *simpleVADStream) takeWindowFrame(samples uint32) *model.AudioFrame {
	var taken []*model.AudioFrame
	remaining := samples
	for remaining > 0 && len(s.windowFrames) > 0 {
		frame := s.windowFrames[0]
		if frame.SamplesPerChannel <= remaining {
			taken = append(taken, frame)
			remaining -= frame.SamplesPerChannel
			s.windowFrames = s.windowFrames[1:]
			continue
		}

		taken = append(taken, trimFrameEndSamples(frame, remaining))
		s.windowFrames[0] = trimFrameStartSamples(frame, remaining)
		remaining = 0
	}
	s.windowBufferedSamples -= samples - remaining
	frames := combineFrames(taken)
	if len(frames) == 0 {
		return &model.AudioFrame{SampleRate: s.inputSampleRate, NumChannels: 1}
	}
	return frames[0]
}

func (s *simpleVADStream) sampleIndexAdvance(duration float64) int {
	sampleRate := s.options.SampleRate
	if sampleRate == 0 {
		sampleRate = s.inputSampleRate
	}
	advance := duration*float64(sampleRate) + s.sampleIndexRemainder
	whole := int(advance)
	s.sampleIndexRemainder = advance - float64(whole)
	return whole
}

func (s *simpleVADStream) prefixTailFrames(frames []*model.AudioFrame) []*model.AudioFrame {
	if s.options.PrefixPaddingDuration <= 0 || len(frames) == 0 {
		return nil
	}

	var duration float64
	start := len(frames)
	for start > 0 {
		currentFrameDuration := frameDuration(frames[start-1])
		if duration+currentFrameDuration > s.options.PrefixPaddingDuration {
			break
		}
		duration += currentFrameDuration
		start--
	}

	tail := append([]*model.AudioFrame(nil), frames[start:]...)
	remaining := s.options.PrefixPaddingDuration - duration
	if remaining > 0 && start > 0 {
		frame := frames[start-1]
		trimmed := trimFrameStart(frame, frameDuration(frame)-remaining)
		tail = append([]*model.AudioFrame{trimmed}, tail...)
	}
	return tail
}

func (s *simpleVADStream) startSpeechFrames() []*model.AudioFrame {
	frames := make([]*model.AudioFrame, 0, len(s.prefixFrames)+len(s.pendingSpeechFrames))
	frames = append(frames, s.prefixFrames...)
	frames = append(frames, s.pendingSpeechFrames...)
	bufferLimit, limitSet := s.maxBufferedDurationLimit()
	if !limitSet {
		return frames
	}

	var buffered float64
	limited := frames[:0]
	for _, frame := range frames {
		duration := frameDuration(frame)
		if buffered+duration > bufferLimit {
			remaining := bufferLimit - buffered
			if remaining > 0 {
				limited = append(limited, trimFrameEnd(frame, remaining))
			}
			break
		}
		limited = append(limited, frame)
		buffered += duration
	}
	return append([]*model.AudioFrame(nil), limited...)
}

func (s *simpleVADStream) appendSpeechFrame(frame *model.AudioFrame, duration float64) {
	bufferLimit, limitSet := s.maxBufferedDurationLimit()
	if limitSet && s.bufferedSpeechDuration+duration > bufferLimit {
		remaining := bufferLimit - s.bufferedSpeechDuration
		if remaining > 0 {
			partial := trimFrameEnd(frame, remaining)
			s.speechFrames = append(s.speechFrames, partial)
			s.bufferedSpeechDuration += frameDuration(partial)
		}
		return
	}
	s.speechFrames = append(s.speechFrames, frame)
	s.bufferedSpeechDuration += duration
}

func (s *simpleVADStream) trimSpeechFrames() {
	bufferLimit, limitSet := s.maxBufferedDurationLimit()
	if !limitSet {
		s.bufferedSpeechDuration = framesDuration(s.speechFrames)
		return
	}

	var buffered float64
	limited := s.speechFrames[:0]
	for _, frame := range s.speechFrames {
		duration := frameDuration(frame)
		if buffered+duration > bufferLimit {
			remaining := bufferLimit - buffered
			if remaining > 0 {
				partial := trimFrameEnd(frame, remaining)
				limited = append(limited, partial)
				buffered += frameDuration(partial)
			}
			break
		}
		limited = append(limited, frame)
		buffered += duration
	}
	s.speechFrames = limited
	s.bufferedSpeechDuration = buffered
}

func (s *simpleVADStream) appendPendingSpeechFrame(frame *model.AudioFrame, duration float64) {
	bufferLimit, limitSet := s.maxBufferedDurationLimit()
	if limitSet {
		buffered := s.prefixDuration + framesDuration(s.pendingSpeechFrames)
		if buffered+duration > bufferLimit {
			remaining := bufferLimit - buffered
			if remaining > 0 {
				s.pendingSpeechFrames = append(s.pendingSpeechFrames, trimFrameEnd(frame, remaining))
			}
			return
		}
	}
	s.pendingSpeechFrames = append(s.pendingSpeechFrames, frame)
}

func (s *simpleVADStream) trimPendingSpeechFrames() {
	bufferLimit, limitSet := s.maxBufferedDurationLimit()
	if !limitSet {
		return
	}

	remaining := bufferLimit - s.prefixDuration
	if remaining <= 0 {
		s.pendingSpeechFrames = nil
		return
	}

	var buffered float64
	limited := s.pendingSpeechFrames[:0]
	for _, frame := range s.pendingSpeechFrames {
		duration := frameDuration(frame)
		if buffered+duration > remaining {
			partialDuration := remaining - buffered
			if partialDuration > 0 {
				limited = append(limited, trimFrameEnd(frame, partialDuration))
			}
			break
		}
		limited = append(limited, frame)
		buffered += duration
	}
	s.pendingSpeechFrames = limited
}

func (s *simpleVADStream) maxBufferedDurationLimit() (float64, bool) {
	if s.options.MaxBufferedSpeechDuration <= 0 && !s.options.maxBufferedSpeechDurationSet {
		return 0, false
	}
	return max(s.options.MaxBufferedSpeechDuration+s.options.PrefixPaddingDuration, 0), true
}

func (s *simpleVADStream) appendPrefixFrame(frame *model.AudioFrame, duration float64) {
	if s.options.PrefixPaddingDuration <= 0 {
		return
	}
	s.prefixFrames = append(s.prefixFrames, frame)
	s.prefixDuration += duration
	s.trimPrefixFrames()
}

func (s *simpleVADStream) trimPrefixFrames() {
	for s.prefixDuration > s.options.PrefixPaddingDuration && len(s.prefixFrames) > 0 {
		excess := s.prefixDuration - s.options.PrefixPaddingDuration
		first := s.prefixFrames[0]
		firstDuration := frameDuration(first)
		if excess < firstDuration {
			trimmed := trimFrameStart(first, excess)
			s.prefixDuration -= firstDuration - frameDuration(trimmed)
			s.prefixFrames[0] = trimmed
			return
		}
		s.prefixDuration -= firstDuration
		s.prefixFrames = s.prefixFrames[1:]
	}
}

func trimFrameStart(frame *model.AudioFrame, duration float64) *model.AudioFrame {
	if duration <= 0 || frame.SampleRate == 0 || frame.NumChannels == 0 {
		return frame
	}

	samplesToTrim := uint32(duration * float64(frame.SampleRate))
	return trimFrameStartSamples(frame, samplesToTrim)
}

func trimFrameEnd(frame *model.AudioFrame, duration float64) *model.AudioFrame {
	if duration <= 0 || frame.SampleRate == 0 || frame.NumChannels == 0 {
		return frame
	}

	samplesToKeep := uint32(duration * float64(frame.SampleRate))
	return trimFrameEndSamples(frame, samplesToKeep)
}

func trimFrameStartSamples(frame *model.AudioFrame, samplesToTrim uint32) *model.AudioFrame {
	if samplesToTrim == 0 {
		return frame
	}
	if samplesToTrim >= frame.SamplesPerChannel {
		return &model.AudioFrame{
			SampleRate:  frame.SampleRate,
			NumChannels: frame.NumChannels,
		}
	}

	bytesPerSample := len(frame.Data) / int(frame.SamplesPerChannel) / int(frame.NumChannels)
	if bytesPerSample <= 0 {
		return frame
	}
	byteOffset := int(samplesToTrim) * int(frame.NumChannels) * bytesPerSample
	data := append([]byte(nil), frame.Data[byteOffset:]...)
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        frame.SampleRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: frame.SamplesPerChannel - samplesToTrim,
	}
}

func trimFrameEndSamples(frame *model.AudioFrame, samplesToKeep uint32) *model.AudioFrame {
	if samplesToKeep >= frame.SamplesPerChannel {
		return frame
	}
	if samplesToKeep == 0 {
		return &model.AudioFrame{
			SampleRate:  frame.SampleRate,
			NumChannels: frame.NumChannels,
		}
	}

	bytesPerSample := len(frame.Data) / int(frame.SamplesPerChannel) / int(frame.NumChannels)
	if bytesPerSample <= 0 {
		return frame
	}
	byteCount := int(samplesToKeep) * int(frame.NumChannels) * bytesPerSample
	data := append([]byte(nil), frame.Data[:byteCount]...)
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        frame.SampleRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: samplesToKeep,
	}
}

func frameRMS(frame *model.AudioFrame) float64 {
	sampleCount := len(frame.Data) / 2
	if sampleCount == 0 {
		return 0
	}

	var sum float64
	for i := 0; i < len(frame.Data); i += 2 {
		if i+1 >= len(frame.Data) {
			break
		}
		val := int16(uint16(frame.Data[i]) | uint16(frame.Data[i+1])<<8)
		fval := float64(val) / 32768.0
		sum += fval * fval
	}
	return math.Sqrt(sum / float64(sampleCount))
}

func frameDuration(frame *model.AudioFrame) float64 {
	if frame.SampleRate == 0 {
		return 0
	}
	return float64(frame.SamplesPerChannel) / float64(frame.SampleRate)
}

func framesDuration(frames []*model.AudioFrame) float64 {
	var duration float64
	for _, frame := range frames {
		duration += frameDuration(frame)
	}
	return duration
}

func normalizeDuration(duration float64) float64 {
	return math.Round(duration*1e12) / 1e12
}

func combineFrames(frames []*model.AudioFrame) []*model.AudioFrame {
	if len(frames) == 0 {
		return nil
	}
	if len(frames) == 1 {
		frame := frames[0]
		data := append([]byte(nil), frame.Data...)
		return []*model.AudioFrame{{
			Data:              data,
			SampleRate:        frame.SampleRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: frame.SamplesPerChannel,
		}}
	}

	first := frames[0]
	var samples uint32
	var data []byte
	for _, frame := range frames {
		samples += frame.SamplesPerChannel
		data = append(data, frame.Data...)
	}
	return []*model.AudioFrame{{
		Data:              data,
		SampleRate:        first.SampleRate,
		NumChannels:       first.NumChannels,
		SamplesPerChannel: samples,
	}}
}

func cloneFrame(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil {
		return nil
	}
	data := append([]byte(nil), frame.Data...)
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        frame.SampleRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: frame.SamplesPerChannel,
	}
}

func (v *SimpleVAD) emitMetrics(metrics *telemetry.VADMetrics) {
	v.mu.RLock()
	handlers := append([]VADMetricsHandler(nil), v.handlers...)
	v.mu.RUnlock()
	for _, handler := range handlers {
		go func(handler VADMetricsHandler) {
			defer func() {
				_ = recover()
			}()
			handler(metrics)
		}(handler)
	}
}

func (v *SimpleVAD) unregisterStream(stream *simpleVADStream) {
	v.mu.Lock()
	delete(v.streams, stream)
	v.mu.Unlock()
}

func mergeSimpleVADOptions(current, updates SimpleVADOptions) SimpleVADOptions {
	if updates.Threshold != 0 {
		current.Threshold = updates.Threshold
	}
	if updates.MinSpeechDuration != 0 {
		current.MinSpeechDuration = updates.MinSpeechDuration
	}
	if updates.MinSilenceDuration != 0 {
		current.MinSilenceDuration = updates.MinSilenceDuration
	}
	if updates.PrefixPaddingDuration != 0 {
		current.PrefixPaddingDuration = updates.PrefixPaddingDuration
	}
	if updates.MaxBufferedSpeechDuration != 0 {
		current.MaxBufferedSpeechDuration = updates.MaxBufferedSpeechDuration
		current.maxBufferedSpeechDurationSet = true
	}
	if updates.DeactivationThreshold != 0 {
		current.DeactivationThreshold = updates.DeactivationThreshold
	}
	if updates.UpdateInterval != 0 {
		current.UpdateInterval = updates.UpdateInterval
	}
	if updates.SampleRate != 0 {
		current.SampleRate = updates.SampleRate
	}
	if updates.WindowDuration != 0 {
		current.WindowDuration = updates.WindowDuration
	}
	if updates.ProbabilitySmoothingAlpha != 0 {
		current.ProbabilitySmoothingAlpha = updates.ProbabilitySmoothingAlpha
	}
	return current
}
