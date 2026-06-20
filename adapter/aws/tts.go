package aws

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/polly"
	"github.com/aws/aws-sdk-go-v2/service/polly/types"
	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/tts"
)

type AWSTTS struct {
	client       *polly.Client
	voice        types.VoiceId
	engine       types.Engine
	outputFormat types.OutputFormat
	textType     types.TextType
	language     types.LanguageCode
	sampleRate   int
	mu           sync.Mutex
	streams      map[*awsTTSChunkedStream]struct{}
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
		streams:      make(map[*awsTTSChunkedStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *AWSTTS) Label() string { return "aws.TTS" }
func (t *AWSTTS) Model() string { return string(t.engine) }
func (t *AWSTTS) Provider() string {
	return "Amazon Polly"
}
func (t *AWSTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *AWSTTS) SampleRate() int  { return t.sampleRate }
func (t *AWSTTS) NumChannels() int { return 1 }

func (t *AWSTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.client == nil {
		return nil, fmt.Errorf("aws polly client is not configured")
	}
	out, err := t.client.SynthesizeSpeech(ctx, buildAWSSynthesizeSpeechInput(t, text))
	if err != nil {
		return nil, err
	}

	stream := &awsTTSChunkedStream{
		stream:   out.AudioStream,
		provider: t,
	}
	t.registerStream(stream)
	return stream, nil
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

func (t *AWSTTS) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	streams := make([]*awsTTSChunkedStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*awsTTSChunkedStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *AWSTTS) registerStream(stream *awsTTSChunkedStream) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.streams[stream] = struct{}{}
}

func (t *AWSTTS) unregisterStream(stream *awsTTSChunkedStream) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func (t *AWSTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming input for polly tts is not supported natively via rest")
}

type awsTTSChunkedStream struct {
	stream   io.ReadCloser
	decoder  codecs.AudioStreamDecoder
	started  bool
	provider *AWSTTS
}

func (s *awsTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.stream == nil {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		s.decoder = codecs.NewMP3AudioStreamDecoder()
		data, err := io.ReadAll(s.stream)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, io.EOF
		}
		go func() {
			s.decoder.Push(data)
			s.decoder.EndInput()
		}()
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			return nil, io.EOF
		}
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: frame,
	}, nil
}

func (s *awsTTSChunkedStream) Close() error {
	if s.decoder != nil {
		_ = s.decoder.Close()
	}
	if s.stream == nil {
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return nil
	}
	stream := s.stream
	s.stream = nil
	err := stream.Close()
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	return err
}
