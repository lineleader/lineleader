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
// apply to a Search call through EffectiveFilters, exactly as an inherit
// trip would resolve them. An empty-config control proves the zero result is
// caused by the global filter and not an unrelated reason.
func TestLegacyFixtures_ConfigGlobalFiltersApply(t *testing.T) {
	configPath := filepath.Join("testdata", "legacy_config.json")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(%s): %v", configPath, err)
	}
	if !slices.Contains(cfg.ExcludeRoomTypes, "STUDIO") {
		t.Fatalf("legacy config did not load global filters: %+v", cfg)
	}

	from, _ := ParseDate("2026-01-04")
	to, _ := ParseDate("2026-01-08")
	search := func(global Config) []StayResult {
		filters := EffectiveFilters(global, FilterModeInherit, FilterSet{})
		return Search([]*ResortChart{minimalChart()}, SearchParams{
			WindowStart:      from,
			WindowEnd:        to,
			Budget:           200,
			MinNights:        1,
			ExcludeResorts:   filters.ExcludeResorts,
			ExcludeRoomTypes: filters.ExcludeRoomTypes,
		})
	}

	// With the legacy config's STUDIO exclusion, an inherit-mode search
	// honors the global filter and yields zero results.
	withFilter := search(cfg)
	if len(withFilter) != 0 {
		t.Errorf("inherit search ignored legacy config STUDIO exclusion: %d results", len(withFilter))
	}

	// Control: with no global filters the same search has results, confirming
	// the zero above is caused by the loaded config filter.
	withoutFilter := search(Config{})
	if len(withoutFilter) == 0 {
		t.Fatal("expected results with no global filters; got none")
	}
}
