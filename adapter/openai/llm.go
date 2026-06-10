package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/sashabaranov/go-openai"
)

const defaultOpenAILLMModel = "gpt-4.1"
const defaultAzureOpenAILLMModel = "gpt-4o"
const defaultOVHCloudOpenAILLMModel = "gpt-oss-120b"
const defaultDeepSeekOpenAILLMModel = "deepseek-chat"
const defaultFireworksOpenAILLMModel = "accounts/fireworks/models/llama-v3p3-70b-instruct"
const defaultPerplexityOpenAILLMModel = "llama-3.1-sonar-small-128k-chat"
const defaultTogetherOpenAILLMModel = "meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo"
const defaultTelnyxOpenAILLMModel = "meta-llama/Meta-Llama-3.1-70B-Instruct"
const defaultNebiusOpenAILLMModel = "meta-llama/Meta-Llama-3.1-70B-Instruct"
const defaultOllamaOpenAILLMModel = "llama3.1"
const defaultCometAPIOpenAILLMModel = "gpt-5-chat-latest"
const defaultOctoAIOpenAILLMModel = "llama-2-13b-chat"
const defaultSambaNovaOpenAILLMModel = "DeepSeek-R1-0528"
const defaultCerebrasOpenAILLMModel = "llama-4-scout-17b-16e-instruct"
const defaultXAIOpenAILLMModel = "grok-3-fast"
const openAIAPIKeyRequiredMessage = "OpenAI API key is required, either as argument or set OPENAI_API_KEY environment variable"

const (
	azureOpenAIEndpointEnv = "AZURE_OPENAI_ENDPOINT"
	azureOpenAIAPIKeyEnv   = "AZURE_OPENAI_API_KEY"
	azureOpenAIADTokenEnv  = "AZURE_OPENAI_AD_TOKEN"
	openAIAPIVersionEnv    = "OPENAI_API_VERSION"
	openRouterAPIKeyEnv    = "OPENROUTER_API_KEY"
	deepSeekAPIKeyEnv      = "DEEPSEEK_API_KEY"
	fireworksAPIKeyEnv     = "FIREWORKS_API_KEY"
	perplexityAPIKeyEnv    = "PERPLEXITY_API_KEY"
	togetherAPIKeyEnv      = "TOGETHER_API_KEY"
	telnyxAPIKeyEnv        = "TELNYX_API_KEY"
	nebiusAPIKeyEnv        = "NEBIUS_API_KEY"
	lettaAPIKeyEnv         = "LETTA_API_KEY"
	cometAPIKeyEnv         = "COMETAPI_API_KEY"
	octoAIAPIKeyEnv        = "OCTOAI_TOKEN"
	sambaNovaAPIKeyEnv     = "SAMBANOVA_API_KEY"
	cerebrasAPIKeyEnv      = "CEREBRAS_API_KEY"
	xAIAPIKeyEnv           = "XAI_API_KEY"
)

const defaultOpenRouterLLMURL = "https://openrouter.ai/api/v1"
const defaultDeepSeekOpenAIBaseURL = "https://api.deepseek.com/v1"
const defaultFireworksOpenAIBaseURL = "https://api.fireworks.ai/inference/v1"
const defaultPerplexityOpenAIBaseURL = "https://api.perplexity.ai"
const defaultTogetherOpenAIBaseURL = "https://api.together.xyz/v1"
const defaultTelnyxOpenAIBaseURL = "https://api.telnyx.com/v2/ai"
const defaultNebiusOpenAIBaseURL = "https://api.studio.nebius.com/v1/"
const defaultLettaOpenAIBaseURL = "https://api.letta.com/v1/chat/completions"
const defaultOllamaOpenAIBaseURL = "http://localhost:11434/v1"
const defaultCometAPIOpenAIBaseURL = "https://api.cometapi.com/v1/"
const defaultOctoAIOpenAIBaseURL = "https://text.octoai.run/v1"
const defaultSambaNovaOpenAIBaseURL = "https://api.sambanova.ai/v1"
const defaultCerebrasOpenAIBaseURL = "https://api.cerebras.ai/v1"
const defaultXAIOpenAIBaseURL = "https://api.x.ai/v1"

type OpenAILLM struct {
	client               *openai.Client
	model                string
	baseURL              string
	httpClient           openai.HTTPDoer
	extraParams          map[string]any
	parallelToolCalls    bool
	parallelToolCallsSet bool
	toolChoice           llm.ToolChoice
	defaultReasoning     bool
	strictToolSchema     bool
	extraHeaders         map[string]string
	extraQuery           map[string]string
	extraBody            map[string]any
}

type OpenAILLMOption func(*OpenAILLM)

type openRouterLLMOptions struct {
	siteURL        string
	appName        string
	fallbackModels []string
	provider       map[string]any
	plugins        []map[string]any
	llmOptions     []OpenAILLMOption
}

type OpenRouterLLMOption func(*openRouterLLMOptions)

func WithOpenRouterSiteURL(siteURL string) OpenRouterLLMOption {
	return func(o *openRouterLLMOptions) {
		o.siteURL = siteURL
	}
}

func WithOpenRouterAppName(appName string) OpenRouterLLMOption {
	return func(o *openRouterLLMOptions) {
		o.appName = appName
	}
}

func WithOpenRouterFallbackModels(models []string) OpenRouterLLMOption {
	return func(o *openRouterLLMOptions) {
		o.fallbackModels = append([]string(nil), models...)
	}
}

func WithOpenRouterProvider(provider map[string]any) OpenRouterLLMOption {
	return func(o *openRouterLLMOptions) {
		o.provider = cloneOpenAIAnyMap(provider)
	}
}

func WithOpenRouterPlugins(plugins []map[string]any) OpenRouterLLMOption {
	return func(o *openRouterLLMOptions) {
		o.plugins = cloneOpenAIAnyMapSlice(plugins)
	}
}

func WithOpenRouterLLMOptions(opts ...OpenAILLMOption) OpenRouterLLMOption {
	return func(o *openRouterLLMOptions) {
		o.llmOptions = append(o.llmOptions, opts...)
	}
}

func WithOpenAILLMTemperature(temperature float64) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraParams == nil {
			l.extraParams = map[string]any{}
		}
		l.extraParams["temperature"] = temperature
	}
}

func WithOpenAILLMTopP(topP float64) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraParams == nil {
			l.extraParams = map[string]any{}
		}
		l.extraParams["top_p"] = topP
	}
}

func WithOpenAILLMMaxCompletionTokens(maxCompletionTokens int) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraParams == nil {
			l.extraParams = map[string]any{}
		}
		l.extraParams["max_completion_tokens"] = maxCompletionTokens
	}
}

func WithOpenAILLMStore(store bool) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraParams == nil {
			l.extraParams = map[string]any{}
		}
		l.extraParams["store"] = store
	}
}

func WithOpenAILLMServiceTier(serviceTier string) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraParams == nil {
			l.extraParams = map[string]any{}
		}
		l.extraParams["service_tier"] = serviceTier
	}
}

func WithOpenAILLMSafetyIdentifier(safetyIdentifier string) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraParams == nil {
			l.extraParams = map[string]any{}
		}
		l.extraParams["safety_identifier"] = safetyIdentifier
	}
}

func WithOpenAILLMPromptCacheKey(promptCacheKey string) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraBody == nil {
			l.extraBody = map[string]any{}
		}
		l.extraBody["prompt_cache_key"] = promptCacheKey
	}
}

func WithOpenAILLMPromptCacheRetention(promptCacheRetention string) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraBody == nil {
			l.extraBody = map[string]any{}
		}
		l.extraBody["prompt_cache_retention"] = promptCacheRetention
	}
}

func WithOpenAILLMUser(user string) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraParams == nil {
			l.extraParams = map[string]any{}
		}
		l.extraParams["user"] = user
	}
}

func WithOpenAILLMMetadata(metadata map[string]string) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraParams == nil {
			l.extraParams = map[string]any{}
		}
		l.extraParams["metadata"] = metadata
	}
}

func WithOpenAILLMVerbosity(verbosity string) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraParams == nil {
			l.extraParams = map[string]any{}
		}
		l.extraParams["verbosity"] = verbosity
	}
}

func WithOpenAILLMReasoningEffort(reasoningEffort string) OpenAILLMOption {
	return func(l *OpenAILLM) {
		if l.extraParams == nil {
			l.extraParams = map[string]any{}
		}
		l.extraParams["reasoning_effort"] = reasoningEffort
	}
}

func WithOpenAILLMParallelToolCalls(parallelToolCalls bool) OpenAILLMOption {
	return func(l *OpenAILLM) {
		l.parallelToolCalls = parallelToolCalls
		l.parallelToolCallsSet = true
	}
}

func WithOpenAILLMToolChoice(toolChoice llm.ToolChoice) OpenAILLMOption {
	return func(l *OpenAILLM) {
		l.toolChoice = toolChoice
	}
}

func WithOpenAILLMStrictToolSchema(strict bool) OpenAILLMOption {
	return func(l *OpenAILLM) {
		l.strictToolSchema = strict
	}
}

func WithOpenAILLMExtraHeaders(headers map[string]string) OpenAILLMOption {
	return func(l *OpenAILLM) {
		l.extraHeaders = cloneOpenAIStringMap(headers)
	}
}

func WithOpenAILLMExtraQuery(query map[string]string) OpenAILLMOption {
	return func(l *OpenAILLM) {
		l.extraQuery = cloneOpenAIStringMap(query)
	}
}

func WithOpenAILLMExtraBody(body map[string]any) OpenAILLMOption {
	return func(l *OpenAILLM) {
		l.extraBody = cloneOpenAIAnyMap(body)
	}
}

func withOpenAILLMHTTPClient(httpClient openai.HTTPDoer) OpenAILLMOption {
	return func(l *OpenAILLM) {
		l.httpClient = httpClient
	}
}

func NewOpenAILLM(apiKey string, model string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if apiKey == "" {
		apiKey = os.Getenv(openAIAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("%s", openAIAPIKeyRequiredMessage)
	}
	config := openai.DefaultConfig(apiKey)
	return newOpenAILLMWithConfigAndModel(config, model, opts...)
}

func newOpenAILLMWithConfigAndModel(config openai.ClientConfig, model string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultOpenAILLMModel
	}
	provider := &OpenAILLM{
		model:            model,
		baseURL:          config.BaseURL,
		defaultReasoning: true,
		strictToolSchema: true,
	}
	for _, opt := range opts {
		opt(provider)
	}
	applyOpenAIHTTPClient(provider, &config)
	wrapOpenAIExtraHeaders(provider, &config)
	wrapOpenAIExtraQuery(provider, &config)
	wrapOpenAIExtraBody(provider, &config)
	provider.client = openai.NewClientWithConfig(config)
	return provider, nil
}

func NewAzureOpenAILLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultAzureOpenAILLMModel
	}
	if azureEndpoint == "" {
		azureEndpoint = os.Getenv(azureOpenAIEndpointEnv)
	}
	if apiVersion == "" {
		apiVersion = os.Getenv(openAIAPIVersionEnv)
	}
	if apiKey == "" {
		apiKey = os.Getenv(azureOpenAIAPIKeyEnv)
	}
	if azureADToken == "" {
		azureADToken = os.Getenv(azureOpenAIADTokenEnv)
	}
	if azureEndpoint == "" {
		return nil, fmt.Errorf("%s is required for Azure OpenAI LLM", azureOpenAIEndpointEnv)
	}
	if apiKey == "" && azureADToken == "" {
		return nil, fmt.Errorf("%s or %s is required for Azure OpenAI LLM", azureOpenAIAPIKeyEnv, azureOpenAIADTokenEnv)
	}
	if azureDeployment == "" {
		azureDeployment = model
	}

	provider := &OpenAILLM{model: model, defaultReasoning: true, strictToolSchema: true}
	for _, opt := range opts {
		opt(provider)
	}

	config := openai.DefaultAzureConfig(apiKey, azureEndpoint)
	config.AzureModelMapperFunc = func(string) string {
		return azureDeployment
	}
	if apiVersion != "" {
		config.APIVersion = apiVersion
	}
	if provider.httpClient != nil {
		config.HTTPClient = provider.httpClient
	}
	if apiKey == "" && azureADToken != "" {
		config.HTTPClient = &azureADTokenHTTPClient{
			base:  config.HTTPClient,
			token: azureADToken,
		}
	}
	wrapOpenAIExtraHeaders(provider, &config)
	wrapOpenAIExtraQuery(provider, &config)
	wrapOpenAIExtraBody(provider, &config)
	provider.client = openai.NewClientWithConfig(config)
	provider.baseURL = config.BaseURL
	return provider, nil
}

func NewOVHCloudOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultOVHCloudOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(ovhcloudAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OVHcloud AI Endpoints API key is required, either as argument or set OVHCLOUD_API_KEY environmental variable")
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultOVHCloudOpenAIBaseURL, nil, opts...), nil
}

func NewDeepSeekOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultDeepSeekOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(deepSeekAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("DeepSeek API key is required, either as argument or set DEEPSEEK_API_KEY environmental variable")
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultDeepSeekOpenAIBaseURL, nil, opts...), nil
}

func NewFireworksOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultFireworksOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(fireworksAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("fireworks API key is required, either as argument or set FIREWORKS_API_KEY environmental variable")
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultFireworksOpenAIBaseURL, nil, opts...), nil
}

func NewPerplexityOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultPerplexityOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(perplexityAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("perplexity AI API key is required, either as argument or set PERPLEXITY_API_KEY environmental variable")
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultPerplexityOpenAIBaseURL, nil, opts...), nil
}

func NewTogetherOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultTogetherOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(togetherAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("together AI API key is required, either as argument or set TOGETHER_API_KEY environmental variable")
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultTogetherOpenAIBaseURL, nil, opts...), nil
}

func NewTelnyxOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultTelnyxOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(telnyxAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("telnyx AI API key is required, either as argument or set TELNYX_API_KEY environmental variable")
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultTelnyxOpenAIBaseURL, nil, opts...), nil
}

func NewNebiusOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultNebiusOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(nebiusAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("nebius API key is required, either as argument or set NEBIUS_API_KEY environmental variable")
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultNebiusOpenAIBaseURL, nil, opts...), nil
}

func NewLettaOpenAILLM(agentID, baseURL, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if baseURL == "" {
		baseURL = defaultLettaOpenAIBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid URL scheme: %q; must be %q or %q", parsed.Scheme, "http", "https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("URL %q is missing a network location (e.g., domain name)", baseURL)
	}
	if apiKey == "" {
		apiKey = os.Getenv(lettaAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("letta API key is required, either as argument or set LETTA_API_KEY environmental variable")
	}
	sdkBaseURL := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/chat/completions")
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, agentID, sdkBaseURL, nil, opts...), nil
}

func NewOllamaOpenAILLM(model string, opts ...OpenAILLMOption) *OpenAILLM {
	if model == "" {
		model = defaultOllamaOpenAILLMModel
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient("ollama", model, defaultOllamaOpenAIBaseURL, nil, opts...)
}

func NewCometAPIOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultCometAPIOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(cometAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("CometAPI API key is required, either as argument or set COMETAPI_API_KEY environmental variable")
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultCometAPIOpenAIBaseURL, nil, opts...), nil
}

func NewOctoAIOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultOctoAIOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(octoAIAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OctoAI API key is required, either as argument or set OCTOAI_TOKEN environmental variable")
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultOctoAIOpenAIBaseURL, nil, opts...), nil
}

func NewSambaNovaOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultSambaNovaOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(sambaNovaAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("SambaNova API key is required, either as argument or set SAMBANOVA_API_KEY environment variable")
	}
	options := []OpenAILLMOption{WithOpenAILLMStrictToolSchema(false)}
	options = append(options, opts...)
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultSambaNovaOpenAIBaseURL, nil, options...), nil
}

func NewCerebrasOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultCerebrasOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(cerebrasAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("cerebras API key is required, either as argument or set CEREBRAS_API_KEY environment variable")
	}
	options := []OpenAILLMOption{WithOpenAILLMStrictToolSchema(false)}
	options = append(options, opts...)
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultCerebrasOpenAIBaseURL, nil, options...), nil
}

func NewXAIOpenAILLM(model, apiKey string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultXAIOpenAILLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(xAIAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("XAI API key is required, either as argument or set XAI_API_KEY environmental variable")
	}
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, defaultXAIOpenAIBaseURL, nil, opts...), nil
}

func NewOpenRouterLLM(apiKey, model string, opts ...OpenRouterLLMOption) (*OpenAILLM, error) {
	return newOpenRouterLLM(apiKey, model, defaultOpenRouterLLMURL, nil, opts...)
}

func NewOpenRouterLLMWithHTTPClient(apiKey, model string, httpClient openai.HTTPDoer, opts ...OpenRouterLLMOption) (*OpenAILLM, error) {
	return newOpenRouterLLM(apiKey, model, defaultOpenRouterLLMURL, httpClient, opts...)
}

func newOpenRouterLLM(apiKey, model, baseURL string, httpClient openai.HTTPDoer, opts ...OpenRouterLLMOption) (*OpenAILLM, error) {
	if apiKey == "" {
		apiKey = os.Getenv(openRouterAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("%s is required, either as argument or set %s environment variable", openRouterAPIKeyEnv, openRouterAPIKeyEnv)
	}
	if model == "" {
		model = "auto"
	}

	options := &openRouterLLMOptions{}
	for _, opt := range opts {
		opt(options)
	}

	headers := map[string]string{}
	if options.siteURL != "" {
		headers["HTTP-Referer"] = options.siteURL
	}
	if options.appName != "" {
		headers["X-Title"] = options.appName
	}

	body := map[string]any{}
	if len(options.provider) > 0 {
		body["provider"] = cloneOpenAIAnyMap(options.provider)
	}
	if len(options.fallbackModels) > 0 {
		models := make([]string, 0, len(options.fallbackModels)+1)
		models = append(models, model)
		models = append(models, options.fallbackModels...)
		body["models"] = models
	}
	if len(options.plugins) > 0 {
		body["plugins"] = cloneOpenAIAnyMapSlice(options.plugins)
	}

	llmOptions := []OpenAILLMOption{
		WithOpenAILLMToolChoice("auto"),
		WithOpenAILLMExtraHeaders(headers),
		WithOpenAILLMExtraBody(body),
	}
	llmOptions = append(llmOptions, options.llmOptions...)
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, baseURL, httpClient, llmOptions...), nil
}

type azureADTokenHTTPClient struct {
	base  openai.HTTPDoer
	token string
}

func (c *azureADTokenHTTPClient) Do(req *http.Request) (*http.Response, error) {
	base := c.base
	if base == nil {
		base = http.DefaultClient
	}
	cloned := req.Clone(req.Context())
	cloned.Header.Del(openai.AzureAPIKeyHeader)
	cloned.Header.Set("Authorization", "Bearer "+c.token)
	return base.Do(cloned)
}

func NewOpenAILLMWithBaseURL(apiKey string, model string, baseURL string, opts ...OpenAILLMOption) *OpenAILLM {
	return NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, baseURL, nil, opts...)
}

func NewOpenAILLMWithBaseURLAndHTTPClient(apiKey string, model string, baseURL string, httpClient openai.HTTPDoer, opts ...OpenAILLMOption) *OpenAILLM {
	config := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		config.BaseURL = baseURL
	}
	if httpClient != nil {
		config.HTTPClient = httpClient
	}
	provider := &OpenAILLM{
		model:            model,
		baseURL:          config.BaseURL,
		defaultReasoning: true,
		strictToolSchema: true,
	}
	for _, opt := range opts {
		opt(provider)
	}
	applyOpenAIHTTPClient(provider, &config)
	wrapOpenAIExtraHeaders(provider, &config)
	wrapOpenAIExtraQuery(provider, &config)
	wrapOpenAIExtraBody(provider, &config)
	provider.client = openai.NewClientWithConfig(config)
	return provider
}

func applyOpenAIHTTPClient(provider *OpenAILLM, config *openai.ClientConfig) {
	if provider.httpClient != nil {
		config.HTTPClient = provider.httpClient
	}
}

func wrapOpenAIExtraHeaders(provider *OpenAILLM, config *openai.ClientConfig) {
	if len(provider.extraHeaders) > 0 {
		config.HTTPClient = &extraHeadersHTTPClient{
			base:    config.HTTPClient,
			headers: cloneOpenAIStringMap(provider.extraHeaders),
		}
	}
}

func wrapOpenAIExtraQuery(provider *OpenAILLM, config *openai.ClientConfig) {
	if len(provider.extraQuery) > 0 {
		config.HTTPClient = &extraQueryHTTPClient{
			base:  config.HTTPClient,
			query: cloneOpenAIStringMap(provider.extraQuery),
		}
	}
}

func wrapOpenAIExtraBody(provider *OpenAILLM, config *openai.ClientConfig) {
	if len(provider.extraBody) > 0 {
		config.HTTPClient = &extraBodyHTTPClient{
			base: config.HTTPClient,
			body: cloneOpenAIAnyMap(provider.extraBody),
		}
	}
}

type extraHeadersHTTPClient struct {
	base    openai.HTTPDoer
	headers map[string]string
}

func (c *extraHeadersHTTPClient) Do(req *http.Request) (*http.Response, error) {
	base := c.base
	if base == nil {
		base = http.DefaultClient
	}
	cloned := req.Clone(req.Context())
	for key, value := range c.headers {
		cloned.Header.Set(key, value)
	}
	return base.Do(cloned)
}

type extraQueryHTTPClient struct {
	base  openai.HTTPDoer
	query map[string]string
}

func (c *extraQueryHTTPClient) Do(req *http.Request) (*http.Response, error) {
	base := c.base
	if base == nil {
		base = http.DefaultClient
	}
	cloned := req.Clone(req.Context())
	query := cloned.URL.Query()
	for key, value := range c.query {
		query.Set(key, value)
	}
	cloned.URL.RawQuery = query.Encode()
	return base.Do(cloned)
}

type extraBodyHTTPClient struct {
	base openai.HTTPDoer
	body map[string]any
}

func (c *extraBodyHTTPClient) Do(req *http.Request) (*http.Response, error) {
	base := c.base
	if base == nil {
		base = http.DefaultClient
	}
	if req.Body == nil {
		return base.Do(req)
	}
	original, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()

	payload := map[string]any{}
	if len(original) > 0 {
		if err := json.Unmarshal(original, &payload); err != nil {
			cloned := req.Clone(req.Context())
			cloned.Body = io.NopCloser(bytes.NewReader(original))
			cloned.ContentLength = int64(len(original))
			return base.Do(cloned)
		}
	}
	for key, value := range c.body {
		payload[key] = value
	}
	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cloned := req.Clone(req.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(merged))
	cloned.ContentLength = int64(len(merged))
	cloned.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(merged)), nil
	}
	return base.Do(cloned)
}

func (l *OpenAILLM) Model() string {
	return l.model
}

func (l *OpenAILLM) Provider() string {
	u, err := url.Parse(l.baseURL)
	if err != nil || u.Host == "" {
		return "openai"
	}
	return u.Host
}

func (l *OpenAILLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{}
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

	effectiveOptions := options
	if len(l.extraParams) > 0 || (l.parallelToolCallsSet && !options.ParallelToolCallsSet) || (l.toolChoice != nil && options.ToolChoice == nil) {
		copied := *options
		if len(l.extraParams) > 0 {
			copied.ExtraParams = mergeOpenAIExtraParams(options.ExtraParams, l.extraParams)
		}
		if l.parallelToolCallsSet && !options.ParallelToolCallsSet {
			copied.ParallelToolCalls = l.parallelToolCalls
			copied.ParallelToolCallsSet = true
		}
		if l.toolChoice != nil && options.ToolChoice == nil {
			copied.ToolChoice = l.toolChoice
		}
		effectiveOptions = &copied
	}

	req := buildOpenAIChatCompletionRequestWithReasoningDefaultAndToolSchema(l.model, chatCtx, effectiveOptions, l.defaultReasoning, l.strictToolSchema)

	var lastErr error
	for attempt := 0; attempt <= connectOptions.MaxRetry; attempt++ {
		stream, err := l.client.CreateChatCompletionStream(ctx, req)
		if err == nil {
			return &openaiStream{
				stream: stream,
				cancel: cancel,
			}, nil
		}
		lastErr = mapOpenAIError(err)
		if attempt == connectOptions.MaxRetry || !openAIShouldRetryError(lastErr) {
			if cancel != nil {
				cancel()
			}
			return nil, lastErr
		}
		if err := waitOpenAIRetryInterval(ctx, connectOptions.IntervalForRetry(attempt)); err != nil {
			if cancel != nil {
				cancel()
			}
			return nil, err
		}
	}

	if cancel != nil {
		cancel()
	}
	return nil, lastErr
}

func mapOpenAIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError("")
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatusCode != 0 {
		return llm.CreateAPIErrorFromHTTP(apiErr.Message, apiErr.HTTPStatusCode, "", apiErr)
	}
	if errors.As(err, &apiErr) {
		return llm.NewAPIError(apiErr.Message, apiErr, true)
	}
	var requestErr *openai.RequestError
	if errors.As(err, &requestErr) && requestErr.HTTPStatusCode != 0 {
		message := strings.TrimSpace(string(requestErr.Body))
		return llm.CreateAPIErrorFromHTTP(message, requestErr.HTTPStatusCode, "", message)
	}
	return llm.NewAPIConnectionError(openAIConnectionErrorMessage(err))
}

func openAIConnectionErrorMessage(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err.Error()
	}
	return err.Error()
}

func openAIShouldRetryError(err error) bool {
	var apiErr *llm.APIError
	return errors.As(err, &apiErr) && apiErr.Retryable
}

func waitOpenAIRetryInterval(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func mergeOpenAIExtraParams(callParams, providerParams map[string]any) map[string]any {
	merged := make(map[string]any, len(callParams)+len(providerParams))
	for key, value := range callParams {
		merged[key] = value
	}
	for key, value := range providerParams {
		merged[key] = value
	}
	return merged
}

func cloneOpenAIStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneOpenAIAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneOpenAIAnyMapSlice(src []map[string]any) []map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make([]map[string]any, 0, len(src))
	for _, item := range src {
		dst = append(dst, cloneOpenAIAnyMap(item))
	}
	return dst
}

func buildOpenAIChatCompletionRequest(model string, chatCtx *llm.ChatContext, options *llm.ChatOptions) openai.ChatCompletionRequest {
	return buildOpenAIChatCompletionRequestWithReasoningDefault(model, chatCtx, options, true)
}

func buildOpenAIChatCompletionRequestWithReasoningDefault(model string, chatCtx *llm.ChatContext, options *llm.ChatOptions, defaultReasoning bool) openai.ChatCompletionRequest {
	return buildOpenAIChatCompletionRequestWithReasoningDefaultAndToolSchema(model, chatCtx, options, defaultReasoning, true)
}

func buildOpenAIChatCompletionRequestWithReasoningDefaultAndToolSchema(model string, chatCtx *llm.ChatContext, options *llm.ChatOptions, defaultReasoning bool, strictToolSchema bool) openai.ChatCompletionRequest {
	messages := buildOpenAIChatMessages(chatCtx)

	tools := make([]openai.Tool, 0, len(options.Tools))
	for _, tool := range options.Tools {
		params, _ := json.Marshal(tool.Parameters())
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Strict:      strictToolSchema,
				Parameters:  json.RawMessage(params),
			},
		})
	}

	req := openai.ChatCompletionRequest{
		Model:    model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
	}
	if options.ParallelToolCallsSet {
		req.ParallelToolCalls = &options.ParallelToolCalls
	}

	if options.ToolChoice != nil {
		if toolChoice := buildOpenAIToolChoice(options.ToolChoice); toolChoice != nil {
			req.ToolChoice = toolChoice
		}
	}
	if responseFormat := buildOpenAIResponseFormat(options.ResponseFormat); responseFormat != nil {
		req.ResponseFormat = responseFormat
	}

	applyOpenAIExtraParams(&req, dropUnsupportedOpenAIParams(model, options.ExtraParams, len(options.Tools) > 0))
	if defaultReasoning && req.ReasoningEffort == "" {
		req.ReasoningEffort = defaultOpenAIReasoningEffort(model, len(options.Tools) > 0)
	}
	return req
}

func buildOpenAIToolChoice(choice llm.ToolChoice) any {
	switch tc := choice.(type) {
	case string:
		return tc
	case openai.ToolChoice:
		return tc
	case map[string]any:
		if tc["type"] != "function" {
			return nil
		}
		function, ok := tc["function"].(map[string]any)
		if !ok {
			return nil
		}
		name, ok := function["name"].(string)
		if !ok || name == "" {
			return nil
		}
		return openai.ToolChoice{
			Type:     openai.ToolTypeFunction,
			Function: openai.ToolFunction{Name: name},
		}
	default:
		return nil
	}
}

func buildOpenAIResponseFormat(format map[string]any) *openai.ChatCompletionResponseFormat {
	if len(format) == 0 {
		return nil
	}
	formatType, ok := format["type"].(string)
	if !ok || formatType == "" {
		return nil
	}
	responseFormat := &openai.ChatCompletionResponseFormat{
		Type: openai.ChatCompletionResponseFormatType(formatType),
	}
	if formatType != string(openai.ChatCompletionResponseFormatTypeJSONSchema) {
		return responseFormat
	}
	jsonSchema, ok := format["json_schema"].(map[string]any)
	if !ok {
		return responseFormat
	}
	schema := &openai.ChatCompletionResponseFormatJSONSchema{}
	if name, ok := jsonSchema["name"].(string); ok {
		schema.Name = name
	}
	if description, ok := jsonSchema["description"].(string); ok {
		schema.Description = description
	}
	if strict, ok := jsonSchema["strict"].(bool); ok {
		schema.Strict = strict
	}
	if rawSchema, ok := jsonSchema["schema"]; ok {
		if data, err := json.Marshal(rawSchema); err == nil {
			schema.Schema = json.RawMessage(data)
		}
	}
	responseFormat.JSONSchema = schema
	return responseFormat
}

func applyOpenAIExtraParams(req *openai.ChatCompletionRequest, params map[string]any) {
	for key, value := range params {
		switch key {
		case "temperature":
			if v, ok := asFloat32(value); ok {
				req.Temperature = v
			}
		case "top_p":
			if v, ok := asFloat32(value); ok {
				req.TopP = v
			}
		case "presence_penalty":
			if v, ok := asFloat32(value); ok {
				req.PresencePenalty = v
			}
		case "frequency_penalty":
			if v, ok := asFloat32(value); ok {
				req.FrequencyPenalty = v
			}
		case "n":
			if v, ok := asInt(value); ok {
				req.N = v
			}
		case "max_tokens":
			if v, ok := asInt(value); ok {
				req.MaxTokens = v
			}
		case "max_completion_tokens":
			if v, ok := asInt(value); ok {
				req.MaxCompletionTokens = v
			}
		case "logit_bias":
			if v := asIntMap(value); v != nil {
				req.LogitBias = v
			}
		case "logprobs":
			if v, ok := value.(bool); ok {
				req.LogProbs = v
			}
		case "top_logprobs":
			if v, ok := asInt(value); ok {
				req.TopLogProbs = v
			}
		case "reasoning_effort":
			if v, ok := value.(string); ok {
				req.ReasoningEffort = v
			}
		case "metadata":
			if v := asStringMap(value); v != nil {
				req.Metadata = v
			}
		case "seed":
			if v, ok := asInt(value); ok {
				req.Seed = &v
			}
		case "stop":
			if v := asStringSlice(value); v != nil {
				req.Stop = v
			}
		case "user":
			if v, ok := value.(string); ok {
				req.User = v
			}
		case "store":
			if v, ok := value.(bool); ok {
				req.Store = v
			}
		case "stream_options":
			if v := asStreamOptions(value); v != nil {
				req.StreamOptions = v
			}
		case "parallel_tool_calls":
			if v, ok := value.(bool); ok {
				req.ParallelToolCalls = &v
			}
		case "tool_choice":
			if v := buildOpenAIToolChoice(value); v != nil {
				req.ToolChoice = v
			}
		case "response_format":
			if v := asAnyMap(value); v != nil {
				req.ResponseFormat = buildOpenAIResponseFormat(v)
			}
		case "service_tier":
			if v, ok := value.(string); ok {
				req.ServiceTier = openai.ServiceTier(v)
			}
		case "verbosity":
			if v, ok := value.(string); ok {
				req.Verbosity = v
			}
		case "safety_identifier":
			if v, ok := value.(string); ok {
				req.SafetyIdentifier = v
			}
		case "chat_template_kwargs":
			if v, ok := value.(map[string]any); ok {
				req.ChatTemplateKwargs = v
			}
		case "prediction":
			if v := asPrediction(value); v != nil {
				req.Prediction = v
			}
		}
	}
}

var openAIReasoningUnsupportedParams = map[string]struct{}{
	"temperature":       {},
	"top_p":             {},
	"presence_penalty":  {},
	"frequency_penalty": {},
	"logit_bias":        {},
	"logprobs":          {},
	"top_logprobs":      {},
	"n":                 {},
}

var xAIReasoningUnsupportedParams = map[string]struct{}{
	"presence_penalty":  {},
	"frequency_penalty": {},
	"stop":              {},
}

func dropUnsupportedOpenAIParams(model string, params map[string]any, hasTools bool) map[string]any {
	if len(params) == 0 {
		return params
	}
	modelName := model
	if slash := strings.LastIndex(modelName, "/"); slash >= 0 {
		modelName = modelName[slash+1:]
	}
	unsupported := unsupportedOpenAIParamsForModel(modelName)
	if len(unsupported) == 0 && !(hasTools && openAIReasoningEffortToolIncompatible(modelName)) {
		return params
	}
	filtered := make(map[string]any, len(params))
	for key, value := range params {
		if _, drop := unsupported[key]; drop {
			continue
		}
		if key == "reasoning_effort" && hasTools && openAIReasoningEffortToolIncompatible(modelName) {
			continue
		}
		filtered[key] = value
	}
	return filtered
}

func unsupportedOpenAIParamsForModel(modelName string) map[string]struct{} {
	for _, prefix := range []string{"o1", "o3", "o4", "gpt-5"} {
		if strings.HasPrefix(modelName, prefix) {
			return openAIReasoningUnsupportedParams
		}
	}
	for _, prefix := range []string{"grok-4-1-fast-reasoning", "grok-4.20-0309-reasoning", "grok-4.20-multi-agent"} {
		if strings.HasPrefix(modelName, prefix) {
			return xAIReasoningUnsupportedParams
		}
	}
	return nil
}

func openAIReasoningEffortToolIncompatible(modelName string) bool {
	return strings.HasPrefix(modelName, "gpt-5.2") || strings.HasPrefix(modelName, "gpt-5.4")
}

func defaultOpenAIReasoningEffort(model string, hasTools bool) string {
	modelName := model
	if slash := strings.LastIndex(modelName, "/"); slash >= 0 {
		modelName = modelName[slash+1:]
	}
	if hasTools && openAIReasoningEffortToolIncompatible(modelName) {
		return ""
	}
	switch modelName {
	case "gpt-5.1", "gpt-5.2", "gpt-5.4":
		return "none"
	case "gpt-5", "gpt-5-mini", "gpt-5-nano", "gpt-5.4-mini":
		return "minimal"
	default:
		return ""
	}
}

func asFloat32(value any) (float32, bool) {
	switch v := value.(type) {
	case float32:
		return v, true
	case float64:
		return float32(v), true
	case int:
		return float32(v), true
	default:
		return 0, false
	}
}

func asInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func asIntMap(value any) map[string]int {
	switch v := value.(type) {
	case map[string]int:
		return v
	case map[string]any:
		out := make(map[string]int, len(v))
		for key, val := range v {
			intVal, ok := asInt(val)
			if !ok {
				return nil
			}
			out[key] = intVal
		}
		return out
	default:
		return nil
	}
}

func asStringMap(value any) map[string]string {
	switch v := value.(type) {
	case map[string]string:
		return v
	case map[string]any:
		out := make(map[string]string, len(v))
		for key, val := range v {
			out[key] = fmt.Sprint(val)
		}
		return out
	default:
		return nil
	}
}

func asAnyMap(value any) map[string]any {
	switch v := value.(type) {
	case map[string]any:
		return v
	default:
		return nil
	}
}

func asStringSlice(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			str, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, str)
		}
		return out
	default:
		return nil
	}
}

func asStreamOptions(value any) *openai.StreamOptions {
	optionsMap, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	options := &openai.StreamOptions{}
	if includeUsage, ok := optionsMap["include_usage"].(bool); ok {
		options.IncludeUsage = includeUsage
	}
	return options
}

func asPrediction(value any) *openai.Prediction {
	predictionMap, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	prediction := &openai.Prediction{}
	if content, ok := predictionMap["content"].(string); ok {
		prediction.Content = content
	}
	if predictionType, ok := predictionMap["type"].(string); ok {
		prediction.Type = predictionType
	}
	if prediction.Content == "" && prediction.Type == "" {
		return nil
	}
	return prediction
}

func buildOpenAIChatMessages(chatCtx *llm.ChatContext) []openai.ChatCompletionMessage {
	messages := make([]openai.ChatCompletionMessage, 0, len(chatCtx.Items))
	for _, group := range groupOpenAIChatItems(chatCtx.Items) {
		if group.message == nil && len(group.toolCalls) == 0 && len(group.toolOutputs) == 0 {
			continue
		}

		var msg openai.ChatCompletionMessage
		if group.message != nil {
			msg = buildOpenAIChatMessage(group.message)
		} else {
			msg = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant}
		}
		for _, toolCall := range group.toolCalls {
			msg.ToolCalls = append(msg.ToolCalls, buildOpenAIToolCall(toolCall))
		}
		messages = append(messages, msg)

		for _, toolOutput := range group.toolOutputs {
			messages = append(messages, buildOpenAIToolOutput(toolOutput))
		}
	}
	return messages
}

func buildOpenAIChatMessage(msg *llm.ChatMessage) openai.ChatCompletionMessage {
	oaMsg := openai.ChatCompletionMessage{
		Role: string(msg.Role),
	}
	if len(msg.Content) == 1 && msg.Content[0].Text != "" {
		oaMsg.Content = msg.Content[0].Text
		return oaMsg
	}

	parts := make([]openai.ChatMessagePart, 0, len(msg.Content))
	for _, c := range msg.Content {
		if c.Text != "" {
			parts = append(parts, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeText,
				Text: c.Text,
			})
		} else if c.Image != nil {
			imageURL := ""
			if str, ok := c.Image.Image.(string); ok {
				imageURL = str
			}
			if imageURL != "" {
				parts = append(parts, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL:    imageURL,
						Detail: openai.ImageURLDetail(c.Image.InferenceDetail),
					},
				})
			}
		}
	}
	oaMsg.MultiContent = parts
	return oaMsg
}

type openAIChatItemGroup struct {
	message     *llm.ChatMessage
	toolCalls   []*llm.FunctionCall
	toolOutputs []*llm.FunctionCallOutput
}

func groupOpenAIChatItems(items []llm.ChatItem) []*openAIChatItemGroup {
	groups := make([]*openAIChatItemGroup, 0)
	groupsByID := make(map[string]*openAIChatItemGroup)
	toolOutputs := make([]*llm.FunctionCallOutput, 0)

	addToGroup := func(groupID string, item llm.ChatItem) {
		group := groupsByID[groupID]
		if group == nil {
			group = &openAIChatItemGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *llm.ChatMessage:
			if it.Role == llm.ChatRoleAssistant {
				addToGroup(openAIGroupID(it.ID, nil), it)
			} else {
				addToGroup(it.ID, it)
			}
		case *llm.FunctionCall:
			addToGroup(openAIGroupID(it.ID, it.GroupID), it)
		case *llm.FunctionCallOutput:
			toolOutputs = append(toolOutputs, it)
		}
	}

	groupsByCallID := make(map[string]*openAIChatItemGroup)
	for _, group := range groups {
		for _, toolCall := range group.toolCalls {
			groupsByCallID[toolCall.CallID] = group
		}
	}
	for _, toolOutput := range toolOutputs {
		if group := groupsByCallID[toolOutput.CallID]; group != nil {
			group.add(toolOutput)
		}
	}
	for _, group := range groups {
		group.removeInvalidToolItems()
	}
	return groups
}

func (g *openAIChatItemGroup) add(item llm.ChatItem) {
	switch it := item.(type) {
	case *llm.ChatMessage:
		g.message = it
	case *llm.FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *llm.FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
}

func (g *openAIChatItemGroup) removeInvalidToolItems() {
	if len(g.toolCalls) == len(g.toolOutputs) {
		return
	}

	outputsByCallID := make(map[string]*llm.FunctionCallOutput)
	for _, toolOutput := range g.toolOutputs {
		outputsByCallID[toolOutput.CallID] = toolOutput
	}

	validCalls := make([]*llm.FunctionCall, 0, len(g.toolCalls))
	validOutputs := make([]*llm.FunctionCallOutput, 0, len(g.toolOutputs))
	for _, toolCall := range g.toolCalls {
		if toolOutput := outputsByCallID[toolCall.CallID]; toolOutput != nil {
			validCalls = append(validCalls, toolCall)
			validOutputs = append(validOutputs, toolOutput)
		}
	}

	g.toolCalls = validCalls
	g.toolOutputs = validOutputs
}

func openAIGroupID(itemID string, groupID *string) string {
	if groupID != nil && *groupID != "" {
		return *groupID
	}
	for i, r := range itemID {
		if r == '/' {
			return itemID[:i]
		}
	}
	return itemID
}

func buildOpenAIToolCall(toolCall *llm.FunctionCall) openai.ToolCall {
	return openai.ToolCall{
		ID:   toolCall.CallID,
		Type: openai.ToolTypeFunction,
		Function: openai.FunctionCall{
			Name:      toolCall.Name,
			Arguments: toolCall.Arguments,
		},
	}
}

func buildOpenAIToolOutput(toolOutput *llm.FunctionCallOutput) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role:       openai.ChatMessageRoleTool,
		Content:    toolOutput.Output,
		ToolCallID: toolOutput.CallID,
	}
}

type openaiStream struct {
	stream *openai.ChatCompletionStream
	cancel context.CancelFunc
}

func (s *openaiStream) Next() (*llm.ChatChunk, error) {
	resp, err := s.stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, mapOpenAIError(err)
	}

	if len(resp.Choices) == 0 {
		return &llm.ChatChunk{ID: resp.ID}, nil
	}

	choice := resp.Choices[0]
	chunk := &llm.ChatChunk{
		ID: resp.ID,
		Delta: &llm.ChoiceDelta{
			Role:    llm.ChatRole(choice.Delta.Role),
			Content: choice.Delta.Content,
		},
	}

	if len(choice.Delta.ToolCalls) > 0 {
		chunk.Delta.ToolCalls = make([]llm.FunctionToolCall, 0, len(choice.Delta.ToolCalls))
		for _, tc := range choice.Delta.ToolCalls {
			chunk.Delta.ToolCalls = append(chunk.Delta.ToolCalls, llm.FunctionToolCall{
				Type:      string(tc.Type),
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
				CallID:    tc.ID,
			})
		}
	}

	if resp.Usage != nil {
		chunk.Usage = openAICompletionUsage(resp.Usage)
	}

	return chunk, nil
}

func openAICompletionUsage(usage *openai.Usage) *llm.CompletionUsage {
	if usage == nil {
		return nil
	}
	result := &llm.CompletionUsage{
		CompletionTokens: usage.CompletionTokens,
		PromptTokens:     usage.PromptTokens,
		TotalTokens:      usage.TotalTokens,
	}
	if usage.PromptTokensDetails != nil {
		result.PromptCachedTokens = usage.PromptTokensDetails.CachedTokens
	}
	return result
}

func (s *openaiStream) Close() error {
	s.stream.Close()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	return nil
}
