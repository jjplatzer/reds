#!/usr/bin/env bash
# Build reds and its SWIM/Solace target reader, then run the menu.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

if [[ -f .env ]]; then
    set -o allexport
    # shellcheck disable=SC1091
    . ./.env
    set +o allexport
fi

WS_PORT="${WS_PORT:-8080}"
export WS_PORT

REMOTE_TARGET_URL="${REDS_TARGET_WS_URL:-}"
USE_REMOTE_TARGETS=0
if [[ -n "$REMOTE_TARGET_URL" ]]; then
    USE_REMOTE_TARGETS=1
fi

mkdir -p build

echo "[build] Building reds (Go frontend)..." >&2
go build -o build/reds ./cmd/reds

if [[ "$USE_REMOTE_TARGETS" -eq 0 ]]; then
    echo "[build] Building SMES reader..." >&2
    mvn -q -f server/smes/pom.xml package

    STALE_PIDS="$(lsof -ti "tcp:${WS_PORT}" -sTCP:LISTEN 2>/dev/null || true)"
    if [[ -n "$STALE_PIDS" ]]; then
        echo "[run] Killing stale listener(s) on :$WS_PORT - $STALE_PIDS" >&2
        kill $STALE_PIDS 2>/dev/null || true
        sleep 0.3
        kill -9 $STALE_PIDS 2>/dev/null || true
    fi
else
    echo "[build] Remote target stream configured: $REMOTE_TARGET_URL" >&2
    echo "[build] Skipping local SMES build and :$WS_PORT listener cleanup." >&2
fi

echo "[run] Launching reds..." >&2
./build/reds
