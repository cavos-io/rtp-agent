package ultravox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	audiomodel "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/gorilla/websocket"
)

func TestUltravoxRealtimeConstructorMatchesReference(t *testing.T) {
	t.Run("defaults", TestUltravoxRealtimeDefaultsMatchReference)
	t.Run("env_key", TestNewUltravoxRealtimeModelUsesEnvironmentAPIKey)
	t.Run("missing_key", TestNewUltravoxRealtimeModelRequiresAPIKey)
	t.Run("options", TestUltravoxRealtimeOptionsMatchReference)
}

func TestUltravoxRealtimeDefaultsMatchReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if got := model.Model(); got != "fixie-ai/ultravox" {
		t.Fatalf("model = %q, want reference default", got)
	}
	if got := model.Provider(); got != "Ultravox" {
		t.Fatalf("provider = %q, want Ultravox", got)
	}
	if got := model.Label(); got != "ultravox-fixie-ai/ultravox" {
		t.Fatalf("label = %q, want reference label", got)
	}
	if got := model.Voice(); got != "Mark" {
		t.Fatalf("voice = %q, want reference default voice", got)
	}
	if got := model.BaseURL(); got != "https://api.ultravox.ai/api" {
		t.Fatalf("base URL = %q, want reference API URL", got)
	}
	if got := model.SystemPrompt(); got != "You are a helpful assistant." {
		t.Fatalf("system prompt = %q, want reference default prompt", got)
	}
	if got := model.InputSampleRate(); got != 16000 {
		t.Fatalf("input sample rate = %d, want reference 16000", got)
	}
	if got := model.OutputSampleRate(); got != 24000 {
		t.Fatalf("output sample rate = %d, want reference 24000", got)
	}
	if got := model.OutputMedium(); got != "voice" {
		t.Fatalf("output medium = %q, want reference voice", got)
	}
	if got, ok := model.FirstSpeaker(); !ok || got != "FIRST_SPEAKER_USER" {
		t.Fatalf("first speaker = %q/%v, want reference FIRST_SPEAKER_USER/true", got, ok)
	}

	caps := model.Capabilities()
	if !caps.MessageTruncation || !caps.TurnDetection || !caps.UserTranscription || !caps.AutoToolReplyGeneration || !caps.AudioOutput {
		t.Fatalf("capabilities = %+v, want reference realtime voice capabilities", caps)
	}
	if caps.ManualFunctionCalls || caps.PerResponseToolChoice {
		t.Fatalf("capabilities = %+v, want no manual function calls or per-response tool choice", caps)
	}
	var _ llm.RealtimeModel = model
}

func TestNewUltravoxRealtimeModelUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ULTRAVOX_API_KEY", "env-key")

	model, err := NewRealtimeModel("")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v, want env fallback", err)
	}
	if got := model.APIKey(); got != "env-key" {
		t.Fatalf("api key = %q, want env key", got)
	}
}

func TestNewUltravoxRealtimeModelRequiresAPIKey(t *testing.T) {
	t.Setenv("ULTRAVOX_API_KEY", "")

	_, err := NewRealtimeModel("")
	if err == nil || !strings.Contains(err.Error(), "ULTRAVOX_API_KEY") {
		t.Fatalf("NewRealtimeModel error = %v, want missing key guidance", err)
	}
}

func TestUltravoxRealtimeOptionsMatchReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key",
		WithRealtimeModel("fixie-ai/ultravox-llama3.3-70b"),
		WithRealtimeVoice("Jessica"),
		WithRealtimeBaseURL("https://ultravox.example/api/"),
		WithRealtimeSystemPrompt("stay concise"),
		WithRealtimeOutputMedium("text"),
		WithRealtimeInputSampleRate(8000),
		WithRealtimeOutputSampleRate(48000),
		WithRealtimeTemperature(0.2),
		WithRealtimeLanguageHint("es"),
		WithRealtimeMaxDuration("30m"),
		WithRealtimeTimeExceededMessage("done"),
		WithRealtimeEnableGreetingPrompt(false),
		WithRealtimeFirstSpeaker("FIRST_SPEAKER_AGENT"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if got := model.Model(); got != "fixie-ai/ultravox-llama3.3-70b" {
		t.Fatalf("model = %q, want configured model", got)
	}
	if got := model.Voice(); got != "Jessica" {
		t.Fatalf("voice = %q, want configured voice", got)
	}
	if got := model.BaseURL(); got != "https://ultravox.example/api" {
		t.Fatalf("base URL = %q, want trimmed configured URL", got)
	}
	if got := model.OutputMedium(); got != "text" {
		t.Fatalf("output medium = %q, want text", got)
	}
	if model.Capabilities().AudioOutput {
		t.Fatal("audio output = true, want false for text output medium")
	}
	if got, ok := model.Temperature(); !ok || got != 0.2 {
		t.Fatalf("temperature = %v/%v, want 0.2/true", got, ok)
	}
	if got, ok := model.LanguageHint(); !ok || got != "es" {
		t.Fatalf("language hint = %q/%v, want es/true", got, ok)
	}
	if got, ok := model.MaxDuration(); !ok || got != "30m" {
		t.Fatalf("max duration = %q/%v, want 30m/true", got, ok)
	}
	if got, ok := model.TimeExceededMessage(); !ok || got != "done" {
		t.Fatalf("time exceeded message = %q/%v, want done/true", got, ok)
	}
	if got, ok := model.EnableGreetingPrompt(); !ok || got {
		t.Fatalf("enable greeting prompt = %v/%v, want false/true", got, ok)
	}
	if got, ok := model.FirstSpeaker(); !ok || got != "FIRST_SPEAKER_AGENT" {
		t.Fatalf("first speaker = %q/%v, want FIRST_SPEAKER_AGENT/true", got, ok)
	}
}

func TestUltravoxRealtimeOptionsPreserveReferenceEmptyOutputMedium(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeOutputMedium(""))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	if got := model.OutputMedium(); got != "" {
		t.Fatalf("output medium = %q, want explicit empty reference output_medium", got)
	}
	if model.Capabilities().AudioOutput {
		t.Fatal("audio output = true, want false for empty output_medium")
	}
}

func TestUltravoxRealtimeOptionsPreserveReferenceZeroSampleRates(t *testing.T) {
	model, err := NewRealtimeModel("test-key",
		WithRealtimeInputSampleRate(0),
		WithRealtimeOutputSampleRate(0),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	if got := model.InputSampleRate(); got != 0 {
		t.Fatalf("input sample rate = %d, want explicit zero reference input_sample_rate", got)
	}
	if got := model.OutputSampleRate(); got != 0 {
		t.Fatalf("output sample rate = %d, want explicit zero reference output_sample_rate", got)
	}

	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	_, _, payload := session.createCallRequest()
	medium := payload["medium"].(map[string]any)
	serverWebsocket := medium["serverWebSocket"].(map[string]any)
	if got := serverWebsocket["inputSampleRate"]; got != 0 {
		t.Fatalf("inputSampleRate payload = %#v, want explicit zero reference input_sample_rate", got)
	}
	if got := serverWebsocket["outputSampleRate"]; got != 0 {
		t.Fatalf("outputSampleRate payload = %#v, want explicit zero reference output_sample_rate", got)
	}
}

func TestUltravoxRealtimeUpdateOptionsMatchReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	model.UpdateOptions(WithRealtimeUpdateOutputMedium("text"))
	if got := model.OutputMedium(); got != "text" {
		t.Fatalf("output medium = %q, want text after reference update_options", got)
	}
	if model.Capabilities().AudioOutput {
		t.Fatal("audio output = true, want false after output_medium=text")
	}

	model.UpdateOptions(WithRealtimeUpdateOutputMedium("voice"))
	if got := model.OutputMedium(); got != "voice" {
		t.Fatalf("output medium = %q, want voice after reference update_options", got)
	}
	if !model.Capabilities().AudioOutput {
		t.Fatal("audio output = false, want true after output_medium=voice")
	}
}

func TestUltravoxRealtimeModelUpdateOptionsPropagatesReferenceSessions(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	model.UpdateOptions(WithRealtimeUpdateOutputMedium("text"))
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeModalities(t, message.ModalitiesCh, []string{"text"})
}

func TestUltravoxRealtimeModelUpdateOptionsPreservesReferenceEmptyOutputMedium(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	model.UpdateOptions(WithRealtimeUpdateOutputMedium(""))
	if got := model.OutputMedium(); got != "" {
		t.Fatalf("output medium = %q, want explicit empty reference output_medium", got)
	}
	if model.Capabilities().AudioOutput {
		t.Fatal("audio output = true, want false after empty output_medium")
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "",
	})
}

func TestUltravoxRealtimeSessionUpdateOptionsQueuesReferenceOutputMedium(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{
		OutputMedium:    "text",
		OutputMediumSet: true,
	}); err != nil {
		t.Fatalf("UpdateOptions output medium error = %v, want reference set_output_medium event", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{}); err != nil {
		t.Fatalf("UpdateOptions empty error = %v, want reference no-op for unset output medium", err)
	}
	select {
	case got := <-session.clientEventCh:
		t.Fatalf("unexpected client event for empty UpdateOptions = %#v", got)
	default:
	}
}

func TestUltravoxRealtimeSessionQueuesReferenceInitialTextOutputMedium(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeOutputMedium("text"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})
}

func TestUltravoxRealtimeSessionCreateCallRequestMatchesReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key",
		WithRealtimeModel("fixie-ai/ultravox-llama3.3-70b"),
		WithRealtimeVoice("Jessica"),
		WithRealtimeBaseURL("https://ultravox.example/api/"),
		WithRealtimeSystemPrompt("stay concise"),
		WithRealtimeInputSampleRate(8000),
		WithRealtimeOutputSampleRate(48000),
		WithRealtimeTemperature(0.2),
		WithRealtimeLanguageHint("es"),
		WithRealtimeMaxDuration("30m"),
		WithRealtimeTimeExceededMessage("done"),
		WithRealtimeEnableGreetingPrompt(false),
		WithRealtimeFirstSpeaker("FIRST_SPEAKER_AGENT"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.UpdateTools([]llm.Tool{ultravoxRealtimeTestTool{
		name:        "lookup_weather",
		description: "Lookup weather",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{
					"type":        "string",
					"description": "City name",
				},
				"unit": map[string]any{
					"enum":        []any{"c", "f"},
					"description": "Temperature unit",
				},
			},
			"required": []any{"city"},
		},
	}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}

	gotURL, gotHeaders, gotPayload := session.createCallRequest()
	if gotURL != "https://ultravox.example/api/calls?enableGreetingPrompt=false" {
		t.Fatalf("create-call URL = %q, want reference URL with greeting query", gotURL)
	}
	wantHeaders := map[string]string{
		"User-Agent":   "LiveKit Agents",
		"X-API-Key":    "test-key",
		"Content-Type": "application/json",
	}
	if !reflect.DeepEqual(gotHeaders, wantHeaders) {
		t.Fatalf("headers = %#v, want %#v", gotHeaders, wantHeaders)
	}

	wantPayload := map[string]any{
		"systemPrompt": "stay concise",
		"model":        "fixie-ai/ultravox-llama3.3-70b",
		"voice":        "Jessica",
		"medium": map[string]any{
			"serverWebSocket": map[string]any{
				"inputSampleRate":    8000,
				"outputSampleRate":   48000,
				"clientBufferSizeMs": 30000,
			},
		},
		"selectedTools": []map[string]any{
			{
				"temporaryTool": map[string]any{
					"modelToolName": "lookup_weather",
					"description":   "Lookup weather",
					"dynamicParameters": []map[string]any{
						{
							"name":     "city",
							"location": "PARAMETER_LOCATION_BODY",
							"schema": map[string]any{
								"type":        "string",
								"description": "City name",
							},
							"required": true,
						},
						{
							"name":     "unit",
							"location": "PARAMETER_LOCATION_BODY",
							"schema": map[string]any{
								"type":        "string",
								"description": "Temperature unit",
							},
							"required": false,
						},
					},
					"client": map[string]any{},
				},
			},
		},
		"temperature":         0.2,
		"languageHint":        "es",
		"maxDuration":         "30m",
		"timeExceededMessage": "done",
		"firstSpeaker":        "FIRST_SPEAKER_AGENT",
	}
	if !reflect.DeepEqual(gotPayload, wantPayload) {
		t.Fatalf("payload = %#v, want %#v", gotPayload, wantPayload)
	}
}

func TestUltravoxRealtimeSessionCreateCallDefaultDisablesReferenceGreetingPrompt(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeBaseURL("https://ultravox.example/api/"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	gotURL, _, _ := session.createCallRequest()
	if gotURL != "https://ultravox.example/api/calls?enableGreetingPrompt=false" {
		t.Fatalf("create-call URL = %q, want reference default greeting prompt disabled query", gotURL)
	}
}

func TestUltravoxRealtimeSessionCreateCallPreservesReferenceEmptySystemPrompt(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeSystemPrompt(""))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	_, _, payload := session.createCallRequest()
	if got := payload["systemPrompt"]; got != "" {
		t.Fatalf("systemPrompt = %#v, want explicit empty reference system_prompt", got)
	}
}

func TestUltravoxRealtimeSessionCreateCallPreservesReferenceEmptyModel(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeModel(""))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	_, _, payload := session.createCallRequest()
	if got := payload["model"]; got != "" {
		t.Fatalf("model = %#v, want explicit empty reference model", got)
	}
}

func TestUltravoxRealtimeSessionCreateCallPreservesReferenceEmptyVoice(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeVoice(""))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	_, _, payload := session.createCallRequest()
	if got := payload["voice"]; got != "" {
		t.Fatalf("voice = %#v, want explicit empty reference voice", got)
	}
}

func TestUltravoxRealtimeSessionCreateCallPreservesReferenceEmptyFirstSpeaker(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeFirstSpeaker(""))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	_, _, payload := session.createCallRequest()
	if got := payload["firstSpeaker"]; got != "" {
		t.Fatalf("firstSpeaker = %#v, want explicit empty reference first_speaker", got)
	}
}

func TestUltravoxRealtimeSessionCreateCallPreservesReferenceEmptyLanguageHint(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeLanguageHint(""))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	_, _, payload := session.createCallRequest()
	if got, ok := payload["languageHint"]; !ok || got != "" {
		t.Fatalf("languageHint = %#v/%v, want explicit empty reference language_hint", got, ok)
	}
}

func TestUltravoxRealtimeSessionCreateCallPreservesReferenceEmptyMaxDuration(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeMaxDuration(""))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	_, _, payload := session.createCallRequest()
	if got, ok := payload["maxDuration"]; !ok || got != "" {
		t.Fatalf("maxDuration = %#v/%v, want explicit empty reference max_duration", got, ok)
	}
}

func TestUltravoxRealtimeSessionCreateCallPreservesReferenceEmptyTimeExceededMessage(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeTimeExceededMessage(""))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	_, _, payload := session.createCallRequest()
	if got, ok := payload["timeExceededMessage"]; !ok || got != "" {
		t.Fatalf("timeExceededMessage = %#v/%v, want explicit empty reference time_exceeded_message", got, ok)
	}
}

func TestUltravoxRealtimeCreateCallResponseRequiresReferenceJoinURL(t *testing.T) {
	got, err := ultravoxRealtimeCreateCallJoinURL([]byte(`{"joinUrl":"wss://ultravox.example/join"}`))
	if err != nil {
		t.Fatalf("joinUrl parse error = %v, want nil", err)
	}
	if got != "wss://ultravox.example/join" {
		t.Fatalf("joinUrl = %q, want reference response URL", got)
	}

	for _, body := range []string{
		`{}`,
		`{"joinUrl":""}`,
	} {
		if _, err := ultravoxRealtimeCreateCallJoinURL([]byte(body)); err == nil || err.Error() != "Ultravox call created, but no joinUrl received." {
			t.Fatalf("joinUrl parse error for %s = %v, want reference missing joinUrl error", body, err)
		}
	}
}

func TestUltravoxRealtimeSessionCreateCallPostsReferenceRequest(t *testing.T) {
	model, err := NewRealtimeModel("test-key",
		WithRealtimeBaseURL("https://ultravox.example/api/"),
		WithRealtimeSystemPrompt("stay concise"),
		WithRealtimeEnableGreetingPrompt(false),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	doer := &ultravoxRealtimeTestHTTPDoer{
		responseStatus: http.StatusOK,
		responseBody:   `{"joinUrl":"wss://ultravox.example/join"}`,
	}
	got, err := session.createCall(context.Background(), doer)
	if err != nil {
		t.Fatalf("createCall error = %v, want nil", err)
	}
	if got != "wss://ultravox.example/join" {
		t.Fatalf("joinUrl = %q, want reference joinUrl", got)
	}
	if doer.request == nil {
		t.Fatal("createCall did not issue HTTP request")
	}
	if doer.request.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", doer.request.Method)
	}
	if gotURL := doer.request.URL.String(); gotURL != "https://ultravox.example/api/calls?enableGreetingPrompt=false" {
		t.Fatalf("URL = %q, want reference create-call URL", gotURL)
	}
	if gotHeader := doer.request.Header.Get("User-Agent"); gotHeader != "LiveKit Agents" {
		t.Fatalf("User-Agent = %q, want LiveKit Agents", gotHeader)
	}
	if gotHeader := doer.request.Header.Get("X-API-Key"); gotHeader != "test-key" {
		t.Fatalf("X-API-Key = %q, want API key", gotHeader)
	}
	if gotHeader := doer.request.Header.Get("Content-Type"); gotHeader != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotHeader)
	}
	var payload map[string]any
	if err := json.Unmarshal(doer.requestBody, &payload); err != nil {
		t.Fatalf("request JSON = %q failed decode: %v", doer.requestBody, err)
	}
	if payload["systemPrompt"] != "stay concise" || payload["model"] != "fixie-ai/ultravox" || payload["voice"] != "Mark" {
		t.Fatalf("request payload core fields = %#v, want reference model prompt voice", payload)
	}
}

func TestUltravoxRealtimeModelDialsReferenceJoinURL(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	wantConn := &ultravoxRealtimeTestWebsocketConn{}
	var gotEndpoint string
	var gotHeaders http.Header
	model.dialWebsocket = func(_ context.Context, endpoint string, headers http.Header) (ultravoxRealtimeWebsocketConn, error) {
		gotEndpoint = endpoint
		gotHeaders = headers
		return wantConn, nil
	}

	gotConn, err := model.dialRealtimeWebsocket(context.Background(), "wss://ultravox.example/join")
	if err != nil {
		t.Fatalf("dialRealtimeWebsocket error = %v, want nil", err)
	}
	if gotConn != wantConn {
		t.Fatalf("websocket conn = %#v, want fake connection", gotConn)
	}
	if gotEndpoint != "wss://ultravox.example/join" {
		t.Fatalf("websocket endpoint = %q, want reference joinUrl", gotEndpoint)
	}
	if len(gotHeaders) != 0 {
		t.Fatalf("websocket headers = %#v, want reference ws_connect without extra headers", gotHeaders)
	}
}

func TestUltravoxRealtimeSessionConnectsReferenceCreateCallToWebsocket(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeBaseURL("https://ultravox.example/api/"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	wantConn := &ultravoxRealtimeTestWebsocketConn{}
	var gotEndpoint string
	model.dialWebsocket = func(_ context.Context, endpoint string, headers http.Header) (ultravoxRealtimeWebsocketConn, error) {
		gotEndpoint = endpoint
		if len(headers) != 0 {
			t.Fatalf("websocket headers = %#v, want reference empty headers", headers)
		}
		return wantConn, nil
	}
	doer := &ultravoxRealtimeTestHTTPDoer{
		responseStatus: http.StatusOK,
		responseBody:   `{"joinUrl":"wss://ultravox.example/join"}`,
	}

	gotConn, err := session.connectRealtimeWebsocket(context.Background(), doer)
	if err != nil {
		t.Fatalf("connectRealtimeWebsocket error = %v, want nil", err)
	}
	if gotConn != wantConn {
		t.Fatalf("connection = %#v, want websocket conn from dialer", gotConn)
	}
	if doer.request == nil || doer.request.Method != http.MethodPost {
		t.Fatalf("create-call request = %#v, want POST before websocket dial", doer.request)
	}
	if gotEndpoint != "wss://ultravox.example/join" {
		t.Fatalf("websocket endpoint = %q, want joinUrl from create-call response", gotEndpoint)
	}
}

func TestUltravoxRealtimeSessionRestartRequeuesReferenceTextOutputMedium(t *testing.T) {
	model, err := NewRealtimeModel("test-key",
		WithRealtimeSystemPrompt("stay concise"),
		WithRealtimeOutputMedium("text"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})
	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})
}

func TestUltravoxRealtimeSessionUpdateInstructionsMarksReferenceRestart(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeSystemPrompt("stay concise"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.UpdateInstructions("stay concise"); err != nil {
		t.Fatalf("UpdateInstructions same prompt error = %v, want reference no-op", err)
	}
	if got := session.restartCount; got != 0 {
		t.Fatalf("restart count after unchanged instructions = %d, want 0", got)
	}

	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions changed prompt error = %v, want reference restart", err)
	}
	if got := session.restartCount; got != 1 {
		t.Fatalf("restart count after changed instructions = %d, want 1", got)
	}
	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions repeated prompt error = %v, want reference no-op", err)
	}
	if got := session.restartCount; got != 1 {
		t.Fatalf("restart count after repeated instructions = %d, want 1", got)
	}
}

func TestUltravoxRealtimeSessionUpdateInstructionsUpdatesReferenceModelPrompt(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeSystemPrompt("stay concise"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions changed prompt error = %v, want reference shared prompt update", err)
	}
	if got := model.SystemPrompt(); got != "answer briefly" {
		t.Fatalf("model system prompt = %q, want reference shared prompt update", got)
	}

	nextSessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("second Session error = %v", err)
	}
	nextSession := nextSessionInterface.(*realtimeSession)
	defer nextSession.Close()
	if got := nextSession.systemPrompt; got != "answer briefly" {
		t.Fatalf("new session system prompt = %q, want updated prompt", got)
	}
}

func TestUltravoxRealtimeSessionUpdateInstructionsKeepsActiveGenerationForReferenceRestart(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeSystemPrompt("stay concise"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hello",
		Final:   false,
		Ordinal: 1,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hello")

	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions changed prompt error = %v, want reference restart", err)
	}
	assertUltravoxRealtimeTextOpen(t, message.TextCh)
	assertUltravoxRealtimeAudioOpen(t, message.AudioCh)
	assertNoUltravoxRealtimeMetrics(t, session)
}

func TestUltravoxRealtimeSessionUpdateToolsMarksReferenceRestartOnNameSetChange(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	lookup := ultravoxRealtimeTestTool{name: "lookup"}
	if err := session.UpdateTools([]llm.Tool{lookup}); err != nil {
		t.Fatalf("UpdateTools lookup error = %v, want reference restart", err)
	}
	if got := session.restartCount; got != 1 {
		t.Fatalf("restart count after adding lookup = %d, want 1", got)
	}
	if err := session.UpdateTools([]llm.Tool{ultravoxRealtimeTestTool{name: "lookup"}}); err != nil {
		t.Fatalf("UpdateTools same name error = %v, want reference no-op", err)
	}
	if got := session.restartCount; got != 1 {
		t.Fatalf("restart count after same tool-name set = %d, want 1", got)
	}
	if err := session.UpdateTools([]llm.Tool{lookup, ultravoxRealtimeTestTool{name: "calendar"}}); err != nil {
		t.Fatalf("UpdateTools changed name set error = %v, want reference restart", err)
	}
	if got := session.restartCount; got != 2 {
		t.Fatalf("restart count after changed tool-name set = %d, want 2", got)
	}
}

func TestUltravoxRealtimeSessionUpdateToolsKeepsReferenceSameNameToolState(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	lookupV1 := ultravoxRealtimeTestTool{name: "lookup", description: "old schema"}
	if err := session.UpdateTools([]llm.Tool{lookupV1}); err != nil {
		t.Fatalf("UpdateTools lookup v1 error = %v", err)
	}
	if got := session.restartCount; got != 1 {
		t.Fatalf("restart count after first tool = %d, want 1", got)
	}

	lookupV2 := ultravoxRealtimeTestTool{name: "lookup", description: "new schema"}
	if err := session.UpdateTools([]llm.Tool{lookupV2}); err != nil {
		t.Fatalf("UpdateTools lookup v2 error = %v", err)
	}
	if got := session.restartCount; got != 1 {
		t.Fatalf("restart count after same-name tool update = %d, want no restart", got)
	}
	if got := len(session.tools); got != 1 {
		t.Fatalf("tools len = %d, want 1 updated reference tool", got)
	}
	if got := session.tools[0].Description(); got != "new schema" {
		t.Fatalf("tool description = %q, want updated same-name reference tool", got)
	}
}

func TestUltravoxRealtimeSessionUpdateToolsKeepsActiveGenerationForReferenceRestart(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hello",
		Final:   false,
		Ordinal: 1,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hello")

	if err := session.UpdateTools([]llm.Tool{ultravoxRealtimeTestTool{name: "lookup"}}); err != nil {
		t.Fatalf("UpdateTools changed tool set error = %v, want reference restart", err)
	}
	assertUltravoxRealtimeTextOpen(t, message.TextCh)
	assertUltravoxRealtimeAudioOpen(t, message.AudioCh)
	assertNoUltravoxRealtimeMetrics(t, session)
}

func TestUltravoxRealtimeSessionLifecycleMatchesReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v, want reference session lifecycle", err)
	}
	if session == nil {
		t.Fatal("Session = nil, want reference realtime session")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if _, ok := <-session.EventCh(); ok {
		t.Fatal("EventCh still open after Close")
	}
}

func TestUltravoxRealtimeSessionCloseFinishesReferenceActiveGeneration(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hello",
		Final:   false,
		Ordinal: 1,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hello")

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v, want reference active generation cleanup", err)
	}
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionPushAudioQueuesReferenceInputChunk(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	pcm := make([]byte, 3200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	frame := &audiomodel.AudioFrame{
		Data:              pcm,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}

	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio error = %v, want reference audio input accepted", err)
	}

	select {
	case got := <-session.audioCh:
		if !bytes.Equal(got, pcm) {
			t.Fatalf("queued audio bytes = %v, want original 100ms PCM chunk", got[:min(len(got), 8)])
		}
	case <-time.After(time.Second):
		t.Fatal("PushAudio did not queue reference 100ms PCM chunk")
	}
	if err := session.CommitAudio(); err != nil {
		t.Fatalf("CommitAudio error = %v, want reference no-op", err)
	}
	if err := session.ClearAudio(); err != nil {
		t.Fatalf("ClearAudio error = %v, want reference no-op", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio after Close error = %v, want reference no-op", err)
	}

	resamplingModel, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	resamplingSessionInterface, err := resamplingModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	resamplingSession := resamplingSessionInterface.(*realtimeSession)
	defer resamplingSession.Close()

	stereo8K := make([]byte, 800*2*2)
	left, right := int16(1000), int16(-1000)
	for sample := 0; sample < 800; sample++ {
		offset := sample * 4
		binary.LittleEndian.PutUint16(stereo8K[offset:], uint16(left))
		binary.LittleEndian.PutUint16(stereo8K[offset+2:], uint16(right))
	}
	if err := resamplingSession.PushAudio(&audiomodel.AudioFrame{
		Data:              stereo8K,
		SampleRate:        8000,
		NumChannels:       2,
		SamplesPerChannel: 800,
	}); err != nil {
		t.Fatalf("PushAudio stereo 8k error = %v, want reference resample/downmix", err)
	}
	select {
	case got := <-resamplingSession.audioCh:
		want := make([]byte, 3200)
		if !bytes.Equal(got, want) {
			t.Fatalf("resampled/downmixed audio bytes = %v, want 16k mono mixed silence", got[:min(len(got), 8)])
		}
	case <-time.After(time.Second):
		t.Fatal("PushAudio did not queue resampled/downmixed chunk")
	}
}

func TestUltravoxRealtimeSessionOutboundQueuePreservesReferenceMessageOrder(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	firstPCM := make([]byte, 3200)
	secondPCM := make([]byte, 3200)
	for i := range firstPCM {
		firstPCM[i] = byte(i % 251)
		secondPCM[i] = byte((i + 17) % 251)
	}
	firstFrame := &audiomodel.AudioFrame{
		Data:              firstPCM,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}
	secondFrame := &audiomodel.AudioFrame{
		Data:              secondPCM,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}

	if err := session.PushAudio(firstFrame); err != nil {
		t.Fatalf("first PushAudio error = %v", err)
	}
	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{
		Instructions:    "respond now",
		InstructionsSet: true,
	}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	if err := session.PushAudio(secondFrame); err != nil {
		t.Fatalf("second PushAudio error = %v", err)
	}

	first := requireUltravoxRealtimeOutbound(t, session)
	if !bytes.Equal(first.Audio, firstPCM) || first.Event != nil {
		t.Fatalf("first outbound = %#v, want first audio bytes", first)
	}
	second := requireUltravoxRealtimeOutbound(t, session)
	if second.Audio != nil || second.Event["type"] != "user_text_message" || second.Event["text"] != "<instruction>respond now</instruction>" {
		t.Fatalf("second outbound = %#v, want reference user_text_message event", second)
	}
	third := requireUltravoxRealtimeOutbound(t, session)
	if !bytes.Equal(third.Audio, secondPCM) || third.Event != nil {
		t.Fatalf("third outbound = %#v, want second audio bytes", third)
	}
}

func TestUltravoxRealtimeOutboundMessageSerializesReferenceFrames(t *testing.T) {
	writer := &ultravoxRealtimeTestWebsocketWriter{}
	audio := []byte{0x01, 0x02, 0x03}
	if err := writeUltravoxRealtimeOutboundMessage(writer, ultravoxRealtimeOutboundMessage{Audio: audio}); err != nil {
		t.Fatalf("write audio outbound error = %v", err)
	}
	event := map[string]any{
		"type":          "user_text_message",
		"text":          "<instruction>respond now</instruction>",
		"deferResponse": false,
	}
	if err := writeUltravoxRealtimeOutboundMessage(writer, ultravoxRealtimeOutboundMessage{Event: event}); err != nil {
		t.Fatalf("write event outbound error = %v", err)
	}

	if len(writer.frames) != 2 {
		t.Fatalf("websocket frame count = %d, want audio then text", len(writer.frames))
	}
	if writer.frames[0].typ != ultravoxRealtimeWebsocketBinaryFrame || !bytes.Equal(writer.frames[0].data, audio) {
		t.Fatalf("first websocket frame = %#v, want binary audio", writer.frames[0])
	}
	if writer.frames[1].typ != ultravoxRealtimeWebsocketTextFrame {
		t.Fatalf("second websocket frame type = %d, want text", writer.frames[1].typ)
	}
	var got map[string]any
	if err := json.Unmarshal(writer.frames[1].data, &got); err != nil {
		t.Fatalf("event websocket JSON = %q failed decode: %v", writer.frames[1].data, err)
	}
	for key, want := range event {
		if got[key] != want {
			t.Fatalf("event JSON %s = %#v, want %#v in %s", key, got[key], want, writer.frames[1].data)
		}
	}
}

func TestUltravoxRealtimeSessionSendTaskWritesReferenceOutboundStream(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)

	writer := &ultravoxRealtimeTestWebsocketWriter{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- session.sendOutboundMessages(writer)
	}()

	pcm := make([]byte, 3200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	if err := session.PushAudio(&audiomodel.AudioFrame{
		Data:              pcm,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{
		Instructions:    "respond now",
		InstructionsSet: true,
	}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("send task error = %v, want nil after channel close", err)
		}
	case <-time.After(time.Second):
		t.Fatal("send task did not exit after session Close")
	}
	if len(writer.frames) != 2 {
		t.Fatalf("websocket frame count = %d, want audio then client event", len(writer.frames))
	}
	if writer.frames[0].typ != ultravoxRealtimeWebsocketBinaryFrame || !bytes.Equal(writer.frames[0].data, pcm) {
		t.Fatalf("first websocket frame = %#v, want binary audio", writer.frames[0])
	}
	if writer.frames[1].typ != ultravoxRealtimeWebsocketTextFrame {
		t.Fatalf("second websocket frame type = %d, want text", writer.frames[1].typ)
	}
	var event map[string]any
	if err := json.Unmarshal(writer.frames[1].data, &event); err != nil {
		t.Fatalf("client event frame JSON = %q failed decode: %v", writer.frames[1].data, err)
	}
	if event["type"] != "user_text_message" || event["text"] != "<instruction>respond now</instruction>" {
		t.Fatalf("client event frame = %#v, want reference user_text_message", event)
	}
}

func TestUltravoxRealtimeSessionSendTaskStopsStaleOutboundAfterReferenceRestart(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	pcm := make([]byte, 3200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	if err := session.PushAudio(&audiomodel.AudioFrame{
		Data:              pcm,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}

	session.mu.Lock()
	oldOutbound := session.outboundCh
	restartCount := session.restartCount
	session.mu.Unlock()
	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}

	writer := &ultravoxRealtimeTestWebsocketWriter{}
	if err := session.sendOutboundMessagesFrom(writer, oldOutbound, restartCount); err != nil {
		t.Fatalf("send old outbound after restart error = %v, want nil", err)
	}
	if len(writer.frames) != 0 {
		t.Fatalf("websocket frames after restart = %#v, want stale old-session outbound dropped", writer.frames)
	}
}

func TestUltravoxRealtimeSessionSendTaskStopsOnReferenceConnectionCancel(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- session.sendOutboundMessagesWithContext(ctx, &ultravoxRealtimeTestWebsocketWriter{})
	}()
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("send task cancel error = %v, want nil like reference cancel_and_wait", err)
		}
	case <-time.After(time.Second):
		t.Fatal("send task did not exit after reference connection cancel")
	}
}

func TestUltravoxRealtimeSessionInputResamplerKeepsReferencePhaseAcrossFrames(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	for sample := 0; sample < 4410; sample++ {
		value := int16(sample % 32767)
		pcm := make([]byte, 2)
		binary.LittleEndian.PutUint16(pcm, uint16(value))
		if err := session.PushAudio(&audiomodel.AudioFrame{
			Data:              pcm,
			SampleRate:        44100,
			NumChannels:       1,
			SamplesPerChannel: 1,
		}); err != nil {
			t.Fatalf("PushAudio split 44.1k sample %d error = %v", sample, err)
		}
	}

	select {
	case got := <-session.audioCh:
		if gotLen, wantLen := len(got), 3200; gotLen != wantLen {
			t.Fatalf("split-frame resampled chunk bytes = %d, want %d from reference stateful resampler", gotLen, wantLen)
		}
	case <-time.After(time.Second):
		t.Fatal("split-frame PushAudio did not queue one reference 100ms chunk")
	}
	select {
	case extra := <-session.audioCh:
		t.Fatalf("extra resampled chunk bytes = %d, want phase preserved without over-emitting", len(extra))
	default:
	}
}

func TestUltravoxRealtimeSessionPushAudioDropsInvalidReferenceFrames(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	invalidFrames := []*audiomodel.AudioFrame{
		{Data: []byte{1}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1},
		{Data: []byte{0, 0}, SampleRate: 16000, NumChannels: 0, SamplesPerChannel: 1},
		{Data: []byte{0, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 2},
	}
	for i, frame := range invalidFrames {
		if err := session.PushAudio(frame); err != nil {
			t.Fatalf("PushAudio invalid frame %d error = %v, want reference drop", i, err)
		}
	}
	select {
	case got := <-session.audioCh:
		t.Fatalf("queued audio after invalid frames length = %d, want no provider audio", len(got))
	default:
	}

	pcm := make([]byte, 3200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	if err := session.PushAudio(&audiomodel.AudioFrame{
		Data:              pcm,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("PushAudio valid after invalid frames error = %v", err)
	}
	select {
	case got := <-session.audioCh:
		if !bytes.Equal(got, pcm) {
			t.Fatalf("queued audio after invalid frames = %v, want later valid PCM", got[:min(len(got), 8)])
		}
	case <-time.After(time.Second):
		t.Fatal("valid PushAudio after invalid frames did not queue reference 100ms chunk")
	}
}

func TestUltravoxRealtimeSessionPushAudioGrowsReferenceQueue(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	for i := 0; i < cap(session.audioCh); i++ {
		session.audioCh <- []byte{byte(i)}
	}

	pcm := make([]byte, 3200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	if err := session.PushAudio(&audiomodel.AudioFrame{
		Data:              pcm,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("PushAudio with full queue error = %v, want reference unbounded queue behavior", err)
	}

	for i := 0; i < 256; i++ {
		<-session.audioCh
	}
	select {
	case got := <-session.audioCh:
		if !bytes.Equal(got, pcm) {
			t.Fatalf("queued audio after full queue = %v, want new PCM chunk", got[:min(len(got), 8)])
		}
	default:
		t.Fatal("new audio missing after full queue, want reference queue growth")
	}
}

func TestUltravoxRealtimeSessionPushVideoIsReferenceNoop(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.PushVideo(&images.VideoFrame{
		Data:   []byte{255, 0, 0, 255},
		Width:  1,
		Height: 1,
		Format: "rgba",
	}); err != nil {
		t.Fatalf("PushVideo error = %v, want reference warning-only no-op", err)
	}
}

func TestUltravoxRealtimeSessionGenerateReplyQueuesReferenceUserTextMessage(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v, want reference user text event", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"deferResponse": false,
	})

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{
		Instructions:    "answer briefly",
		InstructionsSet: true,
	}); err != nil {
		t.Fatalf("GenerateReply with instructions error = %v, want reference instruction event", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "<instruction>answer briefly</instruction>",
		"deferResponse": false,
	})

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply after Close error = %v, want reference no-op", err)
	}
}

func TestUltravoxRealtimeSessionGenerateReplyBuffersBeyondOldClientEventLimit(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	fullCap := cap(session.clientEventCh)
	if fullCap == 0 {
		t.Fatal("client event queue cap = 0, want buffered reference queue")
	}
	for i := 0; i < fullCap; i++ {
		session.clientEventCh <- map[string]any{"type": "queued"}
	}

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v, want reference unbounded client event queue growth", err)
	}
	for i := 0; i < fullCap; i++ {
		<-session.clientEventCh
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"deferResponse": false,
	})
}

func TestUltravoxRealtimeSessionGenerateReplyMarksReferenceUserInitiatedGeneration(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"deferResponse": false,
	})

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	userGeneration := requireUltravoxRealtimeGeneration(t, session)
	if !userGeneration.UserInitiated {
		t.Fatal("generation UserInitiated = false, want true for GenerateReply response")
	}
	requireUltravoxRealtimeMessage(t, userGeneration)
	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{Role: "agent", Text: "done", Final: true, Ordinal: 1})

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	providerGeneration := requireUltravoxRealtimeGeneration(t, session)
	if providerGeneration.UserInitiated {
		t.Fatal("next generation UserInitiated = true, want pending GenerateReply consumed once")
	}
}

func TestUltravoxRealtimeSessionGenerateReplySpeakingConsumesReferenceActiveGenerationPending(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	activeGeneration := requireUltravoxRealtimeGeneration(t, session)
	if activeGeneration.UserInitiated {
		t.Fatal("active generation UserInitiated = true, want provider-started setup")
	}
	requireUltravoxRealtimeMessage(t, activeGeneration)

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"deferResponse": false,
	})

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "speaking"})
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeSpeechStopped {
			t.Fatalf("event type = %s, want speech_stopped", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for speech_stopped")
	}

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "listening"})
	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	nextGeneration := requireUltravoxRealtimeGeneration(t, session)
	if nextGeneration.UserInitiated {
		t.Fatal("next generation UserInitiated = true, want speaking event to consume active-generation pending reply")
	}
}

func TestUltravoxRealtimeSessionGenerateReplyExpiresReferencePendingOwner(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"deferResponse": false,
	})

	session.mu.Lock()
	session.pendingReplyAt = time.Now().Add(-ultravoxGenerateReplyTimeout - time.Millisecond)
	session.mu.Unlock()

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	if generation.UserInitiated {
		t.Fatal("generation UserInitiated = true after reference GenerateReply timeout, want provider-started generation")
	}
}

func TestUltravoxRealtimeSessionGenerateReplyTimerClearsReferencePendingOwner(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	session.generateReplyTimeout = 10 * time.Millisecond
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"deferResponse": false,
	})

	requireUltravoxRealtimePendingReplyCleared(t, session)

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	if generation.UserInitiated {
		t.Fatal("generation UserInitiated = true after reference GenerateReply timer, want provider-started generation")
	}
}

func TestUltravoxRealtimeSessionRestartClearsReferencePendingGenerateReply(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeSystemPrompt("stay concise"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"deferResponse": false,
	})

	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	if generation.UserInitiated {
		t.Fatal("generation UserInitiated = true, want restart to cancel pending GenerateReply")
	}
}

func TestUltravoxRealtimeSessionRestartDropsReferenceQueuedClientEvents(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeSystemPrompt("stay concise"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}

	select {
	case event := <-session.clientEventCh:
		t.Fatalf("queued client event after restart = %#v, want reference old message channel dropped", event)
	default:
	}
}

func TestUltravoxRealtimeSessionRestartDropsReferenceQueuedAudio(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeSystemPrompt("stay concise"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	pcm := make([]byte, 3200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	if err := session.PushAudio(&audiomodel.AudioFrame{
		Data:              pcm,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}

	select {
	case audio := <-session.audioCh:
		t.Fatalf("queued audio after restart length = %d, want reference old message channel dropped", len(audio))
	default:
	}
}

func TestUltravoxRealtimeSessionTruncateIsReferenceNoop(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.Truncate(llm.RealtimeTruncateOptions{
		MessageID:      "msg-1",
		Modalities:     []string{"audio"},
		AudioEndMillis: 120,
	}); err != nil {
		t.Fatalf("Truncate error = %v, want reference no-op", err)
	}
}

func TestUltravoxRealtimeSessionSayReportsReferenceUnsupported(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	err = session.Say("hello")
	if err == nil {
		t.Fatal("Say error = nil, want reference unsupported direct-speech error")
	}
	want := "*ultravox.realtimeSession does not implement say(). use a TTS model instead"
	if err.Error() != want {
		t.Fatalf("Say error = %q, want %q", err.Error(), want)
	}
}

func TestUltravoxRealtimeSessionInterruptSendsReferenceBargeIn(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt without active generation error = %v, want reference no-op", err)
	}
	select {
	case event := <-session.clientEventCh:
		t.Fatalf("barge-in event without active generation = %#v", event)
	default:
	}

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hello",
		Final:   false,
		Ordinal: 1,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hello")

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt active generation error = %v, want reference barge-in", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"urgency":       "immediate",
		"deferResponse": true,
	})
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionInterruptBuffersFullReferenceClientQueue(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hello",
		Final:   false,
		Ordinal: 1,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hello")

	for i := 0; i < cap(session.clientEventCh); i++ {
		session.clientEventCh <- map[string]any{"type": "queued"}
	}

	oldCap := cap(session.clientEventCh)
	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v, want reference unbounded client event queue growth", err)
	}
	for i := 0; i < oldCap; i++ {
		<-session.clientEventCh
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"urgency":       "immediate",
		"deferResponse": true,
	})
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionOutputAudioStartsReferenceGeneration(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	audio := make([]byte, 960)
	for i := range audio {
		audio[i] = byte(i % 251)
	}
	session.handleOutputAudio(audio)

	var generation *llm.GenerationCreatedEvent
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeGenerationCreated {
			t.Fatalf("event type = %s, want generation_created", event.Type)
		}
		generation = event.Generation
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generation_created")
	}
	if generation == nil {
		t.Fatal("generation = nil")
	}

	var message llm.MessageGeneration
	select {
	case message = <-generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	select {
	case got := <-message.AudioCh:
		if got.SampleRate != 24000 || got.NumChannels != 1 || got.SamplesPerChannel != 480 {
			t.Fatalf("audio frame shape = rate %d channels %d samples %d, want 24000/1/480", got.SampleRate, got.NumChannels, got.SamplesPerChannel)
		}
		if !bytes.Equal(got.Data, audio) {
			t.Fatalf("audio data = %v, want original output bytes", got.Data[:min(len(got.Data), 8)])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for output audio frame")
	}
}

func TestUltravoxRealtimeSessionOutputAudioBuffersBeyondOldDropLimit(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "speaking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	session.mu.Lock()
	generationState := session.generation
	session.mu.Unlock()
	if generationState == nil {
		t.Fatal("session generation = nil")
	}

	const oldDropLimit = 16
	if cap(generationState.audioCh) <= oldDropLimit {
		t.Fatalf("output audio queue cap = %d, want above old 16-frame drop limit", cap(generationState.audioCh))
	}
	for i := 0; i < oldDropLimit; i++ {
		generationState.audioCh <- &audiomodel.AudioFrame{Data: []byte{byte(i)}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}
	}

	audio := make([]byte, 960)
	for i := range audio {
		audio[i] = byte(i % 251)
	}
	session.handleOutputAudio(audio)

	for i := 0; i < oldDropLimit; i++ {
		<-message.AudioCh
	}
	select {
	case got := <-message.AudioCh:
		if !bytes.Equal(got.Data, audio) {
			t.Fatalf("queued output audio = %v, want reference-preserved provider bytes", got.Data[:min(len(got.Data), 8)])
		}
	default:
		t.Fatal("new output audio missing after old 16-frame queue limit, want reference buffering")
	}
}

func TestUltravoxRealtimeSessionForwardsReferenceOddAndEmptyOutputAudio(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	audio := []byte{1, 2, 3}
	session.handleOutputAudio(audio)
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	select {
	case got := <-message.AudioCh:
		if got.SampleRate != 24000 || got.NumChannels != 1 || got.SamplesPerChannel != 1 {
			t.Fatalf("odd audio frame shape = rate %d channels %d samples %d, want reference 24000/1/1", got.SampleRate, got.NumChannels, got.SamplesPerChannel)
		}
		if len(got.Data) != len(audio) {
			t.Fatalf("odd output audio length = %d, want preserved provider byte length %d", len(got.Data), len(audio))
		}
		if !bytes.Equal(got.Data, audio) {
			t.Fatalf("odd output audio = %v, want reference-preserved provider bytes %v", got.Data, audio)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reference odd-sized output audio")
	}

	session.handleOutputAudio(nil)
	select {
	case got := <-message.AudioCh:
		if got.SampleRate != 24000 || got.NumChannels != 1 || got.SamplesPerChannel != 0 || len(got.Data) != 0 {
			t.Fatalf("empty audio frame = rate %d channels %d samples %d len %d, want reference 24000/1/0/0", got.SampleRate, got.NumChannels, got.SamplesPerChannel, len(got.Data))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reference empty output audio")
	}
}

func TestUltravoxRealtimeSessionGenerationMessageExposesReferenceModalities(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outputMedium string
		want         []string
	}{
		{name: "voice", outputMedium: "voice", want: []string{"audio", "text"}},
		{name: "text", outputMedium: "text", want: []string{"text"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, err := NewRealtimeModel("test-key", WithRealtimeOutputMedium(tc.outputMedium))
			if err != nil {
				t.Fatalf("NewRealtimeModel error = %v", err)
			}
			sessionInterface, err := model.Session()
			if err != nil {
				t.Fatalf("Session error = %v", err)
			}
			session := sessionInterface.(*realtimeSession)
			defer session.Close()
			if tc.outputMedium == "text" {
				requireUltravoxRealtimeClientEvent(t, session, map[string]any{
					"type":   "set_output_medium",
					"medium": "text",
				})
			}

			session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
			generation := requireUltravoxRealtimeGeneration(t, session)
			message := requireUltravoxRealtimeMessage(t, generation)
			requireUltravoxRealtimeModalities(t, message.ModalitiesCh, tc.want)
		})
	}
}

func TestUltravoxRealtimeSessionOutputMediumUpdateKeepsReferenceModalities(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{
		OutputMedium:    "text",
		OutputMediumSet: true,
	}); err != nil {
		t.Fatalf("UpdateOptions text error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})
	if got := model.OutputMedium(); got != "voice" {
		t.Fatalf("model output medium = %q, want unchanged reference capability owner", got)
	}

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	textGeneration := requireUltravoxRealtimeGeneration(t, session)
	textMessage := requireUltravoxRealtimeMessage(t, textGeneration)
	requireUltravoxRealtimeModalities(t, textMessage.ModalitiesCh, []string{"audio", "text"})
	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "listening"})
	requireUltravoxRealtimeClosedText(t, textMessage.TextCh)

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{
		OutputMedium:    "voice",
		OutputMediumSet: true,
	}); err != nil {
		t.Fatalf("UpdateOptions voice error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "voice",
	})

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	voiceGeneration := requireUltravoxRealtimeGeneration(t, session)
	voiceMessage := requireUltravoxRealtimeMessage(t, voiceGeneration)
	requireUltravoxRealtimeModalities(t, voiceMessage.ModalitiesCh, []string{"audio", "text"})
}

func TestUltravoxRealtimeSessionOutputMediumQueueGrowthKeepsReferenceModalities(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	fullCap := cap(session.clientEventCh)
	for i := 0; i < fullCap; i++ {
		session.clientEventCh <- map[string]any{"type": "queued"}
	}

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{
		OutputMedium:    "text",
		OutputMediumSet: true,
	}); err != nil {
		t.Fatalf("UpdateOptions error = %v, want reference unbounded client event queue growth", err)
	}
	for i := 0; i < fullCap; i++ {
		<-session.clientEventCh
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeModalities(t, message.ModalitiesCh, []string{"audio", "text"})
}

func TestUltravoxRealtimeSessionUserTranscriptEmitsReferenceFinality(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "user",
		Text:    "hello",
		Final:   false,
		Ordinal: 7,
	})
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_7", "hello", false)

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "user",
		Text:    "hello world",
		Final:   true,
		Ordinal: 7,
	})
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_7", "hello world", true)
}

func TestUltravoxRealtimeSessionFinalUserTranscriptMarksReferenceChatContext(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleUserTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "user",
		Text:    "hello world",
		Final:   true,
		Ordinal: 7,
	})
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_7", "hello world", true)

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{ID: "msg_user_7", Role: llm.ChatRoleUser, Text: "hello world"})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want reference duplicate transcript no-op", err)
	}
	select {
	case event := <-session.clientEventCh:
		t.Fatalf("duplicate transcript context event = %#v, want no user_text_message", event)
	default:
	}
}

func TestUltravoxRealtimeSessionUserTranscriptBuffersBeyondOldDropLimit(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	const oldDropLimit = 16
	if cap(session.eventCh) <= oldDropLimit {
		t.Fatalf("event queue cap = %d, want above old 16-event drop limit", cap(session.eventCh))
	}
	for i := 0; i < oldDropLimit; i++ {
		session.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeText}
	}

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "user",
		Text:    "final words",
		Final:   true,
		Ordinal: 8,
	})

	for i := 0; i < oldDropLimit; i++ {
		<-session.eventCh
	}
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_8", "final words", true)
}

func TestUltravoxRealtimeSessionAgentTranscriptStreamsReferenceDeltas(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hel",
		Final:   false,
		Ordinal: 2,
	})

	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hel")

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Text:    "hello",
		Final:   true,
		Ordinal: 2,
	})
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionAgentTranscriptBuffersBeyondOldDropLimit(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	session.mu.Lock()
	generationState := session.generation
	session.mu.Unlock()
	if generationState == nil {
		t.Fatal("session generation = nil")
	}

	const oldDropLimit = 16
	if cap(generationState.textCh) <= oldDropLimit {
		t.Fatalf("agent text queue cap = %d, want above old 16-delta drop limit", cap(generationState.textCh))
	}
	for i := 0; i < oldDropLimit; i++ {
		generationState.textCh <- "queued"
	}

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{Role: "agent", Delta: "new delta", Final: false, Ordinal: 2})

	for i := 0; i < oldDropLimit; i++ {
		<-message.TextCh
	}
	select {
	case got := <-message.TextCh:
		if got != "new delta" {
			t.Fatalf("agent text delta = %q, want reference-preserved delta", got)
		}
	default:
		t.Fatal("agent text delta missing after old 16-delta queue limit, want reference buffering")
	}
}

func TestUltravoxRealtimeSessionAgentTranscriptFinalEmitsReferenceMetrics(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hel",
		Final:   false,
		Ordinal: 2,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hel")

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Text:    "hello",
		Final:   true,
		Ordinal: 2,
	})
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	metrics := requireUltravoxRealtimeMetrics(t, session)
	if metrics.RequestID != generation.ResponseID || metrics.Cancelled {
		t.Fatalf("metrics identity = %#v, want completed generation %q", metrics, generation.ResponseID)
	}
	if metrics.Label != "ultravox-fixie-ai/ultravox" || metrics.Metadata == nil ||
		metrics.Metadata.ModelName != "fixie-ai/ultravox" || metrics.Metadata.ModelProvider != "Ultravox" {
		t.Fatalf("metrics metadata = %#v, want Ultravox model metadata", metrics)
	}
	if metrics.TTFT < 0 || metrics.Duration < metrics.TTFT {
		t.Fatalf("metrics timing = ttft %f duration %f, want non-negative reference timing", metrics.TTFT, metrics.Duration)
	}
}

func TestUltravoxRealtimeSessionMetricsAnchorToRecentReferenceUserFinal(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "user",
		Text:    "done",
		Final:   true,
		Ordinal: 4,
	})
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_4", "done", true)
	time.Sleep(25 * time.Millisecond)

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "ok",
		Final:   false,
		Ordinal: 5,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "ok")

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Text:    "ok",
		Final:   true,
		Ordinal: 5,
	})
	metrics := requireUltravoxRealtimeMetrics(t, session)
	if metrics.TTFT < 0.01 {
		t.Fatalf("metrics TTFT = %f, want anchored to recent user final transcript", metrics.TTFT)
	}
}

func TestUltravoxRealtimeSessionInterruptEmitsReferenceCancelledMetrics(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hello",
		Final:   false,
		Ordinal: 1,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hello")

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"urgency":       "immediate",
		"deferResponse": true,
	})
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	metrics := requireUltravoxRealtimeMetrics(t, session)
	if metrics.RequestID != generation.ResponseID || !metrics.Cancelled {
		t.Fatalf("metrics identity = %#v, want cancelled generation %q", metrics, generation.ResponseID)
	}
}

func TestUltravoxRealtimeSessionToolOnlyGenerationSuppressesReferenceMetrics(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleToolInvocationEvent(ultravoxRealtimeToolInvocationEvent{
		ToolName:     "lookup",
		InvocationID: "call-7",
		Parameters:   map[string]any{"city": "Paris"},
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	select {
	case <-generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool function call")
	}
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	assertNoUltravoxRealtimeMetrics(t, session)
}

func TestUltravoxRealtimeSessionGenerationCreatedDoesNotBlockProviderReceive(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	for i := 0; i < cap(session.eventCh); i++ {
		session.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeText}
	}

	done := make(chan struct{})
	go func() {
		session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
			Role:    "agent",
			Delta:   "hello",
			Final:   false,
			Ordinal: 1,
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		<-session.eventCh
		<-done
		t.Fatal("agent transcript handler blocked on full generation_created event buffer")
	}
}

func TestUltravoxRealtimeSessionGenerationsUseReferenceUniqueMessageIDs(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	firstGeneration := requireUltravoxRealtimeGeneration(t, session)
	firstMessage := requireUltravoxRealtimeMessage(t, firstGeneration)
	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{Role: "agent", Text: "done", Final: true, Ordinal: 1})
	requireUltravoxRealtimeClosedText(t, firstMessage.TextCh)

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	secondGeneration := requireUltravoxRealtimeGeneration(t, session)
	secondMessage := requireUltravoxRealtimeMessage(t, secondGeneration)

	if !strings.HasPrefix(firstMessage.MessageID, "ultravox-turn-") ||
		!strings.HasPrefix(secondMessage.MessageID, "ultravox-turn-") {
		t.Fatalf("message IDs = %q/%q, want reference ultravox-turn-* prefix", firstMessage.MessageID, secondMessage.MessageID)
	}
	if firstMessage.MessageID == secondMessage.MessageID {
		t.Fatalf("message IDs both %q, want unique reference turn IDs", firstMessage.MessageID)
	}
}

func TestUltravoxRealtimeSessionGenerationCreatedCarriesReferenceMetadata(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)

	if generation.ResponseID != message.MessageID {
		t.Fatalf("generation response id = %q, want message id %q", generation.ResponseID, message.MessageID)
	}
	if generation.UserInitiated {
		t.Fatal("generation UserInitiated = true, want false for provider-started generation")
	}
}

func TestUltravoxRealtimeSessionStateEventsMatchReferenceTurnLifecycle(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "speaking"})
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeSpeechStopped {
			t.Fatalf("event type = %s, want speech_stopped", event.Type)
		}
		if event.SpeechStopped == nil || event.SpeechStopped.UserTranscriptionEnabled {
			t.Fatalf("SpeechStopped = %+v, want user transcription disabled", event.SpeechStopped)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for speech_stopped")
	}

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "listening"})
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionToolInvocationEmitsReferenceFunctionCall(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleToolInvocationEvent(ultravoxRealtimeToolInvocationEvent{
		ToolName:     "lookup",
		InvocationID: "call-7",
		Parameters:   map[string]any{"city": "Paris"},
	})

	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	select {
	case call := <-generation.FunctionCh:
		if call == nil {
			t.Fatal("function call = nil")
		}
		if call.CallID != "call-7" || call.Name != "lookup" || call.Arguments != `{"city": "Paris"}` {
			t.Fatalf("function call = %+v, want call-7 lookup JSON args", call)
		}
		if strings.Contains(call.Arguments, `":"`) {
			t.Fatalf("function call arguments = %q, want reference json.dumps spacing", call.Arguments)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function call")
	}
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionToolInvocationAcceptsReferenceEmptyStringFields(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.handleServerTextMessage([]byte(`{"type":"client_tool_invocation","toolName":"","invocationId":"","parameters":{}}`)); err != nil {
		t.Fatalf("handle empty-string tool JSON error = %v", err)
	}

	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	select {
	case call := <-generation.FunctionCh:
		if call == nil {
			t.Fatal("function call = nil")
		}
		if call.CallID != "" || call.Name != "" || call.Arguments != `{}` {
			t.Fatalf("function call = %+v, want reference empty string fields and empty JSON args", call)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for empty-string function call")
	}
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionToolInvocationBuffersBeyondOldDropLimit(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	requireUltravoxRealtimeMessage(t, generation)
	session.mu.Lock()
	generationState := session.generation
	session.mu.Unlock()
	if generationState == nil {
		t.Fatal("session generation = nil")
	}

	const oldDropLimit = 1
	if cap(generationState.functionCh) <= oldDropLimit {
		t.Fatalf("function queue cap = %d, want above old 1-call drop limit", cap(generationState.functionCh))
	}
	generationState.functionCh <- &llm.FunctionCall{CallID: "queued", Name: "queued", Arguments: "{}"}

	session.handleToolInvocationEvent(ultravoxRealtimeToolInvocationEvent{
		ToolName:     "lookup",
		InvocationID: "call-buffered",
		Parameters:   map[string]any{"city": "Paris"},
	})

	<-generation.FunctionCh
	select {
	case call := <-generation.FunctionCh:
		if call == nil {
			t.Fatal("function call = nil")
		}
		if call.CallID != "call-buffered" || call.Name != "lookup" || call.Arguments != `{"city": "Paris"}` {
			t.Fatalf("function call = %+v, want buffered lookup call", call)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for buffered function call")
	}
}

func TestUltravoxRealtimeSessionToolInvocationPreservesReferenceArgumentOrder(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.handleServerTextMessage([]byte(`{"type":"client_tool_invocation","toolName":"lookup","invocationId":"call-ordered","parameters":{"z":1,"a":{"b":2},"list":[3,4]}}`)); err != nil {
		t.Fatalf("handle tool JSON error = %v", err)
	}

	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	select {
	case call := <-generation.FunctionCh:
		if call == nil {
			t.Fatal("function call = nil")
		}
		want := `{"z": 1, "a": {"b": 2}, "list": [3, 4]}`
		if call.Arguments != want {
			t.Fatalf("function call arguments = %q, want reference json.dumps order %q", call.Arguments, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function call")
	}
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionToolInvocationEscapesReferenceUnicodeArguments(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.handleServerTextMessage([]byte(`{"type":"client_tool_invocation","toolName":"lookup","invocationId":"call-unicode","parameters":{"word":"café","drink":"☕"}}`)); err != nil {
		t.Fatalf("handle unicode tool JSON error = %v", err)
	}

	generation := requireUltravoxRealtimeGeneration(t, session)
	select {
	case call := <-generation.FunctionCh:
		want := `{"word": "caf\u00e9", "drink": "\u2615"}`
		if call.Arguments != want {
			t.Fatalf("function call arguments = %q, want Python json.dumps unicode escaping %q", call.Arguments, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for unicode function call")
	}
}

func TestUltravoxRealtimeSessionToolInvocationNormalizesReferenceEscapedUnicodeArguments(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.handleServerTextMessage([]byte(`{"type":"client_tool_invocation","toolName":"lookup","invocationId":"call-escaped-unicode","parameters":{"word":"caf\u00E9","emoji":"\uD83D\uDE00"}}`)); err != nil {
		t.Fatalf("handle escaped unicode tool JSON error = %v", err)
	}

	generation := requireUltravoxRealtimeGeneration(t, session)
	select {
	case call := <-generation.FunctionCh:
		want := `{"word": "caf\u00e9", "emoji": "\ud83d\ude00"}`
		if call.Arguments != want {
			t.Fatalf("function call arguments = %q, want Python json.dumps normalized unicode escaping %q", call.Arguments, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for escaped unicode function call")
	}
}

func TestUltravoxRealtimeSessionToolInvocationDoesNotConsumeReferencePendingGenerateReply(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"deferResponse": false,
	})

	session.handleToolInvocationEvent(ultravoxRealtimeToolInvocationEvent{
		ToolName:     "lookup",
		InvocationID: "call-7",
		Parameters:   map[string]any{"city": "Paris"},
	})
	toolGeneration := requireUltravoxRealtimeGeneration(t, session)
	if toolGeneration.UserInitiated {
		t.Fatal("tool generation UserInitiated = true, want false for tool-only placeholder")
	}
	requireUltravoxRealtimeMessage(t, toolGeneration)

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	replyGeneration := requireUltravoxRealtimeGeneration(t, session)
	if !replyGeneration.UserInitiated {
		t.Fatal("reply generation UserInitiated = false, want pending GenerateReply preserved")
	}
}

func TestUltravoxRealtimeSessionToolResultQueuesReferenceClientEvent(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		ID:     "result-1",
		CallID: "call-7",
		Name:   "lookup",
		Output: "Paris",
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want reference tool result event", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "client_tool_result",
		"invocationId":  "call-7",
		"result":        "Paris",
		"agentReaction": "speaks",
		"responseType":  "tool-response",
	})

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("second UpdateChatContext error = %v", err)
	}
	select {
	case got := <-session.clientEventCh:
		t.Fatalf("duplicate tool result event = %#v", got)
	default:
	}
}

func TestUltravoxRealtimeSessionToolResultAcceptsReferenceEmptyInvocationID(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		ID:     "result-empty",
		CallID: "",
		Name:   "lookup",
		Output: "ok",
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want reference empty invocationId result event", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "client_tool_result",
		"invocationId":  "",
		"result":        "ok",
		"agentReaction": "speaks",
		"responseType":  "tool-response",
	})
}

func TestUltravoxRealtimeSessionToolErrorResultQueuesReferenceClientEvent(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		ID:      "result-err",
		CallID:  "call-err",
		Name:    "lookup",
		Output:  "database unavailable",
		IsError: true,
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want reference tool error result event", err)
	}
	select {
	case got := <-session.clientEventCh:
		if got["type"] != "client_tool_result" ||
			got["invocationId"] != "call-err" ||
			got["agentReaction"] != "speaks" ||
			got["responseType"] != "tool-response" ||
			got["errorType"] != "implementation-error" ||
			got["errorMessage"] != "database unavailable" {
			t.Fatalf("tool error event = %#v, want reference error fields", got)
		}
		if _, ok := got["result"]; ok {
			t.Fatalf("tool error event result = %#v, want no result field", got["result"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool error event")
	}
}

func TestUltravoxRealtimeSessionUpdateChatContextQueuesReferenceDeferredMessages(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{ID: "sys", Role: llm.ChatRoleSystem, Text: "be concise"})
	ctx.AddMessage(llm.ChatMessageArgs{ID: "user", Role: llm.ChatRoleUser, Text: "remember Paris"})
	ctx.AddMessage(llm.ChatMessageArgs{ID: "assistant", Role: llm.ChatRoleAssistant, Text: "managed by provider"})

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want reference deferred messages", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "<instruction>be concise</instruction>",
		"deferResponse": true,
	})
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "remember Paris",
		"deferResponse": true,
	})
	select {
	case got := <-session.clientEventCh:
		t.Fatalf("unexpected assistant/duplicate context event = %#v", got)
	default:
	}

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("second UpdateChatContext error = %v", err)
	}
	select {
	case got := <-session.clientEventCh:
		t.Fatalf("duplicate context event = %#v", got)
	default:
	}
}

func TestUltravoxRealtimeSessionUpdateChatContextResendsReferenceReaddedItems(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{ID: "memo", Role: llm.ChatRoleUser, Text: "remember Paris"})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext initial error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "remember Paris",
		"deferResponse": true,
	})

	if err := session.UpdateChatContext(llm.NewChatContext()); err != nil {
		t.Fatalf("UpdateChatContext empty error = %v", err)
	}
	select {
	case got := <-session.clientEventCh:
		t.Fatalf("unexpected event for deletion-only context update = %#v", got)
	default:
	}

	readded := llm.NewChatContext()
	readded.AddMessage(llm.ChatMessageArgs{ID: "memo", Role: llm.ChatRoleUser, Text: "remember Paris"})
	if err := session.UpdateChatContext(readded); err != nil {
		t.Fatalf("UpdateChatContext readd error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "remember Paris",
		"deferResponse": true,
	})
}

func TestUltravoxRealtimeSessionUpdateChatContextBuffersFullReferenceClientQueue(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	fullCap := cap(session.clientEventCh)
	for i := 0; i < fullCap; i++ {
		session.clientEventCh <- map[string]any{"type": "filler"}
	}

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{ID: "memo", Role: llm.ChatRoleUser, Text: "remember Paris"})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want reference unbounded client event queue growth", err)
	}
	for i := 0; i < fullCap; i++ {
		<-session.clientEventCh
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "remember Paris",
		"deferResponse": true,
	})
}

func TestUltravoxRealtimeSessionPlaybackClearBufferEmitsReferenceSpeechStarted(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handlePlaybackClearBufferEvent()

	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeSpeechStarted {
			t.Fatalf("event type = %s, want speech_started", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for speech_started")
	}
}

func TestUltravoxRealtimeSessionServerJSONDispatchesReferenceEvents(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.handleServerTextMessage([]byte(`{"type":"transcript","role":"user","medium":"voice","text":"hello","final":true,"ordinal":4}`)); err != nil {
		t.Fatalf("handle transcript JSON error = %v", err)
	}
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_4", "hello", true)

	if err := session.handleServerTextMessage([]byte(`{"type":"state","state":"thinking"}`)); err != nil {
		t.Fatalf("handle state JSON error = %v", err)
	}
	generation := requireUltravoxRealtimeGeneration(t, session)

	if err := session.handleServerTextMessage([]byte(`{"type":"client_tool_invocation","toolName":"lookup","invocationId":"call-9","parameters":{"city":"Paris"}}`)); err != nil {
		t.Fatalf("handle tool JSON error = %v", err)
	}
	select {
	case call := <-generation.FunctionCh:
		if call == nil {
			t.Fatal("function call = nil")
		}
		if call.CallID != "call-9" || call.Name != "lookup" || call.Arguments != `{"city": "Paris"}` {
			t.Fatalf("function call = %+v, want call-9 lookup JSON args", call)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dispatched function call")
	}

	if err := session.handleServerTextMessage([]byte(`{"type":"playback_clear_buffer"}`)); err != nil {
		t.Fatalf("handle playback clear JSON error = %v", err)
	}
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeSpeechStarted {
			t.Fatalf("event type = %s, want speech_started", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for speech_started")
	}
}

func TestUltravoxRealtimeSessionReceiveTaskDispatchesReferenceFrames(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	audio := []byte{1, 0, 2, 0}
	conn := &ultravoxRealtimeTestWebsocketConn{
		readMessages: []ultravoxRealtimeTestWebsocketFrame{
			{typ: ultravoxRealtimeWebsocketTextFrame, data: []byte(`{"type":"transcript","role":"user","medium":"voice","text":"hello","final":true,"ordinal":8}`)},
			{typ: ultravoxRealtimeWebsocketBinaryFrame, data: audio},
		},
		readErr: context.Canceled,
	}

	if err := session.receiveRealtimeMessages(conn); err != nil {
		t.Fatalf("receiveRealtimeMessages error = %v, want nil after reference receive loop drains test frames", err)
	}
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_8", "hello", true)
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	select {
	case got := <-message.AudioCh:
		if !bytes.Equal(got.Data, audio) {
			t.Fatalf("received audio bytes = %v, want websocket binary payload", got.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket binary audio frame")
	}
}

func TestUltravoxRealtimeSessionReceiveTaskStopsStaleFramesAfterReferenceRestart(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.mu.Lock()
	restartCount := session.restartCount
	session.mu.Unlock()
	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	conn := &ultravoxRealtimeTestWebsocketConn{
		readMessages: []ultravoxRealtimeTestWebsocketFrame{
			{typ: ultravoxRealtimeWebsocketTextFrame, data: []byte(`{"type":"transcript","role":"user","medium":"voice","text":"stale","final":true,"ordinal":9}`)},
		},
		readErr: context.Canceled,
	}

	if err := session.receiveRealtimeMessagesFrom(conn, restartCount); err != nil {
		t.Fatalf("receive old frames after restart error = %v, want nil", err)
	}
	select {
	case event := <-session.EventCh():
		t.Fatalf("event after restart stale receive = %#v, want old websocket frame ignored", event)
	default:
	}
}

func TestUltravoxRealtimeSessionReceiveTaskUnexpectedCloseReturnsReferenceError(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	conn := &ultravoxRealtimeTestWebsocketConn{
		readErr: &websocket.CloseError{Code: websocket.CloseNormalClosure, Text: "provider closed"},
	}
	err = session.receiveRealtimeMessages(conn)
	if err == nil || err.Error() != "Ultravox S2S connection closed unexpectedly" {
		t.Fatalf("receive close error = %v, want reference unexpected close error", err)
	}

	conn = &ultravoxRealtimeTestWebsocketConn{readErr: io.EOF}
	err = session.receiveRealtimeMessages(conn)
	if err == nil || err.Error() != "Ultravox S2S connection closed unexpectedly" {
		t.Fatalf("receive EOF error = %v, want reference unexpected close error", err)
	}
}

func TestUltravoxRealtimeSessionRunConnectionClosesReferenceWebsocket(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	conn := &ultravoxRealtimeTestWebsocketConn{readErr: context.Canceled}
	if err := session.runRealtimeConnection(conn); err != nil {
		t.Fatalf("runRealtimeConnection error = %v, want nil after receive loop exits", err)
	}
	if conn.closeCount != 1 {
		t.Fatalf("websocket close count = %d, want reference close in finally", conn.closeCount)
	}
}

func TestUltravoxRealtimeSessionRunConnectionStartsReferenceSendTask(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	pcm := make([]byte, 3200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	if err := session.PushAudio(&audiomodel.AudioFrame{
		Data:              pcm,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}

	readBlock := make(chan struct{})
	writeCh := make(chan struct{}, 1)
	conn := &ultravoxRealtimeTestWebsocketConn{
		ultravoxRealtimeTestWebsocketWriter: ultravoxRealtimeTestWebsocketWriter{writeCh: writeCh},
		readBlock:                           readBlock,
		readErr:                             context.Canceled,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- session.runRealtimeConnection(conn)
	}()

	select {
	case <-writeCh:
	case <-time.After(time.Second):
		t.Fatal("connection runner did not start reference send task")
	}
	close(readBlock)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runRealtimeConnection error = %v, want nil after receive loop exits", err)
		}
	case <-time.After(time.Second):
		t.Fatal("connection runner did not exit after receive loop completed")
	}
	if len(conn.frames) != 1 || conn.frames[0].typ != ultravoxRealtimeWebsocketBinaryFrame || !bytes.Equal(conn.frames[0].data, pcm) {
		t.Fatalf("websocket frames = %#v, want queued audio sent as binary frame", conn.frames)
	}
}

func TestUltravoxRealtimeSessionRunOnceConnectsAndRunsReferenceConnection(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeBaseURL("https://ultravox.example/api/"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	pcm := make([]byte, 3200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	if err := session.PushAudio(&audiomodel.AudioFrame{
		Data:              pcm,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}

	doer := &ultravoxRealtimeTestHTTPDoer{
		responseStatus: http.StatusOK,
		responseBody:   `{"joinUrl":"wss://ultravox.example/join"}`,
	}
	readBlock := make(chan struct{})
	writeCh := make(chan struct{}, 1)
	conn := &ultravoxRealtimeTestWebsocketConn{
		ultravoxRealtimeTestWebsocketWriter: ultravoxRealtimeTestWebsocketWriter{writeCh: writeCh},
		readBlock:                           readBlock,
		readErr:                             context.Canceled,
	}
	var gotEndpoint string
	model.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (ultravoxRealtimeWebsocketConn, error) {
		gotEndpoint = endpoint
		if len(headers) != 0 {
			t.Fatalf("websocket headers = %#v, want none like reference ws_connect(join_url)", headers)
		}
		return conn, nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.runRealtimeOnce(context.Background(), doer)
	}()

	select {
	case <-writeCh:
	case <-time.After(time.Second):
		t.Fatal("run once did not start reference send task after websocket connect")
	}
	close(readBlock)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runRealtimeOnce error = %v, want nil after receive loop exits", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run once did not exit after reference receive task completed")
	}
	if doer.request == nil || doer.request.Method != http.MethodPost {
		t.Fatalf("create-call request = %#v, want POST before websocket dial", doer.request)
	}
	if gotEndpoint != "wss://ultravox.example/join" {
		t.Fatalf("websocket endpoint = %q, want provider joinUrl", gotEndpoint)
	}
	if conn.closeCount != 1 {
		t.Fatalf("websocket close count = %d, want reference close in finally", conn.closeCount)
	}
	if len(conn.frames) != 1 || conn.frames[0].typ != ultravoxRealtimeWebsocketBinaryFrame || !bytes.Equal(conn.frames[0].data, pcm) {
		t.Fatalf("websocket frames = %#v, want queued audio sent as binary frame", conn.frames)
	}
}

func TestUltravoxRealtimeSessionRestartLoopReconnectsAfterReferenceRestartSignal(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeBaseURL("https://ultravox.example/api/"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	doer := &ultravoxRealtimeTestHTTPDoer{
		responseStatus: http.StatusOK,
		responseBody:   `{"joinUrl":"wss://ultravox.example/join"}`,
	}
	firstReadBlock := make(chan struct{})
	firstConn := &ultravoxRealtimeTestWebsocketConn{
		readBlock: firstReadBlock,
		readErr:   context.Canceled,
	}
	secondConn := &ultravoxRealtimeTestWebsocketConn{readErr: context.Canceled}
	dialCh := make(chan int, 2)
	var conns = []*ultravoxRealtimeTestWebsocketConn{firstConn, secondConn}
	var dialCount int
	model.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (ultravoxRealtimeWebsocketConn, error) {
		if endpoint != "wss://ultravox.example/join" {
			t.Fatalf("websocket endpoint = %q, want provider joinUrl", endpoint)
		}
		if dialCount >= len(conns) {
			t.Fatalf("unexpected extra websocket dial %d", dialCount+1)
		}
		conn := conns[dialCount]
		dialCount++
		dialCh <- dialCount
		return conn, nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.runRealtimeRestartLoop(context.Background(), doer)
	}()

	select {
	case got := <-dialCh:
		if got != 1 {
			t.Fatalf("first dial marker = %d, want 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("restart loop did not create initial reference websocket")
	}
	if err := session.UpdateInstructions("new prompt"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	close(firstReadBlock)
	select {
	case got := <-dialCh:
		if got != 2 {
			t.Fatalf("second dial marker = %d, want 2 after restart signal", got)
		}
	case <-time.After(time.Second):
		t.Fatal("restart loop did not reconnect after reference restart signal")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runRealtimeRestartLoop error = %v, want nil after restart reconnect drains", err)
		}
	case <-time.After(time.Second):
		t.Fatal("restart loop did not exit after second connection completed")
	}
	if doer.requestCount != 2 {
		t.Fatalf("create-call count = %d, want one per reference websocket session", doer.requestCount)
	}
	if firstConn.closeCount != 1 || secondConn.closeCount != 1 {
		t.Fatalf("websocket close counts = %d, %d, want both closed", firstConn.closeCount, secondConn.closeCount)
	}
}

func TestUltravoxRealtimeSessionRestartLoopEmitsReferenceConnectionError(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	err = session.runRealtimeRestartLoop(context.Background(), &ultravoxRealtimeTestHTTPDoer{
		err: errors.New("provider unavailable"),
	})
	if err != nil {
		t.Fatalf("runRealtimeRestartLoop error = %v, want reference main task to emit error event and stop", err)
	}
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeError {
			t.Fatalf("event type = %s, want error", event.Type)
		}
		var modelErr *llm.RealtimeModelError
		if !errors.As(event.Error, &modelErr) {
			t.Fatalf("event error = %T, want RealtimeModelError", event.Error)
		}
		if modelErr.Label != "ultravox-fixie-ai/ultravox" || modelErr.Recoverable {
			t.Fatalf("RealtimeModelError = %#v, want reference label and non-recoverable", modelErr)
		}
		var connectionErr *llm.APIConnectionError
		if !errors.As(modelErr, &connectionErr) || connectionErr.Error() != "Connection failed: provider unavailable" {
			t.Fatalf("RealtimeModelError unwrap = %v, want reference APIConnectionError", modelErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reference error event")
	}
}

func TestUltravoxRealtimeSessionRestartLoopMapsReferenceHTTPStatusError(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.runRealtimeRestartLoop(context.Background(), &ultravoxRealtimeTestHTTPDoer{
		responseStatus: http.StatusServiceUnavailable,
		responseBody:   `service unavailable`,
	}); err != nil {
		t.Fatalf("runRealtimeRestartLoop error = %v, want reference error event", err)
	}
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeError {
			t.Fatalf("event type = %s, want error", event.Type)
		}
		var modelErr *llm.RealtimeModelError
		if !errors.As(event.Error, &modelErr) || modelErr.Recoverable {
			t.Fatalf("event error = %#v, want non-recoverable RealtimeModelError", event.Error)
		}
		var apiErr *llm.APIError
		if !errors.As(modelErr, &apiErr) || apiErr.Error() != "HTTP 503: Service Unavailable" {
			t.Fatalf("RealtimeModelError unwrap = %v, want reference APIError HTTP status", modelErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reference HTTP status error event")
	}
}

func TestUltravoxRealtimeSessionRetriesReferenceRecoverableConnectionError(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeBaseURL("https://ultravox.example/api/"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()
	session.recoverableErrorDelay = 0

	doer := &ultravoxRealtimeTestHTTPDoer{
		errs:           []error{ultravoxRealtimeTestTimeoutError("temporary timeout"), nil},
		responseStatus: http.StatusOK,
		responseBody:   `{"joinUrl":"wss://ultravox.example/join"}`,
	}
	conn := &ultravoxRealtimeTestWebsocketConn{readErr: context.Canceled}
	model.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (ultravoxRealtimeWebsocketConn, error) {
		return conn, nil
	}

	if err := session.runRealtimeRestartLoop(context.Background(), doer); err != nil {
		t.Fatalf("runRealtimeRestartLoop error = %v, want nil after reference recoverable retry", err)
	}
	if doer.requestCount != 2 {
		t.Fatalf("create-call count = %d, want retry after recoverable timeout", doer.requestCount)
	}
	if conn.closeCount != 1 {
		t.Fatalf("websocket close count = %d, want successful retry connection closed", conn.closeCount)
	}
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeError {
			t.Fatalf("event type = %s, want recoverable error", event.Type)
		}
		var modelErr *llm.RealtimeModelError
		if !errors.As(event.Error, &modelErr) || !modelErr.Recoverable {
			t.Fatalf("event error = %#v, want recoverable RealtimeModelError", event.Error)
		}
		var connectionErr *llm.APIConnectionError
		if !errors.As(modelErr, &connectionErr) || connectionErr.Error() != "Connection failed: temporary timeout" {
			t.Fatalf("recoverable error unwrap = %v, want reference connection error", modelErr)
		}
	default:
		t.Fatal("missing recoverable error event before retry")
	}
}

func TestUltravoxRealtimeSessionReconnectsAfterReferenceSendError(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeBaseURL("https://ultravox.example/api/"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	pcm := make([]byte, 3200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	if err := session.PushAudio(&audiomodel.AudioFrame{
		Data:              pcm,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}

	doer := &ultravoxRealtimeTestHTTPDoer{
		responseStatus: http.StatusOK,
		responseBody:   `{"joinUrl":"wss://ultravox.example/join"}`,
	}
	firstReadBlock := make(chan struct{})
	firstConn := &ultravoxRealtimeTestWebsocketConn{
		ultravoxRealtimeTestWebsocketWriter: ultravoxRealtimeTestWebsocketWriter{writeErr: errors.New("socket write failed")},
		readBlock:                           firstReadBlock,
		readErr:                             context.Canceled,
	}
	secondConn := &ultravoxRealtimeTestWebsocketConn{readErr: context.Canceled}
	conns := []*ultravoxRealtimeTestWebsocketConn{firstConn, secondConn}
	dialCh := make(chan int, 2)
	var dialCount int
	model.dialWebsocket = func(ctx context.Context, endpoint string, headers http.Header) (ultravoxRealtimeWebsocketConn, error) {
		if dialCount >= len(conns) {
			t.Fatalf("unexpected extra websocket dial %d", dialCount+1)
		}
		conn := conns[dialCount]
		dialCount++
		dialCh <- dialCount
		return conn, nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.runRealtimeRestartLoop(context.Background(), doer)
	}()
	for want := 1; want <= 2; want++ {
		select {
		case got := <-dialCh:
			if got != want {
				t.Fatalf("dial marker = %d, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for websocket dial %d", want)
		}
	}
	close(firstReadBlock)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runRealtimeRestartLoop error = %v, want nil after reference send-error reconnect", err)
		}
	case <-time.After(time.Second):
		t.Fatal("restart loop did not exit after reconnect drained")
	}
	if doer.requestCount != 2 {
		t.Fatalf("create-call count = %d, want reconnect create-call after send error", doer.requestCount)
	}
	if firstConn.closeCount != 1 || secondConn.closeCount != 1 {
		t.Fatalf("websocket close counts = %d, %d, want both closed", firstConn.closeCount, secondConn.closeCount)
	}
	select {
	case event := <-session.EventCh():
		t.Fatalf("event after send-error reconnect = %#v, want no fatal error event", event)
	default:
	}
}

func TestUltravoxRealtimeSessionServerJSONIgnoresUnknownReferenceEvents(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.handleServerTextMessage([]byte(`{"type":"future_event","value":1}`)); err != nil {
		t.Fatalf("handle unknown JSON error = %v, want reference receive loop to continue", err)
	}
	select {
	case event := <-session.EventCh():
		t.Fatalf("event after unknown JSON = %#v, want no emitted event", event)
	default:
	}

	if err := session.handleServerTextMessage([]byte(`{"type":"transcript","role":"user","medium":"voice","text":"still connected","final":true,"ordinal":5}`)); err != nil {
		t.Fatalf("handle transcript after unknown JSON error = %v", err)
	}
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_5", "still connected", true)
}

func TestUltravoxRealtimeSessionServerJSONIgnoresMalformedReferenceEvents(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	for _, payload := range [][]byte{
		[]byte(`{"type":"transcript","role":"user","medium":"voice","text":"bad","final":true,"ordinal":"bad"}`),
		[]byte(`{"type":"transcript","role":"user","text":"missing medium","final":true,"ordinal":7}`),
		[]byte(`{"type":"transcript","role":"user","medium":"voice","text":"missing final","ordinal":8}`),
		[]byte(`{"type":"transcript","role":"user","medium":"voice","text":"missing ordinal","final":true}`),
		[]byte(`{"type":"client_tool_invocation","invocationId":"call-missing-name","parameters":{}}`),
		[]byte(`{"type":"client_tool_invocation","toolName":"lookup","parameters":{}}`),
		[]byte(`{"type":"client_tool_invocation","toolName":"lookup","invocationId":"call-missing-params"}`),
		[]byte(`{"type":"client_tool_invocation","toolName":"lookup","invocationId":"call-bad-params","parameters":[1,2]}`),
		[]byte(`{"type":"client_tool_invocation","toolName":"lookup","invocationId":"call-null-params","parameters":null}`),
		[]byte(`{"type":"pong"}`),
		[]byte(`{"type":"pong","timestamp":"bad"}`),
		[]byte(`{not-json`),
	} {
		if err := session.handleServerTextMessage(payload); err != nil {
			t.Fatalf("handle malformed JSON error = %v, want reference recv loop to continue", err)
		}
	}
	select {
	case event := <-session.EventCh():
		t.Fatalf("event after malformed JSON = %#v, want no emitted event", event)
	default:
	}
	select {
	case event := <-session.clientEventCh:
		t.Fatalf("client event after malformed JSON = %#v, want none", event)
	default:
	}

	if err := session.handleServerTextMessage([]byte(`{"type":"transcript","role":"user","medium":"voice","text":"still connected","final":true,"ordinal":6}`)); err != nil {
		t.Fatalf("handle transcript after malformed JSON error = %v", err)
	}
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_6", "still connected", true)
}

func TestUltravoxRealtimeSessionPongQueuesReferencePing(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.handleServerTextMessage([]byte(`{"type":"pong","timestamp":123.4}`)); err != nil {
		t.Fatalf("handle pong JSON error = %v", err)
	}
	select {
	case got := <-session.clientEventCh:
		if got["type"] != "ping" {
			t.Fatalf("pong response event type = %#v, want ping in %#v", got["type"], got)
		}
		timestamp, ok := got["timestamp"].(float64)
		if !ok || timestamp <= 0 {
			t.Fatalf("pong response timestamp = %#v, want positive float64", got["timestamp"])
		}
		if timestamp > 1_000_000_000 {
			t.Fatalf("pong response timestamp = %f, want reference monotonic perf_counter-style seconds, not Unix epoch seconds", timestamp)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reference ping after pong")
	}
}

func requireUltravoxRealtimeGeneration(t *testing.T, session *realtimeSession) *llm.GenerationCreatedEvent {
	t.Helper()
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeGenerationCreated {
			t.Fatalf("event type = %s, want generation_created", event.Type)
		}
		if event.Generation == nil {
			t.Fatal("generation = nil")
		}
		return event.Generation
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generation_created")
	}
	return nil
}

func requireUltravoxRealtimeMessage(t *testing.T, generation *llm.GenerationCreatedEvent) llm.MessageGeneration {
	t.Helper()
	select {
	case message := <-generation.MessageCh:
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}
	return llm.MessageGeneration{}
}

func requireUltravoxRealtimeText(t *testing.T, textCh <-chan string, want string) {
	t.Helper()
	select {
	case got := <-textCh:
		if got != want {
			t.Fatalf("text delta = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for text delta %q", want)
	}
}

func requireUltravoxRealtimeModalities(t *testing.T, modalitiesCh <-chan []string, want []string) {
	t.Helper()
	select {
	case got := <-modalitiesCh:
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("modalities = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for modalities %#v", want)
	}
}

func requireUltravoxRealtimeClosedText(t *testing.T, textCh <-chan string) {
	t.Helper()
	select {
	case _, ok := <-textCh:
		if ok {
			t.Fatal("text channel still open after final agent transcript")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed text channel")
	}
}

func requireUltravoxRealtimeClosedAudio(t *testing.T, audioCh <-chan *audiomodel.AudioFrame) {
	t.Helper()
	select {
	case _, ok := <-audioCh:
		if ok {
			t.Fatal("audio channel still open after final agent transcript")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed audio channel")
	}
}

func assertUltravoxRealtimeTextOpen(t *testing.T, textCh <-chan string) {
	t.Helper()
	select {
	case _, ok := <-textCh:
		if !ok {
			t.Fatal("text channel closed on reference restart")
		}
		t.Fatal("unexpected text delta after reference restart")
	default:
	}
}

func assertUltravoxRealtimeAudioOpen(t *testing.T, audioCh <-chan *audiomodel.AudioFrame) {
	t.Helper()
	select {
	case _, ok := <-audioCh:
		if !ok {
			t.Fatal("audio channel closed on reference restart")
		}
		t.Fatal("unexpected audio frame after reference restart")
	default:
	}
}

func requireUltravoxRealtimeTranscriptEvent(t *testing.T, session *realtimeSession, itemID string, transcript string, final bool) {
	t.Helper()
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted {
			t.Fatalf("event type = %s, want input_audio_transcription_completed", event.Type)
		}
		if event.InputTranscription == nil {
			t.Fatal("InputTranscription = nil")
		}
		if event.InputTranscription.ItemID != itemID ||
			event.InputTranscription.Transcript != transcript ||
			event.InputTranscription.IsFinal != final {
			t.Fatalf("InputTranscription = %+v, want item=%q transcript=%q final=%v", event.InputTranscription, itemID, transcript, final)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transcript event")
	}
}

func requireUltravoxRealtimeMetrics(t *testing.T, session *realtimeSession) *telemetry.RealtimeModelMetrics {
	t.Helper()
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeMetricsCollected {
			t.Fatalf("event type = %s, want metrics_collected", event.Type)
		}
		if event.Metrics == nil {
			t.Fatal("Metrics = nil")
		}
		return event.Metrics
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for metrics_collected")
	}
	return nil
}

func assertNoUltravoxRealtimeMetrics(t *testing.T, session *realtimeSession) {
	t.Helper()
	select {
	case event := <-session.EventCh():
		if event.Type == llm.RealtimeEventTypeMetricsCollected {
			t.Fatalf("unexpected metrics event = %#v", event.Metrics)
		}
		t.Fatalf("unexpected realtime event = %#v", event)
	default:
	}
}

func requireUltravoxRealtimePendingReplyCleared(t *testing.T, session *realtimeSession) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		session.mu.Lock()
		pending := session.pendingReply
		pendingAt := session.pendingReplyAt
		session.mu.Unlock()
		if !pending && pendingAt.IsZero() {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("timed out waiting for reference GenerateReply pending owner cleanup")
		}
	}
}

func requireUltravoxRealtimeClientEvent(t *testing.T, session *realtimeSession, want map[string]any) {
	t.Helper()
	select {
	case got := <-session.clientEventCh:
		for key, wantValue := range want {
			if gotValue := got[key]; gotValue != wantValue {
				t.Fatalf("client event %s = %#v, want %#v in %#v", key, gotValue, wantValue, got)
			}
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for client event %#v", want)
	}
}

func requireUltravoxRealtimeOutbound(t *testing.T, session *realtimeSession) ultravoxRealtimeOutboundMessage {
	t.Helper()
	select {
	case got := <-session.outboundCh:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outbound websocket message")
		return ultravoxRealtimeOutboundMessage{}
	}
}

type ultravoxRealtimeTestWebsocketFrame struct {
	typ  int
	data []byte
}

type ultravoxRealtimeTestWebsocketWriter struct {
	frames   []ultravoxRealtimeTestWebsocketFrame
	writeCh  chan struct{}
	writeErr error
}

func (w *ultravoxRealtimeTestWebsocketWriter) WriteMessage(typ int, data []byte) error {
	if w.writeErr != nil {
		return w.writeErr
	}
	w.frames = append(w.frames, ultravoxRealtimeTestWebsocketFrame{
		typ:  typ,
		data: append([]byte(nil), data...),
	})
	if w.writeCh != nil {
		select {
		case w.writeCh <- struct{}{}:
		default:
		}
	}
	return nil
}

type ultravoxRealtimeTestWebsocketConn struct {
	ultravoxRealtimeTestWebsocketWriter
	readMessages []ultravoxRealtimeTestWebsocketFrame
	readBlock    <-chan struct{}
	readErr      error
	closeCount   int
}

func (c *ultravoxRealtimeTestWebsocketConn) ReadMessage() (int, []byte, error) {
	if len(c.readMessages) == 0 {
		if c.readBlock != nil {
			<-c.readBlock
		}
		if c.readErr != nil {
			return 0, nil, c.readErr
		}
		return 0, nil, context.Canceled
	}
	message := c.readMessages[0]
	c.readMessages = c.readMessages[1:]
	return message.typ, append([]byte(nil), message.data...), nil
}

func (c *ultravoxRealtimeTestWebsocketConn) Close() error {
	c.closeCount++
	return nil
}

type ultravoxRealtimeTestHTTPDoer struct {
	request        *http.Request
	requestBody    []byte
	requestCount   int
	errs           []error
	responseStatus int
	responseBody   string
	err            error
}

func (d *ultravoxRealtimeTestHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	d.requestCount++
	d.request = req
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		d.requestBody = body
	}
	if len(d.errs) > 0 {
		err := d.errs[0]
		d.errs = d.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	if d.err != nil {
		return nil, d.err
	}
	status := d.responseStatus
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(d.responseBody)),
		Header:     make(http.Header),
	}, nil
}

type ultravoxRealtimeTestTimeoutError string

func (e ultravoxRealtimeTestTimeoutError) Error() string   { return string(e) }
func (e ultravoxRealtimeTestTimeoutError) Timeout() bool   { return true }
func (e ultravoxRealtimeTestTimeoutError) Temporary() bool { return true }

type ultravoxRealtimeTestTool struct {
	name        string
	description string
	parameters  map[string]any
}

func (t ultravoxRealtimeTestTool) ID() string { return t.name }
func (t ultravoxRealtimeTestTool) Name() string {
	return t.name
}
func (t ultravoxRealtimeTestTool) Description() string { return t.description }
func (t ultravoxRealtimeTestTool) Parameters() map[string]any {
	return t.parameters
}
func (t ultravoxRealtimeTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}
