---
id: overview
title: Manage and deploy
---

# Manage and deploy

Status: **partial**.

Evidence:

- `cmd/main.go`
- `app/app.go`
- `interface/cli/cli.go`
- `interface/worker/server.go`

Deploy a Go binary that creates `app.App`, configures `worker.WorkerOptions`, and runs `cli.RunApp`. Platform-specific deployment recipes are deferred until backed by source or CI.

