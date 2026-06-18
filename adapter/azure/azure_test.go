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
	if got := final.Alternatives[0].Confidence; got != 0.87 {
		t.Fatalf("final confidence = %v, want 0.87", got)
	}
	if got := final.Alternatives[0].Language; got != "id-ID" {
		t.Fatalf("final language = %q, want id-ID", got)
	}

	end := nextAzureTestEvent(t, stream)
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("end Type = %s, want end_of_speech", end.Type)
	}
}

func TestAzureSTTStreamPreservesExplicitZeroConfidence(t *testing.T) {
	event := parseAzureSTTMessage(
		resolveAzureSTTLanguage("id-ID"),
		[]byte("Path: speech.phrase\r\nContent-Type: application/json\r\n\r\n{\"RecognitionStatus\":\"Success\",\"DisplayText\":\"fallback text\",\"NBest\":[{\"Display\":\"zero confidence\",\"Confidence\":0}]}"),
	)
	if event == nil {
		t.Fatal("event = nil, want final transcript")
	}
	if got := event.Alternatives[0].Text; got != "zero confidence" {
		t.Fatalf("text = %q, want NBest display", got)
	}
	if got := event.Alternatives[0].Confidence; got != 0 {
		t.Fatalf("confidence = %v, want explicit Azure NBest zero confidence", got)
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

func TestAzureSTTStreamClosesAfterAudioWriteFailure(t *testing.T) {
	requests := make(chan *http.Request, defaultAzureSTTRetries+1)
	configMessages := make(chan string, defaultAzureSTTRetries+1)
	serverClosed := make(chan struct{}, defaultAzureSTTRetries+1)

	provider, err := NewAzureSTT("key", "eastus", WithAzureSTTWebsocketURL("ws://azure.test/speech/recognition/conversation/cognitiveservices/v1"))
	if err != nil {
		t.Fatalf("NewAzureSTT error = %v", err)
	}
	closingDialer := azureTestClosingDialer(t, requests, configMessages, serverClosed)
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
	receiveAzureTestSignal(t, serverClosed, "server close")

	err = stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	})
	if err == nil {
		t.Fatal("first PushFrame error = nil, want websocket write failure")
	}
	providerStream, ok := stream.(*azureSTTStream)
	if !ok {
		t.Fatalf("stream = %T, want *azureSTTStream", stream)
	}
	if !providerStream.closed {
		t.Fatal("stream closed = false after write failure, want true")
	}
	terminalAttempts := attempts
	if terminalAttempts != defaultAzureSTTRetries+1 {
		t.Fatalf("dial attempts after terminal write failure = %d, want initial plus retries", terminalAttempts)
	}

	_, nextErr := stream.Next()
	if !errors.Is(nextErr, err) {
		t.Fatalf("Next error = %v, want terminal write error %v", nextErr, err)
	}
	_, nextErr = stream.Next()
	if nextErr == nil || errors.Is(nextErr, err) {
		t.Fatalf("second Next error = %v, want shutdown/EOF after one terminal error", nextErr)
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

func TestAzureSTTStreamReconnectsAfterAudioWriteFailure(t *testing.T) {
	requests := make(chan *http.Request, 2)
	configMessages := make(chan string, 2)
	audioMessages := make(chan []byte, 1)
	serverClosed := make(chan struct{}, defaultAzureSTTRetries+1)

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

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	receiveAzureTestValue(t, requests, "first request")
	receiveAzureTestValue(t, configMessages, "first speech config")
	receiveAzureTestSignal(t, serverClosed, "server close")

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x02},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame after reconnect error = %v", err)
	}

	receiveAzureTestValue(t, requests, "second request")
	receiveAzureTestValue(t, configMessages, "second speech config")
	audioMessage := receiveAzureTestValue(t, audioMessages, "audio after reconnect")
	_, audioPayload := splitAzureTestBinaryMessage(t, audioMessage)
	if !bytes.Equal(audioPayload, []byte{0x01, 0x02}) {
		t.Fatalf("audio payload after reconnect = %v, want original pushed PCM", audioPayload)
	}
	if attempts != 2 {
		t.Fatalf("dial attempts = %d, want 2", attempts)
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

	_, err := NewAzureTTS("", "", "")

	if err == nil || !strings.Contains(err.Error(), "AZURE_SPEECH_KEY") {
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

func TestAzureTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider, err := NewAzureTTS("key", "eastus", "")
	if err != nil {
		t.Fatalf("NewAzureTTS error = %v", err)
	}

	provider.UpdateOptions("id-ID-GadisNeural", "id-ID")

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
			if serverErr := <-errCh; serverErr != nil {
				t.Errorf("test websocket server: %v", serverErr)
			}
		}()
		return conn, resp, nil
	}
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
		"Path: turn.start\r\nContent-Type: application/json\r\n\r\n{}",
		"Path: speech.hypothesis\r\nContent-Type: application/json\r\n\r\n{\"Text\":\"halo sementara\"}",
		"Path: speech.phrase\r\nContent-Type: application/json\r\n\r\n{\"RecognitionStatus\":\"Success\",\"DisplayText\":\"halo final\",\"NBest\":[{\"Display\":\"halo final\",\"Confidence\":0.87}]}",
		"Path: turn.end\r\nContent-Type: application/json\r\n\r\n{}",
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
