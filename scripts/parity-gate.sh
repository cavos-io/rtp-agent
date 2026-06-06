#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

bash -n scripts/parity-check.sh
bash -n scripts/parity-validate.sh
bash -n scripts/parity-test-inventory.sh
bash -n scripts/check-test-integrity.sh
bash -n scripts/check-deadcode.sh

scripts/parity-validate.sh
scripts/check-test-integrity.sh
scripts/check-deadcode.sh
