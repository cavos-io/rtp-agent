package murf

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

type MurfTTS struct {
	apiKey  string
	voiceID string
	model   string
}

func NewMurfTTS(apiKey string, voiceID string, model string) *MurfTTS {
	if voiceID == "" {
		voiceID = "en-US-ava" // Placeholder default
	}
	if model == "" {
		model = "FALCON"
	}
	return &MurfTTS{
		apiKey:  apiKey,
		voiceID: voiceID,
		model:   model,
	}
}

func (t *MurfTTS) Label() string { return "murf.TTS" }
func (t *MurfTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *MurfTTS) SampleRate() int  { return 24000 }
func (t *MurfTTS) NumChannels() int { return 1 }

func (t *MurfTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	apiURL := "https://api.murf.ai/v1/speech/generate"
	body := map[string]interface{}{
		"text":       text,
		"voiceId":    t.voiceID,
		"model":      t.model,
		"format":     "PCM",
		"sampleRate": 24000,
	}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("murf error: %s", string(respBody))
	}

	return &murfChunkedStream{
		resp: resp,
	}, nil
}

type murfChunkedStream struct {
	resp *http.Response
}

func (s *murfChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 8192)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF && n > 0 {
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              buf[:n],
					SampleRate:        24000,
					NumChannels:       1,
					SamplesPerChannel: uint32(n / 2),
				},
				IsFinal: true,
			}, nil
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

func (s *murfChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func (t *MurfTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	// For Murf, 'streaming' currently means audio streaming for a single input text.
	// In the future, if they support WebSocket increments like ElevenLabs, this can be updated.
	// For now, we'll wrap a buffer that synthesizes on Flush().
	return &murfStreamAdapter{
		ctx: ctx,
		tts: t,
	}, nil
}

type murfStreamAdapter struct {
	ctx    context.Context
	tts    *MurfTTS
	buffer bytes.Buffer
	stream tts.ChunkedStream
}

func (s *murfStreamAdapter) PushText(text string) error {
	s.buffer.WriteString(text)
	return nil
}

func (s *murfStreamAdapter) Flush() error {
	if s.buffer.Len() == 0 {
		return nil
	}
	stream, err := s.tts.Synthesize(s.ctx, s.buffer.String())
	if err != nil {
		return err
	}
	s.stream = stream
	s.buffer.Reset()
	return nil
}

func (s *murfStreamAdapter) Next() (*tts.SynthesizedAudio, error) {
	if s.stream == nil {
		return nil, io.EOF
	}
	return s.stream.Next()
}

func (s *murfStreamAdapter) Close() error {
	if s.stream != nil {
		return s.stream.Close()
	}
	return nil
}
