# Per-Trip Filters — Implementation Plan

Companion to [per-trip-filters.md](./per-trip-filters.md) — turns the tech
design into an ordered, dependency-aware build plan.

## Context

`dvc search` runs across one or more trips but applies a single **global**
filter set (`Config.ExcludeResorts` / `Config.ExcludeRoomTypes`). Real planning
sessions want different exclusions per trip ("trip 1: studios only at VGF; trip 3:
show me everything cheap"). Today the only workaround is flipping the global
filters between searches, which defeats the cross-trip comparison the multi-trip
view exists for.

This plan implements per-trip filters and performs the **full centralization**
the design calls for: today `recomputeAll`, `recomputeTrip`, budget math,
selection toggle, `stayEquals`, plan snapshot/apply/save/delete, and filter-option
building are **duplicated** between `internal/dvc/tui.go` and
`internal/web/session.go`. We extract all of it into a new concurrency-safe
`dvc.Planner` that becomes the single source of truth; the TUI and web become thin
presentation layers over it. Per-trip filtering is then added once, in the shared
layer.

Intended outcome: each trip inherits the global filters or fully overrides them;
saved plans round-trip per-trip filters between TUI and web; old plans/configs
load unchanged; both UIs share one code path so fixes land once.

### Already merged on this branch (don't redo)
- Per-trip **selection** already persists in saved plans (`TripSpec.Selected`).
- Web trips have a **`Collapsed`** flag + `toggleCollapsed`; collapse-on-select.

### Key constraint the design doc predates: view-only state
The design's `Snapshot`/`TripSnapshot` model **domain** state only. Two fields are
**presentation-only** and must NOT move into the Planner:
- TUI per-trip scroll **`Offset`** (`Trip.Offset`).
- Web per-trip **`Collapsed`** (`tripState.Collapsed`).

Decision: the Planner owns domain state; each UI keeps a parallel slice of its own
view-only state, keyed by trip index, and reconciles length on add/remove/load.
`TripSnapshot` carries the stable identity the UI needs to re-associate its
view-state after a recompute.

Follow red/green TDD and commit each self-contained piece with Conventional
Commit messages.

---

## Work Breakdown & Flow

```
Phase 1: Domain types ─┐
                       ├─▶ Phase 2: Planner ─┬─▶ Phase 3: TUI  ─┐
(blocks everything)    ┘   (blocks both UIs) ├─▶ Phase 4: Web  ─┼─▶ Phase 5:
                                              (3 & 4 parallel)  ┘   Validation
```

- **Phase 1** is small and blocks everything; do it first.
- **Phase 2** (Planner) blocks both UIs; it is the bulk of the work.
- **Phase 3 (TUI)** and **Phase 4 (Web)** share no files and run **fully in
  parallel** once the Planner API is stable.
- **Phase 5** runs after both UIs land.

---

## Phase 1 — Domain Foundation  *(sequential, blocks all)*

Small, pure-data changes in `internal/dvc`. One commit, or three tiny ones.

**1a. `FilterSet` + `FilterMode`** — `types.go`
```go
type FilterSet struct {
    ExcludeResorts   []string `json:"exclude_resorts,omitempty"`
    ExcludeRoomTypes []string `json:"exclude_room_types,omitempty"`
}
type FilterMode string
const (
    FilterModeInherit  FilterMode = ""         // use global filters
    FilterModeOverride FilterMode = "override" // use trip.Filters only
)
func (c Config) AsFilterSet() FilterSet { /* trivial */ }
```

**1b. `TripSpec` gains optional fields** — `plans.go`
Add `FilterMode FilterMode \`json:"filter_mode,omitempty"\`` and
`Filters FilterSet \`json:"filters,omitempty"\``. `omitempty` keeps old
`plans.json` loading cleanly as inherit — **no migration needed**.

**1c. `EffectiveFilters` helper** — `planner.go` (new file, start it here)
```go
func EffectiveFilters(global Config, mode FilterMode, f FilterSet) FilterSet {
    if mode == FilterModeOverride {
        return f
    }
    return global.AsFilterSet()
}
```
Test: inherit returns global; override returns trip's own set.

---

## Phase 2 — Centralized Planner  *(sequential within; blocks both UIs)*

New `internal/dvc/planner.go`. This is the keystone. Build it test-first
(`planner_test.go`) with no bubbletea / net-http imports.

**2a–2c. Planner skeleton + recompute + budget/selection (sequential).**
Define `Planner` (owns `charts`, `budget`, `[]Trip`, `global Config`, `plans`,
`loadedPlanName`, `configPath`, `plansPath`, `sync.Mutex`), `NewPlanner`,
`PlannerOptions`. Move the package-level helpers that already live in `tui.go`
(`BudgetForTrip`, `SelectedPoints`, `RemainingBudget`, `stayEquals`, `ParseDate`)
— they stay package-level. Make `recomputeTrip`/`recomputeAll` private methods
that call `Search` with `EffectiveFilters(p.global, trip.FilterMode, trip.Filters)`.
Implement `SetBudget`, `AddTrip`, `RemoveTrip`, `SetTripField`, `ToggleSelection`.
Every mutator ends with `recomputeAll()`.

Extend the existing `Trip` (`tui.go`) with `FilterMode FilterMode` + `Filters
FilterSet` (default inherit) — but **drop `Offset` from what the Planner reasons
about**; `Offset` stays a TUI concern (see Phase 3).

**2d. Global filter ops (+persist).**
`ToggleGlobalResort(code)` / `ToggleGlobalRoomType(name)` mutate `p.global`,
recompute, and `dvc.SaveConfig`. This replaces `session.toggleResort/toggleRoomType`
and the TUI's `rebuildFiltersFromItems` path.

**2e. Per-trip filter ops + override seeding  *(trickiest — extra tests)***.
```go
func (p *Planner) SetTripFilterMode(i int, mode FilterMode)
func (p *Planner) ToggleTripResort(i int, code string)
func (p *Planner) ToggleTripRoomType(i int, name string)
func (p *Planner) ResetTripFilters(i int) // mode=inherit, clear Filters
```
Seeding rule: toggling a resort/room type on an **inherit** trip auto-flips it to
**override** and seeds `trip.Filters` from `p.global.AsFilterSet()` *before*
applying the toggle. `SetTripFilterMode(i, override)` on an inherit trip likewise
seeds from global. Tests must assert: (a) per-trip toggle never affects another
trip; (b) inherit→override seeds from current global; (c) `ResetTripFilters`
returns to inherit and re-uses global.

**2f. Plan ops.** Move `snapshotPlan`/`applyPlan`/`savePlan`/`deletePlan` here.
`snapshotPlan` now also captures `FilterMode`+`Filters` per spec; `applyPlan`
restores them. `LoadPlan(name) bool`, `Plans() []Plan`.

**2g. `Snapshot` + `FilterOptions`.**
```go
type Snapshot struct {
    Budget, BudgetErr, LoadedPlanName string
    Remaining int
    GlobalFilters Config
    Trips []TripSnapshot
}
type TripSnapshot struct {
    Spec TripSpec                 // From/To/MinNights/FilterMode/Filters
    EffectiveBudget int
    EffectiveFilters FilterSet     // resolved (global or per-trip)
    Results []StayResult
    Selected *StayResult
    Err string
}
func (p *Planner) Snapshot() Snapshot
func (p *Planner) FilterOptions(tripIdx int) FilterOptionsView // -1 = global
```
`FilterOptions` replaces both `tui.go`'s `buildFilterItems` and `session.go`'s
`filterOptions()` — one de-dup/sort of resort codes + room types, with
`Enabled` derived from global filters (tripIdx < 0) or the trip's effective set
(tripIdx ≥ 0), plus `Mode` for the panel header. Returns `FilterOptionsView{
TripIndex, Mode, Resorts []ResortOption, RoomTypes []RoomTypeOption }`.

**2h. `planner_test.go`** grows alongside 2a–2g. Also: old-plan JSON (no
`filter_mode`/`filters`) loads as inherit; plans serialize/deserialize per-trip
filters.

---

## Phase 3 — TUI  *(parallel with Phase 4)*

`internal/dvc/tui.go` shrinks substantially. `tuiModel` drops `trips []Trip`,
`filters Config`, `plans`, `loadedPlanName`, and all `recompute*`/`snapshot*`/
`apply*`/`save*` methods in favor of:
```go
type tuiModel struct {
    planner *Planner
    snap    Snapshot      // refreshed after each planner call
    offsets []int          // VIEW-ONLY per-trip scroll, kept here
    width, height, focused, activeTripIdx int
    filterOpen bool
    filterTrip int          // -1 = global, ≥0 = trip i
    filterCursor int
    filterItems []filterItem // built from planner.FilterOptions(filterTrip)
    plansOpen, plansNaming bool
    plansCursor int
    plansNameBuf, plansErr string
}
```
The update pattern becomes: handle key → `m.planner.<Op>(...)` →
`m.snap = m.planner.Snapshot()`. Keep `offsets` length in sync on add/remove/load
and clamp against `len(snap.Trips[i].Results)` (the clamp logic currently inside
`recomputeTrip` moves to the View/scroll handling since Offset is no longer a
domain field).

**3b. New keybindings / scope.** `f` = global panel (unchanged); `F` = panel for
the **active trip**; inside a trip panel `i` toggles inherit/override and `r`
resets. Panel header shows scope, e.g. `FILTERS — TRIP 2 (override)   i: inherit`.
Toggling a row while inherit auto-flips to override (handled by
`ToggleTripResort/RoomType`'s seeding — TUI just calls it).

**3c. Trip header badge.** Append `[filters: override]` (bold when override,
faint otherwise) to each trip header in `View()` — read from
`snap.Trips[i].Spec.FilterMode`.

**3d. Plans panel hint.** Show "(some trip-local filters)" when any spec in a
plan has `FilterModeOverride`.

**3e. Tests.** Rewrite `multitrip_test.go` to drive the `Planner` directly;
keep `tuiModel` view-level smoke tests (`tui_test.go`).

---

## Phase 4 — Web  *(parallel with Phase 3)*

**4a→4b (sequential first).** `session.go` collapses to a thin wrapper:
```go
type Session struct {
    p *dvc.Planner
    mu sync.Mutex          // still guards collapsed[] + serializes handlers
    collapsed []bool        // VIEW-ONLY per-trip, kept here (not in Planner)
}
```
`NewSession` builds `dvc.NewPlanner(...)`. Every handler in `handlers.go` loses
its recompute/locking-of-domain logic and delegates: `s.p.<Op>()` then
`s.p.Snapshot()` once to build the view. Keep `collapsed` reconciled on
add/remove/load and toggled by the existing `/trips/{i}/collapse` route.

**4c. `tripView` additions** — `render.go`:
```go
FilterMode   dvc.FilterMode
UsesOverride bool
Filters      dvc.FilterSet
```
`buildAppView` reads from a `Snapshot` instead of recomputing budget/selection/
stay-key locally (the stay-key fallback for filtered-out selections stays).

**4f-1. Scope abstraction (keystone for the rest of Phase 4).** Define a
`filterScope` the templates key off:
```go
type filterScope struct {
    IsTrip   bool
    TripIndex int
    Mode     dvc.FilterMode
}
```
This is the contract 4d/4e/4f-2 depend on — design it before fanning out.

**4d. New routes** (`server.go` + `handlers.go`); existing `/filters*` stays for
global:
```
GET    /trips/{i}/filters                 open per-trip panel
POST   /trips/{i}/filters/mode            {mode=inherit|override}
POST   /trips/{i}/filters/resorts/{code}  toggle per-trip resort
POST   /trips/{i}/filters/roomtypes/{name}toggle per-trip room type
DELETE /trips/{i}/filters                 reset to inherit
```
Reuse the existing OOB-swap pattern (`filters_toggle.html`): after a per-trip
change, return the panel **plus** the single affected `tripView` via
`hx-swap-oob` for `#trip-{i}-results`, not the whole app.

**4e/4f-2. Templates** (parallel after 4f-1).
- `trip.html`: per-trip "Filters" button → loads `/trips/{i}/filters` into
  `#panel`; an `[filters: override]` chip next to `[budget: …]`.
- `filters.html`: parameterized by `filterScope` — `IsTrip` switches POST URLs to
  per-trip routes; `Mode` shows the inherit/override switch; when inherit, rows
  are disabled with a "Switch to override to edit" hint (clicking override seeds
  from global server-side via `SetTripFilterMode`).

**4g. `handlers_test.go`** gains coverage for the new routes and the
inherit→override seeding behavior.

---

## Phase 5 — Validation & Rollout

- **5a.** Full suite green: `go test ./...`. Explicitly load an old `plans.json`
  with no `filter_mode`/`filters` and confirm inherit.
- **5b.** Manual smoke for TUI + web parity (see Verification).
- **5c.** Docs/README touch-ups.

Rollout is additive — no user migration. Plans using override load in old code
but silently lose per-trip filters; acceptable since both UIs ship together.

---

## Files Touched

```
internal/dvc/
  planner.go    NEW — Planner, Snapshot, TripSnapshot, FilterOptionsView,
                EffectiveFilters, private recompute*. Owns config+plans persistence.
  types.go      +FilterSet, +FilterMode, +Config.AsFilterSet
  plans.go      +TripSpec.FilterMode/Filters (omitempty)
  tui.go        tuiModel becomes thin view over *Planner; keeps offsets[] only;
                remove duplicated recompute/budget/select/snapshot helpers
  planner_test.go     NEW
  multitrip_test.go   rewritten against Planner
internal/web/
  session.go    thin wrapper around *dvc.Planner; keeps collapsed[] only
  handlers.go   delegate to planner; add /trips/{i}/filters* handlers
  render.go     tripView +FilterMode/UsesOverride/Filters; buildAppView from Snapshot
  server.go     register new routes
  templates/
    trip.html     +Filters button + override chip
    filters.html  +filterScope (global vs per-trip) + inherit/override switch
  handlers_test.go  +new-route + seeding coverage
```

---

## Verification

1. **Unit:** `go test ./internal/dvc/... ./internal/web/...` — new
   `planner_test.go` covers inherit/override resolution, per-trip isolation,
   seeding, reset, and plan round-trip incl. legacy JSON.
2. **TUI manual:** run `dvc search`; press `]` to a second trip, `F`, toggle a
   resort → confirm only that trip's results change and the header shows
   `[filters: override]`; `r` resets to inherit. Save a plan, quit, reload →
   per-trip filters persist. (Restart note: kill stale `exe/main`, not just
   `go run`.)
3. **Web manual:** start the web server; click a trip's "Filters" button, switch
   to override, toggle a room type → confirm OOB swap updates only that trip and
   the chip appears. Save/load a plan and confirm round-trip. Open the same
   `plans.json` in both UIs to confirm parity.
4. **Backward compat:** point at an existing pre-feature `plans.json`; load a
   plan and confirm all trips behave as inherit (global filters applied).
```
