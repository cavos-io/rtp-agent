---
id: turn-handling-options
title: Turn handling options
---

# Turn handling options

Status: **implemented**.

Use this page to look up turn detection, endpointing, and interruption controls.

## Turn detection modes

| Mode | Constant |
|---|---|
| `stt` | `agent.TurnDetectionModeSTT` |
| `vad` | `agent.TurnDetectionModeVAD` |
| `realtime_llm` | `agent.TurnDetectionModeRealtimeLLM` |
| `manual` | `agent.TurnDetectionModeManual` |

The active mode can come from `Agent.TurnDetection`, `AgentSessionOptions.TurnDetection`, or `AgentSessionUpdateOptions.TurnDetection`, depending on where the session is being configured.

## Session options

Turn-related fields live in `AgentSessionOptions`, including:

- `AllowInterruptions`
- `DiscardAudioIfUninterruptible`
- `MinInterruptionDuration`
- `MinInterruptionWords`
- `MinEndpointingDelay`
- `MaxEndpointingDelay`
- `EndpointingMode`
- `EndpointingAlpha`
- `Endpointing`
- `FalseInterruptionTimeout`
- `ResumeFalseInterruption`
- `MinConsecutiveSpeechDelay`
- `TurnDetection`

Provider-specific endpointing and turn detector options are configured through app/provider fields only when the selected adapter consumes them.

Evidence:

- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `core/agent/endpointing.go`
- `adapter/livekit/turn_detector.go`
- `adapter/pipecat/smart_turn.go`
