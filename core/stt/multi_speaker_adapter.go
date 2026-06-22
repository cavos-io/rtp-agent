package stt

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/logger"
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

func NewDefaultMultiSpeakerAdapter(stt STT) (*MultiSpeakerAdapter, error) {
	return NewMultiSpeakerAdapter(stt, true, false, "{text}", "{text}", nil)
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
	return "stt.MultiSpeakerAdapter"
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
		adapter:   a,
		inner:     innerStream,
		ctx:       ctx,
		cancel:    cancel,
		detector:  newPrimarySpeakerDetector(a.detectPrimarySpeaker, a.suppressBackgroundSpeaker, a.primaryFormat, a.backgroundFormat, a.opt),
		eventCh:   make(chan *SpeechEvent, 100),
		errCh:     make(chan error, 1),
		inputCh:   make(chan multiSpeakerInput, 100),
		doneCh:    make(chan struct{}),
		startTime: streamStartTimeNow(),
	}
	w.applyTiming()

	go w.run()

	return w, nil
}

type multiSpeakerAdapterWrapper struct {
	adapter     *MultiSpeakerAdapter
	inner       RecognizeStream
	ctx         context.Context
	cancel      context.CancelFunc
	detector    *primarySpeakerDetector
	eventCh     chan *SpeechEvent
	errCh       chan error
	inputCh     chan multiSpeakerInput
	doneCh      chan struct{}
	mu          sync.Mutex
	closed      bool
	doneClosed  bool
	terminalErr error
	rateGuard   SampleRateGuard
	inputEnded  bool
	startOffset float64
	startTime   float64
}

type multiSpeakerInput struct {
	frame *model.AudioFrame
	flush bool
	end   bool
}

func (w *multiSpeakerAdapterWrapper) StartTimeOffset() float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.startOffset
}

func (w *multiSpeakerAdapterWrapper) SetStartTimeOffset(offset float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.startOffset = nonNegativeStreamTime(offset)
	w.applyTiming()
}

func (w *multiSpeakerAdapterWrapper) StartTime() float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.startTime
}

func (w *multiSpeakerAdapterWrapper) SetStartTime(startTime float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.startTime = nonNegativeStreamTime(startTime)
	w.applyTiming()
}

func (w *multiSpeakerAdapterWrapper) applyTiming() {
	timing, ok := w.inner.(StreamTiming)
	if !ok {
		return
	}
	SetStreamStartTimeOffset(timing, w.startOffset)
	SetStreamStartTime(timing, w.startTime)
}

func (w *multiSpeakerAdapterWrapper) PushFrame(frame *model.AudioFrame) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return fmt.Errorf("stream closed")
	}
	if w.inputEnded {
		w.mu.Unlock()
		return fmt.Errorf("stream input ended")
	}
	if err := w.rateGuard.Check(frame); err != nil {
		w.mu.Unlock()
		return err
	}
	w.ensureDoneChLocked()
	w.mu.Unlock()
	return w.enqueueInput(multiSpeakerInput{frame: frame})
}

func (w *multiSpeakerAdapterWrapper) Flush() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return fmt.Errorf("stream closed")
	}
	if w.inputEnded {
		w.mu.Unlock()
		return fmt.Errorf("stream input ended")
	}
	w.ensureDoneChLocked()
	w.mu.Unlock()
	return w.enqueueInput(multiSpeakerInput{flush: true})
}

func (w *multiSpeakerAdapterWrapper) EndInput() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return fmt.Errorf("stream closed")
	}
	if w.inputEnded {
		w.mu.Unlock()
		return fmt.Errorf("stream input ended")
	}
	w.inputEnded = true
	w.ensureDoneChLocked()
	w.mu.Unlock()
	return w.enqueueInput(multiSpeakerInput{end: true})
}

func (w *multiSpeakerAdapterWrapper) enqueueInput(input multiSpeakerInput) error {
	w.mu.Lock()
	doneCh := w.ensureDoneChLocked()
	w.mu.Unlock()

	select {
	case <-doneCh:
		return fmt.Errorf("stream closed")
	default:
	}

	select {
	case <-doneCh:
		return fmt.Errorf("stream closed")
	case <-w.ctx.Done():
		return fmt.Errorf("stream closed")
	case w.inputCh <- input:
		return nil
	}
}

func (w *multiSpeakerAdapterWrapper) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	w.closeInputLocked()
	w.cancel()
	return w.inner.Close()
}

func (w *multiSpeakerAdapterWrapper) Next() (*SpeechEvent, error) {
	select {
	case ev, ok := <-w.eventCh:
		if ok {
			return ev, nil
		}
		select {
		case err := <-w.errCh:
			if err != nil {
				return nil, err
			}
		default:
		}
		w.mu.Lock()
		err := w.terminalErr
		w.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, context.Canceled
	case err := <-w.errCh:
		if err != nil {
			return nil, err
		}
		return nil, context.Canceled
	case <-w.ctx.Done():
		w.mu.Lock()
		err := w.terminalErr
		w.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, w.ctx.Err()
	}
}

func (w *multiSpeakerAdapterWrapper) sendErr(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	w.terminalErr = err
	w.mu.Unlock()
	select {
	case w.errCh <- err:
	default:
	}
	_ = w.inner.Close()
}

func (w *multiSpeakerAdapterWrapper) sendEvent(ev *SpeechEvent) {
	select {
	case <-w.ctx.Done():
	case w.eventCh <- ev:
	}
}

func (w *multiSpeakerAdapterWrapper) run() {
	defer close(w.eventCh)
	defer w.markClosedFromRun()
	defer w.inner.Close()

	go func() {
		for {
			select {
			case <-w.ctx.Done():
				return
			case input, ok := <-w.inputCh:
				if !ok {
					return
				}
				if input.flush {
					if err := w.inner.Flush(); err != nil {
						w.sendErr(err)
					}
					continue
				}
				if input.end {
					if ending, ok := w.inner.(InputEnding); ok {
						if err := ending.EndInput(); err != nil && !isReferenceEndInputRuntimeError(err) {
							w.sendErr(err)
						}
					}
					return
				}
				if err := w.inner.PushFrame(input.frame); err != nil {
					w.sendErr(err)
					return
				}
				w.detector.pushAudio(input.frame)
			}
		}
	}()

	for {
		ev, err := w.inner.Next()
		if err != nil {
			w.sendErr(err)
			return
		}

		updatedEv := w.detector.onSttEvent(ev)
		if updatedEv != nil {
			w.sendEvent(updatedEv)
		} else if ev.Type == SpeechEventFinalTranscript {
			w.sendEvent(&SpeechEvent{
				Type:         SpeechEventFinalTranscript,
				Alternatives: []SpeechData{{Language: "", Text: ""}},
			})
		}
	}
}

func isReferenceEndInputRuntimeError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "input ended") || strings.Contains(msg, "stream closed")
}

func (w *multiSpeakerAdapterWrapper) markClosedFromRun() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		w.closeInputLocked()
	}
	w.mu.Unlock()
	if w.cancel != nil {
		w.cancel()
	}
}

func (w *multiSpeakerAdapterWrapper) closeInputLocked() {
	doneCh := w.ensureDoneChLocked()
	if !w.doneClosed {
		close(doneCh)
		w.doneClosed = true
	}
}

func (w *multiSpeakerAdapterWrapper) ensureDoneChLocked() chan struct{} {
	if w.doneCh == nil {
		w.doneCh = make(chan struct{})
	}
	return w.doneCh
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
	duration := audio.CalculateFrameDuration(frame)
	if !d.detectPrimary {
		d.pushedDuration += duration
		return
	}

	if d.bstream == nil {
		samplePerChannel := uint32(float64(frame.SampleRate) * d.frameSize)
		d.bstream = audio.NewAudioByteStream(frame.SampleRate, frame.NumChannels, samplePerChannel)
		d.frameSize = float64(samplePerChannel) / float64(frame.SampleRate)
	}

	frames := d.bstream.Push(frame.Data)
	for _, f := range frames {
		rms := d.computeRms(f)
		d.rmsBuffer = append(d.rmsBuffer, rms)
	}
	d.pushedDuration += audio.CalculateAudioDuration(frames)

	if d.maxRmsSize > 0 && len(d.rmsBuffer) > d.maxRmsSize {
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

func (d *primarySpeakerDetector) getRmsForTimerange(startTime float64, endTime float64) (float64, bool) {
	if len(d.rmsBuffer) == 0 {
		return 0.0, false
	}

	start := int((d.pushedDuration - startTime) / d.frameSize)
	end := int((d.pushedDuration - endTime) / d.frameSize)
	startIdx := len(d.rmsBuffer) - start - 1
	endIdx := len(d.rmsBuffer) - end

	if endIdx < 0 || startIdx >= len(d.rmsBuffer) {
		return 0.0, false
	}
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > len(d.rmsBuffer) {
		endIdx = len(d.rmsBuffer)
	}

	if endIdx-startIdx < d.opt.MinRMSSamples {
		return 0.0, false
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

	return median, true
}

func (d *primarySpeakerDetector) updatePrimarySpeaker(sd *SpeechData) {
	if sd.SpeakerID == "" || !d.detectPrimary {
		d.primarySpeaker = ""
		return
	}

	rms, ok := d.getRmsForTimerange(sd.StartTime, sd.EndTime)
	if !ok {
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
		sd.Text = formatSpeakerText(d.primaryFormat, sd.Text, sd.SpeakerID)
	} else {
		if d.suppressBackground {
			return nil
		}
		sd.Text = formatSpeakerText(d.backgroundFormat, sd.Text, sd.SpeakerID)
	}
	return ev
}

func formatSpeakerText(format string, text string, speakerID string) string {
	formatted := strings.ReplaceAll(format, "{text}", text)
	formatted = strings.ReplaceAll(formatted, "{speaker_id}", speakerID)
	return formatted
}
