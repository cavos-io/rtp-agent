#!/usr/bin/env bash

set -euo pipefail

token="${GITHUB_ACCESS_TOKEN:-${GL_ACCESS_TOKEN:-}}"
if [[ -z "$token" ]]; then
	echo "Error: set GITHUB_ACCESS_TOKEN or GL_ACCESS_TOKEN before running this script."
	exit 1
fi

export GOPRIVATE="${GOPRIVATE:-github.com/cavos-io/*}"
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0="url.https://x-access-token:${token}@github.com/.insteadOf"
export GIT_CONFIG_VALUE_0="https://github.com/"

if [[ "$#" -gt 0 ]]; then
	go get "$@"
else
	go get -u ./...
fi

go mod tidy
