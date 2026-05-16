package web

import (
	"html/template"
	"net/http"
	"strconv"
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
	h.render(w, "layout.html", struct{ App appView }{App: h.session.buildAppView()})
}

// updateBudget handles POST /budget.
func (h *handlers) updateBudget(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.budget = r.FormValue("budget")
	h.session.recomputeAll()
	h.render(w, "app", h.session.buildAppView())
}

// addTrip handles POST /trips.
func (h *handlers) addTrip(w http.ResponseWriter, r *http.Request) {
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	h.session.addTrip()
	h.render(w, "app", h.session.buildAppView())
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
	h.session.removeTrip(i)
	h.render(w, "app", h.session.buildAppView())
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
	if i < 0 || i >= len(h.session.trips) {
		http.Error(w, "trip out of range", http.StatusBadRequest)
		return
	}
	t := h.session.trips[i]
	t.Spec.From = r.FormValue("from")
	t.Spec.To = r.FormValue("to")
	t.Spec.MinNights = r.FormValue("min_nights")
	h.session.recomputeTrip(i)
	view := h.session.buildAppView()
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
	h.session.toggleSelection(i, row)
	h.render(w, "app", h.session.buildAppView())
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
	if i < 0 || i >= len(h.session.trips) {
		http.Error(w, "trip out of range", http.StatusBadRequest)
		return
	}
	h.session.toggleCollapsed(i)
	view := h.session.buildAppView()
	h.render(w, "trip", view.Trips[i])
}

// openFilters handles GET /filters.
func (h *handlers) openFilters(w http.ResponseWriter, r *http.Request) {
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	resorts, rts := h.session.filterOptions()
	h.render(w, "filters", filtersView{Resorts: resorts, RoomTypes: rts})
}

// toggleResortFilter handles POST /filters/resorts/{code}.
func (h *handlers) toggleResortFilter(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	if err := h.session.toggleResort(code); err != nil {
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
	if err := h.session.toggleRoomType(name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderFilterToggle(w)
}

// renderFilterToggle renders the filters_toggle template (panel + OOB trip-list).
// Caller must hold session lock.
func (h *handlers) renderFilterToggle(w http.ResponseWriter) {
	resorts, rts := h.session.filterOptions()
	data := struct {
		Filters filtersView
		App     appView
	}{
		Filters: filtersView{Resorts: resorts, RoomTypes: rts},
		App:     h.session.buildAppView(),
	}
	h.render(w, "filters_toggle", data)
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
	if err := h.session.savePlan(name); err != nil {
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
	if err := h.session.savePlan(name); err != nil {
		saveErr = err.Error()
	}
	h.renderPlans(w, saveErr)
}

// loadPlan handles POST /plans/{name}/load.
func (h *handlers) loadPlan(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	for _, p := range h.session.plans {
		if p.Name == name {
			h.session.applyPlan(p)
			break
		}
	}
	h.render(w, "plan_load", struct{ App appView }{App: h.session.buildAppView()})
}

// deletePlan handles DELETE /plans/{name}.
func (h *handlers) deletePlan(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.session.mu.Lock()
	defer h.session.mu.Unlock()
	saveErr := ""
	if err := h.session.deletePlan(name); err != nil {
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
		Plans:          h.session.plans,
		LoadedPlanName: h.session.loadedPlanName,
		Err:            errMsg,
	})
}
