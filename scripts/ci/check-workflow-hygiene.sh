#!/usr/bin/env bash
# Guard GitHub Actions supply-chain hygiene. Keep this small and dependency-free
# so it can run early in CI before heavier Go and security jobs.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

fail=0

check() {
  local message=$1
  shift
  if "$@"; then
    printf 'ok: %s\n' "$message"
  else
    printf 'error: %s\n' "$message" >&2
    fail=1
  fi
}

all_workflow_uses_are_sha_pinned() {
  local bad=0 line file line_no text ref
  while IFS= read -r line; do
    file=${line%%:*}
    text=${line#*:}
    line_no=${text%%:*}
    text=${text#*:}
    ref="$(printf '%s\n' "$text" | sed -E 's/^[[:space:]]*uses:[[:space:]]*([^[:space:]#]+).*/\1/')"
    if [[ ! $ref =~ @[0-9a-f]{40}$ ]]; then
      printf '%s:%s: %s\n' "$file" "$line_no" "$ref" >&2
      bad=1
    fi
  done < <(grep -RInE '^[[:space:]]*uses:[[:space:]]*' .github/workflows/*.yml .github/workflows/*.yaml 2>/dev/null || true)

  [ "$bad" -eq 0 ]
}

all_setup_node_jobs_use_node24() {
  local bad=0 line file line_no text version
  while IFS= read -r line; do
    file=${line%%:*}
    text=${line#*:}
    line_no=${text%%:*}
    text=${text#*:}
    version="$(printf '%s\n' "$text" | sed -E 's/^[[:space:]]*node-version:[[:space:]]*["'\'']?([^"'\''[:space:]]+)["'\'']?.*/\1/')"
    if [ "$version" != "24" ]; then
      printf '%s:%s: node-version %s\n' "$file" "$line_no" "$version" >&2
      bad=1
    fi
  done < <(grep -RInE '^[[:space:]]*node-version:' .github/workflows/*.yml .github/workflows/*.yaml 2>/dev/null || true)

  [ "$bad" -eq 0 ]
}

no_unpinned_gitnexus_cli_calls() {
  ! grep -RInE 'npx --yes gitnexus([[:space:]]|$)' \
    .github/workflows scripts/gitnexus
}

gitnexus_version_is_pinned_consistently() {
  local workflow_version script_default
  workflow_version="$(sed -nE 's/^  GITNEXUS_VERSION:[[:space:]]*"([^"]+)".*/\1/p' .github/workflows/gitnexus-index.yml)"
  script_default="$(sed -nE 's/.*GITNEXUS_VERSION:-([^}]+)}.*/\1/p' scripts/gitnexus/refresh.sh)"

  [ -n "$workflow_version" ] &&
    [ "$workflow_version" = "$script_default" ] &&
    grep -q "gitnexus@\$GITNEXUS_VERSION" .github/workflows/gitnexus-index.yml &&
    grep -q 'gitnexus@\$gitnexus_version' scripts/gitnexus/refresh.sh
}

gitnexus_docs_use_sha_pinned_cache_example() {
  ! grep -q 'actions/cache@v4' scripts/gitnexus/README.md &&
    grep -q 'actions/cache@[0-9a-f]\{40\}' scripts/gitnexus/README.md
}

release_goreleaser_action_uses_node24_pin() {
  ! grep -qF 'goreleaser/goreleaser-action@e435ccd777264be153ace6237001ef4d979d3a7a' .github/workflows/release.yml &&
    [ "$(grep -cF 'goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94 # v7.2.3' .github/workflows/release.yml)" -eq 1 ] &&
    grep -qF 'install-only: true' .github/workflows/release.yml &&
    grep -qF 'run: goreleaser check' .github/workflows/release.yml &&
    grep -qF 'run: goreleaser build --single-target --snapshot --clean' .github/workflows/release.yml &&
    [ "$(grep -cF 'run: goreleaser release --clean --skip=docker --skip=homebrew' .github/workflows/release.yml)" -eq 1 ]
}

release_docker_job_has_timeout() {
  awk '
    /^  docker:/ { in_job=1; next }
    in_job && /^  [a-zA-Z0-9_-]+:/ { exit }
    in_job && /^[[:space:]]+timeout-minutes:[[:space:]]*[0-9]+/ { found=1 }
    END { exit found ? 0 : 1 }
  ' .github/workflows/release.yml
}

release_docker_builds_emit_plain_progress() {
  [ "$(grep -c -- '--progress=plain' .github/workflows/release.yml)" -ge 2 ]
}

release_docker_trivy_blocks_before_push() {
  local trivy_line push_line
  trivy_line="$(grep -nF 'name: Trivy image scan (block push on fixable HIGH/CRITICAL CVEs)' .github/workflows/release.yml | cut -d: -f1)"
  push_line="$(grep -nF 'name: Build and push multi-arch image' .github/workflows/release.yml | cut -d: -f1)"

  [ -n "$trivy_line" ] &&
    [ -n "$push_line" ] &&
    [ "$trivy_line" -lt "$push_line" ] &&
    grep -qF 'exit-code: "1"' .github/workflows/release.yml &&
    grep -qF 'severity: HIGH,CRITICAL' .github/workflows/release.yml &&
    grep -qF 'ignore-unfixed: true' .github/workflows/release.yml
}

release_homebrew_stays_formula_only_until_notarized() {
  ! grep -q '^brews:' .goreleaser.yaml &&
    ! grep -q '^homebrew_casks:' .goreleaser.yaml &&
    ! grep -q 'homebrew_casks' .github/workflows/release.yml &&
    grep -qF 'scripts/release/update-homebrew-formula.rb' .github/workflows/release.yml &&
    grep -qF 'homebrew-tap/Formula/trvl.rb' .github/workflows/release.yml &&
    grep -qF 'git -C homebrew-tap push origin HEAD:main' .github/workflows/release.yml &&
    grep -q 'Formula-only until Developer ID notarization is proven in release CI' docs/DISTRIBUTION.md
}

check "workflow action uses refs are pinned to commit SHAs" all_workflow_uses_are_sha_pinned
check "setup-node jobs use Node 24" all_setup_node_jobs_use_node24
check "GitNexus CLI calls include an explicit npm package version" no_unpinned_gitnexus_cli_calls
check "GitNexus CI and local refresh use the same pinned CLI version" gitnexus_version_is_pinned_consistently
check "GitNexus cached-index docs use a SHA-pinned cache action example" gitnexus_docs_use_sha_pinned_cache_example
check "release GoReleaser action uses the reviewed Node 24 pin" release_goreleaser_action_uses_node24_pin
check "release Docker job has an explicit timeout" release_docker_job_has_timeout
check "release Docker buildx commands emit plain progress logs" release_docker_builds_emit_plain_progress
check "release Docker push remains behind the Trivy HIGH/CRITICAL gate" release_docker_trivy_blocks_before_push
check "release Homebrew distribution stays Formula-only until notarized casks are proven" release_homebrew_stays_formula_only_until_notarized

exit "$fail"
