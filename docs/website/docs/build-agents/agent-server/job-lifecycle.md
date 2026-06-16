---
id: job-lifecycle
title: Job lifecycle
---

# Job lifecycle

Status: **implemented**.

Evidence:

- `interface/worker/job.go`
- `interface/worker/server.go`
- `interface/worker/ipc/executor.go`

Jobs are accepted, assigned, executed, terminated, and finished through `JobContext`, `JobProcess`, and `AgentServer` lifecycle methods.

