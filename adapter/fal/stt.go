package fal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
)

type FalSTT struct {
	apiKey     string
	language   string
	task       string
	chunkLevel string
	version    string
}

type FalSTTOption func(*FalSTT)

func WithFalSTTLanguage(language string) FalSTTOption {
	return func(s *FalSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithFalSTTTask(task string) FalSTTOption {
	return func(s *FalSTT) {
		if task != "" {
			s.task = task
		}
	}
}

func WithFalSTTChunkLevel(chunkLevel string) FalSTTOption {
	return func(s *FalSTT) {
		if chunkLevel != "" {
			s.chunkLevel = chunkLevel
		}
	}
}

func WithFalSTTVersion(version string) FalSTTOption {
	return func(s *FalSTT) {
		if version != "" {
			s.version = version
		}
	}
}

func NewFalSTT(apiKey string, opts ...FalSTTOption) *FalSTT {
	provider := &FalSTT{
		apiKey:     resolveFalAPIKey(apiKey),
		language:   "en",
		task:       "transcribe",
		chunkLevel: "segment",
		version:    "3",
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *FalSTT) UpdateOptions(opts ...FalSTTOption) {
	if s == nil {
		return
	}
	candidate := &FalSTT{
		language:   s.language,
		task:       s.task,
		chunkLevel: s.chunkLevel,
		version:    s.version,
	}
	for _, opt := range opts {
		opt(candidate)
	}
	s.language = candidate.language
}

func (s *FalSTT) Label() string { return "fal.STT" }
func (s *FalSTT) Model() string { return "Wizper" }
func (s *FalSTT) Provider() string {
	return "Fal"
}

func (s *FalSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: true, Diarization: false, OfflineRecognize: true}
}

func (s *FalSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("streaming stt not natively supported by standard fal REST API")
}

func (s *FalSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if err := validateFalSTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}
	if language != "" {
		s.language = language
	}

	resolvedLanguage := s.resolveLanguage("")
	req, err := buildFalSTTRequest(ctx, s, falSTTWAVBytes(frames), resolvedLanguage)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("Fal STT request failed: %v", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("Fal STT request failed: %s", string(respBody)))
	}

	var result falSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("Fal STT response decode failed: %v", err))
	}

	return falSTTResponseToEvent(result, resolvedLanguage), nil
}

type falSTTResponse struct {
	Text string `json:"text"`
}

func falSTTResponseToEvent(result falSTTResponse, language string) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{
				Text:       result.Text,
				Language:   language,
				Confidence: stt.DefaultTranscriptConfidence(result.Text),
			},
		},
	}
}

func (s *FalSTT) resolveLanguage(language string) string {
	if language != "" {
		return language
	}
	return s.language
}

func validateFalSTTAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("fal AI API key is required. It should be set with env FAL_KEY")
	}
	return nil
}

func falSTTWAVBytes(frames []*model.AudioFrame) []byte {
	sampleRate := uint32(16000)
	numChannels := uint32(1)
	var data bytes.Buffer
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate > 0 && data.Len() == 0 {
			sampleRate = frame.SampleRate
		}
		if frame.NumChannels > 0 && data.Len() == 0 {
			numChannels = frame.NumChannels
		}
		data.Write(frame.Data)
	}
	pcm := data.Bytes()
	dataSize := uint32(len(pcm))
	blockAlign := numChannels * 2
	byteRate := sampleRate * blockAlign

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
	_ = binary.Write(&wav, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, dataSize)
	wav.Write(pcm)
	return wav.Bytes()
}

func buildFalSTTRequest(ctx context.Context, s *FalSTT, audio []byte, language string) (*http.Request, error) {
	b64 := base64.StdEncoding.EncodeToString(audio)
	resolvedLanguage := s.language
	if language != "" {
		resolvedLanguage = language
	}

	body := map[string]interface{}{
		"audio_url":   fmt.Sprintf("data:audio/x-wav;base64,%s", b64),
		"task":        s.task,
		"language":    resolvedLanguage,
		"chunk_level": s.chunkLevel,
		"version":     s.version,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fal.run/fal-ai/wizper", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Key "+s.apiKey)
	return req, nil
}
