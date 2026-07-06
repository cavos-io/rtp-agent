package spitch

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultSpitchBaseURL      = "https://api.spitch.ai"
	defaultSpitchVoice        = "lina"
	defaultSpitchLanguage     = "en"
	defaultSpitchOutputFormat = "mp3"
	defaultSpitchSampleRate   = 24000
)

type SpitchTTS struct {
	apiKey       string
	baseURL      string
	voice        string
	language     string
	outputFormat string
	sampleRate   int
}

type SpitchTTSOption func(*SpitchTTS)

func WithSpitchTTSBaseURL(baseURL string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSpitchTTSVoice(voice string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithSpitchTTSLanguage(language string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithSpitchTTSOutputFormat(outputFormat string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if outputFormat != "" {
			t.outputFormat = outputFormat
		}
	}
}

func WithSpitchTTSSampleRate(sampleRate int) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func NewSpitchTTS(apiKey string, voice string, opts ...SpitchTTSOption) *SpitchTTS {
	provider := &SpitchTTS{
		apiKey:       resolveSpitchAPIKey(apiKey),
		baseURL:      defaultSpitchBaseURL,
		voice:        voice,
		language:     defaultSpitchLanguage,
		outputFormat: defaultSpitchOutputFormat,
		sampleRate:   defaultSpitchSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultSpitchVoice
	}
	return provider
}

func (t *SpitchTTS) Label() string { return "spitch.TTS" }
func (t *SpitchTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SpitchTTS) SampleRate() int  { return t.sampleRate }
func (t *SpitchTTS) NumChannels() int { return 1 }
func (t *SpitchTTS) Model() string    { return "unknown" }
func (t *SpitchTTS) Provider() string { return "Spitch" }

func (t *SpitchTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	return &spitchTTSChunkedStream{
		ctx:          ctx,
		text:         text,
		apiKey:       t.apiKey,
		baseURL:      t.baseURL,
		voice:        t.voice,
		language:     t.language,
		outputFormat: t.outputFormat,
		sampleRate:   t.sampleRate,
	}, nil
}

func buildSpitchTTSRequest(ctx context.Context, t *SpitchTTS, text string) (*http.Request, error) {
	return buildSpitchTTSRequestFromOptions(ctx, spitchTTSRequestOptions{
		text:         text,
		apiKey:       t.apiKey,
		baseURL:      t.baseURL,
		voice:        t.voice,
		language:     t.language,
		outputFormat: t.outputFormat,
	})
}

type spitchTTSRequestOptions struct {
	text         string
	apiKey       string
	baseURL      string
	voice        string
	language     string
	outputFormat string
}

func buildSpitchTTSRequestFromOptions(ctx context.Context, opts spitchTTSRequestOptions) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":     opts.text,
		"language": opts.language,
		"voice":    opts.voice,
		"format":   opts.outputFormat,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(opts.baseURL, "/")+"/tts/v1/synthesize", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.apiKey)
	return req, nil
}

func (t *SpitchTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("spitch streaming tts not natively supported by basic rest api")
}

type spitchTTSChunkedStream struct {
	resp         *http.Response
	ctx          context.Context
	text         string
	apiKey       string
	baseURL      string
	voice        string
	language     string
	outputFormat string
	sampleRate   int
	requested    bool
	emitted      bool
	decoder      codecs.AudioStreamDecoder
	started      bool
	hasAudio     bool
	finalSent    bool
	closed       bool
}

func (s *spitchTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.outputFormat == "mp3" {
		return s.nextDecodedMP3()
	}
	if s.emitted {
		if !s.finalSent {
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		return nil, io.EOF
	}
	s.emitted = true

	data, err := io.ReadAll(s.resp.Body)
	if err != nil {
		return nil, spitchTTSReadError(err)
	}
	if len(data) == 0 {
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	frame, err := decodeSpitchWAVPCM16(data)
	if err != nil {
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("Spitch TTS response decode failed: %v", err))
	}

	return &tts.SynthesizedAudio{
		Frame: frame,
	}, nil
}

func (s *spitchTTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.requested || s.text == "" {
		return nil
	}
	s.requested = true
	req, err := buildSpitchTTSRequestFromOptions(s.ctx, spitchTTSRequestOptions{
		text:         s.text,
		apiKey:       s.apiKey,
		baseURL:      s.baseURL,
		voice:        s.voice,
		language:     s.language,
		outputFormat: s.outputFormat,
	})
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return spitchTTSTransportError(err)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("Spitch TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *spitchTTSChunkedStream) nextDecodedMP3() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		s.decoder = codecs.NewMP3AudioStreamDecoder()
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, spitchTTSReadError(err)
		}
		if len(data) == 0 {
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		s.hasAudio = true
		decoder := s.decoder
		go func() {
			decoder.Push(data)
			decoder.EndInput()
		}()
	}
	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			if s.hasAudio && !s.finalSent {
				s.finalSent = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, io.EOF
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("Spitch TTS response decode failed: %v", err))
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func spitchTTSReadError(err error) error {
	msg := fmt.Sprintf("Spitch TTS response read failed: %v", err)
	if spitchTTSIsTimeout(err) {
		return llm.NewAPITimeoutError(msg)
	}
	return llm.NewAPIConnectionError(msg)
}

func spitchTTSTransportError(err error) error {
	msg := err.Error()
	if spitchTTSIsTimeout(err) {
		return llm.NewAPITimeoutError(msg)
	}
	return llm.NewAPIConnectionError(msg)
}

func spitchTTSIsTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout())
}

func (s *spitchTTSChunkedStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.finalSent = true
	if s.decoder != nil {
		_ = s.decoder.Close()
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	return s.resp.Body.Close()
}

func decodeSpitchWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid spitch wav data")
	}

	var (
		audioFormat   uint16
		numChannels   uint16
		sampleRate    uint32
		bitsPerSample uint16
		pcmData       []byte
	)
	for offset := 12; offset+8 <= len(data); {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 || offset+chunkSize > len(data) {
			return nil, fmt.Errorf("invalid spitch wav chunk size")
		}
		chunk := data[offset : offset+chunkSize]
		switch chunkID {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, fmt.Errorf("invalid spitch wav fmt chunk")
			}
			audioFormat = binary.LittleEndian.Uint16(chunk[0:2])
			numChannels = binary.LittleEndian.Uint16(chunk[2:4])
			sampleRate = binary.LittleEndian.Uint32(chunk[4:8])
			bitsPerSample = binary.LittleEndian.Uint16(chunk[14:16])
		case "data":
			pcmData = append([]byte(nil), chunk...)
		}
		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}
	if audioFormat != 1 || bitsPerSample != 16 {
		return nil, fmt.Errorf("unsupported spitch wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
	}
	if sampleRate == 0 || numChannels == 0 {
		return nil, fmt.Errorf("missing spitch wav format metadata")
	}
	if pcmData == nil {
		return nil, fmt.Errorf("missing spitch wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcmData,
		SampleRate:        sampleRate,
		NumChannels:       uint32(numChannels),
		SamplesPerChannel: uint32(len(pcmData) / (int(numChannels) * 2)),
	}, nil
}
