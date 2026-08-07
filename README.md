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

**Interactive TUI:**

`dvc search` launches an interactive terminal UI. Use Tab to move between
fields and Enter to run a search. Press `f` to open the **global** filter
panel, which lists all imported resorts (by full name) and room types as
toggleable items:

| Key | Action |
|-----|--------|
| `↑` / `k` | Move up |
| `↓` / `j` | Move down |
| `space` / `x` | Toggle resort or room type |
| `i` | Toggle the active trip between inherit and override (per-trip panel only) |
| `r` | Reset the active trip to inherit (per-trip panel only) |
| `f` / `esc` | Close filter panel |

Global filters are applied live to every trip that inherits them. Excluded
items are saved to `~/.config/lineleader/config.json` and loaded on next
launch.

**Per-trip filters (inherit vs override):**

Each trip uses the global filters unless it **overrides** them. Press `F`
(shift+`f`) from the table to open the filter panel scoped to the *active*
trip. A trip in **inherit** mode mirrors the global exclusions; toggling any
row with `space` / `x` auto-seeds an **override** for that trip (copying the
current global exclusions as a starting point) so further edits affect only
that trip. Inside the per-trip panel, `i` flips between inherit and override
and `r` resets the trip back to inherit. Each trip header shows a
`[filters: inherit]` or `[filters: override]` badge reflecting its mode.
Global toggles still affect every inherit trip but leave override trips
untouched.

**Saving and loading trip plans:**

Press `p` (from table focus) to open the plans panel. A plan captures all
trip date ranges, the global budget, the global filter state, and each
trip's per-trip filter mode and exclusions so you can pick up a multi-trip
research session exactly where you left off. Inherit trips store nothing
extra; only override trips persist their own exclusions.

| Key | Action |
|-----|--------|
| `↑` / `k` | Move up |
| `↓` / `j` | Move down |
| `enter` | Load highlighted plan |
| `s` | Start typing a new plan name (press `enter` to save, `esc` to cancel) |
| `d` | Delete highlighted plan |
| `p` / `esc` | Close plans panel |

Plans are saved to `~/.config/lineleader/plans.json` and loaded
automatically on the next launch. A single Planner is the source of truth,
so plans round-trip cleanly between the TUI and the web UI. Plans (and
configs) written before per-trip filters existed load unchanged: every trip
opens in inherit mode, so older sessions keep working exactly as before.

**Web UI:**

The same planning session is available in a browser. Each trip card has a
**Filters** button that opens a panel scoped to that trip, with an
**Inherit** / **Override** switch. While inheriting, the trip's filter rows
mirror the global exclusions and are read-only; switch to Override (or toggle
a row) to edit that trip's own exclusions. Changes update only that trip via
an out-of-band swap — other trips are untouched — and an
`[filters: override]` / `[filters: inherit]` chip on the trip card reflects
the current mode. Per-trip filter changes save with the plan; global filter
changes persist to `config.json`.

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
allotment restores it. It is stored in SQLite at
`~/.config/lineleader/ledger.db` (override with `--db` on the CLI or `--ledger`
on the server); all other data (charts, plans, config) stays in JSON.

Each row has a **use year** (defaulted from the date, editable) so the app can
roll entries up per use year — surfacing over/under-spend that a single pooled
total hides. **Contracts** are templates (name, annual points, use year month)
that drive the once-a-year *distribute* action, which posts next year's
allotment for each contract. Running distribute again the same year is a no-op.

### Web

Run the server (`make dev`) and open <http://localhost:8080/ledger>. Add, edit,
and delete entries; add contracts; and click **Distribute next year**. Negative
balances and over-borrowed use years are flagged.

### CLI

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
