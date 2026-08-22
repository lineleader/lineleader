#!/usr/bin/env bash
# Regression test for the /state permission-denied bug (500s from
# POST /filters/resorts/{code} and POST /filters/roomtypes/{name} on
# lineleader.io).
#
# Root cause: the runtime image runs as USER nonroot:nonroot (uid/gid
# 65532, distroless/static-debian12:nonroot) and deploy/percival/
# docker-compose.yml mounts a *named volume* at /state for config.json.
# Docker only special-cases a named volume's ownership on first use: if
# the mount point does not already exist inside the image, Docker creates
# it fresh as root:root 0755, and the nonroot process can never write
# there (dvc.SaveConfig fails, and handlers.toggleResortFilter turns that
# into an HTTP 500). If the mount point already exists in the image,
# Docker "copies up" that directory (including its ownership) into a
# brand-new or still-empty volume instead.
#
# So the fix is for the image to contain an empty, nonroot-owned /state
# directory. This test builds the real repo Dockerfile and proves the
# resulting image's runtime user can actually write into /state once
# Docker has initialised the volume — for both:
#   1. a brand-new named volume (Docker has never seen it before), and
#   2. a pre-existing EMPTY root-owned named volume — this is production's
#      current state, since every write so far has failed with EACCES and
#      never created anything, so the volume that's already there on
#      percival is still empty and root-owned.
#
# The final image is distroless (no shell, no coreutils) so we can't just
# `docker run --entrypoint sh ... touch /state/probe`. Instead we use the
# standard trick: after letting the real image's container start (which
# triggers Docker's volume copy-up/initialisation of the mount point),
# mount the *same* volume into a busybox container running as the same
# uid:gid (-u 65532:65532) and have busybox do the write/read/stat probe.
#
# Not a testing framework: fail() prints a diagnostic and exits 1
# immediately, mirroring scripts/rollout_test.sh / scripts/seed-ledger_test.sh.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

IMAGE_TAG="lineleader-image-state-test:$$"
VOL_FRESH="lineleader_state_test_fresh_$$"
VOL_PREEXISTING="lineleader_state_test_preexisting_$$"
PROBE_CONTAINER_PREFIX="lineleader-state-test-probe-$$"

fail() {
  echo "image_state_test.sh: FAILED — $*" >&2
  exit 1
}

skip() {
  echo "image_state_test.sh: SKIPPED — $*" >&2
  exit 0
}

if ! command -v docker >/dev/null 2>&1; then
  skip "docker not found"
fi
if ! docker info >/dev/null 2>&1; then
  skip "docker daemon not reachable"
fi

built_image=0
cleanup() {
  # Runs under an EXIT trap; every branch is explicit and it always
  # `return 0`s so it never clobbers an already-decided exit status.
  docker rm -f "${PROBE_CONTAINER_PREFIX}-fresh" >/dev/null 2>&1 || true
  docker rm -f "${PROBE_CONTAINER_PREFIX}-preexisting" >/dev/null 2>&1 || true
  docker rm -f "${PROBE_CONTAINER_PREFIX}-app-fresh" >/dev/null 2>&1 || true
  docker rm -f "${PROBE_CONTAINER_PREFIX}-app-preexisting" >/dev/null 2>&1 || true
  docker volume rm -f "$VOL_FRESH" >/dev/null 2>&1 || true
  docker volume rm -f "$VOL_PREEXISTING" >/dev/null 2>&1 || true
  if [[ "$built_image" -eq 1 ]]; then
    docker rmi -f "$IMAGE_TAG" >/dev/null 2>&1 || true
  fi
  return 0
}
trap cleanup EXIT

echo "==> building $IMAGE_TAG from $REPO_ROOT/Dockerfile" >&2
if ! docker build -t "$IMAGE_TAG" "$REPO_ROOT" >&2; then
  fail "docker build failed"
fi
built_image=1

echo "==> pulling busybox (probe image)" >&2
docker pull busybox >&2 || fail "could not pull busybox for the write probe"

# assert_volume_writable_by_nonroot NAME LABEL
#
# Starts the real app image against the named volume NAME (this is what
# triggers Docker's volume init/copy-up from the image's /state
# directory), then mounts the same volume into a busybox container
# running as uid:gid 65532:65532 (matching the distroless "nonroot" user)
# and asserts it can write a file into /state and that the volume root is
# owned by 65532.
assert_volume_writable_by_nonroot() {
  local vol="$1" label="$2"
  local app_container="${PROBE_CONTAINER_PREFIX}-app-${label}"
  local probe_container="${PROBE_CONTAINER_PREFIX}-${label}"

  # Start (and immediately stop) the real image against the volume so
  # Docker performs its normal mount-point initialisation of $vol from
  # the image's /state directory, exactly as it does on a real deploy.
  # No Postgres/AUTH_SECRET is provided — the server may fail to become
  # healthy or may exit, and that's fine; volume init happens at
  # container-create/start time regardless of what the app does next.
  docker run -d --name "$app_container" -v "${vol}:/state" "$IMAGE_TAG" >/dev/null 2>&1 || true
  sleep 1
  docker rm -f "$app_container" >/dev/null 2>&1 || true

  local owner
  owner="$(docker run --rm -u 65532:65532 -v "${vol}:/state" busybox stat -c '%u:%g' /state 2>&1)"
  if [[ "$owner" != "65532:65532" ]]; then
    fail "[$label] expected volume root /state to be owned by 65532:65532 after image init, got: $owner"
  fi

  local write_out
  if ! write_out="$(docker run --rm -u 65532:65532 -v "${vol}:/state" busybox sh -c 'echo probe > /state/config.json.tmp && cat /state/config.json.tmp' 2>&1)"; then
    fail "[$label] nonroot (65532:65532) write into /state failed: $write_out"
  fi
  if [[ "$write_out" != "probe" ]]; then
    fail "[$label] nonroot write/readback mismatch, got: $write_out"
  fi
}

# ============================================================
echo "== test 1: fresh, never-before-seen named volume (production's exact mount config) ==" >&2
assert_volume_writable_by_nonroot "$VOL_FRESH" "fresh"
echo "== test 1: PASS ==" >&2

# ============================================================
echo "== test 2: pre-existing EMPTY root-owned named volume (production's current state) ==" >&2
docker volume create "$VOL_PREEXISTING" >/dev/null
# Docker creates a brand-new named volume as an empty directory owned by
# root:root — this reproduces the state percival's `state` volume is
# already in (every write so far has failed with EACCES before creating
# anything).
preexisting_owner="$(docker run --rm -u 0:0 -v "${VOL_PREEXISTING}:/state" busybox stat -c '%u:%g' /state 2>&1)"
if [[ "$preexisting_owner" != "0:0" ]]; then
  fail "test setup: expected a freshly-created volume to start root-owned (0:0), got: $preexisting_owner"
fi
assert_volume_writable_by_nonroot "$VOL_PREEXISTING" "preexisting"
echo "== test 2: PASS ==" >&2

echo "image_state_test.sh: PASSED" >&2
