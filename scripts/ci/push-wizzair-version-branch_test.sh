#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
helper="$repo_root/scripts/ci/push-wizzair-version-branch.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

git init --bare "$tmp/remote.git" >/dev/null
git init -b main "$tmp/work" >/dev/null
git -C "$tmp/work" config user.name test
git -C "$tmp/work" config user.email test@example.com
git -C "$tmp/work" remote add origin "$tmp/remote.git"

printf 'base\n' >"$tmp/work/value"
git -C "$tmp/work" add value
git -C "$tmp/work" commit -m base >/dev/null

branch="auto/wizzair-version-99.1.0"
git -C "$tmp/work" switch -c "$branch" >/dev/null

# A missing automation branch is created normally.
(cd "$tmp/work" && "$helper" "$branch")
local_sha="$(git -C "$tmp/work" rev-parse HEAD)"
remote_sha="$(git --git-dir="$tmp/remote.git" rev-parse "refs/heads/$branch")"
test "$remote_sha" = "$local_sha"

# Simulate an orphan branch advancing independently after a failed workflow.
git clone --branch "$branch" "$tmp/remote.git" "$tmp/rival" >/dev/null 2>&1
git -C "$tmp/rival" config user.name rival
git -C "$tmp/rival" config user.email rival@example.com
printf 'rival\n' >>"$tmp/rival/value"
git -C "$tmp/rival" commit -am rival >/dev/null
git -C "$tmp/rival" push origin "$branch" >/dev/null
rival_sha="$(git -C "$tmp/rival" rev-parse HEAD)"
git -C "$tmp/work" fetch origin "$branch" >/dev/null

printf 'current-main\n' >>"$tmp/work/value"
git -C "$tmp/work" commit -am current-main >/dev/null
replacement_sha="$(git -C "$tmp/work" rev-parse HEAD)"
test "$replacement_sha" != "$rival_sha"
git -C "$tmp/work" merge-base --is-ancestor "$rival_sha" "$replacement_sha" && {
  echo "test setup error: replacement unexpectedly descends from orphan branch" >&2
  exit 1
}

(cd "$tmp/work" && "$helper" "$branch")
remote_sha="$(git --git-dir="$tmp/remote.git" rev-parse "refs/heads/$branch")"
test "$remote_sha" = "$replacement_sha"

# The lease-based force path is restricted to the sentinel-owned namespace.
if (cd "$tmp/work" && "$helper" "feature/not-automation"); then
  echo "helper accepted a non-sentinel branch" >&2
  exit 1
fi

echo "ok: Wizz sentinel branch push handles new and orphaned branches safely"
