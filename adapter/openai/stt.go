package openai

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
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
	"github.com/sashabaranov/go-openai"
)

const (
	defaultOpenAIBaseURL               = "https://api.openai.com/v1"
	openAIRealtimeSTTSampleRate        = 24000
	openAIRealtimeSTTNumChannels       = 1
	openAIRealtimeSTTDefaultThreshold  = 0.5
	openAIRealtimeSTTPrefixPaddingMS   = 600
	openAIRealtimeSTTSilenceDurationMS = 350
	openAIAPIKeyEnv                    = "OPENAI_API_KEY"
)

type OpenAISTT struct {
	client         *openai.Client
	httpClient     openai.HTTPDoer
	apiKey         string
	baseURL        string
	model          string
	language       string
	detectLanguage bool
	prompt         string
	noiseReduction string
	useRealtime    bool
	dialWebsocket  openAIRealtimeSTTWebsocketDialer
}

type OpenAISTTOption func(*OpenAISTT)

type openAIRealtimeSTTWebsocketDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

func WithOpenAISTTLanguage(language string) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.language = language
	}
}

func WithOpenAISTTDetectLanguage(detect bool) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.detectLanguage = detect
	}
}

func WithOpenAISTTPrompt(prompt string) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.prompt = prompt
	}
}

func WithOpenAISTTNoiseReductionType(noiseReductionType string) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.noiseReduction = noiseReductionType
	}
}

func WithOpenAISTTRealtime(useRealtime bool) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.useRealtime = useRealtime
	}
}

func WithOpenAISTTBaseURL(baseURL string) OpenAISTTOption {
	return func(s *OpenAISTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func withOpenAISTTHTTPClient(client openai.HTTPDoer) OpenAISTTOption {
	return func(s *OpenAISTT) {
		if client != nil {
			s.httpClient = client
		}
	}
}

func NewOpenAISTT(apiKey string, model string, opts ...OpenAISTTOption) (*OpenAISTT, error) {
	if apiKey == "" {
		apiKey = os.Getenv(openAIAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required, either as argument or set OPENAI_API_KEY environment variable")
	}
	if model == "" {
		model = "gpt-4o-mini-transcribe"
	}
	provider := &OpenAISTT{
		apiKey:        apiKey,
		baseURL:       defaultOpenAIBaseURL,
		model:         model,
		language:      "en",
		dialWebsocket: defaultOpenAIRealtimeSTTWebsocketDialer,
	}
	for _, opt := range opts {
		opt(provider)
	}
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = provider.baseURL
	if provider.httpClient != nil {
		config.HTTPClient = provider.httpClient
	}
	provider.client = openai.NewClientWithConfig(config)
	return provider, nil
}

func (s *OpenAISTT) Label() string    { return "openai.STT" }
func (s *OpenAISTT) Provider() string { return "openai" }
func (s *OpenAISTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: s.useRealtime, InterimResults: s.useRealtime, Diarization: false, AlignedTranscript: "word", OfflineRecognize: true}
}

func (s *OpenAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if !s.useRealtime {
		return nil, fmt.Errorf("openai realtime stt is not enabled")
	}
	conn, _, err := s.dialWebsocket(ctx, buildOpenAIRealtimeSTTWebsocketURL(s).String(), buildOpenAIRealtimeSTTHeaders(s))
	if err != nil {
		return nil, fmt.Errorf("failed to dial openai realtime stt websocket: %w", err)
	}
	if language != "" {
		s.language = language
	}
	sessionUpdate, err := buildOpenAIRealtimeSTTSessionUpdate(s)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, sessionUpdate); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &openAIRealtimeSTTStream{
		conn:   conn,
		ctx:    streamCtx,
		cancel: cancel,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		state: &openAIRealtimeSTTMessageState{
			language: s.language,
			timing:   map[string]openAIRealtimeSTTTiming{},
		},
	}
	go stream.readLoop()
	return stream, nil
}

func defaultOpenAIRealtimeSTTWebsocketDialer(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
}

func buildOpenAIRealtimeSTTWebsocketURL(s *OpenAISTT) *url.URL {
	baseURL := strings.TrimRight(s.baseURL, "/")
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	}
	wsURL, err := url.Parse(baseURL + "/realtime")
	if err != nil {
		return &url.URL{Scheme: "wss", Host: strings.TrimPrefix(baseURL, "wss://"), Path: "/realtime"}
	}
	query := wsURL.Query()
	query.Set("intent", "transcription")
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func buildOpenAIRealtimeSTTHeaders(s *OpenAISTT) http.Header {
	headers := make(http.Header)
	headers.Set("User-Agent", "LiveKit Agents")
	headers.Set("Authorization", "Bearer "+s.apiKey)
	return headers
}

func buildOpenAIRealtimeSTTSessionUpdate(s *OpenAISTT) ([]byte, error) {
	transcription := map[string]interface{}{"model": s.model}
	if s.prompt != "" {
		transcription["prompt"] = s.prompt
	}
	if s.language != "" {
		transcription["language"] = s.language
	}
	input := map[string]interface{}{
		"format": map[string]interface{}{
			"type": "audio/pcm",
			"rate": openAIRealtimeSTTSampleRate,
		},
		"transcription": transcription,
	}
	if !openAIRealtimeIsWhisperModel(s.model) {
		input["turn_detection"] = map[string]interface{}{
			"type":                "server_vad",
			"threshold":           openAIRealtimeSTTDefaultThreshold,
			"prefix_padding_ms":   openAIRealtimeSTTPrefixPaddingMS,
			"silence_duration_ms": openAIRealtimeSTTSilenceDurationMS,
		}
	}
	if s.noiseReduction != "" {
		input["noise_reduction"] = map[string]interface{}{
			"type": s.noiseReduction,
		}
	}
	return json.Marshal(map[string]interface{}{
		"type": "session.update",
		"session": map[string]interface{}{
			"type": "transcription",
			"audio": map[string]interface{}{
				"input": input,
			},
		},
	})
}

func openAIRealtimeIsWhisperModel(model string) bool {
	return strings.HasPrefix(model, "gpt-realtime-whisper")
}

func buildOpenAIRealtimeSTTAudioAppendMessage(frame *model.AudioFrame) ([]byte, error) {
	if frame == nil {
		return json.Marshal(map[string]interface{}{
			"type":  "input_audio_buffer.append",
			"audio": "",
		})
	}
	return json.Marshal(map[string]interface{}{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString(frame.Data),
	})
}

func buildOpenAIRealtimeSTTCommitMessage() ([]byte, error) {
	return json.Marshal(map[string]interface{}{"type": "input_audio_buffer.commit"})
}

func (s *OpenAISTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	// Concatenate frames into a single buffer
	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	req := openAIAudioRequest(s, bytes.NewReader(buf.Bytes()), language)

	resp, err := s.client.CreateTranscription(ctx, req)
	if err != nil {
		return nil, err
	}

	return openAISpeechEvent(resp), nil
}

func openAIAudioRequest(s *OpenAISTT, reader io.Reader, language string) openai.AudioRequest {
	requestLanguage := s.language
	if language != "" {
		requestLanguage = language
	}
	if s.detectLanguage {
		requestLanguage = ""
	}
	req := openai.AudioRequest{
		Model:    s.model,
		FilePath: "audio.wav", // Static filename required by API when Reader is used.
		Reader:   reader,
		Language: requestLanguage,
		Prompt:   s.prompt,
		Format:   openai.AudioResponseFormatJSON,
	}
	if s.model == "whisper-1" {
		req.Format = openai.AudioResponseFormatVerboseJSON
		req.TimestampGranularities = []openai.TranscriptionTimestampGranularity{
			openai.TranscriptionTimestampGranularityWord,
		}
	}
	return req
}

func openAISpeechEvent(resp openai.AudioResponse) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{
				Text:  resp.Text,
				Words: openAITimedStrings(resp.Words),
			},
		},
	}
}

func openAITimedStrings(words []struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}

	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:      word.Word,
			StartTime: word.Start,
			EndTime:   word.End,
		})
	}
	return timed
}

type openAIRealtimeSTTStream struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool
	state  *openAIRealtimeSTTMessageState
}

func (s *openAIRealtimeSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	chunkBytes := openAIRealtimeSTTChunkBytes()
	for start := 0; start < len(frame.Data); start += chunkBytes {
		end := start + chunkBytes
		if end > len(frame.Data) {
			end = len(frame.Data)
		}
		chunk := *frame
		chunk.Data = frame.Data[start:end]
		chunk.SamplesPerChannel = uint32(len(chunk.Data) / 2)
		message, err := buildOpenAIRealtimeSTTAudioAppendMessage(&chunk)
		if err != nil {
			return err
		}
		if err := s.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			return err
		}
	}
	return nil
}

func (s *openAIRealtimeSTTStream) Flush() error {
	message, err := buildOpenAIRealtimeSTTCommitMessage()
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *openAIRealtimeSTTStream) Close() error {
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

func (s *openAIRealtimeSTTStream) Next() (*stt.SpeechEvent, error) {
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

func (s *openAIRealtimeSTTStream) readLoop() {
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
		events, err := openAIRealtimeSTTEventsFromMessage(payload, s.state)
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

func openAIRealtimeSTTChunkBytes() int {
	return openAIRealtimeSTTSampleRate / 20 * openAIRealtimeSTTNumChannels * 2
}

type openAIRealtimeSTTTiming struct {
	startMS int
	endMS   int
}

type openAIRealtimeSTTMessageState struct {
	language      string
	currentText   string
	currentItemID string
	timing        map[string]openAIRealtimeSTTTiming
}

func openAIRealtimeSTTEventsFromMessage(payload []byte, state *openAIRealtimeSTTMessageState) ([]*stt.SpeechEvent, error) {
	if state.timing == nil {
		state.timing = map[string]openAIRealtimeSTTTiming{}
	}
	var message map[string]interface{}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, err
	}
	switch openAIString(message["type"]) {
	case "input_audio_buffer.speech_started":
		itemID := openAIString(message["item_id"])
		if itemID != "" {
			state.currentItemID = itemID
			state.timing[itemID] = openAIRealtimeSTTTiming{startMS: openAIInt(message["audio_start_ms"])}
		}
		return nil, nil
	case "input_audio_buffer.speech_stopped":
		itemID := openAIString(message["item_id"])
		if itemID == "" {
			itemID = state.currentItemID
		}
		if itemID != "" {
			timing := state.timing[itemID]
			timing.endMS = openAIInt(message["audio_end_ms"])
			state.timing[itemID] = timing
		}
		return nil, nil
	case "conversation.item.input_audio_transcription.delta":
		itemID := openAIString(message["item_id"])
		if itemID == "" {
			itemID = state.currentItemID
		}
		if itemID != "" {
			state.currentItemID = itemID
		}
		delta := openAIString(message["delta"])
		if delta == "" {
			return nil, nil
		}
		state.currentText += delta
		return []*stt.SpeechEvent{{
			Type:      stt.SpeechEventInterimTranscript,
			RequestID: state.currentItemID,
			Alternatives: []stt.SpeechData{{
				Text:     state.currentText,
				Language: state.language,
			}},
		}}, nil
	case "conversation.item.input_audio_transcription.completed":
		itemID := openAIString(message["item_id"])
		transcript := openAIString(message["transcript"])
		state.currentText = ""
		events := []*stt.SpeechEvent{}
		if transcript != "" {
			events = append(events, &stt.SpeechEvent{
				Type:      stt.SpeechEventFinalTranscript,
				RequestID: itemID,
				Alternatives: []stt.SpeechData{{
					Text:     transcript,
					Language: state.language,
				}},
			})
		}
		usage, _ := message["usage"].(map[string]interface{})
		audioDuration := openAIRealtimeSTTAudioDuration(state, itemID)
		events = append(events, &stt.SpeechEvent{
			Type: stt.SpeechEventRecognitionUsage,
			RecognitionUsage: &stt.RecognitionUsage{
				AudioDuration: audioDuration,
				InputTokens:   openAIInt(usage["input_tokens"]),
				OutputTokens:  openAIInt(usage["output_tokens"]),
			},
		})
		delete(state.timing, itemID)
		return events, nil
	case "error":
		errorBody, _ := message["error"].(map[string]interface{})
		return nil, fmt.Errorf("openai realtime stt error: %s", openAIString(errorBody["message"]))
	default:
		return nil, nil
	}
}

func openAIRealtimeSTTAudioDuration(state *openAIRealtimeSTTMessageState, itemID string) float64 {
	timing, ok := state.timing[itemID]
	if !ok || timing.endMS <= timing.startMS {
		return 0
	}
	return float64(timing.endMS-timing.startMS) / 1000.0
}

func openAIString(value interface{}) string {
	if v, ok := value.(string); ok {
		return v
	}
	return ""
}

func openAIInt(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}
