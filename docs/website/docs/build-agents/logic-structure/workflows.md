---
id: workflows
title: Workflows
---

# Workflows

Status: **partial**.

Use workflows when the agent must guide a caller through a known task, such as collecting contact details or transferring a call.

The source-backed workflow implementation is currently in `core/beta/workflows`. App configuration can select a workflow task with `RTP_AGENT_WORKFLOW_TASK` and related `RTP_AGENT_WORKFLOW_*` variables.

## Implemented workflow helpers

The beta package includes helpers for:

- address
- email address
- phone number
- date of birth
- name
- credit-card fields
- DTMF input
- task groups
- warm transfer

Use these helpers when their tests match your scenario. For new workflow behavior, add source and tests before documenting it as a supported guide.

Evidence:

- `core/beta/workflows`
- `app/app.go`
- `examples/warm-transfer`
