---
id: overview
title: Overview
---

# Models overview

Status: **implemented** for provider interfaces and many adapters; **partial** for cloud model catalog behavior.

Evidence:

- `core/llm/llm.go`
- `core/stt/stt.go`
- `core/tts/tts.go`
- `core/vad/vad.go`
- `adapter/*`

Models are Go interfaces and provider-specific adapters. The app layer wires configured providers into `agent.Agent` and `agent.AgentSession`.

