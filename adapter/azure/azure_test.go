package azure

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

type azureRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f azureRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAzureSTTFallsBackToSpeechEnvironment(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "env-key")
	t.Setenv(azureSpeechRegionEnv, "eastus")

	provider, err := NewAzureSTT("", "")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v, want nil from env config", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", provider.apiKey)
	}
	if provider.region != "eastus" {
		t.Fatalf("region = %q, want eastus", provider.region)
	}
	if provider.Label() != "azure.STT" {
		t.Fatalf("Label = %q, want azure.STT", provider.Label())
	}
	if provider.Provider() != "Azure STT" {
		t.Fatalf("Provider = %q, want Azure STT", provider.Provider())
	}
	if provider.Model() != "unknown" {
		t.Fatalf("Model = %q, want unknown", provider.Model())
	}
	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || caps.AlignedTranscript != "chunk" || caps.OfflineRecognize {
		t.Fatalf("Capabilities = %+v, want streaming/interim/chunk without offline recognize", caps)
	}
	if caps.OfflineRecognize {
		t.Fatal("OfflineRecognize = true, want false like reference Azure STT")
	}
}

func TestAzureSTTFallsBackToSpeechHostEnvironment(t *testing.T) {
	t.Setenv(azureSpeechHostEnv, "https://speech.container.test")
	t.Setenv(azureSpeechKeyEnv, "")
	t.Setenv(azureSpeechRegionEnv, "")

	provider, err := NewAzureSTT("", "")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v, want nil from speech host config", err)
	}

	if provider.speechHost != "https://speech.container.test" {
		t.Fatalf("speechHost = %q, want speech host from env", provider.speechHost)
	}
	if provider.apiKey != "" {
		t.Fatalf("apiKey = %q, want empty for host-only config", provider.apiKey)
	}
	if provider.region != "" {
		t.Fatalf("region = %q, want empty for host-only config", provider.region)
	}
}

func TestAzureSTTRequiresSpeechConfig(t *testing.T) {
	t.Setenv(azureSpeechHostEnv, "")
	t.Setenv(azureSpeechKeyEnv, "")
	t.Setenv(azureSpeechRegionEnv, "")

	_, err := NewAzureSTT("", "")

	if err == nil || !strings.Contains(err.Error(), "AZURE_SPEECH_KEY") {
		t.Fatalf("NewAzureSTT error = %v, want speech config error", err)
	}
}

func TestAzureSTTRequiresKeyWithSpeechEndpoint(t *testing.T) {
	t.Setenv(azureSpeechHostEnv, "")
	t.Setenv(azureSpeechKeyEnv, "")
	t.Setenv(azureSpeechRegionEnv, "")

	_, err := NewAzureSTT("", "", WithAzureSTTSpeechEndpoint("https://speech.endpoint.test/custom/stt"))

	if err == nil || !strings.Contains(err.Error(), "AZURE_SPEECH_KEY") {
		t.Fatalf("NewAzureSTT error = %v, want endpoint auth config error", err)
	}
}

func TestAzureSTTRecognizeUsesRESTRequestAndMapsDetailedResult(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("method = %q, want POST", req.Method)
			}
			if req.URL.Scheme != "https" || req.URL.Host != "eastus.stt.speech.microsoft.com" || req.URL.Path != "/speech/recognition/conversation/cognitiveservices/v1" {
				t.Fatalf("URL = %q, want Azure STT REST endpoint", req.URL.String())
			}
			if req.URL.Query().Get("language") != "id-ID" {
				t.Fatalf("language query = %q, want id-ID", req.URL.Query().Get("language"))
			}
			if req.URL.Query().Get("format") != "detailed" {
				t.Fatalf("format query = %q, want detailed", req.URL.Query().Get("format"))
			}
			if got := req.Header.Get("Ocp-Apim-Subscription-Key"); got != "key" {
				t.Fatalf("subscription header = %q, want key", got)
			}
			if got := req.Header.Get("Content-Type"); got != "audio/wav; codecs=audio/pcm; samplerate=16000" {
				t.Fatalf("content-type = %q, want Azure WAV PCM content type", got)
			}
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			if !bytes.Contains(body, []byte("RIFF")) || !bytes.Contains(body, []byte("WAVE")) || !bytes.Contains(body, []byte{0x01, 0x02, 0x03, 0x04}) {
				t.Fatalf("request body does not contain WAV payload: %v", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
					"RecognitionStatus":"Success",
					"DisplayText":"fallback text",
					"NBest":[{"Display":"halo final","Confidence":0.87}]
				}`)),
				Request: req,
			}, nil
		}),
	}

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{
		Data:              []byte{0x01, 0x02, 0x03, 0x04},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}}, "id-ID")
	if err != nil {
		t.Fatalf("Recognize error = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %s, want final transcript", event.Type)
	}
	alt := event.Alternatives[0]
	if alt.Text != "halo final" || alt.Confidence != 0.87 || alt.Language != "id-ID" {
		t.Fatalf("alternative = %+v, want NBest text/confidence with requested language", alt)
	}
}

func TestAzureSTTRecognizeUsesConfiguredSpeechHost(t *testing.T) {
	provider, err := NewAzureSTT("", "", WithAzureSTTSpeechHost("https://speech.container.test"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Scheme != "https" || req.URL.Host != "speech.container.test" || req.URL.Path != "/speech/recognition/conversation/cognitiveservices/v1" {
				t.Fatalf("URL = %q, want configured Azure Speech host endpoint", req.URL.String())
			}
			if req.URL.Query().Get("language") != "id-ID" {
				t.Fatalf("language query = %q, want id-ID", req.URL.Query().Get("language"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
					"RecognitionStatus":"Success",
					"DisplayText":"host final"
				}`)),
				Request: req,
			}, nil
		}),
	}

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}}, "id-ID")
	if err != nil {
		t.Fatalf("Recognize error = %v, want nil", err)
	}
	if got := event.Alternatives[0].Text; got != "host final" {
		t.Fatalf("recognized text = %q, want host final", got)
	}
}

func TestAzureSTTRecognizeReportsRecognitionFailure(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"RecognitionStatus":"NoMatch","DisplayText":""}`)),
				Request:    req,
			}, nil
		}),
	}

	_, err = provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en-US")
	if err == nil || !strings.Contains(err.Error(), "NoMatch") {
		t.Fatalf("Recognize error = %v, want recognition status context", err)
	}
}

func TestAzureSTTRecognizePreservesExplicitZeroConfidence(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
					"RecognitionStatus":"Success",
					"DisplayText":"fallback text",
					"NBest":[{"Display":"zero confidence","Confidence":0}]
				}`)),
				Request: req,
			}, nil
		}),
	}

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en-US")
	if err != nil {
		t.Fatalf("Recognize error = %v", err)
	}
	if got := event.Alternatives[0].Confidence; got != 0 {
		t.Fatalf("confidence = %v, want explicit Azure NBest zero confidence", got)
	}
}

func TestAzureSTTRecognizeHTTPErrorIncludesBody(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"bad key"}`)),
				Request:    req,
			}, nil
		}),
	}

	_, err = provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "en-US")
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("Recognize error = %v, want response body context", err)
	}
}

func TestAzureSTTBuildsReferenceStreamURL(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	streamURL := buildAzureSTTStreamURL(provider, "id-ID")
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}

	if parsed.Scheme != "wss" {
		t.Fatalf("stream URL scheme = %q, want wss", parsed.Scheme)
	}
	if parsed.Host != "eastus.stt.speech.microsoft.com" {
		t.Fatalf("stream URL host = %q, want eastus.stt.speech.microsoft.com", parsed.Host)
	}
	if parsed.Path != "/speech/recognition/conversation/cognitiveservices/v1" {
		t.Fatalf("stream URL path = %q, want Azure conversation endpoint", parsed.Path)
	}
	query := parsed.Query()
	if query.Get("language") != "id-ID" {
		t.Fatalf("language query = %q, want id-ID", query.Get("language"))
	}
	if query.Get("format") != "detailed" {
		t.Fatalf("format query = %q, want detailed", query.Get("format"))
	}
}

func TestAzureSTTStreamURLUsesExplicitPunctuationOption(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTExplicitPunctuation(true))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	streamURL := buildAzureSTTStreamURL(provider, "id-ID")
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}

	if got := parsed.Query().Get("punctuation"); got != "explicit" {
		t.Fatalf("punctuation query = %q, want explicit", got)
	}
}

func TestAzureSTTUsesReferenceProfanityOption(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTProfanity("raw"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	parsed, err := url.Parse(buildAzureSTTStreamURL(provider, "id-ID"))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if got := parsed.Query().Get("profanity"); got != "raw" {
		t.Fatalf("stream profanity query = %q, want raw", got)
	}

	req, err := buildAzureSTTRecognizeRequest(context.Background(), provider, nil, "id-ID")
	if err != nil {
		t.Fatalf("build recognize request: %v", err)
	}
	if got := req.URL.Query().Get("profanity"); got != "raw" {
		t.Fatalf("recognize profanity query = %q, want raw", got)
	}
}

func TestAzureSTTBuildsReferenceHostStreamURL(t *testing.T) {
	provider, err := NewAzureSTT("", "", WithAzureSTTSpeechHost("https://speech.container.test"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	streamURL := buildAzureSTTStreamURL(provider, "id-ID")
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}

	if parsed.Scheme != "wss" {
		t.Fatalf("stream URL scheme = %q, want wss for host websocket", parsed.Scheme)
	}
	if parsed.Host != "speech.container.test" {
		t.Fatalf("stream URL host = %q, want speech host", parsed.Host)
	}
	if parsed.Path != "/speech/recognition/conversation/cognitiveservices/v1" {
		t.Fatalf("stream URL path = %q, want Azure conversation endpoint", parsed.Path)
	}
	if parsed.Query().Get("language") != "id-ID" {
		t.Fatalf("language query = %q, want id-ID", parsed.Query().Get("language"))
	}
	if parsed.Query().Get("format") != "detailed" {
		t.Fatalf("format query = %q, want detailed", parsed.Query().Get("format"))
	}
}

func TestAzureSTTBuildsReferenceEndpointStreamURL(t *testing.T) {
	provider, err := NewAzureSTT("key", "", WithAzureSTTSpeechEndpoint("https://speech.endpoint.test/custom/stt"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	streamURL := buildAzureSTTStreamURL(provider, "id-ID")
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}

	if parsed.Scheme != "wss" {
		t.Fatalf("stream URL scheme = %q, want wss", parsed.Scheme)
	}
	if parsed.Host != "speech.endpoint.test" {
		t.Fatalf("stream URL host = %q, want speech endpoint host", parsed.Host)
	}
	if parsed.Path != "/custom/stt" {
		t.Fatalf("stream URL path = %q, want speech endpoint path", parsed.Path)
	}
	if got := parsed.Query().Get("language"); got != "id-ID" {
		t.Fatalf("language query = %q, want id-ID", got)
	}
}

func TestAzureSTTHeadersUseAuthToken(t *testing.T) {
	provider, err := NewAzureSTT("", "eastus", WithAzureSTTAuthToken("token-123"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	headers := buildAzureSTTHeaders(provider, "connection-id")

	if got := headers.Get("Authorization"); got != "Bearer token-123" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
	if got := headers.Get("Ocp-Apim-Subscription-Key"); got != "" {
		t.Fatalf("subscription header = %q, want omitted for auth token", got)
	}
	if got := headers.Get("X-ConnectionId"); got != "connection-id" {
		t.Fatalf("X-ConnectionId = %q, want connection-id", got)
	}
}

func TestAzureSTTStreamUsesConfiguredDefaultLanguage(t *testing.T) {
	provider, err := NewAzureSTT("", "", WithAzureSTTSpeechHost("https://speech.container.test"), WithAzureSTTLanguage("id-ID"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	streamURL := buildAzureSTTStreamURL(provider, provider.streamLanguage(""))
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if got := parsed.Query().Get("language"); got != "id-ID" {
		t.Fatalf("stream language = %q, want configured provider language id-ID", got)
	}

	overrideURL := buildAzureSTTStreamURL(provider, provider.streamLanguage("en-US"))
	overrideParsed, err := url.Parse(overrideURL)
	if err != nil {
		t.Fatalf("parse override stream URL: %v", err)
	}
	if got := overrideParsed.Query().Get("language"); got != "en-US" {
		t.Fatalf("override stream language = %q, want explicit stream language en-US", got)
	}
}

func TestAzureSTTUpdateOptionsMatchesReference(t *testing.T) {
	provider, err := NewAzureSTT("", "", WithAzureSTTSpeechHost("https://speech.container.test"), WithAzureSTTLanguage("en-US"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	provider.UpdateOptions("id-ID")

	streamURL := buildAzureSTTStreamURL(provider, provider.streamLanguage(""))
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if got := parsed.Query().Get("language"); got != "id-ID" {
		t.Fatalf("stream language = %q, want updated provider language id-ID", got)
	}

	req, err := buildAzureSTTRecognizeRequest(context.Background(), provider, []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
	if err != nil {
		t.Fatalf("build recognize request: %v", err)
	}
	if got := req.URL.Query().Get("language"); got != "id-ID" {
		t.Fatalf("recognize language = %q, want updated provider language id-ID", got)
	}
}

func TestAzureSTTExposesReferenceInputSampleRate(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate = %d, want reference sample rate 16000", got)
	}
}

func TestAzureSTTExposesConfiguredInputSampleRate(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTSampleRate(8000))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	if got := provider.InputSampleRate(); got != 8000 {
		t.Fatalf("InputSampleRate = %d, want configured sample rate 8000", got)
	}
}

func TestAzureSTTStreamAudioContentTypeUsesConfiguredSampleRate(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTSampleRate(16000))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}

	got := azureSTTStreamAudioContentType(provider, &model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	})
	if got != "audio/x-wav;codec=audio/pcm;samplerate=16000" {
		t.Fatalf("audio content type = %q, want configured Azure sample rate", got)
	}
}

func TestAzureSTTStreamRejectsReferenceSampleRateChange(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	stream := &azureSTTStream{
		provider: provider,
		ctx:      context.Background(),
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame first rate error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x03, 0x04},
		SampleRate:        8000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err == nil || !strings.Contains(err.Error(), "sample rate") {
		t.Fatalf("PushFrame changed rate error = %v, want sample rate mismatch", err)
	}
}

func TestAzureSTTStreamUsesWebsocketProtocol(t *testing.T) {
	requests := make(chan *http.Request, 1)
	configMessages := make(chan string, 1)
	audioMessages := make(chan []byte, 1)

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.dialWebsocket = azureTestDialer(t, requests, configMessages, audioMessages)

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	req := receiveAzureTestValue(t, requests, "request")
	if req.URL.Path != "/speech/recognition/conversation/cognitiveservices/v1" {
		t.Fatalf("path = %q, want Azure conversation endpoint", req.URL.Path)
	}
	if req.URL.Query().Get("language") != "id-ID" {
		t.Fatalf("language query = %q, want id-ID", req.URL.Query().Get("language"))
	}
	if req.URL.Query().Get("format") != "detailed" {
		t.Fatalf("format query = %q, want detailed", req.URL.Query().Get("format"))
	}
	if got := req.Header.Get("Ocp-Apim-Subscription-Key"); got != "key" {
		t.Fatalf("subscription header = %q, want key", got)
	}
	if got := req.Header.Get("X-ConnectionId"); got == "" {
		t.Fatal("X-ConnectionId header empty")
	}

	configMessage := receiveAzureTestValue(t, configMessages, "speech config")
	configHeaders, configBody := splitAzureTestMessage(t, []byte(configMessage))
	if configHeaders["Path"] != "speech.config" {
		t.Fatalf("speech config Path = %q, want speech.config", configHeaders["Path"])
	}
	var configPayload map[string]any
	if err := json.Unmarshal(configBody, &configPayload); err != nil {
		t.Fatalf("speech config JSON: %v", err)
	}
	if _, ok := configPayload["context"].(map[string]any); !ok {
		t.Fatalf("speech config = %s, want context object", string(configBody))
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02, 0x03, 0x04},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	audioMessage := receiveAzureTestValue(t, audioMessages, "audio")
	audioHeaders, audioPayload := splitAzureTestBinaryMessage(t, audioMessage)
	if audioHeaders["Path"] != "audio" {
		t.Fatalf("audio Path = %q, want audio", audioHeaders["Path"])
	}
	if audioHeaders["Content-Type"] != "audio/x-wav;codec=audio/pcm;samplerate=16000" {
		t.Fatalf("audio Content-Type = %q, want Azure raw PCM stream format", audioHeaders["Content-Type"])
	}
	if !strings.Contains(audioHeaders["Content-Type"], "codec=audio/pcm") {
		t.Fatalf("audio Content-Type = %q, want explicit PCM codec", audioHeaders["Content-Type"])
	}
	if !bytes.Equal(audioPayload, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("audio payload = %v, want pushed PCM", audioPayload)
	}

	start := nextAzureTestEvent(t, stream)
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("start Type = %s, want start_of_speech", start.Type)
	}

	interim := nextAzureTestEvent(t, stream)
	if interim.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("interim Type = %s, want interim_transcript", interim.Type)
	}
	if got := interim.Alternatives[0].Text; got != "halo sementara" {
		t.Fatalf("interim text = %q, want halo sementara", got)
	}
	if got := interim.Alternatives[0].Language; got != "id-ID" {
		t.Fatalf("interim language = %q, want id-ID", got)
	}

	final := nextAzureTestEvent(t, stream)
	if final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("final Type = %s, want final_transcript", final.Type)
	}
	if got := final.Alternatives[0].Text; got != "halo final" {
		t.Fatalf("final text = %q, want halo final", got)
	}
	if got := final.Alternatives[0].Confidence; got != 1.0 {
		t.Fatalf("final confidence = %v, want reference final confidence 1.0", got)
	}
	if got := final.Alternatives[0].Language; got != "id-ID" {
		t.Fatalf("final language = %q, want id-ID", got)
	}

	end := nextAzureTestEvent(t, stream)
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("end Type = %s, want end_of_speech", end.Type)
	}
}

func TestAzureSTTStreamSpeechConfigUsesReferenceSegmentationOptions(t *testing.T) {
	requests := make(chan *http.Request, 1)
	configMessages := make(chan string, 1)
	audioMessages := make(chan []byte, 1)

	provider, err := NewAzureSTT("key", "eastus",
		WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"),
		WithAzureSTTSegmentationSilenceTimeout(450),
		WithAzureSTTSegmentationMaxTime(4000),
		WithAzureSTTSegmentationStrategy("Semantic"),
	)
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.dialWebsocket = azureTestDialer(t, requests, configMessages, audioMessages)

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	receiveAzureTestValue(t, requests, "request")
	configMessage := receiveAzureTestValue(t, configMessages, "speech config")
	_, configBody := splitAzureTestMessage(t, []byte(configMessage))
	var configPayload struct {
		Properties map[string]string `json:"properties"`
	}
	if err := json.Unmarshal(configBody, &configPayload); err != nil {
		t.Fatalf("speech config JSON: %v", err)
	}
	if got := configPayload.Properties["Speech_SegmentationSilenceTimeoutMs"]; got != "450" {
		t.Fatalf("segmentation silence timeout = %q, want 450", got)
	}
	if got := configPayload.Properties["Speech_SegmentationMaximumTimeMs"]; got != "4000" {
		t.Fatalf("segmentation max time = %q, want 4000", got)
	}
	if got := configPayload.Properties["Speech_SegmentationStrategy"]; got != "Semantic" {
		t.Fatalf("segmentation strategy = %q, want Semantic", got)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	receiveAzureTestValue(t, audioMessages, "audio")
}

func TestAzureSTTStreamSpeechConfigUsesReferenceTrueTextOption(t *testing.T) {
	requests := make(chan *http.Request, 1)
	configMessages := make(chan string, 1)
	audioMessages := make(chan []byte, 1)

	provider, err := NewAzureSTT("key", "eastus",
		WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"),
		WithAzureSTTTrueTextPostProcessing(true),
	)
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.dialWebsocket = azureTestDialer(t, requests, configMessages, audioMessages)

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	receiveAzureTestValue(t, requests, "request")
	configMessage := receiveAzureTestValue(t, configMessages, "speech config")
	_, configBody := splitAzureTestMessage(t, []byte(configMessage))
	var configPayload struct {
		Properties map[string]string `json:"properties"`
	}
	if err := json.Unmarshal(configBody, &configPayload); err != nil {
		t.Fatalf("speech config JSON: %v", err)
	}
	if got := configPayload.Properties["SpeechServiceResponse_PostProcessingOption"]; got != "TrueText" {
		t.Fatalf("post-processing option = %q, want TrueText", got)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	receiveAzureTestValue(t, audioMessages, "audio")
}

func TestAzureSTTStreamFinalTranscriptMatchesReferenceResultTextAndConfidence(t *testing.T) {
	event := parseAzureSTTMessage(
		resolveAzureSTTLanguage("id-ID"),
		[]byte("Path: speech.phrase\r\nContent-Type: application/json\r\n\r\n{\"RecognitionStatus\":\"Success\",\"DisplayText\":\"display text\",\"NBest\":[{\"Display\":\"nbest text\",\"Confidence\":0.42}]}"),
	)
	if event == nil {
		t.Fatal("event = nil, want final transcript")
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event Type = %s, want final_transcript", event.Type)
	}
	if got := event.Alternatives[0].Text; got != "display text" {
		t.Fatalf("text = %q, want reference result text", got)
	}
	if got := event.Alternatives[0].Confidence; got != 1.0 {
		t.Fatalf("confidence = %v, want reference final confidence 1.0", got)
	}
}

func TestAzureSTTStreamInterimConfidenceMatchesReference(t *testing.T) {
	event := parseAzureSTTMessage(
		resolveAzureSTTLanguage("id-ID"),
		[]byte("Path: speech.hypothesis\r\nContent-Type: application/json\r\n\r\n{\"Text\":\"halo sementara\"}"),
	)
	if event == nil {
		t.Fatal("event = nil, want interim transcript")
	}
	if event.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event Type = %s, want interim_transcript", event.Type)
	}
	if got := event.Alternatives[0].Text; got != "halo sementara" {
		t.Fatalf("text = %q, want hypothesis text", got)
	}
	if got := event.Alternatives[0].Language; got != "id-ID" {
		t.Fatalf("language = %q, want requested language", got)
	}
	if got := event.Alternatives[0].Confidence; got != 0 {
		t.Fatalf("confidence = %v, want Azure reference interim confidence 0", got)
	}
}

func TestAzureSTTStreamUsesDetectedLanguageWhenPresent(t *testing.T) {
	interim := parseAzureSTTMessage(
		resolveAzureSTTLanguage("en-US"),
		[]byte("Path: speech.hypothesis\r\nContent-Type: application/json\r\n\r\n{\"Text\":\"bonjour\",\"PrimaryLanguage\":{\"Language\":\"fr-FR\",\"Confidence\":\"High\"}}"),
	)
	if interim == nil {
		t.Fatal("interim event = nil, want transcript")
	}
	if got := interim.Alternatives[0].Language; got != "fr-FR" {
		t.Fatalf("interim language = %q, want Azure detected language fr-FR", got)
	}

	final := parseAzureSTTMessage(
		resolveAzureSTTLanguage("en-US"),
		[]byte("Path: speech.phrase\r\nContent-Type: application/json\r\n\r\n{\"RecognitionStatus\":\"Success\",\"DisplayText\":\"hola\",\"PrimaryLanguage\":{\"Language\":\"es-ES\",\"Confidence\":\"High\"}}"),
	)
	if final == nil {
		t.Fatal("final event = nil, want transcript")
	}
	if got := final.Alternatives[0].Language; got != "es-ES" {
		t.Fatalf("final language = %q, want Azure detected language es-ES", got)
	}
}

func TestAzureSTTStreamAppliesReferenceStartTimeOffset(t *testing.T) {
	stream := &azureSTTStream{language: "id-ID"}
	timing, ok := any(stream).(stt.StreamTiming)
	if !ok {
		t.Fatal("stream does not implement stt.StreamTiming")
	}
	timing.SetStartTimeOffset(2.5)

	event := stream.parseMessage([]byte("Path: speech.phrase\r\nContent-Type: application/json\r\n\r\n{\"RecognitionStatus\":\"Success\",\"DisplayText\":\"fallback text\",\"Offset\":1000000,\"Duration\":3000000,\"NBest\":[{\"Display\":\"halo final\",\"Confidence\":0.87}]}"))
	if event == nil {
		t.Fatal("event = nil, want final transcript")
	}
	alt := event.Alternatives[0]
	if alt.StartTime != 2.6 || alt.EndTime != 2.9 {
		t.Fatalf("time range = %v-%v, want 2.6-2.9", alt.StartTime, alt.EndTime)
	}
}

func TestAzureSTTStreamSuppressesDuplicateSpeechBoundaries(t *testing.T) {
	stream := &azureSTTStream{language: "id-ID"}
	startPayload := []byte("Path: speech.startDetected\r\nContent-Type: application/json\r\n\r\n{}")
	endPayload := []byte("Path: speech.endDetected\r\nContent-Type: application/json\r\n\r\n{}")

	start := stream.parseMessage(startPayload)
	if start == nil || start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first start = %#v, want start_of_speech", start)
	}
	if duplicateStart := stream.parseMessage(startPayload); duplicateStart != nil {
		t.Fatalf("duplicate start = %#v, want nil", duplicateStart)
	}

	end := stream.parseMessage(endPayload)
	if end == nil || end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("first end = %#v, want end_of_speech", end)
	}
	if duplicateEnd := stream.parseMessage(endPayload); duplicateEnd != nil {
		t.Fatalf("duplicate end = %#v, want nil", duplicateEnd)
	}
}

func TestAzureSTTStreamParsesReferenceSpeechBoundaryPaths(t *testing.T) {
	stream := &azureSTTStream{language: "id-ID"}

	start := stream.parseMessage([]byte("Path: speech.startDetected\r\nContent-Type: application/json\r\n\r\n{}"))
	if start == nil || start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("speech.startDetected event = %#v, want start_of_speech", start)
	}

	end := stream.parseMessage([]byte("Path: speech.endDetected\r\nContent-Type: application/json\r\n\r\n{}"))
	if end == nil || end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("speech.endDetected event = %#v, want end_of_speech", end)
	}
}

func TestAzureSTTStreamIgnoresReferenceSessionTurnEvents(t *testing.T) {
	stream := &azureSTTStream{language: "id-ID"}

	if event := stream.parseMessage([]byte("Path: turn.start\r\nContent-Type: application/json\r\n\r\n{}")); event != nil {
		t.Fatalf("turn.start event = %#v, want nil session lifecycle event", event)
	}
	if event := stream.parseMessage([]byte("Path: turn.end\r\nContent-Type: application/json\r\n\r\n{}")); event != nil {
		t.Fatalf("turn.end event = %#v, want nil session lifecycle event", event)
	}
}

func TestAzureSTTStreamBuffersAudioUntilSessionStarted(t *testing.T) {
	requests := make(chan *http.Request, 1)
	configMessages := make(chan string, 1)
	audioMessages := make(chan []byte, 1)
	sessionReady := make(chan struct{})

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.dialWebsocket = azureTestSessionReadyDialer(t, requests, configMessages, audioMessages, sessionReady)

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	receiveAzureTestValue(t, requests, "request")
	receiveAzureTestValue(t, configMessages, "speech config")

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- stream.PushFrame(&model.AudioFrame{
			Data:              []byte{0x01, 0x02},
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 1,
		})
	}()

	select {
	case err := <-pushDone:
		if err != nil {
			t.Fatalf("PushFrame error = %v", err)
		}
	case <-time.After(time.Second):
		close(sessionReady)
		select {
		case <-pushDone:
		case <-time.After(time.Second):
		}
		t.Fatal("PushFrame blocked before Azure session start, want buffered audio")
	}

	close(sessionReady)
	audioMessage := receiveAzureTestValue(t, audioMessages, "audio after session start")
	_, audioPayload := splitAzureTestBinaryMessage(t, audioMessage)
	if !bytes.Equal(audioPayload, []byte{0x01, 0x02}) {
		t.Fatalf("audio payload after session start = %v, want buffered PCM", audioPayload)
	}
}

func TestAzureSTTStreamPendingAudioSessionStopReturnsAPIConnectionError(t *testing.T) {
	requests := make(chan *http.Request, defaultAzureSTTRetries+1)
	configMessages := make(chan string, defaultAzureSTTRetries+1)
	serverClosed := make(chan struct{}, defaultAzureSTTRetries+1)
	stopSession := make(chan struct{})

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	closingDialer := azureTestSessionStopDialer(t, requests, configMessages, serverClosed, stopSession)
	var attempts int
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempts++
		return closingDialer(ctx, endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	receiveAzureTestValue(t, requests, "request")
	receiveAzureTestValue(t, configMessages, "speech config")

	err = stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	})
	if err != nil {
		t.Fatalf("PushFrame before session stop error = %v, want buffered audio", err)
	}
	close(stopSession)
	receiveAzureTestSignal(t, serverClosed, "server close")

	_, nextErr := stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(nextErr, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", nextErr, nextErr)
	}

	providerStream, ok := stream.(*azureSTTStream)
	if !ok {
		t.Fatalf("stream = %T, want *azureSTTStream", stream)
	}
	if !providerStream.closed {
		t.Fatal("stream closed = false after session stop, want true")
	}
	terminalAttempts := attempts
	if terminalAttempts != 1 {
		t.Fatalf("dial attempts after terminal session stop = %d, want no reconnect", terminalAttempts)
	}
	_, nextErr = stream.Next()
	if nextErr == nil {
		t.Fatal("second Next error = nil, want shutdown/EOF")
	}

	err = stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x03, 0x04},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second PushFrame error = %v, want io.ErrClosedPipe", err)
	}
	if attempts != terminalAttempts {
		t.Fatalf("dial attempts after closed PushFrame = %d, want unchanged %d", attempts, terminalAttempts)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close after write failure error = %v", err)
	}
}

func TestAzureSTTStreamSessionStopReturnsAPIConnectionError(t *testing.T) {
	requests := make(chan *http.Request, 1)
	configMessages := make(chan string, 1)
	audioMessages := make(chan []byte, 1)
	serverClosed := make(chan struct{}, 1)

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.dialWebsocket = azureTestCloseAfterAudioDialer(t, requests, configMessages, audioMessages, serverClosed)

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	receiveAzureTestValue(t, requests, "request")
	receiveAzureTestValue(t, configMessages, "speech config")
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	receiveAzureTestValue(t, audioMessages, "audio")
	receiveAzureTestSignal(t, serverClosed, "server close")

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestAzureSTTStreamSessionStopBeforeAudioReturnsAPIConnectionError(t *testing.T) {
	requests := make(chan *http.Request, 1)
	configMessages := make(chan string, 1)
	serverClosed := make(chan struct{}, 1)

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	provider.dialWebsocket = azureTestClosingDialer(t, requests, configMessages, serverClosed)

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	receiveAzureTestValue(t, requests, "request")
	receiveAzureTestValue(t, configMessages, "speech config")
	receiveAzureTestSignal(t, serverClosed, "server close")

	errCh := make(chan error, 1)
	go func() {
		_, nextErr := stream.Next()
		errCh <- nextErr
	}()

	select {
	case err := <-errCh:
		var connectionErr *llm.APIConnectionError
		if !errors.As(err, &connectionErr) {
			t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Next timed out, want APIConnectionError after provider session stop")
	}
}

func TestAzureSTTStreamPendingAudioDoesNotReconnectAfterSessionStop(t *testing.T) {
	requests := make(chan *http.Request, 2)
	configMessages := make(chan string, 2)
	serverClosed := make(chan struct{}, defaultAzureSTTRetries+1)
	stopSession := make(chan struct{})

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	closingDialer := azureTestSessionStopDialer(t, requests, configMessages, serverClosed, stopSession)
	var attempts int
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempts++
		return closingDialer(ctx, endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	receiveAzureTestValue(t, requests, "first request")
	receiveAzureTestValue(t, configMessages, "first speech config")

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame before session stop error = %v", err)
	}
	close(stopSession)
	receiveAzureTestSignal(t, serverClosed, "server close")

	_, nextErr := stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(nextErr, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", nextErr, nextErr)
	}
	if attempts != 1 {
		t.Fatalf("dial attempts = %d, want no reconnect before Azure session start", attempts)
	}
}

func TestAzureSTTUpdateOptionsPropagatesLanguageToActiveStream(t *testing.T) {
	requests := make(chan *http.Request, 2)
	configMessages := make(chan string, 2)
	audioMessages := make(chan []byte, 1)
	serverClosed := make(chan struct{}, 1)

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	closingDialer := azureTestClosingDialer(t, requests, configMessages, serverClosed)
	okDialer := azureTestDialer(t, requests, configMessages, audioMessages)
	var attempts int
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempts++
		if attempts == 1 {
			return closingDialer(ctx, endpoint, headers)
		}
		return okDialer(ctx, endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	firstReq := receiveAzureTestValue(t, requests, "first request")
	if got := firstReq.URL.Query().Get("language"); got != "en-US" {
		t.Fatalf("first stream language = %q, want en-US", got)
	}
	receiveAzureTestValue(t, configMessages, "first speech config")
	receiveAzureTestSignal(t, serverClosed, "server close")

	provider.UpdateOptions("id-ID")
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame after update error = %v", err)
	}

	secondReq := receiveAzureTestValue(t, requests, "second request")
	if got := secondReq.URL.Query().Get("language"); got != "id-ID" {
		t.Fatalf("second stream language = %q, want updated language id-ID", got)
	}
	receiveAzureTestValue(t, configMessages, "second speech config")
	receiveAzureTestValue(t, audioMessages, "audio after update reconnect")

	nextAzureTestEvent(t, stream)
	interim := nextAzureTestEvent(t, stream)
	if got := interim.Alternatives[0].Language; got != "id-ID" {
		t.Fatalf("interim language = %q, want updated language id-ID", got)
	}
}

func TestAzureSTTUpdateOptionsPropagatesOptionLanguageToActiveStream(t *testing.T) {
	requests := make(chan *http.Request, 2)
	configMessages := make(chan string, 2)
	audioMessages := make(chan []byte, 1)
	serverClosed := make(chan struct{}, 1)

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	closingDialer := azureTestClosingDialer(t, requests, configMessages, serverClosed)
	okDialer := azureTestDialer(t, requests, configMessages, audioMessages)
	var attempts int
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempts++
		if attempts == 1 {
			return closingDialer(ctx, endpoint, headers)
		}
		return okDialer(ctx, endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	firstReq := receiveAzureTestValue(t, requests, "first request")
	if got := firstReq.URL.Query().Get("language"); got != "en-US" {
		t.Fatalf("first stream language = %q, want en-US", got)
	}
	receiveAzureTestValue(t, configMessages, "first speech config")
	receiveAzureTestSignal(t, serverClosed, "server close")

	provider.UpdateOptions("", WithAzureSTTLanguage("id-ID"))
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame after option update error = %v", err)
	}

	secondReq := receiveAzureTestValue(t, requests, "second request")
	if got := secondReq.URL.Query().Get("language"); got != "id-ID" {
		t.Fatalf("second stream language = %q, want option language id-ID", got)
	}
	receiveAzureTestValue(t, configMessages, "second speech config")
	receiveAzureTestValue(t, audioMessages, "audio after option update reconnect")

	nextAzureTestEvent(t, stream)
	interim := nextAzureTestEvent(t, stream)
	if got := interim.Alternatives[0].Language; got != "id-ID" {
		t.Fatalf("interim language = %q, want option language id-ID", got)
	}
}

func TestAzureSTTUpdateOptionsPropagatesSegmentationToActiveStream(t *testing.T) {
	requests := make(chan *http.Request, 2)
	configMessages := make(chan string, 2)
	audioMessages := make(chan []byte, 1)
	serverClosed := make(chan struct{}, 1)

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	closingDialer := azureTestClosingDialer(t, requests, configMessages, serverClosed)
	okDialer := azureTestDialer(t, requests, configMessages, audioMessages)
	var attempts int
	provider.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		attempts++
		if attempts == 1 {
			return closingDialer(ctx, endpoint, headers)
		}
		return okDialer(ctx, endpoint, headers)
	}

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	receiveAzureTestValue(t, requests, "first request")
	receiveAzureTestValue(t, configMessages, "first speech config")
	receiveAzureTestSignal(t, serverClosed, "server close")

	provider.UpdateOptions("id-ID",
		WithAzureSTTSegmentationSilenceTimeout(650),
		WithAzureSTTSegmentationMaxTime(5000),
		WithAzureSTTSegmentationStrategy("Time"),
	)
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame after update error = %v", err)
	}

	receiveAzureTestValue(t, requests, "second request")
	secondConfig := receiveAzureTestValue(t, configMessages, "second speech config")
	_, configBody := splitAzureTestMessage(t, []byte(secondConfig))
	var configPayload struct {
		Properties map[string]string `json:"properties"`
	}
	if err := json.Unmarshal(configBody, &configPayload); err != nil {
		t.Fatalf("speech config JSON: %v", err)
	}
	if got := configPayload.Properties["Speech_SegmentationSilenceTimeoutMs"]; got != "650" {
		t.Fatalf("updated segmentation silence timeout = %q, want 650", got)
	}
	if got := configPayload.Properties["Speech_SegmentationMaximumTimeMs"]; got != "5000" {
		t.Fatalf("updated segmentation max time = %q, want 5000", got)
	}
	if got := configPayload.Properties["Speech_SegmentationStrategy"]; got != "Time" {
		t.Fatalf("updated segmentation strategy = %q, want Time", got)
	}
	receiveAzureTestValue(t, audioMessages, "audio after update reconnect")
}

func TestAzureTTSDefaultsAndEnvironmentMatchReference(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "env-key")
	t.Setenv(azureSpeechRegionEnv, "westus")

	provider, err := NewAzureTTS("", "", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v, want nil from env config", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", provider.apiKey)
	}
	if provider.region != "westus" {
		t.Fatalf("region = %q, want westus", provider.region)
	}
	if provider.voice != "en-US-JennyNeural" {
		t.Fatalf("voice = %q, want reference default", provider.voice)
	}
	if provider.language != "en-US" {
		t.Fatalf("language = %q, want reference default", provider.language)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sampleRate = %d, want 24000", provider.sampleRate)
	}
	if provider.Label() != "azure.TTS" {
		t.Fatalf("Label = %q, want azure.TTS", provider.Label())
	}
	if provider.Provider() != "Azure TTS" {
		t.Fatalf("Provider = %q, want Azure TTS", provider.Provider())
	}
	if provider.Model() != "unknown" {
		t.Fatalf("Model = %q, want unknown", provider.Model())
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("SampleRate = %d, want 24000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("NumChannels = %d, want 1", provider.NumChannels())
	}
	if provider.Language() != "en-US" {
		t.Fatalf("Language = %q, want en-US", provider.Language())
	}
	if provider.Capabilities().Streaming {
		t.Fatal("Streaming = true, want false for Azure REST TTS")
	}
}

func TestAzureTTSRequiresSpeechConfig(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "")
	t.Setenv(azureSpeechRegionEnv, "")
	t.Setenv(azureSpeechEndpointEnv, "")

	_, err := NewAzureTTS("", "", "")

	if err == nil || !strings.Contains(err.Error(), "AZURE_SPEECH_ENDPOINT") {
		t.Fatalf("NewAzureTTS error = %v, want speech config error", err)
	}
}

func TestAzureTTSBuildsReferenceRequest(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "en-US-AvaNeural")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.URL.String() != "https://eastus.tts.speech.microsoft.com/cognitiveservices/v1" {
		t.Fatalf("URL = %q, want reference endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-Microsoft-OutputFormat"); got != "raw-24khz-16bit-mono-pcm" {
		t.Fatalf("output format = %q, want raw-24khz-16bit-mono-pcm", got)
	}
	if got := req.Header.Get("Ocp-Apim-Subscription-Key"); got != "key" {
		t.Fatalf("subscription header = %q, want key", got)
	}
	if got := req.Header.Get("User-Agent"); got != "LiveKit Agents" {
		t.Fatalf("User-Agent = %q, want LiveKit Agents", got)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), `voice name="en-US-AvaNeural"`) {
		t.Fatalf("SSML = %q, want voice name", string(body))
	}
	if !strings.Contains(string(body), `xmlns="http://www.w3.org/2001/10/synthesis"`) {
		t.Fatalf("SSML = %q, want reference synthesis namespace", string(body))
	}
	if !strings.Contains(string(body), `xmlns:mstts="http://www.w3.org/2001/mstts"`) {
		t.Fatalf("SSML = %q, want reference mstts namespace", string(body))
	}
}

func TestAzureTTSBuildsRequestWithConfiguredLanguage(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "id-ID-GadisNeural", "id-ID")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "halo")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	ssml := string(body)
	if !strings.Contains(ssml, `xml:lang="id-ID"`) {
		t.Fatalf("SSML = %q, want configured language", ssml)
	}
	if !strings.Contains(ssml, `voice name="id-ID-GadisNeural"`) {
		t.Fatalf("SSML = %q, want configured voice", ssml)
	}
}

func TestAzureTTSBuildsRequestWithConfiguredSampleRate(t *testing.T) {
	provider, err := NewAzureTTSWithOptions(
		"key",
		"eastus",
		"en-US-AvaNeural",
		WithAzureTTSSampleRate(16000),
	)
	if err != nil {
		t.Fatalf("NewAzureTTSWithOptions error = %v", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if provider.SampleRate() != 16000 {
		t.Fatalf("SampleRate() = %d, want 16000", provider.SampleRate())
	}
	if got := req.Header.Get("X-Microsoft-OutputFormat"); got != "raw-16khz-16bit-mono-pcm" {
		t.Fatalf("output format = %q, want raw-16khz-16bit-mono-pcm", got)
	}
}

func TestAzureTTSBuildsRequestWithEndpointDeploymentAndAuthToken(t *testing.T) {
	t.Setenv(azureSpeechKeyEnv, "")

	provider, err := NewAzureTTSWithOptions(
		"",
		"eastus",
		"en-US-AvaNeural",
		WithAzureTTSSpeechEndpoint("https://speech.example.test/cognitiveservices/v1"),
		WithAzureTTSDeploymentID("voice-deployment"),
		WithAzureTTSAuthToken("token-123"),
	)
	if err != nil {
		t.Fatalf("NewAzureTTSWithOptions error = %v", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if got, want := req.URL.String(), "https://speech.example.test/cognitiveservices/v1?deploymentId=voice-deployment"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer token-123" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Ocp-Apim-Subscription-Key"); got != "" {
		t.Fatalf("subscription header = %q, want omitted when auth token is configured", got)
	}
}

func TestAzureTTSBuildsRequestWithReferenceSSMLOptions(t *testing.T) {
	provider, err := NewAzureTTSWithOptions(
		"key",
		"eastus",
		"en-US-AvaNeural",
		WithAzureTTSLexiconURI("https://example.com/lexicon.xml"),
		WithAzureTTSStyle(AzureTTSStyle{Style: "cheerful", Degree: 1.5}),
		WithAzureTTSProsody(AzureTTSProsody{Rate: "fast", Volume: "loud", Pitch: "high"}),
	)
	if err != nil {
		t.Fatalf("NewAzureTTSWithOptions error = %v", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	want := `<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="http://www.w3.org/2001/mstts" xml:lang="en-US"><voice name="en-US-AvaNeural"><lexicon uri="https://example.com/lexicon.xml"/><mstts:express-as style="cheerful" styledegree="1.5"><prosody rate="fast" volume="loud" pitch="high">hello</prosody></mstts:express-as></voice></speak>`
	if string(body) != want {
		t.Fatalf("SSML = %q, want %q", string(body), want)
	}
}

func TestAzureTTSRejectsInvalidReferenceVoiceControls(t *testing.T) {
	tests := []struct {
		name string
		opts []AzureTTSOption
		want string
	}{
		{
			name: "style degree",
			opts: []AzureTTSOption{WithAzureTTSStyle(AzureTTSStyle{Style: "cheerful", Degree: 2.5})},
			want: "style degree",
		},
		{
			name: "prosody rate",
			opts: []AzureTTSOption{WithAzureTTSProsody(AzureTTSProsody{Rate: "warp"})},
			want: "prosody rate",
		},
		{
			name: "prosody volume",
			opts: []AzureTTSOption{WithAzureTTSProsody(AzureTTSProsody{Volume: "blast"})},
			want: "prosody volume",
		},
		{
			name: "prosody pitch",
			opts: []AzureTTSOption{WithAzureTTSProsody(AzureTTSProsody{Pitch: "sideways"})},
			want: "prosody pitch",
		},
		{
			name: "numeric prosody rate range",
			opts: []AzureTTSOption{WithAzureTTSProsody(AzureTTSProsody{Rate: "2.5"})},
			want: "prosody rate must be between 0.5 and 2",
		},
		{
			name: "numeric prosody volume range",
			opts: []AzureTTSOption{WithAzureTTSProsody(AzureTTSProsody{Volume: "101"})},
			want: "prosody volume must be between 0 and 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAzureTTSWithOptions("key", "eastus", "en-US-AvaNeural", tt.opts...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewAzureTTSWithOptions error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestAzureTTSAcceptsReferenceNumericProsody(t *testing.T) {
	provider, err := NewAzureTTSWithOptions(
		"key",
		"eastus",
		"en-US-AvaNeural",
		WithAzureTTSProsody(AzureTTSProsody{Rate: "1.5", Volume: "80"}),
	)
	if err != nil {
		t.Fatalf("NewAzureTTSWithOptions error = %v, want nil", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), `<prosody rate="1.5" volume="80">hello</prosody>`) {
		t.Fatalf("SSML = %q, want numeric prosody attrs", string(body))
	}
}

func TestAzureTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	if err := provider.UpdateOptions(
		"id-ID-GadisNeural",
		"id-ID",
		WithAzureTTSLexiconURI("https://example.com/runtime-lexicon.xml"),
		WithAzureTTSStyle(AzureTTSStyle{Style: "customerservice", Degree: 1.2}),
		WithAzureTTSProsody(AzureTTSProsody{Rate: "slow", Volume: "soft", Pitch: "low"}),
	); err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}

	req, err := buildAzureTTSRequest(context.Background(), provider, "halo")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	ssml := string(body)
	if !strings.Contains(ssml, `xml:lang="id-ID"`) {
		t.Fatalf("SSML = %q, want updated language", ssml)
	}
	if !strings.Contains(ssml, `voice name="id-ID-GadisNeural"`) {
		t.Fatalf("SSML = %q, want updated voice", ssml)
	}
	if provider.Language() != "id-ID" {
		t.Fatalf("Language() = %q, want id-ID", provider.Language())
	}
	if !strings.Contains(ssml, `<lexicon uri="https://example.com/runtime-lexicon.xml"/>`) {
		t.Fatalf("SSML = %q, want updated lexicon", ssml)
	}
	if !strings.Contains(ssml, `<mstts:express-as style="customerservice" styledegree="1.2">`) {
		t.Fatalf("SSML = %q, want updated style", ssml)
	}
	if !strings.Contains(ssml, `<prosody rate="slow" volume="soft" pitch="low">halo</prosody>`) {
		t.Fatalf("SSML = %q, want updated prosody", ssml)
	}
}

func TestAzureTTSUpdateOptionsRejectsUnsupportedSampleRate(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	err = provider.UpdateOptions("", "", WithAzureTTSSampleRate(12345))
	if err == nil {
		t.Fatal("UpdateOptions error = nil, want unsupported sample rate error")
	}
	if !strings.Contains(err.Error(), "unsupported sample rate") {
		t.Fatalf("UpdateOptions error = %q, want unsupported sample rate", err)
	}
	if got := provider.SampleRate(); got != 24000 {
		t.Fatalf("sample rate mutated to %d, want original 24000", got)
	}
	req, buildErr := buildAzureTTSRequest(context.Background(), provider, "hello")
	if buildErr != nil {
		t.Fatalf("build request after rejected update: %v", buildErr)
	}
	if got := req.Header.Get("X-Microsoft-OutputFormat"); got != defaultAzureTTSSampleFormat {
		t.Fatalf("output format = %q, want %q", got, defaultAzureTTSSampleFormat)
	}
}

func TestAzureTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &azureTTSChunkedStream{
		body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sampleRate = %d, want 24000", audio.Frame.SampleRate)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestAzureTTSChunkedStreamKeepsFinalReadBytes(t *testing.T) {
	stream := &azureTTSChunkedStream{
		body:       &finalReadBytesCloser{data: []byte{0x01, 0x02}},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02}) {
		t.Fatalf("data = %v, want final read bytes", audio.Frame.Data)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
}

func TestAzureTTSChunkedStreamReadFailureReturnsAPIConnectionError(t *testing.T) {
	stream := &azureTTSChunkedStream{
		body:       errorReadCloser{err: errors.New("socket closed")},
		sampleRate: 24000,
	}

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestAzureTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &countingReadCloser{}
	stream := &azureTTSChunkedStream{
		body:       body,
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1", body.closed)
	}
}

func TestAzureTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	stream := &azureTTSChunkedStream{
		body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
		sampleRate: 24000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if audio, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after Close = (%#v, %v), want io.EOF", audio, err)
	}
}

func TestAzureTTSSynthesizeUsesConfiguredClient(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Ocp-Apim-Subscription-Key") != "key" {
				t.Fatalf("subscription key header = %q, want key", req.Header.Get("Ocp-Apim-Subscription-Key"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sampleRate = %d, want 24000", audio.Frame.SampleRate)
	}
}

func TestAzureTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
}

func TestAzureTTSSynthesizeReturnsAPITimeoutError(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		}),
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Synthesize error = %T %v, want APITimeoutError", err, err)
	}
}

func TestAzureTTSSynthesizeReturnsAPIConnectionError(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: azureRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("dial refused")
		}),
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Synthesize error = %T %v, want APIConnectionError", err, err)
	}
	var timeoutErr *llm.APITimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("Synthesize error = %T %v, want non-timeout APIConnectionError", err, err)
	}
}

type finalReadBytesCloser struct {
	data []byte
	done bool
}

func (r *finalReadBytesCloser) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func (r *finalReadBytesCloser) Close() error {
	return nil
}

type errorReadCloser struct {
	err error
}

func (r errorReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r errorReadCloser) Close() error {
	return nil
}

type countingReadCloser struct {
	closed int
}

func (r *countingReadCloser) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (r *countingReadCloser) Close() error {
	r.closed++
	if r.closed > 1 {
		return fmt.Errorf("closed twice")
	}
	return nil
}

func receiveAzureTestValue[T any](t *testing.T, ch <-chan T, name string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		var zero T
		t.Fatalf("timed out waiting for %s", name)
		return zero
	}
}

func receiveAzureTestSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func splitAzureTestMessage(t *testing.T, payload []byte) (map[string]string, []byte) {
	t.Helper()
	parts := bytes.SplitN(payload, []byte("\r\n\r\n"), 2)
	if len(parts) != 2 {
		t.Fatalf("azure message %q missing header separator", string(payload))
	}
	headers := map[string]string{}
	for _, line := range strings.Split(string(parts[0]), "\r\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			headers[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return headers, parts[1]
}

func splitAzureTestBinaryMessage(t *testing.T, payload []byte) (map[string]string, []byte) {
	t.Helper()
	if len(payload) < 2 {
		t.Fatalf("azure binary message length = %d, want header length prefix", len(payload))
	}
	headerLen := int(binary.BigEndian.Uint16(payload[:2]))
	if len(payload) < 2+headerLen {
		t.Fatalf("azure binary message header length = %d exceeds payload length %d", headerLen, len(payload))
	}
	headers, body := splitAzureTestMessage(t, payload[2:2+headerLen])
	body = append(body, payload[2+headerLen:]...)
	return headers, body
}

func nextAzureTestEvent(t *testing.T, stream stt.RecognizeStream) *stt.SpeechEvent {
	t.Helper()
	type result struct {
		event *stt.SpeechEvent
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		event, err := stream.Next()
		ch <- result{event: event, err: err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("Next error = %v", res.err)
		}
		return res.event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream event")
		return nil
	}
}

func azureTestClosingDialer(
	t *testing.T,
	requests chan<- *http.Request,
	configMessages chan<- string,
	serverClosed chan<- struct{},
) azureSTTWebsocketDialer {
	t.Helper()
	return func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		errCh := make(chan error, 1)
		go runAzureTestClosingWebsocketServer(serverConn, requests, configMessages, serverClosed, errCh)

		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, nil, err
		}
		dialer := websocket.Dialer{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
			Proxy: nil,
		}
		conn, resp, err := dialer.DialContext(ctx, parsed.String(), headers)
		if err != nil {
			clientConn.Close()
			select {
			case serverErr := <-errCh:
				return nil, resp, fmt.Errorf("%w; server: %v", err, serverErr)
			default:
				return nil, resp, err
			}
		}
		go func() {
			if serverErr := <-errCh; serverErr != nil {
				t.Errorf("test closing websocket server: %v", serverErr)
			}
		}()
		return conn, resp, nil
	}
}

func runAzureTestClosingWebsocketServer(
	conn net.Conn,
	requests chan<- *http.Request,
	configMessages chan<- string,
	serverClosed chan<- struct{},
	errCh chan<- error,
) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	requests <- req
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", azureTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	opcode, payload, err := readAzureTestWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.TextMessage {
		errCh <- fmt.Errorf("speech config opcode = %d, want text", opcode)
		return
	}
	configMessages <- string(payload)
	serverClosed <- struct{}{}
	errCh <- nil
}

func azureTestCloseAfterAudioDialer(
	t *testing.T,
	requests chan<- *http.Request,
	configMessages chan<- string,
	audioMessages chan<- []byte,
	serverClosed chan<- struct{},
) azureSTTWebsocketDialer {
	t.Helper()
	return func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		errCh := make(chan error, 1)
		go runAzureTestCloseAfterAudioServer(serverConn, requests, configMessages, audioMessages, serverClosed, errCh)

		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, nil, err
		}
		dialer := websocket.Dialer{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
			Proxy: nil,
		}
		conn, resp, err := dialer.DialContext(ctx, parsed.String(), headers)
		if err != nil {
			clientConn.Close()
			select {
			case serverErr := <-errCh:
				return nil, resp, fmt.Errorf("%w; server: %v", err, serverErr)
			default:
				return nil, resp, err
			}
		}
		go func() {
			if serverErr := <-errCh; serverErr != nil && !isAzureTestWebsocketCleanupError(serverErr) {
				t.Errorf("test close-after-audio websocket server: %v", serverErr)
			}
		}()
		return conn, resp, nil
	}
}

func azureTestSessionStopDialer(
	t *testing.T,
	requests chan<- *http.Request,
	configMessages chan<- string,
	serverClosed chan<- struct{},
	stopSession <-chan struct{},
) azureSTTWebsocketDialer {
	t.Helper()
	return func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		errCh := make(chan error, 1)
		go runAzureTestSessionStopWebsocketServer(serverConn, requests, configMessages, serverClosed, stopSession, errCh)

		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, nil, err
		}
		dialer := websocket.Dialer{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
			Proxy: nil,
		}
		conn, resp, err := dialer.DialContext(ctx, parsed.String(), headers)
		if err != nil {
			clientConn.Close()
			select {
			case serverErr := <-errCh:
				return nil, resp, fmt.Errorf("%w; server: %v", err, serverErr)
			default:
				return nil, resp, err
			}
		}
		go func() {
			if serverErr := <-errCh; serverErr != nil && !isAzureTestWebsocketCleanupError(serverErr) {
				t.Errorf("test session-stop websocket server: %v", serverErr)
			}
		}()
		return conn, resp, nil
	}
}

func runAzureTestSessionStopWebsocketServer(
	conn net.Conn,
	requests chan<- *http.Request,
	configMessages chan<- string,
	serverClosed chan<- struct{},
	stopSession <-chan struct{},
	errCh chan<- error,
) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	requests <- req
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", azureTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	opcode, payload, err := readAzureTestWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.TextMessage {
		errCh <- fmt.Errorf("speech config opcode = %d, want text", opcode)
		return
	}
	configMessages <- string(payload)
	<-stopSession
	serverClosed <- struct{}{}
	errCh <- nil
}

func runAzureTestCloseAfterAudioServer(
	conn net.Conn,
	requests chan<- *http.Request,
	configMessages chan<- string,
	audioMessages chan<- []byte,
	serverClosed chan<- struct{},
	errCh chan<- error,
) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	requests <- req
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", azureTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	opcode, payload, err := readAzureTestWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.TextMessage {
		errCh <- fmt.Errorf("speech config opcode = %d, want text", opcode)
		return
	}
	configMessages <- string(payload)
	if err := writeAzureTestWebsocketFrame(conn, websocket.TextMessage, []byte("Path: turn.start\r\nContent-Type: application/json\r\n\r\n{}")); err != nil {
		errCh <- err
		return
	}
	opcode, payload, err = readAzureTestWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.BinaryMessage {
		errCh <- fmt.Errorf("audio opcode = %d, want binary", opcode)
		return
	}
	audioMessages <- payload
	if err := writeAzureTestWebsocketFrame(conn, websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
		errCh <- err
		return
	}
	serverClosed <- struct{}{}
	errCh <- nil
}

func azureTestDialer(
	t *testing.T,
	requests chan<- *http.Request,
	configMessages chan<- string,
	audioMessages chan<- []byte,
) azureSTTWebsocketDialer {
	t.Helper()
	return func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		errCh := make(chan error, 1)
		go runAzureTestWebsocketServer(t, serverConn, requests, configMessages, audioMessages, errCh)

		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, nil, err
		}
		dialer := websocket.Dialer{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
			Proxy: nil,
		}
		conn, resp, err := dialer.DialContext(ctx, parsed.String(), headers)
		if err != nil {
			clientConn.Close()
			select {
			case serverErr := <-errCh:
				return nil, resp, fmt.Errorf("%w; server: %v", err, serverErr)
			default:
				return nil, resp, err
			}
		}
		go func() {
			if serverErr := <-errCh; serverErr != nil && !isAzureTestWebsocketCleanupError(serverErr) {
				t.Errorf("test websocket server: %v", serverErr)
			}
		}()
		return conn, resp, nil
	}
}

func azureTestSessionReadyDialer(
	t *testing.T,
	requests chan<- *http.Request,
	configMessages chan<- string,
	audioMessages chan<- []byte,
	sessionReady <-chan struct{},
) azureSTTWebsocketDialer {
	t.Helper()
	return func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		errCh := make(chan error, 1)
		go runAzureTestSessionReadyWebsocketServer(serverConn, requests, configMessages, audioMessages, sessionReady, errCh)

		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, nil, err
		}
		dialer := websocket.Dialer{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
			Proxy: nil,
		}
		conn, resp, err := dialer.DialContext(ctx, parsed.String(), headers)
		if err != nil {
			clientConn.Close()
			select {
			case serverErr := <-errCh:
				return nil, resp, fmt.Errorf("%w; server: %v", err, serverErr)
			default:
				return nil, resp, err
			}
		}
		go func() {
			if serverErr := <-errCh; serverErr != nil && !isAzureTestWebsocketCleanupError(serverErr) {
				t.Errorf("test session-ready websocket server: %v", serverErr)
			}
		}()
		return conn, resp, nil
	}
}

func runAzureTestSessionReadyWebsocketServer(
	conn net.Conn,
	requests chan<- *http.Request,
	configMessages chan<- string,
	audioMessages chan<- []byte,
	sessionReady <-chan struct{},
	errCh chan<- error,
) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	requests <- req
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", azureTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}

	opcode, payload, err := readAzureTestWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.TextMessage {
		errCh <- fmt.Errorf("speech config opcode = %d, want text", opcode)
		return
	}
	configMessages <- string(payload)

	<-sessionReady
	if err := writeAzureTestWebsocketFrame(conn, websocket.TextMessage, []byte("Path: turn.start\r\nContent-Type: application/json\r\n\r\n{}")); err != nil {
		errCh <- err
		return
	}

	opcode, payload, err = readAzureTestWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.BinaryMessage {
		errCh <- fmt.Errorf("audio opcode = %d, want binary", opcode)
		return
	}
	audioMessages <- payload
	errCh <- nil
}

func isAzureTestWebsocketCleanupError(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed)
}

func runAzureTestWebsocketServer(
	t *testing.T,
	conn net.Conn,
	requests chan<- *http.Request,
	configMessages chan<- string,
	audioMessages chan<- []byte,
	errCh chan<- error,
) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	requests <- req
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", azureTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}

	opcode, payload, err := readAzureTestWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.TextMessage {
		errCh <- fmt.Errorf("speech config opcode = %d, want text", opcode)
		return
	}
	configMessages <- string(payload)
	if err := writeAzureTestWebsocketFrame(conn, websocket.TextMessage, []byte("Path: turn.start\r\nContent-Type: application/json\r\n\r\n{}")); err != nil {
		errCh <- err
		return
	}

	opcode, payload, err = readAzureTestWebsocketFrame(reader)
	if err != nil {
		errCh <- err
		return
	}
	if opcode != websocket.BinaryMessage {
		errCh <- fmt.Errorf("audio opcode = %d, want binary", opcode)
		return
	}
	audioMessages <- payload

	for _, message := range []string{
		"Path: speech.startDetected\r\nContent-Type: application/json\r\n\r\n{}",
		"Path: speech.hypothesis\r\nContent-Type: application/json\r\n\r\n{\"Text\":\"halo sementara\"}",
		"Path: speech.phrase\r\nContent-Type: application/json\r\n\r\n{\"RecognitionStatus\":\"Success\",\"DisplayText\":\"halo final\",\"NBest\":[{\"Display\":\"halo final\",\"Confidence\":0.87}]}",
		"Path: speech.endDetected\r\nContent-Type: application/json\r\n\r\n{}",
	} {
		if err := writeAzureTestWebsocketFrame(conn, websocket.TextMessage, []byte(message)); err != nil {
			errCh <- err
			return
		}
	}
	_, _, _ = readAzureTestWebsocketFrame(reader)
	errCh <- nil
}

func azureTestAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func readAzureTestWebsocketFrame(r io.Reader) (int, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	opcode := int(header[0] & 0x0f)
	masked := header[1]&0x80 != 0
	payloadLen := uint64(header[1] & 0x7f)
	switch payloadLen {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(r, extended[:]); err != nil {
			return 0, nil, err
		}
		payloadLen = uint64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(r, extended[:]); err != nil {
			return 0, nil, err
		}
		payloadLen = binary.BigEndian.Uint64(extended[:])
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

func writeAzureTestWebsocketFrame(w io.Writer, opcode int, payload []byte) error {
	header := []byte{0x80 | byte(opcode)}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(payload)))
		header = append(header, 127)
		header = append(header, length[:]...)
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func TestAzureTTSStreamReportsUnsupported(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	_, err = provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Stream error = %v, want unsupported error", err)
	}
}

func TestAzureTTSChunkedStreamByteAlignment(t *testing.T) {
	dataCh := [][]byte{
		{0x01},
		{0x02, 0x03},
		{0x04, 0x05, 0x06},
	}
	closer := &testMultiChunkCloser{chunks: dataCh}
	stream := &azureTTSChunkedStream{
		body:       closer,
		sampleRate: 24000,
	}

	audio1, err := stream.Next()
	if err != nil {
		t.Fatalf("Next (1) error = %v", err)
	}
	if !bytes.Equal(audio1.Frame.Data, []byte{0x01, 0x02}) {
		t.Fatalf("audio1 data = %v, want [0x01, 0x02]", audio1.Frame.Data)
	}

	audio2, err := stream.Next()
	if err != nil {
		t.Fatalf("Next (2) error = %v", err)
	}
	if !bytes.Equal(audio2.Frame.Data, []byte{0x03, 0x04, 0x05, 0x06}) {
		t.Fatalf("audio2 data = %v, want [0x03, 0x04, 0x05, 0x06]", audio2.Frame.Data)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

type testMultiChunkCloser struct {
	chunks [][]byte
	idx    int
}

func (c *testMultiChunkCloser) Read(p []byte) (int, error) {
	if c.idx >= len(c.chunks) {
		return 0, io.EOF
	}
	chunk := c.chunks[c.idx]
	c.idx++
	n := copy(p, chunk)
	return n, nil
}

func (c *testMultiChunkCloser) Close() error {
	return nil
}

func TestAzureTTSImplementsInterface(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}
	var _ tts.TTS = provider
}

func TestAzureSTTImplementsInterface(t *testing.T) {
	provider, err := NewAzureSTT("key", "eastus")
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	var _ stt.STT = provider
}
