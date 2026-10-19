# Inference API errors migration implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move generic provider API errors out of `core/llm` while preserving behavior and source compatibility.

**Architecture:** Add `core/inference` as the canonical owner of the existing API error hierarchy. Keep deprecated type aliases and forwarding constructors in `core/llm`, then migrate STT and TTS production code to the canonical package. Do not change status-code interpretation, retry rules, formatting, or stream behavior.

**Tech stack:** Go 1.25, standard library errors, repository architecture checker, Go tests, Lefthook.

**Spec:** `/Users/Vino/Desktop/rtp-agent-api-error-analysis.md`

## Global constraints

- Implement option 1 only: one generic inference API error hierarchy.
- Preserve protocol status values and retry semantics exactly.
- Preserve existing `llm.API*Error` source compatibility.
- Make the smallest coherent change.
- Do not commit or push automatically.

## Review focus

- Legacy `errors.As` checks using `*llm.APIStatusError` must recognize canonical inference errors.
- Canonical `errors.As` checks must recognize errors returned by legacy LLM constructors.
- Explicit retryability overrides must retain current `4xx` enforcement.
- Error strings, bodies, request IDs, and nil receiver behavior must remain unchanged.
- STT/TTS files that still use `llm.APIConnectOptions` must retain the LLM import while using canonical errors.

---

### Task 1: Add canonical inference API errors

**Files:**

- Create: `core/inference/errors.go`
- Create: `core/inference/errors_test.go`

**Interfaces:**

- Produces: `APIError`, `APIStatusError`, `APIConnectionError`, `APITimeoutError` and their existing constructors.
- Preserves: current `Error`, `Unwrap`, formatting, default messages, and retry classification behavior.

- [x] Write table-driven tests for status retryability, explicit overrides, formatting, error-chain relationships, and default messages.
- [x] Run `go test ./core/inference` and confirm failure because package implementation is absent.
- [x] Move the existing implementation into `core/inference/errors.go` without semantic changes.
- [x] Run `go test ./core/inference` and confirm success.

### Task 2: Preserve the LLM compatibility API

**Files:**

- Create: `core/llm/api_errors_compat.go`
- Create: `core/llm/api_errors_compat_test.go`
- Modify: `core/llm/llm.go`

**Interfaces:**

- Consumes: canonical types and constructors from `core/inference`.
- Produces: deprecated aliases and forwarding constructors under all existing `llm.API*` names.

- [x] Write compatibility tests proving bidirectional `errors.As`, field preservation, formatting equality, and constructor compatibility.
- [x] Run focused compatibility tests and confirm failure because aliases do not exist yet.
- [x] Add aliases and forwarding constructors.
- [x] Remove the old duplicate implementation from `core/llm/llm.go`.
- [x] Run `go test ./core/inference ./core/llm` and confirm success.

### Task 3: Migrate STT and TTS production references

**Files:**

- Modify: `core/tts/*.go` where API errors are referenced.
- Modify: `adapter/*/stt.go`, `adapter/google/beta/stt.go`, and `adapter/*/tts.go` where API errors are referenced.
- Modify: affected tests only when needed for canonical package assertions.

**Interfaces:**

- Consumes: canonical `core/inference` API errors.
- Preserves: legacy LLM aliases for LLM adapters and downstream consumers.

- [x] Add or update a core TTS test to assert canonical `inference.APIStatusError` handling for status `499`.
- [x] Run the focused test and confirm it fails before core TTS imports the canonical package.
- [x] Replace STT/TTS error references with `inference.*` without changing control flow.
- [x] Retain `core/llm` imports where non-error symbols such as `APIConnectOptions` remain.
- [x] Run `gofmt` on changed Go files.
- [x] Run `go test ./core/inference ./core/llm ./core/stt ./core/tts ./adapter/...` and confirm success.

### Task 4: Verify architecture and repository behavior

**Files:**

- Create: `/tmp/inference-api-errors-migration-pr.md`

**Interfaces:**

- Consumes: completed migration.
- Produces: verification evidence and a review-ready PR description.

- [x] Review the complete diff for unrelated changes and protocol-status behavior changes.
- [x] Run `go tool go-file-arch -config .architecture.yaml ./...`.
- [x] Run `scripts/go-test-all.sh`.
- [x] Run `go tool lefthook run ci`.
- [x] Write the PR description with summary, testing evidence, compatibility notes, and risk.
- [x] Leave all changes uncommitted for human review.
