package web

import (
	"fmt"
	"html/template"
	"strconv"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
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

type filtersView struct {
	Resorts   []resortOption
	RoomTypes []roomTypeOption
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

// buildAppView projects Session state into a render-ready appView.
// Caller must hold s.mu.
func (s *Session) buildAppView() appView {
	v := appView{
		Budget:         s.budget,
		Remaining:      s.remainingBudget(),
		LoadedPlanName: s.loadedPlanName,
		Trips:          make([]tripView, len(s.trips)),
	}
	if _, err := strconv.Atoi(s.budget); err != nil {
		v.BudgetErr = "invalid Budget"
	}

	globalBudget, _ := strconv.Atoi(s.budget)
	for i, t := range s.trips {
		tv := tripView{
			Index:           i,
			Spec:            t.Spec,
			EffectiveBudget: s.budgetForTrip(globalBudget, i),
			Err:             t.Err,
			HasSelection:    t.Selected != nil,
		}
		var selKey string
		if t.Selected != nil {
			selKey = stayKey(*t.Selected)
		}
		tv.Results = make([]resultRow, len(t.Results))
		for j, r := range t.Results {
			tv.Results[j] = resultRow{
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
		}
		v.Trips[i] = tv
	}
	return v
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
	}
}
