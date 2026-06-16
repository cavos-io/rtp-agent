---
id: xai
title: xAI
---

# xAI

Status: **implemented** for LLM, STT, and TTS.

Use xAI when the selected app configuration or direct constructor should route LLM, STT, or TTS behavior through the xAI adapter package.

## Source-backed capabilities

- LLM: `adapter/xai/llm.go`
- STT: `adapter/xai/stt.go`
- TTS: `adapter/xai/tts.go`

Constructors include `NewXaiLLM`, `NewXaiSTT`, and `NewXaiTTS`. App configuration reads `XAI_API_KEY` for provider credentials.

The adapter does not currently expose realtime or avatar capability files.

Evidence:

- `adapter/xai/llm.go`
- `adapter/xai/stt.go`
- `adapter/xai/tts.go`
- `adapter/xai/*_test.go`
