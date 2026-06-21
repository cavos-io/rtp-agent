package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/tokenize"
)

type StreamAdapter struct {
	MetricsEmitter
	ErrorEmitter
	mu                  sync.Mutex
	tts                 TTS
	metricsUnsubscribes []func()
}

func NewStreamAdapter(t TTS) *StreamAdapter {
	return &StreamAdapter{
		tts: t,
	}
}

func (a *StreamAdapter) Label() string {
	return "tts.StreamAdapter"
}

func (a *StreamAdapter) Model() string {
	return Model(a.tts)
}

func (a *StreamAdapter) Provider() string {
	return Provider(a.tts)
}

func (a *StreamAdapter) Prewarm() {
	Prewarm(a.tts)
}

func (a *StreamAdapter) Close() error {
	a.mu.Lock()
	unsubscribes := append([]func(){}, a.metricsUnsubscribes...)
	a.metricsUnsubscribes = nil
	a.mu.Unlock()

	for _, unsubscribe := range unsubscribes {
		unsubscribe()
	}
	return nil
}

func (a *StreamAdapter) OnMetricsCollected(handler TTSMetricsHandler) func() {
	localUnsubscribe := a.MetricsEmitter.OnMetricsCollected(handler)
	providerUnsubscribes := make([]func(), 0, 1)
	if collector, ok := a.tts.(metricsCollectorTTS); ok {
		providerUnsubscribes = append(providerUnsubscribes, collector.OnMetricsCollected(handler))
	}
	a.mu.Lock()
	a.metricsUnsubscribes = append(a.metricsUnsubscribes, providerUnsubscribes...)
	a.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			localUnsubscribe()
			for _, unsubscribe := range providerUnsubscribes {
				unsubscribe()
			}
		})
	}
}

func (a *StreamAdapter) OnError(handler TTSErrorHandler) func() {
	return a.ErrorEmitter.OnError(handler)
}

func (a *StreamAdapter) Capabilities() TTSCapabilities {
	return TTSCapabilities{Streaming: true, AlignedTranscript: true}
}

func (a *StreamAdapter) SampleRate() int {
	return a.tts.SampleRate()
}

func (a *StreamAdapter) NumChannels() int {
	return a.tts.NumChannels()
}

func (a *StreamAdapter) Synthesize(ctx context.Context, text string) (ChunkedStream, error) {
	return a.tts.Synthesize(ctx, text)
}

type streamAdapterWrapper struct {
	adapter   *StreamAdapter
	ctx       context.Context
	cancel    context.CancelFunc
	requestID string
	segmentID string
	eventCh   chan *SynthesizedAudio
	errCh     chan error
	inputCh   chan streamAdapterInput
	flushCh   chan struct{}
	doneCh    chan struct{}
	mu        sync.Mutex
	active    ChunkedStream
	closed    bool
	inputDone bool
	started   bool
	done      bool
	exception error

	segmentPending *SynthesizedAudio
	metrics        map[string]*streamAdapterSegmentMetrics
	audioCursor    float64
}

type streamAdapterInput struct {
	text  string
	flush bool
}

type streamAdapterSegmentMetrics struct {
	text      string
	startedAt time.Time
	ttfb      float64
	ttfbSet   bool
	audioDur  float64
}

func (a *StreamAdapter) Stream(ctx context.Context) (SynthesizeStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	w := &streamAdapterWrapper{
		adapter:   a,
		ctx:       ctx,
		cancel:    cancel,
		requestID: cavosmath.ShortUUID(""),
		segmentID: cavosmath.ShortUUID(""),
		eventCh:   make(chan *SynthesizedAudio, 100),
		errCh:     make(chan error, 1),
		inputCh:   make(chan streamAdapterInput, 100),
		flushCh:   make(chan struct{}, 100),
		doneCh:    make(chan struct{}),
		metrics:   make(map[string]*streamAdapterSegmentMetrics),
	}

	go w.run()
	return w, nil
}

func (w *streamAdapterWrapper) run() {
	defer close(w.doneCh)
	defer close(w.eventCh)

	tokenizer := newRetainFormatSentenceStream("en")

	// Stream text to tokenizer
	go func() {
		for {
			select {
			case <-w.ctx.Done():
				tokenizer.AClose()
				return
			case input, ok := <-w.inputCh:
				if !ok {
					if w.isClosed() {
						tokenizer.AClose()
						return
					}
					tokenizer.Flush()
					tokenizer.Close()
					return
				}
				if input.flush {
					tokenizer.Flush()
					select {
					case w.flushCh <- struct{}{}:
					default:
					}
					continue
				}
				tokenizer.PushText(input.text)
			}
		}
	}()

	// Read sentences and synthesize
	for {
		tok, err := tokenizer.Next()
		if err != nil {
			break
		}
		if tok.Token != "" {
			if err := w.synthesize(tok.Token, w.segmentID); err != nil {
				if w.isClosed() {
					w.markDone(nil)
					return
				}
				w.markDone(err)
				w.sendErr(err)
				return
			}
		}
		w.flushCompletedSegments()
	}
	w.flushSegmentPending(true)
	w.markDone(nil)
}

func (w *streamAdapterWrapper) flushCompletedSegments() {
	for {
		select {
		case <-w.flushCh:
			w.flushSegmentPending(false)
		default:
			return
		}
	}
}

func (w *streamAdapterWrapper) synthesize(text string, segmentID string) error {
	synthText := strings.TrimSpace(text)
	if synthText == "" {
		return nil
	}

	w.flushSegmentPending(false)

	stream, err := w.adapter.tts.Synthesize(w.ctx, synthText)
	if err != nil {
		return err
	}
	if isNilChunkedStream(stream) {
		return fmt.Errorf("TTS returned nil chunked stream")
	}
	w.setActiveStream(stream)
	defer func() {
		w.clearActiveStream(stream)
		stream.Close()
	}()

	var pending *SynthesizedAudio
	pendingTail := false
	transcriptPending := true
	for {
		audio, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if pending != nil {
					w.setSegmentPending(pending, text, segmentID, transcriptPending)
				} else {
					return fmt.Errorf("no audio frames were pushed for text: %s", synthText)
				}
				return nil
			}
			return err
		}
		if audio == nil || audio.Frame == nil {
			continue
		}

		if pending != nil {
			combined, combineErr := combineAudioFrames(pending.Frame, audio.Frame)
			if pendingTail && combineErr == nil {
				audio = cloneSynthesizedAudio(audio)
				audio.Frame = combined
			} else {
				w.sendSynthesizedAudio(pending, text, segmentID, false, transcriptPending)
				transcriptPending = false
				pendingTail = false
			}
		}

		head, tail, ok := splitSynthesizedAudioTail(audio)
		if ok {
			w.sendSynthesizedAudio(head, text, segmentID, false, transcriptPending)
			transcriptPending = false
			pending = tail
			pendingTail = true
			continue
		}
		pending = tail
		pendingTail = false
	}
}

func isNilChunkedStream(stream ChunkedStream) bool {
	if stream == nil {
		return true
	}
	value := reflect.ValueOf(stream)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (w *streamAdapterWrapper) setSegmentPending(audio *SynthesizedAudio, text string, segmentID string, includeTranscript bool) {
	audio = cloneSynthesizedAudio(audio)
	audio.SegmentID = segmentID
	audio.RequestID = w.requestID
	audio.IsFinal = false
	if includeTranscript {
		audio.DeltaText = text
		audio.TimedTranscript = []TimedString{{Text: text, StartTime: w.audioCursor}}
	}
	w.segmentPending = audio
}

func (w *streamAdapterWrapper) flushSegmentPending(isFinal bool) {
	if w.segmentPending == nil {
		if isFinal {
			w.sendFinalMarker(w.segmentID)
		}
		return
	}
	audio := cloneSynthesizedAudio(w.segmentPending)
	audio.IsFinal = isFinal
	w.segmentPending = nil
	w.observeSegmentAudio(audio, audio.DeltaText)
	if isFinal {
		w.emitSegmentMetrics(audio)
	}
	w.eventCh <- audio
}

func (w *streamAdapterWrapper) sendFinalMarker(segmentID string) {
	if _, ok := w.metrics[segmentID]; !ok {
		return
	}
	sampleRate := uint32(w.adapter.SampleRate())
	numChannels := uint32(w.adapter.NumChannels())
	if sampleRate == 0 {
		sampleRate = 24000
	}
	if numChannels == 0 {
		numChannels = 1
	}
	samples := sampleRate * finalTailMillis / 1000
	if samples == 0 {
		samples = 1
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, samples*numChannels*2),
		SampleRate:        sampleRate,
		NumChannels:       numChannels,
		SamplesPerChannel: samples,
	}
	audio := &SynthesizedAudio{
		Frame:     frame,
		RequestID: w.requestID,
		SegmentID: segmentID,
		IsFinal:   true,
	}
	w.emitSegmentMetrics(audio)
	w.eventCh <- audio
}

func (w *streamAdapterWrapper) sendSynthesizedAudio(audio *SynthesizedAudio, text string, segmentID string, isFinal bool, includeTranscript bool) {
	audio = cloneSynthesizedAudio(audio)
	audio.SegmentID = segmentID
	audio.RequestID = w.requestID
	audio.IsFinal = isFinal
	if includeTranscript {
		audio.DeltaText = text
		audio.TimedTranscript = []TimedString{{Text: text, StartTime: w.audioCursor}}
	}
	w.observeSegmentAudio(audio, text)
	if isFinal {
		w.emitSegmentMetrics(audio)
	}
	w.eventCh <- audio
}

func (w *streamAdapterWrapper) observeSegmentAudio(audio *SynthesizedAudio, text string) {
	if audio == nil || audio.Frame == nil {
		return
	}
	key := audio.SegmentID
	metrics := w.metrics[key]
	if metrics == nil {
		metrics = &streamAdapterSegmentMetrics{startedAt: time.Now()}
		w.metrics[key] = metrics
	}
	if metrics.text == "" && text != "" {
		metrics.text = text
	}
	if !metrics.ttfbSet {
		metrics.ttfb = time.Since(metrics.startedAt).Seconds()
		metrics.ttfbSet = true
	}
	duration := audioFrameDurationSeconds(audio.Frame)
	metrics.audioDur += duration
	w.audioCursor += duration
}

func (w *streamAdapterWrapper) emitSegmentMetrics(audio *SynthesizedAudio) {
	if audio == nil {
		return
	}
	key := audio.SegmentID
	metrics := w.metrics[key]
	if metrics == nil {
		return
	}
	delete(w.metrics, key)

	ttfb := -1.0
	if metrics.ttfbSet {
		ttfb = metrics.ttfb
	}
	w.adapter.EmitMetricsCollected(&telemetry.TTSMetrics{
		Label:           w.adapter.Label(),
		RequestID:       audio.RequestID,
		SegmentID:       audio.SegmentID,
		Timestamp:       time.Now(),
		TTFB:            ttfb,
		Duration:        time.Since(metrics.startedAt).Seconds(),
		AudioDuration:   metrics.audioDur,
		CharactersCount: len(metrics.text),
		Streamed:        true,
		Metadata: &telemetry.Metadata{
			ModelName:     w.adapter.Model(),
			ModelProvider: w.adapter.Provider(),
		},
	})
}

func newRetainFormatSentenceStream(language string) tokenize.SentenceStream {
	return tokenize.NewBufferedTokenStream(func(s string) []string {
		res := tokenize.SplitSentences(s, 20, true)
		tokens := make([]string, len(res))
		for i, r := range res {
			tokens[i] = r.Token
		}
		return tokens
	}, 20, 10)
}

func (w *streamAdapterWrapper) sendErr(err error) {
	emitTTSError(w.adapter, err, false)
	select {
	case w.errCh <- err:
	default:
	}
}

func (w *streamAdapterWrapper) setActiveStream(stream ChunkedStream) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.active = stream
}

func (w *streamAdapterWrapper) clearActiveStream(stream ChunkedStream) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.active == stream {
		w.active = nil
	}
}

func (w *streamAdapterWrapper) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func (w *streamAdapterWrapper) PushText(text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	if w.inputDone {
		return nil
	}
	if text == "" {
		return nil
	}
	w.started = true
	w.inputCh <- streamAdapterInput{text: text}
	return nil
}

func (w *streamAdapterWrapper) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	if w.inputDone {
		return nil
	}
	w.inputCh <- streamAdapterInput{flush: true}
	return nil
}

func (w *streamAdapterWrapper) EndInput() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	if w.inputDone {
		return nil
	}
	w.inputDone = true
	close(w.inputCh)
	return nil
}

func (w *streamAdapterWrapper) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	active := w.active
	w.cancel()
	if !w.inputDone {
		w.inputDone = true
		close(w.inputCh)
	}
	w.mu.Unlock()
	w.markDone(nil)
	var err error
	if active != nil {
		err = active.Close()
	}
	<-w.doneCh
	return err
}

func (w *streamAdapterWrapper) Next() (*SynthesizedAudio, error) {
	ev, ok := <-w.eventCh
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
	return nil, io.EOF
}

func (w *streamAdapterWrapper) Done() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.done
}

func (w *streamAdapterWrapper) Exception() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.exception
}

func (w *streamAdapterWrapper) markDone(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.done = true
	if err != nil && !errors.Is(err, io.EOF) {
		w.exception = err
	}
}
