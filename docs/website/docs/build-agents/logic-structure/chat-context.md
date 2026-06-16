---
id: chat-context
title: Chat context
---

# Chat context

Status: **implemented**.

Evidence:

- `core/llm/chat_context.go`
- `core/llm/chat_context_test.go`
- `core/llm/remote_chat_context.go`

`llm.ChatContext` stores conversation items used by LLM and realtime providers. `Agent.ChatContext()` returns a read-only copy, while `Agent.UpdateChatContext` replaces the agent-owned context.

