#!/usr/bin/env bash
# LEGACY — DOES NOT CURRENTLY WORK. This script authenticates to dockhand
# with a bearer API token (`Authorization: Bearer dh_...`), but the
# dockhand version running on percival has no API tokens at all — its API
# auth is a session cookie from POST /api/auth/login. Every call this
# script makes to dockhand's REST API will fail to authenticate.
#
# The supported rollout path is now the dockhand git stack + cron polling
# described in deploy/README.md: commit a LINELEADER_VERSION bump in
# deploy/percival/lineleader.env and push; dockhand polls the repo and
# redeploys on its own. Left in place (unmodified) for reference and in
# case dockhand regains token auth in a future version — do not use it as
# written today.
#
# rollout.sh — the second half of a release: bump LINELEADER_VERSION on the
# percival host and redeploy the lineleader stack through dockhand's REST
# API. scripts/release.sh tags a version and hands off to CI, which builds
# and publishes ghcr.io/lineleader/lineleader:<version>; this script is what
# you run once that image exists.
#
# Usage:
#   scripts/rollout.sh <version> [options]
#
#   <version>              version to roll out, e.g. v0.1.0 (must match
#                           ^v[0-9]+\.[0-9]+\.[0-9]+$)
#
# Options (each has an environment-variable equivalent; flags win):
#   --dockhand-url URL     dockhand base URL, e.g. http://percival:3000
#                           (env: DOCKHAND_URL) — required, no default
#   --token TOKEN           dockhand API token (env: DOCKHAND_TOKEN) —
#                           required, no default. Never appears in argv,
#                           stdout, or stderr — see "Token handling" below.
#   --env-id ID             dockhand environment id (env: DOCKHAND_ENV_ID,
#                           default: 1)
#   --stack NAME             dockhand stack name (env: DOCKHAND_STACK,
#                           default: lineleader)
#   --host HOST              SSH host to reach the stack's env file and
#                           running container (env: ROLLOUT_SSH_HOST,
#                           default: percival)
#   --env-file PATH          absolute path, on --host, of the stack's env
#                           file (env: ROLLOUT_ENV_FILE) — required, no
#                           default: guessing wrong would rewrite the wrong
#                           file on a real host.
#   --health-url URL         URL to poll after redeploying (env:
#                           ROLLOUT_HEALTH_URL, default:
#                           https://lineleader.io/healthz)
#   --container NAME         container name to `docker inspect` after
#                           redeploying, to verify the image that actually
#                           landed (env: ROLLOUT_CONTAINER, default:
#                           "<stack>-<stack>-1", e.g. lineleader-lineleader-1)
#   --health-timeout SECS    total time to keep polling --health-url (env:
#                           ROLLOUT_HEALTH_TIMEOUT, default: 90)
#   --health-interval SECS   time between health poll attempts (env:
#                           ROLLOUT_HEALTH_INTERVAL, default: 3)
#   --dry-run                print every ssh/curl command that would run
#                           and make no changes: no remote mutation, no
#                           deploy POST, no health polling, no image
#                           verification. Config/version are still
#                           validated, and the current on-host version is
#                           still read (read-only) so the old -> new line
#                           is accurate.
#
# Steps:
#   1. Validate the version and required configuration.
#   2. Over SSH, read the current LINELEADER_VERSION from --env-file.
#      Fails if the key is absent (that almost certainly means the wrong
#      file was given) rather than appending it.
#   3. Over SSH, back up --env-file (timestamped copy alongside the
#      original) and rewrite only the LINELEADER_VERSION= line in place,
#      byte-preserving everything else.
#   4. POST {dockhand-url}/api/stacks/{stack}/deploy?env={env-id} with
#      Accept: application/json (this is what makes dockhand run the
#      redeploy synchronously and return a single JSON result, instead of
#      handing back a jobId to poll).
#   5. Poll --health-url until it returns HTTP 200 with body exactly "ok",
#      or fail after --health-timeout seconds.
#   6. Over SSH, `docker inspect` --container and confirm the deployed
#      image matches ghcr.io/lineleader/lineleader:<version>.
#
# On any failure at or after step 3 (the env file has already been
# mutated), a rollback hint is printed on stderr: the previous version and
# the ssh command to restore the backup.
#
# Token handling: the dockhand token is passed to curl via
# `curl --config <file>`, where <file> is a mode-600 temp file containing
# `header = "Authorization: Bearer <token>"`. This keeps the token out of
# the process list (a bare `-H "Authorization: Bearer $token"` argument is
# visible via `ps`) and it is removed on exit even if the script fails
# partway through. It is never printed; anywhere it would otherwise appear
# (including --dry-run output) it is redacted as `dh_***`.
set -euo pipefail

REDACTED_TOKEN="dh_***"

usage() {
  cat <<'EOF'
Usage: scripts/rollout.sh <version> [options]

  <version>                version to roll out, e.g. v0.1.0

Options (env var in parens; flags override):
  --dockhand-url URL       (DOCKHAND_URL)        required
  --token TOKEN            (DOCKHAND_TOKEN)      required
  --env-id ID              (DOCKHAND_ENV_ID)     default: 1
  --stack NAME             (DOCKHAND_STACK)      default: lineleader
  --host HOST              (ROLLOUT_SSH_HOST)    default: percival
  --env-file PATH          (ROLLOUT_ENV_FILE)    required
  --health-url URL         (ROLLOUT_HEALTH_URL)  default: https://lineleader.io/healthz
  --container NAME         (ROLLOUT_CONTAINER)   default: <stack>-<stack>-1
  --health-timeout SECS    (ROLLOUT_HEALTH_TIMEOUT)  default: 90
  --health-interval SECS   (ROLLOUT_HEALTH_INTERVAL) default: 3
  --dry-run                print what would run; change nothing
  -h, --help               show this help
EOF
}

require_value() {
  # $1 = flag name, $2 = remaining arg count
  if [[ "$2" -lt 2 ]]; then
    echo "rollout: missing value for $1" >&2
    exit 1
  fi
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "rollout: required command not found: $1" >&2
    exit 1
  fi
}

# --- defaults from environment ---
dockhand_url="${DOCKHAND_URL:-}"
token="${DOCKHAND_TOKEN:-}"
env_id="${DOCKHAND_ENV_ID:-1}"
stack="${DOCKHAND_STACK:-lineleader}"
host="${ROLLOUT_SSH_HOST:-percival}"
env_file="${ROLLOUT_ENV_FILE:-}"
health_url="${ROLLOUT_HEALTH_URL:-https://lineleader.io/healthz}"
container="${ROLLOUT_CONTAINER:-}"
health_timeout="${ROLLOUT_HEALTH_TIMEOUT:-90}"
health_interval="${ROLLOUT_HEALTH_INTERVAL:-3}"
dry_run=0
version=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dockhand-url)
      require_value "$1" "$#"
      dockhand_url="$2"
      shift 2
      ;;
    --token)
      require_value "$1" "$#"
      token="$2"
      shift 2
      ;;
    --env-id)
      require_value "$1" "$#"
      env_id="$2"
      shift 2
      ;;
    --stack)
      require_value "$1" "$#"
      stack="$2"
      shift 2
      ;;
    --host)
      require_value "$1" "$#"
      host="$2"
      shift 2
      ;;
    --env-file)
      require_value "$1" "$#"
      env_file="$2"
      shift 2
      ;;
    --health-url)
      require_value "$1" "$#"
      health_url="$2"
      shift 2
      ;;
    --container)
      require_value "$1" "$#"
      container="$2"
      shift 2
      ;;
    --health-timeout)
      require_value "$1" "$#"
      health_timeout="$2"
      shift 2
      ;;
    --health-interval)
      require_value "$1" "$#"
      health_interval="$2"
      shift 2
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    --*)
      echo "rollout: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
    *)
      if [[ -n "$version" ]]; then
        echo "rollout: unexpected extra argument: $1" >&2
        usage >&2
        exit 1
      fi
      version="$1"
      shift
      ;;
  esac
done

# --- 1. validate version and required config, before any ssh/curl ---
if [[ -z "$version" ]]; then
  echo "rollout: missing required <version> argument" >&2
  usage >&2
  exit 1
fi
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "rollout: invalid version '$version' — expected e.g. v0.1.0" >&2
  usage >&2
  exit 1
fi

missing=()
if [[ -z "$dockhand_url" ]]; then missing+=("--dockhand-url/DOCKHAND_URL"); fi
if [[ -z "$token" ]]; then missing+=("--token/DOCKHAND_TOKEN"); fi
if [[ -z "$env_file" ]]; then missing+=("--env-file/ROLLOUT_ENV_FILE"); fi
if [[ ${#missing[@]} -gt 0 ]]; then
  echo "rollout: missing required configuration: ${missing[*]}" >&2
  usage >&2
  exit 1
fi

if [[ ! "$health_timeout" =~ ^[0-9]+$ || "$health_timeout" -le 0 ]]; then
  echo "rollout: --health-timeout must be a positive integer, got '$health_timeout'" >&2
  exit 1
fi
if [[ ! "$health_interval" =~ ^[0-9]+$ || "$health_interval" -le 0 ]]; then
  echo "rollout: --health-interval must be a positive integer, got '$health_interval'" >&2
  exit 1
fi

# Derived default, computed once (not hardcoded in multiple places).
if [[ -z "$container" ]]; then
  container="${stack}-${stack}-1"
fi

require_cmd ssh
require_cmd curl

# --- token handling: a mode-600 curl config file, never a bare -H arg ---
umask_orig="$(umask)"
umask 077
token_file="$(mktemp)"
umask "$umask_orig"
chmod 600 "$token_file"
printf 'header = "Authorization: Bearer %s"\n' "$token" >"$token_file"

health_body_file="$(mktemp)"

cleanup_files=("$token_file" "$health_body_file")
cleanup() {
  if [[ ${#cleanup_files[@]} -gt 0 ]]; then
    rm -f "${cleanup_files[@]}"
  fi
}
trap cleanup EXIT

mutated=0
backup_file=""
old_version=""

fail() {
  # $1 = message, $2 = optional exit code (default 1)
  echo "rollout: $1" >&2
  if [[ "$mutated" -eq 1 ]]; then
    echo "rollout: the env file on $host has already been rewritten — rollback:" >&2
    echo "rollout:   previous LINELEADER_VERSION was: ${old_version}" >&2
    echo "rollout:   ssh $host \"cp '${backup_file}' '${env_file}'\"" >&2
    echo "rollout:   then redeploy, e.g.:" >&2
    echo "rollout:   scripts/rollout.sh ${old_version} --dockhand-url ${dockhand_url} --token ${REDACTED_TOKEN} --env-id ${env_id} --stack ${stack} --host ${host} --env-file ${env_file}" >&2
  fi
  exit "${2:-1}"
}

# --- 2. read the current LINELEADER_VERSION from the host, over ssh ---
read_cmd="$(printf 'grep -m1 -E %q %q' '^LINELEADER_VERSION=' "$env_file")"
# shellcheck disable=SC2029 # $read_cmd is meant to expand client-side: it's
# already a fully quoted (printf %q) remote command string, sent to ssh as
# one literal argument.
old_line="$(ssh "$host" "$read_cmd")" && ssh_status=0 || ssh_status=$?
if [[ $ssh_status -eq 255 ]]; then
  fail "ssh to $host failed (exit 255) — check the host is reachable and your key is loaded"
elif [[ $ssh_status -eq 2 ]]; then
  fail "env file could not be read at $env_file on $host"
elif [[ $ssh_status -eq 1 ]]; then
  # File exists but LINELEADER_VERSION not found
  old_line=""
elif [[ $ssh_status -ne 0 ]]; then
  # Unexpected exit code
  fail "unexpected error reading from $host (exit $ssh_status)"
fi
if [[ -z "$old_line" ]]; then
  fail "LINELEADER_VERSION not found in $env_file on $host — refusing to append it (this probably isn't the right env file)"
fi
old_version="${old_line#LINELEADER_VERSION=}"

echo "==> LINELEADER_VERSION: ${old_version} -> ${version}"
if [[ "$old_version" == "$version" ]]; then
  echo "==> already at ${version}; redeploying anyway"
fi

if [[ "$dry_run" -eq 1 ]]; then
  timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
  dry_backup_file="${env_file}.bak.${timestamp}"
  write_cmd="$(printf 'cp %q %q && sed -i -E %q %q' "$env_file" "$dry_backup_file" "s/^LINELEADER_VERSION=.*/LINELEADER_VERSION=${version}/" "$env_file")"
  deploy_url="${dockhand_url}/api/stacks/${stack}/deploy?env=${env_id}"
  deploy_body='{"pull":true,"build":false,"forceRecreate":true}'
  inspect_cmd="$(printf 'docker inspect --format %q %q' '{{.Config.Image}}' "$container")"

  echo "DRY-RUN: ssh $host \"$write_cmd\""
  echo "DRY-RUN: curl -X POST '$deploy_url' -H 'Accept: application/json' -H 'Content-Type: application/json' -H 'Authorization: Bearer ${REDACTED_TOKEN}' --data '$deploy_body'"
  echo "DRY-RUN: poll $health_url every ${health_interval}s up to ${health_timeout}s for HTTP 200 body 'ok'"
  echo "DRY-RUN: ssh $host \"$inspect_cmd\""
  echo
  echo "==> DRY RUN complete; no changes were made."
  exit 0
fi

# --- 3. back up, then rewrite the LINELEADER_VERSION= line in place ---
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_file="${env_file}.bak.${timestamp}"
write_cmd="$(printf 'cp %q %q && sed -i -E %q %q' "$env_file" "$backup_file" "s/^LINELEADER_VERSION=.*/LINELEADER_VERSION=${version}/" "$env_file")"
# shellcheck disable=SC2029 # see the note on the read_cmd ssh call above.
if ! ssh "$host" "$write_cmd"; then
  fail "failed to rewrite LINELEADER_VERSION in $env_file on $host"
fi
mutated=1
echo "==> backed up $env_file to $backup_file on $host"

# --- 4. trigger a synchronous redeploy through dockhand ---
deploy_url="${dockhand_url}/api/stacks/${stack}/deploy?env=${env_id}"
deploy_body='{"pull":true,"build":false,"forceRecreate":true}'
echo "==> POST $deploy_url"
if ! deploy_response="$(curl --config "$token_file" --fail-with-body -sS \
  -H "Accept: application/json" -H "Content-Type: application/json" \
  -X POST --data "$deploy_body" "$deploy_url")"; then
  fail "dockhand deploy request failed: ${deploy_response}"
fi
echo "==> dockhand accepted the redeploy"

# --- 5. poll the health endpoint ---
echo "==> polling $health_url (every ${health_interval}s, up to ${health_timeout}s)"
deadline=$((SECONDS + health_timeout))
healthy=0
while [[ "$SECONDS" -lt "$deadline" ]]; do
  code="$(curl -sS -o "$health_body_file" -w '%{http_code}' "$health_url" 2>/dev/null || echo 000)"
  if [[ "$code" == "200" ]] && [[ "$(cat "$health_body_file")" == "ok" ]]; then
    healthy=1
    break
  fi
  sleep "$health_interval"
done
if [[ "$healthy" -ne 1 ]]; then
  fail "health check at $health_url never returned 200 'ok' within ${health_timeout}s"
fi
echo "==> $health_url is healthy"

# --- 6. verify the deployed image ---
# This step can fail for reasons that have nothing to do with whether the
# rollout worked: the ssh connection can drop, or the remote user may not
# be permitted to run docker at all (true on percival today — the deploy
# user isn't in the docker group and passwordless sudo isn't available).
# Steps 4 and 5 already proved the deploy landed and the app is healthy,
# so an unrunnable verification must not report the rollout as failed.
# Only a verification that *ran* and found a mismatch is a real failure.
inspect_cmd="$(printf 'docker inspect --format %q %q' '{{.Config.Image}}' "$container")"
expected_image="ghcr.io/lineleader/lineleader:${version}"
manual_verify_hint="ssh $host \"docker inspect --format '{{.Config.Image}}' $container\""
# shellcheck disable=SC2029 # see the note on the read_cmd ssh call above.
inspect_output="$(ssh "$host" "$inspect_cmd" 2>&1)" && inspect_status=0 || inspect_status=$?

image_verified=0
deployed_image=""

if [[ $inspect_status -eq 0 ]]; then
  deployed_image="$(echo "$inspect_output" | tr -d '[:space:]')"
  if [[ "$deployed_image" != "$expected_image" ]]; then
    fail "deployed image mismatch on $container: expected $expected_image, got $deployed_image"
  fi
  image_verified=1
elif [[ $inspect_status -eq 255 ]]; then
  echo "rollout: WARNING: could not verify the deployed image — ssh to $host failed (exit 255) while running docker inspect on $container." >&2
  echo "rollout: WARNING: the deploy (step 4) and health check (step 5) already succeeded; this warning only concerns the final image-verification step." >&2
  echo "rollout: WARNING: verify manually with: $manual_verify_hint" >&2
elif echo "$inspect_output" | grep -qE 'permission denied|Cannot connect to the Docker daemon|command not found'; then
  echo "rollout: WARNING: could not verify the deployed image on $host — the SSH user is not permitted to run docker there:" >&2
  echo "rollout: WARNING:   $inspect_output" >&2
  echo "rollout: WARNING: the deploy (step 4) and health check (step 5) already succeeded; this warning only concerns the final image-verification step." >&2
  echo "rollout: WARNING: verify manually with: $manual_verify_hint" >&2
else
  fail "failed to inspect container $container on $host: $inspect_output"
fi

image_summary="Deployed image:      $deployed_image (verified)"
if [[ "$image_verified" -ne 1 ]]; then
  image_summary="Deployed image:      NOT VERIFIED — expected $expected_image; see warning above"
fi

cat <<EOF

==> Rollout of $version complete.

$image_summary
Health check:        OK ($health_url)
Env file backup:     $backup_file (on $host)
EOF
