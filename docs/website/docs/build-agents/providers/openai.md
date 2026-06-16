---
id: openai
title: OpenAI
---

# OpenAI

Status: **implemented** for LLM, STT, TTS, and realtime.

Evidence:

- `adapter/openai/llm.go`
- `adapter/openai/stt.go`
- `adapter/openai/tts.go`
- `adapter/openai/realtime.go`
- `adapter/openai/*_test.go`

Constructors include `NewOpenAILLM`, `NewOpenAISTT`, `NewOpenAITTS`, and `NewRealtimeModel`. App configuration uses `RTP_AGENT_*_PROVIDER=openai` and `OPENAI_API_KEY`.

