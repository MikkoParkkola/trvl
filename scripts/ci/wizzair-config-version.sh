#!/usr/bin/env bash
# wizzair-config-version.sh — read the Wizz Air API version out of the homepage
# HTML on stdin. Echoes X.Y.Z, or nothing when the page names no qualifying URL.
#
# The Wizz homepage embeds its API base URL in an inline bootstrap config
# (apiUrl:"https://be.wizzair.com/<version>/Api"), so the version the real
# browser client uses is published in plain HTML — no JS, no cookies. This is
# the shell mirror of wizzVersionFromConfigBody in
# internal/flights/wizzair_selfheal.go, used by the version sentinel.
#
# The match is delimited by quotes at BOTH ends. The opening quote is what stops
# a lookalike host (notbe.wizzair.com), the host appearing as a path segment
# (https://evil.example/be.wizzair.com/1.2.3/Api) and a bare URL in another URL's
# query string (https://evil.example/?u=https://be.wizzair.com/1.2.3/Api) from
# contributing a version. The closing quote is what stops a longer path that
# merely begins with /Api (.../1.2.3/Apiary) being read as the API base:
# wizzVersionFromConfigBody anchors its path pattern end to end, so without that
# delimiter this side would accept a version the runtime rejects, and a page
# carrying such a URL ahead of the real config would shadow it.
#
# The ceiling, stated rather than glossed over: a *quoted* URL nested inside
# another URL's query value (?next='https://be.wizzair.com/1.2.3/Api') still
# matches. wizzVersionFromConfigBody has the identical property — its candidate
# regex also breaks on quotes, so the inner URL is scanned as its own candidate
# and its host does compare equal. Neither side treats extraction as the
# safeguard. What this emits is a claim; the caller MUST confirm it against the
# live host with a probe before acting on it, and does.
set -euo pipefail

# No qualifying URL is a normal outcome, not an error: the caller falls back to
# the candidate walk. Exit 0 with empty output rather than making "nothing found"
# indistinguishable from "the page could not be read".
version="$(grep -oE "[\"']https://be\.wizzair\.com/[0-9]+\.[0-9]+\.[0-9]+/Api[\"']" \
	| grep -oE '[0-9]+\.[0-9]+\.[0-9]+' \
	| head -1 || true)"
[ -z "$version" ] || echo "$version"
