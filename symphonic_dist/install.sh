#!/bin/bash
# Symphonic v1.2 installer.
#
# This does NOT silently install everything for you — it builds the
# Symphonic binary from src/, then checks which external tools are
# missing and tells you how to get them, so you know exactly what's on
# your system.
#
# BUG FIX (previous version): this used to run
#   go build -o symphonic main.go
# which only compiles that single file. Once main.go's logic was split
# across config.go/tools.go/signals.go/plugins.go/results.go, that
# command failed with "undefined: loadConfig" and similar errors for
# every symbol living in the other files. The fix is building the whole
# package (`go build .` from inside src/), not one file from it.
set -euo pipefail

echo "== Symphonic v1.2 installer =="

if ! command -v go >/dev/null 2>&1; then
    echo "Go is not installed. Install it first: https://go.dev/dl/"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Building symphonic binary from src/..."
(
    cd "$SCRIPT_DIR/src"
    go build -o "$SCRIPT_DIR/symphonic" .
)
echo "Built $SCRIPT_DIR/symphonic"

echo ""
echo "Checking for external tools..."

check_tool() {
    local name="$1"
    local hint="$2"
    if command -v "$name" >/dev/null 2>&1; then
        echo "  [ok]      $name"
    else
        echo "  [missing] $name  ->  $hint"
    fi
}

check_tool "subfinder" "go install github.com/projectdiscovery/subfinder/v2/cmd/subfinder@latest"
check_tool "katana"    "go install github.com/projectdiscovery/katana/cmd/katana@latest"
check_tool "httpx"     "go install github.com/projectdiscovery/httpx/cmd/httpx@latest"
check_tool "ffuf"      "go install github.com/ffuf/ffuf/v2@latest"
check_tool "arjun"     "pip install arjun --break-system-packages"
check_tool "nuclei"    "go install github.com/projectdiscovery/nuclei/v3/cmd/nuclei@latest"
check_tool "dalfox"    "go install github.com/hahwul/dalfox/v2@latest"
check_tool "sqlmap"    "pip install sqlmap --break-system-packages   (or: git clone https://github.com/sqlmapproject/sqlmap.git)"

if ! command -v python3 >/dev/null 2>&1; then
    echo "  [missing] python3  ->  required for the plugin system, install via your OS package manager"
else
    echo "  [ok]      python3 (needed for plugins)"
fi

echo ""
if [ ! -f "$SCRIPT_DIR/conf.yaml" ]; then
    cp "$SCRIPT_DIR/conf.example.yaml" "$SCRIPT_DIR/conf.yaml"
    echo "Created conf.yaml from conf.example.yaml — edit it before running."
    echo "consent_confirmed is set to false by default. You must change it"
    echo "to true yourself, and only against targets you're authorized to test."
else
    echo "conf.yaml already exists, leaving it alone."
fi

echo ""
echo "Done. Edit conf.yaml, then run: ./symphonic"
