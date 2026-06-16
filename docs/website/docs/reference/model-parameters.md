---
id: model-parameters
title: Model parameters
---

# Model parameters

Status: **partial**.

Evidence:

- `app/app.go`
- adapter option types under `adapter/*`
- `core/llm/llm.go`

Model parameters are provider-specific. App-level environment variables cover common provider, model, voice, language, base URL, fallback, and extra-parameter fields. For full provider-specific parameters, inspect the adapter constructor options in source.

