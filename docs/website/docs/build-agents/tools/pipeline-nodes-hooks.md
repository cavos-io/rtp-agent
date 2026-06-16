---
id: pipeline-nodes-hooks
title: Pipeline nodes and hooks
---

# Pipeline nodes and hooks

Status: **intentionally different**.

Use Go tools and session events instead of LiveKit's exact pipeline-node API.

In `rtp-agent`, a callable tool implements the `llm.Tool` interface. The model path can execute those tools during generation, and `core/agent/events.go` exposes events for tool execution, speech, interruption, and run context behavior.

## What to use

- Add tools to `Agent.Tools`.
- Use `Agent.UpdateTools` when tools must change at runtime.
- Use `llm.ToolFlagger` when a tool needs supported flags such as `ToolFlagIgnoreOnEnter` or `ToolFlagCancellable`.
- Use `llm.ToolDuplicateModer` when duplicate tool calls need reject, replace, or confirm behavior.
- Use MCP servers when tools come from an external process or HTTP endpoint.

This is not a node graph API. Keep custom pipeline behavior in ordinary Go code unless a future source package introduces named pipeline nodes.

Evidence:

- `core/agent/generation.go`
- `core/agent/agent.go`
- `core/agent/events.go`
- `core/llm/llm.go`
- `core/llm/mcp.go`
