---
id: agent-dispatch
title: Agent dispatch
---

# Agent dispatch

Status: **partial**.

Evidence:

- `interface/worker/server.go`
- `interface/worker/job.go`
- `interface/worker/server_test.go`

The worker handles LiveKit availability requests and assignments. It does not expose a separate documented dispatch service client surface equivalent to every LiveKit API page.

