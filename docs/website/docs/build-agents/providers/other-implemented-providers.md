---
id: other-implemented-providers
title: Other implemented providers
---

# Other implemented providers

Status: **implemented or partial by capability file**.

Evidence:

- `adapter/*/llm.go`
- `adapter/*/stt.go`
- `adapter/*/tts.go`
- `adapter/*/realtime.go`
- `adapter/*/avatar.go`
- `adapter/*/vad.go`

Provider capability is determined by source files. See the [provider capability reference](/reference/providers) for the complete table.

Do not infer a capability from package name alone. For example, a package with only `tts.go` is a TTS provider only.
