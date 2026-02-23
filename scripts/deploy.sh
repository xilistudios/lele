#!/bin/bash
# Picoclaw Build & Deploy Script
# Builds the binary with Go and deploys it to the system

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY_NAME="picoclaw"
BUILD_DIR="$SCRIPT_DIR/build"
INSTALL_PATH="/usr/local/bin/$BINARY_NAME"
SERVICE_NAME="picoclaw"

echo "🦞 Picoclaw Build & Deploy"
echo "=========================="

# Check if Go is available
if ! command -v go &> /dev/null; then
    if [ -f "/usr/local/go/bin/go" ]; then
        export PATH=$PATH:/usr/local/go/bin
    else
        echo "❌ Go not found. Install Go first."
        exit 1
    fi
fi

echo "✓ Go version: $(go version)"

# Build
echo ""
echo "📦 Building..."
cd "$SCRIPT_DIR"
go build -o "$BUILD_DIR/$BINARY_NAME" ./cmd/picoclaw

if [ $? -eq 0 ]; then
    echo "✓ Build successful: $BUILD_DIR/$BINARY_NAME"
else
    echo "❌ Build failed"
    exit 1
fi

# Stop service if running
echo ""
echo "🛑 Stopping service..."
if sudo systemctl is-active --quiet "$SERVICE_NAME"; then
    sudo systemctl stop "$SERVICE_NAME"
    echo "✓ Service stopped"
else
    echo "ℹ Service not running"
fi

# Replace binary
echo ""
echo "📥 Installing binary..."
sudo cp "$BUILD_DIR/$BINARY_NAME" "$INSTALL_PATH"
sudo chmod +x "$INSTALL_PATH"
echo "✓ Binary installed to $INSTALL_PATH"

# Start service
echo ""
echo "🚀 Starting service..."
sudo systemctl start "$SERVICE_NAME"
sleep 2

# Check status
if sudo systemctl is-active --quiet "$SERVICE_NAME"; then
    echo "✓ Service started successfully"
    echo ""
    echo "📊 Status:"
    sudo systemctl status "$SERVICE_NAME" --no-pager | head -10
    echo ""
    echo "✅ Deploy complete!"
else
    echo "❌ Service failed to start"
    sudo systemctl status "$SERVICE_NAME" --no-pager | tail -20
    exit 1
fi
