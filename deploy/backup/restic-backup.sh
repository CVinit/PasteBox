#!/bin/sh
set -eu

. /usr/local/bin/pastebox-textfile-metrics.sh 2>/dev/null || pastebox_write_textfile_metrics() { :; }

started_at="$(date +%s)"
if ! restic snapshots >/dev/null 2>&1; then
	restic init
fi

restic backup /backups
restic forget --keep-daily 30 --prune
restic check --read-data-subset=1/20
finished_at="$(date +%s)"
duration_seconds=$((finished_at - started_at))

pastebox_write_textfile_metrics "pastebox-offhost-backup.prom" \
	"# HELP pastebox_offhost_backup_last_success_timestamp_seconds Unix timestamp of the latest successful off-host backup push and integrity check." \
	"# TYPE pastebox_offhost_backup_last_success_timestamp_seconds gauge" \
	"pastebox_offhost_backup_last_success_timestamp_seconds $finished_at" \
	"# HELP pastebox_offhost_backup_last_duration_seconds Duration of the latest successful off-host backup push and integrity check." \
	"# TYPE pastebox_offhost_backup_last_duration_seconds gauge" \
	"pastebox_offhost_backup_last_duration_seconds $duration_seconds"
