#!/bin/sh
set -eu

backup_root=/backups/basebackups
wal_dir=/backups/wal
source_base="${PASTEBOX_PITR_SOURCE_BASE:-}"
target_time="${PASTEBOX_PITR_TARGET_TIME:-}"
drill_db="${PASTEBOX_PITR_DRILL_DATABASE:-${PGDATABASE:-pastebox}}"
drill_port="${PASTEBOX_PITR_DRILL_PORT:-55432}"
recovery_wait_seconds="${PASTEBOX_PITR_RECOVERY_WAIT_SECONDS:-300}"
keep_drill_dir="${PASTEBOX_KEEP_PITR_DRILL_DIR:-false}"
work_dir="${PASTEBOX_PITR_DRILL_DIR:-/tmp/pastebox-pitr-drill}"
server_started=false
data_dir=""

is_positive_int() {
	case "$1" in
		""|*[!0-9]*)
			return 1
			;;
	esac
	[ "$1" -gt 0 ]
}

case "$drill_db" in
	""|*[!A-Za-z0-9_-]*)
		echo "invalid PASTEBOX_PITR_DRILL_DATABASE: $drill_db" >&2
		exit 2
		;;
esac

case "$drill_port" in
	""|*[!0-9]*)
		echo "invalid PASTEBOX_PITR_DRILL_PORT: $drill_port" >&2
		exit 2
		;;
esac
if [ "$drill_port" -le 0 ] || [ "$drill_port" -gt 65535 ]; then
	echo "PASTEBOX_PITR_DRILL_PORT must be a valid TCP port, got $drill_port" >&2
	exit 2
fi
if ! is_positive_int "$recovery_wait_seconds"; then
	echo "invalid PASTEBOX_PITR_RECOVERY_WAIT_SECONDS: $recovery_wait_seconds" >&2
	exit 2
fi

case "$target_time" in
	*"'"*)
		echo "PASTEBOX_PITR_TARGET_TIME must not contain single quotes" >&2
		exit 2
		;;
esac

cleanup() {
	if [ "$server_started" = "true" ] && [ -n "$data_dir" ]; then
		gosu postgres pg_ctl --pgdata="$data_dir" --mode=fast --wait stop >/dev/null 2>&1 || true
	fi
	if [ "$keep_drill_dir" != "true" ]; then
		rm -rf "$work_dir"
	fi
}
trap cleanup EXIT INT TERM

if [ -z "$source_base" ]; then
	source_base="$(ls -1t "$backup_root"/pastebox-base-*.tar.gz 2>/dev/null | head -n 1 || true)"
fi

if [ -z "$source_base" ] || [ ! -f "$source_base" ]; then
	echo "PITR source base backup not found: ${source_base:-<latest>}" >&2
	exit 1
fi
if [ ! -f "$source_base.sha256" ]; then
	echo "missing checksum for $source_base" >&2
	exit 1
fi
if [ ! -d "$wal_dir" ] || [ -z "$(ls -A "$wal_dir" 2>/dev/null)" ]; then
	echo "missing WAL archive files under $wal_dir" >&2
	exit 1
fi

sha256sum -c "$source_base.sha256"

started_at="$(date +%s)"
rm -rf "$work_dir"
mkdir -p "$work_dir"
tar -xzf "$source_base" -C "$work_dir"

base_name="$(basename "$source_base" .tar.gz)"
data_dir="$work_dir/$base_name"
if [ ! -d "$data_dir" ]; then
	echo "base backup tar did not contain expected directory $base_name" >&2
	exit 1
fi

pg_verifybackup --wal-directory="$wal_dir" "$data_dir"

cat >> "$data_dir/postgresql.auto.conf" <<EOF
archive_mode = 'off'
hot_standby = 'on'
listen_addresses = '127.0.0.1'
port = $drill_port
restore_command = 'cp /backups/wal/%f %p'
unix_socket_directories = '$work_dir'
EOF
if [ -n "$target_time" ]; then
	cat >> "$data_dir/postgresql.auto.conf" <<EOF
recovery_target_time = '$target_time'
recovery_target_action = 'promote'
EOF
fi
touch "$data_dir/recovery.signal"

chown -R postgres:postgres "$work_dir"
chmod 700 "$data_dir"

gosu postgres pg_ctl --pgdata="$data_dir" --log="$work_dir/postgres.log" --wait start
server_started=true

recovery_state="t"
deadline=$(( $(date +%s) + recovery_wait_seconds ))
while [ "$recovery_state" != "f" ] && [ "$(date +%s)" -le "$deadline" ]; do
	recovery_state="$(psql --host=127.0.0.1 --port="$drill_port" --username="$PGUSER" --dbname=postgres \
		--tuples-only --no-align --set=ON_ERROR_STOP=1 \
		--command="SELECT pg_is_in_recovery();" | tr -d '[:space:]')"
	if [ "$recovery_state" != "f" ]; then
		sleep 1
	fi
done
if [ "$recovery_state" != "f" ]; then
	echo "PITR drill did not finish recovery within ${recovery_wait_seconds}s" >&2
	exit 1
fi

schema_count="$(psql --host=127.0.0.1 --port="$drill_port" --username="$PGUSER" --dbname="$drill_db" \
	--tuples-only --no-align --set=ON_ERROR_STOP=1 \
	--command="SELECT count(*) FROM schema_migrations;" | tr -d '[:space:]')"

finished_at="$(date +%s)"
duration_seconds=$((finished_at - started_at))

echo "pitr restore drill succeeded base_backup=$source_base duration_seconds=$duration_seconds schema_migrations=$schema_count in_recovery=$recovery_state kept_dir=$keep_drill_dir"
