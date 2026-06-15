package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Options configures NewServer.
type Options struct {
	Charts     []*dvc.ResortChart
	Config     dvc.Config
	ConfigPath string
	Plans      []dvc.Plan
	PlansPath  string
	Defaults   Defaults
	Ledger     *ledger.Store // optional; when set, the /ledger pages are served
}

// Defaults are the initial trip-0 input values.
type Defaults struct {
	From, To, Budget, MinNights string
}

// NewServer builds an http.Handler that serves the web UI.
func NewServer(opts Options) http.Handler {
	tmpl := template.Must(template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, "templates/*.html"))
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	h := &handlers{
		tmpl:    tmpl,
		session: NewSession(opts.Charts, opts.Config, opts.ConfigPath, opts.Plans, opts.PlansPath, opts.Defaults),
	}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("GET /", h.index)
	mux.HandleFunc("POST /budget", h.updateBudget)
	mux.HandleFunc("POST /trips", h.addTrip)
	mux.HandleFunc("DELETE /trips/{i}", h.removeTrip)
	mux.HandleFunc("POST /trips/{i}/field", h.updateField)
	mux.HandleFunc("POST /trips/{i}/select/{row}", h.toggleSelection)
	mux.HandleFunc("POST /trips/{i}/collapse", h.toggleCollapsed)
	mux.HandleFunc("GET /filters", h.openFilters)
	mux.HandleFunc("POST /filters/resorts/{code}", h.toggleResortFilter)
	mux.HandleFunc("POST /filters/roomtypes/{name}", h.toggleRoomTypeFilter)
	mux.HandleFunc("GET /trips/{i}/filters", h.openTripFilters)
	mux.HandleFunc("POST /trips/{i}/filters/mode", h.setTripFilterMode)
	mux.HandleFunc("POST /trips/{i}/filters/resorts/{code}", h.toggleTripResort)
	mux.HandleFunc("POST /trips/{i}/filters/roomtypes/{name}", h.toggleTripRoomType)
	mux.HandleFunc("DELETE /trips/{i}/filters", h.resetTripFilters)
	mux.HandleFunc("GET /plans", h.openPlans)
	mux.HandleFunc("POST /plans", h.savePlan)
	mux.HandleFunc("POST /plans/{name}/load", h.loadPlan)
	mux.HandleFunc("POST /plans/{name}/update", h.updatePlan)
	mux.HandleFunc("DELETE /plans/{name}", h.deletePlan)
	mux.HandleFunc("GET /panel/close", h.closePanel)

	if opts.Ledger != nil {
		lh := &ledgerHandlers{tmpl: tmpl, store: opts.Ledger}
		mux.HandleFunc("GET /ledger", lh.page)
		mux.HandleFunc("POST /ledger/entries", lh.addEntry)
		mux.HandleFunc("GET /ledger/entries/{id}/edit", lh.editEntry)
		mux.HandleFunc("POST /ledger/entries/{id}/update", lh.updateEntry)
		mux.HandleFunc("POST /ledger/entries/edit/cancel", lh.cancelEdit)
		mux.HandleFunc("DELETE /ledger/entries/{id}", lh.deleteEntry)
		mux.HandleFunc("POST /ledger/contracts", lh.addContract)
		mux.HandleFunc("DELETE /ledger/contracts/{id}", lh.deleteContract)
		mux.HandleFunc("POST /ledger/distribute", lh.distribute)
	}
	return mux
}
