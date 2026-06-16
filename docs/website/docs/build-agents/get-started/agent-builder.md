---
id: agent-builder
title: Agent Builder
---

# Agent Builder

Status: **planned/not implemented**.

LiveKit Agent Builder is a browser product surface. This Go repository does not include an equivalent browser-based builder.

Use code-first app composition instead. The practical replacement is:

1. Define the agent instructions and tools in Go.
2. Choose models through `app.AppConfig` or `RTP_AGENT_*` environment variables.
3. Run the app through `interface/cli.RunApp` during development.
4. Add tests or parity cases for behavior that must stay stable.

This page exists so readers coming from the LiveKit Agents docs do not go looking for an unsupported UI.

Evidence:

- No builder package exists under `cmd`, `app`, `interface`, `core`, or `docs/website/src`.
- The supported composition path is code-first in `cmd/main.go` and `app/app.go`.
