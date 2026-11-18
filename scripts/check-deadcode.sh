#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"
REPO_TEMP_ENV_FORCE=1
source "$REPO_ROOT/scripts/repo-temp-env.sh"

target_go_files=("$@")
if (( ${#target_go_files[@]} == 0 )); then
  echo "No Go files to check; skipping Go dead-code checks."
  exit 0
fi

# Use the module's compiler baseline: the pinned analyzers may not understand
# export data emitted by a newer developer-installed Go compiler.
export GOTOOLCHAIN="go$(awk '$1 == "go" {print $2; exit}' go.mod)"
tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/check-deadcode.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

staticcheck_out="$tmpdir/staticcheck.out"
status=0
# Staticcheck merges symbol use across builds for U1000, while retaining other
# diagnostics from either build. Do not disable U1000 for native packages.
printf 'default: \nnative: -tags=nativeinprocess\n' |
  go tool staticcheck -matrix ./... >"$staticcheck_out" 2>&1 || status=$?
if (( status > 1 )) || { (( status != 0 )) && [[ ! -s "$staticcheck_out" ]]; } ||
  grep -Ev '(^$|\([A-Z]+[0-9]+\)$)' "$staticcheck_out" >/dev/null; then
  echo "Staticcheck did not complete successfully:" >&2
  cat "$staticcheck_out" >&2
  exit 1
fi

# Deadcode has no cross-build merge. Keep a finding unless the same source file
# is compiled in the other build AND that build considers the function live.
# A simple intersection would hide dead code in build-tag-exclusive files.
file_template='{{range .GoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .CgoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .TestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}'
for variant in default native; do
  tags=()
  if [[ "$variant" == native ]]; then tags=(-tags=nativeinprocess); fi
  if ! go list "${tags[@]}" -f "$file_template" ./... >"$tmpdir/$variant.files.raw"; then
    echo "Could not enumerate $variant build files." >&2
    exit 1
  fi
  if ! go tool deadcode "${tags[@]}" -test ./... >"$tmpdir/$variant.out.raw" 2>&1; then
    echo "Deadcode failed for $variant build:" >&2
    cat "$tmpdir/$variant.out.raw" >&2
    exit 1
  fi
  for kind in files out; do
    sed "s|$REPO_ROOT/||g; s|^\./||" "$tmpdir/$variant.$kind.raw" >"$tmpdir/$variant.$kind"
  done
done

related_findings=()
line_references_target_file() {
  local line=$1 file
  for file in "${target_go_files[@]}"; do
    case "$line" in
      "$file":* | "./$file":* | "$REPO_ROOT/$file":*) return 0 ;;
    esac
  done
  return 1
}

while IFS= read -r line || [[ -n "$line" ]]; do
  if line_references_target_file "$line"; then related_findings+=("[staticcheck] $line"); fi
done < "$staticcheck_out"

cat "$tmpdir/default.out" "$tmpdir/native.out" | LC_ALL=C sort -u > "$tmpdir/deadcode.out"
while IFS= read -r line || [[ -n "$line" ]]; do
  [[ -z "$line" ]] && continue
  if ! line_references_target_file "$line"; then continue; fi
  file=${line%%:*}
  live=false
  for variant in default native; do
    if grep -Fxq "$file" "$tmpdir/$variant.files" && ! grep -Fxq "$line" "$tmpdir/$variant.out"; then live=true; fi
  done
  if [[ "$live" == true ]]; then continue; fi
  # bridge.c's callback invokes //export nipEvent, which calls push and nowNS.
  # RTA cannot see this C -> Go edge. Exempt only that exact callback chain;
  # all other functions in this file and package remain subject to the gate.
  case "$line" in
    interface/worker/livekit/nativeinprocess/native.go:*': unreachable func: nipEvent' | \
    interface/worker/livekit/nativeinprocess/native.go:*': unreachable func: eventQueue.push' | \
    interface/worker/livekit/nativeinprocess/native.go:*': unreachable func: nowNS') continue ;;
  esac
  related_findings+=("[deadcode] $line")
done < "$tmpdir/deadcode.out"

if (( ${#related_findings[@]} > 0 )); then
  echo "Blocking Go analyzer findings in targeted Go files:"
  printf '  %s\n' "${related_findings[@]}"
  exit 1
fi

echo "No Go analyzer findings reference targeted Go files (default + nativeinprocess)."
