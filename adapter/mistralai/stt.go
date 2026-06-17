package mistralai

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
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
)

const (
	defaultMistralAISTTBaseURL    = "https://api.mistral.ai/v1"
	defaultMistralAISTTModel      = "voxtral-mini-latest"
	defaultMistralAISTTSampleRate = 16000
)

type MistralAISTT struct {
	apiKey      string
	baseURL     string
	model       string
	language    string
	contextBias []string
	sampleRate  int
}

type MistralAISTTOption func(*MistralAISTT)

func WithMistralAISTTBaseURL(baseURL string) MistralAISTTOption {
	return func(s *MistralAISTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithMistralAISTTModel(model string) MistralAISTTOption {
	return func(s *MistralAISTT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithMistralAISTTLanguage(language string) MistralAISTTOption {
	return func(s *MistralAISTT) {
		s.language = language
	}
}

func WithMistralAISTTContextBias(contextBias []string) MistralAISTTOption {
	return func(s *MistralAISTT) {
		s.contextBias = contextBias
	}
}

func NewMistralAISTT(apiKey string, opts ...MistralAISTTOption) *MistralAISTT {
	if apiKey == "" {
		apiKey = os.Getenv("MISTRAL_API_KEY")
	}
	provider := &MistralAISTT{
		apiKey:     apiKey,
		baseURL:    defaultMistralAISTTBaseURL,
		model:      defaultMistralAISTTModel,
		sampleRate: defaultMistralAISTTSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *MistralAISTT) Label() string { return "mistralai.STT" }
func (s *MistralAISTT) Model() string { return s.model }
func (s *MistralAISTT) Provider() string {
	return "MistralAI"
}
func (s *MistralAISTT) Capabilities() stt.STTCapabilities {
	realtime := mistralAISTTIsRealtime(s.model)
	return stt.STTCapabilities{
		Streaming:         realtime,
		InterimResults:    realtime,
		Diarization:       false,
		AlignedTranscript: "",
		OfflineRecognize:  !realtime,
	}
}

func (s *MistralAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if mistralAISTTIsRealtime(s.model) {
		return nil, fmt.Errorf("mistralai realtime stt streaming is not implemented")
	}
	return nil, fmt.Errorf("mistralai stt streaming is only available for realtime models")
}

func (s *MistralAISTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if err := validateMistralAISTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	if mistralAISTTIsRealtime(s.model) {
		return nil, fmt.Errorf("mistralai realtime models do not support offline recognize")
	}
	audio := mistralAISTTWAVBytes(frames, uint32(s.sampleRate), 1)
	req, err := buildMistralAISTTRecognizeRequest(ctx, s, audio, language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, llm.NewAPIStatusError("MistralAI STT request failed", resp.StatusCode, "", string(respBody))
	}
	var result mistralAISTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return mistralAISTTSpeechEvent(resolveMistralAISTTLanguage(s, language), result), nil
}

func mistralAISTTWAVBytes(frames []*model.AudioFrame, defaultSampleRate uint32, defaultNumChannels uint32) []byte {
	sampleRate := defaultSampleRate
	numChannels := defaultNumChannels
	var pcm bytes.Buffer
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate != 0 {
			sampleRate = frame.SampleRate
		}
		if frame.NumChannels != 0 {
			numChannels = frame.NumChannels
		}
		pcm.Write(frame.Data)
	}
	if sampleRate == 0 {
		sampleRate = defaultMistralAISTTSampleRate
	}
	if numChannels == 0 {
		numChannels = 1
	}
	data := pcm.Bytes()
	dataSize := uint32(len(data))
	byteRate := sampleRate * numChannels * 2
	blockAlign := numChannels * 2
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
	wav.Write(data)
	return wav.Bytes()
}

func buildMistralAISTTRecognizeRequest(ctx context.Context, s *MistralAISTT, audio []byte, language string) (*http.Request, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="audio.wav"`)
	header.Set("Content-Type", "audio/wav")
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	if err := writer.WriteField("model", s.model); err != nil {
		return nil, err
	}
	if len(s.contextBias) > 0 {
		if err := writer.WriteField("context_bias", strings.Join(s.contextBias, ",")); err != nil {
			return nil, err
		}
	}
	if requestLanguage := resolveMistralAISTTLanguage(s, language); requestLanguage != "" {
		if err := writer.WriteField("language", requestLanguage); err != nil {
			return nil, err
		}
	} else if err := writer.WriteField("timestamp_granularities", "segment"); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.baseURL, "/")+"/audio/transcriptions", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, nil
}

func validateMistralAISTTAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("mistral AI API key is required. Set MISTRAL_API_KEY or pass api_key")
	}
	return nil
}

func resolveMistralAISTTLanguage(s *MistralAISTT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

func mistralAISTTIsRealtime(model string) bool {
	return strings.Contains(model, "realtime")
}

type mistralAISTTResponse struct {
	Text     string                `json:"text"`
	Language string                `json:"language"`
	Segments []mistralAISTTSegment `json:"segments"`
}

type mistralAISTTSegment struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func mistralAISTTSpeechEvent(defaultLanguage string, resp mistralAISTTResponse) *stt.SpeechEvent {
	language := resp.Language
	if language == "" {
		language = defaultLanguage
	}
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{
				Text:       resp.Text,
				Language:   language,
				Confidence: stt.DefaultTranscriptConfidence(resp.Text),
				StartTime:  mistralAISTTStartTime(resp.Segments),
				EndTime:    mistralAISTTEndTime(resp.Segments),
				Words:      mistralAISTTTimedStrings(resp.Segments),
			},
		},
	}
}

func mistralAISTTStartTime(segments []mistralAISTTSegment) float64 {
	if len(segments) == 0 {
		return 0
	}
	return segments[0].Start
}

func mistralAISTTEndTime(segments []mistralAISTTSegment) float64 {
	if len(segments) == 0 {
		return 0
	}
	return segments[len(segments)-1].End
}

func mistralAISTTTimedStrings(segments []mistralAISTTSegment) []stt.TimedString {
	if len(segments) == 0 {
		return nil
	}
	timed := make([]stt.TimedString, 0, len(segments))
	for _, segment := range segments {
		timed = append(timed, stt.TimedString{
			Text:      segment.Text,
			StartTime: segment.Start,
			EndTime:   segment.End,
		})
	}
	return timed
}
