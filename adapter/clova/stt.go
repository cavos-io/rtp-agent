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
	"os"
	"strings"

	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
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
	if secret == "" {
		secret = os.Getenv("CLOVA_STT_SECRET_KEY")
	}
	if invokeURL == "" {
		invokeURL = os.Getenv("CLOVA_STT_INVOKE_URL")
	}
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
func (s *ClovaSTT) Model() string { return "unknown" }
func (s *ClovaSTT) Provider() string {
	return "Clova"
}
func (s *ClovaSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: true, Diarization: false, OfflineRecognize: true}
}

func (s *ClovaSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("clova stt streaming is not supported")
}

func (s *ClovaSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	wav, err := clovaSTTWAVBytesFromFrames(frames)
	if err != nil {
		return nil, err
	}
	req, err := buildClovaSTTRecognizeRequestWithWAV(ctx, s, wav, language)
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
	wav := clovaSTTWAVBytes(audio, defaultClovaSTTInputSampleRate, clovaSTTNumChannels)
	return buildClovaSTTRecognizeRequestWithWAV(ctx, s, wav, language)
}

func buildClovaSTTRecognizeRequestWithWAV(ctx context.Context, s *ClovaSTT, wav []byte, language string) (*http.Request, error) {
	provider := s.withLanguage(language)
	params, err := json.Marshal(map[string]string{
		"language":   provider.language,
		"completion": "sync",
	})
	if err != nil {
		return nil, err
	}
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

func clovaSTTWAVBytesFromFrames(frames []*model.AudioFrame) ([]byte, error) {
	var audio bytes.Buffer
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		mono, err := clovaSTTDownmixToMono(frame)
		if err != nil {
			return nil, err
		}
		resampled, err := coreaudio.ResampleAudioFrame(mono, defaultClovaSTTInputSampleRate)
		if err != nil {
			return nil, err
		}
		if resampled != nil {
			audio.Write(resampled.Data)
		}
	}
	return clovaSTTWAVBytes(audio.Bytes(), defaultClovaSTTInputSampleRate, clovaSTTNumChannels), nil
}

func clovaSTTDownmixToMono(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if frame == nil {
		return frame, nil
	}
	if frame.NumChannels == 0 {
		return nil, fmt.Errorf("cannot downmix clova stt audio with zero channels")
	}
	if frame.NumChannels == 1 {
		return frame, nil
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("cannot downmix non-16-bit PCM clova stt audio")
	}
	channels := int(frame.NumChannels)
	samples := len(frame.Data) / (channels * 2)
	if samples == 0 {
		mono := *frame
		mono.Data = nil
		mono.NumChannels = 1
		mono.SamplesPerChannel = 0
		return &mono, nil
	}
	out := make([]byte, samples*2)
	for sampleIdx := 0; sampleIdx < samples; sampleIdx++ {
		sum := 0
		for ch := 0; ch < channels; ch++ {
			offset := (sampleIdx*channels + ch) * 2
			sum += int(int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2])))
		}
		binary.LittleEndian.PutUint16(out[sampleIdx*2:sampleIdx*2+2], uint16(int16(sum/channels)))
	}
	mono := *frame
	mono.Data = out
	mono.NumChannels = 1
	mono.SamplesPerChannel = uint32(samples)
	return &mono, nil
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
			Text:     resp.Text,
			Language: s.language,
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
