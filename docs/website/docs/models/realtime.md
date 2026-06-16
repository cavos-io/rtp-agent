---
id: realtime
title: Realtime
---

# Realtime

Realtime model support is represented by `llm.RealtimeModel`.

Source-backed realtime adapters at `v0.0.67`:

- `adapter/openai/realtime.go`
- `adapter/phonic/realtime.go`

Example:

```go
model := openai.NewRealtimeModel(apiKey, "gpt-realtime")
```

When a realtime model is configured through `AppConfig`, `app.NewApp` assigns it to the session and creates a multimodal assistant with `agent.NewMultimodalAgent`.

