package mistralai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
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

type TTS struct {
	apiKey         string
	baseURL        string
	model          string
	voice          string
	refAudio       string
	responseFormat string
}

type TTSOption func(*TTS)

func WithMistralAITTSBaseURL(baseURL string) TTSOption {
	return func(t *TTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithMistralAITTSModel(model string) TTSOption {
	return func(t *TTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithMistralAITTSVoice(voice string) TTSOption {
	return func(t *TTS) {
		t.voice = voice
	}
}

func WithMistralAITTSRefAudio(refAudio string) TTSOption {
	return func(t *TTS) {
		t.refAudio = refAudio
	}
}

func WithMistralAITTSResponseFormat(responseFormat string) TTSOption {
	return func(t *TTS) {
		if responseFormat != "" {
			t.responseFormat = responseFormat
		}
	}
}

func NewTTS(apiKey string, voice string, opts ...TTSOption) (*TTS, error) {
	if apiKey == "" {
		apiKey = os.Getenv("MISTRAL_API_KEY")
	}
	provider := &TTS{
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
	if provider.apiKey == "" {
		return nil, fmt.Errorf("mistral AI API key is required. Set MISTRAL_API_KEY or pass api_key")
	}
	return provider, nil
}

func (t *TTS) Label() string { return "mistralai.TTS" }
func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *TTS) SampleRate() int  { return mistralAITTSSampleRate }
func (t *TTS) NumChannels() int { return mistralAITTSNumChannels }
func (t *TTS) Model() string    { return t.model }
func (t *TTS) Provider() string { return "MistralAI" }

func (t *TTS) UpdateOptions(opts ...TTSOption) error {
	updated := *t
	voiceUpdated := false
	refAudioUpdated := false
	for _, opt := range opts {
		beforeVoice := updated.voice
		beforeRefAudio := updated.refAudio
		opt(&updated)
		voiceChanged := updated.voice != beforeVoice
		refAudioChanged := updated.refAudio != beforeRefAudio
		if voiceChanged {
			voiceUpdated = true
			updated.refAudio = ""
		}
		if refAudioChanged {
			refAudioUpdated = true
			updated.voice = ""
		}
	}
	if voiceUpdated && refAudioUpdated {
		return fmt.Errorf("only one of voice or ref_audio may be provided")
	}
	if updated.voice == "" && updated.refAudio == "" {
		updated.voice = defaultMistralAITTSVoice
	}
	*t = updated
	return nil
}

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if _, err := buildMistralAITTSRequest(ctx, t, text); err != nil {
		return nil, err
	}
	opts := *t
	return &mistralAITTSChunkedStream{
		ctx:            ctx,
		text:           text,
		opts:           opts,
		responseFormat: t.responseFormat,
	}, nil
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("mistralai streaming tts is exposed through Synthesize")
}

func buildMistralAITTSRequest(ctx context.Context, t *TTS, text string) (*http.Request, error) {
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
	ctx            context.Context
	text           string
	opts           TTS
	scanner        *bufio.Scanner
	responseFormat string
	requested      bool
	done           bool
	jsonRead       bool
	finalSent      bool
}

func (s *mistralAITTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.done {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.reader)
		s.scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			audio, done, err := mistralAITTSAudioFromStreamEvent(strings.TrimSpace(strings.TrimPrefix(line, "data:")), s.responseFormat)
			if err != nil {
				return nil, llm.NewAPIConnectionError(err.Error())
			}
			if done {
				s.done = true
				if audio != nil {
					return audio, nil
				}
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
				return nil, llm.NewAPIConnectionError(err.Error())
			}
			if audio != nil {
				return audio, nil
			}
		}
	}
	if err := s.scanner.Err(); err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	s.done = true
	if !s.finalSent {
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	return nil, io.EOF
}

func (s *mistralAITTSChunkedStream) ensureResponse() error {
	if s.reader != nil || s.requested || s.text == "" {
		return nil
	}
	s.requested = true
	req, err := buildMistralAITTSRequest(s.ctx, &s.opts, s.text)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("MistralAI TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.reader = resp.Body
	s.closer = resp.Body
	return nil
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
		audio, err := decodeMistralAITTSAudioFrame(event.Data.AudioData, responseFormat)
		if err != nil {
			return nil, false, err
		}
		return audio, false, nil
	case "speech.audio.done":
		return &tts.SynthesizedAudio{IsFinal: true}, true, nil
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
	return decodeMistralAITTSAudioFrame(response.AudioData, responseFormat)
}

func decodeMistralAITTSAudioFrame(audioData string, responseFormat string) (*tts.SynthesizedAudio, error) {
	audio, err := decodeMistralAIBase64Audio(audioData)
	if err != nil {
		return nil, err
	}
	if responseFormat == "pcm" {
		return mistralAITTSAudioFrame(mistralAIF32LEToS16LE(audio)), nil
	}
	if responseFormat == "wav" {
		frame, err := decodeMistralAIWAVPCM16(audio)
		if err != nil {
			return nil, err
		}
		return &tts.SynthesizedAudio{Frame: frame}, nil
	}
	if responseFormat == "mp3" {
		return decodeMistralAIMP3Audio(audio)
	}
	return mistralAITTSAudioFrame(audio), nil
}

func decodeMistralAIBase64Audio(data string) ([]byte, error) {
	clean := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case b >= 'A' && b <= 'Z',
			b >= 'a' && b <= 'z',
			b >= '0' && b <= '9',
			b == '+',
			b == '/',
			b == '=':
			clean = append(clean, b)
		}
	}
	return base64.StdEncoding.DecodeString(string(clean))
}

func decodeMistralAIMP3Audio(audio []byte) (*tts.SynthesizedAudio, error) {
	decoder := codecs.NewMP3AudioStreamDecoder()
	defer decoder.Close()
	go func() {
		decoder.Push(audio)
		decoder.EndInput()
	}()
	frame, err := decoder.Next()
	if err != nil {
		return nil, err
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
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

func decodeMistralAIWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid mistralai wav data")
	}
	offset := 12
	var sampleRate uint32
	var channels uint16
	var bitsPerSample uint16
	var pcm []byte
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 || offset+chunkSize > len(data) {
			return nil, fmt.Errorf("invalid mistralai wav chunk size")
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("invalid mistralai wav fmt chunk")
			}
			audioFormat := binary.LittleEndian.Uint16(data[offset : offset+2])
			channels = binary.LittleEndian.Uint16(data[offset+2 : offset+4])
			sampleRate = binary.LittleEndian.Uint32(data[offset+4 : offset+8])
			bitsPerSample = binary.LittleEndian.Uint16(data[offset+14 : offset+16])
			if audioFormat != 1 || bitsPerSample != 16 {
				return nil, fmt.Errorf("unsupported mistralai wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
			}
		case "data":
			pcm = bytes.Clone(data[offset : offset+chunkSize])
		}
		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}
	if sampleRate == 0 || channels == 0 || bitsPerSample == 0 {
		return nil, fmt.Errorf("missing mistralai wav format metadata")
	}
	if pcm == nil {
		return nil, fmt.Errorf("missing mistralai wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcm,
		SampleRate:        sampleRate,
		NumChannels:       uint32(channels),
		SamplesPerChannel: uint32(len(pcm) / int(channels) / 2),
	}, nil
}

// Deprecated: use TTS.
type MistralAITTS = TTS

// Deprecated: use TTSOption.
type MistralAITTSOption = TTSOption

// Deprecated: use NewTTS.
func NewMistralAITTS(apiKey string, voice string, opts ...TTSOption) (*TTS, error) {
	return NewTTS(apiKey, voice, opts...)
}
