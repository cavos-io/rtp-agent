package groq

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultGroqTTSBaseURL        = "https://api.groq.com/openai/v1"
	defaultGroqTTSModel          = "canopylabs/orpheus-v1-english"
	defaultGroqTTSVoice          = "autumn"
	defaultGroqTTSResponseFormat = "wav"
	defaultGroqTTSSampleRate     = 48000
)

type GroqTTS struct {
	apiKey         string
	baseURL        string
	model          string
	voice          string
	responseFormat string
	sampleRate     int
}

type GroqTTSOption func(*GroqTTS)

func WithGroqTTSBaseURL(baseURL string) GroqTTSOption {
	return func(t *GroqTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithGroqTTSModel(model string) GroqTTSOption {
	return func(t *GroqTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithGroqTTSVoice(voice string) GroqTTSOption {
	return func(t *GroqTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func NewGroqTTS(apiKey string, voice string, opts ...GroqTTSOption) *GroqTTS {
	if apiKey == "" {
		apiKey = os.Getenv("GROQ_API_KEY")
	}
	provider := &GroqTTS{
		apiKey:         apiKey,
		baseURL:        defaultGroqTTSBaseURL,
		model:          defaultGroqTTSModel,
		voice:          voice,
		responseFormat: defaultGroqTTSResponseFormat,
		sampleRate:     defaultGroqTTSSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultGroqTTSVoice
	}
	return provider
}

func (t *GroqTTS) Label() string { return "groq.TTS" }
func (t *GroqTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *GroqTTS) SampleRate() int  { return t.sampleRate }
func (t *GroqTTS) NumChannels() int { return 1 }
func (t *GroqTTS) Model() string    { return t.model }
func (t *GroqTTS) Provider() string { return "Groq" }

func (t *GroqTTS) UpdateOptions(model string, voice string) {
	if model != "" {
		t.model = model
	}
	if voice != "" {
		t.voice = voice
	}
}

func (t *GroqTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateGroqTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	req, err := buildGroqTTSRequest(ctx, t, text)
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
		return nil, llm.NewAPIStatusError("Groq TTS request failed", resp.StatusCode, "", string(respBody))
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "audio") {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("groq tts returned non-audio data: %s", string(respBody))
	}

	return &groqTTSChunkedStream{resp: resp, sampleRate: t.sampleRate}, nil
}

func validateGroqTTSAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("groq API key is required, either as argument or set GROQ_API_KEY environment variable")
	}
	return nil
}

func buildGroqTTSRequest(ctx context.Context, t *GroqTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"model":           t.model,
		"voice":           t.voice,
		"input":           text,
		"response_format": t.responseFormat,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/audio/speech", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *GroqTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("groq streaming tts not natively supported")
}

type groqTTSChunkedStream struct {
	resp             *http.Response
	sampleRate       int
	started          bool
	finalSent        bool
	pendingFinal     bool
	sourceSampleRate uint32
	numChannels      uint16
	remainingData    int
	bytesPerFrame    int
}

func (s *groqTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.finalSent {
		return nil, io.EOF
	}
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}

	if !s.started {
		if err := s.startWAV(); err != nil {
			if err == io.EOF {
				s.finalSent = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, err
		}
		s.started = true
	}
	if s.remainingData == 0 {
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}

	data, err := s.readPCMChunk()
	if err != nil {
		return nil, err
	}
	frame := &model.AudioFrame{
		Data:              data,
		SampleRate:        s.sourceSampleRate,
		NumChannels:       uint32(s.numChannels),
		SamplesPerChannel: uint32(len(data) / s.bytesPerFrame),
	}
	frame = groqDownmixToMono(frame)
	if s.sampleRate > 0 && frame.SampleRate != uint32(s.sampleRate) {
		frame, err = audio.ResampleAudioFrame(frame, uint32(s.sampleRate))
		if err != nil {
			return nil, err
		}
	}
	return &tts.SynthesizedAudio{
		Frame: frame,
	}, nil
}

func groqDownmixToMono(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil || frame.NumChannels <= 1 {
		return frame
	}
	channels := int(frame.NumChannels)
	samples := int(frame.SamplesPerChannel)
	if samples == 0 {
		samples = len(frame.Data) / (channels * 2)
	}
	out := make([]byte, samples*2)
	for sample := 0; sample < samples; sample++ {
		sum := int32(0)
		for channel := 0; channel < channels; channel++ {
			offset := (sample*channels + channel) * 2
			if offset+2 > len(frame.Data) {
				break
			}
			sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2])))
		}
		binary.LittleEndian.PutUint16(out[sample*2:sample*2+2], uint16(int16(sum/int32(channels))))
	}
	mono := *frame
	mono.Data = out
	mono.NumChannels = 1
	mono.SamplesPerChannel = uint32(samples)
	return &mono
}

func (s *groqTTSChunkedStream) startWAV() error {
	header := make([]byte, 12)
	if _, err := io.ReadFull(s.resp.Body, header); err != nil {
		return err
	}
	if string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return fmt.Errorf("invalid groq wav data")
	}

	var (
		audioFormat   uint16
		numChannels   uint16
		sampleRate    uint32
		bitsPerSample uint16
	)
	for {
		chunkHeader := make([]byte, 8)
		if _, err := io.ReadFull(s.resp.Body, chunkHeader); err != nil {
			if err == io.EOF {
				return fmt.Errorf("missing groq wav data chunk")
			}
			return err
		}
		chunkID := string(chunkHeader[:4])
		chunkSize := int(binary.LittleEndian.Uint32(chunkHeader[4:8]))
		if chunkSize < 0 {
			return fmt.Errorf("invalid groq wav chunk size")
		}

		switch chunkID {
		case "fmt ":
			chunk := make([]byte, chunkSize)
			if _, err := io.ReadFull(s.resp.Body, chunk); err != nil {
				return err
			}
			if len(chunk) < 16 {
				return fmt.Errorf("invalid groq wav fmt chunk")
			}
			audioFormat = binary.LittleEndian.Uint16(chunk[0:2])
			numChannels = binary.LittleEndian.Uint16(chunk[2:4])
			sampleRate = binary.LittleEndian.Uint32(chunk[4:8])
			bitsPerSample = binary.LittleEndian.Uint16(chunk[14:16])
			if chunkSize%2 == 1 {
				if err := discardGroqWAVBytes(s.resp.Body, 1); err != nil {
					return err
				}
			}
		case "data":
			if audioFormat != 1 || bitsPerSample != 16 {
				return fmt.Errorf("unsupported groq wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
			}
			if sampleRate == 0 || numChannels == 0 {
				return fmt.Errorf("missing groq wav format metadata")
			}
			bytesPerFrame := int(numChannels) * 2
			if chunkSize%bytesPerFrame != 0 {
				return fmt.Errorf("invalid groq wav data chunk size")
			}
			s.sourceSampleRate = sampleRate
			s.numChannels = numChannels
			s.remainingData = chunkSize
			s.bytesPerFrame = bytesPerFrame
			return nil
		default:
			if err := discardGroqWAVBytes(s.resp.Body, chunkSize); err != nil {
				return err
			}
			if chunkSize%2 == 1 {
				if err := discardGroqWAVBytes(s.resp.Body, 1); err != nil {
					return err
				}
			}
		}
	}
}

func (s *groqTTSChunkedStream) readPCMChunk() ([]byte, error) {
	size := s.remainingData
	if size > 4096 {
		size = 4096
	}
	if rem := size % s.bytesPerFrame; rem != 0 && size > s.bytesPerFrame {
		size -= rem
	}
	buf := make([]byte, size)
	n, err := s.resp.Body.Read(buf)
	if n > 0 {
		if rem := n % s.bytesPerFrame; rem != 0 {
			needed := s.bytesPerFrame - rem
			if n+needed > len(buf) {
				grown := make([]byte, n+needed)
				copy(grown, buf[:n])
				buf = grown
			}
			if _, readErr := io.ReadFull(s.resp.Body, buf[n:n+needed]); readErr != nil {
				return nil, readErr
			}
			n += needed
		}
		s.remainingData -= n
		if s.remainingData == 0 {
			s.pendingFinal = true
		}
		return append([]byte(nil), buf[:n]...), nil
	}
	if err != nil {
		return nil, err
	}
	return nil, io.ErrUnexpectedEOF
}

func discardGroqWAVBytes(r io.Reader, n int) error {
	if n <= 0 {
		return nil
	}
	_, err := io.CopyN(io.Discard, r, int64(n))
	return err
}

func (s *groqTTSChunkedStream) Close() error {
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	return body.Close()
}
