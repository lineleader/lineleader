package dvc

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestParseDate(t *testing.T) {
	cases := []struct {
		in      string
		wantOK  bool
		wantISO string
	}{
		{"2026-01-04", true, "2026-01-04"},
		{"1/4/2026", true, "2026-01-04"},
		{"01/04/2026", true, "2026-01-04"},
		{"not-a-date", false, ""},
		{"", false, ""},
	}
	for _, c := range cases {
		got, err := ParseDate(c.in)
		if c.wantOK {
			if err != nil {
				t.Errorf("ParseDate(%q) err = %v, want nil", c.in, err)
				continue
			}
			if got.Format("2006-01-02") != c.wantISO {
				t.Errorf("ParseDate(%q) = %s, want %s", c.in, got.Format("2006-01-02"), c.wantISO)
			}
		} else if err == nil {
			t.Errorf("ParseDate(%q) err = nil, want error", c.in)
		}
	}
}

// newTestTUIModel creates a model with the minimal chart and valid default field
// values. plansPath points at a temp file so plan-save tests never touch the
// real config dir.
func newTestTUIModel(t *testing.T) tuiModel {
	t.Helper()
	return NewTUIModel(PlannerOptions{
		Charts:    []*ResortChart{minimalChart()},
		PlansPath: filepath.Join(t.TempDir(), "plans.json"),
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "100",
			MinNights: "1",
		},
	})
}

func TestTUIRecompute_ValidParams(t *testing.T) {
	m := newTestTUIModel(t)
	if m.snap.Trips[0].Err != "" {
		t.Fatalf("unexpected error: %s", m.snap.Trips[0].Err)
	}
	if len(m.snap.Trips[0].Results) == 0 {
		t.Error("expected results, got none")
	}
}

func TestTUIRecompute_InvalidFromDate(t *testing.T) {
	m := newTestTUIModel(t)
	prev := len(m.snap.Trips[0].Results)
	m.planner.SetTripField(0, 0, "not-a-date")
	m.refresh()
	if m.snap.Trips[0].Err == "" {
		t.Error("expected validation error for invalid date, got empty")
	}
	if len(m.snap.Trips[0].Results) != prev {
		t.Errorf("results changed on invalid input: was %d, now %d", prev, len(m.snap.Trips[0].Results))
	}
}

// TestTUIOffset_ClampedWhenResultsShrink verifies the view-only offset is clamped
// back into range after a planner op shrinks a trip's results.
func TestTUIOffset_ClampedWhenResultsShrink(t *testing.T) {
	m := newTestTUIModel(t)
	if len(m.snap.Trips[0].Results) == 0 {
		t.Skip("no results with default params, skipping offset clamp test")
	}
	m.offsets[0] = len(m.snap.Trips[0].Results) - 1
	m.planner.SetBudget("9") // very tight budget — zero results (min rate = 10)
	m.refresh()
	if len(m.snap.Trips[0].Results) > 0 && m.offsets[0] >= len(m.snap.Trips[0].Results) {
		t.Errorf("offset %d not clamped; results len = %d", m.offsets[0], len(m.snap.Trips[0].Results))
	}
	if len(m.snap.Trips[0].Results) == 0 && m.offsets[0] != 0 {
		t.Errorf("offset %d should be 0 when results are empty", m.offsets[0])
	}
}

// TestTUIOffset_InvalidBudgetResetsOffset is a regression test for a slice-bounds
// panic where clearing Results without resetting the scroll offset caused
// View to slice results[8:0].
func TestTUIOffset_InvalidBudgetResetsOffset(t *testing.T) {
	m := newTestTUIModel(t)
	if len(m.snap.Trips[0].Results) == 0 {
		t.Skip("no results with default params")
	}
	m.offsets[0] = len(m.snap.Trips[0].Results) - 1 // scrolled to last row
	m.planner.SetBudget("100{")                     // invalid — as if user typed '{'
	m.refresh()
	if m.offsets[0] != 0 {
		t.Errorf("offset = %d after invalid budget; want 0 to prevent slice-bounds panic", m.offsets[0])
	}
}

func TestTUIUpdate_TabCyclesFocus(t *testing.T) {
	m := newTestTUIModel(t)
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
	m := newTestTUIModel(t)
	m.focused = 2
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = next.(tuiModel)
	if m.focused != 1 {
		t.Errorf("after shift+tab from 2, focused = %d, want 1", m.focused)
	}
}

func TestTUIUpdate_QuitFromTable(t *testing.T) {
	m := newTestTUIModel(t)
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
	m := newTestTUIModel(t)
	m.focused = 2
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(tuiModel)
	if m.focused != 4 {
		t.Errorf("after esc, focused = %d, want 4 (table)", m.focused)
	}
}

func TestTUIUpdate_QDoesNotQuitFromInputField(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 0
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd != nil {
		t.Error("q should not quit when an input field is focused")
	}
}

func TestTUIUpdate_CtrlCAlwaysQuits(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 0 // focused on an input field
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected quit cmd for ctrl+c, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("expected QuitMsg, got %T", cmd())
	}
}

// TestTUIUpdate_TypingAppendsToField is an end-to-end Update -> Snapshot cycle:
// keystrokes route through the planner and the cached snapshot reflects them.
func TestTUIUpdate_TypingAppendsToField(t *testing.T) {
	m := newTestTUIModel(t)
	// Clear From, then type "20".
	m.planner.SetTripField(0, 0, "")
	m.refresh()
	m.focused = 0
	next, _ := m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = next.(tuiModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	m = next.(tuiModel)
	if got := m.snap.Trips[0].Spec.From; got != "20" {
		t.Errorf("From value = %q, want %q", got, "20")
	}
}

func TestTUIUpdate_BackspaceDeletesLastRune(t *testing.T) {
	m := newTestTUIModel(t)
	m.planner.SetBudget("100")
	m.refresh()
	m.focused = 3 // budget field
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = next.(tuiModel)
	if m.snap.Budget != "10" {
		t.Errorf("after backspace, budget = %q, want %q", m.snap.Budget, "10")
	}
}

func TestTUIUpdate_FOpensPanelWhenTableFocused(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 4 // table focus
	next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = next.(tuiModel)
	if !m.filterOpen {
		t.Error("expected filterOpen = true after pressing f from table focus")
	}
}

func TestTUIUpdate_FDoesNotOpenPanelFromInputField(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 0
	m.planner.SetTripField(0, 0, "")
	m.refresh()
	next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = next.(tuiModel)
	if m.filterOpen {
		t.Error("f should not open filter panel when an input field is focused")
	}
	if m.snap.Trips[0].Spec.From != "f" {
		t.Errorf("f should type into input field, got %q", m.snap.Trips[0].Spec.From)
	}
}

func TestTUIUpdate_FilterPanelEscCloses(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 4
	m.filterOpen = true
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(tuiModel)
	if m.filterOpen {
		t.Error("expected filterOpen = false after esc")
	}
}

func TestTUIUpdate_SpaceTogglesFilterItem(t *testing.T) {
	m := NewTUIModel(PlannerOptions{
		Charts:     []*ResortChart{minimalChart()}, // ResortCode = "TST"
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Defaults: Defaults{
			From: "2026-01-04", To: "2026-01-08", Budget: "200", MinNights: "1",
		},
	})
	m.focused = 4
	// Open the filter panel via the table handler so filterItems are built.
	next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = next.(tuiModel)
	m.filterCursor = 0 // first item should be the resort

	resultsBefore := len(m.snap.Trips[0].Results)

	// Toggle the resort off.
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m = next.(tuiModel)

	if m.filterItems[0].enabled {
		t.Error("expected filterItems[0].enabled = false after space toggle")
	}
	if len(m.snap.Trips[0].Results) >= resultsBefore && resultsBefore > 0 {
		t.Errorf("expected fewer results after excluding resort; before=%d after=%d",
			resultsBefore, len(m.snap.Trips[0].Results))
	}
}

func TestTUIUpdate_FilterPanelJKNavigation(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 4
	next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = next.(tuiModel)
	start := m.filterCursor

	// j should move down (same as down arrow).
	next, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = next.(tuiModel)
	if m.filterCursor == start {
		t.Error("j should move filterCursor down")
	}

	// k should move back up.
	next, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = next.(tuiModel)
	if m.filterCursor != start {
		t.Errorf("k should move filterCursor back to %d, got %d", start, m.filterCursor)
	}
}

func TestTUIUpdate_FilterPanelXTogglesItem(t *testing.T) {
	m := NewTUIModel(PlannerOptions{
		Charts:     []*ResortChart{minimalChart()},
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Defaults: Defaults{
			From: "2026-01-04", To: "2026-01-08", Budget: "200", MinNights: "1",
		},
	})
	m.focused = 4
	next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = next.(tuiModel)
	m.filterCursor = 0

	resultsBefore := len(m.snap.Trips[0].Results)

	next, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(tuiModel)

	if m.filterItems[0].enabled {
		t.Error("expected filterItems[0].enabled = false after x toggle")
	}
	if len(m.snap.Trips[0].Results) >= resultsBefore && resultsBefore > 0 {
		t.Errorf("expected fewer results after excluding resort; before=%d after=%d",
			resultsBefore, len(m.snap.Trips[0].Results))
	}
}

func TestTUIUpdate_FiltersAppliedToResults(t *testing.T) {
	m := NewTUIModel(PlannerOptions{
		Charts: []*ResortChart{minimalChart()}, // ResortCode = "TST"
		Global: Config{ExcludeResorts: []string{"TST"}},
		Defaults: Defaults{
			From: "2026-01-04", To: "2026-01-08", Budget: "200", MinNights: "1",
		},
	})
	if len(m.snap.Trips[0].Results) != 0 {
		t.Errorf("expected 0 results with TST excluded, got %d", len(m.snap.Trips[0].Results))
	}
}

// --- Group 4: multi-trip key bindings ---

func TestTUIUpdate_PlusAddsTrip(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 4
	if len(m.snap.Trips) != 1 {
		t.Fatalf("initial trips = %d, want 1", len(m.snap.Trips))
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
	m = next.(tuiModel)
	if len(m.snap.Trips) != 2 {
		t.Errorf("after +, trips = %d, want 2", len(m.snap.Trips))
	}
	if m.activeTripIdx != 1 {
		t.Errorf("activeTripIdx = %d, want 1", m.activeTripIdx)
	}
	if len(m.offsets) != 2 {
		t.Errorf("offsets len = %d, want 2 after AddTrip", len(m.offsets))
	}
}

func TestTUIUpdate_MinusRemovesTrip(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 4
	next, _ := m.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
	m = next.(tuiModel)
	if len(m.snap.Trips) != 2 {
		t.Fatalf("expected 2 trips after +, got %d", len(m.snap.Trips))
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: '-', Text: "-"})
	m = next.(tuiModel)
	if len(m.snap.Trips) != 1 {
		t.Errorf("after -, trips = %d, want 1", len(m.snap.Trips))
	}
	if len(m.offsets) != 1 {
		t.Errorf("offsets len = %d, want 1 after RemoveTrip", len(m.offsets))
	}
}

func TestTUIUpdate_MinusNoopOnSingleTrip(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 4
	next, _ := m.Update(tea.KeyPressMsg{Code: '-', Text: "-"})
	m = next.(tuiModel)
	if len(m.snap.Trips) != 1 {
		t.Errorf("- on single trip should be a no-op; trips = %d", len(m.snap.Trips))
	}
}

func TestTUIUpdate_BracketSwitchesActiveTrip(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 4
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
	m := newTestTUIModel(t)
	m.focused = 4
	if len(m.snap.Trips[0].Results) == 0 {
		t.Skip("no results to select")
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(tuiModel)
	if m.snap.Trips[0].Selected == nil {
		t.Error("expected Selected to be set after enter")
	}
}

func TestTUIUpdate_EnterDeselectsResult(t *testing.T) {
	m := newTestTUIModel(t)
	m.focused = 4
	if len(m.snap.Trips[0].Results) == 0 {
		t.Skip("no results to select")
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(tuiModel)
	if m.snap.Trips[0].Selected == nil {
		t.Fatal("expected Selected after first enter")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(tuiModel)
	if m.snap.Trips[0].Selected != nil {
		t.Error("expected Selected = nil after second enter (deselect)")
	}
}

// --- Group 5: loaded plan tracking via the plans panel ---

func TestPlansPanel_SaveSetsLoadedPlanName(t *testing.T) {
	m := newTestTUIModel(t)
	m.plansOpen = true
	m.plansNaming = true
	for _, r := range "summer" {
		next, _ := m.Update(tea.KeyPressMsg{Text: string(r)})
		m = next.(tuiModel)
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(tuiModel)
	if m.plansErr != "" {
		t.Fatalf("unexpected save error: %s", m.plansErr)
	}
	if m.snap.LoadedPlanName != "summer" {
		t.Errorf("LoadedPlanName = %q, want %q", m.snap.LoadedPlanName, "summer")
	}
}

func TestPlansPanel_LoadAppliesPlan(t *testing.T) {
	m := newTestTUIModel(t)
	// Save the current state as a plan, then mutate, then load it back.
	if err := m.planner.SavePlan("spring"); err != nil {
		t.Fatalf("save: %v", err)
	}
	m.planner.SetTripField(0, 0, "2026-12-01")
	m.refresh()

	m.plansOpen = true
	m.plansCursor = 0
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(tuiModel)

	if m.snap.Trips[0].Spec.From != "2026-01-04" {
		t.Errorf("after load, From = %q, want restored 2026-01-04", m.snap.Trips[0].Spec.From)
	}
	if m.snap.LoadedPlanName != "spring" {
		t.Errorf("LoadedPlanName = %q, want spring", m.snap.LoadedPlanName)
	}
}

func TestPlansPanel_DeleteLoadedClearsLoadedPlanName(t *testing.T) {
	m := newTestTUIModel(t)
	if err := m.planner.SavePlan("only-one"); err != nil {
		t.Fatalf("save: %v", err)
	}
	m.refresh()
	if m.snap.LoadedPlanName != "only-one" {
		t.Fatalf("setup: LoadedPlanName = %q, want only-one", m.snap.LoadedPlanName)
	}
	m.plansOpen = true
	m.plansCursor = 0
	next, _ := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = next.(tuiModel)
	if m.snap.LoadedPlanName != "" {
		t.Errorf("LoadedPlanName = %q, want empty after deleting loaded plan", m.snap.LoadedPlanName)
	}
}

func TestPlansPanel_UpdateOverwritesLoadedPlan(t *testing.T) {
	m := newTestTUIModel(t)
	if err := m.planner.SavePlan("trip-a"); err != nil {
		t.Fatalf("save: %v", err)
	}
	m.refresh()
	if len(m.planner.Plans()) != 1 {
		t.Fatalf("setup: plans len = %d, want 1", len(m.planner.Plans()))
	}

	// Mutate a field, then press 'u' to upsert the loaded plan.
	m.planner.SetTripField(0, 0, "2026-12-01")
	m.refresh()
	m.plansOpen = true
	next, _ := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	m = next.(tuiModel)

	plans := m.planner.Plans()
	if len(plans) != 1 {
		t.Errorf("plans len = %d, want 1 (u should upsert, not append)", len(plans))
	}
	if plans[0].Name != "trip-a" {
		t.Errorf("plans[0].Name = %q, want trip-a", plans[0].Name)
	}
	if len(plans[0].Trips) == 0 || plans[0].Trips[0].From != "2026-12-01" {
		t.Errorf("saved Trips[0].From = %+v, want From=2026-12-01", plans[0].Trips)
	}
	if m.plansErr != "" {
		t.Errorf("unexpected plansErr: %s", m.plansErr)
	}
}

func TestPlansPanel_UpdateNoopWhenNoPlanLoaded(t *testing.T) {
	m := newTestTUIModel(t)
	m.plansOpen = true
	next, _ := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	m = next.(tuiModel)
	if len(m.planner.Plans()) != 0 {
		t.Errorf("plans len = %d, want 0 (u with nothing loaded should be a no-op)", len(m.planner.Plans()))
	}
	if m.plansErr != "" {
		t.Errorf("unexpected plansErr: %s", m.plansErr)
	}
}

func TestTUIUpdate_SelectionDeductsFromOtherTrip(t *testing.T) {
	m := NewTUIModel(PlannerOptions{
		Charts: []*ResortChart{minimalChart()},
		Defaults: Defaults{
			From: "2026-01-04", To: "2026-01-08", Budget: "200", MinNights: "1",
		},
	})
	if len(m.snap.Trips[0].Results) == 0 {
		t.Skip("no results for trip 0")
	}

	// Add trip 2 with same dates.
	m.focused = 4
	next, _ := m.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
	m = next.(tuiModel)
	m.activeTripIdx = 0 // go back to trip 0

	resultsBefore := len(m.snap.Trips[1].Results)

	// Select a result on trip 0 — should reduce trip 1's effective budget.
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(tuiModel)

	if m.snap.Trips[0].Selected == nil {
		t.Fatal("expected trip 0 to have a selection")
	}
	if m.snap.Trips[0].Selected.Points == 0 {
		t.Skip("selected stay has 0 points, can't test budget effect")
	}
	if len(m.snap.Trips[1].Results) > resultsBefore {
		t.Errorf("trip 1 results grew after trip 0 selection: before=%d after=%d",
			resultsBefore, len(m.snap.Trips[1].Results))
	}
}
