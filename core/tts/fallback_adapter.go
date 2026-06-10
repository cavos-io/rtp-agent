package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/logger"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

type FallbackAdapter struct {
	MetricsEmitter
	ErrorEmitter

	ttss                 []TTS
	capabilities         TTSCapabilities
	sampleRate           int
	numChannels          int
	maxRetryPerTTS       int
	availabilityHandlers []availabilityHandlerSubscription
	nextAvailabilityID   uint64

	mu     sync.Mutex
	status []fallbackTTSStatus
	closed bool

	nextRecoveryID uint64
	recoveryWG     sync.WaitGroup
}

type fallbackTTSStatus struct {
	available      bool
	recovering     bool
	recoveryID     uint64
	recoveryCancel context.CancelFunc
}

type AvailabilityChangedEvent struct {
	TTS       TTS
	Available bool
}

type AvailabilityChangedHandler func(AvailabilityChangedEvent)

type availabilityHandlerSubscription struct {
	id      uint64
	handler AvailabilityChangedHandler
}

type FallbackAdapterOptions struct {
	MaxRetryPerTTS int
	DisableRetries bool
	SampleRate     int
}

func NewFallbackAdapter(ttss []TTS) *FallbackAdapter {
	return NewFallbackAdapterWithOptions(ttss, FallbackAdapterOptions{})
}

func NewFallbackAdapterWithOptions(ttss []TTS, options FallbackAdapterOptions) *FallbackAdapter {
	if len(ttss) == 0 {
		panic("at least one TTS instance must be provided.")
	}

	numChannels := ttss[0].NumChannels()
	capabilities := TTSCapabilities{AlignedTranscript: true}
	sampleRate := options.SampleRate
	maxRetryPerTTS := options.MaxRetryPerTTS
	if options.DisableRetries {
		maxRetryPerTTS = 0
	} else if maxRetryPerTTS == 0 {
		maxRetryPerTTS = 2
	}
	for _, tts := range ttss {
		if tts.NumChannels() != numChannels {
			panic("all TTS must have the same number of channels")
		}
		ttsCapabilities := tts.Capabilities()
		capabilities.Streaming = capabilities.Streaming || ttsCapabilities.Streaming
		capabilities.AlignedTranscript = capabilities.AlignedTranscript && ttsCapabilities.AlignedTranscript
		if options.SampleRate == 0 && tts.SampleRate() > sampleRate {
			sampleRate = tts.SampleRate()
		}
	}

	status := make([]fallbackTTSStatus, len(ttss))
	for i := range status {
		status[i].available = true
	}

	return &FallbackAdapter{
		ttss:           ttss,
		capabilities:   capabilities,
		sampleRate:     sampleRate,
		numChannels:    numChannels,
		maxRetryPerTTS: maxRetryPerTTS,
		status:         status,
	}
}

func (f *FallbackAdapter) Label() string {
	return fmt.Sprintf("FallbackAdapter(%s)", f.ttss[0].Label())
}

func (f *FallbackAdapter) Model() string {
	return "FallbackAdapter"
}

func (f *FallbackAdapter) Provider() string {
	return "livekit"
}

func (f *FallbackAdapter) Prewarm() {
	Prewarm(f.ttss[0])
}

func (f *FallbackAdapter) Close() error {
	f.mu.Lock()
	f.closed = true
	cancels := make([]context.CancelFunc, 0, len(f.status))
	for i := range f.status {
		if f.status[i].recoveryCancel != nil {
			cancels = append(cancels, f.status[i].recoveryCancel)
			f.status[i].recoveryCancel = nil
		}
		f.status[i].recovering = false
		f.status[i].recoveryID = 0
	}
	f.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	f.recoveryWG.Wait()

	var errs []error
	for _, tts := range f.ttss {
		if err := Close(tts); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (f *FallbackAdapter) OnAvailabilityChanged(handler AvailabilityChangedHandler) func() {
	if handler == nil {
		return func() {}
	}
	f.mu.Lock()
	f.nextAvailabilityID++
	id := f.nextAvailabilityID
	f.availabilityHandlers = append(f.availabilityHandlers, availabilityHandlerSubscription{
		id:      id,
		handler: handler,
	})
	f.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			f.removeAvailabilityChangedHandler(id)
		})
	}
}

func (f *FallbackAdapter) removeAvailabilityChangedHandler(id uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, subscription := range f.availabilityHandlers {
		if subscription.id == id {
			f.availabilityHandlers = append(f.availabilityHandlers[:i], f.availabilityHandlers[i+1:]...)
			return
		}
	}
}

func (f *FallbackAdapter) OnMetricsCollected(handler TTSMetricsHandler) func() {
	if handler == nil {
		return func() {}
	}
	unsubscribes := make([]func(), 0, len(f.ttss)+1)
	unsubscribes = append(unsubscribes, f.MetricsEmitter.OnMetricsCollected(handler))
	for _, tts := range f.ttss {
		collector, ok := tts.(metricsCollectorTTS)
		if !ok {
			continue
		}
		unsubscribes = append(unsubscribes, collector.OnMetricsCollected(handler))
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			for _, unsubscribe := range unsubscribes {
				unsubscribe()
			}
		})
	}
}

func (f *FallbackAdapter) OnError(handler TTSErrorHandler) func() {
	if handler == nil {
		return func() {}
	}
	unsubscribes := make([]func(), 0, len(f.ttss)+1)
	unsubscribes = append(unsubscribes, f.ErrorEmitter.OnError(handler))
	for _, tts := range f.ttss {
		collector, ok := tts.(errorCollectorTTS)
		if !ok {
			continue
		}
		unsubscribes = append(unsubscribes, collector.OnError(handler))
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			for _, unsubscribe := range unsubscribes {
				unsubscribe()
			}
		})
	}
}

func (f *FallbackAdapter) Capabilities() TTSCapabilities {
	return f.capabilities
}

func (f *FallbackAdapter) SampleRate() int {
	return f.sampleRate
}

func (f *FallbackAdapter) NumChannels() int {
	return f.numChannels
}

func (f *FallbackAdapter) allUnavailableLocked() bool {
	for _, status := range f.status {
		if status.available {
			return false
		}
	}
	return true
}

func (f *FallbackAdapter) shouldTry(index int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[index].available || f.allUnavailableLocked()
}

func (f *FallbackAdapter) markUnavailable(index int) {
	f.mu.Lock()
	if index < 0 || index >= len(f.status) {
		f.mu.Unlock()
		return
	}
	if !f.status[index].available {
		f.mu.Unlock()
		return
	}
	f.status[index].available = false
	event := AvailabilityChangedEvent{TTS: f.ttss[index], Available: false}
	subscriptions := append([]availabilityHandlerSubscription(nil), f.availabilityHandlers...)
	f.mu.Unlock()

	for _, subscription := range subscriptions {
		callAvailabilityChangedHandler(subscription.handler, event)
	}
}

func (f *FallbackAdapter) markAvailable(index int) {
	f.mu.Lock()
	if index < 0 || index >= len(f.status) {
		f.mu.Unlock()
		return
	}
	alreadyAvailable := f.status[index].available
	f.status[index].available = true
	f.status[index].recovering = false
	if alreadyAvailable {
		f.mu.Unlock()
		return
	}
	event := AvailabilityChangedEvent{TTS: f.ttss[index], Available: true}
	subscriptions := append([]availabilityHandlerSubscription(nil), f.availabilityHandlers...)
	f.mu.Unlock()

	for _, subscription := range subscriptions {
		callAvailabilityChangedHandler(subscription.handler, event)
	}
}

func (f *FallbackAdapter) tryRecoverChunked(index int, text string) {
	f.mu.Lock()
	if f.closed || f.status[index].recovering {
		f.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.nextRecoveryID++
	recoveryID := f.nextRecoveryID
	f.status[index].recovering = true
	f.status[index].recoveryID = recoveryID
	f.status[index].recoveryCancel = cancel
	tts := f.ttss[index]
	f.recoveryWG.Add(1)
	f.mu.Unlock()

	go func() {
		defer f.recoveryWG.Done()
		defer f.finishRecovery(index, recoveryID)

		stream, err := tts.Synthesize(ctx, text)
		if err != nil || stream == nil {
			f.mu.Lock()
			f.status[index].recovering = false
			f.mu.Unlock()
			return
		}
		defer stream.Close()

		audioSent := false
		for {
			_, err := stream.Next()
			if err == nil {
				audioSent = true
				continue
			}
			if errors.Is(err, io.EOF) {
				if audioSent || strings.TrimSpace(text) == "" {
					f.markAvailable(index)
				} else {
					f.mu.Lock()
					f.status[index].recovering = false
					f.mu.Unlock()
				}
				return
			}
			f.mu.Lock()
			f.status[index].recovering = false
			f.mu.Unlock()
			return
		}
	}()
}

func (f *FallbackAdapter) tryRecoverStream(index int, inputs []fallbackSynthesizeInput) {
	replay := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if input.flush || input.text == "" {
			continue
		}
		replay = append(replay, input.text)
	}
	if len(replay) == 0 {
		return
	}

	f.mu.Lock()
	if f.closed || f.status[index].recovering {
		f.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.nextRecoveryID++
	recoveryID := f.nextRecoveryID
	f.status[index].recovering = true
	f.status[index].recoveryID = recoveryID
	f.status[index].recoveryCancel = cancel
	tts := f.ttss[index]
	f.recoveryWG.Add(1)
	f.mu.Unlock()

	go func() {
		defer f.recoveryWG.Done()
		defer f.finishRecovery(index, recoveryID)

		stream, err := streamForTTS(ctx, tts)
		if err != nil || stream == nil {
			f.mu.Lock()
			f.status[index].recovering = false
			f.mu.Unlock()
			return
		}
		defer stream.Close()

		for _, text := range replay {
			if err := stream.PushText(text); err != nil {
				f.mu.Lock()
				f.status[index].recovering = false
				f.mu.Unlock()
				return
			}
		}
		if err := endSynthesizeStreamInput(stream); err != nil {
			f.mu.Lock()
			f.status[index].recovering = false
			f.mu.Unlock()
			return
		}

		audioSent := false
		for {
			_, err := stream.Next()
			if err == nil {
				audioSent = true
				continue
			}
			if errors.Is(err, io.EOF) {
				if audioSent || strings.TrimSpace(strings.Join(replay, "")) == "" {
					f.markAvailable(index)
				} else {
					f.mu.Lock()
					f.status[index].recovering = false
					f.mu.Unlock()
				}
				return
			}
			f.mu.Lock()
			f.status[index].recovering = false
			f.mu.Unlock()
			return
		}
	}()
}

func (f *FallbackAdapter) finishRecovery(index int, recoveryID uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.status) {
		return
	}
	if f.status[index].recoveryID != recoveryID {
		return
	}
	f.status[index].recovering = false
	f.status[index].recoveryID = 0
	f.status[index].recoveryCancel = nil
}

func streamForTTS(ctx context.Context, tts TTS) (SynthesizeStream, error) {
	if tts.Capabilities().Streaming {
		return tts.Stream(ctx)
	}
	return NewStreamAdapter(tts).Stream(ctx)
}

func (f *FallbackAdapter) normalizeAudio(audio *SynthesizedAudio) (*SynthesizedAudio, error) {
	if audio == nil || audio.Frame == nil || audio.Frame.SampleRate == 0 || audio.Frame.SampleRate == uint32(f.sampleRate) {
		return audio, nil
	}
	frame, err := resampleAudioFrame(audio.Frame, uint32(f.sampleRate))
	if err != nil {
		return nil, err
	}
	normalized := *audio
	normalized.Frame = frame
	return &normalized, nil
}

func resampleAudioFrame(frame *model.AudioFrame, outputRate uint32) (*model.AudioFrame, error) {
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
			Data:              nil,
			SampleRate:        outputRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: 0,
		}, nil
	}

	inRate := frame.SampleRate
	channels := frame.NumChannels
	outSamples := uint32((uint64(frame.SamplesPerChannel)*uint64(outputRate) + uint64(inRate) - 1) / uint64(inRate))
	if outSamples == 0 && frame.SamplesPerChannel > 0 {
		outSamples = 1
	}
	out := make([]byte, int(outSamples*channels*2))

	inputSamples := int(frame.SamplesPerChannel)
	outputSamples := int(outSamples)
	channelCount := int(channels)
	for outIdx := 0; outIdx < outputSamples; outIdx++ {
		srcIdx := 0
		if outputSamples > 0 {
			srcIdx = int(uint64(outIdx) * uint64(inRate) / uint64(outputRate))
		}
		if srcIdx >= inputSamples {
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
		NumChannels:       channels,
		SamplesPerChannel: outSamples,
	}, nil
}

type fallbackChunkedStream struct {
	adapter *FallbackAdapter
	ctx     context.Context
	cancel  context.CancelFunc
	text    string

	mu           sync.Mutex
	activeStream ChunkedStream
	activeIndex  int
	retries      map[int]int
	requestID    string

	eventCh chan *SynthesizedAudio
	errCh   chan error
	closeCh chan struct{}
	doneCh  chan struct{}
	closed  bool
	done    bool
	err     error
	metrics fallbackMetricsState
}

type fallbackMetricsState struct {
	startedAt time.Time
	ttfb      float64
	ttfbSet   bool
	audioDur  float64
}

func (f *FallbackAdapter) Synthesize(ctx context.Context, text string) (ChunkedStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &fallbackChunkedStream{
		adapter:   f,
		ctx:       ctx,
		cancel:    cancel,
		text:      text,
		eventCh:   make(chan *SynthesizedAudio, 100),
		errCh:     make(chan error, 1),
		closeCh:   make(chan struct{}),
		doneCh:    make(chan struct{}),
		retries:   make(map[int]int),
		requestID: cavosmath.ShortUUID(""),
		metrics:   fallbackMetricsState{startedAt: time.Now()},
	}

	if err := s.tryStartStream(0); err != nil {
		cancel()
		return nil, err
	}

	go s.monitorStream()

	return s, nil
}

func (s *fallbackChunkedStream) tryStartStream(index int) error {
	var lastErr error
	for i := index; i < len(s.adapter.ttss); i++ {
		if !s.adapter.shouldTry(i) {
			continue
		}
		for {
			tts := s.adapter.ttss[i]
			stream, err := tts.Synthesize(s.ctx, s.text)
			if err == nil {
				s.activeStream = stream
				s.activeIndex = i
				return nil
			}
			logger.Logger.Errorw("Failed to start TTS synthesize stream", err, "tts", tts.Label())
			lastErr = err
			if s.canRetryTTS(i) {
				emitTTSError(s.adapter, err, true)
				s.retries[i]++
				continue
			}
			s.adapter.markUnavailable(i)
			s.adapter.tryRecoverChunked(i, s.text)
			break
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("all fallback TTS exhausted")
}

func (s *fallbackChunkedStream) monitorStream() {
	defer close(s.doneCh)
	defer s.cancel()

	outputSent := false
	var pending *SynthesizedAudio
	pendingTail := false
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		stream := s.activeStream
		s.mu.Unlock()

		ev, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || outputSent {
				_ = stream.Close()
				if errors.Is(err, io.EOF) && !outputSent && pending == nil && strings.TrimSpace(s.text) != "" {
					err := fmt.Errorf("no audio frames were pushed for text: %s", s.text)
					s.markDone(err)
					emitTTSError(s.adapter, err, false)
					s.errCh <- err
					return
				}
				if pending != nil {
					pending = cloneSynthesizedAudio(pending)
					pending.IsFinal = true
					s.emitMetrics(pending)
					select {
					case s.eventCh <- pending:
						outputSent = true
					case <-s.closeCh:
						return
					}
				}
				s.markDone(nil)
				s.errCh <- io.EOF
				return
			}

			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}

			logger.Logger.Warnw("TTS synthesize stream failed, attempting fallback", err, "failed_tts", s.adapter.ttss[s.activeIndex].Label())
			stream.Close()
			pending = nil
			pendingTail = false

			nextIndex := s.activeIndex + 1
			if s.canRetryTTS(s.activeIndex) {
				emitTTSError(s.adapter, err, true)
				s.retries[s.activeIndex]++
				nextIndex = s.activeIndex
			} else {
				s.adapter.markUnavailable(s.activeIndex)
				s.adapter.tryRecoverChunked(s.activeIndex, s.text)
			}

			if fbErr := s.tryStartStream(nextIndex); fbErr != nil {
				s.markDoneLocked(fbErr)
				emitTTSError(s.adapter, fbErr, false)
				s.errCh <- fbErr
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			continue
		}

		ev, err = s.adapter.normalizeAudio(ev)
		if err != nil {
			if outputSent {
				_ = stream.Close()
				if pending != nil {
					pending = cloneSynthesizedAudio(pending)
					pending.IsFinal = true
					s.emitMetrics(pending)
					select {
					case s.eventCh <- pending:
					case <-s.closeCh:
						return
					}
				}
				s.markDone(nil)
				s.errCh <- io.EOF
				return
			}
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			logger.Logger.Warnw("TTS synthesize stream produced invalid audio, attempting fallback", err, "failed_tts", s.adapter.ttss[s.activeIndex].Label())
			stream.Close()
			pending = nil
			pendingTail = false

			nextIndex := s.activeIndex + 1
			if s.canRetryTTS(s.activeIndex) {
				emitTTSError(s.adapter, err, true)
				s.retries[s.activeIndex]++
				nextIndex = s.activeIndex
			} else {
				s.adapter.markUnavailable(s.activeIndex)
				s.adapter.tryRecoverChunked(s.activeIndex, s.text)
			}

			if fbErr := s.tryStartStream(nextIndex); fbErr != nil {
				s.markDoneLocked(fbErr)
				emitTTSError(s.adapter, fbErr, false)
				s.errCh <- fbErr
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			continue
		}
		ev = cloneSynthesizedAudio(ev)
		ev.RequestID = s.requestID
		ev.SegmentID = ""
		s.observeAudio(ev)

		if pending != nil {
			combined, combineErr := combineAudioFrames(pending.Frame, ev.Frame)
			if pendingTail && combineErr == nil {
				ev = cloneSynthesizedAudio(ev)
				ev.Frame = combined
			} else {
				pending = cloneSynthesizedAudio(pending)
				pending.IsFinal = false
				select {
				case s.eventCh <- pending:
					outputSent = true
				case <-s.closeCh:
					return
				}
				pendingTail = false
			}
		}
		head, tail, ok := splitSynthesizedAudioTail(ev)
		if ok {
			head.RequestID = s.requestID
			head.SegmentID = ""
			head.IsFinal = false
			select {
			case s.eventCh <- head:
				outputSent = true
			case <-s.closeCh:
				return
			}
			pending = tail
			pendingTail = true
			continue
		}
		pending = tail
		pendingTail = false
	}
}

func (s *fallbackChunkedStream) canRetryTTS(index int) bool {
	if s.adapter.maxRetryPerTTS <= 0 {
		return false
	}
	if s.retries == nil {
		s.retries = make(map[int]int)
	}
	return s.retries[index] < s.adapter.maxRetryPerTTS
}

func (s *fallbackChunkedStream) observeAudio(audio *SynthesizedAudio) {
	if audio == nil || audio.Frame == nil {
		return
	}
	if s.metrics.startedAt.IsZero() {
		s.metrics.startedAt = time.Now()
	}
	if !s.metrics.ttfbSet {
		s.metrics.ttfb = time.Since(s.metrics.startedAt).Seconds()
		s.metrics.ttfbSet = true
	}
	s.metrics.audioDur += audioFrameDurationSeconds(audio.Frame)
}

func (s *fallbackChunkedStream) emitMetrics(audio *SynthesizedAudio) {
	if audio == nil {
		return
	}
	ttfb := -1.0
	if s.metrics.ttfbSet {
		ttfb = s.metrics.ttfb
	}
	startedAt := s.metrics.startedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	s.adapter.EmitMetricsCollected(&telemetry.TTSMetrics{
		Label:           s.adapter.Label(),
		RequestID:       audio.RequestID,
		Timestamp:       time.Now(),
		TTFB:            ttfb,
		Duration:        time.Since(startedAt).Seconds(),
		AudioDuration:   s.metrics.audioDur,
		CharactersCount: len(s.text),
		Streamed:        false,
		Metadata: &telemetry.Metadata{
			ModelName:     s.adapter.Model(),
			ModelProvider: s.adapter.Provider(),
		},
	})
}

func (s *fallbackChunkedStream) Next() (*SynthesizedAudio, error) {
	select {
	case ev := <-s.eventCh:
		return ev, nil
	default:
	}

	select {
	case ev := <-s.eventCh:
		return ev, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.closeCh:
		return nil, context.Canceled
	}
}

func (s *fallbackChunkedStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.closeCh)
	s.cancel()
	s.markDoneLocked(nil)
	active := s.activeStream
	s.mu.Unlock()

	err := active.Close()
	<-s.doneCh
	return err
}

func (s *fallbackChunkedStream) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *fallbackChunkedStream) Exception() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *fallbackChunkedStream) markDone(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markDoneLocked(err)
}

func (s *fallbackChunkedStream) markDoneLocked(err error) {
	s.done = true
	if err != nil && !errors.Is(err, io.EOF) {
		s.err = err
	}
}

type fallbackSynthesizeStream struct {
	adapter *FallbackAdapter
	ctx     context.Context
	cancel  context.CancelFunc

	mu           sync.Mutex
	activeStream SynthesizeStream
	activeIndex  int
	retries      map[int]int
	inputBuffer  []fallbackSynthesizeInput
	requestID    string
	segmentID    string

	eventCh   chan *SynthesizedAudio
	errCh     chan error
	closeCh   chan struct{}
	doneCh    chan struct{}
	closed    bool
	inputDone bool
	started   bool
	flushed   bool
	done      bool
	err       error
	metrics   fallbackMetricsState
}

type fallbackSynthesizeInput struct {
	text  string
	flush bool
}

func (f *FallbackAdapter) Stream(ctx context.Context) (SynthesizeStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &fallbackSynthesizeStream{
		adapter:   f,
		ctx:       ctx,
		cancel:    cancel,
		eventCh:   make(chan *SynthesizedAudio, 100),
		errCh:     make(chan error, 1),
		closeCh:   make(chan struct{}),
		doneCh:    make(chan struct{}),
		retries:   make(map[int]int),
		requestID: cavosmath.ShortUUID(""),
		segmentID: cavosmath.ShortUUID(""),
		metrics:   fallbackMetricsState{startedAt: time.Now()},
	}

	if err := s.tryStartStream(0); err != nil {
		cancel()
		return nil, err
	}

	go s.monitorStream()

	return s, nil
}

func (s *fallbackSynthesizeStream) tryStartStream(index int) error {
	var lastErr error
	for i := index; i < len(s.adapter.ttss); i++ {
		if !s.adapter.shouldTry(i) {
			continue
		}
		for {
			tts := s.adapter.ttss[i]
			stream, err := s.startProviderStream(tts)
			if err != nil {
				logger.Logger.Errorw("Failed to start TTS stream", err, "tts", tts.Label())
				lastErr = err
				if s.canRetryTTS(i) {
					emitTTSError(s.adapter, err, true)
					s.retries[i]++
					continue
				}
				s.adapter.markUnavailable(i)
				s.adapter.tryRecoverStream(i, s.inputBuffer)
				break
			}

			if err := s.replayBufferedText(stream); err != nil {
				stream.Close()
				lastErr = err
				if s.canRetryTTS(i) {
					emitTTSError(s.adapter, err, true)
					s.retries[i]++
					continue
				}
				s.adapter.markUnavailable(i)
				s.adapter.tryRecoverStream(i, s.inputBuffer)
				break
			}

			s.activeStream = stream
			s.activeIndex = i
			return nil
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("all fallback TTS exhausted")
}

func (s *fallbackSynthesizeStream) startProviderStream(tts TTS) (SynthesizeStream, error) {
	return streamForTTS(s.ctx, tts)
}

func (s *fallbackSynthesizeStream) replayBufferedText(stream SynthesizeStream) error {
	for _, input := range s.inputBuffer {
		if input.flush {
			continue
		}
		if input.text == "" {
			continue
		}
		if err := stream.PushText(input.text); err != nil {
			return err
		}
	}
	if s.inputDone {
		return endSynthesizeStreamInput(stream)
	}
	return nil
}

func (s *fallbackSynthesizeStream) monitorStream() {
	defer close(s.doneCh)
	defer s.cancel()

	outputSent := false
	var pending *SynthesizedAudio
	pendingTail := false
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		stream := s.activeStream
		s.mu.Unlock()

		ev, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || outputSent {
				_ = stream.Close()
				if errors.Is(err, io.EOF) && !outputSent && pending == nil && strings.TrimSpace(s.pushedText()) != "" {
					err := fmt.Errorf("no audio frames were pushed for text: %s", s.pushedText())
					s.markDone(err)
					emitTTSError(s.adapter, err, false)
					s.errCh <- err
					return
				}
				if pending != nil {
					pending = cloneSynthesizedAudio(pending)
					pending.IsFinal = true
					s.emitMetrics(pending)
					select {
					case s.eventCh <- pending:
						outputSent = true
					case <-s.closeCh:
						return
					}
				}
				s.markDone(nil)
				s.errCh <- io.EOF
				return
			}

			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}

			logger.Logger.Warnw("TTS stream failed, attempting fallback", err, "failed_tts", s.adapter.ttss[s.activeIndex].Label())
			stream.Close()
			pending = nil
			pendingTail = false

			nextIndex := s.activeIndex + 1
			if s.canRetryTTS(s.activeIndex) {
				emitTTSError(s.adapter, err, true)
				s.retries[s.activeIndex]++
				nextIndex = s.activeIndex
			} else {
				s.adapter.markUnavailable(s.activeIndex)
				s.adapter.tryRecoverStream(s.activeIndex, s.inputBuffer)
			}

			if fbErr := s.tryStartStream(nextIndex); fbErr != nil {
				s.markDoneLocked(fbErr)
				emitTTSError(s.adapter, fbErr, false)
				s.errCh <- fbErr
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			continue
		}

		ev, err = s.adapter.normalizeAudio(ev)
		if err != nil {
			if outputSent {
				_ = stream.Close()
				if pending != nil {
					pending = cloneSynthesizedAudio(pending)
					pending.IsFinal = true
					s.emitMetrics(pending)
					select {
					case s.eventCh <- pending:
					case <-s.closeCh:
						return
					}
				}
				s.markDone(nil)
				s.errCh <- io.EOF
				return
			}
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			logger.Logger.Warnw("TTS stream produced invalid audio, attempting fallback", err, "failed_tts", s.adapter.ttss[s.activeIndex].Label())
			stream.Close()
			pending = nil
			pendingTail = false

			nextIndex := s.activeIndex + 1
			if s.canRetryTTS(s.activeIndex) {
				emitTTSError(s.adapter, err, true)
				s.retries[s.activeIndex]++
				nextIndex = s.activeIndex
			} else {
				s.adapter.markUnavailable(s.activeIndex)
				s.adapter.tryRecoverStream(s.activeIndex, s.inputBuffer)
			}

			if fbErr := s.tryStartStream(nextIndex); fbErr != nil {
				s.markDoneLocked(fbErr)
				emitTTSError(s.adapter, fbErr, false)
				s.errCh <- fbErr
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			continue
		}
		ev = cloneSynthesizedAudio(ev)
		ev.RequestID = s.requestID
		ev.SegmentID = s.segmentID
		s.observeAudio(ev)

		providerFinal := ev.IsFinal
		if pending != nil {
			combined, combineErr := combineAudioFrames(pending.Frame, ev.Frame)
			if pendingTail && combineErr == nil {
				ev = cloneSynthesizedAudio(ev)
				ev.Frame = combined
			} else {
				pending = cloneSynthesizedAudio(pending)
				pending.IsFinal = false
				select {
				case s.eventCh <- pending:
					outputSent = true
				case <-s.closeCh:
					return
				}
				pendingTail = false
			}
		}
		if providerFinal {
			head, tail, ok := splitSynthesizedAudioTail(ev)
			if ok {
				head.RequestID = s.requestID
				head.SegmentID = s.segmentID
				head.IsFinal = false
				select {
				case s.eventCh <- head:
					outputSent = true
				case <-s.closeCh:
					return
				}
				ev = tail
			}
			ev = cloneSynthesizedAudio(ev)
			ev.RequestID = s.requestID
			ev.SegmentID = s.segmentID
			ev.IsFinal = true
			s.emitMetrics(ev)
			select {
			case s.eventCh <- ev:
				outputSent = true
			case <-s.closeCh:
				return
			}
			_ = stream.Close()
			s.markDone(nil)
			s.errCh <- io.EOF
			return
		}
		head, tail, ok := splitSynthesizedAudioTail(ev)
		if ok {
			head.RequestID = s.requestID
			head.SegmentID = s.segmentID
			head.IsFinal = false
			select {
			case s.eventCh <- head:
				outputSent = true
			case <-s.closeCh:
				return
			}
			pending = tail
			pendingTail = true
			continue
		}
		pending = tail
		pendingTail = false
	}
}

func (s *fallbackSynthesizeStream) canRetryTTS(index int) bool {
	if s.adapter.maxRetryPerTTS <= 0 {
		return false
	}
	if s.retries == nil {
		s.retries = make(map[int]int)
	}
	return s.retries[index] < s.adapter.maxRetryPerTTS
}

func (s *fallbackSynthesizeStream) observeAudio(audio *SynthesizedAudio) {
	if audio == nil || audio.Frame == nil {
		return
	}
	if s.metrics.startedAt.IsZero() {
		s.metrics.startedAt = time.Now()
	}
	if !s.metrics.ttfbSet {
		s.metrics.ttfb = time.Since(s.metrics.startedAt).Seconds()
		s.metrics.ttfbSet = true
	}
	s.metrics.audioDur += audioFrameDurationSeconds(audio.Frame)
}

func (s *fallbackSynthesizeStream) emitMetrics(audio *SynthesizedAudio) {
	if audio == nil {
		return
	}
	ttfb := -1.0
	if s.metrics.ttfbSet {
		ttfb = s.metrics.ttfb
	}
	startedAt := s.metrics.startedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	s.adapter.EmitMetricsCollected(&telemetry.TTSMetrics{
		Label:           s.adapter.Label(),
		RequestID:       audio.RequestID,
		SegmentID:       audio.SegmentID,
		Timestamp:       time.Now(),
		TTFB:            ttfb,
		Duration:        time.Since(startedAt).Seconds(),
		AudioDuration:   s.metrics.audioDur,
		CharactersCount: len(s.pushedText()),
		Streamed:        true,
		Metadata: &telemetry.Metadata{
			ModelName:     s.adapter.Model(),
			ModelProvider: s.adapter.Provider(),
		},
	})
}

func (s *fallbackSynthesizeStream) pushedText() string {
	texts := make([]string, 0, len(s.inputBuffer))
	for _, input := range s.inputBuffer {
		if !input.flush {
			texts = append(texts, input.text)
		}
	}
	return strings.Join(texts, "")
}

func (s *fallbackSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if s.inputDone {
		return nil
	}
	if text == "" {
		return nil
	}
	s.started = true
	s.inputBuffer = append(s.inputBuffer, fallbackSynthesizeInput{text: text})
	return s.activeStream.PushText(text)
}

func (s *fallbackSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if s.inputDone {
		return nil
	}
	if s.started {
		s.flushed = true
	}
	s.inputBuffer = append(s.inputBuffer, fallbackSynthesizeInput{flush: true})
	return s.activeStream.Flush()
}

func (s *fallbackSynthesizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if s.inputDone {
		return nil
	}
	s.inputDone = true
	if s.started {
		s.flushed = true
	}
	return endSynthesizeStreamInput(s.activeStream)
}

func (s *fallbackSynthesizeStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.closeCh)
	s.cancel()
	s.markDoneLocked(nil)
	active := s.activeStream
	s.mu.Unlock()

	err := active.Close()
	<-s.doneCh
	return err
}

func (s *fallbackSynthesizeStream) Next() (*SynthesizedAudio, error) {
	select {
	case ev := <-s.eventCh:
		return ev, nil
	default:
	}

	select {
	case ev := <-s.eventCh:
		return ev, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.closeCh:
		return nil, context.Canceled
	}
}

func (s *fallbackSynthesizeStream) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *fallbackSynthesizeStream) Exception() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *fallbackSynthesizeStream) markDone(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markDoneLocked(err)
}

func (s *fallbackSynthesizeStream) markDoneLocked(err error) {
	s.done = true
	if err != nil && !errors.Is(err, io.EOF) {
		s.err = err
	}
}
