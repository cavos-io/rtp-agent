#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/parity-validate.sh [--case NAME] [--keep-temp]

Options:
  --case NAME   Run a named fixture case. May be repeated.
                Use "all" to run every case. Default: all.
  --list        List available cases.
  --keep-temp   Keep temp output directories after successful runs.
  -h, --help    Show help.

Cases:
  Cases are listed in scripts/parity-fixtures/test-cases.tsv.
  The TSV is simple tab-delimited text, not quoted CSV.
  Tabs are separators and are not allowed inside fields.
  Columns:
    case_name, type, source_ref, target_ref, go_package, go_test,
    python_runner, go_runner, input_json, contract, behavior, notes

Case types:
  go-test        Runs one Go test as target-side regression evidence.
                 Multiple selected go-test cases in the same package are
                 batched into one go test process, then each selected test is
                 still asserted from normalized output.
  symbol-report  Runs a unique Layer 1 symbol-report golden fixture.
  cross-runtime  Runs Python reference and Go target runners with the same
                 input JSON and compares normalized JSON envelopes:
                 {"contract":"<contract>","events":[...]}.
EOF
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_ROOT="$REPO_ROOT/scripts/parity-fixtures"
TEST_CASES_FILE="${PARITY_TEST_CASES_FILE:-$REPO_ROOT/scripts/parity-fixtures/test-cases.tsv}"
EXPECTED_MANIFEST_HEADER=$'case_name\ttype\tsource_ref\ttarget_ref\tgo_package\tgo_test\tpython_runner\tgo_runner\tinput_json\tcontract\tbehavior\tnotes'
KEEP_TEMP=0
LIST_ONLY=0
declare -a REQUESTED_CASES=()

while (($#)); do
  case "$1" in
    --case)
      REQUESTED_CASES+=("${2:-}")
      shift 2
      ;;
    --list)
      LIST_ONLY=1
      shift
      ;;
    --keep-temp)
      KEEP_TEMP=1
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

if (( ${#REQUESTED_CASES[@]} == 0 )); then
  REQUESTED_CASES=("all")
fi

resolve_cases() {
  validate_manifest_schema
  local requested selected=()
  for requested in "${REQUESTED_CASES[@]}"; do
    case "$requested" in
      all)
        while IFS= read -r case_name; do
          selected+=("$case_name")
        done < <(list_cases)
        ;;
      "")
        echo "Error: --case requires a name." >&2
        return 2
        ;;
      *)
        if case_exists "$requested"; then
          selected+=("$requested")
        else
          echo "Error: unknown case: $requested" >&2
          echo "Available cases:" >&2
          list_cases | sed 's/^/  /' >&2
          return 2
        fi
        ;;
    esac
  done
  printf '%s\n' "${selected[@]}"
}

list_cases() {
  validate_manifest_schema
  test_case_names
}

case_exists() {
  local case_name="$1"
  [[ -n "$(test_case_row "$case_name")" ]]
}

normalize_common() {
  local input="$1" output="$2" tmpdir="$3"
  sed -E \
    -e "s#${REPO_ROOT//\#/\\#}#<repo>#g" \
    -e "s#${tmpdir//\#/\\#}#<tmp>#g" \
    -e 's#[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.+-]+Z?#<timestamp>#g' \
    -e 's#[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}#<uuid>#g' \
    -e 's#/tmp/[^[:space:]]+#<tmp-path>#g' \
    -e 's#[[:space:]]+$##' \
    "$input" > "$output"
}

normalize_case() {
  local case_name="$1" input="$2" output="$3" tmpdir="$4"
  local common="$tmpdir/actual.common"
  normalize_common "$input" "$common" "$tmpdir"

  case "$case_name" in
    pull-basic)
      cp "$common" "$output"
      ;;
    *)
      sed -E \
        -e 's/[[:space:]]+/ /g' \
        -e 's/\([0-9.]+s\)/(<duration>)/g' \
        -e 's/ [0-9.]+s$/ <duration>/' \
        "$common" > "$output"
      ;;
  esac
}

test_case_names() {
  [[ -f "$TEST_CASES_FILE" ]] || return 0
  awk -F '\t' 'NR > 1 && $1 != "" { print $1 }' "$TEST_CASES_FILE"
}

test_case_row() {
  local case_name="$1"
  [[ -f "$TEST_CASES_FILE" ]] || return 0
  awk -F '\t' -v name="$case_name" 'NR > 1 && $1 == name { print; exit }' "$TEST_CASES_FILE"
}

validate_manifest_schema() {
  local header
  if [[ ! -f "$TEST_CASES_FILE" ]]; then
    echo "Missing manifest: $TEST_CASES_FILE" >&2
    return 2
  fi
  IFS= read -r header < "$TEST_CASES_FILE"
  if [[ "$header" != "$EXPECTED_MANIFEST_HEADER" ]]; then
    echo "Invalid manifest header in $TEST_CASES_FILE" >&2
    echo "Expected: $EXPECTED_MANIFEST_HEADER" >&2
    echo "Actual:   $header" >&2
    return 2
  fi
  if ! awk -F '\t' 'NR > 1 && NF != 12 { printf "line %d has %d columns, want 12. Tabs are not allowed inside manifest fields.\n", NR, NF; exit 1 }' "$TEST_CASES_FILE" >&2; then
    echo "Invalid manifest row in $TEST_CASES_FILE" >&2
    return 2
  fi
}

case_field() {
  local case_name="$1" field_number="$2"
  awk -F '\t' -v name="$case_name" -v field="$field_number" 'NR > 1 && $1 == name { print $field; exit }' "$TEST_CASES_FILE"
}

module_path() {
  awk '$1 == "module" { print $2; exit }' "$REPO_ROOT/go.mod"
}

go_package_import_path() {
  local go_package="$1"
  case "$go_package" in
    ./*)
      printf '%s/%s\n' "$(module_path)" "${go_package#./}"
      ;;
    *)
      printf '%s\n' "$go_package"
      ;;
  esac
}

run_go_test_manifest_case() {
  local case_name="$1" tmpdir="$2"
  local row case_type source_ref target_ref go_package test_name contract behavior expected_package actual_norm
  row="$(test_case_row "$case_name")"
  if [[ -z "$row" ]]; then
    echo "[$case_name] missing manifest row in $TEST_CASES_FILE" >&2
    return 2
  fi
  case_type="$(case_field "$case_name" 2)"
  source_ref="$(case_field "$case_name" 3)"
  target_ref="$(case_field "$case_name" 4)"
  go_package="$(case_field "$case_name" 5)"
  test_name="$(case_field "$case_name" 6)"
  contract="$(case_field "$case_name" 10)"
  behavior="$(case_field "$case_name" 11)"
  if [[ "$case_type" != "go-test" ]]; then
    echo "[$case_name] manifest row type = $case_type, want go-test" >&2
    return 2
  fi
  if [[ -z "$source_ref" || -z "$target_ref" || -z "$go_package" || -z "$test_name" || -z "$contract" || -z "$behavior" ]]; then
    echo "[$case_name] manifest row must set source_ref, target_ref, go_package, go_test, contract, and behavior" >&2
    return 2
  fi

  if ! (
    cd "$REPO_ROOT"
    GOCACHE="${GOCACHE:-$REPO_ROOT/.tmp/gocache}" go test "$go_package" -run "^$test_name$" -count=1 -v
  ) > "$tmpdir/actual.raw" 2>&1; then
    :
  fi

  actual_norm="$tmpdir/actual.normalized"
  normalize_case "$case_name" "$tmpdir/actual.raw" "$actual_norm" "$tmpdir"
  expected_package="$(go_package_import_path "$go_package")"
  assert_go_test_pass_output "$case_name" "$actual_norm" "$test_name" "$expected_package" "$tmpdir/actual.raw"
}

materialize_input_json() {
  local input_json="$1" tmpdir="$2" output="$3"
  if [[ "$input_json" == "{"* || "$input_json" == "["* ]]; then
    printf '%s\n' "$input_json" > "$output"
    return 0
  fi
  if [[ "$input_json" == /* ]]; then
    cp "$input_json" "$output"
    return 0
  fi
  if [[ -f "$REPO_ROOT/$input_json" ]]; then
    cp "$REPO_ROOT/$input_json" "$output"
    return 0
  fi
  echo "input_json is neither inline JSON nor an existing file: $input_json" >&2
  return 2
}

run_json_runner() {
  local case_name="$1" runtime="$2" runner="$3" input_path="$4" stdout_path="$5" stderr_path="$6"
  local -a command=()
  read -r -a command <<< "$runner"
  if (( ${#command[@]} == 0 )); then
    echo "[$case_name] empty $runtime runner command" >&2
    return 2
  fi
  mkdir -p "$REPO_ROOT/.tmp/gocache"
  if ! (
    cd "$REPO_ROOT"
    GOCACHE="${GOCACHE:-$REPO_ROOT/.tmp/gocache}" "${command[@]}" "$input_path"
  ) > "$stdout_path" 2> "$stderr_path"; then
    echo "[$case_name] $runtime runner failed: $runner" >&2
    echo "[$case_name] $runtime stderr:" >&2
    cat "$stderr_path" >&2
    echo "[$case_name] $runtime stdout:" >&2
    cat "$stdout_path" >&2
    return 1
  fi
}

normalize_json_envelope() {
  local case_name="$1" runtime="$2" input="$3" output="$4" contract="$5"
  if ! python3 - "$input" "$output" "$contract" <<'PY'
import json
import sys

input_path, output_path, expected_contract = sys.argv[1:4]
with open(input_path, "r", encoding="utf-8") as file:
    data = json.load(file)

if not isinstance(data, dict):
    raise SystemExit("output JSON must be an object")
if data.get("contract") != expected_contract:
    raise SystemExit(
        f"contract = {data.get('contract')!r}, want {expected_contract!r}"
    )
events = data.get("events")
if not isinstance(events, list):
    raise SystemExit("events must be a list")

with open(output_path, "w", encoding="utf-8") as file:
    json.dump(data, file, sort_keys=True, separators=(",", ":"))
    file.write("\n")
PY
  then
    echo "[$case_name] $runtime runner did not emit a valid JSON envelope for contract $contract" >&2
    echo "[$case_name] $runtime raw output:" >&2
    cat "$input" >&2
    return 1
  fi
}

run_cross_runtime_manifest_case() {
  local case_name="$1" tmpdir="$2"
  local source_ref target_ref python_runner go_runner input_json contract behavior input_path
  local python_raw python_err python_norm go_raw go_err go_norm diff_path
  source_ref="$(case_field "$case_name" 3)"
  target_ref="$(case_field "$case_name" 4)"
  python_runner="$(case_field "$case_name" 7)"
  go_runner="$(case_field "$case_name" 8)"
  input_json="$(case_field "$case_name" 9)"
  contract="$(case_field "$case_name" 10)"
  behavior="$(case_field "$case_name" 11)"

  if [[ -z "$source_ref" || -z "$target_ref" || -z "$python_runner" || -z "$go_runner" || -z "$input_json" || -z "$contract" || -z "$behavior" ]]; then
    echo "[$case_name] cross-runtime rows must set source_ref, target_ref, python_runner, go_runner, input_json, contract, and behavior" >&2
    return 2
  fi

  input_path="$tmpdir/input.json"
  materialize_input_json "$input_json" "$tmpdir" "$input_path" || return

  python_raw="$tmpdir/python.raw.json"
  python_err="$tmpdir/python.stderr"
  python_norm="$tmpdir/python.normalized.json"
  go_raw="$tmpdir/go.raw.json"
  go_err="$tmpdir/go.stderr"
  go_norm="$tmpdir/go.normalized.json"
  diff_path="$tmpdir/cross-runtime.diff"

  run_json_runner "$case_name" "python" "$python_runner" "$input_path" "$python_raw" "$python_err" || return
  run_json_runner "$case_name" "go" "$go_runner" "$input_path" "$go_raw" "$go_err" || return
  normalize_json_envelope "$case_name" "python" "$python_raw" "$python_norm" "$contract" || return
  normalize_json_envelope "$case_name" "go" "$go_raw" "$go_norm" "$contract" || return

  if ! diff -u "$python_norm" "$go_norm" > "$diff_path"; then
    echo "[$case_name] cross-runtime JSON output differs for contract $contract." >&2
    echo "[$case_name] Python runner: $python_runner" >&2
    echo "[$case_name] Go runner: $go_runner" >&2
    echo "[$case_name] Python raw output:" >&2
    cat "$python_raw" >&2
    echo "[$case_name] Go raw output:" >&2
    cat "$go_raw" >&2
    echo "[$case_name] normalized diff:" >&2
    cat "$diff_path" >&2
    return 1
  fi
}

assert_go_test_pass_output() {
  local case_name="$1" output="$2" test_name="$3" expected_package="$4" raw_output="$5"
  local failures=()

  grep -Fqx -- "=== RUN $test_name" "$output" || failures+=("missing === RUN $test_name")
  grep -Fqx -- "--- PASS: $test_name (<duration>)" "$output" || failures+=("missing --- PASS: $test_name (<duration>)")
  grep -Fqx -- "PASS" "$output" || failures+=("missing final PASS")
  grep -Fqx -- "ok $expected_package <duration>" "$output" || failures+=("missing package result: ok $expected_package <duration>")

  if (( ${#failures[@]} > 0 )); then
    echo "[$case_name] Go test output failed normalized assertions:" >&2
    printf '  - %s\n' "${failures[@]}" >&2
    echo "Captured output:" >&2
    cat "$raw_output" >&2
    return 1
  fi
}

escape_extended_regex() {
  sed -E 's/[][(){}.^$*+?|\\]/\\&/g' <<<"$1"
}

run_go_test_manifest_cases() {
  local go_package="$1" tmpdir="$2"
  shift 2
  local case_name case_type source_ref target_ref test_name contract behavior expected_package actual_norm
  local regex="" escaped

  if (( $# == 0 )); then
    return 0
  fi

  for case_name in "$@"; do
    case_type="$(case_field "$case_name" 2)"
    source_ref="$(case_field "$case_name" 3)"
    target_ref="$(case_field "$case_name" 4)"
    test_name="$(case_field "$case_name" 6)"
    contract="$(case_field "$case_name" 10)"
    behavior="$(case_field "$case_name" 11)"
    if [[ "$case_type" != "go-test" ]]; then
      echo "[$case_name] manifest row type = $case_type, want go-test" >&2
      return 2
    fi
    if [[ -z "$source_ref" || -z "$target_ref" || -z "$go_package" || -z "$test_name" || -z "$contract" || -z "$behavior" ]]; then
      echo "[$case_name] manifest row must set source_ref, target_ref, go_package, go_test, contract, and behavior" >&2
      return 2
    fi
    escaped="$(escape_extended_regex "$test_name")"
    if [[ -z "$regex" ]]; then
      regex="$escaped"
    else
      regex="$regex|$escaped"
    fi
  done

  if ! (
    cd "$REPO_ROOT"
    GOCACHE="${GOCACHE:-$REPO_ROOT/.tmp/gocache}" go test "$go_package" -run "^($regex)$" -count=1 -v
  ) > "$tmpdir/actual.raw" 2>&1; then
    :
  fi

  actual_norm="$tmpdir/actual.normalized"
  normalize_case "go-test-batch" "$tmpdir/actual.raw" "$actual_norm" "$tmpdir"
  expected_package="$(go_package_import_path "$go_package")"
  for case_name in "$@"; do
    test_name="$(case_field "$case_name" 6)"
    assert_go_test_pass_output "$case_name" "$actual_norm" "$test_name" "$expected_package" "$tmpdir/actual.raw" || return
    echo "[$case_name] ok"
  done
}

run_symbol_report_case() {
  local case_name="$1" fixture_path="$2" tmpdir="$3"
  local case_dir="$FIXTURE_ROOT/$fixture_path"
  local source_dir="$tmpdir/source"
  local target_dir="$tmpdir/target"
  local source_report="$tmpdir/source.csv"
  local target_report="$tmpdir/target.csv"

  if [[ -z "$fixture_path" || ! -d "$case_dir" ]]; then
    echo "[$case_name] missing symbol-report fixture directory: $case_dir" >&2
    return 2
  fi

  cp -R "$case_dir/source" "$source_dir"
  mkdir -p "$target_dir"
  while IFS= read -r -d '' fixture; do
    local rel="${fixture#$case_dir/target/}"
    local out="$target_dir/${rel%.fixture}"
    mkdir -p "$(dirname "$out")"
    cp "$fixture" "$out"
  done < <(find "$case_dir/target" -type f -name '*.go.fixture' -print0 | sort -z)

  "$REPO_ROOT/scripts/parity-check.sh" \
    --source-dir "$source_dir" \
    --target-dir "$target_dir" \
    --source-lang python \
    --target-lang go \
    --map-file "$case_dir/map.txt" \
    --report source \
    --output "$source_report" > "$tmpdir/source.stdout" 2> "$tmpdir/source.stderr"

  "$REPO_ROOT/scripts/parity-check.sh" \
    --source-dir "$source_dir" \
    --target-dir "$target_dir" \
    --source-lang python \
    --target-lang go \
    --map-file "$case_dir/map.txt" \
    --report target \
    --output "$target_report" > "$tmpdir/target.stdout" 2> "$tmpdir/target.stderr"

  {
    printf '# source-report\n'
    cat "$source_report"
    printf '# target-report\n'
    cat "$target_report"
  } > "$tmpdir/actual.raw"
}

run_case() {
  local case_name="$1"
  local row case_type target_ref case_dir tmpdir expected actual_norm expected_norm
  row="$(test_case_row "$case_name")"
  if [[ -z "$row" ]]; then
    echo "[$case_name] missing manifest row in $TEST_CASES_FILE" >&2
    return 2
  fi
  case_type="$(case_field "$case_name" 2)"
  target_ref="$(case_field "$case_name" 4)"
  case_dir="$FIXTURE_ROOT/$target_ref"
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/parity-validate.${case_name}.XXXXXX")"
  expected="$case_dir/expected.txt"
  actual_norm="$tmpdir/actual.normalized"
  expected_norm="$tmpdir/expected.normalized"

  case "$case_type" in
    go-test)
      if ! run_go_test_manifest_case "$case_name" "$tmpdir"; then
        echo "Temp dir: $tmpdir" >&2
        return 1
      fi
      ;;
    symbol-report)
      if [[ -z "$target_ref" ]]; then
        echo "[$case_name] manifest row must set target_ref to the fixture path" >&2
        return 2
      fi
      if [[ ! -f "$expected" ]]; then
        echo "[$case_name] missing expected output: $expected" >&2
        return 1
      fi

      if ! run_symbol_report_case "$case_name" "$target_ref" "$tmpdir"; then
        echo "Temp dir: $tmpdir" >&2
        return 1
      fi
      normalize_case "$case_name" "$tmpdir/actual.raw" "$actual_norm" "$tmpdir"
      normalize_common "$expected" "$expected_norm" "$tmpdir"

      if ! diff -u "$expected_norm" "$actual_norm" > "$tmpdir/diff.txt"; then
        echo "[$case_name] fixture output differs from golden." >&2
        echo "Temp dir: $tmpdir" >&2
        cat "$tmpdir/diff.txt" >&2
        return 1
      fi
      ;;
    cross-runtime)
      if ! run_cross_runtime_manifest_case "$case_name" "$tmpdir"; then
        echo "Temp dir: $tmpdir" >&2
        return 1
      fi
      ;;
    *)
      echo "[$case_name] unsupported case_type: $case_type" >&2
      return 2
      ;;
  esac

  echo "[$case_name] ok"
  if (( KEEP_TEMP == 0 )); then
    rm -rf "$tmpdir"
  else
    echo "[$case_name] temp dir: $tmpdir"
  fi
}

main() {
  local case_name case_type go_package failed=0
  local cases_file
  local -a go_packages=() other_cases=()
  declare -A go_package_seen=()
  declare -A go_package_cases=()
  if (( LIST_ONLY == 1 )); then
    list_cases
    return 0
  fi
  cases_file="$(mktemp "${TMPDIR:-/tmp}/parity-validate.cases.XXXXXX")"
  trap 'rm -f "$cases_file"' RETURN
  if ! resolve_cases > "$cases_file"; then
    return 2
  fi
  while IFS= read -r case_name; do
    [[ -z "$case_name" ]] && continue
    case_type="$(case_field "$case_name" 2)"
    if [[ "$case_type" == "go-test" ]]; then
      go_package="$(case_field "$case_name" 5)"
      if [[ -z "$go_package" ]]; then
        echo "[$case_name] manifest row must set go_package" >&2
        failed=1
        continue
      fi
      if [[ -z "${go_package_seen[$go_package]:-}" ]]; then
        go_packages+=("$go_package")
        go_package_seen[$go_package]=1
      fi
      go_package_cases[$go_package]+="$case_name"$'\n'
    else
      other_cases+=("$case_name")
    fi
  done < "$cases_file"

  for go_package in "${go_packages[@]}"; do
    local tmpdir batch_cases=()
    tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/parity-validate.go-test.XXXXXX")"
    while IFS= read -r case_name; do
      [[ -z "$case_name" ]] && continue
      batch_cases+=("$case_name")
    done <<<"${go_package_cases[$go_package]}"
    if ! run_go_test_manifest_cases "$go_package" "$tmpdir" "${batch_cases[@]}"; then
      echo "Temp dir: $tmpdir" >&2
      failed=1
    elif (( KEEP_TEMP == 0 )); then
      rm -rf "$tmpdir"
    else
      echo "[go-test:$go_package] temp dir: $tmpdir"
    fi
  done

  for case_name in "${other_cases[@]}"; do
    if ! run_case "$case_name"; then
      failed=1
    fi
  done
  return "$failed"
}

main
