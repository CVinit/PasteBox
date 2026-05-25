#!/bin/sh
set -eu

wal_dir=/backups/wal
max_age_seconds="${PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS:-900}"
wait_seconds="${PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS:-60}"

is_positive_int() {
	case "$1" in
		""|*[!0-9]*)
			return 1
			;;
	esac
	[ "$1" -gt 0 ]
}

if ! is_positive_int "$max_age_seconds"; then
	echo "invalid PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS: $max_age_seconds" >&2
	exit 2
fi
if [ "$max_age_seconds" -gt 900 ]; then
	echo "PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS must be <= 900 to meet the 15 minute RPO target" >&2
	exit 2
fi
if ! is_positive_int "$wait_seconds"; then
	echo "invalid PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS: $wait_seconds" >&2
	exit 2
fi
if [ "$wait_seconds" -gt 900 ]; then
	echo "PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS must be <= 900 to meet the 15 minute RPO target" >&2
	exit 2
fi

mkdir -p "$wal_dir"

recent_archive_failure() {
	psql --dbname="$PGDATABASE" --tuples-only --no-align --set=ON_ERROR_STOP=1 \
		--command="SELECT COALESCE(last_failed_wal, '') FROM pg_stat_archiver WHERE last_failed_time IS NOT NULL AND (last_archived_time IS NULL OR last_failed_time > last_archived_time);" | tr -d '[:space:]'
}

wait_for_fresh_wal() {
	deadline=$(( $(date +%s) + wait_seconds ))
	latest_wal=""
	latest_age_seconds=""
	while [ "$(date +%s)" -le "$deadline" ]; do
		failed_wal="$(recent_archive_failure)"
		if [ -n "$failed_wal" ]; then
			echo "PostgreSQL WAL archiver reports a recent failure: $failed_wal" >&2
			return 1
		fi

		latest_wal_name="$(ls -1t "$wal_dir" 2>/dev/null | grep -E '^[0-9A-F]{24}$' | head -n 1 || true)"
		if [ -n "$latest_wal_name" ]; then
			latest_wal="$wal_dir/$latest_wal_name"
			now="$(date +%s)"
			wal_mtime="$(stat -c %Y "$latest_wal")"
			latest_age_seconds=$((now - wal_mtime))
			if [ "$latest_age_seconds" -le "$max_age_seconds" ]; then
				printf '%s %s\n' "$latest_wal" "$latest_age_seconds"
				return 0
			fi
		fi
		sleep 1
	done

	if [ -n "$latest_wal" ]; then
		echo "latest archived WAL is too old: file=$latest_wal age_seconds=$latest_age_seconds max_age_seconds=$max_age_seconds" >&2
	else
		echo "WAL archive did not contain an archived WAL segment within ${wait_seconds}s" >&2
	fi
	return 1
}

if ! psql --dbname="$PGDATABASE" --tuples-only --no-align --set=ON_ERROR_STOP=1 \
	--command="SELECT pg_switch_wal();" >/dev/null; then
	echo "pg_switch_wal failed" >&2
	exit 1
fi

latest_wal_info="$(wait_for_fresh_wal)"
latest_wal="${latest_wal_info% *}"
age_seconds="${latest_wal_info##* }"

echo "wal archive check succeeded file=$latest_wal age_seconds=$age_seconds max_age_seconds=$max_age_seconds"
