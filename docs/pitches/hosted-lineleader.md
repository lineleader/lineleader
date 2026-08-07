# Pitch: LineLeader, hosted

## Problem

LineLeader's best interface is trapped on one machine. The web UI (`cmd/server`) already does everything — multi-trip planning against the point charts, budget splitting, filters, saved plans, and the full points ledger — but it binds to `localhost:8080` with no authentication, no TLS, and no deployment story. The ledger database, saved plans, and filter config live in `~/.config/lineleader/` on a single laptop.

The practical consequence: standing in a park or on the couch with a phone, there is no way to check the points balance, log a booking, or sketch a trip. Everything waits until you're back at the machine that has the files.

## Appetite

**Small batch: 2 weeks.** This is a repackaging of working software, not new product surface. Anything that threatens the two weeks gets cut before the deploy story or the auth gate does.

## Solution

Flip the topology: the server becomes the home of all ledger/plan state, running on home-lab hardware behind the existing reverse proxy; phone and laptop browsers use the web UI; the `dvc` CLI keeps its exact command surface but executes ledger commands against the server's API instead of a local database file.

### 1. Lock the front door (auth)

Single-user. One shared secret provisioned to the server (env var / mounted file).

- **Browser:** a `/login` page sets a signed session cookie (`HttpOnly`, `Secure`, `SameSite=Strict`). All existing routes go behind an auth middleware wrapping the mux — the first middleware this codebase will have.
- **CLI / API:** `Authorization: Bearer <token>` checked by the same middleware (constant-time compare).
- Mutating routes additionally check `Origin`/`Sec-Fetch-Site` — that plus `SameSite=Strict` is CSRF coverage enough for one user.

The existing "one global `Session` per process" design (`internal/web/session.go`) is **kept, deliberately**: with a single user it means phone and laptop share one live planner — closing a filter panel on the laptop and seeing it on the phone is a feature, not a bug.

### 2. Hosting-grade server plumbing

- Replace bare `http.ListenAndServe` (`cmd/server/main.go`) with an `http.Server` carrying read/write timeouts and graceful shutdown on SIGTERM.
- **Ledger moves to the existing home-lab Postgres** (decided at the betting table — it's already running and backed up). The `ledger.Store` API stays identical; underneath, swap `modernc.org/sqlite` for the pgx stdlib driver, port `schema.sql` to Postgres dialect (two tables — `AUTOINCREMENT` → identity column, drop the `PRAGMA`), rewrite `?` placeholders to `$n`, and replace `LastInsertId()` with `RETURNING id`. Connection comes from a `--ledger-dsn` flag / `LEDGER_DSN` env replacing today's `--ledger` file path. This deletes the SQLite-under-concurrency problem (no WAL/busy-timeout work) and the DB-volume backup story.
- **One-shot migration** of the existing `~/.config/lineleader/ledger.db` into Postgres — a small script or hidden subcommand that copies both tables, verified by diffing `dvc ledger show` output before and after.
- A `/healthz` endpoint for the reverse proxy.

### 3. JSON API for the ledger

A small `/api/v1/` surface mapping 1:1 onto the existing `ledger.Store` methods the CLI already calls:

```
GET    /api/v1/ledger/entries          → ListEntries (with running balance)
POST   /api/v1/ledger/entries          → AddEntry
PUT    /api/v1/ledger/entries/{id}     → UpdateEntry
DELETE /api/v1/ledger/entries/{id}     → DeleteEntry
GET    /api/v1/ledger/contracts        → ListContracts
POST   /api/v1/ledger/contracts        → AddContract
DELETE /api/v1/ledger/contracts/{id}   → DeleteContract
GET    /api/v1/ledger/summaries        → UseYearSummaries
POST   /api/v1/ledger/distribute       → DistributeNextYear
```

No new domain logic — handlers marshal `ledger.Entry`/`Contract`/`UseYearSummary` structs that already exist. The date/month parsing currently duplicated between `cmd/dvc/ledger.go` and `internal/web/ledger_handlers.go` moves to one shared spot as part of this.

### 4. CLI becomes a thin client (ledger only)

`dvc ledger show|add|edit|delete|contracts|distribute` keep their flags and output byte-for-byte, but issue HTTP calls instead of opening a database. Server URL + token come from `~/.config/lineleader/client.json` (or env vars). The `--db` flag goes away, and the CLI binary carries no database driver at all — only the server links pgx.

`dvc import`, `dvc search`, and `dvc tui` stay exactly as they are: local, operating on the repo-committed chart JSON. Trip planning away from the keyboard is the web UI's job.

Side benefit: `scripts/seed-ledger.sh` drives the ledger through `bin/dvc ledger add`, so it can seed the *hosted* ledger from any machine with `gcloud` creds once the CLI speaks HTTP (its direct-SQLite entry-count safety check is replaced by an API call or dropped).

### 5. Ship it

- Multi-stage Dockerfile — trivially static (pure-Go deps, no CGO). Chart JSON (`data/point-charts/`) baked into the image; new charts = reimport locally, commit, rebuild.
- `docker-compose.yml` with one small volume for the remaining file state (`config.json`, `plans.json`) via the existing `--config/--plans` flags; ledger state lives in the existing Postgres, which already has a backup story. Fixes the current "must run from repo root" trap (`data/point-charts` is a relative default).
- TLS, public hostname, and rate limiting belong to the existing reverse proxy, not the app.

### 6. Phone-sized paint (smallest slice)

The CSS is currently tuned "for TV legibility." One narrow-viewport pass over `static/style.css` (trip cards and the ledger table stack/scroll on small screens), plus a nav link from `/` to `/ledger` (today only the reverse link exists). Cosmetic only — no template restructuring.

## Rabbit holes

- **TUI over the API.** Porting `dvc tui` to a remote planner drags the whole `Planner` state model into the API. Not this cycle — the web UI is the remote planner.
- **Real user accounts.** No users table, no bcrypt, no password reset. One secret, one user. If family access ever matters, that's its own pitch.
- **Dual-database support.** SQLite goes away entirely; do not keep both dialects working behind an abstraction. One driver, one dialect.
- **Migration frameworks.** Keep the existing pattern — one embedded, idempotent `schema.sql` (`CREATE TABLE IF NOT EXISTS`) applied on open, just in Postgres dialect. No goose/atlas/tern.
- **Test-infra gold-plating.** Ledger tests currently run hermetically against SQLite files; after the port they need a real Postgres. Cheapest thing that works: tests read a `LEDGER_TEST_DSN` env var (each test in its own schema or a truncate-per-test), and a `make test-db` target runs a throwaway `docker run postgres`. Skip when the env var is absent. No testcontainers dependency.
- **Ledger scale work.** `ListEntries` reads the whole table and derives the balance in Go. At personal-ledger scale that's fine forever. No pagination, no caching.
- **Plans/config concurrency.** `config.json`/`plans.json` are whole-file rewrites serialized by the planner mutex. Single process, single user — leave them as JSON files; do not migrate them into the database this cycle.
- **API design perfectionism.** The API exists to serve one CLI. No OpenAPI spec, no versioning ceremony beyond the `/v1` prefix, no hypermedia.

## No-gos

- Multi-user / tenancy (no `user_id` columns, no per-user sessions)
- Native mobile app or offline PWA
- Chart import via the web (needs `pdftotext`; stays a local maintainer task)
- Observability stack (request logging at the proxy is enough; app keeps `log.Printf`)
- Reworking `seed-ledger.sh` beyond what falls out of the CLI change

## Risks

- **Public exposure of a home lab.** Mitigated by: auth on every route including static assets' siblings, `Secure` cookies, proxy-level TLS + rate limiting. Worst-case blast radius is one person's vacation ledger.
- **Auth subtleties with htmx.** Expired-session responses to htmx requests must trigger a full-page redirect to `/login` (`HX-Redirect` header), not swap a login form into a fragment. Known, budgeted, small.
- **SQLite → Postgres data migration.** Small dataset, two tables, but foreign keys (`entries.contract_id`) must survive with the same IDs. Verified by diffing `dvc ledger show` output against the old binary before cutover; the old `ledger.db` stays on disk as the rollback.

---

## Appendix: key files

- `cmd/server/main.go` — flags, startup, bare `ListenAndServe` to replace
- `internal/web/server.go` — mux (Go 1.22 patterns), embedded templates/static; where middleware wraps
- `internal/web/session.go` — the single global session (kept)
- `internal/ledger/store.go`, `entries.go`, `distribute.go`, `schema.sql` — the API's entire backend; queries get the Postgres-dialect pass here
- `cmd/dvc/ledger.go` — CLI subcommands to re-point at HTTP (already returns errors / writes to injected `io.Writer`, so it's testable)
- `internal/web/ledger_handlers.go` + `cmd/dvc/ledger.go` — duplicated `ledgerDateLayout`/month parsing to unify
- `internal/web/static/style.css` — mobile pass target
