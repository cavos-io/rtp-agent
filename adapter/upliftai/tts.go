package upliftai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/model"
)

type UpliftAITTS struct {
	apiKey string
	voice  string
}

func NewUpliftAITTS(apiKey string, voice string) *UpliftAITTS {
	if voice == "" {
		voice = "default_voice"
	}
	return &UpliftAITTS{
		apiKey: apiKey,
		voice:  voice,
	}
}

func (t *UpliftAITTS) Label() string { return "upliftai.TTS" }
func (t *UpliftAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *UpliftAITTS) SampleRate() int { return 24000 }
func (t *UpliftAITTS) NumChannels() int { return 1 }

func (t *UpliftAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := "https://api.upliftai.org/v1/tts"

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
		return nil, fmt.Errorf("upliftai tts error: %s", string(respBody))
	}

	return &upliftAITTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *UpliftAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("upliftai streaming tts not natively supported by basic rest api")
}

type upliftAITTSChunkedStream struct {
	resp *http.Response
}

func (s *upliftAITTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *upliftAITTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
