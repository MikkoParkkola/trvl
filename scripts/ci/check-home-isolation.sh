#!/usr/bin/env bash
# Keep test home-directory redirection portable.
#
# os.UserHomeDir reads HOME on unix and USERPROFILE on Windows. A test that sets
# only HOME does not isolate on Windows: it keeps whatever home the package-wide
# TestMain established and shares a store with every sibling test in the package.
# That is invisible until a test asserts on the store's CONTENTS, which is how
# TestRunWatchCheckCycleWithRooms_SkipsInactiveWatches failed on windows-latest
# with count=3 while passing on Linux and macOS throughout (trvl#565).
#
# Two rules, because the obvious fix has a silent failure mode of its own:
#
#   1. Every t.Setenv("HOME", ...) needs a t.Setenv("USERPROFILE", ...) nearby.
#   2. HOME must be bound to a VARIABLE, never an inline t.TempDir().
#
# Rule 2 is the subtle one. t.TempDir() returns a NEW directory on every call,
# so the natural-looking
#
#     t.Setenv("HOME", t.TempDir())
#     t.Setenv("USERPROFILE", t.TempDir())
#
# points the two at unrelated directories. On Windows the test then runs against
# a directory nothing populated -- strictly worse than the bug it was meant to
# fix, and it satisfies rule 1 while doing it. Three such pairs were already in
# the tree when this check was written.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

fail=0
checked=0

while IFS= read -r file; do
  while IFS=: read -r lineno _; do
    [ -n "$lineno" ] || continue
    checked=$((checked + 1))
    line="$(sed -n "${lineno}p" "$file")"

    # Rule 2: HOME must name a variable, so USERPROFILE can reuse the same one.
    if printf '%s' "$line" | grep -q 't\.Setenv("HOME", t\.TempDir())'; then
      printf 'error: %s:%s binds HOME to an inline t.TempDir()\n' "$file" "$lineno" >&2
      printf '       t.TempDir() returns a NEW directory per call, so USERPROFILE cannot reuse it.\n' >&2
      printf '       Capture it first:  dir := t.TempDir(); t.Setenv("HOME", dir); t.Setenv("USERPROFILE", dir)\n' >&2
      fail=1
      continue
    fi

    var="$(printf '%s' "$line" | sed -nE 's/.*t\.Setenv\("HOME", ([A-Za-z_][A-Za-z0-9_]*)\).*/\1/p')"
    if [ -z "$var" ]; then
      printf 'error: %s:%s sets HOME to something this check cannot verify: %s\n' \
        "$file" "$lineno" "$(printf '%s' "$line" | sed -E 's/^[[:space:]]+//')" >&2
      printf '       Bind it to a variable so USERPROFILE can be set to the same directory.\n' >&2
      fail=1
      continue
    fi

    # Rule 1: the same variable must reach USERPROFILE within a short window.
    # A window rather than the next line, because existing call sites legitimately
    # separate them with a comment or a `if runtime.GOOS == "windows"` guard.
    window="$(sed -n "${lineno},$((lineno + 6))p" "$file")"
    if ! printf '%s' "$window" | grep -q "t\.Setenv(\"USERPROFILE\", ${var})"; then
      printf 'error: %s:%s sets HOME=%s with no matching t.Setenv("USERPROFILE", %s) nearby\n' \
        "$file" "$lineno" "$var" "$var" >&2
      printf '       os.UserHomeDir reads USERPROFILE on Windows, so this test does not isolate there.\n' >&2
      fail=1
    fi
  done < <(grep -n 't\.Setenv("HOME"' "$file" 2>/dev/null || true)
done < <(git ls-files '*_test.go' ':!:vendor/**' ':!:third_party/**')

if [ "$fail" -eq 0 ]; then
  printf 'ok: %d HOME redirections also set USERPROFILE to the same directory\n' "$checked"
fi

exit "$fail"
