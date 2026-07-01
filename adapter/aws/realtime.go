package aws

import (
	"errors"

	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultAWSRealtimeModel         = "amazon.nova-2-sonic-v1:0"
	defaultAWSRealtimeNovaSonic1    = "amazon.nova-sonic-v1:0"
	defaultAWSRealtimeNovaSonic2    = "amazon.nova-2-sonic-v1:0"
	defaultAWSRealtimeVoice         = "tiffany"
	defaultAWSRealtimeTurnDetection = "MEDIUM"
	defaultAWSRealtimeModalities    = "mixed"
	awsRealtimeAudioModalities      = "audio"
	awsRealtimeProvider             = "Amazon"
)

type AWSRealtimeModel struct {
	model         string
	region        string
	voice         string
	modalities    string
	turnDetection string
}

type AWSRealtimeOption func(*AWSRealtimeModel)

func NewAWSRealtimeModel(model string, opts ...AWSRealtimeOption) *AWSRealtimeModel {
	provider := &AWSRealtimeModel{
		model:         awsRealtimeModelOrDefault(model),
		region:        awsRegionOrDefault(""),
		voice:         defaultAWSRealtimeVoice,
		modalities:    defaultAWSRealtimeModalities,
		turnDetection: defaultAWSRealtimeTurnDetection,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func NewAWSRealtimeModelWithNovaSonic1(opts ...AWSRealtimeOption) *AWSRealtimeModel {
	provider := NewAWSRealtimeModel(defaultAWSRealtimeNovaSonic1, opts...)
	if provider.model == "" {
		provider.model = defaultAWSRealtimeNovaSonic1
	}
	provider.modalities = awsRealtimeAudioModalities
	return provider
}

func NewAWSRealtimeModelWithNovaSonic2(opts ...AWSRealtimeOption) *AWSRealtimeModel {
	provider := NewAWSRealtimeModel(defaultAWSRealtimeNovaSonic2, opts...)
	if provider.model == "" {
		provider.model = defaultAWSRealtimeNovaSonic2
	}
	provider.modalities = defaultAWSRealtimeModalities
	return provider
}

func WithAWSRealtimeModel(model string) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		if model != "" {
			provider.model = model
		}
	}
}

func WithAWSRealtimeRegion(region string) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		provider.region = awsRegionOrDefault(region)
	}
}

func WithAWSRealtimeVoice(voice string) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		if voice != "" {
			provider.voice = voice
		}
	}
}

func WithAWSRealtimeTurnDetection(turnDetection string) AWSRealtimeOption {
	return func(provider *AWSRealtimeModel) {
		if turnDetection != "" {
			provider.turnDetection = turnDetection
		}
	}
}

func awsRealtimeModelOrDefault(model string) string {
	if model != "" {
		return model
	}
	return defaultAWSRealtimeModel
}

func (m *AWSRealtimeModel) Label() string         { return "aws.RealtimeModel" }
func (m *AWSRealtimeModel) Model() string         { return m.model }
func (m *AWSRealtimeModel) Provider() string      { return awsRealtimeProvider }
func (m *AWSRealtimeModel) Region() string        { return m.region }
func (m *AWSRealtimeModel) Voice() string         { return m.voice }
func (m *AWSRealtimeModel) Modalities() string    { return m.modalities }
func (m *AWSRealtimeModel) TurnDetection() string { return m.turnDetection }

func (m *AWSRealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{
		MessageTruncation:       false,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             true,
		ManualFunctionCalls:     false,
		PerResponseToolChoice:   false,
	}
}

func (m *AWSRealtimeModel) Session() (llm.RealtimeSession, error) {
	return nil, errors.New("AWS Nova Sonic realtime session transport is not implemented")
}

func (m *AWSRealtimeModel) Close() error {
	return nil
}
