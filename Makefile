.PHONY: dev
dev:
	go run cmd/server/main.go

.PHONY: all
all: dev

.PHONY: build
build:
	go build -o bin/server cmd/server/main.go

.PHONY: dvc
dvc:
	go build -o bin/dvc ./cmd/dvc

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
seed-test: dvc
	./scripts/seed-ledger_test.sh

.PHONY: clean
clean:
	rm -rf bin/*

.PHONY: tui
tui: dvc
	./bin/dvc tui
