package tts

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/library/tokenize"
)

// SentenceStreamPacer buffers text from the LLM, groups it into sentences,
// and only forwards sentences to the underlying TTS stream when the synthesized
// audio buffer falls below a certain duration threshold.
// This prevents runaway TTS generation costs if the user interrupts early.
type SentenceStreamPacer struct {
	underlying        SynthesizeStream
	tokenizer         tokenize.SentenceTokenizer
	minRemainingAudio time.Duration
	maxTextLength     int

	ctx    context.Context
	cancel context.CancelFunc

	textCh     chan string
	sentenceCh chan string
	audioCh    chan *SynthesizedAudio

	mu                sync.Mutex
	yieldedAudioTime  time.Duration
	playbackStartTime time.Time
	playbackStarted   bool

	closed bool
}

type SentenceStreamPacerOptions struct {
	MinRemainingAudio time.Duration
	MaxTextLength     int
}

func NewSentenceStreamPacer(ctx context.Context, underlying SynthesizeStream, minRemainingAudio time.Duration) *SentenceStreamPacer {
	return NewSentenceStreamPacerWithOptions(ctx, underlying, SentenceStreamPacerOptions{
		MinRemainingAudio: minRemainingAudio,
	})
}

func NewSentenceStreamPacerWithOptions(ctx context.Context, underlying SynthesizeStream, opts SentenceStreamPacerOptions) *SentenceStreamPacer {
	if opts.MinRemainingAudio == 0 {
		opts.MinRemainingAudio = 5 * time.Second
	}
	if opts.MaxTextLength == 0 {
		opts.MaxTextLength = 300
	}

	pacerCtx, cancel := context.WithCancel(ctx)
	p := &SentenceStreamPacer{
		underlying:        underlying,
		tokenizer:         tokenize.NewBasicSentenceTokenizer(),
		minRemainingAudio: opts.MinRemainingAudio,
		maxTextLength:     opts.MaxTextLength,
		ctx:               pacerCtx,
		cancel:            cancel,
		textCh:            make(chan string, 100),
		sentenceCh:        make(chan string, 100),
		audioCh:           make(chan *SynthesizedAudio, 100),
	}

	go p.tokenizeLoop()
	go p.paceLoop()
	go p.audioLoop()

	return p
}

func (p *SentenceStreamPacer) tokenizeLoop() {
	defer close(p.sentenceCh)

	var buffer string
	for {
		select {
		case <-p.ctx.Done():
			return
		case text, ok := <-p.textCh:
			if !ok {
				p.sendTokenizedSentences(buffer)
				return
			}

			buffer += text
			if len(buffer) < 10 {
				continue
			}
			if p.sendTokenizedSentences(buffer) {
				buffer = ""
			}
		}
	}
}

func (p *SentenceStreamPacer) sendTokenizedSentences(text string) bool {
	sentences := p.tokenizer.Tokenize(text, "en")
	if len(sentences) == 0 {
		return false
	}
	for _, sentence := range sentences {
		if sentence != "" {
			p.sentenceCh <- sentence
		}
	}
	return true
}

func (p *SentenceStreamPacer) paceLoop() {
	var pending []string
	firstSentence := true

	for {
		select {
		case <-p.ctx.Done():
			return
		case sentence, ok := <-p.sentenceCh:
			if !ok {
				for len(pending) > 0 {
					if err := p.pushBatch(&pending, &firstSentence); err != nil {
						return
					}
				}
				p.underlying.Flush()
				return
			}
			pending = append(pending, sentence)
			p.drainAvailableSentences(&pending)

			// Wait until buffer is low enough
			for {
				p.mu.Lock()
				var remaining time.Duration
				if p.playbackStarted {
					elapsed := time.Since(p.playbackStartTime)
					if p.yieldedAudioTime > elapsed {
						remaining = p.yieldedAudioTime - elapsed
					}
				}
				p.mu.Unlock()

				if remaining <= p.minRemainingAudio {
					break
				}

				// Sleep a bit before checking again
				select {
				case <-p.ctx.Done():
					return
				case <-time.After(200 * time.Millisecond):
				}
			}

			if err := p.pushBatch(&pending, &firstSentence); err != nil {
				return
			}
		}
	}
}

func (p *SentenceStreamPacer) drainAvailableSentences(pending *[]string) {
	for {
		select {
		case sentence, ok := <-p.sentenceCh:
			if !ok {
				return
			}
			*pending = append(*pending, sentence)
		default:
			return
		}
	}
}

func (p *SentenceStreamPacer) pushBatch(pending *[]string, firstSentence *bool) error {
	if len(*pending) == 0 {
		return nil
	}

	batch := []string{(*pending)[0]}
	*pending = (*pending)[1:]
	textLen := len(batch[0])

	if *firstSentence {
		*firstSentence = false
	} else {
		for len(*pending) > 0 && textLen < p.maxTextLength {
			next := (*pending)[0]
			*pending = (*pending)[1:]
			batch = append(batch, next)
			textLen += len(next)
		}
	}

	text := strings.Join(batch, " ")
	logger.Logger.Debugw("Pacer pushing text to TTS", "text", text)
	return p.underlying.PushText(text)
}

func (p *SentenceStreamPacer) audioLoop() {
	defer close(p.audioCh)

	for {
		audio, err := p.underlying.Next()
		if err != nil {
			return
		}

		p.mu.Lock()
		if !p.playbackStarted {
			p.playbackStarted = true
			p.playbackStartTime = time.Now()
		}

		if audio.Frame != nil && audio.Frame.SampleRate > 0 {
			duration := time.Duration(float64(audio.Frame.SamplesPerChannel)/float64(audio.Frame.SampleRate)*1000.0) * time.Millisecond
			p.yieldedAudioTime += duration
		}
		p.mu.Unlock()

		select {
		case <-p.ctx.Done():
			return
		case p.audioCh <- audio:
		}
	}
}

func (p *SentenceStreamPacer) PushText(text string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("pacer closed")
	}
	p.mu.Unlock()

	p.textCh <- text
	return nil
}

func (p *SentenceStreamPacer) Flush() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("pacer closed")
	}
	p.closed = true
	p.mu.Unlock()

	close(p.textCh)
	return nil
}

func (p *SentenceStreamPacer) Close() error {
	p.cancel()
	return p.underlying.Close()
}

func (p *SentenceStreamPacer) Next() (*SynthesizedAudio, error) {
	select {
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	case audio, ok := <-p.audioCh:
		if !ok {
			return nil, fmt.Errorf("stream closed")
		}
		return audio, nil
	}
}
