---
id: test-framework
title: Test framework
---

# Test framework

Status: **implemented** through native Go tests and repository parity scripts.

Evidence:

- `scripts/parity-gate.sh`
- `scripts/parity-validate.sh`
- `scripts/parity-fixtures/test-cases.tsv`
- `scripts/go-test-all.sh`

Common commands:

```bash
go test ./...
scripts/parity-gate.sh --local
scripts/parity-gate.sh
```

`--local` is a fast loop, not final validation.

