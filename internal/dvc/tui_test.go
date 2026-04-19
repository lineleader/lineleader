package dvc

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// newTestTUIModel creates a model with the minimal chart and valid default field values.
func newTestTUIModel() tuiModel {
	m := newTUIModel([]*ResortChart{minimalChart()})
	m.trips[0].Fields[0].value = "2026-01-04"
	m.trips[0].Fields[1].value = "2026-01-08"
	m.trips[0].Fields[2].value = "1"
	m.budgetField.value = "100"
	return m
}

func TestTUIRecompute_ValidParams(t *testing.T) {
	m := newTestTUIModel()
	m = m.recomputeAll()
	if m.trips[0].Err != "" {
		t.Fatalf("unexpected error: %s", m.trips[0].Err)
	}
	if len(m.trips[0].Results) == 0 {
		t.Error("expected results, got none")
	}
}

func TestTUIRecompute_InvalidFromDate(t *testing.T) {
	m := newTestTUIModel()
	m = m.recomputeAll() // prime with results
	prev := len(m.trips[0].Results)
	m.trips[0].Fields[0].value = "not-a-date"
	m = m.recomputeAll()
	if m.trips[0].Err == "" {
		t.Error("expected validation error for invalid date, got empty")
	}
	if len(m.trips[0].Results) != prev {
		t.Errorf("results changed on invalid input: was %d, now %d", prev, len(m.trips[0].Results))
	}
}

func TestTUIRecompute_OffsetClamped(t *testing.T) {
	m := newTestTUIModel()
	m = m.recomputeAll()
	if len(m.trips[0].Results) == 0 {
		t.Skip("no results with default params, skipping offset clamp test")
	}
	m.trips[0].Offset = len(m.trips[0].Results) - 1
	m.budgetField.value = "9" // very tight budget — zero results from minimal chart (min rate = 10)
	m = m.recomputeAll()
	if len(m.trips[0].Results) > 0 && m.trips[0].Offset >= len(m.trips[0].Results) {
		t.Errorf("offset %d not clamped; results len = %d", m.trips[0].Offset, len(m.trips[0].Results))
	}
	if len(m.trips[0].Results) == 0 && m.trips[0].Offset != 0 {
		t.Errorf("offset %d should be 0 when results are empty", m.trips[0].Offset)
	}
}

func TestTUIUpdate_TabCyclesFocus(t *testing.T) {
	m := newTestTUIModel()
	if m.focused != 0 {
		t.Fatalf("initial focused = %d, want 0", m.focused)
	}
	// Tab should cycle through 0→1→2→3→4→0
	for i := 1; i <= 5; i++ {
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		m = next.(tuiModel)
		want := i % 5
		if m.focused != want {
			t.Errorf("after %d tab(s), focused = %d, want %d", i, m.focused, want)
		}
	}
}

func TestTUIUpdate_ShiftTabCyclesBackward(t *testing.T) {
	m := newTestTUIModel()
	m.focused = 2
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = next.(tuiModel)
	if m.focused != 1 {
		t.Errorf("after shift+tab from 2, focused = %d, want 1", m.focused)
	}
}

func TestTUIUpdate_QuitFromTable(t *testing.T) {
	m := newTestTUIModel()
	m.focused = 4 // table focus
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("expected quit cmd, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("expected QuitMsg, got %T", cmd())
	}
}

func TestTUIUpdate_EscMovesToTableFocus(t *testing.T) {
	m := newTestTUIModel()
	m.focused = 2
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(tuiModel)
	if m.focused != 4 {
		t.Errorf("after esc, focused = %d, want 4 (table)", m.focused)
	}
}

func TestTUIUpdate_QDoesNotQuitFromInputField(t *testing.T) {
	m := newTestTUIModel()
	m.focused = 0
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd != nil {
		t.Error("q should not quit when an input field is focused")
	}
}

func TestTUIUpdate_CtrlCAlwaysQuits(t *testing.T) {
	m := newTestTUIModel()
	m.focused = 0 // focused on an input field
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected quit cmd for ctrl+c, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("expected QuitMsg, got %T", cmd())
	}
}

func TestTUIUpdate_TypingAppendsToField(t *testing.T) {
	m := newTestTUIModel()
	m.trips[0].Fields[0].value = ""
	m.focused = 0
	next, _ := m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = next.(tuiModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	m = next.(tuiModel)
	if m.trips[0].Fields[0].value != "20" {
		t.Errorf("field value = %q, want %q", m.trips[0].Fields[0].value, "20")
	}
}

func TestTUIUpdate_BackspaceDeletesLastRune(t *testing.T) {
	m := newTestTUIModel()
	m.budgetField.value = "100"
	m.focused = 3 // budget field
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = next.(tuiModel)
	if m.budgetField.value != "10" {
		t.Errorf("after backspace, budget = %q, want %q", m.budgetField.value, "10")
	}
}

func TestTUIUpdate_FOpensPanelWhenTableFocused(t *testing.T) {
	m := newTestTUIModel()
	m = m.recomputeAll()
	m.focused = 4 // table focus
	next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = next.(tuiModel)
	if !m.filterOpen {
		t.Error("expected filterOpen = true after pressing f from table focus")
	}
}

func TestTUIUpdate_FDoesNotOpenPanelFromInputField(t *testing.T) {
	m := newTestTUIModel()
	m.focused = 0
	m.trips[0].Fields[0].value = ""
	next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = next.(tuiModel)
	if m.filterOpen {
		t.Error("f should not open filter panel when an input field is focused")
	}
	if m.trips[0].Fields[0].value != "f" {
		t.Errorf("f should type into input field, got %q", m.trips[0].Fields[0].value)
	}
}

func TestTUIUpdate_FilterPanelEscCloses(t *testing.T) {
	m := newTestTUIModel()
	m = m.recomputeAll()
	m.focused = 4
	m.filterOpen = true
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(tuiModel)
	if m.filterOpen {
		t.Error("expected filterOpen = false after esc")
	}
}

func TestTUIUpdate_SpaceTogglesFilterItem(t *testing.T) {
	chart := minimalChart() // ResortCode = "TST", RoomType = "STUDIO"
	m := newTUIModel([]*ResortChart{chart})
	m.trips[0].Fields[0].value = "2026-01-04"
	m.trips[0].Fields[1].value = "2026-01-08"
	m.trips[0].Fields[2].value = "1"
	m.budgetField.value = "200"
	m = m.withFilters(Config{})
	m = m.recomputeAll()
	m.focused = 4
	m.filterOpen = true
	m.filterCursor = 0 // first item should be the resort

	resultsBefore := len(m.trips[0].Results)

	// Toggle the resort off
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m = next.(tuiModel)

	if m.filterItems[0].enabled {
		t.Error("expected filterItems[0].enabled = false after space toggle")
	}
	if len(m.trips[0].Results) >= resultsBefore && resultsBefore > 0 {
		t.Errorf("expected fewer results after excluding resort; before=%d after=%d",
			resultsBefore, len(m.trips[0].Results))
	}
}

func TestTUIUpdate_FilterPanelJKNavigation(t *testing.T) {
	chart := minimalChart()
	m := newTUIModel([]*ResortChart{chart})
	m = m.withFilters(Config{})
	m.focused = 4
	m.filterOpen = true
	m.filterCursor = 0

	// j should move down (same as down arrow)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = next.(tuiModel)
	if m.filterCursor == 0 {
		t.Error("j should move filterCursor down")
	}
	afterJ := m.filterCursor

	// k should move back up (same as up arrow)
	next, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = next.(tuiModel)
	if m.filterCursor != 0 {
		t.Errorf("k should move filterCursor back to 0, got %d (was %d after j)", m.filterCursor, afterJ)
	}
}

func TestTUIUpdate_FilterPanelXTogglesItem(t *testing.T) {
	chart := minimalChart()
	m := newTUIModel([]*ResortChart{chart})
	m = m.withFilters(Config{})
	m = m.recomputeAll()
	m.focused = 4
	m.filterOpen = true
	m.filterCursor = 0

	resultsBefore := len(m.trips[0].Results)

	next, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(tuiModel)

	if m.filterItems[0].enabled {
		t.Error("expected filterItems[0].enabled = false after x toggle")
	}
	if len(m.trips[0].Results) >= resultsBefore && resultsBefore > 0 {
		t.Errorf("expected fewer results after excluding resort; before=%d after=%d",
			resultsBefore, len(m.trips[0].Results))
	}
}

func TestTUIUpdate_FiltersAppliedToResults(t *testing.T) {
	chart := minimalChart() // ResortCode = "TST"
	m := newTUIModel([]*ResortChart{chart})
	m.trips[0].Fields[0].value = "2026-01-04"
	m.trips[0].Fields[1].value = "2026-01-08"
	m.trips[0].Fields[2].value = "1"
	m.budgetField.value = "200"
	cfg := Config{ExcludeResorts: []string{"TST"}}
	m = m.withFilters(cfg)
	m = m.recomputeAll()
	if len(m.trips[0].Results) != 0 {
		t.Errorf("expected 0 results with TST excluded, got %d", len(m.trips[0].Results))
	}
}

// TestTUIRecompute_InvalidBudgetResetsOffset is a regression test for a panic
// where typing an invalid character into the budget field (e.g. '{' instead of
// '[') cleared Results without resetting Offset, causing View to slice
// trip.Results[8:0].
func TestTUIRecompute_InvalidBudgetResetsOffset(t *testing.T) {
	m := newTestTUIModel()
	m = m.recomputeAll()
	if len(m.trips[0].Results) == 0 {
		t.Skip("no results with default params")
	}
	m.trips[0].Offset = len(m.trips[0].Results) - 1 // scrolled to last row

	m.budgetField.value = "100{" // invalid — as if user typed '{' by mistake
	m = m.recomputeAll()

	if m.trips[0].Offset != 0 {
		t.Errorf("Offset = %d after invalid budget; want 0 to prevent slice-bounds panic", m.trips[0].Offset)
	}
}

// --- Group 4: multi-trip key bindings ---

func TestTUIUpdate_PlusAddsTrip(t *testing.T) {
	m := newTestTUIModel()
	m.focused = 4
	if len(m.trips) != 1 {
		t.Fatalf("initial trips = %d, want 1", len(m.trips))
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
	m = next.(tuiModel)
	if len(m.trips) != 2 {
		t.Errorf("after +, trips = %d, want 2", len(m.trips))
	}
	if m.activeTripIdx != 1 {
		t.Errorf("activeTripIdx = %d, want 1", m.activeTripIdx)
	}
}

func TestTUIUpdate_MinusRemovesTrip(t *testing.T) {
	m := newTestTUIModel()
	m.focused = 4
	// Add a second trip first.
	next, _ := m.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
	m = next.(tuiModel)
	if len(m.trips) != 2 {
		t.Fatalf("expected 2 trips after +, got %d", len(m.trips))
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: '-', Text: "-"})
	m = next.(tuiModel)
	if len(m.trips) != 1 {
		t.Errorf("after -, trips = %d, want 1", len(m.trips))
	}
}

func TestTUIUpdate_MinusNoopOnSingleTrip(t *testing.T) {
	m := newTestTUIModel()
	m.focused = 4
	next, _ := m.Update(tea.KeyPressMsg{Code: '-', Text: "-"})
	m = next.(tuiModel)
	if len(m.trips) != 1 {
		t.Errorf("- on single trip should be a no-op; trips = %d", len(m.trips))
	}
}

func TestTUIUpdate_BracketSwitchesActiveTrip(t *testing.T) {
	m := newTestTUIModel()
	m.focused = 4
	// Add second trip.
	next, _ := m.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
	m = next.(tuiModel) // activeTripIdx = 1

	// ] wraps — already at last, no change.
	next, _ = m.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	m = next.(tuiModel)
	if m.activeTripIdx != 1 {
		t.Errorf("] beyond last: activeTripIdx = %d, want 1", m.activeTripIdx)
	}

	// [ goes back.
	next, _ = m.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	m = next.(tuiModel)
	if m.activeTripIdx != 0 {
		t.Errorf("after [, activeTripIdx = %d, want 0", m.activeTripIdx)
	}

	// [ at first — no change.
	next, _ = m.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	m = next.(tuiModel)
	if m.activeTripIdx != 0 {
		t.Errorf("[ beyond first: activeTripIdx = %d, want 0", m.activeTripIdx)
	}
}

func TestTUIUpdate_EnterSelectsResult(t *testing.T) {
	m := newTestTUIModel()
	m = m.recomputeAll()
	m.focused = 4
	if len(m.trips[0].Results) == 0 {
		t.Skip("no results to select")
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(tuiModel)
	if m.trips[0].Selected == nil {
		t.Error("expected Selected to be set after enter")
	}
}

func TestTUIUpdate_EnterDeselectsResult(t *testing.T) {
	m := newTestTUIModel()
	m = m.recomputeAll()
	m.focused = 4
	if len(m.trips[0].Results) == 0 {
		t.Skip("no results to select")
	}
	// Select.
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(tuiModel)
	if m.trips[0].Selected == nil {
		t.Fatal("expected Selected after first enter")
	}
	// Deselect.
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(tuiModel)
	if m.trips[0].Selected != nil {
		t.Error("expected Selected = nil after second enter (deselect)")
	}
}

// --- Group 5: loaded plan tracking ---

func TestApplyPlan_SetsLoadedPlanName(t *testing.T) {
	m := newTestTUIModel()
	m = m.applyPlan(Plan{
		Name:   "spring-break",
		Budget: "150",
		Trips:  []TripSpec{{From: "2026-03-15", To: "2026-03-22", MinNights: "3"}},
	})
	if m.loadedPlanName != "spring-break" {
		t.Errorf("loadedPlanName = %q, want %q", m.loadedPlanName, "spring-break")
	}
}

func TestSavePlan_UpdatesLoadedPlanName(t *testing.T) {
	m := newTestTUIModel()
	m.plansPath = filepath.Join(t.TempDir(), "plans.json")
	m = m.savePlan("summer")
	if m.plansErr != "" {
		t.Fatalf("unexpected save error: %s", m.plansErr)
	}
	if m.loadedPlanName != "summer" {
		t.Errorf("loadedPlanName = %q, want %q", m.loadedPlanName, "summer")
	}
}

func TestDeleteLoadedPlan_ClearsLoadedPlanName(t *testing.T) {
	m := newTestTUIModel()
	m.plansPath = filepath.Join(t.TempDir(), "plans.json")
	m = m.savePlan("only-one")
	if m.loadedPlanName != "only-one" {
		t.Fatalf("setup: loadedPlanName = %q, want %q", m.loadedPlanName, "only-one")
	}
	m.plansOpen = true
	m.plansCursor = 0
	next, _ := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = next.(tuiModel)
	if m.loadedPlanName != "" {
		t.Errorf("loadedPlanName = %q, want empty after deleting loaded plan", m.loadedPlanName)
	}
}

func TestUpdateKey_OverwritesLoadedPlan(t *testing.T) {
	m := newTestTUIModel()
	m.plansPath = filepath.Join(t.TempDir(), "plans.json")
	m = m.savePlan("trip-a")
	if len(m.plans) != 1 {
		t.Fatalf("setup: plans len = %d, want 1", len(m.plans))
	}

	// Mutate a trip field in-place before pressing 'u'.
	m.trips[0].Fields[0].value = "2026-12-01"

	m.plansOpen = true
	next, _ := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	m = next.(tuiModel)

	if len(m.plans) != 1 {
		t.Errorf("plans len = %d, want 1 (u should upsert, not append)", len(m.plans))
	}
	if m.plans[0].Name != "trip-a" {
		t.Errorf("plans[0].Name = %q, want %q", m.plans[0].Name, "trip-a")
	}
	if len(m.plans[0].Trips) == 0 || m.plans[0].Trips[0].From != "2026-12-01" {
		t.Errorf("saved Trips[0].From = %+v, want From=2026-12-01", m.plans[0].Trips)
	}
	if m.plansErr != "" {
		t.Errorf("unexpected plansErr: %s", m.plansErr)
	}
}

func TestUpdateKey_NoopWhenNoPlanLoaded(t *testing.T) {
	m := newTestTUIModel()
	m.plansPath = filepath.Join(t.TempDir(), "plans.json")
	m.plansOpen = true

	next, _ := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	m = next.(tuiModel)

	if len(m.plans) != 0 {
		t.Errorf("plans len = %d, want 0 (u with nothing loaded should be a no-op)", len(m.plans))
	}
	if m.plansErr != "" {
		t.Errorf("unexpected plansErr: %s", m.plansErr)
	}
}

func TestTUIUpdate_SelectionDeductsFromOtherTrip(t *testing.T) {
	chart := minimalChart()
	m := newTUIModel([]*ResortChart{chart})
	m.trips[0].Fields[0].value = "2026-01-04"
	m.trips[0].Fields[1].value = "2026-01-08"
	m.trips[0].Fields[2].value = "1"
	m.budgetField.value = "200"
	m = m.recomputeAll()
	if len(m.trips[0].Results) == 0 {
		t.Skip("no results for trip 0")
	}

	// Add trip 2 with same dates.
	m.focused = 4
	next, _ := m.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
	m = next.(tuiModel)
	m.activeTripIdx = 0 // go back to trip 0

	resultsBefore := len(m.trips[1].Results)

	// Select a result on trip 0 — should reduce trip 1's effective budget.
	m.focused = 4
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(tuiModel)

	if m.trips[0].Selected == nil {
		t.Fatal("expected trip 0 to have a selection")
	}
	selectedPts := m.trips[0].Selected.Points
	if selectedPts == 0 {
		t.Skip("selected stay has 0 points, can't test budget effect")
	}
	// Trip 1 should have fewer or equal results (budget shrank).
	if len(m.trips[1].Results) > resultsBefore {
		t.Errorf("trip 1 results grew after trip 0 selection: before=%d after=%d",
			resultsBefore, len(m.trips[1].Results))
	}
}
