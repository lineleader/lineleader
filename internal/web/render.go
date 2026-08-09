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
	Budget         string
	BudgetErr      string
	Remaining      int
	LoadedPlanName string
	Trips          []tripView
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

type plansView struct {
	Plans          []dvc.Plan
	LoadedPlanName string
	Err            string
}

// stayKey is the identity composite used to mark the selected result row.
func stayKey(r dvc.StayResult) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s",
		r.Resort, r.RoomType, r.View,
		r.CheckIn.Format("2006-01-02"),
		r.CheckOut.Format("2006-01-02"),
	)
}

// buildAppView projects a Planner Snapshot into a render-ready appView, layering
// in the web's view-only collapsed flags. Caller must hold s.mu (so collapsed
// and the snapshot stay consistent).
func (s *Session) buildAppView(snap dvc.Snapshot) appView {
	v := appView{
		Budget:         snap.Budget,
		BudgetErr:      snap.BudgetErr,
		Remaining:      snap.Remaining,
		LoadedPlanName: snap.LoadedPlanName,
		Trips:          make([]tripView, len(snap.Trips)),
	}

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
			tv.Results[j] = row
			if row.Selected {
				sel := row
				tv.Selected = &sel
			}
		}
		// If the trip has a selection that's not in the current results
		// (e.g. filtered out), fall back to the stored Selected stay.
		if tv.Selected == nil && t.Selected != nil {
			tv.Selected = &resultRow{
				Resort:   t.Selected.Resort,
				RoomType: t.Selected.RoomType,
				View:     t.Selected.View,
				CheckIn:  t.Selected.CheckIn,
				CheckOut: t.Selected.CheckOut,
				Nights:   t.Selected.Nights,
				Points:   t.Selected.Points,
				Selected: true,
			}
		}
		v.Trips[i] = tv
	}
	return v
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
