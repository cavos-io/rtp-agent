---
id: llm
title: LLM
---

# LLM

Status: **implemented**.

Evidence:

- `core/llm/llm.go`
- `core/llm/llm_test.go`
- `adapter/*/llm.go`

LLM adapters implement `llm.LLM`. Use `RTP_AGENT_LLM_PROVIDER` and `RTP_AGENT_LLM_MODEL` through `app.AppConfig`, or construct providers directly.

