---
id: pipeline-types
title: Pipeline types
---

# Pipeline types

Status: **implemented** for speech pipeline and realtime model paths.

Use pipeline type to decide how audio, text, and models flow through the session.

## Speech pipeline

The speech pipeline composes separate components:

- VAD or turn detection decides when speech starts and ends.
- STT converts user audio into text.
- LLM generates the reply or tool calls.
- TTS converts reply text back into audio.

This path is implemented by `core/agent/pipeline_agent.go` and generation helpers.

## Realtime model path

The realtime path uses `llm.RealtimeModel` and `core/agent/multimodal_agent.go`. It is useful when a provider exposes a realtime session that handles streaming interaction more directly than separate STT/LLM/TTS adapters.

## Choosing between them

Use the speech pipeline when you want provider flexibility and separate control over STT, LLM, and TTS. Use realtime only when the adapter you want has a `realtime.go` implementation and the provider's session semantics match your product.

Evidence:

- `core/agent/pipeline_agent.go`
- `core/agent/multimodal_agent.go`
- `core/agent/generation.go`
- `interface/worker/room_io.go`
