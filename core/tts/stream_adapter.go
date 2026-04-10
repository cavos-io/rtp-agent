package tts

import (
	"context"
	"fmt"

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
	adapter *StreamAdapter
	ctx     context.Context
	cancel  context.CancelFunc
	eventCh chan *SynthesizedAudio
	textCh  chan string
}

func (a *StreamAdapter) Stream(ctx context.Context) (SynthesizeStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	w := &streamAdapterWrapper{
		adapter: a,
		ctx:     ctx,
		cancel:  cancel,
		eventCh: make(chan *SynthesizedAudio, 100),
		textCh:  make(chan string, 100),
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
				return
			case text, ok := <-w.textCh:
				if !ok {
					tokenizer.Flush()
					tokenizer.Close()
					return
				}
				tokenizer.PushText(text)
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
			w.synthesize(tok.Token)
		}
	}
}

func (w *streamAdapterWrapper) synthesize(text string) {
	stream, err := w.adapter.tts.Synthesize(w.ctx, text)
	if err != nil {
		return
	}
	defer stream.Close()

	for {
		audio, err := stream.Next()
		if err != nil {
			break
		}
		w.eventCh <- audio
	}
}

func (w *streamAdapterWrapper) PushText(text string) error {
	w.textCh <- text
	return nil
}

func (w *streamAdapterWrapper) Flush() error {
	return nil
}

func (w *streamAdapterWrapper) Close() error {
	w.cancel()
	close(w.textCh)
	return nil
}

func (w *streamAdapterWrapper) Next() (*SynthesizedAudio, error) {
	ev, ok := <-w.eventCh
	if !ok {
		return nil, context.Canceled
	}
	return ev, nil
}
