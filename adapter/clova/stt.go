package clova

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
)

const (
	defaultClovaSTTLanguage        = "en-US"
	defaultClovaSTTThreshold       = 0.5
	defaultClovaSTTInputSampleRate = 16000
	clovaSTTNumChannels            = 1
)

type ClovaSTT struct {
	secret    string
	invokeURL string
	language  string
	threshold float64
}

type ClovaSTTOption func(*ClovaSTT)

func WithClovaSTTLanguage(language string) ClovaSTTOption {
	return func(s *ClovaSTT) {
		if language != "" {
			s.language = normalizeClovaSTTLanguage(language)
		}
	}
}

func WithClovaSTTThreshold(threshold float64) ClovaSTTOption {
	return func(s *ClovaSTT) {
		s.threshold = threshold
	}
}

func NewClovaSTT(secret, invokeURL string, opts ...ClovaSTTOption) *ClovaSTT {
	provider := &ClovaSTT{
		secret:    secret,
		invokeURL: strings.TrimRight(invokeURL, "/"),
		language:  defaultClovaSTTLanguage,
		threshold: defaultClovaSTTThreshold,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *ClovaSTT) Label() string { return "clova.STT" }
func (s *ClovaSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: true, Diarization: false, OfflineRecognize: true}
}

func (s *ClovaSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("clova stt streaming is not supported")
}

func (s *ClovaSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	var audio bytes.Buffer
	for _, f := range frames {
		audio.Write(f.Data)
	}
	req, err := buildClovaSTTRecognizeRequest(ctx, s, audio.Bytes(), language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("clova stt error: %s", string(respBody))
	}
	var result clovaSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return clovaSTTResponseToEvent(s.withLanguage(language), result)
}

func buildClovaSTTRecognizeRequest(ctx context.Context, s *ClovaSTT, audio []byte, language string) (*http.Request, error) {
	provider := s.withLanguage(language)
	params, err := json.Marshal(map[string]string{
		"language":   provider.language,
		"completion": "sync",
	})
	if err != nil {
		return nil, err
	}
	wav := clovaSTTWAVBytes(audio, defaultClovaSTTInputSampleRate, clovaSTTNumChannels)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("params", string(params)); err != nil {
		return nil, err
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="media"; filename="audio.wav"`)
	header.Set("Content-Type", "audio/wav")
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(wav); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.urlBuilder("recognizer/upload"), &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-CLOVASPEECH-API-KEY", provider.secret)
	return req, nil
}

type clovaSTTResponse struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	Error      any     `json:"error"`
}

func clovaSTTResponseToEvent(s *ClovaSTT, resp clovaSTTResponse) (*stt.SpeechEvent, error) {
	if resp.Error != nil || resp.Text == "" {
		return nil, fmt.Errorf("unexpected clova stt response: %+v", resp)
	}
	if resp.Confidence < s.threshold {
		return nil, fmt.Errorf("confidence %.3f is below threshold %.3f", resp.Confidence, s.threshold)
	}
	return &stt.SpeechEvent{
		Type: stt.SpeechEventInterimTranscript,
		Alternatives: []stt.SpeechData{{
			Text:       resp.Text,
			Language:   s.language,
			Confidence: resp.Confidence,
		}},
	}, nil
}

func (s *ClovaSTT) urlBuilder(processMethod string) string {
	return strings.TrimRight(s.invokeURL, "/") + "/" + processMethod
}

func (s *ClovaSTT) withLanguage(language string) *ClovaSTT {
	if language == "" {
		return s
	}
	clone := *s
	clone.language = normalizeClovaSTTLanguage(language)
	return &clone
}

func normalizeClovaSTTLanguage(language string) string {
	switch language {
	case "en":
		return "en-US"
	case "zh-CN":
		return "zh-cn"
	case "zh-TW":
		return "zh-tw"
	default:
		return language
	}
}

func clovaSTTWAVBytes(audio []byte, sampleRate int, numChannels int) []byte {
	var buf bytes.Buffer
	dataSize := uint32(len(audio))
	byteRate := uint32(sampleRate * numChannels * 2)
	blockAlign := uint16(numChannels * 2)

	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36)+dataSize)
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, byteRate)
	_ = binary.Write(&buf, binary.LittleEndian, blockAlign)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, dataSize)
	buf.Write(audio)
	return buf.Bytes()
}
