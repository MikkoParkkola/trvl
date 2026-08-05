#!/usr/bin/env bash
# Keep test-only helpers out of production call paths.
#
# Some helpers exist purely to let tests drive a package offline -- a client
# whose transport redirects every request to a local test server, for instance.
# They are exported because tests in OTHER packages need them, which means the
# compiler cannot keep production from calling them.
#
# That is not academic. gosec's G704 finding on batchexec's redirect transport
# was baselined as NOT REACHABLE, and the reasoning was "the only callers are
# *_test.go files". True at the time, and a property nothing enforced: a future
# production caller would have made the finding live WITHOUT changing the
# baseline count, so the gate would have stayed green (trvl#539, TRVL.HARDEN.2).
#
# The stronger fix -- move the helper into a _test.go file so a production
# caller fails to compile -- was tried and does not work here. _test.go is
# compiled only when testing its own package, so importers' tests stop building.
# The issue named this fallback explicitly: "item 2 becomes a lint rule instead
# of a move."
#
# So this is the rule. A helper listed below may be referenced from *_test.go
# files and from its own declaration file, and nowhere else.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# symbol:declaring-file
HELPERS=(
  "NewTestClient:internal/batchexec/testclient.go"
)

fail=0
checked=0

for spec in "${HELPERS[@]}"; do
  symbol="${spec%%:*}"
  home="${spec#*:}"

  if [ ! -f "$home" ]; then
    printf 'error: %s is declared to live in %s, which does not exist\n' "$symbol" "$home" >&2
    printf '       Update scripts/ci/check-test-only-helpers.sh if the helper moved.\n' >&2
    fail=1
    continue
  fi
  checked=$((checked + 1))

  while IFS= read -r file; do
    case "$file" in
      *_test.go) continue ;;   # tests are the whole point
      "$home") continue ;;     # its own declaration
    esac
    printf 'error: %s references the test-only helper %s\n' "$file" "$symbol" >&2
    printf '       That helper redirects requests to a local test server and exists for tests only.\n' >&2
    printf '       A production caller makes a baselined gosec finding live without changing the\n' >&2
    printf '       baseline count, so the security gate would not notice.\n' >&2
    fail=1
  done < <(git grep -l -F -e "$symbol" -- '*.go' ':!:vendor/**' ':!:third_party/**' 2>/dev/null || true)
done

if [ "$fail" -eq 0 ]; then
  printf 'ok: %d test-only helper(s) referenced from tests only\n' "$checked"
fi

exit "$fail"
