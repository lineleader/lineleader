#!/usr/bin/env bash
# seed-ledger.sh — seed the DVC points ledger (`dvc ledger`) from the "Ledger"
# tab of the DVC Master Google Sheet.
#
# Usage:
#   scripts/seed-ledger.sh [--server URL] [--token TOKEN] [--sheet-id ID] [--fixture FILE] [--dry-run] [--force]
#
#   --server    lineleader server URL (default: the LINELEADER_SERVER
#               environment variable, same default dvc itself uses)
#   --token     auth token for the server (default: the LINELEADER_TOKEN
#               environment variable, same default dvc itself uses)
#   --sheet-id  Google Sheet id (default: the DVC Master sheet)
#   --fixture   read the Ledger rows from a local JSON file instead of calling
#               the Sheets API (this is what makes the script testable offline).
#               In --fixture mode, contract annual points are NOT read from the
#               sheet's "Point cost" tab (there is no offline fixture for it);
#               they default to 120 (contract 1) / 150 (contract 2), the real
#               values at the time this script was written.
#   --dry-run   print the `dvc` commands that would run; make no changes
#   --force     allow seeding into a ledger that already has entries
#
# Data model (see cmd/dvc/ledger.go, internal/ledgerclient, and the server's
# /api/v1/ledger API in internal/web/api_handlers.go):
#   Two API calls are made (both with valueRenderOption=UNFORMATTED_VALUE and
#   dateTimeRenderOption=FORMATTED_STRING):
#     1. Ledger!A2:G           -> entry rows (no header; A2 is the first data row)
#     2. 'Point cost'!A2:C3    -> contract annual points (column C, rows 2-3)
#   Columns of the Ledger range: A=Year(unused), B=Date, C=Desc, D=Allotted,
#   E=Used, F=Total(running balance, ignored, derived instead), G=Tag/scratch.
#
# Transformation rules (decided with the ledger owner; implement exactly):
#   1. Skip a row with no points at all (both D and E missing/empty). An
#      explicit 0 is NOT blank -- e.g. a used=0 row is kept.
#   2. Skip (and warn on stderr) rows with an implausible year (<2000 or
#      >2100) in the date column -- this is an independent guard, not just a
#      side effect of rule 1.
#   3. Desc is trimmed of surrounding whitespace.
#   4. Kind: desc matching /^point allocation/i -> allocation, linked to
#      contract 2 if the desc contains "#2" else contract 1. Desc exactly
#      "one-time use" (case-insensitive, trimmed) -> single_use. Else -> usage.
#   5. Use year is DERIVED from the date with the April use-year rule
#      (month >= 4 -> that year, else year-1), NOT copied from column A.
#   6. Tag comes from column G, but only when it's a plain text value: a
#      numeric G (spreadsheet scratch formulas) is ignored, "-" means empty.
#   7. Blank Allotted/Used become 0.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

DEFAULT_SHEET_ID="1f9X4EiG6QApCDAjy0MGV_pVsYjXIBAPIyhqvBouJmIk"
FIXTURE_DEFAULT_C1_POINTS=120
FIXTURE_DEFAULT_C2_POINTS=150

server=""
token=""
sheet_id="$DEFAULT_SHEET_ID"
fixture=""
dry_run=0
force=0

usage() {
  cat <<'EOF'
Usage: seed-ledger.sh [--server URL] [--token TOKEN] [--sheet-id ID] [--fixture FILE] [--dry-run] [--force]
EOF
}

require_value() {
  # $1 = flag name, $2 = remaining arg count
  if [[ "$2" -lt 2 ]]; then
    echo "seed-ledger: missing value for $1" >&2
    exit 1
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --server)
      require_value "$1" "$#"
      server="$2"
      shift 2
      ;;
    --token)
      require_value "$1" "$#"
      token="$2"
      shift 2
      ;;
    --sheet-id)
      require_value "$1" "$#"
      sheet_id="$2"
      shift 2
      ;;
    --fixture)
      require_value "$1" "$#"
      fixture="$2"
      shift 2
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    --force)
      force=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "seed-ledger: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "seed-ledger: required command not found: $1" >&2
    exit 1
  fi
}

require_cmd python3
if [[ -z "$fixture" ]]; then
  require_cmd curl
  require_cmd gcloud
fi

# Resolve the server URL/token the same way dvc itself would when
# --server/--token are omitted, so the "already seeded" check below
# inspects the right server.
if [[ -z "$server" ]]; then
  server="${LINELEADER_SERVER:-}"
fi
if [[ -z "$server" ]]; then
  echo "seed-ledger: no lineleader server configured (use --server or set LINELEADER_SERVER)" >&2
  exit 1
fi
if [[ -z "$token" ]]; then
  token="${LINELEADER_TOKEN:-}"
fi

# dvc_flags is passed to every `dvc ledger` invocation below.
dvc_flags=(--server "$server")
if [[ -n "$token" ]]; then
  dvc_flags+=(--token "$token")
fi

cleanup_files=()
cleanup() {
  if [[ ${#cleanup_files[@]} -gt 0 ]]; then
    rm -f "${cleanup_files[@]}"
  fi
}
trap cleanup EXIT

# --- build (or locate) the dvc binary ---
dvc_bin="$REPO_ROOT/bin/dvc"
if [[ ! -x "$dvc_bin" ]]; then
  require_cmd make
  echo "==> building dvc (bin/dvc missing)" >&2
  make -C "$REPO_ROOT" dvc >&2
fi

# --- refuse to seed into a non-empty ledger unless --force ---
# Counts entry rows straight from `dvc ledger show`'s table (the CLI is the
# only thing that talks to the ledger — over HTTP now, not a direct
# database connection): rows after the header line, up to the blank line
# preceding the optional "Per use year:" section (or EOF when the ledger is
# empty and there is no such section).
existing_entries="$("$dvc_bin" ledger show "${dvc_flags[@]}" | awk 'NR==1{next} /^$/{exit} {c++} END{print c+0}')"

if [[ "$existing_entries" -gt 0 && $force -eq 0 ]]; then
  echo "seed-ledger: ledger at $server already has $existing_entries entries; refusing to seed (pass --force to override)" >&2
  exit 1
fi

# --- fetch (or read) the Ledger rows, and the contract annual points ---
ledger_json_file=""
c1_points="$FIXTURE_DEFAULT_C1_POINTS"
c2_points="$FIXTURE_DEFAULT_C2_POINTS"

if [[ -n "$fixture" ]]; then
  if [[ ! -f "$fixture" ]]; then
    echo "seed-ledger: fixture file not found: $fixture" >&2
    exit 1
  fi
  ledger_json_file="$fixture"
  echo "==> using fixture $fixture; contract points default to ${c1_points}/${c2_points} (no offline 'Point cost' fixture)" >&2
else
  token="$(gcloud auth print-access-token)" || {
    echo "seed-ledger: gcloud auth print-access-token failed" >&2
    exit 1
  }

  ledger_json_file="$(mktemp)"
  cleanup_files+=("$ledger_json_file")
  http_code="$(curl -sS -G \
    -H "Authorization: Bearer ${token}" \
    --data-urlencode "valueRenderOption=UNFORMATTED_VALUE" \
    --data-urlencode "dateTimeRenderOption=FORMATTED_STRING" \
    -o "$ledger_json_file" -w '%{http_code}' \
    "https://sheets.googleapis.com/v4/spreadsheets/${sheet_id}/values/Ledger!A2:G")"
  if [[ "$http_code" != "200" ]]; then
    echo "seed-ledger: Sheets API request for Ledger!A2:G failed (HTTP $http_code):" >&2
    cat "$ledger_json_file" >&2
    exit 1
  fi

  points_json_file="$(mktemp)"
  cleanup_files+=("$points_json_file")
  http_code="$(curl -sS -G \
    -H "Authorization: Bearer ${token}" \
    --data-urlencode "valueRenderOption=UNFORMATTED_VALUE" \
    --data-urlencode "dateTimeRenderOption=FORMATTED_STRING" \
    -o "$points_json_file" -w '%{http_code}' \
    "https://sheets.googleapis.com/v4/spreadsheets/${sheet_id}/values/Point%20cost!A2:C3")"
  if [[ "$http_code" != "200" ]]; then
    echo "seed-ledger: Sheets API request for 'Point cost'!A2:C3 failed (HTTP $http_code):" >&2
    cat "$points_json_file" >&2
    exit 1
  fi

  points_out="$(python3 -c '
import json, sys
d = json.load(open(sys.argv[1]))
values = d.get("values", [])
if len(values) < 2 or len(values[0]) < 3 or len(values[1]) < 3:
    print("seed-ledger: Point cost!A2:C3 did not return two full rows", file=sys.stderr)
    sys.exit(1)
print(int(values[0][2]))
print(int(values[1][2]))
' "$points_json_file")"
  c1_points="$(echo "$points_out" | sed -n '1p')"
  c2_points="$(echo "$points_out" | sed -n '2p')"
fi

# --- transform the Ledger rows into dvc-ready fields (unit-separated) ---
transform_py='
import json, re, sys

ledger_path, summary_path = sys.argv[1], sys.argv[2]

with open(ledger_path) as f:
    data = json.load(f)
rows = data.get("values", [])

DATE_RE = re.compile(r"^(\d{4})-(\d{2})-(\d{2})$")


def is_blank(v):
    return v is None or v == ""


def cell(row, i):
    return row[i] if i < len(row) else None


accepted = []
skipped = []

for row in rows:
    desc = (cell(row, 2) or "").strip()
    date_raw = cell(row, 1)

    m = DATE_RE.match(str(date_raw)) if date_raw is not None else None
    if not m:
        skipped.append(("unparseable date", desc, date_raw))
        print(f"seed-ledger: skipping row (unparseable date {date_raw!r}): {desc!r}", file=sys.stderr)
        continue

    year_in_date = int(m.group(1))
    if year_in_date < 2000 or year_in_date > 2100:
        skipped.append(("implausible date", desc, date_raw))
        print(f"seed-ledger: skipping row (implausible date {date_raw}): {desc!r}", file=sys.stderr)
        continue

    d_cell = cell(row, 3)
    e_cell = cell(row, 4)
    if is_blank(d_cell) and is_blank(e_cell):
        skipped.append(("no points", desc, date_raw))
        print(f"seed-ledger: skipping row (no allotted/used points): {desc!r} ({date_raw})", file=sys.stderr)
        continue

    allotted = 0 if is_blank(d_cell) else int(d_cell)
    used = 0 if is_blank(e_cell) else int(e_cell)

    month = int(m.group(2))
    use_year = year_in_date if month >= 4 else year_in_date - 1

    desc_lower = desc.lower()
    contract_num = 0
    if desc_lower.startswith("point allocation"):
        kind = "allocation"
        contract_num = 2 if "#2" in desc else 1
    elif desc_lower == "one-time use":
        kind = "single_use"
    else:
        kind = "usage"

    g_cell = cell(row, 6)
    if is_blank(g_cell) or g_cell == "-" or isinstance(g_cell, (int, float)):
        tag = ""
    else:
        tag = str(g_cell).strip()

    accepted.append(
        {
            "use_year": use_year,
            "date": date_raw,
            "desc": desc,
            "kind": kind,
            "allotted": allotted,
            "used": used,
            "tag": tag,
            "contract_num": contract_num,
        }
    )

for e in accepted:
    fields = [
        str(e["use_year"]),
        e["date"],
        e["desc"],
        e["kind"],
        str(e["allotted"]),
        str(e["used"]),
        e["tag"],
        str(e["contract_num"]),
    ]
    # A non-whitespace separator (unit separator, 0x1f) is used instead of a
    # tab: bash "read" collapses consecutive tab/IFS-whitespace delimiters,
    # which would silently swallow empty fields like a blank tag and shift
    # contract_num into it, dropping the --contract link.
    print("\x1f".join(fields))

total_allotted = sum(e["allotted"] for e in accepted)
total_used = sum(e["used"] for e in accepted)
counts = {}
for e in accepted:
    counts[e["kind"]] = counts.get(e["kind"], 0) + 1

summary = {
    "entries": len(accepted),
    "skipped": len(skipped),
    "skipped_rows": [{"reason": r, "desc": d, "date": str(dt)} for (r, d, dt) in skipped],
    "total_allotted": total_allotted,
    "total_used": total_used,
    "balance": total_allotted - total_used,
    "counts": counts,
}
with open(summary_path, "w") as f:
    json.dump(summary, f, indent=2)
'

summary_file="$(mktemp)"
cleanup_files+=("$summary_file")

mapfile -t entry_lines < <(python3 -c "$transform_py" "$ledger_json_file" "$summary_file")

# --- create the two contracts, capturing their real ids ---
add_contract() {
  # $1 = name, $2 = points; prints the contract id (or a placeholder in
  # --dry-run mode, since no command actually runs) on stdout.
  local name="$1" points="$2"
  local -a cmd=("$dvc_bin" ledger contracts add "${dvc_flags[@]}" --name "$name" --points "$points" --use-year-month Apr)
  if [[ $dry_run -eq 1 ]]; then
    printf 'DRY-RUN:' >&2
    printf ' %q' "${cmd[@]}" >&2
    printf '\n' >&2
    echo "<id>"
    return 0
  fi
  local output
  output="$("${cmd[@]}")"
  echo "$output" >&2
  local id
  id="$(printf '%s\n' "$output" | sed -nE 's/^added contract ([0-9]+).*/\1/p')"
  if [[ -z "$id" ]]; then
    echo "seed-ledger: could not parse contract id from: $output" >&2
    exit 1
  fi
  echo "$id"
}

contract1_id="$(add_contract "Point allocation" "$c1_points")"
contract2_id="$(add_contract "Point allocation #2" "$c2_points")"

# --- add the entries, linking allocation rows to their contract ---
entries_added=0
while IFS=$'\x1f' read -r use_year date desc kind allotted used tag contract_num; do
  [[ -z "${use_year:-}" ]] && continue

  contract_id=""
  case "$contract_num" in
    1) contract_id="$contract1_id" ;;
    2) contract_id="$contract2_id" ;;
  esac

  cmd=("$dvc_bin" ledger add "${dvc_flags[@]}" --year "$use_year" --date "$date" --desc "$desc" \
    --kind "$kind" --allotted "$allotted" --used "$used" --tag "$tag")
  if [[ -n "$contract_id" ]]; then
    cmd+=(--contract "$contract_id")
  fi

  if [[ $dry_run -eq 1 ]]; then
    printf 'DRY-RUN:'
    printf ' %q' "${cmd[@]}"
    printf '\n'
  else
    "${cmd[@]}" >/dev/null
  fi
  entries_added=$((entries_added + 1))
done < <(printf '%s\n' "${entry_lines[@]}")

# --- summary ---
echo
if [[ $dry_run -eq 1 ]]; then
  echo "==> DRY RUN complete; no changes were made."
fi
echo "==> seed-ledger summary"
python3 -c '
import json, sys

summary_path, c1_points, c2_points = sys.argv[1], sys.argv[2], sys.argv[3]
d = json.load(open(summary_path))
print("Contracts created: 2 (Point allocation=" + c1_points + " pts, Point allocation #2=" + c2_points + " pts)")
print("Entries seeded:    " + str(d["entries"]))
print("Rows skipped:      " + str(d["skipped"]))
for row in d["skipped_rows"]:
    print("  - " + repr(row["desc"]) + " (" + str(row["date"]) + "): " + row["reason"])
print("Total allotted:    " + str(d["total_allotted"]))
print("Total used:        " + str(d["total_used"]))
print("Final balance:     " + str(d["balance"]))
' "$summary_file" "$c1_points" "$c2_points"
