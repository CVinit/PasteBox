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

prometheus_image=$(env_value PASTEBOX_PROMETHEUS_IMAGE prom/prometheus:v2.55.1)
caddy_image=$(env_value PASTEBOX_CADDY_IMAGE caddy:2.10-alpine)
blackbox_image=$(env_value PASTEBOX_BLACKBOX_EXPORTER_IMAGE prom/blackbox-exporter:v0.25.0)

export PASTEBOX_ENV_FILE="$env_file"

section "Compose config"
run docker compose --env-file "$env_file" -f compose.production.yaml config >/dev/null
run docker compose --env-file "$env_file" -f compose.production.yaml --profile monitoring config >/dev/null
run docker compose --env-file "$env_file" -f compose.production.yaml --profile maintenance config >/dev/null

section "Maintenance script syntax"
run sh -n \
	deploy/monitoring/textfile-metrics.sh \
	deploy/backup/postgres-backup.sh \
	deploy/backup/postgres-basebackup.sh \
	deploy/backup/postgres-wal-check.sh \
	deploy/backup/restic-backup.sh \
	deploy/backup/postgres-restore-drill.sh \
	deploy/backup/postgres-pitr-restore-drill.sh

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

section "Project tests"
run make test

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
