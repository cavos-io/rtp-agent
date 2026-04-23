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

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

type SarvamSTT struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type STTOption func(*SarvamSTT)

func WithSTTURL(url string) STTOption {
	return func(s *SarvamSTT) {
		s.baseURL = url
	}
}

func WithSTTHttpClient(client *http.Client) STTOption {
	return func(s *SarvamSTT) {
		s.httpClient = client
	}
}

func NewSarvamSTT(apiKey string, opts ...STTOption) *SarvamSTT {
	s := &SarvamSTT{
		apiKey:     apiKey,
		baseURL:    "https://api.sarvam.ai/speech-to-text",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
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

	url := s.baseURL

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
	resp, err := s.httpClient.Do(req)
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
	apiKey     string
	voice      string
	baseURL    string
	httpClient *http.Client
}

type TTSOption func(*SarvamTTS)

func WithTTSURL(url string) TTSOption {
	return func(t *SarvamTTS) {
		t.baseURL = url
	}
}

func WithTTSHttpClient(client *http.Client) TTSOption {
	return func(t *SarvamTTS) {
		t.httpClient = client
	}
}

func NewSarvamTTS(apiKey string, voice string, opts ...TTSOption) *SarvamTTS {
	if voice == "" {
		voice = "meera"
	}
	t := &SarvamTTS{
		apiKey:     apiKey,
		voice:      voice,
		baseURL:    "https://api.sarvam.ai/text-to-speech",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *SarvamTTS) Label() string { return "sarvam.TTS" }
func (t *SarvamTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SarvamTTS) SampleRate() int { return 8000 }
func (t *SarvamTTS) NumChannels() int { return 1 }

func (t *SarvamTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := t.baseURL

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
	resp, err := t.httpClient.Do(req)
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

