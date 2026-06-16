---
id: external-data-rag
title: External data and RAG
---

# External data and RAG

Status: **partial**.

Use tools or MCP servers when the agent needs external data.

`rtp-agent` does not provide a dedicated RAG framework package. The source-backed path is to expose retrieval as a tool, then let the LLM call that tool during generation. For external tool servers, use MCP.

## MCP options

The source exposes:

- `llm.NewMCPServerHTTP`
- `llm.NewMCPServerStdio`
- `llm.NewMCPToolset`
- `RTP_AGENT_MCP_STDIO_SERVERS`
- `RTP_AGENT_MCP_HTTP_SERVERS`

Use a dedicated RAG page only after the repository contains source and tests for retrieval-specific indexing, chunking, ranking, or citation behavior.

Evidence:

- `core/llm/mcp.go`
- `app/app.go`
- `core/llm/tool_context.go`
