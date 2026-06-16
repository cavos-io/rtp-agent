---
id: fallback-strategies
title: Fallback strategies
---

# Fallback strategies

Status: **implemented** for LLM, STT, and TTS fallback adapters.

Evidence:

- `core/llm/llm.go`
- `core/stt/fallback_adapter.go`
- `core/tts/fallback_adapter.go`
- `app/app.go`

Configure fallback providers with:

- `RTP_AGENT_LLM_FALLBACK_PROVIDERS`
- `RTP_AGENT_STT_FALLBACK_PROVIDERS`
- `RTP_AGENT_TTS_FALLBACK_PROVIDERS`

Fallback behavior is provider-list based and does not imply all LiveKit fallback recipes are implemented.

