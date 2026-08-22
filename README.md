# LineLeader

LineLeader is a Disney tools platform satisfying unique needs of both
beginner and expert Disney vacation enthusiast.

## DVC Search

Find every possible stay at a Disney Vacation Club resort that fits within
a points budget across a flexible travel window.

### Setup

Build the binary and import your point chart PDFs:

```sh
make dvc
./bin/dvc import path/to/VGF-2026.pdf path/to/VGF-2027.pdf
```

Imported data is saved as JSON in `data/point-charts/` and can be committed
to the repo. Run `make import` to import the bundled VGF 2026 and 2027 charts.

### Usage

```
dvc import [--data-dir PATH] <pdf-file> [pdf-files...]
dvc search --from DATE --to DATE --budget N [--min-nights N] [--data-dir PATH]
dvc list   [--data-dir PATH]
```

**Search for stays:**

```sh
# All stays at VGF in January 2026 under 100 points
./bin/dvc search --from 2026-01-01 --to 2026-01-31 --budget 100

# At least a 4-night stay over spring break
./bin/dvc search --from 2026-03-15 --to 2026-03-30 --budget 200 --min-nights 4
```

Results are sorted by points (ascending) and show resort, room type, view,
check-in/out dates, nights, and total points.

**Web UI:**

The planner is also available as a browser app. Each trip card has a
**Filters** button that opens a panel scoped to that trip, with an
**Inherit** / **Override** switch. While inheriting, the trip's filter rows
mirror the global exclusions and are read-only; switch to Override (or toggle
a row) to edit that trip's own exclusions. Changes update only that trip via
an out-of-band swap — other trips are untouched — and an
`[filters: override]` / `[filters: inherit]` chip on the trip card reflects
the current mode. Per-trip filter changes are in-memory only for now (lost on
restart); global filter changes persist to `config.json`.

**Show available data:**

```sh
./bin/dvc list
```

**Config file:**

Default exclusions can be set in `~/.config/lineleader/config.json`:

```json
{
  "exclude_resorts": ["AKV", "BCV"],
  "exclude_room_types": ["3-Bedroom Grand Villa"]
}
```

Resort codes match what `dvc list` shows. The filter panel uses full resort
names for display but stores codes internally.

### Adding more resorts

Run `dvc import` on any DVC point chart PDF (standard Walt Disney World
resort format). The tool extracts room types, view categories, and all
seasonal date ranges automatically.

### Requirements

- `pdftotext` (from [poppler-utils](https://poppler.freedesktop.org/)) must
  be installed for `dvc import`
- Go 1.26+

## Points Ledger

Track DVC points over time — the "master ledger" of annual allotments and the
trips that consume them. The ledger is a single chronological list whose running
balance is derived: each row adds points (`Allotted`) or spends them (`Used`), and
borrowing simply shows up as the running balance going negative until the next
allotment restores it. It is stored in Postgres, which the server connects to
via `--ledger-dsn` (or the `LEDGER_DSN` env var); charts and the global
filter config stay in JSON. On boot the server runs any outstanding goose
migrations (`internal/ledger/migrations/`) automatically, so pointing it at
an empty database is enough — no separate migration step. If you still have the old
SQLite ledger, see [Seeding the hosted ledger](#seeding-the-hosted-ledger) for
the one-shot import.

Each row has a **use year** (defaulted from the date, editable) so the app can
roll entries up per use year — surfacing over/under-spend that a single pooled
total hides. **Contracts** are templates (name, annual points, use year month)
that drive the once-a-year *distribute* action, which posts next year's
allotment for each contract. Running distribute again the same year is a no-op.

### Cost

Every point also carries a real dollar cost: the amortised acquisition cost of
the contract it came from, plus that use year's dues.

```
stay_cost = points_used × ($/pt/year + dues $/pt for that use year)
```

`$/pt/year` is *derived, never stored* — `(purchase_price + closing_costs) /
(annual_points × term_years)` — from each contract's purchase price, closing
costs and term, entered (or backfilled) on the Contracts view. A usage entry
prices against its own `contract_id`'s rate; when that's unset, or the linked
contract has no cost data, it falls back to a blended rate weighted by annual
points across every priced contract. Planner trips have no contract, so they
always use the blended rate.

Dues are a single global use-year → rate table (not per-contract), also
managed from the Contracts view. Years beyond the last stored rate are
projected forward from the mean year-over-year growth of the stored series,
and any figure computed from a projected rate is marked with a `*` in the UI.

`single_use` points are **not separately priced in v1**: they're purchased
outside the pooled balance and carry no dues, but the ledger is one pooled
balance with no way to know which stay actually drew them. v1 prices every
point drawn at the contract/blended rate and attaches no cost to the
`single_use` row itself — a small, deliberate distortion (see decision 5 in
`docs/plans/stay-cost.md`).

**No dollar figure appears anywhere — ledger or planner — until at least one
contract has purchase price, closing costs and term years entered.** Until
then the UI renders exactly as it did before this feature existed.

### Web

Run the server (`make dev`) and open <http://localhost:8080/ledger>. Add, edit,
and delete entries; add contracts; and click **Distribute next year**. Negative
balances and over-borrowed use years are flagged. The Contracts view is also
where a contract's purchase price, closing costs and term years are entered
or edited, and where the dues rate table is managed — that's what turns cost
figures on everywhere else in the app.

### CLI

The CLI does not open the database itself — it talks to a running server over
HTTP. Point it at one with `--server`/`--token`, the `LINELEADER_SERVER`/
`LINELEADER_TOKEN` env vars, or `server_url`/`token` in
`~/.config/lineleader/client.json` (in that precedence order).

```sh
# Contracts (templates that drive `distribute`)
./bin/dvc ledger contracts add --name "Point allocation" --number 1234567.000 \
    --resort BLT --points 120 --use-year-month Apr
./bin/dvc ledger contracts list

# Entries — a usage just has --used; link allocations to a contract with --contract
./bin/dvc ledger add --date 2026-04-01 --desc "Point allocation" \
    --kind allocation --allotted 120 --contract 1
./bin/dvc ledger add --date 2026-06-04 --desc "BLT studio TPV" --used 26 --tag Borrow
./bin/dvc ledger add --date 2027-04-01 --desc "Single-use points" \
    --kind single_use --allotted 24

./bin/dvc ledger edit   --id 2 --date 2026-06-04 --desc "BLT studio (1br)" --used 40
./bin/dvc ledger delete --id 3

# Print the grid with running balance + per-use-year rollups
./bin/dvc ledger show

# Post next year's allotments for every contract (idempotent within the year)
./bin/dvc ledger distribute
```

Entry kinds: `allocation`, `usage`, `bonus`, `single_use`, `adjustment`. The
`--year` flag defaults to the year of `--date`; override it for points drawn
from a banked or borrowed use year.

### Local development

For local ledger development, start a throwaway Postgres with `docker compose up -d`,
then run the server:

```sh
docker compose up -d
LEDGER_DSN=postgres://postgres:dev@localhost:5432/lineleader?sslmode=disable \
AUTH_SECRET=devsecret \
make dev
```

Migrations self-apply on boot. To clean up the local database entirely (not
just stop the container), run `docker compose down -v`.

## Deployment

Hosted, single-user deployment per `docs/pitches/hosted-lineleader.md`. Chart
JSON is baked into the image (`data/point-charts/`); the points ledger lives
in an existing Postgres instance — this repo never runs its own database
container in production.

### Running it

Deploy via `deploy/percival/docker-compose.yml`, which is managed by dockhand
on the percival homelab host. The file pulls a published GHCR image (no build
on the host), joins the existing shared Postgres network, and publishes 8080
to the Caddy reverse proxy. Set `LINELEADER_VERSION` in dockhand to roll out a
release (scripts/release.sh prints the value to set once CI has published the
image).

Two required env vars:

- **`LINELEADER_VERSION`** — the image tag to deploy. Dockhand manages this;
  `scripts/release.sh` prints the value after CI publishes.
- **`AUTH_SECRET`** — the single shared login secret, set in dockhand's
  environment/secrets UI for the service (generate with
  `openssl rand -base64 32`; see `deploy/README.md`). It is deliberately not
  committed, and the compose file fails the deploy if it is unset rather than
  starting unauthenticated. The server also accepts `--auth-secret-file` to
  read the secret from a mounted Docker secret instead, if you'd rather not
  pass it through the environment.

`LEDGER_DSN` is not an input here — it is written into that file directly,
pointing at the shared Postgres container already running on percival
(reached as `postgresql:5432` over the external `postgres` network). The
server applies any outstanding goose migrations automatically on boot, so
there is no separate migration step.

A named volume (`state`) is mounted at `/state` inside the container for
`config.json` — the only file state outside Postgres.

### Seeding the hosted ledger

If you're moving from the old local SQLite ledger (`~/.config/lineleader/ledger.db`)
to the hosted Postgres, run the one-shot migration once against an *empty*
target database:

```sh
go run ./cmd/ledger-migrate \
    --sqlite ~/.config/lineleader/ledger.db \
    --dsn "$LEDGER_DSN"
```

It copies both the `contracts` and `entries` tables, preserving ids and the
`entries.contract_id` foreign key. It refuses to run if the target already
has ledger rows — it's a migration, not a sync — and leaves the old
`ledger.db` untouched on disk as a rollback path. A `make migrate-ledger`
target wraps the same command (see the Makefile for the `SQLITE_PATH`
default).

### Building the image directly

```sh
make docker-build   # docker build -t lineleader:local .
make docker-run      # local smoke test only: publishes :8080 on the host
```
