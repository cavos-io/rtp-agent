package hume

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
	"net/http"
	"os"
	"strings"
	"unicode"

	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultHumeBaseURL       = "https://api.hume.ai"
	defaultHumeModelVersion  = "1"
	defaultHumeVoiceName     = "Male English Actor"
	defaultHumeVoiceProvider = "HUME_AI"
	defaultHumeAudioFormat   = "mp3"
	defaultHumeSampleRate    = 48000
)

type TTS struct {
	apiKey          string
	baseURL         string
	modelVersion    string
	voiceID         string
	voiceName       string
	voiceProvider   string
	description     string
	speed           *float64
	trailingSilence *float64
	instantMode     bool
	audioFormat     string
	sampleRate      int
	contextID       string
	context         []HumeTTSUtterance
}

type HumeTTSVoice struct {
	ID       string
	Name     string
	Provider string
}

type HumeTTSUtterance struct {
	Text            string
	Description     string
	Speed           *float64
	Voice           *HumeTTSVoice
	TrailingSilence *float64
}

type TTSOption func(*TTS)

func WithHumeTTSBaseURL(baseURL string) TTSOption {
	return func(t *TTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithHumeTTSModelVersion(modelVersion string) TTSOption {
	return func(t *TTS) {
		if modelVersion != "" {
			t.modelVersion = modelVersion
		}
	}
}

func WithHumeTTSVoiceName(name string, provider string) TTSOption {
	return func(t *TTS) {
		t.voiceID = ""
		t.voiceName = name
		t.voiceProvider = provider
		t.instantMode = name != ""
	}
}

func WithHumeTTSVoiceID(id string, provider string) TTSOption {
	return func(t *TTS) {
		t.voiceID = id
		t.voiceName = ""
		t.voiceProvider = provider
		t.instantMode = id != ""
	}
}

func WithHumeTTSDescription(description string) TTSOption {
	return func(t *TTS) {
		t.description = description
	}
}

func WithHumeTTSSpeed(speed float64) TTSOption {
	return func(t *TTS) {
		t.speed = &speed
	}
}

func WithHumeTTSTrailingSilence(trailingSilence float64) TTSOption {
	return func(t *TTS) {
		t.trailingSilence = &trailingSilence
	}
}

func WithHumeTTSInstantMode(enabled bool) TTSOption {
	return func(t *TTS) {
		t.instantMode = enabled
	}
}

func WithHumeTTSAudioFormat(audioFormat string) TTSOption {
	return func(t *TTS) {
		if audioFormat != "" {
			t.audioFormat = audioFormat
		}
	}
}

func WithHumeTTSContextGenerationID(generationID string) TTSOption {
	return func(t *TTS) {
		t.contextID = generationID
		t.context = nil
	}
}

func WithHumeTTSContextUtterances(context []HumeTTSUtterance) TTSOption {
	return func(t *TTS) {
		t.context = context
		t.contextID = ""
	}
}

func NewTTS(apiKey string, model string, opts ...TTSOption) *TTS {
	if apiKey == "" {
		apiKey = os.Getenv("HUME_API_KEY")
	}
	provider := &TTS{
		apiKey:        apiKey,
		baseURL:       defaultHumeBaseURL,
		modelVersion:  defaultHumeModelVersion,
		voiceName:     defaultHumeVoiceName,
		voiceProvider: defaultHumeVoiceProvider,
		instantMode:   true,
		audioFormat:   defaultHumeAudioFormat,
		sampleRate:    defaultHumeSampleRate,
	}
	if model != "" {
		provider.modelVersion = model
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *TTS) Label() string { return "hume.TTS" }
func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *TTS) SampleRate() int  { return t.sampleRate }
func (t *TTS) NumChannels() int { return 1 }
func (t *TTS) Model() string    { return "Octave" }
func (t *TTS) Provider() string { return "Hume" }

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if _, err := buildHumeTTSRequest(ctx, t, text); err != nil {
		return nil, err
	}

	opts := *t
	return &humeTTSChunkedStream{
		ctx:         ctx,
		text:        text,
		opts:        opts,
		audioFormat: t.audioFormat,
		sampleRate:  t.sampleRate,
	}, nil
}

func buildHumeTTSRequest(ctx context.Context, t *TTS, text string) (*http.Request, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("hume API key is required via api_key or HUME_API_KEY env var")
	}
	if err := validateHumeTTSOptions(t); err != nil {
		return nil, err
	}

	utterance := map[string]interface{}{
		"text": text,
	}
	if t.voiceID != "" || t.voiceName != "" {
		utterance["voice"] = humeTTSVoicePayload(HumeTTSVoice{
			ID:       t.voiceID,
			Name:     t.voiceName,
			Provider: t.voiceProvider,
		})
	}
	if t.description != "" {
		utterance["description"] = t.description
	}
	if t.speed != nil {
		utterance["speed"] = *t.speed
	}
	if t.trailingSilence != nil {
		utterance["trailing_silence"] = *t.trailingSilence
	}

	body := map[string]interface{}{
		"utterances":    []map[string]interface{}{utterance},
		"version":       t.modelVersion,
		"strip_headers": true,
		"instant_mode":  t.instantMode,
		"format":        map[string]interface{}{"type": t.audioFormat},
	}
	if t.contextID != "" {
		body["context"] = map[string]interface{}{"generation_id": t.contextID}
	} else if len(t.context) > 0 {
		contextUtterances := make([]map[string]interface{}, 0, len(t.context))
		for _, utterance := range t.context {
			contextUtterances = append(contextUtterances, humeTTSUtterancePayload(utterance))
		}
		body["context"] = map[string]interface{}{"utterances": contextUtterances}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/v0/tts/stream/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hume-Api-Key", t.apiKey)
	req.Header.Set("X-Hume-Client-Name", "livekit")
	req.Header.Set("X-Hume-Client-Version", "go")
	return req, nil
}

func validateHumeTTSOptions(t *TTS) error {
	if t.instantMode && t.voiceID == "" && t.voiceName == "" {
		return fmt.Errorf("hume TTS: instant_mode cannot be enabled without specifying a voice")
	}
	return nil
}

func humeTTSUtterancePayload(utterance HumeTTSUtterance) map[string]interface{} {
	payload := map[string]interface{}{"text": utterance.Text}
	if utterance.Description != "" {
		payload["description"] = utterance.Description
	}
	if utterance.Speed != nil {
		payload["speed"] = *utterance.Speed
	}
	if utterance.TrailingSilence != nil {
		payload["trailing_silence"] = *utterance.TrailingSilence
	}
	if utterance.Voice != nil {
		voice := humeTTSVoicePayload(*utterance.Voice)
		if len(voice) > 0 {
			payload["voice"] = voice
		}
	}
	return payload
}

func humeTTSVoicePayload(voice HumeTTSVoice) map[string]interface{} {
	payload := map[string]interface{}{}
	if voice.ID != "" {
		payload["id"] = voice.ID
	}
	if voice.Name != "" {
		payload["name"] = voice.Name
	}
	if voice.Provider != "" {
		payload["provider"] = voice.Provider
	}
	return payload
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts not natively supported by basic hume rest api")
}

type humeTTSChunkedStream struct {
	resp          *http.Response
	ctx           context.Context
	text          string
	opts          TTS
	audioFormat   string
	sampleRate    int
	scanner       *bufio.Scanner
	decoder       codecs.AudioStreamDecoder
	requested     bool
	decodeStarted bool
	closed        bool
	hasAudio      bool
	finalSent     bool
}

func (s *humeTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.finalSent {
		return nil, io.EOF
	}
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp.Body)
		s.scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	}
	if s.audioFormat == "mp3" {
		return s.nextDecodedMP3()
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}
		audio, err := humeAudioFromJSONLine(line)
		if err != nil {
			return nil, err
		}
		if len(audio) == 0 {
			continue
		}
		if s.audioFormat == "wav" {
			frame, err := decodeHumeWAVPCM16(audio)
			if err != nil {
				return nil, err
			}
			s.hasAudio = true
			return &tts.SynthesizedAudio{Frame: frame}, nil
		}
		s.hasAudio = true
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              audio,
				SampleRate:        uint32(s.sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(len(audio) / 2),
			},
		}, nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	s.finalSent = true
	return &tts.SynthesizedAudio{IsFinal: true}, nil
}

func (s *humeTTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.requested || s.text == "" {
		return nil
	}
	s.requested = true
	req, err := buildHumeTTSRequest(s.ctx, &s.opts, s.text)
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
		return llm.NewAPIStatusError("Hume TTS request failed", resp.StatusCode, "", string(respBody))
	}

	s.resp = resp
	return nil
}

func (s *humeTTSChunkedStream) nextDecodedMP3() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.decodeStarted {
		s.decodeStarted = true
		audio, err := s.collectJSONLineAudio()
		if err != nil {
			return nil, err
		}
		if len(audio) == 0 {
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		s.hasAudio = true
		decoder := codecs.NewMP3AudioStreamDecoder()
		s.decoder = decoder
		go func() {
			decoder.Push(audio)
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
		return nil, err
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func (s *humeTTSChunkedStream) collectJSONLineAudio() ([]byte, error) {
	var audio bytes.Buffer
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}
		chunk, err := humeAudioFromJSONLine(line)
		if err != nil {
			return nil, err
		}
		if len(chunk) == 0 {
			continue
		}
		audio.Write(chunk)
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return audio.Bytes(), nil
}

func (s *humeTTSChunkedStream) Close() error {
	s.closed = true
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	body := s.resp.Body
	decoder := s.decoder
	s.resp = nil
	s.decoder = nil
	if decoder != nil {
		_ = decoder.Close()
	}
	return body.Close()
}

func decodeHumeWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid hume wav data")
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
			return nil, fmt.Errorf("invalid hume wav chunk size")
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("invalid hume wav fmt chunk")
			}
			audioFormat := binary.LittleEndian.Uint16(data[offset : offset+2])
			channels = binary.LittleEndian.Uint16(data[offset+2 : offset+4])
			sampleRate = binary.LittleEndian.Uint32(data[offset+4 : offset+8])
			bitsPerSample = binary.LittleEndian.Uint16(data[offset+14 : offset+16])
			if audioFormat != 1 || bitsPerSample != 16 {
				return nil, fmt.Errorf("unsupported hume wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
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
		return nil, fmt.Errorf("missing hume wav format metadata")
	}
	if pcm == nil {
		return nil, fmt.Errorf("missing hume wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcm,
		SampleRate:        sampleRate,
		NumChannels:       uint32(channels),
		SamplesPerChannel: uint32(len(pcm) / int(channels) / 2),
	}, nil
}

func humeAudioFromJSONLine(line string) ([]byte, error) {
	var data struct {
		Type  string `json:"type"`
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		return nil, err
	}
	if data.Type == "error" {
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("Hume TTS error: %s", line))
	}
	if data.Audio == "" {
		return nil, nil
	}
	return decodeHumeBase64Audio(data.Audio)
}

func decodeHumeBase64Audio(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	filtered := make([]rune, 0, len(value))
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '/' || r == '=' {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(string(filtered))
}

// Deprecated: use TTS.
type HumeTTS = TTS

// Deprecated: use TTSOption.
type HumeTTSOption = TTSOption

// Deprecated: use NewTTS.
func NewHumeTTS(apiKey string, model string, opts ...TTSOption) *TTS {
	return NewTTS(apiKey, model, opts...)
}
