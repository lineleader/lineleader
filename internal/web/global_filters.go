package web

import (
	"slices"
	"sync"

	"github.com/lineleader/lineleader/internal/dvc"
)

// globalFilters owns the process-wide dvc.Config: the global exclusion set that
// inherit-mode trips resolve through, persisted to config.json. It is all that
// survives of the old in-memory planning-session singleton — the trips
// themselves now live in
// Postgres, so there is no other shared mutable state in this package and no
// other handler needs a lock.
type globalFilters struct {
	mu   sync.Mutex
	cfg  dvc.Config
	path string
}

// newGlobalFilters builds a globalFilters seeded with cfg, persisting future
// toggles to path.
func newGlobalFilters(cfg dvc.Config, path string) *globalFilters {
	return &globalFilters{cfg: cfg, path: path}
}

// Get returns a copy of the current global config.
func (g *globalFilters) Get() dvc.Config {
	g.mu.Lock()
	defer g.mu.Unlock()
	return dvc.Config{
		ExcludeResorts:   append([]string(nil), g.cfg.ExcludeResorts...),
		ExcludeRoomTypes: append([]string(nil), g.cfg.ExcludeRoomTypes...),
	}
}

// toggleString flips presence of v in slice s: removing it if present,
// appending it otherwise. It returns the updated slice.
func toggleString(s []string, v string) []string {
	if i := slices.Index(s, v); i >= 0 {
		return slices.Delete(s, i, i+1)
	}
	return append(s, v)
}

// ToggleResort flips exclusion of a resort code in the global config and
// persists it. The error from persisting is returned; the in-memory toggle
// is NOT rolled back on a save failure — the state change and the error are
// both real, independent outcomes.
func (g *globalFilters) ToggleResort(code string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cfg.ExcludeResorts = toggleString(g.cfg.ExcludeResorts, code)
	return dvc.SaveConfig(g.path, g.cfg)
}

// ToggleRoomType flips exclusion of a room type in the global config and
// persists it. It behaves like ToggleResort but operates on
// g.cfg.ExcludeRoomTypes.
func (g *globalFilters) ToggleRoomType(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cfg.ExcludeRoomTypes = toggleString(g.cfg.ExcludeRoomTypes, name)
	return dvc.SaveConfig(g.path, g.cfg)
}
