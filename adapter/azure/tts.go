package azure

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultAzureTTSVoice        = "en-US-JennyNeural"
	defaultAzureTTSLanguage     = "en-US"
	defaultAzureTTSSampleRate   = 24000
	defaultAzureTTSSampleFormat = "raw-24khz-16bit-mono-pcm"
)

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
	ssml := fmt.Sprintf(`<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="http://www.w3.org/2001/mstts" xml:lang="%s"><voice name="%s">%s</voice></speak>`, language, t.voice, text)

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
	carry      byte
	hasCarry   bool
}

func (s *azureTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	var start int

	for {
		if s.hasCarry {
			buf[0] = s.carry
			start = 1
		} else {
			start = 0
		}

		n, err := s.body.Read(buf[start:])
		if err != nil && n == 0 {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, err
		}

		total := start + n
		if total%2 != 0 {
			s.carry = buf[total-1]
			s.hasCarry = true
			total--
		} else {
			s.hasCarry = false
		}

		if total > 0 {
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              buf[:total],
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(total / 2),
				},
			}, nil
		}

		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, err
		}
	}
}

func (s *azureTTSChunkedStream) Close() error {
	if s.body == nil {
		return nil
	}
	return s.body.Close()
}
