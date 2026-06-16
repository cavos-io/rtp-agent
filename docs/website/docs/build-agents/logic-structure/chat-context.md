---
id: chat-context
title: Chat context
---

# Chat context

Status: **implemented**.

Use `llm.ChatContext` to carry model-visible conversation state across turns.

The context can contain messages, tool/function items, config updates, and handoff records. Provider paths use it to build the request sent to an LLM or realtime model.

## Read and update safely

`Agent.ChatContext()` returns a read-only copy so callers can inspect history without mutating the agent's internal state. To replace the agent-owned context, call `Agent.UpdateChatContext` or `Agent.UpdateChatCtx`.

When tools are configured, chat-context copy logic can filter function items against the active tools. That keeps stale or invalid function state from being carried into a new context unless you explicitly opt out.

## When to use it

- seed an agent with prior conversation
- carry tool-call results into the next model turn
- inspect session history in tests
- create parity evidence for conversation behavior

Evidence:

- `core/llm/chat_context.go`
- `core/llm/chat_context_test.go`
- `core/llm/remote_chat_context.go`
- `core/agent/agent_test.go`
