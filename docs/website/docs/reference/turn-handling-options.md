---
id: turn-handling-options
title: Turn handling options
---

# Turn handling options

Status: **implemented**.

Evidence:

- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `core/agent/endpointing.go`
- `adapter/livekit/turn_detector.go`
- `adapter/pipecat/smart_turn.go`

Source-backed turn modes are `stt`, `vad`, `realtime_llm`, and `manual`. Interruption and endpointing controls live in `AgentSessionOptions`.

