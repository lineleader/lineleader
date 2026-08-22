.PHONY: dev
dev:
	# Requires a Postgres ledger DSN and an auth secret. Both are picked up
	# from the environment automatically (make passes them through to
	# `go run`). Add --insecure-cookies so the session cookie works over
	# plain http in local dev (real deployments keep Secure cookies, TLS
	# terminates at the reverse proxy).
	#
	# Start the local dev database with: docker compose up -d
	# Then run:
	#   LEDGER_DSN=postgres://postgres:dev@localhost:5432/lineleader?sslmode=disable \
	#   AUTH_SECRET=devsecret \
	#   make dev
	go run cmd/server/main.go --insecure-cookies

.PHONY: all
all: dev

.PHONY: build
build:
	go build -o bin/server cmd/server/main.go

.PHONY: dvc
dvc:
	go build -o bin/dvc ./cmd/dvc

.PHONY: ledger-migrate
ledger-migrate:
	go build -o bin/ledger-migrate ./cmd/ledger-migrate

# One-shot copy of the legacy SQLite ledger into Postgres. Target DSN must
# be empty (no rows in contracts or entries) — this is a migration, not a
# sync. Override SQLITE_PATH/LEDGER_DSN to point elsewhere.
SQLITE_PATH := $(HOME)/.config/lineleader/ledger.db

.PHONY: migrate-ledger
migrate-ledger: ledger-migrate
	./bin/ledger-migrate --sqlite $(SQLITE_PATH) --dsn "$(LEDGER_DSN)"

.PHONY: import
import: dvc
	./bin/dvc import ~/Documents/DVC/point-charts/2026/VGF-2026.pdf
	./bin/dvc import ~/Documents/DVC/point-charts/2027/2027_VGF.pdf

.PHONY: test
test:
	go test -v ./...

# TEST_DB_DSN must match the container's exposed port/credentials below.
TEST_DB_DSN := postgres://postgres:test@localhost:5433/lineleader_test?sslmode=disable

.PHONY: test-db
test-db:
	docker run --rm -d --name lineleader-test-pg -p 5433:5432 \
		-e POSTGRES_PASSWORD=test -e POSTGRES_DB=lineleader_test postgres:17-alpine
	@echo "==> throwaway Postgres started; before running tests:"
	@echo "    export LEDGER_TEST_DSN=$(TEST_DB_DSN)"

.PHONY: test-db-stop
test-db-stop:
	docker stop lineleader-test-pg

.PHONY: seed-test
seed-test: dvc build
	./scripts/seed-ledger_test.sh

IMAGE := lineleader:local

.PHONY: docker-build
docker-build:
	docker build -t $(IMAGE) .

# Local smoke test only: publishes 8080 on the host and relies on
# LEDGER_DSN/AUTH_SECRET already being set in the environment. Real
# deployments use deploy/percival/docker-compose.yml (publishes 8080 for the
# reverse proxy to reach).
.PHONY: docker-run
docker-run: docker-build
	docker run --rm -p 8080:8080 \
		-e LEDGER_DSN="$(LEDGER_DSN)" -e AUTH_SECRET="$(AUTH_SECRET)" \
		$(IMAGE)

.PHONY: clean
clean:
	rm -rf bin/*

.PHONY: tui
tui: dvc
	./bin/dvc tui
