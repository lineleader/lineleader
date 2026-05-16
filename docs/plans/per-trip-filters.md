# Per-Trip Filters — Technical Design

## Context

Today, `dvc search` runs across one or more trips with a **shared, global**
filter set (`Config.ExcludeResorts`, `Config.ExcludeRoomTypes`). The filter
panel toggles those globals and every trip is re-searched with the same
exclusion lists.

In real planning sessions, trips want different filters:

- "Trip 1 is a quick solo stay — only studios at VGF."
- "Trip 2 is a family trip — 1-bedrooms or larger, any monorail resort."
- "Trip 3 is a points-bargain hunt — let me see everything cheap."

Currently the only way to do this is to keep flipping the global filters
between searches, which is tedious and destroys the cross-trip comparison
the multi-trip view was built for.

This document covers:

1. The shared domain model and operations (centralized in `internal/dvc`).
2. Persistence (config & plans backward-compatibility).
3. The TUI presentation layer.
4. The web presentation layer.
5. Migration & testing.

The guiding principle: **all multi-trip planning logic lives in `internal/dvc`.
The TUI and web are thin presentation layers that translate user events into
calls on a shared `Planner` and render its state.**

---

## 1. Goals & Non-Goals

### Goals

- Each trip can have its own resort + room-type exclusion list.
- A trip can either inherit the global filter set or fully override it,
  toggleable per trip.
- Saved plans round-trip per-trip filters between TUI and web.
- Existing plans / configs continue to load without manual migration.
- The two UIs share a single source of truth for state and operations so
  fixes apply to both.

### Non-Goals

- Per-trip budgets (budget remains global; trips already share via
  `BudgetForTrip`).
- Per-trip view filters (View column) — not requested.
- "Allow at trip level what global excludes" via per-trip *inclusion*
  lists. Override-mode covers this case more simply.

---

## 2. Domain Model

### 2.1 New types — `internal/dvc/types.go`

```go
// FilterSet is a pair of exclusion lists. The zero value excludes nothing.
type FilterSet struct {
    ExcludeResorts   []string `json:"exclude_resorts,omitempty"`
    ExcludeRoomTypes []string `json:"exclude_room_types,omitempty"`
}

// FilterMode controls how a trip resolves its effective filter set.
type FilterMode string

const (
    FilterModeInherit  FilterMode = ""         // use global filters
    FilterModeOverride FilterMode = "override" // use trip.Filters only
)
```

`Config` keeps its current shape (treated as the **global** `FilterSet`):

```go
type Config struct {
    ExcludeResorts   []string `json:"exclude_resorts"`
    ExcludeRoomTypes []string `json:"exclude_room_types"`
}

func (c Config) AsFilterSet() FilterSet { /* trivial */ }
```

### 2.2 Trip changes

Extend the existing `Trip` (in `tui.go`) and `TripSpec` (in `plans.go`):

```go
type Trip struct {
    Fields      [3]inputField
    Results     []StayResult
    Selected    *StayResult
    Offset      int
    Err         string

    FilterMode  FilterMode // "" inherits global, "override" uses Filters
    Filters     FilterSet  // used when FilterMode == FilterModeOverride
}

type TripSpec struct {
    From       string     `json:"from"`
    To         string     `json:"to"`
    MinNights  string     `json:"min_nights"`
    FilterMode FilterMode `json:"filter_mode,omitempty"`
    Filters    FilterSet  `json:"filters,omitempty"`
}
```

Trips default to `FilterModeInherit` (current behavior).

### 2.3 Effective filter resolution

A single helper, used by every code path that calls `Search`:

```go
// EffectiveFilters returns the FilterSet that should be applied for a trip
// given the current global config.
func EffectiveFilters(global Config, trip Trip) FilterSet {
    if trip.FilterMode == FilterModeOverride {
        return trip.Filters
    }
    return global.AsFilterSet()
}
```

Rationale for **override** rather than **additive merge**:

- Additive (global ∪ trip) can't express "trip 3 wants everything" when
  global excludes most resorts. Override can.
- Override has one mental model per trip ("this trip uses these filters"),
  not two (inherited + overlay).
- Trip-level override is what users requested ("different filters for each
  trip").

### 2.4 Centralized planner — new `internal/dvc/planner.go`

Today, `recomputeAll`, `recomputeTrip`, `BudgetForTrip`, `SelectedPoints`,
`RemainingBudget`, `stayEquals`, `snapshotPlan`, `applyPlan`, `savePlan`,
`deletePlan`, and filter-options building are **duplicated** between
`tui.go` and `web/session.go`. The per-trip filter change is the natural
moment to centralize.

```go
// Planner owns the canonical state for a multi-trip search session.
// It is concurrency-safe and is the single source of truth shared by
// the TUI and the web UI.
type Planner struct {
    mu sync.Mutex

    charts []*ResortChart

    budget  string    // raw input
    trips   []Trip
    global  Config    // global filters (persisted to config.json)

    plans          []Plan
    loadedPlanName string

    configPath string
    plansPath  string
}

// Construction
func NewPlanner(charts []*ResortChart, opts PlannerOptions) *Planner

type PlannerOptions struct {
    Config     Config
    ConfigPath string
    Plans      []Plan
    PlansPath  string
    Defaults   Defaults // From, To, Budget, MinNights for trip 0
}

// Read-only snapshot of state for rendering. Returned by value; UIs
// project this into their own view structs.
type Snapshot struct {
    Budget         string
    BudgetErr      string
    Remaining      int
    LoadedPlanName string
    GlobalFilters  Config
    Trips          []TripSnapshot
}

type TripSnapshot struct {
    Spec            TripSpec    // From/To/MinNights/FilterMode/Filters
    EffectiveBudget int
    EffectiveFilters FilterSet  // global or per-trip, after resolution
    Results         []StayResult
    Selected        *StayResult
    Err             string
}

func (p *Planner) Snapshot() Snapshot

// Trip operations
func (p *Planner) SetBudget(s string)
func (p *Planner) AddTrip()
func (p *Planner) RemoveTrip(i int)
func (p *Planner) SetTripField(i, fieldIdx int, value string) // 0=From,1=To,2=MinNights
func (p *Planner) ToggleSelection(i, rowIdx int)

// Filter operations — global
func (p *Planner) ToggleGlobalResort(code string) error    // persists config
func (p *Planner) ToggleGlobalRoomType(name string) error  // persists config

// Filter operations — per trip
func (p *Planner) SetTripFilterMode(i int, mode FilterMode)
func (p *Planner) ToggleTripResort(i int, code string)
func (p *Planner) ToggleTripRoomType(i int, name string)
func (p *Planner) ResetTripFilters(i int) // clear overrides, set inherit

// Plan operations
func (p *Planner) SavePlan(name string) error
func (p *Planner) LoadPlan(name string) bool
func (p *Planner) DeletePlan(name string) error
func (p *Planner) Plans() []Plan
```

Every mutating call ends with an internal `recomputeAll()` that calls
`Search` per trip using `EffectiveFilters(p.global, trip)` and
`BudgetForTrip(...)`.

The existing `BudgetForTrip`, `SelectedPoints`, `RemainingBudget`,
`stayEquals`, `ParseDate` stay as package-level helpers; `recomputeTrip`
becomes a private helper inside `planner.go`.

### 2.5 Filter options service

The filter panel for global filters (and now per-trip filters) needs the
de-duplicated, sorted list of resorts + room types. This lives once on
the planner:

```go
// FilterOptions returns the global enable/disable lists for a panel
// when tripIdx < 0, or the per-trip lists when tripIdx >= 0. The
// per-trip view also reports whether the trip is in override mode.
func (p *Planner) FilterOptions(tripIdx int) FilterOptionsView

type FilterOptionsView struct {
    TripIndex int        // -1 = global
    Mode      FilterMode // empty for global
    Resorts   []ResortOption
    RoomTypes []RoomTypeOption
}

type ResortOption   struct{ Code, Name string; Enabled bool }
type RoomTypeOption struct{ Name string; Enabled bool }
```

This replaces both `tui.go`'s `buildFilterItems` and
`session.go`'s `filterOptions()`.

---

## 3. Persistence & Backward Compatibility

### 3.1 `config.json`

Unchanged on disk. It continues to hold the **global** filter set.

### 3.2 `plans.json`

`TripSpec` gains two optional fields. Because they use `omitempty`, old
plans on disk deserialize cleanly: missing → `FilterModeInherit` and
empty `FilterSet`. New plans round-trip identically through the TUI and
the web.

Example new shape:

```json
{
  "name": "Spring Break 2026",
  "budget": "350",
  "trips": [
    {
      "from": "2026-03-15",
      "to":   "2026-03-22",
      "min_nights": "5"
    },
    {
      "from": "2026-09-10",
      "to":   "2026-09-17",
      "min_nights": "4",
      "filter_mode": "override",
      "filters": {
        "exclude_resorts":   ["AKV", "OKW"],
        "exclude_room_types": ["3-Bedroom Grand Villa"]
      }
    }
  ]
}
```

### 3.3 Migration

None required. Existing plans implicitly use `FilterModeInherit`.

---

## 4. TUI Presentation Layer

`internal/dvc/tui.go` shrinks substantially. `tuiModel` becomes:

```go
type tuiModel struct {
    planner       *Planner
    snap          Snapshot   // cached after each Planner call
    width, height int

    focused       int
    activeTripIdx int

    // panel state — purely UI
    filterOpen     bool
    filterTrip     int   // -1 = global, ≥0 = trip i
    filterCursor   int
    filterItems    []filterItem // built from FilterOptions

    plansOpen, plansNaming bool
    plansCursor            int
    plansNameBuf, plansErr string
}
```

All `recompute*` methods and the per-trip state structs are removed in
favor of `m.planner.<Op>()` + `m.snap = m.planner.Snapshot()`.

### 4.1 New keybinding: open filter panel for the active trip

Today, `f` from table focus opens the **global** filter panel. We add a
second binding:

| Key       | Action                                                |
|-----------|-------------------------------------------------------|
| `f`       | Open global filter panel (unchanged)                  |
| `F`       | Open filter panel **for the active trip**             |
| `i`       | (Inside trip filter panel) toggle inherit/override    |
| `r`       | (Inside trip filter panel) reset to inherit + clear   |

The filter panel header shows which scope is active:

```
─────────────────────────────────────────────
FILTERS — TRIP 2 (override)        i: inherit
─────────────────────────────────────────────
```

When a trip is in **inherit** mode, its panel still shows the current
global selections but the toggle hint reads
`i: override (edit per-trip)`. Toggling a resort/room type while in
inherit mode automatically flips the trip to override mode and seeds the
trip's `Filters` from the current global set, then applies the toggle.

### 4.2 Trip header indicator

Each trip header now shows a small badge when overriding:

```
▶ TRIP 2  From: 2026-09-10  To: 2026-09-17  Min nights: 4  [budget: 220 pts] [filters: override]
```

The label `filters: override` is faint when default, bold when active.
It is also a hint that `F` opens the trip-specific panel.

### 4.3 Plans panel

`snapshotPlan` and `applyPlan` move to the planner and naturally include
the per-trip `FilterMode` + `Filters`. The plans panel display gains a
small "(some trip-local filters)" hint when any spec in the plan has
`FilterMode == FilterModeOverride`.

---

## 5. Web Presentation Layer

`internal/web/session.go` is reduced to a thin wrapper around `*Planner`:

```go
type Session struct {
    p *dvc.Planner
}

func NewSession(charts []*dvc.ResortChart, ...) *Session {
    return &Session{p: dvc.NewPlanner(charts, dvc.PlannerOptions{...})}
}
```

Handlers in `handlers.go` lose all their locking + recompute logic and
delegate to `s.p.<Op>()`, then call `s.p.Snapshot()` once at the end to
build the render view.

### 5.1 View struct additions — `render.go`

```go
type tripView struct {
    // ...existing...
    FilterMode      dvc.FilterMode // "" or "override"
    UsesOverride    bool
    Filters         dvc.FilterSet  // only meaningful when override
}
```

`buildAppView` reads everything from a `Snapshot`, removing duplicate
budget / selection / stay-key logic.

### 5.2 New routes

```
GET    /trips/{i}/filters                    open per-trip filter panel
POST   /trips/{i}/filters/mode                {mode=inherit|override}
POST   /trips/{i}/filters/resorts/{code}     toggle per-trip resort
POST   /trips/{i}/filters/roomtypes/{name}   toggle per-trip room type
DELETE /trips/{i}/filters                    reset to inherit
```

The existing `/filters*` routes continue to manage the global filter set.

### 5.3 Templates

- `trip.html` header gains a per-trip "Filters" button that opens
  `/trips/{i}/filters` into the right-hand panel slot (`#panel`).
- `filters.html` is parameterised by `Scope`:
  - `Scope.IsTrip` — toggles `POST` URLs to the per-trip routes
  - `Scope.Mode`   — shows the inherit/override switch
  - When inherit, the resort/room-type rows are disabled and a hint
    reads "Switch to override to edit". Clicking inherit → override
    seeds the trip filters from the current global filters (server-side
    in `Planner.SetTripFilterMode`).
- A small chip next to each trip's `[budget: …]` shows
  `filters: override` when applicable.

The OOB swap pattern used by `filters_toggle.html` continues to work:
after any per-trip filter change, the server returns the panel + the
single affected `tripView` (via `hx-swap-oob` for `#trip-{i}-results`),
not the whole app.

---

## 6. Where Code Moves

```
internal/dvc/
  planner.go     NEW — Planner, Snapshot, TripSnapshot, FilterOptionsView,
                       EffectiveFilters, recomputeAll/Trip (private).
                       Owns config + plans persistence wrappers.
  types.go       +FilterSet, +FilterMode, +Config.AsFilterSet
  plans.go       +TripSpec.FilterMode/Filters (omitempty)
  tui.go         tuiModel becomes a thin view over *Planner; remove
                 duplicated recompute/budget/select/snapshot helpers.

internal/web/
  session.go     becomes a thin wrapper around *dvc.Planner
  handlers.go    handlers delegate to planner; add /trips/{i}/filters*
  render.go      tripView gains FilterMode/Filters
  templates/
    trip.html       +per-trip Filters button + override chip
    filters.html    +scope (global vs per-trip) + inherit/override switch
```

Everything in `internal/dvc/planner.go` is unit-tested without bringing
in bubbletea or net/http.

---

## 7. Lifecycle of a Per-Trip Filter Change

```diagram
╭──────────╮     event      ╭──────────╮     call      ╭───────────╮
│   TUI    │───────────────▶│ tuiModel │──────────────▶│  Planner  │
│ keypress │                │ .Update  │               │ .Toggle…  │
╰──────────╯                ╰──────────╯               ╰─────┬─────╯
                                                              │ recomputeAll
                                                              ▼
                                                      ╭──────────────╮
                                                      │ dvc.Search × │
                                                      │  N trips     │
                                                      ╰──────┬───────╯
                                                              │
                                ╭───────── Snapshot ◀─────────╯
                                ▼
                          ╭───────────╮                  ╭──────────╮
                          │ tuiModel  │─── tea.View ────▶│ terminal │
                          │ .View     │                  ╰──────────╯
                          ╰───────────╯

╭──────────╮  HTTP POST  ╭──────────╮     call      ╭───────────╮
│ Browser  │────────────▶│ handler  │──────────────▶│  Planner  │
│ (htmx)   │             │  .Toggle…│               │ .Toggle…  │
╰──────────╯             ╰──────────╯               ╰─────┬─────╯
                                                          │ recomputeAll
                                                          ▼
                                                  ╭──────────────╮
                                                  │ dvc.Search × │
                                                  │  N trips     │
                                                  ╰──────┬───────╯
                                                          │
                                ╭───────── Snapshot ◀─────╯
                                ▼
                          ╭───────────╮  HTML (incl. OOB)  ╭──────────╮
                          │ render.go │───────────────────▶│ Browser  │
                          ╰───────────╯                    ╰──────────╯
```

Both UIs converge on the same `Planner` API and the same `Snapshot`
projection. Bug fixes (e.g., budget arithmetic, selection toggle, plan
upsert) happen once.

---

## 8. Testing

Existing tests stay green; new ones live with the centralized code.

- `planner_test.go` (new): covers
  - inherit vs override resolution (`EffectiveFilters`),
  - per-trip toggles do not affect other trips,
  - switching inherit → override seeds from global,
  - `ResetTripFilters` returns the trip to inherit and re-uses global,
  - plans serialize/deserialize per-trip filters,
  - old plan JSON (no `filter_mode`/`filters`) loads as inherit.
- `multitrip_test.go` is rewritten to test the planner directly instead
  of `tuiModel`. The `tuiModel` view-level smoke tests stay.
- `web/handlers_test.go` gains coverage for the new `/trips/{i}/filters*`
  routes and the inherit→override seeding behavior.

---

## 9. Rollout

The change is internal and additive. There is no migration step for
users. After the change:

- `dvc search` (TUI) gains the `F` keybinding and per-trip filter chip.
- The web UI gains a "Filters" button on each trip card and the per-trip
  panel.
- Plans saved in the new code load identically in old code if they don't
  use override (because the extra fields are `omitempty`). Plans that do
  use override will load in old code but silently lose the per-trip
  filters — acceptable since the two UIs are released together.
