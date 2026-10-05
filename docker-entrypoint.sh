#!/bin/sh
set -e

# Run the app as an unprivileged user (PUID/PGID, default 1000) instead of root.
# When the container is started with --user, it already runs unprivileged.
if [ "$(id -u)" = "0" ]; then
	# Give the data folder to the app. Files mounted read-only into it (e.g. prod.keys)
	# and other mounts are left alone. The library folder is never changed: it must be
	# writable by PUID/PGID to organize files.
	find "$SLM_DATA_DIR" -xdev \( ! -user "$PUID" -o ! -group "$PGID" \) \
		-exec chown "$PUID:$PGID" {} + 2>/dev/null || true

	if ! su-exec "$PUID:$PGID" test -w "$SLM_DATA_DIR"; then
		echo "The data folder $SLM_DATA_DIR is not writable by user $PUID:$PGID." >&2
		echo "Set PUID/PGID to the owner of the folder on the host, or change its permissions." >&2
		exit 1
	fi

	exec su-exec "$PUID:$PGID" env HOME="$SLM_DATA_DIR" "$@"
fi

exec "$@"
