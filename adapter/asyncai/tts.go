package asyncai

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

type AsyncAITTS struct {
	apiKey string
	voice  string
}

func NewAsyncAITTS(apiKey string, voice string) *AsyncAITTS {
	if voice == "" {
		voice = "default_voice"
	}
	return &AsyncAITTS{
		apiKey: apiKey,
		voice:  voice,
	}
}

func (t *AsyncAITTS) Label() string { return "asyncai.TTS" }
func (t *AsyncAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *AsyncAITTS) SampleRate() int { return 24000 }
func (t *AsyncAITTS) NumChannels() int { return 1 }

func (t *AsyncAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := "https://api.async.ai/v1/tts"

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
		return nil, fmt.Errorf("asyncai tts error: %s", string(respBody))
	}

	return &asyncaiTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *AsyncAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("asyncai streaming tts not natively supported by basic rest api")
}

type asyncaiTTSChunkedStream struct {
	resp *http.Response
}

func (s *asyncaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *asyncaiTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
