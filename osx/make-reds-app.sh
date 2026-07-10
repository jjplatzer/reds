#!/usr/bin/env bash

set -euo pipefail

ROOT="$(
	cd "$(dirname "${BASH_SOURCE[0]}")/.."
	pwd
)"

cd "$ROOT"

BINARY="build/reds"
APP="build/REDS.app"
CONTENTS="$APP/Contents"
MACOS="$CONTENTS/MacOS"
RESOURCES="$CONTENTS/Resources"

if [[ ! -x "$BINARY" ]]; then
	echo "Error: $BINARY does not exist." >&2
	echo "Run ./build.sh --build-only first." >&2
	exit 1
fi

echo "[app] Creating REDS.app..."

rm -rf "$APP"

mkdir -p \
	"$MACOS" \
	"$RESOURCES"

cp "$BINARY" \
	"$MACOS/reds"

cp "osx/Info.plist" \
	"$CONTENTS/Info.plist"

echo "[app] Copying REDS resources..."

cp -R \
	"resources" \
	"$RESOURCES/resources"

cp -R \
	"fonts" \
	"$RESOURCES/fonts"

mkdir -p \
	"$RESOURCES/asdex"

cp -R \
	"asdex/surface" \
	"$RESOURCES/asdex/surface"

chmod +x \
	"$MACOS/reds"

echo "[app] Created $APP"
