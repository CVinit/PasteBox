#!/bin/sh
set -eu

backup_dir=/backups/postgres
mkdir -p "$backup_dir"

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
target="$backup_dir/pastebox-$timestamp.sql.gz"

pg_dump --format=plain --no-owner --no-privileges | gzip > "$target"
sha256sum "$target" > "$target.sha256"

find "$backup_dir" -type f -name 'pastebox-*.sql.gz' -mtime +"${PASTEBOX_BACKUP_RETENTION_DAYS:-30}" -delete
find "$backup_dir" -type f -name 'pastebox-*.sql.gz.sha256' -mtime +"${PASTEBOX_BACKUP_RETENTION_DAYS:-30}" -delete

echo "created $target"
