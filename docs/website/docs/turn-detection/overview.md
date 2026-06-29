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

To enable smart turn detection backed by the `grpc-llm` `SmartTurnServiceV1` inference service:

```bash
RTP_AGENT_TURN_DETECTOR_PROVIDER=cavos
RTP_AGENT_SMART_TURN_GRPC_ADDR=localhost:9001
RTP_AGENT_VAD_PROVIDER=silero
```

`RTP_AGENT_SMART_TURN_GRPC_ADDR` defaults to `localhost:9001`. Smart turn layers on
VAD as a confirmation step, so keep `RTP_AGENT_VAD_PROVIDER` set.

Source-backed turn detector providers include:

- `adapter/livekit` for LiveKit turn detector models.
- `adapter/cavos` for gRPC-backed smart turn detection (`SmartTurnServiceV1` ONNX inference).
- `adapter/pipecat` for local smart turn detection.
- VAD adapters such as `adapter/silero` and `adapter/ten`.

Interruption behavior is controlled by options such as `AllowInterruptions`, `MinInterruptionDuration`, `FalseInterruptionTimeout`, `ResumeFalseInterruption`, `MinEndpointingDelay`, and `MaxEndpointingDelay`.

