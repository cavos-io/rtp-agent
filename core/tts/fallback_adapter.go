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
	ttss           []TTS
	capabilities   TTSCapabilities
	sampleRate     int
	numChannels    int
	maxRetryPerTTS int

	mu     sync.Mutex
	status []fallbackTTSStatus
}

type fallbackTTSStatus struct {
	available  bool
	recovering bool
}

type FallbackAdapterOptions struct {
	MaxRetryPerTTS int
}

func NewFallbackAdapter(ttss []TTS) *FallbackAdapter {
	return NewFallbackAdapterWithOptions(ttss, FallbackAdapterOptions{MaxRetryPerTTS: 2})
}

func NewFallbackAdapterWithOptions(ttss []TTS, options FallbackAdapterOptions) *FallbackAdapter {
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

	status := make([]fallbackTTSStatus, len(ttss))
	for i := range status {
		status[i].available = true
	}

	return &FallbackAdapter{
		ttss:           ttss,
		capabilities:   capabilities,
		sampleRate:     sampleRate,
		numChannels:    numChannels,
		maxRetryPerTTS: options.MaxRetryPerTTS,
		status:         status,
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

func (f *FallbackAdapter) allUnavailableLocked() bool {
	for _, status := range f.status {
		if status.available {
			return false
		}
	}
	return true
}

func (f *FallbackAdapter) shouldTry(index int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[index].available || f.allUnavailableLocked()
}

func (f *FallbackAdapter) markUnavailable(index int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[index].available = false
}

func (f *FallbackAdapter) markAvailable(index int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[index].available = true
	f.status[index].recovering = false
}

func (f *FallbackAdapter) tryRecoverChunked(index int, text string) {
	f.mu.Lock()
	if f.status[index].recovering {
		f.mu.Unlock()
		return
	}
	f.status[index].recovering = true
	tts := f.ttss[index]
	f.mu.Unlock()

	go func() {
		stream, err := tts.Synthesize(context.Background(), text)
		if err != nil || stream == nil {
			f.mu.Lock()
			f.status[index].recovering = false
			f.mu.Unlock()
			return
		}
		defer stream.Close()

		for {
			_, err := stream.Next()
			if err == nil {
				continue
			}
			if errors.Is(err, io.EOF) {
				f.markAvailable(index)
				return
			}
			f.mu.Lock()
			f.status[index].recovering = false
			f.mu.Unlock()
			return
		}
	}()
}

func (f *FallbackAdapter) tryRecoverStream(index int, inputs []fallbackSynthesizeInput) {
	if len(inputs) == 0 {
		return
	}

	replay := append([]fallbackSynthesizeInput(nil), inputs...)

	f.mu.Lock()
	if f.status[index].recovering {
		f.mu.Unlock()
		return
	}
	f.status[index].recovering = true
	tts := f.ttss[index]
	f.mu.Unlock()

	go func() {
		stream, err := streamForTTS(context.Background(), tts)
		if err != nil || stream == nil {
			f.mu.Lock()
			f.status[index].recovering = false
			f.mu.Unlock()
			return
		}
		defer stream.Close()

		for _, input := range replay {
			if input.flush {
				if err := stream.Flush(); err != nil {
					f.mu.Lock()
					f.status[index].recovering = false
					f.mu.Unlock()
					return
				}
				continue
			}
			if err := stream.PushText(input.text); err != nil {
				f.mu.Lock()
				f.status[index].recovering = false
				f.mu.Unlock()
				return
			}
		}
		if !replay[len(replay)-1].flush {
			if err := stream.Flush(); err != nil {
				f.mu.Lock()
				f.status[index].recovering = false
				f.mu.Unlock()
				return
			}
		}

		for {
			_, err := stream.Next()
			if err == nil {
				continue
			}
			if errors.Is(err, io.EOF) {
				f.markAvailable(index)
				return
			}
			f.mu.Lock()
			f.status[index].recovering = false
			f.mu.Unlock()
			return
		}
	}()
}

func streamForTTS(ctx context.Context, tts TTS) (SynthesizeStream, error) {
	if tts.Capabilities().Streaming {
		return tts.Stream(ctx)
	}
	return NewStreamAdapter(tts).Stream(ctx)
}

type fallbackChunkedStream struct {
	adapter *FallbackAdapter
	ctx     context.Context
	text    string

	mu           sync.Mutex
	activeStream ChunkedStream
	activeIndex  int
	retries      map[int]int

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
		retries: make(map[int]int),
	}

	if err := s.tryStartStream(0); err != nil {
		return nil, err
	}

	go s.monitorStream()

	return s, nil
}

func (s *fallbackChunkedStream) tryStartStream(index int) error {
	var lastErr error
	for i := index; i < len(s.adapter.ttss); i++ {
		if !s.adapter.shouldTry(i) {
			continue
		}
		for {
			tts := s.adapter.ttss[i]
			stream, err := tts.Synthesize(s.ctx, s.text)
			if err == nil {
				s.activeStream = stream
				s.activeIndex = i
				return nil
			}
			logger.Logger.Errorw("Failed to start TTS synthesize stream", err, "tts", tts.Label())
			lastErr = err
			if !s.canRetryTTS(i) {
				s.adapter.markUnavailable(i)
				s.adapter.tryRecoverChunked(i, s.text)
				break
			}
			s.retries[i]++
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("all fallback TTS exhausted")
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

			nextIndex := s.activeIndex + 1
			if s.canRetryTTS(s.activeIndex) {
				s.retries[s.activeIndex]++
				nextIndex = s.activeIndex
			} else {
				s.adapter.markUnavailable(s.activeIndex)
				s.adapter.tryRecoverChunked(s.activeIndex, s.text)
			}

			if fbErr := s.tryStartStream(nextIndex); fbErr != nil {
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

func (s *fallbackChunkedStream) canRetryTTS(index int) bool {
	if s.adapter.maxRetryPerTTS <= 0 {
		return false
	}
	if s.retries == nil {
		s.retries = make(map[int]int)
	}
	return s.retries[index] < s.adapter.maxRetryPerTTS
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
	retries      map[int]int
	inputBuffer  []fallbackSynthesizeInput

	eventCh chan *SynthesizedAudio
	errCh   chan error
	closeCh chan struct{}
	closed  bool
}

type fallbackSynthesizeInput struct {
	text  string
	flush bool
}

func (f *FallbackAdapter) Stream(ctx context.Context) (SynthesizeStream, error) {
	s := &fallbackSynthesizeStream{
		adapter: f,
		ctx:     ctx,
		eventCh: make(chan *SynthesizedAudio, 100),
		errCh:   make(chan error, 1),
		closeCh: make(chan struct{}),
		retries: make(map[int]int),
	}

	if err := s.tryStartStream(0); err != nil {
		return nil, err
	}

	go s.monitorStream()

	return s, nil
}

func (s *fallbackSynthesizeStream) tryStartStream(index int) error {
	var lastErr error
	for i := index; i < len(s.adapter.ttss); i++ {
		if !s.adapter.shouldTry(i) {
			continue
		}
		for {
			tts := s.adapter.ttss[i]
			stream, err := s.startProviderStream(tts)
			if err != nil {
				logger.Logger.Errorw("Failed to start TTS stream", err, "tts", tts.Label())
				lastErr = err
				if s.canRetryTTS(i) {
					s.retries[i]++
					continue
				}
				s.adapter.markUnavailable(i)
				s.adapter.tryRecoverStream(i, s.inputBuffer)
				break
			}

			if err := s.replayBufferedText(stream); err != nil {
				stream.Close()
				lastErr = err
				if s.canRetryTTS(i) {
					s.retries[i]++
					continue
				}
				s.adapter.markUnavailable(i)
				s.adapter.tryRecoverStream(i, s.inputBuffer)
				break
			}

			s.activeStream = stream
			s.activeIndex = i
			return nil
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("all fallback TTS exhausted")
}

func (s *fallbackSynthesizeStream) startProviderStream(tts TTS) (SynthesizeStream, error) {
	return streamForTTS(s.ctx, tts)
}

func (s *fallbackSynthesizeStream) replayBufferedText(stream SynthesizeStream) error {
	for _, input := range s.inputBuffer {
		if input.flush {
			if err := stream.Flush(); err != nil {
				return err
			}
			continue
		}
		if err := stream.PushText(input.text); err != nil {
			return err
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

			nextIndex := s.activeIndex + 1
			if s.canRetryTTS(s.activeIndex) {
				s.retries[s.activeIndex]++
				nextIndex = s.activeIndex
			} else {
				s.adapter.markUnavailable(s.activeIndex)
				s.adapter.tryRecoverStream(s.activeIndex, s.inputBuffer)
			}

			if fbErr := s.tryStartStream(nextIndex); fbErr != nil {
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

func (s *fallbackSynthesizeStream) canRetryTTS(index int) bool {
	if s.adapter.maxRetryPerTTS <= 0 {
		return false
	}
	if s.retries == nil {
		s.retries = make(map[int]int)
	}
	return s.retries[index] < s.adapter.maxRetryPerTTS
}

func (s *fallbackSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	s.inputBuffer = append(s.inputBuffer, fallbackSynthesizeInput{text: text})
	return s.activeStream.PushText(text)
}

func (s *fallbackSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	s.inputBuffer = append(s.inputBuffer, fallbackSynthesizeInput{flush: true})
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
