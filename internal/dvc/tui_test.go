package dvc

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// newTestTUIModel creates a model with the minimal chart and valid default field values.
func newTestTUIModel() tuiModel {
	m := newTUIModel([]*ResortChart{minimalChart()})
	m.fields[0].value = "2026-01-04"
	m.fields[1].value = "2026-01-08"
	m.fields[2].value = "100"
	m.fields[3].value = "1"
	return m
}

func TestTUIRecompute_ValidParams(t *testing.T) {
	m := newTestTUIModel()
	m = m.recompute()
	if m.err != "" {
		t.Fatalf("unexpected error: %s", m.err)
	}
	if len(m.results) == 0 {
		t.Error("expected results, got none")
	}
}

func TestTUIRecompute_InvalidFromDate(t *testing.T) {
	m := newTestTUIModel()
	m = m.recompute() // prime with results
	prev := len(m.results)
	m.fields[0].value = "not-a-date"
	m = m.recompute()
	if m.err == "" {
		t.Error("expected validation error for invalid date, got empty")
	}
	if len(m.results) != prev {
		t.Errorf("results changed on invalid input: was %d, now %d", prev, len(m.results))
	}
}

func TestTUIRecompute_OffsetClamped(t *testing.T) {
	m := newTestTUIModel()
	m = m.recompute()
	if len(m.results) == 0 {
		t.Skip("no results with default params, skipping offset clamp test")
	}
	m.offset = len(m.results) - 1
	m.fields[2].value = "9" // very tight budget — zero results from minimal chart (min rate = 10)
	m = m.recompute()
	if len(m.results) > 0 && m.offset >= len(m.results) {
		t.Errorf("offset %d not clamped; results len = %d", m.offset, len(m.results))
	}
	if len(m.results) == 0 && m.offset != 0 {
		t.Errorf("offset %d should be 0 when results are empty", m.offset)
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
	m.fields[0].value = ""
	m.focused = 0
	next, _ := m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = next.(tuiModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	m = next.(tuiModel)
	if m.fields[0].value != "20" {
		t.Errorf("field value = %q, want %q", m.fields[0].value, "20")
	}
}

func TestTUIUpdate_BackspaceDeletesLastRune(t *testing.T) {
	m := newTestTUIModel()
	m.fields[2].value = "100"
	m.focused = 2
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = next.(tuiModel)
	if m.fields[2].value != "10" {
		t.Errorf("after backspace, field = %q, want %q", m.fields[2].value, "10")
	}
}

func TestTUIUpdate_FOpensPanelWhenTableFocused(t *testing.T) {
	m := newTestTUIModel()
	m = m.recompute()
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
	m.fields[0].value = ""
	next, _ := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = next.(tuiModel)
	if m.filterOpen {
		t.Error("f should not open filter panel when an input field is focused")
	}
	if m.fields[0].value != "f" {
		t.Errorf("f should type into input field, got %q", m.fields[0].value)
	}
}

func TestTUIUpdate_FilterPanelEscCloses(t *testing.T) {
	m := newTestTUIModel()
	m = m.recompute()
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
	m.fields[0].value = "2026-01-04"
	m.fields[1].value = "2026-01-08"
	m.fields[2].value = "200"
	m.fields[3].value = "1"
	m = m.withFilters(Config{})
	m = m.recompute()
	m.focused = 4
	m.filterOpen = true
	m.filterCursor = 0 // first item should be the resort

	resultsBefore := len(m.results)

	// Toggle the resort off
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m = next.(tuiModel)

	if m.filterItems[0].enabled {
		t.Error("expected filterItems[0].enabled = false after space toggle")
	}
	if len(m.results) >= resultsBefore && resultsBefore > 0 {
		t.Errorf("expected fewer results after excluding resort; before=%d after=%d",
			resultsBefore, len(m.results))
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
	m = m.recompute()
	m.focused = 4
	m.filterOpen = true
	m.filterCursor = 0

	resultsBefore := len(m.results)

	next, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(tuiModel)

	if m.filterItems[0].enabled {
		t.Error("expected filterItems[0].enabled = false after x toggle")
	}
	if len(m.results) >= resultsBefore && resultsBefore > 0 {
		t.Errorf("expected fewer results after excluding resort; before=%d after=%d",
			resultsBefore, len(m.results))
	}
}

func TestTUIUpdate_FiltersAppliedToResults(t *testing.T) {
	chart := minimalChart() // ResortCode = "TST"
	m := newTUIModel([]*ResortChart{chart})
	m.fields[0].value = "2026-01-04"
	m.fields[1].value = "2026-01-08"
	m.fields[2].value = "200"
	m.fields[3].value = "1"
	cfg := Config{ExcludeResorts: []string{"TST"}}
	m = m.withFilters(cfg)
	m = m.recompute()
	if len(m.results) != 0 {
		t.Errorf("expected 0 results with TST excluded, got %d", len(m.results))
	}
}
