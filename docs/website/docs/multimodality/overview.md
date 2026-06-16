---
id: overview
title: Multimodality
---

# Multimodality

`rtp-agent` models multiple runtime boundaries:

- LLM through `core/llm`
- STT through `core/stt`
- TTS through `core/tts`
- VAD through `core/vad`
- realtime model sessions through `llm.RealtimeModel`
- avatar providers through `agent.AvatarProvider`

The app layer wires these capabilities from `AppConfig`. A single provider package may implement one or more modality files, such as `llm.go`, `stt.go`, `tts.go`, `realtime.go`, or `avatar.go`.

Do not assume that a provider package supports every modality. Check the provider capability reference for the source-backed list.

