package slng

import (
	"context"
	"testing"

	corestt "github.com/cavos-io/rtp-agent/core/stt"
)

func TestSTTConstructorContract(t *testing.T) {
	var _ corestt.STT = (*STT)(nil)
	provider := NewSTT("key", WithSTTModel("custom/model"), WithSTTLanguage("id"))
	if provider.apiKey != "key" || provider.model != "custom/model" || provider.language != "id" {
		t.Fatalf("NewSTT() options not applied: key=%q model=%q language=%q", provider.apiKey, provider.model, provider.language)
	}
}

func TestSTTInputSampleRate(t *testing.T) {
	for _, test := range []struct {
		name     string
		provider *STT
		want     uint32
	}{
		{name: "default", provider: NewSTT("key"), want: 16000},
		{name: "configured", provider: NewSTT("key", WithSTTSampleRate(8000)), want: 8000},
		{name: "nil receiver", provider: nil, want: 16000},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.provider.InputSampleRate(); got != test.want {
				t.Fatalf("InputSampleRate() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestSTTRejectsUnsupportedBridgeEncodingBeforeDial(t *testing.T) {
	provider := NewSTT(
		"test-key",
		WithSTTModel("deepgram/nova:3"),
		WithSTTEncoding("pcm_mulaw"),
	)

	_, err := provider.Stream(context.Background(), "")
	const want = "only pcm_s16le encoding is supported: LiveKit audio frames are 16-bit PCM and the plugin does not transcode"
	if err == nil || err.Error() != want {
		t.Fatalf("Stream() error = %v, want %q", err, want)
	}
}

func TestSTTRetainedUtteranceAudioIsBounded(t *testing.T) {
	stream := &sttStream{
		sampleRate: 1,
		encoding:   "pcm_s16le",
	}
	audio := make([]byte, maxSLNGSTTReplaySeconds*2+37)
	for i := range audio {
		audio[i] = byte(i)
	}

	stream.retainUtteranceAudioLocked(audio)

	want := audio[len(audio)-maxSLNGSTTReplaySeconds*2:]
	if len(stream.utteranceAudio) != len(want) {
		t.Fatalf("retained bytes = %d, want %d", len(stream.utteranceAudio), len(want))
	}
	for i := range want {
		if stream.utteranceAudio[i] != want[i] {
			t.Fatalf("retained byte %d = %d, want %d", i, stream.utteranceAudio[i], want[i])
		}
	}
}

func TestSTTConstructorPreservesOptionValidationErrors(t *testing.T) {
	connection := STTConnectionConfig{
		Endpoint: "wss://api.slng.ai/v1/bridges/unmute/stt/deepgram/nova:3",
	}
	for _, test := range []struct {
		name string
		opts []STTOption
		want string
	}{
		{
			name: "invalid model",
			opts: []STTOption{WithSTTModel("invalid")},
			want: slngModelIdentifierError,
		},
		{
			name: "invalid base URL",
			opts: []STTOption{WithSTTBaseURL("://invalid")},
			want: `invalid bridge base URL "://invalid"`,
		},
		{
			name: "invalid connection endpoint",
			opts: []STTOption{WithSTTConnections(STTConnectionConfig{Endpoint: "wss://api.slng.ai/wrong"})},
			want: "STT endpoint must target the Unmute Bridge path /v1/bridges/unmute/stt/",
		},
		{
			name: "model then connections",
			opts: []STTOption{WithSTTModel("deepgram/nova:3"), WithSTTConnections(connection)},
			want: "use model or connections, not both",
		},
		{
			name: "connections then model",
			opts: []STTOption{WithSTTConnections(connection), WithSTTModel("deepgram/nova:3")},
			want: "use model or connections, not both",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := NewSTT("key", test.opts...)
			if provider.optionError == nil || provider.optionError.Error() != test.want {
				t.Fatalf("option error = %v, want %q", provider.optionError, test.want)
			}
		})
	}
}
