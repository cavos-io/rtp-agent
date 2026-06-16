---
id: overview
title: Overview
---

# Logic and structure overview

Status: **implemented** for Go runtime primitives; **partial** for all LiveKit patterns.

Use this section when your agent needs more structure than one instruction string and one model call.

The runtime is organized around three primary objects:

- `Agent` holds instructions, model providers, tools, chat context, and runtime policy.
- `AgentSession` coordinates activity, turn handling, generation, speech, state, and room I/O.
- `AgentActivity` binds an agent implementation to a running session.

For most applications, create the app through `app.Init` and then update the configured agent/session. Use low-level constructors only in tests, examples, or custom composition code.

## Pick the right structure

- Use chat context when the model needs prior conversation or tool-call state.
- Use tools when the model should call Go code.
- Use `Run`, `GenerateReply`, or `Say` for text-driven session actions.
- Use `UpdateAgent` when a session should switch to a different agent implementation.
- Use beta workflow helpers only when their current source matches your task.

LiveKit docs include broader named patterns such as supervisor orchestration. In `rtp-agent`, those are documented as unavailable unless a source package and tests implement them.

Evidence:

- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `core/agent/agent_activity.go`
- `core/beta/workflows`
