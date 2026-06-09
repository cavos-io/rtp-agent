#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/parity-validate-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

VALID_MANIFEST="$WORKDIR/test-cases.tsv"
BAD_MANIFEST="$WORKDIR/bad-test-cases.tsv"
INCOMPLETE_CROSS_MANIFEST="$WORKDIR/incomplete-cross-test-cases.tsv"

cat > "$VALID_MANIFEST" <<'TSV'
case_name	type	source_ref	target_ref	go_package	go_test	python_runner	go_runner	input_json	contract	behavior	notes
go-dev-mode	go-test	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc_test.go	./library/utils	TestIsDevModeMatchesReferenceEnv				dev-mode-env-exact	Development mode is enabled only when LIVEKIT_DEV_MODE is exactly 1.	Smoke test for batched go-test manifest dispatch.
go-hosted-mode	go-test	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc_test.go	./library/utils	TestIsHostedUsesReferenceEnv				hosted-env-presence	Hosted mode follows LIVEKIT_REMOTE_EOT_URL environment presence.	Smoke test for batched go-test manifest dispatch.
dev-mode-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc.go			python3 scripts/parity-runners/python-utils.py	go run ./scripts/parity-runners/go-utils	{"env_values":["1","","true","on"]}	dev-mode-env-exact	Development mode is enabled only when LIVEKIT_DEV_MODE is exactly 1.	Smoke test for real cross-runtime runner dispatch.
hosted-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc.go			python3 scripts/parity-runners/python-utils.py	go run ./scripts/parity-runners/go-utils	{"contract":"hosted-env-presence","env_values":[null,"","https://hosted.example"]}	hosted-env-presence	Hosted mode follows LIVEKIT_REMOTE_EOT_URL environment presence.	Smoke test for multi-contract cross-runtime runner dispatch.
cloud-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc.go			python3 scripts/parity-runners/python-utils.py	go run ./scripts/parity-runners/go-utils	{"contract":"cloud-url-host-suffix","url_values":["wss://tenant.livekit.cloud","https://tenant.livekit.run/path","http://localhost:7880","://bad-url","https://livekit.cloud.evil.example"]}	cloud-url-host-suffix	Cloud URL detection follows reference hostname suffix rules.	Smoke test for URL-vector cross-runtime runner dispatch.
camel-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc.go			python3 scripts/parity-runners/python-utils.py	go run ./scripts/parity-runners/go-utils	{"contract":"camel-to-snake-case","name_values":["HTTPServerID","roomID","JobContext","already_ok","URL"]}	camel-to-snake-case	CamelCase names convert to snake_case using reference word boundaries.	Smoke test for string-result cross-runtime runner dispatch.
exp-filter-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/exp_filter.py	library/math/filter.go			python3 scripts/parity-runners/python-utils.py	go run ./scripts/parity-runners/go-utils	{"contract":"exp-filter-initial-minimum","alpha":0.5,"initial":10,"min_val":6,"exp":1,"sample":2}	exp-filter-initial-minimum	ExpFilter applies reference initial values and minimum clamping.	Smoke test for numeric-result cross-runtime runner dispatch.
moving-average-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/moving_average.py	library/math/filter.go			python3 scripts/parity-runners/python-utils.py	go run ./scripts/parity-runners/go-utils	{"contract":"moving-average-window","window_size":3,"sample_values":[1,2,3,4]}	moving-average-window	MovingAverage tracks rolling average, size, and reset behavior.	Smoke test for rolling-window cross-runtime runner dispatch.
bounded-dict-cross	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/bounded_dict.py	library/utils/bounded_dict.go			python3 scripts/parity-runners/python-utils.py	go run ./scripts/parity-runners/go-utils	{"contract":"bounded-dict-pop-if-order"}	bounded-dict-pop-if-order	BoundedDict PopIf follows reference predicate and oldest-pop order.	Smoke test for object-result cross-runtime runner dispatch.
TSV

cat > "$BAD_MANIFEST" <<'TSV'
case_name	type	source_ref	target_ref	go_package	go_test	python_runner	go_runner	input_json	contract	behavior	notes
bad	go-test	source	target	./pkg	TestName				contract	behavior	notes	unexpected-tab-field
TSV

cat > "$INCOMPLETE_CROSS_MANIFEST" <<'TSV'
case_name	type	source_ref	target_ref	go_package	go_test	python_runner	go_runner	input_json	contract	behavior	notes
missing-runner	cross-runtime	refs/agents/livekit-agents/livekit/agents/utils/misc.py	library/utils/misc_test.go				go-runner	input.json	json-contract	dev mode env contract	must fail before placeholder dispatch when a runner is absent
TSV

bash -n "$ROOT/scripts/parity-validate.sh"
bash -n "$ROOT/scripts/repo-temp-env.sh"

TEMP_ENV_REPO="$WORKDIR/temp-env-repo"
mkdir -p "$TEMP_ENV_REPO" "$WORKDIR/shared-tmp"
ln -s "$WORKDIR/shared-tmp" "$TEMP_ENV_REPO/.tmp"
(
  unset GOCACHE TMPDIR
  REPO_ROOT="$TEMP_ENV_REPO"
  source "$ROOT/scripts/repo-temp-env.sh"
  [[ ! -L "$TEMP_ENV_REPO/.tmp" ]]
  [[ -d "$TEMP_ENV_REPO/.tmp/gocache" ]]
  [[ -d "$TEMP_ENV_REPO/.tmp/gotmp" ]]
  [[ "$GOCACHE" == "$TEMP_ENV_REPO/.tmp" ]]
  [[ "$TMPDIR" == "$TEMP_ENV_REPO/.tmp/gotmp" ]]
)
(
  export GOCACHE="$WORKDIR/custom-gocache"
  export TMPDIR="$WORKDIR/custom-tmpdir"
  mkdir -p "$GOCACHE" "$TMPDIR"
  REPO_ROOT="$TEMP_ENV_REPO"
  source "$ROOT/scripts/repo-temp-env.sh"
  [[ "$GOCACHE" == "$WORKDIR/custom-gocache" ]]
  [[ "$TMPDIR" == "$WORKDIR/custom-tmpdir" ]]
)

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'go-dev-mode'
PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'go-hosted-mode'
PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'dev-mode-cross'
PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'hosted-cross'
PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'cloud-cross'
PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'camel-cross'
PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'exp-filter-cross'
PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'moving-average-cross'
PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list \
  | grep -Fxq 'bounded-dict-cross'

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case dev-mode-cross > "$WORKDIR/cross.out" 2>&1
grep -q '^\[dev-mode-cross\] ok$' "$WORKDIR/cross.out"

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case hosted-cross > "$WORKDIR/hosted-cross.out" 2>&1
grep -q '^\[hosted-cross\] ok$' "$WORKDIR/hosted-cross.out"

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case cloud-cross > "$WORKDIR/cloud-cross.out" 2>&1
grep -q '^\[cloud-cross\] ok$' "$WORKDIR/cloud-cross.out"

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case camel-cross > "$WORKDIR/camel-cross.out" 2>&1
grep -q '^\[camel-cross\] ok$' "$WORKDIR/camel-cross.out"

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case exp-filter-cross > "$WORKDIR/exp-filter-cross.out" 2>&1
grep -q '^\[exp-filter-cross\] ok$' "$WORKDIR/exp-filter-cross.out"

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case moving-average-cross > "$WORKDIR/moving-average-cross.out" 2>&1
grep -q '^\[moving-average-cross\] ok$' "$WORKDIR/moving-average-cross.out"

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case bounded-dict-cross > "$WORKDIR/bounded-dict-cross.out" 2>&1
grep -q '^\[bounded-dict-cross\] ok$' "$WORKDIR/bounded-dict-cross.out"

PARITY_TEST_CASES_FILE="$VALID_MANIFEST" "$ROOT/scripts/parity-validate.sh" > "$WORKDIR/all-cross.out" 2>&1
grep -q '^\[go-dev-mode\] ok$' "$WORKDIR/all-cross.out"
grep -q '^\[go-hosted-mode\] ok$' "$WORKDIR/all-cross.out"
grep -q '^\[dev-mode-cross\] ok$' "$WORKDIR/all-cross.out"
grep -q '^\[hosted-cross\] ok$' "$WORKDIR/all-cross.out"
grep -q '^\[cloud-cross\] ok$' "$WORKDIR/all-cross.out"
grep -q '^\[camel-cross\] ok$' "$WORKDIR/all-cross.out"
grep -q '^\[exp-filter-cross\] ok$' "$WORKDIR/all-cross.out"
grep -q '^\[moving-average-cross\] ok$' "$WORKDIR/all-cross.out"
grep -q '^\[bounded-dict-cross\] ok$' "$WORKDIR/all-cross.out"

if PARITY_TEST_CASES_FILE="$INCOMPLETE_CROSS_MANIFEST" "$ROOT/scripts/parity-validate.sh" --case missing-runner > "$WORKDIR/incomplete-cross.out" 2>&1; then
  echo "cross-runtime case with a missing runner unexpectedly passed" >&2
  exit 1
fi
grep -q 'cross-runtime rows must set source_ref, target_ref, python_runner, go_runner, input_json, contract, and behavior' "$WORKDIR/incomplete-cross.out"

if PARITY_TEST_CASES_FILE="$BAD_MANIFEST" "$ROOT/scripts/parity-validate.sh" --list > "$WORKDIR/bad.out" 2>&1; then
  echo "manifest with an extra tab field unexpectedly passed" >&2
  exit 1
fi
grep -q 'Tabs are not allowed inside manifest fields' "$WORKDIR/bad.out"
