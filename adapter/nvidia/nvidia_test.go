package nvidia

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestNvidiaPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.nvidia" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.nvidia", PluginTitle)
	}
	if PluginVersion == "" {
		t.Fatalf("PluginVersion = %q, want non-empty project release version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.nvidia" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.nvidia", PluginPackage)
	}
}

func TestNvidiaTTSReferenceDefaultsAndCapabilities(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	if provider.apiKey != "secret" {
		t.Fatalf("apiKey = %q, want secret", provider.apiKey)
	}
	if got, want := provider.voice, "Magpie-Multilingual.EN-US.Leo"; got != want {
		t.Fatalf("voice = %q, want reference default voice %q", got, want)
	}
	if got, want := provider.server, "grpc.nvcf.nvidia.com:443"; got != want {
		t.Fatalf("server = %q, want reference default server %q", got, want)
	}
	if got, want := provider.functionID, "877104f7-e885-42b9-8de8-f6e4c6303969"; got != want {
		t.Fatalf("functionID = %q, want reference function id %q", got, want)
	}
	if got, want := provider.languageCode, "en-US"; got != want {
		t.Fatalf("languageCode = %q, want %q", got, want)
	}
	if !provider.useSSL {
		t.Fatal("useSSL = false, want reference default true")
	}
	if got, want := provider.Label(), "nvidia.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := tts.Model(provider), "Magpie-Multilingual.EN-US.Leo"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "nvidia"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 16000; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := provider.NumChannels(), 1; got != want {
		t.Fatalf("NumChannels() = %d, want %d", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
}

func TestNvidiaTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "env-secret")

	provider, err := NewNvidiaTTS("", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	if got, want := provider.apiKey, "env-secret"; got != want {
		t.Fatalf("apiKey = %q, want environment key %q", got, want)
	}
}

func TestNvidiaTTSRequiresAPIKeyWhenUsingSSL(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")

	_, err := NewNvidiaTTS("", "")

	if err == nil || !strings.Contains(err.Error(), "nvidia api key") {
		t.Fatalf("NewNvidiaTTS error = %v, want missing key error", err)
	}
}

func TestNvidiaTTSAllowsLocalRivaWithoutAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")

	provider, err := NewNvidiaTTS("", "", WithNvidiaTTSUseSSL(false))
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v, want local Riva config without key", err)
	}

	if provider.useSSL {
		t.Fatal("useSSL = true, want false")
	}
	if provider.apiKey != "" {
		t.Fatalf("apiKey = %q, want empty local key", provider.apiKey)
	}
}

func TestNvidiaTTSOptionsMatchReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "Magpie-Multilingual.ID-ID.Ayu",
		WithNvidiaTTSServer("localhost:50051"),
		WithNvidiaTTSFunctionID("local-function"),
		WithNvidiaTTSLanguageCode("id-ID"),
		WithNvidiaTTSUseSSL(false),
	)
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	if got, want := provider.voice, "Magpie-Multilingual.ID-ID.Ayu"; got != want {
		t.Fatalf("voice = %q, want %q", got, want)
	}
	if got, want := provider.server, "localhost:50051"; got != want {
		t.Fatalf("server = %q, want %q", got, want)
	}
	if got, want := provider.functionID, "local-function"; got != want {
		t.Fatalf("functionID = %q, want %q", got, want)
	}
	if got, want := provider.languageCode, "id-ID"; got != want {
		t.Fatalf("languageCode = %q, want %q", got, want)
	}
	if provider.useSSL {
		t.Fatal("useSSL = true, want false")
	}
}

func TestNvidiaTTSReportsUnsupportedRivaCalls(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	if _, err := provider.Synthesize(context.Background(), "hello"); err == nil || !strings.Contains(err.Error(), "riva tts synthesis is not implemented") {
		t.Fatalf("Synthesize() error = %v, want explicit unsupported synthesis error", err)
	}
	if _, err := provider.Stream(context.Background()); err == nil || !strings.Contains(err.Error(), "riva tts streaming is not implemented") {
		t.Fatalf("Stream() error = %v, want explicit unsupported stream error", err)
	}
}

func TestNvidiaTTSReturnsCallerCancellationBeforeUnsupportedTransport(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := provider.Synthesize(ctx, "hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Synthesize() error = %v, want context.Canceled", err)
	}
	if _, err := provider.Stream(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream() error = %v, want context.Canceled", err)
	}
}

func TestNvidiaSTTReferenceDefaultsAndCapabilities(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	if provider.apiKey != "secret" {
		t.Fatalf("apiKey = %q, want secret", provider.apiKey)
	}
	if got, want := provider.model, "parakeet-1.1b-en-US-asr-streaming-silero-vad-sortformer"; got != want {
		t.Fatalf("model = %q, want reference default model %q", got, want)
	}
	if got, want := provider.server, "grpc.nvcf.nvidia.com:443"; got != want {
		t.Fatalf("server = %q, want reference default server %q", got, want)
	}
	if got, want := provider.functionID, "1598d209-5e27-4d3c-8079-4751568b1081"; got != want {
		t.Fatalf("functionID = %q, want reference function id %q", got, want)
	}
	if got, want := provider.language, "en-US"; got != want {
		t.Fatalf("language = %q, want %q", got, want)
	}
	if !provider.punctuate {
		t.Fatal("punctuate = false, want reference default true")
	}
	if !provider.useSSL {
		t.Fatal("useSSL = false, want reference default true")
	}
	if got, want := provider.Label(), "nvidia.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "parakeet-1.1b-en-US-asr-streaming-silero-vad-sortformer"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "nvidia"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if got, want := provider.InputSampleRate(), uint32(16000); got != want {
		t.Fatalf("InputSampleRate() = %d, want reference sample rate %d", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize || caps.Diarization || caps.AlignedTranscript != "word" {
		t.Fatalf("Capabilities() = %+v, want reference streaming interim STT with word alignment and without offline recognition", caps)
	}
}

func TestNvidiaSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "env-secret")

	provider, err := NewNvidiaSTT("", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}

	if got, want := provider.apiKey, "env-secret"; got != want {
		t.Fatalf("apiKey = %q, want environment key %q", got, want)
	}
}

func TestNvidiaSTTRequiresAPIKeyWhenUsingSSL(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")

	_, err := NewNvidiaSTT("", "")

	if err == nil || !strings.Contains(err.Error(), "nvidia api key") {
		t.Fatalf("NewNvidiaSTT error = %v, want missing key error", err)
	}
}

func TestNvidiaSTTAllowsLocalRivaWithoutAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")

	provider, err := NewNvidiaSTT("", "", WithNvidiaSTTUseSSL(false))
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v, want local Riva config without key", err)
	}

	if provider.useSSL {
		t.Fatal("useSSL = true, want false")
	}
	if provider.apiKey != "" {
		t.Fatalf("apiKey = %q, want empty local key", provider.apiKey)
	}
}

func TestNvidiaSTTOptionsMatchReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "parakeet-rnnt-1.1b",
		WithNvidiaSTTServer("localhost:50051"),
		WithNvidiaSTTFunctionID("local-function"),
		WithNvidiaSTTLanguage("id-ID"),
		WithNvidiaSTTSampleRate(24000),
		WithNvidiaSTTUseSSL(false),
		WithNvidiaSTTDiarization(true),
		WithNvidiaSTTMaxSpeakerCount(4),
		WithNvidiaSTTPunctuate(false),
	)
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}

	if got, want := provider.model, "parakeet-rnnt-1.1b"; got != want {
		t.Fatalf("model = %q, want %q", got, want)
	}
	if got, want := provider.server, "localhost:50051"; got != want {
		t.Fatalf("server = %q, want %q", got, want)
	}
	if got, want := provider.functionID, "local-function"; got != want {
		t.Fatalf("functionID = %q, want %q", got, want)
	}
	if got, want := provider.language, "id-ID"; got != want {
		t.Fatalf("language = %q, want %q", got, want)
	}
	if got, want := provider.InputSampleRate(), uint32(24000); got != want {
		t.Fatalf("InputSampleRate() = %d, want %d", got, want)
	}
	if !provider.diarization {
		t.Fatal("diarization = false, want true")
	}
	if got, want := provider.maxSpeakerCount, 4; got != want {
		t.Fatalf("maxSpeakerCount = %d, want %d", got, want)
	}
	if provider.punctuate {
		t.Fatal("punctuate = true, want false")
	}
	if caps := provider.Capabilities(); !caps.Diarization || caps.AlignedTranscript != "word" {
		t.Fatalf("Capabilities() = %+v, want reference diarization and word alignment", caps)
	}
	if provider.useSSL {
		t.Fatal("useSSL = true, want false")
	}
}

func TestNvidiaSTTStreamExposesReferenceTimingOffset(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	timing, ok := stream.(stt.StreamTiming)
	if !ok {
		t.Fatal("stream does not implement stt.StreamTiming")
	}

	stt.SetStreamStartTimeOffset(timing, 1.25)
	stt.SetStreamStartTime(timing, 10.5)
	if got, want := timing.StartTimeOffset(), 1.25; got != want {
		t.Fatalf("StartTimeOffset() = %v, want %v", got, want)
	}
	if got, want := timing.StartTime(), 10.5; got != want {
		t.Fatalf("StartTime() = %v, want %v", got, want)
	}
}

func TestNvidiaSTTStreamDropsEmptyFramesLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.PushFrame(&model.AudioFrame{}); err != nil {
		t.Fatalf("PushFrame(empty) error = %v, want nil", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0, 1}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err == nil || !strings.Contains(err.Error(), "riva stt streaming is not implemented") {
		t.Fatalf("PushFrame(non-empty) error = %v, want explicit unsupported streaming error", err)
	}
}

func TestNvidiaSTTStreamReturnsCallerCancellationBeforeUnsupportedTransport(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := provider.Stream(ctx, "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	cancel()

	err = stream.PushFrame(&model.AudioFrame{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PushFrame() error = %v, want context.Canceled", err)
	}
	if err := stream.Flush(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Flush() error = %v, want context.Canceled", err)
	}
	if event, err := stream.Next(); !errors.Is(err, context.Canceled) || event != nil {
		t.Fatalf("Next() = (%v, %v), want nil context.Canceled", event, err)
	}
}

func TestNvidiaSTTReportsUnsupportedRivaCallsAndClosedInput(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	if _, err := provider.Recognize(context.Background(), nil, ""); err == nil || !strings.Contains(err.Error(), "riva stt recognition is not implemented") {
		t.Fatalf("Recognize() error = %v, want explicit unsupported recognition error", err)
	}

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaSTTStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaSTTStream", stream)
	}
	if got, want := concrete.language, "id-ID"; got != want {
		t.Fatalf("stream language = %q, want %q", got, want)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err == nil || !strings.Contains(err.Error(), "riva stt streaming is not implemented") {
		t.Fatalf("PushFrame() error = %v, want explicit unsupported streaming error", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{}); err != io.ErrClosedPipe {
		t.Fatalf("PushFrame() after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
	if err := stream.Flush(); err != io.ErrClosedPipe {
		t.Fatalf("Flush() after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
	if event, err := stream.Next(); err != io.EOF || event != nil {
		t.Fatalf("Next() after Close = (%v, %v), want nil EOF", event, err)
	}
}
