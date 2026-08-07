package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

// apiHandlers serves the /api/v1/ledger/* JSON surface described in
// docs/pitches/hosted-lineleader.md ("3. JSON API for the ledger"). It is a
// thin marshal/unmarshal layer over ledger.Store — no domain logic lives
// here beyond the validation/defaulting the CLI and HTML form already do.
type apiHandlers struct {
	store *ledger.Store
}

// entryDTO is the wire shape of a ledger.Entry response (no running balance —
// that's list-only; see entryListDTO).
type entryDTO struct {
	ID         int64  `json:"id"`
	UseYear    int    `json:"use_year"`
	Date       string `json:"date"`
	Desc       string `json:"description"`
	Kind       string `json:"kind"`
	Allotted   int    `json:"allotted"`
	Used       int    `json:"used"`
	Tag        string `json:"tag"`
	ContractID *int64 `json:"contract_id"`
}

func entryDTOFrom(e ledger.Entry) entryDTO {
	return entryDTO{
		ID:         e.ID,
		UseYear:    e.UseYear,
		Date:       e.Date.Format(ledger.DateLayout),
		Desc:       e.Desc,
		Kind:       e.Kind,
		Allotted:   e.Allotted,
		Used:       e.Used,
		Tag:        e.Tag,
		ContractID: e.ContractID,
	}
}

// entryListDTO adds the running balance ListEntries derives, per row.
type entryListDTO struct {
	entryDTO
	RunningBalance int `json:"running_balance"`
}

func entryListDTOFrom(e ledger.Entry) entryListDTO {
	return entryListDTO{entryDTO: entryDTOFrom(e), RunningBalance: e.RunningBalance}
}

// entryRequestDTO is the wire shape accepted by POST/PUT entries. id comes
// from the path (PUT) or is assigned by the store (POST), never the body.
type entryRequestDTO struct {
	UseYear    int    `json:"use_year"`
	Date       string `json:"date"`
	Desc       string `json:"description"`
	Kind       string `json:"kind"`
	Allotted   int    `json:"allotted"`
	Used       int    `json:"used"`
	Tag        string `json:"tag"`
	ContractID *int64 `json:"contract_id"`
}

// toEntry validates dto and builds a ledger.Entry with the given id, mirroring
// the defaulting rules of parseEntryForm/entryFields.toEntry: date is
// required and must parse, kind defaults to ledger.KindUsage when empty and
// must otherwise be a known Kind* constant, and use_year defaults to the
// date's year when zero.
func (dto entryRequestDTO) toEntry(id int64) (ledger.Entry, error) {
	if dto.Date == "" {
		return ledger.Entry{}, fmt.Errorf("date is required")
	}
	d, err := time.Parse(ledger.DateLayout, dto.Date)
	if err != nil {
		return ledger.Entry{}, fmt.Errorf("invalid date %q: %w", dto.Date, err)
	}
	if dto.Desc == "" {
		return ledger.Entry{}, fmt.Errorf("description is required")
	}
	kind := dto.Kind
	if kind == "" {
		kind = ledger.KindUsage
	}
	if !validEntryKind(kind) {
		return ledger.Entry{}, fmt.Errorf("invalid kind %q (want one of allocation, usage, bonus, single_use, adjustment)", kind)
	}
	year := dto.UseYear
	if year == 0 {
		year = d.Year()
	}
	return ledger.Entry{
		ID:         id,
		UseYear:    year,
		Date:       d,
		Desc:       dto.Desc,
		Kind:       kind,
		Allotted:   dto.Allotted,
		Used:       dto.Used,
		Tag:        dto.Tag,
		ContractID: dto.ContractID,
	}, nil
}

func validEntryKind(k string) bool {
	switch k {
	case ledger.KindAllocation, ledger.KindUsage, ledger.KindBonus, ledger.KindSingleUse, ledger.KindAdjustment:
		return true
	default:
		return false
	}
}

// contractDTO is the wire shape of a ledger.Contract response and the
// accepted POST body (id is ignored on decode).
type contractDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Number       string `json:"number"`
	HomeResort   string `json:"home_resort"`
	AnnualPoints int    `json:"annual_points"`
	UseYearMonth string `json:"use_year_month"`
}

func contractDTOFrom(c ledger.Contract) contractDTO {
	return contractDTO{
		ID:           c.ID,
		Name:         c.Name,
		Number:       c.Number,
		HomeResort:   c.HomeResort,
		AnnualPoints: c.AnnualPoints,
		UseYearMonth: c.UseYearMonth.String(),
	}
}

// toContract validates dto, mirroring `dvc ledger contracts add`'s required
// flags: name, annual_points, and use_year_month are all required, and
// use_year_month must parse via ledger.ParseMonth.
func (dto contractDTO) toContract() (ledger.Contract, error) {
	if dto.Name == "" {
		return ledger.Contract{}, fmt.Errorf("name is required")
	}
	if dto.AnnualPoints == 0 {
		return ledger.Contract{}, fmt.Errorf("annual_points is required")
	}
	if dto.UseYearMonth == "" {
		return ledger.Contract{}, fmt.Errorf("use_year_month is required")
	}
	month, err := ledger.ParseMonth(dto.UseYearMonth)
	if err != nil {
		return ledger.Contract{}, err
	}
	return ledger.Contract{
		Name:         dto.Name,
		Number:       dto.Number,
		HomeResort:   dto.HomeResort,
		AnnualPoints: dto.AnnualPoints,
		UseYearMonth: month,
	}, nil
}

// summaryDTO is the wire shape of a ledger.UseYearSummary.
type summaryDTO struct {
	UseYear      int  `json:"use_year"`
	Allotted     int  `json:"allotted"`
	Used         int  `json:"used"`
	Net          int  `json:"net"`
	OverBorrowed bool `json:"over_borrowed"`
}

func summaryDTOFrom(s ledger.UseYearSummary) summaryDTO {
	return summaryDTO{
		UseYear:      s.UseYear,
		Allotted:     s.Allotted,
		Used:         s.Used,
		Net:          s.Net,
		OverBorrowed: s.OverBorrowed,
	}
}

// writeJSON encodes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeAPIError writes {"error": msg} with the given status code.
func writeAPIError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// apiPathID parses the {id} path value, writing a 400 JSON error and
// returning ok=false on failure.
func apiPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

// listEntries handles GET /api/v1/ledger/entries.
func (h *apiHandlers) listEntries(w http.ResponseWriter, r *http.Request) {
	entries, err := h.store.ListEntries()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]entryListDTO, len(entries))
	for i, e := range entries {
		out[i] = entryListDTOFrom(e)
	}
	writeJSON(w, http.StatusOK, out)
}

// addEntry handles POST /api/v1/ledger/entries.
func (h *apiHandlers) addEntry(w http.ResponseWriter, r *http.Request) {
	var dto entryRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	e, err := dto.toEntry(0)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.store.AddEntry(e)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	e.ID = id
	writeJSON(w, http.StatusCreated, entryDTOFrom(e))
}

// updateEntry handles PUT /api/v1/ledger/entries/{id}.
//
// NOTE: ledger.Store.UpdateEntry does not report rows affected, so an
// unknown id currently succeeds (200) rather than 404 — see the Store API
// note in the pitch's rabbit-hole list; adding that plumbing is out of
// scope here.
func (h *apiHandlers) updateEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := apiPathID(w, r)
	if !ok {
		return
	}
	var dto entryRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	e, err := dto.toEntry(id)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.UpdateEntry(e); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entryDTOFrom(e))
}

// deleteEntry handles DELETE /api/v1/ledger/entries/{id}. See updateEntry's
// note: an unknown id still returns 204, since rows-affected isn't available.
func (h *apiHandlers) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := apiPathID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteEntry(id); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listContracts handles GET /api/v1/ledger/contracts.
func (h *apiHandlers) listContracts(w http.ResponseWriter, r *http.Request) {
	contracts, err := h.store.ListContracts()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]contractDTO, len(contracts))
	for i, c := range contracts {
		out[i] = contractDTOFrom(c)
	}
	writeJSON(w, http.StatusOK, out)
}

// addContract handles POST /api/v1/ledger/contracts.
func (h *apiHandlers) addContract(w http.ResponseWriter, r *http.Request) {
	var dto contractDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	c, err := dto.toContract()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.store.AddContract(c)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c.ID = id
	writeJSON(w, http.StatusCreated, contractDTOFrom(c))
}

// deleteContract handles DELETE /api/v1/ledger/contracts/{id}. Same
// no-rows-affected caveat as deleteEntry.
func (h *apiHandlers) deleteContract(w http.ResponseWriter, r *http.Request) {
	id, ok := apiPathID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteContract(id); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// summaries handles GET /api/v1/ledger/summaries.
func (h *apiHandlers) summaries(w http.ResponseWriter, r *http.Request) {
	summaries, err := h.store.UseYearSummaries()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]summaryDTO, len(summaries))
	for i, s := range summaries {
		out[i] = summaryDTOFrom(s)
	}
	writeJSON(w, http.StatusOK, out)
}

// distribute handles POST /api/v1/ledger/distribute. It returns 200 (not
// 201) since this is an action endpoint, not a single-resource create — it
// may create zero, one, or several entries.
func (h *apiHandlers) distribute(w http.ResponseWriter, r *http.Request) {
	created, err := h.store.DistributeNextYear()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]entryDTO, len(created))
	for i, e := range created {
		out[i] = entryDTOFrom(e)
	}
	writeJSON(w, http.StatusOK, out)
}
