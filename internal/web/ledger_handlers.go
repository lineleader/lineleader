package web

import (
	"html/template"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

// ledgerHandlers serves the /ledger page. It is independent of the trip-planner
// Session and guards the store with its own mutex.
type ledgerHandlers struct {
	tmpl  *template.Template
	store *ledger.Store
	mu    sync.Mutex
}

// ledgerViewKind is the explicit view discriminator for /ledger's three
// routes (Recent, History, Contracts). Every template that hosts a mutation
// control (add/edit/update/delete/distribute) carries it as a "view" query
// parameter on the control's hx-* URL, so a mutation handler knows which
// view invoked it and renderBody can re-render that view's #ledger-body
// fragment — the same swap-in-place target every view shares.
type ledgerViewKind string

const (
	ledgerViewRecent    ledgerViewKind = "recent"
	ledgerViewHistory   ledgerViewKind = "history"
	ledgerViewContracts ledgerViewKind = "contracts"
)

// parseLedgerViewKind maps a "view" query-param value to its ledgerViewKind.
// An absent or unrecognised value defaults to Recent, so a bare or
// hand-made request still renders something coherent instead of erroring.
func parseLedgerViewKind(s string) ledgerViewKind {
	switch ledgerViewKind(s) {
	case ledgerViewHistory:
		return ledgerViewHistory
	case ledgerViewContracts:
		return ledgerViewContracts
	default:
		return ledgerViewRecent
	}
}

// viewFromRequest reads the "view" discriminator off the request's query
// string — the one place every mutation control (GET/POST/DELETE alike)
// carries it, per parseLedgerViewKind's default-to-Recent contract.
func viewFromRequest(r *http.Request) ledgerViewKind {
	return parseLedgerViewKind(r.URL.Query().Get("view"))
}

// bodyTemplateName is the #ledger-body fragment template that renders this view.
func (k ledgerViewKind) bodyTemplateName() string {
	switch k {
	case ledgerViewHistory:
		return "history_body"
	case ledgerViewContracts:
		return "contracts_body"
	default:
		return "recent_body"
	}
}

// ledgerPageData wraps the (unmodified) ledgerView with the view
// discriminator so templates can render nav active-state and embed the
// correct "view" query param into their own mutation URLs.
type ledgerPageData struct {
	ledgerView
	View ledgerViewKind
}

func (h *ledgerHandlers) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderBody re-renders the #ledger-body fragment for the given view. editID
// and editContractID mark, respectively, an entry row or a contract row for
// inline editing (0 for neither — the common case). Caller holds the lock.
func (h *ledgerHandlers) renderBody(w http.ResponseWriter, view ledgerViewKind, editID, editContractID int64, errMsg string) {
	lv, err := h.buildLedgerView(editID, editContractID, errMsg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, view.bodyTemplateName(), ledgerPageData{ledgerView: lv, View: view})
}

// renderPage renders the full ledger_page shell for the given view. A fresh
// GET never starts in edit mode, so editContractID is always 0 from its
// three callers below — it's a parameter (rather than hardcoding 0 inside)
// purely so its signature mirrors renderBody/buildLedgerView. Caller holds
// the lock.
func (h *ledgerHandlers) renderPage(w http.ResponseWriter, view ledgerViewKind, editContractID int64) {
	lv, err := h.buildLedgerView(0, editContractID, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "ledger_page", ledgerPageData{ledgerView: lv, View: view})
}

// page handles GET /ledger — the Recent view.
func (h *ledgerHandlers) page(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.renderPage(w, ledgerViewRecent, 0)
}

// history handles GET /ledger/history.
func (h *ledgerHandlers) history(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.renderPage(w, ledgerViewHistory, 0)
}

// contracts handles GET /ledger/contracts.
func (h *ledgerHandlers) contracts(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.renderPage(w, ledgerViewContracts, 0)
}

// addEntry handles POST /ledger/entries. It is hosted on both Recent and
// History (their entry_add_form each carry their own "view" query param), so
// the response must swap into whichever view invoked it.
func (h *ledgerHandlers) addEntry(w http.ResponseWriter, r *http.Request) {
	view := viewFromRequest(r)
	e, err := parseEntryForm(r, 0)
	if err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.renderBody(w, view, 0, 0, err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.store.AddEntry(e); err != nil {
		h.renderBody(w, view, 0, 0, err.Error())
		return
	}
	h.renderBody(w, view, 0, 0, "")
}

// editEntry handles GET /ledger/entries/{id}/edit — re-render with that row in edit mode.
func (h *ledgerHandlers) editEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	view := viewFromRequest(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.renderBody(w, view, id, 0, "")
}

// updateEntry handles POST /ledger/entries/{id}/update.
func (h *ledgerHandlers) updateEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	view := viewFromRequest(r)
	e, err := parseEntryForm(r, id)
	if err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.renderBody(w, view, id, 0, err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.UpdateEntry(e); err != nil {
		h.renderBody(w, view, id, 0, err.Error())
		return
	}
	h.renderBody(w, view, 0, 0, "")
}

// cancelEdit handles POST /ledger/entries/edit/cancel — leave edit mode.
func (h *ledgerHandlers) cancelEdit(w http.ResponseWriter, r *http.Request) {
	view := viewFromRequest(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.renderBody(w, view, 0, 0, "")
}

// deleteEntry handles DELETE /ledger/entries/{id}.
func (h *ledgerHandlers) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	view := viewFromRequest(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.DeleteEntry(id); err != nil {
		h.renderBody(w, view, 0, 0, err.Error())
		return
	}
	h.renderBody(w, view, 0, 0, "")
}

// addContract handles POST /ledger/contracts.
func (h *ledgerHandlers) addContract(w http.ResponseWriter, r *http.Request) {
	view := viewFromRequest(r)
	c, err := parseContractForm(r, 0)
	if err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.renderBody(w, view, 0, 0, err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.store.AddContract(c); err != nil {
		h.renderBody(w, view, 0, 0, err.Error())
		return
	}
	h.renderBody(w, view, 0, 0, "")
}

// editContract handles GET /ledger/contracts/{id}/edit — re-render with that
// contract row in edit mode.
func (h *ledgerHandlers) editContract(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	view := viewFromRequest(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.renderBody(w, view, 0, id, "")
}

// updateContract handles POST /ledger/contracts/{id}/update. It calls
// Store.UpdateContract — an UPDATE in place, never delete-and-re-add, which
// would violate entries.contract_id's ON DELETE SET NULL and silently strip
// every entry's contract attribution (see
// TestLedgerUpdateContractPersists).
func (h *ledgerHandlers) updateContract(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	view := viewFromRequest(r)
	c, err := parseContractForm(r, id)
	if err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.renderBody(w, view, 0, id, err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.UpdateContract(c); err != nil {
		h.renderBody(w, view, 0, id, err.Error())
		return
	}
	h.renderBody(w, view, 0, 0, "")
}

// cancelContractEdit handles POST /ledger/contracts/edit/cancel — leave edit mode.
func (h *ledgerHandlers) cancelContractEdit(w http.ResponseWriter, r *http.Request) {
	view := viewFromRequest(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.renderBody(w, view, 0, 0, "")
}

// parseContractForm builds a Contract from form values. id is set on the
// result (0 for add). Follows parseEntryForm's short-name field convention
// (contract, not contract_id) for the new cost fields: term_years, price,
// closing. price/closing are money fields — parsed with ledger.ParseCents,
// which treats a blank field as 0 ("cost unknown", not an error) and a
// malformed one as a hard error that flows out to the caller's Err
// rendering, exactly like use_year_month's ledger.ParseMonth below.
func parseContractForm(r *http.Request, id int64) (ledger.Contract, error) {
	if err := r.ParseForm(); err != nil {
		return ledger.Contract{}, err
	}
	month, err := ledger.ParseMonth(r.FormValue("use_year_month"))
	if err != nil {
		return ledger.Contract{}, err
	}
	price, err := ledger.ParseCents(r.FormValue("price"))
	if err != nil {
		return ledger.Contract{}, err
	}
	closing, err := ledger.ParseCents(r.FormValue("closing"))
	if err != nil {
		return ledger.Contract{}, err
	}
	return ledger.Contract{
		ID:            id,
		Name:          r.FormValue("name"),
		Number:        r.FormValue("number"),
		HomeResort:    r.FormValue("resort"),
		AnnualPoints:  atoiOr(r.FormValue("points"), 0),
		UseYearMonth:  month,
		TermYears:     atoiOr(r.FormValue("term_years"), 0),
		PurchasePrice: price,
		ClosingCosts:  closing,
	}, nil
}

// deleteContract handles DELETE /ledger/contracts/{id}.
func (h *ledgerHandlers) deleteContract(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	view := viewFromRequest(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.DeleteContract(id); err != nil {
		h.renderBody(w, view, 0, 0, err.Error())
		return
	}
	h.renderBody(w, view, 0, 0, "")
}

// upsertDues handles POST /ledger/dues — insert a new dues year or overwrite
// an existing one (see Store.UpsertDuesRate). A blank rate parses to 0 via
// ledger.ParseMicros (the standard "cost unknown" convention), but a dues
// rate of 0 is nonsense on its own terms — not a valid "unknown" state the
// way a contract's unset purchase price is — so the store's positivity
// check rejects it and that error surfaces through the usual Err path.
func (h *ledgerHandlers) upsertDues(w http.ResponseWriter, r *http.Request) {
	view := viewFromRequest(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	year := atoiOr(r.FormValue("year"), 0)
	rate, err := ledger.ParseMicros(r.FormValue("rate"))
	if err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.renderBody(w, view, 0, 0, err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.UpsertDuesRate(ledger.DuesRate{UseYear: year, Rate: rate}); err != nil {
		h.renderBody(w, view, 0, 0, err.Error())
		return
	}
	h.renderBody(w, view, 0, 0, "")
}

// deleteDues handles DELETE /ledger/dues/{year}.
func (h *ledgerHandlers) deleteDues(w http.ResponseWriter, r *http.Request) {
	year, ok := pathYear(w, r)
	if !ok {
		return
	}
	view := viewFromRequest(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.DeleteDuesRate(year); err != nil {
		h.renderBody(w, view, 0, 0, err.Error())
		return
	}
	h.renderBody(w, view, 0, 0, "")
}

// distribute handles POST /ledger/distribute.
func (h *ledgerHandlers) distribute(w http.ResponseWriter, r *http.Request) {
	view := viewFromRequest(r)
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.store.DistributeNextYear(); err != nil {
		h.renderBody(w, view, 0, 0, err.Error())
		return
	}
	h.renderBody(w, view, 0, 0, "")
}

// parseEntryForm builds an Entry from form values. id is set on the result (0 for add).
func parseEntryForm(r *http.Request, id int64) (ledger.Entry, error) {
	if err := r.ParseForm(); err != nil {
		return ledger.Entry{}, err
	}
	d, err := time.Parse(ledger.DateLayout, r.FormValue("date"))
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

// pathYear parses the {year} path value, writing 400 and returning ok=false
// on failure. Unlike pathID this isn't an entity id; DuesRate.UseYear is a
// plain int, so this returns one too rather than pathID's int64.
func pathYear(w http.ResponseWriter, r *http.Request) (int, bool) {
	year, err := strconv.Atoi(r.PathValue("year"))
	if err != nil {
		http.Error(w, "bad year", http.StatusBadRequest)
		return 0, false
	}
	return year, true
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
