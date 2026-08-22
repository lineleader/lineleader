package dvc

import (
	"path/filepath"
	"slices"
	"testing"
)

// This test proves the backward-compatibility guarantee for global filters
// using a COMMITTED pre-feature fixture under testdata/ (as opposed to
// hand-written inline JSON elsewhere in the package): testdata/
// legacy_config.json is the global filter defaults a legacy install kept in
// config.json (exclude STUDIO), with no per-trip concepts.

// TestLegacyFixtures_ConfigGlobalFiltersApply loads the committed pre-feature
// config.json via the REAL LoadConfig path and confirms its global filters
// apply to an inherit trip on a freshly-built Planner (before any plan is
// loaded). An empty-config control proves the zero result is caused by the
// global filter and not an unrelated reason.
func TestLegacyFixtures_ConfigGlobalFiltersApply(t *testing.T) {
	configPath := filepath.Join("testdata", "legacy_config.json")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(%s): %v", configPath, err)
	}
	if !slices.Contains(cfg.ExcludeRoomTypes, "STUDIO") {
		t.Fatalf("legacy config did not load global filters: %+v", cfg)
	}

	newPlanner := func(global Config) *Planner {
		return NewPlanner(PlannerOptions{
			Charts: []*ResortChart{minimalChart()},
			Global: global,
			Defaults: Defaults{
				From: "2026-01-04", To: "2026-01-08", Budget: "200", MinNights: "1",
			},
		})
	}

	// With the legacy config's STUDIO exclusion, the inherit default trip honors
	// the global filter and yields zero results.
	withFilter := newPlanner(cfg).Snapshot()
	if len(withFilter.Trips) != 1 {
		t.Fatalf("Trips = %d, want 1", len(withFilter.Trips))
	}
	tr := withFilter.Trips[0]
	if tr.Spec.FilterMode != FilterModeInherit {
		t.Errorf("default trip FilterMode = %q, want inherit", tr.Spec.FilterMode)
	}
	if !slices.Contains(tr.EffectiveFilters.ExcludeRoomTypes, "STUDIO") {
		t.Errorf("EffectiveFilters did not inherit global STUDIO exclusion: %+v", tr.EffectiveFilters)
	}
	if len(tr.Results) != 0 {
		t.Errorf("inherit trip ignored legacy config STUDIO exclusion: %d results", len(tr.Results))
	}

	// Control: with no global filters the same trip has results, confirming the
	// zero above is caused by the loaded config filter.
	withoutFilter := newPlanner(Config{}).Snapshot()
	if len(withoutFilter.Trips[0].Results) == 0 {
		t.Fatal("expected results with no global filters; got none")
	}
}
