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
ICON_SOURCE="cmd/reds/icons/reds-1024x1024.png"
ICONSET="build/reds.iconset"
ICON="$RESOURCES/reds.icns"

if [[ ! -x "$BINARY" ]]; then
	echo "Error: $BINARY does not exist." >&2
	echo "Run ./build.sh --build-only first." >&2
	exit 1
fi

if [[ "$(uname -s)" != "Darwin" ]]; then
	echo "Error: REDS.app can only be packaged on macOS." >&2
	exit 1
fi

if [[ ! -f "$ICON_SOURCE" ]]; then
	echo "Error: missing macOS icon source: $ICON_SOURCE" >&2
	exit 1
fi

for tool in sips iconutil plutil; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "Error: required macOS tool not found: $tool" >&2
		exit 1
	fi
done

echo "[app] Creating REDS.app..."

rm -rf "$APP"

mkdir -p \
	"$MACOS" \
	"$RESOURCES"

cp "$BINARY" \
	"$MACOS/reds"

cp "osx/Info.plist" \
	"$CONTENTS/Info.plist"

echo "[app] Creating application icon..."

rm -rf "$ICONSET"
mkdir -p "$ICONSET"

make_icon() {
	local pixels="$1"
	local filename="$2"

	sips \
		-s format png \
		-z "$pixels" "$pixels" \
		"$ICON_SOURCE" \
		--out "$ICONSET/$filename" \
		>/dev/null
}

make_icon 16 "icon_16x16.png"
make_icon 32 "icon_16x16@2x.png"

make_icon 32 "icon_32x32.png"
make_icon 64 "icon_32x32@2x.png"

make_icon 128 "icon_128x128.png"
make_icon 256 "icon_128x128@2x.png"

make_icon 256 "icon_256x256.png"
make_icon 512 "icon_256x256@2x.png"

make_icon 512 "icon_512x512.png"
make_icon 1024 "icon_512x512@2x.png"

if ! iconutil \
	-c icns \
	"$ICONSET" \
	-o "$ICON" \
	2>/dev/null; then

	echo "[app] iconutil rejected the iconset; writing ICNS directly..." >&2

	if ! command -v python3 >/dev/null 2>&1; then
		echo "Error: python3 is required for ICNS fallback generation." >&2
		exit 1
	fi

	python3 - "$ICONSET" "$ICON" <<'PY'
import pathlib
import struct
import sys

iconset = pathlib.Path(sys.argv[1])
output = pathlib.Path(sys.argv[2])

entries = [
    ("icp4", "icon_16x16.png"),
    ("icp5", "icon_32x32.png"),
    ("icp6", "icon_32x32@2x.png"),
    ("ic07", "icon_128x128.png"),
    ("ic08", "icon_256x256.png"),
    ("ic09", "icon_512x512.png"),
    ("ic10", "icon_512x512@2x.png"),
    ("ic11", "icon_16x16@2x.png"),
    ("ic12", "icon_32x32@2x.png"),
    ("ic13", "icon_128x128@2x.png"),
    ("ic14", "icon_256x256@2x.png"),
]

chunks = []
for icon_type, filename in entries:
    data = (iconset / filename).read_bytes()
    chunks.append(
        icon_type.encode("ascii") +
        struct.pack(">I", len(data) + 8) +
        data
    )

payload = b"".join(chunks)
output.write_bytes(
    b"icns" +
    struct.pack(">I", len(payload) + 8) +
    payload
)
PY
fi

rm -rf "$ICONSET"

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

echo "[app] Validating application bundle..."

plutil \
	-lint \
	"$CONTENTS/Info.plist"

test -x \
	"$MACOS/reds"

test -f \
	"$RESOURCES/reds.icns"

test -d \
	"$RESOURCES/resources/videomaps/asdex"

test -d \
	"$RESOURCES/resources/configs/asdex"

test -d \
	"$RESOURCES/resources/audio/asdex"

test -d \
	"$RESOURCES/fonts"

test -d \
	"$RESOURCES/asdex/surface"

echo "[app] Created $APP"
echo "[app] Binary: $MACOS/reds"
echo "[app] Resources: $RESOURCES"
