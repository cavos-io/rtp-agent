package aws

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/polly"
	"github.com/aws/aws-sdk-go-v2/service/polly/types"
	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
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
	closed       bool
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
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if t.client == nil {
		return nil, fmt.Errorf("aws polly client is not configured")
	}
	out, err := t.client.SynthesizeSpeech(ctx, buildAWSSynthesizeSpeechInput(t, text))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}

	stream := &awsTTSChunkedStream{
		stream:   out.AudioStream,
		provider: t,
	}
	if !t.registerStream(stream) {
		stream.Close()
		return nil, io.ErrClosedPipe
	}
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
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
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

func (t *AWSTTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *AWSTTS) registerStream(stream *awsTTSChunkedStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.streams[stream] = struct{}{}
	return true
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
	stream    io.ReadCloser
	decoder   codecs.AudioStreamDecoder
	started   bool
	hasAudio  bool
	finalSent bool
	provider  *AWSTTS
}

func (s *awsTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.stream == nil {
		return nil, io.EOF
	}
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		s.decoder = codecs.NewMP3AudioStreamDecoder()
		data, err := io.ReadAll(s.stream)
		if err != nil {
			_ = s.Close()
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("AWS Polly TTS response read failed: %v", err))
		}
		if len(data) == 0 {
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		s.hasAudio = true
		go func() {
			s.decoder.Push(data)
			s.decoder.EndInput()
		}()
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			if s.hasAudio && !s.finalSent {
				s.finalSent = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, io.EOF
		}
		return nil, err
	}
	frame, err = normalizeAWSTTSFrame(frame, s.provider)
	if err != nil {
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: frame,
	}, nil
}

func normalizeAWSTTSFrame(frame *model.AudioFrame, provider *AWSTTS) (*model.AudioFrame, error) {
	if frame == nil || provider == nil {
		return frame, nil
	}
	frame = downmixAWSTTSFrameToMono(frame)
	if provider.sampleRate <= 0 {
		return frame, nil
	}
	return coreaudio.ResampleAudioFrame(frame, uint32(provider.sampleRate))
}

func downmixAWSTTSFrameToMono(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil || frame.NumChannels <= 1 {
		return frame
	}
	channels := int(frame.NumChannels)
	samples := int(frame.SamplesPerChannel)
	if samples == 0 {
		samples = len(frame.Data) / (channels * 2)
	}
	out := make([]byte, samples*2)
	for sample := 0; sample < samples; sample++ {
		var sum int32
		for channel := 0; channel < channels; channel++ {
			offset := (sample*channels + channel) * 2
			if offset+2 > len(frame.Data) {
				break
			}
			sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2])))
		}
		binary.LittleEndian.PutUint16(out[sample*2:sample*2+2], uint16(int16(sum/int32(channels))))
	}
	mono := *frame
	mono.Data = out
	mono.NumChannels = 1
	mono.SamplesPerChannel = uint32(samples)
	return &mono
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
	_ = stream.Close()
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	return nil
}
