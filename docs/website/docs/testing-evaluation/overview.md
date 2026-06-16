---
id: overview
title: Testing and evaluation
---

# Testing and evaluation

Use Go tests for package behavior, and use the parity gate for behavior that is intended to match LiveKit Agents reference behavior.

## Go tests

```bash
go test ./...
```

The repository also includes wrapper scripts:

```bash
scripts/go-test-all.sh
scripts/go-build-all.sh
```

## Parity validation

The canonical manifest is:

```text
scripts/parity-fixtures/test-cases.tsv
```

Run the full parity-sensitive gate:

```bash
scripts/parity-gate.sh
```

Run a focused case:

```bash
scripts/parity-gate.sh --case <case-name>
```

Run the fast changed-file loop:

```bash
scripts/parity-gate.sh --local
```

`--local` is useful during editing but is not final validation. It maps changed files to manifest cases and skips the staged dead-code/analyzer checks.

## Evaluation

`app.App` exposes `EvaluateSession`, which is used by the checked-in examples to return an `EvaluationSummary` with score and pass/fail aggregate fields.

