---
id: pipeline-nodes-hooks
title: Pipeline nodes and hooks
---

# Pipeline nodes and hooks

Status: **intentionally different**.

Evidence:

- `core/agent/generation.go`
- `core/agent/agent.go`
- `core/agent/events.go`

`rtp-agent` exposes Go functions, interfaces, event emitters, and generation helpers instead of LiveKit's exact pipeline-node API. Tool execution and LLM/TTS inference behavior are implemented in `core/agent/generation.go`.

