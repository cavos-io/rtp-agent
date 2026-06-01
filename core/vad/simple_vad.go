package vad

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/library/telemetry"
	"github.com/cavos-io/conversation-worker/model"
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
}

func NewSimpleVAD(threshold float64) *SimpleVAD {
	if threshold == 0 {
		threshold = 0.05
	}
	return NewSimpleVADWithOptions(SimpleVADOptions{Threshold: threshold})
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
	v.options = mergeSimpleVADOptions(v.options, options)
	streams := make([]*simpleVADStream, 0, len(v.streams))
	for stream := range v.streams {
		streams = append(streams, stream)
	}
	v.mu.Unlock()

	for _, stream := range streams {
		stream.updateOptions(options)
	}
}

func (v *SimpleVAD) Stream(ctx context.Context) (VADStream, error) {
	v.mu.RLock()
	options := v.options
	v.mu.RUnlock()
	stream := &simpleVADStream{
		vad:     v,
		ctx:     ctx,
		options: options,
		events:  make(chan *VADEvent, 10),
	}
	v.mu.Lock()
	v.streams[stream] = struct{}{}
	v.mu.Unlock()
	return stream, nil
}

type simpleVADStream struct {
	vad                        *SimpleVAD
	ctx                        context.Context
	options                    SimpleVADOptions
	events                     chan *VADEvent
	speaking                   bool
	closed                     bool
	inputEnded                 bool
	inputSampleRate            uint32
	samplesIndex               int
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
	lastActivity               time.Time
	metricsInferenceDuration   float64
	metricsInferenceCount      int
	mu                         sync.Mutex
}

func (s *simpleVADStream) updateOptions(options SimpleVADOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options = mergeSimpleVADOptions(s.options, options)
	s.trimSpeechFrames()
	s.trimPrefixFrames()
}

func (s *simpleVADStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("vad stream closed")
	}
	if s.inputEnded {
		return errors.New("vad stream input ended")
	}
	if s.inputSampleRate == 0 {
		s.inputSampleRate = frame.SampleRate
	} else if frame.SampleRate != s.inputSampleRate {
		return nil
	}

	probability := frameRMS(frame)
	duration := frameDuration(frame)
	if s.lastActivity.IsZero() {
		s.lastActivity = time.Now()
	}
	s.samplesIndex += int(frame.SamplesPerChannel)
	s.timestamp += duration

	inferenceStart := time.Now()
	inferenceDuration := time.Since(inferenceStart).Seconds()
	inferenceSpeechDuration := s.speechDuration
	inferenceSilenceDuration := s.silenceDuration
	if s.speaking {
		inferenceSpeechDuration += duration
	} else {
		inferenceSilenceDuration += duration
	}
	s.events <- &VADEvent{
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
	}
	s.collectInferenceMetrics(inferenceDuration)

	if probability >= s.options.Threshold || (s.speaking && probability > s.options.DeactivationThreshold) {
		s.accumulatedSpeechDuration += duration
		s.accumulatedSilenceDuration = 0

		if !s.speaking {
			s.pendingSpeechFrames = append(s.pendingSpeechFrames, frame)
			if s.accumulatedSpeechDuration >= s.options.MinSpeechDuration {
				s.speaking = true
				s.speechDuration = s.accumulatedSpeechDuration
				s.silenceDuration = 0
				startFrames := s.startSpeechFrames()
				s.speechFrames = append([]*model.AudioFrame(nil), startFrames...)
				s.bufferedSpeechDuration = framesDuration(startFrames)
				s.events <- &VADEvent{
					Type:           VADEventStartOfSpeech,
					SamplesIndex:   s.samplesIndex,
					Timestamp:      s.timestamp,
					SpeechDuration: s.speechDuration,
					Frames:         combineFrames(startFrames),
					Speaking:       true,
				}
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
			s.silenceDuration = s.accumulatedSilenceDuration
			s.appendSpeechFrame(frame, duration)
			if s.accumulatedSilenceDuration >= s.options.MinSilenceDuration {
				s.speaking = false
				frames := append([]*model.AudioFrame(nil), s.speechFrames...)
				s.events <- &VADEvent{
					Type:            VADEventEndOfSpeech,
					SamplesIndex:    s.samplesIndex,
					Timestamp:       s.timestamp,
					SpeechDuration:  s.speechDuration,
					SilenceDuration: s.silenceDuration,
					Frames:          combineFrames(frames),
					Speaking:        false,
				}
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

	return nil
}

func (s *simpleVADStream) collectInferenceMetrics(inferenceDuration float64) {
	s.metricsInferenceDuration += inferenceDuration
	s.metricsInferenceCount++
	updateInterval := s.options.UpdateInterval
	if updateInterval <= 0 {
		updateInterval = 1
	}
	if float64(s.metricsInferenceCount) < 1/updateInterval {
		return
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
	s.vad.emitMetrics(metrics)

	s.metricsInferenceDuration = 0
	s.metricsInferenceCount = 0
}

func (s *simpleVADStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("vad stream closed")
	}
	if s.inputEnded {
		return errors.New("vad stream input ended")
	}
	s.resetState()
	return nil
}

func (s *simpleVADStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("vad stream closed")
	}
	if s.inputEnded {
		return errors.New("vad stream input ended")
	}
	s.resetState()
	s.inputEnded = true
	s.closed = true
	close(s.events)
	s.vad.unregisterStream(s)
	return nil
}

func (s *simpleVADStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.events)
	s.vad.unregisterStream(s)
	return nil
}

func (s *simpleVADStream) Next() (*VADEvent, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case ev, ok := <-s.events:
		if !ok {
			return nil, io.EOF
		}
		return ev, nil
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
	s.resetSegment()
	for _, frame := range prefixFrames {
		s.appendPrefixFrame(frame, frameDuration(frame))
	}
}

func (s *simpleVADStream) resetState() {
	s.resetSegment()
	s.samplesIndex = 0
	s.timestamp = 0
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
	bufferLimit := s.maxBufferedDurationLimit()
	if bufferLimit <= 0 {
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
	bufferLimit := s.maxBufferedDurationLimit()
	if bufferLimit > 0 && s.bufferedSpeechDuration+duration > bufferLimit {
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
	bufferLimit := s.maxBufferedDurationLimit()
	if bufferLimit <= 0 {
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

func (s *simpleVADStream) maxBufferedDurationLimit() float64 {
	if s.options.MaxBufferedSpeechDuration <= 0 {
		return 0
	}
	return s.options.MaxBufferedSpeechDuration + s.options.PrefixPaddingDuration
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

func trimFrameEnd(frame *model.AudioFrame, duration float64) *model.AudioFrame {
	if duration <= 0 || frame.SampleRate == 0 || frame.NumChannels == 0 {
		return frame
	}

	samplesToKeep := uint32(duration * float64(frame.SampleRate))
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

func (v *SimpleVAD) emitMetrics(metrics *telemetry.VADMetrics) {
	v.mu.RLock()
	handlers := append([]VADMetricsHandler(nil), v.handlers...)
	v.mu.RUnlock()
	for _, handler := range handlers {
		handler(metrics)
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
	}
	if updates.DeactivationThreshold != 0 {
		current.DeactivationThreshold = updates.DeactivationThreshold
	} else if updates.Threshold != 0 {
		current.DeactivationThreshold = updates.Threshold
	}
	if updates.UpdateInterval != 0 {
		current.UpdateInterval = updates.UpdateInterval
	}
	return current
}
