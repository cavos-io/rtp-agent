---
id: livekit-inference
title: LiveKit Inference
---

# LiveKit Inference

Status: **implemented** as adapter support.

Use the `livekit` provider when you want `rtp-agent` to reach LiveKit Inference through the repository's adapter package.

The adapter supports LLM, STT, and TTS. It is not a separate model catalog page; model names should come from source examples, tests, or LiveKit Inference configuration you control.

## App configuration

The basic agent configures LiveKit Inference by setting:

- `LLMProvider = "livekit"`
- `STTProvider = "livekit"`
- `TTSProvider = "livekit"`

Credentials are resolved from `LIVEKIT_API_KEY` and `LIVEKIT_API_SECRET` for the inference adapter paths.

## Direct constructors

Use direct constructors when you are outside `app.Init`:

- `livekit.NewLiveKitInferenceLLM`
- `livekit.NewSTT`
- `livekit.NewTTS`

Evidence:

- `adapter/livekit/llm.go`
- `adapter/livekit/stt.go`
- `adapter/livekit/tts.go`
- `adapter/livekit/models.go`
- `app/app.go`
- `examples/voice_agents/basic_agent/basicagent/basic_agent.go`
