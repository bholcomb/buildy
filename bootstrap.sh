#!/bin/bash
# Bootstrap script for building buildy
# Uses buildy to build itself, then copies the result to bin/
# This avoids the "text file busy" error when buildy tries to overwrite itself

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Build configuration
CONFIG="${1:-release}"

echo "Building buildy ($CONFIG)..."

# Check if buildy exists, if not do initial build with go
if [ ! -x "bin/buildy" ]; then
    echo "No existing buildy found, performing initial build with go..."
    BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
    LDFLAGS="-X main.BuildTime=$BUILD_TIME -X main.GitCommit=$GIT_COMMIT -X main.BuildConfig=$CONFIG -s -w"
    mkdir -p bin
    (cd src && go build -o ../bin/buildy -ldflags "$LDFLAGS" .)
    echo "✓ Initial build complete"
fi

# Use buildy to build itself
./bin/buildy --config "$CONFIG"

# Determine build output location
PLATFORM="linux"
ARCH="x86_64"
BUILD_DIR="build/${PLATFORM}-${ARCH}-${CONFIG}"

# Copy the built binary to bin/ (buildy has exited, so no "text file busy")
if [ -f "${BUILD_DIR}/bin/buildy" ]; then
    cp "${BUILD_DIR}/bin/buildy" bin/buildy
    echo "✓ Copied to bin/buildy"
else
    echo "ERROR: Build output not found at ${BUILD_DIR}/bin/buildy"
    exit 1
fi

# Cross-compile for Windows x64
echo "Cross-compiling for Windows x64..."
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
LDFLAGS="-X main.BuildTime=$BUILD_TIME -X main.GitCommit=$GIT_COMMIT -X main.BuildConfig=$CONFIG -s -w"
(cd src && GOOS=windows GOARCH=amd64 go build -o ../bin/buildy.exe -ldflags "$LDFLAGS" .)
echo "✓ Windows build complete: bin/buildy.exe"

# Show version info
./bin/buildy --help 2>&1 | head -4
