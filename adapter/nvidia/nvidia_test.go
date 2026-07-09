package nvidia

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
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

func TestNvidiaRealtimeDefaultsMatchReference(t *testing.T) {
	t.Setenv("PERSONAPLEX_URL", "")

	model := NewNvidiaRealtimeModel()

	if got, want := model.Model(), "personaplex-7b"; got != want {
		t.Fatalf("Model() = %q, want %q", got, want)
	}
	if got, want := model.Provider(), "nvidia"; got != want {
		t.Fatalf("Provider() = %q, want %q", got, want)
	}
	if got, want := model.Label(), "personaplex-NATF2"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := model.baseURL, "localhost:8998"; got != want {
		t.Fatalf("baseURL = %q, want %q", got, want)
	}
	if got, want := model.voice, "NATF2"; got != want {
		t.Fatalf("voice = %q, want %q", got, want)
	}
	if got, want := model.textPrompt, "You are a helpful assistant."; got != want {
		t.Fatalf("textPrompt = %q, want %q", got, want)
	}
	if model.seed != nil {
		t.Fatalf("seed = %v, want nil", *model.seed)
	}
	if got, want := model.silenceThresholdMS, 500; got != want {
		t.Fatalf("silenceThresholdMS = %d, want %d", got, want)
	}
	if model.useSSL {
		t.Fatal("useSSL = true, want false for reference localhost default")
	}
	if got, want := model.InputSampleRate(), 24000; got != want {
		t.Fatalf("InputSampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := model.OutputSampleRate(), 24000; got != want {
		t.Fatalf("OutputSampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := model.NumChannels(), 1; got != want {
		t.Fatalf("NumChannels() = %d, want mono", got)
	}
	caps := model.Capabilities()
	if caps.MessageTruncation || caps.TurnDetection || caps.UserTranscription || caps.AutoToolReplyGeneration || !caps.AudioOutput || caps.ManualFunctionCalls || caps.PerResponseToolChoice {
		t.Fatalf("Capabilities() = %+v, want PersonaPlex reference audio-output-only realtime flags", caps)
	}
	var realtime llm.RealtimeModel = model
	if err := realtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNvidiaRealtimeOptionsMatchReference(t *testing.T) {
	seed := 42
	model := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL("wss://personaplex.example:9443"),
		WithNvidiaRealtimeVoice("VARF1"),
		WithNvidiaRealtimeTextPrompt("Speak tersely."),
		WithNvidiaRealtimeSeed(seed),
		WithNvidiaRealtimeSilenceThresholdMS(750),
	)

	if got, want := model.baseURL, "personaplex.example:9443"; got != want {
		t.Fatalf("baseURL = %q, want stripped host %q", got, want)
	}
	if !model.useSSL {
		t.Fatal("useSSL = false, want true for wss URL")
	}
	if got, want := model.voice, "VARF1"; got != want {
		t.Fatalf("voice = %q, want %q", got, want)
	}
	if got, want := model.textPrompt, "Speak tersely."; got != want {
		t.Fatalf("textPrompt = %q, want %q", got, want)
	}
	if model.seed == nil || *model.seed != seed {
		t.Fatalf("seed = %v, want %d", model.seed, seed)
	}
	if got, want := model.silenceThresholdMS, 750; got != want {
		t.Fatalf("silenceThresholdMS = %d, want %d", got, want)
	}
	if _, err := model.Session(); err == nil || !strings.Contains(err.Error(), "personaplex realtime session is not implemented") {
		t.Fatalf("Session() error = %v, want explicit unsupported realtime session error", err)
	}
}

func TestNvidiaRealtimeAllowsZeroSilenceThresholdLikeReference(t *testing.T) {
	model := NewNvidiaRealtimeModel(WithNvidiaRealtimeSilenceThresholdMS(0))

	if got, want := model.silenceThresholdMS, 0; got != want {
		t.Fatalf("silenceThresholdMS = %d, want explicit reference override %d", got, want)
	}
}

func TestNvidiaRealtimeStripsOnlyFirstURLSchemeLikeReference(t *testing.T) {
	model := NewNvidiaRealtimeModel(WithNvidiaRealtimeBaseURL("wss://http://personaplex.local:8998"))

	if got, want := model.baseURL, "http://personaplex.local:8998"; got != want {
		t.Fatalf("baseURL = %q, want one reference scheme stripped to %q", got, want)
	}
	if !model.useSSL {
		t.Fatal("useSSL = false, want true from first wss scheme")
	}
}

func TestNvidiaRealtimeWebsocketURLMatchesReference(t *testing.T) {
	model := NewNvidiaRealtimeModel(
		WithNvidiaRealtimeBaseURL("https://personaplex.example:9443"),
		WithNvidiaRealtimeVoice("VARF1"),
		WithNvidiaRealtimeTextPrompt("Speak tersely & listen."),
		WithNvidiaRealtimeSeed(7),
	)

	got := model.websocketURL()
	want := "wss://personaplex.example:9443/api/chat?seed=7&text_prompt=Speak%20tersely%20%26%20listen.&voice_prompt=VARF1.pt"
	if got != want {
		t.Fatalf("websocketURL() = %q, want %q", got, want)
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

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v, want chunked stream before native transport", err)
	}
	doneStream, ok := stream.(tts.DoneStream)
	if !ok {
		t.Fatal("chunked stream does not implement tts.DoneStream")
	}
	exceptionStream, ok := stream.(tts.ExceptionStream)
	if !ok {
		t.Fatal("chunked stream does not implement tts.ExceptionStream")
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before synthesis output")
	}
	if audio, err := stream.Next(); err == nil || !strings.Contains(err.Error(), "riva tts synthesis is not implemented") || audio != nil {
		t.Fatalf("Next() = (%v, %v), want nil explicit unsupported synthesis error", audio, err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after synthesis error")
	}
	if err := exceptionStream.Exception(); err == nil || !strings.Contains(err.Error(), "riva tts synthesis is not implemented") {
		t.Fatalf("Exception() after synthesis error = %v, want unsupported synthesis error", err)
	}
}

func TestNvidiaTTSSynthesizeEmptyTextEndsWithoutTransport(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	doneStream, ok := stream.(tts.DoneStream)
	if !ok {
		t.Fatal("chunked stream does not implement tts.DoneStream")
	}
	exceptionStream, ok := stream.(tts.ExceptionStream)
	if !ok {
		t.Fatal("chunked stream does not implement tts.ExceptionStream")
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before empty input EOF")
	}
	if audio, err := stream.Next(); err != io.EOF || audio != nil {
		t.Fatalf("Next() = (%v, %v), want nil EOF for empty input", audio, err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after empty input EOF")
	}
	if err := exceptionStream.Exception(); err != nil {
		t.Fatalf("Exception() after empty input EOF = %v, want nil", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNvidiaTTSStreamConstructsBeforeUnsupportedTransport(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v, want stream construction before native transport", err)
	}
	if err := stream.PushText(""); err != nil {
		t.Fatalf("PushText(empty) error = %v, want nil", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v, want nil", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText(non-empty) error = %v, want nil before native transport", err)
	}
	if err := stream.PushText(" again"); err != nil {
		t.Fatalf("PushText(second) error = %v, want nil before native transport", err)
	}
	doneStream, ok := stream.(tts.DoneStream)
	if !ok {
		t.Fatal("synthesize stream does not implement tts.DoneStream")
	}
	exceptionStream, ok := stream.(tts.ExceptionStream)
	if !ok {
		t.Fatal("synthesize stream does not implement tts.ExceptionStream")
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before stream output")
	}
	if audio, err := stream.Next(); err == nil || !strings.Contains(err.Error(), "riva tts streaming is not implemented") || audio != nil {
		t.Fatalf("Next() = (%v, %v), want nil explicit unsupported stream error", audio, err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after stream output error")
	}
	if err := exceptionStream.Exception(); err == nil || !strings.Contains(err.Error(), "riva tts streaming is not implemented") {
		t.Fatalf("Exception() after stream output error = %v, want unsupported stream error", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.PushText("late"); err != io.ErrClosedPipe {
		t.Fatalf("PushText() after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
	if err := stream.Flush(); err != io.ErrClosedPipe {
		t.Fatalf("Flush() after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
	if audio, err := stream.Next(); err != io.EOF || audio != nil {
		t.Fatalf("Next() after Close = (%v, %v), want nil EOF", audio, err)
	}
}

func TestNvidiaTTSStreamEndInputCompletesEmptyReferenceStream(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	doneStream, ok := stream.(tts.DoneStream)
	if !ok {
		t.Fatal("synthesize stream does not implement tts.DoneStream")
	}
	exceptionStream, ok := stream.(tts.ExceptionStream)
	if !ok {
		t.Fatal("synthesize stream does not implement tts.ExceptionStream")
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before end input")
	}
	if err := tts.EndSynthesizeStreamInput(stream); err != nil {
		t.Fatalf("EndSynthesizeStreamInput() error = %v", err)
	}
	if err := stream.PushText("late"); err != nil {
		t.Fatalf("PushText() after EndInput error = %v, want nil ignored late text", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() after EndInput error = %v, want nil no-op", err)
	}
	if audio, err := stream.Next(); err != io.EOF || audio != nil {
		t.Fatalf("Next() after empty EndInput = (%v, %v), want nil EOF", audio, err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after empty EndInput EOF")
	}
	if err := exceptionStream.Exception(); err != nil {
		t.Fatalf("Exception() after empty EndInput EOF = %v, want nil", err)
	}
}

func TestNvidiaTTSStreamIgnoresSecondSegmentLikeReference(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	concrete, ok := stream.(*nvidiaTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *nvidiaTTSSynthesizeStream", stream)
	}

	if err := stream.PushText("first"); err != nil {
		t.Fatalf("PushText(first) error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := stream.PushText("second"); err != nil {
		t.Fatalf("PushText(second) error = %v, want nil ignored second segment", err)
	}
	if got, want := concrete.text, "first"; got != want {
		t.Fatalf("stream text = %q, want only first segment %q", got, want)
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

func TestNvidiaSTTResponseEventsMatchReferenceOrdering(t *testing.T) {
	stream := &nvidiaSTTStream{
		language:        "en-US",
		startTimeOffset: 1.25,
		stt:             &NvidiaSTT{diarization: true},
	}

	events := stream.eventsFromResult(nvidiaSTTResult{
		RequestID: "nvidia-response-1",
		IsFinal:   false,
		Alternative: nvidiaSTTAlternative{
			Transcript: "hello",
			Confidence: 0.7,
			Words: []nvidiaSTTWord{{
				Word:       "hello",
				StartTime:  100,
				EndTime:    340,
				SpeakerTag: 2,
			}},
		},
	})
	if len(events) != 2 {
		t.Fatalf("interim event count = %d, want start_of_speech + interim_transcript", len(events))
	}
	if events[0].Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("event[0].Type = %q, want start_of_speech", events[0].Type)
	}
	if events[1].Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event[1].Type = %q, want interim_transcript", events[1].Type)
	}
	if got, want := events[1].RequestID, "nvidia-response-1"; got != want {
		t.Fatalf("interim RequestID = %q, want %q", got, want)
	}
	interim := events[1].Alternatives[0]
	if interim.Text != "hello" || interim.Language != "en-US" || interim.Confidence != 0.7 {
		t.Fatalf("interim speech data = %+v, want transcript/language/confidence from Riva alternative", interim)
	}
	if interim.SpeakerID != "" {
		t.Fatalf("interim SpeakerID = %q, want empty until final diarization", interim.SpeakerID)
	}
	if interim.StartTime != 1.35 || interim.EndTime != 1.59 {
		t.Fatalf("interim timing = (%v, %v), want seconds plus offset", interim.StartTime, interim.EndTime)
	}
	if len(interim.Words) != 1 || interim.Words[0].Text != "hello" || interim.Words[0].StartTime != 101.25 || interim.Words[0].EndTime != 341.25 {
		t.Fatalf("interim words = %+v, want reference millisecond word timings plus offset", interim.Words)
	}

	events = stream.eventsFromResult(nvidiaSTTResult{
		RequestID: "nvidia-response-2",
		IsFinal:   true,
		Alternative: nvidiaSTTAlternative{
			Transcript: "hello there",
			Confidence: 0.9,
			Words: []nvidiaSTTWord{
				{Word: "hello", StartTime: 100, EndTime: 340, SpeakerTag: 2},
				{Word: "there", StartTime: 350, EndTime: 700, SpeakerTag: 2},
				{Word: "aside", StartTime: 710, EndTime: 900, SpeakerTag: 1},
			},
		},
	})
	if len(events) != 2 {
		t.Fatalf("final event count = %d, want final_transcript + end_of_speech", len(events))
	}
	if events[0].Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event[0].Type = %q, want final_transcript", events[0].Type)
	}
	if got, want := events[0].RequestID, "nvidia-response-2"; got != want {
		t.Fatalf("final RequestID = %q, want %q", got, want)
	}
	if events[1].Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("event[1].Type = %q, want end_of_speech", events[1].Type)
	}
	final := events[0].Alternatives[0]
	if final.SpeakerID != "S2" {
		t.Fatalf("final SpeakerID = %q, want majority speaker S2", final.SpeakerID)
	}
	if final.StartTime != 1.35 || final.EndTime != 2.15 {
		t.Fatalf("final timing = (%v, %v), want first/last word seconds plus offset", final.StartTime, final.EndTime)
	}
}

func TestNvidiaSTTResponseEventsPreserveMultipleResultOrder(t *testing.T) {
	stream := &nvidiaSTTStream{language: "en-US"}

	events := stream.eventsFromResponse(nvidiaSTTResponse{
		RequestID: "nvidia-response",
		Results: []nvidiaSTTResult{
			{Alternative: nvidiaSTTAlternative{Transcript: "   "}},
			{
				IsFinal: false,
				Alternative: nvidiaSTTAlternative{
					Transcript: "first",
					Confidence: 0.4,
				},
			},
			{
				IsFinal: true,
				Alternative: nvidiaSTTAlternative{
					Transcript: "second",
					Confidence: 0.8,
				},
			},
		},
	})

	if len(events) != 4 {
		t.Fatalf("event count = %d, want start, interim, final, end", len(events))
	}
	wantTypes := []stt.SpeechEventType{
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event[%d].Type = %q, want %q", i, events[i].Type, want)
		}
	}
	if got, want := events[1].RequestID, "nvidia-response"; got != want {
		t.Fatalf("interim RequestID = %q, want %q", got, want)
	}
	if got, want := events[2].RequestID, "nvidia-response"; got != want {
		t.Fatalf("final RequestID = %q, want %q", got, want)
	}
	if got, want := events[1].Alternatives[0].Text, "first"; got != want {
		t.Fatalf("interim text = %q, want %q", got, want)
	}
	if got, want := events[2].Alternatives[0].Text, "second"; got != want {
		t.Fatalf("final text = %q, want %q", got, want)
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

func TestNvidiaSTTStreamSeedsReferenceStartTime(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	before := float64(time.Now().Add(-time.Second).UnixNano()) / float64(time.Second)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	timing, ok := stream.(stt.StreamTiming)
	if !ok {
		t.Fatal("stream does not implement stt.StreamTiming")
	}
	after := float64(time.Now().Add(time.Second).UnixNano()) / float64(time.Second)

	if got := timing.StartTime(); got < before || got > after {
		t.Fatalf("StartTime() = %v, want stream creation wall-clock between %v and %v", got, before, after)
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

func TestNvidiaSTTStreamRejectsMismatchedSampleRatesLikeReference(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.PushFrame(&model.AudioFrame{SampleRate: 16000, NumChannels: 1}); err != nil {
		t.Fatalf("PushFrame(first empty frame) error = %v, want nil", err)
	}
	err = stream.PushFrame(&model.AudioFrame{SampleRate: 8000, NumChannels: 1})
	if err == nil || !strings.Contains(err.Error(), "sample rate of the input frames must be consistent") {
		t.Fatalf("PushFrame(mismatched sample rate) error = %v, want reference consistency error", err)
	}
}

func TestNvidiaSTTStreamEndInputCompletesEmptyReferenceStream(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatal("stream does not implement stt.InputEnding")
	}

	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if err := ending.EndInput(); err != io.ErrClosedPipe {
		t.Fatalf("second EndInput() error = %v, want %v", err, io.ErrClosedPipe)
	}
	if err := stream.PushFrame(&model.AudioFrame{}); err != io.ErrClosedPipe {
		t.Fatalf("PushFrame() after EndInput error = %v, want %v", err, io.ErrClosedPipe)
	}
	if err := stream.Flush(); err != io.ErrClosedPipe {
		t.Fatalf("Flush() after EndInput error = %v, want %v", err, io.ErrClosedPipe)
	}
	if event, err := stream.Next(); err != io.EOF || event != nil {
		t.Fatalf("Next() after empty EndInput = (%v, %v), want nil EOF", event, err)
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

func TestNvidiaSTTReturnsCallerCancellationBeforeUnsupportedRecognize(t *testing.T) {
	provider, err := NewNvidiaSTT("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaSTT error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	event, err := provider.Recognize(ctx, []*model.AudioFrame{{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}}, "")
	if !errors.Is(err, context.Canceled) || event != nil {
		t.Fatalf("Recognize() = (%v, %v), want nil context.Canceled", event, err)
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
