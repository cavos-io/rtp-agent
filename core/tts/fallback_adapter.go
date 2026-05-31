package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/cavos-io/conversation-worker/library/logger"
)

type FallbackAdapter struct {
	ttss         []TTS
	capabilities TTSCapabilities
	sampleRate   int
	numChannels  int
}

func NewFallbackAdapter(ttss []TTS) *FallbackAdapter {
	if len(ttss) == 0 {
		panic("FallbackAdapter requires at least one TTS")
	}

	numChannels := ttss[0].NumChannels()
	capabilities := TTSCapabilities{AlignedTranscript: true}
	sampleRate := 0
	for _, tts := range ttss {
		if tts.NumChannels() != numChannels {
			panic("all TTS must have the same number of channels")
		}
		ttsCapabilities := tts.Capabilities()
		capabilities.Streaming = capabilities.Streaming || ttsCapabilities.Streaming
		capabilities.AlignedTranscript = capabilities.AlignedTranscript && ttsCapabilities.AlignedTranscript
		if tts.SampleRate() > sampleRate {
			sampleRate = tts.SampleRate()
		}
	}

	return &FallbackAdapter{
		ttss:         ttss,
		capabilities: capabilities,
		sampleRate:   sampleRate,
		numChannels:  numChannels,
	}
}

func (f *FallbackAdapter) Label() string {
	return fmt.Sprintf("FallbackAdapter(%s)", f.ttss[0].Label())
}

func (f *FallbackAdapter) Capabilities() TTSCapabilities {
	return f.capabilities
}

func (f *FallbackAdapter) SampleRate() int {
	return f.sampleRate
}

func (f *FallbackAdapter) NumChannels() int {
	return f.numChannels
}

type fallbackChunkedStream struct {
	adapter *FallbackAdapter
	ctx     context.Context
	text    string

	mu           sync.Mutex
	activeStream ChunkedStream
	activeIndex  int

	eventCh chan *SynthesizedAudio
	errCh   chan error
	closeCh chan struct{}
	closed  bool
}

func (f *FallbackAdapter) Synthesize(ctx context.Context, text string) (ChunkedStream, error) {
	s := &fallbackChunkedStream{
		adapter: f,
		ctx:     ctx,
		text:    text,
		eventCh: make(chan *SynthesizedAudio, 100),
		errCh:   make(chan error, 1),
		closeCh: make(chan struct{}),
	}

	if err := s.tryStartStream(0); err != nil {
		return nil, err
	}

	go s.monitorStream()

	return s, nil
}

func (s *fallbackChunkedStream) tryStartStream(index int) error {
	if index >= len(s.adapter.ttss) {
		return fmt.Errorf("all fallback TTS exhausted")
	}

	tts := s.adapter.ttss[index]
	stream, err := tts.Synthesize(s.ctx, s.text)
	if err != nil {
		logger.Logger.Errorw("Failed to start TTS synthesize stream", err, "tts", tts.Label())
		return s.tryStartStream(index + 1)
	}

	s.activeStream = stream
	s.activeIndex = index
	return nil
}

func (s *fallbackChunkedStream) monitorStream() {
	audioSent := false
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		stream := s.activeStream
		s.mu.Unlock()

		ev, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || audioSent {
				s.errCh <- io.EOF
				return
			}

			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}

			logger.Logger.Warnw("TTS synthesize stream failed, attempting fallback", err, "failed_tts", s.adapter.ttss[s.activeIndex].Label())
			stream.Close()

			if fbErr := s.tryStartStream(s.activeIndex + 1); fbErr != nil {
				s.errCh <- fbErr
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			continue
		}

		audioSent = true
		select {
		case s.eventCh <- ev:
		case <-s.closeCh:
			return
		}
	}
}

func (s *fallbackChunkedStream) Next() (*SynthesizedAudio, error) {
	select {
	case ev := <-s.eventCh:
		return ev, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.closeCh:
		return nil, context.Canceled
	}
}

func (s *fallbackChunkedStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.closeCh)
	return s.activeStream.Close()
}

type fallbackSynthesizeStream struct {
	adapter *FallbackAdapter
	ctx     context.Context

	mu           sync.Mutex
	activeStream SynthesizeStream
	activeIndex  int
	textBuffer   string

	eventCh chan *SynthesizedAudio
	errCh   chan error
	closeCh chan struct{}
	closed  bool
}

func (f *FallbackAdapter) Stream(ctx context.Context) (SynthesizeStream, error) {
	s := &fallbackSynthesizeStream{
		adapter: f,
		ctx:     ctx,
		eventCh: make(chan *SynthesizedAudio, 100),
		errCh:   make(chan error, 1),
		closeCh: make(chan struct{}),
	}

	if err := s.tryStartStream(0); err != nil {
		return nil, err
	}

	go s.monitorStream()

	return s, nil
}

func (s *fallbackSynthesizeStream) tryStartStream(index int) error {
	if index >= len(s.adapter.ttss) {
		return fmt.Errorf("all fallback TTS exhausted")
	}

	tts := s.adapter.ttss[index]
	var (
		stream SynthesizeStream
		err    error
	)
	if tts.Capabilities().Streaming {
		stream, err = tts.Stream(s.ctx)
	} else {
		stream, err = NewStreamAdapter(tts).Stream(s.ctx)
	}
	if err != nil {
		logger.Logger.Errorw("Failed to start TTS stream", err, "tts", tts.Label())
		return s.tryStartStream(index + 1)
	}

	s.activeStream = stream
	s.activeIndex = index

	if s.textBuffer != "" {
		if err := stream.PushText(s.textBuffer); err != nil {
			stream.Close()
			return s.tryStartStream(index + 1)
		}
	}

	return nil
}

func (s *fallbackSynthesizeStream) monitorStream() {
	audioSent := false
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		stream := s.activeStream
		s.mu.Unlock()

		ev, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || audioSent {
				s.errCh <- io.EOF
				return
			}

			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}

			logger.Logger.Warnw("TTS stream failed, attempting fallback", err, "failed_tts", s.adapter.ttss[s.activeIndex].Label())
			stream.Close()

			if fbErr := s.tryStartStream(s.activeIndex + 1); fbErr != nil {
				s.errCh <- fbErr
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			continue
		}

		audioSent = true
		select {
		case s.eventCh <- ev:
		case <-s.closeCh:
			return
		}
	}
}

func (s *fallbackSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	s.textBuffer += text
	return s.activeStream.PushText(text)
}

func (s *fallbackSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	return s.activeStream.Flush()
}

func (s *fallbackSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.closeCh)
	return s.activeStream.Close()
}

func (s *fallbackSynthesizeStream) Next() (*SynthesizedAudio, error) {
	select {
	case ev := <-s.eventCh:
		return ev, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.closeCh:
		return nil, context.Canceled
	}
}
