package web

import (
	"sync"

	"github.com/lineleader/lineleader/internal/dvc"
)

// Session is a thin web-layer wrapper around the dvc.Planner. The Planner owns
// all domain state (budget, trips, filters, plans) and is the single source of
// truth shared with the TUI through the same on-disk JSON files. Session adds
// only the web's view-only per-trip collapsed flags and serializes handlers so
// each "mutate then Snapshot" sequence is atomic from the web's perspective.
type Session struct {
	p         *dvc.Planner
	mu        sync.Mutex // guards collapsed[] + serializes handlers around the planner
	collapsed []bool     // VIEW-ONLY per-trip (NOT in the Planner)
}

// NewSession builds the Planner from the given args and seeds collapsed to a
// single (expanded) trip, matching the Planner's initial single trip.
func NewSession(charts []*dvc.ResortChart, cfg dvc.Config, configPath string, plans []dvc.Plan, plansPath string, defaults Defaults) *Session {
	p := dvc.NewPlanner(dvc.PlannerOptions{
		Charts:     charts,
		Global:     cfg,
		ConfigPath: configPath,
		Plans:      plans,
		PlansPath:  plansPath,
		Defaults: dvc.Defaults{
			From:      defaults.From,
			To:        defaults.To,
			Budget:    defaults.Budget,
			MinNights: defaults.MinNights,
		},
	})
	return &Session{
		p:         p,
		collapsed: []bool{false},
	}
}

// toggleCollapsed flips the view-only collapsed flag for trip i. No-op if i is
// out of range. Caller must hold s.mu.
func (s *Session) toggleCollapsed(i int) {
	if i < 0 || i >= len(s.collapsed) {
		return
	}
	s.collapsed[i] = !s.collapsed[i]
}

// reconcileCollapsed grows or shrinks the collapsed slice so its length matches
// the number of trips in the given snapshot. New trips default to expanded.
// Caller must hold s.mu.
func (s *Session) reconcileCollapsed(snap dvc.Snapshot) {
	n := len(snap.Trips)
	switch {
	case n < len(s.collapsed):
		s.collapsed = s.collapsed[:n]
	case n > len(s.collapsed):
		for len(s.collapsed) < n {
			s.collapsed = append(s.collapsed, false)
		}
	}
}
