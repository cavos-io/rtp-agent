---
id: overview
title: Overview
---

# Agent Server overview

Status: **implemented**.

Use `worker.AgentServer` as the process-level runtime for agents.

The server owns worker registration, availability responses, job assignment, job entrypoint execution, active-job tracking, drain, and shutdown. Application code typically reaches it through `app.App.Server` and starts it with `interface/cli.RunApp`.

## What the server manages

- worker options and LiveKit credentials
- registered job and participant entrypoints
- assignment and rejection decisions
- job process state and current job context
- drain and shutdown behavior
- local unregistered execution paths for development

Use this section when you need to understand deployment or lifecycle behavior. Use `core/agent` pages when you are changing model/session behavior inside a job.

Evidence:

- `interface/worker/server.go`
- `interface/worker/job.go`
- `interface/worker/ipc`
- `interface/cli/cli.go`
