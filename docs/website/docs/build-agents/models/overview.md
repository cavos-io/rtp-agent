---
id: overview
title: Overview
---

# Models overview

Status: **implemented** for provider interfaces and many adapters; **partial** for cloud model catalog behavior.

Use this section to choose which model interface your agent needs.

`rtp-agent` treats models as Go interfaces plus provider adapters. The app layer wires configured adapters into `agent.Agent` and `agent.AgentSession`, but you can also construct providers directly when you need provider-specific options.

## Choose by runtime path

- Use `llm.LLM` for text/chat generation in the speech pipeline.
- Use `stt.STT` to turn user audio into speech events.
- Use `tts.TTS` to synthesize agent speech into audio frames.
- Use `llm.RealtimeModel` for realtime model sessions.
- Use `agent.AvatarProvider` when a provider drives virtual-avatar state.
- Use VAD providers when endpointing or speech activity should be separated from STT.

## Choose providers by source capability

A provider supports a capability only when its adapter package contains the matching source file, such as `llm.go`, `stt.go`, `tts.go`, `realtime.go`, or `avatar.go`. Model names and provider option details belong to the adapter source and tests, not a hand-maintained marketing catalog.

Evidence:

- `core/llm/llm.go`
- `core/stt/stt.go`
- `core/tts/tts.go`
- `core/vad/vad.go`
- `core/agent/avatar.go`
- `adapter/*`
