---
id: realtime
title: Realtime
---

# Realtime

Status: **partial**.

Use realtime models when a provider exposes a session-oriented API that can handle ongoing interaction directly.

The core boundary is `llm.RealtimeModel`, which creates realtime sessions consumed by `core/agent/multimodal_agent.go`. This is separate from the speech pipeline, where STT, LLM, and TTS are separate components.

## Current adapter evidence

At `v0.0.67`, source-backed realtime adapter files exist for:

- OpenAI: `adapter/openai/realtime.go`
- Phonic: `adapter/phonic/realtime.go`

Do not document realtime support for a provider unless its adapter package contains `realtime.go` or equivalent source implementing `llm.RealtimeModel`.

Evidence:

- `core/llm/llm.go`
- `adapter/openai/realtime.go`
- `adapter/phonic/realtime.go`
- `core/agent/multimodal_agent.go`
