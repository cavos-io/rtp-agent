package nvidia

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

const (
	defaultNvidiaSTTServer     = "grpc.nvcf.nvidia.com:443"
	defaultNvidiaSTTModel      = "parakeet-1.1b-en-US-asr-streaming-silero-vad-sortformer"
	defaultNvidiaSTTFunctionID = "1598d209-5e27-4d3c-8079-4751568b1081"
	defaultNvidiaSTTLanguage   = "en-US"
	defaultNvidiaSTTSampleRate = 16000
)

type NvidiaSTT struct {
	apiKey          string
	model           string
	functionID      string
	server          string
	language        string
	sampleRate      int
	punctuate       bool
	useSSL          bool
	diarization     bool
	maxSpeakerCount int
}

type NvidiaSTTOption func(*NvidiaSTT)

func WithNvidiaSTTServer(server string) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		if server != "" {
			s.server = server
		}
	}
}

func WithNvidiaSTTFunctionID(functionID string) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		if functionID != "" {
			s.functionID = functionID
		}
	}
}

func WithNvidiaSTTLanguage(language string) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithNvidiaSTTSampleRate(sampleRate int) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithNvidiaSTTPunctuate(enabled bool) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.punctuate = enabled
	}
}

func WithNvidiaSTTUseSSL(useSSL bool) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.useSSL = useSSL
	}
}

func WithNvidiaSTTDiarization(enabled bool) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		s.diarization = enabled
	}
}

func WithNvidiaSTTMaxSpeakerCount(count int) NvidiaSTTOption {
	return func(s *NvidiaSTT) {
		if count >= 0 {
			s.maxSpeakerCount = count
		}
	}
}

func NewNvidiaSTT(apiKey string, model string, opts ...NvidiaSTTOption) (*NvidiaSTT, error) {
	if apiKey == "" {
		apiKey = os.Getenv(nvidiaAPIKeyEnv)
	}
	if model == "" {
		model = defaultNvidiaSTTModel
	}

	provider := &NvidiaSTT{
		apiKey:          apiKey,
		model:           model,
		functionID:      defaultNvidiaSTTFunctionID,
		server:          defaultNvidiaSTTServer,
		language:        defaultNvidiaSTTLanguage,
		sampleRate:      defaultNvidiaSTTSampleRate,
		punctuate:       true,
		useSSL:          true,
		diarization:     false,
		maxSpeakerCount: 0,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.useSSL && provider.apiKey == "" {
		return nil, fmt.Errorf("nvidia api key is required while using SSL")
	}
	return provider, nil
}

func (s *NvidiaSTT) Label() string { return "nvidia.STT" }
func (s *NvidiaSTT) Model() string { return s.model }
func (s *NvidiaSTT) Provider() string {
	return "nvidia"
}
func (s *NvidiaSTT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return defaultNvidiaSTTSampleRate
	}
	return uint32(s.sampleRate)
}
func (s *NvidiaSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    true,
		Diarization:       s.diarization,
		AlignedTranscript: "word",
		OfflineRecognize:  false,
	}
}

func (s *NvidiaSTT) Recognize(ctx context.Context, _ []*model.AudioFrame, _ string) (*stt.SpeechEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("nvidia riva stt recognition is not implemented")
}

func (s *NvidiaSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	streamLanguage := s.language
	if language != "" {
		streamLanguage = language
	}
	return &nvidiaSTTStream{
		ctx:      ctx,
		language: streamLanguage,
	}, nil
}

type nvidiaSTTStream struct {
	ctx             context.Context
	language        string
	closed          bool
	startTimeOffset float64
	startTime       float64
}

func (s *nvidiaSTTStream) PushFrame(frame *model.AudioFrame) error {
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return fmt.Errorf("nvidia riva stt streaming is not implemented")
}

func (s *nvidiaSTTStream) Flush() error {
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *nvidiaSTTStream) Close() error {
	s.closed = true
	return nil
}

func (s *nvidiaSTTStream) Next() (*stt.SpeechEvent, error) {
	if s.closed {
		return nil, io.EOF
	}
	if s.ctx != nil {
		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		default:
		}
	}
	return nil, fmt.Errorf("nvidia riva stt streaming is not implemented")
}

func (s *nvidiaSTTStream) StartTimeOffset() float64 {
	return s.startTimeOffset
}

func (s *nvidiaSTTStream) SetStartTimeOffset(offset float64) {
	if offset < 0 {
		panic("start_time_offset must be non-negative")
	}
	s.startTimeOffset = offset
}

func (s *nvidiaSTTStream) StartTime() float64 {
	return s.startTime
}

func (s *nvidiaSTTStream) SetStartTime(startTime float64) {
	if startTime < 0 {
		panic("start_time must be non-negative")
	}
	s.startTime = startTime
}
