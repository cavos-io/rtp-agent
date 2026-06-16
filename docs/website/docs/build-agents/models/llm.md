---
id: llm
title: LLM
---

# LLM

Status: **implemented**.

Use an LLM when your agent needs chat generation, tool calls, or text reasoning in the speech pipeline.

Adapters implement `llm.LLM`. In app configuration, select the provider and model with:

```bash
export RTP_AGENT_LLM_PROVIDER="openai"
export RTP_AGENT_LLM_MODEL="gpt-4o-mini"
```

Provider names must match the app-layer provider switch in `app/app.go`. Model names are passed to provider constructors; they are not validated by a universal model catalog in these docs.

## Direct construction

Construct provider adapters directly when you need options that are not exposed by `AppConfig`. For example, provider packages expose constructors such as `openai.NewOpenAILLM`, `google.NewGoogleLLM`, `groq.NewGroqLLM`, and similar source-backed names.

Use the provider page or adapter source before copying a constructor call.

Evidence:

- `core/llm/llm.go`
- `core/llm/llm_test.go`
- `adapter/*/llm.go`
