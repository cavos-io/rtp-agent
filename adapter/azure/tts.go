package azure

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultAzureTTSVoice        = "en-US-JennyNeural"
	defaultAzureTTSLanguage     = "en-US"
	defaultAzureTTSSampleRate   = 24000
	defaultAzureTTSSampleFormat = "raw-24khz-16bit-mono-pcm"
	azureSpeechEndpointEnv      = "AZURE_SPEECH_ENDPOINT"
)

var azureTTSSampleFormats = map[int]string{
	8000:  "raw-8khz-16bit-mono-pcm",
	16000: "raw-16khz-16bit-mono-pcm",
	22050: "raw-22050hz-16bit-mono-pcm",
	24000: defaultAzureTTSSampleFormat,
	44100: "raw-44100hz-16bit-mono-pcm",
	48000: "raw-48khz-16bit-mono-pcm",
}

type AzureTTS struct {
	apiKey         string
	region         string
	voice          string
	language       string
	sampleRate     int
	speechEndpoint string
	deploymentID   string
	authToken      string
	prosody        AzureTTSProsody
	style          AzureTTSStyle
	lexiconURI     string
	httpClient     *http.Client
}

type AzureTTSProsody struct {
	Rate   string
	Volume string
	Pitch  string
}

type AzureTTSStyle struct {
	Style  string
	Degree float64
}

type AzureTTSOption func(*AzureTTS)

func WithAzureTTSLanguage(language string) AzureTTSOption {
	return func(t *AzureTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithAzureTTSSampleRate(sampleRate int) AzureTTSOption {
	return func(t *AzureTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithAzureTTSProsody(prosody AzureTTSProsody) AzureTTSOption {
	return func(t *AzureTTS) {
		t.prosody = prosody
	}
}

func WithAzureTTSStyle(style AzureTTSStyle) AzureTTSOption {
	return func(t *AzureTTS) {
		t.style = style
	}
}

func WithAzureTTSLexiconURI(lexiconURI string) AzureTTSOption {
	return func(t *AzureTTS) {
		if lexiconURI != "" {
			t.lexiconURI = lexiconURI
		}
	}
}

func WithAzureTTSSpeechEndpoint(speechEndpoint string) AzureTTSOption {
	return func(t *AzureTTS) {
		if speechEndpoint != "" {
			t.speechEndpoint = speechEndpoint
		}
	}
}

func WithAzureTTSDeploymentID(deploymentID string) AzureTTSOption {
	return func(t *AzureTTS) {
		if deploymentID != "" {
			t.deploymentID = deploymentID
		}
	}
}

func WithAzureTTSAuthToken(authToken string) AzureTTSOption {
	return func(t *AzureTTS) {
		if authToken != "" {
			t.authToken = authToken
		}
	}
}

func NewAzureTTS(apiKey string, region string, voice string, languages ...string) (*AzureTTS, error) {
	opts := []AzureTTSOption{}
	if len(languages) > 0 {
		opts = append(opts, WithAzureTTSLanguage(languages[0]))
	}
	return NewAzureTTSWithOptions(apiKey, region, voice, opts...)
}

func NewAzureTTSWithOptions(apiKey string, region string, voice string, opts ...AzureTTSOption) (*AzureTTS, error) {
	if apiKey == "" {
		apiKey = os.Getenv(azureSpeechKeyEnv)
	}
	if region == "" {
		region = os.Getenv(azureSpeechRegionEnv)
	}
	if voice == "" {
		voice = defaultAzureTTSVoice
	}
	provider := &AzureTTS{
		apiKey:         apiKey,
		region:         region,
		voice:          voice,
		language:       defaultAzureTTSLanguage,
		sampleRate:     defaultAzureTTSSampleRate,
		speechEndpoint: os.Getenv(azureSpeechEndpointEnv),
		httpClient:     http.DefaultClient,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.speechEndpoint == "" && !((provider.apiKey != "" && provider.region != "") || (provider.authToken != "" && provider.region != "")) {
		return nil, fmt.Errorf("azure speech config requires AZURE_SPEECH_ENDPOINT or AZURE_SPEECH_KEY and AZURE_SPEECH_REGION or AZURE_SPEECH_AUTH_TOKEN and AZURE_SPEECH_REGION")
	}
	if _, ok := azureTTSSampleFormats[provider.sampleRate]; !ok {
		return nil, fmt.Errorf("azure tts unsupported sample rate: %d", provider.sampleRate)
	}
	if err := validateAzureTTSVoiceControls(provider); err != nil {
		return nil, err
	}
	return provider, nil
}

func (t *AzureTTS) Label() string { return "azure.TTS" }
func (t *AzureTTS) Model() string { return "unknown" }
func (t *AzureTTS) Provider() string {
	return "Azure TTS"
}
func (t *AzureTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *AzureTTS) SampleRate() int  { return t.sampleRate }
func (t *AzureTTS) NumChannels() int { return 1 }
func (t *AzureTTS) Language() string { return t.language }

func (t *AzureTTS) UpdateOptions(voice string, language string, opts ...AzureTTSOption) error {
	next := *t
	if voice != "" {
		next.voice = voice
	}
	if language != "" {
		next.language = language
	}
	for _, opt := range opts {
		opt(&next)
	}
	if err := validateAzureTTSVoiceControls(&next); err != nil {
		return err
	}
	*t = next
	return nil
}

func (t *AzureTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildAzureTTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}

	client := t.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, llm.NewAPIStatusError("Azure TTS request failed", resp.StatusCode, "", string(respBody))
	}

	return &azureTTSChunkedStream{
		body:       resp.Body,
		sampleRate: t.sampleRate,
	}, nil
}

func buildAzureTTSRequest(ctx context.Context, t *AzureTTS, text string) (*http.Request, error) {
	endpointURL, err := azureTTSEndpointURL(t)
	if err != nil {
		return nil, err
	}
	language := t.language
	if language == "" {
		language = defaultAzureTTSLanguage
	}
	ssml := buildAzureTTSSSML(t, language, text)

	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL, bytes.NewBufferString(ssml))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", azureTTSSampleFormats[t.sampleRate])
	if t.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.authToken)
	} else if t.apiKey != "" {
		req.Header.Set("Ocp-Apim-Subscription-Key", t.apiKey)
	}
	req.Header.Set("User-Agent", "LiveKit Agents")
	return req, nil
}

func azureTTSEndpointURL(t *AzureTTS) (string, error) {
	endpointURL := t.speechEndpoint
	if endpointURL == "" {
		endpointURL = fmt.Sprintf("https://%s.tts.speech.microsoft.com/cognitiveservices/v1", t.region)
	}
	if t.deploymentID == "" {
		return endpointURL, nil
	}
	parsed, err := url.Parse(endpointURL)
	if err != nil {
		return "", fmt.Errorf("azure tts endpoint url: %w", err)
	}
	query := parsed.Query()
	query.Set("deploymentId", t.deploymentID)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func validateAzureTTSVoiceControls(t *AzureTTS) error {
	if t.style.Degree != 0 && (t.style.Degree < 0.1 || t.style.Degree > 2.0) {
		return fmt.Errorf("style degree must be between 0.1 and 2.0")
	}
	if t.prosody.Rate != "" && !azureTTSAllowed(t.prosody.Rate, "x-slow", "slow", "medium", "fast", "x-fast") {
		rate, err := strconv.ParseFloat(t.prosody.Rate, 64)
		if err != nil {
			return fmt.Errorf("prosody rate must be one of 'x-slow', 'slow', 'medium', 'fast', 'x-fast'")
		}
		if rate < 0.5 || rate > 2 {
			return fmt.Errorf("prosody rate must be between 0.5 and 2")
		}
	}
	if t.prosody.Volume != "" && !azureTTSAllowed(t.prosody.Volume, "silent", "x-soft", "soft", "medium", "loud", "x-loud") {
		volume, err := strconv.ParseFloat(t.prosody.Volume, 64)
		if err != nil {
			return fmt.Errorf("prosody volume must be one of 'silent', 'x-soft', 'soft', 'medium', 'loud', 'x-loud'")
		}
		if volume < 0 || volume > 100 {
			return fmt.Errorf("prosody volume must be between 0 and 100")
		}
	}
	if t.prosody.Pitch != "" && !azureTTSAllowed(t.prosody.Pitch, "x-low", "low", "medium", "high", "x-high") {
		return fmt.Errorf("prosody pitch must be one of 'x-low', 'low', 'medium', 'high', 'x-high'")
	}
	return nil
}

func azureTTSAllowed(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func buildAzureTTSSSML(t *AzureTTS, language string, text string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="http://www.w3.org/2001/mstts" xml:lang="%s">`, language))
	b.WriteString(fmt.Sprintf(`<voice name="%s">`, t.voice))
	if t.lexiconURI != "" {
		b.WriteString(fmt.Sprintf(`<lexicon uri="%s"/>`, t.lexiconURI))
	}
	if t.style.Style != "" {
		b.WriteString(fmt.Sprintf(`<mstts:express-as style="%s"`, t.style.Style))
		if t.style.Degree != 0 {
			b.WriteString(fmt.Sprintf(` styledegree="%s"`, strconv.FormatFloat(t.style.Degree, 'f', -1, 64)))
		}
		b.WriteString(">")
	}
	if t.prosody.Rate != "" || t.prosody.Volume != "" || t.prosody.Pitch != "" {
		b.WriteString("<prosody")
		if t.prosody.Rate != "" {
			b.WriteString(fmt.Sprintf(` rate="%s"`, t.prosody.Rate))
		}
		if t.prosody.Volume != "" {
			b.WriteString(fmt.Sprintf(` volume="%s"`, t.prosody.Volume))
		}
		if t.prosody.Pitch != "" {
			b.WriteString(fmt.Sprintf(` pitch="%s"`, t.prosody.Pitch))
		}
		b.WriteString(">")
		b.WriteString(text)
		b.WriteString("</prosody>")
	} else {
		b.WriteString(text)
	}
	if t.style.Style != "" {
		b.WriteString("</mstts:express-as>")
	}
	b.WriteString("</voice></speak>")
	return b.String()
}

func (t *AzureTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming azure tts is not supported")
}

type azureTTSChunkedStream struct {
	body       io.ReadCloser
	sampleRate int
	carry      byte
	hasCarry   bool
}

func (s *azureTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.body == nil {
		return nil, io.EOF
	}
	buf := make([]byte, 4096)
	var start int

	for {
		if s.body == nil {
			return nil, io.EOF
		}
		if s.hasCarry {
			buf[0] = s.carry
			start = 1
		} else {
			start = 0
		}

		n, err := s.body.Read(buf[start:])
		if err != nil && n == 0 {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, llm.NewAPIConnectionError(err.Error())
		}

		total := start + n
		if total%2 != 0 {
			s.carry = buf[total-1]
			s.hasCarry = true
			total--
		} else {
			s.hasCarry = false
		}

		if total > 0 {
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              buf[:total],
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(total / 2),
				},
			}, nil
		}

		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, llm.NewAPIConnectionError(err.Error())
		}
	}
}

func (s *azureTTSChunkedStream) Close() error {
	if s.body == nil {
		return nil
	}
	body := s.body
	s.body = nil
	s.carry = 0
	s.hasCarry = false
	return body.Close()
}
