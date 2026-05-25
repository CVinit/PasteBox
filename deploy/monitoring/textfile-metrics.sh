#!/bin/sh

pastebox_write_textfile_metrics() {
	metric_file="$1"
	shift
	metric_dir="${PASTEBOX_TEXTFILE_COLLECTOR_DIR:-/var/lib/node_exporter/textfile_collector}"
	if [ -z "$metric_file" ]; then
		return 0
	fi
	if ! mkdir -p "$metric_dir" 2>/dev/null; then
		return 0
	fi
	tmp="$metric_dir/$metric_file.$$"
	if {
		for line in "$@"; do
			printf '%s\n' "$line"
		done
	} > "$tmp"; then
		mv "$tmp" "$metric_dir/$metric_file" 2>/dev/null || rm -f "$tmp"
	else
		rm -f "$tmp"
	fi
	return 0
}
