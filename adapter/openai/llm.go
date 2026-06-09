package openai

import (
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

const (
	azureOpenAIEndpointEnv = "AZURE_OPENAI_ENDPOINT"
	azureOpenAIAPIKeyEnv   = "AZURE_OPENAI_API_KEY"
	azureOpenAIADTokenEnv  = "AZURE_OPENAI_AD_TOKEN"
	openAIAPIVersionEnv    = "OPENAI_API_VERSION"
)

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
}

type OpenAILLMOption func(*OpenAILLM)

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
		return nil, fmt.Errorf("OPENAI_API_KEY is required, either as argument or set OPENAI_API_KEY environment variable")
	}
	config := openai.DefaultConfig(apiKey)
	return newOpenAILLMWithConfigAndModel(config, model, opts...)
}

func newOpenAILLMWithConfigAndModel(config openai.ClientConfig, model string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultOpenAILLMModel
	}
	provider := &OpenAILLM{
		client:           openai.NewClientWithConfig(config),
		model:            model,
		baseURL:          config.BaseURL,
		defaultReasoning: true,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider, nil
}

func NewAzureOpenAILLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...OpenAILLMOption) (*OpenAILLM, error) {
	if model == "" {
		model = defaultOpenAILLMModel
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

	provider := &OpenAILLM{model: model, defaultReasoning: true}
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
	provider.client = openai.NewClientWithConfig(config)
	provider.baseURL = config.BaseURL
	return provider, nil
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
		client:           openai.NewClientWithConfig(config),
		model:            model,
		baseURL:          config.BaseURL,
		defaultReasoning: true,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
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

	req := buildOpenAIChatCompletionRequestWithReasoningDefault(l.model, chatCtx, effectiveOptions, l.defaultReasoning)

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

func buildOpenAIChatCompletionRequest(model string, chatCtx *llm.ChatContext, options *llm.ChatOptions) openai.ChatCompletionRequest {
	return buildOpenAIChatCompletionRequestWithReasoningDefault(model, chatCtx, options, true)
}

func buildOpenAIChatCompletionRequestWithReasoningDefault(model string, chatCtx *llm.ChatContext, options *llm.ChatOptions, defaultReasoning bool) openai.ChatCompletionRequest {
	messages := buildOpenAIChatMessages(chatCtx)

	tools := make([]openai.Tool, 0, len(options.Tools))
	for _, tool := range options.Tools {
		params, _ := json.Marshal(tool.Parameters())
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Strict:      true,
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
