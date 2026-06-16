---
id: supervisor-pattern
title: Supervisor pattern
---

# Supervisor pattern

Status: **planned/not implemented** as a named framework pattern.

LiveKit's docs describe supervisor-style orchestration as a way to coordinate specialized agents. `rtp-agent` has pieces that can support handoff behavior, but it does not currently expose a named supervisor framework.

Use the existing primitives instead:

- `AgentSession.UpdateAgent` can switch the active agent.
- `RunResult` records agent handoff events.
- `core/beta/workflows` contains specific workflow helpers.

Do not document a production supervisor pattern until the repository has a source-backed package or example that defines the pattern and tests its behavior.

Evidence:

- `app/app.go` includes a `handoff` workflow entry.
- No source package named supervisor or full supervisor orchestration exists under `core/agent` or `core/beta/workflows`.
