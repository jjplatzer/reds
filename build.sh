#!/usr/bin/env bash
# Build (and run) reds. Kept as a shell script for now; a Windows/.exe build
# path can come later. Unlike the old Qt build.sh, the menu no longer prints a
# selected airport for the script to capture — selection is handled in-process,
# so this just builds the Go binary and the SMES reader, then launches reds.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

if [[ -f .env ]]; then
    set -o allexport
    # shellcheck disable=SC1091
    . ./.env
    set +o allexport
fi

echo "[build] Building reds (Go frontend)..." >&2
go build -o build/reds ./cmd/reds

echo "[build] Building SMES reader..." >&2
mvn -q -f swim/smes/pom.xml package

# NOTE: the SWIM/Solace consumer is no longer started here. Once Confirm wires
# up the scopes, the selected airport will start the consumer in-process.

echo "[run] Launching reds..." >&2
exec ./build/reds
