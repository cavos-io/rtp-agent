---
id: agent-sessions
title: Agent sessions
---

# Agent sessions

Status: **implemented**.

Evidence:

- `core/agent/agent_session.go`
- `core/agent/agent_session_test.go`
- `core/agent/events.go`

The low-level constructor is:

```go
package main

import "github.com/cavos-io/rtp-agent/core/agent"

func newSession() *agent.AgentSession {
	a := agent.NewAgent("You are a helpful realtime agent.")
	return agent.NewAgentSession(a, nil, agent.AgentSessionOptions{})
}
```

Most applications should use `app.NewApp`, which configures providers and creates the session.

