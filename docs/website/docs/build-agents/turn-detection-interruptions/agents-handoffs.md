---
id: agents-handoffs
title: Agents and handoffs
---

# Agents and handoffs

Status: **partial**.

Evidence:

- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `app/app.go`

Agents can be updated on a session with `AgentSession.UpdateAgent`. App workflow configuration includes a `handoff` workflow entry, but this is not a complete LiveKit multi-agent handoff system.

