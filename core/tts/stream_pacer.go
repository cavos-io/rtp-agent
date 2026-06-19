package tts

import (
	"context"
	"fmt"
	"io"
	"strings"
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
	underlying        SynthesizeStream
	tokenizer         tokenize.SentenceTokenizer
	minRemainingAudio time.Duration
	maxTextLength     int

	ctx    context.Context
	cancel context.CancelFunc

	textCh     chan pacerInput
	sentenceCh chan pacerSentence
	audioCh    chan *SynthesizedAudio

	sendMu            sync.Mutex
	wg                sync.WaitGroup
	mu                sync.Mutex
	yieldedAudioTime  time.Duration
	playbackStartTime time.Time
	playbackStarted   bool
	generationStarted bool
	lastAudioTime     time.Time
	audioErr          error

	closed              bool
	inputDone           bool
	underlyingInputDone bool
}

type pacerInput struct {
	text  string
	flush bool
}

type pacerSentence struct {
	text  string
	flush bool
}

type SentenceStreamPacerOptions struct {
	MinRemainingAudio    time.Duration
	MinRemainingAudioSet bool
	MaxTextLength        int
	MaxTextLengthSet     bool
}

func NewSentenceStreamPacerWithOptions(ctx context.Context, underlying SynthesizeStream, opts SentenceStreamPacerOptions) *SentenceStreamPacer {
	if opts.MinRemainingAudio == 0 && !opts.MinRemainingAudioSet {
		opts.MinRemainingAudio = 5 * time.Second
	}
	if opts.MaxTextLength == 0 && !opts.MaxTextLengthSet {
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
		textCh:            make(chan pacerInput, 100),
		sentenceCh:        make(chan pacerSentence, 100),
		audioCh:           make(chan *SynthesizedAudio, 100),
	}

	p.wg.Add(3)
	go p.tokenizeLoop()
	go p.paceLoop()
	go p.audioLoop()

	return p
}

func (p *SentenceStreamPacer) tokenizeLoop() {
	defer p.wg.Done()
	defer close(p.sentenceCh)

	var buffer string
	for {
		select {
		case <-p.ctx.Done():
			return
		case input, ok := <-p.textCh:
			if !ok {
				p.sendTokenizedSentences(buffer)
				return
			}
			if input.flush {
				p.sendTokenizedSentences(buffer)
				buffer = ""
				select {
				case <-p.ctx.Done():
					return
				case p.sentenceCh <- pacerSentence{flush: true}:
				}
				continue
			}

			text := input.text
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
			select {
			case <-p.ctx.Done():
				return false
			case p.sentenceCh <- pacerSentence{text: sentence}:
			}
		}
	}
	return true
}

func (p *SentenceStreamPacer) paceLoop() {
	defer p.wg.Done()

	var pending []string
	firstSentence := true

	for {
		select {
		case <-p.ctx.Done():
			return
		case sentence, ok := <-p.sentenceCh:
			if !ok {
				p.flushPendingSentences(&pending, &firstSentence, p.isInputDone())
				return
			}
			if sentence.flush {
				p.flushPendingSentences(&pending, &firstSentence, false)
				continue
			}
			pending = append(pending, sentence.text)
			if p.drainAvailableSentences(&pending) {
				p.flushPendingSentences(&pending, &firstSentence, p.isInputDone())
				continue
			}

			if !p.waitUntilReadyToSend(firstSentence) {
				return
			}

			if err := p.pushBatch(&pending, &firstSentence); err != nil {
				return
			}
		}
	}
}

func (p *SentenceStreamPacer) waitUntilReadyToSend(firstSentence bool) bool {
	if firstSentence {
		return true
	}

	for {
		p.mu.Lock()
		remaining := p.remainingAudioLocked()
		generationStopped := p.generationStarted && !p.lastAudioTime.IsZero() && time.Since(p.lastAudioTime) >= 100*time.Millisecond
		p.mu.Unlock()

		if generationStopped && remaining <= p.minRemainingAudio {
			return true
		}

		select {
		case <-p.ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (p *SentenceStreamPacer) remainingAudioLocked() time.Duration {
	if !p.playbackStarted {
		return 0
	}
	elapsed := time.Since(p.playbackStartTime)
	if p.yieldedAudioTime > elapsed {
		return p.yieldedAudioTime - elapsed
	}
	return 0
}

func (p *SentenceStreamPacer) flushPendingSentences(pending *[]string, firstSentence *bool, endInput bool) {
	for len(*pending) > 0 {
		if !p.waitUntilReadyToSend(*firstSentence) {
			return
		}
		if err := p.pushBatch(pending, firstSentence); err != nil {
			return
		}
	}
	if endInput {
		p.endUnderlyingInput()
		return
	}
	p.underlying.Flush()
}

func (p *SentenceStreamPacer) drainAvailableSentences(pending *[]string) bool {
	for {
		select {
		case sentence, ok := <-p.sentenceCh:
			if !ok {
				return true
			}
			if sentence.flush {
				return true
			}
			*pending = append(*pending, sentence.text)
		default:
			return false
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
	p.mu.Lock()
	p.generationStarted = false
	p.lastAudioTime = time.Time{}
	p.mu.Unlock()
	return p.underlying.PushText(text)
}

func (p *SentenceStreamPacer) audioLoop() {
	defer p.wg.Done()
	defer close(p.audioCh)

	for {
		audio, err := p.underlying.Next()
		if err != nil {
			p.mu.Lock()
			p.audioErr = err
			p.mu.Unlock()
			return
		}
		if audio == nil || audio.Frame == nil {
			continue
		}

		p.mu.Lock()
		p.generationStarted = true
		p.lastAudioTime = time.Now()
		if !p.playbackStarted {
			p.playbackStarted = true
			p.playbackStartTime = p.lastAudioTime
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
	p.sendMu.Lock()
	defer p.sendMu.Unlock()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("pacer closed")
	}
	if p.inputDone {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	p.textCh <- pacerInput{text: text}
	return nil
}

func (p *SentenceStreamPacer) Flush() error {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("pacer closed")
	}
	if p.inputDone {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	p.textCh <- pacerInput{flush: true}
	return nil
}

func (p *SentenceStreamPacer) EndInput() error {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("pacer closed")
	}
	if p.inputDone {
		p.mu.Unlock()
		return nil
	}
	p.inputDone = true
	close(p.textCh)
	p.mu.Unlock()
	return nil
}

func (p *SentenceStreamPacer) Close() error {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	if !p.inputDone {
		p.inputDone = true
		close(p.textCh)
	}
	p.mu.Unlock()
	p.cancel()
	err := p.underlying.Close()
	p.wg.Wait()
	return err
}

func (p *SentenceStreamPacer) isInputDone() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inputDone
}

func (p *SentenceStreamPacer) endUnderlyingInput() {
	p.mu.Lock()
	if p.underlyingInputDone {
		p.mu.Unlock()
		return
	}
	p.underlyingInputDone = true
	p.mu.Unlock()

	_ = endSynthesizeStreamInput(p.underlying)
}

func (p *SentenceStreamPacer) Next() (*SynthesizedAudio, error) {
	select {
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	case audio, ok := <-p.audioCh:
		if !ok {
			p.mu.Lock()
			err := p.audioErr
			p.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		return audio, nil
	}
}
