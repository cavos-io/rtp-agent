---
id: overview
title: Models
---

# Models

Models are split by capability:

- `core/llm.LLM`
- `core/stt.STT`
- `core/tts.TTS`
- `llm.RealtimeModel`
- `agent.AvatarProvider`
- `vad.VAD`

App-level provider selection is string-based through `AppConfig`:

```go
cfg := app.DefaultConfigFromEnv()
cfg.LLMProvider = "openai"
cfg.LLMModel = "gpt-4.1-mini"
cfg.STTProvider = "deepgram"
cfg.STTModel = "nova-3"
cfg.TTSProvider = "openai"
cfg.TTSModel = "gpt-4o-mini-tts"
cfg.TTSVoice = "alloy"

rtpApp, err := app.Init(cfg)
```

Direct provider constructors are documented in the provider capability reference.

