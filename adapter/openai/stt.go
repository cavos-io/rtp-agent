package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
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
	client           *openai.Client
	httpClient       openai.HTTPDoer
	apiKey           string
	baseURL          string
	model            string
	language         string
	languageSet      bool
	languageValue    string
	detectLanguage   bool
	detectOptionSet  bool
	prompt           string
	turnDetection    map[string]interface{}
	turnDetectionSet bool
	noiseReduction   string
	useRealtime      bool
	connect          llm.APIConnectOptions
	maxSession       time.Duration
	dialWebsocket    openAIRealtimeSTTWebsocketDialer
	vad              vad.VAD
	vadSet           bool
	streamsMu        sync.Mutex
	streams          map[*openAIRealtimeSTTStream]struct{}
	nextRequestID    uint64
	requestCancels   map[uint64]context.CancelFunc
	closed           bool
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
		s.turnDetectionSet = true
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

func WithOpenAISTTVAD(v vad.VAD) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.vadSet = true
		s.vad = v
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

func WithOpenAISTTHTTPClient(client openai.HTTPDoer) OpenAISTTOption {
	return withOpenAISTTHTTPClient(client)
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
	provider.applyDetectLanguage()
	if provider.useRealtime && openAIRealtimeIsWhisperModel(provider.model) && !provider.vadSet {
		provider.vad = silero.NewSileroVAD()
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
	provider.applyDetectLanguage()
	if provider.useRealtime && openAIRealtimeIsWhisperModel(provider.model) && !provider.vadSet {
		provider.vad = silero.NewSileroVAD()
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
func (s *OpenAISTT) applyDetectLanguage() {
	if s != nil && s.detectLanguage {
		s.language = ""
	}
}

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

func (s *OpenAISTT) Close() error {
	if s == nil {
		return nil
	}
	s.streamsMu.Lock()
	s.closed = true
	streams := make([]*openAIRealtimeSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streams = make(map[*openAIRealtimeSTTStream]struct{})
	requestCancels := make([]context.CancelFunc, 0, len(s.requestCancels))
	for _, cancel := range s.requestCancels {
		requestCancels = append(requestCancels, cancel)
	}
	s.requestCancels = make(map[uint64]context.CancelFunc)
	s.streamsMu.Unlock()

	for _, cancel := range requestCancels {
		cancel()
	}
	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (s *OpenAISTT) isClosed() bool {
	if s == nil {
		return true
	}
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	return s.closed
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

func (s *OpenAISTT) registerRequest(cancel context.CancelFunc) (uint64, bool) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.closed {
		return 0, false
	}
	if s.requestCancels == nil {
		s.requestCancels = make(map[uint64]context.CancelFunc)
	}
	s.nextRequestID++
	id := s.nextRequestID
	s.requestCancels[id] = cancel
	return id, true
}

func (s *OpenAISTT) unregisterRequest(id uint64) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	delete(s.requestCancels, id)
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
	if s.isClosed() {
		return nil, fmt.Errorf("openai stt is closed: %w", io.ErrClosedPipe)
	}
	if language != "" {
		s.language = language
	}
	conn, _, err := s.dialRealtimeSTTWebsocket(ctx)
	if err != nil {
		return nil, mapOpenAIError(err)
	}
	if s.isClosed() {
		conn.Close()
		return nil, fmt.Errorf("openai stt is closed: %w", io.ErrClosedPipe)
	}
	eventLanguage := s.language
	if eventLanguage != "" {
		eventLanguage = openAISTTRequestLanguage(eventLanguage)
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
	var vadStream vad.VADStream
	if s.vad != nil {
		vadStream, err = s.vad.Stream(ctx)
		if err != nil {
			conn.Close()
			return nil, err
		}
	}
	streamCtx, cancel := context.WithCancel(ctx)
	eventStream := newOpenAIRealtimeQueuedStream[*stt.SpeechEvent]()
	stream := &openAIRealtimeSTTStream{
		conn:        conn,
		ctx:         streamCtx,
		cancel:      cancel,
		events:      eventStream.rawChan(),
		eventStream: eventStream,
		errCh:       make(chan error, 1),
		vadStream:   vadStream,
		state: &openAIRealtimeSTTMessageState{
			language: eventLanguage,
			timing:   map[string]openAIRealtimeSTTTiming{},
		},
		owner: s,
	}
	s.registerRealtimeSTTStream(stream)
	go stream.readLoop()
	if vadStream != nil {
		go stream.vadLoopFor(vadStream)
	}
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
		if !s.turnDetectionSet && turnDetection == nil {
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
	if s.isClosed() {
		return nil, fmt.Errorf("openai stt is closed: %w", io.ErrClosedPipe)
	}
	ctx, cancel := context.WithCancel(ctx)
	requestID, ok := s.registerRequest(cancel)
	if !ok {
		cancel()
		return nil, fmt.Errorf("openai stt is closed: %w", io.ErrClosedPipe)
	}
	cleanupRequest := func() {
		s.unregisterRequest(requestID)
		cancel()
	}
	if language != "" {
		s.language = language
	}
	audio := openAISTTWAVBytes(frames)
	req := openAIAudioRequest(s, bytes.NewReader(audio), language)

	resp, err := s.client.CreateTranscription(ctx, req)
	if err != nil {
		if s.isClosed() && errors.Is(ctx.Err(), context.Canceled) {
			cleanupRequest()
			return nil, fmt.Errorf("openai stt is closed: %w", io.ErrClosedPipe)
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			cleanupRequest()
			return nil, context.Canceled
		}
		cleanupRequest()
		return nil, mapOpenAIError(err)
	}
	cleanupRequest()
	if s.isClosed() {
		return nil, fmt.Errorf("openai stt is closed: %w", io.ErrClosedPipe)
	}
	if resp.Language == "" {
		resp.Language = openAIAudioRequestLanguage(s, language)
	}

	return openAISpeechEvent(resp, req.Language), nil
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
	requestLanguage := openAIAudioRequestLanguage(s, language)
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

func openAIAudioRequestLanguage(s *OpenAISTT, language string) string {
	requestLanguage := s.language
	if language != "" {
		requestLanguage = language
	}
	if requestLanguage != "" {
		requestLanguage = openAISTTRequestLanguage(requestLanguage)
	}
	return requestLanguage
}

func openAISTTRequestLanguage(language string) string {
	return languageutil.Language(language)
}

func openAISpeechEvent(resp openai.AudioResponse, fallbackLanguage string) *stt.SpeechEvent {
	language := resp.Language
	if language == "" {
		language = fallbackLanguage
	}
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{
				Text:       resp.Text,
				Language:   language,
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
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	events      chan *stt.SpeechEvent
	eventStream *openAIRealtimeQueuedStream[*stt.SpeechEvent]
	errCh       chan error
	pendingErr  error
	mu          sync.Mutex
	closed      bool
	inputEnded  bool
	committed   bool
	hasAudio    bool
	pushedSR    uint32
	audio       *audio.AudioByteStream
	normalizer  openAIRealtimeInputAudioNormalizer
	state       *openAIRealtimeSTTMessageState
	owner       *OpenAISTT
	vadStream   vad.VADStream
}

func (s *openAIRealtimeSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	if s.inputEnded {
		s.mu.Unlock()
		return openAIRealtimeSTTInputEndedError()
	}
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	if frame.SampleRate != 0 {
		if s.pushedSR != 0 && s.pushedSR != frame.SampleRate {
			s.mu.Unlock()
			return fmt.Errorf("the sample rate of the input frames must be consistent")
		}
		s.pushedSR = frame.SampleRate
	}
	normalizedFrame, err := s.normalizer.normalize(frame)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if s.audio == nil {
		s.audio = newOpenAIRealtimeSTTAudioByteStream()
	}
	vadStream := s.vadStream
	vadFrame := frame
	s.mu.Unlock()
	if vadStream != nil && vadFrame != nil && len(vadFrame.Data) > 0 {
		if err := vadStream.PushFrame(vadFrame); err != nil {
			s.mu.Lock()
			closed := s.closed
			if !closed {
				s.closeAfterTerminalFailureLocked()
			}
			s.mu.Unlock()
			if closed {
				return io.ErrClosedPipe
			}
			return err
		}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		s.mu.Unlock()
		return openAIRealtimeSTTInputEndedError()
	}
	for _, chunk := range s.audio.Push(normalizedFrame.Data) {
		message, err := buildOpenAIRealtimeSTTAudioAppendMessage(chunk)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		if err := s.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			s.closeAfterWriteFailureLocked()
			s.mu.Unlock()
			return err
		}
		s.hasAudio = true
		s.committed = false
	}
	s.mu.Unlock()
	return nil
}

func (s *openAIRealtimeSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return openAIRealtimeSTTInputEndedError()
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	vadStream := s.vadStream
	if vadStream != nil {
		if err := vadStream.Flush(); err != nil {
			s.closeAfterTerminalFailureLocked()
			return err
		}
	}
	return s.flushAudioLocked()
}

func (s *openAIRealtimeSTTStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return openAIRealtimeSTTInputEndedError()
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if err := s.flushAudioLocked(); err != nil {
		return err
	}
	s.inputEnded = true
	var vadErr error
	if s.vadStream != nil {
		vadErr = s.vadStream.EndInput()
		if vadErr != nil {
			s.closeAfterTerminalFailureLocked()
			return vadErr
		}
	}
	if s.vadStream == nil && s.shouldCommitOnEndInputLocked() {
		if err := s.commitAudioLocked(); err != nil {
			return err
		}
	}
	return vadErr
}

func (s *openAIRealtimeSTTStream) shouldCommitOnEndInputLocked() bool {
	if s.owner == nil {
		return true
	}
	return !s.owner.usesRealtimeSTTServerTurnDetection()
}

func (s *OpenAISTT) usesRealtimeSTTServerTurnDetection() bool {
	if s == nil || openAIRealtimeIsWhisperModel(s.model) {
		return false
	}
	if s.turnDetectionSet && s.turnDetection == nil {
		return false
	}
	return true
}

func (s *openAIRealtimeSTTStream) flushAudioLocked() error {
	if tail := s.normalizer.flush(); tail != nil {
		if s.audio == nil {
			s.audio = newOpenAIRealtimeSTTAudioByteStream()
		}
		for _, chunk := range s.audio.Push(tail.Data) {
			message, err := buildOpenAIRealtimeSTTAudioAppendMessage(chunk)
			if err != nil {
				return err
			}
			if err := s.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				s.closeAfterWriteFailureLocked()
				return err
			}
			s.hasAudio = true
			s.committed = false
		}
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
			s.hasAudio = true
			s.committed = false
		}
	}
	return nil
}

func (s *openAIRealtimeSTTStream) commitAudioLocked() error {
	if s.committed || !s.hasAudio {
		return nil
	}
	message, err := buildOpenAIRealtimeSTTCommitMessage()
	if err != nil {
		return err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	s.committed = true
	return nil
}

func (s *openAIRealtimeSTTStream) UpdateOptions(language string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.state == nil {
		s.state = &openAIRealtimeSTTMessageState{}
	}
	s.state.language = language
	if s.closed || s.conn == nil || s.owner == nil {
		s.mu.Unlock()
		return
	}
	conn := s.conn
	s.mu.Unlock()
	_ = conn.Close()
}

func (s *openAIRealtimeSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.inputEnded = true
	s.closed = true
	if s.owner != nil {
		s.owner.unregisterRealtimeSTTStream(s)
	}
	s.cancel()
	s.closeVADStreamLocked()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *openAIRealtimeSTTStream) closeAfterWriteFailureLocked() {
	s.closeAfterTerminalFailureLocked()
}

func (s *openAIRealtimeSTTStream) closeAfterTerminalFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	if s.owner != nil {
		s.owner.unregisterRealtimeSTTStream(s)
	}
	s.cancel()
	s.closeVADStreamLocked()
	_ = s.conn.Close()
}

func openAIRealtimeSTTInputEndedError() error {
	return fmt.Errorf("stream input ended")
}

func (s *openAIRealtimeSTTStream) closeVADStreamLocked() {
	if s.vadStream == nil {
		return
	}
	vadStream := s.vadStream
	s.vadStream = nil
	go func() {
		_ = vadStream.EndInput()
		_ = vadStream.Close()
	}()
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

func (s *openAIRealtimeSTTStream) sendEvent(event *stt.SpeechEvent) bool {
	if s == nil || event == nil {
		return false
	}
	if s.eventStream != nil {
		return s.eventStream.Send(event)
	}
	if s.events == nil {
		return false
	}
	s.events <- event
	return true
}

func (s *openAIRealtimeSTTStream) closeEventStream() {
	if s == nil {
		return
	}
	if s.eventStream != nil {
		s.eventStream.Close()
		return
	}
	if s.events != nil {
		close(s.events)
	}
}

func (s *openAIRealtimeSTTStream) nextQueuedEvent() (*stt.SpeechEvent, bool) {
	if s == nil || s.events == nil {
		return nil, false
	}
	select {
	case event, ok := <-s.events:
		if ok {
			return event, true
		}
		return nil, false
	default:
	}
	if s.eventStream != nil && s.eventStream.pending() > 0 {
		event, ok := <-s.events
		return event, ok
	}
	return nil, false
}

func (s *openAIRealtimeSTTStream) Next() (*stt.SpeechEvent, error) {
	if s.pendingErr != nil {
		if event, ok := s.nextQueuedEvent(); ok {
			return event, nil
		}
		err := s.pendingErr
		s.pendingErr = nil
		return nil, err
	}
	if event, ok := s.nextQueuedEvent(); ok {
		return event, nil
	}
	select {
	case err := <-s.errCh:
		if event, ok := s.nextQueuedEvent(); ok {
			s.pendingErr = err
			return event, nil
		}
		return nil, err
	default:
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		return nil, io.EOF
	}

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
		if event, ok := s.nextQueuedEvent(); ok {
			s.pendingErr = err
			return event, nil
		}
		return nil, err
	case <-s.ctx.Done():
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return nil, io.EOF
		}
		return nil, s.ctx.Err()
	}
}

func (s *openAIRealtimeSTTStream) readLoop() {
	defer func() {
		if s.owner != nil {
			s.owner.unregisterRealtimeSTTStream(s)
		}
		s.mu.Lock()
		s.closeVADStreamLocked()
		s.mu.Unlock()
		s.closeEventStream()
	}()
	connectedAt := time.Now()
	providerErrorRetries := 0
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if s.isClosed() || s.ctx.Err() != nil {
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
			var apiErr *llm.APIError
			if errors.As(err, &apiErr) && s.owner != nil && providerErrorRetries < s.owner.connect.MaxRetry {
				providerErrorRetries++
				if reconnectErr := s.reconnectAfterUnexpectedClose(); reconnectErr != nil {
					if s.isClosed() || s.ctx.Err() != nil {
						return
					}
					s.errCh <- reconnectErr
					return
				}
				connectedAt = time.Now()
				continue
			}
			if errors.As(err, &apiErr) && s.owner != nil && s.owner.connect.MaxRetry > 0 {
				s.errCh <- llm.NewAPIConnectionError(fmt.Sprintf("failed to recognize speech after %d attempts", providerErrorRetries))
				return
			}
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.sendEvent(event)
		}
		if openAIRealtimeSTTHasFinalTranscript(events) {
			providerErrorRetries = 0
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

func openAIRealtimeSTTHasFinalTranscript(events []*stt.SpeechEvent) bool {
	for _, event := range events {
		if event != nil && event.Type == stt.SpeechEventFinalTranscript {
			return true
		}
	}
	return false
}

func (s *openAIRealtimeSTTStream) vadLoopFor(vadStream vad.VADStream) {
	if vadStream == nil {
		return
	}
	for {
		ev, err := vadStream.Next()
		if err != nil {
			if err != io.EOF {
				s.mu.Lock()
				s.sendErrorLocked(err)
				s.closeAfterTerminalFailureLocked()
				s.mu.Unlock()
			}
			return
		}
		if ev == nil || ev.Type != vad.VADEventEndOfSpeech {
			continue
		}
		s.mu.Lock()
		if !s.closed {
			if msgErr := s.commitAudioLocked(); msgErr != nil {
				s.sendErrorLocked(msgErr)
			}
		}
		s.mu.Unlock()
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
		s.closeVADStreamLocked()
		return mapOpenAIError(err)
	}
	sessionUpdate, err := buildOpenAIRealtimeSTTSessionUpdate(s.owner)
	if err != nil {
		_ = conn.Close()
		s.closeVADStreamLocked()
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, sessionUpdate); err != nil {
		_ = conn.Close()
		s.closeVADStreamLocked()
		return mapOpenAIError(err)
	}
	s.conn = conn
	s.audio = nil
	s.normalizer.reset()
	s.resetMessageStateAfterReconnectLocked()
	s.hasAudio = false
	s.committed = false
	if s.owner.vad != nil {
		s.closeVADStreamLocked()
		vadStream, err := s.owner.vad.Stream(s.ctx)
		if err != nil {
			_ = conn.Close()
			return err
		}
		s.vadStream = vadStream
		if vadStream != nil {
			go s.vadLoopFor(vadStream)
		}
	}
	return nil
}

func (s *openAIRealtimeSTTStream) resetMessageStateAfterReconnectLocked() {
	if s.state == nil {
		s.state = &openAIRealtimeSTTMessageState{}
		return
	}
	s.state.currentItemID = ""
	s.state.currentText = ""
	s.state.lastInterimAt = time.Time{}
	s.state.timing = map[string]openAIRealtimeSTTTiming{}
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
	currentItemID string
	currentText   string
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
			RequestID: itemID,
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
