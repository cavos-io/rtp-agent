package tts

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/tokenize"
)

// SentenceStreamPacer buffers text from the LLM, groups it into sentences,
// and only forwards sentences to the underlying TTS stream when the synthesized
// audio buffer falls below a certain duration threshold.
// This prevents runaway TTS generation costs if the user interrupts early.
type SentenceStreamPacer struct {
	underlying          SynthesizeStream
	tokenizer           tokenize.SentenceStream
	minRemainingAudio   time.Duration

	ctx                 context.Context
	cancel              context.CancelFunc

	textCh              chan string
	sentenceCh          chan string
	audioCh             chan *SynthesizedAudio

	mu                  sync.Mutex
	yieldedAudioTime    time.Duration
	playbackStartTime   time.Time
	playbackStarted     bool

	closed              bool
}

func NewSentenceStreamPacer(ctx context.Context, underlying SynthesizeStream, minRemainingAudio time.Duration) *SentenceStreamPacer {
	if minRemainingAudio == 0 {
		minRemainingAudio = 5 * time.Second
	}

	pacerCtx, cancel := context.WithCancel(ctx)
	p := &SentenceStreamPacer{
		underlying:        underlying,
		tokenizer:         tokenize.NewBasicSentenceTokenizer().Stream("en"),
		minRemainingAudio: minRemainingAudio,
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
	defer p.tokenizer.Close()

	for {
		select {
		case <-p.ctx.Done():
			return
		case text, ok := <-p.textCh:
			if !ok {
				p.tokenizer.Flush()
				for {
					tok, err := p.tokenizer.Next()
					if err != nil {
						break
					}
					if tok.Token != "" {
						p.sentenceCh <- tok.Token
					}
				}
				return
			}

			p.tokenizer.PushText(text)
			for {
				tok, err := p.tokenizer.Next()
				if err != nil {
					break
				}
				if tok.Token != "" {
					p.sentenceCh <- tok.Token
				}
			}
		}
	}
}

func (p *SentenceStreamPacer) paceLoop() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case sentence, ok := <-p.sentenceCh:
			if !ok {
				p.underlying.Flush()
				return
			}

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

			logger.Logger.Debugw("Pacer pushing sentence to TTS", "sentence", sentence)
			if err := p.underlying.PushText(sentence); err != nil {
				return
			}
		}
	}
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

