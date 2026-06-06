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
5. Add or update a behavior parity fixture when the task changes a meaningful
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
review. Do not treat this as a mandatory command for every task; run it when it
helps scope gaps, validate package placement, or inspect nearby reference-to-Go
coverage.

## Behavior Parity Validation

Name-based symbol matching is only Layer 1 parity discovery. Behavioral parity
must be validated with tests, fixtures, or explicit review evidence.

Use the parity layers as follows:

1. **Layer 1: Symbol candidate report**

  * `scripts/parity-check.sh` finds possible source/target symbol matches.
  * It helps identify gaps and candidate files.
  * It does not prove that behavior is equivalent.

2. **Layer 2: Agent-assisted review placeholder**

  * Future tooling may classify candidate pairs as `exists`, `partial`,
    `missing`, `intentionally_different`, or `unknown`.
  * Until that tooling exists, do not depend on agent review as the only proof
    of parity.

3. **Layer 3: Fixture/golden behavior validation**

  * Use `scripts/parity-validate.sh` and checked-in cases under
    `scripts/parity-fixtures/` when available.
  * Every important parity slice should add or update at least one Layer 3
    fixture.
  * Do not create one fixture per private helper unless it proves meaningful
    behavior.
  * Prefer fixtures for public flows, lifecycle behavior, error handling,
    config precedence, streaming behavior, retries, tool calls, room I/O,
    telemetry, and other behavior that can regress.
  * Fixtures should define input state, command/runner behavior, expected output
    or trace, normalization rules, and assertions/invariants.
  * Normalize unstable fields such as timestamps, absolute paths, UUIDs,
    random IDs, and nondeterministic ordering before comparing actual and
    expected output.
  * A fixture should fail with a clear diff when target behavior diverges from
    expected reference behavior.
  * Reuse shared expectation templates when multiple fixtures have the same
    output shape, such as normalized `go test -v` pass output.
    * Prefer small per-case metadata files over duplicated golden files when the
      only differences are package name, test name, command, or fixture inputs.
    * Keep per-case `expected.txt` files only when the expected output is
      genuinely case-specific, such as symbol report CSV output or behavior
      traces with unique content.
    * Adding a new parity case should usually mean adding the smallest useful
      fixture metadata, not copying an existing golden file.


4. **Layer 4: Quality gates**

  * Run focused Go tests and broaden verification as needed.
  * Run architecture checks when imports or package boundaries change.
  * Run `staticcheck ./...` and `deadcode ./...` when the task is likely to
    affect shared behavior, public interfaces, or unused parity scaffolding.
  * Fix issues related to the current task. Do not use unrelated staticcheck or
    deadcode output as permission for broad, unfocused rewrites.

A parity implementation is not complete unless new target code is tested, wired
into production flow, or explicitly documented as pending parity. Do not leave
new deadcode behind.

If Go tries to write module cache files under a read-only home directory, use a
workspace-local `.tmp` cache rather than changing project code to work around
the environment.

## Current Known Drift

* Several LiveKit reference areas already have partial Go equivalents. Treat
  existing Go code as the starting point and close behavioral gaps
  incrementally.

## Documentation Hygiene

Keep this file focused on instructions for future coding agents. Put user-facing
setup and feature documentation in `README.md` or the Docusaurus docs. Put
architecture policy in `ARCHITECTURE.md`.
