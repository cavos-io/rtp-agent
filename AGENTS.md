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

## Test Integrity Guard

`check-test-integrity.sh` only checks staged test changes.

It blocks:

- deleting `*_test.go`
- staged test files where additions are not greater than deletions
- newly added `t.Skip`, `t.Skipf`, `SkipNow`
- new `testing.Short()` guards
- suspicious always-true assertions
- constant `if true` or `if false` conditions

It warns on new equality assertions because they can hide self-comparison bugs.

Important limitation:

If nothing is staged, `check-test-integrity.sh` does not protect unstaged test changes.

## Deadcode and Analyzer Guard

`check-deadcode.sh` operates from staged Go files.

It:

- collects staged `*.go`
- requires `staticcheck` and `deadcode`
- runs:

```sh
staticcheck ./...
deadcode -test ./...
```

It only blocks findings whose output lines reference staged Go files.

This is intentional. It avoids blocking on existing repo-wide analyzer debt while catching new or touched issues.

## Supporting Scripts

### `scripts/parity-check.sh`

Generic symbol scanner.

It can compare Python and Go trees and emit CSV candidates.

Use it for discovery only. It does not prove behavior.

### `scripts/parity-test-inventory.sh`

Finds Go tests not represented in the TSV manifest.

Classifies missing tests as:

- `reference-parity`
- `target-regression`
- `infrastructure`
- `implementation-detail`
- `unknown`

Use it as an inventory aid only.

### `scripts/go-test-all.sh`

Simple full Go test wrapper:

```sh
go test ./...
```

Uses local `.tmp` cache/temp dirs.

### `scripts/go-build-all.sh`

Simple full build wrapper:

```sh
go build ./...
```

## Script Self-Tests

These test the gate tooling itself:

- `scripts/test-parity-validate.sh`
- `scripts/test-parity-gate.sh`
- `scripts/test-parity-check.sh`

They create temporary manifests and fixtures, then verify:

- manifest schema enforcement
- tab-field rejection
- `--list`
- `--case`
- batched `go-test`
- real `cross-runtime`
- changed-file case selection
- symbol mapping behavior

These are not automatically invoked by `parity-gate.sh`.

Run them when changing parity scripts, manifest parsing, normalization, case selection, batching, or runner semantics.

## Testing Rules

- Do not delete, skip, weaken, or rewrite tests just to pass.
- Do not add `t.Skip`, `t.Skipf`, `SkipNow`, or `testing.Short` guards.
- Do not remove meaningful assertions.
- Do not add meaningless tests only to increase coverage.
- Do not hide failures by changing tests to match broken behavior.
- Add tests near the package being changed.
- Prefer table-driven tests when behavior has multiple scenarios.
- For parity-sensitive behavior, add or update TSV manifest rows.
- Keep JSON input embedded in the TSV `input_json` field.
- For contract-sensitive behavior, document the contract in the TSV row or test name.
- For runtime dependency behavior, add or update integration tests.
- Prefer deterministic fixtures over sleeps.
- Use explicit deadlines and cancellation in async/concurrent tests.
- Avoid network calls to external paid services in default tests.
- Mock provider APIs at the adapter boundary unless a real integration test is explicitly required.
- Keep fast tests fast.
- Keep integration tests clearly labeled and runnable separately.

## Dead Code and Inert Port Policy

Dead code is not acceptable as a side effect of porting.

Do not add:

- unused interfaces
- unused adapters
- unused constructors
- unused registries
- unused compatibility shims
- unused provider methods
- unused parity runners
- unused fixtures
- unused scripts
- unused fake implementations

New functionality must be connected to at least one of:

- real runtime flow
- composition root
- registry/factory
- CLI command
- configuration path
- provider adapter path
- test exercising meaningful behavior
- TSV parity manifest row
- integration scenario

Do not wire code only to avoid a deadcode warning. The wiring must represent intended product behavior.

When working in an area with large existing dead code:

1. Do not run broad cleanup blindly.
2. Identify dead code relevant to the current task.
3. Remove or wire only what is clearly tied to the behavior being implemented.
4. Avoid exploding the diff.
5. Document any large cleanup left for later.

## Architecture Rules

Respect existing package boundaries.

General direction:

```text
cmd / app composition
        ↓
interface/*
        ↓
core/*
        ↓
library/*
```

Adapters implement provider or infrastructure details and should depend inward on stable core interfaces, not leak provider-specific concerns into core.

Rules:

- `core` must not depend on provider SDKs.
- `core` must not depend on CLI or worker transport.
- `interface/worker` may orchestrate LiveKit room/worker concerns.
- `interface/cli` may parse commands and call application services.
- `adapter/<provider>` owns provider-specific HTTP/WebSocket/API behavior.
- `library/*` should remain small and cohesive.
- Do not introduce import cycles.
- Do not bypass architecture checks by moving code into vague packages.

## LiveKit Runtime Areas That Need Special Care

Treat these areas as parity-sensitive:

- worker registration and lifecycle
- job context creation and cleanup
- agent dispatch
- room connection and participant lifecycle
- agent session startup/shutdown
- agent activity state transitions
- interruption and cancellation
- VAD and turn detection
- STT streaming event boundaries
- LLM streaming and function/tool calls
- TTS streaming and playout lifecycle
- transcription synchronization
- telemetry and metrics
- provider adapter error normalization
- CLI developer workflow
- IPC behavior
- eval/test helper behavior

For these areas, prefer contract, TSV parity, or integration tests in addition to local Go unit tests.

## Provider Adapter Rules

Provider integrations belong in `adapter/<provider>`.

Rules:

- Keep provider SDK details out of `core`.
- Normalize provider errors into core-level error categories.
- Test provider request/response translation with unit tests.
- Use fake local HTTP/WebSocket servers where practical.
- Do not require real API keys in default CI.
- If a provider behavior mirrors LiveKit plugin behavior, cite the relevant `livekit-plugins/*` reference path in tests or TSV manifest rows.
- Add integration tests only when they can be deterministic and safe.

## Concurrency and Streaming Rules

Streaming behavior is often the real product contract.

When touching streaming code:

- Test event order when order matters.
- Test cancellation.
- Test backpressure or blocked consumers where practical.
- Test close/error paths.
- Test partial output behavior.
- Test finalization behavior.
- Avoid goroutine leaks.
- Use context deadlines.
- Avoid sleep-based tests unless there is no better synchronization mechanism.
- Normalize nondeterministic scheduling details in parity traces.

## Documentation Rules

Keep this file focused on instructions for coding agents.

Put user-facing setup and feature documentation in `README.md` or docs.

Put architecture policy in `ARCHITECTURE.md`.

When adding or changing major behavior:

- update relevant docs
- add examples if the behavior is user-facing
- document parity limitations if full parity is not yet possible
- keep operational notes separate from internal coding-agent instructions

## Commit Rules

Keep commits small and focused.

Each commit should represent one coherent functionality-mirroring improvement.

Suggested commit types:

- `feat(core): mirror <reference behavior>`
- `feat(adapter): mirror <provider behavior>`
- `fix(core): align <behavior> with reference`
- `fix(adapter): align <provider> behavior with reference`
- `test(core): add Go parity coverage for <behavior>`
- `test(parity): add TSV manifest case for <behavior>`
- `test(parity): add cross-runtime case for <behavior>`
- `test(contract): add <contract> coverage`
- `test(integration): cover <runtime dependency behavior>`
- `refactor(core): wire <component> into <flow>`
- `chore(test): add CI reporting for <suite>`

Before committing:

1. Run relevant Python reference runner or parity case.
2. Run relevant Go test or runner.
3. Run relevant contract tests.
4. Run integration tests if runtime dependencies were touched.
5. Run `scripts/parity-gate.sh --case <case-name>` for focused parity changes.
6. Run `scripts/parity-gate.sh` for final parity-sensitive validation.
7. Run broader Go validation.
8. Run architecture checks if package boundaries changed.
9. Run staticcheck/deadcode gates when touched code may affect wiring.
10. Confirm no new deadcode remains.
11. Confirm test reports or parity artifacts are generated where expected.
12. Confirm `staticcheck.txt`, `deadcode.txt`, temporary traces, and generated scratch files are not staged unless intentionally tracked.

Do not use `--no-verify` to bypass pre-commit hooks. If a hook fails, fix the issue or explain why the hook itself needs to change.

## Current Known Drift

Several LiveKit reference areas already have partial Go equivalents. Treat existing Go code as the starting point.

Do not re-port from scratch when Go behavior already exists. Instead:

1. compare behavior
2. identify gaps
3. add or update tests
4. update the TSV manifest if parity-sensitive
5. adjust incrementally
6. validate against the selected contract

Parity is close enough that symbol coverage is less important than behavior proof, test quality, integration correctness, and dead-code-free wiring.

Most current parity coverage may be `go-test`, not true `cross-runtime`. That is acceptable when the Go tests intentionally encode reference behavior, but important runtime behavior should graduate to `cross-runtime` when practical.

## Agent Work Summary Format

When finishing a task, report:

- behavior mirrored or changed
- Python reference files inspected
- Go packages/files changed
- tests added or updated
- TSV manifest rows added or updated
- case type used: `go-test`, `cross-runtime`, or `symbol-report`
- contract/integration cases added or updated
- commands run
- remaining drift or limitations
- deadcode/staticcheck status
- commit hash, if committed

Do not claim broader parity than the evidence supports.
