package nvidia

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

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
		t.languageCode = languageCode
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &nvidiaTTSChunkedStream{ctx: ctx, text: text}, nil
}

func (t *NvidiaTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &nvidiaTTSSynthesizeStream{ctx: ctx}, nil
}

type nvidiaTTSSynthesizeStream struct {
	ctx        context.Context
	done       bool
	closed     bool
	inputEnded bool
	hasText    bool
	flushed    bool
	text       string
	exception  error
}

type nvidiaTTSChunkedStream struct {
	ctx       context.Context
	text      string
	done      bool
	exception error
}

func (s *nvidiaTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.done {
		return nil, io.EOF
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			return nil, err
		}
	}
	if strings.TrimSpace(s.text) == "" {
		s.done = true
		return nil, io.EOF
	}
	err := fmt.Errorf("nvidia riva tts synthesis is not implemented")
	s.done = true
	s.exception = err
	return nil, err
}

func (s *nvidiaTTSChunkedStream) Close() error {
	s.done = true
	return nil
}

func (s *nvidiaTTSChunkedStream) Done() bool {
	return s.done
}

func (s *nvidiaTTSChunkedStream) Exception() error {
	return s.exception
}

func (s *nvidiaTTSSynthesizeStream) PushText(text string) error {
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return nil
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			return err
		}
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if s.flushed {
		return nil
	}
	s.hasText = true
	s.text += text
	return nil
}

func (s *nvidiaTTSSynthesizeStream) Flush() error {
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return nil
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			return err
		}
	}
	if s.hasText {
		s.flushed = true
	}
	return nil
}

func (s *nvidiaTTSSynthesizeStream) EndInput() error {
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return nil
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			return err
		}
	}
	if !s.flushed {
		s.flushed = s.hasText
	}
	s.inputEnded = true
	if !s.hasText {
		s.done = true
	}
	return nil
}

func (s *nvidiaTTSSynthesizeStream) Close() error {
	s.closed = true
	s.done = true
	return nil
}

func (s *nvidiaTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed {
		s.done = true
		return nil, io.EOF
	}
	if s.inputEnded && !s.hasText {
		s.done = true
		return nil, io.EOF
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			return nil, err
		}
	}
	err := fmt.Errorf("nvidia riva tts streaming is not implemented")
	s.done = true
	s.exception = err
	return nil, err
}

func (s *nvidiaTTSSynthesizeStream) Done() bool {
	return s.done
}

func (s *nvidiaTTSSynthesizeStream) Exception() error {
	return s.exception
}
