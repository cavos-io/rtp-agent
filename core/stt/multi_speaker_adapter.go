package stt

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
)

type PrimarySpeakerDetectionOptions struct {
	FrameSizeMs            int
	RMSBufferDuration      float64
	MinRMSSamples          int
	RMSSmoothingFactor     float64
	ThresholdMultiplier    float64
	DecayToEqualTime       float64
	ThresholdMinMultiplier float64
}

func DefaultPrimarySpeakerDetectionOptions() PrimarySpeakerDetectionOptions {
	return PrimarySpeakerDetectionOptions{
		FrameSizeMs:            100,
		RMSBufferDuration:      120.0,
		MinRMSSamples:          3,
		RMSSmoothingFactor:     0.5,
		ThresholdMultiplier:    1.3,
		DecayToEqualTime:       60,
		ThresholdMinMultiplier: 0.5,
	}
}

type MultiSpeakerAdapter struct {
	stt                       STT
	detectPrimarySpeaker      bool
	suppressBackgroundSpeaker bool
	primaryFormat             string
	backgroundFormat          string
	opt                       PrimarySpeakerDetectionOptions
}

func NewMultiSpeakerAdapter(stt STT, detectPrimary bool, suppressBackground bool, primaryFormat string, backgroundFormat string, opt *PrimarySpeakerDetectionOptions) (*MultiSpeakerAdapter, error) {
	if !stt.Capabilities().Diarization {
		return nil, fmt.Errorf("MultiSpeakerAdapter needs STT with diarization capability")
	}

	if primaryFormat == "" {
		primaryFormat = "{text}"
	}
	if backgroundFormat == "" {
		backgroundFormat = "{text}"
	}
	if suppressBackground && !detectPrimary {
		logger.Logger.Warnw("Suppressing background speaker is not supported when detectPrimarySpeaker is False", nil)
		suppressBackground = false
	}

	options := DefaultPrimarySpeakerDetectionOptions()
	if opt != nil {
		options = *opt
	}

	return &MultiSpeakerAdapter{
		stt:                       stt,
		detectPrimarySpeaker:      detectPrimary,
		suppressBackgroundSpeaker: suppressBackground,
		primaryFormat:             primaryFormat,
		backgroundFormat:          backgroundFormat,
		opt:                       options,
	}, nil
}

func (a *MultiSpeakerAdapter) Label() string {
	return "MultiSpeakerAdapter(" + a.stt.Label() + ")"
}

func (a *MultiSpeakerAdapter) Capabilities() STTCapabilities {
	return a.stt.Capabilities()
}

func (a *MultiSpeakerAdapter) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
	return a.stt.Recognize(ctx, frames, language)
}

func (a *MultiSpeakerAdapter) Stream(ctx context.Context, language string) (RecognizeStream, error) {
	innerStream, err := a.stt.Stream(ctx, language)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	w := &multiSpeakerAdapterWrapper{
		adapter:  a,
		inner:    innerStream,
		ctx:      ctx,
		cancel:   cancel,
		detector: newPrimarySpeakerDetector(a.detectPrimarySpeaker, a.suppressBackgroundSpeaker, a.primaryFormat, a.backgroundFormat, a.opt),
		eventCh:  make(chan *SpeechEvent, 100),
		audioCh:  make(chan *model.AudioFrame, 100),
	}

	go w.run()

	return w, nil
}

type multiSpeakerAdapterWrapper struct {
	adapter  *MultiSpeakerAdapter
	inner    RecognizeStream
	ctx      context.Context
	cancel   context.CancelFunc
	detector *primarySpeakerDetector
	eventCh  chan *SpeechEvent
	audioCh  chan *model.AudioFrame
	mu       sync.Mutex
	closed   bool
}

func (w *multiSpeakerAdapterWrapper) PushFrame(frame *model.AudioFrame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("stream closed")
	}
	w.audioCh <- frame
	return nil
}

func (w *multiSpeakerAdapterWrapper) Flush() error {
	return w.inner.Flush()
}

func (w *multiSpeakerAdapterWrapper) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	w.cancel()
	close(w.audioCh)
	return w.inner.Close()
}

func (w *multiSpeakerAdapterWrapper) Next() (*SpeechEvent, error) {
	ev, ok := <-w.eventCh
	if !ok {
		return nil, context.Canceled
	}
	return ev, nil
}

func (w *multiSpeakerAdapterWrapper) run() {
	defer close(w.eventCh)

	go func() {
		for {
			select {
			case <-w.ctx.Done():
				return
			case frame, ok := <-w.audioCh:
				if !ok {
					return
				}
				w.inner.PushFrame(frame)
				w.detector.pushAudio(frame)
			}
		}
	}()

	for {
		ev, err := w.inner.Next()
		if err != nil {
			return
		}

		updatedEv := w.detector.onSttEvent(ev)
		if updatedEv != nil {
			w.eventCh <- updatedEv
		} else if ev.Type == SpeechEventFinalTranscript {
			w.eventCh <- &SpeechEvent{
				Type:         SpeechEventFinalTranscript,
				Alternatives: []SpeechData{{Language: "", Text: ""}},
			}
		}
	}
}

type speakerData struct {
	speakerID        string
	lastActivityTime float64
	rms              float64
}

type primarySpeakerDetector struct {
	detectPrimary      bool
	suppressBackground bool
	primaryFormat      string
	backgroundFormat   string
	opt                PrimarySpeakerDetectionOptions

	pushedDuration float64
	primarySpeaker string
	speakerDataMap map[string]*speakerData
	bstream        *audio.AudioByteStream
	rmsBuffer      []float64
	frameSize      float64
	maxRmsSize     int
}

func newPrimarySpeakerDetector(detectPrimary bool, suppressBackground bool, primaryFormat string, backgroundFormat string, opt PrimarySpeakerDetectionOptions) *primarySpeakerDetector {
	return &primarySpeakerDetector{
		detectPrimary:      detectPrimary,
		suppressBackground: suppressBackground,
		primaryFormat:      primaryFormat,
		backgroundFormat:   backgroundFormat,
		opt:                opt,
		speakerDataMap:     make(map[string]*speakerData),
		rmsBuffer:          make([]float64, 0),
		frameSize:          float64(opt.FrameSizeMs) / 1000.0,
		maxRmsSize:         int(opt.RMSBufferDuration / (float64(opt.FrameSizeMs) / 1000.0)),
	}
}

func (d *primarySpeakerDetector) pushAudio(frame *model.AudioFrame) {
	duration := float64(frame.SamplesPerChannel) / float64(frame.SampleRate)
	if !d.detectPrimary {
		d.pushedDuration += duration
		return
	}

	if d.bstream == nil {
		samplePerChannel := uint32(float64(frame.SampleRate) * d.frameSize)
		d.bstream = &audio.AudioByteStream{
			SampleRate:        frame.SampleRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: samplePerChannel,
		}
		d.frameSize = float64(samplePerChannel) / float64(frame.SampleRate)
	}

	frames := d.bstream.Push(frame.Data)
	for _, f := range frames {
		rms := d.computeRms(f)
		d.rmsBuffer = append(d.rmsBuffer, rms)
		fduration := float64(f.SamplesPerChannel) / float64(f.SampleRate)
		d.pushedDuration += fduration
	}

	if len(d.rmsBuffer) > d.maxRmsSize {
		d.rmsBuffer = d.rmsBuffer[len(d.rmsBuffer)-d.maxRmsSize:]
	}
}

func (d *primarySpeakerDetector) computeRms(frame *model.AudioFrame) float64 {
	if len(frame.Data) == 0 {
		return 0.0
	}

	// Assuming 16-bit PCM
	count := len(frame.Data) / 2
	var sum float64 = 0
	for i := 0; i < count; i++ {
		sample := int16(frame.Data[i*2]) | int16(frame.Data[i*2+1])<<8
		sum += float64(sample) * float64(sample)
	}
	mean := sum / float64(count)
	return math.Sqrt(mean)
}

func (d *primarySpeakerDetector) getRmsForTimerange(startTime float64, endTime float64) float64 {
	if len(d.rmsBuffer) == 0 {
		return 0.0
	}

	start := int((d.pushedDuration - startTime) / d.frameSize)
	end := int((d.pushedDuration - endTime) / d.frameSize)
	startIdx := len(d.rmsBuffer) - start - 1
	endIdx := len(d.rmsBuffer) - end

	if endIdx < 0 || startIdx >= len(d.rmsBuffer) {
		return 0.0
	}
	if startIdx < 0 {
		startIdx = 0
	}

	if endIdx-startIdx < d.opt.MinRMSSamples {
		return 0.0
	}

	slice := make([]float64, endIdx-startIdx)
	copy(slice, d.rmsBuffer[startIdx:endIdx])

	// Simple median calculation
	for i := 0; i < len(slice)-1; i++ {
		for j := i + 1; j < len(slice); j++ {
			if slice[i] > slice[j] {
				slice[i], slice[j] = slice[j], slice[i]
			}
		}
	}
	var median float64
	n := len(slice)
	if n%2 == 0 {
		median = (slice[n/2-1] + slice[n/2]) / 2.0
	} else {
		median = slice[n/2]
	}

	return median
}

func (d *primarySpeakerDetector) updatePrimarySpeaker(sd *SpeechData) {
	if sd.SpeakerID == "" || !d.detectPrimary {
		d.primarySpeaker = ""
		return
	}

	rms := d.getRmsForTimerange(sd.StartTime, sd.EndTime)
	if rms == 0.0 {
		return
	}

	speakerID := sd.SpeakerID
	data, ok := d.speakerDataMap[speakerID]
	if ok {
		data.lastActivityTime = sd.EndTime
		data.rms = data.rms*d.opt.RMSSmoothingFactor + rms*(1-d.opt.RMSSmoothingFactor)
	} else {
		data = &speakerData{
			speakerID:        speakerID,
			lastActivityTime: sd.EndTime,
			rms:              rms,
		}
		d.speakerDataMap[speakerID] = data
	}

	if d.primarySpeaker == speakerID {
		return
	}

	primary, hasPrimary := d.speakerDataMap[d.primarySpeaker]
	if d.primarySpeaker == "" || !hasPrimary {
		d.primarySpeaker = speakerID
		logger.Logger.Debugw("set first primary speaker", "speaker_id", speakerID, "rms", rms)
		return
	}

	silenceDuration := d.pushedDuration - primary.lastActivityTime

	decayRate := 0.0
	if d.opt.ThresholdMultiplier > 1.0 {
		decayRate = (d.opt.ThresholdMultiplier - 1.0) / d.opt.DecayToEqualTime
	}

	multiplier := d.opt.ThresholdMultiplier - (decayRate * silenceDuration)
	if multiplier < d.opt.ThresholdMinMultiplier {
		multiplier = d.opt.ThresholdMinMultiplier
	}

	rmsThreshold := primary.rms * multiplier
	extra := []interface{}{
		"speaker_id", speakerID,
		"rms", rms,
		"rms_threshold", rmsThreshold,
		"silence_duration", silenceDuration,
		"multiplier", multiplier,
	}

	if rms > rmsThreshold {
		d.primarySpeaker = speakerID
		logger.Logger.Debugw("primary speaker switched", extra...)
	} else {
		logger.Logger.Debugw("primary speaker unchanged", extra...)
	}
}

func (d *primarySpeakerDetector) onSttEvent(ev *SpeechEvent) *SpeechEvent {
	if len(ev.Alternatives) == 0 {
		return ev
	}

	sd := &ev.Alternatives[0]
	if ev.Type == SpeechEventFinalTranscript {
		d.updatePrimarySpeaker(sd)
	}

	if sd.SpeakerID == "" || d.primarySpeaker == "" {
		return ev
	}

	isPrimary := sd.SpeakerID == d.primarySpeaker
	sd.IsPrimarySpeaker = &isPrimary

	if isPrimary {
		sd.Text = d.primaryFormat // simplification, no easy dynamic format template built-in
	} else {
		if d.suppressBackground {
			return nil
		}
		sd.Text = d.backgroundFormat
	}
	return ev
}
