#!/bin/sh
set -eu

backup_dir=/backups/postgres
source_backup="${PASTEBOX_RESTORE_SOURCE:-}"
drill_db="${PASTEBOX_RESTORE_DRILL_DATABASE:-pastebox_restore_drill}"
keep_drill_db="${PASTEBOX_KEEP_RESTORE_DRILL_DB:-false}"

case "$drill_db" in
	""|*[!A-Za-z0-9_-]*)
		echo "invalid PASTEBOX_RESTORE_DRILL_DATABASE: $drill_db" >&2
		exit 2
		;;
esac

if [ -z "$source_backup" ]; then
	source_backup="$(find "$backup_dir" -type f -name 'pastebox-*.sql.gz' | sort | tail -n 1)"
fi

if [ -z "$source_backup" ] || [ ! -f "$source_backup" ]; then
	echo "restore source backup not found: ${source_backup:-<latest>}" >&2
	exit 1
fi

if [ -f "$source_backup.sha256" ]; then
	sha256sum -c "$source_backup.sha256"
else
	echo "missing checksum for $source_backup" >&2
	exit 1
fi

started_at="$(date +%s)"
echo "restoring $source_backup into scratch database $drill_db"

psql --dbname=postgres --set=ON_ERROR_STOP=1 --set=drill_db="$drill_db" <<'SQL'
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = :'drill_db'
  AND pid <> pg_backend_pid();
SQL

dropdb --if-exists "$drill_db"
createdb "$drill_db"
gunzip -c "$source_backup" | psql --dbname="$drill_db" --set=ON_ERROR_STOP=1

psql --dbname="$drill_db" --tuples-only --no-align --set=ON_ERROR_STOP=1 \
	--command="SELECT count(*) FROM schema_migrations;" >/tmp/pastebox-restore-drill-schema-count

finished_at="$(date +%s)"
duration_seconds=$((finished_at - started_at))
schema_count="$(cat /tmp/pastebox-restore-drill-schema-count | tr -d '[:space:]')"

if [ "$keep_drill_db" != "true" ]; then
	dropdb "$drill_db"
fi

echo "restore drill succeeded backup=$source_backup duration_seconds=$duration_seconds schema_migrations=$schema_count kept_database=$keep_drill_db"
