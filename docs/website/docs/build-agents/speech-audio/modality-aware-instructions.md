---
id: modality-aware-instructions
title: Modality-aware instructions
---

# Modality-aware instructions

Status: **partial**.

Use modality-aware instructions when a provider path needs different guidance for audio and text.

Most agents can use a single instruction string through `AppConfig.Instructions` or `agent.NewAgent(instructions)`. Use `llm.NewInstructions(audio, text...)` only when the model path consumes instruction variants.

## Choosing the right instruction API

- Use `AppConfig.Instructions` for app-wide defaults.
- Use `Agent.UpdateInstructions` when the active agent needs a new baseline.
- Use `GenerateReplyOptions.Instructions` for one reply.
- Use `llm.NewInstructions(audio, text...)` for paths that distinguish spoken and text behavior.

Keep voice instructions short and concrete. The basic agent constrains spoken output by asking for concise responses and avoiding markdown or special characters.

Evidence:

- `core/llm/llm.go`
- `core/agent/agent.go`
- `core/agent/agent_activity.go`
- `examples/voice_agents/basic_agent/basicagent/basic_agent.go`
