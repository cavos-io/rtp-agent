package gnani

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/model"
)

const (
	defaultSTTLanguage   = "en-IN"
	defaultSTTSampleRate = 16000
)

type STT struct {
	apiKey         string
	baseURL        string
	language       string
	sampleRate     int
	organizationID string
	userID         string
}

type STTOption func(*STT)

func WithSTTBaseURL(baseURL string) STTOption {
	return func(s *STT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
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

func WithSTTSampleRate(sampleRate int) STTOption {
	return func(s *STT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithSTTOrganizationID(organizationID string) STTOption {
	return func(s *STT) {
		s.organizationID = organizationID
	}
}

func WithSTTUserID(userID string) STTOption {
	return func(s *STT) {
		s.userID = userID
	}
}

func NewSTT(apiKey string, opts ...STTOption) *STT {
	provider := &STT{
		apiKey:     apiKey,
		baseURL:    defaultBaseURL,
		language:   defaultSTTLanguage,
		sampleRate: defaultSTTSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *STT) Label() string { return "gnani.STT" }
func (s *STT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("gnani websocket streaming stt not implemented")
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	var audio bytes.Buffer
	for _, frame := range frames {
		audio.Write(frame.Data)
	}

	req, err := buildSTTRequest(ctx, s, audio.Bytes(), language)
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
		return nil, fmt.Errorf("gnani stt error: %s", string(respBody))
	}

	var result gnaniSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return gnaniSpeechEventFromResponse(result, resolveSTTLanguage(s, language))
}

func buildSTTRequest(ctx context.Context, s *STT, audio []byte, language string) (*http.Request, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreatePart(textprotoMIMEHeader(map[string]string{
		"Content-Disposition": `form-data; name="audio_file"; filename="audio.wav"`,
		"Content-Type":        "audio/wav",
	}))
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	if err := writer.WriteField("language_code", resolveSTTLanguage(s, language)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.baseURL, "/")+"/stt/v3", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-API-Key-ID", s.apiKey)
	if s.organizationID != "" {
		req.Header.Set("X-Organization-ID", s.organizationID)
	}
	if s.userID != "" {
		req.Header.Set("X-API-User-ID", s.userID)
	}
	return req, nil
}

type gnaniSTTResponse struct {
	Transcript string `json:"transcript"`
	RequestID  string `json:"request_id"`
}

func gnaniSpeechEventFromResponse(resp gnaniSTTResponse, language string) (*stt.SpeechEvent, error) {
	return &stt.SpeechEvent{
		Type:      stt.SpeechEventFinalTranscript,
		RequestID: resp.RequestID,
		Alternatives: []stt.SpeechData{
			{
				Language:   language,
				Text:       resp.Transcript,
				Confidence: 1.0,
			},
		},
	}, nil
}

func resolveSTTLanguage(s *STT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

func textprotoMIMEHeader(values map[string]string) textproto.MIMEHeader {
	header := make(textproto.MIMEHeader, len(values))
	for key, value := range values {
		header.Set(key, value)
	}
	return header
}
