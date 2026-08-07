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

   No schema migration is needed after this — `internal/ledger.Store`
   applies the ledger schema idempotently on server startup.

   The `casaos:casaos` credentials are inherited from the old CasaOS host.
   If they no longer work, check the container's `POSTGRES_USER` /
   `POSTGRES_PASSWORD` and update `LEDGER_DSN` in the compose file.

2. **Networking is already settled**, but the naming is a trap worth
   knowing: the shared Postgres *container* is named `postgresql` while
   the Docker *network* it sits on is named `postgres` (singular). The
   compose file joins the `postgres` network as `external: true` and
   reaches the database at host `postgresql`. Re-check with:

   ```bash
   ssh -t percival 'sudo docker network inspect postgres --format "{{range .Containers}}{{.Name}} {{end}}"'
   ```

3. **Generate an AUTH_SECRET** and set it in dockhand (as an environment
   variable/secret for the service — it is never committed to this repo):

   ```bash
   openssl rand -base64 32
   ```

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
   GitHub package settings after the first CI publish.

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

## Releasing

```bash
./scripts/release.sh v0.1.0
```

This tags and pushes; GitHub Actions then builds and publishes
`ghcr.io/lineleader/lineleader:v0.1.0` (see
`.github/workflows/release.yml`). Once the build finishes, bump the image
tag in dockhand on percival to that version.

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
