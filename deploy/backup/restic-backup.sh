#!/bin/sh
set -eu

. /usr/local/bin/pastebox-textfile-metrics.sh 2>/dev/null || pastebox_write_textfile_metrics() { :; }

manifest_dir=/backups/restic
mkdir -p "$manifest_dir"

backup_log="$(mktemp /tmp/pastebox-restic-backup.XXXXXX)"
snapshots_log="$(mktemp /tmp/pastebox-restic-snapshots.XXXXXX)"
cleanup() {
	rm -f "$backup_log" "$snapshots_log"
}
trap cleanup EXIT INT TERM

json_string_value() {
	key="$1"
	file="$2"
	sed -n 's/.*"'"$key"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file" | tail -n 1
}

started_at="$(date +%s)"
if ! restic snapshots >/dev/null 2>&1; then
	restic init
fi

restic backup --json /backups > "$backup_log"
snapshot_id="$(json_string_value snapshot_id "$backup_log")"
if [ -z "$snapshot_id" ]; then
	restic snapshots --latest 1 --json > "$snapshots_log"
	snapshot_id="$(json_string_value short_id "$snapshots_log")"
fi
if [ -z "$snapshot_id" ]; then
	echo "unable to determine restic snapshot id from backup output" >&2
	exit 1
fi

restic forget --keep-daily 30 --prune
restic check --read-data-subset=1/20
finished_at="$(date +%s)"
duration_seconds=$((finished_at - started_at))
manifest="$manifest_dir/pastebox-restic-$(date -u +%Y%m%dT%H%M%SZ).manifest"

{
	echo "created_at_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "snapshot_id=$snapshot_id"
	echo "duration_seconds=$duration_seconds"
	echo "read_data_subset=1/20"
	echo "integrity_check=passed"
} > "$manifest"

find "$manifest_dir" -type f -name 'pastebox-restic-*.manifest' -mtime +"${PASTEBOX_BACKUP_RETENTION_DAYS:-30}" -delete

pastebox_write_textfile_metrics "pastebox-offhost-backup.prom" \
	"# Latest restic snapshot id: $snapshot_id" \
	"# HELP pastebox_offhost_backup_last_success_timestamp_seconds Unix timestamp of the latest successful off-host backup push and integrity check." \
	"# TYPE pastebox_offhost_backup_last_success_timestamp_seconds gauge" \
	"pastebox_offhost_backup_last_success_timestamp_seconds $finished_at" \
	"# HELP pastebox_offhost_backup_last_duration_seconds Duration of the latest successful off-host backup push and integrity check." \
	"# TYPE pastebox_offhost_backup_last_duration_seconds gauge" \
	"pastebox_offhost_backup_last_duration_seconds $duration_seconds"

echo "off-host backup succeeded snapshot_id=$snapshot_id manifest=$manifest duration_seconds=$duration_seconds"
