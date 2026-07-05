package rime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

const (
	defaultRimeHTTPBaseURL   = "https://users.rime.ai/v1/rime-tts"
	defaultRimeWSBaseURL     = "wss://users-ws.rime.ai"
	defaultRimeModel         = "arcana"
	defaultRimeArcanaVoice   = "astra"
	defaultRimeMistVoice     = "cove"
	defaultRimeCodaVoice     = "lyra"
	defaultRimeLang          = "eng"
	defaultRimeSampleRate    = 22050
	defaultRimeSegment       = "bySentence"
	defaultRimeStreamTimeout = 10 * time.Second
	rimeArcanaModelTimeout   = 240 * time.Second
	rimeMistModelTimeout     = 30 * time.Second
)

type RimeTTS struct {
	mu                       sync.Mutex
	streams                  map[*rimeTTSSynthesizeStream]struct{}
	apiKey                   string
	baseURL                  string
	model                    string
	voice                    string
	lang                     string
	sampleRate               int
	requestSampleRate        int
	timeScaleFactor          *float64
	repetitionPenalty        *float64
	temperature              *float64
	topP                     *float64
	maxTokens                *int
	speedAlpha               *float64
	reduceLatency            *bool
	pauseBetweenBrackets     *bool
	phonemizeBetweenBrackets *bool
	useWebsocket             bool
	segment                  string
	streamResponseTimeout    time.Duration
	closed                   bool
}

type RimeTTSOption func(*RimeTTS)

func WithRimeTTSBaseURL(baseURL string) RimeTTSOption {
	return func(t *RimeTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
			if strings.HasPrefix(baseURL, "ws://") || strings.HasPrefix(baseURL, "wss://") {
				t.useWebsocket = true
			}
		}
	}
}

func WithRimeTTSModel(model string) RimeTTSOption {
	return func(t *RimeTTS) {
		if model != "" {
			t.model = model
			if t.voice == "" {
				t.voice = defaultRimeVoice(model)
			}
		}
	}
}

func WithRimeTTSVoice(voice string) RimeTTSOption {
	return func(t *RimeTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithRimeTTSSampleRate(sampleRate int) RimeTTSOption {
	return func(t *RimeTTS) {
		if sampleRate > 0 {
			t.requestSampleRate = sampleRate
		}
	}
}

func WithRimeTTSLang(lang string) RimeTTSOption {
	return func(t *RimeTTS) {
		if lang != "" {
			t.lang = lang
		}
	}
}

func WithRimeTTSTimeScaleFactor(timeScaleFactor float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.timeScaleFactor = &timeScaleFactor
	}
}

func WithRimeTTSRepetitionPenalty(repetitionPenalty float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.repetitionPenalty = &repetitionPenalty
	}
}

func WithRimeTTSTemperature(temperature float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.temperature = &temperature
	}
}

func WithRimeTTSTopP(topP float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.topP = &topP
	}
}

func WithRimeTTSMaxTokens(maxTokens int) RimeTTSOption {
	return func(t *RimeTTS) {
		t.maxTokens = &maxTokens
	}
}

func WithRimeTTSSpeedAlpha(speedAlpha float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.speedAlpha = &speedAlpha
	}
}

func WithRimeTTSReduceLatency(reduceLatency bool) RimeTTSOption {
	return func(t *RimeTTS) {
		t.reduceLatency = &reduceLatency
	}
}

func WithRimeTTSPauseBetweenBrackets(pauseBetweenBrackets bool) RimeTTSOption {
	return func(t *RimeTTS) {
		t.pauseBetweenBrackets = &pauseBetweenBrackets
	}
}

func WithRimeTTSPhonemizeBetweenBrackets(phonemizeBetweenBrackets bool) RimeTTSOption {
	return func(t *RimeTTS) {
		t.phonemizeBetweenBrackets = &phonemizeBetweenBrackets
	}
}

func WithRimeTTSWebsocket(useWebsocket bool) RimeTTSOption {
	return func(t *RimeTTS) {
		t.useWebsocket = useWebsocket
	}
}

func WithRimeTTSSegment(segment string) RimeTTSOption {
	return func(t *RimeTTS) {
		if segment != "" {
			t.segment = segment
		}
	}
}

func WithRimeTTSStreamResponseTimeout(timeout time.Duration) RimeTTSOption {
	return func(t *RimeTTS) {
		if timeout >= 0 {
			t.streamResponseTimeout = timeout
		}
	}
}

func NewRimeTTS(apiKey string, voice string, opts ...RimeTTSOption) *RimeTTS {
	if apiKey == "" {
		apiKey = os.Getenv("RIME_API_KEY")
	}
	provider := &RimeTTS{
		apiKey:                apiKey,
		baseURL:               defaultRimeHTTPBaseURL,
		model:                 defaultRimeModel,
		lang:                  defaultRimeLang,
		sampleRate:            defaultRimeSampleRate,
		requestSampleRate:     defaultRimeSampleRate,
		segment:               defaultRimeSegment,
		streamResponseTimeout: defaultRimeStreamTimeout,
	}
	for _, opt := range opts {
		opt(provider)
	}
	provider.sampleRate = provider.requestSampleRate
	normalizeRimeTransportBaseURL(provider)
	if voice != "" {
		provider.voice = voice
	}
	if provider.voice == "" {
		voice = defaultRimeVoice(provider.model)
		provider.voice = voice
	}
	return provider
}

func defaultRimeVoice(model string) string {
	switch {
	case model == "coda":
		return defaultRimeCodaVoice
	case strings.Contains(model, "mist"):
		return defaultRimeMistVoice
	default:
		return defaultRimeArcanaVoice
	}
}

func (t *RimeTTS) Label() string { return "rime.TTS" }
func (t *RimeTTS) Model() string { return t.model }
func (t *RimeTTS) Provider() string {
	return "Rime"
}

func (t *RimeTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: t.useWebsocket, AlignedTranscript: t.useWebsocket}
}
func (t *RimeTTS) SampleRate() int  { return t.sampleRate }
func (t *RimeTTS) NumChannels() int { return 1 }

func (t *RimeTTS) UpdateOptions(opts ...RimeTTSOption) error {
	if t == nil {
		return io.ErrClosedPipe
	}
	t.mu.Lock()
	currentUseWebsocket := t.useWebsocket
	candidate := &RimeTTS{
		apiKey:                   t.apiKey,
		baseURL:                  t.baseURL,
		model:                    t.model,
		voice:                    t.voice,
		lang:                     t.lang,
		sampleRate:               t.sampleRate,
		requestSampleRate:        t.requestSampleRate,
		timeScaleFactor:          t.timeScaleFactor,
		repetitionPenalty:        t.repetitionPenalty,
		temperature:              t.temperature,
		topP:                     t.topP,
		maxTokens:                t.maxTokens,
		speedAlpha:               t.speedAlpha,
		reduceLatency:            t.reduceLatency,
		pauseBetweenBrackets:     t.pauseBetweenBrackets,
		phonemizeBetweenBrackets: t.phonemizeBetweenBrackets,
		useWebsocket:             t.useWebsocket,
		segment:                  t.segment,
		streamResponseTimeout:    t.streamResponseTimeout,
	}
	t.mu.Unlock()

	for _, opt := range opts {
		opt(candidate)
	}
	if err := validateRimeTimeScaleFactor(candidate); err != nil {
		return err
	}
	candidate.useWebsocket = currentUseWebsocket

	t.mu.Lock()
	defer t.mu.Unlock()
	t.apiKey = candidate.apiKey
	t.baseURL = candidate.baseURL
	t.model = candidate.model
	t.voice = candidate.voice
	t.lang = candidate.lang
	t.requestSampleRate = candidate.requestSampleRate
	t.timeScaleFactor = candidate.timeScaleFactor
	t.repetitionPenalty = candidate.repetitionPenalty
	t.temperature = candidate.temperature
	t.topP = candidate.topP
	t.maxTokens = candidate.maxTokens
	t.speedAlpha = candidate.speedAlpha
	t.reduceLatency = candidate.reduceLatency
	t.pauseBetweenBrackets = candidate.pauseBetweenBrackets
	t.phonemizeBetweenBrackets = candidate.phonemizeBetweenBrackets
	t.useWebsocket = candidate.useWebsocket
	t.segment = candidate.segment
	t.streamResponseTimeout = candidate.streamResponseTimeout
	return nil
}

func normalizeRimeTransportBaseURL(t *RimeTTS) {
	if t.useWebsocket && t.baseURL == defaultRimeHTTPBaseURL {
		t.baseURL = defaultRimeWSBaseURL
		return
	}
	if !t.useWebsocket && t.baseURL == defaultRimeWSBaseURL {
		t.baseURL = defaultRimeHTTPBaseURL
	}
}

func (t *RimeTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if t.useWebsocket {
		return nil, fmt.Errorf("rime tts one-shot synthesize requires websocket mode disabled")
	}
	if err := validateRimeAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	if _, err := buildRimeTTSRequest(ctx, t, text); err != nil {
		return nil, err
	}

	opts := *t
	return &rimeTTSChunkedStream{
		ctx:        ctx,
		text:       text,
		provider:   t,
		opts:       opts,
		sampleRate: t.sampleRate,
		requestID:  cavosmath.ShortUUID(""),
	}, nil
}

func buildRimeTTSRequest(ctx context.Context, t *RimeTTS, text string) (*http.Request, error) {
	if err := validateRimeTimeScaleFactor(t); err != nil {
		return nil, err
	}
	reqBody := map[string]interface{}{
		"speaker":      t.voice,
		"text":         text,
		"modelId":      t.model,
		"lang":         t.lang,
		"samplingRate": t.requestSampleRate,
	}
	if t.timeScaleFactor != nil {
		reqBody["timeScaleFactor"] = *t.timeScaleFactor
	}
	addRimeModelParams(reqBody, t, true)

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "audio/pcm")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

func (t *RimeTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if !t.useWebsocket {
		return nil, fmt.Errorf("rime tts streaming requires websocket mode enabled")
	}
	if err := validateRimeAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	if err := validateRimeTimeScaleFactor(t); err != nil {
		return nil, err
	}
	dialCtx := ctx
	var cancelDial context.CancelFunc
	if t.streamResponseTimeout > 0 {
		dialCtx, cancelDial = context.WithTimeout(ctx, t.streamResponseTimeout)
		defer cancelDial()
	}
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, buildRimeTTSWebsocketURL(t).String(), buildRimeTTSWebsocketHeaders(t))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial rime tts websocket: %v", err))
	}
	if t.isClosed() {
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &rimeTTSSynthesizeStream{
		conn:      conn,
		ctx:       streamCtx,
		cancel:    cancel,
		provider:  t,
		requestID: cavosmath.ShortUUID(""),
		contextID: cavosmath.ShortUUID(""),
		events:    make(chan *tts.SynthesizedAudio, 100),
		errCh:     make(chan error, 1),
	}
	stream.writeMessage = stream.writeWebsocketMessage
	stream.closeConn = stream.closeWebsocketConn
	if !t.registerStream(stream) {
		cancel()
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *RimeTTS) Close() error {
	t.mu.Lock()
	t.closed = true
	streams := make([]*rimeTTSSynthesizeStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.closeFromProvider(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *RimeTTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *RimeTTS) registerStream(stream *rimeTTSSynthesizeStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*rimeTTSSynthesizeStream]struct{})
	}
	t.streams[stream] = struct{}{}
	stream.provider = t
	return true
}

func (t *RimeTTS) unregisterStream(stream *rimeTTSSynthesizeStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func validateRimeAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("rime API key is required, either as argument or set RIME_API_KEY environmental variable")
	}
	return nil
}

func validateRimeTimeScaleFactor(t *RimeTTS) error {
	if t.model == "mistv2" && t.timeScaleFactor != nil {
		return fmt.Errorf("time_scale_factor is not supported by the mistv2 model; use arcana, mistv3, or coda")
	}
	return nil
}

func buildRimeTTSWebsocketURL(t *RimeTTS) *url.URL {
	wsURL, err := url.Parse(strings.TrimRight(t.baseURL, "/") + "/ws3")
	if err != nil {
		wsURL = &url.URL{Scheme: "wss", Host: strings.TrimPrefix(t.baseURL, "wss://"), Path: "/ws3"}
	}
	query := wsURL.Query()
	query.Set("speaker", t.voice)
	query.Set("modelId", t.model)
	query.Set("audioFormat", "pcm")
	query.Set("samplingRate", strconv.Itoa(t.sampleRate))
	query.Set("segment", t.segment)
	if t.lang != "" {
		query.Set("lang", t.lang)
	}
	if t.timeScaleFactor != nil {
		query.Set("timeScaleFactor", strconv.FormatFloat(*t.timeScaleFactor, 'f', -1, 64))
	}
	addRimeModelQueryParams(query, t)
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func addRimeModelParams(params map[string]interface{}, t *RimeTTS, includeHTTPOnly bool) {
	switch {
	case t.model == "arcana":
		if t.repetitionPenalty != nil {
			params["repetition_penalty"] = *t.repetitionPenalty
		}
		if t.temperature != nil {
			params["temperature"] = *t.temperature
		}
		if t.topP != nil {
			params["top_p"] = *t.topP
		}
		if t.maxTokens != nil {
			params["max_tokens"] = *t.maxTokens
		}
	case t.model == "coda":
		if t.maxTokens != nil {
			params["max_tokens"] = *t.maxTokens
		}
	case strings.Contains(t.model, "mist"):
		if t.speedAlpha != nil {
			params["speedAlpha"] = *t.speedAlpha
		}
		if t.pauseBetweenBrackets != nil {
			params["pauseBetweenBrackets"] = *t.pauseBetweenBrackets
		}
		if t.phonemizeBetweenBrackets != nil {
			params["phonemizeBetweenBrackets"] = *t.phonemizeBetweenBrackets
		}
		if includeHTTPOnly && t.model == "mistv2" && t.reduceLatency != nil {
			params["reduceLatency"] = *t.reduceLatency
		}
	}
}

func addRimeModelQueryParams(query url.Values, t *RimeTTS) {
	params := map[string]interface{}{}
	addRimeModelParams(params, t, false)
	for key, value := range params {
		query.Set(key, fmt.Sprint(value))
	}
}

func buildRimeTTSWebsocketHeaders(t *RimeTTS) http.Header {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+t.apiKey)
	return header
}

func buildRimeTTSTextMessage(contextID string, text string) ([]byte, error) {
	if !strings.HasSuffix(text, " ") {
		text += " "
	}
	return json.Marshal(map[string]interface{}{
		"text":      text,
		"contextId": contextID,
	})
}

func buildRimeTTSFlushMessage(contextID string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"operation": "flush",
		"contextId": contextID,
	})
}

func buildRimeTTSEOSMessage() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"operation": "eos",
	})
}

type rimeTTSChunkedStream struct {
	resp         *http.Response
	ctx          context.Context
	cancel       context.CancelFunc
	text         string
	provider     *RimeTTS
	opts         RimeTTS
	sampleRate   int
	requestID    string
	requested    bool
	pendingPCM   []byte
	pendingFinal bool
	pendingErr   error
	finalSent    bool
}

func (s *rimeTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return s.annotateAudio(&tts.SynthesizedAudio{IsFinal: true}), nil
	}
	if s.pendingErr != nil {
		err := s.pendingErr
		s.pendingErr = nil
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	for {
		buf := make([]byte, 4096)
		n, err := s.resp.Body.Read(buf)
		if n > 0 {
			if err == io.EOF {
				s.pendingFinal = true
			} else if err != nil {
				s.pendingErr = rimeTTSReadBodyError(err)
			}
			frameData := rimeTTSPCMFrameData(&s.pendingPCM, buf[:n])
			if len(frameData) == 0 {
				if s.pendingFinal {
					s.pendingPCM = nil
					s.pendingFinal = false
					s.finalSent = true
					return s.annotateAudio(&tts.SynthesizedAudio{IsFinal: true}), nil
				}
				if s.pendingErr != nil {
					err := s.pendingErr
					s.pendingErr = nil
					return nil, err
				}
				continue
			}
			return s.annotateAudio(&tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              frameData,
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(len(frameData) / 2),
				},
			}), nil
		}
		if err != nil {
			if err == io.EOF {
				s.pendingPCM = nil
				if !s.finalSent {
					s.finalSent = true
					return s.annotateAudio(&tts.SynthesizedAudio{IsFinal: true}), nil
				}
				return nil, io.EOF
			}
			return nil, rimeTTSReadBodyError(err)
		}
	}
}

func rimeTTSReadBodyError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(err.Error())
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return llm.NewAPITimeoutError(err.Error())
	}
	return rimeTTSConnectionError("Rime TTS stream read failed", err)
}

func rimeTTSPCMFrameData(pending *[]byte, data []byte) []byte {
	if len(*pending) > 0 {
		combined := make([]byte, 0, len(*pending)+len(data))
		combined = append(combined, (*pending)...)
		combined = append(combined, data...)
		data = combined
		*pending = nil
	}
	if len(data)%2 != 0 {
		*pending = append((*pending)[:0], data[len(data)-1])
		data = data[:len(data)-1]
	}
	if len(data) == 0 {
		return nil
	}
	return bytes.Clone(data)
}

func (s *rimeTTSChunkedStream) annotateAudio(audio *tts.SynthesizedAudio) *tts.SynthesizedAudio {
	if audio == nil {
		return nil
	}
	if s.requestID == "" {
		s.requestID = cavosmath.ShortUUID("")
	}
	audio.RequestID = s.requestID
	audio.SegmentID = ""
	return audio
}

func (s *rimeTTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.requested || s.text == "" {
		return nil
	}
	s.requested = true
	if s.provider != nil {
		s.provider.mu.Lock()
		s.opts.baseURL = s.provider.baseURL
		timeout := rimeTTSTotalTimeout(s.provider.model)
		s.provider.mu.Unlock()
		requestCtx, cancel := context.WithTimeout(s.ctx, timeout)
		return s.openResponse(requestCtx, cancel)
	}
	requestCtx, cancel := context.WithTimeout(s.ctx, rimeTTSTotalTimeout(s.opts.model))
	return s.openResponse(requestCtx, cancel)
}

func (s *rimeTTSChunkedStream) openResponse(requestCtx context.Context, cancel context.CancelFunc) error {
	req, err := buildRimeTTSRequest(requestCtx, &s.opts, s.text)
	if err != nil {
		cancel()
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return rimeTTSConnectionError("Rime TTS request failed", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if resp.StatusCode == 499 {
			resp.Body.Close()
			cancel()
			return nil
		}
		resp.Body.Close()
		cancel()
		message := http.StatusText(resp.StatusCode)
		if message == "" {
			message = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return llm.NewAPIStatusError(message, resp.StatusCode, "", nil)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "audio") {
		resp.Body.Close()
		cancel()
		if strings.TrimSpace(s.text) != "" {
			s.pendingErr = llm.NewAPIError(fmt.Sprintf("no audio frames were pushed for text: %s", s.text), nil, true)
		}
		return nil
	}

	s.resp = resp
	s.cancel = cancel
	return nil
}

func (s *rimeTTSChunkedStream) Close() error {
	s.finalSent = true
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	return body.Close()
}

func rimeTTSTotalTimeout(model string) time.Duration {
	if model == "arcana" || model == "coda" {
		return rimeArcanaModelTimeout
	}
	return rimeMistModelTimeout
}

type rimeTTSSynthesizeStream struct {
	conn                  *websocket.Conn
	ctx                   context.Context
	cancel                context.CancelFunc
	provider              *RimeTTS
	requestID             string
	contextID             string
	events                chan *tts.SynthesizedAudio
	errCh                 chan error
	mu                    sync.Mutex
	closed                bool
	started               bool
	readStarted           bool
	pendingText           string
	pushedText            string
	audioSeen             bool
	pendingPCM            []byte
	pendingTranscriptText string
	pendingTranscript     []tts.TimedString
	segmentDone           bool
	inputEnded            bool

	writeMessage func(int, []byte) error
	closeConn    func() error
}

func (s *rimeTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return nil
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.segmentDone {
		return nil
	}
	s.pendingText += text
	if err := s.sendCompleteSentencesLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *rimeTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return nil
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.flushLocked(false)
}

func (s *rimeTTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return nil
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if err := s.flushLocked(true); err != nil {
		return err
	}
	s.inputEnded = true
	if !s.started {
		s.closed = true
		s.cancel()
		if s.events != nil {
			close(s.events)
		}
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return s.closeConnection()
	}
	return nil
}

func (s *rimeTTSSynthesizeStream) flushLocked(sendProviderFlush bool) error {
	if s.pendingText != "" {
		text := strings.Join(tokenize.NewBasicSentenceTokenizer().Tokenize(s.pendingText, ""), " ")
		s.pendingText = ""
		if err := s.sendSentenceLocked(text); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
	}
	if !s.started {
		return nil
	}
	if !sendProviderFlush {
		s.segmentDone = true
		return nil
	}
	message, err := buildRimeTTSFlushMessage(s.contextID)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(websocket.TextMessage, message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *rimeTTSSynthesizeStream) sendCompleteSentencesLocked() error {
	for {
		tokens := tokenize.NewBasicSentenceTokenizer().Tokenize(s.pendingText, "")
		if len(tokens) <= 1 {
			return nil
		}
		sentence := tokens[0]
		if err := s.sendSentenceLocked(sentence); err != nil {
			return err
		}
		tokenIdx := strings.Index(s.pendingText, sentence)
		if tokenIdx < 0 {
			s.pendingText = strings.TrimSpace(strings.TrimPrefix(s.pendingText, sentence))
			continue
		}
		s.pendingText = strings.TrimLeftFunc(s.pendingText[tokenIdx+len(sentence):], func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r'
		})
	}
}

func (s *rimeTTSSynthesizeStream) sendSentenceLocked(text string) error {
	if text == "" {
		return nil
	}
	message, err := buildRimeTTSTextMessage(s.contextID, text)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(websocket.TextMessage, message); err != nil {
		return err
	}
	if s.pushedText != "" {
		s.pushedText += " "
	}
	s.pushedText += text
	s.started = true
	s.startReadLoopLocked()
	return nil
}

func (s *rimeTTSSynthesizeStream) startReadLoopLocked() {
	if s.readStarted || s.conn == nil || s.events == nil || s.errCh == nil {
		return
	}
	s.readStarted = true
	go s.readLoop()
}

func (s *rimeTTSSynthesizeStream) Close() error {
	return s.close(false)
}

func (s *rimeTTSSynthesizeStream) closeFromProvider() error {
	return s.close(true)
}

func (s *rimeTTSSynthesizeStream) close(sendEOS bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	defer func() {
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
	}()
	if sendEOS {
		message, err := buildRimeTTSEOSMessage()
		if err != nil {
			return err
		}
		if err := s.writeMessageData(websocket.TextMessage, message); err != nil {
			if closeErr := s.closeConnection(); closeErr != nil {
				return closeErr
			}
			return err
		}
	}
	if s.conn != nil {
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}
	return s.closeConnection()
}

func (s *rimeTTSSynthesizeStream) writeMessageData(messageType int, data []byte) error {
	if s.writeMessage != nil {
		return s.writeMessage(messageType, data)
	}
	return s.writeWebsocketMessage(messageType, data)
}

func (s *rimeTTSSynthesizeStream) writeWebsocketMessage(messageType int, data []byte) error {
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	return s.conn.WriteMessage(messageType, data)
}

func (s *rimeTTSSynthesizeStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *rimeTTSSynthesizeStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *rimeTTSSynthesizeStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	s.cancel()
	_ = s.closeConnection()
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
}

func (s *rimeTTSSynthesizeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *rimeTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	if s.isClosed() {
		return nil, io.EOF
	}
	select {
	case audio, ok := <-s.events:
		if ok {
			return audio, nil
		}
		select {
		case err := <-s.errCh:
			return nil, err
		default:
			return nil, io.EOF
		}
	default:
	}
	select {
	case audio, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return audio, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.ctx.Done():
		if s.isClosed() {
			return nil, io.EOF
		}
		return nil, s.ctx.Err()
	}
}

func (s *rimeTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		if s.provider != nil && s.provider.streamResponseTimeout > 0 {
			timeout := s.provider.streamResponseTimeout
			_ = s.conn.SetReadDeadline(time.Now().Add(timeout))
		}
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !s.isClosed() {
				s.errCh <- rimeTTSReadErrorWithRequestID(err, s.requestID)
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, transcript, err := rimeTTSAudioFromWebsocketMessage(payload, s.provider.sampleRate)
		if err != nil {
			s.errCh <- err
			return
		}
		if audio != nil && audio.Frame != nil {
			frameData := rimeTTSPCMFrameData(&s.pendingPCM, audio.Frame.Data)
			if len(frameData) == 0 {
				audio = nil
			} else {
				audio.Frame.Data = frameData
				audio.Frame.SamplesPerChannel = uint32(len(frameData) / 2)
			}
		}
		if done {
			s.pendingPCM = nil
		}
		hasAudio := audio != nil && audio.Frame != nil && len(audio.Frame.Data) > 0
		s.mu.Lock()
		if hasAudio {
			s.audioSeen = true
		}
		audioSeen := s.audioSeen
		pushedText := s.pushedText
		s.mu.Unlock()
		if done && !audioSeen && strings.TrimSpace(pushedText) != "" {
			s.errCh <- llm.NewAPIError(fmt.Sprintf("no audio frames were pushed for text: %s", pushedText), nil, true)
			return
		}
		if audio != nil && audio.Frame == nil && len(audio.TimedTranscript) > 0 {
			s.pendingTranscriptText += audio.DeltaText
			s.pendingTranscript = append(s.pendingTranscript, audio.TimedTranscript...)
			audio = nil
		}
		if audio != nil {
			if s.pendingTranscriptText != "" && audio.DeltaText == "" {
				audio.DeltaText = s.pendingTranscriptText
				s.pendingTranscriptText = ""
			}
			if len(s.pendingTranscript) > 0 && len(audio.TimedTranscript) == 0 {
				audio.TimedTranscript = append(audio.TimedTranscript, s.pendingTranscript...)
				s.pendingTranscript = nil
			}
			s.annotateAudio(audio)
			s.events <- audio
		}
		if transcript != "" {
			audio := &tts.SynthesizedAudio{DeltaText: transcript}
			s.annotateAudio(audio)
			s.events <- audio
		}
		if done {
			return
		}
	}
}

func (s *rimeTTSSynthesizeStream) annotateAudio(audio *tts.SynthesizedAudio) {
	if audio == nil {
		return
	}
	audio.RequestID = s.requestID
	audio.SegmentID = s.contextID
}

func rimeTTSReadError(err error) error {
	return rimeTTSReadErrorWithRequestID(err, "")
}

func rimeTTSReadErrorWithRequestID(err error, requestID string) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(err.Error())
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return llm.NewAPITimeoutError(err.Error())
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return llm.NewAPIStatusError("Rime ws closed unexpectedly", 0, requestID, nil)
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("Rime WS error: %v", err))
}

func rimeTTSAudioFromWebsocketMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, string, error) {
	var message struct {
		Type           string `json:"type"`
		Data           string `json:"data"`
		Message        string `json:"message"`
		WordTimestamps struct {
			Words []string  `json:"words"`
			Start []float64 `json:"start"`
			End   []float64 `json:"end"`
		} `json:"word_timestamps"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, "", rimeTTSConnectionError("Rime websocket payload decode failed", err)
	}
	switch message.Type {
	case "chunk":
		if message.Data == "" {
			return nil, false, "", nil
		}
		audio, err := base64.StdEncoding.DecodeString(message.Data)
		if err != nil {
			return nil, false, "", rimeTTSConnectionError("Rime websocket audio decode failed", err)
		}
		if len(audio) == 0 {
			return nil, false, "", nil
		}
		return rimeTTSAudioFrame(audio, sampleRate), false, "", nil
	case "timestamps":
		timed := rimeTTSTimedTranscript(message.WordTimestamps.Words, message.WordTimestamps.Start, message.WordTimestamps.End)
		if len(timed) == 0 {
			return nil, false, "", nil
		}
		return &tts.SynthesizedAudio{
			DeltaText:       rimeTTSTimedTranscriptText(timed),
			TimedTranscript: timed,
		}, false, "", nil
	case "done":
		return &tts.SynthesizedAudio{IsFinal: true}, true, "", nil
	case "error":
		if message.Message == "" {
			message.Message = "(no message)"
		}
		return nil, false, "", llm.NewAPIError("Rime ws error: "+message.Message, nil, true)
	default:
		return nil, false, "", nil
	}
}

func rimeTTSConnectionError(message string, err error) *llm.APIConnectionError {
	if err == nil {
		return llm.NewAPIConnectionError(message)
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("%s: %v", message, err))
}

func rimeTTSTimedTranscript(words []string, starts []float64, ends []float64) []tts.TimedString {
	count := min(len(words), len(starts), len(ends))
	if count == 0 {
		return nil
	}
	timed := make([]tts.TimedString, 0, count)
	for i := 0; i < count; i++ {
		timed = append(timed, tts.TimedString{
			Text:      words[i] + " ",
			StartTime: starts[i],
			EndTime:   ends[i],
		})
	}
	return timed
}

func rimeTTSTimedTranscriptText(timed []tts.TimedString) string {
	if len(timed) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, word := range timed {
		builder.WriteString(word.Text)
	}
	return builder.String()
}

func rimeTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
