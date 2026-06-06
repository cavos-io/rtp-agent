#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/parity-check-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

SOURCE_DIR="$WORKDIR/source"
TARGET_DIR="$WORKDIR/target"
MAP_FILE="$WORKDIR/map.txt"
SOURCE_REPORT="$WORKDIR/source.csv"
TARGET_REPORT="$WORKDIR/target.csv"

mkdir -p "$SOURCE_DIR/pkg" "$TARGET_DIR/core/llm" "$TARGET_DIR/core/llmfoo"

cat > "$SOURCE_DIR/pkg/ref.py" <<'PY'
class Widget:
    pass

def exact_name():
    pass
PY

cat > "$TARGET_DIR/core/llm/types.go" <<'GO'
package llm

type Widget struct{}

const Answer, BackupAnswer = 42, 7

var Global, FallbackGlobal = Widget{}, Widget{}
GO

cat > "$TARGET_DIR/core/llmfoo/misleading.go" <<'GO'
package llmfoo

func ExactName() {}
GO

cat > "$MAP_FILE" <<'MAP'
pkg,core/llm
MAP

bash -n "$ROOT/scripts/parity-check.sh"

"$ROOT/scripts/parity-check.sh" \
  --source-dir "$SOURCE_DIR" \
  --target-dir "$TARGET_DIR" \
  --source-lang python \
  --target-lang go \
  --map-file "$MAP_FILE" \
  --report source \
  --output "$SOURCE_REPORT" >/dev/null

"$ROOT/scripts/parity-check.sh" \
  --source-dir "$SOURCE_DIR" \
  --target-dir "$TARGET_DIR" \
  --source-lang python \
  --target-lang go \
  --map-file "$MAP_FILE" \
  --report target \
  --output "$TARGET_REPORT" >/dev/null

awk -F, 'NF && NF != 12 { printf "%s:%d has %d columns\n", FILENAME, NR, NF; exit 1 }' \
  "$SOURCE_REPORT" "$TARGET_REPORT"

grep -q '"Widget","class"' "$SOURCE_REPORT"
grep -q '"Widget","type"' "$TARGET_REPORT"
grep -q '"Answer","const"' "$TARGET_REPORT"
grep -q '"BackupAnswer","const"' "$TARGET_REPORT"
grep -q '"Global","var"' "$TARGET_REPORT"
grep -q '"FallbackGlobal","var"' "$TARGET_REPORT"
grep -q '"core/llmfoo/misleading.go".*"unmapped_target"' "$TARGET_REPORT"
