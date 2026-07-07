package cartesia

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
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

const (
	defaultCartesiaTTSBaseURL         = "https://api.cartesia.ai"
	defaultCartesiaTTSAPIVersion      = "2025-04-16"
	cartesiaTTSUserAgent              = "LiveKit Agents Cartesia Plugin/Go"
	cartesiaTTSExperimentalAPIVersion = "2024-11-13"
)

type CartesiaTTS struct {
	apiKey              string
	baseURL             string
	voiceID             string
	voiceEmbedding      []float64
	model               string
	language            string
	encoding            string
	sampleRate          int
	apiVersion          string
	speed               any
	emotion             string
	volume              *float64
	wordTimestamps      bool
	pronunciationDictID string
	mu                  sync.Mutex
	streams             map[*cartesiaTTSStream]struct{}
	closed              bool
}

type CartesiaTTSOption func(*CartesiaTTS)

func WithCartesiaBaseURL(baseURL string) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithCartesiaLanguage(language string) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		t.language = language
	}
}

func WithCartesiaModel(model string) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithCartesiaVoiceID(voiceID string) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		if voiceID != "" {
			t.voiceID = voiceID
			t.voiceEmbedding = nil
		}
	}
}

func WithCartesiaAudioFormat(encoding string, sampleRate int) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		if encoding != "" {
			t.encoding = encoding
		}
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithCartesiaAPIVersion(apiVersion string) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		if apiVersion != "" {
			t.apiVersion = apiVersion
		}
	}
}

func WithCartesiaWordTimestamps(wordTimestamps bool) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		t.wordTimestamps = wordTimestamps
	}
}

func WithCartesiaVoiceEmbedding(embedding []float64) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		t.voiceEmbedding = append([]float64(nil), embedding...)
	}
}

func WithCartesiaSpeed(speed any) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		t.speed = speed
	}
}

func WithCartesiaEmotion(emotion string) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		t.emotion = emotion
	}
}

func WithCartesiaVolume(volume float64) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		t.volume = &volume
	}
}

func WithCartesiaPronunciationDictID(id string) CartesiaTTSOption {
	return func(t *CartesiaTTS) {
		t.pronunciationDictID = id
	}
}

func NewCartesiaTTS(apiKey string, voiceID string, model string, opts ...CartesiaTTSOption) *CartesiaTTS {
	if apiKey == "" {
		apiKey = os.Getenv("CARTESIA_API_KEY")
	}
	if voiceID == "" {
		voiceID = "f786b574-daa5-4673-aa0c-cbe3e8534c02"
	}
	if model == "" {
		model = "sonic-3"
	}
	provider := &CartesiaTTS{
		apiKey:         apiKey,
		baseURL:        defaultCartesiaTTSBaseURL,
		voiceID:        voiceID,
		model:          model,
		language:       "en",
		encoding:       "pcm_s16le",
		sampleRate:     24000,
		apiVersion:     defaultCartesiaTTSAPIVersion,
		wordTimestamps: true,
		streams:        make(map[*cartesiaTTSStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *CartesiaTTS) Label() string { return "cartesia.TTS" }
func (t *CartesiaTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: t.wordTimestamps}
}
func (t *CartesiaTTS) SampleRate() int  { return t.sampleRate }
func (t *CartesiaTTS) NumChannels() int { return 1 }
func (t *CartesiaTTS) Model() string    { return t.model }
func (t *CartesiaTTS) Provider() string { return "Cartesia" }

func (t *CartesiaTTS) UpdateOptions(opts ...CartesiaTTSOption) {
	t.mu.Lock()
	defer t.mu.Unlock()
	candidate := &CartesiaTTS{
		baseURL:             t.baseURL,
		voiceID:             t.voiceID,
		voiceEmbedding:      append([]float64(nil), t.voiceEmbedding...),
		model:               t.model,
		language:            t.language,
		encoding:            t.encoding,
		sampleRate:          t.sampleRate,
		apiVersion:          t.apiVersion,
		speed:               t.speed,
		emotion:             t.emotion,
		volume:              t.volume,
		wordTimestamps:      t.wordTimestamps,
		pronunciationDictID: t.pronunciationDictID,
	}
	for _, opt := range opts {
		opt(candidate)
	}
	t.voiceID = candidate.voiceID
	t.voiceEmbedding = append([]float64(nil), candidate.voiceEmbedding...)
	t.model = candidate.model
	t.language = candidate.language
	t.apiVersion = candidate.apiVersion
	t.speed = candidate.speed
	t.emotion = candidate.emotion
	t.volume = candidate.volume
	t.pronunciationDictID = candidate.pronunciationDictID
}

func (t *CartesiaTTS) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	t.closed = true
	streams := make([]*cartesiaTTSStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*cartesiaTTSStream]struct{})
	t.mu.Unlock()
	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *CartesiaTTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *CartesiaTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateCartesiaTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	apiURL, jsonBody, err := buildCartesiaSynthesizeRequest(t, text)
	if err != nil {
		return nil, err
	}

	return &cartesiaTTSChunkedStream{
		ctx:        ctx,
		apiURL:     apiURL,
		jsonBody:   append([]byte(nil), jsonBody...),
		apiKey:     t.apiKey,
		sampleRate: t.sampleRate,
	}, nil
}

func buildCartesiaSynthesizeRequest(t *CartesiaTTS, text string) (string, []byte, error) {
	reqBody := buildCartesiaOptions(t, false)
	reqBody["transcript"] = text
	jsonBody, err := json.Marshal(reqBody)
	return strings.TrimRight(t.baseURL, "/") + "/tts/bytes", jsonBody, err
}

func buildCartesiaOptions(t *CartesiaTTS, streaming bool) map[string]interface{} {
	voice := map[string]interface{}{
		"mode": "id",
		"id":   t.voiceID,
	}
	if len(t.voiceEmbedding) > 0 {
		voice = map[string]interface{}{
			"mode":      "embedding",
			"embedding": append([]float64(nil), t.voiceEmbedding...),
		}
	}
	if t.apiVersion == cartesiaTTSExperimentalAPIVersion {
		voiceControls := map[string]interface{}{}
		if t.speed != nil {
			voiceControls["speed"] = t.speed
		}
		if t.emotion != "" {
			voiceControls["emotion"] = []string{t.emotion}
		}
		if len(voiceControls) > 0 {
			voice["__experimental_controls"] = voiceControls
		}
	}
	options := map[string]interface{}{
		"model_id": t.model,
		"voice":    voice,
		"output_format": map[string]interface{}{
			"container":   "raw",
			"encoding":    t.encoding,
			"sample_rate": t.sampleRate,
		},
		"language": t.language,
	}
	if t.pronunciationDictID != "" {
		options["pronunciation_dict_id"] = t.pronunciationDictID
	}
	generationConfig := map[string]interface{}{}
	if t.apiVersion > cartesiaTTSExperimentalAPIVersion && strings.HasPrefix(t.model, "sonic-3") {
		if t.speed != nil {
			generationConfig["speed"] = t.speed
		}
		if t.emotion != "" {
			generationConfig["emotion"] = t.emotion
		}
		if t.volume != nil {
			generationConfig["volume"] = *t.volume
		}
	}
	if len(generationConfig) > 0 {
		options["generation_config"] = generationConfig
	}
	if streaming {
		options["add_timestamps"] = t.wordTimestamps
	}
	return options
}

type cartesiaTTSChunkedStream struct {
	ctx          context.Context
	apiURL       string
	jsonBody     []byte
	apiKey       string
	resp         *http.Response
	sampleRate   int
	pendingFinal bool
	finalSent    bool
	mu           sync.Mutex
}

func (s *cartesiaTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalSent {
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
		return s.emitFinal()
	}
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n > 0 {
		if err == io.EOF {
			s.pendingFinal = true
		}
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              buf[:n],
				SampleRate:        uint32(s.sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(n / 2),
			},
		}, nil
	}
	if err != nil {
		if err == io.EOF {
			return s.emitFinal()
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *cartesiaTTSChunkedStream) ensureResponse() error {
	if s.resp != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(s.ctx, "POST", s.apiURL, bytes.NewBuffer(s.jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", s.apiKey)
	req.Header.Set("Cartesia-Version", defaultCartesiaTTSAPIVersion)
	req.Header.Set("User-Agent", cartesiaTTSUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("Cartesia TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *cartesiaTTSChunkedStream) emitFinal() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	s.finalSent = true
	return &tts.SynthesizedAudio{IsFinal: true}, nil
}

func (s *cartesiaTTSChunkedStream) Close() error {
	s.mu.Lock()
	s.finalSent = true
	if s.resp == nil || s.resp.Body == nil {
		s.mu.Unlock()
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	s.mu.Unlock()
	return body.Close()
}

func (t *CartesiaTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateCartesiaTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if ctx == nil {
		ctx = context.Background()
	}

	stream := &cartesiaTTSStream{
		provider:      t,
		ctx:           ctx,
		audio:         make(chan *tts.SynthesizedAudio, 10),
		errCh:         make(chan error, 1),
		sampleRate:    t.sampleRate,
		contextID:     cavosmath.ShortUUID(""),
		streamURL:     buildCartesiaStreamURL(t),
		streamHeaders: buildCartesiaStreamHeaders(t),
		streamOptions: cartesiaTTSStreamOptions(t),
	}
	stream.writeJSON = stream.writeJSONMessage
	if !t.registerStream(stream) {
		return nil, io.ErrClosedPipe
	}

	return stream, nil
}

func (t *CartesiaTTS) registerStream(stream *cartesiaTTSStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*cartesiaTTSStream]struct{})
	}
	t.streams[stream] = struct{}{}
	return true
}

func (t *CartesiaTTS) unregisterStream(stream *cartesiaTTSStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func validateCartesiaTTSAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("cartesia API key is required, either as argument or set CARTESIA_API_KEY environment variable")
	}
	return nil
}

func buildCartesiaStreamURL(t *CartesiaTTS) string {
	baseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	}
	u, err := url.Parse(baseURL + "/tts/websocket")
	if err != nil {
		u = &url.URL{Scheme: "wss", Host: "api.cartesia.ai", Path: "/tts/websocket"}
	}
	q := u.Query()
	q.Set("cartesia_version", t.apiVersion)
	u.RawQuery = q.Encode()
	return u.String()
}

func buildCartesiaStreamHeaders(t *CartesiaTTS) http.Header {
	headers := make(http.Header)
	headers.Set("X-API-Key", t.apiKey)
	headers.Set("User-Agent", cartesiaTTSUserAgent)
	return headers
}

type cartesiaTTSStream struct {
	provider *CartesiaTTS
	ctx      context.Context
	conn     *websocket.Conn
	audio    chan *tts.SynthesizedAudio
	errCh    chan error
	mu       sync.Mutex
	closed   bool
	flushed  bool

	sampleRate    int
	contextID     string
	streamURL     string
	streamHeaders http.Header
	writeJSON     func(any) error
	sentTokens    []string
	pushedText    string
	skipAlignment bool
	pendingText   string
	streamOptions map[string]interface{}
}

type cartesiaWSResponse struct {
	Type           string                    `json:"type"`
	Error          string                    `json:"error"`
	Data           string                    `json:"data"` // base64 encoded audio
	Done           bool                      `json:"done"`
	WordTimestamps cartesiaTTSWordTimestamps `json:"word_timestamps"`
}

type cartesiaTTSWordTimestamps struct {
	Words []string  `json:"words"`
	Start []float64 `json:"start"`
	End   []float64 `json:"end"`
}

func (s *cartesiaTTSStream) readLoop() {
	defer close(s.audio)
	emittedAudio := false
	emittedFinal := false
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !s.isClosed() {
				if _, ok := err.(*websocket.CloseError); ok || err == io.EOF {
					s.errCh <- llm.NewAPIConnectionError("Cartesia connection closed unexpectedly")
				} else {
					s.errCh <- err
				}
			}
			return
		}

		var resp cartesiaWSResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			s.errCh <- llm.NewAPIConnectionError(fmt.Sprintf("failed to parse Cartesia message: %v", err))
			return
		}

		if resp.Type == "error" {
			s.errCh <- llm.NewAPIConnectionError(fmt.Sprintf("cartesia error: %s", resp.Error))
			return
		}

		if resp.Type == "chunk" && resp.Data != "" {
			data, err := cartesiaDecodeBase64Chunk(resp.Data)
			if err != nil {
				s.errCh <- llm.NewAPIConnectionError(fmt.Sprintf("failed to decode Cartesia audio: %v", err))
				return
			}
			if len(data) == 0 {
				continue
			}
			s.audio <- &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              data,
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(len(data) / 2),
				},
			}
			emittedAudio = true
			continue
		}

		if len(resp.WordTimestamps.Words) > 0 {
			s.audio <- &tts.SynthesizedAudio{
				TimedTranscript: s.cartesiaTimedTranscript(resp.WordTimestamps),
			}
		}

		if resp.Type == "done" || resp.Done {
			if s.isFlushed() {
				if !emittedAudio {
					if pushedText := s.pushedInputText(); strings.TrimSpace(pushedText) != "" {
						s.errCh <- llm.NewAPIError(fmt.Sprintf("no audio frames were pushed for text: %s", pushedText), nil, true)
					}
				}
				if emittedAudio && !emittedFinal {
					s.audio <- &tts.SynthesizedAudio{IsFinal: true}
				}
				return
			}
		}
	}
}

func cartesiaDecodeBase64Chunk(data string) ([]byte, error) {
	clean := make([]byte, 0, len(data))
	dataChars := 0
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case b >= 'A' && b <= 'Z',
			b >= 'a' && b <= 'z',
			b >= '0' && b <= '9',
			b == '+',
			b == '/':
			clean = append(clean, b)
			dataChars++
		case b == '=':
			clean = append(clean, b)
		}
	}
	if dataChars == 0 {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(string(clean))
}

func (s *cartesiaTTSStream) cartesiaTimedTranscript(timestamps cartesiaTTSWordTimestamps) []tts.TimedString {
	count := min(len(timestamps.Words), len(timestamps.Start), len(timestamps.End))
	timed := make([]tts.TimedString, 0, count)
	words := s.alignCartesiaTimestampWords(timestamps.Words[:count])
	for i := 0; i < count; i++ {
		timed = append(timed, tts.TimedString{
			Text:      words[i],
			StartTime: timestamps.Start[i],
			EndTime:   timestamps.End[i],
		})
	}
	return timed
}

func (s *cartesiaTTSStream) alignCartesiaTimestampWords(words []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	aligned := make([]string, 0, len(words))
	for _, word := range words {
		if len(s.sentTokens) == 0 || s.skipAlignment {
			aligned = append(aligned, word+" ")
			s.skipAlignment = true
			continue
		}
		sent := s.sentTokens[0]
		s.sentTokens = s.sentTokens[1:]
		if idx := strings.Index(sent, word); idx != -1 {
			alignedWord := sent[:idx+len(word)]
			remaining := sent[idx+len(word):]
			if strings.TrimSpace(remaining) != "" {
				s.sentTokens = append([]string{remaining}, s.sentTokens...)
			} else if remaining != "" && len(s.sentTokens) > 0 {
				s.sentTokens[0] = remaining + s.sentTokens[0]
			}
			aligned = append(aligned, alignedWord)
			continue
		}
		aligned = append(aligned, word+" ")
		s.skipAlignment = true
	}
	return aligned
}

func (s *cartesiaTTSStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *cartesiaTTSStream) isFlushed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushed
}

func (s *cartesiaTTSStream) pushedInputText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pushedText
}

func (s *cartesiaTTSStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.pushedText += text
	s.pendingText += text
	if err := s.sendCompleteSentencesLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *cartesiaTTSStream) sendCompleteSentencesLocked() error {
	for {
		tokens := tokenize.NewBasicSentenceTokenizer().Tokenize(s.pendingText, "")
		if len(tokens) <= 1 {
			return nil
		}
		sentence := tokens[0]
		if err := s.sendTranscriptLocked(sentence); err != nil {
			return err
		}
		s.pendingText = strings.TrimPrefix(s.pendingText, sentence)
	}
}

func (s *cartesiaTTSStream) sendTranscriptLocked(text string) error {
	msg := s.streamPacketLocked()
	msg["context_id"] = s.packetContextIDLocked()
	msg["transcript"] = text + " "
	msg["continue"] = true
	s.sentTokens = append(s.sentTokens, text+" ")
	return s.writeJSONData(msg)
}

func (s *cartesiaTTSStream) streamPacketLocked() map[string]interface{} {
	msg := make(map[string]interface{}, len(s.streamOptions)+3)
	for key, value := range s.streamOptions {
		msg[key] = value
	}
	return msg
}

func cartesiaTTSStreamOptions(t *CartesiaTTS) map[string]interface{} {
	options := buildCartesiaOptions(t, true)
	options["max_buffer_delay_ms"] = 0
	return options
}

func (s *cartesiaTTSStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if err := s.flushPendingTextLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *cartesiaTTSStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if err := s.flushPendingTextLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	msg := s.streamPacketLocked()
	msg["context_id"] = s.packetContextIDLocked()
	msg["transcript"] = " "
	msg["continue"] = false
	s.flushed = true
	s.sentTokens = append(s.sentTokens, " ")
	if err := s.writeJSONData(msg); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *cartesiaTTSStream) flushPendingTextLocked() error {
	if s.pendingText == "" {
		return nil
	}
	text := strings.Join(tokenize.NewBasicSentenceTokenizer().Tokenize(s.pendingText, ""), " ")
	s.pendingText = ""
	return s.sendTranscriptLocked(text)
}

func (s *cartesiaTTSStream) packetContextIDLocked() string {
	if s.contextID != "" {
		return s.contextID
	}
	return "default"
}

func (s *cartesiaTTSStream) closeAfterWriteFailureLocked() {
	s.closed = true
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

func (s *cartesiaTTSStream) writeJSONData(msg any) error {
	if s.writeJSON != nil {
		return s.writeJSON(msg)
	}
	return s.writeJSONMessage(msg)
}

func (s *cartesiaTTSStream) writeJSONMessage(msg any) error {
	if err := s.ensureConnectedLocked(); err != nil {
		return err
	}
	return s.conn.WriteJSON(msg)
}

func (s *cartesiaTTSStream) ensureConnectedLocked() error {
	if s.conn != nil {
		return nil
	}
	if s.provider != nil && s.provider.isClosed() {
		return io.ErrClosedPipe
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, s.streamURL, s.streamHeaders)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(err.Error())
	}
	if s.closed {
		_ = conn.Close()
		return io.ErrClosedPipe
	}
	if s.provider != nil && s.provider.isClosed() {
		_ = conn.Close()
		return io.ErrClosedPipe
	}
	s.conn = conn
	go s.readLoop()
	return nil
}

func (s *cartesiaTTSStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *cartesiaTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if s.isClosed() {
		return nil, io.EOF
	}

	select {
	case audio, ok := <-s.audio:
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
	case audio, ok := <-s.audio:
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
		select {
		case audio, ok := <-s.audio:
			if ok {
				return audio, nil
			}
		default:
		}
		return nil, err
	}
}
