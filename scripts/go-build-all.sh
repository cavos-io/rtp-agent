#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
mkdir -p "$REPO_ROOT/.tmp/gocache" "$REPO_ROOT/.tmp/gotmp"

export GOCACHE="${GOCACHE:-$REPO_ROOT/.tmp/gocache}"
export TMPDIR="${TMPDIR:-$REPO_ROOT/.tmp/gotmp}"
go build ./...
