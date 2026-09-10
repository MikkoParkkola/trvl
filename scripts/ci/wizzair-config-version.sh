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
# The match requires the URL to begin at a quote delimiter, so a lookalike host
# (notbe.wizzair.com) or a URL embedded in another URL's query string
# (https://evil.example/?u=https://be.wizzair.com/1.2.3/Api) cannot contribute a
# version. The Go side parses each candidate and compares the host for exact
# equality, which a shell pattern cannot do — hence the delimiter requirement
# here, and hence the caller's obligation to probe the result. What this emits
# is a claim, never a verdict.
set -euo pipefail

# No qualifying URL is a normal outcome, not an error: the caller falls back to
# the candidate walk. Exit 0 with empty output rather than making "nothing found"
# indistinguishable from "the page could not be read".
version="$(grep -oE "[\"']https://be\.wizzair\.com/[0-9]+\.[0-9]+\.[0-9]+/Api" \
	| grep -oE '[0-9]+\.[0-9]+\.[0-9]+' \
	| head -1 || true)"
[ -z "$version" ] || echo "$version"
