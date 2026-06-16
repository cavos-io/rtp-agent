---
id: agents-framework
title: Agents framework
---

# Agents framework reference

Status: **implemented** for Go runtime APIs.

Use this page as a quick lookup for the main Go runtime surfaces.

## Application layer

| API | Purpose |
|---|---|
| `app.DefaultConfigFromEnv()` | Read environment variables into `app.AppConfig`. |
| `app.Init(cfg)` | Build an app with worker server, agent session, model providers, tools, telemetry, and evaluation wiring. |
| `app.NewApp(cfg)` | App construction entrypoint where used by callers/examples. |
| `(*app.App).Close(ctx)` | Close app-owned runtime resources. |
| `(*app.App).EvaluateSession(ctx, reference)` | Run configured evaluators against the active session. |

## Agent layer

| API | Purpose |
|---|---|
| `agent.NewAgent(instructions)` | Create a code-first agent with instructions, chat context, and tools. |
| `agent.NewAgentSession(agent, room, options)` | Create a low-level session. Most apps use `app.Init`. |
| `(*agent.Agent).UpdateInstructions(ctx, instructions)` | Replace active instructions. |
| `(*agent.Agent).UpdateTools(ctx, tools)` | Replace active tools and update chat context state. |
| `(*agent.AgentSession).Run(ctx, userInput)` | Run a text turn and collect a `RunResult`. |
| `(*agent.AgentSession).GenerateReply(ctx, userInput)` | Schedule a model-generated reply. |
| `(*agent.AgentSession).Say(ctx, text)` | Schedule fixed speech text. |
| `(*agent.AgentSession).UpdateAgent(agent)` | Switch the active agent implementation. |

## Worker and CLI layer

| API | Purpose |
|---|---|
| `worker.NewAgentServer(options)` | Create the worker server. |
| `(*worker.AgentServer).Run(ctx)` | Run the registered worker. |
| `(*worker.AgentServer).Drain(ctx)` | Start drain behavior. |
| `cli.RunApp(server, evalRunners...)` | Run CLI modes against an agent server. |

Evidence:

- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `core/agent/agent_activity.go`
- `app/app.go`
- `interface/worker/server.go`
- `interface/cli/cli.go`
