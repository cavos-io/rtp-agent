---
id: agents-handoffs
title: Agents and handoffs
---

# Agents and handoffs

Status: **partial**.

Use `AgentSession.UpdateAgent` when a running session needs to switch to a different agent implementation.

The handoff support in this repository is lower-level than the full LiveKit multi-agent guide. The session can update the active agent, copy tools and model components, and record handoff events in `RunResult`. App workflow configuration also includes a `handoff` workflow entry.

## Practical boundary

This is enough to build controlled agent switching in Go code. It is not yet a complete supervisor or multi-agent framework with all LiveKit handoff recipes documented as supported.

Before relying on handoff behavior, inspect the tests around `RunResult`, `AgentSession.UpdateAgent`, and any workflow helper you plan to use.

Evidence:

- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `core/agent/run_result.go`
- `app/app.go`
