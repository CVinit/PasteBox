SHELL := /bin/sh

GO_ENV := env GOCACHE=$(CURDIR)/.cache/go-build GOPATH=$(CURDIR)/.cache/gopath
NPM := npm --prefix web --cache $(CURDIR)/.cache/npm

.PHONY: help dev api web test test-api test-web build build-api build-web fmt clean

help:
	@printf '%s\n' 'PasteBox commands:'
	@printf '%s\n' '  make dev        Start local dependencies with Docker Compose'
	@printf '%s\n' '  make api        Run the Go API'
	@printf '%s\n' '  make web        Run the Vite dev server'
	@printf '%s\n' '  make test       Run backend and frontend checks'
	@printf '%s\n' '  make build      Build backend binary and frontend assets'

dev:
	docker compose up -d postgres redis minio clamav mailpit

api:
	$(GO_ENV) go run ./cmd/pastebox

web:
	$(NPM) run dev

test: test-api test-web

test-api:
	$(GO_ENV) go test ./...

test-web:
	$(NPM) run typecheck
	$(NPM) run build

build: build-api build-web

build-api:
	$(GO_ENV) go build -o bin/pastebox ./cmd/pastebox

build-web:
	$(NPM) run build

fmt:
	gofmt -w cmd internal
	$(NPM) run format

clean:
	rm -rf bin web/dist
