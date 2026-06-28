package groq

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultGroqTTSBaseURL        = "https://api.groq.com/openai/v1"
	defaultGroqTTSModel          = "canopylabs/orpheus-v1-english"
	defaultGroqTTSVoice          = "autumn"
	defaultGroqTTSResponseFormat = "wav"
	defaultGroqTTSSampleRate     = 48000
	defaultGroqTTSTotalTimeout   = 30 * time.Second
)

type GroqTTS struct {
	apiKey         string
	baseURL        string
	model          string
	voice          string
	responseFormat string
	sampleRate     int
	mu             sync.Mutex
	closed         bool
	streams        map[*groqTTSChunkedStream]struct{}
	nextRequestID  uint64
	requestCancels map[uint64]context.CancelFunc
}

type GroqTTSOption func(*GroqTTS)

func WithGroqTTSBaseURL(baseURL string) GroqTTSOption {
	return func(t *GroqTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithGroqTTSModel(model string) GroqTTSOption {
	return func(t *GroqTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithGroqTTSVoice(voice string) GroqTTSOption {
	return func(t *GroqTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func NewGroqTTS(apiKey string, voice string, opts ...GroqTTSOption) *GroqTTS {
	if apiKey == "" {
		apiKey = os.Getenv("GROQ_API_KEY")
	}
	provider := &GroqTTS{
		apiKey:         apiKey,
		baseURL:        defaultGroqTTSBaseURL,
		model:          defaultGroqTTSModel,
		voice:          voice,
		responseFormat: defaultGroqTTSResponseFormat,
		sampleRate:     defaultGroqTTSSampleRate,
		streams:        make(map[*groqTTSChunkedStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultGroqTTSVoice
	}
	return provider
}

func (t *GroqTTS) Label() string { return "groq.TTS" }
func (t *GroqTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *GroqTTS) SampleRate() int  { return t.sampleRate }
func (t *GroqTTS) NumChannels() int { return 1 }
func (t *GroqTTS) Model() string    { return t.model }
func (t *GroqTTS) Provider() string { return "Groq" }

func (t *GroqTTS) UpdateOptions(model string, voice string) {
	if model != "" {
		t.model = model
	}
	if voice != "" {
		t.voice = voice
	}
}

func (t *GroqTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateGroqTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	if t.isClosed() {
		return nil, fmt.Errorf("groq tts is closed: %w", io.ErrClosedPipe)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &groqTTSChunkedStream{
		ctx:            streamCtx,
		cancel:         cancel,
		inputText:      text,
		apiKey:         t.apiKey,
		baseURL:        t.baseURL,
		model:          t.model,
		voice:          t.voice,
		responseFormat: t.responseFormat,
		sampleRate:     t.sampleRate,
		provider:       t,
	}
	if !t.registerStream(stream) {
		return nil, fmt.Errorf("groq tts is closed: %w", io.ErrClosedPipe)
	}
	return stream, nil
}

func (t *GroqTTS) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	t.closed = true
	streams := make([]*groqTTSChunkedStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*groqTTSChunkedStream]struct{})
	requestCancels := make([]context.CancelFunc, 0, len(t.requestCancels))
	for _, cancel := range t.requestCancels {
		requestCancels = append(requestCancels, cancel)
	}
	t.requestCancels = make(map[uint64]context.CancelFunc)
	t.mu.Unlock()

	for _, cancel := range requestCancels {
		cancel()
	}
	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *GroqTTS) registerStream(stream *groqTTSChunkedStream) bool {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = stream.Close()
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*groqTTSChunkedStream]struct{})
	}
	t.streams[stream] = struct{}{}
	t.mu.Unlock()
	return true
}

func (t *GroqTTS) registerRequest(cancel context.CancelFunc) (uint64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, false
	}
	if t.requestCancels == nil {
		t.requestCancels = make(map[uint64]context.CancelFunc)
	}
	t.nextRequestID++
	id := t.nextRequestID
	t.requestCancels[id] = cancel
	return id, true
}

func (t *GroqTTS) unregisterRequest(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.requestCancels, id)
}

func (t *GroqTTS) unregisterStream(stream *groqTTSChunkedStream) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func (t *GroqTTS) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func groqTTSTransportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(err.Error())
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return llm.NewAPITimeoutError(err.Error())
	}
	return llm.NewAPIConnectionError(err.Error())
}

func validateGroqTTSAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("groq API key is required, either as argument or set GROQ_API_KEY environment variable")
	}
	return nil
}

func buildGroqTTSRequest(ctx context.Context, t *GroqTTS, text string) (*http.Request, error) {
	return buildGroqTTSRequestWithOptions(ctx, t.baseURL, t.apiKey, t.model, t.voice, t.responseFormat, text)
}

func buildGroqTTSRequestWithOptions(ctx context.Context, baseURL, apiKey, modelName, voice, responseFormat, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"model":           modelName,
		"voice":           voice,
		"input":           text,
		"response_format": responseFormat,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/audio/speech", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *GroqTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("groq streaming tts not natively supported")
}

type groqTTSChunkedStream struct {
	ctx              context.Context
	cancel           context.CancelFunc
	requestCancel    context.CancelFunc
	inputText        string
	startOnce        sync.Once
	startErr         error
	resp             *http.Response
	apiKey           string
	baseURL          string
	model            string
	voice            string
	responseFormat   string
	sampleRate       int
	provider         *GroqTTS
	requestID        string
	started          bool
	finalSent        bool
	pendingFinal     bool
	closed           bool
	sourceSampleRate uint32
	numChannels      uint16
	remainingData    int
	bytesPerFrame    int
}

func (s *groqTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureStarted(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		_ = s.Close()
		return &tts.SynthesizedAudio{RequestID: s.requestID, IsFinal: true}, nil
	}

	if !s.started {
		if err := s.startWAV(); err != nil {
			if errors.Is(err, io.EOF) {
				s.finalSent = true
				_ = s.Close()
				return &tts.SynthesizedAudio{RequestID: s.requestID, IsFinal: true}, nil
			}
			return nil, s.fail(err)
		}
		s.started = true
	}
	if s.remainingData == 0 {
		s.finalSent = true
		_ = s.Close()
		return &tts.SynthesizedAudio{RequestID: s.requestID, IsFinal: true}, nil
	}

	data, err := s.readPCMChunk()
	if err != nil {
		return nil, s.fail(err)
	}
	frame := &model.AudioFrame{
		Data:              data,
		SampleRate:        s.sourceSampleRate,
		NumChannels:       uint32(s.numChannels),
		SamplesPerChannel: uint32(len(data) / s.bytesPerFrame),
	}
	frame = groqDownmixToMono(frame)
	if s.sampleRate > 0 && frame.SampleRate != uint32(s.sampleRate) {
		frame, err = audio.ResampleAudioFrame(frame, uint32(s.sampleRate))
		if err != nil {
			return nil, s.fail(err)
		}
	}
	return &tts.SynthesizedAudio{
		Frame:     frame,
		RequestID: s.requestID,
	}, nil
}

func (s *groqTTSChunkedStream) fail(err error) error {
	_ = s.Close()
	return groqTTSStreamError(err)
}

func (s *groqTTSChunkedStream) ensureStarted() error {
	if s.resp != nil || s.provider == nil || s.inputText == "" {
		return nil
	}
	s.startOnce.Do(func() {
		if s.isClosed() {
			s.startErr = io.EOF
			s.unregister()
			return
		}
		if s.provider.isClosed() {
			s.startErr = fmt.Errorf("groq tts is closed: %w", io.ErrClosedPipe)
			s.unregister()
			return
		}
		reqCtx, reqCancel := context.WithTimeout(s.ctx, defaultGroqTTSTotalTimeout)
		s.requestCancel = reqCancel
		requestID, ok := s.provider.registerRequest(reqCancel)
		if !ok {
			reqCancel()
			s.startErr = fmt.Errorf("groq tts is closed: %w", io.ErrClosedPipe)
			s.unregister()
			return
		}
		cleanupRequest := func() {
			s.provider.unregisterRequest(requestID)
			reqCancel()
			s.requestCancel = nil
		}
		req, err := buildGroqTTSRequestWithOptions(reqCtx, s.baseURL, s.apiKey, s.model, s.voice, s.responseFormat, s.inputText)
		if err != nil {
			cleanupRequest()
			s.startErr = err
			s.unregister()
			return
		}
		resp, err := http.DefaultClient.Do(req)
		s.provider.unregisterRequest(requestID)
		if err != nil {
			defer reqCancel()
			s.requestCancel = nil
			if s.isClosed() {
				s.startErr = io.EOF
				s.unregister()
				return
			}
			if s.provider.isClosed() && errors.Is(reqCtx.Err(), context.Canceled) {
				s.startErr = fmt.Errorf("groq tts is closed: %w", io.ErrClosedPipe)
				s.unregister()
				return
			}
			if errors.Is(reqCtx.Err(), context.Canceled) {
				s.startErr = context.Canceled
				s.unregister()
				return
			}
			s.startErr = groqTTSTransportError(err)
			s.unregister()
			return
		}
		if s.isClosed() {
			resp.Body.Close()
			reqCancel()
			s.requestCancel = nil
			s.startErr = io.EOF
			s.unregister()
			return
		}
		if s.provider.isClosed() {
			resp.Body.Close()
			reqCancel()
			s.requestCancel = nil
			s.startErr = fmt.Errorf("groq tts is closed: %w", io.ErrClosedPipe)
			s.unregister()
			return
		}
		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == 499 {
				resp.Body.Close()
				reqCancel()
				s.requestCancel = nil
				s.finalSent = true
				s.startErr = io.EOF
				s.unregister()
				return
			}
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			reqCancel()
			s.requestCancel = nil
			s.startErr = llm.NewAPIStatusError("Groq TTS request failed", resp.StatusCode, "", string(respBody))
			s.unregister()
			return
		}
		if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "audio") {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			reqCancel()
			s.requestCancel = nil
			s.startErr = llm.NewAPIError("Groq returned non-audio data", string(respBody), true)
			s.unregister()
			return
		}
		s.requestID = fmt.Sprintf("groq-tts-%d", requestID)
		s.resp = resp
	})
	return s.startErr
}

func (s *groqTTSChunkedStream) isClosed() bool {
	return s.closed
}

func (s *groqTTSChunkedStream) unregister() {
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
}

func groqTTSStreamError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	return groqTTSTransportError(err)
}

func groqDownmixToMono(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil || frame.NumChannels <= 1 {
		return frame
	}
	channels := int(frame.NumChannels)
	samples := int(frame.SamplesPerChannel)
	if samples == 0 {
		samples = len(frame.Data) / (channels * 2)
	}
	out := make([]byte, samples*2)
	for sample := 0; sample < samples; sample++ {
		sum := int32(0)
		for channel := 0; channel < channels; channel++ {
			offset := (sample*channels + channel) * 2
			if offset+2 > len(frame.Data) {
				break
			}
			sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2])))
		}
		binary.LittleEndian.PutUint16(out[sample*2:sample*2+2], uint16(int16(sum/int32(channels))))
	}
	mono := *frame
	mono.Data = out
	mono.NumChannels = 1
	mono.SamplesPerChannel = uint32(samples)
	return &mono
}

func (s *groqTTSChunkedStream) startWAV() error {
	header := make([]byte, 12)
	if _, err := io.ReadFull(s.resp.Body, header); err != nil {
		return err
	}
	if string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return fmt.Errorf("invalid groq wav data")
	}

	var (
		audioFormat   uint16
		numChannels   uint16
		sampleRate    uint32
		bitsPerSample uint16
	)
	for {
		chunkHeader := make([]byte, 8)
		if _, err := io.ReadFull(s.resp.Body, chunkHeader); err != nil {
			if err == io.EOF {
				return fmt.Errorf("missing groq wav data chunk")
			}
			return err
		}
		chunkID := string(chunkHeader[:4])
		chunkSize := int(binary.LittleEndian.Uint32(chunkHeader[4:8]))
		if chunkSize < 0 {
			return fmt.Errorf("invalid groq wav chunk size")
		}

		switch chunkID {
		case "fmt ":
			chunk := make([]byte, chunkSize)
			if _, err := io.ReadFull(s.resp.Body, chunk); err != nil {
				return err
			}
			if len(chunk) < 16 {
				return fmt.Errorf("invalid groq wav fmt chunk")
			}
			audioFormat = binary.LittleEndian.Uint16(chunk[0:2])
			numChannels = binary.LittleEndian.Uint16(chunk[2:4])
			sampleRate = binary.LittleEndian.Uint32(chunk[4:8])
			bitsPerSample = binary.LittleEndian.Uint16(chunk[14:16])
			if chunkSize%2 == 1 {
				if err := discardGroqWAVBytes(s.resp.Body, 1); err != nil {
					return err
				}
			}
		case "data":
			if audioFormat != 1 || bitsPerSample != 16 {
				return fmt.Errorf("unsupported groq wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
			}
			if sampleRate == 0 || numChannels == 0 {
				return fmt.Errorf("missing groq wav format metadata")
			}
			bytesPerFrame := int(numChannels) * 2
			if chunkSize%bytesPerFrame != 0 {
				return fmt.Errorf("invalid groq wav data chunk size")
			}
			s.sourceSampleRate = sampleRate
			s.numChannels = numChannels
			s.remainingData = chunkSize
			s.bytesPerFrame = bytesPerFrame
			return nil
		default:
			if err := discardGroqWAVBytes(s.resp.Body, chunkSize); err != nil {
				return err
			}
			if chunkSize%2 == 1 {
				if err := discardGroqWAVBytes(s.resp.Body, 1); err != nil {
					return err
				}
			}
		}
	}
}

func (s *groqTTSChunkedStream) readPCMChunk() ([]byte, error) {
	size := s.remainingData
	if size > 4096 {
		size = 4096
	}
	if rem := size % s.bytesPerFrame; rem != 0 && size > s.bytesPerFrame {
		size -= rem
	}
	buf := make([]byte, size)
	n, err := s.resp.Body.Read(buf)
	if n > 0 {
		if rem := n % s.bytesPerFrame; rem != 0 {
			needed := s.bytesPerFrame - rem
			if n+needed > len(buf) {
				grown := make([]byte, n+needed)
				copy(grown, buf[:n])
				buf = grown
			}
			if _, readErr := io.ReadFull(s.resp.Body, buf[n:n+needed]); readErr != nil {
				return nil, readErr
			}
			n += needed
		}
		s.remainingData -= n
		if s.remainingData == 0 {
			s.pendingFinal = true
		}
		return append([]byte(nil), buf[:n]...), nil
	}
	if err != nil {
		return nil, err
	}
	return nil, io.ErrUnexpectedEOF
}

func discardGroqWAVBytes(r io.Reader, n int) error {
	if n <= 0 {
		return nil
	}
	_, err := io.CopyN(io.Discard, r, int64(n))
	return err
}

func (s *groqTTSChunkedStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.requestCancel != nil {
		s.requestCancel()
		s.requestCancel = nil
	}
	if s.resp == nil || s.resp.Body == nil {
		s.unregister()
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	err := body.Close()
	s.unregister()
	return err
}
