package dvc

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// These tests prove the backward-compatibility guarantee for per-trip filters
// using COMMITTED pre-feature fixtures under testdata/ (as opposed to the
// hand-written inline JSON in plans_test.go / planner_test.go). The fixtures
// were authored to match exactly what an OLD binary (predating the
// filter_mode/filters keys) would have written:
//
//   - testdata/legacy_plans.json: trips have no filter_mode/filters keys; the
//     plan's filter snapshot lives in the plan-level exclude_room_types list,
//     which is how legacy installs persisted filter state in a saved plan.
//   - testdata/legacy_config.json: the global filter defaults a legacy install
//     kept in config.json (exclude STUDIO), with no per-trip concepts.

// TestLegacyFixtures_PlanLoadsAsInherit loads the committed pre-feature
// plans.json through the REAL load path (LoadPlans -> NewPlanner -> LoadPlan)
// and asserts every trip resolves as inherit, with the plan's restored global
// filters applied to those inherit trips.
func TestLegacyFixtures_PlanLoadsAsInherit(t *testing.T) {
	plansPath := filepath.Join("testdata", "legacy_plans.json")

	// Real load path, step 1: read the legacy plans.json from disk.
	plans, err := LoadPlans(plansPath)
	if err != nil {
		t.Fatalf("LoadPlans(%s): %v", plansPath, err)
	}
	if len(plans) != 1 {
		t.Fatalf("loaded %d plans, want 1", len(plans))
	}

	// Real load path, step 2+3: build the Planner and LoadPlan the legacy plan.
	p := NewPlanner(PlannerOptions{
		Charts:    []*ResortChart{minimalChart()},
		Plans:     plans,
		PlansPath: plansPath,
		Defaults: Defaults{
			From: "2026-01-04", To: "2026-01-08", Budget: "200", MinNights: "1",
		},
	})
	if !p.LoadPlan("legacy") {
		t.Fatal("LoadPlan(legacy) returned false")
	}

	// The plan's filter snapshot was restored into the global config.
	if got := p.global.ExcludeRoomTypes; !slices.Contains(got, "STUDIO") {
		t.Fatalf("plan global ExcludeRoomTypes = %v, want STUDIO restored", got)
	}

	s := p.Snapshot()
	if len(s.Trips) != 2 {
		t.Fatalf("Trips = %d, want 2", len(s.Trips))
	}

	for i, tr := range s.Trips {
		// Backward-compat guarantee: every legacy trip resolves as inherit.
		if tr.Spec.FilterMode != FilterModeInherit {
			t.Errorf("trip %d FilterMode = %q, want inherit", i, tr.Spec.FilterMode)
		}
		if tr.Spec.Filters != nil {
			t.Errorf("trip %d Spec.Filters = %+v, want nil", i, tr.Spec.Filters)
		}
		// Global filters are applied to inherit trips: EffectiveFilters equals
		// the global set restored from the plan.
		if !reflect.DeepEqual(tr.EffectiveFilters, p.global.AsFilterSet()) {
			t.Errorf("trip %d EffectiveFilters = %+v, want global %+v",
				i, tr.EffectiveFilters, p.global.AsFilterSet())
		}
		// minimalChart only has STUDIO rooms, which the restored global excludes,
		// so an inherit trip honoring the global filter yields zero results.
		if len(tr.Results) != 0 {
			t.Errorf("inherit trip %d ignored global STUDIO exclusion: %d results",
				i, len(tr.Results))
		}
	}
}

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
