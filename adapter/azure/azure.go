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
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	azureSpeechKeyEnv           = "AZURE_SPEECH_KEY"
	azureSpeechRegionEnv        = "AZURE_SPEECH_REGION"
	defaultAzureSTTLanguage     = "en-US"
	defaultAzureTTSVoice        = "en-US-JennyNeural"
	defaultAzureTTSLanguage     = "en-US"
	defaultAzureTTSSampleRate   = 24000
	defaultAzureTTSSampleFormat = "raw-24khz-16bit-mono-pcm"
)

type azureSTTWebsocketDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

type AzureSTT struct {
	apiKey        string
	region        string
	httpClient    *http.Client
	websocketURL  string
	dialWebsocket azureSTTWebsocketDialer
}

type AzureSTTOption func(*AzureSTT)

func WithAzureSTTWebsocketURL(websocketURL string) AzureSTTOption {
	return func(s *AzureSTT) {
		if websocketURL != "" {
			s.websocketURL = websocketURL
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
	if apiKey == "" || region == "" {
		return nil, fmt.Errorf("azure speech config requires AZURE_SPEECH_KEY and AZURE_SPEECH_REGION")
	}
	provider := &AzureSTT{
		apiKey:        apiKey,
		region:        region,
		httpClient:    http.DefaultClient,
		dialWebsocket: defaultAzureSTTWebsocketDialer,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider, nil
}

func (s *AzureSTT) Label() string { return "azure.STT" }
func (s *AzureSTT) Model() string { return "unknown" }
func (s *AzureSTT) Provider() string {
	return "Azure STT"
}
func (s *AzureSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, AlignedTranscript: "chunk", OfflineRecognize: true}
}

func (s *AzureSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	connectionID := strings.ReplaceAll(uuid.NewString(), "-", "")
	conn, _, err := s.dialWebsocket(ctx, buildAzureSTTStreamURL(s, language), buildAzureSTTHeaders(s, connectionID))
	if err != nil {
		return nil, fmt.Errorf("failed to dial azure stt websocket: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, buildAzureSTTMessage("speech.config", connectionID, "application/json", buildAzureSTTSpeechConfig())); err != nil {
		_ = conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &azureSTTStream{
		conn:         conn,
		connectionID: connectionID,
		language:     resolveAzureSTTLanguage(language),
		events:       make(chan *stt.SpeechEvent, 100),
		errCh:        make(chan error, 1),
		ctx:          streamCtx,
		cancel:       cancel,
	}
	go stream.readLoop()
	return stream, nil
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
	return azureSTTRecognizeSpeechEvent(resolveAzureSTTLanguage(languageStr), result)
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
	u := url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s.stt.speech.microsoft.com", s.region),
		Path:   "/speech/recognition/conversation/cognitiveservices/v1",
	}
	query := u.Query()
	query.Set("language", resolveAzureSTTLanguage(language))
	query.Set("format", "detailed")
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
		base = fmt.Sprintf("wss://%s.stt.speech.microsoft.com/speech/recognition/conversation/cognitiveservices/v1", s.region)
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/speech/recognition/conversation/cognitiveservices/v1"
	}
	query := u.Query()
	query.Set("language", resolveAzureSTTLanguage(language))
	query.Set("format", "detailed")
	u.RawQuery = query.Encode()
	return u.String()
}

func resolveAzureSTTLanguage(language string) string {
	if language != "" {
		return language
	}
	return defaultAzureSTTLanguage
}

func buildAzureSTTHeaders(s *AzureSTT, connectionID string) http.Header {
	headers := make(http.Header)
	headers.Set("Ocp-Apim-Subscription-Key", s.apiKey)
	headers.Set("X-ConnectionId", connectionID)
	return headers
}

func buildAzureSTTSpeechConfig() []byte {
	payload := map[string]any{
		"context": map[string]any{
			"system": map[string]any{
				"version": "1.0.00000",
			},
		},
	}
	b, _ := json.Marshal(payload)
	return b
}

func buildAzureSTTMessage(path string, requestID string, contentType string, body []byte) []byte {
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
	b.Write(body)
	return b.Bytes()
}

type azureSTTStream struct {
	conn         *websocket.Conn
	connectionID string
	language     string
	events       chan *stt.SpeechEvent
	errCh        chan error
	mu           sync.Mutex
	closed       bool
	ctx          context.Context
	cancel       context.CancelFunc
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
	if err := s.conn.WriteMessage(websocket.BinaryMessage, buildAzureSTTMessage("audio", s.connectionID, "audio/x-wav", frame.Data)); err != nil {
		s.closed = true
		s.cancel()
		_ = s.conn.Close()
		return err
	}
	return nil
}

func (s *azureSTTStream) Flush() error {
	return nil
}

func (s *azureSTTStream) Close() error {
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

func (s *azureSTTStream) Next() (*stt.SpeechEvent, error) {
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

func (s *azureSTTStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				select {
				case s.errCh <- err:
				default:
				}
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		if event := parseAzureSTTMessage(s.language, payload); event != nil {
			s.events <- event
		}
	}
}

func parseAzureSTTMessage(language string, payload []byte) *stt.SpeechEvent {
	headers, body := splitAzureSTTMessage(payload)
	switch headers["Path"] {
	case "speech.hypothesis":
		var message struct {
			Text string `json:"Text"`
		}
		if err := json.Unmarshal(body, &message); err != nil || strings.TrimSpace(message.Text) == "" {
			return nil
		}
		return azureSTTSpeechEvent(stt.SpeechEventInterimTranscript, language, message.Text, stt.DefaultTranscriptConfidence(message.Text))
	case "speech.phrase":
		var message struct {
			RecognitionStatus string `json:"RecognitionStatus"`
			DisplayText       string `json:"DisplayText"`
			NBest             []struct {
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
		return azureSTTSpeechEvent(stt.SpeechEventFinalTranscript, language, text, confidence)
	case "turn.start":
		return &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}
	case "turn.end":
		return &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}
	default:
		return nil
	}
}

func azureSTTSpeechEvent(eventType stt.SpeechEventType, language string, text string, confidence float64) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type: eventType,
		Alternatives: []stt.SpeechData{{
			Language:   language,
			Text:       text,
			Confidence: confidence,
		}},
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

type AzureTTS struct {
	apiKey     string
	region     string
	voice      string
	language   string
	sampleRate int
	httpClient *http.Client
}

func NewAzureTTS(apiKey string, region string, voice string, languages ...string) (*AzureTTS, error) {
	if apiKey == "" {
		apiKey = os.Getenv(azureSpeechKeyEnv)
	}
	if region == "" {
		region = os.Getenv(azureSpeechRegionEnv)
	}
	if apiKey == "" || region == "" {
		return nil, fmt.Errorf("azure speech config requires AZURE_SPEECH_KEY and AZURE_SPEECH_REGION")
	}
	if voice == "" {
		voice = defaultAzureTTSVoice
	}
	language := defaultAzureTTSLanguage
	if len(languages) > 0 && languages[0] != "" {
		language = languages[0]
	}
	return &AzureTTS{
		apiKey:     apiKey,
		region:     region,
		voice:      voice,
		language:   language,
		sampleRate: defaultAzureTTSSampleRate,
		httpClient: http.DefaultClient,
	}, nil
}

func (t *AzureTTS) Label() string { return "azure.TTS" }
func (t *AzureTTS) Model() string { return "unknown" }
func (t *AzureTTS) Provider() string {
	return "Azure TTS"
}
func (t *AzureTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *AzureTTS) SampleRate() int  { return t.sampleRate }
func (t *AzureTTS) NumChannels() int { return 1 }
func (t *AzureTTS) Language() string { return t.language }

func (t *AzureTTS) UpdateOptions(voice string, language string) {
	if voice != "" {
		t.voice = voice
	}
	if language != "" {
		t.language = language
	}
}

func (t *AzureTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildAzureTTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}

	client := t.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("azure tts error: %s", string(respBody))
	}

	return &azureTTSChunkedStream{
		body:       resp.Body,
		sampleRate: t.sampleRate,
	}, nil
}

func buildAzureTTSRequest(ctx context.Context, t *AzureTTS, text string) (*http.Request, error) {
	url := fmt.Sprintf("https://%s.tts.speech.microsoft.com/cognitiveservices/v1", t.region)
	language := t.language
	if language == "" {
		language = defaultAzureTTSLanguage
	}
	ssml := fmt.Sprintf(`<speak version="1.0" xml:lang="%s"><voice name="%s">%s</voice></speak>`, language, t.voice, text)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(ssml))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", defaultAzureTTSSampleFormat)
	req.Header.Set("Ocp-Apim-Subscription-Key", t.apiKey)
	return req, nil
}

func (t *AzureTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming azure tts is not supported")
}

type azureTTSChunkedStream struct {
	body       io.ReadCloser
	sampleRate int
}

func (s *azureTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.body.Read(buf)
	if err != nil && n == 0 {
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

func (s *azureTTSChunkedStream) Close() error {
	if s.body == nil {
		return nil
	}
	return s.body.Close()
}
