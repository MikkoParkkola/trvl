#!/usr/bin/env bash
# Run CI's pull-request gates locally, cheapest first, and stop at the first
# failure, so the verdict describes the committed HEAD before it is pushed.
#
# Runs, with the commands and pinned versions CI uses (ci.yaml and the
# regression-test-gate workflow):
#   go vet; golangci-lint (pinned version); the six workflow-hygiene scripts;
#   the regression-test gate (same logic as the workflow); goreleaser check,
#   the Homebrew formula test (ruby) and both node --test suites, each when
#   the tool is installed; GOOS=windows GOARCH=amd64 go vet; the race tests
#   with the 80% coverage floor; the 60 s FuzzBuildFilters step; the offline
#   guard when strace is installed (Linux).
# Not covered, run them separately: staticcheck and govulncheck (make lint),
#   gosec (make security-gosec), and CI's osv-scanner and secrets-scan jobs.
# A gate whose tool is missing is reported as SKIPPED in the final summary.
#
# Usage: make prepush   (or scripts/ci/prepush.sh)
#   PREPUSH_BASE             base branch for the regression gate (origin/main)
#   PREPUSH_TITLE            the PR title, which the regression gate also reads
#   PREPUSH_SKIP_REGRESSION  1 skips the regression gate, like CI's
#                            skip-regression-test label
#   PREPUSH_SKIP_TESTS       1 skips the race tests, the fuzz step and the
#                            offline guard (all run the suite), to check the
#                            gate's shell logic on a machine without the room
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.27.4}"
lint_version="${GOLANGCI_LINT_VERSION:-v2.15.0}"
base_ref="${PREPUSH_BASE:-origin/main}"
skipped=""

step() { printf '\n== prepush: %s\n' "$*"; }
skip() { printf 'SKIPPED: %s\n' "$*"; skipped="${skipped}  - $*
"; }
fail() { printf 'prepush: %s\n' "$*" >&2; exit 1; }

# dirs_of prints the unique directories of the file paths on stdin; empty
# input prints nothing (portable: no GNU xargs -r).
dirs_of() {
  while IFS= read -r f; do
    if [ -n "${f}" ]; then dirname "${f}"; fi
  done | sort -u
}

step "working tree is clean"
dirty="$(git status --porcelain)" || fail "git status failed"
if [ -n "${dirty}" ]; then
  fail "uncommitted changes; commit first so the verdict describes HEAD"
fi

step "go vet ./..."
go vet ./...

step "golangci-lint ${lint_version} run ./..."
command -v golangci-lint >/dev/null 2>&1 ||
  fail "golangci-lint not installed. Install with: GOTOOLCHAIN=${GOTOOLCHAIN} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${lint_version}"
installed="$(golangci-lint version 2>/dev/null | sed -n 's/.*version v\{0,1\}\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/v\1/p' | head -n 1)" || installed=""
if [ "${installed}" != "${lint_version}" ]; then
  fail "golangci-lint ${installed:-of unknown version} installed, CI uses ${lint_version}; install it with: GOTOOLCHAIN=${GOTOOLCHAIN} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${lint_version}"
fi
golangci-lint run ./...

step "workflow-hygiene scripts"
for check in check-workflow-hygiene check-language-hygiene check-file-size \
  check-release-metadata check-home-isolation check-test-only-helpers; do
  echo "-- scripts/ci/${check}.sh"
  "scripts/ci/${check}.sh"
done

step "regression-test gate against ${base_ref}"
if [ "${PREPUSH_SKIP_REGRESSION:-}" = 1 ]; then
  skip "regression-test gate (PREPUSH_SKIP_REGRESSION=1, like the skip-regression-test label)"
else
  # Same logic as the workflow: BASE is the base branch tip, the diff and the
  # commit subjects are BASE..HEAD.
  base="$(git rev-parse --verify "${base_ref}")" || fail "cannot resolve ${base_ref}; run git fetch"
  is_fix=false
  if printf '%s\n' "${PREPUSH_TITLE:-}" | grep -qiE '^(fix|hotfix|bugfix)(\(|:| )'; then
    is_fix=true
  fi
  subjects="$(git log --format=%s "${base}..HEAD")" || fail "git log ${base}..HEAD failed"
  if printf '%s\n' "${subjects}" | grep -qiE '^(fix|hotfix|bugfix)(\(|:| )'; then
    is_fix=true
  fi
  changed="$(git diff --name-only "${base}..HEAD")" || fail "git diff ${base}..HEAD failed"
  src="$(printf '%s\n' "${changed}" | grep -E '\.go$' | grep -v '_test\.go$' | grep -vE '^(docs/|\.github/)' || true)"
  tests="$(printf '%s\n' "${changed}" | grep -E '_test\.go$' || true)"
  src_dirs="$(printf '%s\n' "${src}" | dirs_of)" || fail "listing source packages failed"
  test_dirs="$(printf '%s\n' "${tests}" | dirs_of)" || fail "listing test packages failed"
  missing=""
  for d in ${src_dirs}; do
    if ! printf '%s\n' "${test_dirs}" | grep -qx "${d}"; then
      missing="${missing} ${d}"
    fi
  done
  echo "is_fix=${is_fix}; source packages changed: $(printf '%s' "${src_dirs}" | tr '\n' ' ')"
  if [ "${is_fix}" = true ] && [ -n "${missing}" ]; then
    fail "a fix changes Go source in${missing} with no *_test.go change in the same package; add a regression test beside the fix"
  fi
fi

step "release and installer checks"
if command -v goreleaser >/dev/null 2>&1; then goreleaser check; else skip "goreleaser check (goreleaser not installed)"; fi
if command -v ruby >/dev/null 2>&1; then ruby scripts/release/update-homebrew-formula_test.rb; else skip "Homebrew formula test (ruby not installed)"; fi
if command -v node >/dev/null 2>&1; then
  node --test workers/heartbeat/src/index.test.js
  node --test npm/bin/install.test.js
else
  skip "node --test suites (node not installed)"
fi

step "GOOS=windows GOARCH=amd64 go vet ./..."
GOOS=windows GOARCH=amd64 go vet ./...

if [ "${PREPUSH_SKIP_TESTS:-}" = 1 ]; then
  skip "race tests and the 80% coverage floor (PREPUSH_SKIP_TESTS=1)"
  skip "fuzz FuzzBuildFilters (PREPUSH_SKIP_TESTS=1)"
  skip "offline guard (PREPUSH_SKIP_TESTS=1)"
else
  step "go test -p=1 -race -timeout=10m ./... with the 80% coverage floor"
  mkdir -p bin
  coverage="bin/prepush-coverage.out"
  go test -p=1 -race -count=1 -timeout=10m -coverprofile "${coverage}" ./...
  total="$(go tool cover -func="${coverage}" | awk '/^total:/ {print substr($3, 1, length($3)-1)}')" || fail "go tool cover failed"
  echo "total coverage: ${total}%"
  if [ -z "${total}" ] || awk -v c="${total}" 'BEGIN { exit !(c < 80) }'; then
    fail "coverage ${total:-unparsed}% is below CI's 80% threshold"
  fi

  step "fuzz FuzzBuildFilters for 60s"
  go test ./internal/flights/ -run '^$' -fuzz=FuzzBuildFilters -fuzztime=60s

  step "offline guard"
  if command -v strace >/dev/null 2>&1; then
    go test -count=1 ./scripts/ci/offline-guard/
    go run ./scripts/ci/offline-guard
  else
    skip "offline guard (strace not installed; Linux only)"
  fi
fi

if [ -n "${skipped}" ]; then
  printf '\nprepush: every gate that ran passed, but these did NOT run here; CI still runs them:\n%s' "${skipped}"
else
  printf '\nprepush: every gate passed.\n'
fi
