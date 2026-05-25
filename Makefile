SHELL := /bin/sh

GO_ENV := env GOCACHE=$(CURDIR)/.cache/go-build GOPATH=$(CURDIR)/.cache/gopath
NPM := npm --prefix web --cache $(CURDIR)/.cache/npm

ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help dev api web db-up object-bucket db-status db-migrate db-reset test test-api test-web build build-api build-web fmt clean

help:
	@printf '%s\n' 'PasteBox commands:'
	@printf '%s\n' '  make dev        Start local dependencies with Docker Compose'
	@printf '%s\n' '  make api        Run the Go API'
	@printf '%s\n' '  make web        Run the Vite dev server'
	@printf '%s\n' '  make object-bucket Ensure the local MinIO pastebox bucket exists'
	@printf '%s\n' '  make db-migrate Apply local PostgreSQL migrations'
	@printf '%s\n' '  make db-reset   Reset local PostgreSQL schema and rerun migrations'
	@printf '%s\n' '  make test       Run backend and frontend checks'
	@printf '%s\n' '  make build      Build backend binary and frontend assets'

dev:
	docker compose up -d postgres redis minio clamav mailpit
	docker compose run --rm minio-init

api: dev
	$(MAKE) db-migrate
	$(GO_ENV) go run ./cmd/pastebox

web:
	$(NPM) run dev

db-up:
	docker compose up -d postgres

object-bucket:
	docker compose up -d minio
	docker compose run --rm minio-init

db-status: db-up
	$(GO_ENV) go run ./cmd/pastebox migrate status

db-migrate: db-up
	$(GO_ENV) go run ./cmd/pastebox migrate up

db-reset: db-up
	docker compose exec -T postgres psql -U pastebox -d pastebox -v ON_ERROR_STOP=1 -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;'
	$(MAKE) db-migrate

test: test-api test-web

test-api:
	$(GO_ENV) go test ./cmd/... ./internal/...

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
