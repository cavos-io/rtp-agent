package fishaudio

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

type FishAudioTTS struct {
	apiKey string
	voice  string
}

func NewFishAudioTTS(apiKey string, voice string) *FishAudioTTS {
	if voice == "" {
		voice = "default_voice"
	}
	return &FishAudioTTS{
		apiKey: apiKey,
		voice:  voice,
	}
}

func (t *FishAudioTTS) Label() string { return "fishaudio.TTS" }
func (t *FishAudioTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *FishAudioTTS) SampleRate() int { return 44100 }
func (t *FishAudioTTS) NumChannels() int { return 1 }

func (t *FishAudioTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := "https://api.fish.audio/v1/tts"

	reqBody := map[string]interface{}{
		"text":       text,
		"reference_id": t.voice,
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
		return nil, fmt.Errorf("fishaudio tts error: %s", string(respBody))
	}

	return &fishaudioTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *FishAudioTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("fishaudio streaming tts not natively supported by basic rest api")
}

type fishaudioTTSChunkedStream struct {
	resp *http.Response
}

func (s *fishaudioTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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
			SampleRate:        44100,
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *fishaudioTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
