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

// filterItem represents one toggleable entry in the filter panel.
type filterItem struct {
	kind        string // "resort" or "roomtype" or "" (separator)
	value       string // resort code or room type name (used for filtering logic)
	displayName string // human-readable label shown in the UI (full resort name for resorts)
	enabled     bool   // true = included in search (not excluded)
}

// tuiModel is the bubbletea model for the interactive search UI.
type tuiModel struct {
	charts      []*ResortChart
	fields      [4]inputField // from, to, budget, minNights
	focused     int           // 0–3 = input fields, 4 = table
	results     []StayResult
	offset      int // scroll position within results
	height      int // terminal height (updated on WindowSizeMsg)
	width       int // terminal width
	err         string
	filters     Config
	filterOpen  bool
	filterItems []filterItem
	filterCursor int
}

// newTUIModel creates a TUI model with the given charts and empty fields.
// Used internally and by tests.
func newTUIModel(charts []*ResortChart) tuiModel {
	return tuiModel{
		charts:  charts,
		focused: 0,
		fields: [4]inputField{
			{label: "From"},
			{label: "To"},
			{label: "Budget"},
			{label: "Min nights"},
		},
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

// WithDefaults populates the four input fields and runs an initial search.
func (m tuiModel) WithDefaults(from, to, budget, minNights string) tuiModel {
	m.fields[0].value = from
	m.fields[1].value = to
	m.fields[2].value = budget
	m.fields[3].value = minNights
	return m.recompute()
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

// recompute re-runs the search using the current field values.
// On a parse error it sets m.err and leaves results unchanged.
// On success it clears m.err, updates m.results, and clamps m.offset.
func (m tuiModel) recompute() tuiModel {
	from, err1 := parseDateTUI(m.fields[0].value)
	to, err2 := parseDateTUI(m.fields[1].value)
	budget, err3 := strconv.Atoi(m.fields[2].value)
	minNights, err4 := strconv.Atoi(m.fields[3].value)

	switch {
	case err1 != nil:
		m.err = "invalid From date"
		return m
	case err2 != nil:
		m.err = "invalid To date"
		return m
	case err3 != nil:
		m.err = "invalid Budget"
		return m
	case err4 != nil:
		m.err = "invalid Min nights"
		return m
	}

	m.err = ""
	m.results = Search(m.charts, SearchParams{
		WindowStart:      from,
		WindowEnd:        to,
		Budget:           budget,
		MinNights:        minNights,
		ExcludeResorts:   m.filters.ExcludeResorts,
		ExcludeRoomTypes: m.filters.ExcludeRoomTypes,
	})

	// Clamp scroll offset to valid range.
	if len(m.results) == 0 {
		m.offset = 0
	} else if m.offset >= len(m.results) {
		m.offset = len(m.results) - 1
	}

	return m
}

// visibleRows returns how many result rows fit in the current terminal height.
func (m tuiModel) visibleRows() int {
	// Reserve: 1 input row + 1 error row + 2 separators + 1 header + 1 status = 6
	rows := m.height - 6
	if rows < 1 {
		rows = 1
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
					m = m.recompute()
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
			switch msg.String() {
			case "f":
				if len(m.filterItems) == 0 {
					m.filterItems = buildFilterItems(m.charts, m.filters)
				}
				m.filterOpen = true
				// Ensure cursor lands on a real item.
				if m.filterCursor < len(m.filterItems) && m.filterItems[m.filterCursor].kind == "" {
					m.filterCursor = m.nextFilterCursor(1)
				}
			case "up":
				if m.offset > 0 {
					m.offset--
				}
			case "down":
				if m.offset < len(m.results)-1 {
					m.offset++
				}
			}
			return m, nil
		}

		// Input field handling.
		switch msg.String() {
		case "backspace":
			runes := []rune(m.fields[m.focused].value)
			if len(runes) > 0 {
				m.fields[m.focused].value = string(runes[:len(runes)-1])
				m = m.recompute()
			}
		default:
			if msg.Text != "" {
				m.fields[m.focused].value += msg.Text
				m = m.recompute()
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

	// Input fields row.
	for i, f := range m.fields {
		label := labelStyle.Render(f.label+": ")
		value := f.value
		if m.focused == i {
			value = activeStyle.Render(f.value) + "█"
		}
		b.WriteString(label + value)
		if i < 3 {
			b.WriteString("   ")
		}
	}
	b.WriteString("\n")

	// Separator.
	sep := sepStyle.Render(strings.Repeat("─", max(m.width, 1)))
	b.WriteString(sep + "\n")

	if m.filterOpen {
		// Filter panel replaces the results area.
		b.WriteString(headerStyle.Render("FILTERS") + "\n")
		b.WriteString(sep + "\n")

		visible := m.visibleRows()
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
		// Normal results view.
		header := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s",
			colResort, "RESORT",
			colRoomType, "ROOM TYPE",
			colView, "VIEW",
			colCheckIn, "CHECK-IN",
			colCheckOut, "CHECK-OUT",
			colNights, "NIGHTS",
			"PTS",
		)
		b.WriteString(headerStyle.Render(header) + "\n")
		b.WriteString(sep + "\n")

		visible := m.visibleRows()
		end := m.offset + visible
		if end > len(m.results) {
			end = len(m.results)
		}
		for _, r := range m.results[m.offset:end] {
			view := r.View
			if view == "" {
				view = "—"
			}
			b.WriteString(fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*d  %d\n",
				colResort, truncateRunes(r.Resort, colResort),
				colRoomType, truncateRunes(r.RoomType, colRoomType),
				colView, view,
				colCheckIn, r.CheckIn.Format("2006-01-02"),
				colCheckOut, r.CheckOut.Format("2006-01-02"),
				colNights, r.Nights,
				r.Points,
			))
		}

		b.WriteString(sep + "\n")
		noun := "results"
		if len(m.results) == 1 {
			noun = "result"
		}
		var quitHint string
		if m.focused == 4 {
			quitHint = "f: filters  │  q: quit"
		} else {
			quitHint = "esc: stop editing  ctrl+c: quit"
		}
		status := fmt.Sprintf("%d %s  │  Tab: next field  │  ↑↓: scroll  │  %s",
			len(m.results), noun, quitHint)
		if m.err != "" {
			status = errStyle.Render(m.err) + "  │  " + status
		}
		b.WriteString(status)
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
