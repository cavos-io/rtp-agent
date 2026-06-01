#!/usr/bin/env bash
set -Eeuo pipefail

BASE_BRANCH="ramdhan/main"

# Change this if your Codex non-interactive command is different.
# Examples:
#   CODEX_RUN="codex exec"
#   CODEX_RUN="codex"
CODEX_RUN="${CODEX_RUN:-codex exec}"

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

LOG_DIR="$REPO_ROOT/.git/worktree-integrator/logs"
mkdir -p "$LOG_DIR"

LOG_FILE="$LOG_DIR/$(date +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG_FILE") 2>&1

LOCK_FILE="$REPO_ROOT/.git/worktree-integrator.lock"
exec 9>"$LOCK_FILE"

if ! flock -n 9; then
  echo "Another integration is already running. Exiting."
  exit 0
fi

WORKTREES=(
  "worktrees/interface:ramdhan/feature/interface"
  "worktrees/core/agent:ramdhan/feature/core-agent"
  "worktrees/core/llm:ramdhan/feature/core-llm"
  "worktrees/core/stt:ramdhan/feature/core-stt"
  "worktrees/core/tts:ramdhan/feature/core-tts"
  "worktrees/core/vad:ramdhan/feature/core-vad"
  "worktrees/adapter:ramdhan/feature/adapter"
)

is_clean() {
  local path="$1"

  if [[ -n "$(git -C "$path" status --porcelain)" ]]; then
    echo "Dirty worktree detected: $path"
    git -C "$path" status --short
    return 1
  fi
}

assert_all_clean() {
  echo "Checking clean worktrees..."

  is_clean "$REPO_ROOT"

  for item in "${WORKTREES[@]}"; do
    local path="${item%%:*}"
    is_clean "$path"
  done
}

rebase_in_progress() {
  local path="$1"

  [[ -d "$(git -C "$path" rev-parse --git-path rebase-merge)" ]] || \
  [[ -d "$(git -C "$path" rev-parse --git-path rebase-apply)" ]]
}

ask_codex_to_resolve() {
  local path="$1"
  local branch="$2"
  local failed_command="$3"

  local prompt_file="$REPO_ROOT/.git/worktree-integrator/codex-conflict-prompt.md"

  cat > "$prompt_file" <<EOF
We are running an hourly Git worktree integration.

Repository root:
$REPO_ROOT

Base branch:
$BASE_BRANCH

Failing worktree path:
$path

Failing branch:
$branch

Failed command:
$failed_command

Goal:
Resolve the current Git conflict or failed rebase/fast-forward state.

Rules:
- Preserve every original commit message.
- Do not squash.
- Do not create merge commits.
- Prefer rebase + fast-forward only.
- Do not use "git merge --no-ff".
- Do not use "git merge --squash".
- Inspect status with:
  git -C "$path" status
- If this is a rebase conflict:
  1. Resolve conflict markers.
  2. Run formatting/tests if obvious and reasonably scoped.
  3. Stage resolved files.
  4. Continue with:
     git -C "$path" rebase --continue
- If the failure is in the main worktree, inspect:
  git status
- After resolving, verify:
  git -C "$path" status
  git log --oneline --decorate --graph --max-count=20

Return a concise summary of what you changed.
EOF

  echo "Invoking Codex for conflict resolution..."
  echo "Prompt file: $prompt_file"

  # shellcheck disable=SC2086
  $CODEX_RUN "$(cat "$prompt_file")"

  if rebase_in_progress "$path"; then
    echo "Rebase is still in progress after Codex attempt."
    git -C "$path" status
    exit 1
  fi

  if [[ -n "$(git -C "$path" status --porcelain)" ]]; then
    echo "Worktree is still dirty after Codex attempt: $path"
    git -C "$path" status --short
    exit 1
  fi
}

run_or_codex() {
  local path="$1"
  local branch="$2"
  shift 2

  echo
  echo "Running: $*"

  if ! "$@"; then
    echo "Command failed: $*"
    ask_codex_to_resolve "$path" "$branch" "$*"
  fi
}

echo "============================================================"
echo "Hourly worktree integration started at $(date --iso-8601=seconds)"
echo "Repo: $REPO_ROOT"
echo "Base: $BASE_BRANCH"
echo "Log:  $LOG_FILE"
echo "============================================================"

assert_all_clean

git fetch origin

git switch "$BASE_BRANCH"

for item in "${WORKTREES[@]}"; do
  path="${item%%:*}"
  branch="${item##*:}"

  echo
  echo "============================================================"
  echo "Integrating $branch from $path"
  echo "============================================================"

  run_or_codex "$path" "$branch" git -C "$path" rebase "$BASE_BRANCH"

  git switch "$BASE_BRANCH"

  run_or_codex "$REPO_ROOT" "$branch" git merge --ff-only "$branch"
done

echo
echo "============================================================"
echo "Rebasing all worktree branches onto latest $BASE_BRANCH"
echo "============================================================"

for item in "${WORKTREES[@]}"; do
  path="${item%%:*}"
  branch="${item##*:}"

  run_or_codex "$path" "$branch" git -C "$path" rebase "$BASE_BRANCH"
done

echo
echo "Final verification:"
git log --oneline --decorate --graph --max-count=30
git log --merges --oneline "$BASE_BRANCH" --max-count=10 || true
git worktree list

echo
echo "Hourly worktree integration completed at $(date --iso-8601=seconds)"