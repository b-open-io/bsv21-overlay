#!/bin/bash

# BSV21 Config UI Launcher
# Starts the Wails-based configuration UI for BSV21 overlay

cd "$(dirname "$0")/bsv21-config-ui"

# Check if wails is available
if ! command -v wails &> /dev/null; then
    # Try the Go bin directory
    if [ -f "$HOME/go/bin/wails" ]; then
        echo "Using wails from $HOME/go/bin/wails"
        "$HOME/go/bin/wails" dev
    else
        echo "Error: wails not found. Please install with: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
        exit 1
    fi
else
    echo "Starting BSV21 Config UI..."
    wails dev
fi