---
id: groq
title: Groq
---

# Groq

Status: **implemented** for LLM, STT, and TTS.

Evidence:

- `adapter/groq/llm.go`
- `adapter/groq/stt.go`
- `adapter/groq/tts.go`
- `adapter/groq/*_test.go`

Constructors include `NewGroqLLM`, `NewGroqSTT`, and `NewGroqTTS`. App configuration reads `GROQ_API_KEY`.

