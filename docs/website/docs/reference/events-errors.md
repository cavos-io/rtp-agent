---
id: events-errors
title: Events and errors
---

# Events and errors

Status: **implemented**.

Use this page as a lookup for runtime event and error categories.

## Agent events

Agent events implement `agent.Event` with `GetType() string`.

| Event type | Source struct |
|---|---|
| `user_input_transcribed` | `UserInputTranscribedEvent` |
| `agent_output_transcribed` | `AgentOutputTranscribedEvent` |
| `user_turn_exceeded` | `UserTurnExceededEvent` |
| `overlapping_speech` | `OverlappingSpeechEvent` |
| `conversation_item_added` | `ConversationItemAddedEvent` |
| `agent_false_interruption` | `AgentFalseInterruptionEvent` |
| `function_tools_executed` | `FunctionToolsExecutedEvent` |
| `metrics_collected` | `MetricsCollectedEvent` |
| `session_usage_updated` | `SessionUsageUpdatedEvent` |
| `error` | `ErrorEvent` |
| `speech_created` | `SpeechCreatedEvent` |
| `close` | `CloseEvent` |

## Close reasons

Source-backed close reasons are:

- `error`
- `job_shutdown`
- `participant_disconnected`
- `user_initiated`
- `task_completed`

## Error categories

Core packages define typed errors for LLM, STT, TTS, realtime models, and tools. Provider adapters normalize errors where implemented, but the exact normalization is adapter-specific and should be checked in the adapter tests before documenting provider behavior.

Evidence:

- `core/agent/events.go`
- `core/llm/errors.go`
- `core/stt/errors.go`
- `core/tts/errors.go`
- tests under `core/*/*_test.go`
