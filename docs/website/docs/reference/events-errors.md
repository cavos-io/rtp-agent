---
id: events-errors
title: Events and errors
---

# Events and errors

Status: **implemented**.

Evidence:

- `core/agent/events.go`
- `core/llm/errors.go`
- `core/stt/errors.go`
- `core/tts/errors.go`
- tests under `core/*/*_test.go`

Event and error types are Go structs and interfaces. Provider adapters normalize errors to core LLM/STT/TTS error types where implemented.

