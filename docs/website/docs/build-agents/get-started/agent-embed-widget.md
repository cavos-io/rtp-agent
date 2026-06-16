---
id: agent-embed-widget
title: Agent Embed Widget
---

# Agent Embed Widget

Status: **planned/not implemented**.

There is no source-backed web embed widget in `rtp-agent`.

Evidence:

- No frontend widget package exists under `docs/website/src`, `interface`, `core`, or `adapter`.
- Room participation is handled by worker and room I/O packages: `interface/worker/server.go` and `interface/worker/room_io.go`.

Use an external LiveKit frontend or application-specific client with the worker runtime.

