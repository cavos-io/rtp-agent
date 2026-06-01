package mistralai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultMistralAITTSBaseURL        = "https://api.mistral.ai/v1"
	defaultMistralAITTSModel          = "voxtral-mini-tts-latest"
	defaultMistralAITTSVoice          = "en_paul_neutral"
	defaultMistralAITTSResponseFormat = "mp3"
	mistralAITTSSampleRate            = 24000
	mistralAITTSNumChannels           = 1
)

type MistralAITTS struct {
	apiKey         string
	baseURL        string
	model          string
	voice          string
	refAudio       string
	responseFormat string
}

type MistralAITTSOption func(*MistralAITTS)

func WithMistralAITTSBaseURL(baseURL string) MistralAITTSOption {
	return func(t *MistralAITTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithMistralAITTSModel(model string) MistralAITTSOption {
	return func(t *MistralAITTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithMistralAITTSVoice(voice string) MistralAITTSOption {
	return func(t *MistralAITTS) {
		t.voice = voice
	}
}

func WithMistralAITTSRefAudio(refAudio string) MistralAITTSOption {
	return func(t *MistralAITTS) {
		t.refAudio = refAudio
	}
}

func WithMistralAITTSResponseFormat(responseFormat string) MistralAITTSOption {
	return func(t *MistralAITTS) {
		if responseFormat != "" {
			t.responseFormat = responseFormat
		}
	}
}

func NewMistralAITTS(apiKey string, voice string, opts ...MistralAITTSOption) (*MistralAITTS, error) {
	provider := &MistralAITTS{
		apiKey:         apiKey,
		baseURL:        defaultMistralAITTSBaseURL,
		model:          defaultMistralAITTSModel,
		voice:          voice,
		responseFormat: defaultMistralAITTSResponseFormat,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" && provider.refAudio == "" {
		provider.voice = defaultMistralAITTSVoice
	}
	if provider.voice != "" && provider.refAudio != "" {
		return nil, fmt.Errorf("only one of voice or ref_audio may be provided")
	}
	return provider, nil
}

func (t *MistralAITTS) Label() string { return "mistralai.TTS" }
func (t *MistralAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *MistralAITTS) SampleRate() int  { return mistralAITTSSampleRate }
func (t *MistralAITTS) NumChannels() int { return mistralAITTSNumChannels }

func (t *MistralAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildMistralAITTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("mistralai tts error: %s", string(respBody))
	}
	return &mistralAITTSChunkedStream{
		reader:         resp.Body,
		closer:         resp.Body,
		responseFormat: t.responseFormat,
	}, nil
}

func (t *MistralAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("mistralai streaming tts is exposed through Synthesize")
}

func buildMistralAITTSRequest(ctx context.Context, t *MistralAITTS, text string) (*http.Request, error) {
	body := map[string]any{
		"model":           t.model,
		"input":           text,
		"response_format": t.responseFormat,
		"stream":          true,
	}
	if t.refAudio != "" {
		body["ref_audio"] = t.refAudio
	} else {
		voice := t.voice
		if voice == "" {
			voice = defaultMistralAITTSVoice
		}
		body["voice_id"] = voice
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/audio/speech", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}

type mistralAITTSChunkedStream struct {
	reader         io.Reader
	closer         io.Closer
	scanner        *bufio.Scanner
	responseFormat string
	done           bool
	jsonRead       bool
}

func (s *mistralAITTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.done {
		return nil, io.EOF
	}
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.reader)
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			audio, done, err := mistralAITTSAudioFromStreamEvent(strings.TrimSpace(strings.TrimPrefix(line, "data:")), s.responseFormat)
			if err != nil {
				return nil, err
			}
			if done {
				s.done = true
				return nil, io.EOF
			}
			if audio != nil {
				return audio, nil
			}
			continue
		}
		if !s.jsonRead {
			s.jsonRead = true
			audio, err := mistralAITTSAudioFromJSONResponse(line, s.responseFormat)
			if err != nil {
				return nil, err
			}
			if audio != nil {
				return audio, nil
			}
		}
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	s.done = true
	return nil, io.EOF
}

func (s *mistralAITTSChunkedStream) Close() error {
	s.done = true
	if s.closer == nil {
		return nil
	}
	return s.closer.Close()
}

func mistralAITTSAudioFromStreamEvent(data string, responseFormat string) (*tts.SynthesizedAudio, bool, error) {
	var event struct {
		Event string `json:"event"`
		Data  struct {
			AudioData string `json:"audio_data"`
			Usage     struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil, false, err
	}
	switch event.Event {
	case "speech.audio.delta":
		audio, err := decodeMistralAITTSAudio(event.Data.AudioData, responseFormat)
		if err != nil {
			return nil, false, err
		}
		return mistralAITTSAudioFrame(audio), false, nil
	case "speech.audio.done":
		return nil, true, nil
	default:
		return nil, false, nil
	}
}

func mistralAITTSAudioFromJSONResponse(data string, responseFormat string) (*tts.SynthesizedAudio, error) {
	var response struct {
		AudioData string `json:"audio_data"`
	}
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return nil, err
	}
	if response.AudioData == "" {
		return nil, nil
	}
	audio, err := decodeMistralAITTSAudio(response.AudioData, responseFormat)
	if err != nil {
		return nil, err
	}
	return mistralAITTSAudioFrame(audio), nil
}

func decodeMistralAITTSAudio(audioData string, responseFormat string) ([]byte, error) {
	audio, err := base64.StdEncoding.DecodeString(audioData)
	if err != nil {
		return nil, err
	}
	if responseFormat == "pcm" {
		return mistralAIF32LEToS16LE(audio), nil
	}
	return audio, nil
}

func mistralAITTSAudioFrame(audio []byte) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        mistralAITTSSampleRate,
			NumChannels:       mistralAITTSNumChannels,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}

func mistralAIF32LEToS16LE(data []byte) []byte {
	samples := len(data) / 4
	out := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		bits := binary.LittleEndian.Uint32(data[i*4 : i*4+4])
		sample := float64(math.Float32frombits(bits))
		value := int(sample * 32767)
		if value > 32767 {
			value = 32767
		}
		if value < -32768 {
			value = -32768
		}
		binary.LittleEndian.PutUint16(out[i*2:i*2+2], uint16(int16(value)))
	}
	return out
}
