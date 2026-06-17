package elevenlabs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/gorilla/websocket"
)

const (
	defaultElevenLabsBaseURL           = "https://api.elevenlabs.io/v1"
	defaultElevenLabsInactivityTimeout = 180
)

type ElevenLabsTTS struct {
	apiKey            string
	baseURL           string
	voiceID           string
	modelID           string
	encoding          string
	sampleRate        int
	language          string
	enableSSMLParsing bool
}

type ElevenLabsTTSOption func(*ElevenLabsTTS)

func WithElevenLabsVoiceID(voiceID string) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		if voiceID != "" {
			t.voiceID = voiceID
		}
	}
}

func WithElevenLabsModel(modelID string) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		if modelID != "" {
			t.modelID = modelID
		}
	}
}

func WithElevenLabsBaseURL(baseURL string) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithElevenLabsLanguage(language string) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		t.language = language
	}
}

func WithElevenLabsEnableSSMLParsing(enable bool) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		t.enableSSMLParsing = enable
	}
}

func WithElevenLabsEncoding(encoding string) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		if encoding != "" {
			t.encoding = encoding
			t.sampleRate = elevenLabsSampleRate(encoding)
		}
	}
}

func NewElevenLabsTTS(apiKey string, voiceID string, modelID string, opts ...ElevenLabsTTSOption) (*ElevenLabsTTS, error) {
	if voiceID == "" {
		voiceID = "hpp4J3VqNfWAUOO0d1Us"
	}
	if modelID == "" {
		modelID = "eleven_turbo_v2_5"
	}
	provider := &ElevenLabsTTS{
		apiKey:     resolveElevenLabsAPIKey(apiKey),
		baseURL:    defaultElevenLabsBaseURL,
		voiceID:    voiceID,
		modelID:    modelID,
		encoding:   "mp3_22050_32",
		sampleRate: 22050,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider, nil
}

func (t *ElevenLabsTTS) Label() string { return "elevenlabs.TTS" }
func (t *ElevenLabsTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: true}
}
func (t *ElevenLabsTTS) SampleRate() int  { return t.sampleRate }
func (t *ElevenLabsTTS) NumChannels() int { return 1 }
func (t *ElevenLabsTTS) Model() string    { return t.modelID }
func (t *ElevenLabsTTS) Provider() string { return "ElevenLabs" }

func (t *ElevenLabsTTS) UpdateOptions(opts ...ElevenLabsTTSOption) {
	for _, opt := range opts {
		opt(t)
	}
}

// Synthesize performs a full HTTP POST for non-streaming scenarios.
func (t *ElevenLabsTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateElevenLabsAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	apiURL, jsonBody := buildElevenLabsSynthesizeRequest(t, text)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("elevenlabs error: %s", string(respBody))
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "audio/") {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("elevenlabs returned non-audio data: %s", string(respBody))
	}

	return &elevenLabsChunkedStream{
		resp:       resp,
		encoding:   t.encoding,
		sampleRate: t.sampleRate,
	}, nil
}

func buildElevenLabsSynthesizeRequest(t *ElevenLabsTTS, text string) (string, []byte) {
	apiURL := fmt.Sprintf("%s/text-to-speech/%s/stream?model_id=%s&output_format=%s", strings.TrimRight(t.baseURL, "/"), t.voiceID, url.QueryEscape(t.modelID), url.QueryEscape(t.encoding))
	body := map[string]interface{}{
		"text":     text,
		"model_id": t.modelID,
	}
	if t.language != "" && elevenLabsSupportsLanguageCode(t.modelID) {
		body["language_code"] = t.language
	}
	if t.enableSSMLParsing {
		body["enable_ssml_parsing"] = true
	}
	jsonBody, _ := json.Marshal(body)
	return apiURL, jsonBody
}

type elevenLabsChunkedStream struct {
	resp       *http.Response
	encoding   string
	sampleRate int
	decoder    codecs.AudioStreamDecoder
	started    bool
	emitted    bool
}

func (s *elevenLabsChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if strings.HasPrefix(s.encoding, "mp3") {
		return s.nextDecodedMP3()
	}

	// Read PCM audio in chunks from the HTTP response
	buf := make([]byte, 8192)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF && n > 0 {
			s.emitted = true
			// Return final chunk
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              buf[:n],
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(n / 2),
				},
				IsFinal: true,
			}, nil
		}
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("elevenlabs TTS chunked pcm response read %s: %w", s.audioByteState(), err)
	}

	s.emitted = true
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *elevenLabsChunkedStream) nextDecodedMP3() (*tts.SynthesizedAudio, error) {
	if !s.started {
		s.started = true
		s.decoder = codecs.NewMP3AudioStreamDecoder()
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, fmt.Errorf("elevenlabs TTS chunked mp3 response read %s: %w", s.audioByteState(), err)
		}
		if len(data) == 0 {
			return nil, io.EOF
		}
		go func() {
			s.decoder.Push(data)
			s.decoder.EndInput()
		}()
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("elevenlabs TTS chunked mp3 decode %s: %w", s.audioByteState(), err)
	}
	frame, err = normalizeElevenLabsMP3Frame(frame, s.sampleRate)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs TTS chunked mp3 resample %s: %w", s.audioByteState(), err)
	}
	s.emitted = true
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func (s *elevenLabsChunkedStream) audioByteState() string {
	if s.emitted {
		return "after audio bytes"
	}
	return "before audio bytes"
}

func (s *elevenLabsChunkedStream) Close() error {
	if s.decoder != nil {
		_ = s.decoder.Close()
	}
	return s.resp.Body.Close()
}

// Stream establishes a high-performance WebSocket connection to ElevenLabs for low-latency streaming TTS.
func (t *ElevenLabsTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateElevenLabsAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	header := make(http.Header)
	header.Set("xi-api-key", t.apiKey)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildElevenLabsStreamURL(t), header)
	if err != nil {
		return nil, fmt.Errorf("failed to dial elevenlabs websocket: %w", err)
	}

	// Send initial configuration
	initMsg := map[string]interface{}{
		"text": " ", // Start with a space to initialize
		"voice_settings": map[string]interface{}{
			"stability":        0.5,
			"similarity_boost": 0.8,
		},
		"generation_config": map[string]interface{}{
			"chunk_length_schedule": []int{120, 160, 250, 290},
		},
	}
	if err := conn.WriteJSON(initMsg); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to write initial config to elevenlabs: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	stream := &elevenLabsStream{
		conn:       conn,
		audio:      make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
		ctx:        ctx,
		cancel:     cancel,
		encoding:   t.encoding,
		sampleRate: t.sampleRate,
	}

	go stream.readLoop()
	go stream.pingLoop()

	return stream, nil
}

func buildElevenLabsStreamURL(t *ElevenLabsTTS) string {
	streamBaseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(streamBaseURL, "http://") || strings.HasPrefix(streamBaseURL, "https://") {
		streamBaseURL = strings.Replace(streamBaseURL, "http", "ws", 1)
	}
	parsed, err := url.Parse(streamBaseURL + fmt.Sprintf("/text-to-speech/%s/stream-input", t.voiceID))
	if err != nil {
		u := url.URL{Scheme: "wss", Host: "api.elevenlabs.io", Path: fmt.Sprintf("/v1/text-to-speech/%s/stream-input", t.voiceID)}
		parsed = &u
	}
	q := parsed.Query()
	q.Set("model_id", t.modelID)
	q.Set("output_format", t.encoding)
	if t.language != "" && elevenLabsSupportsLanguageCode(t.modelID) {
		q.Set("language_code", t.language)
	}
	q.Set("enable_ssml_parsing", strconv.FormatBool(t.enableSSMLParsing))
	q.Set("enable_logging", "true")
	q.Set("inactivity_timeout", strconv.Itoa(defaultElevenLabsInactivityTimeout))
	q.Set("apply_text_normalization", "auto")
	q.Set("sync_alignment", "true")
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func elevenLabsSupportsLanguageCode(modelID string) bool {
	switch modelID {
	case "eleven_turbo_v2_5", "eleven_turbo_v2", "eleven_flash_v2_5", "eleven_flash_v2":
		return true
	default:
		return false
	}
}

func elevenLabsSampleRate(encoding string) int {
	parts := strings.Split(encoding, "_")
	if len(parts) >= 2 {
		var sampleRate int
		if _, err := fmt.Sscanf(parts[1], "%d", &sampleRate); err == nil && sampleRate > 0 {
			return sampleRate
		}
	}
	return 22050
}

type elevenLabsStream struct {
	conn   *websocket.Conn
	audio  chan *tts.SynthesizedAudio
	errCh  chan error
	mu     sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc

	encoding   string
	sampleRate int
}

type elWSResponse struct {
	Audio               string `json:"audio"`
	IsFinal             bool   `json:"isFinal"`
	NormalizedAlignment *struct {
		Chars            []string `json:"chars"`
		CharStartTimesMs []int    `json:"charStartTimesMs"`
		CharDurationsMs  []int    `json:"charDurationsMs"`
	} `json:"normalizedAlignment"`
	Alignment *struct {
		Chars            []string `json:"chars"`
		CharStartTimesMs []int    `json:"charStartTimesMs"`
		CharDurationsMs  []int    `json:"charDurationsMs"`
	} `json:"alignment"`
	Error string `json:"error,omitempty"`
}

func (s *elevenLabsStream) readLoop() {
	defer s.Close()
	defer close(s.audio)

	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				logger.Logger.Errorw("ElevenLabs WebSocket read error", err)
				s.sendError(fmt.Errorf("elevenlabs TTS websocket read: %w", err))
			}
			return
		}

		var resp elWSResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			logger.Logger.Warnw("Failed to unmarshal ElevenLabs response", err, "payload", string(message))
			continue
		}

		if resp.Error != "" {
			logger.Logger.Errorw("ElevenLabs WebSocket returned error", nil, "error", resp.Error)
			s.sendError(fmt.Errorf("elevenlabs error: %s", resp.Error))
			return
		}

		var deltaText strings.Builder
		if resp.NormalizedAlignment != nil {
			for _, char := range resp.NormalizedAlignment.Chars {
				deltaText.WriteString(char)
			}
		} else if resp.Alignment != nil {
			for _, char := range resp.Alignment.Chars {
				deltaText.WriteString(char)
			}
		}

		if resp.Audio != "" {
			audio, err := elevenLabsSynthesizedAudio(resp, s.sampleRate, s.encoding)
			if err != nil {
				logger.Logger.Errorw("Failed to decode ElevenLabs audio", err)
				s.sendError(fmt.Errorf("elevenlabs TTS websocket audio decode: %w", err))
				return
			}
			select {
			case <-s.ctx.Done():
				return
			case s.audio <- audio:
			}
		} else if resp.IsFinal || deltaText.Len() > 0 {
			// Even if there's no audio, pass alignment or final flags
			select {
			case <-s.ctx.Done():
				return
			case s.audio <- &tts.SynthesizedAudio{
				IsFinal:   resp.IsFinal,
				DeltaText: deltaText.String(),
			}:
			}
		}

		if resp.IsFinal {
			return
		}
	}
}

func elevenLabsSynthesizedAudio(resp elWSResponse, sampleRate int, encoding string) (*tts.SynthesizedAudio, error) {
	data, err := base64.StdEncoding.DecodeString(resp.Audio)
	if err != nil {
		return nil, err
	}
	var deltaText strings.Builder
	if resp.NormalizedAlignment != nil {
		for _, char := range resp.NormalizedAlignment.Chars {
			deltaText.WriteString(char)
		}
	} else if resp.Alignment != nil {
		for _, char := range resp.Alignment.Chars {
			deltaText.WriteString(char)
		}
	}

	if strings.HasPrefix(encoding, "mp3") {
		frame, err := decodeElevenLabsMP3Audio(data, sampleRate)
		if err != nil {
			return nil, err
		}
		return &tts.SynthesizedAudio{
			Frame:     frame,
			IsFinal:   resp.IsFinal,
			DeltaText: deltaText.String(),
		}, nil
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              data,
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(data) / 2),
		},
		IsFinal:   resp.IsFinal,
		DeltaText: deltaText.String(),
	}, nil
}

func decodeElevenLabsMP3Audio(data []byte, sampleRate int) (*model.AudioFrame, error) {
	decoder := codecs.NewMP3AudioStreamDecoder()
	defer decoder.Close()
	go func() {
		decoder.Push(data)
		decoder.EndInput()
	}()
	frame, err := decoder.Next()
	if err != nil {
		return nil, err
	}
	return normalizeElevenLabsMP3Frame(frame, sampleRate)
}

func normalizeElevenLabsMP3Frame(frame *model.AudioFrame, sampleRate int) (*model.AudioFrame, error) {
	frame = elevenLabsDownmixToMono(frame)
	if sampleRate <= 0 {
		return frame, nil
	}
	return coreaudio.ResampleAudioFrame(frame, uint32(sampleRate))
}

func elevenLabsDownmixToMono(frame *model.AudioFrame) *model.AudioFrame {
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

func (s *elevenLabsStream) pingLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if !s.closed {
				_ = s.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))
			}
			s.mu.Unlock()
		}
	}
}

func (s *elevenLabsStream) sendError(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *elevenLabsStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	msg := map[string]interface{}{
		"text":                   text,
		"try_trigger_generation": true,
	}
	if err := s.conn.WriteJSON(msg); err != nil {
		s.closeAfterWriteFailureLocked()
		return fmt.Errorf("failed to write text to elevenlabs: %w", err)
	}
	return nil
}

func (s *elevenLabsStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if err := s.conn.WriteJSON(elevenLabsFlushPayload()); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func elevenLabsFlushPayload() map[string]interface{} {
	return map[string]interface{}{"text": ""}
}

func (s *elevenLabsStream) closeAfterWriteFailureLocked() {
	s.closed = true
	s.cancel()
	_ = s.conn.Close()
}

func (s *elevenLabsStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	// Clean close via empty text
	_ = s.conn.WriteJSON(map[string]interface{}{"text": ""})
	// Wait a moment for final chunks
	time.Sleep(50 * time.Millisecond)
	return s.conn.Close()
}

func (s *elevenLabsStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case <-s.ctx.Done():
		return nil, io.EOF
	case err := <-s.errCh:
		return nil, err
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
	}
}
