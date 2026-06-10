#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/repo-temp-env.sh"

mapfile -t staged_go_files < <(git diff --cached --name-only --diff-filter=ACMR -- '*.go')

if (( ${#staged_go_files[@]} == 0 )); then
  echo "No staged Go files; skipping Go dead-code checks."
  exit 0
fi

if ! command -v staticcheck >/dev/null 2>&1; then
  echo "missing staticcheck: go install honnef.co/go/tools/cmd/staticcheck@latest"
  exit 1
fi

if ! command -v deadcode >/dev/null 2>&1; then
  echo "missing deadcode: go install golang.org/x/tools/cmd/deadcode@latest"
  exit 1
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/check-deadcode.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

staticcheck_out="$tmpdir/staticcheck.out"
deadcode_out="$tmpdir/deadcode.out"

set +e
staticcheck ./... >"$staticcheck_out" 2>&1
staticcheck_status=$?
deadcode -test ./... >"$deadcode_out" 2>&1
deadcode_status=$?
set -e

related_findings=()

line_references_staged_file() {
  local line=$1
  local file abs_file

  for file in "${staged_go_files[@]}"; do
    abs_file="$REPO_ROOT/$file"
    case "$line" in
      "$file":* | "./$file":* | "$abs_file":*)
        return 0
        ;;
    esac
  done

  return 1
}

classify_output() {
  local tool=$1
  local output_file=$2
  local status=$3
  local line

  if [[ ! -s "$output_file" ]]; then
    return
  fi

  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" ]] && continue
    if line_references_staged_file "$line"; then
      related_findings+=("[$tool] $line")
    fi
  done <"$output_file"
}

classify_output "staticcheck" "$staticcheck_out" "$staticcheck_status"
classify_output "deadcode" "$deadcode_out" "$deadcode_status"

if (( ${#related_findings[@]} > 0 )); then
  echo "Blocking Go analyzer findings in staged Go files:"
  printf '  %s\n' "${related_findings[@]}"
  exit 1
fi

echo "No Go analyzer findings reference staged Go files."
