#!/usr/bin/env bash
# Fixture test for wizzair-config-version.sh. The extraction it guards decides
# which upstream API version the sentinel proposes, so the shapes that must be
# REJECTED matter as much as the one that must be accepted: a match that reaches
# inside another URL lets an unrelated page choose the version.
set -euo pipefail

helper="$(dirname "$0")/wizzair-config-version.sh"
fail=0

expect() {
	local name="$1" want="$2" body="$3" got
	got="$(printf '%s' "$body" | bash "$helper")"
	if [ "$got" = "$want" ]; then
		echo "ok: $name"
	else
		echo "FAIL: $name — want '${want}', got '${got}'" >&2
		fail=1
	fi
}

expect "double-quoted apiUrl" 29.15.1 \
	'{"apiUrl":"https://be.wizzair.com/29.15.1/Api","x":1}'
expect "single-quoted apiUrl" 29.15.1 \
	"var cfg={apiUrl:'https://be.wizzair.com/29.15.1/Api'};"
expect "first match wins" 29.15.1 \
	'"https://be.wizzair.com/29.15.1/Api" ... "https://be.wizzair.com/30.0.0/Api"'
expect "rejected noise before the real config does not shadow it" 29.15.1 \
	'"https://notbe.wizzair.com/1.2.3/Api" then {apiUrl:"https://be.wizzair.com/29.15.1/Api"}'

expect "lookalike host is not the API host" "" \
	'"https://notbe.wizzair.com/1.2.3/Api"'
expect "host as a path segment elsewhere" "" \
	'"https://evil.example/be.wizzair.com/1.2.3/Api"'
expect "bare URL in another URL query string" "" \
	'"https://evil.example/?u=https://be.wizzair.com/1.2.3/Api"'
expect "http is not https" "" \
	'"http://be.wizzair.com/1.2.3/Api"'
expect "a different path is not the API base" "" \
	'"https://be.wizzair.com/29.15.1/Assets"'
expect "a page naming no version" "" \
	'<html><body>no config here</body></html>'

# A longer path that merely BEGINS with /Api is not the API base. Without the
# closing delimiter this matched, and because the first match wins, a page
# carrying such a URL ahead of the real config would shadow it -- while
# wizzVersionFromConfigBody, whose path pattern is anchored end to end, would
# have rejected the same string. The pair below pins both halves.
expect "a longer path beginning with /Api is not the API base" "" \
	'"https://be.wizzair.com/1.2.3/Apiary"'
expect "a prefix lookalike does not shadow the real config" 29.15.1 \
	'"https://be.wizzair.com/1.2.3/Apiary" {apiUrl:"https://be.wizzair.com/29.15.1/Api"}'

# Known ceiling, pinned deliberately rather than left to be discovered: a QUOTED
# URL nested in another URL's query value is accepted. wizzVersionFromConfigBody
# behaves the same way — its candidate regex also breaks on quotes — so this
# fixture records parity, not an oversight. The probe in the caller is what stops
# a version sourced this way from being reported.
expect "quoted URL nested in a query value is accepted (probe is the guard)" 1.2.3 \
	"\"https://evil.example/?next='https://be.wizzair.com/1.2.3/Api'\""

exit "$fail"
