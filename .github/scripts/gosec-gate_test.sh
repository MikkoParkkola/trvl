#!/usr/bin/env bash
# Tests for gosec-gate.sh. Fixture-driven: no gosec run required, so this is
# safe to run before the scan and on any machine.
#
# Run: .github/scripts/gosec-gate_test.sh
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
gate="$here/gosec-gate.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

failures=0
LAST_OUTPUT=""

# Records one HIGH G704 in internal/providers/auth.go plus one ungated MEDIUM.
cat >"$tmp/baseline.json" <<'JSON'
{
  "entries": [
    {
      "rule": "G704",
      "file": "internal/providers/auth.go",
      "count": 1,
      "status": "deferred",
      "reason": "SSRF via taint analysis; verdict owed under issue #532 TRVL.GOSEC.2."
    }
  ]
}
JSON

report() {
  # report <outfile> <json-array-of-issues> [<golang-errors-object>]
  local errs=${3:-}
  [ -n "$errs" ] || errs='{}'
  printf '{"Issues": %s, "Golang errors": %s, "Stats": {"found": 0}}\n' \
    "$2" "$errs" >"$1"
}

issue() {
  # issue <rule> <severity> <file>
  printf '{"rule_id":"%s","severity":"%s","confidence":"HIGH","file":"%s","line":"1","details":"x","code":"x"}' \
    "$1" "$2" "$3"
}

expect() {
  # expect <name> <want-exit> <report-file> [<baseline-file>]
  local name=$1 want=$2 rep=$3 base=${4:-$tmp/baseline.json} out got
  out="$(GOSEC_REPO_ROOT=/repo GOSEC_BASELINE="$base" "$gate" "$rep" 2>&1)"
  got=$?
  if [ "$got" -ne "$want" ]; then
    printf 'FAIL %s: want exit %s, got %s\n%s\n' "$name" "$want" "$got" "$out" >&2
    failures=$((failures + 1))
    return
  fi
  printf 'ok   %s (exit %s)\n' "$name" "$got"
  LAST_OUTPUT="$out"
}

# 1. Exactly the baselined HIGH and nothing else: pass.
report "$tmp/match.json" "[$(issue G704 HIGH /repo/internal/providers/auth.go)]"
expect "baselined HIGH alone passes" 0 "$tmp/match.json"

# 2. A HIGH in a (rule, file) pair the baseline has never seen: fail.
#    This is the assertion the whole gate exists for.
report "$tmp/newpair.json" "[$(issue G704 HIGH /repo/internal/providers/auth.go),$(issue G404 HIGH /repo/internal/newthing/rng.go)]"
expect "unbaselined HIGH fails the build" 1 "$tmp/newpair.json"
case "$LAST_OUTPUT" in
  *"G404 internal/newthing/rng.go"*) ;;
  *) echo "FAIL: failure message does not name the offending rule and file" >&2
     failures=$((failures + 1)) ;;
esac

# 3. A second HIGH of an already-baselined rule in the same file: fail.
report "$tmp/extra.json" "[$(issue G704 HIGH /repo/internal/providers/auth.go),$(issue G704 HIGH /repo/internal/providers/auth.go)]"
expect "extra HIGH in a baselined file fails the build" 1 "$tmp/extra.json"

# 4. New MEDIUM findings do not gate. Gating MEDIUM on day one would block
#    every PR against 99 pre-existing findings; issue #532 TRVL.GOSEC.4 owns them.
report "$tmp/medium.json" "[$(issue G704 HIGH /repo/internal/providers/auth.go),$(issue G304 MEDIUM /repo/internal/newthing/read.go),$(issue G306 LOW /repo/internal/newthing/write.go)]"
expect "new MEDIUM and LOW do not fail the build" 0 "$tmp/medium.json"

# 5. Someone fixed the baselined finding: warn, never fail. A source fix landing
#    while the baseline still lists it must not red-fail CI.
report "$tmp/fixed.json" "[]"
expect "fixed finding warns instead of failing" 0 "$tmp/fixed.json"
case "$LAST_OUTPUT" in
  *"baseline is now looser than reality"*) ;;
  *) echo "FAIL: stale baseline entry produced no notice" >&2
     failures=$((failures + 1)) ;;
esac

# 6. A suppression with no written reason is itself a failure.
cat >"$tmp/noreason.json" <<'JSON'
{
  "entries": [
    {"rule": "G704", "file": "internal/providers/auth.go", "count": 1, "status": "accepted", "reason": "ok"}
  ]
}
JSON
expect "suppression without a reason fails the build" 1 "$tmp/match.json" "$tmp/noreason.json"

# 7. A missing report is an operator error, not a silent pass.
expect "missing report file exits 2" 2 "$tmp/does-not-exist.json"

# 8. gosec could not compile a package, so it reported no findings for it. The
#    finding set is smaller than reality and the gate must not read that as
#    clean. Observed for real on this repo: an unused import in
#    internal/flights made two baselined G115 findings disappear.
report "$tmp/broken.json" "[$(issue G704 HIGH /repo/internal/providers/auth.go)]" \
  '{"/repo/internal/flights/x.go": [{"line": 30, "column": 2, "error": "imported and not used"}]}'
expect "incomplete scan fails the build" 1 "$tmp/broken.json"
case "$LAST_OUTPUT" in
  *"internal/flights/x.go"*) ;;
  *) echo "FAIL: incomplete-scan message does not name the uncompilable file" >&2
     failures=$((failures + 1)) ;;
esac

# 9. The report was produced under a different root, so relpath cannot strip the
#    prefix and every path stays absolute. Left unchecked that reads as 24 brand
#    new HIGH findings and looks like a broken gate; it must name the real cause.
report "$tmp/otherroot.json" "[$(issue G704 HIGH /elsewhere/internal/providers/auth.go)]"
expect "unresolved paths report the root mismatch" 2 "$tmp/otherroot.json"
case "$LAST_OUTPUT" in
  *"did not resolve relative to"*) ;;
  *) echo "FAIL: root mismatch was not diagnosed as such" >&2
     failures=$((failures + 1)) ;;
esac

if [ "$failures" -ne 0 ]; then
  printf '\n%s gosec-gate test(s) failed\n' "$failures" >&2
  exit 1
fi
echo
echo "all gosec-gate tests passed"
