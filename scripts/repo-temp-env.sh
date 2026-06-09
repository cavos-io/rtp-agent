#!/usr/bin/env bash

repo_temp_env_root="${REPO_ROOT:-$(git rev-parse --show-toplevel)}"
repo_temp_dir="$repo_temp_env_root/.tmp"

if [[ -L "$repo_temp_dir" ]]; then
  rm -f "$repo_temp_dir"
fi
if [[ -e "$repo_temp_dir" && ! -d "$repo_temp_dir" ]]; then
  echo "Refusing to use non-directory temp path: $repo_temp_dir" >&2
  return 1 2>/dev/null || exit 1
fi

mkdir -p "$repo_temp_dir" "$repo_temp_dir/gotmp" "$repo_temp_dir/gocache"

export GOCACHE="${GOCACHE:-$repo_temp_dir}"
export TMPDIR="${TMPDIR:-$repo_temp_dir/gotmp}"

unset repo_temp_env_root repo_temp_dir
