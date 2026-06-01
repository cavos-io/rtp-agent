package minimax

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultMinimaxBaseURL     = "https://api-uw.minimax.io"
	defaultMinimaxModel       = "speech-02-turbo"
	defaultMinimaxVoice       = "socialmedia_female_2_v1"
	defaultMinimaxSampleRate  = 24000
	defaultMinimaxBitrate     = 128000
	defaultMinimaxAudioFormat = "mp3"
)

type MinimaxTTS struct {
	apiKey            string
	baseURL           string
	model             string
	voice             string
	sampleRate        int
	bitrate           int
	audioFormat       string
	emotion           string
	speed             float64
	vol               float64
	pitch             int
	textNormalization bool
}

type MinimaxTTSOption func(*MinimaxTTS)

func WithMinimaxTTSBaseURL(baseURL string) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithMinimaxTTSModel(model string) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithMinimaxTTSVoice(voice string) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithMinimaxTTSSampleRate(sampleRate int) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithMinimaxTTSBitrate(bitrate int) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if bitrate > 0 {
			t.bitrate = bitrate
		}
	}
}

func WithMinimaxTTSAudioFormat(audioFormat string) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if audioFormat != "" {
			t.audioFormat = audioFormat
		}
	}
}

func WithMinimaxTTSEmotion(emotion string) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		t.emotion = emotion
	}
}

func WithMinimaxTTSSpeed(speed float64) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		t.speed = speed
	}
}

func WithMinimaxTTSVolume(vol float64) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		t.vol = vol
	}
}

func WithMinimaxTTSPitch(pitch int) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		t.pitch = pitch
	}
}

func WithMinimaxTTSTextNormalization(enabled bool) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		t.textNormalization = enabled
	}
}

func NewMinimaxTTS(apiKey string, voice string, opts ...MinimaxTTSOption) *MinimaxTTS {
	provider := &MinimaxTTS{
		apiKey:      apiKey,
		baseURL:     defaultMinimaxBaseURL,
		model:       defaultMinimaxModel,
		voice:       voice,
		sampleRate:  defaultMinimaxSampleRate,
		bitrate:     defaultMinimaxBitrate,
		audioFormat: defaultMinimaxAudioFormat,
		speed:       1.0,
		vol:         1.0,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultMinimaxVoice
	}
	return provider
}

func (t *MinimaxTTS) Label() string { return "minimax.TTS" }
func (t *MinimaxTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *MinimaxTTS) SampleRate() int  { return t.sampleRate }
func (t *MinimaxTTS) NumChannels() int { return 1 }

func (t *MinimaxTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildMinimaxTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("minimax tts error: %s", string(respBody))
	}

	return &minimaxTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildMinimaxTTSRequest(ctx context.Context, t *MinimaxTTS, text string) (*http.Request, error) {
	reqBody := minimaxOptions(t)
	reqBody["text"] = text
	reqBody["stream"] = true
	reqBody["stream_options"] = map[string]interface{}{
		"exclude_aggregated_audio": true,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/v1/t2a_v2", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

func minimaxOptions(t *MinimaxTTS) map[string]interface{} {
	voiceSetting := map[string]interface{}{
		"voice_id": t.voice,
		"speed":    t.speed,
		"vol":      t.vol,
		"pitch":    t.pitch,
	}
	if t.emotion != "" {
		voiceSetting["emotion"] = t.emotion
	}

	return map[string]interface{}{
		"model":         t.model,
		"voice_setting": voiceSetting,
		"audio_setting": map[string]interface{}{
			"sample_rate": t.sampleRate,
			"bitrate":     t.bitrate,
			"format":      t.audioFormat,
			"channel":     1,
		},
		"text_normalization": t.textNormalization,
	}
}

func (t *MinimaxTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildMinimaxTTSWebsocketURL(t), buildMinimaxTTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial minimax tts websocket: %w", err)
	}
	startPayload, err := buildMinimaxTTSTaskStartMessage(t)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, startPayload); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &minimaxTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
		segmentID:  fmt.Sprintf("seg-%d", time.Now().UnixNano()),
	}, nil
}

func buildMinimaxTTSWebsocketURL(t *MinimaxTTS) string {
	baseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(baseURL, "https://") {
		baseURL = "wss://" + strings.TrimPrefix(baseURL, "https://")
	} else if strings.HasPrefix(baseURL, "http://") {
		baseURL = "ws://" + strings.TrimPrefix(baseURL, "http://")
	}
	return baseURL + "/ws/v1/t2a_v2"
}

func buildMinimaxTTSWebsocketHeaders(t *MinimaxTTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+t.apiKey)
	return headers
}

func buildMinimaxTTSTaskStartMessage(t *MinimaxTTS) ([]byte, error) {
	message := minimaxOptions(t)
	message["event"] = "task_start"
	return json.Marshal(message)
}

func buildMinimaxTTSTaskContinueMessage(text string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"event": "task_continue",
		"text":  text,
	})
}

func buildMinimaxTTSTaskFinishMessage() ([]byte, error) {
	return json.Marshal(map[string]any{"event": "task_finish"})
}

type minimaxTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	scanner    *bufio.Scanner
	requestID  string
}

func (s *minimaxTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.requestID == "" {
		s.requestID = s.resp.Header.Get("Trace-Id")
		if s.requestID == "" {
			s.requestID = s.resp.Header.Get("X-Trace-Id")
		}
	}
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp.Body)
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		audio, err := minimaxAudioFromSSELine(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		if err != nil {
			return nil, err
		}
		if len(audio) == 0 {
			continue
		}
		return &tts.SynthesizedAudio{
			RequestID: s.requestID,
			Frame: &model.AudioFrame{
				Data:              audio,
				SampleRate:        uint32(s.sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(len(audio) / 2),
			},
		}, nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *minimaxTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

type minimaxTTSWebsocketChunkedStream struct {
	conn       *websocket.Conn
	sampleRate int
	segmentID  string
}

func (s *minimaxTTSWebsocketChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.conn == nil {
		return nil, io.EOF
	}
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || err == io.EOF {
				return nil, io.EOF
			}
			return nil, err
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := minimaxAudioFromWebsocketMessage(payload, s.sampleRate, s.segmentID)
		if err != nil {
			return nil, err
		}
		if done {
			return nil, io.EOF
		}
		if audio != nil {
			return audio, nil
		}
	}
}

func (s *minimaxTTSWebsocketChunkedStream) Close() error {
	if s.conn == nil {
		return nil
	}
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

type minimaxTTSSynthesizeStream struct {
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	sampleRate  int
	segmentID   string
	pendingText bytes.Buffer
	mu          sync.Mutex
	closed      bool
}

func (s *minimaxTTSSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if text == "" {
		return nil
	}
	_, err := s.pendingText.WriteString(text)
	return err
}

func (s *minimaxTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := strings.TrimSpace(s.pendingText.String())
	s.pendingText.Reset()
	if s.conn == nil {
		return nil
	}
	if text != "" {
		payload, err := buildMinimaxTTSTaskContinueMessage(text)
		if err != nil {
			return err
		}
		if err := s.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			return err
		}
	}
	finishPayload, err := buildMinimaxTTSTaskFinishMessage()
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, finishPayload)
}

func (s *minimaxTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn == nil {
		return nil
	}
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *minimaxTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	if s.ctx != nil {
		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		default:
		}
	}
	return (&minimaxTTSWebsocketChunkedStream{conn: s.conn, sampleRate: s.sampleRate, segmentID: s.segmentID}).Next()
}

func minimaxAudioFromWebsocketMessage(payload []byte, sampleRate int, segmentID string) (*tts.SynthesizedAudio, bool, error) {
	var data struct {
		Event   string `json:"event"`
		TraceID string `json:"trace_id"`
		IsFinal bool   `json:"is_final"`
		Data    struct {
			Audio string `json:"audio"`
		} `json:"data"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
			TraceID    string `json:"trace_id"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, false, err
	}
	traceID := data.TraceID
	if traceID == "" {
		traceID = data.BaseResp.TraceID
	}
	if data.BaseResp.StatusCode != 0 {
		if data.BaseResp.StatusMsg == "" {
			data.BaseResp.StatusMsg = "unknown error"
		}
		return nil, false, fmt.Errorf("minimax error [%d]: %s", data.BaseResp.StatusCode, data.BaseResp.StatusMsg)
	}
	switch data.Event {
	case "task_continued":
		if data.Data.Audio == "" {
			return nil, false, nil
		}
		audio, err := hex.DecodeString(data.Data.Audio)
		if err != nil {
			return nil, false, err
		}
		return &tts.SynthesizedAudio{
			RequestID: traceID,
			SegmentID: segmentID,
			Frame: &model.AudioFrame{
				Data:              audio,
				SampleRate:        uint32(sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(len(audio) / 2),
			},
		}, false, nil
	case "task_finished":
		return nil, true, nil
	case "task_failed":
		return nil, false, fmt.Errorf("minimax task failed: %s", string(payload))
	default:
		return nil, false, nil
	}
}

func minimaxAudioFromSSELine(line string) ([]byte, error) {
	var data struct {
		Data struct {
			Audio string `json:"audio"`
		} `json:"data"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		return nil, err
	}
	if data.BaseResp.StatusCode != 0 {
		if data.BaseResp.StatusMsg == "" {
			data.BaseResp.StatusMsg = "unknown error"
		}
		return nil, fmt.Errorf("minimax error [%d]: %s", data.BaseResp.StatusCode, data.BaseResp.StatusMsg)
	}
	if data.Data.Audio == "" {
		return nil, nil
	}
	return hex.DecodeString(data.Data.Audio)
}
