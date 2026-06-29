package groq

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultGroqLLMBaseURL = "https://api.groq.com/openai/v1"
	defaultGroqLLMModel   = "llama-3.3-70b-versatile"
)

type GroqLLM struct {
	inner           *openai.OpenAILLM
	apiKey          string
	baseURL         string
	reasoningEffort string
	llmOptions      []openai.OpenAILLMOption
	mu              sync.Mutex
	closed          bool
	streams         map[*groqLLMStream]struct{}
	httpClient      interface {
		Do(*http.Request) (*http.Response, error)
	}
}

type GroqLLMOption func(*GroqLLM)

func WithGroqLLMBaseURL(baseURL string) GroqLLMOption {
	return func(l *GroqLLM) {
		if baseURL != "" {
			l.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithGroqLLMReasoningEffort(reasoningEffort string) GroqLLMOption {
	return func(l *GroqLLM) {
		l.reasoningEffort = reasoningEffort
	}
}

func WithGroqLLMTimeout(timeout time.Duration) GroqLLMOption {
	return func(l *GroqLLM) {
		if timeout > 0 {
			l.llmOptions = append(l.llmOptions, openai.WithOpenAILLMTimeout(timeout))
		}
	}
}

func WithGroqLLMMaxRetries(maxRetries int) GroqLLMOption {
	return func(l *GroqLLM) {
		if maxRetries >= 0 {
			l.llmOptions = append(l.llmOptions, openai.WithOpenAILLMMaxRetries(maxRetries))
		}
	}
}

func WithGroqLLMOptions(opts ...openai.OpenAILLMOption) GroqLLMOption {
	return func(l *GroqLLM) {
		l.llmOptions = append(l.llmOptions, opts...)
	}
}

func withGroqLLMHTTPClient(client interface {
	Do(*http.Request) (*http.Response, error)
}) GroqLLMOption {
	return func(l *GroqLLM) {
		l.httpClient = client
	}
}

func NewGroqLLM(apiKey string, model string, opts ...GroqLLMOption) *GroqLLM {
	resolvedAPIKey := resolveGroqAPIKey(apiKey)
	if model == "" {
		model = defaultGroqLLMModel
	}
	provider := &GroqLLM{
		apiKey:  resolvedAPIKey,
		baseURL: defaultGroqLLMBaseURL,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.reasoningEffort == "" {
		provider.reasoningEffort = defaultGroqLLMReasoningEffort(model)
	}
	openAIOpts := []openai.OpenAILLMOption{openai.WithOpenAILLMMaxRetries(0)}
	if provider.reasoningEffort != "" {
		openAIOpts = append(openAIOpts, openai.WithOpenAILLMReasoningEffort(provider.reasoningEffort))
	}
	openAIOpts = append(openAIOpts, provider.llmOptions...)
	provider.inner = openai.NewOpenAILLMWithBaseURLAndHTTPClient(resolvedAPIKey, model, provider.baseURL, provider.httpClient, openAIOpts...)
	return provider
}

func resolveGroqAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("GROQ_API_KEY")
}

func (l *GroqLLM) Model() string { return l.inner.Model() }
func (l *GroqLLM) Provider() string {
	return groqProviderHost(l.baseURL, "groq")
}

func defaultGroqLLMReasoningEffort(model string) string {
	switch model {
	case "openai/gpt-oss-120b", "openai/gpt-oss-20b":
		return "low"
	case "qwen/qwen3-32b":
		return "none"
	default:
		return ""
	}
}

func (l *GroqLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.apiKey == "" {
		return nil, fmt.Errorf("groq API key is required, either as argument or set GROQ_API_KEY environmental variable")
	}
	if l.isClosed() {
		return nil, fmt.Errorf("groq llm is closed: %w", io.ErrClosedPipe)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &groqLLMStream{
		ctx:      streamCtx,
		cancel:   cancel,
		provider: l,
		chatCtx:  chatCtx,
		opts:     append([]llm.ChatOption(nil), opts...),
	}
	if !l.registerStream(stream) {
		_ = stream.Close()
		return nil, fmt.Errorf("groq llm is closed: %w", io.ErrClosedPipe)
	}
	return stream, nil
}

func (l *GroqLLM) Close() error {
	if l == nil || l.inner == nil {
		return nil
	}
	l.mu.Lock()
	l.closed = true
	streams := make([]*groqLLMStream, 0, len(l.streams))
	for stream := range l.streams {
		streams = append(streams, stream)
	}
	l.streams = make(map[*groqLLMStream]struct{})
	l.mu.Unlock()
	for _, stream := range streams {
		_ = stream.Close()
	}
	return l.inner.Close()
}

func (l *GroqLLM) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

func (l *GroqLLM) registerStream(stream *groqLLMStream) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	if l.streams == nil {
		l.streams = make(map[*groqLLMStream]struct{})
	}
	l.streams[stream] = struct{}{}
	return true
}

func (l *GroqLLM) unregisterStream(stream *groqLLMStream) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.streams, stream)
}

type groqLLMStream struct {
	ctx      context.Context
	cancel   context.CancelFunc
	provider *GroqLLM
	chatCtx  *llm.ChatContext
	opts     []llm.ChatOption

	startOnce sync.Once
	startErr  error
	stream    llm.LLMStream
	mu        sync.Mutex
	closed    bool
}

func (s *groqLLMStream) Next() (*llm.ChatChunk, error) {
	if s.isClosed() {
		return nil, io.EOF
	}
	if err := s.ensureStarted(); err != nil {
		return nil, err
	}
	if s.stream == nil {
		return nil, io.EOF
	}
	chunk, err := s.stream.Next()
	if s.isClosed() && err != nil {
		return nil, io.EOF
	}
	return chunk, err
}

func (s *groqLLMStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel := s.cancel
	s.cancel = nil
	stream := s.stream
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stream != nil {
		err := stream.Close()
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return err
	}
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	return nil
}

func (s *groqLLMStream) ensureStarted() error {
	s.startOnce.Do(func() {
		if s.isClosed() {
			s.startErr = io.EOF
			return
		}
		stream, err := s.provider.inner.Chat(s.ctx, s.chatCtx, s.opts...)
		if err != nil {
			if s.isClosed() {
				s.startErr = io.EOF
				return
			}
			s.startErr = err
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = stream.Close()
			s.startErr = io.EOF
			return
		}
		s.stream = stream
		s.mu.Unlock()
	})
	return s.startErr
}

func (s *groqLLMStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
