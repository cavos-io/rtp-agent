package openai

import (
	"bufio"
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

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/sashabaranov/go-openai"
)

type OpenAITTS struct {
	client         *openai.Client
	httpClient     openai.HTTPDoer
	apiKey         string
	model          openai.SpeechModel
	voice          openai.SpeechVoice
	baseURL        string
	speed          float64
	instructions   string
	responseFormat openai.SpeechResponseFormat
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

	return &openaiTTSChunkedStream{
		resp:         resp,
		streamFormat: openAITTSStreamFormatForModel(t.model),
	}, nil
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

type openaiTTSChunkedStream struct {
	resp         io.ReadCloser
	streamFormat string
	scanner      *bufio.Scanner
}

func (s *openaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.streamFormat == openAITTSStreamFormatSSE {
		return s.nextSSE()
	}
	return s.nextAudio()
}

func (s *openaiTTSChunkedStream) nextAudio() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Read(buf)
	if err != nil && n == 0 {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        24000, // Common for TTS, though MP3 needs decoding
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *openaiTTSChunkedStream) nextSSE() (*tts.SynthesizedAudio, error) {
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
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              audioData,
					SampleRate:        24000,
					NumChannels:       1,
					SamplesPerChannel: uint32(len(audioData) / 2),
				},
			}, nil
		case "speech.audio.done":
			return nil, io.EOF
		}
	}
	if err := s.scanner.Err(); err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	return nil, io.EOF
}

func (s *openaiTTSChunkedStream) Close() error {
	return s.resp.Close()
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
