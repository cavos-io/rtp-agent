---
id: google
title: Google
---

# Google

Status: **implemented** for LLM, STT, and TTS.

Use Google when you need Gemini-backed LLM behavior or Google speech services through the source-backed adapter package.

## Source-backed capabilities

- LLM: `adapter/google/llm.go`
- STT: `adapter/google/stt.go`
- TTS: `adapter/google/tts.go`

Constructors include `NewGoogleLLM`, `NewGoogleSTT`, and `NewGoogleTTS`.

## Credentials

The LLM path reads API-key style configuration. STT and TTS use Google credentials file configuration through constructor arguments or app configuration such as `RTP_AGENT_GOOGLE_CREDENTIALS_FILE` and `GOOGLE_APPLICATION_CREDENTIALS`.

Do not document Google realtime or avatar support unless matching source files are added.

Evidence:

- `adapter/google/llm.go`
- `adapter/google/stt.go`
- `adapter/google/tts.go`
- `adapter/google/*_test.go`
