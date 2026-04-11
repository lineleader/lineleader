.PHONY: dev
dev:
	go run cmd/server/main.go

.PHONY: all
all: dev

.PHONY: build
build:
	go build -o bin/server cmd/server/main.go

.PHONY: test
test:
	go test -v ./...

.PHONY: clean
clean:
	rm -rf bin/*
