package deepgram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const defaultDeepgramSTTv2BaseURL = "wss://api.deepgram.com/v2/listen"
const deepgramSTTv2CloseMessage = `{"type": "CloseStream"}`

var deepgramSTTv2HeartbeatInterval = 30 * time.Second

type DeepgramSTTv2 struct {
	apiKey     string
	model      string
	sampleRate int
	baseURL    string
	mipOptOut  bool
	language   string
	eagerEOT   float64
	eot        float64
	eotTimeout int
	keyterms   []string
	tags       []string
	langHints  []string
	mu         sync.Mutex
	streams    map[*deepgramV2Stream]struct{}
	closed     bool
}

type DeepgramSTTv2Option func(*DeepgramSTTv2)

func NewDeepgramSTTv2(apiKey string, opts ...DeepgramSTTv2Option) *DeepgramSTTv2 {
	if apiKey == "" {
		apiKey = os.Getenv("DEEPGRAM_API_KEY")
	}
	provider := &DeepgramSTTv2{
		apiKey:     apiKey,
		model:      "flux-general-en",
		sampleRate: 16000,
		baseURL:    defaultDeepgramSTTv2BaseURL,
		language:   "en",
		streams:    make(map[*deepgramV2Stream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func WithDeepgramSTTv2BaseURL(baseURL string) DeepgramSTTv2Option {
	return func(s *DeepgramSTTv2) {
		if baseURL != "" {
			s.baseURL = baseURL
		}
	}
}

func WithDeepgramSTTv2Model(model string) DeepgramSTTv2Option {
	return func(s *DeepgramSTTv2) {
		if model != "" {
			s.model = model
		}
	}
}

func WithDeepgramSTTv2SampleRate(sampleRate int) DeepgramSTTv2Option {
	return func(s *DeepgramSTTv2) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithDeepgramSTTv2MipOptOut(mipOptOut bool) DeepgramSTTv2Option {
	return func(s *DeepgramSTTv2) {
		s.mipOptOut = mipOptOut
	}
}

func WithDeepgramSTTv2EagerEOTThreshold(threshold float64) DeepgramSTTv2Option {
	return func(s *DeepgramSTTv2) {
		if threshold > 0 {
			s.eagerEOT = threshold
		}
	}
}

func WithDeepgramSTTv2EOTThreshold(threshold float64) DeepgramSTTv2Option {
	return func(s *DeepgramSTTv2) {
		if threshold > 0 {
			s.eot = threshold
		}
	}
}

func WithDeepgramSTTv2EOTTimeout(timeoutMS int) DeepgramSTTv2Option {
	return func(s *DeepgramSTTv2) {
		if timeoutMS > 0 {
			s.eotTimeout = timeoutMS
		}
	}
}

func WithDeepgramSTTv2Keyterms(keyterms []string) DeepgramSTTv2Option {
	return func(s *DeepgramSTTv2) {
		s.keyterms = append([]string(nil), keyterms...)
	}
}

func WithDeepgramSTTv2Tags(tags []string) DeepgramSTTv2Option {
	return func(s *DeepgramSTTv2) {
		s.tags = append([]string(nil), tags...)
	}
}

func WithDeepgramSTTv2LanguageHints(hints []string) DeepgramSTTv2Option {
	return func(s *DeepgramSTTv2) {
		s.langHints = append([]string(nil), hints...)
	}
}

func (s *DeepgramSTTv2) Label() string { return "deepgram.STTv2" }

func (s *DeepgramSTTv2) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    true,
		AlignedTranscript: "word",
		OfflineRecognize:  false,
	}
}

func (s *DeepgramSTTv2) InputSampleRate() uint32 { return uint32(s.sampleRate) }
func (s *DeepgramSTTv2) Model() string           { return s.model }
func (s *DeepgramSTTv2) Provider() string        { return "Deepgram" }

func (s *DeepgramSTTv2) Recognize(context.Context, []*model.AudioFrame, string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("V2 API does not support non-streaming recognize. Use with a StreamAdapter")
}

func (s *DeepgramSTTv2) UpdateOptions(opts ...DeepgramSTTv2Option) error {
	s.mu.Lock()
	next := &DeepgramSTTv2{
		apiKey:     s.apiKey,
		model:      s.model,
		sampleRate: s.sampleRate,
		baseURL:    s.baseURL,
		mipOptOut:  s.mipOptOut,
		language:   s.language,
		eagerEOT:   s.eagerEOT,
		eot:        s.eot,
		eotTimeout: s.eotTimeout,
		keyterms:   append([]string(nil), s.keyterms...),
		tags:       append([]string(nil), s.tags...),
		langHints:  append([]string(nil), s.langHints...),
	}
	for _, opt := range opts {
		opt(next)
	}
	if err := validateDeepgramSTTv2Options(next); err != nil {
		s.mu.Unlock()
		return err
	}

	s.model = next.model
	s.sampleRate = next.sampleRate
	s.baseURL = next.baseURL
	s.mipOptOut = next.mipOptOut
	s.language = next.language
	s.eagerEOT = next.eagerEOT
	s.eot = next.eot
	s.eotTimeout = next.eotTimeout
	s.keyterms = next.keyterms
	s.tags = next.tags
	s.langHints = next.langHints
	streams := make([]*deepgramV2Stream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()

	for _, stream := range streams {
		stream.updateOptions()
	}
	return nil
}

func (s *DeepgramSTTv2) Stream(ctx context.Context, _ string) (stt.RecognizeStream, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if s.apiKey == "" {
		return nil, fmt.Errorf("Deepgram API key is required")
	}
	if err := validateDeepgramSTTv2Options(s); err != nil {
		return nil, err
	}
	header := make(http.Header)
	header.Set("Authorization", "Token "+s.apiKey)
	streamURL := buildDeepgramSTTv2StreamURL(s)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, streamURL, header)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, llm.NewAPIConnectionError("failed to connect to deepgram")
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &deepgramV2Stream{
		provider:   s,
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		streamURL:  streamURL,
		events:     make(chan *stt.SpeechEvent, 100),
		errCh:      make(chan error, 1),
		language:   s.language,
		sampleRate: s.sampleRate,
	}
	if !s.registerStream(stream) {
		_ = conn.Close()
		cancel()
		return nil, io.ErrClosedPipe
	}
	go stream.readLoop(conn)
	go stream.heartbeatLoop()
	return stream, nil
}

func (s *DeepgramSTTv2) Close() error {
	s.mu.Lock()
	s.closed = true
	streams := make([]*deepgramV2Stream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streams = make(map[*deepgramV2Stream]struct{})
	s.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (s *DeepgramSTTv2) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *DeepgramSTTv2) registerStream(stream *deepgramV2Stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.streams[stream] = struct{}{}
	return true
}

func (s *DeepgramSTTv2) unregisterStream(stream *deepgramV2Stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func buildDeepgramSTTv2StreamURL(s *DeepgramSTTv2) string {
	u, err := url.Parse(s.baseURL)
	if err != nil {
		u, _ = url.Parse(defaultDeepgramSTTv2BaseURL)
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme == "http" {
		u.Scheme = "ws"
	}
	q := u.Query()
	q.Set("model", s.model)
	q.Set("sample_rate", strconv.Itoa(s.sampleRate))
	q.Set("encoding", "linear16")
	q.Set("mip_opt_out", strconv.FormatBool(s.mipOptOut))
	if s.eagerEOT > 0 {
		q.Set("eager_eot_threshold", strconv.FormatFloat(s.eagerEOT, 'f', -1, 64))
	}
	if s.eot > 0 {
		q.Set("eot_threshold", strconv.FormatFloat(s.eot, 'f', -1, 64))
	}
	if s.eotTimeout > 0 {
		q.Set("eot_timeout_ms", strconv.Itoa(s.eotTimeout))
	}
	for _, keyterm := range s.keyterms {
		if keyterm != "" {
			q.Add("keyterm", keyterm)
		}
	}
	for _, tag := range s.tags {
		if tag != "" {
			q.Add("tag", tag)
		}
	}
	for _, hint := range s.langHints {
		if hint != "" {
			q.Add("language_hint", hint)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func validateDeepgramSTTv2Options(s *DeepgramSTTv2) error {
	eot := s.eot
	if eot == 0 {
		eot = 0.7
	}
	if s.eagerEOT > 0 && s.eagerEOT > eot {
		return fmt.Errorf("eager_eot_threshold (%v) must be less than or equal to eot_threshold (%v)", s.eagerEOT, eot)
	}
	for _, tag := range s.tags {
		if len(tag) > 128 {
			return fmt.Errorf("tag must be no more than 128 characters")
		}
	}
	return nil
}

type deepgramV2Stream struct {
	provider       *DeepgramSTTv2
	conn           *websocket.Conn
	events         chan *stt.SpeechEvent
	errCh          chan error
	mu             sync.Mutex
	closed         bool
	inputEnded     bool
	speaking       bool
	requestID      string
	language       string
	start          float64
	offset         float64
	sampleRate     int
	rateGuard      stt.SampleRateGuard
	inputAudio     deepgramSTTInputAudioNormalizer
	audioBStream   *audio.AudioByteStream
	streamURL      string
	reconnectNext  bool
	usageTotal     float64
	usageLastFlush time.Time
	ctx            context.Context
	cancel         context.CancelFunc
}

type deepgramV2Response struct {
	Type             string           `json:"type"`
	Event            string           `json:"event"`
	RequestID        string           `json:"request_id"`
	Transcript       string           `json:"transcript"`
	AudioWindowStart float64          `json:"audio_window_start"`
	AudioWindowEnd   float64          `json:"audio_window_end"`
	Languages        []string         `json:"languages"`
	Words            []deepgramV2Word `json:"words"`
	Description      string           `json:"description"`
}

type deepgramV2Word struct {
	Word       string  `json:"word"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
}

func (s *deepgramV2Stream) readLoop(conn *websocket.Conn) {
	defer func() {
		s.mu.Lock()
		stale := conn != s.conn
		if !stale && !s.closed {
			s.closed = true
			if s.cancel != nil {
				s.cancel()
			}
			if s.provider != nil {
				s.provider.unregisterStream(s)
			}
			if s.conn != nil {
				_ = s.conn.Close()
			}
		}
		s.mu.Unlock()
		if !stale {
			close(s.events)
		}
	}()

	for {
		msgType, message, err := conn.ReadMessage()
		if err != nil {
			if s.isCurrentConn(conn) && !s.isClosed() && !s.hasInputEnded() {
				s.sendError(deepgramSTTUnexpectedCloseError(err))
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var resp deepgramV2Response
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}
		if err := s.processEvent(resp); err != nil {
			continue
		}
	}
}

func (s *deepgramV2Stream) heartbeatLoop() {
	ticker := time.NewTicker(deepgramSTTv2HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done():
			return
		case <-ticker.C:
			if err := s.sendHeartbeat(); err != nil {
				s.sendError(err)
				return
			}
		}
	}
}

func (s *deepgramV2Stream) sendHeartbeat() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.conn == nil {
		return nil
	}
	return s.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))
}

func (s *deepgramV2Stream) processEvent(resp deepgramV2Response) error {
	if resp.RequestID != "" {
		s.requestID = resp.RequestID
	}

	switch resp.Type {
	case "TurnInfo":
		return s.processTurnInfo(resp)
	case "Error":
		description := resp.Description
		if description == "" {
			description = "unknown error from deepgram"
		}
		return llm.NewAPIStatusError(description, -1, "", "")
	default:
		return nil
	}
}

func (s *deepgramV2Stream) processTurnInfo(resp deepgramV2Response) error {
	switch resp.Event {
	case "StartOfTurn":
		if s.speaking {
			return nil
		}
		s.speaking = true
		s.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
		s.sendTranscriptEvent(stt.SpeechEventInterimTranscript, resp)
	case "Update":
		if !s.speaking {
			return nil
		}
		s.sendTranscriptEvent(stt.SpeechEventInterimTranscript, resp)
	case "EagerEndOfTurn":
		if !s.speaking {
			return nil
		}
		s.sendTranscriptEvent(stt.SpeechEventPreflightTranscript, resp)
	case "TurnResumed":
		s.sendTranscriptEvent(stt.SpeechEventInterimTranscript, resp)
	case "EndOfTurn":
		if !s.speaking {
			return nil
		}
		s.speaking = false
		s.sendTranscriptEvent(stt.SpeechEventFinalTranscript, resp)
		s.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
	}
	return nil
}

func (s *deepgramV2Stream) sendTranscriptEvent(eventType stt.SpeechEventType, resp deepgramV2Response) {
	alts := deepgramV2SpeechData(s.language, resp, s.offset)
	if len(alts) == 0 {
		return
	}
	s.sendEvent(&stt.SpeechEvent{
		Type:         eventType,
		RequestID:    s.requestID,
		Alternatives: alts,
	})
}

func deepgramV2HasTranscript(resp deepgramV2Response) bool {
	return len(resp.Words) > 0
}

func deepgramV2SpeechData(language string, resp deepgramV2Response, startTimeOffset float64) []stt.SpeechData {
	if !deepgramV2HasTranscript(resp) {
		return nil
	}
	confidence := 0.0
	words := make([]stt.TimedString, 0, len(resp.Words))
	for _, word := range resp.Words {
		confidence += word.Confidence
		words = append(words, stt.TimedString{
			Text:            word.Word,
			StartTime:       word.Start + startTimeOffset,
			EndTime:         word.End + startTimeOffset,
			Confidence:      word.Confidence,
			StartTimeOffset: startTimeOffset,
		})
	}
	if len(resp.Words) > 0 {
		confidence /= float64(len(resp.Words))
	}
	if len(resp.Languages) > 0 {
		language = resp.Languages[0]
	}
	return []stt.SpeechData{{
		Language:        language,
		Text:            resp.Transcript,
		StartTime:       resp.AudioWindowStart + startTimeOffset,
		EndTime:         resp.AudioWindowEnd + startTimeOffset,
		Confidence:      confidence,
		Words:           words,
		SourceLanguages: append([]string(nil), resp.Languages...),
	}}
}

func (s *deepgramV2Stream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.reconnectNext {
		if err := s.reconnectLocked(); err != nil {
			s.closed = true
			return err
		}
		s.reconnectNext = false
	}
	if err := s.rateGuard.Check(frame); err != nil {
		return err
	}
	normalizedFrame, err := s.inputAudio.normalize(frame, uint32(s.sampleRate))
	if err != nil {
		return err
	}
	if s.audioBStream == nil {
		s.audioBStream = audio.NewAudioByteStream(uint32(s.sampleRate), 1, uint32(s.sampleRate/20))
	}
	for _, chunk := range s.audioBStream.Push(normalizedFrame.Data) {
		if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk.Data); err != nil {
			s.closed = true
			return err
		}
		s.sendRecognitionUsage(chunk)
	}
	return nil
}

func (s *deepgramV2Stream) updateOptions() {
	s.mu.Lock()
	if s.closed || s.provider == nil {
		s.mu.Unlock()
		return
	}
	nextURL := buildDeepgramSTTv2StreamURL(s.provider)
	s.streamURL = nextURL
	s.reconnectNext = true
	reconnectNow := s.conn != nil
	s.sampleRate = s.provider.sampleRate
	s.audioBStream = nil
	s.mu.Unlock()

	if reconnectNow {
		go s.reconnectNow()
	}
}

func (s *deepgramV2Stream) reconnectLocked() error {
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.provider == nil {
		return nil
	}
	header := make(http.Header)
	header.Set("Authorization", "Token "+s.provider.apiKey)
	conn, _, err := websocket.DefaultDialer.DialContext(s.ctx, s.streamURL, header)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return llm.NewAPIConnectionError("failed to connect to deepgram")
	}
	oldConn := s.conn
	s.conn = conn
	if oldConn != nil {
		_ = oldConn.Close()
	}
	go s.readLoop(conn)
	return nil
}

func (s *deepgramV2Stream) reconnectNow() {
	s.mu.Lock()
	if s.closed || !s.reconnectNext {
		s.mu.Unlock()
		return
	}
	err := s.reconnectLocked()
	if err == nil {
		s.reconnectNext = false
	} else {
		s.closed = true
		if s.cancel != nil {
			s.cancel()
		}
		if s.conn != nil {
			_ = s.conn.Close()
		}
	}
	s.mu.Unlock()

	if err != nil {
		s.sendError(err)
	}
}

func (s *deepgramV2Stream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.audioBStream == nil {
		return nil
	}
	flushedFrame := false
	if tail := s.inputAudio.flush(); tail != nil {
		for _, chunk := range s.audioBStream.Push(tail.Data) {
			flushedFrame = true
			if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk.Data); err != nil {
				s.closed = true
				return err
			}
			s.sendRecognitionUsage(chunk)
		}
	}
	for _, chunk := range s.audioBStream.Flush() {
		flushedFrame = true
		if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk.Data); err != nil {
			s.closed = true
			return err
		}
		s.sendRecognitionUsage(chunk)
	}
	if flushedFrame {
		s.flushRecognitionUsageLocked()
	}
	return nil
}

func (s *deepgramV2Stream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if tail := s.inputAudio.flush(); tail != nil {
		if s.audioBStream == nil {
			s.audioBStream = audio.NewAudioByteStream(uint32(s.sampleRate), 1, uint32(s.sampleRate/20))
		}
		for _, chunk := range s.audioBStream.Push(tail.Data) {
			if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk.Data); err != nil {
				s.closed = true
				return err
			}
			s.sendRecognitionUsage(chunk)
		}
	}
	if s.audioBStream != nil {
		for _, chunk := range s.audioBStream.Flush() {
			if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk.Data); err != nil {
				s.closed = true
				return err
			}
			s.sendRecognitionUsage(chunk)
		}
		s.flushRecognitionUsageLocked()
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, []byte(deepgramSTTv2CloseMessage)); err != nil {
		s.closed = true
		return err
	}
	s.inputEnded = true
	return nil
}

func (s *deepgramV2Stream) sendRecognitionUsage(frame *model.AudioFrame) {
	if s.ctx == nil || s.events == nil || frame == nil {
		return
	}
	duration := audio.CalculateFrameDuration(frame)
	if duration <= 0 {
		return
	}
	s.usageTotal += duration
	if s.usageLastFlush.IsZero() {
		s.usageLastFlush = time.Now()
		return
	}
	if time.Since(s.usageLastFlush) >= deepgramSTTUsageInterval {
		s.flushRecognitionUsageLocked()
	}
}

func (s *deepgramV2Stream) flushRecognitionUsageLocked() {
	if s.usageTotal <= 0 {
		return
	}
	duration := s.usageTotal
	s.usageTotal = 0
	s.usageLastFlush = time.Now()
	s.sendEvent(&stt.SpeechEvent{
		Type:      stt.SpeechEventRecognitionUsage,
		RequestID: s.requestID,
		RecognitionUsage: &stt.RecognitionUsage{
			AudioDuration: duration,
		},
	})
}

func (s *deepgramV2Stream) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.conn != nil {
		_ = s.conn.WriteMessage(websocket.TextMessage, []byte(deepgramSTTv2CloseMessage))
	}
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *deepgramV2Stream) Next() (*stt.SpeechEvent, error) {
	select {
	case event, ok := <-s.events:
		if ok {
			return event, nil
		}
		select {
		case err := <-s.errCh:
			return nil, err
		default:
			return nil, io.EOF
		}
	default:
	}

	if s.isClosed() {
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		select {
		case err := <-s.errCh:
			return nil, err
		default:
		}
		return nil, io.EOF
	}

	select {
	case err := <-s.errCh:
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		return nil, err
	case <-s.done():
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		return nil, io.EOF
	case event, ok := <-s.events:
		if ok {
			return event, nil
		}
		select {
		case err := <-s.errCh:
			return nil, err
		default:
			return nil, io.EOF
		}
	}
}

func (s *deepgramV2Stream) done() <-chan struct{} {
	if s.ctx == nil {
		return nil
	}
	return s.ctx.Done()
}

func (s *deepgramV2Stream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offset
}

func (s *deepgramV2Stream) SetStartTimeOffset(offset float64) {
	if offset < 0 {
		panic("start_time_offset must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offset = offset
}

func (s *deepgramV2Stream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.start
}

func (s *deepgramV2Stream) SetStartTime(startTime float64) {
	if startTime < 0 {
		panic("start_time must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.start = startTime
}

func (s *deepgramV2Stream) sendEvent(ev *stt.SpeechEvent) {
	select {
	case <-s.done():
	case s.events <- ev:
	}
}

func (s *deepgramV2Stream) sendError(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *deepgramV2Stream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *deepgramV2Stream) hasInputEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inputEnded
}

func (s *deepgramV2Stream) isCurrentConn(conn *websocket.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return conn == s.conn
}

var _ stt.STT = (*DeepgramSTTv2)(nil)
var _ stt.RecognizeStream = (*deepgramV2Stream)(nil)
var _ stt.StreamTiming = (*deepgramV2Stream)(nil)
