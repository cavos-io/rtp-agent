package upliftai

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultUpliftAIVoiceID    = "v_meklc281"
	defaultUpliftAISampleRate = 22050
	defaultUpliftAIFormat     = "MP3_22050_32"
	defaultUpliftAIBaseURL    = "https://api.upliftai.org/v1/tts"
)

type UpliftAITTS struct {
	apiKey                    string
	voice                     string
	outputFormat              string
	baseURL                   string
	phraseReplacementConfigID string
	mu                        sync.Mutex
	closed                    bool
	streams                   map[io.Closer]struct{}
}

type UpliftAITTSOption func(*UpliftAITTS)
type UpliftAITTSUpdateOption func(*upliftAITTSUpdateOptions)

type upliftAITTSUpdateOptions struct {
	voiceID      *string
	outputFormat *string
}

func WithUpliftAIBaseURL(baseURL string) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		t.baseURL = baseURL
	}
}

func WithUpliftAIOutputFormat(outputFormat string) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		t.outputFormat = outputFormat
	}
}

func WithUpliftAIPhraseReplacementConfigID(configID string) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		t.phraseReplacementConfigID = configID
	}
}

func WithUpliftAIUpdateVoiceID(voiceID string) UpliftAITTSUpdateOption {
	return func(opts *upliftAITTSUpdateOptions) {
		opts.voiceID = &voiceID
	}
}

func WithUpliftAIUpdateOutputFormat(outputFormat string) UpliftAITTSUpdateOption {
	return func(opts *upliftAITTSUpdateOptions) {
		opts.outputFormat = &outputFormat
	}
}

func NewUpliftAITTS(apiKey string, voice string, opts ...UpliftAITTSOption) *UpliftAITTS {
	if apiKey == "" {
		apiKey = os.Getenv("UPLIFTAI_API_KEY")
	}
	if voice == "" {
		voice = defaultUpliftAIVoiceID
	}
	baseURL := os.Getenv("UPLIFTAI_BASE_URL")
	if baseURL == "" {
		baseURL = defaultUpliftAIBaseURL
	}
	tts := &UpliftAITTS{
		apiKey:       apiKey,
		voice:        voice,
		outputFormat: defaultUpliftAIFormat,
		baseURL:      baseURL,
		streams:      make(map[io.Closer]struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(tts)
		}
	}
	return tts
}

func (t *UpliftAITTS) Label() string { return "upliftai.TTS" }
func (t *UpliftAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *UpliftAITTS) SampleRate() int  { return defaultUpliftAISampleRate }
func (t *UpliftAITTS) NumChannels() int { return 1 }

func (t *UpliftAITTS) UpdateOptions(opts ...UpliftAITTSUpdateOption) {
	var update upliftAITTSUpdateOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&update)
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if update.voiceID != nil {
		t.voice = *update.voiceID
	}
	if update.outputFormat != nil {
		t.outputFormat = *update.outputFormat
	}
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

func (t *UpliftAITTS) requestOptions() (baseURL string, voiceID string, outputFormat string, phraseReplacementConfigID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.baseURL, t.voice, t.outputFormat, t.phraseReplacementConfigID
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
	decoder      codecs.AudioStreamDecoder
	outputFormat string
	started      bool
	hasAudio     bool
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
	if strings.HasPrefix(s.currentOutputFormat(), "MP3") {
		return s.nextDecodedMP3()
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
	baseURL, voiceID, outputFormat, phraseReplacementConfigID := s.owner.requestOptions()
	s.outputFormat = outputFormat
	reqBody := map[string]interface{}{
		"text":         s.text,
		"voiceId":      voiceID,
		"outputFormat": outputFormat,
	}
	if phraseReplacementConfigID != "" {
		reqBody["phraseReplacementConfigId"] = phraseReplacementConfigID
	}
	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(s.ctx, "POST", baseURL, bytes.NewBuffer(jsonBody))
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

func (s *upliftAITTSChunkedStream) currentOutputFormat() string {
	if s.outputFormat != "" {
		return s.outputFormat
	}
	if s.owner == nil {
		return ""
	}
	_, _, outputFormat, _ := s.owner.requestOptions()
	return outputFormat
}

func (s *upliftAITTSChunkedStream) nextDecodedMP3() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS MP3 read failed: %v", err))
		}
		if len(data) == 0 {
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		s.hasAudio = true
		decoder := codecs.NewMP3AudioStreamDecoder()
		s.decoder = decoder
		go func() {
			decoder.Push(data)
			decoder.EndInput()
		}()
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			if s.hasAudio && !s.finalSent {
				s.finalSent = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, io.EOF
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS MP3 decode failed: %v", err))
	}
	frame = upliftAIDownmixToMono(frame)
	if frame.SampleRate != defaultUpliftAISampleRate {
		resampled, err := coreaudio.ResampleAudioFrame(frame, defaultUpliftAISampleRate)
		if err != nil {
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS MP3 resample failed: %v", err))
		}
		frame = resampled
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func upliftAIDownmixToMono(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil || frame.NumChannels <= 1 {
		return frame
	}
	channels := int(frame.NumChannels)
	sampleCount := len(frame.Data) / (2 * channels)
	data := make([]byte, sampleCount*2)
	for i := 0; i < sampleCount; i++ {
		sum := 0
		for ch := 0; ch < channels; ch++ {
			offset := (i*channels + ch) * 2
			sum += int(int16(binary.LittleEndian.Uint16(frame.Data[offset:])))
		}
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(sum/channels)))
	}
	clone := *frame
	clone.Data = data
	clone.NumChannels = 1
	clone.SamplesPerChannel = uint32(sampleCount)
	return &clone
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
		if s.decoder != nil {
			_ = s.decoder.Close()
		}
	})
	return s.err
}
