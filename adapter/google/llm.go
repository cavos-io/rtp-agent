package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cavos-io/rtp-agent/core/llm"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"google.golang.org/genai"
)

type GoogleLLM struct {
	client            *genai.Client
	model             string
	thoughtMu         sync.RWMutex
	thoughtSignatures map[string][]byte
}

func NewGoogleLLM(apiKey string, model string) (*GoogleLLM, error) {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	resolvedAPIKey := resolveGoogleAPIKey(apiKey)
	if resolvedAPIKey == "" {
		return nil, errors.New("google API key is required either via api_key or GOOGLE_API_KEY environment variable")
	}
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  resolvedAPIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}
	return &GoogleLLM{
		client:            client,
		model:             model,
		thoughtSignatures: make(map[string][]byte),
	}, nil
}

func resolveGoogleAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("GOOGLE_API_KEY")
}

func googleModelRequiresThoughtSignatures(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "gemini-3") || strings.Contains(model, "gemini-2.5")
}

func (l *GoogleLLM) Model() string { return l.model }
func (l *GoogleLLM) Provider() string {
	return "Gemini"
}

func (l *GoogleLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}
	if err := validateGoogleChatExtraParams(options.ExtraParams); err != nil {
		return nil, err
	}
	connectOptions, err := options.EffectiveConnectOptions()
	if err != nil {
		return nil, err
	}

	contents, systemInstructions, err := buildGoogleContentsWithThoughtSignatures(chatCtx, l.snapshotThoughtSignatures())
	if err != nil {
		return nil, err
	}

	config := buildGoogleGenerateContentConfigForModelWithConnectOptions(l.model, options, systemInstructions, connectOptions)
	if err := validateGoogleThinkingConfigForModel(config, l.model); err != nil {
		return nil, err
	}

	stream := l.client.Models.GenerateContentStream(ctx, l.model, contents, config)

	next, stop := iter.Pull2(stream)

	return &googleLLMStream{
		next:              next,
		stop:              stop,
		thoughtMu:         &l.thoughtMu,
		thoughtSignatures: l.thoughtSignaturesForStream(),
	}, nil
}

func validateGoogleChatExtraParams(params map[string]any) error {
	thinkingConfig, ok := params["thinking_config"].(map[string]any)
	if !ok {
		return nil
	}
	budget, ok := thinkingConfig["thinking_budget"]
	if !ok || budget == nil {
		return nil
	}
	switch budget.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32:
		return nil
	default:
		return errors.New("thinking_budget inside thinking_config must be an integer")
	}
}

func (l *GoogleLLM) snapshotThoughtSignatures() map[string][]byte {
	if !googleModelRequiresThoughtSignatures(l.model) {
		return nil
	}
	l.thoughtMu.RLock()
	defer l.thoughtMu.RUnlock()
	if len(l.thoughtSignatures) == 0 {
		return nil
	}
	signatures := make(map[string][]byte, len(l.thoughtSignatures))
	for callID, signature := range l.thoughtSignatures {
		signatures[callID] = append([]byte(nil), signature...)
	}
	return signatures
}

func (l *GoogleLLM) thoughtSignaturesForStream() map[string][]byte {
	if !googleModelRequiresThoughtSignatures(l.model) {
		return nil
	}
	return l.thoughtSignatures
}

func buildGoogleGenerateContentConfig(options *llm.ChatOptions, systemInstructions string) *genai.GenerateContentConfig {
	return buildGoogleGenerateContentConfigForModel("", options, systemInstructions)
}

func buildGoogleGenerateContentConfigForModel(model string, options *llm.ChatOptions, systemInstructions string) *genai.GenerateContentConfig {
	connectOptions, _ := options.EffectiveConnectOptions()
	return buildGoogleGenerateContentConfigForModelWithConnectOptions(model, options, systemInstructions, connectOptions)
}

func buildGoogleGenerateContentConfigForModelWithConnectOptions(model string, options *llm.ChatOptions, systemInstructions string, connectOptions llm.APIConnectOptions) *genai.GenerateContentConfig {
	if options == nil {
		options = &llm.ChatOptions{}
	}
	config := &genai.GenerateContentConfig{}
	if systemInstructions != "" {
		config.SystemInstruction = genai.NewContentFromText(systemInstructions, genai.RoleUser)
	}

	if tools := googleToolsConfig(options.Tools, ""); len(tools) > 0 {
		config.Tools = tools
	}
	if toolConfig := buildGoogleToolConfig(options.Tools, options.ToolChoice); toolConfig != nil {
		config.ToolConfig = toolConfig
	}

	applyGoogleExtraParams(config, options.ExtraParams)
	applyGoogleConnectOptions(config, connectOptions)
	normalizeGoogleThinkingConfigForModel(config, model)
	applyGoogleResponseFormat(config, options.ResponseFormat)
	_, cachedContentSet := options.ExtraParams["cached_content"]
	if cachedContentSet || config.CachedContent != "" {
		config.SystemInstruction = nil
		config.Tools = nil
		config.ToolConfig = nil
	}

	return config
}

func applyGoogleConnectOptions(config *genai.GenerateContentConfig, options llm.APIConnectOptions) {
	if config == nil {
		return
	}
	if config.HTTPOptions == nil {
		config.HTTPOptions = &genai.HTTPOptions{}
	}
	if config.HTTPOptions.Timeout == nil {
		timeout := options.Timeout
		config.HTTPOptions.Timeout = &timeout
	}
	if config.HTTPOptions.Headers == nil {
		config.HTTPOptions.Headers = make(map[string][]string)
	}
	for key := range config.HTTPOptions.Headers {
		if strings.EqualFold(key, "x-goog-api-client") {
			delete(config.HTTPOptions.Headers, key)
		}
	}
	config.HTTPOptions.Headers["x-goog-api-client"] = []string{"livekit-agents/" + PluginVersion}
}

func normalizeGoogleThinkingConfigForModel(config *genai.GenerateContentConfig, model string) {
	if config == nil || config.ThinkingConfig == nil || !googleIsGemini3Model(model) {
		if config != nil && config.ThinkingConfig != nil && config.ThinkingConfig.ThinkingBudget != nil {
			config.ThinkingConfig.ThinkingLevel = ""
		}
		return
	}
	level := config.ThinkingConfig.ThinkingLevel
	if level == "" {
		if googleIsGemini3FlashModel(model) {
			level = genai.ThinkingLevel("MINIMAL")
		} else {
			level = genai.ThinkingLevel("LOW")
		}
	}
	config.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
}

func validateGoogleThinkingConfigForModel(config *genai.GenerateContentConfig, model string) error {
	if config == nil || config.ThinkingConfig == nil || googleIsGemini3Model(model) {
		return nil
	}
	if config.ThinkingConfig.ThinkingLevel != "" && config.ThinkingConfig.ThinkingBudget == nil {
		return fmt.Errorf("model %s does not support thinking_level; please use thinking_budget instead for Gemini 2.5 and earlier models", model)
	}
	return nil
}

func googleIsGemini3Model(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "gemini-3") || strings.HasPrefix(model, "gemini-3")
}

func googleIsGemini3FlashModel(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "gemini-3-flash") || strings.HasPrefix(model, "gemini-3-flash")
}

func buildGoogleFunctionDeclaration(t llm.Tool) *genai.FunctionDeclaration {
	schemaMap := llm.ToolParameters(t)
	parameters := googleSchemaFromMap(schemaMap)
	parameters.Type = genai.TypeObject

	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters:  parameters,
	}
}

func googleSchemaFromMap(schemaMap map[string]any) *genai.Schema {
	schema := &genai.Schema{
		Type:        googleSchemaType(schemaMap["type"]),
		Description: googleStringParam(schemaMap["description"]),
		Format:      googleStringParam(schemaMap["format"]),
		Enum:        googleStringList(schemaMap["enum"]),
		Required:    googleStringList(schemaMap["required"]),
	}
	if props, ok := schemaMap["properties"].(map[string]any); ok {
		schema.Properties = make(map[string]*genai.Schema, len(props))
		for name, value := range props {
			if propMap, ok := value.(map[string]any); ok {
				schema.Properties[name] = googleSchemaFromMap(propMap)
			}
		}
	}
	if itemMap, ok := schemaMap["items"].(map[string]any); ok {
		schema.Items = googleSchemaFromMap(itemMap)
	}
	return schema
}

func googleSchemaType(value any) genai.Type {
	typeStr, _ := value.(string)
	return genai.Type(strings.ToUpper(typeStr))
}

func googleStringParam(value any) string {
	str, _ := value.(string)
	return str
}

func googleStringList(value any) []string {
	switch items := value.(type) {
	case []string:
		return append([]string(nil), items...)
	case []genai.Modality:
		result := make([]string, 0, len(items))
		for _, item := range items {
			result = append(result, string(item))
		}
		return result
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			switch typed := item.(type) {
			case string:
				result = append(result, typed)
			case genai.Modality:
				result = append(result, string(typed))
			default:
				continue
			}
		}
		return result
	default:
		return nil
	}
}

func applyGoogleExtraParams(config *genai.GenerateContentConfig, params map[string]any) {
	if len(params) == 0 {
		return
	}
	if value, ok := params["cached_content"].(string); ok {
		config.CachedContent = value
	}
	if value, ok := googleHTTPOptionsParam(params["http_options"]); ok {
		config.HTTPOptions = value
	}
	if value, ok := googleFloat32Param(params["temperature"]); ok {
		config.Temperature = &value
	}
	if value, ok := googleFloat32Param(params["top_p"]); ok {
		config.TopP = &value
	}
	if value, ok := googleFloat32Param(params["top_k"]); ok {
		config.TopK = &value
	}
	if value, ok := params["stop_sequences"]; ok {
		config.StopSequences = googleStringList(value)
	}
	if value, ok := googleInt32Param(params["candidate_count"]); ok {
		config.CandidateCount = value
	}
	if value, ok := googleBoolParam(params["response_logprobs"]); ok {
		config.ResponseLogprobs = value
	}
	if value, ok := googleInt32Param(params["logprobs"]); ok {
		config.Logprobs = &value
	}
	if value, ok := googleFloat32Param(params["presence_penalty"]); ok {
		config.PresencePenalty = &value
	}
	if value, ok := googleFloat32Param(params["frequency_penalty"]); ok {
		config.FrequencyPenalty = &value
	}
	if value, ok := googleInt32Param(params["max_output_tokens"]); ok {
		config.MaxOutputTokens = value
	}
	if value, ok := googleInt32Param(params["seed"]); ok {
		config.Seed = &value
	}
	if value, ok := params["response_mime_type"].(string); ok {
		config.ResponseMIMEType = value
	}
	if value, ok := googleResponseSchemaParam(params["response_schema"]); ok {
		config.ResponseSchema = value
	}
	if value, ok := params["response_json_schema"]; ok {
		config.ResponseJsonSchema = value
	}
	if value, ok := googleRoutingConfigParam(params["routing_config"]); ok {
		config.RoutingConfig = value
	}
	if value, ok := googleModelSelectionConfigParam(params["model_selection_config"]); ok {
		config.ModelSelectionConfig = value
	}
	if value, ok := googleLabelsParam(params["labels"]); ok {
		config.Labels = value
	}
	if value, ok := googleModelArmorConfigParam(params["model_armor_config"]); ok {
		config.ModelArmorConfig = value
	}
	if value, ok := googleBoolParam(params["enable_enhanced_civic_answers"]); ok {
		config.EnableEnhancedCivicAnswers = &value
	}
	if value, ok := googleImageConfigParam(params["image_config"]); ok {
		config.ImageConfig = value
	}
	if value, ok := params["response_modalities"]; ok {
		config.ResponseModalities = googleStringList(value)
	}
	if value, ok := googleSpeechConfigParam(params["speech_config"]); ok {
		config.SpeechConfig = value
	}
	if value, ok := googleBoolParam(params["audio_timestamp"]); ok {
		config.AudioTimestamp = value
	}
	if value, ok := googleServiceTierParam(params["service_tier"]); ok {
		config.ServiceTier = value
	}
	if value, ok := googleThinkingConfigParam(params["thinking_config"]); ok {
		config.ThinkingConfig = value
	}
	if value, ok := googleSafetySettingsParam(params["safety_settings"]); ok {
		config.SafetySettings = value
	}
	if value, ok := googleMediaResolutionParam(params["media_resolution"]); ok {
		config.MediaResolution = value
	}
	if value, ok := googleToolConfigParam(params["tool_config"]); ok && config.ToolConfig == nil {
		config.ToolConfig = value
	}
	if value, ok := googleRetrievalConfigParam(params["retrieval_config"]); ok {
		if config.ToolConfig == nil {
			config.ToolConfig = &genai.ToolConfig{}
		}
		config.ToolConfig.RetrievalConfig = value
	}
}

func googleHTTPOptionsParam(value any) (*genai.HTTPOptions, bool) {
	switch typed := value.(type) {
	case *genai.HTTPOptions:
		return googleCloneHTTPOptions(typed), typed != nil
	case genai.HTTPOptions:
		return googleCloneHTTPOptions(&typed), true
	default:
		return nil, false
	}
}

func googleCloneHTTPOptions(options *genai.HTTPOptions) *genai.HTTPOptions {
	if options == nil {
		return nil
	}
	clone := *options
	if options.Headers != nil {
		clone.Headers = options.Headers.Clone()
	}
	if options.ExtraBody != nil {
		clone.ExtraBody = make(map[string]any, len(options.ExtraBody))
		for key, value := range options.ExtraBody {
			clone.ExtraBody[key] = value
		}
	}
	return &clone
}

func applyGoogleResponseFormat(config *genai.GenerateContentConfig, format map[string]any) {
	if len(format) == 0 {
		return
	}
	config.ResponseMIMEType = "application/json"
	if schema, ok := googleResponseFormatSchema(format); ok {
		config.ResponseSchema = schema
	}
}

func googleFloat32Param(value any) (float32, bool) {
	switch v := value.(type) {
	case float32:
		return v, true
	case float64:
		return float32(v), true
	case int:
		return float32(v), true
	case int32:
		return float32(v), true
	case int64:
		return float32(v), true
	default:
		return 0, false
	}
}

func googleFloat64Param(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func googleInt32Param(value any) (int32, bool) {
	switch v := value.(type) {
	case int:
		return int32(v), true
	case int32:
		return v, true
	case int64:
		return int32(v), true
	case float64:
		return int32(v), true
	case float32:
		return int32(v), true
	default:
		return 0, false
	}
}

func googleResponseSchemaParam(value any) (*genai.Schema, bool) {
	switch schema := value.(type) {
	case *genai.Schema:
		return schema, schema != nil
	case map[string]any:
		return googleSchemaFromMap(schema), true
	default:
		return nil, false
	}
}

func googleResponseFormatSchema(format map[string]any) (*genai.Schema, bool) {
	if format == nil {
		return nil, false
	}
	if format["type"] == "json_schema" {
		jsonSchema, ok := format["json_schema"].(map[string]any)
		if !ok {
			return nil, false
		}
		return googleResponseSchemaParam(jsonSchema["schema"])
	}
	if format["type"] == "json_object" || format["type"] == "text" {
		return nil, false
	}
	return googleResponseSchemaParam(format)
}

func googleSpeechConfigParam(value any) (*genai.SpeechConfig, bool) {
	switch config := value.(type) {
	case *genai.SpeechConfig:
		return config, config != nil
	case genai.SpeechConfig:
		return &config, true
	case map[string]any:
		result := &genai.SpeechConfig{}
		if languageCode := googleStringParam(config["language_code"]); languageCode != "" {
			result.LanguageCode = languageCode
		} else if languageCode := googleStringParam(config["languageCode"]); languageCode != "" {
			result.LanguageCode = languageCode
		}
		if voiceConfig, ok := googleVoiceConfigParam(config["voice_config"]); ok {
			result.VoiceConfig = voiceConfig
		} else if voiceConfig, ok := googleVoiceConfigParam(config["voiceConfig"]); ok {
			result.VoiceConfig = voiceConfig
		}
		return result, true
	default:
		return nil, false
	}
}

func googleVoiceConfigParam(value any) (*genai.VoiceConfig, bool) {
	switch config := value.(type) {
	case *genai.VoiceConfig:
		return config, config != nil
	case genai.VoiceConfig:
		return &config, true
	case map[string]any:
		result := &genai.VoiceConfig{}
		if prebuilt, ok := googlePrebuiltVoiceConfigParam(config["prebuilt_voice_config"]); ok {
			result.PrebuiltVoiceConfig = prebuilt
		} else if prebuilt, ok := googlePrebuiltVoiceConfigParam(config["prebuiltVoiceConfig"]); ok {
			result.PrebuiltVoiceConfig = prebuilt
		}
		return result, true
	default:
		return nil, false
	}
}

func googlePrebuiltVoiceConfigParam(value any) (*genai.PrebuiltVoiceConfig, bool) {
	switch config := value.(type) {
	case *genai.PrebuiltVoiceConfig:
		return config, config != nil
	case genai.PrebuiltVoiceConfig:
		return &config, true
	case map[string]any:
		result := &genai.PrebuiltVoiceConfig{}
		if voiceName := googleStringParam(config["voice_name"]); voiceName != "" {
			result.VoiceName = voiceName
		} else if voiceName := googleStringParam(config["voiceName"]); voiceName != "" {
			result.VoiceName = voiceName
		}
		return result, true
	default:
		return nil, false
	}
}

func googleRoutingConfigParam(value any) (*genai.GenerationConfigRoutingConfig, bool) {
	switch config := value.(type) {
	case *genai.GenerationConfigRoutingConfig:
		return config, config != nil
	case genai.GenerationConfigRoutingConfig:
		return &config, true
	case map[string]any:
		result := &genai.GenerationConfigRoutingConfig{}
		if manualMode, ok := googleRoutingManualModeParam(config["manual_mode"]); ok {
			result.ManualMode = manualMode
		} else if manualMode, ok := googleRoutingManualModeParam(config["manualMode"]); ok {
			result.ManualMode = manualMode
		}
		if autoMode, ok := googleRoutingAutoModeParam(config["auto_mode"]); ok {
			result.AutoMode = autoMode
		} else if autoMode, ok := googleRoutingAutoModeParam(config["autoMode"]); ok {
			result.AutoMode = autoMode
		}
		return result, true
	default:
		return nil, false
	}
}

func googleRoutingManualModeParam(value any) (*genai.GenerationConfigRoutingConfigManualRoutingMode, bool) {
	switch config := value.(type) {
	case *genai.GenerationConfigRoutingConfigManualRoutingMode:
		return config, config != nil
	case genai.GenerationConfigRoutingConfigManualRoutingMode:
		return &config, true
	case map[string]any:
		result := &genai.GenerationConfigRoutingConfigManualRoutingMode{}
		if modelName := googleStringParam(config["model_name"]); modelName != "" {
			result.ModelName = modelName
		} else if modelName := googleStringParam(config["modelName"]); modelName != "" {
			result.ModelName = modelName
		}
		return result, true
	default:
		return nil, false
	}
}

func googleRoutingAutoModeParam(value any) (*genai.GenerationConfigRoutingConfigAutoRoutingMode, bool) {
	switch config := value.(type) {
	case *genai.GenerationConfigRoutingConfigAutoRoutingMode:
		return config, config != nil
	case genai.GenerationConfigRoutingConfigAutoRoutingMode:
		return &config, true
	case map[string]any:
		result := &genai.GenerationConfigRoutingConfigAutoRoutingMode{}
		if preference := googleStringParam(config["model_routing_preference"]); preference != "" {
			result.ModelRoutingPreference = preference
		} else if preference := googleStringParam(config["modelRoutingPreference"]); preference != "" {
			result.ModelRoutingPreference = preference
		}
		return result, true
	default:
		return nil, false
	}
}

func googleModelSelectionConfigParam(value any) (*genai.ModelSelectionConfig, bool) {
	switch config := value.(type) {
	case *genai.ModelSelectionConfig:
		return config, config != nil
	case genai.ModelSelectionConfig:
		return &config, true
	case map[string]any:
		result := &genai.ModelSelectionConfig{}
		if preference := googleStringParam(config["feature_selection_preference"]); preference != "" {
			result.FeatureSelectionPreference = genai.FeatureSelectionPreference(preference)
		} else if preference := googleStringParam(config["featureSelectionPreference"]); preference != "" {
			result.FeatureSelectionPreference = genai.FeatureSelectionPreference(preference)
		}
		return result, true
	default:
		return nil, false
	}
}

func googleLabelsParam(value any) (map[string]string, bool) {
	switch labels := value.(type) {
	case map[string]string:
		result := make(map[string]string, len(labels))
		for key, value := range labels {
			result[key] = value
		}
		return result, true
	case map[string]any:
		result := make(map[string]string, len(labels))
		for key, value := range labels {
			label, ok := value.(string)
			if !ok {
				continue
			}
			result[key] = label
		}
		return result, true
	default:
		return nil, false
	}
}

func googleModelArmorConfigParam(value any) (*genai.ModelArmorConfig, bool) {
	switch config := value.(type) {
	case *genai.ModelArmorConfig:
		return config, config != nil
	case genai.ModelArmorConfig:
		return &config, true
	case map[string]any:
		result := &genai.ModelArmorConfig{}
		if name := googleStringParam(config["prompt_template_name"]); name != "" {
			result.PromptTemplateName = name
		} else if name := googleStringParam(config["promptTemplateName"]); name != "" {
			result.PromptTemplateName = name
		}
		if name := googleStringParam(config["response_template_name"]); name != "" {
			result.ResponseTemplateName = name
		} else if name := googleStringParam(config["responseTemplateName"]); name != "" {
			result.ResponseTemplateName = name
		}
		return result, true
	default:
		return nil, false
	}
}

func googleImageConfigParam(value any) (*genai.ImageConfig, bool) {
	switch config := value.(type) {
	case *genai.ImageConfig:
		return config, config != nil
	case genai.ImageConfig:
		return &config, true
	case map[string]any:
		result := &genai.ImageConfig{}
		if value := googleStringParam(config["aspect_ratio"]); value != "" {
			result.AspectRatio = value
		} else if value := googleStringParam(config["aspectRatio"]); value != "" {
			result.AspectRatio = value
		}
		if value := googleStringParam(config["image_size"]); value != "" {
			result.ImageSize = value
		} else if value := googleStringParam(config["imageSize"]); value != "" {
			result.ImageSize = value
		}
		if value := googleStringParam(config["person_generation"]); value != "" {
			result.PersonGeneration = value
		} else if value := googleStringParam(config["personGeneration"]); value != "" {
			result.PersonGeneration = value
		}
		if value := googleStringParam(config["output_mime_type"]); value != "" {
			result.OutputMIMEType = value
		} else if value := googleStringParam(config["outputMimeType"]); value != "" {
			result.OutputMIMEType = value
		}
		if value, ok := googleInt32Param(config["output_compression_quality"]); ok {
			result.OutputCompressionQuality = &value
		} else if value, ok := googleInt32Param(config["outputCompressionQuality"]); ok {
			result.OutputCompressionQuality = &value
		}
		return result, true
	default:
		return nil, false
	}
}

func googleServiceTierParam(value any) (genai.ServiceTier, bool) {
	switch tier := value.(type) {
	case genai.ServiceTier:
		return tier, tier != ""
	case string:
		if tier == "" {
			return "", false
		}
		return genai.ServiceTier(tier), true
	default:
		return "", false
	}
}

func googleThinkingConfigParam(value any) (*genai.ThinkingConfig, bool) {
	switch cfg := value.(type) {
	case *genai.ThinkingConfig:
		return cfg, cfg != nil
	case map[string]any:
		config := &genai.ThinkingConfig{}
		if value, ok := googleInt32Param(cfg["thinking_budget"]); ok {
			config.ThinkingBudget = &value
		}
		if value, ok := googleBoolParam(cfg["include_thoughts"]); ok {
			config.IncludeThoughts = value
		}
		if value, ok := googleThinkingLevelParam(cfg["thinking_level"]); ok {
			config.ThinkingLevel = value
		}
		return config, true
	default:
		return nil, false
	}
}

func googleBoolParam(value any) (bool, bool) {
	v, ok := value.(bool)
	return v, ok
}

func googleThinkingLevelParam(value any) (genai.ThinkingLevel, bool) {
	switch level := value.(type) {
	case genai.ThinkingLevel:
		return level, level != ""
	case string:
		if level == "" {
			return "", false
		}
		return genai.ThinkingLevel(strings.ToUpper(level)), true
	default:
		return "", false
	}
}

func googleSafetySettingsParam(value any) ([]*genai.SafetySetting, bool) {
	switch settings := value.(type) {
	case []*genai.SafetySetting:
		result := make([]*genai.SafetySetting, len(settings))
		copy(result, settings)
		return result, true
	case []genai.SafetySetting:
		result := make([]*genai.SafetySetting, 0, len(settings))
		for i := range settings {
			setting := settings[i]
			result = append(result, &setting)
		}
		return result, true
	case []map[string]any:
		result := make([]*genai.SafetySetting, 0, len(settings))
		for _, setting := range settings {
			if parsed, ok := googleSafetySettingParam(setting); ok {
				result = append(result, parsed)
			}
		}
		return result, true
	case []any:
		result := make([]*genai.SafetySetting, 0, len(settings))
		for _, setting := range settings {
			if parsed, ok := googleSafetySettingParam(setting); ok {
				result = append(result, parsed)
			}
		}
		return result, true
	default:
		return nil, false
	}
}

func googleSafetySettingParam(value any) (*genai.SafetySetting, bool) {
	switch setting := value.(type) {
	case *genai.SafetySetting:
		return setting, setting != nil
	case genai.SafetySetting:
		return &setting, true
	case map[string]any:
		return &genai.SafetySetting{
			Category:  genai.HarmCategory(googleStringParam(setting["category"])),
			Threshold: genai.HarmBlockThreshold(googleStringParam(setting["threshold"])),
		}, true
	default:
		return nil, false
	}
}

func googleMediaResolutionParam(value any) (genai.MediaResolution, bool) {
	switch resolution := value.(type) {
	case genai.MediaResolution:
		return resolution, resolution != ""
	case string:
		if resolution == "" {
			return "", false
		}
		return genai.MediaResolution(resolution), true
	default:
		return "", false
	}
}

func googleRetrievalConfigParam(value any) (*genai.RetrievalConfig, bool) {
	switch config := value.(type) {
	case *genai.RetrievalConfig:
		return config, config != nil
	case genai.RetrievalConfig:
		return &config, true
	case map[string]any:
		result := &genai.RetrievalConfig{}
		if languageCode := googleStringParam(config["language_code"]); languageCode != "" {
			result.LanguageCode = languageCode
		} else if languageCode := googleStringParam(config["languageCode"]); languageCode != "" {
			result.LanguageCode = languageCode
		}
		latLng, _ := config["lat_lng"].(map[string]any)
		if latLng == nil {
			latLng, _ = config["latLng"].(map[string]any)
		}
		if latLng != nil {
			result.LatLng = &genai.LatLng{}
			if latitude, ok := googleFloat64Param(latLng["latitude"]); ok {
				result.LatLng.Latitude = &latitude
			}
			if longitude, ok := googleFloat64Param(latLng["longitude"]); ok {
				result.LatLng.Longitude = &longitude
			}
		}
		return result, true
	default:
		return nil, false
	}
}

func googleToolConfigParam(value any) (*genai.ToolConfig, bool) {
	switch config := value.(type) {
	case *genai.ToolConfig:
		return config, config != nil
	case genai.ToolConfig:
		return &config, true
	case map[string]any:
		result := &genai.ToolConfig{}
		if retrieval, ok := googleRetrievalConfigParam(config["retrieval_config"]); ok {
			result.RetrievalConfig = retrieval
		} else if retrieval, ok := googleRetrievalConfigParam(config["retrievalConfig"]); ok {
			result.RetrievalConfig = retrieval
		}
		if include, ok := googleBoolParam(config["include_server_side_tool_invocations"]); ok {
			result.IncludeServerSideToolInvocations = &include
		} else if include, ok := googleBoolParam(config["includeServerSideToolInvocations"]); ok {
			result.IncludeServerSideToolInvocations = &include
		}
		if functionConfig, ok := googleFunctionCallingConfigParam(config["function_calling_config"]); ok {
			result.FunctionCallingConfig = functionConfig
		} else if functionConfig, ok := googleFunctionCallingConfigParam(config["functionCallingConfig"]); ok {
			result.FunctionCallingConfig = functionConfig
		}
		return result, true
	default:
		return nil, false
	}
}

func googleFunctionCallingConfigParam(value any) (*genai.FunctionCallingConfig, bool) {
	switch config := value.(type) {
	case *genai.FunctionCallingConfig:
		return config, config != nil
	case genai.FunctionCallingConfig:
		return &config, true
	case map[string]any:
		result := &genai.FunctionCallingConfig{}
		if mode := googleStringParam(config["mode"]); mode != "" {
			result.Mode = genai.FunctionCallingConfigMode(mode)
		}
		if names := googleStringList(config["allowed_function_names"]); len(names) > 0 {
			result.AllowedFunctionNames = names
		} else if names := googleStringList(config["allowedFunctionNames"]); len(names) > 0 {
			result.AllowedFunctionNames = names
		}
		if stream, ok := googleBoolParam(config["stream_function_call_arguments"]); ok {
			result.StreamFunctionCallArguments = &stream
		} else if stream, ok := googleBoolParam(config["streamFunctionCallArguments"]); ok {
			result.StreamFunctionCallArguments = &stream
		}
		return result, true
	default:
		return nil, false
	}
}

func buildGoogleToolConfig(tools []llm.Tool, choice llm.ToolChoice) *genai.ToolConfig {
	switch tc := choice.(type) {
	case string:
		switch tc {
		case "auto":
			return &genai.ToolConfig{
				FunctionCallingConfig: &genai.FunctionCallingConfig{
					Mode: genai.FunctionCallingConfigModeAuto,
				},
			}
		case "required":
			return &genai.ToolConfig{
				FunctionCallingConfig: &genai.FunctionCallingConfig{
					Mode:                 genai.FunctionCallingConfigModeAny,
					AllowedFunctionNames: googleToolNames(tools),
				},
			}
		case "none":
			return &genai.ToolConfig{
				FunctionCallingConfig: &genai.FunctionCallingConfig{
					Mode: genai.FunctionCallingConfigModeNone,
				},
			}
		}
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
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode:                 genai.FunctionCallingConfigModeAny,
				AllowedFunctionNames: []string{name},
			},
		}
	}
	return nil
}

func googleToolNames(tools []llm.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if _, ok := tool.(llm.ProviderTool); ok {
			continue
		}
		names = append(names, tool.Name())
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

type googleLLMStream struct {
	next              func() (*genai.GenerateContentResponse, error, bool)
	stop              func()
	closed            atomic.Bool
	closeOnce         sync.Once
	responseGenerated bool
	chunkEmitted      bool
	requestID         string
	thoughtMu         *sync.RWMutex
	thoughtSignatures map[string][]byte
	pending           []*llm.ChatChunk
	pendingErr        error
	finishReason      genai.FinishReason
}

func buildGoogleContents(chatCtx *llm.ChatContext) ([]*genai.Content, string, error) {
	return buildGoogleContentsWithThoughtSignatures(chatCtx, nil)
}

func buildGoogleContentsWithThoughtSignatures(chatCtx *llm.ChatContext, thoughtSignatures map[string][]byte) ([]*genai.Content, string, error) {
	contents := make([]*genai.Content, 0, len(chatCtx.Items))
	var systemInstructions string
	var currentRole genai.Role
	parts := make([]*genai.Part, 0)

	flush := func() {
		if currentRole == "" || len(parts) == 0 {
			return
		}
		contents = append(contents, genai.NewContentFromParts(parts, currentRole))
		parts = nil
	}

	appendParts := func(role genai.Role, newParts ...*genai.Part) {
		if currentRole == "" || currentRole != role {
			flush()
			currentRole = role
			parts = make([]*genai.Part, 0, len(newParts))
		}
		parts = append(parts, newParts...)
	}

	for _, group := range groupGoogleChatItems(chatCtx.Items) {
		for _, item := range group.flatten() {
			switch msg := item.(type) {
			case *llm.ChatMessage:
				if msg.Role == llm.ChatRoleSystem || msg.Role == llm.ChatRoleDeveloper {
					if text := msg.TextContent(); text != "" {
						systemInstructions += text + "\n"
					}
					continue
				}
				role := genai.Role(genai.RoleUser)
				if msg.Role == llm.ChatRoleAssistant {
					role = genai.Role(genai.RoleModel)
				}
				messageParts := googleMessageParts(msg)
				if len(messageParts) > 0 {
					appendParts(role, messageParts...)
				}
			case *llm.FunctionCall:
				part, err := googleFunctionCallPart(msg, thoughtSignatures)
				if err != nil {
					return nil, "", err
				}
				appendParts(genai.Role(genai.RoleModel), part)
			case *llm.FunctionCallOutput:
				appendParts(genai.Role(genai.RoleUser), googleFunctionResponsePart(msg))
			}
		}
	}
	flush()

	if currentRole != genai.Role(genai.RoleUser) {
		contents = append(contents, genai.NewContentFromParts([]*genai.Part{genai.NewPartFromText(".")}, genai.Role(genai.RoleUser)))
	}

	return contents, systemInstructions, nil
}

func googleMessageParts(msg *llm.ChatMessage) []*genai.Part {
	parts := make([]*genai.Part, 0, len(msg.Content))
	for _, content := range msg.Content {
		if content.Text != "" {
			parts = append(parts, genai.NewPartFromText(content.Text))
		}
		if content.Image != nil {
			if part := googleImagePart(content.Image); part != nil {
				parts = append(parts, part)
			}
		}
	}
	return parts
}

func googleImagePart(image *llm.ImageContent) *genai.Part {
	img, err := llm.SerializeImage(image)
	if err != nil {
		return nil
	}
	if img.ExternalURL == "" {
		return genai.NewPartFromBytes(img.DataBytes, img.MIMEType)
	}
	mimeType := img.MIMEType
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return genai.NewPartFromURI(img.ExternalURL, mimeType)
}

func googleFunctionCallPart(fc *llm.FunctionCall, thoughtSignatures map[string][]byte) (*genai.Part, error) {
	args := make(map[string]any)
	if err := json.Unmarshal([]byte(fc.Arguments), &args); err != nil {
		return nil, fmt.Errorf("google function call arguments: %w", err)
	}
	part := genai.NewPartFromFunctionCall(fc.Name, args)
	part.FunctionCall.ID = fc.CallID
	if thoughtSignatures != nil {
		if signature, ok := thoughtSignatures[fc.CallID]; ok {
			part.ThoughtSignature = append([]byte(nil), signature...)
		}
	}
	return part, nil
}

func googleFunctionResponsePart(fco *llm.FunctionCallOutput) *genai.Part {
	response := map[string]any{"output": fco.Output}
	if fco.IsError {
		response = map[string]any{"error": fco.Output}
	}
	part := genai.NewPartFromFunctionResponse(fco.Name, response)
	part.FunctionResponse.ID = fco.CallID
	return part
}

type googleChatItemGroup struct {
	message     *llm.ChatMessage
	toolCalls   []*llm.FunctionCall
	toolOutputs []*llm.FunctionCallOutput
}

func groupGoogleChatItems(items []llm.ChatItem) []*googleChatItemGroup {
	groups := make([]*googleChatItemGroup, 0)
	groupsByID := make(map[string]*googleChatItemGroup)
	toolOutputs := make([]*llm.FunctionCallOutput, 0)

	addToGroup := func(groupID string, item llm.ChatItem) {
		group := groupsByID[groupID]
		if group == nil {
			group = &googleChatItemGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *llm.ChatMessage:
			if it.Role == llm.ChatRoleAssistant {
				addToGroup(googleGroupID(it.ID, nil), it)
			} else {
				addToGroup(it.ID, it)
			}
		case *llm.FunctionCall:
			addToGroup(googleGroupID(it.ID, it.GroupID), it)
		case *llm.FunctionCallOutput:
			toolOutputs = append(toolOutputs, it)
		}
	}

	groupsByCallID := make(map[string]*googleChatItemGroup)
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

func (g *googleChatItemGroup) add(item llm.ChatItem) {
	switch it := item.(type) {
	case *llm.ChatMessage:
		g.message = it
	case *llm.FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *llm.FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
}

func (g *googleChatItemGroup) flatten() []llm.ChatItem {
	items := make([]llm.ChatItem, 0, 1+len(g.toolCalls)+len(g.toolOutputs))
	if g.message != nil {
		items = append(items, g.message)
	}
	for _, toolCall := range g.toolCalls {
		items = append(items, toolCall)
	}
	for _, toolOutput := range g.toolOutputs {
		items = append(items, toolOutput)
	}
	return items
}

func (g *googleChatItemGroup) removeInvalidToolItems() {
	if len(g.toolCalls) == len(g.toolOutputs) {
		return
	}

	callIDs := make(map[string]struct{}, len(g.toolCalls))
	outputIDs := make(map[string]struct{}, len(g.toolOutputs))
	for _, toolCall := range g.toolCalls {
		callIDs[toolCall.CallID] = struct{}{}
	}
	for _, toolOutput := range g.toolOutputs {
		outputIDs[toolOutput.CallID] = struct{}{}
	}

	validCallIDs := make(map[string]struct{})
	for callID := range callIDs {
		if _, ok := outputIDs[callID]; ok {
			validCallIDs[callID] = struct{}{}
		}
	}

	validCalls := make([]*llm.FunctionCall, 0, len(g.toolCalls))
	validOutputs := make([]*llm.FunctionCallOutput, 0, len(g.toolOutputs))
	for _, toolCall := range g.toolCalls {
		if _, ok := validCallIDs[toolCall.CallID]; ok {
			validCalls = append(validCalls, toolCall)
		}
	}
	for _, toolOutput := range g.toolOutputs {
		if _, ok := validCallIDs[toolOutput.CallID]; ok {
			validOutputs = append(validOutputs, toolOutput)
		}
	}

	g.toolCalls = validCalls
	g.toolOutputs = validOutputs
}

func googleGroupID(itemID string, groupID *string) string {
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

func (s *googleLLMStream) Next() (*llm.ChatChunk, error) {
	if s.closed.Load() {
		return nil, io.EOF
	}
	requestID := s.id()

	for {
		if len(s.pending) > 0 {
			chunk := s.pending[0]
			s.pending = s.pending[1:]
			return chunk, nil
		}
		if s.pendingErr != nil {
			err := s.pendingErr
			s.pendingErr = nil
			return nil, err
		}

		resp, err, ok := s.next()
		if s.closed.Load() {
			return nil, io.EOF
		}
		if !ok {
			if !s.responseGenerated {
				return nil, llm.NewAPIStatusError("no response generated", -1, requestID, googleLLMFinishReasonBody(s.finishReason))
			}
			return nil, io.EOF
		}
		if err != nil {
			if errors.Is(err, genai.ErrPageDone) || errors.Is(err, io.EOF) {
				if !s.responseGenerated {
					return nil, llm.NewAPIStatusError("no response generated", -1, requestID, googleLLMFinishReasonBody(s.finishReason))
				}
				return nil, io.EOF
			}
			return nil, googleLLMStreamError(err, !s.chunkEmitted, requestID)
		}

		if resp.PromptFeedback != nil {
			message, marshalErr := json.Marshal(resp.PromptFeedback)
			if marshalErr != nil {
				return nil, marshalErr
			}
			return nil, llm.NewAPIStatusErrorWithRetryable(string(message), -1, requestID, nil, false)
		}

		if resp.UsageMetadata != nil {
			s.pending = append(s.pending, &llm.ChatChunk{
				ID: requestID,
				Usage: &llm.CompletionUsage{
					PromptTokens:       int(resp.UsageMetadata.PromptTokenCount),
					PromptCachedTokens: int(resp.UsageMetadata.CachedContentTokenCount),
					CompletionTokens:   int(resp.UsageMetadata.CandidatesTokenCount),
					TotalTokens:        int(resp.UsageMetadata.TotalTokenCount),
				},
			})
		}

		if len(resp.Candidates) > 0 {
			cand := resp.Candidates[0]
			if cand.FinishReason != genai.FinishReasonUnspecified {
				s.finishReason = cand.FinishReason
			}
			if googleBlockedFinishReason(cand.FinishReason) {
				err := llm.NewAPIStatusErrorWithRetryable(fmt.Sprintf("generation blocked by gemini: %s", cand.FinishReason), -1, requestID, nil, false)
				if len(s.pending) > 0 {
					s.pendingErr = err
					chunk := s.pending[0]
					s.pending = s.pending[1:]
					return chunk, nil
				}
				return nil, err
			}
			if cand.Content != nil {
				for _, part := range cand.Content.Parts {
					s.responseGenerated = true
					chunk, parseErr := googleChatChunkFromPart(part)
					if parseErr != nil {
						err := googleLLMStreamError(parseErr, !s.chunkEmitted, requestID)
						if len(s.pending) > 0 {
							s.pendingErr = err
							chunk := s.pending[0]
							s.pending = s.pending[1:]
							return chunk, nil
						}
						return nil, err
					}
					if chunk != nil {
						chunk.ID = requestID
						s.storeThoughtSignature(part, chunk)
						s.chunkEmitted = true
						s.pending = append(s.pending, chunk)
					}
				}
			}
		}

		if len(s.pending) > 0 {
			chunk := s.pending[0]
			s.pending = s.pending[1:]
			return chunk, nil
		}
	}
}

func (s *googleLLMStream) storeThoughtSignature(part *genai.Part, chunk *llm.ChatChunk) {
	if part == nil || len(part.ThoughtSignature) == 0 || s.thoughtSignatures == nil || chunk == nil || chunk.Delta == nil {
		return
	}
	for _, call := range chunk.Delta.ToolCalls {
		if call.CallID == "" {
			continue
		}
		signature := append([]byte(nil), part.ThoughtSignature...)
		if s.thoughtMu != nil {
			s.thoughtMu.Lock()
			s.thoughtSignatures[call.CallID] = signature
			s.thoughtMu.Unlock()
			continue
		}
		s.thoughtSignatures[call.CallID] = signature
	}
}

func googleLLMFinishReasonBody(reason genai.FinishReason) any {
	if reason == "" || reason == genai.FinishReasonUnspecified {
		return "finish reason: None"
	}
	return fmt.Sprintf("finish reason: %s", reason)
}

func (s *googleLLMStream) id() string {
	if s.requestID == "" {
		s.requestID = cavosmath.ShortUUID("")
	}
	return s.requestID
}

func googleLLMStreamError(err error, retryable bool, requestID string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		message := "gemini llm: api error"
		if apiErr.Code >= 400 && apiErr.Code < 500 {
			message = "gemini llm: client error"
			retryable = apiErr.Code == 429 || apiErr.Code == 499
		} else if apiErr.Code >= 500 && apiErr.Code < 600 {
			message = "gemini llm: server error"
		}
		return llm.NewAPIStatusErrorWithRetryable(message, apiErr.Code, requestID, strings.TrimSpace(apiErr.Message+" "+apiErr.Status), retryable)
	}
	return llm.NewAPIConnectionErrorWithRetryable(fmt.Sprintf("gemini llm: error generating content %s", err), retryable)
}

func googleChatChunkFromPart(part *genai.Part) (*llm.ChatChunk, error) {
	if part == nil {
		return nil, nil
	}
	chunk := &llm.ChatChunk{
		Delta: &llm.ChoiceDelta{
			Role: llm.ChatRoleAssistant,
		},
	}
	if part.FunctionCall == nil {
		if part.Text != "" {
			chunk.Delta.Content = part.Text
			return chunk, nil
		}
		return nil, nil
	}
	args, err := json.Marshal(part.FunctionCall.Args)
	if err != nil {
		return nil, err
	}
	chunk.Delta.ToolCalls = append(chunk.Delta.ToolCalls, llm.FunctionToolCall{
		Name:      part.FunctionCall.Name,
		Arguments: string(args),
		Type:      "function",
		CallID:    googleFunctionCallID(part.FunctionCall),
	})
	return chunk, nil
}

func googleBlockedFinishReason(reason genai.FinishReason) bool {
	switch reason {
	case genai.FinishReasonSafety,
		genai.FinishReasonSPII,
		genai.FinishReasonProhibitedContent,
		genai.FinishReasonBlocklist,
		genai.FinishReasonLanguage,
		genai.FinishReasonRecitation:
		return true
	default:
		return false
	}
}

func googleFunctionCallID(call *genai.FunctionCall) string {
	if call == nil {
		return ""
	}
	if call.ID != "" {
		return call.ID
	}
	return cavosmath.ShortUUID("function_call_")
}

func (s *googleLLMStream) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		if s.stop != nil {
			s.stop()
		}
	})
	return nil
}
