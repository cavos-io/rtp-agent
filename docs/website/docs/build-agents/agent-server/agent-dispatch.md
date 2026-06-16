---
id: agent-dispatch
title: Agent dispatch
---

# Agent dispatch

Status: **partial**.

Use dispatch behavior through the worker server unless you are integrating directly with LiveKit APIs outside this repo.

The worker handles LiveKit availability requests and assignments. It evaluates worker state, accepts or rejects jobs, creates `JobContext`, and runs configured entrypoints. The docs do not currently expose a standalone dispatch service client equivalent to every LiveKit API page.

## What is source-backed

- worker-side availability and assignment handling
- active job tracking
- job context creation
- participant entrypoint registration and replay behavior

For API-level dispatch outside the worker, use LiveKit service APIs directly and document that integration separately.

Evidence:

- `interface/worker/server.go`
- `interface/worker/job.go`
- `interface/worker/server_test.go`
- `interface/worker/job_test.go`
