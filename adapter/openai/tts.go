package openai

import (
	"bufio"
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

	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/sashabaranov/go-openai"
)

type OpenAITTS struct {
	tts.MetricsEmitter
	client         *openai.Client
	httpClient     openai.HTTPDoer
	apiKey         string
	model          openai.SpeechModel
	voice          openai.SpeechVoice
	baseURL        string
	speed          float64
	instructions   string
	responseFormat openai.SpeechResponseFormat
	mu             sync.Mutex
	streams        map[*openaiTTSChunkedStream]struct{}
}

const (
	openAITTSStreamFormatAudio = "audio"
	openAITTSStreamFormatSSE   = "sse"
	openAITTSMaxSSELineBytes   = 16 * 1024 * 1024
)

type OpenAITTSOption func(*OpenAITTS)

func WithOpenAITTSModel(model openai.SpeechModel) OpenAITTSOption {
	return func(t *OpenAITTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithOpenAITTSVoice(voice openai.SpeechVoice) OpenAITTSOption {
	return func(t *OpenAITTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithOpenAITTSSpeed(speed float64) OpenAITTSOption {
	return func(t *OpenAITTS) {
		t.speed = speed
	}
}

func WithOpenAITTSInstructions(instructions string) OpenAITTSOption {
	return func(t *OpenAITTS) {
		t.instructions = instructions
	}
}

func WithOpenAITTSResponseFormat(format openai.SpeechResponseFormat) OpenAITTSOption {
	return func(t *OpenAITTS) {
		t.responseFormat = format
	}
}

func WithOpenAITTSBaseURL(baseURL string) OpenAITTSOption {
	return func(t *OpenAITTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func withOpenAITTSHTTPClient(client openai.HTTPDoer) OpenAITTSOption {
	return func(t *OpenAITTS) {
		if client != nil {
			t.httpClient = client
		}
	}
}

func NewOpenAITTS(apiKey string, model openai.SpeechModel, voice openai.SpeechVoice, opts ...OpenAITTSOption) (*OpenAITTS, error) {
	if apiKey == "" {
		apiKey = os.Getenv(openAIAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("%s", openAIAPIKeyRequiredMessage)
	}
	return newOpenAITTS(openai.NewClient(apiKey), apiKey, model, voice, opts...), nil
}

func NewAzureOpenAITTS(model openai.SpeechModel, voice openai.SpeechVoice, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...OpenAITTSOption) (*OpenAITTS, error) {
	if model == "" {
		model = openai.TTSModelGPT4oMini
	}
	if voice == "" {
		voice = openai.VoiceAsh
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
		return nil, fmt.Errorf("%s is required for Azure OpenAI TTS", azureOpenAIEndpointEnv)
	}
	if apiKey == "" && azureADToken == "" {
		return nil, fmt.Errorf("%s or %s is required for Azure OpenAI TTS", azureOpenAIAPIKeyEnv, azureOpenAIADTokenEnv)
	}
	if azureDeployment == "" {
		azureDeployment = string(model)
	}

	provider := &OpenAITTS{
		apiKey:         apiKey,
		model:          model,
		voice:          voice,
		baseURL:        azureEndpoint,
		speed:          1.0,
		responseFormat: openai.SpeechResponseFormatMp3,
		streams:        make(map[*openaiTTSChunkedStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.responseFormat == "" {
		provider.responseFormat = openai.SpeechResponseFormatMp3
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
	config.HTTPClient = &openAITTSStreamFormatHTTPClient{base: config.HTTPClient, provider: provider}
	if apiKey == "" && azureADToken != "" {
		config.HTTPClient = &azureADTokenHTTPClient{
			base:  config.HTTPClient,
			token: azureADToken,
		}
	}
	provider.client = openai.NewClientWithConfig(config)
	return provider, nil
}

func newOpenAITTS(client *openai.Client, apiKey string, model openai.SpeechModel, voice openai.SpeechVoice, opts ...OpenAITTSOption) *OpenAITTS {
	if model == "" {
		model = openai.TTSModelGPT4oMini
	}
	if voice == "" {
		voice = openai.VoiceAsh
	}
	provider := &OpenAITTS{
		client:         client,
		apiKey:         apiKey,
		model:          model,
		voice:          voice,
		baseURL:        defaultOpenAIBaseURL,
		speed:          1.0,
		responseFormat: openai.SpeechResponseFormatMp3,
		streams:        make(map[*openaiTTSChunkedStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.responseFormat == "" {
		provider.responseFormat = openai.SpeechResponseFormatMp3
	}
	if provider.baseURL != "" || provider.httpClient != nil {
		config := openai.DefaultConfig(apiKey)
		if provider.baseURL != "" {
			config.BaseURL = provider.baseURL
		}
		if provider.httpClient != nil {
			config.HTTPClient = provider.httpClient
		}
		config.HTTPClient = &openAITTSStreamFormatHTTPClient{base: config.HTTPClient, provider: provider}
		provider.client = openai.NewClientWithConfig(config)
	}
	return provider
}

func (t *OpenAITTS) UpdateOptions(opts ...OpenAITTSOption) {
	for _, opt := range opts {
		opt(t)
	}
}

func (t *OpenAITTS) Label() string { return "openai.TTS" }
func (t *OpenAITTS) Provider() string {
	u, err := url.Parse(t.baseURL)
	if err != nil || u.Host == "" {
		return "openai"
	}
	return u.Host
}
func (t *OpenAITTS) Model() string { return string(t.model) }
func (t *OpenAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *OpenAITTS) SampleRate() int  { return 24000 }
func (t *OpenAITTS) NumChannels() int { return 1 }

func (t *OpenAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req := buildOpenAITTSSpeechRequest(t, text)

	resp, err := t.client.CreateSpeech(ctx, req)
	if err != nil {
		return nil, mapOpenAIError(err)
	}

	stream := &openaiTTSChunkedStream{
		resp:           resp,
		responseFormat: t.responseFormat,
		streamFormat:   openAITTSStreamFormatForModel(t.model),
		provider:       t,
		inputText:      text,
	}
	t.registerStream(stream)
	return stream, nil
}

func buildOpenAITTSSpeechRequest(t *OpenAITTS, text string) openai.CreateSpeechRequest {
	return openai.CreateSpeechRequest{
		Model:          t.model,
		Input:          text,
		Voice:          t.voice,
		Instructions:   t.instructions,
		ResponseFormat: t.responseFormat,
		Speed:          t.speed,
	}
}

func (t *OpenAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	// OpenAI does not have a native streaming API for TTS via standard REST.
	return nil, io.ErrUnexpectedEOF
}

func (t *OpenAITTS) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	streams := make([]*openaiTTSChunkedStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*openaiTTSChunkedStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *OpenAITTS) registerStream(stream *openaiTTSChunkedStream) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.streams[stream] = struct{}{}
}

func (t *OpenAITTS) unregisterStream(stream *openaiTTSChunkedStream) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

type openaiTTSChunkedStream struct {
	resp           io.ReadCloser
	responseFormat openai.SpeechResponseFormat
	streamFormat   string
	provider       *OpenAITTS
	inputText      string
	scanner        *bufio.Scanner
	decoder        codecs.AudioStreamDecoder
	decodeStarted  bool
	decodeErrCh    chan error
	wavBuffer      []byte
	wavDone        bool
	wavHeaderDone  bool
	wavDataLeft    int
	wavSampleRate  uint32
	wavChannels    uint32
	closed         bool
}

func (s *openaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed {
		return nil, io.EOF
	}
	if s.streamFormat == openAITTSStreamFormatSSE {
		return s.nextSSE()
	}
	return s.nextAudio()
}

func (s *openaiTTSChunkedStream) nextAudio() (*tts.SynthesizedAudio, error) {
	if s.responseFormat == openai.SpeechResponseFormatMp3 {
		return s.nextMP3Audio()
	}

	buf := make([]byte, 4096)
	for {
		n, err := s.resp.Read(buf)
		if n > 0 {
			audio, frameErr := s.audioFrameFromPCMChunk(buf[:n])
			if frameErr != nil {
				return nil, frameErr
			}
			if audio != nil {
				return audio, nil
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, llm.NewAPIConnectionError(err.Error())
		}
	}
}

func (s *openaiTTSChunkedStream) nextMP3Audio() (*tts.SynthesizedAudio, error) {
	if !s.decodeStarted {
		s.decodeStarted = true
		s.decoder = codecs.NewMP3AudioStreamDecoder()
		s.decodeErrCh = make(chan error, 1)
		go s.feedMP3Audio()
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if readErr := s.decodeReadError(); readErr != nil {
			return nil, readErr
		}
		if openAITTSMP3DecodeEOF(err) {
			return nil, io.EOF
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func (s *openaiTTSChunkedStream) feedMP3Audio() {
	defer s.decoder.EndInput()
	buf := make([]byte, 4096)
	for {
		n, err := s.resp.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.decoder.Push(chunk)
		}
		if err != nil {
			if err != io.EOF {
				select {
				case s.decodeErrCh <- llm.NewAPIConnectionError(err.Error()):
				default:
				}
			}
			return
		}
	}
}

func (s *openaiTTSChunkedStream) nextSSE() (*tts.SynthesizedAudio, error) {
	if s.responseFormat == openai.SpeechResponseFormatMp3 {
		return s.nextSSEMP3Audio()
	}
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp)
		s.scanner.Buffer(make([]byte, 0, 64*1024), openAITTSMaxSSELineBytes)
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if data == "[DONE]" {
			return nil, io.EOF
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		eventType, _ := event["type"].(string)
		switch eventType {
		case "speech.audio.delta":
			audioB64, _ := event["delta"].(string)
			if audioB64 == "" {
				audioB64, _ = event["audio"].(string)
			}
			if audioB64 == "" {
				continue
			}
			audioData, err := base64.StdEncoding.DecodeString(audioB64)
			if err != nil {
				return nil, llm.NewAPIConnectionError(err.Error())
			}
			audio, err := s.audioFrameFromPCMChunk(audioData)
			if err != nil {
				return nil, err
			}
			if audio == nil {
				continue
			}
			return audio, nil
		case "speech.audio.done":
			s.emitSSEUsageMetrics(event)
			return nil, io.EOF
		}
	}
	if err := s.scanner.Err(); err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	return nil, io.EOF
}

func (s *openaiTTSChunkedStream) nextSSEMP3Audio() (*tts.SynthesizedAudio, error) {
	if !s.decodeStarted {
		s.decodeStarted = true
		s.decoder = codecs.NewMP3AudioStreamDecoder()
		s.decodeErrCh = make(chan error, 1)
		go s.feedSSEMP3Audio()
	}
	frame, err := s.decoder.Next()
	if err != nil {
		if readErr := s.decodeReadError(); readErr != nil {
			return nil, readErr
		}
		if openAITTSMP3DecodeEOF(err) {
			return nil, io.EOF
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func openAITTSMP3DecodeEOF(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "decoder closed") || strings.Contains(msg, "failed to initialize mp3 decoder: EOF")
}

func (s *openaiTTSChunkedStream) audioFrameFromPCMChunk(data []byte) (*tts.SynthesizedAudio, error) {
	sampleRate := uint32(24000)
	channels := uint32(1)
	if s.wavSampleRate > 0 {
		sampleRate = s.wavSampleRate
	}
	if s.wavChannels > 0 {
		channels = s.wavChannels
	}
	if s.responseFormat == openai.SpeechResponseFormatWav {
		frame, ok, err := s.nextWAVFrame(data)
		if err != nil {
			return nil, llm.NewAPIConnectionError(err.Error())
		}
		if ok {
			return &tts.SynthesizedAudio{Frame: frame}, nil
		}
		return nil, nil
	}
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              data,
			SampleRate:        sampleRate,
			NumChannels:       channels,
			SamplesPerChannel: uint32(len(data)) / max(channels*2, 1),
		},
	}, nil
}

func (s *openaiTTSChunkedStream) nextWAVFrame(data []byte) (*model.AudioFrame, bool, error) {
	if s.wavDone {
		return nil, false, nil
	}
	s.wavBuffer = append(s.wavBuffer, data...)
	if !s.wavHeaderDone {
		ok, err := s.parseWAVHeader()
		if err != nil || !ok {
			return nil, ok, err
		}
	}
	if len(s.wavBuffer) == 0 || s.wavDataLeft <= 0 {
		return nil, false, nil
	}
	emitLen := min(len(s.wavBuffer), s.wavDataLeft)
	blockAlign := int(max(s.wavChannels*2, 1))
	if emitLen < s.wavDataLeft {
		emitLen -= emitLen % blockAlign
		if emitLen == 0 {
			return nil, false, nil
		}
	}
	pcm := s.wavBuffer[:emitLen]
	s.wavBuffer = s.wavBuffer[emitLen:]
	s.wavDataLeft -= emitLen
	if s.wavDataLeft == 0 {
		s.wavDone = true
	}
	return &model.AudioFrame{
		Data:              pcm,
		SampleRate:        s.wavSampleRate,
		NumChannels:       s.wavChannels,
		SamplesPerChannel: uint32(len(pcm)) / max(s.wavChannels*2, 1),
	}, true, nil
}

func (s *openaiTTSChunkedStream) parseWAVHeader() (bool, error) {
	if len(s.wavBuffer) < 12 {
		return false, nil
	}
	if string(s.wavBuffer[:4]) != "RIFF" || string(s.wavBuffer[8:12]) != "WAVE" {
		return false, nil
	}
	pos := 12
	for pos+8 <= len(s.wavBuffer) {
		chunkID := string(s.wavBuffer[pos : pos+4])
		chunkSize := int(binary.LittleEndian.Uint32(s.wavBuffer[pos+4 : pos+8]))
		if chunkSize < 0 {
			return true, fmt.Errorf("invalid openai wav chunk size")
		}
		chunkDataStart := pos + 8
		chunkEnd := chunkDataStart + chunkSize
		switch chunkID {
		case "fmt ":
			if chunkEnd > len(s.wavBuffer) {
				return false, nil
			}
			if chunkSize < 16 {
				return true, fmt.Errorf("invalid openai wav fmt chunk")
			}
			audioFormat := binary.LittleEndian.Uint16(s.wavBuffer[chunkDataStart : chunkDataStart+2])
			channels := uint32(binary.LittleEndian.Uint16(s.wavBuffer[chunkDataStart+2 : chunkDataStart+4]))
			sampleRate := binary.LittleEndian.Uint32(s.wavBuffer[chunkDataStart+4 : chunkDataStart+8])
			bitsPerSample := binary.LittleEndian.Uint16(s.wavBuffer[chunkDataStart+14 : chunkDataStart+16])
			if audioFormat != 1 || bitsPerSample != 16 {
				return true, fmt.Errorf("unsupported openai wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
			}
			s.wavSampleRate = sampleRate
			s.wavChannels = channels
		case "data":
			if s.wavSampleRate == 0 || s.wavChannels == 0 {
				return true, fmt.Errorf("missing openai wav format metadata")
			}
			s.wavHeaderDone = true
			s.wavDataLeft = chunkSize
			s.wavBuffer = s.wavBuffer[chunkDataStart:]
			return true, nil
		default:
			if chunkEnd > len(s.wavBuffer) {
				return false, nil
			}
		}
		pos = chunkEnd
		if chunkSize%2 == 1 {
			pos++
		}
	}
	return false, nil
}

func (s *openaiTTSChunkedStream) feedSSEMP3Audio() {
	defer s.decoder.EndInput()
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp)
		s.scanner.Buffer(make([]byte, 0, 64*1024), openAITTSMaxSSELineBytes)
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if data == "[DONE]" {
			break
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		eventType, _ := event["type"].(string)
		switch eventType {
		case "speech.audio.delta":
			audioB64, _ := event["delta"].(string)
			if audioB64 == "" {
				audioB64, _ = event["audio"].(string)
			}
			if audioB64 == "" {
				continue
			}
			audioData, err := base64.StdEncoding.DecodeString(audioB64)
			if err != nil {
				s.sendDecodeReadError(llm.NewAPIConnectionError(err.Error()))
				return
			}
			s.decoder.Push(audioData)
		case "speech.audio.done":
			s.emitSSEUsageMetrics(event)
			return
		}
	}
	if err := s.scanner.Err(); err != nil {
		s.sendDecodeReadError(llm.NewAPIConnectionError(err.Error()))
	}
}

func (s *openaiTTSChunkedStream) sendDecodeReadError(err error) {
	if err == nil || s.decodeErrCh == nil {
		return
	}
	select {
	case s.decodeErrCh <- err:
	default:
	}
}

func (s *openaiTTSChunkedStream) decodeReadError() error {
	if s.decodeErrCh == nil {
		return nil
	}
	select {
	case err := <-s.decodeErrCh:
		return err
	default:
		return nil
	}
}

func (s *openaiTTSChunkedStream) emitSSEUsageMetrics(event map[string]any) {
	if s.provider == nil {
		return
	}
	usage, _ := event["usage"].(map[string]any)
	inputTokens := openAIInt(usage["input_tokens"])
	outputTokens := openAIInt(usage["output_tokens"])
	if inputTokens == 0 && outputTokens == 0 {
		return
	}
	s.provider.EmitMetricsCollected(&telemetry.TTSMetrics{
		Label:           s.provider.Label(),
		Timestamp:       time.Now(),
		CharactersCount: len(s.inputText),
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		Streamed:        false,
		Metadata: &telemetry.Metadata{
			ModelName:     s.provider.Model(),
			ModelProvider: s.provider.Provider(),
		},
	})
}

func (s *openaiTTSChunkedStream) Close() error {
	if s.closed {
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return nil
	}
	s.closed = true
	if s.decoder != nil {
		_ = s.decoder.Close()
	}
	err := s.resp.Close()
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	return err
}

type openAITTSStreamFormatHTTPClient struct {
	base     openai.HTTPDoer
	provider *OpenAITTS
}

func (c *openAITTSStreamFormatHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if req != nil && req.Body != nil && strings.HasSuffix(req.URL.Path, "/audio/speech") {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		updated := addOpenAITTSRequestFields(body, c.provider)
		req.Body = io.NopCloser(bytes.NewReader(updated))
		req.ContentLength = int64(len(updated))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(updated)), nil
		}
	}
	base := c.base
	if base == nil {
		base = http.DefaultClient
	}
	return base.Do(req)
}

func addOpenAITTSRequestFields(body []byte, provider *OpenAITTS) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	if _, ok := payload["stream_format"]; !ok {
		modelName, _ := payload["model"].(string)
		payload["stream_format"] = openAITTSStreamFormatForModel(openai.SpeechModel(modelName))
	}
	if provider != nil && provider.speed == 0 {
		if _, ok := payload["speed"]; !ok {
			payload["speed"] = 0
		}
	}
	updated, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return updated
}

func openAITTSStreamFormatForModel(model openai.SpeechModel) string {
	switch model {
	case openai.TTSModel1, openai.TTSModel1HD:
		return openAITTSStreamFormatAudio
	default:
		return openAITTSStreamFormatSSE
	}
}
