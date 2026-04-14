package tts

import (
	"context"
	"strings"
	"sync"
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
		adapter: s,
		ctx:     ctx,
		events:  make(chan *SynthesizedAudio, 100),
	}
	return wrapper, nil
}

type streamAdapterWrapper struct {
	adapter *StreamAdapter
	ctx     context.Context

	textBuffer string
	mu         sync.Mutex

	events chan *SynthesizedAudio
	closed bool
}

func (w *streamAdapterWrapper) PushText(text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.textBuffer += text

	if strings.ContainsAny(text, ".!?\n") {
		sentences := splitSentences(w.textBuffer)
		if len(sentences) > 0 {
			lastChar := w.textBuffer[len(w.textBuffer)-1]
			hasPunctuation := lastChar == '.' || lastChar == '!' || lastChar == '?' || lastChar == '\n'

			if !hasPunctuation {
				w.textBuffer = sentences[len(sentences)-1]
				sentences = sentences[:len(sentences)-1]
			} else {
				w.textBuffer = ""
			}

			for _, sentence := range sentences {
				sentence = strings.TrimSpace(sentence)
				if sentence != "" {
					go w.synthesize(sentence)
				}
			}
		}
	}
	return nil
}

func (w *streamAdapterWrapper) synthesize(text string) {
	chunked, err := w.adapter.tts.Synthesize(w.ctx, text)
	if err != nil {
		return
	}
	defer chunked.Close()

	for {
		audio, err := chunked.Next()
		if err != nil || audio == nil {
			break
		}
		w.events <- audio
	}
}

func (w *streamAdapterWrapper) Flush() error {
	w.mu.Lock()
	text := strings.TrimSpace(w.textBuffer)
	w.textBuffer = ""
	w.mu.Unlock()

	if text != "" {
		w.synthesize(text)
	}
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
	close(w.events)
	return nil
}

func (w *streamAdapterWrapper) Next() (*SynthesizedAudio, error) {
	select {
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case ev, ok := <-w.events:
		if !ok {
			return nil, context.Canceled
		}
		return ev, nil
	}
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
