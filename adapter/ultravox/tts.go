package ultravox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

type UltravoxTTS struct {
	apiKey string
	voice  string
}

func NewUltravoxTTS(apiKey string, voice string) *UltravoxTTS {
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
func (t *UltravoxTTS) SampleRate() int { return 24000 }
func (t *UltravoxTTS) NumChannels() int { return 1 }

func (t *UltravoxTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
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
	resp *http.Response
}

func (s *ultravoxTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
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
	return s.resp.Body.Close()
}
