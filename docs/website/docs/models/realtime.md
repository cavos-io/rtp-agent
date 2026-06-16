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
package main

import "github.com/cavos-io/rtp-agent/adapter/openai"

func configureRealtime(apiKey string) {
	model := openai.NewRealtimeModel(apiKey, "gpt-realtime")
	_ = model
}
```

When a realtime model is configured through `AppConfig`, `app.NewApp` assigns it to the session and creates a multimodal assistant with `agent.NewMultimodalAgent`.
