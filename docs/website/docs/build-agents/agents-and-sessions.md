---
id: agents-and-sessions
title: Agents and sessions
---

# Agents and sessions

Use `app.NewApp` for most applications. It creates a base `agent.Agent`, configures providers from `AppConfig`, creates an `agent.AgentSession`, and attaches everything to a `worker.AgentServer`.

For custom agent behavior, embed or wrap `*agent.Agent` and implement lifecycle hooks.

```go
package main

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/agent"
)

type assistant struct {
	*agent.Agent
}

func newAssistant() *assistant {
	return &assistant{
		Agent: agent.NewAgent("You are a concise realtime assistant."),
	}
}

func (a *assistant) OnEnter() {
	activity := a.GetActivity()
	if activity == nil || activity.Session == nil {
		return
	}
	_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
		Instructions: "Greet the user.",
	})
}
```

This mirrors the pattern used by `examples/voice_agents/basic_agent/basicagent/basic_agent.go`.

## Low-level session constructor

The low-level constructor is:

```go
package main

import "github.com/cavos-io/rtp-agent/core/agent"

func newSession() *agent.AgentSession {
	myAgent := agent.NewAgent("You are a concise realtime assistant.")

	return agent.NewAgentSession(myAgent, nil, agent.AgentSessionOptions{
		AllowInterruptions:    true,
		AllowInterruptionsSet: true,
		MaxToolSteps:          3,
		MaxToolStepsSet:       true,
	})
}
```

The second argument is a `*livekit.Room`. App-level setup usually passes `nil` and lets worker room I/O attach runtime room state later.

## Session options

`AgentSessionOptions` controls runtime behavior such as:

- interruption handling
- endpointing delays
- turn detection mode
- max tool steps
- text transforms for TTS
- background audio
- mock tools for tests

Defaults are applied by `agent.NewAgentSession`.
