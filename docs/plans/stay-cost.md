# Actual dollar cost per stay

## Context

The ledger tracks points, and points feel free once the contracts are paid for.
They are not. Every point carries two real costs: the amortised acquisition cost
of the contract that produced it, and the annual dues levied on it. The owner has
been computing this by hand in a Google Sheet ("Point cost" tab); this brings it
into the app so every recorded stay — and every prospective one in the planner —
shows what it actually cost.

**Formula:** `stay_cost = points_used × ($/pt/year + dues_$/pt for that use year)`

where `$/pt/year = (purchase_price + closing_costs) / (annual_points × term_years)`.

Source data (from the owner's sheet):

| contract | term | pts/yr | purchase | closing | $/pt/yr |
| --- | --- | --- | --- | --- | --- |
| "Point allocation" (2019) | 44 | 120 | $29,400 | $588.35 | $5.6796 |
| "Point allocation #2" (2022) | 41 | 150 | $30,150 | $665.00 | $5.0106 |

Dues, $ per point per year (global series): 2019 6.385, 2020 6.5616, 2021 6.8118,
2022 7.0077, 2023 7.3332, 2024 7.574, 2025 7.9298, 2026 8.2235.

## Decisions

1. **Both surfaces.** Ledger usage entries (`/ledger`) and planner trips (`/`).
2. **Per-entry contract attribution.** A usage row prices against its
   `entries.contract_id` contract's rate; when unset (or that contract has no cost
   data) it falls back to a blended portfolio rate weighted by annual points.
   Planner trips have no contract, so they always use the blended rate.
3. **Dues: stored year→rate table, projected forward.** Years past the last stored
   rate are extrapolated from the mean year-over-year ratio and flagged as
   projected in the UI.
4. **Dues are global**, not per-contract.
5. **`single_use` points are out of scope for v1.** They are separately purchased
   and carry no dues, but the ledger is one pooled balance with no way to know
   which stay drew them. v1 prices every point drawn at the contract/blended rate
   and attaches no cost to the `single_use` row itself (~56 pts of ~11,430
   lifetime, <0.5% distortion). Revisit with a per-entry cost override if it
   starts to matter.
6. **The balance is valued in dollars** on the Recent view:
   `balance × (blended + current-year dues)`, labelled as an approximation.
7. **The Bubble Tea TUI is out of scope.** Giving `internal/dvc` ledger access
   would link the pgx driver into the `dvc` binary, which `internal/ledgerclient`'s
   package doc forbids. Cost in the TUI would have to go through the HTTP client
   and needs new JSON API surface — a separate project.

## Money representation

The repo has zero monetary values and zero floats today. Keep it that way:
two named `int64` types in a new `internal/ledger/money.go`.

- `Micros` — millionths of a dollar, for **per-point rates**. Cent precision is
  not enough: `$8.2235`/pt and `$5.6796117`/pt/yr must survive multiplication by
  a few hundred points without the error landing in whole dollars.
- `Cents` — for **totals**: purchase prices, closing costs, computed stay costs.

Two types rather than one so the compiler catches adding a rate to a total.
`microsPerCent = 10_000`. Largest intermediate is `points × micros` ≈ 4.2e9 —
comfortably inside int64.

Helpers, all in `money.go`:

```go
func divRound(n, d int64) int64            // half away from zero; the ONE rounding rule
func parseDecimal(s string, scale int) (int64, error)
func ParseMicros(s string) (Micros, error) // "8.2235" -> 8_223_500
func ParseCents(s string) (Cents, error)   // "588.35" -> 58_835
func (m Micros) String() string            // "8.2235" — round-trips
func (c Cents) String() string             // "-1234.56" — round-trips
func FormatUSD(c Cents) string             // "$1,234.56" / "-$1,234.56"
func FormatRate(m Micros) string           // "$5.6796"
func PointCost(points int, rate, dues Micros) Cents
```

`parseDecimal` accepts a leading `$`, thousands separators, and an empty string
(→ 0, matching the `atoiOr(_, 0)` convention on the existing forms). It **rejects**
more fractional digits than the scale allows rather than silently rounding.

Money inputs are `<input type="text">`, not `type="number"` — `step="0.01"` would
reject `8.2235` and `step="any"` serialises inconsistently across browsers. One
server-side parser is the single source of truth, exactly like `ledger.ParseMonth`.

## Schema

`internal/ledger/schema.sql` is `//go:embed`ed and re-executed on every process
start (`store.go:33`) as a single multi-statement `Exec`. That means **no `$1`
placeholders anywhere in it** (multi-statement exec uses the simple protocol), and
every statement must stay idempotent. There is no migration tool and the deploy
docs promise there is no migration step, so a column change must be expressed
twice: in the canonical `CREATE TABLE` (for fresh databases) *and* as an
`ALTER TABLE ... ADD COLUMN IF NOT EXISTS` (for the deployed one).

Add to `contracts`:

```sql
term_years           INTEGER NOT NULL DEFAULT 0,
purchase_price_cents BIGINT  NOT NULL DEFAULT 0,
closing_costs_cents  BIGINT  NOT NULL DEFAULT 0
```

Zero in any of the three means "cost unknown" for that contract. The `$/pt/yr`
rate is **derived, never stored**.

Append, under a comment block explaining that nothing here may ever be removed:

```sql
ALTER TABLE contracts ADD COLUMN IF NOT EXISTS term_years           INTEGER NOT NULL DEFAULT 0;
ALTER TABLE contracts ADD COLUMN IF NOT EXISTS purchase_price_cents BIGINT  NOT NULL DEFAULT 0;
ALTER TABLE contracts ADD COLUMN IF NOT EXISTS closing_costs_cents  BIGINT  NOT NULL DEFAULT 0;
```

New table:

```sql
CREATE TABLE IF NOT EXISTS dues_rates (
    use_year    INTEGER PRIMARY KEY,
    rate_micros BIGINT  NOT NULL
);
```

### Seeding

DML stays out of `schema.sql`. Add `internal/ledger/seed.sql`, a second
`//go:embed`, and a second `db.Exec` in `Open` wrapped as
`"seeding reference data: %w"`. It inserts the eight dues years guarded by
`WHERE NOT EXISTS (SELECT 1 FROM dues_rates)` — a whole-table guard, not
per-row `ON CONFLICT DO NOTHING`, so a year the operator deletes in the UI is
never resurrected on restart.

**Contract costs are NOT seeded.** Contract rows are user data with generated
ids; matching on `name = 'Point allocation'` would bake one owner's purchase
price into the shipped binary. They are entered through the UI — which means
**contract editing has to be built** (`Store.UpdateContract` already exists and
has no callers; there is no web or API route for it). Delete-and-re-add is not an
option: `entries.contract_id` is `ON DELETE SET NULL` and would lose every
attribution. `deploy/README.md` gets the `psql` escape hatch as a footnote.

## Cost model — `internal/ledger/cost.go`

All cost math lives in a pure value type that performs no I/O, so it is
table-driven-testable with no Postgres and those tests never `t.Skip`.

```go
type CostBasis struct {
    rates        map[int64]Micros // contract id -> $/pt/yr, priced contracts only
    blended      Micros
    dues         map[int]Micros
    firstYear, lastYear int
    growthMicros int64      // mean YoY dues ratio × 1e6
    useYearMonth time.Month
    hasRate, hasDues bool
}

func NewCostBasis(contracts []Contract, dues []DuesRate) CostBasis
func (b CostBasis) Known() bool                                  // hasRate && hasDues
func (b CostBasis) Blended() Micros
func (b CostBasis) UseYearMonth() time.Month
func (b CostBasis) DuesFor(year int) (rate Micros, projected bool)
func (b CostBasis) RateFor(contractID *int64) Micros
func (b CostBasis) Cost(points, year int, contractID *int64) (Cents, bool, bool)
func (b CostBasis) PriceEntries(entries []Entry)                  // in place, like RunningBalance
func PriceSummaries(summaries []UseYearSummary, priced []Entry)
func (s *Store) CostBasis() (CostBasis, error)                    // the only I/O
```

`Contract` gains `TermYears int`, `PurchasePrice Cents`, `ClosingCosts Cents` and
a `PricePerPointYear() (Micros, bool)` method. `Entry` gains derived
`Cost Cents`, `CostKnown bool`, `CostProjected bool`. `UseYearSummary` gains the
same three.

`useYearMonth` is the `UseYearMonth` of the first contract returned by
`ListContracts` (ordered by id), `time.January` when there are none — documented
as a heuristic; all of this owner's contracts share an April use year.

### Blended rate (exact, integer)

```
num = Σ over PRICED contracts of (annual_points × rate_micros)
den = Σ over PRICED contracts of  annual_points
blended = divRound(num, den)        // hasRate=false when den == 0
```

Golden values:
- 2019 contract: `divRound(29_988_350_000, 5_280)` = **5_679_612**
- 2022 contract: `divRound(30_815_000_000, 6_150)` = **5_010_569**
- blended: `divRound(1_433_138_790, 270)` = **5_307_921** (`$5.3079`)

### Dues projection

Growth is an integer ratio scaled by 1e6, so no float enters the repo.

```
1. Sort stored years ascending.
2. For every ADJACENT pair exactly one year apart:
       r_i = divRound(rate[y+1] × 1_000_000, rate[y])
   (Gaps > 1 year are skipped — they would bias the mean.)
3. No such pair (0 or 1 rows, or all gaps > 1y): growthMicros = 1_000_000 (flat)
   else: growthMicros = divRound(Σ r_i, count)
```

For the seeded series this is **1_036_836** (≈3.68 %/yr). The algorithm, not that
constant, is the spec; the test pins what the algorithm produces.

`DuesFor(year)`:
- stored → `(rate, false)`
- `year > lastYear` → compound forward one year at a time from `dues[lastYear]`
- `year < firstYear` → divide backwards one year at a time from `dues[firstYear]`
- interior gap → walk forward from the nearest stored year below

all flagged `projected = true`. Compounding stepwise (not `pow`) keeps it integer
and deterministic; per-step drift is sub-micro-dollar. No cap on projection
distance — the flag is the honesty mechanism.

Golden values: 2027 = **8_526_421**, 2028 = **8_840_500**.

### Pricing

```
Cost(points, year, contractID):
    if !Known() || points <= 0 -> (0, false, false)
    return PointCost(points, RateFor(contractID), DuesFor(year)), projected, true
```

`PriceEntries` calls this with `e.Used` and `e.UseYear` — **kind-agnostic**. An
`adjustment` that draws points down really did consume dollars. Only `Used` is
ever priced; `Allotted` never is.

Golden values: 100 pts / UY2025 / 2019 contract = **136_094 cents ($1,360.94)**;
240 pts / UY2021 / blended = **290_873 cents ($2,908.73)**.

## Ledger surface

`buildLedgerView` gains `basis, err := h.store.CostBasis()`, then
`basis.PriceEntries(entries)` and `ledger.PriceSummaries(summaries, entries)`.

`ledgerView` gains `ShowCosts bool` (== `basis.Known()`), `BlendedRateLabel`,
`TotalCostLabel`, `BalanceValueLabel`, `ContractRows []contractRow`,
`Dues []duesRow`, `EditContractID int64`. `recentEntryRow` and `yearSpend` each
gain `CostLabel string` + `CostProjected bool`. `contractRow` pre-formats
`PriceLabel/ClosingLabel/RateLabel` plus round-trippable `PriceInput/ClosingInput`
for the edit form. `Dues` is every stored row ascending plus
`duesPreviewYears = 3` projected rows, read-only.

New routes inside the `opts.Ledger != nil` block in `server.go`:

| route | handler |
| --- | --- |
| `GET /ledger/contracts/{id}/edit` | `lh.editContract` |
| `POST /ledger/contracts/{id}/update` | `lh.updateContract` |
| `POST /ledger/contracts/edit/cancel` | `lh.cancelContractEdit` |
| `POST /ledger/dues` | `lh.upsertDues` |
| `DELETE /ledger/dues/{year}` | `lh.deleteDues` |

All carry `?view=contracts` and swap `#ledger-body`, like the existing controls.
`renderBody`/`renderPage`/`buildLedgerView` gain an `editContractID` parameter.
A new `parseContractForm` sits next to `parseEntryForm`; `addContract` is
refactored onto it. Note the existing form field for contract is named
`contract`, not `contract_id` — follow that short-name convention: `price`,
`closing`, `term_years`, `rate`.

Every contract/dues mutation calls `h.costs.Invalidate()`.

Template funcs in `render.go`: `"money": ledger.FormatUSD`, `"rate": ledger.FormatRate`.

`ledger.html`:
- `history_body`: Cost column on both the per-use-year table and the ledger grid;
  `<tfoot>` "Total spent"; `colspan`s bumped conditionally; projected cells carry
  `<abbr class="est" title="Dues for {year} are projected, not actual">*</abbr>`
  and a single footnote renders below the table when any shown cost is projected.
- `recent_body`: cost on each recent row and each spent-by-year `<li>`; balance
  gains `≈ {{.BalanceValueLabel}}`.
- `contracts_body`: columns `Name | Number | Resort | Points | Use year | Term |
  Purchase | Closing | $/pt/yr | actions`, an Edit button per row, a
  `contract_edit_row` partial mirroring `entry_edit_row`, the new fields on the
  add form, and "Blended portfolio rate: … /pt/yr" below. A new
  `<section class="ledger-dues">` holds the dues table + upsert form — this
  avoids a fourth `ledgerViewKind`/route/`bodyTemplateName` branch.

CSS: right-aligned tabular-nums cost cells, dim `.est` superscript, dim italic
`.projected` dues rows, and ~90px more `min-width` on the ledger tables in the
`@media` block.

## Planner surface

`Options.Ledger` is nil in ~20 existing planner tests (`newTestServer` passes
none), though `cmd/server/main.go` exits without a DSN so it is never nil in
production. The nil path is therefore test-only but load-bearing: every cost
addition sits inside a `ShowCosts` guard so a nil-ledger server renders
byte-identical HTML and no existing web test needs editing.

New `internal/web/costs.go`:

```go
type costProvider struct {
    mu        sync.Mutex
    fetch     func() (ledger.CostBasis, error) // nil when there is no store
    now       func() time.Time                 // test seam
    basis     ledger.CostBasis
    ok        bool
    fetchedAt time.Time
}
const costBasisTTL = 60 * time.Second
func newCostProvider(store *ledger.Store) *costProvider
func (p *costProvider) Basis() (ledger.CostBasis, bool)
func (p *costProvider) Invalidate()
```

This is the one bridge between the two worlds: the planner gets a read-only
pricing snapshot, never a `Store`. `Basis` **never returns an error** — a ledger
blip degrades the planner to its pre-cost rendering rather than 500-ing a search.
It caches because the planner's htmx endpoints fire on a 300ms input debounce.
Ledger handlers keep calling `h.store.CostBasis()` directly (low traffic,
freshness matters) and only touch the provider to `Invalidate()`.

`Session` gains a `costs *costProvider` field via a new final parameter on
`NewSession`, which keeps `buildAppView`'s signature and all ~12 call sites in
`handlers.go` untouched. **`internal/dvc` is not modified at all** — the planner
package stays ignorant of money.

`resultRow` gains `CostLabel`, `CostProjected` and an unexported `cost Cents`
(so the total sums cents, not formatted labels). `tripView` gains `ShowCosts`,
`SelectedCostLabel`, `SelectedCostProjected`. `appView` gains `ShowCosts` and
`SelectedCostLabel`.

Year attribution for a prospective stay is
`ledger.UseYearForDate(r.CheckIn, basis.UseYearMonth())` — this finally gives
that function a production caller and makes planner and ledger agree on what
"year" means. `contractID` is always `nil`, so the blended rate applies.

Templates: a `COST` column in `results.html`, a `summary-cost` chip in
`trip.html`'s `trip_summary`, and `≈ {{.SelectedCostLabel}}` next to
`Remaining:` in `app.html`.

## Out of scope / untouched

`api_handlers.go`, `internal/ledgerclient`, `cmd/dvc` — the CLI was not in scope,
and the ledgerclient DTOs are hand-duplicated with an explicit warning that both
sides must move together; not touching either is the cheapest correct outcome.
`contractDTO.toContract()` builds by field name, so API-created contracts land
cost-unknown, which is correct.

`cmd/ledger-migrate` is untouched but needs a regression assertion: both INSERTs
name columns explicitly (`migrate.go:126,136`) and `ensureTargetEmpty` counts only
`contracts`/`entries`, so the new columns and the dues rows `Open` now seeds
inside `Migrate` do not trip the "target not empty" guard.

`scripts/seed-ledger.sh` is untouched; seeded contracts land with no cost data
and the owner fills them in through the Contracts view. Document that in its
header.

## Commit sequence (red/green)

1. `feat(ledger): add Micros and Cents money types` — `money_test.go`, no DB
2. `feat(ledger): store contract purchase cost and term` — `store_test.go`
3. `feat(ledger): derive a contract's price per point-year` — `cost_test.go`, no DB
4. `feat(ledger): add the global dues rate table` — `dues_test.go`
5. `feat(ledger): compute the blended portfolio rate` — `cost_test.go`, no DB
6. `feat(ledger): project dues rates beyond the stored series` — `cost_test.go`, no DB
7. `feat(ledger): price a stay from points, use year and contract` — `cost_test.go`, no DB
8. `feat(ledger): derive entry and use-year costs` — `cost_store_test.go`
9. `feat(web): show cost per entry and per use year on the ledger history view`
10. `feat(web): show cost in recent activity, spent-by-year and balance`
11. `feat(web): show contract cost fields and the blended rate`
12. `feat(web): edit a contract inline`
13. `feat(web): manage dues rates from the contracts view`
14. `feat(web): cache the ledger cost basis for the planner` — `costs_test.go`, no DB
15. `feat(web): show dollar cost on planner results and trip summaries`
16. `feat(web): show total selected cost in the planner bar`
17. `style(web): style the cost columns and projected markers`
18. `docs: document the cost model, dues seeding and contract backfill`

## Edge cases

| Case | Behaviour |
| --- | --- |
| No contracts | `Known() == false` → `ShowCosts == false` everywhere; UI identical to today |
| `annual_points == 0` | Excluded from the blend's numerator *and* denominator |
| `term_years == 0` (every row pre-backfill) | Cost-unknown; feature stays dark until backfill |
| `contract_id` → an unpriced contract | Falls back to blended, not $0 |
| Dues year missing / interior gap | Walked forward from nearest stored year below, flagged projected |
| Dues year before the series | Back-projected by dividing by `growthMicros`, flagged projected |
| 0 or 1 stored dues rows | `growthMicros = 1_000_000` (flat); 0 rows → `Known() == false` |
| `Used == 0` | `CostKnown == false`, cell renders blank (matches `blankZero`) |
| Negative / borrowed balance | No effect; cost is per-entry on `Used`, always ≥ 0 |
| `adjustment` with `Used > 0` | Priced identically to `usage` — pricing is kind-agnostic |
| Blank money field on a form | Parses to 0 = cost unknown, not an error |
| Too many fractional digits | Hard error with a clear message, not silent rounding |

## Verification

```sh
# Pure cost math must pass with NO database — these must not print SKIP.
unset LEDGER_TEST_DSN
go test ./internal/ledger/ -run 'Money|Cost|PricePerPointYear' -v

make test-db
export LEDGER_TEST_DSN=postgres://postgres:test@localhost:5433/lineleader_test?sslmode=disable
make test
go vet ./...
bash scripts/seed-ledger_test.sh
```

Then, against the dev DB, run the server **twice** (the second run is the real
idempotency check) and walk:

1. `/ledger/contracts` before backfill — no `$` anywhere on `/ledger` or `/`.
2. Edit "Point allocation" → `44`, `29400`, `588.35` → `$/pt/yr` shows **$5.6796**.
3. Edit "Point allocation #2" → `41`, `30150`, `665` → **$5.0106**.
4. Below the table: **Blended portfolio rate: $5.3079 /pt/yr**.
5. Dues lists 2019–2026 (2026 = **$8.2235**) then greyed projected 2027 **$8.5264**,
   2028 **$8.8405**, 2029.
6. `/ledger/history` — usage rows priced, allocation rows blank, per-use-year Cost
   column, "Total spent" footer. Spot-check one row by hand.
7. Rows past 2026 carry `*` + tooltip; the footnote renders once.
8. `/ledger` — recent rows and spent-by-year show dollars; balance shows `≈ $X`.
9. `/` — results have a COST column; selecting a stay updates
   `Remaining: N pts ≈ $X`; the collapsed summary chip carries the cost; two
   selections sum.
10. Edit a dues rate, reload `/` — planner figures update (proves `Invalidate()`).
11. Restart: dues unchanged, no duplicates. Delete a dues year, restart, it **stays**
    deleted.
12. Mobile width — ledger and results tables still scroll inside their container.
