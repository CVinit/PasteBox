#!/bin/sh
set -eu

. /usr/local/bin/pastebox-textfile-metrics.sh 2>/dev/null || pastebox_write_textfile_metrics() { :; }

backup_dir=/backups/postgres
mkdir -p "$backup_dir"

started_at="$(date +%s)"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
target="$backup_dir/pastebox-$timestamp.sql.gz"

pg_dump --format=plain --no-owner --no-privileges | gzip > "$target"
sha256sum "$target" > "$target.sha256"
finished_at="$(date +%s)"
duration_seconds=$((finished_at - started_at))
size_bytes="$(wc -c < "$target" | tr -d '[:space:]')"

find "$backup_dir" -type f -name 'pastebox-*.sql.gz' -mtime +"${PASTEBOX_BACKUP_RETENTION_DAYS:-30}" -delete
find "$backup_dir" -type f -name 'pastebox-*.sql.gz.sha256' -mtime +"${PASTEBOX_BACKUP_RETENTION_DAYS:-30}" -delete

pastebox_write_textfile_metrics "pastebox-logical-backup.prom" \
	"# HELP pastebox_logical_backup_last_success_timestamp_seconds Unix timestamp of the latest successful logical PostgreSQL backup." \
	"# TYPE pastebox_logical_backup_last_success_timestamp_seconds gauge" \
	"pastebox_logical_backup_last_success_timestamp_seconds $finished_at" \
	"# HELP pastebox_logical_backup_last_duration_seconds Duration of the latest successful logical PostgreSQL backup." \
	"# TYPE pastebox_logical_backup_last_duration_seconds gauge" \
	"pastebox_logical_backup_last_duration_seconds $duration_seconds" \
	"# HELP pastebox_logical_backup_last_size_bytes Size of the latest successful logical PostgreSQL backup artifact." \
	"# TYPE pastebox_logical_backup_last_size_bytes gauge" \
	"pastebox_logical_backup_last_size_bytes $size_bytes"

echo "created $target"
