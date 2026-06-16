---
id: fallback-strategies
title: Fallback strategies
---

# Fallback strategies

Status: **implemented** for LLM, STT, and TTS fallback adapters.

Use fallback providers when a model call can be retried through another configured provider.

Fallback behavior is provider-list based. Configure a primary provider, then provide ordered fallback providers for the same capability. The app layer constructs fallback adapters for supported LLM, STT, and TTS paths.

## Environment variables

```bash
export RTP_AGENT_LLM_FALLBACK_PROVIDERS="openai,groq"
export RTP_AGENT_STT_FALLBACK_PROVIDERS="deepgram,assemblyai"
export RTP_AGENT_TTS_FALLBACK_PROVIDERS="cartesia,elevenlabs"
```

Provider names must match names supported by `app.AppConfig` construction. A fallback entry does not make an adapter support a capability it lacks.

## What fallback does not cover

This page does not claim every LiveKit fallback recipe is implemented. Provider-specific retries, circuit breakers, and business-level fallback behavior should be documented only when source and tests cover them.

Evidence:

- `core/llm/llm.go`
- `core/llm/fallback_adapter_test.go`
- `core/stt/fallback_adapter.go`
- `core/tts/fallback_adapter.go`
- `app/app.go`
