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
  pull-basic       Validates source and target symbol candidate reports against
                   checked-in fixture/golden output.
  dtmf-tool-error  Validates the beta DTMF tool invalid-event behavior through
                   the existing Go package test command.
  address-confirmation-default
                   Validates that address capture asks for confirmation by
                   default, matching the reference audio behavior.
  email-confirmation-default
                   Validates that email capture asks for confirmation by
                   default, matching the reference audio behavior.
  phone-confirmation-default
                   Validates that phone number capture asks for confirmation by
                   default, matching the reference audio behavior.
EOF
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_ROOT="$REPO_ROOT/scripts/parity-fixtures/cases"
KEEP_TEMP=0
declare -a REQUESTED_CASES=()
declare -a ALL_CASES=("pull-basic" "dtmf-tool-error" "address-confirmation-default" "email-confirmation-default" "phone-confirmation-default")

while (($#)); do
  case "$1" in
    --case)
      REQUESTED_CASES+=("${2:-}")
      shift 2
      ;;
    --list)
      printf '%s\n' "${ALL_CASES[@]}"
      exit 0
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
  local requested selected=()
  for requested in "${REQUESTED_CASES[@]}"; do
    case "$requested" in
      all)
        selected+=("${ALL_CASES[@]}")
        ;;
      pull-basic|dtmf-tool-error|address-confirmation-default|email-confirmation-default|phone-confirmation-default)
        selected+=("$requested")
        ;;
      "")
        echo "Error: --case requires a name." >&2
        return 2
        ;;
      *)
        echo "Error: unknown case: $requested" >&2
        echo "Available cases:" >&2
        printf '  %s\n' "${ALL_CASES[@]}" >&2
        return 2
        ;;
    esac
  done
  printf '%s\n' "${selected[@]}"
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
    dtmf-tool-error|address-confirmation-default|email-confirmation-default|phone-confirmation-default)
      sed -E \
        -e 's/[[:space:]]+/ /g' \
        -e 's/\([0-9.]+s\)/(<duration>)/g' \
        -e 's/ [0-9.]+s$/ <duration>/' \
        "$common" > "$output"
      ;;
    pull-basic)
      cp "$common" "$output"
      ;;
  esac
}

run_pull_basic() {
  local tmpdir="$1"
  local case_dir="$FIXTURE_ROOT/pull-basic"
  local source_dir="$tmpdir/source"
  local target_dir="$tmpdir/target"
  local source_report="$tmpdir/source.csv"
  local target_report="$tmpdir/target.csv"

  cp -R "$case_dir/source" "$source_dir"
  mkdir -p "$target_dir"
  while IFS= read -r -d '' fixture; do
    local rel="${fixture#$case_dir/target-src/}"
    local out="$target_dir/${rel%.fixture}"
    mkdir -p "$(dirname "$out")"
    cp "$fixture" "$out"
  done < <(find "$case_dir/target-src" -type f -name '*.go.fixture' -print0 | sort -z)

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

run_dtmf_tool_error() {
  local tmpdir="$1"
  (
    cd "$REPO_ROOT"
    go test ./core/beta/tools -run TestSendDTMFToolReturnsFailureOutputForInvalidEvent -count=1 -v
  ) > "$tmpdir/actual.raw" 2>&1
}

run_address_confirmation_default() {
  local tmpdir="$1"
  (
    cd "$REPO_ROOT"
    go test ./core/beta/workflows -run TestGetAddressTaskInjectsConfirmToolAfterUpdate -count=1 -v
  ) > "$tmpdir/actual.raw" 2>&1
}

run_email_confirmation_default() {
  local tmpdir="$1"
  (
    cd "$REPO_ROOT"
    go test ./core/beta/workflows -run TestGetEmailTaskInjectsConfirmToolAfterUpdate -count=1 -v
  ) > "$tmpdir/actual.raw" 2>&1
}

run_phone_confirmation_default() {
  local tmpdir="$1"
  (
    cd "$REPO_ROOT"
    go test ./core/beta/workflows -run TestGetPhoneNumberTaskRequiresConfirmation -count=1 -v
  ) > "$tmpdir/actual.raw" 2>&1
}

run_case() {
  local case_name="$1"
  local tmpdir expected actual_norm expected_norm
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/parity-validate.${case_name}.XXXXXX")"
  expected="$FIXTURE_ROOT/$case_name/expected.txt"
  actual_norm="$tmpdir/actual.normalized"
  expected_norm="$tmpdir/expected.normalized"

  if [[ ! -f "$expected" ]]; then
    echo "[$case_name] missing expected output: $expected" >&2
    return 1
  fi

  case "$case_name" in
    pull-basic) run_pull_basic "$tmpdir" ;;
    dtmf-tool-error) run_dtmf_tool_error "$tmpdir" ;;
    address-confirmation-default) run_address_confirmation_default "$tmpdir" ;;
    email-confirmation-default) run_email_confirmation_default "$tmpdir" ;;
    phone-confirmation-default) run_phone_confirmation_default "$tmpdir" ;;
    *) echo "unknown case: $case_name" >&2; return 2 ;;
  esac

  normalize_case "$case_name" "$tmpdir/actual.raw" "$actual_norm" "$tmpdir"
  normalize_common "$expected" "$expected_norm" "$tmpdir"

  if ! diff -u "$expected_norm" "$actual_norm" > "$tmpdir/diff.txt"; then
    echo "[$case_name] fixture output differs from golden." >&2
    echo "Temp dir: $tmpdir" >&2
    cat "$tmpdir/diff.txt" >&2
    return 1
  fi

  echo "[$case_name] ok"
  if (( KEEP_TEMP == 0 )); then
    rm -rf "$tmpdir"
  else
    echo "[$case_name] temp dir: $tmpdir"
  fi
}

main() {
  local case_name failed=0
  local cases_file
  cases_file="$(mktemp "${TMPDIR:-/tmp}/parity-validate.cases.XXXXXX")"
  trap 'rm -f "$cases_file"' RETURN
  if ! resolve_cases > "$cases_file"; then
    return 2
  fi
  while IFS= read -r case_name; do
    [[ -z "$case_name" ]] && continue
    if ! run_case "$case_name"; then
      failed=1
    fi
  done < "$cases_file"
  return "$failed"
}

main
