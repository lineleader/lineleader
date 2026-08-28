package web

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lineleader/lineleader/internal/dvc"
)

func TestGlobalFilters_ToggleResortRoundTripAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	g := newGlobalFilters(dvc.Config{}, path)

	if err := g.ToggleResort("TST"); err != nil {
		t.Fatalf("ToggleResort add: %v", err)
	}
	if !slices.Contains(g.Get().ExcludeResorts, "TST") {
		t.Errorf("after add: ExcludeResorts = %v, want to contain TST", g.Get().ExcludeResorts)
	}

	// Persisted to disk.
	cfg, err := dvc.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !slices.Contains(cfg.ExcludeResorts, "TST") {
		t.Errorf("saved config ExcludeResorts = %v, want to contain TST", cfg.ExcludeResorts)
	}

	// Second toggle removes it (round-trip).
	if err := g.ToggleResort("TST"); err != nil {
		t.Fatalf("ToggleResort remove: %v", err)
	}
	if slices.Contains(g.Get().ExcludeResorts, "TST") {
		t.Errorf("after remove: ExcludeResorts = %v, want TST gone", g.Get().ExcludeResorts)
	}
	cfg, err = dvc.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after remove: %v", err)
	}
	if slices.Contains(cfg.ExcludeResorts, "TST") {
		t.Errorf("saved config after remove still has TST: %v", cfg.ExcludeResorts)
	}
}

func TestGlobalFilters_ToggleRoomTypeRoundTripAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	g := newGlobalFilters(dvc.Config{}, path)

	if err := g.ToggleRoomType("STUDIO"); err != nil {
		t.Fatalf("ToggleRoomType add: %v", err)
	}
	if !slices.Contains(g.Get().ExcludeRoomTypes, "STUDIO") {
		t.Errorf("after add: ExcludeRoomTypes = %v, want to contain STUDIO", g.Get().ExcludeRoomTypes)
	}
	cfg, err := dvc.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !slices.Contains(cfg.ExcludeRoomTypes, "STUDIO") {
		t.Errorf("saved config ExcludeRoomTypes = %v, want to contain STUDIO", cfg.ExcludeRoomTypes)
	}

	if err := g.ToggleRoomType("STUDIO"); err != nil {
		t.Fatalf("ToggleRoomType remove: %v", err)
	}
	if slices.Contains(g.Get().ExcludeRoomTypes, "STUDIO") {
		t.Errorf("after remove: ExcludeRoomTypes = %v, want STUDIO gone", g.Get().ExcludeRoomTypes)
	}
}

// TestGlobalFilters_SaveErrorReturnedButStateMutated carries over the
// planner-era TestToggleGlobalResort_SaveErrorReturnedButStateMutated: on a
// SaveConfig error, the in-memory state is still mutated and the error is
// returned — the toggle is not rolled back.
func TestGlobalFilters_SaveErrorReturnedButStateMutated(t *testing.T) {
	dir := t.TempDir()
	g := newGlobalFilters(dvc.Config{}, filepath.Join(dir, "config.json"))

	// Point path at a path whose parent is a file, so MkdirAll/Save fails.
	fileAsDir := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	g.path = filepath.Join(fileAsDir, "config.json")

	err := g.ToggleResort("TST")
	if err == nil {
		t.Fatal("expected SaveConfig error, got nil")
	}
	// In-memory toggle still happened (not rolled back).
	if !slices.Contains(g.Get().ExcludeResorts, "TST") {
		t.Errorf("state rolled back on save error: ExcludeResorts = %v", g.Get().ExcludeResorts)
	}
}
