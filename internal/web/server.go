package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

// healthzHandler reports liveness for reverse-proxy health checks. It pings
// the ledger database when one is configured.
//
// NOTE: intentionally left unauthenticated — a future auth task must exempt
// /healthz from any auth middleware it adds.
func healthzHandler(store *ledger.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store != nil {
			if err := store.Ping(r.Context()); err != nil {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
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
	Plans      []dvc.Plan
	PlansPath  string
	Defaults   Defaults
	Ledger     *ledger.Store // optional; when set, the /ledger pages are served

	// AuthSecret gates the whole mux behind the single-secret auth scheme
	// from docs/pitches/hosted-lineleader.md when non-empty. Empty means no
	// auth at all — the zero value existing tests and local dev rely on.
	AuthSecret string
	// SecureCookies sets the Secure attribute on the session cookie. Leave
	// false for plain-http local dev; true in any real deployment (TLS is
	// terminated at the reverse proxy per the pitch).
	SecureCookies bool
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
	mux.HandleFunc("GET /healthz", healthzHandler(opts.Ledger))
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
	}

	if opts.AuthSecret == "" {
		return mux
	}
	ah := &authHandlers{tmpl: tmpl, secret: opts.AuthSecret, secureCookies: opts.SecureCookies}
	mux.HandleFunc("GET /login", ah.loginPage)
	mux.HandleFunc("POST /login", ah.loginSubmit)
	mux.HandleFunc("POST /logout", ah.logout)
	return authMiddleware(opts.AuthSecret)(mux)
}
