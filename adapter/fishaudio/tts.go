package fishaudio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	defaultFishAudioModel       = "s2-pro"
	defaultFishAudioVoiceID     = "933563129e564b19a115bedd57b7406a"
	defaultFishAudioBaseURL     = "https://api.fish.audio"
	defaultFishAudioFormat      = "wav"
	defaultFishAudioLatencyMode = "balanced"
	defaultFishAudioChunkLength = 100
	fishAudioTTSUserAgent       = "livekit-plugins-fishaudio/go"
)

type FishAudioTTS struct {
	apiKey       string
	baseURL      string
	model        string
	voice        string
	outputFormat string
	sampleRate   int
	latencyMode  string
	chunkLength  int
}

type FishAudioTTSOption func(*FishAudioTTS)

func WithFishAudioTTSBaseURL(baseURL string) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithFishAudioTTSModel(model string) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithFishAudioTTSVoice(voice string) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithFishAudioTTSOutputFormat(outputFormat string) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if outputFormat != "" {
			t.outputFormat = outputFormat
			t.sampleRate = defaultFishAudioSampleRate(outputFormat)
		}
	}
}

func WithFishAudioTTSSampleRate(sampleRate int) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithFishAudioTTSLatencyMode(latencyMode string) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if latencyMode != "" {
			t.latencyMode = latencyMode
		}
	}
}

func WithFishAudioTTSChunkLength(chunkLength int) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if chunkLength > 0 {
			t.chunkLength = chunkLength
		}
	}
}

func NewFishAudioTTS(apiKey string, voice string, opts ...FishAudioTTSOption) *FishAudioTTS {
	provider := &FishAudioTTS{
		apiKey:       apiKey,
		baseURL:      defaultFishAudioBaseURL,
		model:        defaultFishAudioModel,
		voice:        voice,
		outputFormat: defaultFishAudioFormat,
		sampleRate:   defaultFishAudioSampleRate(defaultFishAudioFormat),
		latencyMode:  defaultFishAudioLatencyMode,
		chunkLength:  defaultFishAudioChunkLength,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultFishAudioVoiceID
	}
	return provider
}

func defaultFishAudioSampleRate(outputFormat string) int {
	switch outputFormat {
	case "opus":
		return 48000
	case "mp3":
		return 32000
	default:
		return 24000
	}
}

func (t *FishAudioTTS) Label() string { return "fishaudio.TTS" }
func (t *FishAudioTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *FishAudioTTS) SampleRate() int  { return t.sampleRate }
func (t *FishAudioTTS) NumChannels() int { return 1 }

func (t *FishAudioTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildFishAudioTTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("fishaudio tts error: %s", string(respBody))
	}

	return &fishaudioTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildFishAudioTTSRequest(ctx context.Context, t *FishAudioTTS, text string) (*http.Request, error) {
	packedBody, err := msgpack.Marshal(fishAudioTTSRequestPayload(t, text))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/v1/tts", bytes.NewBuffer(packedBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/msgpack")
	req.Header.Set("model", t.model)
	return req, nil
}

func fishAudioTTSRequestPayload(t *FishAudioTTS, text string) map[string]interface{} {
	return map[string]interface{}{
		"text":         text,
		"chunk_length": t.chunkLength,
		"format":       t.outputFormat,
		"sample_rate":  t.sampleRate,
		"mp3_bitrate":  64,
		"opus_bitrate": 64000,
		"references":   []interface{}{},
		"reference_id": t.voice,
		"normalize":    true,
		"latency":      t.latencyMode,
		"prosody":      nil,
		"top_p":        0.7,
		"temperature":  0.7,
	}
}

func buildFishAudioTTSWebsocketURL(t *FishAudioTTS) string {
	baseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	}
	return baseURL + "/v1/tts/live"
}

func buildFishAudioTTSWebsocketHeaders(t *FishAudioTTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+t.apiKey)
	headers.Set("User-Agent", fishAudioTTSUserAgent)
	headers.Set("model", t.model)
	return headers
}

func buildFishAudioTTSStartMessage(t *FishAudioTTS) ([]byte, error) {
	return msgpack.Marshal(map[string]interface{}{
		"event":   "start",
		"request": fishAudioTTSRequestPayload(t, ""),
	})
}

func buildFishAudioTTSTextMessage(text string) ([]byte, error) {
	return msgpack.Marshal(map[string]interface{}{
		"event": "text",
		"text":  text + " ",
	})
}

func buildFishAudioTTSSimpleEvent(event string) ([]byte, error) {
	return msgpack.Marshal(map[string]interface{}{"event": event})
}

func (t *FishAudioTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildFishAudioTTSWebsocketURL(t), buildFishAudioTTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial fishaudio tts websocket: %w", err)
	}
	startMessage, err := buildFishAudioTTSStartMessage(t)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, startMessage); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &fishAudioTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
		events:     make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
	}
	go stream.readLoop()
	return stream, nil
}

type fishaudioTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *fishaudioTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *fishaudioTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

type fishAudioTTSSynthesizeStream struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	sampleRate int
	events     chan *tts.SynthesizedAudio
	errCh      chan error
	mu         sync.Mutex
	closed     bool
}

func (s *fishAudioTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	message, err := buildFishAudioTTSTextMessage(text)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, message)
}

func (s *fishAudioTTSSynthesizeStream) Flush() error {
	message, err := buildFishAudioTTSSimpleEvent("flush")
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, message)
}

func (s *fishAudioTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if stopMessage, err := buildFishAudioTTSSimpleEvent("stop"); err == nil {
		_ = s.conn.WriteMessage(websocket.BinaryMessage, stopMessage)
	}
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *fishAudioTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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
		return nil, s.ctx.Err()
	}
}

func (s *fishAudioTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
			}
			return
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		audio, done, err := fishAudioTTSAudioFromStreamMessage(payload, s.sampleRate)
		if err != nil {
			s.errCh <- err
			return
		}
		if audio != nil {
			s.events <- audio
		}
		if done {
			return
		}
	}
}

func fishAudioTTSAudioFromStreamMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message map[string]interface{}
	if err := msgpack.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	event, _ := message["event"].(string)
	switch event {
	case "audio":
		audio, ok := fishAudioBytes(message["audio"])
		if !ok || len(audio) == 0 {
			return nil, false, nil
		}
		return fishAudioTTSAudioFrame(audio, sampleRate), false, nil
	case "finish":
		if reason, _ := message["reason"].(string); reason == "error" {
			return nil, false, fmt.Errorf("fishaudio tts stream finished with error")
		}
		return nil, true, nil
	default:
		return nil, false, nil
	}
}

func fishAudioBytes(value interface{}) ([]byte, bool) {
	switch v := value.(type) {
	case []byte:
		return v, true
	case string:
		return []byte(v), true
	default:
		return nil, false
	}
}

func fishAudioTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
