#!/bin/sh
set -e

# Run the app as an unprivileged user (PUID/PGID, default 1000) instead of root.
# When the container is started with --user, it already runs unprivileged.
if [ "$(id -u)" = "0" ]; then
	# Give the data folder to the app. Files mounted read-only into it (e.g. prod.keys)
	# and other mounts are left alone. The library folder is never changed: it must be
	# writable by PUID/PGID to organize files.
	#
	# Going through every file of the data folder (covers, thumbnails, caches: tens of
	# thousands in a big library) takes minutes on a network share or an Unraid user share,
	# and the files the app writes already belong to PUID/PGID. So it is done once for
	# each owner: a mark in the data folder remembers who was given the files, and later
	# starts only check the folder itself.
	mark="$SLM_DATA_DIR/.owner"
	owner="$PUID:$PGID"
	if [ "$(cat "$mark" 2>/dev/null)" != "$owner" ]; then
		echo "Giving the data folder to $owner (only after a change of PUID/PGID, it can take a while)..."
		find "$SLM_DATA_DIR" -xdev \( ! -user "$PUID" -o ! -group "$PGID" \) \
			-exec chown "$PUID:$PGID" {} + 2>/dev/null || true
		echo "$owner" > "$mark" 2>/dev/null && chown "$PUID:$PGID" "$mark" 2>/dev/null || true
	else
		# the folder itself and the files at its top, which a restore or a copy may replace
		find "$SLM_DATA_DIR" -xdev -maxdepth 1 \( ! -user "$PUID" -o ! -group "$PGID" \) \
			-exec chown "$PUID:$PGID" {} + 2>/dev/null || true
	fi

	if ! su-exec "$PUID:$PGID" test -w "$SLM_DATA_DIR"; then
		echo "The data folder $SLM_DATA_DIR is not writable by user $PUID:$PGID." >&2
		echo "Set PUID/PGID to the owner of the folder on the host, or change its permissions." >&2
		exit 1
	fi

	if [ "$PUID" = "0" ]; then
		echo "Warning: PUID=0 runs the app as root. Set PUID/PGID to an unprivileged user (on Unraid 99/100)." >&2
	fi
	exec su-exec "$PUID:$PGID" env HOME="$SLM_DATA_DIR" "$@"
fi

exec "$@"
