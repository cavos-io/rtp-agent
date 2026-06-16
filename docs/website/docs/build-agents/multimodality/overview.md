---
id: overview
title: Overview
---

# Multimodality overview

Status: **partial**.

The runtime exposes LLM, STT, TTS, VAD, realtime model, image/video frame, and avatar boundaries. End-to-end multimodal behavior depends on configured providers and room I/O.

Evidence:

- `core/llm/llm.go`
- `core/stt/stt.go`
- `core/tts/tts.go`
- `core/vad/vad.go`
- `core/agent/video_sampler.go`
- `core/agent/avatar.go`
- `interface/worker/room_io.go`

Provider support is capability-based. See the [provider capability reference](/reference/providers).
