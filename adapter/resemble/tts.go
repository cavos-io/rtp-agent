package resemble

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	resembleRESTAPIURL        = "https://f.cluster.resemble.ai/synthesize"
	resembleWebsocketURL      = "wss://websocket.cluster.resemble.ai/stream"
	defaultResembleVoiceUUID  = "55592656"
	defaultResembleSampleRate = 44100
)

type ResembleTTS struct {
	apiKey     string
	voice      string
	sampleRate int
	model      string
}

type ResembleTTSOption func(*ResembleTTS)

func WithResembleTTSVoice(voice string) ResembleTTSOption {
	return func(t *ResembleTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithResembleTTSSampleRate(sampleRate int) ResembleTTSOption {
	return func(t *ResembleTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithResembleTTSModel(model string) ResembleTTSOption {
	return func(t *ResembleTTS) {
		t.model = model
	}
}

func NewResembleTTS(apiKey string, voice string, opts ...ResembleTTSOption) *ResembleTTS {
	if apiKey == "" {
		apiKey = os.Getenv("RESEMBLE_API_KEY")
	}
	provider := &ResembleTTS{
		apiKey:     apiKey,
		voice:      voice,
		sampleRate: defaultResembleSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultResembleVoiceUUID
	}
	return provider
}

func (t *ResembleTTS) Label() string { return "resemble.TTS" }
func (t *ResembleTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *ResembleTTS) SampleRate() int  { return t.sampleRate }
func (t *ResembleTTS) NumChannels() int { return 1 }
func (t *ResembleTTS) Model() string {
	if t.model == "" {
		return "unknown"
	}
	return t.model
}
func (t *ResembleTTS) Provider() string { return "Resemble" }

func (t *ResembleTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateResembleAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	req, err := buildResembleTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("resemble tts error: %s", string(respBody))
	}

	return &resembleTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildResembleTTSRequest(ctx context.Context, t *ResembleTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"voice_uuid":  t.voice,
		"data":        text,
		"sample_rate": t.sampleRate,
		"precision":   "PCM_16",
	}
	if t.model != "" {
		reqBody["model"] = t.model
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resembleRESTAPIURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (t *ResembleTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateResembleAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildResembleTTSWebsocketURL(), buildResembleTTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial resemble tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &resembleTTSSynthesizeStream{
		conn:     conn,
		ctx:      streamCtx,
		cancel:   cancel,
		provider: t,
		events:   make(chan *tts.SynthesizedAudio, 100),
		errCh:    make(chan error, 1),
	}
	go stream.readLoop()
	return stream, nil
}

func validateResembleAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("resemble API key is required, either as argument or set RESEMBLE_API_KEY environment variable")
	}
	return nil
}

func buildResembleTTSWebsocketURL() string {
	return resembleWebsocketURL
}

func buildResembleTTSWebsocketHeaders(t *ResembleTTS) http.Header {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+t.apiKey)
	return header
}

func buildResembleTTSWebsocketMessage(t *ResembleTTS, text string, requestID int) ([]byte, error) {
	message := map[string]interface{}{
		"voice_uuid":    t.voice,
		"data":          text,
		"request_id":    requestID,
		"sample_rate":   t.sampleRate,
		"precision":     "PCM_16",
		"output_format": "mp3",
	}
	if t.model != "" {
		message["model"] = t.model
	}
	return json.Marshal(message)
}

type resembleTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	done       bool
}

func (s *resembleTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true

	var result struct {
		Success      bool     `json:"success"`
		AudioContent string   `json:"audio_content"`
		Issues       []string `json:"issues"`
	}
	if err := json.NewDecoder(s.resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.Success {
		issues := "unknown error"
		if len(result.Issues) > 0 {
			issues = strings.Join(result.Issues, "; ")
		}
		return nil, fmt.Errorf("resemble api returned failure: %s", issues)
	}
	audio, err := base64.StdEncoding.DecodeString(result.AudioContent)
	if err != nil {
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              audio,
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}, nil
}

func (s *resembleTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

type resembleTTSSynthesizeStream struct {
	conn      *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	provider  *ResembleTTS
	events    chan *tts.SynthesizedAudio
	errCh     chan error
	mu        sync.Mutex
	closed    bool
	requestID int
	lastID    int
	flushed   bool
}

func (s *resembleTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("resemble tts stream is closed")
	}
	s.requestID++
	s.lastID = s.requestID
	message, err := buildResembleTTSWebsocketMessage(s.provider, text, s.requestID)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *resembleTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	s.flushed = true
	s.mu.Unlock()
	return nil
}

func (s *resembleTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *resembleTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *resembleTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, requestID, err := resembleTTSAudioFromWebsocketMessage(payload)
		if err != nil {
			s.errCh <- err
			return
		}
		if audio != nil {
			s.events <- audio
		}
		if done && s.shouldStopAfterAudioEnd(requestID) {
			return
		}
	}
}

func (s *resembleTTSSynthesizeStream) shouldStopAfterAudioEnd(requestID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushed && requestID >= s.lastID
}

func resembleTTSAudioFromWebsocketMessage(payload []byte) (*tts.SynthesizedAudio, bool, int, error) {
	var message struct {
		Type         string `json:"type"`
		AudioContent string `json:"audio_content"`
		RequestID    int    `json:"request_id"`
		Message      string `json:"message"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, 0, err
	}
	switch message.Type {
	case "audio":
		if message.AudioContent == "" {
			return nil, false, message.RequestID, nil
		}
		audio, err := base64.StdEncoding.DecodeString(message.AudioContent)
		if err != nil {
			return nil, false, message.RequestID, err
		}
		if len(audio) == 0 {
			return nil, false, message.RequestID, nil
		}
		decoded, err := resembleTTSMP3AudioFrame(audio)
		if err != nil {
			return nil, false, message.RequestID, err
		}
		return decoded, false, message.RequestID, nil
	case "audio_end":
		return nil, true, message.RequestID, nil
	case "error":
		if message.Message == "" {
			message.Message = string(payload)
		}
		return nil, false, message.RequestID, fmt.Errorf("resemble tts stream error: %s", message.Message)
	default:
		return nil, false, message.RequestID, nil
	}
}

func resembleTTSMP3AudioFrame(audio []byte) (*tts.SynthesizedAudio, error) {
	decoder := codecs.NewMP3AudioStreamDecoder()
	defer decoder.Close()
	go func() {
		decoder.Push(audio)
		decoder.EndInput()
	}()
	frame, err := decoder.Next()
	if err != nil {
		return nil, err
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
}
