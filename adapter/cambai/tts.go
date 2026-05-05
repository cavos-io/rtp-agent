package cambai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/library/logger"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

type CambaiTTS struct {
	apiKey string
	voice  string
}

func NewCambaiTTS(apiKey string, voice string) *CambaiTTS {
	if voice == "" {
		voice = "default_voice"
	}
	return &CambaiTTS{
		apiKey: apiKey,
		voice:  voice,
	}
}

func (t *CambaiTTS) Label() string { return "cambai.TTS" }
func (t *CambaiTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *CambaiTTS) SampleRate() int  { return 24000 }
func (t *CambaiTTS) NumChannels() int { return 1 }

func (t *CambaiTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := "https://api.cambai.com/v1/tts"

	reqBody := map[string]interface{}{
		"text":  text,
		"voice": t.voice,
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		logger.Logger.Errorw("[cambai.Synthesize] http.NewRequestWithContext failed", err)
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Logger.Errorw("[cambai.Synthesize] http.DefaultClient.Do failed", err)
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		logger.Logger.Warnw("[cambai.Synthesize] HTTP response non-OK status", nil)
		return nil, fmt.Errorf("cambai tts error: %s", string(respBody))
	}

	return &cambaiTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *CambaiTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	logger.Logger.Warnw("[cambai.TTS.Stream] streaming input is not supported natively via REST API", nil)
	return nil, fmt.Errorf("cambai streaming tts not natively supported by basic rest api")
}

type cambaiTTSChunkedStream struct {
	resp *http.Response
}

func (s *cambaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		logger.Logger.Errorw("[cambaiTTSChunkedStream.Next] error reading response body", err)
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

func (s *cambaiTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
