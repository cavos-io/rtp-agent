package speechmatics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/google/uuid"
)

const (
	defaultSpeechmaticsTTSBaseURL    = "https://preview.tts.speechmatics.com"
	defaultSpeechmaticsTTSVoice      = "sarah"
	defaultSpeechmaticsTTSSampleRate = 16000
	defaultSpeechmaticsTTSTimeout    = 30 * time.Second
	speechmaticsTTSSDKParam          = "livekit-plugins-1.5.19.rc1"
	speechmaticsTTSAppParam          = "livekit/0.2.8"
)

var speechmaticsTTSRetryInterval = func(retryAttempt int) time.Duration {
	return llm.DefaultAPIConnectOptions().IntervalForRetry(retryAttempt)
}

type SpeechmaticsTTS struct {
	mu         sync.Mutex
	streams    map[*speechmaticsTTSChunkedStream]struct{}
	apiKey     string
	voice      string
	sampleRate int
	baseURL    string
	closed     bool
}

type SpeechmaticsTTSOption func(*SpeechmaticsTTS)

func WithSpeechmaticsTTSVoice(voice string) SpeechmaticsTTSOption {
	return func(t *SpeechmaticsTTS) {
		t.voice = voice
	}
}

func WithSpeechmaticsTTSSampleRate(sampleRate int) SpeechmaticsTTSOption {
	return func(t *SpeechmaticsTTS) {
		t.sampleRate = sampleRate
	}
}

func WithSpeechmaticsTTSBaseURL(baseURL string) SpeechmaticsTTSOption {
	return func(t *SpeechmaticsTTS) {
		t.baseURL = baseURL
	}
}

func NewSpeechmaticsTTS(apiKey string, opts ...SpeechmaticsTTSOption) *SpeechmaticsTTS {
	if apiKey == "" {
		apiKey = os.Getenv(speechmaticsAPIKeyEnv)
	}
	provider := &SpeechmaticsTTS{
		apiKey:     apiKey,
		voice:      defaultSpeechmaticsTTSVoice,
		sampleRate: defaultSpeechmaticsTTSSampleRate,
		baseURL:    defaultSpeechmaticsTTSBaseURL,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *SpeechmaticsTTS) Label() string { return "speechmatics.TTS" }
func (t *SpeechmaticsTTS) Model() string { return "unknown" }
func (t *SpeechmaticsTTS) Provider() string {
	return "Speechmatics"
}

func (t *SpeechmaticsTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SpeechmaticsTTS) SampleRate() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sampleRate
}
func (t *SpeechmaticsTTS) NumChannels() int { return 1 }

func (t *SpeechmaticsTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	t.mu.Lock()
	closed := t.closed
	apiKey := t.apiKey
	baseURL := t.baseURL
	voice := t.voice
	sampleRate := t.sampleRate
	t.mu.Unlock()
	if closed {
		return nil, io.ErrClosedPipe
	}
	if apiKey == "" {
		return nil, fmt.Errorf("speechmatics API key is required. Pass one in via the apiKey parameter, or set SPEECHMATICS_API_KEY")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &speechmaticsTTSChunkedStream{
		ctx:        streamCtx,
		cancel:     cancel,
		text:       text,
		apiKey:     apiKey,
		baseURL:    baseURL,
		voice:      voice,
		sampleRate: sampleRate,
		requestID:  uuid.NewString(),
		owner:      t,
	}
	if !t.registerStream(stream) {
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func buildSpeechmaticsTTSRequest(ctx context.Context, t *SpeechmaticsTTS, text string) (*http.Request, error) {
	t.mu.Lock()
	apiKey := t.apiKey
	baseURL := t.baseURL
	voice := t.voice
	sampleRate := t.sampleRate
	t.mu.Unlock()
	return buildSpeechmaticsTTSRequestFromOptions(ctx, speechmaticsTTSRequestOptions{
		text:       text,
		apiKey:     apiKey,
		baseURL:    baseURL,
		voice:      voice,
		sampleRate: sampleRate,
	})
}

type speechmaticsTTSRequestOptions struct {
	text       string
	apiKey     string
	baseURL    string
	voice      string
	sampleRate int
}

func buildSpeechmaticsTTSRequestFromOptions(ctx context.Context, opts speechmaticsTTSRequestOptions) (*http.Request, error) {
	u, err := url.Parse(opts.baseURL + "/generate/" + opts.voice)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("output_format", fmt.Sprintf("pcm_%d", opts.sampleRate))
	q.Set("sm-sdk", speechmaticsTTSSDKParam)
	q.Set("sm-app", speechmaticsTTSAppParam)
	u.RawQuery = q.Encode()

	body := map[string]string{"text": opts.text}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+opts.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *SpeechmaticsTTS) UpdateOptions(opts ...SpeechmaticsTTSOption) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sampleRate := t.sampleRate
	baseURL := t.baseURL
	for _, opt := range opts {
		opt(t)
	}
	t.sampleRate = sampleRate
	t.baseURL = baseURL
}

func (t *SpeechmaticsTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return nil, io.ErrClosedPipe
	}
	return nil, fmt.Errorf("streaming is not supported by this TTS, please use a different TTS or use a StreamAdapter")
}

func (t *SpeechmaticsTTS) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	streams := make([]*speechmaticsTTSChunkedStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*speechmaticsTTSChunkedStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *SpeechmaticsTTS) registerStream(stream *speechmaticsTTSChunkedStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = stream.Close()
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*speechmaticsTTSChunkedStream]struct{})
	}
	t.streams[stream] = struct{}{}
	t.mu.Unlock()
	return true
}

func (t *SpeechmaticsTTS) unregisterStream(stream *speechmaticsTTSChunkedStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	delete(t.streams, stream)
	t.mu.Unlock()
}

type speechmaticsTTSChunkedStream struct {
	mu            sync.Mutex
	stream        io.ReadCloser
	ctx           context.Context
	cancel        context.CancelFunc
	requestCancel context.CancelFunc
	owner         *SpeechmaticsTTS
	text          string
	apiKey        string
	baseURL       string
	voice         string
	sampleRate    int
	requestID     string
	requested     bool
	retryAttempt  int
	pcm           *audio.AudioByteStream
	pendingFrames []*model.AudioFrame
	pendingErr    error
	emittedAudio  bool
	finalReady    bool
	finalSent     bool
	closed        bool
}

func (s *speechmaticsTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.isClosedOrFinal() {
		return nil, io.EOF
	}
	if len(s.pendingFrames) > 0 {
		return s.emitFrame(s.popPendingFrame()), nil
	}
	if s.pendingErr != nil {
		err := s.pendingErr
		s.pendingErr = nil
		s.finish()
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if speechmaticsTTSTimeoutError(err) {
			return nil, speechmaticsTTSTimeoutAPIError()
		}
		return nil, speechmaticsTTSConnectionAPIError()
	}
	if s.finalReady {
		s.finalReady = false
		return s.emitFinal()
	}
	if err := s.ensureStream(); err != nil {
		if s.isClosedOrFinal() {
			return nil, io.EOF
		}
		s.finish()
		return nil, err
	}
	if s.isClosedOrFinal() {
		return nil, io.EOF
	}
	if s.stream == nil {
		return nil, io.EOF
	}
	for {
		buf := make([]byte, 4096)
		n, err := s.stream.Read(buf)
		if s.isClosedOrFinal() {
			return nil, io.EOF
		}
		if n > 0 {
			frames := s.pcmStream().Push(buf[:n])
			if err == io.EOF {
				frames = append(frames, s.pcmStream().Flush()...)
				s.finalReady = true
			} else if err != nil {
				frames = append(frames, s.pcmStream().Flush()...)
				s.pendingErr = err
			}
			if len(frames) == 0 {
				if err != nil {
					if err == io.EOF {
						return s.emitFinal()
					}
					if errors.Is(err, context.Canceled) {
						s.finish()
						return nil, context.Canceled
					}
					apiErr := speechmaticsTTSReadAPIError(err)
					if retryErr := s.prepareRetryBeforeAudio(apiErr); retryErr == nil {
						if openErr := s.ensureStream(); openErr != nil {
							s.finish()
							return nil, openErr
						}
						continue
					} else if retryErr != apiErr {
						s.finish()
						return nil, retryErr
					}
					s.finish()
					return nil, apiErr
				}
				continue
			}
			if err != nil {
				s.closeTerminalResponse()
			}
			s.pendingFrames = append(s.pendingFrames, frames[1:]...)
			return s.emitFrame(frames[0]), nil
		}
		if err != nil {
			if err == io.EOF {
				frames := s.pcmStream().Flush()
				if len(frames) > 0 {
					s.finalReady = true
					s.pendingFrames = append(s.pendingFrames, frames[1:]...)
					return s.emitFrame(frames[0]), nil
				}
				return s.emitFinal()
			}
			if errors.Is(err, context.Canceled) {
				s.finish()
				return nil, context.Canceled
			}
			apiErr := speechmaticsTTSReadAPIError(err)
			if retryErr := s.prepareRetryBeforeAudio(apiErr); retryErr == nil {
				if openErr := s.ensureStream(); openErr != nil {
					s.finish()
					return nil, openErr
				}
				continue
			} else if retryErr != apiErr {
				s.finish()
				return nil, retryErr
			}
			s.finish()
			return nil, apiErr
		}
	}
}

func (s *speechmaticsTTSChunkedStream) closeTerminalResponse() {
	s.cancelRequest()
	s.mu.Lock()
	stream := s.stream
	s.stream = nil
	s.mu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
}

func (s *speechmaticsTTSChunkedStream) pcmStream() *audio.AudioByteStream {
	if s.pcm == nil {
		if s.sampleRate <= 0 {
			s.pcm = audio.NewAudioByteStream(0, 1, 1)
			return s.pcm
		}
		samplesPerChannel := uint32(s.sampleRate) * 200 / 1000
		s.pcm = audio.NewAudioByteStreamWithOptions(uint32(s.sampleRate), 1, samplesPerChannel, audio.AudioByteStreamOptions{
			Progressive: true,
		})
	}
	return s.pcm
}

func (s *speechmaticsTTSChunkedStream) popPendingFrame() *model.AudioFrame {
	frame := s.pendingFrames[0]
	s.pendingFrames = s.pendingFrames[1:]
	return frame
}

func (s *speechmaticsTTSChunkedStream) emitFrame(frame *model.AudioFrame) *tts.SynthesizedAudio {
	s.emittedAudio = true
	return &tts.SynthesizedAudio{
		RequestID: s.requestID,
		Frame:     frame,
	}
}

func (s *speechmaticsTTSChunkedStream) ensureStream() error {
	if s.stream != nil || s.requested {
		return nil
	}
	s.requested = true
	for {
		err := s.openStream()
		if err == nil || err == io.EOF || errors.Is(err, context.Canceled) {
			return err
		}
		if retryErr := s.prepareRetryBeforeAudio(err); retryErr != nil {
			return retryErr
		}
	}
}

func (s *speechmaticsTTSChunkedStream) openStream() error {
	requestCtx, requestCancel := context.WithTimeout(s.ctx, defaultSpeechmaticsTTSTimeout)
	s.mu.Lock()
	if s.closed || s.finalSent {
		s.mu.Unlock()
		requestCancel()
		return io.EOF
	}
	s.requestCancel = requestCancel
	s.mu.Unlock()

	req, err := buildSpeechmaticsTTSRequestFromOptions(requestCtx, speechmaticsTTSRequestOptions{
		text:       s.text,
		apiKey:     s.apiKey,
		baseURL:    s.baseURL,
		voice:      s.voice,
		sampleRate: s.sampleRate,
	})
	if err != nil {
		requestCancel()
		return speechmaticsTTSConnectionAPIError()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		requestCancel()
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if speechmaticsTTSTimeoutError(err) {
			return speechmaticsTTSTimeoutAPIError()
		}
		return speechmaticsTTSConnectionAPIError()
	}
	if resp.StatusCode == 499 {
		requestCancel()
		resp.Body.Close()
		return io.EOF
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		requestCancel()
		resp.Body.Close()
		message := speechmaticsTTSStatusReason(resp)
		return llm.NewAPIStatusError(message, resp.StatusCode, "", nil)
	}
	s.mu.Lock()
	if s.closed || s.finalSent {
		s.mu.Unlock()
		requestCancel()
		resp.Body.Close()
		return io.EOF
	}
	s.stream = resp.Body
	s.mu.Unlock()
	return nil
}

func speechmaticsTTSRetryableError(err error) bool {
	var apiErr *llm.APIError
	return errors.As(err, &apiErr) && apiErr.Retryable
}

func speechmaticsTTSTimeoutAPIError() error {
	return llm.NewAPITimeoutError("")
}

func speechmaticsTTSConnectionAPIError() error {
	return llm.NewAPIConnectionError("")
}

func speechmaticsTTSReadAPIError(err error) error {
	if speechmaticsTTSTimeoutError(err) {
		return speechmaticsTTSTimeoutAPIError()
	}
	return speechmaticsTTSConnectionAPIError()
}

func (s *speechmaticsTTSChunkedStream) prepareRetryBeforeAudio(err error) error {
	if s.emittedAudio || !speechmaticsTTSRetryableError(err) {
		return err
	}
	maxRetry := llm.DefaultAPIConnectOptions().MaxRetry
	if maxRetry <= 0 || s.retryAttempt >= maxRetry {
		return err
	}
	interval := speechmaticsTTSRetryInterval(s.retryAttempt)
	s.retryAttempt++
	s.resetRetryableAttempt()
	if interval <= 0 {
		return nil
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-s.ctx.Done():
		if s.isClosedOrFinal() {
			return io.EOF
		}
		return context.Canceled
	}
}

func (s *speechmaticsTTSChunkedStream) resetRetryableAttempt() {
	s.cancelRequest()
	s.mu.Lock()
	stream := s.stream
	s.stream = nil
	s.requested = false
	s.pcm = nil
	s.pendingFrames = nil
	s.pendingErr = nil
	s.finalReady = false
	s.mu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
}

func speechmaticsTTSStatusReason(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	if fields := strings.Fields(resp.Status); len(fields) > 1 && fields[0] == fmt.Sprintf("%d", resp.StatusCode) {
		return strings.TrimSpace(strings.TrimPrefix(resp.Status, fields[0]))
	}
	if message := http.StatusText(resp.StatusCode); message != "" {
		return message
	}
	return fmt.Sprintf("HTTP %d", resp.StatusCode)
}

func speechmaticsTTSTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func (s *speechmaticsTTSChunkedStream) emitFinal() (*tts.SynthesizedAudio, error) {
	if s.isClosedOrFinal() {
		return nil, io.EOF
	}
	if strings.TrimSpace(s.text) != "" && !s.emittedAudio {
		err := llm.NewAPIError(fmt.Sprintf("no audio frames were pushed for text: %s", s.text), nil, true)
		if retryErr := s.prepareRetryBeforeAudio(err); retryErr == nil {
			if openErr := s.ensureStream(); openErr != nil {
				s.finish()
				return nil, openErr
			}
			return s.Next()
		} else if retryErr != err {
			s.finish()
			return nil, retryErr
		}
		s.finish()
		return nil, err
	}
	if !s.markFinalSent() {
		return nil, io.EOF
	}
	s.finish()
	return &tts.SynthesizedAudio{RequestID: s.requestID, IsFinal: true}, nil
}

func (s *speechmaticsTTSChunkedStream) markFinalSent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.finalSent {
		return false
	}
	s.finalSent = true
	return true
}

func (s *speechmaticsTTSChunkedStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.finalSent = true
	stream := s.stream
	s.stream = nil
	cancel := s.cancel
	requestCancel := s.requestCancel
	s.mu.Unlock()

	if requestCancel != nil {
		requestCancel()
	}
	if cancel != nil {
		cancel()
	}
	if stream == nil {
		s.finish()
		return nil
	}
	_ = stream.Close()
	s.finish()
	return nil
}

func (s *speechmaticsTTSChunkedStream) isClosedOrFinal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed || s.finalSent
}

func (s *speechmaticsTTSChunkedStream) cancelRequest() {
	s.mu.Lock()
	cancel := s.requestCancel
	s.requestCancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *speechmaticsTTSChunkedStream) finish() {
	s.cancelRequest()
	s.mu.Lock()
	stream := s.stream
	s.stream = nil
	s.mu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
}
