#!/usr/bin/env bash

set -euo pipefail

VERSION="$(
	tr -d '[:space:]' \
		< VERSION
)"

if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9]+(\.[A-Za-z0-9]+)*)?$ ]]; then
	echo "Invalid VERSION: $VERSION" >&2
	echo "Expected x.y.z or x.y.z-prerelease, e.g. 0.1.0 or 0.1.0-beta.1" >&2
	exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
	echo "Working tree is dirty." >&2
	git status --short
	exit 1
fi

echo "[release] gofmt"
if [[ -n "$(gofmt -l .)" ]]; then
	echo "The following Go files need gofmt:" >&2
	gofmt -l . >&2
	exit 1
fi

echo "[release] go test"
go test ./...

echo "[release] VERSION=$VERSION"
echo "[release] tag commands:"
echo "  git tag $VERSION"
echo "  git push origin $VERSION"
