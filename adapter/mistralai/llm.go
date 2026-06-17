package mistralai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
	goopenai "github.com/sashabaranov/go-openai"
)

type MistralLLM struct {
	inner       *openai.OpenAILLM
	apiKey      string
	model       string
	baseURL     string
	httpClient  goopenai.HTTPDoer
	extraParams map[string]any
	toolChoice  llm.ToolChoice
}

type MistralLLMOption func(*MistralLLM)

func WithMistralLLMModel(model string) MistralLLMOption {
	return func(l *MistralLLM) {
		if model != "" {
			l.model = model
		}
	}
}

func WithMistralLLMTemperature(temperature float64) MistralLLMOption {
	return func(l *MistralLLM) {
		l.setExtraParam("temperature", temperature)
	}
}

func WithMistralLLMTopP(topP float64) MistralLLMOption {
	return func(l *MistralLLM) {
		l.setExtraParam("top_p", topP)
	}
}

func WithMistralLLMMaxCompletionTokens(maxCompletionTokens int) MistralLLMOption {
	return func(l *MistralLLM) {
		l.setExtraParam("max_tokens", maxCompletionTokens)
	}
}

func WithMistralLLMPresencePenalty(presencePenalty float64) MistralLLMOption {
	return func(l *MistralLLM) {
		l.setExtraParam("presence_penalty", presencePenalty)
	}
}

func WithMistralLLMFrequencyPenalty(frequencyPenalty float64) MistralLLMOption {
	return func(l *MistralLLM) {
		l.setExtraParam("frequency_penalty", frequencyPenalty)
	}
}

func WithMistralLLMRandomSeed(randomSeed int) MistralLLMOption {
	return func(l *MistralLLM) {
		l.setExtraParam("seed", randomSeed)
	}
}

func WithMistralLLMToolChoice(toolChoice llm.ToolChoice) MistralLLMOption {
	return func(l *MistralLLM) {
		l.toolChoice = toolChoice
	}
}

func withMistralLLMHTTPClient(httpClient goopenai.HTTPDoer) MistralLLMOption {
	return func(l *MistralLLM) {
		l.httpClient = httpClient
	}
}

func NewMistralLLM(apiKey string, model string, opts ...MistralLLMOption) *MistralLLM {
	if model == "" {
		model = "ministral-8b-latest"
	}
	resolvedAPIKey := resolveMistralLLMAPIKey(apiKey)
	provider := &MistralLLM{
		apiKey:  resolvedAPIKey,
		model:   model,
		baseURL: "https://api.mistral.ai/v1",
	}
	for _, opt := range opts {
		opt(provider)
	}
	provider.rebuildInner()
	return provider
}

func resolveMistralLLMAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("MISTRAL_API_KEY")
}

func (l *MistralLLM) Model() string {
	return l.model
}

func (l *MistralLLM) Provider() string { return "MistralAI" }

func (l *MistralLLM) UpdateOptions(opts ...MistralLLMOption) {
	for _, opt := range opts {
		opt(l)
	}
	l.rebuildInner()
}

func (l *MistralLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.apiKey == "" {
		return nil, fmt.Errorf("mistral AI API key is required; set MISTRAL_API_KEY or pass api_key")
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}

func (l *MistralLLM) rebuildInner() {
	opts := []openai.OpenAILLMOption{}
	if len(l.extraParams) > 0 {
		opts = append(opts, openai.WithOpenAILLMExtraParams(cloneMistralLLMAnyMap(l.extraParams)))
	}
	if l.toolChoice != nil {
		opts = append(opts, openai.WithOpenAILLMToolChoice(l.toolChoice))
	}
	httpClient := l.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	l.inner = openai.NewOpenAILLMWithBaseURLAndHTTPClient(
		l.apiKey,
		l.model,
		l.baseURL,
		mistralLLMRequestRewriter{next: httpClient},
		opts...,
	)
}

func (l *MistralLLM) setExtraParam(key string, value any) {
	if l.extraParams == nil {
		l.extraParams = map[string]any{}
	}
	l.extraParams[key] = value
}

func cloneMistralLLMAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	clone := make(map[string]any, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

type mistralLLMRequestRewriter struct {
	next goopenai.HTTPDoer
}

func (r mistralLLMRequestRewriter) Do(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		return r.next.Do(req)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body.Close()
	body = rewriteMistralLLMRequestBody(body)
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return r.next.Do(req)
}

func rewriteMistralLLMRequestBody(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	changed := false
	if seed, ok := payload["seed"]; ok {
		if _, exists := payload["random_seed"]; !exists {
			payload["random_seed"] = seed
		}
		delete(payload, "seed")
		changed = true
	}
	if rewriteMistralLLMProviderTools(payload) {
		changed = true
	}
	if !changed {
		return body
	}
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

func rewriteMistralLLMProviderTools(payload map[string]any) bool {
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) == 0 {
		return false
	}
	changed := false
	rewritten := make([]any, 0, len(tools))
	for _, tool := range tools {
		mistralTool, ok := mistralLLMProviderToolPayload(tool)
		if ok {
			rewritten = append(rewritten, mistralTool)
			changed = true
			continue
		}
		rewritten = append(rewritten, tool)
	}
	if changed {
		payload["tools"] = rewritten
	}
	return changed
}

func mistralLLMProviderToolPayload(tool any) (map[string]any, bool) {
	toolMap, ok := tool.(map[string]any)
	if !ok || toolMap["type"] != "function" {
		return nil, false
	}
	function, ok := toolMap["function"].(map[string]any)
	if !ok {
		return nil, false
	}
	name, ok := function["name"].(string)
	if !ok {
		return nil, false
	}
	switch name {
	case "mistral_web_search":
		return map[string]any{"type": "web_search"}, true
	case "mistral_document_library":
		out := map[string]any{"type": "document_library"}
		if params, ok := function["parameters"].(map[string]any); ok {
			if libraryIDs, ok := params["library_ids"]; ok {
				out["library_ids"] = libraryIDs
			}
		}
		return out, true
	case "mistral_code_interpreter":
		return map[string]any{"type": "code_interpreter"}, true
	default:
		const prefix = "mistral_connector_"
		if connectorID := strings.TrimPrefix(name, prefix); connectorID != name && connectorID != "" {
			return map[string]any{"type": "connector", "connector_id": connectorID}, true
		}
		return nil, false
	}
}
