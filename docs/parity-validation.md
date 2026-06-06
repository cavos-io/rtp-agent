# Reference-To-Go Validation Layers

This project uses multiple layers to move from reference discovery to behavior
confidence. These layers complement the porting workflow in `AGENTS.md`.

## Layer 1: Symbol Candidate Report

`scripts/parity-check.sh` extracts source and target symbols and proposes
candidate matches by normalized name and mapped paths. This layer is only a
candidate discovery tool.

Layer 1 does not prove behavior. Name matches can be false positives, missing
matches can be false negatives, and every row still needs human or behavioral
validation before it should be treated as implemented.

## Layer 2: Future Assisted Review Placeholder

Layer 2 is reserved for future agent-assisted review of Layer 1 candidates. No
agent calls are implemented yet.

When implemented, review output should use these statuses:

- `exists`: the target behavior matches the reference closely enough for the
  reviewed scope.
- `partial`: the target contains related behavior but has known gaps.
- `missing`: no meaningful target behavior was found.
- `intentionally_different`: the target differs for a documented product,
  architecture, or provider-boundary reason.
- `unknown`: the available evidence is insufficient.

Layer 2 output should remain advisory until backed by Layer 3 fixtures or direct
human review.

## Layer 3: Fixture And Golden Behavior Validation

`scripts/parity-validate.sh` runs named deterministic fixture cases under
`scripts/parity-fixtures/` and compares normalized actual output with checked-in
golden output.

Available cases:

- `pull-basic`: validates source and target symbol report behavior, including
  source report column shape, Go/Python symbol extraction, and mapped target path
  boundary handling.
- `dtmf-tool-error`: validates beta DTMF tool invalid-event behavior through the
  existing Go package test command.
- `address-confirmation-default`: validates that address capture asks for
  confirmation by default, matching the reference audio behavior.
- `email-confirmation-default`: validates that email capture asks for
  confirmation by default, matching the reference audio behavior.
- `phone-confirmation-default`: validates that phone number capture asks for
  confirmation by default, matching the reference audio behavior.

Run all cases:

```sh
scripts/parity-validate.sh
```

Run a single case:

```sh
scripts/parity-validate.sh --case pull-basic
```

The validator captures raw command output in a temporary directory, normalizes
unstable values such as absolute paths, timestamps, UUIDs, temp paths, and
durations, then runs `diff -u` against the golden file. On failure it prints the
temp directory and the diff.

## Layer 4: Quality Gates

New validation code must satisfy the same quality gates as other repository
work:

```sh
bash -n scripts/parity-check.sh
bash -n scripts/parity-validate.sh
scripts/parity-validate.sh
go test ./...
staticcheck ./... > staticcheck.txt
deadcode ./... > deadcode.txt
```

`staticcheck.txt` and `deadcode.txt` should be empty and must not be committed.
New validation code must be tested by fixture cases, wired into an executed
validation command, or explicitly documented as pending Layer 2 review work.
