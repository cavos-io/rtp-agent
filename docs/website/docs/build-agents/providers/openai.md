---
id: openai
title: OpenAI
---

# OpenAI

Status: **implemented** for LLM, STT, TTS, and realtime.

Use OpenAI when you need one provider package that covers chat generation, speech recognition, speech synthesis, and realtime sessions.

## Source-backed capabilities

- LLM: `adapter/openai/llm.go`
- STT: `adapter/openai/stt.go`
- TTS: `adapter/openai/tts.go`
- Realtime: `adapter/openai/realtime.go`

Constructors include `NewOpenAILLM`, `NewOpenAISTT`, `NewOpenAITTS`, and `NewRealtimeModel`. App configuration uses `RTP_AGENT_*_PROVIDER=openai` with `OPENAI_API_KEY` for the provider credentials.

## Notes

The OpenAI adapter package also contains compatibility constructors for OpenAI-compatible hosts. Use the exact constructor in source for those paths; do not infer that every OpenAI-compatible host supports every OpenAI capability.

Evidence:

- `adapter/openai/llm.go`
- `adapter/openai/stt.go`
- `adapter/openai/tts.go`
- `adapter/openai/realtime.go`
- `adapter/openai/*_test.go`
