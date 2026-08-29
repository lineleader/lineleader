package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

// healthzHandler reports liveness for reverse-proxy health checks by pinging
// the ledger database — always configured, since Options.Ledger is required.
//
// NOTE: intentionally left unauthenticated — a future auth task must exempt
// /healthz from any auth middleware it adds.
func healthzHandler(store *ledger.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.Ping(r.Context()); err != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Options configures NewServer.
type Options struct {
	Charts     []*dvc.ResortChart
	Config     dvc.Config
	ConfigPath string
	Ledger     *ledger.Store // required; see NewServer

	// AuthSecret gates the whole mux behind the single-secret auth scheme
	// from docs/pitches/hosted-lineleader.md when non-empty. Empty means no
	// auth at all — the zero value existing tests and local dev rely on.
	AuthSecret string
	// SecureCookies sets the Secure attribute on the session cookie. Leave
	// false for plain-http local dev; true in any real deployment (TLS is
	// terminated at the reverse proxy per the pitch).
	SecureCookies bool
}

// NewServer builds an http.Handler that serves the web UI. Options.Ledger is
// required: trips and stays live in Postgres now, so there is no meaningful
// server without one.
func NewServer(opts Options) http.Handler {
	if opts.Ledger == nil {
		panic("web.NewServer: Options.Ledger is required")
	}
	tmpl := template.Must(template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, "templates/*.html"))
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	// costs bridges the trip handlers to the ledger's cost model (see
	// costs.go); the invariant that survives from the planner era is that
	// internal/dvc still never imports internal/ledger.
	costs := newCostProvider(opts.Ledger)
	h := &handlers{
		tmpl:   tmpl,
		charts: opts.Charts,
		global: newGlobalFilters(opts.Config, opts.ConfigPath),
		store:  opts.Ledger,
		costs:  costs,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler(opts.Ledger))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("GET /", h.tripList)
	mux.HandleFunc("GET /trips/new", h.newTripForm)
	mux.HandleFunc("POST /trips", h.createTrip)
	mux.HandleFunc("GET /trips/{id}", h.tripPage)
	mux.HandleFunc("POST /trips/{id}", h.updateTrip)
	mux.HandleFunc("DELETE /trips/{id}", h.deleteTrip)
	mux.HandleFunc("POST /trips/{id}/stays/{row}", h.addStay)
	mux.HandleFunc("DELETE /trips/{id}/stays/{sid}", h.removeStay)
	mux.HandleFunc("GET /filters", h.openFilters)
	mux.HandleFunc("POST /filters/resorts/{code}", h.toggleResortFilter)
	mux.HandleFunc("POST /filters/roomtypes/{name}", h.toggleRoomTypeFilter)
	mux.HandleFunc("GET /trips/{id}/filters", h.openTripFilters)
	mux.HandleFunc("POST /trips/{id}/filters/mode", h.setTripFilterMode)
	mux.HandleFunc("POST /trips/{id}/filters/resorts/{code}", h.toggleTripResortFilter)
	mux.HandleFunc("POST /trips/{id}/filters/roomtypes/{name}", h.toggleTripRoomTypeFilter)
	mux.HandleFunc("DELETE /trips/{id}/filters", h.resetTripFilters)
	mux.HandleFunc("GET /panel/close", h.closePanel)

	lh := &ledgerHandlers{tmpl: tmpl, store: opts.Ledger, costs: costs}
	mux.HandleFunc("GET /ledger", lh.page)
	mux.HandleFunc("GET /ledger/history", lh.history)
	mux.HandleFunc("GET /ledger/contracts", lh.contracts)
	mux.HandleFunc("POST /ledger/entries", lh.addEntry)
	mux.HandleFunc("GET /ledger/entries/{id}/edit", lh.editEntry)
	mux.HandleFunc("POST /ledger/entries/{id}/update", lh.updateEntry)
	mux.HandleFunc("POST /ledger/entries/edit/cancel", lh.cancelEdit)
	mux.HandleFunc("DELETE /ledger/entries/{id}", lh.deleteEntry)
	mux.HandleFunc("POST /ledger/contracts", lh.addContract)
	mux.HandleFunc("GET /ledger/contracts/{id}/edit", lh.editContract)
	mux.HandleFunc("POST /ledger/contracts/{id}/update", lh.updateContract)
	mux.HandleFunc("POST /ledger/contracts/edit/cancel", lh.cancelContractEdit)
	mux.HandleFunc("DELETE /ledger/contracts/{id}", lh.deleteContract)
	mux.HandleFunc("POST /ledger/dues", lh.upsertDues)
	mux.HandleFunc("DELETE /ledger/dues/{year}", lh.deleteDues)
	mux.HandleFunc("POST /ledger/distribute", lh.distribute)

	// /api/v1/ledger/* — the JSON surface for the dvc CLI (and any other
	// API client). See docs/pitches/hosted-lineleader.md, "3. JSON API
	// for the ledger": one CLI to serve, so no OpenAPI, no hypermedia,
	// /v1 is the only versioning.
	apih := &apiHandlers{store: opts.Ledger}
	mux.HandleFunc("GET /api/v1/ledger/entries", apih.listEntries)
	mux.HandleFunc("POST /api/v1/ledger/entries", apih.addEntry)
	mux.HandleFunc("PUT /api/v1/ledger/entries/{id}", apih.updateEntry)
	mux.HandleFunc("DELETE /api/v1/ledger/entries/{id}", apih.deleteEntry)
	mux.HandleFunc("GET /api/v1/ledger/contracts", apih.listContracts)
	mux.HandleFunc("POST /api/v1/ledger/contracts", apih.addContract)
	mux.HandleFunc("DELETE /api/v1/ledger/contracts/{id}", apih.deleteContract)
	mux.HandleFunc("GET /api/v1/ledger/summaries", apih.summaries)
	mux.HandleFunc("POST /api/v1/ledger/distribute", apih.distribute)

	if opts.AuthSecret == "" {
		return mux
	}
	ah := &authHandlers{tmpl: tmpl, secret: opts.AuthSecret, secureCookies: opts.SecureCookies}
	mux.HandleFunc("GET /login", ah.loginPage)
	mux.HandleFunc("POST /login", ah.loginSubmit)
	mux.HandleFunc("POST /logout", ah.logout)
	return authMiddleware(opts.AuthSecret)(mux)
}
