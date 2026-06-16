---
id: agent-embed-widget
title: Agent Embed Widget
---

# Agent Embed Widget

Status: **planned/not implemented**.

There is no source-backed web embed widget in `rtp-agent`.

If your product needs a browser or mobile frontend, build it as a separate LiveKit client application and connect it to the room your Go worker joins. The Go runtime handles worker lifecycle and room I/O; it does not ship a reusable embed widget.

Use this page as a boundary marker:

- frontend UI belongs outside this repository today
- room participation from the agent side belongs in `interface/worker`
- user-facing client behavior should be documented only after a real frontend package or example exists

Evidence:

- No frontend widget package exists under `docs/website/src`, `interface`, `core`, or `adapter`.
- Room participation is handled by worker and room I/O packages: `interface/worker/server.go` and `interface/worker/room_io.go`.
