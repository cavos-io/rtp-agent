package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	oa "github.com/sashabaranov/go-openai"
	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/utils/language"
	"github.com/cavos-io/rtp-agent/model"
)

type AzureSTT struct {
	apiKey     string
	region     string
	baseURL    string
	httpClient *http.Client
}

type STTOption func(*AzureSTT)

func WithSTTBaseURL(url string) STTOption {
	return func(s *AzureSTT) {
		s.baseURL = url
	}
}

func WithSTTHTTPClient(client *http.Client) STTOption {
	return func(s *AzureSTT) {
		s.httpClient = client
	}
}

func NewAzureSTT(apiKey string, region string, opts ...STTOption) *AzureSTT {
	s := &AzureSTT{
		apiKey:     apiKey,
		region:     region,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *AzureSTT) Label() string { return "azure.STT" }
func (s *AzureSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *AzureSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("streaming azure stt not yet implemented via rest")
}

func (s *AzureSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, languageStr string) (*stt.SpeechEvent, error) {
	languageStr = language.NormalizeLanguage(languageStr)
	if languageStr == "" {
		languageStr = "en-US"
	}
	url := s.baseURL
	if url == "" {
		url = fmt.Sprintf("https://%s.stt.speech.microsoft.com/speech/recognition/conversation/cognitiveservices/v1", s.region)
	}
	url = fmt.Sprintf("%s?language=%s", strings.TrimSuffix(url, "/"), languageStr)

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "audio/wav; codecs=audio/pcm; samplerate=16000")
	req.Header.Set("Ocp-Apim-Subscription-Key", s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure stt error: %s", string(respBody))
	}

	var result struct {
		DisplayText string `json:"DisplayText"`
		RecognitionStatus string `json:"RecognitionStatus"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: result.DisplayText},
		},
	}, nil
}

type AzureTTS struct {
	apiKey     string
	region     string
	voice      string
	baseURL    string
	httpClient *http.Client
}

type TTSOption func(*AzureTTS)

func WithTTSBaseURL(url string) TTSOption {
	return func(t *AzureTTS) {
		t.baseURL = url
	}
}

func WithTTSHTTPClient(client *http.Client) TTSOption {
	return func(t *AzureTTS) {
		t.httpClient = client
	}
}

func NewAzureTTS(apiKey string, region string, voice string, opts ...TTSOption) *AzureTTS {
	if voice == "" {
		voice = "en-US-AvaMultilingualNeural"
	}
	t := &AzureTTS{
		apiKey:     apiKey,
		region:     region,
		voice:      voice,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *AzureTTS) Label() string { return "azure.TTS" }
func (t *AzureTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *AzureTTS) SampleRate() int { return 16000 }
func (t *AzureTTS) NumChannels() int { return 1 }

func (t *AzureTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	apiURL := t.baseURL
	if apiURL == "" {
		apiURL = fmt.Sprintf("https://%s.tts.speech.microsoft.com/cognitiveservices/v1", t.region)
	}
	ssml := fmt.Sprintf(`<speak version='1.0' xml:lang='en-US'><voice name='%s'>%s</voice></speak>`, t.voice, text)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBufferString(ssml))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", "raw-16khz-16bit-mono-pcm")
	req.Header.Set("Ocp-Apim-Subscription-Key", t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("azure tts error: %s", string(respBody))
	}

	return &azureTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *AzureTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming azure tts not yet implemented")
}

type azureTTSChunkedStream struct {
	resp *http.Response
}

func (s *azureTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n == 0 && err != nil {
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *azureTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

// AzureLLM is a thin wrapper around OpenAILLM configured for Azure.
// It is recommended to use the openai adapter directly with Azure configuration
// if more advanced options are needed.
func NewAzureLLM(apiKey string, endpoint string, deployment string, opts ...openai.Option) *openai.OpenAILLM {
	config := oa.DefaultAzureConfig(apiKey, endpoint)
	// We use the deployment name as the model for Azure OpenAI
	return openai.NewOpenAILLM(apiKey, deployment, append(opts, func(c *oa.ClientConfig) {
		*c = config
	})...)
}

