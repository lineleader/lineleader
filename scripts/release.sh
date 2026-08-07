#!/usr/bin/env bash
# release.sh — tag a version and hand off to CI, which builds and publishes
# the image (this script never builds or pushes Docker images itself; see
# .github/workflows/release.yml).
#
# Usage:
#   scripts/release.sh <version>
#
#   <version>   the version tag to release, e.g. v0.1.0 (must match
#               ^v[0-9]+\.[0-9]+\.[0-9]+$)
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/release.sh <version>

  <version>   version tag to release, e.g. v0.1.0
EOF
}

if [[ $# -ne 1 ]]; then
  echo "release: missing required <version> argument" >&2
  usage >&2
  exit 1
fi

version="$1"

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release: invalid version '$version' — expected e.g. v0.1.0" >&2
  usage >&2
  exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
  echo "release: working tree is dirty; commit or stash changes before releasing" >&2
  exit 1
fi

if git rev-parse "$version" >/dev/null 2>&1; then
  echo "release: tag $version already exists" >&2
  exit 1
fi

git tag -a "$version" -m "Release $version"
git push origin "$version"

cat <<EOF

==> Tag $version pushed to origin.

GitHub Actions is now building and publishing:
    ghcr.io/lineleader/lineleader:$version

Watch the build with:
    gh run watch

Once it finishes, update the image tag in dockhand on percival to:
    ghcr.io/lineleader/lineleader:$version
EOF
