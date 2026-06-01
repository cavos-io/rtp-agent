package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/tokenize"
)

type StreamAdapter struct {
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
	mu        sync.Mutex
	active    ChunkedStream
	closed    bool
	inputDone bool
	started   bool

	segmentPending *SynthesizedAudio
}

type streamAdapterInput struct {
	text  string
	flush bool
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
	}

	go w.run()
	return w, nil
}

func (w *streamAdapterWrapper) run() {
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
				w.sendErr(err)
				return
			}
		}
		w.flushCompletedSegments()
	}
	w.flushSegmentPending(true)
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
	w.eventCh <- audio
}

func (w *streamAdapterWrapper) sendSynthesizedAudio(audio *SynthesizedAudio, text string, segmentID string, isFinal bool, includeTranscript bool) {
	audio = cloneSynthesizedAudio(audio)
	audio.SegmentID = segmentID
	audio.RequestID = w.requestID
	audio.IsFinal = isFinal
	if includeTranscript {
		audio.DeltaText = text
	}
	w.eventCh <- audio
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
	if active != nil {
		return active.Close()
	}
	return nil
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
