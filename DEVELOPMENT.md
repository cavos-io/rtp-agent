# RTP Agent Development

## Purpose

This document defines the development workflow for RTP Agent. It complements `ARCHITECTURE.md`: architecture owns structural boundaries and adapter contracts; this document owns implementation, reference-parity, testing, and validation practices.

## Reference parity

RTP Agent incrementally mirrors useful runtime behavior from:

- `refs/agents/livekit-agents`
- `refs/agents/livekit-plugins`
- `refs/pipecat`
- `refs/smart-turn`
- `refs/ten-framework`
- `refs/ten-vad`

The goal is behavioral compatibility where the reference applies, not mechanical translation or symbol coverage. Existing Go behavior is the starting point. Compare it with the reference, identify the gap, and adjust it incrementally.

Do not modify `refs/*` unless the task explicitly updates vendored reference material.

### LiveKit reference-to-Go map

| LiveKit reference | RTP Agent destination |
| --- | --- |
| `livekit/agents/worker.py`, `job.py` | `interface/worker` |
| `livekit/agents/ipc/*` | `interface/worker/ipc` |
| `livekit/agents/cli/*` | `interface/cli` |
| `livekit/agents/voice/agent.py` | `core/agent/agent.go` |
| `livekit/agents/voice/agent_session.py` | `core/agent/agent_session.go` |
| `livekit/agents/voice/agent_activity.py` | `core/agent/agent_activity.go` |
| `livekit/agents/voice/generation.py` | `core/agent/generation.go` |
| `livekit/agents/voice/room_io/*` | `interface/worker/livekit/room_io.go` |
| `livekit/agents/voice/recorder_io/*` | `interface/worker/livekit/recorder_io.go` |
| `livekit/agents/voice/transcription/*` | `core/agent/transcription.go` |
| `livekit/agents/voice/avatar/*` | `core/agent/avatar.go` and provider adapters |
| `livekit/agents/voice/ivr/*` | `core/agent/ivr.go` and `core/beta/tools` |
| `livekit/agents/llm/*` | `core/llm` |
| `livekit/agents/stt/*` | `core/stt` |
| `livekit/agents/tts/*` | `core/tts` |
| `livekit/agents/vad.py` | `core/vad` |
| `livekit/agents/inference/*` | `adapter/livekit` compatibility behavior |
| `livekit/agents/evals/*` | `core/evals` |
| `livekit/agents/metrics/*`, `telemetry/*` | `library/telemetry` |
| `livekit/agents/tokenize/*` | `library/tokenize` |
| `livekit/agents/utils/*` | `library/utils` or a narrower existing package |
| `livekit-plugins/*` | `adapter/<provider>` |

Follow `ARCHITECTURE.md` and `.architecture.yaml` when a reference concept could map to more than one component.

### Pipecat and TEN starting points

These are starting points, not structural equivalence. Select the Go destination from the behavior contract and the architecture boundaries.

| Reference | Common RTP Agent destination |
| --- | --- |
| `refs/pipecat/src/pipecat/audio`, `turns`, `pipeline`, `processors` | Provider-neutral behavior in `core`, transport behavior in `interface/worker` |
| `refs/pipecat/src/pipecat/services/*` | Matching `adapter/<provider>` |
| `refs/smart-turn/*` | `adapter/pipecat` and its provider-neutral integration in `core/agent` |
| `refs/ten-framework/ai_agents/**/rtm-transport/*` | `interface/worker/agora`, `core/audio`, or `core/agent` according to ownership |
| `refs/ten-framework/ai_agents/**/ten_packages/extension/*` | Matching provider adapter or transport interface |
| `refs/ten-vad/*` | `adapter/ten` |

### Selecting among references

Choose the source that owns the selected behavior. Use framework sources for orchestration and lifecycle semantics, provider-plugin sources for provider protocol behavior, and model sources such as Smart Turn or TEN VAD for model input and inference contracts.

When references overlap or disagree, do not merge them into a synthetic contract. Record the authoritative path and revision, classify whether the difference is version drift or intentional applicability, and keep secondary evidence in the manifest notes, test rationale, or a separate case. Capture a revision with `git -C <reference-root> rev-parse HEAD` when the contract is version-sensitive.

### Behavior-first workflow

For an explicit parity task:

1. Inspect the matching reference implementation.
2. Inspect the existing Go package and tests.
3. Define the observable behavior contract.
4. Run both implementations for the same scenario when practical.
5. Compare normalized output, events, state transitions, errors, or traces.
6. Classify the difference as missing Go behavior, intentional Go behavior, inapplicable reference behavior, a reference-version conflict, a runner or fixture error, or a normalization error.
7. For analysis or review, report the evidence without changing code. For implementation, make the smallest Go change that closes the demonstrated gap.
8. When code changes, add or update Go tests and shared parity cases.
9. Validate focused behavior before running broader gates.

Before implementation, record:

- behavior gap
- authoritative reference file and any secondary references
- Go target packages or files
- expected reference behavior
- reference and Go execution commands
- comparison contract
- validation plan

### Parity evidence

Use one or more of:

- a cross-runtime scenario using the same input
- a shared manifest case
- reference and Go runner output compared after normalization
- a Go test explicitly encoding observed reference behavior
- documented review evidence when execution is impractical

Symbol reports are discovery tools only. They do not prove runtime parity.

The shared manifest is `scripts/parity-fixtures/test-cases.tsv`. Prefer adding a manifest row over creating a one-off runner. The row's `source_ref` must identify the relevant file under one of the approved reference roots listed above. Other reference ecosystems in the manifest are outside this workflow.

`cross-runtime` and `json-scenario` cases must execute both sides, use the same scenario, emit comparable output, and normalize unstable values such as timestamps, durations, paths, UUIDs, generated IDs, and nondeterministic ordering. Use `json-scenario` for the repository's generic scenario runners and scenario-defined ignored fields. Case classification comes from the manifest `type` column; embedded JSON for a `json-scenario` deliberately uses `"case_type":"cross-runtime"`. If neither runtime comparison is practical, use a `go-test` case and state which reference behavior the test encodes.

Most current manifest evidence is `go-test`. This is valid target-side regression evidence when the test intentionally encodes observed reference behavior. Graduate important executable behavior to `cross-runtime` or `json-scenario` when practical.

Current Pipecat adapter cases derive from `refs/smart-turn`; the manifest has no case sourced directly from `refs/pipecat` yet. Treat Pipecat as an available reference, not as demonstrated coverage, until behavior-specific rows are added.

### Parity-sensitive runtime behavior

Use contract or integration evidence—not symbol matching alone—for worker and job lifecycles, agent/session/activity transitions, interruption and cancellation, VAD and turn detection, streaming STT/LLM/TTS boundaries, tools, room I/O, IPC, transcription synchronization, telemetry, and provider error normalization.

## Streaming and concurrency

Streaming behavior is part of the product contract. When relevant, test:

- event ordering
- cancellation and deadlines
- backpressure or blocked consumers
- close and error paths
- partial output
- finalization
- goroutine and stream cleanup

Prefer deterministic synchronization over sleeps. Preserve the ownership rules in `ARCHITECTURE.md`, and normalize scheduling details that are not part of the behavioral contract.

## Provider adapters

Provider integrations belong in `adapter/<provider>` and follow the contracts in `ARCHITECTURE.md` and `.architecture.yaml`.

- Keep provider SDK and protocol details out of core.
- Test request, response, event, and error translation.
- Use local fake HTTP or WebSocket servers when practical.
- Do not require real API keys or paid provider calls in default tests.
- Cite the authoritative provider reference path in parity tests or manifest cases.
- Add integration tests only when deterministic and safe.

## Test integrity and dead code

Do not delete, skip, weaken, or rewrite tests merely to make a change pass. Do not add meaningless assertions or fake references to silence tooling.

New behavior must connect to an intended runtime flow, composition root, registry or factory, CLI, configuration path, provider path, meaningful test, or parity scenario. When an area contains existing debt, limit cleanup to code directly relevant to the task.

The repository guards operate primarily on staged changes:

- `scripts/check-test-integrity.sh` detects suspicious staged test weakening.
- `scripts/check-deadcode.sh` checks staged Go files against static analysis findings.

Review unstaged changes separately; staged-only guards cannot protect them.

Before treating either guard as evidence, inspect `git diff --cached --name-only` and confirm it contains the intended Go or test files. A zero exit with no applicable staged files proves nothing. Do not stage files solely to satisfy a guard unless the current workflow authorizes staging; run the underlying `go tool staticcheck ./...` and `go tool deadcode -test ./...` checks or report the analyzer coverage gap instead.

## Validation

Start narrow and broaden according to risk:

```sh
go test ./path/to/touched/package
go tool go-file-arch -config .architecture.yaml ./...
scripts/parity-validate.sh --case <case-name>
scripts/parity-gate.sh --case <case-name>
scripts/parity-gate.sh
scripts/go-test-all.sh
scripts/go-build-all.sh
scripts/check-test-integrity.sh
scripts/check-deadcode.sh
```

`parity-validate.sh --case` and `parity-gate.sh --case` are focused inner-loop checks. Run the unfiltered `scripts/parity-gate.sh` before declaring a parity-sensitive implementation complete; it runs all manifest cases and invokes the staged test-integrity and analyzer guards. Confirm those guards actually selected the intended files before citing them as evidence.

Use these as discovery and inventory aids, never as parity proof:

```sh
scripts/parity-check.sh --source-dir <reference-root> --target-dir . --source-lang python --target-lang go --output .tmp/parity-report.csv
scripts/parity-test-inventory.sh
```

Inventory classifications are name/body heuristics, not evidence or manifest decisions. The current `reference-parity` classifier is biased toward LiveKit and the word `reference`; manually inspect Pipecat, Smart Turn, and TEN candidates regardless of their generated classification.

Run parity-tool self-tests when changing manifest parsing, normalization, case selection, batching, runners, or symbol mapping:

```sh
scripts/test-parity-validate.sh
scripts/test-parity-gate.sh
scripts/test-parity-check.sh
```

Do not commit generated reports, temporary traces, or scratch files unless the task explicitly requires them. Never bypass hooks with `--no-verify`.

## Completion report

For parity work, report:

- behavior analyzed or changed
- reference files inspected
- Go files or packages changed
- tests and manifest rows changed, when applicable
- evidence type: `cross-runtime`, `json-scenario`, `go-test`, documented review, or symbol discovery
- commands run and their results
- remaining drift or limitations
- commit hash, when committed

Do not claim broader compatibility than the evidence supports.
