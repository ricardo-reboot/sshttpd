#!/bin/bash
# Build sshweb-mcp binary for the current platform into plugin/bin/.
# For distribution, builds all supported platforms.
#
# Usage:
#   ./scripts/build.sh              # current platform only
#   ./scripts/build.sh --all        # all platforms

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(dirname "$PLUGIN_DIR")"
BIN_DIR="$PLUGIN_DIR/bin"

mkdir -p "$BIN_DIR"

build_for() {
    local os=$1 arch=$2 suffix=$3
    echo "Building sshweb-mcp for ${os}/${arch}..."
    GOOS=$os GOARCH=$arch go build -o "${BIN_DIR}/sshweb-mcp${suffix}" "$REPO_ROOT/cmd/sshweb-mcp/"
}

if [ "$1" = "--all" ]; then
    build_for darwin  arm64 "-darwin-arm64"
    build_for darwin  amd64 "-darwin-amd64"
    build_for linux   amd64 "-linux-amd64"
    build_for linux   arm64 "-linux-arm64"
    echo "All builds complete in $BIN_DIR"
else
    go build -o "${BIN_DIR}/sshweb-mcp" "$REPO_ROOT/cmd/sshweb-mcp/"
    echo "Built ${BIN_DIR}/sshweb-mcp"
fi
