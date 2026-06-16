---
id: parity
title: Parity system
---

# Parity system

The project tracks LiveKit Agents behavior through a manifest-driven parity gate.

The central manifest is:

```text
scripts/parity-fixtures/test-cases.tsv
```

Supported case types are:

- `go-test`: runs Go tests that encode reference behavior.
- `cross-runtime`: runs a Python reference runner and Go runner with the same TSV-embedded `input_json`.
- `symbol-report`: validates symbol inventory fixtures.

Run the gate:

```bash
scripts/parity-gate.sh
```

Run one case:

```bash
scripts/parity-gate.sh --case <case-name>
```

Do not add one-off JSON fixture files or one runner per behavior. New parity-sensitive cases should use the TSV manifest and existing runner types whenever possible.

