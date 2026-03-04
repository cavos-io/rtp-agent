package simplismart

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

type SimplismartTTS struct {
	apiKey string
	voice  string
}

func NewSimplismartTTS(apiKey string, voice string) *SimplismartTTS {
	if voice == "" {
		voice = "default_voice"
	}
	return &SimplismartTTS{
		apiKey: apiKey,
		voice:  voice,
	}
}

func (t *SimplismartTTS) Label() string { return "simplismart.TTS" }
func (t *SimplismartTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SimplismartTTS) SampleRate() int { return 24000 }
func (t *SimplismartTTS) NumChannels() int { return 1 }

func (t *SimplismartTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := "https://api.simplismart.live/tts"

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
		return nil, fmt.Errorf("simplismart tts error: %s", string(respBody))
	}

	return &simplismartTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *SimplismartTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("simplismart streaming tts not natively supported by basic rest api")
}

type simplismartTTSChunkedStream struct {
	resp *http.Response
}

func (s *simplismartTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *simplismartTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
