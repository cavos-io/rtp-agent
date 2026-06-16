---
id: tools
title: Define tools
---

# Define tools

Tools are values that satisfy `llm.Tool`. A tool exposes an ID, name, description, JSON-schema-like parameters, and an `Execute` method.

```go
type lookupWeatherTool struct{}

func (lookupWeatherTool) ID() string   { return "lookup_weather" }
func (lookupWeatherTool) Name() string { return "lookup_weather" }
func (lookupWeatherTool) Description() string {
	return "Look up weather for a location."
}
func (lookupWeatherTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{"type": "string"},
		},
		"required": []string{"location"},
	}
}
func (lookupWeatherTool) Execute(ctx context.Context, args string) (string, error) {
	return "sunny with a temperature of 70 degrees.", nil
}
```

Attach tools to an agent:

```go
a := agent.NewAgent("Use tools when they help.")
a.Tools = []llm.Tool{lookupWeatherTool{}}
```

The checked-in basic agent uses this pattern and also adds `betatools.NewSessionEndCallTool`.

