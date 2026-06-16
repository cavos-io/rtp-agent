---
id: overview
title: Turn detection and interruptions
---

# Turn detection and interruptions

Turn handling is controlled by `agent.AgentSessionOptions` and model-specific providers.

Supported turn detection modes in source are:

- `agent.TurnDetectionModeSTT`
- `agent.TurnDetectionModeVAD`
- `agent.TurnDetectionModeRealtimeLLM`
- `agent.TurnDetectionModeManual`

At app level, use:

```bash
RTP_AGENT_TURN_DETECTOR_PROVIDER=livekit
RTP_AGENT_VAD_PROVIDER=silero
```

Source-backed turn detector providers include:

- `adapter/livekit` for LiveKit turn detector models.
- `adapter/pipecat` for smart turn detection.
- VAD adapters such as `adapter/silero` and `adapter/ten`.

Interruption behavior is controlled by options such as `AllowInterruptions`, `MinInterruptionDuration`, `FalseInterruptionTimeout`, `ResumeFalseInterruption`, `MinEndpointingDelay`, and `MaxEndpointingDelay`.

