# AGENTS.md

## Primary Mission

This repository is a Go implementation effort for LiveKit Agents-style runtime behavior.
Use `refs/agents/livekit-agents` as the behavioral reference and port its useful
functionality into the Go codebase incrementally.

Do not duplicate broad project setup or architecture guidance here. Read:

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

* Start from the Python reference behavior, then implement the Go version in the
  package that matches the map above.
* Preserve public concepts and lifecycle semantics where practical: worker,
  job context, agent, session, activity, tools, streaming LLM/STT/TTS, VAD,
  interruption, room I/O, telemetry, and plugin/provider boundaries.
* Keep `core` provider-agnostic. Provider-specific API details belong in
  `adapter/<provider>`.
* Keep LiveKit transport, room connection, and worker protocol concerns in
  `interface/worker`.
* Keep CLI parsing and local developer commands in `interface/cli`.
* Prefer small Go interfaces around stable behavior over direct translations of
  Python inheritance patterns.
* Do not add new cross-layer imports that violate `.go-arch-lint.yml`.
* Do not edit `refs/agents/*` except when explicitly updating the vendored
  reference material.

## Working Workflow

For each parity task:

1. Inspect the matching Python reference files under `refs/agents/livekit-agents`.
2. Inspect the existing Go package and tests before changing code.
3. Identify whether the task is core behavior, transport glue, provider adapter,
   CLI behavior, or shared utility code.
4. Add or update focused tests near the Go package being changed.
5. Add or update a behavior parity manifest case when the task changes a meaningful
   reference-to-Go behavior.
6. Run the narrowest useful verification first, then broaden as risk increases.

Common verification commands:

```sh
scripts/go-test-all.sh
scripts/go-build-all.sh
go-arch-lint check
go-arch-lint mapping
```

Optional parity guidance tool:

```sh
scripts/parity-check.sh \
  --source-dir refs/agents/livekit-agents \
  --target-dir . \
  --source-lang python \
  --target-lang go \
  --output .tmp/parity_report.csv
```

Use the parity report as a directional aid when deciding whether a port is
moving the Go implementation closer to the LiveKit reference. The report is a
symbol/candidate matching tool, not proof of behavioral parity: it may contain
false positives and false negatives, and `parity_status`/`notes` are for human
review. Layer 1 `match_confidence` values describe candidate matching only, not
tested behavior. Do not treat this as a mandatory command for every task; run it
when it helps scope gaps, validate package placement, or inspect nearby
reference-to-Go coverage.

## Behavior Parity Validation

Name-based symbol matching is only Layer 1 parity discovery. Behavioral parity
must be validated with tests, manifest cases, shared contracts, or explicit
review evidence.

Use the parity layers as follows:

1. **Layer 1: Symbol candidate report**

   * `scripts/parity-check.sh` finds possible source/target symbol matches.
   * It helps identify gaps and candidate files.
   * It does not prove that behavior is equivalent.
   * If a source path has mapped target paths, candidate matching should stay
     inside those mapped paths. Do not treat accidental global name matches
     outside the mapped destination as parity evidence.
   * Layer 1 must not claim behavior is tested or verified.

2. **Layer 2: Agent-assisted review placeholder**

   * Future tooling may classify candidate pairs as `exists`, `partial`,
     `missing`, `intentionally_different`, or `unknown`.
   * Until that tooling exists, do not depend on agent review as the only proof
     of parity.

3. **Layer 3: Manifest-driven behavior validation**

   * Use `scripts/parity-validate.sh` and the shared parity case manifest under
     `scripts/parity-fixtures/` when available.
   * Prefer manifest/table-driven parity cases. Adding a simple parity case
     should usually mean adding one row to the shared TSV manifest rather
     than creating dedicated per-case files.
   * Manifest rows should explain the parity intent, not only the command to run.
     Include fields such as case name, case type, source reference, target
     reference, Go package, Go test, contract label, behavior summary, and notes
     when supported.
   * The current manifest is `scripts/parity-fixtures/test-cases.tsv`. It is
     simple tab-delimited text, not quoted CSV. Columns are:

     ```text
     case_name	type	source_ref	target_ref	go_package	go_test	contract	behavior	notes
     ```

   * Simple Go-test-backed cases are useful as cheap target-side regression
     checks. They should verify that the selected test ran, passed, and completed
     in the expected package after normalization.
   * Go-only cases do not prove Python reference behavior and Go behavior are
     identical unless the Go test itself encodes the reference behavior clearly.
   * For important behavior, prefer future cross-runtime manifest cases when
     practical: one row should define the shared scenario, Python reference
     runner, Go target runner, input payload, and contract or trace to compare.
   * Use dedicated fixture directories or per-case golden files only when the
     case has genuinely unique file inputs, traces, symbol-report output, or
     other content that cannot be represented cleanly as manifest columns.
   * Normalize unstable fields such as timestamps, absolute paths, UUIDs,
     random IDs, durations, and nondeterministic ordering before comparing
     actual and expected output.
   * A validation case should fail with clear output showing which behavior,
     contract, assertion, or diff failed.

4. **Layer 4: Quality gates**

   * Use `scripts/check-test-integrity.sh` and `scripts/check-deadcode.sh` when
     available to prevent fake progress through weakened tests or unused parity
     code.
   * These gates do not prove behavior. They protect the validation work from
     deadcode, inert ports, and obvious test weakening.
   * Run focused Go tests and broaden verification as needed.
   * Run architecture checks when imports or package boundaries change.
   * Run `staticcheck ./...` and `deadcode ./...` when the task is likely to
     affect shared behavior, public interfaces, or unused parity scaffolding.
   * Fix issues related to the current task. Do not use unrelated staticcheck or
     deadcode output as permission for broad, unfocused rewrites.

For parity-sensitive changes, prefer the minimum gate:

```sh
scripts/parity-gate.sh
```

A parity implementation is not complete unless new target code is tested, wired
into production flow, or explicitly documented as pending parity. Do not leave
new deadcode behind.

## Current Known Drift

* Several LiveKit reference areas already have partial Go equivalents. Treat
  existing Go code as the starting point and close behavioral gaps
  incrementally.

## Documentation Hygiene

Keep this file focused on instructions for future coding agents. Put user-facing
setup and feature documentation in `README.md` or the Docusaurus docs. Put
architecture policy in `ARCHITECTURE.md`.
