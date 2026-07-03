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
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
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
	stream := &awsTTSChunkedStream{
		ctx:      ctx,
		text:     text,
		options:  t.snapshotOptions(),
		lazy:     true,
		provider: t,
	}
	if !t.registerStream(stream) {
		stream.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

type awsTTSRequestOptions struct {
	voice        types.VoiceId
	engine       types.Engine
	outputFormat types.OutputFormat
	textType     types.TextType
	language     types.LanguageCode
	sampleRate   int
}

func (t *AWSTTS) snapshotOptions() awsTTSRequestOptions {
	if t == nil {
		return awsTTSRequestOptions{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return awsTTSRequestOptions{
		voice:        t.voice,
		engine:       t.engine,
		outputFormat: t.outputFormat,
		textType:     t.textType,
		language:     t.language,
		sampleRate:   t.sampleRate,
	}
}

func buildAWSSynthesizeSpeechInput(t *AWSTTS, text string) *polly.SynthesizeSpeechInput {
	return buildAWSSynthesizeSpeechInputFromOptions(t.snapshotOptions(), text)
}

func buildAWSSynthesizeSpeechInputFromOptions(opts awsTTSRequestOptions, text string) *polly.SynthesizeSpeechInput {
	input := &polly.SynthesizeSpeechInput{
		OutputFormat: opts.outputFormat,
		Text:         aws.String(text),
		VoiceId:      opts.voice,
		SampleRate:   aws.String(fmt.Sprintf("%d", opts.sampleRate)),
		Engine:       opts.engine,
		TextType:     opts.textType,
	}
	if opts.language != "" {
		input.LanguageCode = opts.language
	}
	return input
}

func (t *AWSTTS) UpdateOptions(opts ...AWSTTSOption) {
	t.mu.Lock()
	defer t.mu.Unlock()
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
	ctx          context.Context
	text         string
	options      awsTTSRequestOptions
	stream       io.ReadCloser
	decoder      codecs.AudioStreamDecoder
	readErr      chan error
	requestID    string
	lazy         bool
	started      bool
	closed       bool
	hasAudio     bool
	emittedAudio bool
	finalSent    bool
	provider     *AWSTTS
}

func (s *awsTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed {
		return nil, io.EOF
	}
	if s.lazy {
		if err := s.open(); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	if s.stream == nil {
		return nil, io.EOF
	}
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		s.decoder = codecs.NewMP3AudioStreamDecoder()
		s.readErr = make(chan error, 1)
		first, firstErr := readAWSTTSChunk(s.stream)
		if len(first) == 0 && firstErr == io.EOF {
			_ = s.decoder.Close()
			s.finalSent = true
			return &tts.SynthesizedAudio{RequestID: s.requestID, IsFinal: true}, nil
		}
		if len(first) == 0 && firstErr != nil {
			_ = s.Close()
			if errors.Is(firstErr, context.DeadlineExceeded) {
				return nil, llm.NewAPITimeoutError(firstErr.Error())
			}
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("AWS Polly TTS response read failed: %v", firstErr))
		}
		s.hasAudio = true
		go s.feedDecoder(s.stream, first, firstErr)
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			if readErr := s.popReadErr(); readErr != nil {
				return nil, readErr
			}
			if s.hasAudio && !s.finalSent {
				s.finalSent = true
				return &tts.SynthesizedAudio{RequestID: s.requestID, IsFinal: true}, nil
			}
			return nil, io.EOF
		}
		_ = s.Close()
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("AWS Polly TTS audio decode failed: %v", err))
	}
	frame, err = normalizeAWSTTSFrame(frame, s.provider)
	if err != nil {
		_ = s.Close()
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("AWS Polly TTS audio decode failed: %v", err))
	}

	s.emittedAudio = true
	return &tts.SynthesizedAudio{
		RequestID: s.requestID,
		Frame:     frame,
	}, nil
}

func (s *awsTTSChunkedStream) open() error {
	s.lazy = false
	if s.provider == nil || s.provider.client == nil {
		return fmt.Errorf("aws polly client is not configured")
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := s.provider.client.SynthesizeSpeech(ctx, buildAWSSynthesizeSpeechInputFromOptions(s.options, s.text))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(err.Error())
	}
	requestID, _ := awsmiddleware.GetRequestIDMetadata(out.ResultMetadata)
	s.stream = out.AudioStream
	s.requestID = requestID
	return nil
}

func readAWSTTSChunk(stream io.Reader) ([]byte, error) {
	buf := make([]byte, 32*1024)
	n, err := stream.Read(buf)
	if n == 0 {
		return nil, err
	}
	chunk := make([]byte, n)
	copy(chunk, buf[:n])
	return chunk, err
}

func (s *awsTTSChunkedStream) feedDecoder(stream io.Reader, first []byte, firstErr error) {
	defer s.decoder.EndInput()
	if len(first) > 0 {
		s.decoder.Push(first)
	}
	if firstErr != nil {
		s.recordReadErr(firstErr)
		return
	}
	for {
		chunk, err := readAWSTTSChunk(stream)
		if len(chunk) > 0 {
			s.hasAudio = true
			s.decoder.Push(chunk)
		}
		if err != nil {
			s.recordReadErr(err)
			return
		}
	}
}

func (s *awsTTSChunkedStream) recordReadErr(err error) {
	if err == nil || err == io.EOF {
		return
	}
	select {
	case s.readErr <- err:
	default:
	}
}

func (s *awsTTSChunkedStream) popReadErr() error {
	if s.readErr == nil {
		return nil
	}
	select {
	case err := <-s.readErr:
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionErrorWithRetryable(fmt.Sprintf("AWS Polly TTS response read failed: %v", err), !s.emittedAudio)
	default:
		return nil
	}
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
	s.closed = true
	s.lazy = false
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
