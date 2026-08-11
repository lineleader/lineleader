#!/usr/bin/env bash
# Offline test harness for scripts/rollout.sh.
#
# This never touches a real network or a real host. It puts a temp
# directory of stub executables (ssh, curl, docker) at the front of PATH so
# every shell-out rollout.sh makes is intercepted:
#
#   - the `ssh` stub logs its argv to $SSH_LOG and then runs the remote
#     command it was given locally, via `bash -c`, against a *local* copy
#     of scripts/testdata/rollout_env_fixture that stands in for "the file
#     on the host" — this is what lets us assert the byte-preserving
#     rewrite without a real host.
#   - the `curl` stub logs its argv to $CURL_LOG, and branches on the
#     request URL: a dockhand deploy URL gets a canned JSON response (or a
#     simulated failure via CURL_DEPLOY_FAIL), the health URL gets a
#     canned status/body driven by HEALTH_SUCCESS_AFTER + a counter file.
#     When it sees `--config <file>`, it also copies that file's contents
#     (and permission bits) into $CURL_CONFIG_LOG *before* rollout.sh's
#     cleanup trap deletes it, so token-handling can be asserted.
#   - the `docker` stub (reached indirectly, via the ssh stub's `bash -c`)
#     logs to $DOCKER_LOG and prints $DOCKER_INSPECT_IMAGE.
#
# Not a testing framework: fail() prints a diagnostic and exits 1
# immediately, mirroring scripts/seed-ledger_test.sh's style.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROLLOUT="$SCRIPT_DIR/rollout.sh"
FIXTURE="$SCRIPT_DIR/testdata/rollout_env_fixture"

fail() {
  echo "rollout_test.sh: FAILED — $*" >&2
  exit 1
}

check_contains() {
  # $1 = haystack, $2 = needle, $3 = description
  if [[ "$1" != *"$2"* ]]; then
    fail "expected $3 to contain '$2', got:
$1"
  fi
}

check_not_contains() {
  # $1 = haystack, $2 = needle, $3 = description
  if [[ "$1" == *"$2"* ]]; then
    fail "expected $3 to NOT contain '$2', got:
$1"
  fi
}

STUB_DIR="$(mktemp -d)"
WORK_DIR="$(mktemp -d)"

cleanup() {
  # Runs under an EXIT trap; every branch is explicit and it always
  # `return 0`s so it never clobbers an already-decided exit status.
  if [[ -n "$STUB_DIR" ]]; then
    rm -rf "$STUB_DIR"
  fi
  if [[ -n "$WORK_DIR" ]]; then
    rm -rf "$WORK_DIR"
  fi
  return 0
}
trap cleanup EXIT

# --- stub executables ---

cat >"$STUB_DIR/ssh" <<'STUB'
#!/usr/bin/env bash
# $1 = host, $2 = remote command string. Logs the call, then actually runs
# the "remote" command locally (it operates on local stand-in paths passed
# in by the test), simulating the host.
{
  echo "=== ssh call ==="
  for a in "$@"; do printf '%s\n' "$a"; done
} >>"${SSH_LOG:?SSH_LOG not set}"

host="$1"
shift
cmd="$1"

# Allow tests to inject specific ssh exit codes
if [[ "${SSH_INJECT_EXIT:-0}" != "0" ]]; then
  exit "$SSH_INJECT_EXIT"
fi

bash -c "$cmd"
STUB
chmod +x "$STUB_DIR/ssh"

cat >"$STUB_DIR/docker" <<'STUB'
#!/usr/bin/env bash
{
  echo "=== docker call ==="
  for a in "$@"; do printf '%s\n' "$a"; done
} >>"${DOCKER_LOG:?DOCKER_LOG not set}"
echo "${DOCKER_INSPECT_IMAGE:?DOCKER_INSPECT_IMAGE not set}"
STUB
chmod +x "$STUB_DIR/docker"

cat >"$STUB_DIR/curl" <<'STUB'
#!/usr/bin/env bash
{
  echo "=== curl call ==="
  for a in "$@"; do printf '%s\n' "$a"; done
} >>"${CURL_LOG:?CURL_LOG not set}"

o_file=""
config_file=""
prev=""
for a in "$@"; do
  if [[ "$prev" == "-o" ]]; then
    o_file="$a"
  fi
  if [[ "$prev" == "--config" ]]; then
    config_file="$a"
  fi
  prev="$a"
done
url="${*: -1}"

if [[ -n "$config_file" && -f "$config_file" ]]; then
  {
    echo "=== curl --config contents ==="
    perm="$(stat -c %a "$config_file" 2>/dev/null || stat -f %Lp "$config_file" 2>/dev/null)"
    echo "perm:$perm"
    cat "$config_file"
  } >>"${CURL_CONFIG_LOG:?CURL_CONFIG_LOG not set}"
fi

if [[ "$url" == *"/deploy?env="* ]]; then
  if [[ "${CURL_DEPLOY_FAIL:-0}" == "1" ]]; then
    echo '{"error":"boom"}'
    exit 22
  fi
  echo '{"status":"deployed"}'
  exit 0
fi

if [[ "$url" == "${ROLLOUT_HEALTH_URL:-}" ]]; then
  count_file="${HEALTH_COUNT_FILE:?HEALTH_COUNT_FILE not set}"
  n=0
  if [[ -f "$count_file" ]]; then
    n="$(cat "$count_file")"
  fi
  n=$((n + 1))
  echo "$n" >"$count_file"
  success_after="${HEALTH_SUCCESS_AFTER:-1}"
  if [[ "$n" -ge "$success_after" ]]; then
    if [[ -n "$o_file" ]]; then printf 'ok' >"$o_file"; fi
    echo "200"
  else
    if [[ -n "$o_file" ]]; then printf 'not ok' >"$o_file"; fi
    echo "503"
  fi
  exit 0
fi

echo "curl-stub: unrecognized url: $url" >&2
exit 1
STUB
chmod +x "$STUB_DIR/curl"

export PATH="$STUB_DIR:$PATH"

# --- shared config for a "valid" invocation ---

BASE_DOCKHAND_URL="http://dockhand.test:3000"
TEST_TOKEN="dh_supersecrettoken12345"
BASE_HEALTH_URL="http://health.test/healthz"

export DOCKHAND_URL="$BASE_DOCKHAND_URL"
export DOCKHAND_TOKEN="$TEST_TOKEN"
export DOCKHAND_ENV_ID="1"
export DOCKHAND_STACK="lineleader"
export ROLLOUT_SSH_HOST="stubhost"
export ROLLOUT_HEALTH_URL="$BASE_HEALTH_URL"
export ROLLOUT_CONTAINER="lineleader-lineleader-1"
export ROLLOUT_HEALTH_TIMEOUT="5"
export ROLLOUT_HEALTH_INTERVAL="1"

SSH_LOG="$WORK_DIR/ssh.log"
CURL_LOG="$WORK_DIR/curl.log"
DOCKER_LOG="$WORK_DIR/docker.log"
CURL_CONFIG_LOG="$WORK_DIR/curl_config.log"
HEALTH_COUNT_FILE="$WORK_DIR/health_count"
export SSH_LOG CURL_LOG DOCKER_LOG CURL_CONFIG_LOG HEALTH_COUNT_FILE
export HEALTH_SUCCESS_AFTER="1"
export CURL_DEPLOY_FAIL="0"
export DOCKER_INSPECT_IMAGE="ghcr.io/lineleader/lineleader:vunset"

reset_logs() {
  : >"$SSH_LOG"
  : >"$CURL_LOG"
  : >"$DOCKER_LOG"
  : >"$CURL_CONFIG_LOG"
  rm -f "$HEALTH_COUNT_FILE"
}

fresh_env_file() {
  local f
  f="$(mktemp "$WORK_DIR/env.XXXXXX")"
  cp "$FIXTURE" "$f"
  echo "$f"
}

run_rollout() {
  # Runs rollout.sh "$@", capturing combined stdout+stderr into $OUT and
  # exit code into $STATUS. Never lets set -e see the failure.
  if OUT="$("$ROLLOUT" "$@" 2>&1)"; then
    STATUS=0
  else
    STATUS=$?
  fi
}

# ============================================================
echo "== test 1: rejects malformed version =="
for bad_version in "1.2.3" "v1.2" ""; do
  reset_logs
  run_rollout "$bad_version" --env-file "$(fresh_env_file)"
  if [[ "$STATUS" -eq 0 ]]; then
    fail "expected non-zero exit for version '$bad_version', got 0. Output:
$OUT"
  fi
  if [[ -s "$SSH_LOG" ]]; then
    fail "expected no ssh stub invocation for bad version '$bad_version', got:
$(cat "$SSH_LOG")"
  fi
  if [[ -s "$CURL_LOG" ]]; then
    fail "expected no curl stub invocation for bad version '$bad_version', got:
$(cat "$CURL_LOG")"
  fi
  check_contains "$OUT" "rollout:" "error output for version '$bad_version'"
done
echo "== test 1: PASS =="

# ============================================================
echo "== test 2: rejects missing required config =="
reset_logs
run_rollout "v1.0.0" --env-file "$(fresh_env_file)" --dockhand-url ""
# (DOCKHAND_URL exported globally, so pass --dockhand-url "" is not a clean
# unset; use `env -u` instead for a true-missing case below.)

for var in DOCKHAND_URL DOCKHAND_TOKEN; do
  reset_logs
  ef="$(fresh_env_file)"
  if OUT="$(env -u "$var" "$ROLLOUT" "v1.0.0" --env-file "$ef" 2>&1)"; then
    STATUS=0
  else
    STATUS=$?
  fi
  if [[ "$STATUS" -eq 0 ]]; then
    fail "expected non-zero exit when $var is missing, got 0. Output:
$OUT"
  fi
  if [[ -s "$SSH_LOG" || -s "$CURL_LOG" ]]; then
    fail "expected no ssh/curl stub invocation when $var is missing"
  fi
  check_contains "$OUT" "rollout:" "error output when $var missing"
done

reset_logs
run_rollout "v1.0.0"
if [[ "$STATUS" -eq 0 ]]; then
  fail "expected non-zero exit when --env-file/ROLLOUT_ENV_FILE is missing, got 0. Output:
$OUT"
fi
if [[ -s "$SSH_LOG" || -s "$CURL_LOG" ]]; then
  fail "expected no ssh/curl stub invocation when env-file is missing"
fi
check_contains "$OUT" "rollout:" "error output when env-file missing"
echo "== test 2: PASS =="

# ============================================================
echo "== test 3: rewrites only the LINELEADER_VERSION line =="
reset_logs
env_file="$(fresh_env_file)"
export DOCKER_INSPECT_IMAGE="ghcr.io/lineleader/lineleader:v0.2.0"
run_rollout "v0.2.0" --env-file "$env_file"
if [[ "$STATUS" -ne 0 ]]; then
  fail "expected a successful rollout, got exit $STATUS. Output:
$OUT"
fi

new_version_line="$(grep '^LINELEADER_VERSION=' "$env_file")"
if [[ "$new_version_line" != "LINELEADER_VERSION=v0.2.0" ]]; then
  fail "expected LINELEADER_VERSION=v0.2.0 in rewritten file, got '$new_version_line'"
fi

if ! diff <(grep -v '^LINELEADER_VERSION=' "$FIXTURE") <(grep -v '^LINELEADER_VERSION=' "$env_file") >/tmp/rollout_test_diff 2>&1; then
  fail "non-version lines changed:
$(cat /tmp/rollout_test_diff)"
fi
rm -f /tmp/rollout_test_diff

if ! compgen -G "${env_file}.bak.*" >/dev/null; then
  fail "expected a timestamped backup file matching ${env_file}.bak.*"
fi
echo "== test 3: PASS =="

# ------------------------------------------------------------
echo "== test 3b: old == new version still redeploys (bonus) =="
reset_logs
env_file="$(fresh_env_file)"
export DOCKER_INSPECT_IMAGE="ghcr.io/lineleader/lineleader:v0.1.0"
run_rollout "v0.1.0" --env-file "$env_file"
if [[ "$STATUS" -ne 0 ]]; then
  fail "expected old==new redeploy to succeed, got exit $STATUS. Output:
$OUT"
fi
check_contains "$(cat "$CURL_LOG")" "/deploy?env=" "curl log for old==new redeploy"
echo "== test 3b: PASS =="

# ============================================================
echo "== test 4: fails cleanly when LINELEADER_VERSION key is absent =="
reset_logs
no_version_file="$WORK_DIR/no_version_env"
grep -v '^LINELEADER_VERSION=' "$FIXTURE" >"$no_version_file"
cp "$no_version_file" "$WORK_DIR/no_version_before"

run_rollout "v0.2.0" --env-file "$no_version_file"
if [[ "$STATUS" -eq 0 ]]; then
  fail "expected non-zero exit when LINELEADER_VERSION key is absent, got 0. Output:
$OUT"
fi
check_contains "$OUT" "LINELEADER_VERSION" "error output for missing key"

if ! diff -q "$WORK_DIR/no_version_before" "$no_version_file" >/dev/null; then
  fail "env file was mutated despite missing LINELEADER_VERSION key"
fi
if grep -q '^LINELEADER_VERSION=' "$no_version_file"; then
  fail "LINELEADER_VERSION key was appended, but it should never be appended"
fi
echo "== test 4: PASS =="

# ============================================================
echo "== test 5: deploy POST has correct URL, headers, and body =="
reset_logs
env_file="$(fresh_env_file)"
export DOCKER_INSPECT_IMAGE="ghcr.io/lineleader/lineleader:v0.3.0"
run_rollout "v0.3.0" --env-file "$env_file"
if [[ "$STATUS" -ne 0 ]]; then
  fail "expected a successful rollout, got exit $STATUS. Output:
$OUT"
fi

curl_log="$(cat "$CURL_LOG")"
check_contains "$curl_log" "http://dockhand.test:3000/api/stacks/lineleader/deploy?env=1" "curl log (deploy URL)"
check_contains "$curl_log" "Accept: application/json" "curl log (Accept header)"
check_contains "$curl_log" "Content-Type: application/json" "curl log (Content-Type header)"
check_contains "$curl_log" '"pull":true' "curl log (deploy body)"
check_contains "$curl_log" '"build":false' "curl log (deploy body)"
check_contains "$curl_log" '"forceRecreate":true' "curl log (deploy body)"
echo "== test 5: PASS =="

# ============================================================
echo "== test 6: token never leaks; dh_*** appears in --dry-run =="
reset_logs
env_file="$(fresh_env_file)"
export DOCKER_INSPECT_IMAGE="ghcr.io/lineleader/lineleader:v0.4.0"
run_rollout "v0.4.0" --env-file "$env_file"
if [[ "$STATUS" -ne 0 ]]; then
  fail "expected a successful rollout, got exit $STATUS. Output:
$OUT"
fi
check_not_contains "$OUT" "$TEST_TOKEN" "combined stdout+stderr of a full run"
check_not_contains "$(cat "$CURL_LOG")" "$TEST_TOKEN" "curl stub argv log"

config_log="$(cat "$CURL_CONFIG_LOG")"
check_contains "$config_log" "Authorization: Bearer $TEST_TOKEN" "curl --config file contents (token delivery)"
check_contains "$config_log" "perm:600" "curl --config file permission bits"

reset_logs
env_file="$(fresh_env_file)"
run_rollout "v0.4.1" --env-file "$env_file" --dry-run
check_not_contains "$OUT" "$TEST_TOKEN" "--dry-run output"
check_contains "$OUT" "dh_***" "--dry-run output (redacted token)"
echo "== test 6: PASS =="

# ============================================================
echo "== test 7: --dry-run performs no mutation =="
reset_logs
env_file="$(fresh_env_file)"
cp "$env_file" "$WORK_DIR/dry_run_before"
run_rollout "v0.6.0" --env-file "$env_file" --dry-run
if [[ "$STATUS" -ne 0 ]]; then
  fail "expected --dry-run to exit 0, got $STATUS. Output:
$OUT"
fi
if ! diff -q "$WORK_DIR/dry_run_before" "$env_file" >/dev/null; then
  fail "--dry-run mutated the env file"
fi
if compgen -G "${env_file}.bak.*" >/dev/null; then
  fail "--dry-run created a backup file"
fi
if [[ -s "$CURL_LOG" ]]; then
  fail "--dry-run should not invoke curl at all (no deploy POST, no health poll), got:
$(cat "$CURL_LOG")"
fi
check_not_contains "$(cat "$SSH_LOG")" "sed -i" "ssh log during --dry-run (no remote mutation)"
echo "== test 7: PASS =="

# ============================================================
echo "== test 8: health check retries then succeeds =="
reset_logs
env_file="$(fresh_env_file)"
export DOCKER_INSPECT_IMAGE="ghcr.io/lineleader/lineleader:v0.7.0"
export HEALTH_SUCCESS_AFTER="3"
run_rollout "v0.7.0" --env-file "$env_file"
export HEALTH_SUCCESS_AFTER="1"
if [[ "$STATUS" -ne 0 ]]; then
  fail "expected rollout to eventually succeed after retries, got exit $STATUS. Output:
$OUT"
fi
final_count="$(cat "$HEALTH_COUNT_FILE")"
if [[ "$final_count" -lt 3 ]]; then
  fail "expected at least 3 health check attempts, got $final_count"
fi
echo "== test 8: PASS =="

# ============================================================
echo "== test 9: health check timeout =="
reset_logs
env_file="$(fresh_env_file)"
export DOCKER_INSPECT_IMAGE="ghcr.io/lineleader/lineleader:v0.8.0"
export HEALTH_SUCCESS_AFTER="999999"
start_ts=$SECONDS
run_rollout "v0.8.0" --env-file "$env_file" --health-timeout 2 --health-interval 1
elapsed=$((SECONDS - start_ts))
export HEALTH_SUCCESS_AFTER="1"
if [[ "$STATUS" -eq 0 ]]; then
  fail "expected non-zero exit when health check never succeeds, got 0. Output:
$OUT"
fi
if [[ "$elapsed" -gt 10 ]]; then
  fail "health check timeout took too long ($elapsed s) — is --health-timeout wired up?"
fi
check_contains "$OUT" "rollback" "output on post-mutation failure (rollback hint)"
check_contains "$OUT" "v0.1.0" "output on post-mutation failure (previous version in rollback hint)"
echo "== test 9: PASS =="

# ============================================================
echo "== test 10 (bonus): image mismatch fails the rollout =="
reset_logs
env_file="$(fresh_env_file)"
export DOCKER_INSPECT_IMAGE="ghcr.io/lineleader/lineleader:v-wrong-image"
run_rollout "v0.9.0" --env-file "$env_file"
if [[ "$STATUS" -eq 0 ]]; then
  fail "expected non-zero exit on image mismatch, got 0. Output:
$OUT"
fi
check_contains "$OUT" "v-wrong-image" "output on image mismatch"
check_contains "$OUT" "rollback" "output on image mismatch (rollback hint)"
echo "== test 10: PASS =="

# ============================================================
echo "== test 11: ssh connection failure (exit 255) reports distinctly =="
reset_logs
env_file="$(fresh_env_file)"
cp "$env_file" "$WORK_DIR/test11_before"
export SSH_INJECT_EXIT=255
run_rollout "v1.1.0" --env-file "$env_file"
export SSH_INJECT_EXIT=0
if [[ "$STATUS" -eq 0 ]]; then
  fail "expected non-zero exit when ssh fails with 255, got 0. Output:
$OUT"
fi
check_contains "$OUT" "ssh to" "error output for ssh connection failure"
check_contains "$OUT" "exit 255" "error output for ssh connection failure"
check_contains "$OUT" "reachable" "error output for ssh connection failure"
check_not_contains "$OUT" "refusing to append" "error output for ssh connection failure (should NOT confuse with missing key)"
if ! diff -q "$WORK_DIR/test11_before" "$env_file" >/dev/null; then
  fail "env file was mutated on ssh connection failure"
fi
echo "== test 11: PASS =="

# ============================================================
echo "== test 12: grep exit 2 (file missing/unreadable) reports distinctly =="
reset_logs
nonexistent_file="/tmp/rollout_test_nonexistent_$$"
cp "$env_file" "$WORK_DIR/test12_before"
run_rollout "v1.2.0" --env-file "$nonexistent_file"
if [[ "$STATUS" -eq 0 ]]; then
  fail "expected non-zero exit when env file doesn't exist, got 0. Output:
$OUT"
fi
check_contains "$OUT" "env file" "error output for missing env file"
check_contains "$OUT" "could not be read" "error output for missing env file"
if [[ -f "$nonexistent_file" ]]; then
  fail "env file should not have been created on missing remote file"
fi
echo "== test 12: PASS =="

echo "rollout_test.sh: PASSED"
