package google

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/llm"
	"google.golang.org/genai"
)

type GoogleSearchTool struct {
	ExcludeDomains     []string
	BlockingConfidence genai.PhishBlockThreshold
	TimeRangeFilter    *genai.Interval
}

func (t *GoogleSearchTool) ID() string          { return "gemini_google_search" }
func (t *GoogleSearchTool) Name() string        { return "gemini_google_search" }
func (t *GoogleSearchTool) Description() string { return "Enable Google Search grounding." }
func (t *GoogleSearchTool) Parameters() map[string]any {
	return nil
}
func (t *GoogleSearchTool) Execute(context.Context, string) (string, error) {
	return "dispatched", nil
}
func (t *GoogleSearchTool) IsProviderTool() bool { return true }

func (t *GoogleSearchTool) googleToolConfig() *genai.Tool {
	if t == nil {
		return nil
	}
	return &genai.Tool{
		GoogleSearch: &genai.GoogleSearch{
			ExcludeDomains:     append([]string(nil), t.ExcludeDomains...),
			BlockingConfidence: t.BlockingConfidence,
			TimeRangeFilter:    t.TimeRangeFilter,
		},
	}
}

type FileSearchTool struct {
	FileSearchStoreNames []string
	TopK                 *int32
	MetadataFilter       string
}

func (t *FileSearchTool) ID() string          { return "gemini_file_search" }
func (t *FileSearchTool) Name() string        { return "gemini_file_search" }
func (t *FileSearchTool) Description() string { return "Enable Gemini file search." }
func (t *FileSearchTool) Parameters() map[string]any {
	return nil
}
func (t *FileSearchTool) Execute(context.Context, string) (string, error) {
	return "dispatched", nil
}
func (t *FileSearchTool) IsProviderTool() bool { return true }

func (t *FileSearchTool) googleToolConfig() *genai.Tool {
	if t == nil {
		return nil
	}
	var topK *int32
	if t.TopK != nil {
		value := *t.TopK
		topK = &value
	}
	return &genai.Tool{
		FileSearch: &genai.FileSearch{
			FileSearchStoreNames: append([]string(nil), t.FileSearchStoreNames...),
			TopK:                 topK,
			MetadataFilter:       t.MetadataFilter,
		},
	}
}

type URLContextTool struct{}

func (t *URLContextTool) ID() string          { return "gemini_url_context" }
func (t *URLContextTool) Name() string        { return "gemini_url_context" }
func (t *URLContextTool) Description() string { return "Enable Gemini URL context." }
func (t *URLContextTool) Parameters() map[string]any {
	return nil
}
func (t *URLContextTool) Execute(context.Context, string) (string, error) {
	return "dispatched", nil
}
func (t *URLContextTool) IsProviderTool() bool { return true }

func (t *URLContextTool) googleToolConfig() *genai.Tool {
	if t == nil {
		return nil
	}
	return &genai.Tool{URLContext: &genai.URLContext{}}
}

type CodeExecutionTool struct{}

func (t *CodeExecutionTool) ID() string          { return "gemini_code_execution" }
func (t *CodeExecutionTool) Name() string        { return "gemini_code_execution" }
func (t *CodeExecutionTool) Description() string { return "Enable Gemini code execution." }
func (t *CodeExecutionTool) Parameters() map[string]any {
	return nil
}
func (t *CodeExecutionTool) Execute(context.Context, string) (string, error) {
	return "dispatched", nil
}
func (t *CodeExecutionTool) IsProviderTool() bool { return true }

func (t *CodeExecutionTool) googleToolConfig() *genai.Tool {
	if t == nil {
		return nil
	}
	return &genai.Tool{CodeExecution: &genai.ToolCodeExecution{}}
}

func googleToolsConfig(tools []llm.Tool, behavior any) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	configs := make([]*genai.Tool, 0, len(tools))
	declarations := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		if _, ok := tool.(llm.ProviderTool); ok {
			if config := googleProviderToolConfig(tool); config != nil {
				configs = append(configs, config)
			}
			continue
		}
		declaration := buildGoogleFunctionDeclaration(tool)
		if behavior := googleRealtimeToolBehavior(behavior); behavior != "" {
			declaration.Behavior = behavior
		}
		declarations = append(declarations, declaration)
	}
	if len(declarations) > 0 {
		configs = append([]*genai.Tool{{FunctionDeclarations: declarations}}, configs...)
	}
	if len(configs) == 0 {
		return nil
	}
	return configs
}

func googleProviderToolConfig(tool llm.Tool) *genai.Tool {
	switch t := tool.(type) {
	case *GoogleSearchTool:
		return t.googleToolConfig()
	case *FileSearchTool:
		return t.googleToolConfig()
	case *URLContextTool:
		return t.googleToolConfig()
	case *CodeExecutionTool:
		return t.googleToolConfig()
	default:
		return nil
	}
}
