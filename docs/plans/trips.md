# Trips: schema, budget model and lifecycle

## Context

The old web planner held its state — a set of collected stays, a hand-typed
budget, per-trip filter overrides — in an in-process `Session`, keyed off the
browser and lost on restart. Nothing about a trip survived a redeploy, and a
"trip" wasn't a thing the app could name, list, or come back to later.

This is the design record for **Trip**, its replacement: a persisted, named
date window (`internal/ledger/trip.go`, `internal/ledger/trip_book.go`) with
its own Postgres tables (`internal/ledger/migrations/00004_trip.sql`), a
derived point budget read live off the ledger, and a book/unbook lifecycle
that turns a trip's collected stays into real ledger usage entries. It
replaces the old `~/.config/lineleader/plans.json` planner session outright
— that file and the code that read it are gone (see `git log --oneline
main..HEAD` for `refactor(dvc): drop saved plans` and the rest of the
sequence this branch ran).

This document covers what was actually built: the schema and why each
column is shaped the way it is, the foreign-key direction between `entry`
and `trip_stay` and why it runs the way it does, the budget arithmetic, the
book/unbook/delete transaction behaviour, and the row-index invariant behind
`POST /trips/{id}/stays/{row}`. Everything below is verifiable against the
code cited next to it.

## Schema

```sql
CREATE TABLE trip (
    id                 BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name               TEXT    NOT NULL,
    start_date         DATE    NOT NULL,
    end_date           DATE    NOT NULL,
    min_nights         INTEGER NOT NULL DEFAULT 1,
    budget_override    INTEGER,                     -- NULL = use the computed budget
    filter_mode        TEXT    NOT NULL DEFAULT '', -- '' inherit | 'override'
    exclude_resorts    TEXT    NOT NULL DEFAULT '[]',
    exclude_room_types TEXT    NOT NULL DEFAULT '[]',
    CHECK (start_date < end_date),
    CHECK (min_nights BETWEEN 1 AND 30),            -- mirrors dvc.MaxNights
    CHECK (budget_override IS NULL OR budget_override >= 0),
    CHECK (filter_mode IN ('', 'override'))
);

CREATE TABLE trip_stay (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    trip_id    BIGINT  NOT NULL REFERENCES trip(id) ON DELETE CASCADE,
    resort     TEXT    NOT NULL,          -- the resort NAME, as dvc.StayResult carries it
    room_type  TEXT    NOT NULL,
    view       TEXT    NOT NULL DEFAULT '',
    check_in   DATE    NOT NULL,
    check_out  DATE    NOT NULL,
    nights     INTEGER NOT NULL,
    points     INTEGER NOT NULL,
    quote_hash TEXT    NOT NULL DEFAULT '',
    entry_id   BIGINT REFERENCES entry(id) ON DELETE SET NULL,
    CHECK (check_in < check_out),
    CHECK (nights = check_out - check_in),
    CHECK (points > 0)
);

CREATE INDEX idx_trip_stay_trip ON trip_stay(trip_id, check_in, id);
```

Tables are singular (`trip`, `trip_stay`), per the `00003_singular_table_names.sql`
convention every table added since follows.

### `DATE`, not `TEXT`

The legacy `entries.date` column (migration `00001_initial.sql`) is `TEXT`
holding an ISO string — a holdover from the original SQLite ledger, which
this repo is stuck with because that table predates goose and cannot be
reshaped without a data migration. `trip`/`trip_stay` carry no such history:
they're new tables, free to use Postgres's native `DATE` type. That buys two
things a `TEXT` column can't: Postgres itself rejects an invalid date at
insert time, and the `CHECK (start_date < end_date)` /
`CHECK (check_in < check_out)` / `CHECK (nights = check_out - check_in)`
constraints below do real date arithmetic (`check_out - check_in` is an
integer day count against two `DATE`s) rather than comparing strings.

### CHECK constraints

- `trip.start_date < end_date` and `trip_stay.check_in < check_out` rule out
  a zero- or negative-length window at the database layer, not just in
  application code.
- `trip.min_nights BETWEEN 1 AND 30` mirrors `dvc.MaxNights` (30 — the
  longest stay Disney permits for a single reservation, see
  `internal/dvc/search.go`), so the constraint and the search engine's own
  cap can never drift apart.
- `trip.budget_override IS NULL OR budget_override >= 0` encodes the
  override's own contract: `NULL` means "no override, use the computed
  budget"; any non-negative integer (including `0`) is a real override. A
  negative override is never meaningful, so it's rejected at the schema
  level rather than merely by `parseBudgetOverride` in `internal/web/handlers.go`.
- `trip.filter_mode IN ('', 'override')` is the two-value enum
  `TripFilterInherit` (`''`) / `TripFilterOverride` (`"override"`) from
  `internal/ledger/trip.go`, pinned so a stray third value can't reach the
  filter-resolution code in `dvc.EffectiveFilters`.
- `trip_stay.nights = check_out - check_in` keeps the redundant `nights`
  column (kept because `dvc.StayResult` already carries it, and re-deriving
  it on every read would be pure overhead) from ever disagreeing with the
  dates that produced it.
- `trip_stay.points > 0` — a stay with zero or negative points is not a
  reservation.

### Filters as JSON in a `TEXT` column

`trip.exclude_resorts` / `exclude_room_types` store a JSON array
(`marshalStringList`/`unmarshalStringList` in `trip.go`) in a `TEXT` column
rather than a normalized join table (e.g. `trip_resort_exclusion(trip_id,
resort_code)`). Two things make that the right tradeoff here: the set is
never queried by member (nothing ever asks "which trips exclude resort
X") — it's read and written as one atomic list per trip, always in full, via
`toggleTripFilter`/`setTripFilterMode` in `internal/web/handlers.go`. And the
shape already exists: `dvc.Config`'s global exclusion lists are themselves
JSON-shaped (`config.json`), so a trip's override set reuses the same
`[]string` representation the global config already normalized on, rather
than inventing a second, relationally "purer" encoding for the same data. A
join table would add two migrations and a multi-row write per toggle for a
list that's realistically a handful of entries, replaced wholesale on every
edit.

### No stored status, no stored booked-ness

Neither table has a `status` column. `Trip`'s doc comment states the reason
directly: status is *derived*, not stored, because a stored value goes
stale the instant someone deletes a booked entry from `/ledger` — the ledger
UI has no idea a `trip_stay` row is watching that entry. The same logic
applies one level down: `trip_stay` has no `booked BOOLEAN`; whether a stay
is booked is read off `entry_id IS NOT NULL`. `internal/web/render.go`'s
`stayBookingStatus` is the single place that derivation happens for the web
layer (from `TripStay.EntryID`), and `tripRowView`'s doc comment repeats the
same warning in the render layer: "Status is DERIVED here from each stay's
EntryID — never read from the database. A stored status becomes a lie the
moment someone deletes a booked entry from `/ledger`."

## The FK direction: `trip_stay.entry_id`, not `entry.trip_stay_id`

The pointer between a stay and the ledger entry it produced lives on
`trip_stay` (`entry_id BIGINT REFERENCES entry(id) ON DELETE SET NULL`), not
on `entry` (no `trip_stay_id` column exists there at all). Two reasons:

1. **`entry` is the general ledger, and stays are a small, optional
   subset of it.** Most entries — allocations, bonuses, single-use grants,
   manual adjustments, usage rows entered by hand on `/ledger` — have
   nothing to do with a trip. Adding a nullable `trip_stay_id` to `entry`
   would put a trip-specific column on a table whose whole reason to exist
   is to stay agnostic about what produced a row of points. Putting the
   pointer on `trip_stay` instead keeps `entry` exactly as it was before
   Trips existed.
2. **`ON DELETE SET NULL` needs to point the right way to self-heal.**
   The FK's direction is what makes deleting a ledger entry from `/ledger`
   automatically unbook the stay that created it: Postgres clears
   `trip_stay.entry_id` back to `NULL` the moment its referenced `entry` row
   disappears, and the derived booked-ness described above reads that as
   "not booked" with no further code involved. If the pointer ran the other
   way (`entry.trip_stay_id`), deleting the `trip_stay` row would be what
   triggers the constraint, not deleting the entry — the exact direction of
   self-healing this feature needs is a stay reverting to unbooked when its
   *entry* goes away, not an entry losing its trip attribution when its
   *stay* goes away.

The cost of this direction is that `entry` carries no foreign key back to
`trip_stay` at all — which is exactly why `DeleteTrip` has to delete a
trip's ledger entries explicitly (next section) rather than getting that for
free from `ON DELETE CASCADE` on `trip`.

## Budget model

`ledger.BudgetForUseYear` (`internal/ledger/budget.go`) is pure — no I/O, no
clock, table-tested with no Postgres. For use year `uy`:

```
current    = Net(uy)
banked     = Net(uy-1)                                   # signed, NOT clamped
borrowable = max(0, annualPointsTotal - Used(uy+1))
total      = current + banked + borrowable
```

`Net` and `Used` come from `ledger.UseYearSummary` (`Net = Allotted - Used`),
which itself depends on the convention the whole model rests on: **a usage
entry is charged to the use year whose points it consumed, which the user
sets explicitly on the entry (the CLI's `--year` flag, or the entry's
`UseYear` field), not the use year its date falls in.** The ledger is one
pooled chronological list; `UseYearSummaries` only knows how to partition it
by each entry's own `UseYear` column.

`current` and `banked` are signed and never clamped to zero — a negative
`banked` means UY(`uy`-1) over-spent its allotment by borrowing `uy`'s own
points backward into it, and clamping would hide that debt and overstate the
budget. `total` can legitimately be negative when the ledger is over-spent.

`annualPointsTotal` sums every contract's `AnnualPoints` (`Store.TripBudget`
in `budget.go`), not the posted UY+1 allotment — a posted allotment can
include non-borrowable `bonus`/`single_use` rows, and `DistributeNextYear`
typically hasn't posted UY+1 yet anyway.

Two deliberate simplifications, both documented in `budget.go`'s doc
comments:

- **No 50% borrow cap.** That was a temporary COVID-era DVC measure, since
  lifted — `borrowable` is the full contractual allotment for `uy`+1, less
  whatever's already used there, floored at zero.
- **Only one year of look-back.** `banked` only ever reads `uy`-1, matching
  DVC's actual single bank-forward rule; residue sitting in `uy`-2 or
  earlier is deliberately dropped. This understates the budget for a
  chronic under-spender — the safe direction to be wrong in — and the
  hand-typed budget override (`trip.budget_override`, resolved by
  `effectiveBudget` in `internal/web/render.go`) exists specifically to
  cover that case.

`Store.TripBudget(ctx, start)` is the only I/O in the model — it looks up
contracts and `UseYearSummaries`, resolves `start`'s use year via
`UseYearForDate`/`UseYearStartMonth`, and is deliberately **uncached**
(unlike `CostBasis`): the budget changes on every ledger mutation, and a
stale budget silently misleads in a way a stale cost basis does not.

## Book / unbook / delete

Collecting a search result onto a trip (`addStay`, `internal/web/handlers.go`)
inserts an unbooked `TripStay` — `EntryID` nil — and touches nothing else.

**`BookTrip`** (`internal/ledger/trip_book.go`) runs in one transaction:

- Lists the trip's stays and skips any that already have an `EntryID` — so
  re-booking (a double-submitted form) is a no-op, not a duplicate entry.
- For each unbooked stay, inserts a `KindUsage` entry whose `UseYear` is
  `UseYearForDate(stay.CheckIn, month)` — **computed per stay, not per
  trip** — because a trip's date window can straddle a use-year boundary
  even though the trip page only ever shows the budget for one use year
  (the window start's). `internal/web/render.go`'s `buildTripView` detects
  that straddle (`startUY != endUY`) and renders the `SpanNote` banner
  explaining which check-in dates draw from the later use year.
- Links the stay to its new entry (`SetTripStayEntryID`).
- `Tag` is left empty on purpose: a trip can draw from current, banked and
  borrowed points all at once with no per-stay attribution the app can
  honestly compute, so `Bank`/`Borrow` tagging is left to the owner to add
  by hand on `/ledger` if they care to.

**`UnbookTrip`** deletes every ledger entry the trip's stays created
(`DeleteEntriesForTrip`, scoped by `trip_id` through the `trip_stay` join)
in one transaction. It relies on `ON DELETE SET NULL` to clear each stay's
`entry_id` as a side effect — it never touches `trip_stay` rows directly.

**`DeleteTrip`** removes the trip, and Postgres cascades away its
`trip_stay` rows (`ON DELETE CASCADE` on `trip_stay.trip_id`) for free. The
ledger entries those stays created are a different story: **`entry` has no
foreign key back to `trip_stay`** (see the FK-direction section above), so
nothing about deleting a `trip_stay` row would ever touch its linked
`entry`. If `DeleteTrip` only deleted the `trip` row and let the cascade run,
every entry a booked stay had created would be stranded in the ledger —
still counted in `used(uy)`, but with no way to trace it back to the trip
that produced it, and no way to reverse it short of finding it by hand on
`/ledger`. So `DeleteTrip` deletes the trip's ledger entries **explicitly,
first**, in the same transaction, before deleting the `trip` row:

```go
txs.q.DeleteEntriesForTrip(ctx, tripID)  // must run BEFORE DeleteTrip
txs.q.DeleteTrip(ctx, tripID)            // cascades trip_stay rows away
```

**`DeleteStay`** is the same shape scoped to one stay, for the identical
reason: it deletes the stay's linked entry (`DeleteEntriesForStay`) before
deleting the `trip_stay` row itself, rather than relying on any cascade.

Every one of these four operations runs inside a single `sql.Tx` with a
deferred `Rollback()` and an explicit `Commit()` on success — either the
whole operation lands or none of it does; there's no state where a trip's
entries are half-deleted, or a trip is gone but its entries survive it.

## The row-index invariant

`POST /trips/{id}/stays/{row}` (`addStay` in `internal/web/handlers.go`)
takes a bare integer `{row}` and indexes it into a search result set that no
longer exists anywhere — nothing about the search was cached when the
browser rendered the page the user clicked from.

That works because `searchTrip` is **deterministic given (charts, params)**:
every parameter it passes to `dvc.Search` comes from the trip *row* (dates,
min nights, resolved filters) and the process-wide, immutable chart set —
never from the request. So `addStay` re-fetches the trip and its stays,
recomputes the same `searchBudgetFor` budget, re-runs `searchTrip`, and gets
back a byte-identical, identically-ordered result slice to the one the
browser rendered — `{row}` resolves to the same `dvc.StayResult` both times.
The old in-memory `Session` got this invariant for free by holding the
actual result slice between requests; persisting trips to Postgres made it
an explicit contract instead.

The one place this invariant has to be guarded carefully is truncation:
`buildTripView` caps what it renders at `maxResultRows` (200 — see the
README's Web section for why), but that cap is applied as a **prefix slice
only** — never a re-sort, re-filter, or tail — precisely because `addStay`
indexes `{row}` into the *full*, untruncated result set it recomputes
server-side. `dvc.Search` already sorts ascending by points, so a plain
prefix slice both keeps the cheapest results and keeps every visible row's
index identical between what the browser displayed and what `addStay`
re-derives.

## Verification

```sh
make test-db
export LEDGER_TEST_DSN=postgres://postgres:test@localhost:5433/lineleader_test?sslmode=disable
go test ./...
gofmt -l .
go vet ./...
```
