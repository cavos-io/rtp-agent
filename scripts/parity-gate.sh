#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/parity-gate.sh [--case NAME ...] [--changed] [--all]

Options:
  --case NAME  Run only the selected parity validation case. May be repeated.
               Use this for the local inner loop of a focused parity slice.
  --changed    Run parity validation cases related to changed files.
               Manifest row edits run the changed manifest cases directly.
               This is a local inner-loop shortcut, not a replacement for --all.
  --all        Run every parity validation case. This is the default.
  -h, --help   Show help.

The gate always runs shell syntax checks, test-integrity checks, and deadcode
checks. Case filtering only changes the Layer 3 parity validation scope. Use
the full default gate before considering a parity-sensitive slice complete.
EOF
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

TEST_CASES_FILE="${PARITY_TEST_CASES_FILE:-$REPO_ROOT/scripts/parity-fixtures/test-cases.tsv}"
declare -a CASE_ARGS=()
MODE="all"

changed_files() {
  if [[ -n "${PARITY_GATE_CHANGED_FILES:-}" ]]; then
    printf '%s\n' "$PARITY_GATE_CHANGED_FILES"
    return 0
  fi

  {
    git diff --name-only HEAD
    git ls-files --others --exclude-standard
  } | sed '/^$/d' | sort -u
}

changed_case_args() {
  {
    changed_manifest_case_names
    changed_path_case_names
  } | awk 'NF && !seen[$0]++ { print "--case"; print }'
}

changed_manifest_case_names() {
  local manifest_path="${TEST_CASES_FILE#$REPO_ROOT/}"
  if [[ "$manifest_path" == "$TEST_CASES_FILE" ]]; then
    return 0
  fi

  git diff HEAD --unified=0 -- "$manifest_path" \
    | awk -F '\t' '
      /^\+[^+]/ {
        line = substr($0, 2)
        if (line == "" || line ~ /^case_name\t/) {
          next
        }
        split(line, fields, "\t")
        if (fields[1] != "") {
          print fields[1]
        }
      }
    '
}

changed_path_case_names() {
  local changed
  changed="$(changed_files)"
  if [[ -z "$changed" ]]; then
    return 0
  fi

  awk -F '\t' -v changed="$changed" '
    function clean(path) {
      gsub(/^[.]\//, "", path)
      gsub(/^"|"$/, "", path)
      return path
    }
    function path_match(file, path) {
      file = clean(file)
      path = clean(path)
      if (file == "" || path == "") {
        return 0
      }
      return file == path || index(file, path "/") == 1 || index(path, file "/") == 1
    }
    function add_path(path) {
      path = clean(path)
      if (path != "") {
        paths[++path_count] = path
      }
    }
    function add_command_paths(command,    parts, count, i, token) {
      count = split(command, parts, /[[:space:]]+/)
      for (i = 1; i <= count; i++) {
        token = parts[i]
        if (token ~ /^(\.\/)?[[:alnum:]_./-]+$/) {
          add_path(token)
        }
      }
    }
    BEGIN {
      changed_count = split(changed, changed_paths, "\n")
    }
    NR == 1 {
      next
    }
    {
      case_name = $1
      case_type = $2
      source_ref = $3
      target_ref = $4
      go_package = $5
      python_runner = $7
      go_runner = $8
      input_json = $9
      delete paths
      path_count = 0

      if (case_type == "go-test") {
        add_path(target_ref)
        add_path(go_package)
      } else if (case_type == "symbol-report") {
        add_path("scripts/parity-fixtures/" target_ref)
      } else if (case_type == "cross-runtime") {
        add_path(source_ref)
        add_path(target_ref)
        add_path(input_json)
        add_command_paths(python_runner)
        add_command_paths(go_runner)
      }

      for (i = 1; i <= changed_count; i++) {
        for (j = 1; j <= path_count; j++) {
          if (path_match(changed_paths[i], paths[j])) {
            print case_name
            next
          }
        }
      }
    }
  ' "$TEST_CASES_FILE"
}

while (($#)); do
  case "$1" in
    --case)
      if [[ -z "${2:-}" ]]; then
        echo "Error: --case requires a name." >&2
        usage >&2
        exit 2
      fi
      CASE_ARGS+=(--case "$2")
      MODE="case"
      shift 2
      ;;
    --changed)
      MODE="changed"
      CASE_ARGS=()
      shift
      ;;
    --all)
      CASE_ARGS=()
      MODE="all"
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

case "$MODE" in
  case)
    scripts/parity-validate.sh "${CASE_ARGS[@]}"
    ;;
  changed)
    mapfile -t CASE_ARGS < <(changed_case_args)
    if (( ${#CASE_ARGS[@]} > 0 )); then
      scripts/parity-validate.sh "${CASE_ARGS[@]}"
    else
      echo "No changed files map to Layer 3 parity validation cases; skipping case validation."
    fi
    ;;
  all)
    scripts/parity-validate.sh
    ;;
esac
scripts/check-test-integrity.sh
scripts/check-deadcode.sh
