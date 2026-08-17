#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

env_file=${PASTEBOX_PREFLIGHT_TEMPLATE:-deploy/production.env.example}
if [ ! -f "$env_file" ]; then
	printf 'missing production env template: %s\n' "$env_file" >&2
	exit 1
fi

synthetic_value() {
	key=$1
	value=$2
	case "$key" in
	PASTEBOX_IMAGE) value="ghcr.io/cvinit/pastebox:sha-0123456789abcdef" ;;
	PASTEBOX_DOMAIN) value="pastebox.app" ;;
	PASTEBOX_ADMIN_EMAIL) value="admin@pastebox.app" ;;
	PASTEBOX_CONFIG_ENCRYPTION_KEY) value="MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=" ;;
	PASTEBOX_METRICS_TOKEN) value="synthetic-metrics-token-32-bytes-prod" ;;
	PASTEBOX_POSTGRES_PASSWORD) value="synthetic-db-secret" ;;
	PASTEBOX_DATABASE_URL) value="postgres://pastebox:synthetic-db-secret@postgres:5432/pastebox?sslmode=disable" ;;
	PASTEBOX_RESTIC_REPOSITORY) value="s3:https://backups.pastebox-storage.app/pastebox-backups" ;;
	PASTEBOX_RESTIC_PASSWORD) value="synthetic-restic-secret" ;;
	PASTEBOX_BACKUP_S3_ACCESS_KEY) value="synthetic-backup-access-key" ;;
	PASTEBOX_BACKUP_S3_SECRET_KEY) value="synthetic-backup-secret-key" ;;
	esac
	printf '%s\n' "$value"
}

unmapped_placeholders=""
while IFS= read -r line || [ -n "$line" ]; do
	case "$line" in
	"" | \#*) continue ;;
	esac
	key=${line%%=*}
	value=${line#*=}
	if [ "$key" = "$line" ] || [ -z "$key" ]; then
		printf 'invalid env template line: %s\n' "$line" >&2
		exit 1
	fi
	case "$key" in
	PASTEBOX_*) ;;
	*)
		printf 'unexpected production env key: %s\n' "$key" >&2
		exit 1
		;;
	esac
	value=$(synthetic_value "$key" "$value")
	case "$value" in
	*CHANGE_ME*) unmapped_placeholders="$unmapped_placeholders $key" ;;
	esac
	export "$key=$value"
done < "$env_file"

if [ -n "$unmapped_placeholders" ]; then
	printf 'unmapped production env placeholders:%s\n' "$unmapped_placeholders" >&2
	exit 1
fi

env GOCACHE="$repo_root/.cache/go-build" GOPATH="$repo_root/.cache/gopath" \
	PASTEBOX_PREFLIGHT_ROOT_ONLY=true \
	go run ./cmd/pastebox preflight production
