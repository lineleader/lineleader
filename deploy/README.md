# Homelab rollout runbook

Deploys the lineleader server to `percival` (Tailscale IP
`100.119.145.113`), managed there by dockhand from
[`deploy/percival/docker-compose.yml`](percival/docker-compose.yml), fronted
by Caddy on the `traefik` tailnet node
([`deploy/Caddyfile.snippet`](Caddyfile.snippet)) at `lineleader.dev`.

## One-time setup

1. **Create the `lineleader` database** on the shared Postgres container
   already running on percival. The exact container name needs confirming
   (`docker ps` on percival); assuming it's called `postgresql`:

   ```bash
   docker exec postgresql psql -U casaos -c 'CREATE DATABASE lineleader;'
   ```

   No schema migration is needed after this — `internal/ledger.Store`
   applies the ledger schema idempotently on server startup.

2. **Confirm the Postgres Docker network name.** `deploy/percival/docker-compose.yml`
   assumes it's called `postgresql` (carried over from the prior CasaOS
   setup) and declares it as an `external: true` network. Verify with:

   ```bash
   docker network ls
   docker ps
   ```

   and correct the compose file if the name differs.

3. **Generate an AUTH_SECRET** and set it in dockhand (as an environment
   variable/secret for the service — it is never committed to this repo):

   ```bash
   openssl rand -base64 32
   ```

4. **Add the Caddy block** from `deploy/Caddyfile.snippet` to the Caddy
   config on the `traefik` node, then reload Caddy.

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
export LINELEADER_SERVER=https://lineleader.dev
export LINELEADER_TOKEN=<the AUTH_SECRET>
```

or a config file at `~/.config/lineleader/client.json`:

```json
{
  "server_url": "https://lineleader.dev",
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
curl https://lineleader.dev/healthz
```

should return `ok`.
