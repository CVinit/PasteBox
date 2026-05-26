#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

postgres_image=${PASTEBOX_TEST_POSTGRES_IMAGE:-postgres:17-alpine}
container_name=${PASTEBOX_TEST_POSTGRES_CONTAINER:-pastebox-postgres-integration-$$}
database=${PASTEBOX_TEST_POSTGRES_DB:-pastebox_test}
user=${PASTEBOX_TEST_POSTGRES_USER:-pastebox}
password=${PASTEBOX_TEST_POSTGRES_PASSWORD:-pastebox}
container_started=false

cleanup() {
	if [ "$container_started" = "true" ]; then
		docker rm -f "$container_name" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT INT TERM

printf 'Starting ephemeral PostgreSQL integration container %s\n' "$container_name"
docker run --rm -d \
	--name "$container_name" \
	-e POSTGRES_DB="$database" \
	-e POSTGRES_USER="$user" \
	-e POSTGRES_PASSWORD="$password" \
	-p 127.0.0.1::5432 \
	"$postgres_image" >/dev/null
container_started=true

port=""
for _ in $(seq 1 60); do
	port_mapping=$(docker port "$container_name" 5432/tcp 2>/dev/null || true)
	port=${port_mapping##*:}
	if [ -n "$port" ] && docker exec "$container_name" pg_isready -U "$user" -d "$database" >/dev/null 2>&1; then
		break
	fi
	sleep 1
done

if [ -z "$port" ] || ! docker exec "$container_name" pg_isready -U "$user" -d "$database" >/dev/null 2>&1; then
	printf 'PostgreSQL integration container did not become ready\n' >&2
	docker logs "$container_name" >&2 || true
	exit 1
fi

database_url="postgres://$user:$password@127.0.0.1:$port/$database?sslmode=disable"
printf 'Running PostgreSQL-backed integration tests\n'
PASTEBOX_TEST_DATABASE_URL="$database_url" \
	env GOCACHE="$repo_root/.cache/go-build" GOPATH="$repo_root/.cache/gopath" \
	go test ./internal/postgres

printf 'PostgreSQL integration checks passed.\n'
