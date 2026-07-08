package cambai

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultCambaiBaseURL      = "https://client.camb.ai/apis"
	defaultCambaiVoiceID      = 147320
	defaultCambaiLanguage     = "en-us"
	defaultCambaiModel        = "mars-flash"
	defaultCambaiOutputFormat = "pcm_s16le"
	cambaiAPIKeyEnv           = "CAMB_API_KEY"
)

var cambaiModelSampleRates = map[string]int{
	"mars-flash":    22050,
	"mars-pro":      48000,
	"mars-instruct": 22050,
}

type CambaiTTS struct {
	apiKey               string
	baseURL              string
	voiceID              int
	language             string
	model                string
	outputFormat         string
	userInstructions     string
	enhanceNamedEntities bool
	sampleRate           int
	explicitSampleRate   bool
	httpClient           *http.Client
}

type CambaiTTSOption func(*CambaiTTS)

type CambaiTTSUpdateOption func(*CambaiTTS)

func WithCambaiTTSBaseURL(baseURL string) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithCambaiTTSVoiceID(voiceID int) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if voiceID > 0 {
			t.voiceID = voiceID
		}
	}
}

func WithCambaiTTSLanguage(language string) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithCambaiTTSModel(model string) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if model != "" {
			t.model = model
			if !t.explicitSampleRate {
				t.sampleRate = cambaiSampleRateForModel(model)
			}
		}
	}
}

func WithCambaiTTSSampleRate(sampleRate int) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
			t.explicitSampleRate = true
		}
	}
}

func WithCambaiTTSOutputFormat(outputFormat string) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if outputFormat != "" {
			t.outputFormat = outputFormat
		}
	}
}

func WithCambaiTTSUserInstructions(userInstructions string) CambaiTTSOption {
	return func(t *CambaiTTS) {
		t.userInstructions = userInstructions
	}
}

func WithCambaiTTSEnhanceNamedEntities(enabled bool) CambaiTTSOption {
	return func(t *CambaiTTS) {
		t.enhanceNamedEntities = enabled
	}
}

func WithCambaiTTSUpdateVoiceID(voiceID int) CambaiTTSUpdateOption {
	return func(t *CambaiTTS) {
		if voiceID > 0 {
			t.voiceID = voiceID
		}
	}
}

func WithCambaiTTSUpdateLanguage(language string) CambaiTTSUpdateOption {
	return func(t *CambaiTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithCambaiTTSUpdateModel(model string) CambaiTTSUpdateOption {
	return func(t *CambaiTTS) {
		if model != "" {
			t.model = model
			t.sampleRate = cambaiSampleRateForModel(model)
		}
	}
}

func WithCambaiTTSUpdateUserInstructions(userInstructions string) CambaiTTSUpdateOption {
	return func(t *CambaiTTS) {
		t.userInstructions = userInstructions
	}
}

func NewCambaiTTS(apiKey string, voice string, opts ...CambaiTTSOption) (*CambaiTTS, error) {
	if apiKey == "" {
		apiKey = os.Getenv(cambaiAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("camb.ai API key must be provided via api_key parameter or CAMB_API_KEY environment variable")
	}
	provider := &CambaiTTS{
		apiKey:       apiKey,
		baseURL:      defaultCambaiBaseURL,
		voiceID:      defaultCambaiVoiceID,
		language:     defaultCambaiLanguage,
		model:        defaultCambaiModel,
		outputFormat: defaultCambaiOutputFormat,
		sampleRate:   cambaiSampleRateForModel(defaultCambaiModel),
		httpClient:   http.DefaultClient,
	}
	if voice != "" {
		if voiceID, err := strconv.Atoi(voice); err == nil && voiceID > 0 {
			provider.voiceID = voiceID
		}
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider, nil
}

func (t *CambaiTTS) Label() string { return "cambai.TTS" }
func (t *CambaiTTS) Model() string { return t.model }
func (t *CambaiTTS) Provider() string {
	return "Camb.ai"
}
func (t *CambaiTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *CambaiTTS) SampleRate() int  { return t.sampleRate }
func (t *CambaiTTS) NumChannels() int { return 1 }

func (t *CambaiTTS) UpdateOptions(opts ...CambaiTTSUpdateOption) {
	for _, opt := range opts {
		opt(t)
	}
}

func (t *CambaiTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	client := t.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	opts := *t

	return &cambaiTTSChunkedStream{
		ctx:          ctx,
		text:         text,
		opts:         opts,
		httpClient:   client,
		sampleRate:   t.sampleRate,
		outputFormat: t.outputFormat,
	}, nil
}

func buildCambaiTTSRequest(ctx context.Context, t *CambaiTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":                                 text,
		"voice_id":                             t.voiceID,
		"language":                             t.language,
		"speech_model":                         t.model,
		"enhance_named_entities_pronunciation": t.enhanceNamedEntities,
		"output_configuration":                 map[string]interface{}{"format": t.outputFormat},
	}
	if t.userInstructions != "" {
		reqBody["user_instructions"] = t.userInstructions
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/tts-stream", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", t.apiKey)
	return req, nil
}

func (t *CambaiTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("cambai streaming tts not natively supported by basic rest api")
}

type cambaiTTSChunkedStream struct {
	resp         *http.Response
	ctx          context.Context
	text         string
	opts         CambaiTTS
	httpClient   *http.Client
	sampleRate   int
	outputFormat string
	requested    bool
	emitted      bool
	pendingFinal bool
	finalSent    bool
	closed       bool
}

func (s *cambaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed || s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.outputFormat == "wav" {
		return s.nextWAV()
	}
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n > 0 {
		if err == io.EOF {
			s.pendingFinal = true
		}
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              buf[:n],
				SampleRate:        uint32(s.sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(n / 2),
			},
		}, nil
	}
	if err != nil {
		if err == io.EOF {
			if !s.finalSent {
				s.finalSent = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, io.EOF
		}
		return nil, err
	}
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *cambaiTTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.requested || s.text == "" {
		return nil
	}
	s.requested = true
	req, err := buildCambaiTTSRequest(s.ctx, &s.opts, s.text)
	if err != nil {
		return err
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("Camb.ai TTS failed", resp.StatusCode, resp.Header.Get("x-request-id"), string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *cambaiTTSChunkedStream) nextWAV() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if s.emitted {
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	s.emitted = true
	data, err := io.ReadAll(s.resp.Body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	frame, err := decodeCambaiWAVPCM16(data)
	if err != nil {
		return nil, err
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func (s *cambaiTTSChunkedStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	return s.resp.Body.Close()
}

func decodeCambaiWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid cambai wav data")
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
			return nil, fmt.Errorf("invalid cambai wav chunk size")
		}
		chunk := data[offset : offset+chunkSize]
		switch chunkID {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, fmt.Errorf("invalid cambai wav fmt chunk")
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
		return nil, fmt.Errorf("unsupported cambai wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
	}
	if sampleRate == 0 || numChannels == 0 {
		return nil, fmt.Errorf("missing cambai wav format metadata")
	}
	if pcmData == nil {
		return nil, fmt.Errorf("missing cambai wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcmData,
		SampleRate:        sampleRate,
		NumChannels:       uint32(numChannels),
		SamplesPerChannel: uint32(len(pcmData)) / (uint32(numChannels) * 2),
	}, nil
}

func cambaiSampleRateForModel(model string) int {
	if sampleRate, ok := cambaiModelSampleRates[model]; ok {
		return sampleRate
	}
	return 22050
}
