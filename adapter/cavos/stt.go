package cavos

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

const (
	defaultBaseURL     = "http://localhost:8080/v1"
	defaultSTTModel    = "whisper-1"
	defaultSTTLanguage = "id"
)

type STT struct {
	httpClient httpDoer
	baseURL    string
	model      string
	language   string
	prompt     string
}

type STTOption func(*STT)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func NewSTT(opts ...STTOption) *STT {
	provider := &STT{
		httpClient: http.DefaultClient,
		baseURL:    defaultBaseURL,
		model:      defaultSTTModel,
		language:   defaultSTTLanguage,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func WithSTTBaseURL(baseURL string) STTOption {
	return func(s *STT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSTTModel(model string) STTOption {
	return func(s *STT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithSTTLanguage(language string) STTOption {
	return func(s *STT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSTTPrompt(prompt string) STTOption {
	return func(s *STT) {
		s.prompt = prompt
	}
}

func withSTTHTTPClient(client http.RoundTripper) STTOption {
	return func(s *STT) {
		if client != nil {
			s.httpClient = &http.Client{Transport: client}
		}
	}
}

func (s *STT) Label() string { return "cavos.STT" }
func (s *STT) Provider() string {
	return "cavos"
}
func (s *STT) Model() string { return s.model }
func (s *STT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{OfflineRecognize: true}
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("cavos streaming stt is not natively supported")
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	req, err := buildSTTRequest(ctx, s, frames, language)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("cavos stt error: %s", strings.TrimSpace(string(body)))
	}
	var result struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	data := stt.SpeechData{Text: result.Text, Language: result.Language}
	if len(result.Segments) > 0 {
		data.StartTime = result.Segments[0].Start
		data.EndTime = result.Segments[len(result.Segments)-1].End
	}
	return &stt.SpeechEvent{
		Type:         stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{data},
	}, nil
}

func buildSTTRequest(ctx context.Context, s *STT, frames []*model.AudioFrame, language string) (*http.Request, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", s.model); err != nil {
		return nil, err
	}
	requestLanguage := s.language
	if language != "" {
		requestLanguage = language
	}
	if requestLanguage != "" {
		if err := writer.WriteField("language", requestLanguage); err != nil {
			return nil, err
		}
	}
	if s.prompt != "" {
		if err := writer.WriteField("prompt", s.prompt); err != nil {
			return nil, err
		}
	}
	file, err := writer.CreateFormFile("file", "file.wav")
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(wavBytes(frames)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.baseURL, "/")+"/audio/transcriptions", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, nil
}

func wavBytes(frames []*model.AudioFrame) []byte {
	var pcm bytes.Buffer
	sampleRate := uint32(16000)
	numChannels := uint32(1)
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate > 0 && sampleRate == 16000 {
			sampleRate = frame.SampleRate
		}
		if frame.NumChannels > 0 && numChannels == 1 {
			numChannels = frame.NumChannels
		}
		pcm.Write(frame.Data)
	}
	data := pcm.Bytes()
	dataSize := uint32(len(data))
	blockAlign := uint16(numChannels * 2)
	byteRate := sampleRate * numChannels * 2
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36)+dataSize)
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(&wav, binary.LittleEndian, sampleRate)
	_ = binary.Write(&wav, binary.LittleEndian, byteRate)
	_ = binary.Write(&wav, binary.LittleEndian, blockAlign)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, dataSize)
	wav.Write(data)
	return wav.Bytes()
}
