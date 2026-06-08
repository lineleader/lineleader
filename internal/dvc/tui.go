package dvc

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Column widths for the results table (in terminal cells).
const (
	colResort   = 30
	colRoomType = 22
	colView     = 5
	colCheckIn  = 10
	colCheckOut = 10
	colNights   = 7
	colPts      = 5
)

// inputField holds the label and current text value for one search parameter.
type inputField struct {
	label string
	value string
}

// Trip represents one planned stay within a multi-trip planning session.
type Trip struct {
	Fields     [3]inputField // 0=From, 1=To, 2=MinNights (Budget is global)
	Results    []StayResult
	Selected   *StayResult // heap-allocated copy; nil = no selection
	Err        string      // per-trip parse/search error
	FilterMode FilterMode  // inherit (zero value) or override the global filters
	Filters    FilterSet   // this trip's exclusions when FilterMode is override
}

// filterItem represents one toggleable entry in the filter panel.
type filterItem struct {
	kind        string // "resort" or "roomtype" or "" (separator)
	value       string // resort code or room type name (used for filtering logic)
	displayName string // human-readable label shown in the UI (full resort name for resorts)
	enabled     bool   // true = included in search (not excluded)
}

// tuiModel is the bubbletea model for the interactive search UI. It is a thin
// view over a *Planner: it owns no domain state, only a cached render-ready
// Snapshot plus view-only state (focus, cursors, scroll offsets, panel flags).
type tuiModel struct {
	planner *Planner
	snap    Snapshot // refreshed after each planner call

	offsets []int // VIEW-ONLY per-trip scroll position (was Trip.Offset)

	width, height int
	focused       int // 0=From, 1=To, 2=MinNights, 3=Budget, 4=Table
	activeTripIdx int

	filterOpen   bool
	filterTrip   int // -1 = global, >=0 = trip i (per-trip scope wired in fpl.11)
	filterCursor int
	filterItems  []filterItem

	plansOpen    bool
	plansNaming  bool   // true while typing a new plan name
	plansCursor  int
	plansNameBuf string // name being typed
	plansErr     string // last save/delete error
}

// NewTUIModel builds a TUI model backed by a Planner constructed from opts. It
// is the single public constructor used by cmd/dvc and tests.
func NewTUIModel(opts PlannerOptions) tuiModel {
	p := NewPlanner(opts)
	m := tuiModel{
		planner:    p,
		snap:       p.Snapshot(),
		focused:    0,
		filterTrip: -1,
	}
	m.reconcileOffsets()
	return m
}

// reconcileOffsets keeps len(m.offsets) == len(m.snap.Trips): it grows with
// zeros, shrinks the tail, clamps each offset against its trip's result count,
// and clamps activeTripIdx into range. Call it after any planner op that can
// change the number of trips or the size of a trip's results.
func (m *tuiModel) reconcileOffsets() {
	n := len(m.snap.Trips)
	if len(m.offsets) < n {
		m.offsets = append(m.offsets, make([]int, n-len(m.offsets))...)
	} else if len(m.offsets) > n {
		m.offsets = m.offsets[:n]
	}
	for i := range m.offsets {
		results := len(m.snap.Trips[i].Results)
		if results == 0 {
			m.offsets[i] = 0
		} else if m.offsets[i] >= results {
			m.offsets[i] = results - 1
		} else if m.offsets[i] < 0 {
			m.offsets[i] = 0
		}
	}
	if m.activeTripIdx >= n {
		m.activeTripIdx = n - 1
	}
	if m.activeTripIdx < 0 {
		m.activeTripIdx = 0
	}
}

// refresh re-reads the Snapshot from the planner and reconciles view-only state
// that depends on it (offsets). Call after every planner mutation.
func (m *tuiModel) refresh() {
	m.snap = m.planner.Snapshot()
	m.reconcileOffsets()
}

// rebuildFilterItems builds filterItems from the planner's FilterOptions for the
// current filterTrip scope (-1 = global, >=0 = trip i). For an inherit trip the
// rows reflect the effective (global) checks; a row toggle auto-seeds override.
func (m *tuiModel) rebuildFilterItems() {
	view := m.planner.FilterOptions(m.filterTrip)
	var items []filterItem
	for _, r := range view.Resorts {
		items = append(items, filterItem{
			kind:        "resort",
			value:       r.Code,
			displayName: r.Name,
			enabled:     r.Enabled,
		})
	}
	items = append(items, filterItem{kind: ""}) // blank separator
	for _, rt := range view.RoomTypes {
		items = append(items, filterItem{
			kind:    "roomtype",
			value:   rt.Name,
			enabled: rt.Enabled,
		})
	}
	m.filterItems = items
}

// openFilterPanel opens the filter panel scoped to tripIdx (-1 = global, >=0 =
// trip i), rebuilds filterItems for that scope, and lands the cursor on a real
// (non-separator) item.
func (m *tuiModel) openFilterPanel(tripIdx int) {
	m.filterTrip = tripIdx
	m.rebuildFilterItems()
	m.filterOpen = true
	if m.filterCursor >= len(m.filterItems) {
		m.filterCursor = 0
	}
	if m.filterCursor < len(m.filterItems) && m.filterItems[m.filterCursor].kind == "" {
		m.filterCursor = m.nextFilterCursor(1)
	}
}

// visibleRowsPerTrip returns how many result rows fit per trip section.
func (m tuiModel) visibleRowsPerTrip() int {
	trips := len(m.snap.Trips)
	if trips == 0 {
		trips = 1
	}
	// Fixed rows per trip: 1 header + 1 sep + 1 col header + 1 sep = 4
	// Plus global bar (1) + global sep (1) + status (1) = 3
	fixed := 3 + trips*4
	rows := (m.height - fixed) / trips
	if rows < 3 {
		rows = 3
	}
	return rows
}

// nextFilterCursor returns the next cursor position in filterItems, skipping separators.
func (m tuiModel) nextFilterCursor(delta int) int {
	n := len(m.filterItems)
	if n == 0 {
		return 0
	}
	pos := m.filterCursor
	for i := 0; i < n; i++ {
		pos = (pos + delta + n) % n
		if m.filterItems[pos].kind != "" {
			return pos
		}
	}
	return m.filterCursor
}

// Init implements tea.Model.
func (m tuiModel) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		return m, nil

	case tea.KeyPressMsg:
		// ctrl+c quits from anywhere.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		// Plans panel handles its own keys when open.
		if m.plansOpen {
			return m.updatePlans(msg)
		}

		// Filter panel handles its own keys when open.
		if m.filterOpen {
			return m.updateFilters(msg)
		}

		switch msg.String() {
		case "tab":
			m.focused = (m.focused + 1) % 5
			return m, nil
		case "shift+tab":
			m.focused = (m.focused + 4) % 5
			return m, nil
		case "esc":
			m.focused = 4 // move to table focus so q works
			return m, nil
		case "q":
			if m.focused == 4 {
				return m, tea.Quit
			}
		}

		if m.focused == 4 { // table is focused
			return m.updateTable(msg)
		}

		// Input field handling (focused 0–3).
		return m.updateField(msg)
	}

	return m, nil
}

// updatePlans handles key presses while the plans panel is open.
func (m tuiModel) updatePlans(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	plans := m.planner.Plans()
	if m.plansNaming {
		switch msg.String() {
		case "esc":
			m.plansNaming = false
			m.plansNameBuf = ""
		case "backspace":
			runes := []rune(m.plansNameBuf)
			if len(runes) > 0 {
				m.plansNameBuf = string(runes[:len(runes)-1])
			}
		case "enter":
			if m.plansNameBuf != "" {
				if err := m.planner.SavePlan(m.plansNameBuf); err != nil {
					m.plansErr = err.Error()
				} else {
					m.plansErr = ""
				}
				m.refresh()
				m.plansNaming = false
				m.plansNameBuf = ""
			}
		default:
			if msg.Text != "" {
				m.plansNameBuf += msg.Text
			}
		}
		return m, nil
	}
	switch msg.String() {
	case "p", "esc":
		m.plansOpen = false
	case "up", "k":
		if m.plansCursor > 0 {
			m.plansCursor--
		}
	case "down", "j":
		if m.plansCursor < len(plans)-1 {
			m.plansCursor++
		}
	case "s":
		m.plansNaming = true
		m.plansNameBuf = ""
	case "u":
		if m.snap.LoadedPlanName != "" {
			if err := m.planner.SavePlan(m.snap.LoadedPlanName); err != nil {
				m.plansErr = err.Error()
			} else {
				m.plansErr = ""
			}
			m.refresh()
		}
	case "d":
		if m.plansCursor < len(plans) {
			name := plans[m.plansCursor].Name
			if err := m.planner.DeletePlan(name); err != nil {
				m.plansErr = err.Error()
			} else {
				m.plansErr = ""
			}
			m.refresh()
			if m.plansCursor >= len(m.planner.Plans()) && m.plansCursor > 0 {
				m.plansCursor--
			}
		}
	case "enter":
		if m.plansCursor < len(plans) {
			if m.planner.LoadPlan(plans[m.plansCursor].Name) {
				m.activeTripIdx = 0
				m.refresh()
			}
			m.plansOpen = false
		}
	}
	return m, nil
}

// updateFilters handles key presses while the filter panel is open. The panel
// is scoped by m.filterTrip: -1 = global (toggles operate on the global config),
// >=0 = a trip (toggles operate on that trip's per-trip filters, auto-flipping
// inherit->override via the Planner's seeding; `i` toggles inherit/override and
// `r` resets to inherit).
func (m tuiModel) updateFilters(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "f", "esc":
		m.filterOpen = false
	case "up", "k":
		m.filterCursor = m.nextFilterCursor(-1)
	case "down", "j":
		m.filterCursor = m.nextFilterCursor(1)
	case "i":
		if m.filterTrip >= 0 && m.filterTrip < len(m.snap.Trips) {
			mode := FilterModeOverride
			if m.snap.Trips[m.filterTrip].Spec.FilterMode == FilterModeOverride {
				mode = FilterModeInherit
			}
			m.planner.SetTripFilterMode(m.filterTrip, mode)
			m.refresh()
			m.rebuildFilterItems()
		}
	case "r":
		if m.filterTrip >= 0 && m.filterTrip < len(m.snap.Trips) {
			m.planner.ResetTripFilters(m.filterTrip)
			m.refresh()
			m.rebuildFilterItems()
		}
	case "space", "x":
		if m.filterCursor < len(m.filterItems) {
			item := m.filterItems[m.filterCursor]
			if m.filterTrip < 0 {
				switch item.kind {
				case "resort":
					if err := m.planner.ToggleGlobalResort(item.value); err != nil {
						m.plansErr = err.Error()
					}
				case "roomtype":
					if err := m.planner.ToggleGlobalRoomType(item.value); err != nil {
						m.plansErr = err.Error()
					}
				}
			} else {
				switch item.kind {
				case "resort":
					m.planner.ToggleTripResort(m.filterTrip, item.value)
				case "roomtype":
					m.planner.ToggleTripRoomType(m.filterTrip, item.value)
				}
			}
			m.refresh()
			m.rebuildFilterItems()
		}
	}
	return m, nil
}

// updateTable handles key presses while the results table is focused.
func (m tuiModel) updateTable(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	i := m.activeTripIdx
	switch msg.String() {
	case "p":
		m.plansOpen = true
		m.plansNaming = false
		if m.plansCursor >= len(m.planner.Plans()) {
			m.plansCursor = 0
		}
	case "f":
		m.openFilterPanel(-1)
	case "F":
		m.openFilterPanel(m.activeTripIdx)
	case "up", "k":
		if m.offsets[i] > 0 {
			m.offsets[i]--
		}
	case "down", "j":
		if m.offsets[i] < len(m.snap.Trips[i].Results)-1 {
			m.offsets[i]++
		}
	case "[":
		if m.activeTripIdx > 0 {
			m.activeTripIdx--
		}
	case "]":
		if m.activeTripIdx < len(m.snap.Trips)-1 {
			m.activeTripIdx++
		}
	case "+":
		m.planner.AddTrip()
		m.refresh()
		m.activeTripIdx = len(m.snap.Trips) - 1
	case "-":
		m.planner.RemoveTrip(m.activeTripIdx)
		m.refresh()
	case "enter":
		m.planner.ToggleSelection(i, m.offsets[i])
		m.refresh()
	}
	return m, nil
}

// updateField handles typing into the focused input field (focused 0–3).
func (m tuiModel) updateField(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Resolve the current value for the focused field.
	var current string
	if m.focused == 3 {
		current = m.snap.Budget
	} else {
		current = m.snap.Trips[m.activeTripIdx].Spec.fieldValue(m.focused)
	}

	var next string
	switch msg.String() {
	case "backspace":
		runes := []rune(current)
		if len(runes) == 0 {
			return m, nil
		}
		next = string(runes[:len(runes)-1])
	default:
		if msg.Text == "" {
			return m, nil
		}
		next = current + msg.Text
	}

	if m.focused == 3 {
		m.planner.SetBudget(next)
	} else {
		m.planner.SetTripField(m.activeTripIdx, m.focused, next)
	}
	m.refresh()
	return m, nil
}

// fieldValue returns the raw value for input field idx (0=From, 1=To, 2=MinNights).
func (s TripSpec) fieldValue(idx int) string {
	switch idx {
	case 0:
		return s.From
	case 1:
		return s.To
	default:
		return s.MinNights
	}
}

// View implements tea.Model. It renders entirely from m.snap and view-only state.
func (m tuiModel) View() tea.View {
	var b strings.Builder

	labelStyle := lipgloss.NewStyle().Faint(true)
	activeStyle := lipgloss.NewStyle().Bold(true)
	headerStyle := lipgloss.NewStyle().Bold(true)
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	sepStyle := lipgloss.NewStyle().Faint(true)
	faintStyle := lipgloss.NewStyle().Faint(true)

	sep := sepStyle.Render(strings.Repeat("─", max(m.width, 1)))

	// Global bar: budget field + remaining counter.
	budgetLabel := labelStyle.Render("Budget: ")
	budgetValue := m.snap.Budget
	if m.focused == 3 {
		budgetValue = activeStyle.Render(m.snap.Budget) + "█"
	}
	b.WriteString(budgetLabel + budgetValue)
	b.WriteString(fmt.Sprintf("   Remaining: %d pts", m.snap.Remaining))
	if m.snap.LoadedPlanName != "" {
		b.WriteString("   Plan: " + m.snap.LoadedPlanName)
	}
	b.WriteString("   f: filters   p: plans   q: quit\n")

	switch {
	case m.plansOpen:
		m.renderPlans(&b, sep, headerStyle, activeStyle, errStyle, faintStyle)
	case m.filterOpen:
		m.renderFilters(&b, sep, headerStyle, activeStyle, faintStyle)
	default:
		m.renderTrips(&b, sep, headerStyle, activeStyle, errStyle)
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// renderPlans renders the plans panel into b.
func (m tuiModel) renderPlans(b *strings.Builder, sep string, headerStyle, activeStyle, errStyle, faintStyle lipgloss.Style) {
	plans := m.planner.Plans()
	b.WriteString(sep + "\n")
	b.WriteString(headerStyle.Render("PLANS") + "\n")
	b.WriteString(sep + "\n")

	if len(plans) == 0 && !m.plansNaming {
		b.WriteString(faintStyle.Render("  (no saved plans)") + "\n")
	}
	for i, p := range plans {
		noun := "trip"
		if len(p.Trips) != 1 {
			noun = "trips"
		}
		line := fmt.Sprintf("  %s  (%d %s, budget: %s)", p.Name, len(p.Trips), noun, p.Budget)
		if i == m.plansCursor && !m.plansNaming {
			line = activeStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if m.plansNaming {
		b.WriteString(activeStyle.Render(fmt.Sprintf("  New plan name: %s█", m.plansNameBuf)) + "\n")
	}
	if m.plansErr != "" {
		b.WriteString(errStyle.Render("  error: "+m.plansErr) + "\n")
	}
	b.WriteString(sep + "\n")
	b.WriteString("enter: load  │  s: new  │  u: update  │  d: delete  │  p/esc: close")
}

// renderFilters renders the filter panel into b.
func (m tuiModel) renderFilters(b *strings.Builder, sep string, headerStyle, activeStyle, faintStyle lipgloss.Style) {
	b.WriteString(sep + "\n")
	b.WriteString(headerStyle.Render(m.filterHeader()) + "\n")
	if m.filterTrip >= 0 && m.filterMode() == FilterModeInherit {
		b.WriteString(faintStyle.Render("  (inheriting global — toggling a row overrides this trip)") + "\n")
	}
	b.WriteString(sep + "\n")

	visible := m.visibleRowsPerTrip() * len(m.snap.Trips)
	shown := 0
	for i, item := range m.filterItems {
		if shown >= visible {
			break
		}
		switch item.kind {
		case "":
			b.WriteString("\n")
		case "resort", "roomtype":
			check := "✓"
			if !item.enabled {
				check = " "
			}
			label := item.value
			if item.displayName != "" {
				label = item.displayName
			}
			line := fmt.Sprintf("  [%s] %s", check, label)
			if i == m.filterCursor {
				line = activeStyle.Render(line)
			} else if !item.enabled {
				line = faintStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
		shown++
	}

	b.WriteString(sep + "\n")
	excluded := countExcluded(m.filterItems)
	footer := fmt.Sprintf("%d excluded  │  ↑↓/j/k: navigate  │  space/x: toggle", excluded)
	if m.filterTrip >= 0 {
		modeAction := "override"
		if m.filterMode() == FilterModeOverride {
			modeAction = "inherit"
		}
		footer += fmt.Sprintf("  │  i: %s  │  r: reset", modeAction)
	}
	footer += "  │  f/esc: close"
	b.WriteString(footer)
}

// filterMode returns the FilterMode for the current panel scope. It is "" for
// the global panel.
func (m tuiModel) filterMode() FilterMode {
	if m.filterTrip >= 0 && m.filterTrip < len(m.snap.Trips) {
		return m.snap.Trips[m.filterTrip].Spec.FilterMode
	}
	return ""
}

// filterHeader returns the panel header line for the current scope, e.g.
// "FILTERS — GLOBAL" or "FILTERS — TRIP 2 (override)".
func (m tuiModel) filterHeader() string {
	if m.filterTrip < 0 {
		return "FILTERS — GLOBAL"
	}
	mode := "inherit"
	if m.filterMode() == FilterModeOverride {
		mode = "override"
	}
	return fmt.Sprintf("FILTERS — TRIP %d (%s)", m.filterTrip+1, mode)
}

// renderTrips renders the stacked per-trip result sections into b.
func (m tuiModel) renderTrips(b *strings.Builder, sep string, headerStyle, activeStyle, errStyle lipgloss.Style) {
	visible := m.visibleRowsPerTrip()
	colHeader := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s",
		colResort, "RESORT",
		colRoomType, "ROOM TYPE",
		colView, "VIEW",
		colCheckIn, "CHECK-IN",
		colCheckOut, "CHECK-OUT",
		colNights, "NIGHTS",
		"PTS",
	)

	for i, trip := range m.snap.Trips {
		offset := m.offsets[i]
		fromVal := trip.Spec.From
		toVal := trip.Spec.To
		minVal := trip.Spec.MinNights

		renderField := func(idx int, val string) string {
			if m.focused == idx && m.activeTripIdx == i && m.focused < 3 {
				return activeStyle.Render(val) + "█"
			}
			return val
		}

		tripHeader := fmt.Sprintf("▶ TRIP %d  From: %s   To: %s   Min nights: %s   [budget: %d pts]",
			i+1,
			renderField(0, fromVal),
			renderField(1, toVal),
			renderField(2, minVal),
			trip.EffectiveBudget,
		)
		if i == m.activeTripIdx {
			tripHeader = activeStyle.Render(tripHeader)
		}
		b.WriteString(sep + "\n")
		b.WriteString(tripHeader + "\n")
		b.WriteString(sep + "\n")
		b.WriteString(headerStyle.Render(colHeader) + "\n")
		b.WriteString(sep + "\n")

		end := offset + visible
		if end > len(trip.Results) {
			end = len(trip.Results)
		}
		for j, r := range trip.Results[offset:end] {
			view := r.View
			if view == "" {
				view = "—"
			}
			prefix := "  "
			rowIdx := offset + j
			if trip.Selected != nil && stayEquals(*trip.Selected, r) {
				prefix = "✓ "
			} else if i == m.activeTripIdx && m.focused == 4 && rowIdx == offset && j == 0 {
				prefix = "> "
			}
			b.WriteString(fmt.Sprintf("%s%-*s  %-*s  %-*s  %-*s  %-*s  %-*d  %d\n",
				prefix,
				colResort, truncateRunes(r.Resort, colResort),
				colRoomType, truncateRunes(r.RoomType, colRoomType),
				colView, view,
				colCheckIn, r.CheckIn.Format("2006-01-02"),
				colCheckOut, r.CheckOut.Format("2006-01-02"),
				colNights, r.Nights,
				r.Points,
			))
		}
		if trip.Err != "" {
			b.WriteString(errStyle.Render(trip.Err) + "\n")
		}
	}

	// Status bar.
	b.WriteString(sep + "\n")
	var counts []string
	for i, trip := range m.snap.Trips {
		noun := "results"
		if len(trip.Results) == 1 {
			noun = "result"
		}
		counts = append(counts, fmt.Sprintf("%d %s (trip %d)", len(trip.Results), noun, i+1))
	}
	var quitHint string
	if m.focused == 4 {
		quitHint = "enter: select  │  +/-: trips  │  [/]: switch trip  │  f: filters  │  F: trip filters  │  p: plans  │  q: quit"
	} else {
		quitHint = "esc: stop editing  │  ctrl+c: quit"
	}
	b.WriteString(strings.Join(counts, " · ") + "  │  Tab: next field  │  ↑↓: scroll  │  " + quitHint)
}

// countExcluded returns the number of disabled (excluded) items in the filter list.
func countExcluded(items []filterItem) int {
	n := 0
	for _, item := range items {
		if item.kind != "" && !item.enabled {
			n++
		}
	}
	return n
}

// truncateRunes truncates s to at most maxCells runes, adding "…" if needed.
func truncateRunes(s string, maxCells int) string {
	runes := []rune(s)
	if len(runes) <= maxCells {
		return s
	}
	return string(runes[:maxCells-1]) + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
