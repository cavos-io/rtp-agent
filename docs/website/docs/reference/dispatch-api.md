---
id: dispatch-api
title: Dispatch API
---

# Dispatch API

Status: **partial**.

Use this page to distinguish internal worker dispatch behavior from an external LiveKit dispatch service API.

`rtp-agent` documents the Go worker side here. The worker receives availability and assignment messages, creates job contexts, runs entrypoints, handles reload/migration/termination paths, and tracks active jobs. A complete external dispatch service client reference is not implemented in these docs.

## Internal surfaces

| Surface | Purpose |
|---|---|
| `worker.AgentServer` | Owns worker registration, assignment handling, active jobs, drain, and shutdown. |
| `worker.JobContext` | Holds job metadata, LiveKit connection settings, participant entrypoints, report metadata, and shutdown callbacks. |
| `worker.JobProcess` | Tracks executor/process state for a job. |
| `interface/worker/ipc` | Defines process IPC messages for executor paths. |

## Reader guidance

Use `interface/worker` APIs when you are writing or testing the Go worker. Use LiveKit service APIs outside this repository for product-level dispatch clients until this repo has a source-backed wrapper.

Evidence:

- `interface/worker/server.go`
- `interface/worker/job.go`
- `interface/worker/ipc/proto.go`
- `interface/worker/server_test.go`
