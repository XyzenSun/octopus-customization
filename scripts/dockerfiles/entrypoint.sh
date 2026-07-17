#!/bin/sh
set -e

PUID="${PUID:-0}"
PGID="${PGID:-0}"

chmod +x /app/octopus

# 仅在必要时 chown，且只改 /app 目录本身（不递归）
if [ "$PUID" != "0" ] || [ "$PGID" != "0" ]; then
    chown "$PUID:$PGID" /app
fi

cd /app

if command -v su-exec >/dev/null 2>&1; then
    exec su-exec "$PUID:$PGID" ./octopus start
elif command -v gosu >/dev/null 2>&1; then
    exec gosu "$PUID:$PGID" ./octopus start
else
    exec ./octopus start
fi
