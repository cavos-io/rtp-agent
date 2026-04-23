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

type PollyAPI interface {
	SynthesizeSpeech(ctx context.Context, params *polly.SynthesizeSpeechInput, optFns ...func(*polly.Options)) (*polly.SynthesizeSpeechOutput, error)
}

type AWSTTS struct {
	client PollyAPI
	voice  types.VoiceId
}

type TTSOption func(*AWSTTS)

func WithPollyClient(client PollyAPI) TTSOption {
	return func(t *AWSTTS) {
		t.client = client
	}
}

func NewAWSTTS(ctx context.Context, region string, voice string, opts ...TTSOption) (*AWSTTS, error) {
	if voice == "" {
		voice = "Matthew"
	}

	t := &AWSTTS{
		voice: types.VoiceId(voice),
	}

	for _, opt := range opts {
		opt(t)
	}

	if t.client == nil {
		cfgOpts := []func(*config.LoadOptions) error{}
		if region != "" {
			cfgOpts = append(cfgOpts, config.WithRegion(region))
		}

		cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
		if err != nil {
			return nil, err
		}
		t.client = polly.NewFromConfig(cfg)
	}

	return t, nil
}

func (t *AWSTTS) Label() string { return "aws.TTS" }
func (t *AWSTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *AWSTTS) SampleRate() int { return 16000 }
func (t *AWSTTS) NumChannels() int { return 1 }

func (t *AWSTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	out, err := t.client.SynthesizeSpeech(ctx, &polly.SynthesizeSpeechInput{
		OutputFormat: types.OutputFormatPcm,
		Text:         aws.String(text),
		VoiceId:      t.voice,
		SampleRate:   aws.String("16000"),
		Engine:       types.EngineNeural,
	})
	if err != nil {
		return nil, err
	}

	return &awsTTSChunkedStream{
		stream: out.AudioStream,
	}, nil
}

func (t *AWSTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming input for polly tts is not supported natively via rest")
}

type awsTTSChunkedStream struct {
	stream io.ReadCloser
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
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *awsTTSChunkedStream) Close() error {
	return s.stream.Close()
}

