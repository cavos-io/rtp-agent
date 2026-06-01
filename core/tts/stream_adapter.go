package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	cavosmath "github.com/cavos-io/conversation-worker/library/math"
	"github.com/cavos-io/conversation-worker/library/tokenize"
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
	mu        sync.Mutex
	active    ChunkedStream
	closed    bool
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
	}

	go w.run()
	return w, nil
}

func (w *streamAdapterWrapper) run() {
	defer close(w.eventCh)

	tokenizer := tokenize.NewBasicSentenceTokenizer().Stream("en")

	// Stream text to tokenizer
	go func() {
		for {
			select {
			case <-w.ctx.Done():
				tokenizer.Flush()
				tokenizer.Close()
				return
			case input, ok := <-w.inputCh:
				if !ok {
					tokenizer.Flush()
					tokenizer.Close()
					return
				}
				if input.flush {
					tokenizer.Flush()
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
	}
}

func (w *streamAdapterWrapper) synthesize(text string, segmentID string) error {
	stream, err := w.adapter.tts.Synthesize(w.ctx, text)
	if err != nil {
		return err
	}
	w.setActiveStream(stream)
	defer func() {
		w.clearActiveStream(stream)
		stream.Close()
	}()

	var pending *SynthesizedAudio
	transcriptPending := true
	for {
		audio, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if pending != nil {
					pending = cloneSynthesizedAudio(pending)
					pending.SegmentID = segmentID
					pending.RequestID = w.requestID
					if transcriptPending {
						pending.DeltaText = text
					}
					pending.IsFinal = true
					w.eventCh <- pending
				} else if strings.TrimSpace(text) != "" {
					return fmt.Errorf("no audio frames were pushed for text: %s", text)
				}
				return nil
			}
			return err
		}
		if pending != nil {
			pending = cloneSynthesizedAudio(pending)
			pending.SegmentID = segmentID
			pending.RequestID = w.requestID
			if transcriptPending {
				pending.DeltaText = text
				transcriptPending = false
			}
			pending.IsFinal = false
			w.eventCh <- pending
		}
		pending = audio
	}
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

func (w *streamAdapterWrapper) PushText(text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("stream closed")
	}
	w.inputCh <- streamAdapterInput{text: text}
	return nil
}

func (w *streamAdapterWrapper) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("stream closed")
	}
	w.inputCh <- streamAdapterInput{flush: true}
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
	close(w.inputCh)
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
