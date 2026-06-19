package smallestai

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultSmallestAISTTBaseURL      = "https://api.smallest.ai/waves/v1"
	defaultSmallestAISTTModel        = "pulse"
	defaultSmallestAISTTLanguage     = "en"
	defaultSmallestAISTTSampleRate   = 16000
	defaultSmallestAISTTEncoding     = "linear16"
	defaultSmallestAISTTEOUTimeoutMS = 0
	smallestAIAPIKeyEnv              = "SMALLEST_API_KEY"
	smallestAIPCMBytesPerSample      = 2
)

type SmallestAISTT struct {
	apiKey         string
	baseURL        string
	model          string
	language       string
	sampleRate     int
	encoding       string
	wordTimestamps bool
	diarize        bool
	eouTimeoutMS   int
	dialWebsocket  smallestAISTTWebsocketDialer
	mu             sync.Mutex
	streams        map[*smallestAISTTStream]struct{}
}

type SmallestAISTTOption func(*SmallestAISTT)
type smallestAISTTWebsocketDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

func WithSmallestAISTTBaseURL(baseURL string) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSmallestAISTTModel(model string) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithSmallestAISTTLanguage(language string) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSmallestAISTTSampleRate(sampleRate int) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithSmallestAISTTEncoding(encoding string) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if encoding != "" {
			s.encoding = encoding
		}
	}
}

func WithSmallestAISTTWordTimestamps(enabled bool) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		s.wordTimestamps = enabled
	}
}

func WithSmallestAISTTDiarize(enabled bool) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		s.diarize = enabled
	}
}

func WithSmallestAISTTEOUTimeoutMS(timeoutMS int) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if timeoutMS >= 0 {
			s.eouTimeoutMS = timeoutMS
		}
	}
}

func withSmallestAISTTWebsocketDialer(dialer smallestAISTTWebsocketDialer) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if dialer != nil {
			s.dialWebsocket = dialer
		}
	}
}

func NewSmallestAISTT(apiKey string, opts ...SmallestAISTTOption) *SmallestAISTT {
	if apiKey == "" {
		apiKey = os.Getenv(smallestAIAPIKeyEnv)
	}
	provider := &SmallestAISTT{
		apiKey:         apiKey,
		baseURL:        defaultSmallestAISTTBaseURL,
		model:          defaultSmallestAISTTModel,
		language:       defaultSmallestAISTTLanguage,
		sampleRate:     defaultSmallestAISTTSampleRate,
		encoding:       defaultSmallestAISTTEncoding,
		wordTimestamps: true,
		diarize:        false,
		eouTimeoutMS:   defaultSmallestAISTTEOUTimeoutMS,
		dialWebsocket:  defaultSmallestAISTTWebsocketDialer,
		streams:        make(map[*smallestAISTTStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *SmallestAISTT) Label() string { return "smallestai.STT" }
func (s *SmallestAISTT) Model() string { return s.model }
func (s *SmallestAISTT) Provider() string {
	return "SmallestAI"
}
func (s *SmallestAISTT) InputSampleRate() uint32 {
	return uint32(s.sampleRate)
}

func (s *SmallestAISTT) UpdateOptions(opts ...SmallestAISTTOption) {
	if s == nil {
		return
	}
	s.mu.Lock()
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	streamOptions := s.streamOptionsLocked("")
	streams := make([]*smallestAISTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()

	for _, stream := range streams {
		stream.updateOptions(streamOptions)
	}
}

func (s *SmallestAISTT) Capabilities() stt.STTCapabilities {
	aligned := ""
	if s.wordTimestamps {
		aligned = "word"
	}
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    true,
		Diarization:       s.diarize,
		AlignedTranscript: aligned,
		OfflineRecognize:  true,
	}
}

func (s *SmallestAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := validateSmallestAISTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	s.mu.Lock()
	streamOptions := s.streamOptionsLocked(language)
	dialWebsocket := s.dialWebsocket
	s.mu.Unlock()
	conn, _, err := dialWebsocket(ctx, buildSmallestAISTTStreamURLFromOptions(streamOptions), buildSmallestAISTTHeadersFromAPIKey(streamOptions.apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to dial smallestai stt websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &smallestAISTTStream{
		owner:         s,
		dialWebsocket: dialWebsocket,
		conn:          conn,
		events:        make(chan *stt.SpeechEvent, 100),
		errCh:         make(chan error, 1),
		ctx:           streamCtx,
		cancel:        cancel,
		state:         &smallestAISTTStreamState{language: streamOptions.language, diarize: streamOptions.diarize},
	}
	stream.applyOptions(streamOptions)
	s.registerStream(stream)
	go stream.readLoop()
	return stream, nil
}

func (s *SmallestAISTT) registerStream(stream *smallestAISTTStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = make(map[*smallestAISTTStream]struct{})
	}
	s.streams[stream] = struct{}{}
}

func (s *SmallestAISTT) unregisterStream(stream *smallestAISTTStream) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func defaultSmallestAISTTWebsocketDialer(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
}

func (s *SmallestAISTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if err := validateSmallestAISTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	req, err := buildSmallestAISTTRecognizeRequest(ctx, s, smallestAISTTWAVBytes(frames, uint32(s.sampleRate)), language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("smallestai stt error: %s", string(respBody))
	}
	var result smallestAIBatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return smallestAIBatchSpeechEvent(resolveSmallestAISTTLanguage(s, language), result), nil
}

func validateSmallestAISTTAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("smallestai API key is required, either as argument or set SMALLEST_API_KEY environment variable")
	}
	return nil
}

func smallestAISTTWAVBytes(frames []*model.AudioFrame, defaultSampleRate uint32) []byte {
	sampleRate := defaultSampleRate
	if sampleRate == 0 {
		sampleRate = defaultSmallestAISTTSampleRate
	}
	numChannels := uint32(1)
	var data bytes.Buffer
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate > 0 && data.Len() == 0 {
			sampleRate = frame.SampleRate
		}
		if frame.NumChannels > 0 && data.Len() == 0 {
			numChannels = frame.NumChannels
		}
		data.Write(frame.Data)
	}
	pcm := data.Bytes()
	dataSize := uint32(len(pcm))
	blockAlign := numChannels * smallestAIPCMBytesPerSample
	byteRate := sampleRate * blockAlign

	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36)+dataSize)
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(&wav, binary.LittleEndian, sampleRate)
	_ = binary.Write(&wav, binary.LittleEndian, byteRate)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, dataSize)
	wav.Write(pcm)
	return wav.Bytes()
}

func buildSmallestAISTTRecognizeRequest(ctx context.Context, s *SmallestAISTT, audio []byte, language string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildSmallestAISTTHTTPURL(s, language), bytes.NewReader(audio))
	if err != nil {
		return nil, err
	}
	for key, values := range buildSmallestAISTTHeaders(s) {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	return req, nil
}

func buildSmallestAISTTHTTPURL(s *SmallestAISTT, language string) string {
	u, _ := url.Parse(strings.TrimRight(s.baseURL, "/") + "/" + s.model + "/get_text")
	q := smallestAISTTQuery(s, language, false)
	u.RawQuery = q.Encode()
	return u.String()
}

func buildSmallestAISTTStreamURL(s *SmallestAISTT, language string) string {
	return buildSmallestAISTTStreamURLFromOptions(s.streamOptionsLocked(language))
}

func buildSmallestAISTTStreamURLFromOptions(opts smallestAISTTStreamOptions) string {
	streamBase := strings.TrimRight(opts.baseURL, "/")
	streamBase = strings.Replace(streamBase, "https://", "wss://", 1)
	streamBase = strings.Replace(streamBase, "http://", "ws://", 1)
	u, _ := url.Parse(streamBase + "/" + opts.model + "/get_text")
	q := smallestAISTTQueryFromOptions(opts, true)
	u.RawQuery = q.Encode()
	return u.String()
}

func smallestAISTTQuery(s *SmallestAISTT, language string, includeEOU bool) url.Values {
	q := url.Values{}
	q.Set("language", resolveSmallestAISTTLanguage(s, language))
	q.Set("encoding", s.encoding)
	q.Set("sample_rate", strconv.Itoa(s.sampleRate))
	q.Set("word_timestamps", strconv.FormatBool(s.wordTimestamps))
	q.Set("diarize", strconv.FormatBool(s.diarize))
	if includeEOU && s.eouTimeoutMS > 0 {
		q.Set("eou_timeout_ms", strconv.Itoa(s.eouTimeoutMS))
	}
	return q
}

func smallestAISTTQueryFromOptions(opts smallestAISTTStreamOptions, includeEOU bool) url.Values {
	q := url.Values{}
	q.Set("language", opts.language)
	q.Set("encoding", opts.encoding)
	q.Set("sample_rate", strconv.Itoa(opts.sampleRate))
	q.Set("word_timestamps", strconv.FormatBool(opts.wordTimestamps))
	q.Set("diarize", strconv.FormatBool(opts.diarize))
	if includeEOU && opts.eouTimeoutMS > 0 {
		q.Set("eou_timeout_ms", strconv.Itoa(opts.eouTimeoutMS))
	}
	return q
}

func buildSmallestAISTTHeaders(s *SmallestAISTT) http.Header {
	return buildSmallestAISTTHeadersFromAPIKey(s.apiKey)
}

func buildSmallestAISTTHeadersFromAPIKey(apiKey string) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+apiKey)
	headers.Set("X-Source", "livekit")
	return headers
}

func resolveSmallestAISTTLanguage(s *SmallestAISTT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

type smallestAISTTStreamOptions struct {
	apiKey         string
	baseURL        string
	model          string
	language       string
	sampleRate     int
	encoding       string
	wordTimestamps bool
	diarize        bool
	eouTimeoutMS   int
}

func (s *SmallestAISTT) streamOptionsLocked(language string) smallestAISTTStreamOptions {
	streamLanguage := s.language
	if language != "" {
		streamLanguage = language
	}
	return smallestAISTTStreamOptions{
		apiKey:         s.apiKey,
		baseURL:        s.baseURL,
		model:          s.model,
		language:       streamLanguage,
		sampleRate:     s.sampleRate,
		encoding:       s.encoding,
		wordTimestamps: s.wordTimestamps,
		diarize:        s.diarize,
		eouTimeoutMS:   s.eouTimeoutMS,
	}
}

type smallestAISTTStream struct {
	owner         *SmallestAISTT
	dialWebsocket smallestAISTTWebsocketDialer
	conn          *websocket.Conn
	events        chan *stt.SpeechEvent
	errCh         chan error
	mu            sync.Mutex
	closed        bool

	apiKey         string
	baseURL        string
	model          string
	language       string
	encoding       string
	wordTimestamps bool
	diarize        bool
	eouTimeoutMS   int

	ctx                context.Context
	cancel             context.CancelFunc
	state              *smallestAISTTStreamState
	audio              bytes.Buffer
	sampleRate         int
	usageAudioDuration float64
	reconnectRequested bool
}

func (s *smallestAISTTStream) readLoop() {
	defer close(s.events)
	defer s.owner.unregisterStream(s)
	for {
		conn := s.currentConn()
		msgType, message, err := conn.ReadMessage()
		if err != nil {
			if s.shouldReconnect() {
				if err := s.reconnect(); err != nil {
					s.errCh <- err
					return
				}
				continue
			}
			if !s.isClosed() {
				s.errCh <- smallestAISTTUnexpectedCloseError(err)
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var resp smallestAIStreamResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}
		for _, event := range processSmallestAISTTStreamEvent(s.state, resp, 0) {
			s.events <- event
		}
		if resp.IsLast {
			return
		}
	}
}

func (s *smallestAISTTStream) applyOptions(opts smallestAISTTStreamOptions) {
	s.apiKey = opts.apiKey
	s.baseURL = opts.baseURL
	s.model = opts.model
	s.language = opts.language
	s.sampleRate = opts.sampleRate
	s.encoding = opts.encoding
	s.wordTimestamps = opts.wordTimestamps
	s.diarize = opts.diarize
	s.eouTimeoutMS = opts.eouTimeoutMS
}

func (s *smallestAISTTStream) updateOptions(opts smallestAISTTStreamOptions) {
	s.mu.Lock()
	s.applyOptions(opts)
	s.audio.Reset()
	s.state.language = opts.language
	s.state.diarize = opts.diarize
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.reconnectRequested = true
	conn := s.conn
	s.mu.Unlock()

	if conn != nil {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.Close()
	}
}

func (s *smallestAISTTStream) currentConn() *websocket.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

func (s *smallestAISTTStream) shouldReconnect() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconnectRequested && !s.closed
}

func (s *smallestAISTTStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func smallestAISTTUnexpectedCloseError(err error) error {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || err == io.EOF {
		return fmt.Errorf("smallestai stt connection closed unexpectedly: %w", err)
	}
	return err
}

func (s *smallestAISTTStream) reconnect() error {
	s.mu.Lock()
	opts := smallestAISTTStreamOptions{
		apiKey:         s.apiKey,
		baseURL:        s.baseURL,
		model:          s.model,
		language:       s.language,
		sampleRate:     s.sampleRate,
		encoding:       s.encoding,
		wordTimestamps: s.wordTimestamps,
		diarize:        s.diarize,
		eouTimeoutMS:   s.eouTimeoutMS,
	}
	dialWebsocket := s.dialWebsocket
	ctx := s.ctx
	s.mu.Unlock()

	conn, _, err := dialWebsocket(ctx, buildSmallestAISTTStreamURLFromOptions(opts), buildSmallestAISTTHeadersFromAPIKey(opts.apiKey))
	if err != nil {
		return fmt.Errorf("failed to reconnect smallestai stt websocket: %w", err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = conn.Close()
		return io.ErrClosedPipe
	}
	s.conn = conn
	s.reconnectRequested = false
	s.mu.Unlock()
	return nil
}

func (s *smallestAISTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("smallestai stt stream is closed")
	}
	if _, err := s.audio.Write(frame.Data); err != nil {
		return err
	}
	chunkBytes := s.sampleRate / 20 * smallestAIPCMBytesPerSample
	for s.audio.Len() >= chunkBytes {
		chunk := make([]byte, chunkBytes)
		if _, err := s.audio.Read(chunk); err != nil {
			return err
		}
		if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			return err
		}
		s.usageAudioDuration += smallestAISTTAudioDuration(len(chunk), s.sampleRate)
	}
	return nil
}

func (s *smallestAISTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("smallestai stt stream is closed")
	}
	if s.audio.Len() == 0 {
		s.emitRecognitionUsageLocked()
		return nil
	}
	chunk := bytes.Clone(s.audio.Bytes())
	s.audio.Reset()
	if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
		return err
	}
	s.usageAudioDuration += smallestAISTTAudioDuration(len(chunk), s.sampleRate)
	s.emitRecognitionUsageLocked()
	return nil
}

func (s *smallestAISTTStream) emitRecognitionUsageLocked() {
	if s.usageAudioDuration <= 0 {
		return
	}
	duration := s.usageAudioDuration
	s.usageAudioDuration = 0
	s.events <- &stt.SpeechEvent{
		Type:      stt.SpeechEventRecognitionUsage,
		RequestID: s.state.sessionID,
		RecognitionUsage: &stt.RecognitionUsage{
			AudioDuration: duration,
		},
	}
}

func smallestAISTTAudioDuration(byteCount int, sampleRate int) float64 {
	if byteCount <= 0 || sampleRate <= 0 {
		return 0
	}
	return float64(byteCount) / float64(sampleRate*smallestAIPCMBytesPerSample)
}

func (s *smallestAISTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"close_stream"}`))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *smallestAISTTStream) Next() (*stt.SpeechEvent, error) {
	select {
	case event, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return event, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

type smallestAISTTStreamState struct {
	language  string
	diarize   bool
	speaking  bool
	sessionID string
}

type smallestAIStreamResponse struct {
	SessionID  string           `json:"session_id"`
	Transcript string           `json:"transcript"`
	IsFinal    bool             `json:"is_final"`
	IsLast     bool             `json:"is_last"`
	Language   string           `json:"language"`
	Words      []smallestAIWord `json:"words"`
}

type smallestAIBatchResponse struct {
	Transcription string           `json:"transcription"`
	Language      string           `json:"language"`
	Words         []smallestAIWord `json:"words"`
}

type smallestAIWord struct {
	Word       string  `json:"word"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
	Speaker    *int    `json:"speaker,omitempty"`
}

func processSmallestAISTTStreamEvent(state *smallestAISTTStreamState, resp smallestAIStreamResponse, startTimeOffset float64) []*stt.SpeechEvent {
	if resp.SessionID != "" {
		state.sessionID = resp.SessionID
	}
	if resp.Transcript == "" {
		return nil
	}
	var events []*stt.SpeechEvent
	if !state.speaking {
		state.speaking = true
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
	}
	eventType := stt.SpeechEventInterimTranscript
	if resp.IsFinal {
		eventType = stt.SpeechEventFinalTranscript
	}
	events = append(events, &stt.SpeechEvent{
		Type:      eventType,
		RequestID: state.sessionID,
		Alternatives: []stt.SpeechData{
			smallestAISpeechData(state.language, resp.Language, resp.Transcript, resp.Words, startTimeOffset, state.diarize),
		},
	})
	if resp.IsFinal {
		state.speaking = false
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
	}
	return events
}

func smallestAIBatchSpeechEvent(language string, resp smallestAIBatchResponse) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			smallestAISpeechData(language, resp.Language, resp.Transcription, resp.Words, 0, false),
		},
	}
}

func smallestAISpeechData(defaultLanguage, detectedLanguage, transcript string, words []smallestAIWord, startTimeOffset float64, diarize bool) stt.SpeechData {
	language := detectedLanguage
	if language == "" {
		language = defaultLanguage
	}
	data := stt.SpeechData{
		Language:   language,
		Text:       transcript,
		Confidence: stt.DefaultTranscriptConfidence(transcript),
	}
	if len(words) == 0 {
		return data
	}
	data.StartTime = words[0].Start + startTimeOffset
	data.EndTime = words[len(words)-1].End + startTimeOffset
	data.Confidence = words[0].Confidence
	data.Words = smallestAITimedStrings(words, startTimeOffset)
	if diarize {
		data.SpeakerID = smallestAIMajoritySpeaker(words)
	}
	return data
}

func smallestAITimedStrings(words []smallestAIWord, startTimeOffset float64) []stt.TimedString {
	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		speakerID := ""
		if word.Speaker != nil {
			speakerID = "S" + strconv.Itoa(*word.Speaker)
		}
		timed = append(timed, stt.TimedString{
			Text:       word.Word,
			StartTime:  word.Start + startTimeOffset,
			EndTime:    word.End + startTimeOffset,
			Confidence: word.Confidence,
			SpeakerID:  speakerID,
		})
	}
	return timed
}

func smallestAIMajoritySpeaker(words []smallestAIWord) string {
	counts := map[int]int{}
	for _, word := range words {
		if word.Speaker != nil {
			counts[*word.Speaker]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	speakers := make([]int, 0, len(counts))
	for speaker := range counts {
		speakers = append(speakers, speaker)
	}
	sort.Slice(speakers, func(i, j int) bool {
		if counts[speakers[i]] == counts[speakers[j]] {
			return speakers[i] < speakers[j]
		}
		return counts[speakers[i]] > counts[speakers[j]]
	})
	return "S" + strconv.Itoa(speakers[0])
}
