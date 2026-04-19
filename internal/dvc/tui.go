package dvc

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

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
	Fields   [3]inputField // 0=From, 1=To, 2=MinNights (Budget is global)
	Results  []StayResult
	Selected *StayResult // heap-allocated copy; nil = no selection
	Offset   int         // scroll position within this trip's results
	Err      string      // per-trip parse/search error
}

// SelectedPoints returns the total points committed across all trips.
func SelectedPoints(trips []Trip) int {
	total := 0
	for _, t := range trips {
		if t.Selected != nil {
			total += t.Selected.Points
		}
	}
	return total
}

// RemainingBudget returns how many points are still available after summing all
// selected stays.
func RemainingBudget(budget int, trips []Trip) int {
	return budget - SelectedPoints(trips)
}

// BudgetForTrip returns the effective search budget for trip at index i: the
// global budget minus points selected by all OTHER trips. Trip i's own
// selection does not reduce its own search budget.
func BudgetForTrip(budget int, trips []Trip, i int) int {
	used := 0
	for j, t := range trips {
		if j != i && t.Selected != nil {
			used += t.Selected.Points
		}
	}
	return budget - used
}

// stayEquals compares two StayResults by identity fields (Resort, RoomType,
// View, CheckIn, CheckOut). Points and Nights are not compared.
func stayEquals(a, b StayResult) bool {
	return a.Resort == b.Resort &&
		a.RoomType == b.RoomType &&
		a.View == b.View &&
		a.CheckIn.Equal(b.CheckIn) &&
		a.CheckOut.Equal(b.CheckOut)
}

// filterItem represents one toggleable entry in the filter panel.
type filterItem struct {
	kind        string // "resort" or "roomtype" or "" (separator)
	value       string // resort code or room type name (used for filtering logic)
	displayName string // human-readable label shown in the UI (full resort name for resorts)
	enabled     bool   // true = included in search (not excluded)
}

// tuiModel is the bubbletea model for the interactive search UI.
type tuiModel struct {
	charts        []*ResortChart
	budgetField   inputField // global shared budget
	trips         []Trip     // len >= 1
	activeTripIdx int
	focused       int // 0=From, 1=To, 2=MinNights, 3=Budget, 4=Table
	height        int // terminal height (updated on WindowSizeMsg)
	width         int // terminal width
	filters       Config
	filterOpen    bool
	filterItems   []filterItem
	filterCursor  int
	plans          []Plan
	plansPath      string
	plansOpen      bool
	plansCursor    int
	plansNaming    bool   // true while typing a new plan name
	plansNameBuf   string // name being typed
	plansErr       string // last save/delete error
	loadedPlanName string // name of the plan last loaded via applyPlan or saved via savePlan; "" when none
}

// newTUIModel creates a TUI model with the given charts and one empty trip.
// Used internally and by tests.
func newTUIModel(charts []*ResortChart) tuiModel {
	trip := Trip{
		Fields: [3]inputField{
			{label: "From"},
			{label: "To"},
			{label: "Min nights"},
		},
	}
	return tuiModel{
		charts:      charts,
		budgetField: inputField{label: "Budget"},
		trips:       []Trip{trip},
		focused:     0,
	}
}

// NewTUIModel creates an exported TUI model for use from cmd/dvc.
func NewTUIModel(charts []*ResortChart) tuiModel {
	return newTUIModel(charts)
}

// WithFilters sets the initial filter config (loaded from the config file) and
// builds the filter item list. Changes made in the TUI are in-memory only.
func (m tuiModel) WithFilters(cfg Config) tuiModel {
	return m.withFilters(cfg)
}

// withFilters is the unexported version used internally and in tests.
func (m tuiModel) withFilters(cfg Config) tuiModel {
	m.filters = cfg
	m.filterItems = buildFilterItems(m.charts, cfg)
	return m
}

// WithPlans sets the loaded plans and the path to persist them.
func (m tuiModel) WithPlans(plans []Plan, path string) tuiModel {
	m.plans = plans
	m.plansPath = path
	return m
}

// WithDefaults populates the global budget and trip 0's date/minNights fields,
// then runs an initial search.
func (m tuiModel) WithDefaults(from, to, budget, minNights string) tuiModel {
	m.trips[0].Fields[0].value = from
	m.trips[0].Fields[1].value = to
	m.trips[0].Fields[2].value = minNights
	m.budgetField.value = budget
	return m.recomputeAll()
}

// buildFilterItems builds the ordered list of filter panel items from the
// unique resort codes and room types across all charts, applying cfg exclusions.
func buildFilterItems(charts []*ResortChart, cfg Config) []filterItem {
	resortNames := map[string]string{} // code → full name
	roomSeen := map[string]bool{}
	var resortCodes, roomTypes []string

	for _, c := range charts {
		if _, seen := resortNames[c.ResortCode]; !seen {
			resortNames[c.ResortCode] = c.ResortName
			resortCodes = append(resortCodes, c.ResortCode)
		}
		for _, col := range c.Columns {
			if !roomSeen[col.RoomType] {
				roomSeen[col.RoomType] = true
				roomTypes = append(roomTypes, col.RoomType)
			}
		}
	}
	sort.Strings(resortCodes)
	sort.Strings(roomTypes)

	var items []filterItem
	for _, code := range resortCodes {
		items = append(items, filterItem{
			kind:        "resort",
			value:       code,
			displayName: resortNames[code],
			enabled:     !slices.Contains(cfg.ExcludeResorts, code),
		})
	}
	// Blank separator between sections.
	items = append(items, filterItem{kind: ""})
	for _, rt := range roomTypes {
		items = append(items, filterItem{
			kind:    "roomtype",
			value:   rt,
			enabled: !slices.Contains(cfg.ExcludeRoomTypes, rt),
		})
	}
	return items
}

// rebuildFiltersFromItems rebuilds the Config exclusion lists from filterItems.
func rebuildFiltersFromItems(items []filterItem) Config {
	var cfg Config
	for _, item := range items {
		if item.kind == "resort" && !item.enabled {
			cfg.ExcludeResorts = append(cfg.ExcludeResorts, item.value)
		}
		if item.kind == "roomtype" && !item.enabled {
			cfg.ExcludeRoomTypes = append(cfg.ExcludeRoomTypes, item.value)
		}
	}
	return cfg
}

// parseDateTUI parses a date string in YYYY-MM-DD or M/D/YYYY format.
func parseDateTUI(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "1/2/2006", "01/02/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q — use YYYY-MM-DD or M/D/YYYY", s)
}

// snapshotPlan builds a Plan from the current model state.
func (m tuiModel) snapshotPlan(name string) Plan {
	specs := make([]TripSpec, len(m.trips))
	for i, t := range m.trips {
		specs[i] = TripSpec{
			From:      t.Fields[0].value,
			To:        t.Fields[1].value,
			MinNights: t.Fields[2].value,
		}
	}
	p := Plan{
		Name:   name,
		Budget: m.budgetField.value,
		Trips:  specs,
	}
	if len(m.filters.ExcludeResorts) > 0 {
		p.ExcludeResorts = append([]string(nil), m.filters.ExcludeResorts...)
	}
	if len(m.filters.ExcludeRoomTypes) > 0 {
		p.ExcludeRoomTypes = append([]string(nil), m.filters.ExcludeRoomTypes...)
	}
	return p
}

// applyPlan replaces trips, budget, and filters from a saved Plan and
// recomputes results.
func (m tuiModel) applyPlan(p Plan) tuiModel {
	trips := make([]Trip, len(p.Trips))
	for i, spec := range p.Trips {
		trips[i] = Trip{
			Fields: [3]inputField{
				{label: "From", value: spec.From},
				{label: "To", value: spec.To},
				{label: "Min nights", value: spec.MinNights},
			},
		}
	}
	m.trips = trips
	m.activeTripIdx = 0
	m.budgetField.value = p.Budget
	m.filters = Config{
		ExcludeResorts:   p.ExcludeResorts,
		ExcludeRoomTypes: p.ExcludeRoomTypes,
	}
	m.filterItems = buildFilterItems(m.charts, m.filters)
	m.loadedPlanName = p.Name
	return m.recomputeAll()
}

// savePlan upserts the current state as a named plan, persists to disk, and
// returns the updated model. Sets m.plansErr on write failure.
func (m tuiModel) savePlan(name string) tuiModel {
	plan := m.snapshotPlan(name)
	found := false
	for i, p := range m.plans {
		if p.Name == name {
			m.plans[i] = plan
			found = true
			break
		}
	}
	if !found {
		m.plans = append(m.plans, plan)
	}
	if err := SavePlans(m.plansPath, m.plans); err != nil {
		m.plansErr = err.Error()
	} else {
		m.plansErr = ""
	}
	m.loadedPlanName = name
	return m
}

// recomputeAll re-runs the search for every trip using the global budget and
// all other trips' selections to compute each trip's effective budget.
func (m tuiModel) recomputeAll() tuiModel {
	budget, err := strconv.Atoi(m.budgetField.value)
	if err != nil {
		for i := range m.trips {
			m.trips[i].Err = "invalid Budget"
			m.trips[i].Results = nil
			m.trips[i].Offset = 0
		}
		return m
	}
	for i := range m.trips {
		m.trips[i] = recomputeTrip(m.charts, m.trips[i], BudgetForTrip(budget, m.trips, i), m.filters)
	}
	return m
}

// recomputeTrip re-runs Search for a single Trip and returns the updated Trip.
// On a parse error it sets trip.Err and leaves results unchanged.
// On success it clears trip.Err, updates trip.Results, and clamps trip.Offset.
// It never auto-clears trip.Selected — the user must deselect explicitly.
func recomputeTrip(charts []*ResortChart, trip Trip, budget int, filters Config) Trip {
	from, err1 := parseDateTUI(trip.Fields[0].value)
	to, err2 := parseDateTUI(trip.Fields[1].value)
	minNights, err3 := strconv.Atoi(trip.Fields[2].value)

	switch {
	case err1 != nil:
		trip.Err = "invalid From date"
		return trip
	case err2 != nil:
		trip.Err = "invalid To date"
		return trip
	case err3 != nil:
		trip.Err = "invalid Min nights"
		return trip
	}

	trip.Err = ""
	trip.Results = Search(charts, SearchParams{
		WindowStart:      from,
		WindowEnd:        to,
		Budget:           budget,
		MinNights:        minNights,
		ExcludeResorts:   filters.ExcludeResorts,
		ExcludeRoomTypes: filters.ExcludeRoomTypes,
	})

	// Clamp scroll offset to valid range.
	if len(trip.Results) == 0 {
		trip.Offset = 0
	} else if trip.Offset >= len(trip.Results) {
		trip.Offset = len(trip.Results) - 1
	}

	return trip
}

// visibleRowsPerTrip returns how many result rows fit per trip section.
func (m tuiModel) visibleRowsPerTrip() int {
	// Fixed rows per trip: 1 header + 1 sep + 1 col header + 1 sep = 4
	// Plus global bar (1) + global sep (1) + status (1) = 3
	fixed := 3 + len(m.trips)*4
	rows := (m.height - fixed) / len(m.trips)
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

// activeField returns a pointer to the currently focused inputField so it can
// be read and written uniformly regardless of whether it is a trip-local field
// (focused 0–2) or the global budget field (focused 3).
func (m *tuiModel) activeField() *inputField {
	if m.focused == 3 {
		return &m.budgetField
	}
	return &m.trips[m.activeTripIdx].Fields[m.focused]
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
						m = m.savePlan(m.plansNameBuf)
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
				if m.plansCursor < len(m.plans)-1 {
					m.plansCursor++
				}
			case "s":
				m.plansNaming = true
				m.plansNameBuf = ""
			case "u":
				if m.loadedPlanName != "" {
					m = m.savePlan(m.loadedPlanName)
				}
			case "d":
				if m.plansCursor < len(m.plans) {
					deleted := m.plans[m.plansCursor].Name
					m.plans = append(m.plans[:m.plansCursor], m.plans[m.plansCursor+1:]...)
					if m.plansCursor >= len(m.plans) && m.plansCursor > 0 {
						m.plansCursor--
					}
					if deleted == m.loadedPlanName {
						m.loadedPlanName = ""
					}
					if err := SavePlans(m.plansPath, m.plans); err != nil {
						m.plansErr = err.Error()
					} else {
						m.plansErr = ""
					}
				}
			case "enter":
				if m.plansCursor < len(m.plans) {
					m = m.applyPlan(m.plans[m.plansCursor])
					m.plansOpen = false
				}
			}
			return m, nil
		}

		// Filter panel handles its own keys when open.
		if m.filterOpen {
			switch msg.String() {
			case "f", "esc":
				m.filterOpen = false
			case "up", "k":
				m.filterCursor = m.nextFilterCursor(-1)
			case "down", "j":
				m.filterCursor = m.nextFilterCursor(1)
			case "space", "x":
				if m.filterCursor < len(m.filterItems) {
					m.filterItems[m.filterCursor].enabled = !m.filterItems[m.filterCursor].enabled
					m.filters = rebuildFiltersFromItems(m.filterItems)
					m = m.recomputeAll()
				}
			}
			return m, nil
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
			trip := &m.trips[m.activeTripIdx]
			switch msg.String() {
			case "p":
				m.plansOpen = true
				m.plansNaming = false
				if m.plansCursor >= len(m.plans) {
					m.plansCursor = 0
				}
			case "f":
				if len(m.filterItems) == 0 {
					m.filterItems = buildFilterItems(m.charts, m.filters)
				}
				m.filterOpen = true
				// Ensure cursor lands on a real item.
				if m.filterCursor < len(m.filterItems) && m.filterItems[m.filterCursor].kind == "" {
					m.filterCursor = m.nextFilterCursor(1)
				}
			case "up", "k":
				if trip.Offset > 0 {
					trip.Offset--
				}
			case "down", "j":
				if trip.Offset < len(trip.Results)-1 {
					trip.Offset++
				}
			case "[":
				if m.activeTripIdx > 0 {
					m.activeTripIdx--
				}
			case "]":
				if m.activeTripIdx < len(m.trips)-1 {
					m.activeTripIdx++
				}
			case "+":
				last := m.trips[len(m.trips)-1]
				newTrip := Trip{
					Fields: [3]inputField{
						{label: "From", value: last.Fields[0].value},
						{label: "To", value: last.Fields[1].value},
						{label: "Min nights", value: "1"},
					},
				}
				m.trips = append(m.trips, newTrip)
				m.activeTripIdx = len(m.trips) - 1
				m = m.recomputeAll()
			case "-":
				if len(m.trips) > 1 {
					m.trips = append(m.trips[:m.activeTripIdx], m.trips[m.activeTripIdx+1:]...)
					if m.activeTripIdx >= len(m.trips) {
						m.activeTripIdx = len(m.trips) - 1
					}
					m = m.recomputeAll()
				}
			case "enter":
				if len(trip.Results) > 0 {
					highlighted := trip.Results[trip.Offset]
					if trip.Selected != nil && stayEquals(*trip.Selected, highlighted) {
						trip.Selected = nil
					} else {
						sel := highlighted // copy by value
						trip.Selected = &sel
					}
					m = m.recomputeAll()
				}
			}
			return m, nil
		}

		// Input field handling (focused 0–3).
		field := m.activeField()
		switch msg.String() {
		case "backspace":
			runes := []rune(field.value)
			if len(runes) > 0 {
				field.value = string(runes[:len(runes)-1])
				m = m.recomputeAll()
			}
		default:
			if msg.Text != "" {
				field.value += msg.Text
				m = m.recomputeAll()
			}
		}
	}

	return m, nil
}

// View implements tea.Model.
func (m tuiModel) View() tea.View {
	var b strings.Builder

	labelStyle := lipgloss.NewStyle().Faint(true)
	activeStyle := lipgloss.NewStyle().Bold(true)
	headerStyle := lipgloss.NewStyle().Bold(true)
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	sepStyle := lipgloss.NewStyle().Faint(true)
	faintStyle := lipgloss.NewStyle().Faint(true)

	sep := sepStyle.Render(strings.Repeat("─", max(m.width, 1)))

	budget, _ := strconv.Atoi(m.budgetField.value)
	remaining := RemainingBudget(budget, m.trips)

	// Global bar: budget field + remaining counter.
	budgetLabel := labelStyle.Render(m.budgetField.label + ": ")
	budgetValue := m.budgetField.value
	if m.focused == 3 {
		budgetValue = activeStyle.Render(m.budgetField.value) + "█"
	}
	b.WriteString(budgetLabel + budgetValue)
	b.WriteString(fmt.Sprintf("   Remaining: %d pts", remaining))
	if m.loadedPlanName != "" {
		b.WriteString("   Plan: " + m.loadedPlanName)
	}
	b.WriteString("   f: filters   p: plans   q: quit\n")

	if m.plansOpen {
		b.WriteString(sep + "\n")
		b.WriteString(headerStyle.Render("PLANS") + "\n")
		b.WriteString(sep + "\n")

		if len(m.plans) == 0 && !m.plansNaming {
			b.WriteString(faintStyle.Render("  (no saved plans)") + "\n")
		}
		for i, p := range m.plans {
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
	} else if m.filterOpen {
		b.WriteString(sep + "\n")
		// Filter panel replaces the results area.
		b.WriteString(headerStyle.Render("FILTERS") + "\n")
		b.WriteString(sep + "\n")

		visible := m.visibleRowsPerTrip() * len(m.trips)
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
		b.WriteString(fmt.Sprintf("%d excluded  │  ↑↓/j/k: navigate  │  space/x: toggle  │  f/esc: close", excluded))
	} else {
		// Stacked trip sections — one per trip.
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

		for i, trip := range m.trips {
			tripBudget := BudgetForTrip(budget, m.trips, i)
			fromVal := trip.Fields[0].value
			toVal := trip.Fields[1].value
			minVal := trip.Fields[2].value

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
				tripBudget,
			)
			if i == m.activeTripIdx {
				tripHeader = activeStyle.Render(tripHeader)
			}
			b.WriteString(sep + "\n")
			b.WriteString(tripHeader + "\n")
			b.WriteString(sep + "\n")
			b.WriteString(headerStyle.Render(colHeader) + "\n")
			b.WriteString(sep + "\n")

			end := trip.Offset + visible
			if end > len(trip.Results) {
				end = len(trip.Results)
			}
			for j, r := range trip.Results[trip.Offset:end] {
				view := r.View
				if view == "" {
					view = "—"
				}
				prefix := "  "
				rowIdx := trip.Offset + j
				if trip.Selected != nil && stayEquals(*trip.Selected, r) {
					prefix = "✓ "
				} else if i == m.activeTripIdx && m.focused == 4 && rowIdx == trip.Offset && j == 0 {
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
		for i, trip := range m.trips {
			noun := "results"
			if len(trip.Results) == 1 {
				noun = "result"
			}
			counts = append(counts, fmt.Sprintf("%d %s (trip %d)", len(trip.Results), noun, i+1))
		}
		var quitHint string
		if m.focused == 4 {
			quitHint = "enter: select  │  +/-: trips  │  [/]: switch trip  │  f: filters  │  p: plans  │  q: quit"
		} else {
			quitHint = "esc: stop editing  │  ctrl+c: quit"
		}
		b.WriteString(strings.Join(counts, " · ") + "  │  Tab: next field  │  ↑↓: scroll  │  " + quitHint)
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
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
