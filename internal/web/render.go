package web

import (
	"fmt"
	"html/template"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

// View structs passed to templates.
type appView struct {
	Budget    string
	BudgetErr string
	Remaining int
	Trips     []tripView

	// ShowCosts gates every dollar affordance on the results tables and trip
	// summary chips (see tripView.ShowCosts's doc comment) — false whenever
	// no ledger is configured or CostBasis isn't Known() yet, so a
	// nil-ledger server (as in ~20 existing planner tests) renders
	// byte-identical HTML. The planner bar's own cost span is gated by
	// HasSelectedCost below, not directly by this field.
	ShowCosts bool

	// SelectedCostLabel is the formatted sum of every trip's
	// tripView.Selected.cost (real cents, summed once and formatted once —
	// never by concatenating or re-parsing each trip's own CostLabel).
	// "" unless HasSelectedCost.
	SelectedCostLabel string

	// HasSelectedCost is true when at least one trip contributed a priced
	// selection (i.e. some trip's tripView.SelectedCostLabel is non-empty).
	// It's what the template gates its "Selected: $X" span on, rather than
	// testing SelectedCostLabel for emptiness or against "$0.00" — those
	// would conflate "nothing selected" with "selected stays that happen to
	// cost nothing", which HasSelectedCost keeps distinct. Always false when
	// !ShowCosts, since a trip can only get a non-empty SelectedCostLabel
	// under ShowCosts.
	HasSelectedCost bool
}

type tripView struct {
	Index           int
	Spec            dvc.TripSpec
	EffectiveBudget int
	Results         []resultRow
	Err             string
	HasSelection    bool
	Collapsed       bool
	Selected        *resultRow
	FilterMode      dvc.FilterMode
	UsesOverride    bool          // == (FilterMode == dvc.FilterModeOverride)
	Filters         dvc.FilterSet // value type; the trip's own exclusions when overriding

	// ShowCosts mirrors appView.ShowCosts (see its doc comment) — every
	// dollar affordance on this trip's results table and collapsed summary
	// chip is gated on it, so a nil-ledger server (as in ~20 existing
	// planner tests) renders byte-identical HTML.
	ShowCosts bool

	// SelectedCostLabel is Selected.CostLabel, hoisted here so the
	// collapsed trip_summary chip doesn't need to reach through a possibly
	// nil Selected. Empty ("") when there's no selection, or ShowCosts is
	// false, or the selection's cost couldn't be priced (see resultRow.cost).
	SelectedCostLabel string
	// SelectedCostProjected mirrors Selected.CostProjected; meaningless
	// unless SelectedCostLabel is non-empty.
	SelectedCostProjected bool
}

type resultRow struct {
	RowIndex int
	Resort   string
	RoomType string
	View     string
	CheckIn  time.Time
	CheckOut time.Time
	Nights   int
	Points   int
	Selected bool

	// CostLabel is this stay's priced dollar cost (ledger.FormatUSD), ""
	// when the trip's tripView.ShowCosts is false or the stay's points
	// couldn't be priced (ledger.CostBasis.Cost's known=false case, e.g.
	// Points <= 0). CostProjected mirrors the underlying dues rate's
	// projected flag; meaningless unless CostLabel is non-empty. Only
	// rendered under the ShowCosts guard.
	CostLabel     string
	CostProjected bool

	// cost is the same value as CostLabel, unformatted, so appView's
	// SelectedCostLabel (added in a later commit) can sum real cents
	// across trips rather than parsing formatted dollar strings back apart.
	cost ledger.Cents
}

// filterScope tells the ONE filters template which panel it is rendering so it
// can serve both the global filter panel and per-trip filter panels.
//
// Contract for templates (fpl.18/19):
//   - .Scope.IsTrip selects the POST URLs: global "/filters/..." when false,
//     per-trip "/trips/{.Scope.TripIndex}/filters/..." when true.
//   - .Scope.Mode (only meaningful when IsTrip) drives the inherit/override
//     switch and the disabled-rows-on-inherit hint. It is empty/ignored for
//     the global panel.
type filterScope struct {
	IsTrip    bool
	TripIndex int
	Mode      dvc.FilterMode // inherit/override; empty/ignored when !IsTrip
}

type filtersView struct {
	Scope     filterScope
	Resorts   []resortOption
	RoomTypes []roomTypeOption
}

// resortOption is one row in the filter panel's resort list.
type resortOption struct {
	Code    string
	Name    string
	Enabled bool
}

// roomTypeOption is one row in the filter panel's room-type list.
type roomTypeOption struct {
	Name    string
	Enabled bool
}

// stayKey is the identity composite used to mark the selected result row. It
// includes Points because a real search can return two rows that are
// otherwise identical (same resort/room type/view/check-in/check-out) but
// priced at different point costs — without Points here, both rows collide
// on the same key, both render the checkmark, and the "last match wins" loop
// in buildAppView leaves tv.Selected pointing at the wrong row (see
// TestBuildAppView_SelectionMatchesOnPointsToo). A stored Selected stay
// (dvc.TripSpec.Selected / dvc.TripSnapshot.Selected) is always a full
// dvc.StayResult with its own Points value copied from the row that was
// selected — StayResult.Points has no omitempty and isn't a pointer, so it
// round-trips through JSON unconditionally. There's no way for a stored
// selection to legitimately lack Points.
func stayKey(r dvc.StayResult) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%d",
		r.Resort, r.RoomType, r.View,
		r.CheckIn.Format("2006-01-02"),
		r.CheckOut.Format("2006-01-02"),
		r.Points,
	)
}

// buildAppView projects a Planner Snapshot into a render-ready appView, layering
// in the web's view-only collapsed flags. Caller must hold s.mu (so collapsed
// and the snapshot stay consistent).
func (s *Session) buildAppView(snap dvc.Snapshot) appView {
	// basis/showCosts gate every dollar affordance below. s.costs is always
	// non-nil in practice (see NewSession), and Basis() is itself
	// nil-store-safe, so this never needs its own nil check — a missing or
	// not-yet-known ledger just means ok/basis.Known() come back false and
	// showCosts stays false, matching the pre-cost rendering every existing
	// planner test (Options.Ledger == nil) expects byte-for-byte.
	basis, ok := s.costs.Basis()
	showCosts := ok && basis.Known()

	v := appView{
		Budget:    snap.Budget,
		BudgetErr: snap.BudgetErr,
		Remaining: snap.Remaining,
		Trips:     make([]tripView, len(snap.Trips)),
		ShowCosts: showCosts,
	}

	// totalSelectedCost sums every trip's selected stay's real cents (never
	// its formatted CostLabel) into v.SelectedCostLabel, formatted once at
	// the end — see TestBuildAppView_SumsSelectedCostAcrossTrips.
	var totalSelectedCost ledger.Cents

	for i := range snap.Trips {
		t := snap.Trips[i]
		collapsed := false
		if i < len(s.collapsed) {
			collapsed = s.collapsed[i]
		}
		var f dvc.FilterSet
		if t.Spec.Filters != nil {
			f = *t.Spec.Filters
		}
		tv := tripView{
			Index:           i,
			Spec:            t.Spec,
			EffectiveBudget: t.EffectiveBudget,
			Err:             t.Err,
			HasSelection:    t.Selected != nil,
			Collapsed:       collapsed,
			FilterMode:      t.Spec.FilterMode,
			UsesOverride:    t.Spec.FilterMode == dvc.FilterModeOverride,
			Filters:         f,
			ShowCosts:       showCosts,
		}
		var selKey string
		if t.Selected != nil {
			selKey = stayKey(*t.Selected)
		}
		tv.Results = make([]resultRow, len(t.Results))
		for j, r := range t.Results {
			row := resultRow{
				RowIndex: j,
				Resort:   r.Resort,
				RoomType: r.RoomType,
				View:     r.View,
				CheckIn:  r.CheckIn,
				CheckOut: r.CheckOut,
				Nights:   r.Nights,
				Points:   r.Points,
				Selected: selKey != "" && stayKey(r) == selKey,
			}
			if showCosts {
				priceRow(&row, basis)
			}
			tv.Results[j] = row
			if row.Selected {
				sel := row
				tv.Selected = &sel
			}
		}
		// If the trip has a selection that's not in the current results
		// (e.g. filtered out), fall back to the stored Selected stay.
		if tv.Selected == nil && t.Selected != nil {
			sel := resultRow{
				Resort:   t.Selected.Resort,
				RoomType: t.Selected.RoomType,
				View:     t.Selected.View,
				CheckIn:  t.Selected.CheckIn,
				CheckOut: t.Selected.CheckOut,
				Nights:   t.Selected.Nights,
				Points:   t.Selected.Points,
				Selected: true,
			}
			if showCosts {
				priceRow(&sel, basis)
			}
			tv.Selected = &sel
		}
		if tv.Selected != nil {
			tv.SelectedCostLabel = tv.Selected.CostLabel
			tv.SelectedCostProjected = tv.Selected.CostProjected
			totalSelectedCost += tv.Selected.cost
			if tv.SelectedCostLabel != "" {
				v.HasSelectedCost = true
			}
		}
		v.Trips[i] = tv
	}
	if showCosts {
		v.SelectedCostLabel = ledger.FormatUSD(totalSelectedCost)
	}
	return v
}

// priceRow prices row's Points at its CheckIn date's use year — see
// ledger.UseYearForDate — against the blended rate (contractID nil: a
// planner stay is never attributed to a specific contract, so the
// portfolio-wide blended rate always applies). It sets row.cost, CostLabel
// and CostProjected in place, and is a no-op (leaving them at their zero
// values) when the stay's points can't be priced — CostBasis.Cost's own
// known=false case, e.g. Points <= 0. Callers must already know
// basis.Known() is true (see buildAppView's showCosts guard); calling this
// otherwise is harmless but pointless, since Cost always reports known=false.
func priceRow(row *resultRow, basis ledger.CostBasis) {
	year := ledger.UseYearForDate(row.CheckIn, basis.UseYearMonth())
	cost, projected, known := basis.Cost(row.Points, year, nil)
	if !known {
		return
	}
	row.cost = cost
	row.CostLabel = ledger.FormatUSD(cost)
	row.CostProjected = projected
}

// toFiltersView adapts a Planner FilterOptionsView into the template's
// filtersView, preserving the existing field names the templates render.
func toFiltersView(opts dvc.FilterOptionsView) filtersView {
	fv := filtersView{
		Scope: filterScope{
			IsTrip:    opts.TripIndex >= 0,
			TripIndex: opts.TripIndex,
			Mode:      opts.Mode,
		},
		Resorts:   make([]resortOption, len(opts.Resorts)),
		RoomTypes: make([]roomTypeOption, len(opts.RoomTypes)),
	}
	for i, r := range opts.Resorts {
		fv.Resorts[i] = resortOption{Code: r.Code, Name: r.Name, Enabled: r.Enabled}
	}
	for i, rt := range opts.RoomTypes {
		fv.RoomTypes[i] = roomTypeOption{Name: rt.Name, Enabled: rt.Enabled}
	}
	return fv
}

// templateFuncs are helpers available inside templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatDate": func(t time.Time) string {
			return t.Format("2006-01-02")
		},
		"viewOrDash": func(v string) string {
			if v == "" {
				return "—"
			}
			return v
		},
		"add1": func(i int) int { return i + 1 },
		"formatMonth": func(m time.Month) string {
			return m.String()
		},
		// blankZero renders 0 as an empty cell so the ledger grid matches the
		// spreadsheet's empty Allotted/Used columns.
		"blankZero": func(n int) string {
			if n == 0 {
				return ""
			}
			return fmt.Sprintf("%d", n)
		},
		// money formats a Cents total as a dollar string, e.g. "$1,234.56".
		// Only ever invoked from inside a ShowCosts guard.
		"money": ledger.FormatUSD,
		// rate formats a Micros per-point rate, e.g. "$5.6796". Only ever
		// invoked from inside a ShowCosts guard.
		"rate": ledger.FormatRate,
		"deref": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		// dict builds a map from alternating key/value pairs so a template can pass
		// multiple named values into a sub-template invocation.
		"dict": func(pairs ...any) (map[string]any, error) {
			if len(pairs)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments")
			}
			m := make(map[string]any, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				key, ok := pairs[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %d is not a string", i)
				}
				m[key] = pairs[i+1]
			}
			return m, nil
		},
		// filterTitle builds the scope-aware panel header text, e.g.
		// "Filters — Global" or "Filters — Trip 2 (override)" using 1-based
		// trip numbering consistent with the rest of the UI.
		"filterTitle": func(s filterScope) string {
			if !s.IsTrip {
				return "Filters — Global"
			}
			mode := "inherit"
			if s.Mode == dvc.FilterModeOverride {
				mode = "override"
			}
			return fmt.Sprintf("Filters — Trip %d (%s)", s.TripIndex+1, mode)
		},
		// modeLabel normalizes a FilterMode into a stable template label:
		// the override constant stays "override", every other value (including
		// the empty inherit zero value) renders as "inherit".
		"modeLabel": func(m dvc.FilterMode) string {
			if m == dvc.FilterModeOverride {
				return "override"
			}
			return "inherit"
		},
	}
}
