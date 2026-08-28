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
	basis, showCosts := h.costBasis(ctx)
	month := basis.UseYearMonth()
	view := buildTripView(t, stays, budget, nil, month, basis, showCosts)
	h.render(w, "trip_page", view)
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
