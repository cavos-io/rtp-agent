package sarvam

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/model"
)

type SarvamSTT struct {
	apiKey string
}

func NewSarvamSTT(apiKey string) *SarvamSTT {
	return &SarvamSTT{
		apiKey: apiKey,
	}
}

func (s *SarvamSTT) Label() string { return "sarvam.STT" }
func (s *SarvamSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *SarvamSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("streaming stt not available via basic rest for sarvam")
}

func (s *SarvamSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if language == "" {
		language = "hi-IN" // Defaulting to Hindi
	}

	url := "https://api.sarvam.ai/speech-to-text"

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "audio.wav")
	part.Write(buf.Bytes())
	writer.WriteField("language_code", language)
	writer.WriteField("model", "saaras:v1")
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("API-Subscription-Key", s.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sarvam stt error: %s", string(respBody))
	}

	var result struct {
		Transcript string `json:"transcript"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: result.Transcript},
		},
	}, nil
}

type SarvamTTS struct {
	apiKey string
	voice  string
}

func NewSarvamTTS(apiKey string, voice string) *SarvamTTS {
	if voice == "" {
		voice = "meera"
	}
	return &SarvamTTS{
		apiKey: apiKey,
		voice:  voice,
	}
}

func (t *SarvamTTS) Label() string { return "sarvam.TTS" }
func (t *SarvamTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SarvamTTS) SampleRate() int { return 8000 }
func (t *SarvamTTS) NumChannels() int { return 1 }

func (t *SarvamTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := "https://api.sarvam.ai/text-to-speech"

	reqBody := map[string]interface{}{
		"inputs": []string{text},
		"target_language_code": "hi-IN",
		"speaker":              t.voice,
		"pitch":                0,
		"pace":                 1.0,
		"loudness":             1.5,
		"speech_sample_rate":   8000,
		"enable_preprocessing": true,
		"model":                "bulbul:v1",
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API-Subscription-Key", t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("sarvam tts error: %s", string(respBody))
	}

	return &sarvamTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *SarvamTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("sarvam streaming tts not natively supported by basic rest api")
}

type sarvamTTSChunkedStream struct {
	resp *http.Response
}

func (s *sarvamTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	var result struct {
		Audios []string `json:"audios"`
	}

	if err := json.NewDecoder(s.resp.Body).Decode(&result); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}

	if len(result.Audios) == 0 {
		return nil, io.EOF
	}

	data, err := base64.StdEncoding.DecodeString(result.Audios[0])
	if err != nil {
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              data,
			SampleRate:        8000,
			NumChannels:       1,
			SamplesPerChannel: uint32(len(data) / 2),
		},
	}, nil
}

func (s *sarvamTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
