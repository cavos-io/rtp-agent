---
id: overview
title: Logic and structure
---

# Logic and structure

Agent logic lives in Go code. The main extension points are:

- `agent.Agent` fields for instructions, chat context, tools, model providers, and runtime policy.
- `AgentInterface` hooks such as `OnEnter`, `OnExit`, and `OnUserTurnCompleted`.
- `AgentSession` methods such as `GenerateReplyWithOptions`, `Say`, `Run`, and update methods.
- Workflow helpers under `core/beta/workflows`.

The app layer can wrap a configured base agent with workflow agents based on `AppConfig`. This keeps provider setup in `app` while allowing task-specific behavior in packages such as `examples/voice_agents/basic_agent/basicagent`.

