---
id: agent-sessions
title: Agent sessions
---

# Agent sessions

Status: **implemented**.

Use an agent session to coordinate an agent's runtime state: chat history, tools, configured model components, speech generation, user and agent states, and room connection.

Most application code should let `app.Init` create and configure the session. Use `agent.NewAgentSession` directly when you are writing a narrow test or building custom composition code.

## Create a low-level session

The low-level constructor is:

```go
package main

import "github.com/cavos-io/rtp-agent/core/agent"

func newSession() *agent.AgentSession {
	a := agent.NewAgent("You are a helpful realtime agent.")
	return agent.NewAgentSession(a, nil, agent.AgentSessionOptions{})
}
```

## Use session methods by intent

- `Run(ctx, userInput)` runs a text turn and returns a `RunResult`.
- `GenerateReply(ctx, userInput)` schedules a generated reply.
- `Say(ctx, text)` schedules fixed speech text.
- `History()` returns the session chat context.
- `UpdateAgent(agent)` swaps the active agent and refreshes its tools/components.
- `SessionOptions()` returns the active options snapshot.

Avoid mutating session internals directly unless the field is explicitly part of the composition path you are using.

Evidence:

- `core/agent/agent_session.go`
- `core/agent/agent_session_test.go`
- `core/agent/events.go`
