---
id: overview
title: Overview
---

# Agent Server overview

Status: **implemented**.

Evidence:

- `interface/worker/server.go`
- `interface/worker/job.go`
- `interface/worker/ipc`
- `interface/cli/cli.go`

`worker.AgentServer` registers, accepts or rejects jobs, runs job entrypoints, reports status, and supports local job execution for console/dev flows.

