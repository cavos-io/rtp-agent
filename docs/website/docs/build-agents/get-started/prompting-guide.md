---
id: prompting-guide
title: Prompting guide
---

# Prompting guide

Status: **partial**.

Use instructions to set the agent's baseline behavior, and use reply options when one turn needs different guidance.

`rtp-agent` does not have a separate prompt-builder product surface. Prompting is part of the Go API:

- `app.AppConfig.Instructions` sets the default instructions when the app is initialized.
- `agent.NewAgent(instructions)` creates an agent with instructions and an empty chat context.
- `Agent.UpdateInstructions(ctx, instructions)` updates instructions before or during activity.
- `GenerateReplyOptions.Instructions` supplies turn-specific instructions for a generated reply.

## Set default instructions from config

Use environment configuration when instructions should be deploy-time data:

```bash
export RTP_AGENT_INSTRUCTIONS="You are concise, accurate, and helpful."
```

Use Go code when instructions are part of the app:

```go
package main

import "github.com/cavos-io/rtp-agent/app"

func config() app.AppConfig {
	cfg := app.DefaultConfigFromEnv()
	cfg.Instructions = "You are concise, accurate, and helpful."
	return cfg
}
```

## Keep spoken responses usable

For voice agents, include constraints that match audio output. The basic agent asks for concise English responses and avoids markdown or special characters because the response is spoken.

## Trigger a first reply

An agent can start a conversation in `OnEnter()` by calling `GenerateReplyWithOptions` on the active session and passing `GenerateReplyOptions{Instructions: ...}`. The basic agent uses this pattern for its greeting.

Evidence:

- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `core/llm/llm.go`
- `app/app.go`
- `examples/voice_agents/basic_agent/basicagent/basic_agent.go`
