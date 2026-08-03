BINARY  := sdb
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build run test lint tidy clean web-dev web-build docker-build

all: build

# CGO off : driver SQLite pur Go (modernc) → binaire statique
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/sdb

run:
	go run ./cmd/sdb

test:
	go test -race -cover ./...

lint:
	go vet ./...
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -rf bin coverage.out web/dist

web-dev:
	cd web && npm run dev

web-build:
	cd web && npm install && npm run build

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t sdb:latest .

.PHONY: test-integration
## Tests d'integration contre un VRAI restic et un vrai depot (Docker requis).
## Exclus du `go test ./...` par le tag de build.
test-integration:
	go test -tags=integration -timeout 15m ./internal/infra/restic/...

.PHONY: vulncheck
## Scan de vulnerabilites avec exceptions declarees dans .govulncheck-allow.
vulncheck:
	bash scripts/vulncheck.sh
