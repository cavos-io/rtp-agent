package upliftai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultUpliftAIVoiceID    = "v_meklc281"
	defaultUpliftAISampleRate = 22050
)

type UpliftAITTS struct {
	apiKey  string
	voice   string
	mu      sync.Mutex
	closed  bool
	streams map[io.Closer]struct{}
}

func NewUpliftAITTS(apiKey string, voice string) *UpliftAITTS {
	if apiKey == "" {
		apiKey = os.Getenv("UPLIFTAI_API_KEY")
	}
	if voice == "" {
		voice = defaultUpliftAIVoiceID
	}
	return &UpliftAITTS{
		apiKey:  apiKey,
		voice:   voice,
		streams: make(map[io.Closer]struct{}),
	}
}

func (t *UpliftAITTS) Label() string { return "upliftai.TTS" }
func (t *UpliftAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *UpliftAITTS) SampleRate() int  { return defaultUpliftAISampleRate }
func (t *UpliftAITTS) NumChannels() int { return 1 }

func (t *UpliftAITTS) UpdateOptions(voice string) {
	if voice == "" {
		return
	}
	t.mu.Lock()
	t.voice = voice
	t.mu.Unlock()
}

func (t *UpliftAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if t.apiKey == "" {
		return nil, fmt.Errorf("API key is required, either as argument or set UPLIFTAI_API_KEY environment variable")
	}

	stream := &upliftAITTSChunkedStream{
		owner: t,
		ctx:   ctx,
		text:  text,
	}
	if !t.registerStream(stream) {
		_ = stream.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *UpliftAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	stream := newUpliftAITTSSynthesizeStream(t, ctx)
	if !t.registerStream(stream) {
		_ = stream.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *UpliftAITTS) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	streams := make([]io.Closer, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[io.Closer]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *UpliftAITTS) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *UpliftAITTS) voiceSnapshot() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.voice
}

func (t *UpliftAITTS) registerStream(stream io.Closer) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.streams[stream] = struct{}{}
	return true
}

func (t *UpliftAITTS) unregisterStream(stream io.Closer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

type upliftAITTSSynthesizeStream struct {
	owner *UpliftAITTS
	ctx   context.Context

	mu        sync.Mutex
	buf       strings.Builder
	inputCh   chan string
	doneCh    chan struct{}
	active    tts.ChunkedStream
	closed    bool
	inputDone bool
	once      sync.Once
}

func newUpliftAITTSSynthesizeStream(owner *UpliftAITTS, ctx context.Context) *upliftAITTSSynthesizeStream {
	if ctx == nil {
		ctx = context.Background()
	}
	return &upliftAITTSSynthesizeStream{
		owner:   owner,
		ctx:     ctx,
		inputCh: make(chan string, 100),
		doneCh:  make(chan struct{}),
	}
}

func (s *upliftAITTSSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputDone {
		return io.ErrClosedPipe
	}
	s.buf.WriteString(text)
	return nil
}

func (s *upliftAITTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputDone {
		return nil
	}
	text := s.buf.String()
	if text == "" {
		return nil
	}
	s.buf.Reset()
	select {
	case s.inputCh <- text:
		return nil
	case <-s.doneCh:
		return io.ErrClosedPipe
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *upliftAITTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputDone {
		return nil
	}
	text := s.buf.String()
	s.buf.Reset()
	if text != "" {
		select {
		case s.inputCh <- text:
		case <-s.doneCh:
			return io.ErrClosedPipe
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
	s.inputDone = true
	close(s.inputCh)
	return nil
}

func (s *upliftAITTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, io.EOF
		}
		active := s.active
		s.mu.Unlock()

		if active != nil {
			audio, err := active.Next()
			if err == io.EOF {
				_ = active.Close()
				s.mu.Lock()
				if s.active == active {
					s.active = nil
				}
				s.mu.Unlock()
				continue
			}
			return audio, err
		}

		select {
		case text, ok := <-s.inputCh:
			if !ok {
				return nil, io.EOF
			}
			stream, err := s.owner.Synthesize(s.ctx, text)
			if err != nil {
				return nil, err
			}
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				_ = stream.Close()
				return nil, io.EOF
			}
			s.active = stream
			s.mu.Unlock()
		case <-s.doneCh:
			return nil, io.EOF
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
	}
}

func (s *upliftAITTSSynthesizeStream) Close() error {
	var closeErr error
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		if !s.inputDone {
			s.inputDone = true
			close(s.inputCh)
		}
		close(s.doneCh)
		active := s.active
		s.active = nil
		s.mu.Unlock()

		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
		if active != nil {
			closeErr = active.Close()
		}
	})
	return closeErr
}

type upliftAITTSChunkedStream struct {
	owner        *UpliftAITTS
	ctx          context.Context
	text         string
	resp         *http.Response
	once         sync.Once
	err          error
	pendingFinal bool
	finalSent    bool
	closed       bool
}

func (s *upliftAITTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed || s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	buf := make([]byte, 4096)
	for {
		n, err := s.resp.Body.Read(buf)
		if n > 0 {
			if err == io.EOF {
				s.pendingFinal = true
			}
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              buf[:n],
					SampleRate:        defaultUpliftAISampleRate,
					NumChannels:       1,
					SamplesPerChannel: uint32(n / 2),
				},
			}, nil
		}
		if err != nil {
			if err == io.EOF {
				if !s.finalSent {
					s.finalSent = true
					return &tts.SynthesizedAudio{IsFinal: true}, nil
				}
				return nil, io.EOF
			}
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS stream read failed: %v", err))
		}
	}
}

func (s *upliftAITTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.owner == nil {
		return nil
	}
	reqBody := map[string]interface{}{
		"text":  s.text,
		"voice": s.owner.voiceSnapshot(),
	}
	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(s.ctx, "POST", "https://api.upliftai.org/v1/tts", bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.owner.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS request failed: %v", err))
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("UpliftAI TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *upliftAITTSChunkedStream) Close() error {
	s.once.Do(func() {
		s.closed = true
		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
		if s.resp != nil && s.resp.Body != nil {
			s.err = s.resp.Body.Close()
		}
	})
	return s.err
}
