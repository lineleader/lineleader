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

Once it finishes, roll it out by bumping the version in the committed
env file and pushing:

    \$EDITOR deploy/percival/lineleader.env   # LINELEADER_VERSION=$version
    git commit -am "chore(deploy): release $version"
    git push

dockhand polls the lineleader git stack on a cron and redeploys once it
sees the new commit on main — no dockhand-side action needed. See
deploy/README.md for the full flow, including a gotcha: if
LINELEADER_VERSION is also set as a dockhand stack variable, that value
overrides the repo file and this push will appear to do nothing.

(deploy/percival/docker-compose.yml interpolates LINELEADER_VERSION into
the image tag, so the compose file itself never needs editing.)
EOF
