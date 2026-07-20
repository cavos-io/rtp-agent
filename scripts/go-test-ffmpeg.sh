#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
REPO_TEMP_ENV_FORCE=1
source "$REPO_ROOT/scripts/repo-temp-env.sh"
cd "$REPO_ROOT"

go test -tags ffmpeg ./interface/worker/livekit ./interface/worker
go test -tags ffmpeg ./app -run '^TestRunSessionStartsAudioRecorderForRecordedJob$'
