package llm

import (
	"fmt"
	"strings"
)

type LLMFactory func(model string) (LLM, error)

var exporters = make(map[string]LLMFactory)

func Register(provider string, factory LLMFactory) {
	exporters[provider] = factory
}

func FromModelString(s string) (LLM, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid model string: %s (expected provider:model)", s)
	}

	provider := parts[0]
	model := strings.Join(parts[1:], ":")

	factory, ok := exporters[provider]
	if !ok {
		return nil, fmt.Errorf("unknown LLM provider: %s", provider)
	}

	return factory(model)
}
