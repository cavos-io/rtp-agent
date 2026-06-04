#!/usr/bin/env bash
set -euo pipefail

# Generic source-to-target symbol parity report.
#
# This script intentionally avoids project-specific architecture assumptions. It
# extracts symbols from a source tree and a target tree, optionally uses a simple
# mapping file to prefer matches in expected target paths, and writes a CSV that
# humans can annotate with parity_status and notes.

usage() {
  cat <<'EOF'
Usage:
  scripts/parity-check.sh --source-dir DIR --target-dir DIR [options]

Options:
  --source-dir DIR       Source/reference tree to scan. Required.
  --target-dir DIR       Target/implementation tree to scan. Required.
  --output FILE          CSV report path. Default: parity_report.csv
  --source-lang LANG     Source language: python|go. Default: auto
  --target-lang LANG     Target language: python|go. Default: auto
  --map-file FILE        Optional CSV/TSV mapping: source_prefix,target_prefixes
                         target_prefixes may be separated by comma, semicolon,
                         colon, or pipe.
  --report TYPE          Report type: source|target. Default: source
                         source: reference symbols with target candidates.
                         target: target symbol inventory with source candidates.
  --exclude-dir PATH     Exclude a directory path relative to each scan root.
                         May be repeated. Common generated/cache directories
                         are excluded by default.
  --include-tests        Include common test files. Default: excluded.
  -h, --help             Show help.

Examples:
  scripts/parity-check.sh \
    --source-dir path/to/reference \
    --target-dir . \
    --source-lang python \
    --target-lang go \
    --output parity_report.csv

  scripts/parity-check.sh \
    --source-dir path/to/reference \
    --target-dir . \
    --map-file parity-map.csv \
    --report target

Mapping file format:
  # source_prefix,target_prefixes
  reference/worker.py,implementation/worker/
  reference/llm/,core/llm/
  reference/stt/,core/stt/

Source report CSV columns:
  source_file,source_module,source_class,source_symbol,source_type,source_line,
  target_candidate,target_candidate_file,target_candidate_line,match_confidence,
  parity_status,notes

Target report CSV columns:
  target_file,target_module,target_owner,target_symbol,target_type,target_line,
  source_candidate,source_candidate_file,source_candidate_line,match_confidence,
  target_category,notes

match_confidence values:
  name        Exact normalized symbol-name match. If mapping exists, mapped
              target paths are preferred.
  module_path Substring symbol match inside mapped target paths only.
  -           No automated candidate found.
EOF
}

SOURCE_DIR=""
TARGET_DIR=""
OUTPUT="parity_report.csv"
SOURCE_LANG="auto"
TARGET_LANG="auto"
MAP_FILE=""
REPORT="source"
INCLUDE_TESTS=0
declare -a EXCLUDE_DIRS=(
  ".git"
  ".hg"
  ".svn"
  ".tmp"
  "tmp"
  "node_modules"
  "vendor"
  "__pycache__"
  ".venv"
  "venv"
  "dist"
  "build"
  "coverage"
)

while (($#)); do
  case "$1" in
    --source-dir) SOURCE_DIR="${2:-}"; shift 2 ;;
    --target-dir) TARGET_DIR="${2:-}"; shift 2 ;;
    --output) OUTPUT="${2:-}"; shift 2 ;;
    --source-lang) SOURCE_LANG="${2:-}"; shift 2 ;;
    --target-lang) TARGET_LANG="${2:-}"; shift 2 ;;
    --map-file) MAP_FILE="${2:-}"; shift 2 ;;
    --report) REPORT="${2:-}"; shift 2 ;;
    --exclude-dir) EXCLUDE_DIRS+=("${2:-}"); shift 2 ;;
    --include-tests) INCLUDE_TESTS=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ -z "$SOURCE_DIR" || -z "$TARGET_DIR" ]]; then
  echo "Error: --source-dir and --target-dir are required." >&2
  usage >&2
  exit 2
fi
if [[ ! -d "$SOURCE_DIR" ]]; then
  echo "Error: source dir not found: $SOURCE_DIR" >&2
  exit 1
fi
if [[ ! -d "$TARGET_DIR" ]]; then
  echo "Error: target dir not found: $TARGET_DIR" >&2
  exit 1
fi
if [[ -n "$MAP_FILE" && ! -f "$MAP_FILE" ]]; then
  echo "Error: map file not found: $MAP_FILE" >&2
  exit 1
fi
if [[ "$REPORT" != "source" && "$REPORT" != "target" ]]; then
  echo "Error: unsupported report type: $REPORT (supported: source, target)" >&2
  exit 1
fi

SOURCE_DIR="$(cd "$SOURCE_DIR" && pwd)"
TARGET_DIR="$(cd "$TARGET_DIR" && pwd)"

csv_escape() {
  local value="${1:-}"
  value="${value//\"/\"\"}"
  printf '"%s"' "$value"
}

snake_to_camel() {
  local name="$1"
  # Trim leading/trailing underscores, then capitalize each underscore-separated part.
  name="${name##_}"
  while [[ "$name" == _* ]]; do name="${name#_}"; done
  while [[ "$name" == *_ ]]; do name="${name%_}"; done
  [[ -z "$name" ]] && return 0

  local out="" part first rest
  IFS='_' read -r -a parts <<< "$name"
  for part in "${parts[@]}"; do
    [[ -z "$part" ]] && continue
    first="${part:0:1}"
    rest="${part:1}"
    out+="${first^^}${rest}"
  done
  printf '%s' "$out"
}

lower() {
  local value="${1:-}"
  printf '%s' "${value,,}"
}

trim() {
  local value="${1:-}"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

is_test_path() {
  local rel="$1"
  [[ "$INCLUDE_TESTS" -eq 1 ]] && return 1
  [[ "$rel" == *_test.go ]] && return 0
  [[ "$rel" == test_*.py || "$rel" == *_test.py || "$rel" == tests/* || "$rel" == */tests/* ]] && return 0
  return 1
}

should_skip_dir_path() {
  local rel="$1" excluded
  for excluded in "${EXCLUDE_DIRS[@]}"; do
    excluded="${excluded#/}"
    excluded="${excluded%/}"
    [[ -z "$excluded" ]] && continue
    if [[ "$rel" == "$excluded" || "$rel" == "$excluded"/* || "$rel" == */"$excluded"/* ]]; then
      return 0
    fi
  done
  return 1
}

auto_lang() {
  local dir="$1"
  local py_count go_count
  py_count=$(find "$dir" -type f -name '*.py' 2>/dev/null | head -101 | wc -l | tr -d ' ')
  go_count=$(find "$dir" -type f -name '*.go' 2>/dev/null | head -101 | wc -l | tr -d ' ')
  if (( py_count >= go_count && py_count > 0 )); then
    printf 'python'
  elif (( go_count > 0 )); then
    printf 'go'
  else
    echo "Error: cannot auto-detect language for $dir" >&2
    exit 1
  fi
}

if [[ "$SOURCE_LANG" == "auto" ]]; then SOURCE_LANG="$(auto_lang "$SOURCE_DIR")"; fi
if [[ "$TARGET_LANG" == "auto" ]]; then TARGET_LANG="$(auto_lang "$TARGET_DIR")"; fi

extract_python_symbols() {
  local root="$1"
  find "$root" -type f -name '*.py' -print0 | sort -z | while IFS= read -r -d '' file; do
    local rel="${file#$root/}"
    should_skip_dir_path "$rel" && continue
    is_test_path "$rel" && continue
    local module="${rel%%/*}"
    [[ "$module" == "$rel" ]] && module="${rel%.py}"
    awk -v rel="$rel" -v module="$module" '
      function ltrim(s) { sub(/^[ \t]+/, "", s); return s }
      function indent_of(s,  m) { match(s, /^[ \t]*/); return RLENGTH }
      BEGIN { class_name=""; class_indent=-1; pending_property=0 }
      {
        raw=$0
        sub(/\r$/, "", raw)
        indent=indent_of(raw)
        trimmed=ltrim(raw)

        if (class_name != "" && indent <= class_indent && trimmed !~ /^($|#|@)/) {
          class_name=""; class_indent=-1
        }

        if (trimmed ~ /^@(property|[A-Za-z_][A-Za-z0-9_]*\.property)([ \t]*\(|[ \t]*$)/) {
          pending_property=1
          next
        }

        if (trimmed ~ /^class[ \t]+[A-Za-z_][A-Za-z0-9_]*/) {
          class_name=trimmed
          sub(/^class[ \t]+/, "", class_name)
          sub(/[^A-Za-z0-9_].*$/, "", class_name)
          class_indent=indent
          pending_property=0
          next
        }

        is_async=0
        name=""
        if (trimmed ~ /^async[ \t]+def[ \t]+[A-Za-z_][A-Za-z0-9_]*[ \t]*\(/) {
          name=trimmed
          sub(/^async[ \t]+def[ \t]+/, "", name)
          sub(/[^A-Za-z0-9_].*$/, "", name)
          is_async=1
        } else if (trimmed ~ /^def[ \t]+[A-Za-z_][A-Za-z0-9_]*[ \t]*\(/) {
          name=trimmed
          sub(/^def[ \t]+/, "", name)
          sub(/[^A-Za-z0-9_].*$/, "", name)
        } else {
          next
        }

        if (class_name != "" && indent > class_indent) {
          typ = pending_property ? "property" : (is_async ? "async_method" : "method")
          cls = class_name
        } else if (indent == 0) {
          typ = is_async ? "async_function" : "function"
          cls = ""
        } else {
          pending_property=0
          next
        }

        print rel "|" module "|" cls "|" name "|" typ "|" NR
        pending_property=0
      }
    ' "$file"
  done
}

extract_go_symbols() {
  local root="$1"
  find "$root" -type f -name '*.go' -print0 | sort -z | while IFS= read -r -d '' file; do
    local rel="${file#$root/}"
    should_skip_dir_path "$rel" && continue
    is_test_path "$rel" && continue
    local module="${rel%%/*}"
    [[ "$module" == "$rel" ]] && module="${rel%.go}"
    awk -v rel="$rel" -v module="$module" '
      function ltrim(s) { sub(/^[ \t]+/, "", s); return s }
      function parse_name(s,  name) {
        sub(/^func[ \t]+/, "", s)
        if (s ~ /^\(/) {
          sub(/^\([^)]*\)[ \t]*/, "", s)
        }
        name=s
        sub(/[\[(].*$/, "", name)
        sub(/[ \t].*$/, "", name)
        return name
      }
      function parse_receiver(s,  recv) {
        sub(/^func[ \t]+/, "", s)
        if (s ~ /^\(/) {
          recv=s
          sub(/^\(/, "", recv)
          sub(/\).*/, "", recv)
          return recv
        }
        return ""
      }
      {
        line=ltrim($0)
        if (line ~ /^func[ \t]+(\([^)]*\)[ \t]+)?[A-Za-z_][A-Za-z0-9_]*[ \t]*(\[|\()/) {
          name=parse_name(line)
          recv=parse_receiver(line)
          typ = recv == "" ? "function" : "method"
          print rel "|" module "|" recv "|" name "|" typ "|" NR
        }
      }
    ' "$file"
  done
}

extract_symbols() {
  local lang="$1" dir="$2"
  case "$lang" in
    python) extract_python_symbols "$dir" ;;
    go) extract_go_symbols "$dir" ;;
    *) echo "Error: unsupported language: $lang (supported: python, go)" >&2; exit 1 ;;
  esac
}

declare -a map_sources=() map_targets=()

load_map_file() {
  [[ -z "$MAP_FILE" ]] && return 0

  local line source targets
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%%#*}"
    line="$(trim "$line")"
    [[ -z "$line" ]] && continue

    if [[ "$line" == *$'\t'* ]]; then
      source="${line%%$'\t'*}"
      targets="${line#*$'\t'}"
    elif [[ "$line" == *,* ]]; then
      source="${line%%,*}"
      targets="${line#*,}"
    else
      continue
    fi

    source="$(trim "$source")"
    targets="$(trim "$targets")"
    [[ -z "$source" || -z "$targets" ]] && continue
    map_sources+=("$source")
    map_targets+=("$targets")
  done < "$MAP_FILE"
}

map_paths_for_source() {
  local source_file="$1"
  local i
  for ((i=0; i<${#map_sources[@]}; i++)); do
    if [[ "$source_file" == "${map_sources[$i]}" || "$source_file" == "${map_sources[$i]}"* ]]; then
      printf '%s' "${map_targets[$i]}"
      return 0
    fi
  done
  return 0
}

is_target_in_paths() {
  local target_file="$1" paths_text="$2" p
  [[ -z "$paths_text" ]] && return 1
  IFS=',;:|' read -r -a paths <<< "$paths_text"
  for p in "${paths[@]}"; do
    p="$(trim "$p")"
    [[ -z "$p" ]] && continue
    [[ "$target_file" == "$p"* ]] && return 0
  done
  return 1
}

target_category() {
  local file="$1"

  if [[ ${#map_targets[@]} -gt 0 ]]; then
    local targets p
    for targets in "${map_targets[@]}"; do
      IFS=',;:|' read -r -a paths <<< "$targets"
      for p in "${paths[@]}"; do
        p="$(trim "$p")"
        [[ -z "$p" ]] && continue
        if [[ "$file" == "$p" || "$file" == "$p"* ]]; then
          printf 'mapped_target'
          return 0
        fi
      done
    done
  fi

  case "$file" in
    */*) printf 'unmapped_target' ;;
    *) printf 'root_target' ;;
  esac
}

load_map_file

source_tsv="$(mktemp)"
target_tsv="$(mktemp)"
trap 'rm -f "$source_tsv" "$target_tsv"' EXIT

echo "Extracting $SOURCE_LANG symbols from $SOURCE_DIR ..." >&2
extract_symbols "$SOURCE_LANG" "$SOURCE_DIR" > "$source_tsv"
source_total=$(wc -l < "$source_tsv" | tr -d ' ')
echo "  Found $source_total source symbols" >&2

echo "Extracting $TARGET_LANG symbols from $TARGET_DIR ..." >&2
extract_symbols "$TARGET_LANG" "$TARGET_DIR" > "$target_tsv"
target_total=$(wc -l < "$target_tsv" | tr -d ' ')
echo "  Found $target_total target symbols" >&2

# Load source symbols into arrays.
declare -a source_file=() source_module=() source_owner=() source_symbol=() source_symbol_camel_lower=() source_type=() source_line=()
declare -A source_by_norm=()
idx=0
while IFS='|' read -r file module owner symbol typ line; do
  [[ -z "${file:-}" ]] && continue
  source_file[$idx]="$file"
  source_module[$idx]="$module"
  source_owner[$idx]="$owner"
  source_symbol[$idx]="$symbol"
  source_type[$idx]="$typ"
  source_line[$idx]="$line"
  normalized="$(snake_to_camel "$symbol")"
  [[ -z "$normalized" ]] && normalized="$symbol"
  norm="$(lower "$normalized")"
  source_symbol_camel_lower[$idx]="$norm"
  source_by_norm[$norm]="${source_by_norm[$norm]:-} $idx"
  ((idx+=1))
done < "$source_tsv"
source_count=$idx

# Load target symbols into arrays.
declare -a target_file=() target_module=() target_owner=() target_symbol=() target_symbol_lower=() target_type=() target_line=()
declare -A target_by_norm=()
idx=0
while IFS='|' read -r file module owner symbol typ line; do
  [[ -z "${file:-}" ]] && continue
  target_file[$idx]="$file"
  target_module[$idx]="$module"
  target_owner[$idx]="$owner"
  target_symbol[$idx]="$symbol"
  target_type[$idx]="$typ"
  target_line[$idx]="$line"
  norm="$(lower "$symbol")"
  target_symbol_lower[$idx]="$norm"
  target_by_norm[$norm]="${target_by_norm[$norm]:-} $idx"
  ((idx+=1))
done < "$target_tsv"
target_count=$idx

if [[ "$REPORT" == "source" ]]; then
{
  printf '%s\n' 'source_file,source_module,source_class,source_symbol,source_type,source_line,target_candidate,target_candidate_file,target_candidate_line,match_confidence,parity_status,notes'

  auto_matches=0
  while IFS='|' read -r s_file s_module s_owner s_symbol s_type s_line; do
    [[ -z "${s_file:-}" ]] && continue
    normalized="$(snake_to_camel "$s_symbol")"
    if [[ -z "$normalized" ]]; then
      normalized="$s_symbol"
    fi
    norm_lower="$(lower "$normalized")"
    mapped_paths="$(map_paths_for_source "$s_file" || true)"

    best_idx=""
    confidence="-"

    # Pass 1: exact normalized name match globally; prefer mapped target paths.
    if [[ -n "${target_by_norm[$norm_lower]:-}" ]]; then
      for cand in ${target_by_norm[$norm_lower]}; do
        if [[ -n "$mapped_paths" ]]; then
          IFS=',;:|' read -r -a paths <<< "$mapped_paths"
          for p in "${paths[@]}"; do
            p="$(trim "$p")"
            [[ -z "$p" ]] && continue
            if [[ "${target_file[$cand]}" == "$p"* ]]; then
              best_idx="$cand"
              break 2
            fi
          done
        fi
      done
      if [[ -z "$best_idx" ]]; then
        for cand in ${target_by_norm[$norm_lower]}; do best_idx="$cand"; break; done
      fi
      confidence="name"
    fi

    # Pass 2: substring match, but only inside mapped target paths.
    if [[ -z "$best_idx" && -n "$mapped_paths" && -n "$normalized" ]]; then
      normalized_l="$(lower "$normalized")"
      IFS=',;:|' read -r -a paths <<< "$mapped_paths"
      for ((cand=0; cand<target_count; cand++)); do
        in_path=0
        for p in "${paths[@]}"; do
          p="$(trim "$p")"
          [[ -z "$p" ]] && continue
          if [[ "${target_file[$cand]}" == "$p"* ]]; then
            in_path=1
            break
          fi
        done
        [[ "$in_path" -eq 1 ]] || continue
        target_l="${target_symbol_lower[$cand]}"
        if [[ "$target_l" == *"$normalized_l"* || "$normalized_l" == *"$target_l"* ]]; then
          best_idx="$cand"
          confidence="module_path"
          break
        fi
      done
    fi

    t_symbol=""; t_file=""; t_line=""
    if [[ -n "$best_idx" ]]; then
      t_symbol="${target_symbol[$best_idx]}"
      t_file="${target_file[$best_idx]}"
      t_line="${target_line[$best_idx]}"
      ((auto_matches+=1))
    fi

    csv_escape "$s_file"; printf ','
    csv_escape "$s_module"; printf ','
    csv_escape "$s_owner"; printf ','
    csv_escape "$s_symbol"; printf ','
    csv_escape "$s_type"; printf ','
    csv_escape "$s_line"; printf ','
    csv_escape "$t_symbol"; printf ','
    csv_escape "$t_file"; printf ','
    csv_escape "$t_line"; printf ','
    csv_escape "$confidence"; printf ','
    csv_escape ""; printf ','
    csv_escape ""; printf '\n'
  done < "$source_tsv"

  printf '\n'
  csv_escape "SUMMARY"; printf ','
  csv_escape "Auto-detected coverage"; printf ','
  csv_escape "$auto_matches/$source_total"; printf ','
  if (( source_total > 0 )); then
    csv_escape "$((100 * auto_matches / source_total))%"
  else
    csv_escape "0%"
  fi
  printf ',,,,,,,\n'

  csv_escape "SUMMARY"; printf ','
  csv_escape "Verified coverage (team fills parity_status=exists)"; printf ','
  csv_escape "0/$source_total"; printf ','
  csv_escape "0%"; printf ',,,,,,,\n'
} > "$OUTPUT"

  echo "Writing report to $OUTPUT ..." >&2
  echo "" >&2
  echo "====================================================" >&2
  echo "  Source symbols total :    $source_total" >&2
  echo "  Target symbols total :    $target_total" >&2
  echo "  Auto-detected matches:    $auto_matches/$source_total" >&2
  if (( source_total > 0 )); then
    echo "  Auto coverage        :    $((100 * auto_matches / source_total))%" >&2
  else
    echo "  Auto coverage        :    0%" >&2
  fi
  echo "====================================================" >&2
  echo "" >&2
  echo "Done. Open $OUTPUT to annotate parity_status and notes." >&2
else
{
  printf '%s\n' 'target_file,target_module,target_owner,target_symbol,target_type,target_line,source_candidate,source_candidate_file,source_candidate_line,match_confidence,target_category,notes'

  auto_matches=0
  unmatched=0
  for ((t=0; t<target_count; t++)); do
    norm_lower="${target_symbol_lower[$t]}"
    best_idx=""
    confidence="-"

    # Pass 1: exact target name match against normalized source names. Prefer
    # source rows whose map-file target paths include this target file.
    if [[ -n "${source_by_norm[$norm_lower]:-}" ]]; then
      for cand in ${source_by_norm[$norm_lower]}; do
        mapped_paths="$(map_paths_for_source "${source_file[$cand]}" || true)"
        if is_target_in_paths "${target_file[$t]}" "$mapped_paths"; then
          best_idx="$cand"
          break
        fi
      done
      if [[ -z "$best_idx" ]]; then
        for cand in ${source_by_norm[$norm_lower]}; do best_idx="$cand"; break; done
      fi
      confidence="name"
    fi

    # Pass 2: substring match against sources that map to this target file.
    if [[ -z "$best_idx" && ${#map_sources[@]} -gt 0 ]]; then
      for ((s=0; s<source_count; s++)); do
        mapped_paths="$(map_paths_for_source "${source_file[$s]}" || true)"
        is_target_in_paths "${target_file[$t]}" "$mapped_paths" || continue
        source_l="${source_symbol_camel_lower[$s]}"
        if [[ "$norm_lower" == *"$source_l"* || "$source_l" == *"$norm_lower"* ]]; then
          best_idx="$s"
          confidence="module_path"
          break
        fi
      done
    fi

    s_symbol=""; s_file=""; s_line=""
    if [[ -n "$best_idx" ]]; then
      s_symbol="${source_symbol[$best_idx]}"
      s_file="${source_file[$best_idx]}"
      s_line="${source_line[$best_idx]}"
      ((auto_matches+=1))
    else
      ((unmatched+=1))
    fi

    csv_escape "${target_file[$t]}"; printf ','
    csv_escape "${target_module[$t]}"; printf ','
    csv_escape "${target_owner[$t]}"; printf ','
    csv_escape "${target_symbol[$t]}"; printf ','
    csv_escape "${target_type[$t]}"; printf ','
    csv_escape "${target_line[$t]}"; printf ','
    csv_escape "$s_symbol"; printf ','
    csv_escape "$s_file"; printf ','
    csv_escape "$s_line"; printf ','
    csv_escape "$confidence"; printf ','
    csv_escape "$(target_category "${target_file[$t]}")"; printf ','
    csv_escape ""; printf '\n'
  done

  printf '\n'
  csv_escape "SUMMARY"; printf ','
  csv_escape "Target symbols with source candidates"; printf ','
  csv_escape "$auto_matches/$target_total"; printf ','
  if (( target_total > 0 )); then
    csv_escape "$((100 * auto_matches / target_total))%"
  else
    csv_escape "0%"
  fi
  printf ',,,,,,,,\n'

  csv_escape "SUMMARY"; printf ','
  csv_escape "Target symbols without source candidates"; printf ','
  csv_escape "$unmatched/$target_total"; printf ','
  if (( target_total > 0 )); then
    csv_escape "$((100 * unmatched / target_total))%"
  else
    csv_escape "0%"
  fi
  printf ',,,,,,,,\n'
} > "$OUTPUT"

  echo "Writing report to $OUTPUT ..." >&2
  echo "" >&2
  echo "====================================================" >&2
  echo "  Source symbols total       :    $source_total" >&2
  echo "  Target symbols total       :    $target_total" >&2
  echo "  Target source candidates   :    $auto_matches/$target_total" >&2
  echo "  Target without candidates  :    $unmatched/$target_total" >&2
  if (( target_total > 0 )); then
    echo "  Candidate coverage         :    $((100 * auto_matches / target_total))%" >&2
  else
    echo "  Candidate coverage         :    0%" >&2
  fi
  echo "====================================================" >&2
  echo "" >&2
  echo "Done. Open $OUTPUT to inspect target symbols and annotate notes." >&2
fi
