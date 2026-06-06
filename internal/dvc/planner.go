package dvc

import (
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"
)

// EffectiveFilters resolves the filter set a trip should search with: its own
// set when overriding, otherwise the global config's set.
func EffectiveFilters(global Config, mode FilterMode, f FilterSet) FilterSet {
	if mode == FilterModeOverride {
		return f
	}
	return global.AsFilterSet()
}

// SelectedPoints returns the total points committed across all trips.
func SelectedPoints(trips []Trip) int {
	total := 0
	for _, t := range trips {
		if t.Selected != nil {
			total += t.Selected.Points
		}
	}
	return total
}

// RemainingBudget returns how many points are still available after summing all
// selected stays.
func RemainingBudget(budget int, trips []Trip) int {
	return budget - SelectedPoints(trips)
}

// BudgetForTrip returns the effective search budget for trip at index i: the
// global budget minus points selected by all OTHER trips. Trip i's own
// selection does not reduce its own search budget.
func BudgetForTrip(budget int, trips []Trip, i int) int {
	used := 0
	for j, t := range trips {
		if j != i && t.Selected != nil {
			used += t.Selected.Points
		}
	}
	return budget - used
}

// stayEquals compares two StayResults by identity fields (Resort, RoomType,
// View, CheckIn, CheckOut). Points and Nights are not compared.
func stayEquals(a, b StayResult) bool {
	return a.Resort == b.Resort &&
		a.RoomType == b.RoomType &&
		a.View == b.View &&
		a.CheckIn.Equal(b.CheckIn) &&
		a.CheckOut.Equal(b.CheckOut)
}

// ParseDate parses a date string in YYYY-MM-DD or M/D/YYYY format.
func ParseDate(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "1/2/2006", "01/02/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q — use YYYY-MM-DD or M/D/YYYY", s)
}

// Defaults are the initial trip-0 input values. It mirrors web.Defaults so the
// web layer can pass its own defaults into the Planner without this pure-domain
// package importing package web.
type Defaults struct {
	From, To, Budget, MinNights string
}

// Planner is the concurrency-safe single source of truth for a multi-trip
// planning session. It holds the trips, the global budget and filters, and the
// loaded plans, and recomputes search results whenever its state changes.
//
// Every exported mutator (and later, reader) locks mu; the private recompute*
// helpers assume the lock is already held.
type Planner struct {
	mu             sync.Mutex
	charts         []*ResortChart
	budget         string // raw input string, like the TUI's budgetField.value
	trips          []Trip
	global         Config
	plans          []Plan
	loadedPlanName string
	configPath     string
	plansPath      string
}

// PlannerOptions configures a new Planner.
type PlannerOptions struct {
	Charts     []*ResortChart
	Global     Config
	ConfigPath string
	Plans      []Plan
	PlansPath  string
	Defaults   Defaults
}

// NewPlanner builds a Planner seeded with a single trip from opts.Defaults and
// runs an initial recompute so the first read has results.
func NewPlanner(opts PlannerOptions) *Planner {
	p := &Planner{
		charts:     opts.Charts,
		budget:     opts.Defaults.Budget,
		global:     opts.Global,
		plans:      opts.Plans,
		configPath: opts.ConfigPath,
		plansPath:  opts.PlansPath,
		trips: []Trip{{
			Fields: [3]inputField{
				{label: "From", value: opts.Defaults.From},
				{label: "To", value: opts.Defaults.To},
				{label: "Min nights", value: opts.Defaults.MinNights},
			},
		}},
	}
	p.recomputeAll()
	return p
}

// recomputeAll re-runs the search for every trip using the global budget and
// all other trips' selections to compute each trip's effective budget. The
// caller must hold p.mu.
func (p *Planner) recomputeAll() {
	budget, err := strconv.Atoi(p.budget)
	if err != nil {
		for i := range p.trips {
			p.trips[i].Err = "invalid Budget"
			p.trips[i].Results = nil
		}
		return
	}
	for i := range p.trips {
		p.recomputeTrip(i, BudgetForTrip(budget, p.trips, i))
	}
}

// recomputeTrip re-runs Search for the trip at index i with the given effective
// budget, resolving the trip's exclusions through EffectiveFilters. On a parse
// error it sets the trip's Err and leaves Results unchanged; on success it
// clears Err and replaces Results. It never auto-clears Selected and never
// touches Offset (TUI view-only). The caller must hold p.mu.
func (p *Planner) recomputeTrip(i, budget int) {
	t := &p.trips[i]
	from, err1 := ParseDate(t.Fields[0].value)
	to, err2 := ParseDate(t.Fields[1].value)
	minNights, err3 := strconv.Atoi(t.Fields[2].value)

	switch {
	case err1 != nil:
		t.Err = "invalid From date"
		return
	case err2 != nil:
		t.Err = "invalid To date"
		return
	case err3 != nil:
		t.Err = "invalid Min nights"
		return
	}

	filters := EffectiveFilters(p.global, t.FilterMode, t.Filters)
	t.Err = ""
	t.Results = Search(p.charts, SearchParams{
		WindowStart:      from,
		WindowEnd:        to,
		Budget:           budget,
		MinNights:        minNights,
		ExcludeResorts:   filters.ExcludeResorts,
		ExcludeRoomTypes: filters.ExcludeRoomTypes,
	})
}

// SetBudget sets the raw global budget input and recomputes all trips.
func (p *Planner) SetBudget(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.budget = s
	p.recomputeAll()
}

// AddTrip appends a new trip cloning the last trip's From/To dates with
// min-nights reset to "1", then recomputes.
func (p *Planner) AddTrip() {
	p.mu.Lock()
	defer p.mu.Unlock()
	last := p.trips[len(p.trips)-1]
	p.trips = append(p.trips, Trip{
		Fields: [3]inputField{
			{label: "From", value: last.Fields[0].value},
			{label: "To", value: last.Fields[1].value},
			{label: "Min nights", value: "1"},
		},
	})
	p.recomputeAll()
}

// RemoveTrip drops the trip at index i; it is a no-op if only one trip exists
// or i is out of range.
func (p *Planner) RemoveTrip(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.trips) <= 1 || i < 0 || i >= len(p.trips) {
		return
	}
	p.trips = append(p.trips[:i], p.trips[i+1:]...)
	p.recomputeAll()
}

// SetTripField sets one of trip i's input fields (0=From, 1=To, 2=MinNights)
// and recomputes. Out-of-range trip or field indices are no-ops.
func (p *Planner) SetTripField(i, field int, value string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.trips) || field < 0 || field > 2 {
		return
	}
	p.trips[i].Fields[field].value = value
	p.recomputeAll()
}

// ToggleGlobalResort flips exclusion of a resort code in the global config,
// recomputes all trips, and persists the config. Toggling a global filter
// affects every inherit trip (which resolves via EffectiveFilters) but leaves
// override trips unchanged. The error from persisting is returned; the
// in-memory toggle and recompute are not rolled back on a save failure.
func (p *Planner) ToggleGlobalResort(code string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i := slices.Index(p.global.ExcludeResorts, code); i >= 0 {
		p.global.ExcludeResorts = slices.Delete(p.global.ExcludeResorts, i, i+1)
	} else {
		p.global.ExcludeResorts = append(p.global.ExcludeResorts, code)
	}
	p.recomputeAll()
	return SaveConfig(p.configPath, p.global)
}

// ToggleGlobalRoomType flips exclusion of a room type in the global config,
// recomputes all trips, and persists the config. It behaves like
// ToggleGlobalResort but operates on p.global.ExcludeRoomTypes.
func (p *Planner) ToggleGlobalRoomType(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i := slices.Index(p.global.ExcludeRoomTypes, name); i >= 0 {
		p.global.ExcludeRoomTypes = slices.Delete(p.global.ExcludeRoomTypes, i, i+1)
	} else {
		p.global.ExcludeRoomTypes = append(p.global.ExcludeRoomTypes, name)
	}
	p.recomputeAll()
	return SaveConfig(p.configPath, p.global)
}

// cloneFilterSet returns a deep copy of f with freshly allocated slices, so the
// result shares no backing arrays with f. Used when seeding a trip's Filters
// from the global config to avoid aliasing global's slices.
func cloneFilterSet(f FilterSet) FilterSet {
	return FilterSet{
		ExcludeResorts:   append([]string(nil), f.ExcludeResorts...),
		ExcludeRoomTypes: append([]string(nil), f.ExcludeRoomTypes...),
	}
}

// ensureOverride flips an inherit trip to override, seeding its Filters from the
// global config (deep-copied) so "override" starts from what the trip currently
// sees rather than a blank slate. A trip already in override is left untouched so
// its existing Filters are preserved. The caller must hold p.mu.
func (p *Planner) ensureOverride(t *Trip) {
	if t.FilterMode != FilterModeOverride {
		t.FilterMode = FilterModeOverride
		t.Filters = cloneFilterSet(p.global.AsFilterSet())
	}
}

// toggleString flips presence of v in slice s: removing it if present, appending
// it otherwise. It returns the updated slice.
func toggleString(s []string, v string) []string {
	if i := slices.Index(s, v); i >= 0 {
		return slices.Delete(s, i, i+1)
	}
	return append(s, v)
}

// SetTripFilterMode sets trip i's filter mode. Switching to override on an
// inherit trip seeds its Filters from the global config (deep-copied) so the
// panel opens showing the same exclusions as global; an already-override trip
// keeps its Filters. Switching to inherit clears Filters. Out-of-range i is a
// no-op. Recomputes all trips afterward.
func (p *Planner) SetTripFilterMode(i int, mode FilterMode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.trips) {
		return
	}
	t := &p.trips[i]
	switch mode {
	case FilterModeOverride:
		p.ensureOverride(t)
	case FilterModeInherit:
		t.FilterMode = FilterModeInherit
		t.Filters = FilterSet{}
	}
	p.recomputeAll()
}

// ToggleTripResort flips exclusion of a resort code in trip i's per-trip
// filters. An inherit trip is first auto-flipped to override and seeded from the
// global config (so the seed's exclusions are preserved) before the toggle is
// applied. Out-of-range i is a no-op. Recomputes all trips afterward.
func (p *Planner) ToggleTripResort(i int, code string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.trips) {
		return
	}
	t := &p.trips[i]
	p.ensureOverride(t)
	t.Filters.ExcludeResorts = toggleString(t.Filters.ExcludeResorts, code)
	p.recomputeAll()
}

// ToggleTripRoomType flips exclusion of a room type in trip i's per-trip
// filters, with the same seed-on-inherit behavior as ToggleTripResort.
// Out-of-range i is a no-op. Recomputes all trips afterward.
func (p *Planner) ToggleTripRoomType(i int, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.trips) {
		return
	}
	t := &p.trips[i]
	p.ensureOverride(t)
	t.Filters.ExcludeRoomTypes = toggleString(t.Filters.ExcludeRoomTypes, name)
	p.recomputeAll()
}

// ResetTripFilters returns trip i to inherit mode and clears its Filters, so it
// resolves through the global config again. Out-of-range i is a no-op.
// Recomputes all trips afterward.
func (p *Planner) ResetTripFilters(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.trips) {
		return
	}
	p.trips[i].FilterMode = FilterModeInherit
	p.trips[i].Filters = FilterSet{}
	p.recomputeAll()
}

// snapshotPlan builds a Plan capturing the current trips, budget, global
// filters, and each trip's per-trip filter mode/filters. The caller must hold
// p.mu.
//
// Per trip, Filters is set to a pointer to the trip's FilterSet only when the
// trip is in override mode; inherit trips leave Filters nil so the JSON key is
// omitted (matching the omitempty intent on TripSpec.Filters). Global
// exclusions are copied onto the Plan only when non-empty.
func (p *Planner) snapshotPlan(name string) Plan {
	specs := make([]TripSpec, len(p.trips))
	for i := range p.trips {
		t := &p.trips[i]
		spec := TripSpec{
			From:       t.Fields[0].value,
			To:         t.Fields[1].value,
			MinNights:  t.Fields[2].value,
			Selected:   t.Selected,
			FilterMode: t.FilterMode,
		}
		if t.FilterMode == FilterModeOverride {
			f := cloneFilterSet(t.Filters)
			spec.Filters = &f
		}
		specs[i] = spec
	}
	pl := Plan{
		Name:   name,
		Budget: p.budget,
		Trips:  specs,
	}
	if len(p.global.ExcludeResorts) > 0 {
		pl.ExcludeResorts = append([]string(nil), p.global.ExcludeResorts...)
	}
	if len(p.global.ExcludeRoomTypes) > 0 {
		pl.ExcludeRoomTypes = append([]string(nil), p.global.ExcludeRoomTypes...)
	}
	return pl
}

// SavePlan upserts the current state as a named plan, persists the plans file,
// and records name as the loaded plan. Saving an existing name overwrites it
// rather than appending a duplicate.
func (p *Planner) SavePlan(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	plan := p.snapshotPlan(name)
	found := false
	for i := range p.plans {
		if p.plans[i].Name == name {
			p.plans[i] = plan
			found = true
			break
		}
	}
	if !found {
		p.plans = append(p.plans, plan)
	}
	p.loadedPlanName = name
	return SavePlans(p.plansPath, p.plans)
}

// applyPlan replaces the live trips, budget, and global filters from a saved
// Plan. Per spec, the trip's FilterMode is restored and Filters is the deref of
// spec.Filters (empty when nil). The caller must hold p.mu and must recompute
// afterward.
func (p *Planner) applyPlan(pl Plan) {
	trips := make([]Trip, len(pl.Trips))
	for i, spec := range pl.Trips {
		t := Trip{
			Fields: [3]inputField{
				{label: "From", value: spec.From},
				{label: "To", value: spec.To},
				{label: "Min nights", value: spec.MinNights},
			},
			Selected:   spec.Selected,
			FilterMode: spec.FilterMode,
		}
		t.Filters = FilterSet{}
		if spec.Filters != nil {
			t.Filters = *spec.Filters
		}
		trips[i] = t
	}
	p.trips = trips
	p.budget = pl.Budget
	p.global = Config{
		ExcludeResorts:   append([]string(nil), pl.ExcludeResorts...),
		ExcludeRoomTypes: append([]string(nil), pl.ExcludeRoomTypes...),
	}
}

// LoadPlan finds a plan by name and applies it, recomputing all trips and
// recording it as the loaded plan. It returns false (leaving state untouched)
// when no plan matches name.
func (p *Planner) LoadPlan(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.plans {
		if p.plans[i].Name == name {
			p.applyPlan(p.plans[i])
			p.loadedPlanName = name
			p.recomputeAll()
			return true
		}
	}
	return false
}

// DeletePlan removes the named plan and persists the plans file. Deleting the
// currently loaded plan clears LoadedPlanName. Deleting an unknown name is a
// no-op (no write, nil error).
func (p *Planner) DeletePlan(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.plans {
		if p.plans[i].Name == name {
			p.plans = slices.Delete(p.plans, i, i+1)
			if p.loadedPlanName == name {
				p.loadedPlanName = ""
			}
			return SavePlans(p.plansPath, p.plans)
		}
	}
	return nil
}

// Plans returns a defensive copy of the saved plans for the UI to render.
func (p *Planner) Plans() []Plan {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Plan(nil), p.plans...)
}

// LoadedPlanName returns the name of the plan last loaded or saved, or "" when
// none.
func (p *Planner) LoadedPlanName() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loadedPlanName
}

// ToggleSelection flips the Selected stay for trip i at result row rowIdx. If
// the row is already selected it deselects; otherwise it selects a copy. Out-of
// range trip or row indices are no-ops. All trips are recomputed afterward
// because other trips' effective budgets depend on this trip's selection.
func (p *Planner) ToggleSelection(i, rowIdx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.trips) {
		return
	}
	t := &p.trips[i]
	if rowIdx < 0 || rowIdx >= len(t.Results) {
		return
	}
	highlighted := t.Results[rowIdx]
	if t.Selected != nil && stayEquals(*t.Selected, highlighted) {
		t.Selected = nil
	} else {
		sel := highlighted
		t.Selected = &sel
	}
	p.recomputeAll()
}
