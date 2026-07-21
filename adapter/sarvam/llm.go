package sarvam

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultSarvamLLMBaseURL = "https://api.sarvam.ai/v1"
	defaultSarvamLLMModel   = "sarvam-30b"
)

var (
	sarvamLLMSupportedModels = map[string]struct{}{
		"sarvam-m":        {},
		"sarvam-30b":      {},
		"sarvam-30b-16k":  {},
		"sarvam-105b":     {},
		"sarvam-105b-32k": {},
	}
	sarvamLLMAllowedExtraBodyParams = map[string]struct{}{
		"frequency_penalty": {},
		"max_tokens":        {},
		"n":                 {},
		"presence_penalty":  {},
		"seed":              {},
		"stop":              {},
		"wiki_grounding":    {},
	}
)

type LLM struct {
	apiKey       string
	model        string
	baseURL      string
	extraHeaders map[string]string
	extraBody    map[string]any
	httpClient   sarvamLLMHTTPDoer
}

type LLMOption func(*LLM)

type sarvamLLMHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func WithSarvamLLMBaseURL(baseURL string) LLMOption {
	return func(l *LLM) {
		if baseURL != "" {
			l.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSarvamLLMExtraHeaders(headers map[string]string) LLMOption {
	return func(l *LLM) {
		l.extraHeaders = cloneSarvamStringMap(headers)
	}
}

func WithSarvamLLMExtraBody(body map[string]any) LLMOption {
	return func(l *LLM) {
		l.extraBody = filterSarvamLLMExtraBody(body)
	}
}

func withSarvamLLMHTTPClient(client sarvamLLMHTTPDoer) LLMOption {
	return func(l *LLM) {
		if client != nil {
			l.httpClient = client
		}
	}
}

func NewLLM(apiKey string, model string, opts ...LLMOption) *LLM {
	provider, _ := NewSarvamLLMWithError(apiKey, model, opts...)
	return provider
}

func NewSarvamLLMWithError(apiKey string, model string, opts ...LLMOption) (*LLM, error) {
	if model == "" {
		model = defaultSarvamLLMModel
	}
	if err := validateSarvamLLMModel(model); err != nil {
		return nil, err
	}
	resolvedAPIKey := resolveSarvamAPIKey(apiKey)
	if resolvedAPIKey == "" {
		return nil, fmt.Errorf("sarvam API key is required, either as argument or set SARVAM_API_KEY environment variable")
	}
	provider := &LLM{
		apiKey:     resolvedAPIKey,
		model:      model,
		baseURL:    defaultSarvamLLMBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider, nil
}

func (l *LLM) Model() string {
	return l.model
}

func (l *LLM) Provider() string {
	return "Sarvam"
}

func (l *LLM) BaseURL() string {
	return l.baseURL
}

func (l *LLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{
		ParallelToolCalls: true,
	}
	for _, opt := range opts {
		opt(options)
	}
	connectOptions, err := options.EffectiveConnectOptions()
	if err != nil {
		return nil, err
	}
	var cancel context.CancelFunc
	if connectOptions.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, connectOptions.Timeout)
	}
	req, err := buildSarvamLLMChatRequest(ctx, l, chatCtx, options)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	resp, err := l.httpClient.Do(req)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		return nil, llm.CreateAPIErrorFromHTTP(strings.TrimSpace(string(respBody)), resp.StatusCode, resp.Header.Get("x-request-id"), string(respBody))
	}
	return &sarvamLLMStream{
		resp:    resp,
		scanner: bufio.NewScanner(resp.Body),
		cancel:  cancel,
	}, nil
}

func buildSarvamLLMChatRequest(ctx context.Context, l *LLM, chatCtx *llm.ChatContext, options *llm.ChatOptions) (*http.Request, error) {
	payload := map[string]any{
		"model":    l.model,
		"messages": buildSarvamLLMMessages(chatCtx),
		"stream":   true,
	}
	for key, value := range l.extraBody {
		payload[key] = value
	}
	for key, value := range filterSarvamLLMExtraBody(options.ExtraParams) {
		payload[key] = value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(l.baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range l.extraHeaders {
		req.Header.Set(key, value)
	}
	req.Header.Set("api-subscription-key", l.apiKey)
	req.Header.Set("User-Agent", sarvamUserAgent)
	return req, nil
}

func buildSarvamLLMMessages(chatCtx *llm.ChatContext) []map[string]any {
	if chatCtx == nil {
		return nil
	}
	messages := make([]map[string]any, 0, len(chatCtx.Items))
	for _, item := range chatCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok {
			continue
		}
		messages = append(messages, map[string]any{
			"role":    string(msg.Role),
			"content": msg.TextContent(),
		})
	}
	return messages
}

func validateSarvamLLMModel(model string) error {
	if _, ok := sarvamLLMSupportedModels[model]; ok {
		return nil
	}
	return fmt.Errorf("unsupported Sarvam model %q; supported models: sarvam-m, sarvam-30b, sarvam-30b-16k, sarvam-105b, sarvam-105b-32k", model)
}

func filterSarvamLLMExtraBody(body map[string]any) map[string]any {
	if len(body) == 0 {
		return nil
	}
	filtered := make(map[string]any, len(body))
	for key, value := range body {
		if _, ok := sarvamLLMAllowedExtraBodyParams[key]; ok {
			filtered[key] = value
		}
	}
	return filtered
}

func cloneSarvamStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

type sarvamLLMStream struct {
	resp    *http.Response
	scanner *bufio.Scanner
	cancel  context.CancelFunc
	closed  bool
}

func (s *sarvamLLMStream) Next() (*llm.ChatChunk, error) {
	if s.closed {
		return nil, io.EOF
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			return nil, io.EOF
		}
		chunk, err := sarvamLLMChunkFromSSEData([]byte(data))
		if err != nil {
			return nil, err
		}
		if chunk != nil {
			return chunk, nil
		}
	}
	if err := s.scanner.Err(); err != nil {
		if s.closed {
			return nil, io.EOF
		}
		return nil, err
	}
	return nil, io.EOF
}

func (s *sarvamLLMStream) Close() error {
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	return s.resp.Body.Close()
}

func sarvamLLMChunkFromSSEData(data []byte) (*llm.ChatChunk, error) {
	var payload struct {
		ID      string `json:"id"`
		Choices []struct {
			Delta struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *llm.CompletionUsage `json:"usage"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if len(payload.Choices) == 0 && payload.Usage == nil {
		return nil, nil
	}
	chunk := &llm.ChatChunk{
		ID:    payload.ID,
		Usage: payload.Usage,
	}
	if len(payload.Choices) > 0 {
		delta := payload.Choices[0].Delta
		chunk.Delta = &llm.ChoiceDelta{
			Role:    llm.ChatRole(delta.Role),
			Content: delta.Content,
		}
	}
	return chunk, nil
}

// Deprecated: use LLM.
type SarvamLLM = LLM

// Deprecated: use LLMOption.
type SarvamLLMOption = LLMOption

// Deprecated: use NewLLM.
func NewSarvamLLM(apiKey string, model string, opts ...LLMOption) *LLM {
	return NewLLM(apiKey, model, opts...)
}
