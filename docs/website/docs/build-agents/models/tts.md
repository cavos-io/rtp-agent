---
id: tts
title: TTS
---

# TTS

Status: **implemented**.

Use TTS when generated text must become audio frames for the agent to publish.

Adapters implement `tts.TTS`. The interface exposes provider capabilities, sample rate, channel count, one-shot synthesis, and streaming synthesis.

Configure app-level TTS with:

```bash
export RTP_AGENT_TTS_PROVIDER="livekit"
export RTP_AGENT_TTS_MODEL="cartesia/sonic-3"
export RTP_AGENT_TTS_VOICE="voice-id"
```

TTS output can also be shaped at the session layer with text replacements, transforms, stream pacing, and aligned transcript options. Use those options when the generated text needs to be spoken differently than it is stored in chat context.

Evidence:

- `core/tts/tts.go`
- `core/tts/tts_test.go`
- `adapter/*/tts.go`
- `core/agent/agent_session.go`
- `app/app.go`
