#!/usr/bin/env bash
# Build reds and, in local mode, its SWIM/Solace target reader, then run the menu.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

DO_RUN=1
DO_APP=0
DO_PACKAGE=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --build-only|--no-run)
            DO_RUN=0
            ;;

        --app)
            DO_APP=1
            DO_RUN=0
            ;;

        --package)
            DO_APP=1
            DO_PACKAGE=1
            DO_RUN=0
            ;;

        --help)
            cat <<'EOF'
Usage: ./build.sh [options]

Options:
  --build-only  Build REDS but do not run it
  --no-run      Alias for --build-only
  --app         Build REDS.app but do not run it
  --package     Build REDS.app and create a distributable ZIP
  --help        Show this help
EOF
            exit 0
            ;;

        *)
            echo "Unknown option: $1" >&2
            exit 2
            ;;
    esac

    shift
done

if [[ -f .env ]]; then
    set -o allexport
    # shellcheck disable=SC1091
    . ./.env
    set +o allexport
fi

WS_PORT="${WS_PORT:-8080}"
export WS_PORT

if [[ -n "${REDS_VERSION:-}" ]]; then
    VERSION="$REDS_VERSION"
elif [[ -f VERSION ]]; then
    VERSION="$(tr -d '[:space:]' < VERSION)"
else
    VERSION="dev"
fi
VERSION="${VERSION:-dev}"

VERSION_CORE="${VERSION%%-*}"
if [[ ! "$VERSION_CORE" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    VERSION_CORE="0.0.0"
fi

if [[ -z "${REDS_COMMIT:-}" ]]; then
    REDS_COMMIT="$(git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)"
fi
if [[ -z "${REDS_BUILD_TIME:-}" ]]; then
    REDS_BUILD_TIME="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
fi

export REDS_VERSION="$VERSION"
export REDS_VERSION_CORE="$VERSION_CORE"
export REDS_COMMIT
export REDS_BUILD_TIME

USE_PUBLIC_SERVER="${USE_PUBLIC_SERVER:-true}"
USE_PUBLIC_SERVER_NORMALIZED="$(printf '%s' "$USE_PUBLIC_SERVER" | tr '[:upper:]' '[:lower:]')"
case "$USE_PUBLIC_SERVER_NORMALIZED" in
    1|true|yes|on)
        USE_PUBLIC_SERVER_ENABLED=1
        ;;
    *)
        USE_PUBLIC_SERVER_ENABLED=0
        ;;
esac
if [[ "$DO_PACKAGE" -eq 1 ]]; then
    USE_PUBLIC_SERVER_ENABLED=1
fi

mkdir -p build

echo "[build] Building reds (Go frontend)..." >&2
LDFLAGS="-X github.com/juliusplatzer/reds/util/buildinfo.Version=$REDS_VERSION"
LDFLAGS="$LDFLAGS -X github.com/juliusplatzer/reds/util/buildinfo.Commit=$REDS_COMMIT"
LDFLAGS="$LDFLAGS -X github.com/juliusplatzer/reds/util/buildinfo.BuildTime=$REDS_BUILD_TIME"

go build \
    -trimpath \
    -ldflags "$LDFLAGS" \
    -o build/reds \
    ./cmd/reds

if [[ "$DO_APP" -eq 1 ]]; then
    ./osx/make-reds-app.sh
fi

if [[ "$DO_PACKAGE" -eq 1 ]]; then
    ARCHIVE="build/REDS-${REDS_VERSION}-macOS.zip"

    echo "[package] Creating $ARCHIVE..." >&2
    rm -f "$ARCHIVE" "$ARCHIVE.sha256"

    ditto \
        -c \
        -k \
        --sequesterRsrc \
        --keepParent \
        build/REDS.app \
        "$ARCHIVE"

    shasum -a 256 "$ARCHIVE" > "$ARCHIVE.sha256"
fi

if [[ "$USE_PUBLIC_SERVER_ENABLED" -eq 0 ]]; then
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
    echo "[build] Public REDS server enabled: wss://reds-stdds-live.jjplatzer.com/ws" >&2
    echo "[build] Skipping local SMES build and :$WS_PORT listener cleanup." >&2
fi

if [[ "$DO_RUN" -eq 1 ]]; then
    echo "[run] Launching reds..." >&2
    ./build/reds
else
    echo "[done] Build complete: build/reds" >&2

    if [[ "$DO_APP" -eq 1 ]]; then
        echo "[done] App bundle: build/REDS.app" >&2
    fi
    if [[ "$DO_PACKAGE" -eq 1 ]]; then
        echo "[done] Archive: build/REDS-${REDS_VERSION}-macOS.zip" >&2
        echo "[done] Checksum: build/REDS-${REDS_VERSION}-macOS.zip.sha256" >&2
    fi
fi
