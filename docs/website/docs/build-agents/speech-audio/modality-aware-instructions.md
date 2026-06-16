---
id: modality-aware-instructions
title: Modality-aware instructions
---

# Modality-aware instructions

Status: **partial**.

The source supports text and audio instruction variants through `llm.Instructions`, and agents can update instructions at runtime.

Evidence:

- `core/llm/llm.go`
- `core/agent/agent.go`
- `core/agent/agent_activity.go`

Use `llm.NewInstructions(audio, text...)` when a provider path consumes modality-specific instructions. Otherwise, use `Agent.Instructions` or `AppConfig.Instructions`.

