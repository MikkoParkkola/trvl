#!/usr/bin/env bash
# Exit 0 if the local GitNexus index matches HEAD, 1 if stale/missing.
# Staleness is decided by .gitnexus/meta.json:lastCommit vs git HEAD —
# NOT by the symbol counts in AGENTS.md/CLAUDE.md (those are cosmetic).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

meta=".gitnexus/meta.json"
if [ ! -f "$meta" ]; then
  echo "gitnexus: stale (no index on disk)"
  exit 1
fi

indexed="$(jq -r '.lastCommit // ""' "$meta" 2>/dev/null || echo "")"
head="$(git rev-parse HEAD)"

if [ "$indexed" = "$head" ]; then
  echo "gitnexus: fresh (index at ${head:0:12})"
  exit 0
fi
echo "gitnexus: stale (index at ${indexed:-none}, HEAD at ${head:0:12})"
exit 1
