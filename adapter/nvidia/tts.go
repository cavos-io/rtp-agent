package nvidia

import (
	"context"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultNvidiaTTSServer     = "grpc.nvcf.nvidia.com:443"
	defaultNvidiaTTSVoice      = "Magpie-Multilingual.EN-US.Leo"
	defaultNvidiaTTSFunctionID = "877104f7-e885-42b9-8de8-f6e4c6303969"
	defaultNvidiaTTSLanguage   = "en-US"
	defaultNvidiaTTSSampleRate = 16000
	nvidiaAPIKeyEnv            = "NVIDIA_API_KEY"
)

type NvidiaTTS struct {
	apiKey       string
	voice        string
	functionID   string
	server       string
	sampleRate   int
	useSSL       bool
	languageCode string
}

type NvidiaTTSOption func(*NvidiaTTS)

func WithNvidiaTTSServer(server string) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		if server != "" {
			t.server = server
		}
	}
}

func WithNvidiaTTSFunctionID(functionID string) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		if functionID != "" {
			t.functionID = functionID
		}
	}
}

func WithNvidiaTTSLanguageCode(languageCode string) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		if languageCode != "" {
			t.languageCode = languageCode
		}
	}
}

func WithNvidiaTTSUseSSL(useSSL bool) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		t.useSSL = useSSL
	}
}

func NewNvidiaTTS(apiKey string, voice string, opts ...NvidiaTTSOption) (*NvidiaTTS, error) {
	if apiKey == "" {
		apiKey = os.Getenv(nvidiaAPIKeyEnv)
	}
	if voice == "" {
		voice = defaultNvidiaTTSVoice
	}

	provider := &NvidiaTTS{
		apiKey:       apiKey,
		voice:        voice,
		functionID:   defaultNvidiaTTSFunctionID,
		server:       defaultNvidiaTTSServer,
		sampleRate:   defaultNvidiaTTSSampleRate,
		useSSL:       true,
		languageCode: defaultNvidiaTTSLanguage,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.useSSL && provider.apiKey == "" {
		return nil, fmt.Errorf("nvidia api key is required while using SSL")
	}
	return provider, nil
}

func (t *NvidiaTTS) Label() string    { return "nvidia.TTS" }
func (t *NvidiaTTS) Model() string    { return t.voice }
func (t *NvidiaTTS) Provider() string { return "nvidia" }
func (t *NvidiaTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *NvidiaTTS) SampleRate() int  { return t.sampleRate }
func (t *NvidiaTTS) NumChannels() int { return 1 }

func (t *NvidiaTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	return nil, fmt.Errorf("nvidia riva tts synthesis is not implemented")
}

func (t *NvidiaTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("nvidia riva tts streaming is not implemented")
}
