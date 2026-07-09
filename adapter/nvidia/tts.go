package nvidia

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultNvidiaTTSServer     = "grpc.nvcf.nvidia.com:443"
	defaultNvidiaTTSVoice      = "Magpie-Multilingual.EN-US.Leo"
	defaultNvidiaTTSFunctionID = "877104f7-e885-42b9-8de8-f6e4c6303969"
	defaultNvidiaTTSLanguage   = "en-US"
	defaultNvidiaTTSSampleRate = 16000
	nvidiaAPIKeyEnv            = "NVIDIA_API_KEY"
	nvidiaTTSMissingAPIKey     = "NVIDIA_API_KEY is not set while using SSL. Either pass api_key parameter, set NVIDIA_API_KEY environment variable or disable SSL and use a locally hosted Riva NIM service."
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
		t.server = server
	}
}

func WithNvidiaTTSFunctionID(functionID string) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		t.functionID = functionID
	}
}

func WithNvidiaTTSVoice(voice string) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		t.voice = voice
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
		return nil, fmt.Errorf("%s", nvidiaTTSMissingAPIKey)
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
	return &nvidiaTTSSynthesizeStream{
		ctx:          ctx,
		stateChanged: make(chan struct{}),
	}, nil
}

type nvidiaTTSSynthesizeStream struct {
	mu           sync.Mutex
	stateChanged chan struct{}
	ctx          context.Context
	done         bool
	closed       bool
	inputEnded   bool
	hasText      bool
	flushed      bool
	text         string
	pendingText  string
	exception    error
}

type nvidiaTTSChunkedStream struct {
	ctx       context.Context
	text      string
	done      bool
	exception error
}

func (s *nvidiaTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.done {
		if s.exception != nil {
			return nil, s.exception
		}
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

func (s *nvidiaTTSSynthesizeStream) notifyLocked() {
	close(s.stateChanged)
	s.stateChanged = make(chan struct{})
}

func (s *nvidiaTTSSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.inputEnded {
		return nil
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			s.notifyLocked()
			return err
		}
	}
	if text == "" {
		return nil
	}
	s.hasText = true
	s.text += text
	if prefix, tail, ok := nvidiaTTSCompletedSentencePrefix(s.text); ok {
		s.text = prefix
		s.pendingText = tail
		s.flushed = true
	}
	s.notifyLocked()
	return nil
}

func (s *nvidiaTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.inputEnded {
		return nil
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			s.notifyLocked()
			return err
		}
	}
	if s.hasText {
		s.flushed = true
		s.notifyLocked()
	}
	return nil
}

func (s *nvidiaTTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.inputEnded {
		return nil
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			s.notifyLocked()
			return err
		}
	}
	if s.hasText {
		s.flushed = true
	}
	s.inputEnded = true
	if !s.hasText {
		s.done = true
	}
	s.notifyLocked()
	return nil
}

func (s *nvidiaTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.done = true
	s.notifyLocked()
	return nil
}

func (s *nvidiaTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.done = true
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.inputEnded && !s.hasText {
			s.done = true
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.inputEnded && strings.TrimSpace(s.text) == "" {
			s.done = true
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.ctx != nil {
			if err := s.ctx.Err(); err != nil {
				s.done = true
				s.exception = err
				s.mu.Unlock()
				return nil, err
			}
		}
		if s.flushed && s.hasText && strings.TrimSpace(s.text) != "" {
			err := fmt.Errorf("nvidia riva tts streaming is not implemented")
			s.done = true
			s.exception = err
			s.mu.Unlock()
			return nil, err
		}
		changed := s.stateChanged
		ctx := s.ctx
		s.mu.Unlock()
		if ctx == nil {
			<-changed
			continue
		}
		select {
		case <-changed:
		case <-ctx.Done():
			s.mu.Lock()
			s.done = true
			s.exception = ctx.Err()
			s.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

func (s *nvidiaTTSSynthesizeStream) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *nvidiaTTSSynthesizeStream) Exception() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exception
}

func nvidiaTTSCompletedSentencePrefix(text string) (string, string, bool) {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 21 {
		return "", "", false
	}
	for i, r := range trimmed {
		next := i + len(string(r))
		if next >= len(trimmed) {
			break
		}
		switch r {
		case '.', '!', '?':
			if r == '.' && nvidiaTTSProtectedAbbreviation(trimmed[:i]) {
				continue
			}
			if r == '.' && nvidiaTTSProtectedInitial(trimmed[:i]) {
				continue
			}
			if r == '.' && nvidiaTTSProtectedSuffix(trimmed[:i], trimmed[next:]) {
				continue
			}
			if r == '.' && nvidiaTTSProtectedAcronym(trimmed, i) {
				continue
			}
			if r == '.' && nvidiaTTSProtectedPhD(trimmed, i) {
				continue
			}
			if trimmed[next] != ' ' && trimmed[next] != '\n' && trimmed[next] != '\t' {
				continue
			}
			if len(strings.TrimSpace(trimmed[:next])) >= 20 && strings.TrimSpace(trimmed[next:]) != "" {
				return strings.TrimSpace(trimmed[:next]), strings.TrimSpace(trimmed[next:]), true
			}
		case '。', '！', '？':
			if len(strings.TrimSpace(trimmed[:next])) >= 20 && strings.TrimSpace(trimmed[next:]) != "" {
				return strings.TrimSpace(trimmed[:next]), strings.TrimSpace(trimmed[next:]), true
			}
		}
	}
	return "", "", false
}

func nvidiaTTSProtectedAbbreviation(prefix string) bool {
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return false
	}
	switch fields[len(fields)-1] {
	case "Mr", "St", "Mrs", "Ms", "Dr":
		return true
	default:
		return false
	}
}

func nvidiaTTSProtectedAcronym(text string, dot int) bool {
	if dot < 1 || text[dot-1] < 'A' || text[dot-1] > 'Z' {
		return false
	}
	if dot >= 2 && text[dot-2] == '.' && dot >= 3 && text[dot-3] >= 'A' && text[dot-3] <= 'Z' {
		return true
	}
	next := dot + 1
	return next+1 < len(text) && text[next] >= 'A' && text[next] <= 'Z' && text[next+1] == '.'
}

func nvidiaTTSProtectedPhD(text string, dot int) bool {
	if dot >= 2 && text[dot-2:dot+1] == "Ph." {
		return true
	}
	return dot >= 4 && text[dot-4:dot+1] == "Ph.D."
}

func nvidiaTTSProtectedInitial(prefix string) bool {
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return false
	}
	token := fields[len(fields)-1]
	if len(token) != 1 {
		return false
	}
	return (token[0] >= 'A' && token[0] <= 'Z') || (token[0] >= 'a' && token[0] <= 'z')
}

func nvidiaTTSProtectedSuffix(prefix string, tail string) bool {
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return false
	}
	switch fields[len(fields)-1] {
	case "Inc", "Ltd", "Jr", "Sr", "Co":
	default:
		return false
	}
	tailFields := strings.Fields(tail)
	if len(tailFields) == 0 {
		return true
	}
	switch tailFields[0] {
	case "Mr", "Mrs", "Ms", "Dr", "Prof", "Capt", "Cpt", "Lt", "He", "She", "It", "They", "Their", "Our", "We", "But", "However", "That", "This", "Wherever":
		return false
	default:
		return true
	}
}
