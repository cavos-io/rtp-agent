package ultravox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const ultravoxAPIKeyEnv = "ULTRAVOX_API_KEY"

type UltravoxTTS struct {
	apiKey string
	voice  string
}

func NewUltravoxTTS(apiKey string, voice string) *UltravoxTTS {
	if apiKey == "" {
		apiKey = os.Getenv(ultravoxAPIKeyEnv)
	}
	if voice == "" {
		voice = "default"
	}
	return &UltravoxTTS{
		apiKey: apiKey,
		voice:  voice,
	}
}

func (t *UltravoxTTS) Label() string { return "ultravox.TTS" }
func (t *UltravoxTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *UltravoxTTS) SampleRate() int  { return 24000 }
func (t *UltravoxTTS) NumChannels() int { return 1 }

func (t *UltravoxTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("ultravox API key is required; provide it via api_key parameter or ULTRAVOX_API_KEY environment variable")
	}

	url := "https://api.ultravox.ai/api/tts"

	reqBody := map[string]interface{}{
		"text":  text,
		"voice": t.voice,
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("ultravox tts error: %s", string(respBody))
	}

	return &ultravoxTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *UltravoxTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("ultravox streaming tts not natively supported by basic rest api")
}

type ultravoxTTSChunkedStream struct {
	resp      *http.Response
	finalSent bool
	closed    bool
}

func (s *ultravoxTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.finalSent || s.closed {
		return nil, io.EOF
	}
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF {
			if n > 0 {
				return &tts.SynthesizedAudio{
					Frame: &model.AudioFrame{
						Data:              buf[:n],
						SampleRate:        24000,
						NumChannels:       1,
						SamplesPerChannel: uint32(n / 2),
					},
				}, nil
			}
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *ultravoxTTSChunkedStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.resp.Body.Close()
}
