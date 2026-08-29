package web

import (
	"fmt"
	"html/template"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

// tripListView is the data for the trip list page (/) and its #trip-list
// swap target.
type tripListView struct {
	Trips []tripRowView
	Err   string
}

// tripRowView is one row of the trip list. Status is DERIVED here from each
// stay's EntryID — never read from the database. A stored status becomes a lie
// the moment someone deletes a booked entry from /ledger.
type tripRowView struct {
	ID         int64
	Name       string
	StartLabel string
	EndLabel   string
	MinNights  int
	Stays      int
	Points     int
	Status     string // "planning" | "booked" | "partly booked"
	CostLabel  string // "" unless ShowCosts and the points priced
}

// budgetView is a render-ready projection of a ledger.TripBudget, with signed
// labels ("+270" / "-60") formatted Go-side to match recentEntryRow.DeltaLabel
// in ledger_view.go.
type budgetView struct {
	UseYear         int
	Current         int
	Banked          int
	Borrowable      int
	Total           int
	CurrentLabel    string // signed, e.g. "+270"
	BankedLabel     string // signed, e.g. "-60"
	BorrowableLabel string

	// Overridden and ComputedTotal let a later commit render "reset to
	// computed (N)" next to the effective budget. Nothing writes
	// BudgetOverride yet, so Overridden is always false today, but the
	// fields exist so that commit only has to add a button.
	Overridden    bool
	ComputedTotal int
}

// stayView is one stay collected on a trip.
type stayView struct {
	ID       int64
	Resort   string
	RoomType string
	View     string
	CheckIn  time.Time
	CheckOut time.Time
	Nights   int
	Points   int
	Booked   bool

	CostLabel     string
	CostProjected bool

	// cost is the same value as CostLabel, unformatted, so buildTripView can
	// sum real cents across a trip's stays rather than parsing formatted
	// dollar strings back apart. See resultRow.cost.
	cost ledger.Cents
}

// tripView is a render-ready projection of one trip's page.
type tripView struct {
	ID         int64
	Name       string
	StartDate  time.Time
	EndDate    time.Time
	StartLabel string
	EndLabel   string
	MinNights  int

	Budget budgetView

	// EffectiveBudget is the point budget the search actually ran against
	// (see effectiveBudget): the trip's BudgetOverride when set, otherwise
	// Budget.Total. BudgetOverridden mirrors whether that override applied.
	EffectiveBudget  int
	BudgetOverridden bool

	Stays          []stayView
	StaysPoints    int
	StaysCostLabel string

	Booked       bool
	PartlyBooked bool

	// HasBookedStays and HasUnbookedStays are exact synonyms for the
	// anyBooked/anyUnbooked values stayBookingStatus computes from stays'
	// EntryID — never re-derived from Booked/PartlyBooked/len(Stays). That
	// combination is genuinely ambiguous: Booked=false && PartlyBooked=false
	// covers BOTH "zero stays" and "one or more stays, all unbooked", and
	// book_controls needs to tell those apart to know whether "Book it"
	// should render.
	HasBookedStays   bool
	HasUnbookedStays bool

	// SpansUseYears and SpanNote flag a trip whose window straddles a
	// use-year boundary — the budget shown is only correct for the window
	// start's use year. See buildTripView.
	SpansUseYears bool
	SpanNote      string

	// Remaining is the point budget the search actually ran against — see
	// searchBudgetFor. Unlike EffectiveBudget, it has already had unbooked
	// stays' points subtracted, so it is what a user thinks of as "what's
	// left to spend."
	Remaining int

	// Results is populated by searchTrip and projected here by
	// buildTripView, capped at maxResultRows.
	Results []resultRow

	// ResultsTotal is the PRE-truncation result count. When it exceeds
	// len(Results), the template renders a truncation notice; the template
	// compares the two directly rather than a separate "truncated" bool, so
	// this must always be set (never left at its zero value once
	// buildTripView has run).
	ResultsTotal int

	Err       string
	ShowCosts bool
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

	// cost is the same value as CostLabel, unformatted, so a caller can sum
	// real cents across rows rather than parsing formatted dollar strings
	// back apart.
	cost ledger.Cents
}

// filterScope tells the ONE filters template which panel it is rendering so it
// can serve both the global filter panel and per-trip filter panels.
//
// Contract for templates (fpl.18/19, ixe.13):
//   - .Scope.IsTrip selects the POST URLs: global "/filters/..." when false,
//     per-trip "/trips/{.Scope.TripID}/filters/..." when true.
//   - .Scope.Mode (only meaningful when IsTrip) drives the inherit/override
//     switch and the disabled-rows-on-inherit hint. It is empty/ignored for
//     the global panel.
//   - .Scope.TripName drives filterTitle's per-trip heading text; unlike
//     TripID it is never rendered into a URL.
type filterScope struct {
	IsTrip   bool
	TripID   int64
	TripName string
	Mode     dvc.FilterMode // inherit/override; empty/ignored when !IsTrip
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
// on the same key.
func stayKey(r dvc.StayResult) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%d",
		r.Resort, r.RoomType, r.View,
		r.CheckIn.Format("2006-01-02"),
		r.CheckOut.Format("2006-01-02"),
		r.Points,
	)
}

// stayKeyForStay is stayKey's counterpart for a stored TripStay, so a result
// row already collected on this trip renders as selected. It MUST produce a
// byte-identical key for the same stay — including Points, for the reason
// stayKey documents.
func stayKeyForStay(st ledger.TripStay) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%d",
		st.Resort, st.RoomType, st.View,
		st.CheckIn.Format("2006-01-02"),
		st.CheckOut.Format("2006-01-02"),
		st.Points,
	)
}

// priceRow prices row's Points at its CheckIn date's use year — see
// ledger.UseYearForDate — against the blended rate (contractID nil: a
// planner stay is never attributed to a specific contract, so the
// portfolio-wide blended rate always applies). It sets row.cost, CostLabel
// and CostProjected in place, and is a no-op (leaving them at their zero
// values) when the stay's points can't be priced — CostBasis.Cost's own
// known=false case, e.g. Points <= 0. Callers must already know
// basis.Known() is true; calling this otherwise is harmless but pointless,
// since Cost always reports known=false.
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

// priceStay prices sv's Points at its CheckIn date's use year, resolved
// through the given use-year start month (the same anchor buildTripView's
// caller used to derive the trip's ledger.TripBudget, rather than reaching
// back into basis for it) against the blended rate. It sets sv.cost,
// CostLabel and CostProjected in place, and is a no-op when the points can't
// be priced.
func priceStay(sv *stayView, month time.Month, basis ledger.CostBasis) {
	year := ledger.UseYearForDate(sv.CheckIn, month)
	cost, projected, known := basis.Cost(sv.Points, year, nil)
	if !known {
		return
	}
	sv.cost = cost
	sv.CostLabel = ledger.FormatUSD(cost)
	sv.CostProjected = projected
}

// stayBookingStatus reports whether ANY and whether ANY-NOT of stays are
// booked, derived from each stay's EntryID — never from a stored status.
// This is the single loop both deriveTripStatus and the
// HasBookedStays/HasUnbookedStays tripView fields are built from, so the two
// can never disagree about what "booked" means.
func stayBookingStatus(stays []ledger.TripStay) (anyBooked, anyUnbooked bool) {
	for _, st := range stays {
		if st.EntryID != nil {
			anyBooked = true
		} else {
			anyUnbooked = true
		}
	}
	return anyBooked, anyUnbooked
}

// deriveTripStatus reports Booked/PartlyBooked from anyBooked/anyUnbooked
// (see stayBookingStatus). Zero stays is neither booked nor partly booked.
func deriveTripStatus(anyBooked, anyUnbooked bool) (booked, partlyBooked bool) {
	switch {
	case anyBooked && !anyUnbooked:
		return true, false
	case anyBooked && anyUnbooked:
		return false, true
	default:
		return false, false
	}
}

// buildTripRowView derives one trip list row. Status is derived from each
// stay's EntryID (see deriveTripStatus), never stored. Like buildTripView,
// it performs no I/O — the caller has already fetched stays.
func buildTripRowView(t ledger.Trip, stays []ledger.TripStay, month time.Month, basis ledger.CostBasis, showCosts bool) tripRowView {
	anyBooked, anyUnbooked := stayBookingStatus(stays)
	booked, partlyBooked := deriveTripStatus(anyBooked, anyUnbooked)
	status := "planning"
	switch {
	case booked:
		status = "booked"
	case partlyBooked:
		status = "partly booked"
	}

	points := 0
	var cost ledger.Cents
	priced := false
	for _, st := range stays {
		points += st.Points
		if !showCosts {
			continue
		}
		year := ledger.UseYearForDate(st.CheckIn, month)
		c, _, known := basis.Cost(st.Points, year, nil)
		if known {
			cost += c
			priced = true
		}
	}

	row := tripRowView{
		ID:         t.ID,
		Name:       t.Name,
		StartLabel: t.StartDate.Format("2006-01-02"),
		EndLabel:   t.EndDate.Format("2006-01-02"),
		MinNights:  t.MinNights,
		Stays:      len(stays),
		Points:     points,
		Status:     status,
	}
	if priced {
		row.CostLabel = ledger.FormatUSD(cost)
	}
	return row
}

// effectiveBudget is the point budget the search runs against: the trip's
// hand-typed override when set, otherwise the ledger-computed total.
// Nothing writes BudgetOverride yet — the UI for it is a later commit — but
// the column exists and search is where it takes effect, so honour it here.
// A *int pointing at 0 must yield 0, not fall through to the computed
// total — only nil means "no override".
func effectiveBudget(t ledger.Trip, b ledger.TripBudget) int {
	if t.BudgetOverride != nil {
		return *t.BudgetOverride
	}
	return b.Total
}

// searchBudgetFor is the point budget a trip's search runs against: the
// effective budget less the points of stays not yet booked. A BOOKED stay's
// points are already reflected in the ledger's used(UY) and so already
// reduced Budget.Current — subtracting them here too would double-count.
// Booking therefore must not move this number: the points shift out of the
// local subtraction and into the ledger's used, leaving the result
// unchanged. ixe.14 pins that as TestBookTrip_DoesNotChangeRemainingBudget.
func searchBudgetFor(t ledger.Trip, b ledger.TripBudget, stays []ledger.TripStay) int {
	budget := effectiveBudget(t, b)
	for _, st := range stays {
		if st.EntryID == nil {
			budget -= st.Points
		}
	}
	return budget
}

// spanBoundary is the first day of month in the END use year — the date on
// or after which a stay checking in draws from the later use year.
func spanBoundary(endUseYear int, month time.Month) time.Time {
	return time.Date(endUseYear, month, 1, 0, 0, 0, 0, time.UTC)
}

// maxResultRows caps how many stays a trip page renders. The
// ledger-computed budget is realistically 400-600 points where the old
// hand-typed default was "100", and dvc.Search's budget only bounds stay
// LENGTH, not the number of (check-in, check-out) pairs — so a 30-day
// window at a real budget produces thousands of rows. Search sorts by
// points ascending, so the first maxResultRows are the cheapest, which is
// the useful slice.
const maxResultRows = 200

// buildTripView projects stored trip state, a computed budget and a search
// result set into a render-ready tripView. It performs no I/O and touches no
// *ledger.Store — every caller has already fetched what it needs — so it is
// unit-testable with no Postgres and no t.Skip.
func buildTripView(
	t ledger.Trip, stays []ledger.TripStay, b ledger.TripBudget,
	results []dvc.StayResult, month time.Month,
	basis ledger.CostBasis, showCosts bool,
) tripView {
	anyBooked, anyUnbooked := stayBookingStatus(stays)
	booked, partlyBooked := deriveTripStatus(anyBooked, anyUnbooked)
	overridden := t.BudgetOverride != nil
	eff := effectiveBudget(t, b)

	tv := tripView{
		ID:               t.ID,
		Name:             t.Name,
		StartDate:        t.StartDate,
		EndDate:          t.EndDate,
		StartLabel:       t.StartDate.Format("2006-01-02"),
		EndLabel:         t.EndDate.Format("2006-01-02"),
		MinNights:        t.MinNights,
		ShowCosts:        showCosts,
		Booked:           booked,
		PartlyBooked:     partlyBooked,
		HasBookedStays:   anyBooked,
		HasUnbookedStays: anyUnbooked,
		EffectiveBudget:  eff,
		BudgetOverridden: overridden,
		Remaining:        searchBudgetFor(t, b, stays),
		Budget: budgetView{
			UseYear:         b.UseYear,
			Current:         b.Current,
			Banked:          b.Banked,
			Borrowable:      b.Borrowable,
			Total:           b.Total,
			CurrentLabel:    formatSignedDelta(b.Current),
			BankedLabel:     formatSignedDelta(b.Banked),
			BorrowableLabel: formatSignedDelta(b.Borrowable),
			Overridden:      overridden,
			ComputedTotal:   b.Total,
		},
	}

	startUY := ledger.UseYearForDate(t.StartDate, month)
	endUY := ledger.UseYearForDate(t.EndDate, month)
	if startUY != endUY {
		tv.SpansUseYears = true
		boundary := spanBoundary(endUY, month)
		tv.SpanNote = fmt.Sprintf(
			"This window spans use years %d → %d. The budget shown is for UY%d (the window start); stays checking in on or after %s draw from UY%d.",
			startUY, endUY, startUY, boundary.Format("2006-01-02"), endUY,
		)
	}

	var staysCost ledger.Cents
	tv.Stays = make([]stayView, len(stays))
	for i, st := range stays {
		sv := stayView{
			ID:       st.ID,
			Resort:   st.Resort,
			RoomType: st.RoomType,
			View:     st.View,
			CheckIn:  st.CheckIn,
			CheckOut: st.CheckOut,
			Nights:   st.Nights,
			Points:   st.Points,
			Booked:   st.EntryID != nil,
		}
		if showCosts {
			priceStay(&sv, month, basis)
		}
		tv.Stays[i] = sv
		tv.StaysPoints += st.Points
		staysCost += sv.cost
	}
	if showCosts {
		tv.StaysCostLabel = ledger.FormatUSD(staysCost)
	}

	collected := make(map[string]bool, len(stays))
	for _, st := range stays {
		collected[stayKeyForStay(st)] = true
	}

	tv.ResultsTotal = len(results)
	shown := results
	if len(shown) > maxResultRows {
		// PREFIX slice only — never a re-sort, filter, or tail. addStay
		// re-runs this same deterministic search and indexes {row} into the
		// FULL untruncated slice (see addStay's doc comment), so a
		// truncated row's index here must stay identical to its index
		// there. Search already sorts ascending by Points, so this keeps
		// the maxResultRows cheapest results.
		shown = shown[:maxResultRows]
	}
	tv.Results = make([]resultRow, len(shown))
	for i, r := range shown {
		row := resultRow{
			RowIndex: i,
			Resort:   r.Resort,
			RoomType: r.RoomType,
			View:     r.View,
			CheckIn:  r.CheckIn,
			CheckOut: r.CheckOut,
			Nights:   r.Nights,
			Points:   r.Points,
			Selected: collected[stayKey(r)],
		}
		if showCosts {
			priceRow(&row, basis)
		}
		tv.Results[i] = row
	}

	return tv
}

// toFiltersView adapts a dvc.FilterOptionsView plus the caller-resolved scope
// into the template's filtersView.
func toFiltersView(opts dvc.FilterOptionsView, scope filterScope) filtersView {
	fv := filtersView{
		Scope:     scope,
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
		// "Filters — Global" or "Filters — Beach week (override)" — the
		// trip's actual name, not positional numbering.
		"filterTitle": func(s filterScope) string {
			if !s.IsTrip {
				return "Filters — Global"
			}
			mode := "inherit"
			if s.Mode == dvc.FilterModeOverride {
				mode = "override"
			}
			return fmt.Sprintf("Filters — %s (%s)", s.TripName, mode)
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
