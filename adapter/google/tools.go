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
	default:
		return nil
	}
}
