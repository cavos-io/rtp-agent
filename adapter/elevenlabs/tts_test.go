package elevenlabs

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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestElevenLabsTTSDefaultsMatchReference(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	if provider.voiceID != "hpp4J3VqNfWAUOO0d1Us" {
		t.Fatalf("voiceID = %q, want reference default", provider.voiceID)
	}
	if provider.modelID != "eleven_turbo_v2_5" {
		t.Fatalf("modelID = %q, want eleven_turbo_v2_5", provider.modelID)
	}
	if provider.encoding != "mp3_22050_32" {
		t.Fatalf("encoding = %q, want mp3_22050_32", provider.encoding)
	}
	if provider.SampleRate() != 22050 {
		t.Fatalf("sample rate = %d, want 22050", provider.SampleRate())
	}
	if got := tts.Model(provider); got != "eleven_turbo_v2_5" {
		t.Fatalf("model metadata = %q, want eleven_turbo_v2_5", got)
	}
	if got := tts.Provider(provider); got != "ElevenLabs" {
		t.Fatalf("provider metadata = %q, want ElevenLabs", got)
	}
}

func TestNewElevenLabsTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "env-key")
	t.Setenv("ELEVEN_API_KEY", "fallback-env-key")

	provider, err := NewElevenLabsTTS("", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want primary env key", provider.apiKey)
	}

	explicit, err := NewElevenLabsTTS("explicit-key", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewElevenLabsTTSUsesFallbackEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "")
	t.Setenv("ELEVEN_API_KEY", "fallback-env-key")

	provider, err := NewElevenLabsTTS("", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	if provider.apiKey != "fallback-env-key" {
		t.Fatalf("api key = %q, want fallback env key", provider.apiKey)
	}
}

func TestElevenLabsTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "")
	t.Setenv("ELEVEN_API_KEY", "")
	provider, err := NewElevenLabsTTS("", "", "", WithElevenLabsBaseURL("://bad-url"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	_, err = provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "ELEVEN_API_KEY") {
		t.Fatalf("Synthesize error = %q, want ELEVEN_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "ELEVEN_API_KEY") {
		t.Fatalf("Stream error = %q, want ELEVEN_API_KEY guidance", err)
	}
}

func TestElevenLabsSynthesizeRequestUsesReferenceOptions(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "",
		WithElevenLabsLanguage("en"),
		WithElevenLabsEnableSSMLParsing(true),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	requestURL, body := buildElevenLabsSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if parsed.Path != "/v1/text-to-speech/hpp4J3VqNfWAUOO0d1Us/stream" {
		t.Fatalf("path = %q, want default voice stream path", parsed.Path)
	}
	if parsed.Query().Get("model_id") != "eleven_turbo_v2_5" {
		t.Fatalf("model_id = %q, want eleven_turbo_v2_5", parsed.Query().Get("model_id"))
	}
	if parsed.Query().Get("output_format") != "mp3_22050_32" {
		t.Fatalf("output_format = %q, want mp3_22050_32", parsed.Query().Get("output_format"))
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["text"] != "hello" {
		t.Fatalf("text = %#v, want hello", payload["text"])
	}
	if payload["model_id"] != "eleven_turbo_v2_5" {
		t.Fatalf("model_id = %#v, want eleven_turbo_v2_5", payload["model_id"])
	}
	if payload["language_code"] != "en" {
		t.Fatalf("language_code = %#v, want en", payload["language_code"])
	}
	if payload["enable_ssml_parsing"] != true {
		t.Fatalf("enable_ssml_parsing = %#v, want true", payload["enable_ssml_parsing"])
	}
}

func TestElevenLabsTTSVoiceSettingsMatchReference(t *testing.T) {
	style := 0.35
	speed := 1.05
	boost := true
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsVoiceSettings(ElevenLabsVoiceSettings{
			Stability:       0.7,
			SimilarityBoost: 0.8,
			Style:           &style,
			Speed:           &speed,
			UseSpeakerBoost: &boost,
		}),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	_, body := buildElevenLabsSynthesizeRequest(provider, "hello")
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	assertElevenLabsTTSVoiceSettings(t, payload["voice_settings"])

	init := elevenLabsInitPayload("ctx_test", elevenLabsVoiceSettingsPayload(provider.voiceSettings), nil)
	assertElevenLabsTTSVoiceSettings(t, init["voice_settings"])
}

func TestElevenLabsSynthesizeRequestUsesConfiguredBaseURL(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1/"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	requestURL, _ := buildElevenLabsSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if parsed.Scheme != "https" || parsed.Host != "eleven.example" {
		t.Fatalf("url = %q, want configured host", requestURL)
	}
	if parsed.Path != "/v1/text-to-speech/voice-1/stream" {
		t.Fatalf("path = %q, want configured base URL with stream synthesize path", parsed.Path)
	}
}

func TestElevenLabsTTSRejectsNonAudioResponse(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"not audio"}`)),
			Request:    r,
		}, nil
	})}

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize returned nil error, want non-audio response error")
	}
	if !strings.Contains(err.Error(), "non-audio") {
		t.Fatalf("Synthesize error = %q, want non-audio guidance", err)
	}
}

func TestElevenLabsTTSDecodesReferenceMP3Response(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/mpeg"}},
			Body:       io.NopCloser(bytes.NewReader(mp3Data)),
			Request:    r,
		}, nil
	})}

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 22050 {
		t.Fatalf("sample rate = %d, want configured mp3 rate 22050", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want reference mono output", audio.Frame.NumChannels)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	if got, want := len(audio.Frame.Data), int(audio.Frame.SamplesPerChannel*audio.Frame.NumChannels*2); got != want {
		t.Fatalf("frame byte length = %d, want %d from samples/channels", got, want)
	}
	if bytes.Equal(audio.Frame.Data, mp3Data[:len(audio.Frame.Data)]) {
		t.Fatal("frame data still contains compressed mp3 bytes")
	}
}

func TestElevenLabsTTSReadErrorIncludesProviderOperationContext(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: elevenLabsRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       elevenLabsErrReader{err: io.ErrClosedPipe},
			Request:    r,
		}, nil
	})}

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_multilingual_v2",
		WithElevenLabsBaseURL("https://eleven.example/v1"),
		WithElevenLabsLanguage("id"),
		WithElevenLabsEncoding("pcm_8000"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "Halo, ada yang bisa saya bantu?")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next returned nil error, want wrapped closed-pipe read error")
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Next error = %v, want errors.Is io.ErrClosedPipe", err)
	}
	for _, want := range []string{"elevenlabs TTS", "chunked pcm response", "before audio bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Next error = %q, want context %q", err, want)
		}
	}
}

func TestElevenLabsStreamURLUsesReferenceOptions(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "",
		WithElevenLabsLanguage("en"),
		WithElevenLabsEnableSSMLParsing(true),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	streamURL := buildElevenLabsStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}

	if parsed.Path != "/v1/text-to-speech/hpp4J3VqNfWAUOO0d1Us/multi-stream-input" {
		t.Fatalf("path = %q, want default voice stream path", parsed.Path)
	}
	if parsed.Query().Get("model_id") != "eleven_turbo_v2_5" {
		t.Fatalf("model_id = %q, want eleven_turbo_v2_5", parsed.Query().Get("model_id"))
	}
	if parsed.Query().Get("output_format") != "mp3_22050_32" {
		t.Fatalf("output_format = %q, want mp3_22050_32", parsed.Query().Get("output_format"))
	}
	if parsed.Query().Get("language_code") != "en" {
		t.Fatalf("language_code = %q, want en", parsed.Query().Get("language_code"))
	}
	if parsed.Query().Get("enable_ssml_parsing") != "true" {
		t.Fatalf("enable_ssml_parsing = %q, want true", parsed.Query().Get("enable_ssml_parsing"))
	}
	if parsed.Query().Get("enable_logging") != "true" {
		t.Fatalf("enable_logging = %q, want true", parsed.Query().Get("enable_logging"))
	}
	if defaultElevenLabsInactivityTimeout != 180 {
		t.Fatalf("default inactivity timeout = %d, want reference 180", defaultElevenLabsInactivityTimeout)
	}
	if parsed.Query().Get("inactivity_timeout") != "180" {
		t.Fatalf("inactivity_timeout = %q, want 180", parsed.Query().Get("inactivity_timeout"))
	}
	if parsed.Query().Get("apply_text_normalization") != "auto" {
		t.Fatalf("apply_text_normalization = %q, want auto", parsed.Query().Get("apply_text_normalization"))
	}
	if parsed.Query().Get("sync_alignment") != "true" {
		t.Fatalf("sync_alignment = %q, want true", parsed.Query().Get("sync_alignment"))
	}
}

func TestElevenLabsStreamURLUsesConfiguredBaseURL(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "voice-1", "",
		WithElevenLabsBaseURL("https://eleven.example/v1/"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	streamURL := buildElevenLabsStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}

	if parsed.Scheme != "wss" || parsed.Host != "eleven.example" {
		t.Fatalf("stream url = %q, want configured websocket host", streamURL)
	}
	if parsed.Path != "/v1/text-to-speech/voice-1/multi-stream-input" {
		t.Fatalf("path = %q, want configured base URL with stream path", parsed.Path)
	}
}

func TestElevenLabsTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider, err := NewElevenLabsTTS("test-key", "", "")
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}

	provider.UpdateOptions(
		WithElevenLabsVoiceID("voice-updated"),
		WithElevenLabsModel("eleven_multilingual_v2"),
		WithElevenLabsLanguage("id"),
	)

	requestURL, body := buildElevenLabsSynthesizeRequest(provider, "halo")
	parsedRequest, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse synthesize url: %v", err)
	}
	if parsedRequest.Path != "/v1/text-to-speech/voice-updated/stream" {
		t.Fatalf("synthesize path = %q, want updated voice", parsedRequest.Path)
	}
	if parsedRequest.Query().Get("model_id") != "eleven_multilingual_v2" {
		t.Fatalf("synthesize model_id = %q, want eleven_multilingual_v2", parsedRequest.Query().Get("model_id"))
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["model_id"] != "eleven_multilingual_v2" {
		t.Fatalf("payload = %#v, want updated model", payload)
	}
	if _, hasLang := payload["language_code"]; hasLang {
		t.Fatalf("payload = %#v, eleven_multilingual_v2 must not include language_code", payload)
	}

	streamURL := buildElevenLabsStreamURL(provider)
	parsedStream, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if parsedStream.Path != "/v1/text-to-speech/voice-updated/multi-stream-input" {
		t.Fatalf("stream path = %q, want updated voice", parsedStream.Path)
	}
	if parsedStream.Query().Get("model_id") != "eleven_multilingual_v2" {
		t.Fatalf("stream model_id = %q, want eleven_multilingual_v2", parsedStream.Query().Get("model_id"))
	}
	if parsedStream.Query().Get("language_code") != "" {
		t.Fatalf("stream language_code = %q, eleven_multilingual_v2 must not include language_code", parsedStream.Query().Get("language_code"))
	}
	if got := provider.Model(); got != "eleven_multilingual_v2" {
		t.Fatalf("Model() = %q, want eleven_multilingual_v2", got)
	}
}

func TestElevenLabsStreamPayloadsUseReferenceContextProtocol(t *testing.T) {
	const contextID = "ctx_test"

	init := elevenLabsInitPayload(contextID, nil, nil)
	if init["text"] != " " || init["context_id"] != contextID {
		t.Fatalf("init payload = %#v, want warmup text with context_id", init)
	}
	voiceSettings, ok := init["voice_settings"].(map[string]any)
	if !ok {
		t.Fatalf("init voice_settings = %#v, want empty settings object", init["voice_settings"])
	}
	if len(voiceSettings) != 0 {
		t.Fatalf("init voice_settings = %#v, want empty settings object", voiceSettings)
	}
	if _, ok := init["generation_config"]; ok {
		t.Fatalf("init payload = %#v, want no generation_config without configured schedule", init)
	}

	scheduledInit := elevenLabsInitPayload(contextID, nil, []int{80, 120, 200})
	generationConfig, ok := scheduledInit["generation_config"].(map[string]any)
	if !ok {
		t.Fatalf("scheduled init generation_config = %#v, want object", scheduledInit["generation_config"])
	}
	chunkSchedule, ok := generationConfig["chunk_length_schedule"].([]int)
	if !ok {
		t.Fatalf("scheduled init chunk_length_schedule = %#v, want []int", generationConfig["chunk_length_schedule"])
	}
	if !equalIntSlices(chunkSchedule, []int{80, 120, 200}) {
		t.Fatalf("scheduled init chunk_length_schedule = %#v, want [80 120 200]", chunkSchedule)
	}

	text := elevenLabsTextPayload(contextID, "hello")
	if text["text"] != "hello" || text["context_id"] != contextID {
		t.Fatalf("text payload = %#v, want text with context_id", text)
	}
	if _, ok := text["try_trigger_generation"]; ok {
		t.Fatalf("text payload = %#v, want no legacy try_trigger_generation flag", text)
	}

	flush := elevenLabsFlushPayload(contextID)
	if flush["text"] != "" {
		t.Fatalf("flush text = %#v, want empty end-of-input signal", flush["text"])
	}
	if flush["context_id"] != contextID || flush["flush"] != true {
		t.Fatalf("flush payload = %#v, want context_id and flush=true", flush)
	}

	closeContext := elevenLabsCloseContextPayload(contextID)
	if closeContext["context_id"] != contextID || closeContext["close_context"] != true {
		t.Fatalf("close payload = %#v, want context_id and close_context=true", closeContext)
	}
}

func TestElevenLabsTTSStreamStartsContextOnFirstText(t *testing.T) {
	messages := make(chan map[string]any, 4)
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsTTSWebsocketServer(messages, serverConn, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
		WithElevenLabsChunkLengthSchedule([]int{80, 120, 200}),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case msg := <-messages:
		t.Fatalf("Stream() sent websocket packet before first text: %#v", msg)
	case err := <-serverErr:
		t.Fatalf("test websocket server error: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}

	init := readElevenLabsTTSStreamMessage(t, messages)
	if init["text"] != " " {
		t.Fatalf("init text = %#v, want warmup space in %#v", init["text"], init)
	}
	contextID, _ := init["context_id"].(string)
	if contextID == "" {
		t.Fatalf("init context_id = %#v, want non-empty string", init["context_id"])
	}
	if _, ok := init["voice_settings"].(map[string]any); !ok {
		t.Fatalf("init voice_settings = %#v, want object", init["voice_settings"])
	}
	generationConfig, ok := init["generation_config"].(map[string]any)
	if !ok {
		t.Fatalf("init generation_config = %#v, want object", init["generation_config"])
	}
	chunkSchedule, ok := generationConfig["chunk_length_schedule"].([]any)
	if !ok || len(chunkSchedule) != 3 || chunkSchedule[0] != float64(80) || chunkSchedule[1] != float64(120) || chunkSchedule[2] != float64(200) {
		t.Fatalf("init chunk_length_schedule = %#v, want [80 120 200]", generationConfig["chunk_length_schedule"])
	}

	text := readElevenLabsTTSStreamMessage(t, messages)
	if text["text"] != "hello" || text["context_id"] != contextID {
		t.Fatalf("text packet = %#v, want hello with context_id %q", text, contextID)
	}
	if _, ok := text["flush"]; ok {
		t.Fatalf("text packet = %#v, want no flush before Flush()", text)
	}
}

func runElevenLabsTTSWebsocketServer(messages chan<- map[string]any, conn net.Conn, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}

	if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		errCh <- err
		return
	}
	msg, err := readElevenLabsClientWebsocketJSONFrame(reader)
	if err == nil {
		messages <- msg
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		errCh <- err
		return
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		errCh <- err
		return
	}

	for {
		msg, err := readElevenLabsClientWebsocketJSONFrame(reader)
		if err != nil {
			return
		}
		messages <- msg
	}
}

func TestElevenLabsTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	closed := make(chan struct{})
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runElevenLabsClosingWebsocketServerAfterFrame(serverConn, closed, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5", WithElevenLabsBaseURL("ws://eleven.test/v1"))
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	writeErr := stream.PushText("hello")
	if writeErr == nil {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for test websocket close")
		}
		select {
		case err := <-serverErr:
			if err != nil {
				t.Fatalf("test websocket server error: %v", err)
			}
		default:
		}
	}

	for range 3 {
		writeErr = stream.PushText("hello")
		if writeErr != nil {
			break
		}
	}
	if writeErr == nil {
		t.Fatal("PushText error = nil after closed websocket, want write failure")
	}
	providerStream, ok := stream.(*elevenLabsStream)
	if !ok {
		t.Fatalf("stream = %T, want *elevenLabsStream", stream)
	}
	if !providerStream.closed {
		t.Fatal("stream closed = false after write failure, want true")
	}

	err = stream.PushText("again")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second PushText error = %v, want io.ErrClosedPipe", err)
	}
}

func readElevenLabsTTSStreamMessage(t *testing.T, messages <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ElevenLabs TTS websocket message")
	}
	return nil
}

func equalIntSlices(a []int, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertElevenLabsTTSVoiceSettings(t *testing.T, raw any) {
	t.Helper()
	settings, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("voice_settings = %#v, want object", raw)
	}
	want := map[string]any{
		"stability":         0.7,
		"similarity_boost":  0.8,
		"style":             0.35,
		"speed":             1.05,
		"use_speaker_boost": true,
	}
	for key, wantValue := range want {
		if settings[key] != wantValue {
			t.Fatalf("voice_settings[%s] = %#v, want %#v in %#v", key, settings[key], wantValue, settings)
		}
	}
}

func runElevenLabsClosingWebsocketServerAfterFrame(conn net.Conn, closed chan<- struct{}, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	if err := readElevenLabsClientWebsocketFrame(reader); err != nil {
		errCh <- err
		return
	}
	close(closed)
	errCh <- nil
}

func readElevenLabsClientWebsocketFrame(reader *bufio.Reader) error {
	_, err := readElevenLabsClientWebsocketFramePayload(reader)
	return err
}

func readElevenLabsClientWebsocketJSONFrame(reader *bufio.Reader) (map[string]any, error) {
	payload, err := readElevenLabsClientWebsocketFramePayload(reader)
	if err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func readElevenLabsClientWebsocketFramePayload(reader *bufio.Reader) ([]byte, error) {
	if _, err := reader.ReadByte(); err != nil {
		return nil, err
	}
	lengthByte, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	masked := lengthByte&0x80 != 0
	length := uint64(lengthByte & 0x7f)
	switch length {
	case 126:
		var buf [2]byte
		if _, err := io.ReadFull(reader, buf[:]); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(buf[:]))
	case 127:
		var buf [8]byte
		if _, err := io.ReadFull(reader, buf[:]); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(buf[:])
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(reader, mask[:]); err != nil {
			return nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return payload, nil
}

func TestElevenLabsSynthesizedAudioUsesConfiguredSampleRate(t *testing.T) {
	resp := elWSResponse{
		Audio: base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
	}

	audio, err := elevenLabsSynthesizedAudio(resp, 22050, "pcm_22050")
	if err != nil {
		t.Fatalf("elevenLabsSynthesizedAudio() error = %v", err)
	}
	if audio.Frame.SampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", audio.Frame.SampleRate)
	}
}

func TestElevenLabsTTSAlignmentMapsTimedTranscript(t *testing.T) {
	resp := elWSResponse{
		Audio:   base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
		IsFinal: true,
		NormalizedAlignment: &elevenLabsAlignment{
			Chars:            []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"},
			CharStartTimesMs: []int{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
			CharDurationsMs:  []int{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10},
		},
	}

	audio, err := elevenLabsSynthesizedAudio(resp, 22050, "pcm_22050")
	if err != nil {
		t.Fatalf("elevenLabsSynthesizedAudio() error = %v", err)
	}
	if audio.DeltaText != "hello world" {
		t.Fatalf("DeltaText = %q, want hello world", audio.DeltaText)
	}
	if len(audio.TimedTranscript) != 2 {
		t.Fatalf("TimedTranscript = %#v, want two timed words", audio.TimedTranscript)
	}
	if got := audio.TimedTranscript[0]; got.Text != "hello " || got.StartTime != 0 || got.EndTime != 0.06 {
		t.Fatalf("TimedTranscript[0] = %#v, want hello from 0 to 0.06", got)
	}
	if got := audio.TimedTranscript[1]; got.Text != "world" || got.StartTime != 0.06 || got.EndTime != 0.11 {
		t.Fatalf("TimedTranscript[1] = %#v, want world from 0.06 to 0.11", got)
	}
}

func TestElevenLabsSynthesizedAudioDecodesReferenceMP3WebsocketAudio(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	resp := elWSResponse{
		Audio: base64.StdEncoding.EncodeToString(mp3Data),
	}

	audio, err := elevenLabsSynthesizedAudio(resp, 22050, "mp3_22050_32")
	if err != nil {
		t.Fatalf("elevenLabsSynthesizedAudio() error = %v", err)
	}
	if audio.Frame.SampleRate != 22050 {
		t.Fatalf("sample rate = %d, want configured mp3 rate 22050", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want reference mono output", audio.Frame.NumChannels)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	if got, want := len(audio.Frame.Data), int(audio.Frame.SamplesPerChannel*audio.Frame.NumChannels*2); got != want {
		t.Fatalf("frame byte length = %d, want %d from samples/channels", got, want)
	}
	if bytes.Equal(audio.Frame.Data, mp3Data[:len(audio.Frame.Data)]) {
		t.Fatal("frame data still contains compressed mp3 bytes")
	}
}

type elevenLabsRoundTripFunc func(*http.Request) (*http.Response, error)

func (f elevenLabsRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type elevenLabsErrReader struct {
	err error
}

func (r elevenLabsErrReader) Read([]byte) (int, error) {
	return 0, r.err
}

func (r elevenLabsErrReader) Close() error {
	return nil
}
