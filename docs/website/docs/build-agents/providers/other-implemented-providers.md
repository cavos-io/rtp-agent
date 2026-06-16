---
id: other-implemented-providers
title: Other implemented providers
---

# Other implemented providers

Status: **implemented or partial by capability file**.

Use this page when a provider is implemented but does not have a dedicated spotlight page.

Provider capability is determined by source files, not package name. A package with only `tts.go` is a TTS provider only. A package with `llm.go`, `stt.go`, and `tts.go` can be documented for those three capabilities, but not realtime or avatar unless the matching file exists.

## Capability files

- `llm.go`: LLM provider
- `stt.go`: speech-to-text provider
- `tts.go`: text-to-speech provider
- `realtime.go`: realtime model provider
- `avatar.go`: virtual avatar provider
- `vad.go`: voice activity detection provider

See the [provider capability reference](/reference/providers) for the complete table.

When adding a new provider page, start from adapter source and tests, then document constructors, credential environment variables, and app-layer provider names only if they are present in source.

Evidence:

- `adapter/*/llm.go`
- `adapter/*/stt.go`
- `adapter/*/tts.go`
- `adapter/*/realtime.go`
- `adapter/*/avatar.go`
- `adapter/*/vad.go`
