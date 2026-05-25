#!/bin/sh
set -eu

backup_root=/backups/basebackups
wal_dir=/backups/wal
retention_days="${PASTEBOX_BACKUP_RETENTION_DAYS:-30}"
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

if ! is_positive_int "$retention_days"; then
	echo "invalid PASTEBOX_BACKUP_RETENTION_DAYS: $retention_days" >&2
	exit 2
fi
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

mkdir -p "$backup_root" "$wal_dir"

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

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
target_dir="$backup_root/pastebox-base-$timestamp"
target_tar="$target_dir.tar.gz"
manifest="$target_dir.manifest"

cleanup_target_dir() {
	rm -rf "$target_dir"
}
trap cleanup_target_dir EXIT INT TERM

pg_basebackup --pgdata="$target_dir" --format=plain --checkpoint=fast --wal-method=none --progress

latest_wal_info="$(wait_for_fresh_wal)"
latest_wal="${latest_wal_info% *}"
wal_age_seconds="${latest_wal_info##* }"

pg_verifybackup --wal-directory="$wal_dir" "$target_dir"
tar -C "$backup_root" -czf "$target_tar" "pastebox-base-$timestamp"
sha256sum "$target_tar" > "$target_tar.sha256"

{
	echo "created_at_utc=$timestamp"
	echo "base_backup=$target_tar"
	echo "base_backup_sha256=$target_tar.sha256"
	echo "latest_wal=$latest_wal"
	echo "latest_wal_age_seconds=$wal_age_seconds"
	echo "pg_verifybackup=passed"
	echo "rpo_target_seconds=900"
	echo "rto_target_seconds=14400"
} > "$manifest"

find "$backup_root" -type f -name 'pastebox-base-*.tar.gz' -mtime +"$retention_days" -delete
find "$backup_root" -type f -name 'pastebox-base-*.tar.gz.sha256' -mtime +"$retention_days" -delete
find "$backup_root" -type f -name 'pastebox-base-*.manifest' -mtime +"$retention_days" -delete
find "$wal_dir" -type f -mtime +"$retention_days" -delete

echo "created base backup $target_tar latest_wal=$latest_wal wal_age_seconds=$wal_age_seconds"
