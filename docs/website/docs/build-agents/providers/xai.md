---
id: xai
title: xAI
---

# xAI

Status: **implemented** for LLM, STT, and TTS.

Evidence:

- `adapter/xai/llm.go`
- `adapter/xai/stt.go`
- `adapter/xai/tts.go`
- `adapter/xai/*_test.go`

Constructors include `NewXaiLLM`, `NewXaiSTT`, and `NewXaiTTS`. App configuration reads `XAI_API_KEY`.

