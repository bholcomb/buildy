#!/bin/bash
# Bootstrap script for building buildy
# Builds Linux and Windows binaries and places them in bin/

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

mkdir -p bin

BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
LDFLAGS="-X main.BuildTime=$BUILD_TIME -X main.GitCommit=$GIT_COMMIT -X main.BuildConfig=release -s -w"

echo "Building buildy for Linux x64..."
(cd src && go build -o ../bin/buildy -ldflags "$LDFLAGS" .)
echo "Done: bin/buildy"

echo "Building buildy for Windows x64..."
(cd src && GOOS=windows GOARCH=amd64 go build -o ../bin/buildy.exe -ldflags "$LDFLAGS" .)
echo "Done: bin/buildy.exe"

./bin/buildy --help 2>&1 | head -4
