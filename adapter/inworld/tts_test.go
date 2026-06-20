package inworld

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestInworldTTSDefaultsMatchReference(t *testing.T) {
	provider := NewInworldTTS("test-key", "")

	if provider.baseURL != "https://api.inworld.ai/" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.wsURL != "wss://api.inworld.ai/" {
		t.Fatalf("websocket URL = %q, want reference websocket URL", provider.wsURL)
	}
	if provider.voice != "Ashley" {
		t.Fatalf("voice = %q, want Ashley", provider.voice)
	}
	if provider.model != "inworld-tts-1.5-max" {
		t.Fatalf("model = %q, want reference model", provider.model)
	}
	if got := tts.Model(provider); got != "inworld-tts-1.5-max" {
		t.Fatalf("model metadata = %q, want inworld-tts-1.5-max", got)
	}
	if got := tts.Provider(provider); got != "Inworld" {
		t.Fatalf("provider metadata = %q, want Inworld", got)
	}
	if provider.encoding != "PCM" {
		t.Fatalf("encoding = %q, want PCM", provider.encoding)
	}
	if provider.bitRate != 64000 {
		t.Fatalf("bit rate = %d, want 64000", provider.bitRate)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true")
	}
	if provider.Capabilities().AlignedTranscript {
		t.Fatal("aligned transcript = true, want false without timestamp type")
	}
}

func TestNewInworldTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("INWORLD_API_KEY", "env-key")

	provider := NewInworldTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	if got := buildInworldTTSHeaders(provider).Get("Authorization"); got != "Basic env-key" {
		t.Fatalf("authorization = %q, want env basic key", got)
	}

	explicit := NewInworldTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
	if got := buildInworldTTSHeaders(explicit).Get("Authorization"); got != "Basic explicit-key" {
		t.Fatalf("authorization = %q, want explicit basic key", got)
	}
}

func TestInworldTTSOptionsMatchReference(t *testing.T) {
	provider := NewInworldTTS("test-key", "",
		WithInworldTTSBaseURL("https://inworld.example/"),
		WithInworldTTSWebsocketURL("wss://inworld.example/"),
		WithInworldTTSVoice("Ava"),
		WithInworldTTSModel("inworld-tts-2"),
		WithInworldTTSEncoding("MP3"),
		WithInworldTTSBitRate(128000),
		WithInworldTTSSampleRate(44100),
		WithInworldTTSSpeakingRate(1.2),
		WithInworldTTSTemperature(0.8),
		WithInworldTTSLanguage("en-US"),
		WithInworldTTSTimestampType("WORD"),
		WithInworldTTSTextNormalization(true),
		WithInworldTTSDeliveryMode("BALANCED"),
		WithInworldTTSTimestampTransportStrategy("SYNC"),
		WithInworldTTSBufferCharThreshold(120),
		WithInworldTTSMaxBufferDelayMS(500),
	)

	if provider.baseURL != "https://inworld.example/" || provider.wsURL != "wss://inworld.example/" {
		t.Fatalf("provider URLs = %q %q, want custom URLs", provider.baseURL, provider.wsURL)
	}
	if provider.voice != "Ava" || provider.model != "inworld-tts-2" || provider.encoding != "MP3" {
		t.Fatalf("provider = %+v, want custom voice/model/encoding", provider)
	}
	if !provider.Capabilities().AlignedTranscript {
		t.Fatal("aligned transcript = false, want true with timestamp type")
	}
}

func TestInworldTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewInworldTTS("test-key", "",
		WithInworldTTSVoice("Ava"),
		WithInworldTTSModel("inworld-tts-2"),
		WithInworldTTSEncoding("MP3"),
		WithInworldTTSLanguage("en-US"),
		WithInworldTTSTimestampType("WORD"),
		WithInworldTTSTextNormalization(false),
		WithInworldTTSDeliveryMode("STABLE"),
	)

	req, err := buildInworldTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.inworld.ai/tts/v1/voice:stream" {
		t.Fatalf("url = %q, want reference stream endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Basic test-key" {
		t.Fatalf("Authorization = %q, want Basic token", got)
	}
	if got := req.Header.Get("X-User-Agent"); got != "livekit-agents-py/1.5.15" {
		t.Fatalf("X-User-Agent = %q, want reference user agent", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertInworldPayload(t, payload, "text", "hello")
	assertInworldPayload(t, payload, "voiceId", "Ava")
	assertInworldPayload(t, payload, "modelId", "inworld-tts-2")
	assertInworldPayload(t, payload, "language", "en-US")
	assertInworldPayload(t, payload, "timestampType", "WORD")
	assertInworldPayload(t, payload, "applyTextNormalization", "OFF")
	assertInworldPayload(t, payload, "deliveryMode", "STABLE")
	assertInworldPayload(t, payload, "timestampTransportStrategy", "ASYNC")
	audioConfig := payload["audioConfig"].(map[string]any)
	assertInworldPayload(t, audioConfig, "audioEncoding", "MP3")
	if audioConfig["sampleRateHertz"] != float64(24000) {
		t.Fatalf("sampleRateHertz = %#v, want 24000", audioConfig["sampleRateHertz"])
	}
}

func TestInworldTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewInworldTTS("test-key", "", WithInworldTTSWebsocketURL("wss://inworld.example/"))

	if got := buildInworldTTSWebsocketURL(provider); got != "wss://inworld.example/tts/v1/voice:streamBidirectional" {
		t.Fatalf("websocket URL = %q, want bidirectional endpoint", got)
	}
	headers := buildInworldTTSWebsocketHeaders(provider)
	if got := headers.Get("Authorization"); got != "Basic test-key" {
		t.Fatalf("Authorization = %q, want Basic token", got)
	}
	if got := headers.Get("X-User-Agent"); got != "livekit-agents-py/1.5.15" {
		t.Fatalf("X-User-Agent = %q, want reference user agent", got)
	}
}

func TestInworldTTSWebsocketMessagesMatchReference(t *testing.T) {
	provider := NewInworldTTS("test-key", "",
		WithInworldTTSVoice("Ava"),
		WithInworldTTSModel("inworld-tts-2"),
		WithInworldTTSLanguage("en-US"),
		WithInworldTTSBufferCharThreshold(120),
		WithInworldTTSMaxBufferDelayMS(500),
	)

	createMessage, err := buildInworldTTSCreateMessage(provider, "ctx-1")
	if err != nil {
		t.Fatalf("build create message: %v", err)
	}
	var createPayload map[string]any
	if err := json.Unmarshal(createMessage, &createPayload); err != nil {
		t.Fatalf("decode create message: %v", err)
	}
	assertInworldPayload(t, createPayload, "contextId", "ctx-1")
	create := createPayload["create"].(map[string]any)
	assertInworldPayload(t, create, "voiceId", "Ava")
	assertInworldPayload(t, create, "modelId", "inworld-tts-2")
	assertInworldPayload(t, create, "language", "en-US")
	if create["autoMode"] != true {
		t.Fatalf("autoMode = %#v, want true", create["autoMode"])
	}
	if create["bufferCharThreshold"] != float64(120) || create["maxBufferDelayMs"] != float64(500) {
		t.Fatalf("buffer settings = %#v/%#v, want 120/500", create["bufferCharThreshold"], create["maxBufferDelayMs"])
	}

	textMessage, err := buildInworldTTSSendTextMessage("ctx-1", "hello")
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var textPayload map[string]any
	if err := json.Unmarshal(textMessage, &textPayload); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	assertInworldPayload(t, textPayload, "contextId", "ctx-1")
	sendText := textPayload["send_text"].(map[string]any)
	assertInworldPayload(t, sendText, "text", "hello")

	flushMessage, err := buildInworldTTSFlushMessage("ctx-1")
	if err != nil {
		t.Fatalf("build flush message: %v", err)
	}
	var flushPayload map[string]any
	if err := json.Unmarshal(flushMessage, &flushPayload); err != nil {
		t.Fatalf("decode flush message: %v", err)
	}
	if _, ok := flushPayload["flush_context"].(map[string]any); !ok {
		t.Fatalf("flush_context missing from %#v", flushPayload)
	}

	closeMessage, err := buildInworldTTSCloseMessage("ctx-1")
	if err != nil {
		t.Fatalf("build close message: %v", err)
	}
	var closePayload map[string]any
	if err := json.Unmarshal(closeMessage, &closePayload); err != nil {
		t.Fatalf("decode close message: %v", err)
	}
	if _, ok := closePayload["close_context"].(map[string]any); !ok {
		t.Fatalf("close_context missing from %#v", closePayload)
	}
}

func TestInworldTTSStreamClosesAfterFlushWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &inworldTTSSynthesizeStream{
		cancel:    func() { cancelled = true },
		contextID: "ctx-1",
		writeMessage: func(int, []byte) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText error = %v, want nil", err)
	}
	if err := stream.Flush(); !errors.Is(err, writeErr) {
		t.Fatalf("Flush error = %v, want write error", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after write failure")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PushText after write failure error = %v, want closed stream error", err)
	}
	if err := stream.Flush(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Flush after write failure error = %v, want closed stream error", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
	}
}

func TestInworldTTSProviderCloseClosesActiveStreams(t *testing.T) {
	provider := NewInworldTTS("test-key", "")
	cancelled := false
	closeCalls := 0
	stream := &inworldTTSSynthesizeStream{
		cancel:    func() { cancelled = true },
		contextID: "ctx-1",
		writeMessage: func(int, []byte) error {
			return nil
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !cancelled {
		t.Fatal("cancel not called")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PushText after provider Close error = %v, want closed stream error", err)
	}
}

func TestInworldTTSAudioFromReferenceResponses(t *testing.T) {
	audio, done, err := inworldTTSAudioFromResponseLine([]byte(`{"result":{"audioContent":"AQIDBA=="}}`), 24000)
	if err != nil {
		t.Fatalf("audio from response line: %v", err)
	}
	if done {
		t.Fatal("done = true for audio response")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}

	wsAudio, done, err := inworldTTSAudioFromWebsocketMessage([]byte(`{"result":{"contextId":"ctx-1","audioChunk":{"audioContent":"AQIDBA=="}}}`), "ctx-1", 24000)
	if err != nil {
		t.Fatalf("audio from websocket message: %v", err)
	}
	if done || wsAudio == nil || string(wsAudio.Frame.Data) != string([]byte{1, 2, 3, 4}) || wsAudio.SegmentID != "ctx-1" {
		t.Fatalf("wsAudio=%+v done=%v, want decoded segment audio", wsAudio, done)
	}

	_, done, err = inworldTTSAudioFromWebsocketMessage([]byte(`{"result":{"contextId":"ctx-1","contextClosed":{}}}`), "ctx-1", 24000)
	if err != nil {
		t.Fatalf("context closed message: %v", err)
	}
	if !done {
		t.Fatal("done = false, want true for contextClosed")
	}

	if _, _, err := inworldTTSAudioFromResponseLine([]byte(`{"error":{"code":3,"message":"bad text"}}`), 24000); err == nil {
		t.Fatal("error response returned nil error, want API error")
	}
}

func TestInworldTTSAudioFromWebsocketMessageIncludesReferenceWordAlignment(t *testing.T) {
	audio, done, err := inworldTTSAudioFromWebsocketMessage([]byte(`{
		"result": {
			"contextId": "ctx-1",
			"audioChunk": {
				"audioContent": "AQIDBA==",
				"timestampInfo": {
					"wordAlignment": {
						"words": ["hello", "world"],
						"wordStartTimeSeconds": [0.1, 0.3],
						"wordEndTimeSeconds": [0.2, 0.5]
					}
				}
			}
		}
	}`), "ctx-1", 24000)
	if err != nil {
		t.Fatalf("audio from websocket message: %v", err)
	}
	if done {
		t.Fatal("done = true, want audio")
	}
	if audio == nil {
		t.Fatal("audio = nil, want decoded audio")
	}
	if len(audio.TimedTranscript) != 2 {
		t.Fatalf("timed transcript = %#v, want two aligned words", audio.TimedTranscript)
	}
	if audio.TimedTranscript[0].Text != "hello" || audio.TimedTranscript[0].StartTime != 0.1 || audio.TimedTranscript[0].EndTime != 0.2 {
		t.Fatalf("first timed word = %#v, want hello 0.1-0.2", audio.TimedTranscript[0])
	}
	if audio.TimedTranscript[1].Text != "world" || audio.TimedTranscript[1].StartTime != 0.3 || audio.TimedTranscript[1].EndTime != 0.5 {
		t.Fatalf("second timed word = %#v, want world 0.3-0.5", audio.TimedTranscript[1])
	}
}

func TestInworldTTSChunkedStreamIncludesReferenceWordAlignment(t *testing.T) {
	audio, done, err := inworldTTSAudioFromResponseLine([]byte(`{
		"result": {
			"audioContent": "AQIDBA==",
			"timestampInfo": {
				"wordAlignment": {
					"words": ["hello", "world"],
					"wordStartTimeSeconds": [0.1, 0.3],
					"wordEndTimeSeconds": [0.2, 0.5]
				}
			}
		}
	}`), 24000)
	if err != nil {
		t.Fatalf("audio from response line: %v", err)
	}
	if done {
		t.Fatal("done = true, want audio")
	}
	if audio == nil {
		t.Fatal("audio = nil, want decoded audio")
	}
	if len(audio.TimedTranscript) != 2 {
		t.Fatalf("timed transcript = %#v, want two aligned words", audio.TimedTranscript)
	}
	if audio.TimedTranscript[0].Text != "hello" || audio.TimedTranscript[0].StartTime != 0.1 || audio.TimedTranscript[0].EndTime != 0.2 {
		t.Fatalf("first timed word = %#v, want hello 0.1-0.2", audio.TimedTranscript[0])
	}
	if audio.TimedTranscript[1].Text != "world" || audio.TimedTranscript[1].StartTime != 0.3 || audio.TimedTranscript[1].EndTime != 0.5 {
		t.Fatalf("second timed word = %#v, want world 0.3-0.5", audio.TimedTranscript[1])
	}
}

func TestInworldTTSStreamOffsetsWordAlignmentAfterFlush(t *testing.T) {
	stream := &inworldTTSSynthesizeStream{contextID: "ctx-1", sampleRate: 24000}

	first, done, err := stream.handleWebsocketMessage([]byte(`{
		"result": {
			"contextId": "ctx-1",
			"audioChunk": {
				"audioContent": "AQIDBA==",
				"timestampInfo": {
					"wordAlignment": {
						"words": ["first"],
						"wordStartTimeSeconds": [0.1],
						"wordEndTimeSeconds": [0.4]
					}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("first message error = %v", err)
	}
	if done || first == nil {
		t.Fatalf("first audio = %#v done=%v, want audio", first, done)
	}

	if _, done, err := stream.handleWebsocketMessage([]byte(`{"result":{"contextId":"ctx-1","flushCompleted":{}}}`)); err != nil || done {
		t.Fatalf("flushCompleted err=%v done=%v, want no audio and no done", err, done)
	}

	second, done, err := stream.handleWebsocketMessage([]byte(`{
		"result": {
			"contextId": "ctx-1",
			"audioChunk": {
				"audioContent": "BQYHCA==",
				"timestampInfo": {
					"wordAlignment": {
						"words": ["second"],
						"wordStartTimeSeconds": [0.0],
						"wordEndTimeSeconds": [0.2]
					}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("second message error = %v", err)
	}
	if done || second == nil {
		t.Fatalf("second audio = %#v done=%v, want audio", second, done)
	}
	if len(second.TimedTranscript) != 1 {
		t.Fatalf("second timed transcript = %#v, want one word", second.TimedTranscript)
	}
	if second.TimedTranscript[0].StartTime < 0.399999 || second.TimedTranscript[0].StartTime > 0.400001 || second.TimedTranscript[0].EndTime < 0.599999 || second.TimedTranscript[0].EndTime > 0.600001 {
		t.Fatalf("second timed word = %#v, want offset by previous generation end", second.TimedTranscript[0])
	}
}

func TestInworldTTSChunkedStreamDecodesReferenceJSONLines(t *testing.T) {
	stream := &inworldTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte("{\"result\":{\"audioContent\":\"AQI=\"}}\n")))},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{1, 2}) {
		t.Fatalf("audio data = %#v, want decoded base64 audio", audio.Frame.Data)
	}
}

func TestInworldTTSChunkedStreamSkipsMalformedReferenceJSONLines(t *testing.T) {
	stream := &inworldTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte("not-json\n{\"result\":{\"audioContent\":\"AQI=\"}}\n")))},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{1, 2}) {
		t.Fatalf("audio data = %#v, want decoded base64 audio after malformed line", audio.Frame.Data)
	}
}

func TestInworldTTSStreamBuffersTextUntilFlush(t *testing.T) {
	stream := &inworldTTSSynthesizeStream{}
	if err := stream.PushText("hello "); err != nil {
		t.Fatalf("push first: %v", err)
	}
	if err := stream.PushText("world"); err != nil {
		t.Fatalf("push second: %v", err)
	}
	if got := stream.pendingText.String(); got != "hello world" {
		t.Fatalf("pending text = %q, want concatenated text", got)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("flush without websocket: %v", err)
	}
	if got := stream.pendingText.String(); got != "" {
		t.Fatalf("pending text = %q, want reset after flush", got)
	}
}

func TestInworldTTSImplementsInterface(t *testing.T) {
	var _ tts.TTS = NewInworldTTS("test-key", "")
}

func assertInworldPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}
