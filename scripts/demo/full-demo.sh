#!/usr/bin/env bash
set -euo pipefail

demo_home="${TRVL_DEMO_HOME:-/tmp/trvl-demo-home}"
rm -rf "$demo_home"

echo "# install trvl as an MCP server (dry run)"
echo "\$ HOME=$demo_home trvl mcp install --client codex --dry-run"
HOME="$demo_home" trvl mcp install --client codex --dry-run
echo

echo "# one prompt: flights + hotel detail + ground + hacks + optional watch"
echo "$ scripts/demo/one-prompt-demo.sh"
scripts/demo/one-prompt-demo.sh
echo

echo "# 1 smart MCP tool · 66 legacy-compatible capabilities · 56 CLI commands · 24 providers · No API keys"
