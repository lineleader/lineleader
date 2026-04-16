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

.PHONY: clean
clean:
	rm -rf bin/*

.PHONY: tui
tui: dvc
	./bin/dvc tui
