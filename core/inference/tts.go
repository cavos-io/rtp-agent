package inference

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/cavos-io/rtp-agent/library/utils"
	"github.com/gorilla/websocket"
)

type TTS struct {
	model             string
	voice             string
	language          string
	encoding          string
	sampleRate        int
	extraKwargs       map[string]any
	fallbackModels    []FallbackModel
	connectOptions    *APIConnectOptions
	apiKey            string
	apiSecret         string
	baseURL           string
	sentenceTokenizer tokenize.SentenceTokenizer
	dialWebsocket     inferenceTTSDialer
	connPoolMu        sync.Mutex
	connPool          *utils.ConnectionPool[inferenceTTSConn]
}

type TTSOption func(*TTS)

type APIConnectOptions = llm.APIConnectOptions

type inferenceTTSConn interface {
	WriteJSON(v any) error
	ReadMessage() (messageType int, p []byte, err error)
	Close() error
}

type inferenceTTSDialer func(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error)

func WithSentenceTokenizer(tokenizer tokenize.SentenceTokenizer) TTSOption {
	return func(t *TTS) {
		t.sentenceTokenizer = tokenizer
	}
}

func WithTTSModel(model string) TTSOption {
	return func(t *TTS) {
		modelName, voice := ttsModelAndVoice(model, "")
		t.model = modelName
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithTTSVoice(voice string) TTSOption {
	return func(t *TTS) {
		t.voice = voice
	}
}

func WithTTSLanguage(language string) TTSOption {
	return func(t *TTS) {
		t.language = language
	}
}

func WithTTSEncoding(encoding string) TTSOption {
	return func(t *TTS) {
		t.encoding = encoding
	}
}

func WithTTSSampleRate(sampleRate int) TTSOption {
	return func(t *TTS) {
		t.sampleRate = sampleRate
	}
}

func WithTTSExtraKwargs(extra map[string]any) TTSOption {
	return func(t *TTS) {
		if len(extra) == 0 {
			return
		}
		if t.extraKwargs == nil {
			t.extraKwargs = make(map[string]any, len(extra))
		}
		for key, value := range extra {
			t.extraKwargs[key] = value
		}
	}
}

func WithTTSFallbackModels(models ...FallbackModel) TTSOption {
	return func(t *TTS) {
		t.fallbackModels = cloneTTSFallbackModels(models)
	}
}

func WithTTSConnectOptions(options APIConnectOptions) TTSOption {
	return func(t *TTS) {
		t.connectOptions = &options
	}
}

func NewTTS(model string, apiKey, apiSecret string, opts ...TTSOption) *TTS {
	if model == "" {
		model = "cartesia/sonic-3"
	}
	model, voice := ttsModelAndVoice(model, "")
	apiKey, apiSecret = resolveInferenceCredentials(apiKey, apiSecret)
	t := &TTS{
		model:         model,
		voice:         voice,
		encoding:      "pcm_s16le",
		sampleRate:    24000,
		apiKey:        apiKey,
		apiSecret:     apiSecret,
		baseURL:       defaultInferenceWebsocketURL(),
		dialWebsocket: defaultInferenceTTSDialer,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *TTS) UpdateOptions(opts ...TTSOption) {
	for _, opt := range opts {
		opt(t)
	}
}

type FallbackModel struct {
	Model       string         `json:"model"`
	Voice       string         `json:"voice"`
	ExtraKwargs map[string]any `json:"extra,omitempty"`
}

func (t *TTS) Label() string {
	return "livekit.TTS"
}

func (t *TTS) Model() string {
	return t.model
}

func (t *TTS) Provider() string {
	return "livekit"
}

func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{
		Streaming:         true,
		AlignedTranscript: ttsHasAlignedTranscript(t.model, t.extraKwargs),
	}
}

func (t *TTS) SampleRate() int {
	return t.sampleRate
}

func (t *TTS) NumChannels() int {
	return 1
}

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	return tts.SynthesizeWithStream(ctx, t, text)
}

func (t *TTS) Prewarm() {
	t.connectionPool().Prewarm(context.Background())
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	conn, err := t.connectionPool().Get(ctx, 0)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	tokenizer := t.sentenceTokenizer
	if tokenizer == nil {
		tokenizer = tokenize.NewBasicSentenceTokenizer()
	}

	stream := &inferenceTTSStream{
		tts:         t,
		conn:        conn,
		connPool:    t.connectionPool(),
		ctx:         ctx,
		cancel:      cancel,
		model:       t.model,
		voice:       t.voice,
		language:    t.language,
		sampleRate:  t.sampleRate,
		extraKwargs: cloneTTSExtra(t.extraKwargs),
		tokenizer:   tokenizer.Stream("en"),
		eventCh:     make(chan *tts.SynthesizedAudio, 100),
	}

	go stream.run()

	return stream, nil
}

func (t *TTS) connectionPool() *utils.ConnectionPool[inferenceTTSConn] {
	t.connPoolMu.Lock()
	defer t.connPoolMu.Unlock()

	if t.connPool == nil {
		t.connPool = utils.NewConnectionPool[inferenceTTSConn](utils.ConnectionPoolOptions[inferenceTTSConn]{
			MaxSessionDuration: 5 * time.Minute,
			MarkRefreshedOnGet: true,
			Connect:            t.connectTTSWebsocket,
			Close: func(ctx context.Context, conn inferenceTTSConn) error {
				_ = conn.Close()
				return nil
			},
		})
	}
	return t.connPool
}

func (t *TTS) connectTTSWebsocket(ctx context.Context) (inferenceTTSConn, error) {
	token, err := CreateAccessToken(t.apiKey, t.apiSecret, InferenceAccessTokenTTL)
	if err != nil {
		return nil, err
	}

	modelName, createParams := ttsSessionCreateParams(t.model, t.voice, t.language, t.encoding, t.sampleRate, t.extraKwargs, t.fallbackModels, t.connectOptions)

	wsURL, err := url.Parse(t.baseURL + "/tts")
	if err != nil {
		return nil, err
	}

	q := wsURL.Query()
	q.Set("model", modelName)
	wsURL.RawQuery = q.Encode()

	header := InferenceHeaders()
	header.Add("Authorization", "Bearer "+token)

	conn, err := t.dialWebsocket(ctx, wsURL.String(), header)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to LiveKit Inference TTS: %w", err)
	}

	if err := conn.WriteJSON(createParams); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send session.create message to LiveKit Inference TTS: %w", err)
	}

	return conn, nil
}

func defaultInferenceTTSDialer(ctx context.Context, endpoint string, header http.Header) (inferenceTTSConn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, header)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func ttsSessionCreateParams(model string, voice string, language string, encoding string, sampleRate int, extra map[string]any, fallback []FallbackModel, connectOptions *APIConnectOptions) (string, map[string]interface{}) {
	modelName, voice := ttsModelAndVoice(model, voice)
	if encoding == "" {
		encoding = "pcm_s16le"
	}
	if sampleRate == 0 {
		sampleRate = 24000
	}
	createParams := map[string]interface{}{
		"type":        "session.create",
		"sample_rate": strconv.Itoa(sampleRate),
		"encoding":    encoding,
		"extra":       ttsExtraPayload(extra),
	}
	if modelName != "" {
		createParams["model"] = modelName
	}
	if voice != "" {
		createParams["voice"] = voice
	}
	if language != "" {
		createParams["language"] = language
	}
	if len(fallback) > 0 {
		createParams["fallback"] = map[string]interface{}{
			"models": ttsFallbackModelsPayload(fallback),
		}
	}
	if connectOptions != nil {
		createParams["connection"] = map[string]interface{}{
			"timeout": connectOptions.Timeout.Seconds(),
			"retries": connectOptions.MaxRetry,
		}
	}
	return modelName, createParams
}

func ttsExtraPayload(extra map[string]any) map[string]interface{} {
	if len(extra) == 0 {
		return map[string]interface{}{}
	}
	payload := make(map[string]interface{}, len(extra))
	for key, value := range extra {
		payload[key] = value
	}
	return payload
}

func cloneTTSExtra(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(extra))
	for key, value := range extra {
		cloned[key] = value
	}
	return cloned
}

func ttsFallbackModelsPayload(models []FallbackModel) []map[string]interface{} {
	payload := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		modelName, voice := ttsModelAndVoice(model.Model, model.Voice)
		payload = append(payload, map[string]interface{}{
			"model": modelName,
			"voice": voice,
			"extra": ttsExtraPayload(model.ExtraKwargs),
		})
	}
	return payload
}

func cloneTTSFallbackModels(models []FallbackModel) []FallbackModel {
	if len(models) == 0 {
		return nil
	}
	cloned := make([]FallbackModel, 0, len(models))
	for _, model := range models {
		model.ExtraKwargs = cloneTTSExtra(model.ExtraKwargs)
		cloned = append(cloned, model)
	}
	return cloned
}

func ttsHasAlignedTranscript(model string, extra map[string]any) bool {
	provider := strings.Split(model, "/")[0]
	switch provider {
	case "cartesia":
		enabled, _ := extra["add_timestamps"].(bool)
		return enabled
	case "elevenlabs":
		enabled, _ := extra["sync_alignment"].(bool)
		return enabled
	case "inworld":
		timestampType, _ := extra["timestamp_type"].(string)
		return timestampType == "WORD" || timestampType == "CHARACTER"
	default:
		return false
	}
}

func ttsModelAndVoice(model string, voice string) (string, string) {
	modelName := model
	if idx := strings.LastIndex(model, ":"); idx != -1 {
		if voice == "" {
			voice = model[idx+1:]
		}
		modelName = model[:idx]
	}
	return modelName, voice
}

type inferenceTTSStream struct {
	tts         *TTS
	conn        inferenceTTSConn
	connPool    *utils.ConnectionPool[inferenceTTSConn]
	ctx         context.Context
	cancel      context.CancelFunc
	model       string
	voice       string
	language    string
	sampleRate  int
	extraKwargs map[string]any
	tokenizer   tokenize.SentenceStream
	eventCh     chan *tts.SynthesizedAudio
	mu          sync.Mutex
	closed      bool
	inputEnded  bool
	done        bool
	streamErr   error
}

func (s *inferenceTTSStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	return s.tokenizer.PushText(text)
}

func (s *inferenceTTSStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	return s.tokenizer.Flush()
}

func (s *inferenceTTSStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	s.inputEnded = true
	return s.tokenizer.EndInput()
}

func (s *inferenceTTSStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	s.tokenizer.AClose()
	if s.connPool != nil {
		s.connPool.Remove(s.conn)
	}
	s.conn.Close()
	close(s.eventCh)
	return nil
}

func (s *inferenceTTSStream) Next() (*tts.SynthesizedAudio, error) {
	ev, ok := <-s.eventCh
	if !ok {
		s.mu.Lock()
		err := s.streamErr
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		done := s.done
		s.mu.Unlock()
		if done {
			return nil, io.EOF
		}
		return nil, context.Canceled
	}
	return ev, nil
}

func (s *inferenceTTSStream) setStreamError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.streamErr == nil {
		s.streamErr = err
	}
	s.mu.Unlock()
}

func (s *inferenceTTSStream) run() {
	defer s.Close()

	// Tokenizer loop
	go func() {
		for {
			tok, err := s.tokenizer.Next()
			if err != nil {
				s.mu.Lock()
				if s.closed {
					s.mu.Unlock()
					return
				}
				err = s.conn.WriteJSON(map[string]interface{}{"type": "session.flush"})
				s.mu.Unlock()
				if err != nil {
					s.setStreamError(fmt.Errorf("failed to send session.flush message to LiveKit Inference TTS: %w", err))
					s.Close()
					return
				}
				return
			}

			generationConfig := map[string]interface{}{}
			if s.model != "" {
				generationConfig["model"] = s.model
			}
			if s.voice != "" {
				generationConfig["voice"] = s.voice
			}
			if s.language != "" {
				generationConfig["language"] = s.language
			}

			tokenPkt := map[string]interface{}{
				"type":              "input_transcript",
				"transcript":        tok.Token + " ",
				"extra":             ttsExtraPayload(s.extraKwargs),
				"generation_config": generationConfig,
			}

			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			err = s.conn.WriteJSON(tokenPkt)
			s.mu.Unlock()

			if err != nil {
				s.setStreamError(fmt.Errorf("failed to send input_transcript message to LiveKit Inference TTS: %w", err))
				s.Close()
				return
			}
		}
	}()

	// Read loop
	currentSessionID := ""
	var pendingAudio *tts.SynthesizedAudio
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			_, msg, err := s.conn.ReadMessage()
			if err != nil {
				select {
				case <-s.ctx.Done():
					return
				default:
					s.setStreamError(fmt.Errorf("%s: %w", "Gateway connection closed unexpectedly", err))
				}
				return
			}

			var ev map[string]interface{}
			if err := json.Unmarshal(msg, &ev); err != nil {
				s.setStreamError(fmt.Errorf("failed to decode LiveKit Inference TTS message: %w", err))
				return
			}
			if currentSessionID == "" {
				currentSessionID = stringFromMap(ev, "session_id")
			}

			if evType, ok := ev["type"].(string); ok {
				if evType == "output_audio" {
					audioB64, ok := ev["audio"].(string)
					if !ok {
						s.setStreamError(fmt.Errorf("missing output_audio payload"))
						return
					}
					data, err := base64.StdEncoding.DecodeString(audioB64)
					if err != nil {
						s.setStreamError(fmt.Errorf("invalid output_audio payload: %w", err))
						return
					}
					if pendingAudio != nil {
						s.eventCh <- pendingAudio
					}
					pendingAudio = &tts.SynthesizedAudio{
						SegmentID: currentSessionID,
						Frame: &model.AudioFrame{
							Data:              data,
							SampleRate:        uint32(s.sampleRate),
							NumChannels:       1,
							SamplesPerChannel: uint32(len(data) / 2),
						},
					}
				} else if evType == "output_alignment" {
					if timedTranscript := inferenceTTSTimedTranscript(ev); len(timedTranscript) > 0 {
						s.eventCh <- &tts.SynthesizedAudio{SegmentID: currentSessionID, TimedTranscript: timedTranscript}
					}
				} else if evType == "done" {
					if pendingAudio != nil {
						pendingAudio.IsFinal = true
						s.eventCh <- pendingAudio
						pendingAudio = nil
					}
					s.mu.Lock()
					s.done = true
					s.mu.Unlock()
					return
				} else if evType == "error" {
					s.setStreamError(fmt.Errorf("LiveKit Inference TTS returned error: %s", string(msg)))
					return
				}
			}
		}
	}
}

func inferenceTTSTimedTranscript(data map[string]interface{}) []tts.TimedString {
	if words, ok := data["words"].([]interface{}); ok {
		timed := make([]tts.TimedString, 0, len(words))
		for _, raw := range words {
			word, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			timed = append(timed, tts.TimedString{
				Text:      stringFromMap(word, "word"),
				StartTime: floatFromMap(word, "start"),
				EndTime:   floatFromMap(word, "end"),
			})
		}
		return timed
	}
	if chars, ok := data["chars"].([]interface{}); ok {
		timed := make([]tts.TimedString, 0, len(chars))
		for _, raw := range chars {
			char, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			timed = append(timed, tts.TimedString{
				Text:      stringFromMap(char, "char"),
				StartTime: floatFromMap(char, "start"),
				EndTime:   floatFromMap(char, "end"),
			})
		}
		return timed
	}
	return nil
}
