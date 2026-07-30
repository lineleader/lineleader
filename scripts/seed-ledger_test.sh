#!/usr/bin/env bash
# Offline test for scripts/seed-ledger.sh.
#
# Runs the seeding script with --fixture (no network/gcloud needed) against a
# throwaway sqlite db in a fresh temp dir -- never the caller's real ledger --
# and asserts the transformed rows match hand-verified expectations for the
# fixture data in scripts/testdata/ledger_values.json.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SEED_SCRIPT="$SCRIPT_DIR/seed-ledger.sh"
FIXTURE="$SCRIPT_DIR/testdata/ledger_values.json"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

DB="$TMPDIR/ledger.db"

pass=0
fail=0
failures=()

check() {
  local desc="$1" got="$2" want="$3"
  if [[ "$got" == "$want" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    failures+=("$desc: want [$want] got [$got]")
  fi
}

query() {
  python3 -c "
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
row = con.execute(sys.argv[2]).fetchone()
val = row[0] if row is not None else ''
print('' if val is None else val)
" "$DB" "$1"
}

echo "==> running seed-ledger.sh --fixture against a fresh temp db: $DB"
"$SEED_SCRIPT" --db "$DB" --fixture "$FIXTURE"
echo

contracts_count=$(query "SELECT COUNT(*) FROM contracts")
check "contract count" "$contracts_count" "2"

entries_count=$(query "SELECT COUNT(*) FROM entries")
check "entry count" "$entries_count" "29"

total_allotted=$(query "SELECT COALESCE(SUM(allotted),0) FROM entries")
check "total allotted" "$total_allotted" "2282"

total_used=$(query "SELECT COALESCE(SUM(used),0) FROM entries")
check "total used" "$total_used" "1943"

balance=$((total_allotted - total_used))
check "final balance (allotted - used)" "$balance" "339"

scary_used=$(query "SELECT used FROM entries WHERE description = 'Not so scary' AND kind = 'usage'")
check "'Not so scary' row present with used=0" "$scary_used" "0"

for placeholder in "Fall quick trip" "Family spring break" "Halloween with The Bostwicks"; do
  count=$(query "SELECT COUNT(*) FROM entries WHERE description = '$placeholder'")
  check "placeholder trip absent: $placeholder" "$count" "0"
done

use_year=$(query "SELECT use_year FROM entries WHERE date = '2025-02-21'")
check "2025-02-21 (Jan-Mar) derives use year 2024, not 2025" "$use_year" "2024"

echo "==> $pass passed, $fail failed"
if [[ $fail -gt 0 ]]; then
  printf 'FAIL: %s\n' "${failures[@]}"
  exit 1
fi
echo "PASS"
