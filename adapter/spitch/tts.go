package spitch

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

type SpitchTTS struct {
	apiKey string
	voice  string
}

func NewSpitchTTS(apiKey string, voice string) *SpitchTTS {
	if voice == "" {
		voice = "default_voice"
	}
	return &SpitchTTS{
		apiKey: apiKey,
		voice:  voice,
	}
}

func (t *SpitchTTS) Label() string { return "spitch.TTS" }
func (t *SpitchTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SpitchTTS) SampleRate() int { return 24000 }
func (t *SpitchTTS) NumChannels() int { return 1 }

func (t *SpitchTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := "https://api.spitch.ai/tts/v1/synthesize"

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
		return nil, fmt.Errorf("spitch tts error: %s", string(respBody))
	}

	return &spitchTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *SpitchTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("spitch streaming tts not natively supported by basic rest api")
}

type spitchTTSChunkedStream struct {
	resp *http.Response
}

func (s *spitchTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *spitchTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
