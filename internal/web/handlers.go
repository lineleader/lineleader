package web

import (
	"html/template"
	"net/http"
	"strconv"

	"github.com/lineleader/lineleader/internal/dvc"
)

// handlers groups the http handlers + shared dependencies.
type handlers struct {
	tmpl    *template.Template
	session *Session
}

// render executes one named template against w.
func (h *handlers) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// index renders the full page.
func (h *handlers) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	snap := h.session.p.Snapshot()
	h.render(w, "layout.html", struct{ App appView }{App: h.session.buildAppView(snap)})
}

// updateBudget handles POST /budget.
func (h *handlers) updateBudget(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.p.SetBudget(r.FormValue("budget"))
	snap := h.session.p.Snapshot()
	h.render(w, "app", h.session.buildAppView(snap))
}

// addTrip handles POST /trips.
func (h *handlers) addTrip(w http.ResponseWriter, r *http.Request) {
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.p.AddTrip()
	snap := h.session.p.Snapshot()
	h.session.reconcileCollapsed(snap)
	h.render(w, "app", h.session.buildAppView(snap))
}

// removeTrip handles DELETE /trips/{i}.
func (h *handlers) removeTrip(w http.ResponseWriter, r *http.Request) {
	i, err := strconv.Atoi(r.PathValue("i"))
	if err != nil {
		http.Error(w, "bad trip index", http.StatusBadRequest)
		return
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.p.RemoveTrip(i)
	snap := h.session.p.Snapshot()
	h.session.reconcileCollapsed(snap)
	h.render(w, "app", h.session.buildAppView(snap))
}

// updateField handles POST /trips/{i}/field.
func (h *handlers) updateField(w http.ResponseWriter, r *http.Request) {
	i, err := strconv.Atoi(r.PathValue("i"))
	if err != nil {
		http.Error(w, "bad trip index", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	snap := h.session.p.Snapshot()
	if i < 0 || i >= len(snap.Trips) {
		http.Error(w, "trip out of range", http.StatusBadRequest)
		return
	}
	h.session.p.SetTripField(i, 0, r.FormValue("from"))
	h.session.p.SetTripField(i, 1, r.FormValue("to"))
	h.session.p.SetTripField(i, 2, r.FormValue("min_nights"))
	view := h.session.buildAppView(h.session.p.Snapshot())
	h.render(w, "results", view.Trips[i])
}

// toggleSelection handles POST /trips/{i}/select/{row}.
func (h *handlers) toggleSelection(w http.ResponseWriter, r *http.Request) {
	i, err := strconv.Atoi(r.PathValue("i"))
	if err != nil {
		http.Error(w, "bad trip index", http.StatusBadRequest)
		return
	}
	row, err := strconv.Atoi(r.PathValue("row"))
	if err != nil {
		http.Error(w, "bad row index", http.StatusBadRequest)
		return
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.p.ToggleSelection(i, row)
	snap := h.session.p.Snapshot()
	// Preserve the collapse-on-select UX: selecting a row collapses the trip so
	// the user can move on; deselecting expands it again. The Planner no longer
	// tracks Collapsed, so derive select vs deselect from the resulting snapshot.
	if i >= 0 && i < len(snap.Trips) && i < len(h.session.collapsed) {
		h.session.collapsed[i] = snap.Trips[i].Selected != nil
	}
	h.render(w, "app", h.session.buildAppView(snap))
}

// toggleCollapsed handles POST /trips/{i}/collapse.
func (h *handlers) toggleCollapsed(w http.ResponseWriter, r *http.Request) {
	i, err := strconv.Atoi(r.PathValue("i"))
	if err != nil {
		http.Error(w, "bad trip index", http.StatusBadRequest)
		return
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	snap := h.session.p.Snapshot()
	if i < 0 || i >= len(snap.Trips) {
		http.Error(w, "trip out of range", http.StatusBadRequest)
		return
	}
	h.session.toggleCollapsed(i)
	view := h.session.buildAppView(snap)
	h.render(w, "trip", view.Trips[i])
}

// openFilters handles GET /filters.
func (h *handlers) openFilters(w http.ResponseWriter, r *http.Request) {
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.render(w, "filters", toFiltersView(h.session.p.FilterOptions(-1)))
}

// toggleResortFilter handles POST /filters/resorts/{code}.
func (h *handlers) toggleResortFilter(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	if err := h.session.p.ToggleGlobalResort(code); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderFilterToggle(w)
}

// toggleRoomTypeFilter handles POST /filters/roomtypes/{name}.
func (h *handlers) toggleRoomTypeFilter(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	if err := h.session.p.ToggleGlobalRoomType(name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderFilterToggle(w)
}

// renderFilterToggle renders the filters_toggle template (panel + OOB trip-list).
// Caller must hold session lock.
func (h *handlers) renderFilterToggle(w http.ResponseWriter) {
	data := struct {
		Filters filtersView
		App     appView
	}{
		Filters: toFiltersView(h.session.p.FilterOptions(-1)),
		App:     h.session.buildAppView(h.session.p.Snapshot()),
	}
	h.render(w, "filters_toggle", data)
}

// openTripFilters handles GET /trips/{i}/filters — opens the per-trip panel.
// It renders only the "filters" panel (no OOB results swap), mirroring openFilters.
func (h *handlers) openTripFilters(w http.ResponseWriter, r *http.Request) {
	i, ok := h.tripIndex(w, r)
	if !ok {
		return
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.render(w, "filters", toFiltersView(h.session.p.FilterOptions(i)))
}

// setTripFilterMode handles POST /trips/{i}/filters/mode.
// The form value "mode" maps "override" -> dvc.FilterModeOverride and "inherit"
// -> dvc.FilterModeInherit. Any UNKNOWN value is treated as inherit: this is the
// safe default (the global filters), and it keeps a malformed request from
// silently seeding a per-trip override.
func (h *handlers) setTripFilterMode(w http.ResponseWriter, r *http.Request) {
	i, ok := h.tripIndex(w, r)
	if !ok {
		return
	}
	mode := dvc.FilterModeInherit
	if r.FormValue("mode") == string(dvc.FilterModeOverride) {
		mode = dvc.FilterModeOverride
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.p.SetTripFilterMode(i, mode)
	h.renderTripFilterToggle(w, i)
}

// toggleTripResort handles POST /trips/{i}/filters/resorts/{code}.
func (h *handlers) toggleTripResort(w http.ResponseWriter, r *http.Request) {
	i, ok := h.tripIndex(w, r)
	if !ok {
		return
	}
	code := r.PathValue("code")
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.p.ToggleTripResort(i, code)
	h.renderTripFilterToggle(w, i)
}

// toggleTripRoomType handles POST /trips/{i}/filters/roomtypes/{name}. The mux
// URL-decodes {name}, so room types with spaces (e.g. "ONE-BEDROOM VILLA")
// arrive intact — matching the global room-type route's decoding.
func (h *handlers) toggleTripRoomType(w http.ResponseWriter, r *http.Request) {
	i, ok := h.tripIndex(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.p.ToggleTripRoomType(i, name)
	h.renderTripFilterToggle(w, i)
}

// resetTripFilters handles DELETE /trips/{i}/filters — back to inherit.
func (h *handlers) resetTripFilters(w http.ResponseWriter, r *http.Request) {
	i, ok := h.tripIndex(w, r)
	if !ok {
		return
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.p.ResetTripFilters(i)
	h.renderTripFilterToggle(w, i)
}

// tripIndex parses and range-checks the {i} path value, writing a 400 and
// returning ok=false on a bad or out-of-range index.
func (h *handlers) tripIndex(w http.ResponseWriter, r *http.Request) (int, bool) {
	i, err := strconv.Atoi(r.PathValue("i"))
	if err != nil {
		http.Error(w, "bad trip index", http.StatusBadRequest)
		return 0, false
	}
	h.session.mu.Lock()
	n := len(h.session.p.Snapshot().Trips)
	h.session.mu.Unlock()
	if i < 0 || i >= n {
		http.Error(w, "trip out of range", http.StatusBadRequest)
		return 0, false
	}
	return i, true
}

// renderTripFilterToggle renders the per-trip filters_trip_toggle template: the
// filter PANEL plus ONLY the affected trip's results, OOB-swapped into
// #trip-{i}-results. Other trips are untouched. Caller must hold session lock.
func (h *handlers) renderTripFilterToggle(w http.ResponseWriter, i int) {
	view := h.session.buildAppView(h.session.p.Snapshot())
	data := struct {
		Filters filtersView
		Trip    tripView
	}{
		Filters: toFiltersView(h.session.p.FilterOptions(i)),
		Trip:    view.Trips[i],
	}
	h.render(w, "filters_trip_toggle", data)
}

// openPlans handles GET /plans.
func (h *handlers) openPlans(w http.ResponseWriter, r *http.Request) {
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.renderPlans(w, "")
}

// savePlan handles POST /plans.
func (h *handlers) savePlan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	saveErr := ""
	if err := h.session.p.SavePlan(name); err != nil {
		saveErr = err.Error()
	}
	h.renderPlans(w, saveErr)
}

// updatePlan handles POST /plans/{name}/update — overwrites the named plan.
func (h *handlers) updatePlan(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	saveErr := ""
	if err := h.session.p.SavePlan(name); err != nil {
		saveErr = err.Error()
	}
	h.renderPlans(w, saveErr)
}

// loadPlan handles POST /plans/{name}/load.
func (h *handlers) loadPlan(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.p.LoadPlan(name)
	snap := h.session.p.Snapshot()
	h.session.reconcileCollapsed(snap)
	h.render(w, "plan_load", struct{ App appView }{App: h.session.buildAppView(snap)})
}

// deletePlan handles DELETE /plans/{name}.
func (h *handlers) deletePlan(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	saveErr := ""
	if err := h.session.p.DeletePlan(name); err != nil {
		saveErr = err.Error()
	}
	h.renderPlans(w, saveErr)
}

// closePanel handles GET /panel/close.
func (h *handlers) closePanel(w http.ResponseWriter, r *http.Request) {
	h.render(w, "panel_empty", nil)
}

// renderPlans renders the plans panel. Caller must hold session lock.
func (h *handlers) renderPlans(w http.ResponseWriter, errMsg string) {
	h.render(w, "plans", plansView{
		Plans:          h.session.p.Plans(),
		LoadedPlanName: h.session.p.LoadedPlanName(),
		Err:            errMsg,
	})
}
