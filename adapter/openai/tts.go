package openai

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
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
		return nil, err
	}

	return &openaiTTSChunkedStream{
		resp: resp,
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
	resp io.ReadCloser
}

func (s *openaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Read(buf)
	if err != nil && n == 0 {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
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

func (s *openaiTTSChunkedStream) Close() error {
	return s.resp.Close()
}
