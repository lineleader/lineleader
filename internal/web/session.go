package web

import (
	"slices"
	"strconv"
	"sync"

	"github.com/lineleader/lineleader/internal/dvc"
)

// Session is the single shared in-memory state for the web UI. It mirrors the
// state held by the bubbletea TUI's model and uses the same JSON files on disk
// for filters and plans, so the two UIs round-trip.
type Session struct {
	mu sync.Mutex

	charts []*dvc.ResortChart

	budget string // raw input string, like TUI's budgetField.value
	trips  []*tripState

	filters    dvc.Config
	configPath string

	plans          []dvc.Plan
	plansPath      string
	loadedPlanName string
}

// tripState is one trip's input + last computed results.
type tripState struct {
	Spec      dvc.TripSpec // From/To/MinNights as strings
	Results   []dvc.StayResult
	Selected  *dvc.StayResult
	Err       string
	Collapsed bool
}

// toggleCollapsed flips Collapsed for trip i. No-op if i is out of range.
func (s *Session) toggleCollapsed(i int) {
	if i < 0 || i >= len(s.trips) {
		return
	}
	s.trips[i].Collapsed = !s.trips[i].Collapsed
}

// NewSession builds a session and runs an initial recompute so the first
// render has results.
func NewSession(charts []*dvc.ResortChart, cfg dvc.Config, configPath string, plans []dvc.Plan, plansPath string, defaults Defaults) *Session {
	s := &Session{
		charts:     charts,
		budget:     defaults.Budget,
		filters:    cfg,
		configPath: configPath,
		plans:      plans,
		plansPath:  plansPath,
		trips: []*tripState{{
			Spec: dvc.TripSpec{
				From:      defaults.From,
				To:        defaults.To,
				MinNights: defaults.MinNights,
			},
		}},
	}
	s.recomputeAll()
	return s
}

// budgetForTrip returns the effective search budget for trip i: the global
// budget minus points selected by all OTHER trips. Trip i's own selection
// does not reduce its own search budget.
func (s *Session) budgetForTrip(global, i int) int {
	used := 0
	for j, t := range s.trips {
		if j != i && t.Selected != nil {
			used += t.Selected.Points
		}
	}
	return global - used
}

// selectedPoints returns the total points committed across all trips.
func (s *Session) selectedPoints() int {
	total := 0
	for _, t := range s.trips {
		if t.Selected != nil {
			total += t.Selected.Points
		}
	}
	return total
}

// remainingBudget returns how many points are still available.
func (s *Session) remainingBudget() int {
	b, err := strconv.Atoi(s.budget)
	if err != nil {
		return 0
	}
	return b - s.selectedPoints()
}

// recomputeAll re-runs the search for every trip using the global budget and
// all other trips' selections to compute each trip's effective budget.
func (s *Session) recomputeAll() {
	budget, err := strconv.Atoi(s.budget)
	if err != nil {
		for _, t := range s.trips {
			t.Err = "invalid Budget"
			t.Results = nil
		}
		return
	}
	for i := range s.trips {
		s.recomputeTripAt(i, s.budgetForTrip(budget, i))
	}
}

// recomputeTrip recomputes a single trip using the current global budget.
// Caller must hold s.mu.
func (s *Session) recomputeTrip(i int) {
	budget, err := strconv.Atoi(s.budget)
	if err != nil {
		s.trips[i].Err = "invalid Budget"
		s.trips[i].Results = nil
		return
	}
	s.recomputeTripAt(i, s.budgetForTrip(budget, i))
}

// recomputeTripAt runs Search for trip i with the given effective budget.
func (s *Session) recomputeTripAt(i int, budget int) {
	t := s.trips[i]
	from, err1 := dvc.ParseDate(t.Spec.From)
	to, err2 := dvc.ParseDate(t.Spec.To)
	minNights, err3 := strconv.Atoi(t.Spec.MinNights)
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
	t.Err = ""
	t.Results = dvc.Search(s.charts, dvc.SearchParams{
		WindowStart:      from,
		WindowEnd:        to,
		Budget:           budget,
		MinNights:        minNights,
		ExcludeResorts:   s.filters.ExcludeResorts,
		ExcludeRoomTypes: s.filters.ExcludeRoomTypes,
	})
}

// addTrip appends a new trip cloning the last trip's dates with min-nights=1.
func (s *Session) addTrip() {
	last := s.trips[len(s.trips)-1]
	s.trips = append(s.trips, &tripState{
		Spec: dvc.TripSpec{
			From:      last.Spec.From,
			To:        last.Spec.To,
			MinNights: "1",
		},
	})
	s.recomputeAll()
}

// removeTrip drops trip i; no-op if only one trip exists.
func (s *Session) removeTrip(i int) {
	if len(s.trips) <= 1 || i < 0 || i >= len(s.trips) {
		return
	}
	s.trips = append(s.trips[:i], s.trips[i+1:]...)
	s.recomputeAll()
}

// toggleSelection flips Selected for trip i at row rowIdx. If rowIdx is out of
// range, this is a no-op. After toggling, all trips are recomputed because
// other trips' effective budgets depend on this trip's selection.
func (s *Session) toggleSelection(i, rowIdx int) {
	if i < 0 || i >= len(s.trips) {
		return
	}
	t := s.trips[i]
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
	s.recomputeAll()
}

// stayEquals compares two StayResults by identity fields.
func stayEquals(a, b dvc.StayResult) bool {
	return a.Resort == b.Resort &&
		a.RoomType == b.RoomType &&
		a.View == b.View &&
		a.CheckIn.Equal(b.CheckIn) &&
		a.CheckOut.Equal(b.CheckOut)
}

// toggleResort flips an exclusion for the given resort code, persists the
// new config, and recomputes.
func (s *Session) toggleResort(code string) error {
	if idx := slices.Index(s.filters.ExcludeResorts, code); idx >= 0 {
		s.filters.ExcludeResorts = append(s.filters.ExcludeResorts[:idx], s.filters.ExcludeResorts[idx+1:]...)
	} else {
		s.filters.ExcludeResorts = append(s.filters.ExcludeResorts, code)
	}
	s.recomputeAll()
	return dvc.SaveConfig(s.configPath, s.filters)
}

// toggleRoomType flips an exclusion for the given room type.
func (s *Session) toggleRoomType(rt string) error {
	if idx := slices.Index(s.filters.ExcludeRoomTypes, rt); idx >= 0 {
		s.filters.ExcludeRoomTypes = append(s.filters.ExcludeRoomTypes[:idx], s.filters.ExcludeRoomTypes[idx+1:]...)
	} else {
		s.filters.ExcludeRoomTypes = append(s.filters.ExcludeRoomTypes, rt)
	}
	s.recomputeAll()
	return dvc.SaveConfig(s.configPath, s.filters)
}

// snapshotPlan builds a Plan from the current session state.
func (s *Session) snapshotPlan(name string) dvc.Plan {
	specs := make([]dvc.TripSpec, len(s.trips))
	for i, t := range s.trips {
		specs[i] = t.Spec
	}
	p := dvc.Plan{Name: name, Budget: s.budget, Trips: specs}
	if len(s.filters.ExcludeResorts) > 0 {
		p.ExcludeResorts = append([]string(nil), s.filters.ExcludeResorts...)
	}
	if len(s.filters.ExcludeRoomTypes) > 0 {
		p.ExcludeRoomTypes = append([]string(nil), s.filters.ExcludeRoomTypes...)
	}
	return p
}

// applyPlan replaces trips/budget/filters from a Plan and recomputes.
func (s *Session) applyPlan(p dvc.Plan) {
	trips := make([]*tripState, len(p.Trips))
	for i, spec := range p.Trips {
		trips[i] = &tripState{Spec: spec}
	}
	s.trips = trips
	s.budget = p.Budget
	s.filters = dvc.Config{
		ExcludeResorts:   append([]string(nil), p.ExcludeResorts...),
		ExcludeRoomTypes: append([]string(nil), p.ExcludeRoomTypes...),
	}
	s.loadedPlanName = p.Name
	s.recomputeAll()
}

// savePlan upserts the current state as a named plan and persists to disk.
func (s *Session) savePlan(name string) error {
	plan := s.snapshotPlan(name)
	found := false
	for i, p := range s.plans {
		if p.Name == name {
			s.plans[i] = plan
			found = true
			break
		}
	}
	if !found {
		s.plans = append(s.plans, plan)
	}
	s.loadedPlanName = name
	return dvc.SavePlans(s.plansPath, s.plans)
}

// deletePlan removes the named plan; no-op if not found.
func (s *Session) deletePlan(name string) error {
	for i, p := range s.plans {
		if p.Name == name {
			s.plans = append(s.plans[:i], s.plans[i+1:]...)
			if s.loadedPlanName == name {
				s.loadedPlanName = ""
			}
			return dvc.SavePlans(s.plansPath, s.plans)
		}
	}
	return nil
}

// resortOption is one row in the filter panel's resort list.
type resortOption struct {
	Code    string
	Name    string
	Enabled bool
}

// roomTypeOption is one row in the filter panel's room-type list.
type roomTypeOption struct {
	Name    string
	Enabled bool
}

// filterOptions returns the de-duplicated resort + room-type lists with
// enabled/disabled flags derived from s.filters.
func (s *Session) filterOptions() (resorts []resortOption, roomTypes []roomTypeOption) {
	resortNames := map[string]string{}
	var resortCodes []string
	roomSeen := map[string]bool{}
	var rts []string

	for _, c := range s.charts {
		if _, ok := resortNames[c.ResortCode]; !ok {
			resortNames[c.ResortCode] = c.ResortName
			resortCodes = append(resortCodes, c.ResortCode)
		}
		for _, col := range c.Columns {
			if !roomSeen[col.RoomType] {
				roomSeen[col.RoomType] = true
				rts = append(rts, col.RoomType)
			}
		}
	}
	slices.Sort(resortCodes)
	slices.Sort(rts)

	for _, code := range resortCodes {
		resorts = append(resorts, resortOption{
			Code:    code,
			Name:    resortNames[code],
			Enabled: !slices.Contains(s.filters.ExcludeResorts, code),
		})
	}
	for _, rt := range rts {
		roomTypes = append(roomTypes, roomTypeOption{
			Name:    rt,
			Enabled: !slices.Contains(s.filters.ExcludeRoomTypes, rt),
		})
	}
	return
}
