#!/usr/bin/env bash
set -euo pipefail

version="${VERSION:-}"
if [ -z "$version" ]; then
  echo "VERSION is required, for example: make release VERSION=v0.1.0" >&2
  exit 1
fi

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "VERSION must use the project release format vX.Y.Z, got: $version" >&2
  exit 1
fi

root="$(git rev-parse --show-toplevel)"
cd "$root"

if git show-ref --tags --verify --quiet "refs/tags/$version"; then
  echo "tag already exists: $version" >&2
  exit 1
fi

if [ -n "$(git status --porcelain)" ]; then
  echo "worktree must be clean before creating a release" >&2
  git status --short >&2
  exit 1
fi

mapfile -t old_versions < <(
  {
    git grep -h -E '^[[:space:]]*PluginVersion[[:space:]]*=' -- '*.go' ':!refs/**' 2>/dev/null || true
    git grep -h -E '^[[:space:]]*(respeecherAPIVersion|inworldPluginVersion|smallestAIPluginVersion)[[:space:]]*=' -- '*.go' ':!refs/**' 2>/dev/null || true
  } | sed -E 's/.*"([^"]+)".*/\1/' | sort -u
)

if [ "${#old_versions[@]}" -eq 0 ]; then
  echo "no project version constants found" >&2
  exit 1
fi

mapfile -t files < <(
  git ls-files \
    'adapter/**/*.go' \
    'app/**/*.go' \
    'app/*.go' \
    'docs/**/*.md' \
    ':!refs/**'
)

for old_version in "${old_versions[@]}"; do
  for file in "${files[@]}"; do
    [ -f "$file" ] || continue
    OLD_VERSION="$old_version" NEW_VERSION="$version" perl -0pi -e '
      my $old = $ENV{"OLD_VERSION"};
      my $new = $ENV{"NEW_VERSION"};
      if (/rtp-agent|PluginVersion|LiveKit-Plugin|X-LiveKit-Version|livekit-agents-py|respeecherAPIVersion|inworldPluginVersion|smallestAIPluginVersion|registeredVersion|plugin version|reference version/s) {
        s/\Q$old\E/$new/g;
      }
    ' "$file"
  done
done

if git diff --quiet; then
  echo "release version is already $version; no changes to commit" >&2
  exit 1
fi

for old_version in "${old_versions[@]}"; do
  if git grep -n -F "$old_version" -- "${files[@]}" >/tmp/rtp-agent-release-version-leftovers.$$ 2>/dev/null; then
    echo "old project version references remain after update:" >&2
    cat /tmp/rtp-agent-release-version-leftovers.$$ >&2
    rm -f /tmp/rtp-agent-release-version-leftovers.$$
    exit 1
  fi
done
rm -f /tmp/rtp-agent-release-version-leftovers.$$

git add "${files[@]}"
git commit -m "chore(release): $version"
git tag -a "$version" -m "Release $version"

echo "Created release commit and annotated tag $version"
