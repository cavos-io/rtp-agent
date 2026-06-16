---
id: pipeline-types
title: Pipeline types
---

# Pipeline types

Status: **implemented** for speech pipeline and realtime model paths.

Evidence:

- `core/agent/pipeline_agent.go`
- `core/agent/multimodal_agent.go`
- `core/agent/generation.go`
- `interface/worker/room_io.go`

The speech pipeline uses STT, VAD, LLM, and TTS. Realtime models use `llm.RealtimeModel` and `agent.NewMultimodalAgent`.

