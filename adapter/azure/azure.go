package azure

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	azureSpeechKeyEnv           = "AZURE_SPEECH_KEY"
	azureSpeechRegionEnv        = "AZURE_SPEECH_REGION"
	defaultAzureTTSVoice        = "en-US-JennyNeural"
	defaultAzureTTSSampleRate   = 24000
	defaultAzureTTSSampleFormat = "raw-24khz-16bit-mono-pcm"
)

type AzureSTT struct {
	apiKey string
	region string
}

func NewAzureSTT(apiKey string, region string) (*AzureSTT, error) {
	if apiKey == "" {
		apiKey = os.Getenv(azureSpeechKeyEnv)
	}
	if region == "" {
		region = os.Getenv(azureSpeechRegionEnv)
	}
	if apiKey == "" || region == "" {
		return nil, fmt.Errorf("azure speech config requires AZURE_SPEECH_KEY and AZURE_SPEECH_REGION")
	}
	return &AzureSTT{
		apiKey: apiKey,
		region: region,
	}, nil
}

func (s *AzureSTT) Label() string { return "azure.STT" }
func (s *AzureSTT) Model() string { return "unknown" }
func (s *AzureSTT) Provider() string {
	return "Azure STT"
}
func (s *AzureSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, AlignedTranscript: "chunk", OfflineRecognize: false}
}

func (s *AzureSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("streaming azure stt not yet implemented via rest")
}

func (s *AzureSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, languageStr string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("azure STT does not support single frame recognition")
}

type AzureTTS struct {
	apiKey     string
	region     string
	voice      string
	sampleRate int
	httpClient *http.Client
}

func NewAzureTTS(apiKey string, region string, voice string) (*AzureTTS, error) {
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
	return &AzureTTS{
		apiKey:     apiKey,
		region:     region,
		voice:      voice,
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
	ssml := fmt.Sprintf(`<speak version="1.0" xml:lang="en-US"><voice name="%s">%s</voice></speak>`, t.voice, text)

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
	if err != nil {
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
