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
