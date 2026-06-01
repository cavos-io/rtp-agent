package aws

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/polly"
	"github.com/aws/aws-sdk-go-v2/service/polly/types"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

type AWSTTS struct {
	client       *polly.Client
	voice        types.VoiceId
	engine       types.Engine
	outputFormat types.OutputFormat
	textType     types.TextType
	language     types.LanguageCode
	sampleRate   int
}

type AWSTTSOption func(*AWSTTS)

func WithAWSTTSVoice(voice types.VoiceId) AWSTTSOption {
	return func(t *AWSTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithAWSTTSEngine(engine types.Engine) AWSTTSOption {
	return func(t *AWSTTS) {
		if engine != "" {
			t.engine = engine
		}
	}
}

func WithAWSTTSTextType(textType types.TextType) AWSTTSOption {
	return func(t *AWSTTS) {
		if textType != "" {
			t.textType = textType
		}
	}
}

func WithAWSTTSLanguage(language types.LanguageCode) AWSTTSOption {
	return func(t *AWSTTS) {
		t.language = language
	}
}

func WithAWSTTSSampleRate(sampleRate int) AWSTTSOption {
	return func(t *AWSTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func NewAWSTTS(ctx context.Context, region string, voice string, providerOpts ...AWSTTSOption) (*AWSTTS, error) {
	if voice == "" {
		voice = string(types.VoiceIdRuth)
	}

	opts := []func(*config.LoadOptions) error{}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return newAWSTTSWithClient(polly.NewFromConfig(cfg), voice, providerOpts...), nil
}

func newAWSTTSWithClient(client *polly.Client, voice string, opts ...AWSTTSOption) *AWSTTS {
	if voice == "" {
		voice = string(types.VoiceIdRuth)
	}
	provider := &AWSTTS{
		client:       client,
		voice:        types.VoiceId(voice),
		engine:       types.EngineGenerative,
		outputFormat: types.OutputFormatMp3,
		textType:     types.TextTypeText,
		sampleRate:   16000,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *AWSTTS) Label() string { return "aws.TTS" }
func (t *AWSTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *AWSTTS) SampleRate() int  { return t.sampleRate }
func (t *AWSTTS) NumChannels() int { return 1 }

func (t *AWSTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	out, err := t.client.SynthesizeSpeech(ctx, buildAWSSynthesizeSpeechInput(t, text))
	if err != nil {
		return nil, err
	}

	return &awsTTSChunkedStream{
		stream:     out.AudioStream,
		sampleRate: t.sampleRate,
	}, nil
}

func buildAWSSynthesizeSpeechInput(t *AWSTTS, text string) *polly.SynthesizeSpeechInput {
	input := &polly.SynthesizeSpeechInput{
		OutputFormat: t.outputFormat,
		Text:         aws.String(text),
		VoiceId:      t.voice,
		SampleRate:   aws.String(fmt.Sprintf("%d", t.sampleRate)),
		Engine:       t.engine,
		TextType:     t.textType,
	}
	if t.language != "" {
		input.LanguageCode = t.language
	}
	return input
}

func (t *AWSTTS) UpdateOptions(opts ...AWSTTSOption) {
	for _, opt := range opts {
		opt(t)
	}
}

func (t *AWSTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming input for polly tts is not supported natively via rest")
}

type awsTTSChunkedStream struct {
	stream     io.ReadCloser
	sampleRate int
}

func (s *awsTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.stream.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *awsTTSChunkedStream) Close() error {
	return s.stream.Close()
}
