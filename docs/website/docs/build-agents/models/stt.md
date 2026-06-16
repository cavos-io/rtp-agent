---
id: stt
title: STT
---

# STT

Status: **implemented**.

Use STT when user audio must become text events for the pipeline.

Adapters implement `stt.STT` and emit speech events such as start of speech, interim transcript, final transcript, usage, and end of speech.

Configure app-level STT with:

```bash
export RTP_AGENT_STT_PROVIDER="deepgram"
export RTP_AGENT_STT_MODEL="nova-3"
export RTP_AGENT_STT_LANGUAGE="multi"
```

Provider-specific options vary widely. `app.DefaultConfigFromEnv()` exposes many `RTP_AGENT_STT_*` fields, but an option only matters when the selected adapter consumes it.

Evidence:

- `core/stt/stt.go`
- `core/stt/stt_test.go`
- `adapter/*/stt.go`
- `app/app.go`
