---
id: tts
title: TTS
---

# TTS

Status: **implemented**.

Evidence:

- `core/tts/tts.go`
- `core/tts/tts_test.go`
- `adapter/*/tts.go`

TTS adapters implement `tts.TTS`. TTS text transforms, replacements, and pacing are configured through `AgentSessionOptions` and `RTP_AGENT_TTS_*` variables.

