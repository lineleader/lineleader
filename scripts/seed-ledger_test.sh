#!/usr/bin/env bash
# Offline-ish integration test for scripts/seed-ledger.sh (lineleader-axr.6:
# full seeder/test alignment, after the CLI became a thin HTTP client).
#
# This boots the real stack — a throwaway Postgres, bin/server, and
# bin/dvc — and runs seed-ledger.sh --fixture against the running server the
# same way a real operator would: over HTTP, via LINELEADER_SERVER/
# LINELEADER_TOKEN. It then asserts the seeded data through `dvc ledger
# show`/`ledger contracts list` — the CLI's own output surface — since that's
# the only thing a client of the hosted ledger can see now that there is no
# database file to query directly.
#
# Coverage lost vs. the pre-Postgres version of this test (which queried a
# local sqlite file directly with python3's sqlite3 module): per-entry
# contract linkage (which entries.contract_id points at which contract row)
# and exact per-kind counts are NOT re-verified here. `dvc ledger show` and
# `ledger contracts list` don't expose contract_id or a kind column, and
# adding that would be new CLI/API surface, out of scope for this change (see
# the now-superseded bd memory this task updates). What IS verified: the
# right number of entries and contracts got created, specific known rows
# (descriptions, tags, contract names/points) show up in the CLI output, and
# the skip/summary counts printed by seed-ledger.sh itself match hand-derived
# expectations for scripts/testdata/ledger_values.json.
#
# Skips cleanly (not a failure) when docker isn't installed/running and
# LEDGER_TEST_DSN isn't set either — same convention as
# internal/ledger.OpenTestDSN.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CONTAINER_NAME="lineleader-seed-test-pg"
DB_PORT=5434
AUTH_SECRET="test-secret"

skip() {
  echo "seed-ledger_test.sh: SKIPPED — $*" >&2
  exit 0
}

fail() {
  echo "seed-ledger_test.sh: FAILED — $*" >&2
  exit 1
}

started_own_db=0
server_pid=""
work_dir=""
seed_out=""

cleanup() {
  # NOTE: this runs under `set -e` as an EXIT trap, so it must never let a
  # false/failing condition become the last command it runs — that would
  # silently replace the real exit code (e.g. skip()'s `exit 0`) with
  # cleanup's own status. Every branch below is an explicit if, and the
  # function ends with `return 0`.
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [[ "$started_own_db" -eq 1 ]]; then
    docker stop "$CONTAINER_NAME" >/dev/null 2>&1 || true
  fi
  if [[ -n "$work_dir" ]]; then
    rm -rf "$work_dir"
  fi
  if [[ -n "$seed_out" ]]; then
    rm -f "$seed_out"
  fi
  return 0
}
trap cleanup EXIT

# --- resolve a test Postgres DSN: reuse LEDGER_TEST_DSN, or start our own ---
test_dsn="${LEDGER_TEST_DSN:-}"

if [[ -z "$test_dsn" ]]; then
  if ! command -v docker >/dev/null 2>&1; then
    skip "docker not found and LEDGER_TEST_DSN not set"
  fi
  if ! docker info >/dev/null 2>&1; then
    skip "docker daemon not reachable and LEDGER_TEST_DSN not set"
  fi
  if docker ps --format '{{.Names}}' 2>/dev/null | grep -qx "$CONTAINER_NAME"; then
    docker stop "$CONTAINER_NAME" >/dev/null 2>&1 || true
  fi

  echo "==> starting a throwaway Postgres ($CONTAINER_NAME) for this test" >&2
  docker run --rm -d --name "$CONTAINER_NAME" -p "${DB_PORT}:5432" \
    -e POSTGRES_PASSWORD=test -e POSTGRES_DB=lineleader_seed_test postgres:17-alpine >/dev/null
  started_own_db=1
  test_dsn="postgres://postgres:test@localhost:${DB_PORT}/lineleader_seed_test?sslmode=disable"

  echo "==> waiting for Postgres to accept connections" >&2
  for _ in $(seq 1 30); do
    if docker exec "$CONTAINER_NAME" pg_isready -U postgres >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  if ! docker exec "$CONTAINER_NAME" pg_isready -U postgres >/dev/null 2>&1; then
    fail "Postgres never became ready in $CONTAINER_NAME"
  fi
else
  echo "==> reusing LEDGER_TEST_DSN" >&2
fi

# --- build bin/server and bin/dvc ---
echo "==> building bin/server and bin/dvc" >&2
make -C "$REPO_ROOT" build dvc >&2

# --- start the server on a free port, pointed at the test DB ---
work_dir="$(mktemp -d)"

port="$(python3 -c 'import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()')"

echo "==> starting bin/server on :$port" >&2
LEDGER_DSN="$test_dsn" AUTH_SECRET="$AUTH_SECRET" \
  "$REPO_ROOT/bin/server" \
  --addr ":$port" \
  --insecure-cookies \
  --data-dir "$REPO_ROOT/data/point-charts" \
  --config "$work_dir/config.json" \
  --plans "$work_dir/plans.json" \
  >"$work_dir/server.log" 2>&1 &
server_pid=$!

server_url="http://localhost:$port"
echo "==> waiting for $server_url/healthz" >&2
ready=0
for _ in $(seq 1 30); do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    cat "$work_dir/server.log" >&2
    fail "bin/server exited before becoming ready"
  fi
  if curl -sf -o /dev/null "$server_url/healthz"; then
    ready=1
    break
  fi
  sleep 1
done
if [[ "$ready" -ne 1 ]]; then
  cat "$work_dir/server.log" >&2
  fail "bin/server never became ready on $server_url"
fi

export LINELEADER_SERVER="$server_url"
export LINELEADER_TOKEN="$AUTH_SECRET"

dvc_bin="$REPO_ROOT/bin/dvc"

# --- baseline counts (the DB may not be empty when LEDGER_TEST_DSN is reused) ---
before_entries="$("$dvc_bin" ledger show | awk 'NR==1{next} /^$/{exit} {c++} END{print c+0}')"
before_contracts="$("$dvc_bin" ledger contracts list | awk 'NR==1{next} {c++} END{print c+0}')"

# --- seed from the fixture (--force: baseline may be non-empty when reusing
# an existing LEDGER_TEST_DSN) and capture its own summary output ---
seed_out="$(mktemp)"
"$SCRIPT_DIR/seed-ledger.sh" --fixture "$SCRIPT_DIR/testdata/ledger_values.json" --force >"$seed_out" 2>&1
cat "$seed_out" >&2

# seed-ledger.sh's own accounting for scripts/testdata/ledger_values.json:
# 29 rows accepted, 3 skipped (hand-verified against the transformation
# rules in seed-ledger.sh's header comment — reproducible with
# `./scripts/seed-ledger.sh --fixture scripts/testdata/ledger_values.json --dry-run`
# against any running server).
if ! grep -q "Entries seeded:    29" "$seed_out"; then
  fail "expected seed-ledger.sh to report 29 seeded entries; got:
$(cat "$seed_out")"
fi
if ! grep -q "Rows skipped:      3" "$seed_out"; then
  fail "expected seed-ledger.sh to report 3 skipped rows; got:
$(cat "$seed_out")"
fi

# --- assert via the CLI's own output surface (see the coverage note above) ---
show_out="$("$dvc_bin" ledger show)"
contracts_out="$("$dvc_bin" ledger contracts list)"

after_entries="$(printf '%s\n' "$show_out" | awk 'NR==1{next} /^$/{exit} {c++} END{print c+0}')"
after_contracts="$(printf '%s\n' "$contracts_out" | awk 'NR==1{next} {c++} END{print c+0}')"

entries_added=$((after_entries - before_entries))
contracts_added=$((after_contracts - before_contracts))

if [[ "$entries_added" -ne 29 ]]; then
  fail "expected 29 new entries, got $entries_added (before=$before_entries after=$after_entries)"
fi
if [[ "$contracts_added" -ne 2 ]]; then
  fail "expected 2 new contracts, got $contracts_added (before=$before_contracts after=$after_contracts)"
fi

check_contains() {
  # $1 = haystack, $2 = needle, $3 = description
  if [[ "$1" != *"$2"* ]]; then
    fail "expected $3 to contain '$2', got:
$1"
  fi
}

check_contains "$show_out" "Point allocation" "ledger show output"
check_contains "$show_out" "Point allocation #2" "ledger show output"
check_contains "$show_out" "HHI 2BR" "ledger show output"
check_contains "$show_out" "One-time use" "ledger show output"
check_contains "$show_out" "Bank" "ledger show output (tag)"
check_contains "$show_out" "Borrow" "ledger show output (tag)"
check_contains "$contracts_out" "Point allocation" "ledger contracts list output"
check_contains "$contracts_out" "150" "ledger contracts list output (points)"
check_contains "$contracts_out" "April" "ledger contracts list output (use year)"

echo "seed-ledger_test.sh: PASSED" >&2
