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
# Three rules, because each of the first two has a silent failure mode of its own.
#
#   1. Every HOME redirection needs a USERPROFILE redirection to the same
#      variable, nearby and IN THE SAME FUNCTION.
#   2. HOME must be bound to a VARIABLE, never an inline t.TempDir().
#   3. Both t.Setenv and os.Setenv count.
#
# Rule 2 is the subtle one. t.TempDir() returns a NEW directory on every call, so
#
#     t.Setenv("HOME", t.TempDir())
#     t.Setenv("USERPROFILE", t.TempDir())
#
# points the two at unrelated directories. On Windows the test then runs against
# a directory nothing populated -- strictly worse than the bug it was meant to
# fix, and it satisfies rule 1 while doing it. Three such pairs were already in
# the tree when this check was written.
#
# Rule 3 exists because the first draft matched only t.Setenv, which left two
# real os.Setenv("HOME") overrides in mcp/coverage_boost4_split2_test.go
# unisolated on Windows while this check reported the class closed. A guard that
# gives false assurance is worse than no guard (adversarial review, 2026-08-04).
#
# The same-function bound on rule 1 matters for the same reason: a plain
# line-window can be satisfied by the NEXT function's USERPROFILE, passing a
# broken site because an unrelated one below it is correct.
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
    if printf '%s' "$line" | grep -qE '(t|os)\.Setenv\("HOME", t\.TempDir\(\)\)'; then
      printf 'error: %s:%s binds HOME to an inline t.TempDir()\n' "$file" "$lineno" >&2
      printf '       t.TempDir() returns a NEW directory per call, so USERPROFILE cannot reuse it.\n' >&2
      printf '       Capture it first:  dir := t.TempDir(); t.Setenv("HOME", dir); t.Setenv("USERPROFILE", dir)\n' >&2
      fail=1
      continue
    fi

    var="$(printf '%s' "$line" | sed -nE 's/.*(t|os)\.Setenv\("HOME", ([A-Za-z_][A-Za-z0-9_]*)\).*/\2/p')"
    if [ -z "$var" ]; then
      printf 'error: %s:%s sets HOME to something this check cannot verify: %s\n' \
        "$file" "$lineno" "$(printf '%s' "$line" | sed -E 's/^[[:space:]]+//')" >&2
      printf '       Bind it to a variable so USERPROFILE can be set to the same directory.\n' >&2
      fail=1
      continue
    fi

    # A restore line -- os.Setenv("HOME", orig) inside a defer -- is the tail of
    # a manual save/restore, not a redirection. The redirection itself is checked
    # on its own line; requiring USERPROFILE here too would demand it twice.
    if printf '%s' "$line" | grep -q 'defer'; then
      continue
    fi

    # Rule 1: same variable must reach USERPROFILE within a short window, and
    # the window stops at the end of the enclosing function so a later
    # function's correct pairing cannot vouch for a broken site above it.
    window=""
    end=$((lineno + 6))
    cur=$((lineno + 1))
    while [ "$cur" -le "$end" ]; do
      wline="$(sed -n "${cur}p" "$file")"
      # Column-0 '}' closes the function; 'func ' opens the next one.
      case "$wline" in
        '}'*) break ;;
        'func '*) break ;;
      esac
      window="${window}
${wline}"
      cur=$((cur + 1))
    done

    if ! printf '%s' "$window" | grep -qE "(t|os)\.Setenv\(\"USERPROFILE\", ${var}\)"; then
      printf 'error: %s:%s sets HOME=%s with no matching Setenv("USERPROFILE", %s) in the same function\n' \
        "$file" "$lineno" "$var" "$var" >&2
      printf '       os.UserHomeDir reads USERPROFILE on Windows, so this test does not isolate there.\n' >&2
      fail=1
    fi
  done < <(grep -nE '(t|os)\.Setenv\("HOME"' "$file" 2>/dev/null || true)
done < <(git ls-files '*_test.go' ':!:vendor/**' ':!:third_party/**')

if [ "$fail" -eq 0 ]; then
  printf 'ok: %d HOME redirections also set USERPROFILE to the same directory\n' "$checked"
fi

exit "$fail"
