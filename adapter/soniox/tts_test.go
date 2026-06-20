package soniox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestSonioxTTSDefaultsMatchReference(t *testing.T) {
	provider := NewSonioxTTS("test-key")

	if provider.websocketURL != "wss://tts-rt.soniox.com/tts-websocket" {
		t.Fatalf("websocket URL = %q, want reference URL", provider.websocketURL)
	}
	if provider.model != "tts-rt-v1-preview" {
		t.Fatalf("model = %q, want tts-rt-v1-preview", provider.model)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.voice != "Maya" {
		t.Fatalf("voice = %q, want Maya", provider.voice)
	}
	if provider.audioFormat != "pcm_s16le" {
		t.Fatalf("audio format = %q, want pcm_s16le", provider.audioFormat)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.bitrate != nil {
		t.Fatalf("bitrate = %#v, want nil", provider.bitrate)
	}
	if got := tts.Model(provider); got != "tts-rt-v1-preview" {
		t.Fatalf("model metadata = %q, want tts-rt-v1-preview", got)
	}
	if got := tts.Provider(provider); got != "Soniox" {
		t.Fatalf("provider metadata = %q, want Soniox", got)
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if caps.AlignedTranscript {
		t.Fatal("aligned transcript = true, want false")
	}
}

func TestNewSonioxTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SONIOX_API_KEY", "env-key")

	provider := NewSonioxTTS("")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSonioxTTS("explicit-key")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSonioxTTSStreamRequiresAPIKeyBeforeDial(t *testing.T) {
	t.Setenv("SONIOX_API_KEY", "")
	provider := NewSonioxTTS("", WithSonioxTTSWebsocketURL("://bad-url"))

	_, err := provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "SONIOX_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", err)
	}
}

func TestSonioxTTSOptionsBuildReferenceStartConfig(t *testing.T) {
	bitrate := 64000
	provider := NewSonioxTTS("test-key",
		WithSonioxTTSWebsocketURL("ws://soniox.example/tts"),
		WithSonioxTTSModel("tts-custom"),
		WithSonioxTTSLanguage("es"),
		WithSonioxTTSVoice("Adrian"),
		WithSonioxTTSAudioFormat("mp3"),
		WithSonioxTTSSampleRate(48000),
		WithSonioxTTSBitrate(bitrate),
	)

	config := buildSonioxTTSStartConfig(provider, "stream-1")

	assertSonioxTTSConfig(t, config, "api_key", "test-key")
	assertSonioxTTSConfig(t, config, "model", "tts-custom")
	assertSonioxTTSConfig(t, config, "language", "es")
	assertSonioxTTSConfig(t, config, "voice", "Adrian")
	assertSonioxTTSConfig(t, config, "audio_format", "mp3")
	assertSonioxTTSConfig(t, config, "stream_id", "stream-1")
	if config["sample_rate"] != 48000 {
		t.Fatalf("sample_rate = %#v, want 48000", config["sample_rate"])
	}
	if config["bitrate"] != 64000 {
		t.Fatalf("bitrate = %#v, want 64000", config["bitrate"])
	}
	if provider.SampleRate() != 48000 || provider.NumChannels() != 1 {
		t.Fatalf("sample/channels = %d/%d, want 48000/1", provider.SampleRate(), provider.NumChannels())
	}
}

func TestSonioxTTSOutboundMessagesMatchReference(t *testing.T) {
	text := buildSonioxTTSTextMessage("stream-1", "hello", false)
	if text["stream_id"] != "stream-1" || text["text"] != "hello" {
		t.Fatalf("text message = %#v, want stream text", text)
	}
	if _, ok := text["text_end"]; ok {
		t.Fatalf("text_end present for text delta: %#v", text)
	}

	end := buildSonioxTTSTextMessage("stream-1", "", true)
	if end["stream_id"] != "stream-1" || end["text_end"] != true {
		t.Fatalf("end message = %#v, want text_end", end)
	}
	if _, ok := end["text"]; ok {
		t.Fatalf("text present for end message: %#v", end)
	}

	cancel := buildSonioxTTSCancelMessage("stream-1")
	if cancel["stream_id"] != "stream-1" || cancel["cancel"] != true {
		t.Fatalf("cancel message = %#v, want cancel", cancel)
	}

	keepalive := buildSonioxTTSKeepaliveMessage()
	if keepalive["keep_alive"] != true {
		t.Fatalf("keepalive = %#v, want keep_alive true", keepalive)
	}
}

func TestSonioxTTSStreamLazilySendsStartConfigLikeReference(t *testing.T) {
	provider := NewSonioxTTS("test-key")
	var sent []map[string]any
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &sonioxTTSSynthesizeStream{
		provider: provider,
		streamID: "stream-1",
		ctx:      ctx,
		events:   make(chan *tts.SynthesizedAudio, 1),
		writeMessage: func(message map[string]any) error {
			sent = append(sent, message)
			return nil
		},
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush before text error = %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("messages after empty flush = %#v, want none", sent)
	}
	if _, err := nextSonioxTTSAudioWithTimeout(t, stream); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after empty flush error = %v, want EOF", err)
	}

	stream = &sonioxTTSSynthesizeStream{
		provider: provider,
		streamID: "stream-1",
		ctx:      ctx,
		events:   make(chan *tts.SynthesizedAudio, 1),
		writeMessage: func(message map[string]any) error {
			sent = append(sent, message)
			return nil
		},
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if len(sent) != 2 {
		t.Fatalf("messages after first text = %#v, want start config then text", sent)
	}
	if sent[0]["api_key"] != "test-key" || sent[0]["stream_id"] != "stream-1" {
		t.Fatalf("first message = %#v, want start config", sent[0])
	}
	if sent[1]["text"] != "hello" || sent[1]["stream_id"] != "stream-1" {
		t.Fatalf("second message = %#v, want text delta", sent[1])
	}
}

func TestSonioxTTSCloseBeforeTextSendsNoCancelLikeReference(t *testing.T) {
	var sent []map[string]any
	stream := &sonioxTTSSynthesizeStream{
		streamID: "stream-1",
		writeMessage: func(message map[string]any) error {
			sent = append(sent, message)
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("messages after close before text = %#v, want none", sent)
	}

	stream = &sonioxTTSSynthesizeStream{
		streamID:   "stream-1",
		configSent: true,
		writeMessage: func(message map[string]any) error {
			sent = append(sent, message)
			return nil
		},
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after config error = %v", err)
	}
	if len(sent) != 1 || sent[0]["cancel"] != true {
		t.Fatalf("messages after close with config = %#v, want cancel", sent)
	}
}

func TestSonioxTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("websocket closed")
	stream := &sonioxTTSSynthesizeStream{
		streamID: "stream-1",
		writeMessage: func(map[string]any) error {
			return writeErr
		},
	}

	err := stream.PushText("hello")
	if !errors.Is(err, writeErr) {
		t.Fatalf("PushText error = %v, want write error", err)
	}
	if !stream.closed {
		t.Fatal("stream closed = false after write failure, want true")
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second PushText error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush error = %v, want io.ErrClosedPipe", err)
	}
}

func TestSonioxTTSProviderCloseClosesActiveStreams(t *testing.T) {
	cancelled := false
	closeCalls := 0
	provider := NewSonioxTTS("test-key")
	stream := &sonioxTTSSynthesizeStream{
		streamID: "stream-1",
		cancel:   func() { cancelled = true },
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after provider Close")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestSonioxTTSAudioFromMessageDecodesAudioAndTermination(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"stream_id":  "stream-1",
		"audio":      base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04}),
		"audio_end":  true,
		"terminated": true,
	})

	audio, audioEnd, done, err := sonioxTTSAudioFromMessage(payload, "stream-1", 24000)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	assertSonioxTTSAudio(t, audio, []byte{0x01, 0x02, 0x03, 0x04})
	if !audioEnd {
		t.Fatal("audioEnd = false, want true for audio_end response")
	}
	if !done {
		t.Fatal("done = false, want true for terminated response")
	}
}

func TestSonioxTTSAudioFromMessageIgnoresOtherStreams(t *testing.T) {
	audio, audioEnd, done, err := sonioxTTSAudioFromMessage([]byte(`{"stream_id":"other","audio":"AQI="}`), "stream-1", 24000)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if audio != nil || audioEnd || done {
		t.Fatalf("audio/audioEnd/done = %#v/%v/%v, want ignored message", audio, audioEnd, done)
	}
}

func TestSonioxTTSAudioFromMessageReportsProviderError(t *testing.T) {
	_, _, _, err := sonioxTTSAudioFromMessage([]byte(`{"stream_id":"stream-1","error_code":429,"error_message":"rate limited"}`), "stream-1", 24000)
	if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v, want provider error", err)
	}
}

func TestSonioxTTSStreamRejectsBareTerminationLikeReference(t *testing.T) {
	stream := &sonioxTTSSynthesizeStream{
		streamID:   "stream-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
	}

	done, err := stream.handleSonioxTTSMessage([]byte(`{"stream_id":"stream-1","terminated":true}`))

	if err == nil || !strings.Contains(err.Error(), "terminated without producing audio") {
		t.Fatalf("handle message error = %v, want bare termination error", err)
	}
	if done {
		t.Fatal("done = true, want false on bare termination error")
	}

	stream = &sonioxTTSSynthesizeStream{
		streamID:   "stream-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
	}
	done, err = stream.handleSonioxTTSMessage([]byte(`{"stream_id":"stream-1","audio_end":true}`))
	if err != nil || done {
		t.Fatalf("audio_end handling = done %v error %v, want open stream", done, err)
	}
	done, err = stream.handleSonioxTTSMessage([]byte(`{"stream_id":"stream-1","terminated":true}`))
	if err != nil || !done {
		t.Fatalf("terminated after audio_end = done %v error %v, want normal completion", done, err)
	}
}

func TestSonioxTTSAudioFrameClonesAudioData(t *testing.T) {
	input := []byte{0x01, 0x02, 0x03, 0x04}

	audio := sonioxTTSAudioFrame(input, 16000)
	input[0] = 0xff

	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want frame", audio)
	}
	if audio.Frame.Data[0] != 0x01 {
		t.Fatalf("frame data was mutated with input: %#v", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", audio.Frame.SampleRate)
	}
}

func assertSonioxTTSConfig(t *testing.T, config map[string]any, key string, want string) {
	t.Helper()
	if got := config[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func assertSonioxTTSAudio(t *testing.T, audio *tts.SynthesizedAudio, want []byte) {
	t.Helper()
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %#v, want frame", audio)
	}
	if string(audio.Frame.Data) != string(want) {
		t.Fatalf("frame data = %#v, want %#v", audio.Frame.Data, want)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want 1", audio.Frame.NumChannels)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("samples = %d, want 2", audio.Frame.SamplesPerChannel)
	}
}

func nextSonioxTTSAudioWithTimeout(t *testing.T, stream *sonioxTTSSynthesizeStream) (*tts.SynthesizedAudio, error) {
	t.Helper()
	type nextResult struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	resultCh := make(chan nextResult, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- nextResult{audio: audio, err: err}
	}()
	select {
	case result := <-resultCh:
		return result.audio, result.err
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for Soniox TTS Next")
		return nil, nil
	}
}
