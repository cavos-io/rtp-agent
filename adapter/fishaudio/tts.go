package fishaudio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
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
	fishAudioReferenceAPIKeyEnv = "FISH_API_KEY"
	fishAudioPrimaryAPIKeyEnv   = "FISHAUDIO_API_KEY"
	fishAudioFallbackAPIKeyEnv  = "FISH_AUDIO_API_KEY"
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
	mu           sync.Mutex
	streams      map[*fishAudioTTSSynthesizeStream]struct{}
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
	if apiKey == "" {
		apiKey = os.Getenv(fishAudioReferenceAPIKeyEnv)
	}
	if apiKey == "" {
		apiKey = os.Getenv(fishAudioPrimaryAPIKeyEnv)
	}
	if apiKey == "" {
		apiKey = os.Getenv(fishAudioFallbackAPIKeyEnv)
	}
	provider := &FishAudioTTS{
		apiKey:       apiKey,
		baseURL:      defaultFishAudioBaseURL,
		model:        defaultFishAudioModel,
		voice:        voice,
		outputFormat: defaultFishAudioFormat,
		sampleRate:   defaultFishAudioSampleRate(defaultFishAudioFormat),
		latencyMode:  defaultFishAudioLatencyMode,
		chunkLength:  defaultFishAudioChunkLength,
		streams:      make(map[*fishAudioTTSSynthesizeStream]struct{}),
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
func (t *FishAudioTTS) Model() string    { return t.model }
func (t *FishAudioTTS) Provider() string { return "FishAudio" }

func (t *FishAudioTTS) UpdateOptions(opts ...FishAudioTTSOption) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	before := t.chunkLength
	for _, opt := range opts {
		opt(t)
	}
	if t.chunkLength < 100 || t.chunkLength > 300 {
		t.chunkLength = before
		return fmt.Errorf("chunk_length must be between 100 and 300")
	}
	return nil
}

func (t *FishAudioTTS) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	streams := make([]*fishAudioTTSSynthesizeStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*fishAudioTTSSynthesizeStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *FishAudioTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateFishAudioAPIKey(t.apiKey); err != nil {
		return nil, err
	}

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
		format:     t.outputFormat,
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

func validateFishAudioAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("fish audio API key is required, either as argument or set FISH_API_KEY environment variable")
	}
	return nil
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
	if err := validateFishAudioAPIKey(t.apiKey); err != nil {
		return nil, err
	}

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
		owner:      t,
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
		format:     t.outputFormat,
		events:     make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
	}
	stream.writeMessage = stream.writeWebsocketMessage
	stream.closeConn = stream.closeWebsocketConn
	t.registerStream(stream)
	go stream.readLoop()
	return stream, nil
}

func (t *FishAudioTTS) registerStream(stream *fishAudioTTSSynthesizeStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.streams == nil {
		t.streams = make(map[*fishAudioTTSSynthesizeStream]struct{})
	}
	t.streams[stream] = struct{}{}
	stream.owner = t
}

func (t *FishAudioTTS) unregisterStream(stream *fishAudioTTSSynthesizeStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
	if stream.owner == t {
		stream.owner = nil
	}
}

type fishaudioTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	format     string
}

func (s *fishaudioTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.format == "wav" {
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, io.EOF
		}
		return fishAudioDecodeTTSFrame(data, s.sampleRate, s.format)
	}

	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}

	return fishAudioDecodeTTSFrame(buf[:n], s.sampleRate, s.format)
}

func (s *fishaudioTTSChunkedStream) Close() error {
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	return body.Close()
}

type fishAudioTTSSynthesizeStream struct {
	owner      *FishAudioTTS
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	sampleRate int
	format     string
	events     chan *tts.SynthesizedAudio
	errCh      chan error
	mu         sync.Mutex
	closed     bool

	writeMessage func(int, []byte) error
	closeConn    func() error
}

func (s *fishAudioTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("fishaudio tts stream is closed")
	}
	message, err := buildFishAudioTTSTextMessage(text)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(websocket.BinaryMessage, message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *fishAudioTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("fishaudio tts stream is closed")
	}
	message, err := buildFishAudioTTSSimpleEvent("flush")
	if err != nil {
		return err
	}
	if err := s.writeMessageData(websocket.BinaryMessage, message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *fishAudioTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if stopMessage, err := buildFishAudioTTSSimpleEvent("stop"); err == nil {
		_ = s.writeMessageData(websocket.BinaryMessage, stopMessage)
	}
	if s.conn != nil {
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}
	err := s.closeConnection()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
	return err
}

func (s *fishAudioTTSSynthesizeStream) writeMessageData(messageType int, data []byte) error {
	if s.writeMessage != nil {
		return s.writeMessage(messageType, data)
	}
	return s.writeWebsocketMessage(messageType, data)
}

func (s *fishAudioTTSSynthesizeStream) writeWebsocketMessage(messageType int, data []byte) error {
	return s.conn.WriteMessage(messageType, data)
}

func (s *fishAudioTTSSynthesizeStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *fishAudioTTSSynthesizeStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *fishAudioTTSSynthesizeStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	_ = s.closeConnection()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
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
		audio, done, err := fishAudioTTSAudioFromStreamMessage(payload, s.sampleRate, s.format)
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

func fishAudioTTSAudioFromStreamMessage(payload []byte, sampleRate int, format string) (*tts.SynthesizedAudio, bool, error) {
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
		decoded, err := fishAudioDecodeTTSFrame(audio, sampleRate, format)
		if err != nil {
			return nil, false, err
		}
		return decoded, false, nil
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

func fishAudioDecodeTTSFrame(audio []byte, sampleRate int, format string) (*tts.SynthesizedAudio, error) {
	if format == "wav" {
		frame, err := decodeFishAudioWAVPCM16(audio)
		if err != nil {
			return nil, err
		}
		return &tts.SynthesizedAudio{Frame: frame}, nil
	}
	return fishAudioTTSAudioFrame(audio, sampleRate), nil
}

func decodeFishAudioWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid fishaudio wav data")
	}
	offset := 12
	var sampleRate uint32
	var channels uint16
	var bitsPerSample uint16
	var pcm []byte
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 || offset+chunkSize > len(data) {
			return nil, fmt.Errorf("invalid fishaudio wav chunk size")
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("invalid fishaudio wav fmt chunk")
			}
			audioFormat := binary.LittleEndian.Uint16(data[offset : offset+2])
			channels = binary.LittleEndian.Uint16(data[offset+2 : offset+4])
			sampleRate = binary.LittleEndian.Uint32(data[offset+4 : offset+8])
			bitsPerSample = binary.LittleEndian.Uint16(data[offset+14 : offset+16])
			if audioFormat != 1 || bitsPerSample != 16 {
				return nil, fmt.Errorf("unsupported fishaudio wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
			}
		case "data":
			pcm = bytes.Clone(data[offset : offset+chunkSize])
		}
		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}
	if sampleRate == 0 || channels == 0 || bitsPerSample == 0 {
		return nil, fmt.Errorf("missing fishaudio wav format metadata")
	}
	if pcm == nil {
		return nil, fmt.Errorf("missing fishaudio wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcm,
		SampleRate:        sampleRate,
		NumChannels:       uint32(channels),
		SamplesPerChannel: uint32(len(pcm) / int(channels) / 2),
	}, nil
}
