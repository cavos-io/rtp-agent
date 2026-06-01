package sarvam

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

const (
	defaultSarvamSTTBaseURL            = "https://api.sarvam.ai/speech-to-text"
	defaultSarvamSTTStreamingURL       = "wss://api.sarvam.ai/speech-to-text/ws"
	defaultSarvamSTTTranslateBaseURL   = "https://api.sarvam.ai/speech-to-text-translate"
	defaultSarvamSTTTranslateWSURL     = "wss://api.sarvam.ai/speech-to-text-translate/ws"
	defaultSarvamSTTModel              = "saarika:v2.5"
	defaultSarvamSTTLanguage           = "en-IN"
	defaultSarvamSTTMode               = "transcribe"
	defaultSarvamSTTSampleRate         = 16000
	defaultSarvamTTSBaseURL            = "https://api.sarvam.ai/text-to-speech"
	defaultSarvamTTSWSURL              = "wss://api.sarvam.ai/text-to-speech/ws"
	defaultSarvamTTSModel              = "bulbul:v3"
	defaultSarvamTTSLanguage           = "en-IN"
	defaultSarvamTTSSampleRate         = 22050
	defaultSarvamTTSPace               = 1.0
	defaultSarvamTTSPitch              = 0.0
	defaultSarvamTTSLoudness           = 1.0
	defaultSarvamTTSTemperature        = 0.6
	defaultSarvamTTSOutputAudioBitrate = "128k"
	defaultSarvamTTSMinBufferSize      = 50
	defaultSarvamTTSMaxChunkLength     = 150
	defaultSarvamTTSOutputAudioCodec   = "mp3"
)

var (
	sarvamAllowedModes = map[string]struct{}{
		"transcribe": {},
		"translate":  {},
		"verbatim":   {},
		"translit":   {},
		"codemix":    {},
	}
	sarvamSaarikaV25Languages = map[string]struct{}{
		"unknown": {}, "hi-IN": {}, "bn-IN": {}, "kn-IN": {}, "ml-IN": {}, "mr-IN": {},
		"od-IN": {}, "pa-IN": {}, "ta-IN": {}, "te-IN": {}, "en-IN": {}, "gu-IN": {},
	}
	sarvamSaarasV3Languages = map[string]struct{}{
		"unknown": {}, "hi-IN": {}, "bn-IN": {}, "kn-IN": {}, "ml-IN": {}, "mr-IN": {},
		"od-IN": {}, "pa-IN": {}, "ta-IN": {}, "te-IN": {}, "en-IN": {}, "gu-IN": {},
		"as-IN": {}, "ur-IN": {}, "ne-IN": {}, "kok-IN": {}, "ks-IN": {}, "sd-IN": {},
		"sa-IN": {}, "sat-IN": {}, "mni-IN": {}, "brx-IN": {}, "mai-IN": {}, "doi-IN": {},
	}
	sarvamTTSV3Speakers = map[string]struct{}{
		"shubh": {}, "ritu": {}, "rahul": {}, "pooja": {}, "simran": {}, "kavya": {},
		"amit": {}, "ratan": {}, "rohan": {}, "dev": {}, "ishita": {}, "shreya": {},
		"manan": {}, "sumit": {}, "priya": {}, "aditya": {}, "kabir": {}, "neha": {},
		"varun": {}, "roopa": {}, "aayan": {}, "ashutosh": {}, "advait": {}, "amelia": {},
		"sophia": {}, "suhani": {}, "rupali": {}, "tanya": {}, "shruti": {}, "kavitha": {},
	}
)

type SarvamSTT struct {
	apiKey       string
	baseURL      string
	streamingURL string
	baseURLSet   bool
	streamingSet bool
	model        string
	language     string
	mode         string
	prompt       string
	sampleRate   int
}

type SarvamSTTOption func(*SarvamSTT)

func WithSarvamSTTBaseURL(baseURL string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
			s.baseURLSet = true
		}
	}
}

func WithSarvamSTTStreamingURL(streamingURL string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if streamingURL != "" {
			s.streamingURL = strings.TrimRight(streamingURL, "/")
			s.streamingSet = true
		}
	}
}

func WithSarvamSTTModel(model string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if model != "" {
			s.model = model
			if sarvamSTTUsesTranslateEndpoint(model) {
				if !s.baseURLSet {
					s.baseURL = defaultSarvamSTTTranslateBaseURL
				}
				if !s.streamingSet {
					s.streamingURL = defaultSarvamSTTTranslateWSURL
				}
			} else {
				if !s.baseURLSet {
					s.baseURL = defaultSarvamSTTBaseURL
				}
				if !s.streamingSet {
					s.streamingURL = defaultSarvamSTTStreamingURL
				}
			}
		}
	}
}

func WithSarvamSTTLanguage(language string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSarvamSTTMode(mode string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if mode != "" {
			s.mode = mode
		}
	}
}

func WithSarvamSTTPrompt(prompt string) SarvamSTTOption {
	return func(s *SarvamSTT) {
		s.prompt = prompt
	}
}

func WithSarvamSTTSampleRate(sampleRate int) SarvamSTTOption {
	return func(s *SarvamSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func NewSarvamSTT(apiKey string, opts ...SarvamSTTOption) *SarvamSTT {
	provider, _ := NewSarvamSTTWithError(apiKey, opts...)
	return provider
}

func NewSarvamSTTWithError(apiKey string, opts ...SarvamSTTOption) (*SarvamSTT, error) {
	provider := &SarvamSTT{
		apiKey:       apiKey,
		baseURL:      defaultSarvamSTTBaseURL,
		streamingURL: defaultSarvamSTTStreamingURL,
		model:        defaultSarvamSTTModel,
		language:     defaultSarvamSTTLanguage,
		mode:         defaultSarvamSTTMode,
		sampleRate:   defaultSarvamSTTSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if err := validateSarvamSTTOptions(provider.model, provider.language, provider.mode); err != nil {
		return nil, err
	}
	return provider, nil
}

func (s *SarvamSTT) Label() string { return "sarvam.STT" }
func (s *SarvamSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: true}
}

func (s *SarvamSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("sarvam websocket stt streaming is not implemented")
}

func (s *SarvamSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	var audio bytes.Buffer
	for _, f := range frames {
		audio.Write(f.Data)
	}
	req, err := buildSarvamSTTRecognizeRequest(ctx, s, audio.Bytes(), language)
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
		return nil, fmt.Errorf("sarvam stt error: %s", string(respBody))
	}
	var result sarvamSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return sarvamSTTSpeechEvent(resolveSarvamSTTLanguage(s, language), result), nil
}

func buildSarvamSTTRecognizeRequest(ctx context.Context, s *SarvamSTT, audio []byte, language string) (*http.Request, error) {
	requestLanguage := resolveSarvamSTTLanguage(s, language)
	if err := validateSarvamSTTOptions(s.model, requestLanguage, s.mode); err != nil {
		return nil, err
	}
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
	if requestLanguage != "" {
		if err := writer.WriteField("language_code", requestLanguage); err != nil {
			return nil, err
		}
	}
	if s.model != "" {
		if err := writer.WriteField("model", s.model); err != nil {
			return nil, err
		}
	}
	if sarvamSTTSupportsMode(s.model) {
		if err := writer.WriteField("mode", s.mode); err != nil {
			return nil, err
		}
	}
	if s.prompt != "" && strings.HasPrefix(s.model, "saaras") {
		if err := writer.WriteField("prompt", s.prompt); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("api-subscription-key", s.apiKey)
	req.Header.Set("User-Agent", "LiveKit Agents Sarvam Plugin/Go")
	return req, nil
}

type sarvamSTTResponse struct {
	Transcript          string              `json:"transcript"`
	RequestID           string              `json:"request_id"`
	LanguageCode        string              `json:"language_code"`
	LanguageProbability float64             `json:"language_probability"`
	Timestamps          sarvamSTTTimestamps `json:"timestamps"`
}

type sarvamSTTTimestamps struct {
	StartTimeSeconds []float64 `json:"start_time_seconds"`
	EndTimeSeconds   []float64 `json:"end_time_seconds"`
}

func sarvamSTTSpeechEvent(defaultLanguage string, resp sarvamSTTResponse) *stt.SpeechEvent {
	language := resp.LanguageCode
	if language == "" {
		language = defaultLanguage
	}
	return &stt.SpeechEvent{
		Type:      stt.SpeechEventFinalTranscript,
		RequestID: resp.RequestID,
		Alternatives: []stt.SpeechData{
			{
				Text:       resp.Transcript,
				Language:   language,
				StartTime:  sarvamSTTStartTime(resp.Timestamps),
				EndTime:    sarvamSTTEndTime(resp.Timestamps),
				Confidence: sarvamSTTConfidence(resp.LanguageProbability),
			},
		},
	}
}

func resolveSarvamSTTLanguage(s *SarvamSTT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

func validateSarvamSTTOptions(model, language, mode string) error {
	if err := validateSarvamSTTLanguage(model, language); err != nil {
		return err
	}
	if _, ok := sarvamAllowedModes[mode]; mode != "" && !ok {
		return fmt.Errorf("mode must be one of codemix, transcribe, translate, translit, verbatim")
	}
	if !sarvamSTTSupportsMode(model) && mode != "" && mode != defaultSarvamSTTMode {
		return fmt.Errorf("mode is not supported for model %s", model)
	}
	return nil
}

func validateSarvamSTTLanguage(model, language string) error {
	if language == "" {
		return nil
	}
	allowed := sarvamSaarasV3Languages
	if model == "saarika:v2.5" || model == "saaras:v2.5" {
		allowed = sarvamSaarikaV25Languages
	}
	if _, ok := allowed[language]; !ok {
		return fmt.Errorf("language %s is not supported for model %s", language, model)
	}
	return nil
}

func sarvamSTTSupportsMode(model string) bool {
	return model == "saaras:v3"
}

func sarvamSTTUsesTranslateEndpoint(model string) bool {
	return model == "saaras:v2.5"
}

func sarvamSTTStartTime(timestamps sarvamSTTTimestamps) float64 {
	if len(timestamps.StartTimeSeconds) == 0 {
		return 0
	}
	return timestamps.StartTimeSeconds[0]
}

func sarvamSTTEndTime(timestamps sarvamSTTTimestamps) float64 {
	if len(timestamps.EndTimeSeconds) == 0 {
		return 0
	}
	return timestamps.EndTimeSeconds[len(timestamps.EndTimeSeconds)-1]
}

func sarvamSTTConfidence(probability float64) float64 {
	if probability == 0 {
		return 1.0
	}
	return probability
}

type SarvamTTS struct {
	apiKey                string
	baseURL               string
	wsURL                 string
	voice                 string
	language              string
	model                 string
	sampleRate            int
	pitch                 float64
	pace                  float64
	loudness              float64
	temperature           float64
	outputAudioBitrate    string
	minBufferSize         int
	maxChunkLength        int
	enablePreprocessing   bool
	enableCachedResponses *bool
	outputAudioCodec      string
}

type SarvamTTSOption func(*SarvamTTS)

func WithSarvamTTSBaseURL(baseURL string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSarvamTTSWSURL(wsURL string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if wsURL != "" {
			t.wsURL = strings.TrimRight(wsURL, "/")
		}
	}
}

func WithSarvamTTSModel(model string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if model != "" {
			t.model = model
			if t.voice == "" {
				t.voice = defaultSarvamTTSVoice(model)
			}
		}
	}
}

func WithSarvamTTSVoice(voice string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithSarvamTTSLanguage(language string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithSarvamTTSSampleRate(sampleRate int) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithSarvamTTSTemperature(temperature float64) SarvamTTSOption {
	return func(t *SarvamTTS) {
		t.temperature = temperature
	}
}

func WithSarvamTTSOutputAudioCodec(codec string) SarvamTTSOption {
	return func(t *SarvamTTS) {
		if codec != "" {
			t.outputAudioCodec = codec
		}
	}
}

func NewSarvamTTS(apiKey string, voice string, opts ...SarvamTTSOption) *SarvamTTS {
	provider := &SarvamTTS{
		apiKey:             apiKey,
		baseURL:            defaultSarvamTTSBaseURL,
		wsURL:              defaultSarvamTTSWSURL,
		voice:              voice,
		language:           defaultSarvamTTSLanguage,
		model:              defaultSarvamTTSModel,
		sampleRate:         defaultSarvamTTSSampleRate,
		pitch:              defaultSarvamTTSPitch,
		pace:               defaultSarvamTTSPace,
		loudness:           defaultSarvamTTSLoudness,
		temperature:        defaultSarvamTTSTemperature,
		outputAudioBitrate: defaultSarvamTTSOutputAudioBitrate,
		minBufferSize:      defaultSarvamTTSMinBufferSize,
		maxChunkLength:     defaultSarvamTTSMaxChunkLength,
		outputAudioCodec:   defaultSarvamTTSOutputAudioCodec,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultSarvamTTSVoice(provider.model)
	}
	return provider
}

func (t *SarvamTTS) Label() string { return "sarvam.TTS" }
func (t *SarvamTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *SarvamTTS) SampleRate() int  { return t.sampleRate }
func (t *SarvamTTS) NumChannels() int { return 1 }

func (t *SarvamTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildSarvamTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("sarvam tts error: %s", string(respBody))
	}
	return &sarvamTTSChunkedStream{resp: resp, sampleRate: t.sampleRate}, nil
}

func buildSarvamTTSRequest(ctx context.Context, t *SarvamTTS, text string) (*http.Request, error) {
	payload := map[string]interface{}{
		"target_language_code": t.language,
		"text":                 text,
		"speaker":              t.voice,
		"pace":                 t.pace,
		"speech_sample_rate":   t.sampleRate,
		"model":                t.model,
		"output_audio_bitrate": t.outputAudioBitrate,
		"min_buffer_size":      t.minBufferSize,
		"max_chunk_length":     t.maxChunkLength,
		"output_audio_codec":   t.outputAudioCodec,
	}
	if t.model == "bulbul:v2" {
		payload["pitch"] = t.pitch
		payload["loudness"] = t.loudness
		payload["enable_preprocessing"] = t.enablePreprocessing
		if t.enableCachedResponses != nil {
			payload["enable_cached_responses"] = *t.enableCachedResponses
		}
	}
	if t.model == "bulbul:v3" || t.model == "bulbul:v3-beta" {
		payload["temperature"] = t.temperature
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-subscription-key", t.apiKey)
	req.Header.Set("User-Agent", "LiveKit Agents Sarvam Plugin/Go")
	return req, nil
}

func (t *SarvamTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("sarvam websocket tts streaming is not implemented")
}

type sarvamTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	read       bool
}

func (s *sarvamTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.read {
		return nil, io.EOF
	}
	s.read = true
	var result struct {
		RequestID string   `json:"request_id"`
		Audios    []string `json:"audios"`
	}
	if err := json.NewDecoder(s.resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Audios) == 0 {
		return nil, io.EOF
	}
	data, err := base64.StdEncoding.DecodeString(result.Audios[0])
	if err != nil {
		return nil, err
	}
	return &tts.SynthesizedAudio{
		RequestID: result.RequestID,
		Frame: &model.AudioFrame{
			Data:              data,
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(data) / 2),
		},
	}, nil
}

func (s *sarvamTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func defaultSarvamTTSVoice(model string) string {
	if model == "bulbul:v3" || model == "bulbul:v3-beta" {
		return "shubh"
	}
	return "anushka"
}

func validateSarvamTTSModelSpeaker(model, speaker string) error {
	if model != "bulbul:v3" && model != "bulbul:v3-beta" {
		return nil
	}
	if _, ok := sarvamTTSV3Speakers[strings.ToLower(speaker)]; !ok {
		return fmt.Errorf("speaker %s is not compatible with model %s", speaker, model)
	}
	return nil
}
