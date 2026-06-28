#!/usr/bin/env bash
# Keep checked-in release metadata aligned with the latest documented release.
# Tag-time publishing still stamps server.json from the pushed tag; this guard
# prevents the source manifests from silently drifting behind the public release.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

fail=0

check_equal() {
  local label=$1 actual=$2 expected=$3
  if [ "$actual" = "$expected" ]; then
    printf 'ok: %s is %s\n' "$label" "$actual"
  else
    printf 'error: %s is %s, expected %s\n' "$label" "$actual" "$expected" >&2
    fail=1
  fi
}

check_contains() {
  local label=$1 pattern=$2 file=$3
  if grep -Fq "$pattern" "$file"; then
    printf 'ok: %s\n' "$label"
  else
    printf 'error: %s missing %s\n' "$label" "$pattern" >&2
    fail=1
  fi
}

latest_version="$(
  sed -nE 's/^## \[([0-9]+\.[0-9]+\.[0-9]+)\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$/\1/p' CHANGELOG.md |
    head -n1
)"

if [ -z "$latest_version" ]; then
  printf 'error: could not parse latest release version from CHANGELOG.md\n' >&2
  exit 1
fi

server_version="$(jq -r '.version' server.json)"
server_first_registry="$(jq -r '.packages[0].registryType' server.json)"
server_oci_identifier="$(jq -r '.packages[] | select(.registryType=="oci") | .identifier' server.json)"
server_npm_identifier="$(jq -r '.packages[] | select(.registryType=="npm") | .identifier' server.json)"
server_npm_version="$(jq -r '.packages[] | select(.registryType=="npm") | .version' server.json)"
npm_name="$(jq -r '.name' npm/package.json)"
npm_version="$(jq -r '.version' npm/package.json)"

check_equal "server.json version" "$server_version" "$latest_version"
check_equal "server.json first package registry" "$server_first_registry" "oci"
check_equal "server.json OCI identifier" "$server_oci_identifier" "ghcr.io/mikkoparkkola/trvl:${latest_version}"
check_equal "server.json npm identifier" "$server_npm_identifier" "$npm_name"
check_equal "server.json npm version" "$server_npm_version" "$latest_version"
check_equal "npm/package.json version" "$npm_version" "$latest_version"

check_contains "release workflow derives metadata from the pushed tag" 'VERSION="${GITHUB_REF_NAME#v}"' .github/workflows/release.yml
check_contains "release workflow stamps server.json version" '.version = $v' .github/workflows/release.yml
check_contains "release workflow stamps OCI identifier" '.packages[0].identifier = "ghcr.io/mikkoparkkola/trvl:" + $v' .github/workflows/release.yml
check_contains "release workflow stamps npm package version" 'select(.registryType=="npm") | .version' .github/workflows/release.yml

exit "$fail"
