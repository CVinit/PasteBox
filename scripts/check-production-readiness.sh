#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

env_file=${PASTEBOX_PRODUCTION_ENV_FILE:-deploy/production.env.example}
image_tag=${PASTEBOX_READINESS_IMAGE:-pastebox:local-readiness}

env_value() {
	key=$1
	default=$2
	value=$(awk -F= -v key="$key" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "$env_file")
	if [ -n "$value" ]; then
		printf '%s\n' "$value"
	else
		printf '%s\n' "$default"
	fi
}

section() {
	printf '\n==> %s\n' "$1"
}

run() {
	printf '+ %s\n' "$*"
	"$@"
}

require_file() {
	if [ ! -f "$1" ]; then
		printf 'missing required file: %s\n' "$1" >&2
		exit 1
	fi
}

require_file "$env_file"
require_file deploy/infra.env.example
require_file deploy/production.combined.env.example
require_file deploy/postgresql.env.example
require_file deploy/redis.env.example
require_file deploy/production.split.env.example
require_file compose.nginx-host.example.yaml

legacy_names='shared''-postgres|shared''-redis|shared''-postgresql|SHARED''_POSTGRES|SHARED''_REDIS|PASTEBOX''_SHARED|compose.''shared|production.''shared|shared''-infra|pastebox-shared''-services|shared''-postgres-net|shared''-redis-net|shared''-postgres-data|shared''-redis-data|shared''-postgres-backups|shared''-split'
if rg -n -i "$legacy_names" \
	compose*.yaml deploy docs scripts .gitignore .dockerignore \
	--glob '!*.png' >/dev/null 2>&1; then
	printf 'legacy PostgreSQL/Redis infrastructure names remain in active files\n' >&2
	exit 1
fi

prometheus_image=$(env_value PASTEBOX_PROMETHEUS_IMAGE prom/prometheus:v2.55.1)
caddy_image=$(env_value PASTEBOX_CADDY_IMAGE caddy:2.10-alpine)
blackbox_image=$(env_value PASTEBOX_BLACKBOX_EXPORTER_IMAGE prom/blackbox-exporter:v0.25.0)

export PASTEBOX_ENV_FILE="$env_file"

section "Compose config"
run docker compose --env-file "$env_file" -f compose.production.yaml config >/dev/null
run docker compose --env-file "$env_file" -f compose.production.yaml --profile monitoring config >/dev/null
run docker compose --env-file "$env_file" -f compose.production.yaml --profile maintenance config >/dev/null
run docker compose --env-file deploy/infra.env.example -f compose.infra.yaml config >/dev/null
infra_config=$(docker compose --env-file deploy/infra.env.example -f compose.infra.yaml config)
printf '%s\n' "$infra_config" | grep -q 'name: infra'
printf '%s\n' "$infra_config" | grep -q 'name: infra-net'
printf '%s\n' "$infra_config" | grep -q -- '- postgresql'
printf '%s\n' "$infra_config" | grep -q -- '- redis'
run env PASTEBOX_ENV_FILE=./deploy/production.combined.env.example docker compose \
	--env-file deploy/infra.env.example \
	--env-file deploy/production.combined.env.example \
	-f compose.production.yaml \
	-f compose.external-services.yaml \
	--profile maintenance \
	config >/dev/null
run env PASTEBOX_ENV_FILE=./deploy/production.combined.env.example docker compose \
	--env-file deploy/infra.env.example \
	--env-file deploy/production.combined.env.example \
	-f compose.production.yaml \
	-f compose.external-services.yaml \
	-f compose.nginx-host.example.yaml \
	config >/dev/null
combined_services=$(PASTEBOX_ENV_FILE=./deploy/production.combined.env.example docker compose \
	--env-file deploy/infra.env.example \
	--env-file deploy/production.combined.env.example \
	-f compose.production.yaml \
	-f compose.external-services.yaml \
	--profile maintenance \
	config --services)
printf '%s\n' "$combined_services" | grep -qx api
printf '%s\n' "$combined_services" | grep -qx worker
printf '%s\n' "$combined_services" | grep -qx migrate
if printf '%s\n' "$combined_services" | grep -Eq '^(postgres|redis|backup-volume-init)$'; then
	printf 'combined production config unexpectedly includes integrated infrastructure\n' >&2
	exit 1
fi

run docker compose --env-file deploy/postgresql.env.example -f compose.postgresql.yaml config >/dev/null
run docker compose --env-file deploy/redis.env.example -f compose.redis.yaml config >/dev/null
postgresql_config=$(docker compose --env-file deploy/postgresql.env.example -f compose.postgresql.yaml config)
redis_config=$(docker compose --env-file deploy/redis.env.example -f compose.redis.yaml config)
printf '%s\n' "$postgresql_config" | grep -q 'name: postgresql'
printf '%s\n' "$postgresql_config" | grep -q 'name: postgresql-net'
printf '%s\n' "$postgresql_config" | grep -q -- '- postgresql'
printf '%s\n' "$redis_config" | grep -q 'name: redis'
printf '%s\n' "$redis_config" | grep -q 'name: redis-net'
printf '%s\n' "$redis_config" | grep -q -- '- redis'
run env PASTEBOX_ENV_FILE=./deploy/production.split.env.example docker compose \
	--env-file deploy/postgresql.env.example \
	--env-file deploy/redis.env.example \
	--env-file deploy/production.split.env.example \
	-f compose.production.yaml \
	-f compose.external-split-services.yaml \
	--profile maintenance \
	config >/dev/null
run env PASTEBOX_ENV_FILE=./deploy/production.split.env.example docker compose \
	--env-file deploy/postgresql.env.example \
	--env-file deploy/redis.env.example \
	--env-file deploy/production.split.env.example \
	-f compose.production.yaml \
	-f compose.external-split-services.yaml \
	-f compose.nginx-host.example.yaml \
	config >/dev/null
split_services=$(PASTEBOX_ENV_FILE=./deploy/production.split.env.example docker compose \
	--env-file deploy/postgresql.env.example \
	--env-file deploy/redis.env.example \
	--env-file deploy/production.split.env.example \
	-f compose.production.yaml \
	-f compose.external-split-services.yaml \
	--profile maintenance \
	config --services)
printf '%s\n' "$split_services" | grep -qx api
printf '%s\n' "$split_services" | grep -qx worker
printf '%s\n' "$split_services" | grep -qx migrate
if printf '%s\n' "$split_services" | grep -Eq '^(postgres|redis|backup-volume-init)$'; then
	printf 'split production config unexpectedly includes integrated infrastructure\n' >&2
	exit 1
fi

section "Maintenance script syntax"
run sh -n scripts/check-production-preflight.sh
run sh -n scripts/check-postgres-integration.sh
run sh -n deploy/pastebox-deploy.sh
run sh -n deploy/monitoring/textfile-metrics.sh
run sh -n deploy/backup/postgres-backup.sh
run sh -n deploy/backup/postgres-basebackup.sh
run sh -n deploy/backup/postgres-wal-check.sh
run sh -n deploy/backup/restic-backup.sh
run sh -n deploy/backup/postgres-restore-drill.sh
run sh -n deploy/backup/postgres-pitr-restore-drill.sh

section "Monitoring config syntax"
run docker run --rm --user 0 --entrypoint sh \
	-v "$repo_root/deploy/monitoring/prometheus.yml:/etc/prometheus/prometheus.yml:ro" \
	-v "$repo_root/deploy/monitoring/pastebox-alerts.yml:/etc/prometheus/rules/pastebox-alerts.yml:ro" \
	"$prometheus_image" \
	-c 'mkdir -p /run/secrets && printf dummy-token >/run/secrets/pastebox_metrics_token && promtool check config /etc/prometheus/prometheus.yml'

run docker run --rm --entrypoint /bin/blackbox_exporter \
	-v "$repo_root/deploy/monitoring/blackbox.yml:/etc/blackbox_exporter/config.yml:ro" \
	"$blackbox_image" \
	--config.check --config.file=/etc/blackbox_exporter/config.yml

section "Caddy config syntax"
run docker run --rm --env-file "$env_file" \
	-v "$repo_root/deploy/caddy/Caddyfile:/etc/caddy/Caddyfile:ro" \
	"$caddy_image" \
	caddy validate --config /etc/caddy/Caddyfile

section "Production preflight"
run sh scripts/check-production-preflight.sh

section "Project tests"
run make test

section "Frontend dependency audit"
run npm --prefix web --cache "$repo_root/.cache/npm" audit --audit-level=high

section "Web launch surfaces"
run node scripts/check-web-launch-surfaces.mjs

section "Release evidence template"
run node scripts/check-release-evidence-template.mjs

section "Release evidence validator"
run node scripts/check-production-release-evidence.mjs --self-test

section "Provider smoke runbook"
run node scripts/check-provider-smoke-runbook.mjs

section "PostgreSQL integration tests"
run make test-coverage

section "Project build"
run make build

if [ "${PASTEBOX_SKIP_DOCKER_BUILD:-false}" = "true" ]; then
	section "Docker image build"
	printf 'skipped because PASTEBOX_SKIP_DOCKER_BUILD=true\n'
else
	section "Docker image build"
	run docker build -t "$image_tag" .
fi

printf '\nProduction readiness local checks passed.\n'
