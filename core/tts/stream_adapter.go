package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/tokenize"
)

type StreamAdapter struct {
	MetricsEmitter
	ErrorEmitter
	tts TTS
}

func NewStreamAdapter(t TTS) *StreamAdapter {
	return &StreamAdapter{
		tts: t,
	}
}

func (a *StreamAdapter) Label() string {
	return fmt.Sprintf("StreamAdapter(%s)", a.tts.Label())
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
	return Close(a.tts)
}

func (a *StreamAdapter) OnMetricsCollected(handler TTSMetricsHandler) func() {
	unsubscribes := []func(){a.MetricsEmitter.OnMetricsCollected(handler)}
	if collector, ok := a.tts.(metricsCollectorTTS); ok {
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

func (a *StreamAdapter) OnError(handler TTSErrorHandler) func() {
	unsubscribes := []func(){a.ErrorEmitter.OnError(handler)}
	if collector, ok := a.tts.(errorCollectorTTS); ok {
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
			if err := w.synthesize(tok.Token, tok.SegmentID); err != nil {
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
			w.flushSegmentPending(true)
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
		return
	}
	audio := cloneSynthesizedAudio(w.segmentPending)
	audio.IsFinal = isFinal
	w.segmentPending = nil
	w.observeSegmentAudio(audio, audio.DeltaText)
	w.eventCh <- audio
	if isFinal {
		w.emitSegmentMetrics(audio)
	}
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
	w.eventCh <- audio
	if isFinal {
		w.emitSegmentMetrics(audio)
	}
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
		return fmt.Errorf("stream closed")
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
		return fmt.Errorf("stream closed")
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
		return fmt.Errorf("stream closed")
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
