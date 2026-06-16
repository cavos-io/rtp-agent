---
id: cerebras
title: Cerebras
---

# Cerebras

Status: **implemented** for LLM only.

Use Cerebras only for LLM behavior in this repository.

## Source-backed capability

- LLM: `adapter/cerebras/llm.go`

Constructor: `NewCerebrasLLM`.

The adapter wraps an OpenAI-compatible LLM path with Cerebras defaults. It does not implement STT, TTS, realtime, avatar, or VAD capability files.

Evidence:

- `adapter/cerebras/llm.go`
- `adapter/cerebras/llm_test.go`
