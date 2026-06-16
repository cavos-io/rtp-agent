---
id: model-parameters
title: Model parameters
---

# Model parameters

Status: **partial**.

Use this page to find where model parameters are configured.

`rtp-agent` does not maintain a universal model parameter catalog. Parameters are split between app-level configuration, core option structs, and provider-specific adapter options.

## App-level groups

| Group | Examples |
|---|---|
| LLM | provider, model, base URL, extra headers, extra JSON body, fallback providers, response format, parallel tool calls |
| STT | provider, model, language, endpointing, timestamps, diarization, keywords, translation, base URL, provider-specific model options |
| TTS | provider, model, voice, language, speed, sample rate, text replacements, transforms, stream pacing, provider-specific JSON/model options |
| Realtime | realtime provider and model |
| Avatar | avatar provider and provider-specific IDs/options |
| VAD/turn detection | VAD provider/options and turn detector provider |

## Where to inspect exact fields

- `app.AppConfig` lists app-level fields.
- `app.DefaultConfigFromEnv()` maps environment variables to those fields.
- Adapter option types under `adapter/<provider>` define provider-specific constructor options.
- `core/llm.ChatOptions`, `core/stt`, and `core/tts` define shared core options.

Do not copy parameter names from LiveKit Python/Node docs unless the same option exists in Go source.

Evidence:

- `app/app.go`
- adapter option types under `adapter/*`
- `core/llm/llm.go`
- `core/stt/stt.go`
- `core/tts/tts.go`
