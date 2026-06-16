---
id: dispatch-api
title: Dispatch API
---

# Dispatch API

Status: **partial**.

Evidence:

- `interface/worker/server.go`
- `interface/worker/job.go`
- `interface/worker/ipc/proto.go`

The worker handles availability, assignment, migration, reload, and termination messages. This page documents the internal Go worker dispatch behavior, not a full external dispatch service API.

