---
id: livekit-inference
title: LiveKit Inference
---

# LiveKit Inference

Status: **implemented** as adapter support.

Evidence:

- `adapter/livekit/llm.go`
- `adapter/livekit/stt.go`
- `adapter/livekit/tts.go`
- `adapter/livekit/models.go`
- `app/app.go`

Use provider `livekit` in app configuration or construct adapters directly with `livekit.NewLiveKitInferenceLLM`, `livekit.NewSTT`, and `livekit.NewTTS`.

