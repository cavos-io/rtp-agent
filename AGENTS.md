# AGENTS.md

## Primary Mission

This repository is a Go implementation effort for LiveKit Agents-style runtime behavior.

Use `refs/agents/livekit-agents` as the behavioral reference and mirror its useful functionality in Go incrementally.

Parity is now close enough that the priority is no longer symbol coverage. The priority is **functionality mirroring**: prove that the Python reference and Go implementation behave identically for the same scenarios.

Read:

* `README.md` for project purpose, setup, and CLI usage.
* `ARCHITECTURE.md` for layer boundaries and dependency direction.

## Reference-To-Go Map

Use this map when deciding where reference behavior belongs:

| LiveKit Python reference                  | Go destination                                 |
| ----------------------------------------- | ---------------------------------------------- |
| `livekit/agents/worker.py`, `job.py`      | `interface/worker`                             |
| `livekit/agents/ipc/*`                    | `interface/worker/ipc`                         |
| `livekit/agents/cli/*`                    | `interface/cli`                                |
| `livekit/agents/voice/agent.py`           | `core/agent/agent.go`                          |
| `livekit/agents/voice/agent_session.py`   | `core/agent/agent_session.go`                  |
| `livekit/agents/voice/agent_activity.py`  | `core/agent/agent_activity.go`                 |
| `livekit/agents/voice/generation.py`      | `core/agent/generation.go`                     |
| `livekit/agents/voice/room_io/*`          | `interface/worker/room_io.go`                  |
| `livekit/agents/voice/recorder_io/*`      | `interface/worker/recorder_io.go`              |
| `livekit/agents/voice/transcription/*`    | `core/agent/transcription.go`                  |
| `livekit/agents/voice/avatar/*`           | `core/agent/avatar.go` plus provider adapters  |
| `livekit/agents/voice/ivr/*`              | `core/agent/ivr.go` and `core/beta/tools`      |
| `livekit/agents/llm/*`                    | `core/llm`                                     |
| `livekit/agents/stt/*`                    | `core/stt`                                     |
| `livekit/agents/tts/*`                    | `core/tts`                                     |
| `livekit/agents/vad.py`                   | `core/vad`                                     |
| `livekit/agents/inference/*`              | `core/inference` for compatibility shims       |
| `livekit/agents/evals/*`                  | `core/evals`                                   |
| `livekit/agents/metrics/*`, `telemetry/*` | `library/telemetry`                            |
| `livekit/agents/tokenize/*`               | `library/tokenize`                             |
| `livekit/agents/utils/*`                  | `library/utils` or a narrower existing package |
| `livekit-plugins/*`                       | `adapter/<provider>`                           |

## Porting Rules

* Start from the Python reference behavior, then implement or adjust the Go behavior in the mapped package.
* Preserve lifecycle semantics where practical: worker, job context, agent, session, activity, tools, streaming LLM/STT/TTS, VAD, interruption, room I/O, telemetry, and provider boundaries.
* Keep `core` provider-agnostic. Provider-specific API details belong in `adapter/<provider>`.
* Keep LiveKit transport, room connection, and worker protocol concerns in `interface/worker`.
* Keep CLI parsing and local developer commands in `interface/cli`.
* Prefer small Go interfaces around stable behavior over direct translations of Python inheritance patterns.
* Do not add cross-layer imports that violate `.go-arch-lint.yml`.
* Do not edit `refs/agents/*` except when explicitly updating vendored reference material.
* Do not leave new deadcode behind. New functionality must be wired into real product flow, tests, registry, factory, config, interface, or composition roots.

## Functionality Mirroring Workflow

For each parity task:

1. Inspect the matching Python reference under `refs/agents/livekit-agents`.
2. Inspect the existing Go package and tests.
3. Identify the behavior to mirror, not only the symbol or file to port.
4. Create or update a shared parity scenario when practical.
5. Run the Python reference and Go implementation for the same scenario.
6. Compare normalized outputs, state transitions, events, errors, or JSON traces.
7. Debug both sides until the expected behavior is understood and the Go behavior matches the Python reference.
8. Add or update Go tests and parity manifest cases.
9. Ensure new code is wired into real flow and does not appear in `deadcode`.
10. Commit only after validation passes.

Before implementation, briefly state:

* selected behavior gap
* Python reference files
* Go target files/packages
* expected reference behavior
* Python run command or runner
* Go run command or test
* comparison contract
* validation plan

## Required Validation Mindset

Name-based matching is not parity. A Go symbol existing with the same name as a Python symbol is only a candidate.

Parity must be proven with one or more of:

* cross-runtime parity cases
* shared manifest scenarios
* Python runner output compared with Go runner output
* Go tests that explicitly encode the Python reference behavior
* documented review evidence for behavior that cannot yet be executed automatically

For important behavior, prefer cross-runtime execution:

```sh
scripts/parity-validate.sh
```

When adding a behavior case, prefer adding a row to the shared manifest under `scripts/parity-fixtures/` rather than creating one-off runners.

The manifest schema is:

```text
case_name, type, source_ref, target_ref, go_package, go_test,
python_runner, go_runner, input_json, contract, behavior, notes
```

Use `cross-runtime` cases when both sides can actually run.

A valid cross-runtime case must:

* run the Python reference
* run the Go target
* use the same input scenario
* emit normalized JSON, trace, or contract output
* compare behavior rather than raw stdout
* normalize unstable fields such as timestamps, paths, UUIDs, random IDs, durations, and nondeterministic ordering

Do not add fake cross-runtime cases. If a case cannot execute both Python and Go, keep it as `go-test` and clearly state what behavior the Go test encodes from the reference.

## Debugging Python and Go Together

When behavior differs:

1. Reproduce the Python reference behavior first.
2. Reproduce the Go behavior second.
3. Capture both outputs in comparable form.
4. Identify whether the difference is:

   * missing Go behavior
   * intentionally different Go design
   * reference behavior not applicable to this project
   * test/runner mismatch
   * unstable output normalization issue
5. Fix the Go implementation or runner as appropriate.
6. Re-run both sides.
7. Only claim parity when both sides match the stated contract.

Do not change tests to match broken behavior. Update tests only when they better encode the reference contract.

## Verification Commands

Use the narrowest useful verification first, then broaden.

Common commands:

```sh
scripts/go-test-all.sh
scripts/go-build-all.sh
go-arch-lint check
go-arch-lint mapping
scripts/parity-validate.sh
scripts/parity-gate.sh
```

Use static and deadcode checks as quality gates:

```sh
staticcheck ./... > staticcheck.txt
deadcode ./... > deadcode.txt
```

Rules:

* Fix staticcheck/deadcode caused by new or touched code before committing.
* Do not commit `staticcheck.txt` or `deadcode.txt` unless explicitly required.
* Do not create fake references to silence deadcode.
* Prefer wiring intended functionality into real flows.
* Remove code only when clearly obsolete, duplicated, or unintended.

Optional parity discovery command:

```sh
scripts/parity-check.sh \
  --source-dir refs/agents/livekit-agents \
  --target-dir . \
  --source-lang python \
  --target-lang go \
  --output .tmp/parity_report.csv
```

Use this as directional guidance only. It does not prove behavior.

## Quality Gates

Use available gates to prevent fake progress:

```sh
scripts/check-test-integrity.sh
scripts/check-deadcode.sh
scripts/parity-gate.sh
```

These gates protect against:

* weakened tests
* unused parity code
* inert ports
* false confidence from symbol-only matching

They do not replace behavior validation.

## Testing Rules

* Do not delete, skip, weaken, or rewrite tests just to pass.
* Do not add `t.Skip`, `t.Skipf`, `SkipNow`, or `testing.Short` guards.
* Do not remove meaningful assertions.
* Do not add meaningless tests only to increase coverage.
* Do not hide failures by changing tests to match broken behavior.
* Add tests near the package being changed.
* Prefer table-driven tests when behavior has multiple scenarios.
* For parity-sensitive behavior, add or update manifest cases.

## Commit Rules

Keep commits small and focused.

Each commit should represent one coherent functionality-mirroring improvement.

Suggested commit types:

* `feat(core): mirror <reference behavior>`
* `feat(adapter): mirror <provider behavior>`
* `fix(core): align <behavior> with reference`
* `fix(adapter): align <provider> behavior with reference`
* `test(core): add cross-runtime parity for <behavior>`
* `test(parity): add manifest case for <behavior>`
* `refactor(core): wire <component> into <flow>`

Before committing:

1. Run relevant Python reference runner or parity case.
2. Run relevant Go test or runner.
3. Run broader Go validation.
4. Run staticcheck/deadcode gates when touched code may affect wiring.
5. Confirm no new deadcode remains.
6. Confirm `staticcheck.txt` and `deadcode.txt` are not staged unless intentionally tracked.

## Current Known Drift

Several LiveKit reference areas already have partial Go equivalents. Treat existing Go code as the starting point.

Do not re-port from scratch when Go behavior already exists. Instead, compare behavior, identify gaps, and adjust incrementally.

## Documentation Hygiene

Keep this file focused on instructions for coding agents.

Put user-facing setup and feature documentation in `README.md` or docs.

Put architecture policy in `ARCHITECTURE.md`.
