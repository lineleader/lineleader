package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

const ledgerDateLayout = "2006-01-02"

// ledgerHandlers serves the /ledger page. It is independent of the trip-planner
// Session and guards the store with its own mutex.
type ledgerHandlers struct {
	tmpl  *template.Template
	store *ledger.Store
	mu    sync.Mutex
}

func (h *ledgerHandlers) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderBody re-renders the #ledger-body fragment. Caller holds the lock.
func (h *ledgerHandlers) renderBody(w http.ResponseWriter, editID int64, errMsg string) {
	view, err := h.buildLedgerView(editID, errMsg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "ledger_body", view)
}

// page handles GET /ledger.
func (h *ledgerHandlers) page(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	view, err := h.buildLedgerView(0, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "ledger_page", view)
}

// addEntry handles POST /ledger/entries.
func (h *ledgerHandlers) addEntry(w http.ResponseWriter, r *http.Request) {
	e, err := parseEntryForm(r, 0)
	if err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.renderBody(w, 0, err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.store.AddEntry(e); err != nil {
		h.renderBody(w, 0, err.Error())
		return
	}
	h.renderBody(w, 0, "")
}

// editEntry handles GET /ledger/entries/{id}/edit — re-render with that row in edit mode.
func (h *ledgerHandlers) editEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.renderBody(w, id, "")
}

// updateEntry handles POST /ledger/entries/{id}/update.
func (h *ledgerHandlers) updateEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	e, err := parseEntryForm(r, id)
	if err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.renderBody(w, id, err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.UpdateEntry(e); err != nil {
		h.renderBody(w, id, err.Error())
		return
	}
	h.renderBody(w, 0, "")
}

// cancelEdit handles POST /ledger/entries/edit/cancel — leave edit mode.
func (h *ledgerHandlers) cancelEdit(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.renderBody(w, 0, "")
}

// deleteEntry handles DELETE /ledger/entries/{id}.
func (h *ledgerHandlers) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.DeleteEntry(id); err != nil {
		h.renderBody(w, 0, err.Error())
		return
	}
	h.renderBody(w, 0, "")
}

// addContract handles POST /ledger/contracts.
func (h *ledgerHandlers) addContract(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	month, err := parseMonth(r.FormValue("use_year_month"))
	if err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.renderBody(w, 0, err.Error())
		return
	}
	c := ledger.Contract{
		Name:         r.FormValue("name"),
		Number:       r.FormValue("number"),
		HomeResort:   r.FormValue("resort"),
		AnnualPoints: atoiOr(r.FormValue("points"), 0),
		UseYearMonth: month,
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.store.AddContract(c); err != nil {
		h.renderBody(w, 0, err.Error())
		return
	}
	h.renderBody(w, 0, "")
}

// deleteContract handles DELETE /ledger/contracts/{id}.
func (h *ledgerHandlers) deleteContract(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.DeleteContract(id); err != nil {
		h.renderBody(w, 0, err.Error())
		return
	}
	h.renderBody(w, 0, "")
}

// distribute handles POST /ledger/distribute.
func (h *ledgerHandlers) distribute(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.store.DistributeNextYear(); err != nil {
		h.renderBody(w, 0, err.Error())
		return
	}
	h.renderBody(w, 0, "")
}

// parseEntryForm builds an Entry from form values. id is set on the result (0 for add).
func parseEntryForm(r *http.Request, id int64) (ledger.Entry, error) {
	if err := r.ParseForm(); err != nil {
		return ledger.Entry{}, err
	}
	d, err := time.Parse(ledgerDateLayout, r.FormValue("date"))
	if err != nil {
		return ledger.Entry{}, err
	}
	year := atoiOr(r.FormValue("year"), 0)
	if year == 0 {
		year = d.Year()
	}
	e := ledger.Entry{
		ID:       id,
		UseYear:  year,
		Date:     d,
		Desc:     r.FormValue("desc"),
		Kind:     r.FormValue("kind"),
		Allotted: atoiOr(r.FormValue("allotted"), 0),
		Used:     atoiOr(r.FormValue("used"), 0),
		Tag:      r.FormValue("tag"),
	}
	if cid := atoi64Or(r.FormValue("contract"), 0); cid != 0 {
		e.ContractID = &cid
	}
	return e, nil
}

// pathID parses the {id} path value, writing 400 and returning ok=false on failure.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func atoi64Or(s string, def int64) int64 {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	return def
}

// parseMonth accepts a month number (1-12) or an English month name/abbreviation.
func parseMonth(s string) (time.Month, error) {
	for m := time.January; m <= time.December; m++ {
		if strings.EqualFold(s, m.String()) || strings.EqualFold(s, m.String()[:3]) {
			return m, nil
		}
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= 12 {
		return time.Month(n), nil
	}
	return 0, fmt.Errorf("invalid month %q (use Apr, April, or 4)", s)
}
