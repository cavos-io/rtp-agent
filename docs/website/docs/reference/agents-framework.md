---
id: agents-framework
title: Agents framework
---

# Agents framework reference

Status: **implemented** for Go runtime APIs.

Evidence:

- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `core/agent/agent_activity.go`
- `app/app.go`

Primary API surfaces:

- `agent.NewAgent`
- `agent.NewAgentSession`
- `app.DefaultConfigFromEnv`
- `app.Init` / `app.NewApp`
- `cli.RunApp`

