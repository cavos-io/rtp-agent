---
id: prompting-guide
title: Prompting guide
---

# Prompting guide

Status: **partial**.

Agent instructions are source-backed through `agent.Agent.Instructions`, `llm.Instructions`, and `app.AppConfig.Instructions`.

Evidence:

- `core/agent/agent.go`
- `core/llm/llm.go`
- `app/app.go`
- `examples/voice_agents/basic_agent/basicagent/basic_agent.go`

Use `RTP_AGENT_INSTRUCTIONS` or set `AppConfig.Instructions` in code:

```go
package main

import "github.com/cavos-io/rtp-agent/app"

func config() app.AppConfig {
	cfg := app.DefaultConfigFromEnv()
	cfg.Instructions = "You are concise, accurate, and helpful."
	return cfg
}
```

