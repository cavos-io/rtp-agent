---
id: overview
title: Build Agents
---

# Build Agents

Status: **partial**.

`rtp-agent` implements a Go-first agent runtime with app composition, agent/session orchestration, tools, model providers, worker lifecycle, room I/O, and parity-oriented tests. It does not implement every LiveKit Agents product surface.

Evidence:

- `app/app.go`
- `core/agent/agent.go`
- `core/agent/agent_session.go`
- `interface/worker/server.go`
- `interface/worker/room_io.go`
- `scripts/parity-fixtures/test-cases.tsv`

Use this section to build a Go agent from source-backed APIs. Pages that mirror LiveKit-only product surfaces are marked as planned/not implemented or intentionally different.

