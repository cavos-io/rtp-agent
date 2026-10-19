// Package beta contains experimental Google provider integrations.
package beta

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/inference"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
	"google.golang.org/genai"
)

const liveEndpoint = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"

// STT is an experimental Gemini Live streaming transcription provider.
// It does not use Google Cloud Speech credentials or support offline recognition.
type STT struct {
	stt.ErrorEmitter
	mu                                sync.Mutex
	streams                           map[*recognizeStream]struct{}
	closed                            bool
	apiKey, model, language, endpoint string
	languageCodes, vocabulary         []string
	languageCodesSet                  bool
	sampleRate                        uint32
	options                           llm.APIConnectOptions
	finalizeTimeout                   time.Duration
}

type STTOption func(*STT)

func WithSTTModel(value string) STTOption { return func(s *STT) { s.model = value } }

// WithSTTLanguage sets the fallback language and default recognition hint.
// An empty string enables language detection unless explicit codes are configured.
func WithSTTLanguage(value string) STTOption { return func(s *STT) { s.language = value } }

// WithSTTLanguageCodes overrides the language hint. An empty list explicitly
// enables automatic detection, including when Stream receives a language override.
func WithSTTLanguageCodes(values []string) STTOption {
	return func(s *STT) { s.languageCodes = append([]string(nil), values...); s.languageCodesSet = true }
}
func WithSTTCustomVocabulary(values []string) STTOption {
	return func(s *STT) { s.vocabulary = append([]string(nil), values...) }
}
func WithSTTSampleRate(value uint32) STTOption { return func(s *STT) { s.sampleRate = value } }
func WithSTTConnectOptions(value llm.APIConnectOptions) STTOption {
	return func(s *STT) { s.options = value }
}

// NewSTT uses an explicit API key, then GOOGLE_API_KEY, then GEMINI_API_KEY.
func NewSTT(apiKey string, opts ...STTOption) (*STT, error) {
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	s := &STT{apiKey: apiKey, model: "gemini-3.5-transcribe-live", language: "en-US", sampleRate: 16000, endpoint: liveEndpoint, options: llm.DefaultAPIConnectOptions(), finalizeTimeout: 2 * time.Second, streams: make(map[*recognizeStream]struct{})}
	for _, opt := range opts {
		opt(s)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("gemini STT API key is required")
	}
	if strings.TrimSpace(s.model) == "" || s.sampleRate == 0 {
		return nil, errors.New("gemini STT requires a model and nonzero sample rate")
	}
	if len(s.vocabulary) > 1000 {
		return nil, errors.New("gemini STT custom vocabulary exceeds 1000 terms")
	}
	if err := s.options.Validate(); err != nil {
		return nil, err
	}
	if s.options.Timeout == 0 {
		s.options.Timeout = 10 * time.Second
	}
	return s, nil
}

func (s *STT) Label() string           { return "google.beta.STT" }
func (s *STT) Model() string           { return s.model }
func (s *STT) Provider() string        { return "google" }
func (s *STT) InputSampleRate() uint32 { return s.sampleRate }
func (s *STT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true}
}
func (s *STT) Recognize(context.Context, []*model.AudioFrame, string) (*stt.SpeechEvent, error) {
	return nil, errors.New("gemini STT only supports streaming recognition")
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, io.ErrClosedPipe
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if language == "" {
		language = s.language
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &recognizeStream{owner: s, ctx: ctx, cancel: cancel, language: language, input: make(chan inputMessage, 32), events: make(chan *stt.SpeechEvent, 32), done: make(chan struct{})}
	s.streams[r] = struct{}{}
	go r.run()
	return r, nil
}

func (s *STT) Close() error {
	s.mu.Lock()
	s.closed = true
	streams := make([]*recognizeStream, 0, len(s.streams))
	for r := range s.streams {
		streams = append(streams, r)
	}
	s.mu.Unlock()
	for _, r := range streams {
		_ = r.Close()
	}
	return nil
}

type inputMessage struct {
	audio *genai.Blob
	end   bool
}
type recognizeStream struct {
	owner     *STT
	ctx       context.Context
	cancel    context.CancelFunc
	language  string
	input     chan inputMessage
	events    chan *stt.SpeechEvent
	done      chan struct{}
	inputMu   sync.Mutex
	ended     bool
	inputRate uint32
	resampler *audio.StreamingResampler
	finals    atomic.Uint64
	err       error // Written before done/events close; read only after events is closed.
}

func (s *recognizeStream) PushFrame(frame *model.AudioFrame) error {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if err := s.checkInput(); err != nil {
		return err
	}
	if frame == nil {
		return nil
	}
	if frame.SampleRate == 0 || frame.NumChannels != 1 || uint64(len(frame.Data)) != uint64(frame.SamplesPerChannel)*2 {
		return errors.New("gemini STT requires valid mono 16-bit PCM frames")
	}
	if s.inputRate != 0 && s.inputRate != frame.SampleRate {
		return errors.New("gemini STT input sample rate changed")
	}
	s.inputRate = frame.SampleRate
	if frame.SampleRate != s.owner.sampleRate {
		if s.resampler == nil {
			var err error
			s.resampler, err = audio.NewStreamingResampler(frame.SampleRate, s.owner.sampleRate, 1)
			if err != nil {
				return err
			}
		}
		var err error
		frame, err = s.resampler.Push(frame)
		if err != nil {
			return err
		}
	}
	return s.pushAudio(frame)
}

func (s *recognizeStream) checkInput() error {
	if s.ended {
		return io.ErrClosedPipe
	}
	select {
	case <-s.done:
		return io.ErrClosedPipe
	case <-s.ctx.Done():
		return s.ctx.Err()
	default:
		return nil
	}
}
func (s *recognizeStream) enqueue(m inputMessage) error {
	select {
	case <-s.done:
		return io.ErrClosedPipe
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.input <- m:
		return nil
	}
}
func (s *recognizeStream) pushAudio(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return s.enqueue(inputMessage{audio: &genai.Blob{Data: append([]byte(nil), frame.Data...), MIMEType: fmt.Sprintf("audio/pcm;rate=%d", s.owner.sampleRate)}})
}
func (s *recognizeStream) flush() error {
	if s.resampler != nil {
		return s.pushAudio(s.resampler.Flush())
	}
	return nil
}

// Flush drains resampled audio but does not finalize a Gemini turn, matching
// the Python base stream plus the Gemini plugin's ignored flush sentinel.
func (s *recognizeStream) Flush() error {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if err := s.checkInput(); err != nil {
		return err
	}
	return s.flush()
}
func (s *recognizeStream) EndInput() error {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if err := s.checkInput(); err != nil {
		return err
	}
	if err := s.flush(); err != nil {
		return err
	}
	if err := s.enqueue(inputMessage{end: true}); err != nil {
		return err
	}
	s.ended = true
	return nil
}
func (s *recognizeStream) Close() error { s.cancel(); <-s.done; return nil }
func (s *recognizeStream) Next() (*stt.SpeechEvent, error) {
	event, ok := <-s.events
	if ok {
		return event, nil
	}
	if s.err != nil {
		return nil, s.err
	}
	return nil, io.EOF
}

func (s *recognizeStream) setup() genai.LiveClientMessage {
	codes := s.owner.languageCodes
	if !s.owner.languageCodesSet && s.language != "" {
		codes = []string{s.language}
	}
	return genai.LiveClientMessage{Setup: &genai.LiveClientSetup{
		Model:                   "models/" + strings.TrimPrefix(s.owner.model, "models/"),
		GenerationConfig:        &genai.GenerationConfig{ResponseModalities: []genai.Modality{genai.ModalityText}},
		InputAudioTranscription: &genai.AudioTranscriptionConfig{LanguageCodes: codes, CustomVocabulary: s.owner.vocabulary},
	}}
}

func (s *recognizeStream) run() {
	defer func() {
		s.cancel()
		s.owner.mu.Lock()
		delete(s.owner.streams, s)
		s.owner.mu.Unlock()
		close(s.events)
		close(s.done)
	}()
	retries := 0
	for {
		finals := s.finals.Load()
		ended, connectedFor, err := s.attempt()
		if s.ctx.Err() != nil {
			return
		}
		if err == nil {
			return
		}
		if s.finals.Load() != finals || connectedFor > s.owner.options.Timeout {
			retries = 0
		}
		var apiErr *inference.APIError
		retryable := errors.As(err, &apiErr) && apiErr.Retryable && !ended && retries < s.owner.options.MaxRetry
		s.owner.EmitError(stt.NewSTTError(s.owner.Label(), err, retryable))
		if !retryable {
			s.err = err
			return
		}
		timer := time.NewTimer(s.owner.options.IntervalForRetry(retries))
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		retries++
	}
}

// The SDK's Live.Connect does not cancel its dial/setup read. Use its typed wire
// messages with a context-aware WebSocket so shutdown also works during setup.
func (s *recognizeStream) connect() (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.owner.options.Timeout)
	defer cancel()
	dialer := *websocket.DefaultDialer
	c, response, err := dialer.DialContext(ctx, s.owner.endpoint, http.Header{"X-Goog-Api-Key": []string{s.owner.apiKey}})
	if err != nil {
		if response != nil {
			if response.Body != nil {
				_ = response.Body.Close()
			}
			return nil, inference.NewAPIStatusError("Gemini STT handshake failed", response.StatusCode, "", nil)
		}
		return nil, normalizeError(err, false)
	}
	c.SetReadLimit(4 << 20)
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	_ = c.SetWriteDeadline(deadline)
	_ = c.SetReadDeadline(deadline)
	if err = c.WriteJSON(s.setup()); err != nil {
		_ = c.Close()
		return nil, normalizeError(err, false)
	}
	msg, err := readMessage(c)
	if err != nil {
		_ = c.Close()
		return nil, normalizeError(err, false)
	}
	if msg.SetupComplete == nil {
		_ = c.Close()
		return nil, inference.NewAPIConnectionErrorWithRetryable("Gemini STT missing setup acknowledgement", false)
	}
	_ = c.SetReadDeadline(time.Time{})
	_ = c.SetWriteDeadline(time.Time{})
	return c, nil
}

type serverMessage struct {
	genai.LiveServerMessage
	Error *struct {
		Code int `json:"code"`
	} `json:"error,omitempty"`
}

func readMessage(c *websocket.Conn) (*genai.LiveServerMessage, error) {
	var msg serverMessage
	if err := c.ReadJSON(&msg); err != nil {
		return nil, err
	}
	if msg.Error != nil {
		return nil, inference.NewAPIStatusError("Gemini STT request failed", msg.Error.Code, "", nil)
	}
	return &msg.LiveServerMessage, nil
}

func (s *recognizeStream) attempt() (ended bool, connectedFor time.Duration, resultErr error) {
	c, err := s.connect()
	if err != nil {
		return false, 0, err
	}
	connectedAt := time.Now()
	defer func() { connectedFor = time.Since(connectedAt) }()
	ctx, cancel := context.WithCancel(s.ctx)
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	var sentEnd atomic.Bool
	var timedOut atomic.Bool
	sent := make(chan error, 1)
	go func() { sent <- s.send(ctx, cancel, c, &sentEnd, &timedOut) }()
	err = s.receive(ctx, c, &sentEnd)
	cancel()
	_ = c.Close()
	stop()
	sendErr := <-sent
	// Preserve a provider response (especially a non-retryable status) when
	// cancelling the writer after receiving it.
	if err == nil && sendErr != nil && !errors.Is(sendErr, context.Canceled) {
		err = sendErr
	}
	if timedOut.Load() {
		return true, 0, nil
	}
	return sentEnd.Load(), 0, err
}

func (s *recognizeStream) send(ctx context.Context, cancel context.CancelFunc, c *websocket.Conn, sentEnd, timedOut *atomic.Bool) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case input := <-s.input:
			payload := struct {
				RealtimeInput struct {
					Audio          *genai.Blob `json:"audio,omitempty"`
					AudioStreamEnd bool        `json:"audioStreamEnd,omitempty"`
				} `json:"realtimeInput"`
			}{}
			payload.RealtimeInput.Audio = input.audio
			payload.RealtimeInput.AudioStreamEnd = input.end
			if input.end {
				sentEnd.Store(true)
			}
			_ = c.SetWriteDeadline(time.Now().Add(s.owner.options.Timeout))
			if err := c.WriteJSON(payload); err != nil {
				_ = c.Close()
				return normalizeError(err, false)
			}
			if input.end {
				timer := time.NewTimer(s.owner.finalizeTimeout)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return nil
				case <-timer.C:
					timedOut.Store(true)
					cancel()
					_ = c.Close()
					return nil
				}
			}
		}
	}
}

func (s *recognizeStream) receive(ctx context.Context, c *websocket.Conn, sentEnd *atomic.Bool) error {
	pending := ""
	language := s.language
	if language == "" {
		language = "en-US"
	}
	fallback := language
	goAway := false
	emit := func(kind stt.SpeechEventType, text string) bool {
		event := &stt.SpeechEvent{Type: kind, Alternatives: []stt.SpeechData{{Text: text, Language: language, Confidence: 1}}}
		select {
		case <-ctx.Done():
			return false
		case s.events <- event:
			if kind == stt.SpeechEventFinalTranscript {
				s.finals.Add(1)
			}
			return true
		}
	}
	for {
		msg, err := readMessage(c)
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) && !goAway {
				return nil
			}
			return normalizeError(err, goAway)
		}
		if msg.GoAway != nil {
			goAway = true
		}
		sc := msg.ServerContent
		if sc == nil {
			continue
		}
		if tr := sc.InterimInputTranscription; tr != nil {
			if tr.LanguageCode != "" {
				language = tr.LanguageCode
			}
			if text := strings.TrimSpace(tr.Text); text != "" {
				pending = text
				if !emit(stt.SpeechEventInterimTranscript, text) {
					return ctx.Err()
				}
			}
		}
		if tr := sc.InputTranscription; tr != nil {
			if tr.LanguageCode != "" {
				language = tr.LanguageCode
			}
			if text := strings.TrimSpace(tr.Text); text != "" {
				pending = ""
				if !emit(stt.SpeechEventFinalTranscript, text) {
					return ctx.Err()
				}
				language = fallback
			}
		}
		if sc.GenerationComplete || sc.TurnComplete {
			if pending != "" {
				if !emit(stt.SpeechEventFinalTranscript, pending) {
					return ctx.Err()
				}
				pending = ""
			}
			language = fallback
		}
		if sentEnd.Load() && (sc.TurnComplete || sc.GenerationComplete || (sc.InputTranscription != nil && sc.InputTranscription.Finished)) {
			return nil
		}
	}
}

func normalizeError(err error, goAway bool) error {
	var api *inference.APIError
	if errors.As(err, &api) {
		return err
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		duration := goAway || strings.Contains(closeErr.Text, "GoAway") || strings.Contains(closeErr.Text, "session duration")
		retryable := duration || (closeErr.Code != 1008 && closeErr.Code != 1003 && closeErr.Code != 1007)
		return inference.NewAPIStatusErrorWithRetryable("Gemini STT connection closed", closeErr.Code, "", nil, retryable)
	}
	return inference.NewAPIConnectionError("Gemini STT connection failed")
}

var _ stt.STT = (*STT)(nil)
var _ stt.InputEnding = (*recognizeStream)(nil)
