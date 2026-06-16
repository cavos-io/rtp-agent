---
id: overview
title: Overview
---

# Multimodality overview

Status: **partial**.

Use this page to decide which modality path your agent should use.

`rtp-agent` exposes separate boundaries for text, audio, realtime models, video frames, and avatars. It does not hide all of those choices behind one universal multimodal builder. You choose the runtime path by configuring the components on `app.AppConfig`, `agent.Agent`, or `agent.AgentSession`.

## Common paths

- Speech pipeline: combine STT, LLM, TTS, and optionally VAD.
- Realtime model: use an adapter that implements `llm.RealtimeModel`.
- Text-only turns: call `AgentSession.Run` or `GenerateReply` with text input.
- Video-aware turns: sample incoming video frames with `VoiceActivityVideoSampler` and use a provider path that consumes images.
- Avatar output: configure an avatar provider when an adapter exposes avatar support.

## Capability boundary

Provider support is capability-based. A provider is only documented for a modality when the adapter package contains the corresponding source file, such as `llm.go`, `stt.go`, `tts.go`, `realtime.go`, or `avatar.go`.

See the [provider capability reference](/reference/providers) before choosing a provider.

Evidence:

- `core/llm/llm.go`
- `core/stt/stt.go`
- `core/tts/tts.go`
- `core/vad/vad.go`
- `core/agent/video_sampler.go`
- `core/agent/avatar.go`
- `interface/worker/room_io.go`
