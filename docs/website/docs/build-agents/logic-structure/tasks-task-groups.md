---
id: tasks-task-groups
title: Tasks and task groups
---

# Tasks and task groups

Status: **partial**.

Use task groups when your workflow needs to collect a set of structured values instead of relying on one free-form model reply.

The current task-group implementation lives in beta workflow helpers. It is useful for repository-supported workflow experiments, but it should not be treated as full parity with every LiveKit task abstraction.

## Source-backed task types

The beta workflow package includes tasks for address, email address, phone number, date of birth, name, credit-card fields, DTMF input, warm transfer, and task groups.

Configure the app-level task-group path with `RTP_AGENT_WORKFLOW_TASK_GROUP_TASKS`, or compose the workflow helpers directly in Go when you need tighter control.

## Stability boundary

Because these APIs are under `core/beta`, document exact behavior only when source and tests cover the scenario you plan to rely on.

Evidence:

- `core/beta/workflows/task_group.go`
- `core/beta/workflows/task_group_test.go`
- `app/app.go`
