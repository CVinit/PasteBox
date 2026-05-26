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
	PASTEBOX_PUBLIC_URL) value="https://pastebox.app" ;;
	PASTEBOX_SUPPORT_EMAIL) value="support@pastebox.app" ;;
	PASTEBOX_ABUSE_EMAIL) value="abuse@pastebox.app" ;;
	PASTEBOX_CSRF_SECRET) value="synthetic-csrf-token-32-bytes-prod" ;;
	PASTEBOX_METRICS_TOKEN) value="synthetic-metrics-token-32-bytes-prod" ;;
	PASTEBOX_CORS_ALLOWED_ORIGINS) value="https://pastebox.app" ;;
	PASTEBOX_POSTGRES_PASSWORD) value="synthetic-db-secret" ;;
	PASTEBOX_DATABASE_URL) value="postgres://pastebox:synthetic-db-secret@postgres:5432/pastebox?sslmode=disable" ;;
	PASTEBOX_S3_ENDPOINT) value="https://objects.pastebox-storage.app" ;;
	PASTEBOX_S3_ACCESS_KEY) value="synthetic-object-access-key" ;;
	PASTEBOX_S3_SECRET_KEY) value="synthetic-object-secret-key" ;;
	PASTEBOX_SMTP_HOST) value="smtp.pastebox-mail.app" ;;
	PASTEBOX_SMTP_USERNAME) value="smtp-user" ;;
	PASTEBOX_SMTP_PASSWORD) value="smtp-secret" ;;
	PASTEBOX_SMTP_FROM_EMAIL) value="noreply@pastebox.app" ;;
	PASTEBOX_GOOGLE_OAUTH_CLIENT_ID) value="google-client-id" ;;
	PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET) value="google-client-secret" ;;
	PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL) value="https://pastebox.app/api/v1/auth/google/callback" ;;
	PASTEBOX_STRIPE_WEBHOOK_SECRET) value="whsec_synthetic_production_webhook_secret" ;;
	PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE) value="https://checkout.pastebox-billing.app/session?order_id={order_id}&success_url={success_url}&cancel_url={cancel_url}" ;;
	PASTEBOX_EPUSDT_PID) value="1000" ;;
	PASTEBOX_EPUSDT_SECRET_KEY) value="epusdt-secret" ;;
	PASTEBOX_EPUSDT_CHECKOUT_URL_TEMPLATE) value="https://epusdt.pastebox-billing.app/pay?order_id={order_id}&amount_cents={amount_cents}&currency={currency}" ;;
	PASTEBOX_EPUSDT_ADDRESS) value="TREALUSDTADDRESS" ;;
	PASTEBOX_BOOTSTRAP_ADMIN_EMAIL) value="admin@pastebox.app" ;;
	PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD) value="rvK9pL4qT7mN2sX8cZ5uY3wA" ;;
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
	go run ./cmd/pastebox preflight production
