---
id: overview
title: Tools
---

# Tools

`core/llm` defines the tool contract used by agents and model adapters. Tools are ordinary Go values and are attached to agents with `Agent.Tools`.

The runtime also includes:

- `core/llm.NewToolContext` for tool context helpers.
- `core/agent` tool execution options.
- `core/beta/tools` for built-in phone/session tools such as ending a call or sending DTMF.
- MCP support through `core/llm.NewMCPServerHTTP`, `core/llm.NewMCPServerStdio`, and `core/llm.NewMCPToolset`.

Configure MCP servers at app level with `RTP_AGENT_MCP_STDIO_SERVERS` or `RTP_AGENT_MCP_HTTP_SERVERS`. These variables contain JSON configuration consumed by `app.DefaultConfigFromEnv`.

