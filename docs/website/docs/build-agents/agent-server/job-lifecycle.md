---
id: job-lifecycle
title: Job lifecycle
---

# Job lifecycle

Status: **implemented**.

Use `JobContext` to access information and helpers for the job currently running inside a worker entrypoint.

The worker creates a job context when a job is assigned. The context carries the LiveKit job, connection settings, logging/report metadata, shutdown callbacks, participant entrypoints, room connection helpers, and process state.

## Important behaviors

- `GetJobContext` and `RequireJobContext` expose the active entrypoint context.
- `JobContext.Shutdown` runs registered shutdown callbacks once.
- `JobContext.Connect` connects the job to the LiveKit room when needed.
- Participant entrypoints can be registered and filtered by participant kind.
- Session reports can be generated from the primary registered agent session.

Use job lifecycle APIs inside worker entrypoints, not as global application state.

Evidence:

- `interface/worker/job.go`
- `interface/worker/server.go`
- `interface/worker/ipc/executor.go`
- `interface/worker/job_test.go`
