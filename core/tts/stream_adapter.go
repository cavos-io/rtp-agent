package tts

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/cavos-io/conversation-worker/library/audio"
)

type StreamAdapter struct {
	tts TTS
}

func NewStreamAdapter(tts TTS) *StreamAdapter {
	return &StreamAdapter{tts: tts}
}

func (s *StreamAdapter) Label() string {
	return "stream_adapter(" + s.tts.Label() + ")"
}

func (s *StreamAdapter) Capabilities() TTSCapabilities {
	return TTSCapabilities{
		Streaming:         true,
		AlignedTranscript: false,
	}
}

func (s *StreamAdapter) SampleRate() int {
	return s.tts.SampleRate()
}

func (s *StreamAdapter) NumChannels() int {
	return s.tts.NumChannels()
}

func (s *StreamAdapter) Synthesize(ctx context.Context, text string) (ChunkedStream, error) {
	return s.tts.Synthesize(ctx, text)
}

func (s *StreamAdapter) Stream(ctx context.Context) (SynthesizeStream, error) {
	wrapper := &streamAdapterWrapper{
		adapter:    s,
		ctx:        ctx,
		events:     make(chan *SynthesizedAudio, 100),
		synthesize: make(chan string, 32),
		done:       make(chan struct{}),
	}
	go wrapper.run()
	return wrapper, nil
}

type streamAdapterWrapper struct {
	adapter *StreamAdapter
	ctx     context.Context

	textBuffer string
	mu         sync.Mutex

	events     chan *SynthesizedAudio
	synthesize chan string
	done       chan struct{}
	closeOnce  sync.Once
	closed     bool
	flushed    bool
}

func (w *streamAdapterWrapper) PushText(text string) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return context.Canceled
	}
	if w.flushed {
		w.mu.Unlock()
		return io.ErrClosedPipe
	}

	w.textBuffer += text
	ready := w.collectReadySentencesLocked(false)
	w.mu.Unlock()

	for _, sentence := range ready {
		if err := w.enqueue(sentence); err != nil {
			return err
		}
	}
	return nil
}

func (w *streamAdapterWrapper) Flush() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return context.Canceled
	}
	if w.flushed {
		w.mu.Unlock()
		return nil
	}

	ready := w.collectReadySentencesLocked(true)
	w.flushed = true
	w.mu.Unlock()

	for _, sentence := range ready {
		if err := w.enqueue(sentence); err != nil {
			return err
		}
	}

	w.closeSynthesisChannel()
	return nil
}

func (w *streamAdapterWrapper) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()
	w.closeSynthesisChannel()
	return nil
}

func (w *streamAdapterWrapper) Next() (*SynthesizedAudio, error) {
	select {
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case ev, ok := <-w.events:
		if !ok {
			return nil, io.EOF
		}
		return ev, nil
	}
}

func (w *streamAdapterWrapper) run() {
	defer close(w.done)
	defer close(w.events)

	for {
		select {
		case <-w.ctx.Done():
			return
		case text, ok := <-w.synthesize:
			if !ok {
				return
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			w.synthesizeOne(text)
		}
	}
}

func (w *streamAdapterWrapper) synthesizeOne(text string) {
	chunked, err := w.adapter.tts.Synthesize(w.ctx, text)
	if err != nil {
		return
	}
	defer chunked.Close()

	sourceRate := w.adapter.tts.SampleRate()
	// Target rate is usually 48000 for LiveKit agents, but we can potentially
	// pull this from the output transport if needed. For now default to 48k parity.
	targetRate := 48000 

	for {
		audioFrame, err := chunked.Next()
		if err != nil || audioFrame == nil {
			return
		}

		if audioFrame.Frame != nil && sourceRate != targetRate {
			// Resample data
			resampled := audio.Resample(audioFrame.Frame.Data, sourceRate, targetRate)
			audioFrame.Frame.Data = resampled
			audioFrame.Frame.SampleRate = uint32(targetRate)
			audioFrame.Frame.SamplesPerChannel = uint32(len(resampled) / 2)
		}

		select {
		case <-w.ctx.Done():
			return
		case w.events <- audioFrame:
		}
	}
}

func (w *streamAdapterWrapper) enqueue(sentence string) error {
	trimmed := strings.TrimSpace(sentence)
	if trimmed == "" {
		return nil
	}

	select {
	case <-w.ctx.Done():
		return w.ctx.Err()
	case w.synthesize <- trimmed:
		return nil
	}
}

func (w *streamAdapterWrapper) closeSynthesisChannel() {
	w.closeOnce.Do(func() {
		close(w.synthesize)
	})
}

func (w *streamAdapterWrapper) collectReadySentencesLocked(flush bool) []string {
	if w.textBuffer == "" {
		return nil
	}

	sentences := splitSentences(w.textBuffer)
	if len(sentences) == 0 {
		return nil
	}

	if flush {
		w.textBuffer = ""
		return trimNonEmpty(sentences)
	}

	lastChar := w.textBuffer[len(w.textBuffer)-1]
	hasPunctuation := lastChar == '.' || lastChar == '!' || lastChar == '?' || lastChar == '\n'
	if !hasPunctuation {
		w.textBuffer = sentences[len(sentences)-1]
		sentences = sentences[:len(sentences)-1]
	} else {
		w.textBuffer = ""
	}

	return trimNonEmpty(sentences)
}

func trimNonEmpty(sentences []string) []string {
	out := make([]string, 0, len(sentences))
	for _, sentence := range sentences {
		trimmed := strings.TrimSpace(sentence)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder
	for _, char := range text {
		current.WriteRune(char)
		if char == '.' || char == '!' || char == '?' || char == '\n' {
			sentences = append(sentences, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		sentences = append(sentences, current.String())
	}
	return sentences
}
