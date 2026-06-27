#!/usr/bin/env bash
# Refresh the local GitNexus index if (and only if) it is stale.
#
# The 222MB index lives in .gitnexus/ (gitignored) and is fully derived from
# source — it is never committed. This script regenerates it from the current
# checkout. By default it discards the cosmetic symbol-count churn GitNexus
# writes into AGENTS.md / CLAUDE.md so `git status` stays clean; pass --commit
# to keep those edits (e.g. when you deliberately want to refresh the counts
# inside a PR).
#
#   refresh.sh           # re-analyze only if stale, keep tree clean
#   refresh.sh --force   # re-analyze regardless of staleness
#   refresh.sh --commit  # re-analyze and KEEP the AGENTS.md/CLAUDE.md count edits
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

force=0; keep_counts=0
for a in "$@"; do
  case "$a" in
    --force)  force=1 ;;
    --commit) keep_counts=1 ;;
  esac
done

if [ "$force" -eq 0 ] && scripts/gitnexus/check-staleness.sh >/dev/null 2>&1; then
  echo "gitnexus: index already fresh; nothing to do"
  exit 0
fi

gitnexus_version="${GITNEXUS_VERSION:-1.6.8}"
echo "gitnexus: re-analyzing (npx gitnexus@$gitnexus_version analyze)..."
npx --yes "gitnexus@$gitnexus_version" analyze

if [ "$keep_counts" -eq 0 ]; then
  # Index (.gitnexus/, gitignored) is updated in place; drop the cosmetic
  # count edits so the working tree is not left dirty after a hook-triggered run.
  git checkout -- AGENTS.md CLAUDE.md 2>/dev/null || true
fi

echo "gitnexus: index refreshed to $(git rev-parse --short HEAD)"
