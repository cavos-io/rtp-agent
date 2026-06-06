#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/parity-gate.sh [--case NAME ...] [--all]

Options:
  --case NAME  Run only the selected parity validation case. May be repeated.
               Use this for the local inner loop of a focused parity slice.
  --all        Run every parity validation case. This is the default.
  -h, --help   Show help.

The gate always runs shell syntax checks, test-integrity checks, and deadcode
checks. Case filtering only changes the Layer 3 parity validation scope.
EOF
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

declare -a CASE_ARGS=()

while (($#)); do
  case "$1" in
    --case)
      if [[ -z "${2:-}" ]]; then
        echo "Error: --case requires a name." >&2
        usage >&2
        exit 2
      fi
      CASE_ARGS+=(--case "$2")
      shift 2
      ;;
    --all)
      CASE_ARGS=()
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

bash -n scripts/parity-check.sh
bash -n scripts/parity-validate.sh
bash -n scripts/parity-test-inventory.sh
bash -n scripts/check-test-integrity.sh
bash -n scripts/check-deadcode.sh

if (( ${#CASE_ARGS[@]} > 0 )); then
  scripts/parity-validate.sh "${CASE_ARGS[@]}"
else
  scripts/parity-validate.sh
fi
scripts/check-test-integrity.sh
scripts/check-deadcode.sh
