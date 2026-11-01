#!/bin/sh
# Health check of the container: asks /healthz on the port the app listens on, the "port" of
# settings.json (3000 when it has none). It only reads that file and runs wget, so checking every
# 30 seconds costs nothing. The app answers in plain HTTP: HTTPS comes from a proxy in front of it.
port=$(grep -o '"port"[[:space:]]*:[[:space:]]*[0-9]*' "$SLM_DATA_DIR/settings.json" 2>/dev/null | head -n 1 | grep -o '[0-9]*$')
case "$port" in
	"" | 0) port=3000 ;;
esac
exec wget -q -T 4 -O /dev/null "http://127.0.0.1:$port/healthz"
