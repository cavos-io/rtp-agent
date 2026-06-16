---
id: groq
title: Groq
---

# Groq

Status: **implemented** for LLM, STT, and TTS.

Use Groq when the agent should route chat, speech recognition, or speech synthesis through Groq-backed OpenAI-compatible paths.

## Source-backed capabilities

- LLM: `adapter/groq/llm.go`
- STT: `adapter/groq/stt.go`
- TTS: `adapter/groq/tts.go`

Constructors include `NewGroqLLM`, `NewGroqSTT`, and `NewGroqTTS`. App configuration reads `GROQ_API_KEY`.

Provider options such as base URL, reasoning effort, and STT/TTS options belong to the adapter source and tests. Do not infer realtime support; there is no `adapter/groq/realtime.go`.

Evidence:

- `adapter/groq/llm.go`
- `adapter/groq/stt.go`
- `adapter/groq/tts.go`
- `adapter/groq/*_test.go`
