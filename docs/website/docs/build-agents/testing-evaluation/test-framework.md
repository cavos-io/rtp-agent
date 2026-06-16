---
id: test-framework
title: Test framework
---

# Test framework

Status: **implemented** through native Go tests and repository parity scripts.

Use the narrowest command that proves the behavior you changed, then broaden before final validation.

Common commands:

```bash
go test ./...
scripts/parity-gate.sh --local
scripts/parity-gate.sh
```

`scripts/parity-gate.sh --local` is a fast changed-file loop. It is useful while editing, but it skips parts of the final gate. Use the full parity gate for parity-sensitive changes before claiming final behavior.

## Add parity coverage only when it proves reference behavior

Do not add every test to the manifest. Add or update a row in `scripts/parity-fixtures/test-cases.tsv` when the test encodes behavior that should stay aligned with the LiveKit reference.

Evidence:

- `scripts/parity-gate.sh`
- `scripts/parity-validate.sh`
- `scripts/parity-fixtures/test-cases.tsv`
- `scripts/go-test-all.sh`
