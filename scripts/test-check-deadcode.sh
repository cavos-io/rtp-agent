#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work=$(mktemp -d "${TMPDIR:-/tmp}/analyzer-gate-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
cat > "$work/bin/go" <<'MOCK'
#!/usr/bin/env bash
set -eu
printf '%s %s\n' "${GOTOOLCHAIN:-unset}" "$*" >> "$TEST_WORK/calls"
if [[ "$1" == list ]]; then
  printf '%s\n' 'fixture/shared.go' 'interface/worker/livekit/nativeinprocess/native.go'
  if [[ "$*" == *nativeinprocess* ]]; then echo fixture/native.go; else echo fixture/default.go; fi
  exit 0
fi
if [[ "$2" == staticcheck ]]; then
  [[ "$*" == *-matrix* ]] || { echo 'fixture/shared.go:1:1: field nativeUse is unused (U1000)'; exit 1; }
  cat > "$TEST_WORK/matrix"
  if [[ "$TEST_CASE" == download-* ]]; then
    printf '%s\n' 'go: downloading honnef.co/go/tools v0.7.0' 'go: downloading golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678' >&2
  fi
  case "$TEST_CASE" in
    download-clean) exit 0 ;;
    download-unrelated) echo 'other/file.go:2:1: bad assignment (SA4006)'; exit 1 ;;
    download-finding) echo 'fixture/shared.go:2:1: bad assignment (SA4006)'; exit 1 ;;
    download-stderr-finding) echo 'fixture/shared.go:2:1: bad assignment (SA4006)' >&2; exit 1 ;;
    download-compile) echo '-: cannot import cmp (compile)'; exit 1 ;;
    download-empty-failure) exit 1 ;;
    download-crash) exit 2 ;;
    download-error) echo 'go: downloading example.com/module: connection refused' >&2; exit 1 ;;
    download-unexpected) echo 'unexpected analyzer output' >&2; exit 0 ;;
    download-stdout) echo 'go: downloading honnef.co/go/tools v0.7.0'; exit 0 ;;
    static-failure) echo '-: cannot import cmp (compile)'; exit 1 ;;
    static-empty-failure) exit 1 ;;
    static-finding) echo 'fixture/shared.go:2:1: bad assignment [native] (SA4006)'; exit 1 ;;
    unrelated) echo 'other/file.go:2:1: bad assignment (SA4006)'; exit 1 ;;
  esac
  exit 0
fi
if [[ "$2" == deadcode ]]; then
  case "$TEST_CASE" in
    dead-failure) echo 'cannot load packages' >&2; exit 1 ;;
    dead-shared) echo 'fixture/shared.go:3:1: unreachable func: dead' ;;
    dead-native) [[ "$*" != *nativeinprocess* ]] || echo 'fixture/native.go:3:1: unreachable func: dead' ;;
    dead-default) [[ "$*" == *nativeinprocess* ]] || echo 'fixture/default.go:3:1: unreachable func: dead' ;;
    callback-other) echo 'interface/worker/livekit/nativeinprocess/native.go:200:1: unreachable func: other' ;;
    callback)
      for f in eventQueue.push nipEvent nowNS; do echo "interface/worker/livekit/nativeinprocess/native.go:45:6: unreachable func: $f"; done ;;
    *) [[ "$*" == *nativeinprocess* ]] || echo 'fixture/shared.go:1:1: unreachable func: nativeUse' ;;
  esac
  exit 0
fi
exit 2
MOCK
chmod +x "$work/bin/go"
run_case() {
  local name=$1 expected=$2 status=0
  : > "$work/calls"
  PATH="$work/bin:$PATH" TEST_WORK="$work" TEST_CASE="$name" bash "$ROOT/scripts/check-deadcode.sh" \
    fixture/shared.go fixture/default.go fixture/native.go interface/worker/livekit/nativeinprocess/native.go > "$work/output" 2>&1 || status=$?
  if [[ "$status" != "$expected" ]]; then cat "$work/output"; echo "$name: expected $expected, got $status"; exit 1; fi
}
run_case variants 0
run_case callback 0
run_case unrelated 0
run_case download-clean 0
grep -Fq 'go: downloading honnef.co/go/tools v0.7.0' "$work/output"
run_case download-unrelated 0
for c in download-finding download-stderr-finding; do
  run_case "$c" 1
  grep -Fq '[staticcheck] fixture/shared.go:2:1: bad assignment (SA4006)' "$work/output"
done
for c in download-compile download-empty-failure download-crash download-error download-unexpected download-stdout; do
  run_case "$c" 1
  grep -Fq 'Staticcheck did not complete successfully:' "$work/output"
done
for c in static-failure static-empty-failure static-finding dead-failure dead-shared dead-native dead-default callback-other; do run_case "$c" 1; done
run_case variants 0
grep -q '^native: -tags=nativeinprocess$' "$work/matrix"
version=$(awk '$1 == "go" {print $2; exit}' "$ROOT/go.mod")
if grep -v "^go${version} " "$work/calls"; then echo 'wrong analyzer toolchain'; exit 1; fi
bash "$ROOT/scripts/check-deadcode.sh" >/dev/null
echo 'Analyzer gate regression checks passed.'
