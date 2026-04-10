package telnyx

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

type TelnyxTTS struct {
	apiKey string
	voice  string
}

func NewTelnyxTTS(apiKey string, voice string) *TelnyxTTS {
	if voice == "" {
		voice = "female-1"
	}
	return &TelnyxTTS{
		apiKey: apiKey,
		voice:  voice,
	}
}

func (t *TelnyxTTS) Label() string { return "telnyx.TTS" }
func (t *TelnyxTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *TelnyxTTS) SampleRate() int { return 24000 }
func (t *TelnyxTTS) NumChannels() int { return 1 }

func (t *TelnyxTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := "https://api.telnyx.com/v2/ai/audio/speech"

	reqBody := map[string]interface{}{
		"input": text,
		"voice": t.voice,
		"model": "eleven_multilingual_v2", // Telnyx proxy
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
		return nil, fmt.Errorf("telnyx tts error: %s", string(respBody))
	}

	return &telnyxTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *TelnyxTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("telnyx streaming tts not natively supported by basic rest api")
}

type telnyxTTSChunkedStream struct {
	resp *http.Response
}

func (s *telnyxTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *telnyxTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
