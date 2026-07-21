---
name: rtp-agent-parity
description: Use when a request explicitly involves parity or behavior mirroring against LiveKit Agents or plugins, Pipecat or Smart Turn, TEN Framework or TEN VAD, or their reference roots under refs/.
---

# RTP Agent parity

## Objective

Establish behavioral parity for a selected reference contract. Matching names, files, or public symbols is discovery evidence, not parity evidence.

## Required context

Before parity work:

1. Read `DEVELOPMENT.md` completely.
2. Read `ARCHITECTURE.md` and inspect `.architecture.yaml` when package boundaries or adapter contracts are relevant.
3. Inspect the matching source under an approved root: `refs/agents/livekit-agents`, `refs/agents/livekit-plugins`, `refs/pipecat`, `refs/smart-turn`, `refs/ten-framework`, or `refs/ten-vad`.
4. Inspect the existing Go implementation and tests. Extend existing behavior incrementally; do not re-port it from scratch.

Do not edit `refs/*` unless the user explicitly requests a reference update.

## Workflow

1. State the behavior gap, authoritative reference file, any secondary references, Go target, expected behavior, comparison contract, and validation plan.
2. Reproduce or trace the reference behavior before deciding how Go should behave.
3. Compare observable results: events, state transitions, errors, tool calls, stream boundaries, cancellation, or normalized traces.
4. Classify any difference before acting: missing Go behavior, intentional Go behavior, inapplicable reference behavior, reference-version conflict, runner or fixture error, or normalization error.
5. For analysis or review requests, stop at evidence-backed findings. For implementation requests, make the smallest architecture-compliant change in the mapped Go package.
6. When code changes, add a deterministic Go test. Add or update a shared parity scenario for the selected reference contract when both runtimes can execute it.
7. Run the focused comparison, focused Go tests, architecture check when applicable, and broader gates proportional to the change.
8. Report the evidence, case type, commands run, and remaining drift. Do not claim parity beyond the executed contract.

## Evidence levels

Prefer, in order: a `cross-runtime` or `json-scenario` comparison, a Go test encoding observed behavior, then documented review evidence. Symbol reports are discovery only.

Normalize unstable timestamps, durations, paths, UUIDs, generated IDs, and nondeterministic ordering before comparison. If both runtimes cannot execute, keep the case classified as `go-test`; never create a fake cross-runtime case.

The shared manifest contains other reference ecosystems. Keep this skill within the approved roots above; do not infer that every manifest row belongs to this workflow.

## Completion gate

Before declaring the task complete:

- For code changes, confirm the behavior is wired into an intended runtime, composition, provider, registry, configuration, or meaningful test path.
- For streaming-sensitive code changes, cover relevant ordering, cancellation, close/error, partial-output, finalization, and leak risks.
- For test changes, confirm default tests do not require paid provider credentials or external network access.
- Run selected commands; confirm staged-only guards included the touched files.
- Identify limitations explicitly.

## Common mistakes

- Do not claim parity from symbols or names.
- Do not blend references; select the source that owns the behavior.
- Do not weaken tests, rewrite working subsystems, or add inert wiring to satisfy a gate.
