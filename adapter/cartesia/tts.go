package cartesia

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/gorilla/websocket"
)

const (
	defaultCartesiaTTSBaseURL = "https://api.cartesia.ai"
	cartesiaTTSUserAgent      = "LiveKit Agents Cartesia Plugin/Go"
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
		apiVersion:     "2025-04-16",
		wordTimestamps: true,
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

func (t *CartesiaTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateCartesiaTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	apiURL, jsonBody, err := buildCartesiaSynthesizeRequest(t, text)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", t.apiKey)
	req.Header.Set("Cartesia-Version", t.apiVersion)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("cartesia tts error: %s", string(respBody))
	}

	return &cartesiaTTSChunkedStream{
		resp:       resp,
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
	if t.speed != nil {
		generationConfig["speed"] = t.speed
	}
	if t.emotion != "" {
		generationConfig["emotion"] = t.emotion
	}
	if t.volume != nil {
		generationConfig["volume"] = *t.volume
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
	resp       *http.Response
	sampleRate int
}

func (s *cartesiaTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
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

func (s *cartesiaTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func (t *CartesiaTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateCartesiaTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildCartesiaStreamURL(t), buildCartesiaStreamHeaders(t))
	if err != nil {
		return nil, err
	}

	// Send context initialization
	initMsg := buildCartesiaStreamInitMessage(t)
	if err := conn.WriteJSON(initMsg); err != nil {
		conn.Close()
		return nil, err
	}

	stream := &cartesiaTTSStream{
		conn:       conn,
		audio:      make(chan *tts.SynthesizedAudio, 10),
		errCh:      make(chan error, 1),
		sampleRate: t.sampleRate,
	}
	stream.writeJSON = stream.writeJSONMessage

	go stream.readLoop()

	return stream, nil
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
	headers.Set("Cartesia-Version", t.apiVersion)
	headers.Set("User-Agent", cartesiaTTSUserAgent)
	return headers
}

func buildCartesiaStreamInitMessage(t *CartesiaTTS) map[string]interface{} {
	initMsg := buildCartesiaOptions(t, true)
	initMsg["context_id"] = "default"
	initMsg["transcript"] = " "
	return initMsg
}

type cartesiaTTSStream struct {
	conn   *websocket.Conn
	audio  chan *tts.SynthesizedAudio
	errCh  chan error
	mu     sync.Mutex
	closed bool

	sampleRate int
	writeJSON  func(any) error
}

type cartesiaWSResponse struct {
	Type  string `json:"type"`
	Error string `json:"error"`
	Data  string `json:"data"` // base64 encoded audio
	Done  bool   `json:"done"`
}

func (s *cartesiaTTSStream) readLoop() {
	defer close(s.audio)
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
			continue
		}

		if resp.Type == "error" {
			s.errCh <- fmt.Errorf("cartesia error: %s", resp.Error)
			return
		}

		if resp.Type == "chunk" && resp.Data != "" {
			data, err := base64.StdEncoding.DecodeString(resp.Data)
			if err == nil {
				s.audio <- &tts.SynthesizedAudio{
					Frame: &model.AudioFrame{
						Data:              data,
						SampleRate:        uint32(s.sampleRate),
						NumChannels:       1,
						SamplesPerChannel: uint32(len(data) / 2),
					},
					IsFinal: resp.Done,
				}
			}
		}

		if resp.Type == "done" || resp.Done {
			// Context finished, but we might keep connection open for more text
		}
	}
}

func (s *cartesiaTTSStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *cartesiaTTSStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	msg := map[string]interface{}{
		"context_id": "default",
		"transcript": text,
		"continue":   true,
	}
	if err := s.writeJSONData(msg); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *cartesiaTTSStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	msg := map[string]interface{}{
		"context_id": "default",
		"transcript": " ",
		"continue":   false,
	}
	if err := s.writeJSONData(msg); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
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
	return s.conn.WriteJSON(msg)
}

func (s *cartesiaTTSStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.conn.Close()
}

func (s *cartesiaTTSStream) Next() (*tts.SynthesizedAudio, error) {
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
		return nil, err
	}
}
