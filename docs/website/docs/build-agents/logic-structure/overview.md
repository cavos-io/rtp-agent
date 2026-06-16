---
id: overview
title: Overview
---

# Logic and structure overview

Status: **implemented** for Go runtime primitives; **partial** for all LiveKit patterns.

Evidence:

- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `core/agent/agent_activity.go`
- `core/beta/workflows`

Core structure:

- `Agent` holds instructions, model providers, tools, and runtime policy.
- `AgentSession` coordinates activity, turn handling, generation, and room I/O.
- `AgentActivity` binds an agent to a session.
- Workflow helpers live under `core/beta/workflows`.

