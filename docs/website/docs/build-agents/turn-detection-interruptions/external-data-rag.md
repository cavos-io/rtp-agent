---
id: external-data-rag
title: External data and RAG
---

# External data and RAG

Status: **partial**.

Evidence:

- `core/llm/mcp.go`
- `app/app.go`
- `core/llm/tool_context.go`

The source supports external tools and MCP servers:

- `llm.NewMCPServerHTTP`
- `llm.NewMCPServerStdio`
- `llm.NewMCPToolset`
- `RTP_AGENT_MCP_STDIO_SERVERS`
- `RTP_AGENT_MCP_HTTP_SERVERS`

There is no dedicated RAG framework package in this repository.

