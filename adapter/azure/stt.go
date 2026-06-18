package azure

import (
	"bytes"
	"context"
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

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	azureSpeechHostEnv        = "AZURE_SPEECH_HOST"
	azureSpeechKeyEnv         = "AZURE_SPEECH_KEY"
	azureSpeechRegionEnv      = "AZURE_SPEECH_REGION"
	defaultAzureSTTLanguage   = "en-US"
	defaultAzureSTTSampleRate = 16000
	defaultAzureSTTRetries    = 3
)

type azureSTTWebsocketDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

type AzureSTT struct {
	apiKey                 string
	region                 string
	speechHost             string
	speechEndpoint         string
	authToken              string
	language               string
	sampleRate             int
	segmentationSilence    int
	segmentationMaxTime    int
	segmentationStrategy   string
	trueTextPostProcessing bool
	explicitPunctuation    bool
	profanity              string
	httpClient             *http.Client
	websocketURL           string
	dialWebsocket          azureSTTWebsocketDialer
	mu                     sync.Mutex
	streams                map[*azureSTTStream]struct{}
}

type AzureSTTOption func(*AzureSTT)

func WithAzureSTTWebsocketURL(websocketURL string) AzureSTTOption {
	return func(s *AzureSTT) {
		if websocketURL != "" {
			s.websocketURL = websocketURL
		}
	}
}

func WithAzureSTTSpeechHost(speechHost string) AzureSTTOption {
	return func(s *AzureSTT) {
		if speechHost != "" {
			s.speechHost = speechHost
		}
	}
}

func WithAzureSTTSpeechEndpoint(speechEndpoint string) AzureSTTOption {
	return func(s *AzureSTT) {
		if speechEndpoint != "" {
			s.speechEndpoint = speechEndpoint
		}
	}
}

func WithAzureSTTAuthToken(authToken string) AzureSTTOption {
	return func(s *AzureSTT) {
		if authToken != "" {
			s.authToken = authToken
		}
	}
}

func WithAzureSTTLanguage(language string) AzureSTTOption {
	return func(s *AzureSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithAzureSTTSampleRate(sampleRate int) AzureSTTOption {
	return func(s *AzureSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithAzureSTTSegmentationSilenceTimeout(timeoutMS int) AzureSTTOption {
	return func(s *AzureSTT) {
		if timeoutMS > 0 {
			s.segmentationSilence = timeoutMS
		}
	}
}

func WithAzureSTTSegmentationMaxTime(maxTimeMS int) AzureSTTOption {
	return func(s *AzureSTT) {
		if maxTimeMS > 0 {
			s.segmentationMaxTime = maxTimeMS
		}
	}
}

func WithAzureSTTSegmentationStrategy(strategy string) AzureSTTOption {
	return func(s *AzureSTT) {
		if strategy != "" {
			s.segmentationStrategy = strategy
		}
	}
}

func WithAzureSTTTrueTextPostProcessing(enabled bool) AzureSTTOption {
	return func(s *AzureSTT) {
		s.trueTextPostProcessing = enabled
	}
}

func WithAzureSTTExplicitPunctuation(explicit bool) AzureSTTOption {
	return func(s *AzureSTT) {
		s.explicitPunctuation = explicit
	}
}

func WithAzureSTTProfanity(profanity string) AzureSTTOption {
	return func(s *AzureSTT) {
		if profanity != "" {
			s.profanity = profanity
		}
	}
}

func NewAzureSTT(apiKey string, region string, opts ...AzureSTTOption) (*AzureSTT, error) {
	if apiKey == "" {
		apiKey = os.Getenv(azureSpeechKeyEnv)
	}
	if region == "" {
		region = os.Getenv(azureSpeechRegionEnv)
	}
	provider := &AzureSTT{
		apiKey:        apiKey,
		region:        region,
		speechHost:    os.Getenv(azureSpeechHostEnv),
		sampleRate:    defaultAzureSTTSampleRate,
		httpClient:    http.DefaultClient,
		dialWebsocket: defaultAzureSTTWebsocketDialer,
		streams:       make(map[*azureSTTStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.speechEndpoint != "" && provider.region != "" {
		provider.region = ""
	}
	if provider.speechHost == "" && provider.speechEndpoint == "" && !((provider.apiKey != "" && provider.region != "") || (provider.authToken != "" && provider.region != "")) {
		return nil, fmt.Errorf("azure speech config requires AZURE_SPEECH_HOST or AZURE_SPEECH_KEY and AZURE_SPEECH_REGION or AZURE_SPEECH_AUTH_TOKEN and AZURE_SPEECH_REGION or AZURE_SPEECH_KEY and AZURE_SPEECH_ENDPOINT")
	}
	return provider, nil
}

func (s *AzureSTT) Label() string { return "azure.STT" }
func (s *AzureSTT) Model() string { return "unknown" }
func (s *AzureSTT) Provider() string {
	return "Azure STT"
}
func (s *AzureSTT) UpdateOptions(language string, opts ...AzureSTTOption) {
	if language == "" && len(opts) == 0 {
		return
	}
	var streams []*azureSTTStream
	s.mu.Lock()
	if language != "" {
		s.language = language
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()
	for _, stream := range streams {
		stream.updateOptions(language, true)
	}
}
func (s *AzureSTT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return defaultAzureSTTSampleRate
	}
	return uint32(s.sampleRate)
}
func (s *AzureSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, AlignedTranscript: "chunk", OfflineRecognize: false}
}

func (s *AzureSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	resolvedLanguage := s.streamLanguage(language)
	streamURL := buildAzureSTTStreamURL(s, resolvedLanguage)
	conn, connectionID, err := openAzureSTTStreamConnection(ctx, s, streamURL)
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &azureSTTStream{
		provider:      s,
		conn:          conn,
		connectionID:  connectionID,
		streamURL:     streamURL,
		language:      resolvedLanguage,
		events:        make(chan *stt.SpeechEvent, 100),
		errCh:         make(chan error, 1),
		ctx:           streamCtx,
		cancel:        cancel,
		maxReconnects: defaultAzureSTTRetries,
	}
	s.registerStream(stream)
	go stream.readLoop(conn)
	return stream, nil
}

func (s *AzureSTT) registerStream(stream *azureSTTStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streams[stream] = struct{}{}
}

func (s *AzureSTT) unregisterStream(stream *azureSTTStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func openAzureSTTStreamConnection(ctx context.Context, provider *AzureSTT, streamURL string) (*websocket.Conn, string, error) {
	connectionID := strings.ReplaceAll(uuid.NewString(), "-", "")
	conn, _, err := provider.dialWebsocket(ctx, streamURL, buildAzureSTTHeaders(provider, connectionID))
	if err != nil {
		return nil, "", fmt.Errorf("failed to dial azure stt websocket: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, buildAzureSTTMessage("speech.config", connectionID, "application/json", buildAzureSTTSpeechConfig(provider))); err != nil {
		_ = conn.Close()
		return nil, "", fmt.Errorf("failed to initialize azure stt websocket: %w", err)
	}
	return conn, connectionID, nil
}

func (s *AzureSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, languageStr string) (*stt.SpeechEvent, error) {
	req, err := buildAzureSTTRecognizeRequest(ctx, s, frames, languageStr)
	if err != nil {
		return nil, err
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("azure stt error: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result azureSTTRecognizeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return azureSTTRecognizeSpeechEvent(s.streamLanguage(languageStr), result)
}

type azureSTTRecognizeResponse struct {
	RecognitionStatus string `json:"RecognitionStatus"`
	DisplayText       string `json:"DisplayText"`
	NBest             []struct {
		Display    string   `json:"Display"`
		Confidence *float64 `json:"Confidence"`
	} `json:"NBest"`
}

func buildAzureSTTRecognizeRequest(ctx context.Context, s *AzureSTT, frames []*model.AudioFrame, language string) (*http.Request, error) {
	if s == nil {
		return nil, fmt.Errorf("azure stt provider is nil")
	}
	u := url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s.stt.speech.microsoft.com", s.region),
		Path:   "/speech/recognition/conversation/cognitiveservices/v1",
	}
	if s.speechHost != "" {
		hostURL, err := url.Parse(s.speechHost)
		if err == nil {
			u.Scheme = hostURL.Scheme
			u.Host = hostURL.Host
			if hostURL.Path != "" && hostURL.Path != "/" {
				u.Path = hostURL.Path
			}
		}
	}
	query := u.Query()
	query.Set("language", s.streamLanguage(language))
	query.Set("format", "detailed")
	if s.profanity != "" {
		query.Set("profanity", s.profanity)
	}
	u.RawQuery = query.Encode()

	wav, sampleRate := azureSTTWAVBytes(frames)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(wav))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", fmt.Sprintf("audio/wav; codecs=audio/pcm; samplerate=%d", sampleRate))
	req.Header.Set("Ocp-Apim-Subscription-Key", s.apiKey)
	return req, nil
}

func azureSTTRecognizeSpeechEvent(language string, result azureSTTRecognizeResponse) (*stt.SpeechEvent, error) {
	if result.RecognitionStatus != "" && !strings.EqualFold(result.RecognitionStatus, "Success") {
		return nil, fmt.Errorf("azure stt recognition failed: %s", result.RecognitionStatus)
	}
	text := result.DisplayText
	confidence := stt.DefaultTranscriptConfidence(text)
	if len(result.NBest) > 0 {
		if result.NBest[0].Display != "" {
			text = result.NBest[0].Display
		}
		if result.NBest[0].Confidence != nil {
			confidence = *result.NBest[0].Confidence
		}
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("azure stt recognition returned empty transcript")
	}
	return azureSTTSpeechEvent(stt.SpeechEventFinalTranscript, language, text, confidence), nil
}

func azureSTTWAVBytes(frames []*model.AudioFrame) ([]byte, uint32) {
	var pcm bytes.Buffer
	sampleRate := uint32(16000)
	numChannels := uint32(1)
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate > 0 && pcm.Len() == 0 {
			sampleRate = frame.SampleRate
		}
		if frame.NumChannels > 0 && pcm.Len() == 0 {
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
	return wav.Bytes(), sampleRate
}

func defaultAzureSTTWebsocketDialer(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
}

func buildAzureSTTStreamURL(s *AzureSTT, language string) string {
	base := s.websocketURL
	if base == "" {
		if s.speechEndpoint != "" {
			base = s.speechEndpoint
		} else if s.speechHost != "" {
			base = s.speechHost
		} else {
			base = fmt.Sprintf("wss://%s.stt.speech.microsoft.com/speech/recognition/conversation/cognitiveservices/v1", s.region)
		}
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "":
		u.Scheme = "wss"
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/speech/recognition/conversation/cognitiveservices/v1"
	}
	query := u.Query()
	query.Set("language", resolveAzureSTTLanguage(language))
	query.Set("format", "detailed")
	if s != nil && s.explicitPunctuation {
		query.Set("punctuation", "explicit")
	}
	if s != nil && s.profanity != "" {
		query.Set("profanity", s.profanity)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func resolveAzureSTTLanguage(language string) string {
	if language != "" {
		return language
	}
	return defaultAzureSTTLanguage
}

func (s *AzureSTT) streamLanguage(language string) string {
	if language != "" {
		return language
	}
	if s != nil && s.language != "" {
		return s.language
	}
	return defaultAzureSTTLanguage
}

func buildAzureSTTHeaders(s *AzureSTT, connectionID string) http.Header {
	headers := make(http.Header)
	if s.authToken != "" {
		headers.Set("Authorization", "Bearer "+s.authToken)
	} else if s.apiKey != "" {
		headers.Set("Ocp-Apim-Subscription-Key", s.apiKey)
	}
	headers.Set("X-ConnectionId", connectionID)
	return headers
}

func buildAzureSTTSpeechConfig(s *AzureSTT) []byte {
	payload := map[string]any{
		"context": map[string]any{
			"system": map[string]any{
				"version": "1.0.00000",
			},
		},
	}
	properties := azureSTTSpeechConfigProperties(s)
	if len(properties) > 0 {
		payload["properties"] = properties
	}
	b, _ := json.Marshal(payload)
	return b
}

func azureSTTSpeechConfigProperties(s *AzureSTT) map[string]string {
	properties := make(map[string]string)
	if s == nil {
		return properties
	}
	if s.segmentationSilence > 0 {
		properties["Speech_SegmentationSilenceTimeoutMs"] = fmt.Sprint(s.segmentationSilence)
	}
	if s.segmentationMaxTime > 0 {
		properties["Speech_SegmentationMaximumTimeMs"] = fmt.Sprint(s.segmentationMaxTime)
	}
	if s.segmentationStrategy != "" {
		properties["Speech_SegmentationStrategy"] = s.segmentationStrategy
	}
	if s.trueTextPostProcessing {
		properties["SpeechServiceResponse_PostProcessingOption"] = "TrueText"
	}
	return properties
}

func buildAzureSTTMessage(path string, requestID string, contentType string, body []byte) []byte {
	headers := buildAzureSTTMessageHeaders(path, requestID, contentType)
	var b bytes.Buffer
	b.Write(headers)
	b.Write(body)
	return b.Bytes()
}

func buildAzureSTTBinaryMessage(path string, requestID string, contentType string, body []byte) []byte {
	headers := buildAzureSTTMessageHeaders(path, requestID, contentType)
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint16(len(headers)))
	b.Write(headers)
	b.Write(body)
	return b.Bytes()
}

func buildAzureSTTMessageHeaders(path string, requestID string, contentType string) []byte {
	var b bytes.Buffer
	b.WriteString("Path: ")
	b.WriteString(path)
	b.WriteString("\r\n")
	b.WriteString("X-RequestId: ")
	b.WriteString(requestID)
	b.WriteString("\r\n")
	b.WriteString("X-Timestamp: ")
	b.WriteString(time.Now().UTC().Format(time.RFC3339Nano))
	b.WriteString("\r\n")
	if contentType != "" {
		b.WriteString("Content-Type: ")
		b.WriteString(contentType)
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	return b.Bytes()
}

type azureSTTStream struct {
	provider        *AzureSTT
	conn            *websocket.Conn
	connectionID    string
	streamURL       string
	language        string
	events          chan *stt.SpeechEvent
	errCh           chan error
	mu              sync.Mutex
	closed          bool
	audioWritten    bool
	reconnectNext   bool
	sessionStopped  bool
	reconnects      int
	maxReconnects   int
	startTimeOffset float64
	startTime       float64
	speaking        bool
	ctx             context.Context
	cancel          context.CancelFunc
}

func (s *azureSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.reconnectNext {
		if err := s.reconnectLocked(); err != nil {
			s.finishWithErrorLocked(err)
			return err
		}
		s.reconnectNext = false
	}
	for {
		if err := s.conn.WriteMessage(websocket.BinaryMessage, buildAzureSTTBinaryMessage("audio", s.connectionID, azureSTTStreamAudioContentType(s.provider, frame), frame.Data)); err != nil {
			if reconnectErr := s.reconnectLocked(); reconnectErr == nil {
				continue
			}
			s.finishWithErrorLocked(err)
			return err
		}
		break
	}
	s.audioWritten = true
	return nil
}

func azureSTTStreamAudioContentType(provider *AzureSTT, frame *model.AudioFrame) string {
	sampleRate := uint32(defaultAzureSTTSampleRate)
	if provider != nil && provider.InputSampleRate() > 0 {
		sampleRate = provider.InputSampleRate()
	}
	return fmt.Sprintf("audio/x-wav;codec=audio/pcm;samplerate=%d", sampleRate)
}

func (s *azureSTTStream) Flush() error {
	return nil
}

func (s *azureSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	err := s.conn.Close()
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	return err
}

func (s *azureSTTStream) Next() (*stt.SpeechEvent, error) {
	select {
	case err := <-s.errCh:
		s.mu.Lock()
		if s.sessionStopped && !s.closed {
			s.finishWithErrorLocked(err)
			select {
			case <-s.errCh:
			default:
			}
		}
		s.mu.Unlock()
		return nil, err
	default:
	}
	s.mu.Lock()
	if s.sessionStopped && !s.closed {
		err := llm.NewAPIConnectionError("SpeechRecognition session stopped")
		s.finishWithErrorLocked(err)
		s.mu.Unlock()
		select {
		case <-s.errCh:
		default:
		}
		return nil, err
	}
	s.mu.Unlock()
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

func (s *azureSTTStream) updateOptions(language string, reconnect bool) {
	if language == "" && !reconnect {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if language != "" {
		s.language = language
	}
	if reconnect {
		s.streamURL = buildAzureSTTStreamURL(s.provider, s.language)
		s.reconnectNext = true
	}
}

func (s *azureSTTStream) reconnectLocked() error {
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.reconnects >= s.maxReconnects {
		return fmt.Errorf("azure stt websocket reconnect attempts exhausted")
	}
	s.reconnects++
	oldConn := s.conn
	conn, connectionID, err := openAzureSTTStreamConnection(s.ctx, s.provider, s.streamURL)
	if err != nil {
		return err
	}
	s.conn = conn
	s.connectionID = connectionID
	s.sessionStopped = false
	_ = oldConn.Close()
	go s.readLoop(conn)
	for {
		select {
		case <-s.errCh:
		default:
			return nil
		}
	}
}

func (s *azureSTTStream) finishWithErrorLocked(err error) {
	if s.closed {
		return
	}
	s.closed = true
	select {
	case s.errCh <- err:
	default:
	}
	s.cancel()
	_ = s.conn.Close()
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
}

func (s *azureSTTStream) readLoop(conn *websocket.Conn) {
	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			if s.closed || conn != s.conn {
				s.mu.Unlock()
				return
			}
			if !s.audioWritten {
				s.sessionStopped = true
				s.reconnectNext = true
				select {
				case s.errCh <- llm.NewAPIConnectionError("SpeechRecognition session stopped"):
				default:
				}
				s.mu.Unlock()
				return
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.finishWithErrorLocked(llm.NewAPIConnectionError("SpeechRecognition session stopped"))
				s.mu.Unlock()
				return
			}
			s.finishWithErrorLocked(err)
			s.mu.Unlock()
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		if event := s.parseMessage(payload); event != nil {
			s.events <- event
		}
	}
}

func parseAzureSTTMessage(language string, payload []byte) *stt.SpeechEvent {
	return parseAzureSTTMessageWithOffset(language, payload, 0)
}

func (s *azureSTTStream) parseMessage(payload []byte) *stt.SpeechEvent {
	event := parseAzureSTTMessageWithOffset(s.language, payload, s.StartTimeOffset())
	if event == nil {
		return nil
	}
	switch event.Type {
	case stt.SpeechEventStartOfSpeech:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.speaking {
			return nil
		}
		s.speaking = true
	case stt.SpeechEventEndOfSpeech:
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.speaking {
			return nil
		}
		s.speaking = false
	}
	return event
}

func (s *azureSTTStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startTimeOffset < 0 {
		return 0
	}
	return s.startTimeOffset
}

func (s *azureSTTStream) SetStartTimeOffset(offset float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if offset < 0 {
		offset = 0
	}
	s.startTimeOffset = offset
}

func (s *azureSTTStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startTime < 0 {
		return 0
	}
	return s.startTime
}

func (s *azureSTTStream) SetStartTime(startTime float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if startTime < 0 {
		startTime = 0
	}
	s.startTime = startTime
}

func parseAzureSTTMessageWithOffset(language string, payload []byte, startTimeOffset float64) *stt.SpeechEvent {
	headers, body := splitAzureSTTMessage(payload)
	switch headers["Path"] {
	case "speech.hypothesis":
		var message struct {
			Text            string `json:"Text"`
			Language        string `json:"Language"`
			PrimaryLanguage struct {
				Language string `json:"Language"`
			} `json:"PrimaryLanguage"`
			Offset   *int64 `json:"Offset"`
			Duration *int64 `json:"Duration"`
		}
		if err := json.Unmarshal(body, &message); err != nil || strings.TrimSpace(message.Text) == "" {
			return nil
		}
		return azureSTTSpeechEventWithTiming(stt.SpeechEventInterimTranscript, azureSTTDetectedLanguage(language, message.Language, message.PrimaryLanguage.Language), message.Text, 0, message.Offset, message.Duration, startTimeOffset)
	case "speech.phrase":
		var message struct {
			RecognitionStatus string `json:"RecognitionStatus"`
			DisplayText       string `json:"DisplayText"`
			Language          string `json:"Language"`
			PrimaryLanguage   struct {
				Language string `json:"Language"`
			} `json:"PrimaryLanguage"`
			Offset   *int64 `json:"Offset"`
			Duration *int64 `json:"Duration"`
			NBest    []struct {
				Display    string   `json:"Display"`
				Confidence *float64 `json:"Confidence"`
			} `json:"NBest"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			return nil
		}
		text := message.DisplayText
		confidence := stt.DefaultTranscriptConfidence(text)
		if len(message.NBest) > 0 {
			if message.NBest[0].Display != "" {
				text = message.NBest[0].Display
			}
			if message.NBest[0].Confidence != nil {
				confidence = *message.NBest[0].Confidence
			}
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return azureSTTSpeechEventWithTiming(stt.SpeechEventFinalTranscript, azureSTTDetectedLanguage(language, message.Language, message.PrimaryLanguage.Language), text, confidence, message.Offset, message.Duration, startTimeOffset)
	case "turn.start", "speech.startDetected":
		return &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}
	case "turn.end", "speech.endDetected":
		return &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}
	default:
		return nil
	}
}

func azureSTTDetectedLanguage(fallback string, detected ...string) string {
	for _, language := range detected {
		if strings.TrimSpace(language) != "" {
			return language
		}
	}
	return fallback
}

func azureSTTSpeechEvent(eventType stt.SpeechEventType, language string, text string, confidence float64) *stt.SpeechEvent {
	return azureSTTSpeechEventWithTiming(eventType, language, text, confidence, nil, nil, 0)
}

func azureSTTSpeechEventWithTiming(eventType stt.SpeechEventType, language string, text string, confidence float64, offset *int64, duration *int64, startTimeOffset float64) *stt.SpeechEvent {
	data := stt.SpeechData{
		Language:   language,
		Text:       text,
		Confidence: confidence,
	}
	if offset != nil {
		startTime := float64(*offset)/10_000_000 + startTimeOffset
		data.StartTime = startTime
		endTicks := *offset
		if duration != nil {
			endTicks += *duration
		}
		data.EndTime = float64(endTicks)/10_000_000 + startTimeOffset
	}
	return &stt.SpeechEvent{
		Type:         eventType,
		Alternatives: []stt.SpeechData{data},
	}
}

func splitAzureSTTMessage(payload []byte) (map[string]string, []byte) {
	headers := map[string]string{}
	parts := bytes.SplitN(payload, []byte("\r\n\r\n"), 2)
	headerBlock := payload
	body := []byte{}
	if len(parts) == 2 {
		headerBlock = parts[0]
		body = parts[1]
	}
	for _, line := range strings.Split(string(headerBlock), "\r\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			headers[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return headers, body
}
