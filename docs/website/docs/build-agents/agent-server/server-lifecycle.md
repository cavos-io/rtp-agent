---
id: server-lifecycle
title: Server lifecycle
---

# Server lifecycle

Status: **implemented**.

Evidence:

- `interface/worker/server.go`
- `interface/worker/server_test.go`

The server resolves options, connects to worker transport, reports availability, handles drain/shutdown, and manages active jobs.

