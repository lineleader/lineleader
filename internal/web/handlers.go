package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

// handlers groups the http handlers + shared dependencies. Trips and stays
// live in Postgres (h.store); global filters are the one piece of shared
// mutable state left in this package (h.global guards itself). Neither the
// trip list nor the trip page handlers need a handler-wide lock.
type handlers struct {
	tmpl   *template.Template
	charts []*dvc.ResortChart
	global *globalFilters
	store  *ledger.Store
	costs  *costProvider
}

// render executes one named template against w.
func (h *handlers) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// costBasis fetches the current cost snapshot and reports whether it's
// priced enough to show dollar figures — see costProvider and
// ledger.CostBasis.Known.
func (h *handlers) costBasis(ctx context.Context) (ledger.CostBasis, bool) {
	basis, ok := h.costs.Basis(ctx)
	return basis, ok && basis.Known()
}

// buildTripListView fetches each trip's stays and projects the trip list
// into a render-ready tripListView.
func (h *handlers) buildTripListView(ctx context.Context, trips []ledger.Trip) (tripListView, error) {
	basis, showCosts := h.costBasis(ctx)
	month := basis.UseYearMonth()

	view := tripListView{Trips: make([]tripRowView, 0, len(trips))}
	for _, t := range trips {
		stays, err := h.store.ListStays(ctx, t.ID)
		if err != nil {
			return tripListView{}, fmt.Errorf("listing stays for trip %d: %w", t.ID, err)
		}
		view.Trips = append(view.Trips, buildTripRowView(t, stays, month, basis, showCosts))
	}
	return view, nil
}

// renderTripList re-fetches every trip and renders the full trips_page, with
// an optional error message (e.g. from a failed create-trip submission).
func (h *handlers) renderTripList(w http.ResponseWriter, r *http.Request, errMsg string) {
	ctx := r.Context()
	trips, err := h.store.ListTrips(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view, err := h.buildTripListView(ctx, trips)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view.Err = errMsg
	h.render(w, "trips_page", view)
}

// tripList handles GET /.
func (h *handlers) tripList(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	h.renderTripList(w, r, "")
}

// newTripForm handles GET /trips/new.
func (h *handlers) newTripForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, "trip_new_form", nil)
}

// parseTripForm parses and validates a trip creation form, following the
// ledger handlers' convention: a validation failure is returned as an error
// for the caller to render inline (200, not a hard 400).
func parseTripForm(r *http.Request) (ledger.Trip, error) {
	if err := r.ParseForm(); err != nil {
		return ledger.Trip{}, err
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return ledger.Trip{}, errors.New("name is required")
	}
	from, err := dvc.ParseDate(r.FormValue("from"))
	if err != nil {
		return ledger.Trip{}, err
	}
	to, err := dvc.ParseDate(r.FormValue("to"))
	if err != nil {
		return ledger.Trip{}, err
	}
	if !from.Before(to) {
		return ledger.Trip{}, errors.New("start date must be before end date")
	}
	minNights, err := strconv.Atoi(r.FormValue("min_nights"))
	if err != nil {
		return ledger.Trip{}, errors.New("invalid min nights")
	}
	if minNights < 1 {
		return ledger.Trip{}, errors.New("min nights must be at least 1")
	}
	if minNights > dvc.MaxNights {
		return ledger.Trip{}, fmt.Errorf("min nights exceeds Disney's %d-night limit", dvc.MaxNights)
	}
	return ledger.Trip{
		Name:      name,
		StartDate: from,
		EndDate:   to,
		MinNights: minNights,
	}, nil
}

// createTrip handles POST /trips.
func (h *handlers) createTrip(w http.ResponseWriter, r *http.Request) {
	t, err := parseTripForm(r)
	if err != nil {
		h.renderTripList(w, r, err.Error())
		return
	}
	id, err := h.store.AddTrip(r.Context(), t)
	if err != nil {
		h.renderTripList(w, r, err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/trips/%d", id), http.StatusSeeOther)
}

// tripID parses the {id} path value, writing a 400 and returning ok=false on
// a malformed value.
func tripID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad trip id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// searchTrip runs dvc.Search for a stored trip. Every parameter comes from
// the trip ROW and the process-wide chart set, never from the request:
// Search is deterministic given (charts, params) and charts are immutable
// for the process lifetime, so a later POST /trips/{id}/stays/{row} can
// reconstruct an identical result set from the same trip row and resolve
// {row} against it. The Planner got that for free by holding the results in
// memory; now it is an explicit invariant.
func (h *handlers) searchTrip(t ledger.Trip, budget int) []dvc.StayResult {
	filters := dvc.EffectiveFilters(h.global.Get(), dvc.FilterMode(t.FilterMode), dvc.FilterSet{
		ExcludeResorts:   t.ExcludeResorts,
		ExcludeRoomTypes: t.ExcludeRoomTypes,
	})
	return dvc.Search(h.charts, dvc.SearchParams{
		WindowStart:      t.StartDate,
		WindowEnd:        t.EndDate,
		Budget:           budget,
		MinNights:        t.MinNights,
		ExcludeResorts:   filters.ExcludeResorts,
		ExcludeRoomTypes: filters.ExcludeRoomTypes,
	})
}

// buildTripPageView fetches t's stays and budget, runs the search and
// projects the render-ready tripView for it, with errMsg applied as
// tv.Err (e.g. from a rejected update-trip submission).
func (h *handlers) buildTripPageView(ctx context.Context, t ledger.Trip, errMsg string) (tripView, error) {
	stays, err := h.store.ListStays(ctx, t.ID)
	if err != nil {
		return tripView{}, fmt.Errorf("listing stays for trip %d: %w", t.ID, err)
	}
	budget, err := h.store.TripBudget(ctx, t.StartDate)
	if err != nil {
		return tripView{}, err
	}
	basis, showCosts := h.costBasis(ctx)
	month := basis.UseYearMonth()
	results := h.searchTrip(t, searchBudgetFor(t, budget, stays))
	view := buildTripView(t, stays, budget, results, month, basis, showCosts)
	view.Err = errMsg
	return view, nil
}

// renderTripFragment re-fetches trip id, rebuilds its page view (no error
// message) and renders the "trip" fragment. This is the shared success tail
// for updateTrip, addStay and removeStay: each mutates something about a
// trip and then needs the freshly re-rendered #trip swapped into the page.
func (h *handlers) renderTripFragment(w http.ResponseWriter, r *http.Request, id int64) {
	ctx := r.Context()
	t, err := h.store.GetTrip(ctx, id)
	if err != nil {
		if errors.Is(err, ledger.ErrTripNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view, err := h.buildTripPageView(ctx, t, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "trip", view)
}

// tripPage handles GET /trips/{id}.
func (h *handlers) tripPage(w http.ResponseWriter, r *http.Request) {
	id, ok := tripID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	t, err := h.store.GetTrip(ctx, id)
	if err != nil {
		if errors.Is(err, ledger.ErrTripNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view, err := h.buildTripPageView(ctx, t, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "trip_page", view)
}

// updateTrip handles POST /trips/{id}: edits a trip's name/dates/min-nights
// and re-runs its search.
//
// A validation failure renders the "trip" fragment (200, not 400) using the
// STORED trip — the rejected input is not preserved, following the ledger
// handlers' inline-error convention. That is a known wart: the user's typed
// values vanish on a rejected submit rather than staying in the form.
//
// On success, parseTripForm's return value carries only
// Name/StartDate/EndDate/MinNights — it knows nothing about
// BudgetOverride/FilterMode/ExcludeResorts/ExcludeRoomTypes, so those four
// fields are copied forward from the existing stored trip before calling
// UpdateTrip. Passing parseTripForm's result straight to UpdateTrip would
// silently wipe them.
func (h *handlers) updateTrip(w http.ResponseWriter, r *http.Request) {
	id, ok := tripID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	existing, err := h.store.GetTrip(ctx, id)
	if err != nil {
		if errors.Is(err, ledger.ErrTripNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	edited, err := parseTripForm(r)
	if err != nil {
		view, verr := h.buildTripPageView(ctx, existing, err.Error())
		if verr != nil {
			http.Error(w, verr.Error(), http.StatusInternalServerError)
			return
		}
		h.render(w, "trip", view)
		return
	}

	edited.ID = existing.ID
	edited.BudgetOverride = existing.BudgetOverride
	edited.FilterMode = existing.FilterMode
	edited.ExcludeResorts = existing.ExcludeResorts
	edited.ExcludeRoomTypes = existing.ExcludeRoomTypes

	if err := h.store.UpdateTrip(ctx, edited); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderTripFragment(w, r, id)
}

// addStay handles POST /trips/{id}/stays/{row}: collects one search result
// row onto the trip as a new, unbooked ledger.TripStay.
func (h *handlers) addStay(w http.ResponseWriter, r *http.Request) {
	id, ok := tripID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	t, err := h.store.GetTrip(ctx, id)
	if err != nil {
		if errors.Is(err, ledger.ErrTripNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	stays, err := h.store.ListStays(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	budget, err := h.store.TripBudget(ctx, t.StartDate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Reconstruct the SAME result set the browser rendered: searchTrip is
	// deterministic given (charts, params), and every param comes from the
	// trip row and the process-wide chart set (see searchTrip's doc
	// comment) — so re-running it here against the same searchBudgetFor
	// budget yields byte-identical rows in the same order, and {row} can be
	// resolved against it.
	results := h.searchTrip(t, searchBudgetFor(t, budget, stays))

	row, err := strconv.Atoi(r.PathValue("row"))
	if err != nil || row < 0 || row >= len(results) {
		http.Error(w, "invalid result row", http.StatusBadRequest)
		return
	}
	res := results[row]

	st := ledger.TripStay{
		TripID:   id,
		Resort:   res.Resort,
		RoomType: res.RoomType,
		View:     res.View,
		CheckIn:  res.CheckIn,
		CheckOut: res.CheckOut,
		Nights:   res.Nights,
		Points:   res.Points,
		// QuoteHash is left empty: dvc.StayResult carries no resort code,
		// column index or nightly rates, and plan §4 pins StayResult as
		// unchanged, so the quote fingerprint cannot be computed here. The
		// column defaults to '' and nothing reads it yet.
	}
	if _, err := h.store.AddStay(ctx, st); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderTripFragment(w, r, id)
}

// removeStay handles DELETE /trips/{id}/stays/{sid}.
func (h *handlers) removeStay(w http.ResponseWriter, r *http.Request) {
	id, ok := tripID(w, r)
	if !ok {
		return
	}
	sid, err := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	if err != nil {
		http.Error(w, "bad stay id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	if err := h.store.DeleteStay(ctx, sid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// DeleteStay may have deleted a BOOKED stay's linked ledger entry (see
	// ledger.Store.DeleteStay) — invalidate the cached CostBasis so the
	// next fetch reflects the mutated ledger, exactly as every
	// ledger-mutating handler does (see ledger_handlers.go).
	h.costs.Invalidate()

	h.renderTripFragment(w, r, id)
}

// deleteTrip handles DELETE /trips/{id}.
func (h *handlers) deleteTrip(w http.ResponseWriter, r *http.Request) {
	id, ok := tripID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteTrip(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectHome(w, r)
}

// redirectHome sends the client back to the trip list. The trip list's delete
// button is an hx-delete with no hx-target, and htmx transparently FOLLOWS a
// 3xx and swaps the redirected page into that default target — nesting the
// whole trips page inside a table cell. So answer an htmx request with
// HX-Redirect (a real browser navigation), matching the pattern
// authMiddleware already uses for /login, and keep the plain 303 for
// non-htmx clients.
func redirectHome(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// openFilters handles GET /filters — the global filter panel.
func (h *handlers) openFilters(w http.ResponseWriter, r *http.Request) {
	cfg := h.global.Get()
	opts := dvc.FilterOptionsFor(h.charts, cfg.AsFilterSet())
	h.render(w, "filters", toFiltersView(opts, filterScope{}))
}

// tripFilterScope builds the per-trip filterScope for t, used both to open
// the panel and to re-render it after a mutation.
func tripFilterScope(t ledger.Trip) filterScope {
	return filterScope{
		IsTrip:   true,
		TripID:   t.ID,
		TripName: t.Name,
		Mode:     dvc.FilterMode(t.FilterMode),
	}
}

// tripFilterSet adapts t's stored exclusion columns into a dvc.FilterSet.
func tripFilterSet(t ledger.Trip) dvc.FilterSet {
	return dvc.FilterSet{
		ExcludeResorts:   t.ExcludeResorts,
		ExcludeRoomTypes: t.ExcludeRoomTypes,
	}
}

// getTripOr404 fetches trip id, writing 404 (ledger.ErrTripNotFound) or 500
// (any other error) and returning ok=false when it can't. Shared by every
// per-trip filter handler, mirroring tripPage/renderTripFragment's own
// GetTrip-or-404 handling.
func (h *handlers) getTripOr404(w http.ResponseWriter, r *http.Request, id int64) (ledger.Trip, bool) {
	t, err := h.store.GetTrip(r.Context(), id)
	if err != nil {
		if errors.Is(err, ledger.ErrTripNotFound) {
			http.NotFound(w, r)
			return ledger.Trip{}, false
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return ledger.Trip{}, false
	}
	return t, true
}

// openTripFilters handles GET /trips/{id}/filters — the per-trip filter
// panel. The options shown reflect the trip's EFFECTIVE filters (its own set
// when overriding, the global set when inheriting) — the same resolution
// searchTrip uses, so the panel never shows something search doesn't apply.
func (h *handlers) openTripFilters(w http.ResponseWriter, r *http.Request) {
	id, ok := tripID(w, r)
	if !ok {
		return
	}
	t, ok := h.getTripOr404(w, r, id)
	if !ok {
		return
	}
	filters := dvc.EffectiveFilters(h.global.Get(), dvc.FilterMode(t.FilterMode), tripFilterSet(t))
	opts := dvc.FilterOptionsFor(h.charts, filters)
	h.render(w, "filters", toFiltersView(opts, tripFilterScope(t)))
}

// renderTripFilterToggle renders the filters_trip_toggle template (the
// per-trip panel plus that trip's #trip-results OOB) reflecting a
// just-applied per-trip filter change on t (already persisted by the
// caller).
func (h *handlers) renderTripFilterToggle(w http.ResponseWriter, r *http.Request, t ledger.Trip) {
	ctx := r.Context()
	filters := dvc.EffectiveFilters(h.global.Get(), dvc.FilterMode(t.FilterMode), tripFilterSet(t))
	opts := dvc.FilterOptionsFor(h.charts, filters)

	tv, err := h.buildTripPageView(ctx, t, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Filters filtersView
		Trip    tripView
	}{
		Filters: toFiltersView(opts, tripFilterScope(t)),
		Trip:    tv,
	}
	h.render(w, "filters_trip_toggle", data)
}

// setTripFilterMode handles POST /trips/{id}/filters/mode, form field "mode":
// "inherit" | "override".
//
// Switching to override SEEDS the trip's exclusion set from the current
// global config via dvc.CloneFilterSet — a deep copy, so the trip's slices
// never alias the global config's. Without seeding, flipping to override
// would silently clear every exclusion the user was already seeing (since an
// override trip with no exclusions of its own would exclude nothing).
//
// Switching to inherit (and DELETE /trips/{id}/filters, see resetTripFilters)
// clears both exclusion slices to nil rather than leaving them populated:
// leaving a stale set behind means a later switch back to override
// resurrects filters the user reset, instead of re-seeding from the
// then-current global config. marshalStringList treats a nil slice the same
// as an empty one (both round-trip through the stored '[]'), so nil is fine
// here.
func (h *handlers) setTripFilterMode(w http.ResponseWriter, r *http.Request) {
	id, ok := tripID(w, r)
	if !ok {
		return
	}
	t, ok := h.getTripOr404(w, r, id)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch r.FormValue("mode") {
	case "override":
		cloned := dvc.CloneFilterSet(h.global.Get().AsFilterSet())
		t.FilterMode = ledger.TripFilterOverride
		t.ExcludeResorts = cloned.ExcludeResorts
		t.ExcludeRoomTypes = cloned.ExcludeRoomTypes
	case "inherit":
		t.FilterMode = ledger.TripFilterInherit
		t.ExcludeResorts = nil
		t.ExcludeRoomTypes = nil
	default:
		http.Error(w, "invalid mode", http.StatusBadRequest)
		return
	}
	if err := h.store.UpdateTrip(r.Context(), t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderTripFilterToggle(w, r, t)
}

// toggleTripFilter is the shared body of toggleTripResortFilter and
// toggleTripRoomTypeFilter: fetch the trip, reject the mutation with 409 if
// it's in inherit mode, otherwise apply mutate and persist.
//
// The template already disables the toggle buttons in inherit mode, so a
// request reaching this handler while inheriting is out-of-band (a stale
// panel, a replayed request, or a client bypassing the UI). Silently writing
// exclusions onto an inherit-mode trip would create a row whose stored
// filters are invisible in the UI (the panel shows the global set, not the
// trip's) and spring back into effect the moment the trip switches to
// override — so this rejects rather than absorbs the request.
func (h *handlers) toggleTripFilter(w http.ResponseWriter, r *http.Request, id int64, mutate func(*ledger.Trip)) {
	t, ok := h.getTripOr404(w, r, id)
	if !ok {
		return
	}
	if dvc.FilterMode(t.FilterMode) != dvc.FilterModeOverride {
		http.Error(w, "trip is inheriting global filters; switch to override to edit its filters", http.StatusConflict)
		return
	}
	mutate(&t)
	if err := h.store.UpdateTrip(r.Context(), t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderTripFilterToggle(w, r, t)
}

// toggleTripResortFilter handles POST /trips/{id}/filters/resorts/{code}.
func (h *handlers) toggleTripResortFilter(w http.ResponseWriter, r *http.Request) {
	id, ok := tripID(w, r)
	if !ok {
		return
	}
	code := r.PathValue("code")
	h.toggleTripFilter(w, r, id, func(t *ledger.Trip) {
		t.ExcludeResorts = toggleString(t.ExcludeResorts, code)
	})
}

// toggleTripRoomTypeFilter handles POST /trips/{id}/filters/roomtypes/{name}.
func (h *handlers) toggleTripRoomTypeFilter(w http.ResponseWriter, r *http.Request) {
	id, ok := tripID(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	h.toggleTripFilter(w, r, id, func(t *ledger.Trip) {
		t.ExcludeRoomTypes = toggleString(t.ExcludeRoomTypes, name)
	})
}

// resetTripFilters handles DELETE /trips/{id}/filters: sets the trip back to
// inherit and clears both exclusion slices (see setTripFilterMode's doc
// comment for why the slices are cleared, not just the mode).
func (h *handlers) resetTripFilters(w http.ResponseWriter, r *http.Request) {
	id, ok := tripID(w, r)
	if !ok {
		return
	}
	t, ok := h.getTripOr404(w, r, id)
	if !ok {
		return
	}
	t.FilterMode = ledger.TripFilterInherit
	t.ExcludeResorts = nil
	t.ExcludeRoomTypes = nil
	if err := h.store.UpdateTrip(r.Context(), t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderTripFilterToggle(w, r, t)
}

// toggleResortFilter handles POST /filters/resorts/{code}.
func (h *handlers) toggleResortFilter(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if err := h.global.ToggleResort(code); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderFilterToggle(w, r)
}

// toggleRoomTypeFilter handles POST /filters/roomtypes/{name}.
func (h *handlers) toggleRoomTypeFilter(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := h.global.ToggleRoomType(name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderFilterToggle(w, r)
}

// renderFilterToggle renders the filters_toggle template (panel + OOB
// trip-list), reflecting a just-applied global filter change.
func (h *handlers) renderFilterToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg := h.global.Get()
	opts := dvc.FilterOptionsFor(h.charts, cfg.AsFilterSet())

	trips, err := h.store.ListTrips(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	listView, err := h.buildTripListView(ctx, trips)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Filters  filtersView
		TripList tripListView
	}{
		Filters:  toFiltersView(opts, filterScope{}),
		TripList: listView,
	}
	h.render(w, "filters_toggle", data)
}

// closePanel handles GET /panel/close.
func (h *handlers) closePanel(w http.ResponseWriter, r *http.Request) {
	h.render(w, "panel_empty", nil)
}
