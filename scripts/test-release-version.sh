#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MAKEFILE="$ROOT/Makefile"
SCRIPT="$ROOT/scripts/release-version.sh"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "$TMPDIR/release-version-test.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

make_version() {
	local major="$1"
	local minor="$2"
	local patch="$3"
	printf 'v%s.%s.%s' "$major" "$minor" "$patch"
}

OLD_VERSION="$(make_version 1 2 3)"
NEW_VERSION="$(make_version 9 8 7)"

bash -n "$SCRIPT"

mkdir -p "$WORKDIR/repo/adapter/example" "$WORKDIR/repo/adapter/respeecher" "$WORKDIR/repo/app"
cd "$WORKDIR/repo"
git init -q
git config user.email "release-test@example.com"
git config user.name "Release Test"

cat > adapter/example/plugin.go <<GO
package example

const (
	PluginTitle   = "rtp-agent.plugins.example"
	PluginVersion = "$OLD_VERSION"
	PluginPackage = "rtp-agent.plugins.example"
)
GO

cat > adapter/example/example_test.go <<GO
package example

import "testing"

func TestExamplePluginVersion(t *testing.T) {
	if PluginVersion != "$OLD_VERSION" {
		t.Fatalf("PluginVersion = %q, want reference version $OLD_VERSION", PluginVersion)
	}
}
GO

cat > adapter/respeecher/tts.go <<GO
package respeecher

const respeecherAPIVersion = "$OLD_VERSION"
GO

cat > app/app_test.go <<GO
package app

import "testing"

func TestAppRegistersSLNGPluginMetadata(t *testing.T) {
	registeredVersion := "$OLD_VERSION"
	if registeredVersion != "$OLD_VERSION" {
		t.Fatalf("plugin version = %q, want reference version", registeredVersion)
	}
}
GO

git add .
git commit -q -m "seed release fixture"

make -f "$MAKEFILE" release VERSION="$NEW_VERSION" RELEASE_SCRIPT="$SCRIPT" >"$WORKDIR/release.out" 2>"$WORKDIR/release.err"

grep -q "PluginVersion = \"$NEW_VERSION\"" adapter/example/plugin.go
grep -q "want reference version $NEW_VERSION" adapter/example/example_test.go
grep -q "respeecherAPIVersion = \"$NEW_VERSION\"" adapter/respeecher/tts.go
grep -q "registeredVersion := \"$NEW_VERSION\"" app/app_test.go

if rg -q "$OLD_VERSION" .; then
	echo "release script left old version references behind" >&2
	rg -n "$OLD_VERSION" . >&2
	exit 1
fi

if [ -n "$(git status --porcelain)" ]; then
	echo "release script left the worktree dirty" >&2
	git status --short >&2
	exit 1
fi

if [ "$(git log -1 --format=%s)" != "chore(release): $NEW_VERSION" ]; then
	echo "release commit subject did not match expected format" >&2
	git log -1 --format=%s >&2
	exit 1
fi

tag_target="$(git rev-list -n 1 "$NEW_VERSION")"
head_commit="$(git rev-parse HEAD)"
if [ "$tag_target" != "$head_commit" ]; then
	echo "release tag does not point at HEAD" >&2
	exit 1
fi

if [ "$(git cat-file -t "$NEW_VERSION")" != "tag" ]; then
	echo "release tag must be annotated" >&2
	exit 1
fi

tag_object="$(git rev-parse "$NEW_VERSION^{tag}")"
if make -f "$MAKEFILE" release VERSION="$NEW_VERSION" RELEASE_SCRIPT="$SCRIPT" >"$WORKDIR/release-existing-tag.out" 2>"$WORKDIR/release-existing-tag.err"; then
	echo "release script unexpectedly overwrote an existing tag" >&2
	exit 1
fi
grep -q "tag already exists: $NEW_VERSION" "$WORKDIR/release-existing-tag.err"
if [ "$(git rev-parse "$NEW_VERSION^{tag}")" != "$tag_object" ]; then
	echo "release script replaced an existing tag object" >&2
	exit 1
fi
