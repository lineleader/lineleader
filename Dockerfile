# syntax=docker/dockerfile:1

# --- build stage -------------------------------------------------------
# Matches go.mod's `go 1.26` directive.
FROM golang:1.26 AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Pure-Go deps only (pgx, bubbletea/lipgloss) — CGO_ENABLED=0 gives a fully
# static binary, so the final stage needs no libc.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# --- final stage ---------------------------------------------------------
# distroless/static: no shell, no package manager, nothing but libc-free
# binaries and CA certs (needed for TLS to the reverse-proxy-fronted
# Postgres, if that connection ever runs over TLS). The "nonroot" variant
# already runs as an unprivileged, non-root user (65532:65532) — matches
# the "non-root user" requirement with no extra RUN useradd steps. tzdata
# isn't included and isn't needed: the app only formats UTC dates.
FROM gcr.io/distroless/static-debian12:nonroot

# Chart JSON is baked into the image per the pitch: new charts mean
# reimport locally, commit, rebuild — not a runtime fetch.
COPY --from=build /out/server /app/server
COPY --chown=nonroot:nonroot data/point-charts /app/data/point-charts

WORKDIR /app
USER nonroot:nonroot

EXPOSE 8080

# --config/--plans point into /state, a volume mounted by docker-compose.
# --data-dir points at the charts baked into the image above.
ENTRYPOINT ["/app/server", "--data-dir", "/app/data/point-charts", "--config", "/state/config.json", "--plans", "/state/plans.json"]
