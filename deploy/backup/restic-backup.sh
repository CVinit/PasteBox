#!/bin/sh
set -eu

if ! restic snapshots >/dev/null 2>&1; then
	restic init
fi

restic backup /backups
restic forget --keep-daily 30 --prune
restic check --read-data-subset=1/20
