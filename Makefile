SHELL := /bin/sh

GO_ENV := env GOCACHE=$(CURDIR)/.cache/go-build GOPATH=$(CURDIR)/.cache/gopath
NPM := npm --prefix web --cache $(CURDIR)/.cache/npm

ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help dev api web db-up object-bucket db-status db-migrate db-reset test test-api test-postgres test-web build build-api build-web sync-static production-readiness release-evidence fmt clean

help:
	@printf '%s\n' 'PasteBox commands:'
	@printf '%s\n' '  make dev        Start local dependencies with Docker Compose'
	@printf '%s\n' '  make api        Run the Go API'
	@printf '%s\n' '  make web        Run the Vite dev server'
	@printf '%s\n' '  make object-bucket Ensure the local MinIO pastebox bucket exists'
	@printf '%s\n' '  make db-migrate Apply local PostgreSQL migrations'
	@printf '%s\n' '  make db-reset   Reset local PostgreSQL schema and rerun migrations'
	@printf '%s\n' '  make test       Run backend and frontend checks'
	@printf '%s\n' '  make test-postgres'
	@printf '%s\n' '                  Run PostgreSQL integration checks in an ephemeral container'
	@printf '%s\n' '  make build      Build backend binary and frontend assets'
	@printf '%s\n' '  make production-readiness'
	@printf '%s\n' '                  Run local production launch-gate checks'
	@printf '%s\n' '  make release-evidence RELEASE_CHECKLIST=<file> RELEASE_NOTES=<file>'
	@printf '%s\n' '                  Validate completed sanitized production release evidence'

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

test-postgres:
	sh scripts/check-postgres-integration.sh

test-web:
	$(NPM) run typecheck
	$(NPM) run build

build: build-api

build-api: build-web sync-static
	$(GO_ENV) go build -o bin/pastebox ./cmd/pastebox

build-web:
	$(NPM) run build

sync-static:
	mkdir -p internal/httpserver/static
	cp -R web/dist/. internal/httpserver/static/

production-readiness:
	sh scripts/check-production-readiness.sh

release-evidence:
	@if [ -z "$(RELEASE_CHECKLIST)" ] || [ -z "$(RELEASE_NOTES)" ]; then \
		printf '%s\n' 'usage: make release-evidence RELEASE_CHECKLIST=<completed-checklist.md> RELEASE_NOTES=<completed-release-notes.md>'; \
		exit 2; \
	fi
	node scripts/check-production-release-evidence.mjs --checklist "$(RELEASE_CHECKLIST)" --release-notes "$(RELEASE_NOTES)"

fmt:
	gofmt -w cmd internal
	$(NPM) run format

clean:
	rm -rf bin web/dist
