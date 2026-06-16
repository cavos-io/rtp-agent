---
id: agent-builder
title: Agent Builder
---

# Agent Builder

Status: **planned/not implemented**.

LiveKit Agent Builder is a browser product surface. `rtp-agent` does not include an equivalent browser-based agent builder in source.

Evidence:

- No builder package exists under `cmd`, `app`, `interface`, `core`, or `docs/website/src`.
- The supported composition path is code-first in `cmd/main.go` and `app/app.go`.

Use Go code and `app.AppConfig` instead.

