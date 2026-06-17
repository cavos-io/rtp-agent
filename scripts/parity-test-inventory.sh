#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/parity-test-inventory.sh [--output FILE] [--manifest FILE] [--all]

Options:
  --output FILE    TSV report path. Default: .tmp/parity-test-inventory.tsv
  --manifest FILE  Parity case manifest. Default: scripts/parity-fixtures/test-cases.tsv
  --all            Include tests already represented in the manifest.
  -h, --help       Show help.

Report columns:
  go_package,go_test,test_file,manifest_status,classification,reason

The report is an inventory aid. Manifest inclusion still requires meaningful
reference behavior intent; do not add every discovered Go test by default.
EOF
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="$REPO_ROOT/scripts/parity-fixtures/test-cases.tsv"
OUTPUT="$REPO_ROOT/.tmp/parity-test-inventory.tsv"
INCLUDE_ALL=0

while (($#)); do
  case "$1" in
    --output) OUTPUT="${2:-}"; shift 2 ;;
    --manifest) MANIFEST="${2:-}"; shift 2 ;;
    --all) INCLUDE_ALL=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ ! -f "$MANIFEST" ]]; then
  echo "manifest not found: $MANIFEST" >&2
  exit 1
fi

mkdir -p "$(dirname "$OUTPUT")"

manifest_keys="$(mktemp "${TMPDIR:-/tmp}/parity-inventory-manifest.XXXXXX")"
discovered_tests="$(mktemp "${TMPDIR:-/tmp}/parity-inventory-tests.XXXXXX")"
trap 'rm -f "$manifest_keys" "$discovered_tests"' EXIT

awk -F '\t' 'NR > 1 && $5 != "" && $6 != "" {
  print $5 "\t" $6
}' "$MANIFEST" | sort -u > "$manifest_keys"

find "$REPO_ROOT" \
  \( -path "$REPO_ROOT/.agents" -o \
     -path "$REPO_ROOT/.git" -o \
     -path "$REPO_ROOT/.tmp" -o \
     -path "$REPO_ROOT/refs" -o \
     -path "$REPO_ROOT/worktrees" -o \
     -path "$REPO_ROOT/vendor" \) -prune -o \
  -type f -name '*_test.go' -print0 |
  sort -z |
  while IFS= read -r -d '' file; do
    rel="${file#$REPO_ROOT/}"
    dir="${rel%/*}"
    if [[ "$dir" == "$rel" ]]; then
      go_package="."
    else
      go_package="./$dir"
    fi
    awk -v pkg="$go_package" -v rel="$rel" '
      /^[[:space:]]*func[[:space:]]+Test[A-Za-z0-9_]+[[:space:]]*\([[:space:]]*t[[:space:]]+\*testing\.T[[:space:]]*\)/ {
        line=$0
        sub(/^[[:space:]]*func[[:space:]]+/, "", line)
        sub(/[[:space:]]*\(.*/, "", line)
        print pkg "\t" line "\t" rel
      }
    ' "$file"
  done | sort -u > "$discovered_tests"

manifest_has() {
  local pkg="$1" test_name="$2"
  grep -Fqx -- "$pkg"$'\t'"$test_name" "$manifest_keys"
}

extract_test_body() {
  local file="$1" test_name="$2"
  awk -v test_name="$test_name" '
    $0 ~ "^[[:space:]]*func[[:space:]]+" test_name "[[:space:]]*\\(" {
      in_test=1
    }
    in_test && NR != start && $0 ~ /^[[:space:]]*func[[:space:]]+Test[A-Za-z0-9_]+[[:space:]]*\(/ {
      if (seen) exit
    }
    in_test {
      seen=1
      print
    }
  ' "$file"
}

classify_missing() {
  local pkg="$1" test_name="$2" rel="$3"
  local file="$REPO_ROOT/$rel"
  local body lowered name_lower
  body="$(extract_test_body "$file" "$test_name")"
  lowered="$(printf '%s\n%s\n%s\n' "$pkg" "$test_name" "$body" | tr '[:upper:]' '[:lower:]')"
  name_lower="$(printf '%s' "$test_name" | tr '[:upper:]' '[:lower:]')"

  if [[ "$lowered" == *"refs/agents/livekit-agents"* || "$lowered" == *"reference"* || "$lowered" == *"livekit"* ]]; then
    printf 'reference-parity\ttest body or name explicitly references LiveKit/reference behavior'
    return
  fi

  case "$rel" in
    scripts/*|cmd/*|interface/cli/*|interface/worker/ipc/*)
      printf 'infrastructure\ttest is in CLI/script/IPC infrastructure surface'
      return
      ;;
  esac

  if [[ "$name_lower" == *"config"* || "$name_lower" == *"env"* || "$name_lower" == *"server"* || "$name_lower" == *"http"* || "$name_lower" == *"command"* || "$name_lower" == *"discover"* ]]; then
    printf 'infrastructure\ttest name indicates configuration, server, command, or discovery infrastructure'
    return
  fi

  if [[ "$name_lower" == *"error"* || "$name_lower" == *"invalid"* || "$name_lower" == *"reject"* || "$name_lower" == *"default"* || "$name_lower" == *"cancel"* || "$name_lower" == *"timeout"* || "$name_lower" == *"preserve"* || "$name_lower" == *"serialize"* || "$name_lower" == *"parse"* || "$name_lower" == *"stream"* || "$name_lower" == *"fallback"* || "$name_lower" == *"event"* || "$name_lower" == *"tool"* ]]; then
    printf 'target-regression\ttest protects observable target behavior but lacks explicit reference evidence'
    return
  fi

  if [[ "$test_name" =~ ^Test[A-Z][A-Za-z0-9_]*(Helper|Internal|Private|State|Queue|Buffer|Cache|Pool|Map|Clone|Copy|Sort|Merge|Split|Retry|Validate|Normalize) ]]; then
    printf 'implementation-detail\ttest appears focused on a local helper/data-structure behavior'
    return
  fi

  printf 'unknown\tmanual review needed before deciding manifest inclusion'
}

discovered_count="$(wc -l < "$discovered_tests" | tr -d ' ')"
manifest_count=0
missing_count=0
declare -A class_counts=(
  ["reference-parity"]=0
  ["target-regression"]=0
  ["infrastructure"]=0
  ["implementation-detail"]=0
  ["unknown"]=0
)

{
  printf 'go_package\tgo_test\ttest_file\tmanifest_status\tclassification\treason\n'
  while IFS=$'\t' read -r pkg test_name rel; do
    [[ -z "${pkg:-}" || -z "${test_name:-}" ]] && continue
    if manifest_has "$pkg" "$test_name"; then
      ((manifest_count+=1))
      if (( INCLUDE_ALL == 1 )); then
        printf '%s\t%s\t%s\tpresent\t-\talready represented in manifest\n' "$pkg" "$test_name" "$rel"
      fi
      continue
    fi

    ((missing_count+=1))
    class_and_reason="$(classify_missing "$pkg" "$test_name" "$rel")"
    classification="${class_and_reason%%$'\t'*}"
    ((class_counts[$classification]+=1))
    printf '%s\t%s\t%s\tmissing\t%s\n' "$pkg" "$test_name" "$rel" "$class_and_reason"
  done < "$discovered_tests"
} > "$OUTPUT"

{
  echo "Wrote $OUTPUT"
  echo "Go tests discovered: $discovered_count"
  echo "Already in manifest: $manifest_count"
  echo "Missing from manifest: $missing_count"
  echo "Classification counts:"
  printf '  reference-parity: %d\n' "${class_counts[reference-parity]}"
  printf '  target-regression: %d\n' "${class_counts[target-regression]}"
  printf '  infrastructure: %d\n' "${class_counts[infrastructure]}"
  printf '  implementation-detail: %d\n' "${class_counts[implementation-detail]}"
  printf '  unknown: %d\n' "${class_counts[unknown]}"
} >&2
