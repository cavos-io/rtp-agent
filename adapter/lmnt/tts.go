package lmnt

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

type LMNTTTS struct {
	apiKey string
	voice  string
}

func NewLMNTTTS(apiKey string, voice string) *LMNTTTS {
	if voice == "" {
		voice = "lily"
	}
	return &LMNTTTS{
		apiKey: apiKey,
		voice:  voice,
	}
}

func (t *LMNTTTS) Label() string { return "lmnt.TTS" }
func (t *LMNTTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *LMNTTTS) SampleRate() int { return 24000 }
func (t *LMNTTTS) NumChannels() int { return 1 }

func (t *LMNTTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := "https://api.lmnt.com/v1/ai/speech"

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("text", text)
	writer.WriteField("voice", t.voice)
	writer.WriteField("format", "wav")
	writer.WriteField("sample_rate", "24000")
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-API-Key", t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("lmnt tts error: %s", string(respBody))
	}

	return &lmntTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *LMNTTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts is not supported by the LMNT TTS API")
}

type lmntTTSChunkedStream struct {
	resp *http.Response
}

func (s *lmntTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *lmntTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

