---
id: overview
title: Overview
---

# Testing and evaluation overview

Status: **implemented** for Go tests, parity manifest cases, and app evaluation summaries.

Use repository-native tests for implementation behavior and parity scripts for behavior that must track LiveKit Agents.

There are three common validation layers:

- Go unit tests near the package being changed.
- Manifest-backed parity checks in `scripts/parity-fixtures/test-cases.tsv`.
- App evaluation summaries through `core/evals` and `App.EvaluateSession`.

For docs work, the most useful checks are Docusaurus build/typecheck plus focused Go package tests for APIs shown in examples. For runtime behavior changes, use the parity gate rules in the repository root instructions.

## When to use evaluation

Use `App.EvaluateSession` when an app has configured evaluators and you need a score summary for a session. The basic agent exposes that path through the CLI eval callback in `examples/voice_agents/basic_agent/main.go`.

Evidence:

- `core/evals/evaluation.go`
- `app/app.go`
- `scripts/parity-fixtures/test-cases.tsv`
- `scripts/parity-gate.sh`
