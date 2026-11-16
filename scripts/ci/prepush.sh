#!/usr/bin/env bash
# Run the gates a pull request meets in CI, cheapest first, and stop at the
# first failure, so a push CI would reject never leaves the machine.
#
# Mirrors .github/workflows/ci.yaml (test, workflow-hygiene and offline-tests
# jobs) and .github/workflows/regression-test-gate.yml with the same commands
# and pinned versions. Not covered here: gosec, staticcheck and govulncheck
# (`make dod` and `make security-gosec`), osv-scanner and the secrets scan.
#
# Usage: make prepush   (or scripts/ci/prepush.sh)
#   PREPUSH_BASE   branch to compare against for the regression gate (origin/main)
#   PREPUSH_TITLE  the PR title, which the regression gate also reads
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.27.4}"
lint_version="${GOLANGCI_LINT_VERSION:-v2.15.0}"
base_ref="${PREPUSH_BASE:-origin/main}"

step() { printf '\n== prepush: %s\n' "$*"; }

step "go vet ./..."
go vet ./...

step "golangci-lint ${lint_version} run ./..."
if ! command -v golangci-lint >/dev/null 2>&1; then
  echo "golangci-lint not installed. Install with: GOTOOLCHAIN=${GOTOOLCHAIN} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${lint_version}" >&2
  exit 1
fi
installed="$(golangci-lint version 2>/dev/null | grep -oE 'version v?[0-9]+\.[0-9]+\.[0-9]+' | head -1 | sed 's/^version v\{0,1\}/v/')"
if [ "${installed}" != "${lint_version}" ]; then
  echo "golangci-lint ${installed:-unknown} installed, CI uses ${lint_version}; findings can differ. Install with: GOTOOLCHAIN=${GOTOOLCHAIN} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${lint_version}" >&2
  exit 1
fi
golangci-lint run ./...

step "workflow-hygiene scripts"
for check in check-workflow-hygiene check-language-hygiene check-file-size \
  check-release-metadata check-home-isolation check-test-only-helpers; do
  echo "-- scripts/ci/${check}.sh"
  "scripts/ci/${check}.sh"
done

step "regression-test gate against ${base_ref}"
base="$(git merge-base "${base_ref}" HEAD)"
is_fix=false
if printf '%s\n' "${PREPUSH_TITLE:-}" | grep -qiE '^(fix|hotfix|bugfix)(\(|:| )'; then
  is_fix=true
fi
if git log --format=%s "${base}..HEAD" | grep -qiE '^(fix|hotfix|bugfix)(\(|:| )'; then
  is_fix=true
fi
changed="$(git diff --name-only "${base}..HEAD")"
src="$(printf '%s\n' "${changed}" | grep -E '\.go$' | grep -v '_test\.go$' | grep -vE '^(docs/|\.github/)' || true)"
tests="$(printf '%s\n' "${changed}" | grep -E '_test\.go$' || true)"
src_dirs="$(printf '%s\n' "${src}" | sed '/^$/d' | xargs -r -n1 dirname | sort -u)"
test_dirs="$(printf '%s\n' "${tests}" | sed '/^$/d' | xargs -r -n1 dirname | sort -u)"
missing=""
for d in ${src_dirs}; do
  if ! printf '%s\n' "${test_dirs}" | grep -qx "${d}"; then
    missing="${missing} ${d}"
  fi
done
echo "is_fix=${is_fix}; source packages changed: $(printf '%s' "${src_dirs}" | tr '\n' ' ')"
if [ "${is_fix}" = true ] && [ -n "${missing}" ]; then
  echo "A fix changes Go source in${missing} with no *_test.go change in the same package. Add a regression test beside the fix." >&2
  exit 1
fi

step "GOOS=windows go vet ./..."
GOOS=windows go vet ./...

step "go test -p=1 -race -count=1 -timeout=10m ./... (with CI's 80% coverage floor)"
mkdir -p bin
coverage="bin/prepush-coverage.out"
go test -p=1 -race -count=1 -timeout=10m -coverprofile "${coverage}" ./...
total="$(go tool cover -func="${coverage}" | awk '/^total:/ {print substr($3, 1, length($3)-1)}')"
echo "total coverage: ${total}%"
if [ -z "${total}" ] || awk -v c="${total}" 'BEGIN { exit !(c < 80) }'; then
  echo "coverage ${total:-unparsed}% is below CI's 80% threshold" >&2
  exit 1
fi

step "offline guard"
if command -v strace >/dev/null 2>&1; then
  go test -count=1 ./scripts/ci/offline-guard/
  go run ./scripts/ci/offline-guard
else
  echo "SKIPPED: strace is not installed (Linux only), so the offline guard did not run here. CI's offline-tests job still runs it."
fi

printf '\nprepush: every gate passed.\n'
