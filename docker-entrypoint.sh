#!/bin/sh
set -e

# Run the app as an unprivileged user (PUID/PGID, default 1000) instead of root.
# When the container is started with --user, it already runs unprivileged.
if [ "$(id -u)" = "0" ]; then
	# the data folder belongs to the app; the library folder is left untouched,
	# it must be writable by PUID/PGID to organize files
	chown -R "$PUID:$PGID" "$SLM_DATA_DIR"
	export HOME="$SLM_DATA_DIR"
	exec su-exec "$PUID:$PGID" "$@"
fi

exec "$@"
