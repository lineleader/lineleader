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
column is shaped the way it is, the link between `entry` and `trip_stay`
and why it runs the way it does, the two independent point models the app
carries side by side, the point allocator that actually decides what funds
a booking, the book/unbook/delete transaction behaviour, and the row-index
invariant behind `POST /trips/{id}/stays/{row}`. Everything below is
verifiable against the code cited next to it.

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
    CHECK (check_in < check_out),
    CHECK (nights = check_out - check_in),
    CHECK (points > 0)
);

CREATE INDEX idx_trip_stay_trip ON trip_stay(trip_id, check_in, id);

-- Added by 00005_trip_stay_entry.sql, replacing an earlier trip_stay.entry_id
-- column. See "The stay/entry link" below for why a link table replaced it.
CREATE TABLE trip_stay_entry (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    trip_stay_id BIGINT NOT NULL REFERENCES trip_stay(id) ON DELETE CASCADE,
    entry_id     BIGINT NOT NULL UNIQUE REFERENCES entry(id) ON DELETE CASCADE
);

CREATE INDEX idx_trip_stay_entry_trip_stay ON trip_stay_entry(trip_stay_id);
```

Tables are singular (`trip`, `trip_stay`, `trip_stay_entry`), per the
`00003_singular_table_names.sql` convention every table added since follows.
The original `trip`/`trip_stay` shape came from `00004_trip.sql`;
`trip_stay_entry` was added later, in `00005_trip_stay_entry.sql`, when a
single `trip_stay.entry_id` column stopped being able to represent a stay
funded by more than one ledger entry (see the allocator section below). The
`CREATE TABLE` block above is the current, combined shape — read the two
migration files directly for the exact history.

`00004_trip.sql`'s comments are a historical record of what was true when
that migration ran and are left as-written rather than edited after the fact
— it predates this document and cannot be reshaped without a data migration.

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
is booked is read off whether it has any rows in `trip_stay_entry`, not a
column on `trip_stay` itself. `TripStay.EntryIDs` (`internal/ledger/trip.go`)
is `ListStays`' in-memory materialization of those link rows, and
`TripStay.Booked()` — `len(EntryIDs) > 0` — is the single place that
derivation is allowed to happen; the doc comment on `Booked` says so
explicitly, echoing the same discipline `Trip`'s own doc comment asks for.
`internal/web/render.go`'s `stayBookingStatus` is the web layer's copy of
that rule, built only from `Booked()`, and `tripRowView`'s doc comment
repeats the warning: "Status is DERIVED here from each stay's Booked() —
never read from the database. A stored status becomes a lie the moment
someone deletes a booked entry from `/ledger`."

Booked-ness alone isn't the whole story once a stay can be funded by more
than one entry (see the allocator below): `TripStay.EntryPoints` sums
`Used` across every linked entry, and `TripStay.PartiallyBooked()` reports
`Booked() && EntryPoints < Points` — a stay whose entries add up to fewer
points than the stay costs, typically because someone deleted one of a
multi-entry booking's entries on `/ledger` without touching the others. See
"Partial booking" below.

## The stay/entry link: on `trip_stay_entry`, not a column on `entry`

The pointer between a stay and the ledger entries it produced lives in the
`trip_stay_entry` link table (`trip_stay_id` / `entry_id`, both `NOT NULL`),
not on `entry` (no `trip_stay_id` column exists there at all) and, as of
`00005_trip_stay_entry.sql`, no longer as a single `entry_id` column on
`trip_stay` either — see "Schema" above for why a column stopped being
able to represent the shape a booking can now take. The reasoning that put
the pointer on the stay side in the first place, from `00004_trip.sql`'s
original `trip_stay.entry_id` column, still holds; only the mechanics of
how it self-heals changed:

1. **`entry` is the general ledger, and stays are a small, optional
   subset of it.** Most entries — allocations, bonuses, single-use grants,
   manual adjustments, usage rows entered by hand on `/ledger` — have
   nothing to do with a trip. Adding a `trip_stay_id` column to `entry`
   would put a trip-specific column on a table whose whole reason to exist
   is to stay agnostic about what produced a row of points. Keeping the
   link out of `entry` — first as a column on `trip_stay`, now as a
   separate table — leaves `entry` exactly as it was before Trips existed.
2. **The FK needs to point the right way to self-heal.** What makes
   deleting a ledger entry from `/ledger` automatically (at least partly)
   unbook the stay that created it is which table's row disappears when.
   Under the original column, `entry_id BIGINT REFERENCES entry(id) ON
   DELETE SET NULL` cleared `trip_stay.entry_id` back to `NULL` the moment
   its referenced `entry` row disappeared. Under `trip_stay_entry`, the
   link is a whole row rather than a nullable column, so there is nothing
   to null out — instead `entry_id BIGINT NOT NULL UNIQUE REFERENCES
   entry(id) ON DELETE CASCADE` deletes the `trip_stay_entry` row itself
   when its `entry` disappears. Either way, the derived booked-ness
   described above reads the result — one fewer linked entry, or none at
   all — as "not booked" (or "partially booked") with no further code
   involved. If the pointer ran the other way (`entry.trip_stay_id`),
   deleting the `trip_stay` row would be what triggers the cleanup, not
   deleting the entry — the exact direction of self-healing this feature
   needs is a stay reverting to unbooked when one of its *entries* goes
   away, not an entry losing its trip attribution when its *stay* goes
   away. The `UNIQUE` constraint on `trip_stay_entry.entry_id` preserves
   the original one-entry-can't-serve-two-stays invariant that a single
   `entry_id` column got for free.

The cost of this direction is the same as before: `entry` carries no
foreign key back to `trip_stay` (or `trip_stay_entry`) at all — which is
exactly why `DeleteTrip` has to delete a trip's ledger entries explicitly
(next section) rather than getting that for free from `ON DELETE CASCADE`
on `trip`.

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

### Two models, deliberately

`BudgetForUseYear` is the *only* thing computed above, and it is
deliberately coarse: one signed number per disposition, for one use year,
with no notion of which contract anything came from. It's what the trip
page shows and what `dvc.Search`'s budget comes from — a projection, not a
funding decision.

Whether a stay can actually be booked is a different, finer-grained
question, answered by `AllocateStayPoints` (`internal/ledger/allocation.go`)
— see the allocator section below. The two can disagree: `BudgetForUseYear`
can show a healthy total while the points behind it are scattered across
contracts in a way that can't actually fund one stay's draw (or vice versa,
in edge cases around bonus/single-use points — see "Known limitation:
points with no `contract_id`" below), because it has no visibility into
per-contract eligibility or into whether
prior draws in the same booking already spent part of a lot. This is
expected, not a bug to reconcile: `PreviewTripFunding`
(`internal/ledger/trip_accounting.go`) exists specifically to run the real
allocator against a trip's unbooked stays and surface any gap between the
two models on the trip page, before the user clicks **Book**, rather than
have `BookTrip` discover it after.

## Book / unbook / delete

Collecting a search result onto a trip (`addStay`, `internal/web/handlers.go`)
inserts an unbooked `TripStay` — `EntryIDs` empty — and touches nothing else.

**`BookTrip`** (`internal/ledger/trip_book.go`) runs in one transaction and
delegates the actual funding decision to `AllocateStayPoints`
(`internal/ledger/allocation.go`) — it does not simply write one entry per
stay.

**The allocator.** `AllocateStayPoints` is a pure function: given the
contracts, the point lots they've been allotted, every point already
consumed against those lots, a stay's check-in date and how many points it
costs, it decides which lots fund the stay and how much to draw from each,
returning a `[]PointDraw`. It is per-stay, not per-night — DVC prices a
stay as a whole, so one allocation walk covers every night at once. Its
spend order is:

1. **Banked** points (the prior use year's lot — expiring soonest) first,
2. then **current** use year points,
3. then **borrowed** points (next use year's lot) last, since spending them
   early forfeits flexibility the member might not need,

tie-broken within a disposition by ascending contract id, with each
candidate lot contributing `min(need, remaining)` before moving to the
next. Eligibility is computed **per contract, not once globally**: each
contract has its own `UseYearMonth`, so for contract `c` and `uy =
UseYearForDate(checkIn, c.UseYearMonth)`, only that contract's lots at
`uy-1` (banked), `uy` (current) or `uy+1` (borrowed) are candidates — the
same check-in calendar date can be a different use year for two contracts
with different use-year-start months. If the sorted candidates can't fully
cover the stay's points, `AllocateStayPoints` returns
`ErrInsufficientPoints` and no partial draws — see its doc comment for why
a partial allocation would be worse than none.

**Attribution is per stay, not per night.** A 150-point stay that draws 95
points from contract A's current-year lot (which has exactly 95 left) and
55 from contract B's is recorded as two draws — 95 from A, 55 from B —
with no record of which night the 55th point paid for, because nothing
downstream ever needs to know that (`TestAllocateStayPointsSplitsAcrossContracts`
in `allocation_test.go` pins this exact 95/100/150 split). An earlier
prototype of this feature, on the abandoned `feat/durable-trips` branch
(`internal/ledger/trip_accounting.go` as of commit `b5d1fed`), allocated
per *night* and needed a `trip_point_allocation` detail table — one row per
night, per contract, per disposition — to record it. At stay granularity
that table is unnecessary: each draw becomes its own ledger entry, so the
entries **are** the attribution record, and nothing else needs to store it
separately.

**One `KindUsage` entry per draw.** For each unbooked stay (in `ListStays`'
`(check_in, id)` order — already-booked stays, including partially-booked
ones, are skipped via `TripStay.Booked()`, so re-booking is a no-op), and
for each `PointDraw` `AllocateStayPoints` returns for it, `BookTrip` inserts
one `KindUsage` entry carrying that draw's `ContractID`, its `UseYear` (the
draw's *source* use year — banked draws post to `uy-1`, borrowed draws to
`uy+1`, not to the stay's check-in use year uniformly), and a `Tag` of
`"Bank"`, `"Borrow"` or `""` for current-year points
(`DispositionTag`). Each new entry is linked to its stay with its own
`trip_stay_entry` row (`InsertTripStayEntry`) — a stay funded by two
contracts gets two entries and two link rows, not one. This entry-per-draw
shape is what makes `UseYearSummaries` (the coarse model) and the allocator
(the fine one) agree by construction: every draw the allocator decided on
is posted as a real, separately use-year-tagged ledger row, so summing the
ledger by use year reproduces exactly what was allocated.

Draws within one multi-stay booking accumulate in an in-memory `consumed`
list, appended after each stay is allocated and before the next stay is
considered, so a trip with several stays can never double-spend the same
points against each other inside one `BookTrip` call. If any stay's
allocation comes back `ErrInsufficientPoints`, the whole attempt rolls
back — either every unbooked stay is booked, or none is.

**Concurrency.** `BookTrip` runs at `SERIALIZABLE` isolation and takes a
locked snapshot of contracts and lots (`lotSnapshot(ctx, tx, true)`, which
adds `FOR UPDATE`) before deciding anything, so two bookings racing for the
same last points can't both succeed. The lots query sums `entry.allotted`
grouped by `(contract_id, use_year)`; Postgres rejects `FOR UPDATE`
combined with `GROUP BY` (a locking clause must identify individual rows, a
`SUM(...)` row doesn't), so the lock is applied inside an inner derived
table that selects the raw rows, with the aggregation wrapped around it.
A transaction that loses the serialization race aborts with SQLSTATE
`40001`, and `BookTrip` retries up to `bookTripAttempts` (3) times with a
fresh, unlocked snapshot before giving up. **Known limitation
(`lineleader-yqp`):** if all 3 attempts are exhausted, `BookTrip` returns
whatever error the last attempt failed with — for a repeated `40001` this
is the raw `*pgconn.PgError` from the driver, not translated into
`ErrInsufficientPoints` or any other ledger-level error, so a caller has to
know to check for the driver error type to distinguish "still
contended" from "actually out of points."

**Preview before booking.** `PreviewStayFunding` and `PreviewTripFunding`
(`internal/ledger/trip_accounting.go`) run the identical allocator logic
read-only, outside any transaction and unlocked, so the trip page can show
which lots would fund each stay — and how short any unfundable stay is
(`shortfall`, a binary search over `AllocateStayPoints` itself) — before
the user commits. A stale preview is caught the normal way: by
`bookTripOnce`'s own locked snapshot at the moment of a real `Book` click,
not by the preview.

**`UnbookTrip`** deletes every ledger entry the trip's stays created —
every entry reachable from the trip through a `trip_stay_entry` row, which
for a multi-contract stay is more than one entry (`DeleteEntriesForTrip`,
joining `trip_stay_entry` to `trip_stay` and filtering on `trip_id`) — in
one transaction. It never touches `trip_stay` or `trip_stay_entry` rows
directly: deleting the `entry` rows is enough, because `trip_stay_entry`'s
`ON DELETE CASCADE` (on `entry_id`) removes the now-dangling link rows as a
side effect, and the derived booked-ness described earlier reads a stay
with zero linked entries as "not booked" with no further code involved.

**Partial booking.** Because `UnbookTrip`/`DeleteTrip`/`DeleteStay` all
delete through `trip_stay_entry` in bulk, they can't leave a stay
half-linked — but a person working directly on `/ledger` can: deleting just
one of a multi-entry stay's entries (say, the borrowed-points draw, leaving
the current-year draw intact) cascades away only that one
`trip_stay_entry` row. The stay is left with `EntryIDs` non-empty (so
`Booked()` is still true) but `EntryPoints` short of `Points`, which is
exactly what `PartiallyBooked()` is for. The web layer never collapses this
back into a plain "booked": `stayView.PartiallyBooked` renders it
distinctly per-stay, and at the trip level `deriveTripStatus`
(`internal/web/render.go`) reports a trip "partly booked" — a third status
alongside "planning" and "booked" — whenever some but not all of its stays
are booked, which a partially-funded stay counts as. Nothing tries to
auto-repair a partially-booked stay; it stays that way until re-booked
(a no-op for its already-linked entries) or unbooked.

**`DeleteTrip`** removes the trip, and Postgres cascades away its
`trip_stay` rows (`ON DELETE CASCADE` on `trip_stay.trip_id`) for free,
which in turn cascades away their `trip_stay_entry` rows (`ON DELETE
CASCADE` on `trip_stay_id`). The ledger entries those stays created are a
different story: **`entry` has no foreign key back to `trip_stay` or
`trip_stay_entry`** (see "The stay/entry link" above), so nothing about
deleting a `trip_stay` row would ever touch its linked `entry` rows. If
`DeleteTrip` only deleted the `trip` row and let the cascade run, every
entry a booked stay had created would be stranded in the ledger — still
counted in `used(uy)`, but with no way to trace it back to the trip that
produced it, and no way to reverse it short of finding it by hand on
`/ledger`. So `DeleteTrip` deletes the trip's ledger entries **explicitly,
first**, in the same transaction, before deleting the `trip` row:

```go
txs.q.DeleteEntriesForTrip(ctx, tripID)  // must run BEFORE DeleteTrip
txs.q.DeleteTrip(ctx, tripID)            // cascades trip_stay rows away
```

**`DeleteStay`** is the same shape scoped to one stay, for the identical
reason: it deletes every entry linked to the stay (`DeleteEntriesForStay`,
which — like `DeleteEntriesForTrip` — can delete more than one row) before
deleting the `trip_stay` row itself, rather than relying on any cascade.

Every one of these four operations runs inside a single `sql.Tx` with a
deferred `Rollback()` and an explicit `Commit()` on success — either the
whole operation lands or none of it does; there's no state where a trip's
entries are half-deleted, or a trip is gone but its entries survive it.

### Known limitation: points with no `contract_id`

`AllocateStayPoints` can only draw from a `PointLot`, and every `PointLot`
`lotSnapshotLots` builds is keyed by `(contract_id, use_year)` — it groups
`entry` rows `WHERE allotted > 0 AND contract_id IS NOT NULL`
(`internal/ledger/trip_accounting.go`). Bonus and single-use points entered
without a `contract_id` are real, spendable points — `lotSnapshotLots`'s own
doc comment is explicit that bonus/single-use *with* a `contract_id` must
not be silently excluded the way an earlier prototype excluded them — but
without a `contract_id` there is no contract `UseYearMonth` to compute
`UseYearForDate(checkIn, month)` against, so there's no use-year anchor to
decide whether such points would be banked, current or borrowed for a
given stay's check-in. `lotSnapshotLots` filters them out at the query
(`contract_id IS NOT NULL`) rather than let them become an unclassifiable
candidate. The same is true on the spend side: `unattributedUsage` sums
every `entry` with `used > 0 AND contract_id IS NULL` — this includes both
usage entered by hand on `/ledger` before this allocator existed (the old
`BookTrip` wrote entries with no `ContractID` either — see "One
`KindUsage` entry per draw" above for the current shape), and the
`trip_stay_entry` rows `00005_trip_stay_entry.sql` backfilled from the old
`trip_stay.entry_id` column for exactly those pre-allocator bookings — and
subtracts that total
from the aggregate `availablePoints` both `BookTrip` and the preview
functions check against, rather than attributing it to any one lot,
because there is no record of which contract actually funded it after the
fact.

Practically: a trip funded partly by bonus or single-use points still
books, because `availablePoints`' aggregate check and `unattributedUsage`
account for that spend in total, but the *allocator itself* can't be the
one to decide bonus/single-use points fund a particular stay — there's no
use year to make that decision against. The hand-typed `budget_override`
(`trip.budget_override`) remains the escape hatch for a trip whose real
funding picture the allocator can't fully see. Related: `lineleader-amz`.

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
