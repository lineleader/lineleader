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

Once it finishes, roll it out with:

    scripts/rollout.sh $version --dockhand-url <url> --token <token> --env-file <path>

That bumps LINELEADER_VERSION in the stack's env file on percival over
SSH, redeploys through dockhand's API, waits for /healthz, and verifies
the image that landed. See deploy/README.md for the full flag reference
(--env-file has no default; guessing wrong would rewrite the wrong file).

(deploy/percival/docker-compose.yml interpolates LINELEADER_VERSION into
the image tag, so the compose file itself never needs editing.)
EOF
