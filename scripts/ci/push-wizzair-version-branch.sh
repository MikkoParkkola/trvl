#!/usr/bin/env bash
set -euo pipefail

branch=${1:?usage: push-wizzair-version-branch.sh <auto/wizzair-version-X.Y.Z>}
if [[ ! $branch =~ ^auto/wizzair-version-[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "refusing to update non-sentinel branch: $branch" >&2
  exit 2
fi

remote_ref="refs/heads/$branch"
remote_sha="$(git ls-remote --heads origin "$remote_ref" | awk 'NR == 1 { print $1 }')"
if [ -z "$remote_sha" ]; then
  git push -u origin "HEAD:$remote_ref"
  exit 0
fi

# A prior run may have pushed the version bump and then failed before opening a
# PR. Refresh that sentinel-owned orphan from the current main checkout without
# weakening the lease: if another process updates it after ls-remote, this push
# fails instead of overwriting the newer work.
git push -u origin "HEAD:$remote_ref" \
  --force-with-lease="$remote_ref:$remote_sha"
