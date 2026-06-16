---
id: google
title: Google
---

# Google

Status: **implemented** for LLM, STT, and TTS.

Evidence:

- `adapter/google/llm.go`
- `adapter/google/stt.go`
- `adapter/google/tts.go`
- `adapter/google/*_test.go`

Constructors include `NewGoogleLLM`, `NewGoogleSTT`, and `NewGoogleTTS`. Credentials are read from `GOOGLE_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`, or `RTP_AGENT_GOOGLE_CREDENTIALS_FILE` depending on provider path.

