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

# symbol:declaring-file[,also-allowed-file...]
#
# A symbol may legitimately appear in more than one production file -- a struct
# field is declared and read where the behaviour lives, and set where the test
# helper is built. Listing every allowed home explicitly keeps the rule "these
# exact files and tests, nothing else" rather than widening it to a directory.
HELPERS=(
  "NewTestClient:internal/batchexec/testclient.go"
  # The transport NewTestClient installs. Guarded for the same reason as the
  # constructor: reaching it directly is the same bypass with one less step.
  "testRedirectTransport:internal/batchexec/testclient.go"
  # The flag that decides whether stealthClient reuses the plain transport.
  # Declared and read in client.go, set only by the test-client constructor. A
  # production file setting it true would silently disable the real Chrome
  # fingerprint for whichever client it touched -- the flag defaults safe
  # (asserted in stealth_flag_test.go), and this keeps it from being set unsafe
  # somewhere else in the package. Unexported, so the compiler bounds the blast
  # radius to this package; this bounds it to these two files.
  "reuseTransportForStealth:internal/batchexec/client.go,internal/batchexec/testclient.go"
  # The registry constructor that still LOADS AND RUNS provider definitions from
  # ~/.trvl/providers/*.json. #538 removed runtime custom providers by having
  # production use NewRegistry, which is source-only; the executable loader was
  # left in the binary for tests that need a registry backed by a temp
  # directory. So the trust boundary is currently "production happens to call
  # the other constructor" -- a convention, not a compiler rule, and exactly the
  # shape TRVL.HARDEN.2 above exists to catch.
  #
  # Raised by adversarial review of #587: the dangerous loader remains
  # reachable, and only entrypoint discipline keeps it unused. This makes that
  # discipline enforced instead of assumed.
  "NewRegistryAt:internal/providers/registry.go"
)

fail=0
checked=0

for spec in "${HELPERS[@]}"; do
  symbol="${spec%%:*}"
  homes="${spec#*:}"

  missing=0
  IFS=',' read -r -a home_list <<< "$homes"
  for home in "${home_list[@]}"; do
    if [ ! -f "$home" ]; then
      printf 'error: %s is declared to live in %s, which does not exist\n' "$symbol" "$home" >&2
      printf '       Update scripts/ci/check-test-only-helpers.sh if the helper moved.\n' >&2
      fail=1
      missing=1
    fi
  done
  [ "$missing" -eq 0 ] || continue
  checked=$((checked + 1))

  # Run the search FIRST and check its status, rather than feeding the loop
  # straight from a process substitution.
  #
  # The old line ended `... 2>/dev/null || true)`, which reported a BROKEN
  # search as a clean repository. git grep exits 1 for "no matches" -- ordinary
  # and expected -- and 2 or more for a real failure: bad pathspec, unreadable
  # object, not a work tree. Swallowing both meant any breakage here produced an
  # empty loop and the "ok: N helper(s) referenced from tests only" success
  # line, with N counted from the symbol list rather than from anything actually
  # searched. A guard that cannot tell "found nothing" from "could not look" is
  # not a guard, and this one reports on whether a test-only network helper
  # reached production code.
  #
  # The status cannot be checked through `< <(...)`: a process substitution's
  # exit status is not available to the redirecting command, so the failure
  # would still be invisible there.
  set +e
  matches="$(git grep -l -F -e "$symbol" -- '*.go' ':!:vendor/**' ':!:third_party/**' 2>/dev/null)"
  grep_status=$?
  set -e
  if [ "$grep_status" -gt 1 ]; then
    printf 'error: git grep failed (exit %d) while searching for %s\n' "$grep_status" "$symbol" >&2
    printf '       Refusing to report a clean result from a search that did not run.\n' >&2
    fail=1
    continue
  fi

  while IFS= read -r file; do
    [ -n "$file" ] || continue
    case "$file" in
      *_test.go) continue ;;   # tests are the whole point
    esac
    allowed=0
    for home in "${home_list[@]}"; do
      [ "$file" = "$home" ] && allowed=1
    done
    [ "$allowed" -eq 1 ] && continue
    printf 'error: %s references the test-only helper %s\n' "$file" "$symbol" >&2
    printf '       That helper redirects requests to a local test server and exists for tests only.\n' >&2
    printf '       A production caller makes a baselined gosec finding live without changing the\n' >&2
    printf '       baseline count, so the security gate would not notice.\n' >&2
    fail=1
  done <<< "$matches"
done

if [ "$fail" -eq 0 ]; then
  printf 'ok: %d test-only helper(s) referenced from tests only\n' "$checked"
fi

exit "$fail"
