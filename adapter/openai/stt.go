package openai

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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	languageutil "github.com/cavos-io/rtp-agent/library/utils/language"
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
	openAIRealtimeSTTDeltaInterval     = 500 * time.Millisecond
	openAIAPIKeyEnv                    = "OPENAI_API_KEY"
	ovhcloudAPIKeyEnv                  = "OVHCLOUD_API_KEY"
	defaultOVHCloudOpenAIBaseURL       = "https://oai.endpoints.kepler.ai.cloud.ovh.net/v1"
	defaultOVHCloudOpenAISTTModel      = "whisper-large-v3-turbo"
)

type OpenAISTT struct {
	client          *openai.Client
	httpClient      openai.HTTPDoer
	apiKey          string
	baseURL         string
	model           string
	language        string
	languageSet     bool
	languageValue   string
	detectLanguage  bool
	detectOptionSet bool
	prompt          string
	turnDetection   map[string]interface{}
	noiseReduction  string
	useRealtime     bool
	connect         llm.APIConnectOptions
	maxSession      time.Duration
	dialWebsocket   openAIRealtimeSTTWebsocketDialer
	streamsMu       sync.Mutex
	streams         map[*openAIRealtimeSTTStream]struct{}
}

type OpenAISTTOption func(*OpenAISTT)

type openAIRealtimeSTTWebsocketDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

func WithOpenAISTTModel(model string) OpenAISTTOption {
	return func(s *OpenAISTT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithOpenAISTTLanguage(language string) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.languageSet = true
		s.languageValue = language
		s.language = language
	}
}

func WithOpenAISTTDetectLanguage(detect bool) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.detectOptionSet = true
		s.detectLanguage = detect
		if detect {
			s.language = ""
		}
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

func WithOpenAISTTTurnDetection(turnDetection map[string]interface{}) OpenAISTTOption {
	return func(s *OpenAISTT) {
		if turnDetection == nil {
			s.turnDetection = nil
			return
		}
		s.turnDetection = make(map[string]interface{}, len(turnDetection))
		for key, value := range turnDetection {
			s.turnDetection[key] = value
		}
	}
}

func WithOpenAISTTRealtime(useRealtime bool) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.useRealtime = useRealtime
	}
}

func WithOpenAISTTConnectOptions(connectOptions llm.APIConnectOptions) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.connect = connectOptions
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
		return nil, fmt.Errorf("%s", openAIAPIKeyRequiredMessage)
	}
	if model == "" {
		model = "gpt-4o-mini-transcribe"
	}
	provider := &OpenAISTT{
		apiKey:        apiKey,
		baseURL:       defaultOpenAIBaseURL,
		model:         model,
		language:      "en",
		connect:       llm.DefaultAPIConnectOptions(),
		maxSession:    10 * time.Minute,
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

func NewAzureOpenAISTT(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...OpenAISTTOption) (*OpenAISTT, error) {
	if model == "" {
		model = "gpt-4o-mini-transcribe"
	}
	if azureEndpoint == "" {
		azureEndpoint = os.Getenv(azureOpenAIEndpointEnv)
	}
	if apiVersion == "" {
		apiVersion = os.Getenv(openAIAPIVersionEnv)
	}
	if apiKey == "" {
		apiKey = os.Getenv(azureOpenAIAPIKeyEnv)
	}
	if azureADToken == "" {
		azureADToken = os.Getenv(azureOpenAIADTokenEnv)
	}
	if azureEndpoint == "" {
		return nil, fmt.Errorf("%s is required for Azure OpenAI STT", azureOpenAIEndpointEnv)
	}
	if apiKey == "" && azureADToken == "" {
		return nil, fmt.Errorf("%s or %s is required for Azure OpenAI STT", azureOpenAIAPIKeyEnv, azureOpenAIADTokenEnv)
	}
	if azureDeployment == "" {
		azureDeployment = model
	}

	provider := &OpenAISTT{
		apiKey:        apiKey,
		baseURL:       azureEndpoint,
		model:         model,
		language:      "en",
		connect:       llm.DefaultAPIConnectOptions(),
		maxSession:    10 * time.Minute,
		dialWebsocket: defaultOpenAIRealtimeSTTWebsocketDialer,
	}
	for _, opt := range opts {
		opt(provider)
	}

	config := openai.DefaultAzureConfig(apiKey, azureEndpoint)
	config.AzureModelMapperFunc = func(string) string {
		return azureDeployment
	}
	if apiVersion != "" {
		config.APIVersion = apiVersion
	}
	if provider.httpClient != nil {
		config.HTTPClient = provider.httpClient
	}
	if apiKey == "" && azureADToken != "" {
		config.HTTPClient = &azureADTokenHTTPClient{
			base:  config.HTTPClient,
			token: azureADToken,
		}
	}
	provider.client = openai.NewClientWithConfig(config)
	return provider, nil
}

func NewOVHCloudOpenAISTT(model, apiKey string, opts ...OpenAISTTOption) (*OpenAISTT, error) {
	if model == "" {
		model = defaultOVHCloudOpenAISTTModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(ovhcloudAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OVHcloud AI Endpoints API key is required")
	}

	options := append([]OpenAISTTOption{WithOpenAISTTBaseURL(defaultOVHCloudOpenAIBaseURL)}, opts...)
	return NewOpenAISTT(apiKey, model, options...)
}

func (s *OpenAISTT) Label() string { return "openai.STT" }
func (s *OpenAISTT) Provider() string {
	u, err := url.Parse(s.baseURL)
	if err != nil || u.Host == "" {
		return "openai"
	}
	return u.Host
}
func (s *OpenAISTT) Model() string { return s.model }
func (s *OpenAISTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: s.useRealtime, InterimResults: s.useRealtime, Diarization: false, OfflineRecognize: true}
}

func (s *OpenAISTT) UpdateOptions(opts ...OpenAISTTOption) {
	previousDetectOptionSet := s.detectOptionSet
	s.languageSet = false
	s.languageValue = ""
	s.detectOptionSet = false
	for _, opt := range opts {
		opt(s)
	}
	languageSet := s.languageSet
	languageValue := s.languageValue
	detectOptionSet := s.detectOptionSet
	s.languageSet = false
	s.languageValue = ""
	s.detectOptionSet = previousDetectOptionSet || detectOptionSet
	if detectOptionSet {
		s.language = ""
	}
	if languageSet {
		s.updateRealtimeSTTStreamLanguage(languageValue)
	}
}

func (s *OpenAISTT) registerRealtimeSTTStream(stream *openAIRealtimeSTTStream) {
	if s == nil || stream == nil {
		return
	}
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.streams == nil {
		s.streams = map[*openAIRealtimeSTTStream]struct{}{}
	}
	s.streams[stream] = struct{}{}
}

func (s *OpenAISTT) unregisterRealtimeSTTStream(stream *openAIRealtimeSTTStream) {
	if s == nil || stream == nil {
		return
	}
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	delete(s.streams, stream)
}

func (s *OpenAISTT) updateRealtimeSTTStreamLanguage(language string) {
	s.streamsMu.Lock()
	streams := make([]*openAIRealtimeSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streamsMu.Unlock()
	for _, stream := range streams {
		stream.UpdateOptions(language)
	}
}

func (s *OpenAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if !s.useRealtime {
		return nil, fmt.Errorf("openai realtime stt is not enabled")
	}
	conn, _, err := s.dialRealtimeSTTWebsocket(ctx)
	if err != nil {
		return nil, mapOpenAIError(err)
	}
	eventLanguage := s.language
	if language != "" {
		eventLanguage = openAISTTRequestLanguage(language)
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
			language: eventLanguage,
			timing:   map[string]openAIRealtimeSTTTiming{},
		},
		owner: s,
	}
	s.registerRealtimeSTTStream(stream)
	go stream.readLoop()
	return stream, nil
}

func defaultOpenAIRealtimeSTTWebsocketDialer(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
}

func (s *OpenAISTT) dialRealtimeSTTWebsocket(ctx context.Context) (*websocket.Conn, *http.Response, error) {
	var (
		conn *websocket.Conn
		resp *http.Response
		err  error
	)
	for attempt := 0; attempt <= s.connect.MaxRetry; attempt++ {
		conn, resp, err = s.dialRealtimeSTTWebsocketAttempt(ctx)
		if err == nil {
			return conn, resp, nil
		}
		if attempt == s.connect.MaxRetry {
			return nil, nil, err
		}
		retryInterval := s.connect.IntervalForRetry(attempt)
		if retryInterval <= 0 {
			continue
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, nil, err
}

func (s *OpenAISTT) dialRealtimeSTTWebsocketAttempt(ctx context.Context) (*websocket.Conn, *http.Response, error) {
	if s.connect.Timeout <= 0 {
		return s.dialWebsocket(ctx, buildOpenAIRealtimeSTTWebsocketURL(s).String(), buildOpenAIRealtimeSTTHeaders(s))
	}
	dialCtx, cancel := context.WithTimeout(ctx, s.connect.Timeout)
	defer cancel()
	return s.dialWebsocket(dialCtx, buildOpenAIRealtimeSTTWebsocketURL(s).String(), buildOpenAIRealtimeSTTHeaders(s))
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
		transcription["language"] = openAISTTRequestLanguage(s.language)
	}
	input := map[string]interface{}{
		"format": map[string]interface{}{
			"type": "audio/pcm",
			"rate": openAIRealtimeSTTSampleRate,
		},
		"transcription": transcription,
	}
	if !openAIRealtimeIsWhisperModel(s.model) {
		turnDetection := s.turnDetection
		if turnDetection == nil {
			turnDetection = map[string]interface{}{
				"type":                "server_vad",
				"threshold":           openAIRealtimeSTTDefaultThreshold,
				"prefix_padding_ms":   openAIRealtimeSTTPrefixPaddingMS,
				"silence_duration_ms": openAIRealtimeSTTSilenceDurationMS,
			}
		}
		input["turn_detection"] = turnDetection
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
	if language != "" {
		s.language = language
	}
	audio := openAISTTWAVBytes(frames)
	req := openAIAudioRequest(s, bytes.NewReader(audio), language)

	resp, err := s.client.CreateTranscription(ctx, req)
	if err != nil {
		return nil, mapOpenAIError(err)
	}

	return openAISpeechEvent(resp), nil
}

func openAISTTWAVBytes(frames []*model.AudioFrame) []byte {
	var pcm bytes.Buffer
	sampleRate := uint32(16000)
	numChannels := uint32(1)
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate > 0 && sampleRate == 16000 {
			sampleRate = frame.SampleRate
		}
		if frame.NumChannels > 0 && numChannels == 1 {
			numChannels = frame.NumChannels
		}
		pcm.Write(frame.Data)
	}

	data := pcm.Bytes()
	dataSize := uint32(len(data))
	blockAlign := uint16(numChannels * 2)
	byteRate := sampleRate * numChannels * 2

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
	_ = binary.Write(&wav, binary.LittleEndian, blockAlign)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, dataSize)
	wav.Write(data)
	return wav.Bytes()
}

func openAIAudioRequest(s *OpenAISTT, reader io.Reader, language string) openai.AudioRequest {
	requestLanguage := s.language
	if language != "" {
		requestLanguage = language
	}
	if requestLanguage != "" {
		requestLanguage = openAISTTRequestLanguage(requestLanguage)
	}
	req := openai.AudioRequest{
		Model:    s.model,
		FilePath: "file.wav", // Static filename required by API when Reader is used.
		Reader:   reader,
		Language: requestLanguage,
		Prompt:   s.prompt,
		Format:   openai.AudioResponseFormatJSON,
	}
	if s.model == "whisper-1" {
		req.Format = openai.AudioResponseFormatVerboseJSON
	}
	return req
}

func openAISTTRequestLanguage(language string) string {
	return languageutil.Language(language)
}

func openAISpeechEvent(resp openai.AudioResponse) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{
				Text:       resp.Text,
				Language:   resp.Language,
				Confidence: stt.DefaultTranscriptConfidence(resp.Text),
				Words:      openAITimedStrings(resp.Words),
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
	audio  *audio.AudioByteStream
	state  *openAIRealtimeSTTMessageState
	owner  *OpenAISTT
}

func (s *openAIRealtimeSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.audio == nil {
		s.audio = newOpenAIRealtimeSTTAudioByteStream()
	}
	for _, chunk := range s.audio.Push(frame.Data) {
		message, err := buildOpenAIRealtimeSTTAudioAppendMessage(chunk)
		if err != nil {
			return err
		}
		if err := s.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
	}
	return nil
}

func (s *openAIRealtimeSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.audio != nil {
		for _, chunk := range s.audio.Flush() {
			message, err := buildOpenAIRealtimeSTTAudioAppendMessage(chunk)
			if err != nil {
				return err
			}
			if err := s.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				s.closeAfterWriteFailureLocked()
				return err
			}
		}
	}
	message, err := buildOpenAIRealtimeSTTCommitMessage()
	if err != nil {
		return err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *openAIRealtimeSTTStream) UpdateOptions(language string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = &openAIRealtimeSTTMessageState{}
	}
	s.state.language = language
	if s.closed || s.conn == nil || s.owner == nil {
		return
	}
	sessionUpdate, err := buildOpenAIRealtimeSTTSessionUpdate(s.owner)
	if err != nil {
		s.sendErrorLocked(err)
		return
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, sessionUpdate); err != nil {
		s.closeAfterWriteFailureLocked()
	}
}

func (s *openAIRealtimeSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.owner != nil {
		s.owner.unregisterRealtimeSTTStream(s)
	}
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *openAIRealtimeSTTStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	if s.owner != nil {
		s.owner.unregisterRealtimeSTTStream(s)
	}
	s.cancel()
	_ = s.conn.Close()
}

func (s *openAIRealtimeSTTStream) sendErrorLocked(err error) {
	if err == nil || s.errCh == nil {
		return
	}
	select {
	case s.errCh <- err:
	default:
	}
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
	defer func() {
		if s.owner != nil {
			s.owner.unregisterRealtimeSTTStream(s)
		}
		close(s.events)
	}()
	connectedAt := time.Now()
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if s.isClosed() || s.ctx.Err() != nil {
				return
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || err == io.EOF {
				return
			}
			if reconnectErr := s.reconnectAfterUnexpectedClose(); reconnectErr != nil {
				if s.isClosed() || s.ctx.Err() != nil {
					return
				}
				s.errCh <- reconnectErr
				return
			}
			continue
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
		if s.shouldRecycleAfterEvents(events, connectedAt) {
			if reconnectErr := s.reconnectAfterUnexpectedClose(); reconnectErr != nil {
				if s.isClosed() || s.ctx.Err() != nil {
					return
				}
				s.errCh <- reconnectErr
				return
			}
			connectedAt = time.Now()
		}
	}
}

func (s *openAIRealtimeSTTStream) shouldRecycleAfterEvents(events []*stt.SpeechEvent, connectedAt time.Time) bool {
	if s == nil || s.owner == nil || s.owner.maxSession <= 0 {
		return false
	}
	if time.Since(connectedAt) <= s.owner.maxSession {
		return false
	}
	for _, event := range events {
		if event != nil && event.Type == stt.SpeechEventRecognitionUsage {
			return true
		}
	}
	return false
}

func (s *openAIRealtimeSTTStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *openAIRealtimeSTTStream) reconnectAfterUnexpectedClose() error {
	if s.owner == nil {
		return llm.NewAPIStatusError("OpenAI Realtime STT connection closed unexpectedly", -1, "", "")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	_ = s.conn.Close()
	conn, _, err := s.owner.dialRealtimeSTTWebsocket(s.ctx)
	if err != nil {
		return mapOpenAIError(err)
	}
	sessionUpdate, err := buildOpenAIRealtimeSTTSessionUpdate(s.owner)
	if err != nil {
		_ = conn.Close()
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, sessionUpdate); err != nil {
		_ = conn.Close()
		return mapOpenAIError(err)
	}
	s.conn = conn
	return nil
}

func openAIRealtimeSTTChunkBytes() int {
	return openAIRealtimeSTTSampleRate / 20 * openAIRealtimeSTTNumChannels * 2
}

func newOpenAIRealtimeSTTAudioByteStream() *audio.AudioByteStream {
	return audio.NewAudioByteStream(openAIRealtimeSTTSampleRate, openAIRealtimeSTTNumChannels, openAIRealtimeSTTSampleRate/20)
}

type openAIRealtimeSTTTiming struct {
	startMS int
	endMS   int
}

type openAIRealtimeSTTMessageState struct {
	language      string
	currentText   string
	currentItemID string
	lastInterimAt time.Time
	now           func() time.Time
	timing        map[string]openAIRealtimeSTTTiming
}

func openAIRealtimeSTTEventsFromMessage(payload []byte, state *openAIRealtimeSTTMessageState) ([]*stt.SpeechEvent, error) {
	if state.timing == nil {
		state.timing = map[string]openAIRealtimeSTTTiming{}
	}
	var message map[string]interface{}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, nil
	}
	switch openAIString(message["type"]) {
	case "input_audio_buffer.speech_started":
		itemID := openAIString(message["item_id"])
		state.currentItemID = itemID
		state.timing[itemID] = openAIRealtimeSTTTiming{startMS: openAIInt(message["audio_start_ms"])}
		return nil, nil
	case "input_audio_buffer.speech_stopped":
		itemID := openAIString(message["item_id"])
		timing, ok := state.timing[itemID]
		if !ok {
			return nil, nil
		}
		timing.endMS = openAIInt(message["audio_end_ms"])
		state.timing[itemID] = timing
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
		now := openAIRealtimeSTTStateNow(state)
		if !state.lastInterimAt.IsZero() && now.Sub(state.lastInterimAt) <= openAIRealtimeSTTDeltaInterval {
			return nil, nil
		}
		state.lastInterimAt = now
		return []*stt.SpeechEvent{{
			Type:      stt.SpeechEventInterimTranscript,
			RequestID: state.currentItemID,
			Alternatives: []stt.SpeechData{{
				Text:       state.currentText,
				Language:   state.language,
				Confidence: stt.DefaultTranscriptConfidence(state.currentText),
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
					Text:       transcript,
					Language:   state.language,
					Confidence: stt.DefaultTranscriptConfidence(transcript),
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
		return nil, llm.NewAPIError(
			fmt.Sprintf("OpenAI Realtime STT error: %s", openAIRealtimeSTTErrorMessage(errorBody)),
			errorBody,
			false,
		)
	default:
		return nil, nil
	}
}

func openAIRealtimeSTTStateNow(state *openAIRealtimeSTTMessageState) time.Time {
	if state.now != nil {
		return state.now()
	}
	return time.Now()
}

func openAIRealtimeSTTErrorMessage(errorBody map[string]interface{}) string {
	message := openAIString(errorBody["message"])
	if message == "" {
		return "Unknown error"
	}
	return message
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
