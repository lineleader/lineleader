# Homelab rollout runbook

Deploys the lineleader server to `percival` (Tailscale IP
`100.119.145.113`), managed there by dockhand from
[`deploy/percival/docker-compose.yml`](percival/docker-compose.yml), fronted
by Caddy on the `traefik` tailnet node
([`deploy/Caddyfile.snippet`](Caddyfile.snippet)) at `lineleader.io`.

## One-time setup

1. **Create the `lineleader` database** on the shared Postgres container
   (`postgresql`, running `postgres:15.17` on percival):

   ```bash
   ssh -t percival "sudo docker exec postgresql psql -U casaos -c 'CREATE DATABASE lineleader;'"
   ```

   No separate operator step is needed after this — `internal/ledger.Store`
   applies any outstanding goose migrations (`internal/ledger/migrations/`)
   automatically on server startup, tracked in a `goose_db_version` table so
   each migration runs at most once.

   The `casaos:casaos` credentials are inherited from the old CasaOS host
   and are confirmed working against this instance.

   This Postgres reports a collation version mismatch (its data directory
   was initialized under glibc 2.36; the host now provides 2.41). If the
   new `lineleader` database reports it too, clear it immediately — this
   is safe and instant while the database is still empty, and avoids
   building any index under stale collation rules:

   ```bash
   ssh -t percival "sudo docker exec postgresql psql -U casaos -d lineleader \
       -c 'ALTER DATABASE lineleader REFRESH COLLATION VERSION;'"
   ```

   The pre-existing databases on this instance (miniflux and others) are a
   separate concern: they hold data indexed under the old rules and need
   `REINDEX DATABASE` before their collation version is refreshed.

2. **Networking is already settled**, but the naming is a trap worth
   knowing: the shared Postgres *container* is named `postgresql` while
   the Docker *network* it sits on is named `postgres` (singular). The
   compose file joins the `postgres` network as `external: true` and
   reaches the database at host `postgresql`. Re-check with:

   ```bash
   ssh -t percival 'sudo docker network inspect postgres --format "{{range .Containers}}{{.Name}} {{end}}"'
   ```

3. **Generate an AUTH_SECRET** and set it in dockhand as a stack variable
   for the `lineleader` stack (encrypted in dockhand's DB) — it is never
   committed to this repo:

   ```bash
   openssl rand -base64 32
   ```

   Stack variables sit above the repo's env file in dockhand's precedence
   order (repo `.env` file → dockhand stack variables → deploy-time env),
   so this is the right place for anything that must not be public. The
   flip side of that precedence is the gotcha called out in "Releasing"
   below: never also set `LINELEADER_VERSION` here.

4. **Point DNS at the Caddy node.** The `traefik` tailnet node is a Google
   Cloud VM with the public IP `34.29.51.143` (it already serves
   `rss.c18l.com` and `mrshll.us`). Add an A record in Cloudflare:

   ```
   lineleader.io.  A  34.29.51.143   ; DNS only — grey cloud, not proxied
   ```

   Keep it DNS-only, matching `rss.c18l.com`; Caddy's ACME challenge needs
   to reach the origin directly.

5. **Add the Caddy block** from `deploy/Caddyfile.snippet` to the Caddy
   config on the `traefik` node, then reload Caddy.

6. **Make sure percival can pull from GHCR.** Packages under the
   `lineleader` org are private — `ghcr.io/lineleader/wall-e` returns 403
   to an anonymous pull — so percival (or dockhand) needs registry
   credentials. An existing org-scoped PAT login covers a new package in
   the same org; otherwise either `docker login ghcr.io` on percival with a
   `read:packages` PAT, or mark the `lineleader` package public in its
   GitHub package settings after the first CI publish. Note this is about
   the *image* registry, not the git repo below — GHCR packages stay
   private even though the source repo is public.

7. **Create the git stack in dockhand.** Unlike the private image
   registry above, the `github.com/lineleader/lineleader` source repo is
   public, so dockhand needs no git credentials for this step
   (`authType: none`):

   - Git → Repositories → Add Repository, pointing at
     `github.com/lineleader/lineleader` (public, no auth).
   - Create a stack from that repository:
     - stack name: `lineleader` (exactly — see the compose file's `name:`
       pin, which this must match for the volume to line up)
     - composePath: `deploy/percival/docker-compose.yml`
     - envFilePath: `deploy/percival/lineleader.env`
     - autoUpdate: on
     - autoUpdateCron: `*/15 * * * *`

   With autoUpdate on, dockhand polls the repo on that cron and redeploys
   whenever `deploy/percival/lineleader.env` or the compose file changes
   on `main` — a release becomes "commit a version bump and push," no
   separate dockhand-side action needed.

## Migrating the existing SQLite ledger

One-shot, run from your workstation (not on percival), against an *empty*
target database:

```bash
go run ./cmd/ledger-migrate \
    --sqlite ~/.config/lineleader/ledger.db \
    --dsn "postgres://casaos:casaos@100.119.145.113:5432/lineleader?sslmode=disable"
```

This uses percival's Tailscale IP directly, rather than the `postgresql`
container hostname, because the migration runs from outside Docker on your
workstation, not from inside the compose network.

`ledger-migrate` refuses to run against a Postgres database that already
has ledger rows — it's a one-shot migration, not a sync. The old
`ledger.db` is left on disk untouched, so it doubles as the rollback path
if the cutover needs to be undone.

## Cost data

Dues rates (the global `dues_rates` table) seed once on first boot, guarded
at the table level — `internal/ledger/seed.sql` only inserts when the table
is empty, so a year deleted through the Contracts view stays deleted across
restarts; it is never resurrected.

Contract purchase price, closing costs and term years are **not** seeded —
they're user data and belong on the Contracts view. As an escape hatch for
backfilling them without going through the UI, e.g. right after a fresh
deploy or a `ledger-migrate` run:

```sql
UPDATE contracts SET term_years=44, purchase_price_cents=2940000, closing_costs_cents=58835 WHERE name='Point allocation';
UPDATE contracts SET term_years=41, purchase_price_cents=3015000, closing_costs_cents=66500  WHERE name='Point allocation #2';
```

These match on `name`, so they're only safe when contract names are unique.
The UI's inline contract edit (which matches on id) is the reliable path —
reach for `psql` only when the UI isn't an option.

## Releasing

```bash
./scripts/release.sh v0.1.0
```

That's the whole release: it tags and pushes, which triggers
`.github/workflows/release.yml`. CI builds and publishes
`ghcr.io/lineleader/lineleader:v0.1.0`, then — for a real tag push, not a
`workflow_dispatch` run — commits the version bump to
`deploy/percival/lineleader.env` on `main` itself (via
`scripts/bump-deployed-version.sh`), as `github-actions[bot]`. There is no
second manual step.

dockhand polls the `lineleader` git stack on a cron (`*/15 * * * *`, see
"One-time setup" above) and redeploys automatically once it sees CI's
version-bump commit on `main` — no dockhand-side action needed. There's no
synchronous "redeploy now" step from here; the deploy lands on dockhand's
next poll, within ~15 minutes. Confirm with `curl
https://lineleader.io/healthz` (see "Verifying" below) or by checking the
stack's deployed image in dockhand.

A manual bump is still possible if you need to point dockhand at a version
without cutting a new tag (e.g. rolling back to an already-published
image):

```bash
./scripts/bump-deployed-version.sh v0.1.0
git commit -am "chore(deploy): release v0.1.0"
git push
```

**Gotcha:** dockhand's variable precedence is repo `.env` file → dockhand
stack variables → deploy-time env, so a stack variable *overrides* the
repo file. If `LINELEADER_VERSION` is ever also set as a dockhand stack
variable for the `lineleader` stack, that value wins and the git-push
flow above will appear to do nothing — the deployed version won't budge
no matter what's committed. If a release doesn't seem to take, check the
stack's variables in dockhand first and remove `LINELEADER_VERSION` from
there if present; it should only ever live in
`deploy/percival/lineleader.env`.

The compose file interpolates `LINELEADER_VERSION` into the image tag
rather than pinning it. Both it and `AUTH_SECRET` use the required
`${VAR:?...}` form — an unset variable fails the deploy loudly instead of
resolving to an empty string.

Image tags keep their leading `v` (`v0.1.0`, not `0.1.0`). The workflow
uses metadata-action's `{{raw}}` for this; `{{version}}` would strip the
prefix and produce a tag the compose file can't find.

`scripts/rollout.sh` (bearer-token dockhand API auth, direct SSH env-file
edit, synchronous redeploy) is legacy and does not work against the
current dockhand instance — see its header comment and "One-time setup"
above for why the git-stack flow replaced it.

## CLI clients

Point the `dvc` CLI (and any other lineleader CLI client) at the hosted
server with either environment variables:

```bash
export LINELEADER_SERVER=https://lineleader.io
export LINELEADER_TOKEN=<the AUTH_SECRET>
```

or a config file at `~/.config/lineleader/client.json`:

```json
{
  "server_url": "https://lineleader.io",
  "token": "<the AUTH_SECRET>"
}
```

Resolution order (highest precedence first), per
`internal/ledgerclient/config.go`: `--server`/`--token` CLI flags, then
`LINELEADER_SERVER`/`LINELEADER_TOKEN` env vars, then
`~/.config/lineleader/client.json`. Each of the server URL and token is
resolved independently, and a missing or malformed config file is not an
error — it just falls through to whatever's already resolved.

## Verifying

```bash
curl https://lineleader.io/healthz
```

should return `ok`.
