#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
mkdir -p "$REPO_ROOT/.tmp" "$REPO_ROOT/.tmp/gotmp"

export GOCACHE="${GOCACHE:-$REPO_ROOT/.tmp}"
export TMPDIR="${TMPDIR:-$REPO_ROOT/.tmp/gotmp}"
go test ./...
